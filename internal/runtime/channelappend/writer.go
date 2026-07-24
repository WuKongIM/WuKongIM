package channelappend

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// channelWriter is the single-writer state machine for one locally authoritative channel.
// Invariant: at most one goroutine advances a writer at a time (guarded by scheduled).
type channelWriter struct {
	key string

	// ports are the dependencies used to advance this writer's state machine.
	ports writerPorts

	// scheduled reports whether a worker is already queued to advance this writer.
	scheduled atomic.Bool
	// lastIdleUnixNano records when this writer last drained all work, or zero while active.
	lastIdleUnixNano atomic.Int64

	// mu guards state, inbox, and the phase transitions inside advance.
	mu    sync.Mutex
	state *channelState
	// inbox holds submitted batches not yet drained into the channelState pending queue.
	inbox []submittedBatch
	// commitRetryQueued is guarded by mu and de-duplicates this writer in the
	// global post-commit retry FIFO.
	commitRetryQueued bool
	// commitRetryTurn lets the FIFO-selected writer attempt pool admission while
	// all newer durable commits remain queued behind the contended gate.
	commitRetryTurn bool
}

// submittedBatch is one admitted SubmitLocal call awaiting prepare+append.
type submittedBatch struct {
	target AuthorityTarget
	items  []SendBatchItem
	future *Future
}

// preparedInboxBatch pairs an accepted submitted batch with its lock-free prepare outcome.
type preparedInboxBatch struct {
	batch   submittedBatch
	outcome prepareOutcome
}

type realtimeDispatch struct {
	target AuthorityTarget
	items  []preparedSend
}

// writerPorts are the dependencies a writer needs to advance its state machine.
type writerPorts struct {
	prepare        preparePorts
	append         appendPorts
	commit         commitPorts
	appendPool     *workerPool
	postCommitPool *workerPool
	handoff        *postCommitHandoff
	commitRetries  *postCommitRetryScheduler
	schedule       func(*channelWriter)
	runtimeCtx     context.Context
	metrics        *groupMetrics

	inboxCoalesceWindow   time.Duration
	inboxCoalesceMaxItems int
}

func newChannelWriter(target AuthorityTarget, limits channelStateLimits) *channelWriter {
	return &channelWriter{
		key:   targetKey(target),
		state: newChannelState(target, limits),
	}
}

// enqueue appends a batch to the inbox and reports whether the caller should
// schedule this writer onto a worker (true only on the scheduled false->true edge).
func (w *channelWriter) enqueue(batch submittedBatch) bool {
	w.lastIdleUnixNano.Store(0)
	w.mu.Lock()
	w.inbox = append(w.inbox, batch)
	w.mu.Unlock()
	return w.tryActivate()
}

// tryActivate marks the writer scheduled. It returns true only for the
// goroutine that won the false->true transition, which then owns advancing it.
func (w *channelWriter) tryActivate() bool {
	return w.scheduled.CompareAndSwap(false, true)
}

// deactivate clears the scheduled flag and reports whether more work arrived
// after the caller stopped advancing (caller must re-activate if true).
func (w *channelWriter) deactivate() bool {
	w.mu.Lock()
	more := w.deactivateLocked()
	w.mu.Unlock()
	return more
}

// deactivateLocked performs the scheduled-to-idle transition while w.mu is held.
func (w *channelWriter) deactivateLocked() bool {
	w.scheduled.Store(false)
	more := w.hasRunnableWorkLocked()
	if !more {
		w.lastIdleUnixNano.Store(time.Now().UnixNano())
	}
	return more
}

func (w *channelWriter) hasRunnableWorkLocked() bool {
	if w.commitRetryQueued {
		// A queued durable commit owns the channel's next retry turn. New append
		// work may be prepared, but it parks until that turn is selected instead
		// of pinning a blocking appendPool submit ahead of the old commit.
		return false
	}
	includeCommit := w.ports.commit.hasPostCommitWork() && !w.commitRetryQueued
	return len(w.inbox) > 0 || w.state.hasRunnableWork(includeCommit)
}

func (w *channelWriter) inboxItemCountLocked() int {
	count := 0
	for _, batch := range w.inbox {
		count += len(batch.items)
	}
	return count
}

// waitForInboxCoalesce gives nearby same-channel submissions a tiny bounded
// window to join the current writer pass. It never holds channelWriter.mu
// while sleeping.
func (w *channelWriter) waitForInboxCoalesce() {
	window := w.ports.inboxCoalesceWindow
	maxItems := w.ports.inboxCoalesceMaxItems
	if window <= 0 || maxItems <= 1 {
		return
	}
	w.mu.Lock()
	shouldWait := len(w.inbox) > 0 && w.inboxItemCountLocked() < maxItems
	w.mu.Unlock()
	if !shouldWait {
		return
	}

	timer := time.NewTimer(window)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	interval := window / 4
	if interval <= 0 {
		interval = window
	}
	if interval > 50*time.Microsecond {
		interval = 50 * time.Microsecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var done <-chan struct{}
	if w.ports.runtimeCtx != nil {
		done = w.ports.runtimeCtx.Done()
	}
	for {
		select {
		case <-done:
			return
		case <-timer.C:
			return
		case <-ticker.C:
			w.mu.Lock()
			ready := len(w.inbox) == 0 || w.inboxItemCountLocked() >= maxItems
			w.mu.Unlock()
			if ready {
				return
			}
		}
	}
}

func (w *channelWriter) idleExpired(now time.Time, idleRetention time.Duration) bool {
	if w == nil || idleRetention <= 0 || w.scheduled.Load() {
		return false
	}
	w.mu.Lock()
	idle := len(w.inbox) == 0 && !w.state.hasPendingWork()
	w.mu.Unlock()
	if !idle {
		return false
	}
	idleAt := w.lastIdleUnixNano.Load()
	if idleAt == 0 {
		return false
	}
	return !time.Unix(0, idleAt).Add(idleRetention).After(now)
}

// advance pushes the writer's state machine forward as far as it can without
// blocking, submitting blocking append and post-commit effects to isolated pools.
// Exactly one goroutine runs advance for a given writer at a time.
func (w *channelWriter) advance() {
	if !w.ports.commit.hasPostCommitWork() {
		w.advanceAppendOnly()
		return
	}
	var appendEff appendEffect
	var commitEff commitEffect
	for {
		w.waitForInboxCoalesce()
		w.mu.Lock()
		inbox := w.takeInboxLocked()
		if len(inbox) > 0 {
			w.mu.Unlock()
			prepared := w.prepareInbox(inbox)
			w.mu.Lock()
			realtime, handoffRejected := w.admitPreparedInboxLocked(prepared)
			w.mu.Unlock()
			w.observeHandoffRejection(handoffRejected)
			w.runRealtimeDispatches(realtime)
			w.mu.Lock()
		}
		retryTurn := w.commitRetryTurn
		hasAppend := false
		hasCommit := false
		if retryTurn {
			// The FIFO-selected writer admits only its old durable commit in this
			// pass. A later ordinary pass resumes append-first processing.
			hasCommit = w.nextCommitLocked(&commitEff)
			if !hasCommit {
				hasAppend = w.nextAppendLocked(&appendEff)
			}
		} else if !w.commitRetryQueued {
			hasAppend = w.nextAppendLocked(&appendEff)
			hasCommit = w.nextCommitLocked(&commitEff)
		}
		if !hasAppend && !hasCommit {
			more := w.deactivateLocked()
			w.mu.Unlock()
			if more && w.tryActivate() {
				continue // work arrived during the deactivate window; keep going
			}
			return
		}
		w.mu.Unlock()

		if hasAppend {
			w.runAppend(&appendEff)
		}
		if hasCommit {
			w.runCommit(&commitEff)
		}
	}
}

func (w *channelWriter) advanceAppendOnly() {
	var appendEff appendEffect
	for {
		w.waitForInboxCoalesce()
		w.mu.Lock()
		inbox := w.takeInboxLocked()
		if len(inbox) > 0 {
			w.mu.Unlock()
			prepared := w.prepareInbox(inbox)
			w.mu.Lock()
			realtime, handoffRejected := w.admitPreparedInboxLocked(prepared)
			w.mu.Unlock()
			w.observeHandoffRejection(handoffRejected)
			w.runRealtimeDispatches(realtime)
			w.mu.Lock()
		}
		hasAppend := w.nextAppendLocked(&appendEff)
		if !hasAppend {
			more := w.deactivateLocked()
			w.mu.Unlock()
			if more && w.tryActivate() {
				continue // work arrived during the deactivate window; keep going
			}
			return
		}
		w.mu.Unlock()

		w.runAppend(&appendEff)
	}
}

// takeInboxLocked detaches all currently accepted inbox batches from the writer.
func (w *channelWriter) takeInboxLocked() []submittedBatch {
	if len(w.inbox) == 0 {
		return nil
	}
	inbox := w.inbox
	w.inbox = nil
	return inbox
}

// prepareInbox runs CPU-side send preparation outside channelWriter.mu.
func (w *channelWriter) prepareInbox(inbox []submittedBatch) []preparedInboxBatch {
	if len(inbox) == 0 {
		return nil
	}
	prepared := make([]preparedInboxBatch, 0, len(inbox))
	for _, batch := range inbox {
		prepared = append(prepared, preparedInboxBatch{
			batch:   batch,
			outcome: prepareBatch(w.ports.runtimeCtx, batch.items, w.ports.prepare),
		})
	}
	return prepared
}

// admitPreparedInboxLocked moves prepared work into channelState while w.mu is held.
func (w *channelWriter) admitPreparedInboxLocked(prepared []preparedInboxBatch) ([]realtimeDispatch, int) {
	var realtime []realtimeDispatch
	handoffRejected := 0
	for _, item := range prepared {
		dispatches, rejected := w.admitPreparedLocked(item.batch, item.outcome)
		realtime = append(realtime, dispatches...)
		handoffRejected += rejected
	}
	return realtime, handoffRejected
}

// admitPreparedLocked applies prepare results: completes terminal items on the
// future immediately and enqueues append-bound items, honoring canAdmit backpressure.
func (w *channelWriter) admitPreparedLocked(batch submittedBatch, outcome prepareOutcome) ([]realtimeDispatch, int) {
	handoffRejected := 0
	for _, item := range outcome.canonicalResults {
		if !preparedCommandMatchesTarget(batch.target, item.command) {
			outcome.results[item.index] = SendBatchItemResult{Err: ErrStaleRoute}
		}
	}

	matching := make([]preparedSend, 0, len(outcome.prepared))
	pendingIndex := make(map[int]struct{}, len(outcome.prepared)+len(outcome.realtime))
	for _, item := range outcome.prepared {
		if !preparedCommandMatchesTarget(batch.target, item.Command) {
			outcome.results[item.Index] = SendBatchItemResult{Err: ErrStaleRoute}
			continue
		}
		item.future = batch.future
		matching = append(matching, item)
		pendingIndex[item.Index] = struct{}{}
	}
	realtimeItems := make([]preparedSend, 0, len(outcome.realtime))
	for _, item := range outcome.realtime {
		if !preparedCommandMatchesTarget(batch.target, item.Command) {
			outcome.results[item.Index] = SendBatchItemResult{Err: ErrStaleRoute}
			continue
		}
		item.future = batch.future
		realtimeItems = append(realtimeItems, item)
		pendingIndex[item.Index] = struct{}{}
	}
	if len(matching) > 0 {
		w.state.refreshTargetMetadata(batch.target)
		if w.state.canAdmit(len(matching)) {
			admitted := matching[:0]
			for _, item := range matching {
				if w.ports.commit.hasPostCommitWork() {
					if !w.ports.handoff.tryAcquire() {
						delete(pendingIndex, item.Index)
						outcome.results[item.Index] = SendBatchItemResult{Err: ErrChannelBusy}
						handoffRejected++
						continue
					}
					item.postCommitReserved = true
				}
				admitted = append(admitted, item)
			}
			w.state.enqueuePrepared(admitted)
			w.ports.metrics.addPendingAppendItems(len(admitted))
			w.ports.metrics.observePressure()
		} else {
			for _, item := range matching {
				delete(pendingIndex, item.Index)
				outcome.results[item.Index] = SendBatchItemResult{Err: ErrChannelBusy}
			}
		}
	}
	batch.future.completeItems(outcome.results, func(index int) bool {
		_, pending := pendingIndex[index]
		return !pending
	})
	if len(realtimeItems) == 0 {
		return nil, handoffRejected
	}
	return []realtimeDispatch{{target: batch.target, items: realtimeItems}}, handoffRejected
}

func (w *channelWriter) observeHandoffRejection(items int) {
	if items <= 0 {
		return
	}
	observeEffect(w.ports.append.observer, EffectObservation{
		Stage:  effectStagePostCommit,
		Result: channelAppendResultBackpressured,
		Items:  items,
	})
}

func (w *channelWriter) nextAppendLocked(out *appendEffect) bool {
	seq, items, ok := w.state.nextAppendBatch()
	if !ok {
		return false
	}
	w.ports.metrics.addPendingAppendItems(-len(items))
	w.ports.metrics.addAppendInflightItems(len(items))
	w.ports.metrics.observePressure()
	out.target = w.state.target
	out.key = w.key
	out.seq = seq
	out.items = items
	return true
}

func (w *channelWriter) runAppend(effect *appendEffect) {
	snapshot := *effect
	err := w.ports.appendPool.submit(func() {
		completion := func() (completion appendCompletedEvent) {
			defer func() {
				if recovered := recover(); recovered != nil {
					completion = appendPanicCompletion(snapshot, recovered)
				}
			}()
			return snapshot.run(w.ports.runtimeCtx, w.ports.append)
		}()
		w.applyAppendCompletion(completion)
		w.rescheduleIfNeeded()
	})
	if err != nil {
		w.applyAppendCompletion(appendScheduleErrorCompletion(snapshot, err))
		w.rescheduleIfNeeded()
	}
}

func (w *channelWriter) runRealtimeDispatches(dispatches []realtimeDispatch) {
	for _, dispatch := range dispatches {
		w.runRealtimeDispatch(dispatch)
	}
}

func (w *channelWriter) runRealtimeDispatch(dispatch realtimeDispatch) {
	if len(dispatch.items) == 0 {
		return
	}
	effect := realtimeEffect{target: dispatch.target, items: dispatch.items}
	w.ports.commitRetries.lockAdmission()
	if w.ports.commitRetries.isContended() {
		w.ports.commitRetries.unlockAdmission()
		w.completeRealtimeScheduleError(dispatch.items, ErrBackpressured)
		return
	}
	err := w.ports.postCommitPool.submitWithCompletion(func() {
		completions := func() (completions []realtimeItemCompletion) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err := effectPanicError(effectStageRealtime, recovered)
					completions = make([]realtimeItemCompletion, 0, len(effect.items))
					for _, item := range effect.items {
						completions = append(completions, realtimeItemCompletion{item: item, result: SendBatchItemResult{Err: err}})
					}
				}
			}()
			return effect.run(w.ports.runtimeCtx, w.ports.commit)
		}()
		for _, completion := range completions {
			completion.item.future.completeItem(completion.item.Index, completion.result)
		}
	}, w.ports.commitRetries.notifyCapacity)
	w.ports.commitRetries.unlockAdmission()
	if err != nil {
		w.completeRealtimeScheduleError(dispatch.items, err)
	}
}

func (w *channelWriter) completeRealtimeScheduleError(items []preparedSend, err error) {
	result := SendBatchItemResult{Err: effectScheduleError(effectStageRealtime, err)}
	for _, item := range items {
		item.future.completeItem(item.Index, result)
	}
}

func (w *channelWriter) applyAppendCompletion(event appendCompletedEvent) {
	var dispatch []appendCompletionDispatchItem
	releaseReservations := 0
	w.mu.Lock()
	w.state.recordAppendCompletion(event)
	for {
		next, ok := w.state.popNextAppendCompletion()
		if !ok {
			break
		}
		w.state.finishAppend(len(next.items))
		w.ports.metrics.addAppendInflightItems(-len(next.items))
		for _, completion := range next.items {
			handedOff := false
			if w.ports.commit.hasPostCommitWork() &&
				completion.committed &&
				completion.traceErr == nil &&
				completion.result.Err == nil &&
				completion.result.Result.Reason == ReasonSuccess {
				envelope := committedEnvelopeForAppend(completion.item, completion.appended)
				w.state.enqueueCommitted(envelope)
				w.ports.metrics.addPostCommitBacklog(1)
				handedOff = true
			}
			if completion.item.postCommitReserved && !handedOff {
				releaseReservations++
			}
			dispatch = append(dispatch, appendCompletionDispatchItem{completion: completion, duration: next.duration})
		}
	}
	w.mu.Unlock()
	w.ports.handoff.release(releaseReservations)
	w.ports.metrics.observePressure()
	for _, item := range dispatch {
		w.dispatchAppendItemCompletion(item.completion, item.duration)
	}
}

type appendCompletionDispatchItem struct {
	completion appendItemCompletion
	duration   time.Duration
}

func (w *channelWriter) dispatchAppendItemCompletion(completion appendItemCompletion, dur time.Duration) {
	observeAppendCompletion(w.ports.append.observer, completion, dur)
	recordAppendDurableTrace(completion.item, appendTraceMessageSeq(completion), completion.traceErr, dur)
	completion.item.future.completeItem(completion.item.Index, completion.result)
}

func (w *channelWriter) nextCommitLocked(out *commitEffect) bool {
	if w.commitRetryQueued {
		return false
	}
	return w.state.nextCommitEffect(w.key, out)
}

func (w *channelWriter) runCommit(effect *commitEffect) {
	snapshot := *effect
	w.ports.commitRetries.lockAdmission()
	w.mu.Lock()
	retryTurn := w.commitRetryTurn
	contended := w.ports.commitRetries.isContended()
	w.mu.Unlock()
	if contended && !retryTurn {
		w.deferPostCommitRetry()
		w.ports.commitRetries.unlockAdmission()
		return
	}
	err := w.ports.postCommitPool.submitWithCompletion(func() {
		completion := func() (completion commitCompletedEvent) {
			defer func() {
				if recovered := recover(); recovered != nil {
					completion = commitPanicCompletion(snapshot, recovered)
				}
			}()
			return snapshot.run(w.ports.runtimeCtx, w.ports.commit)
		}()
		w.applyCommitCompletion(completion)
		w.rescheduleIfNeeded()
	}, w.ports.commitRetries.notifyCapacity)
	if err != nil {
		w.deferPostCommitRetry()
	} else if retryTurn {
		w.mu.Lock()
		w.commitRetryTurn = false
		w.mu.Unlock()
		w.ports.commitRetries.finishRetryTurn(postCommitRetryTurnProgress)
		w.ports.metrics.observePressure()
	}
	w.ports.commitRetries.unlockAdmission()
}

// deferPostCommitRetry restores the current commit to queued state and adds
// this writer to the de-duplicated global FIFO after transient pool overload.
func (w *channelWriter) deferPostCommitRetry() {
	defer w.ports.metrics.observePressure()
	w.mu.Lock()
	w.state.cancelCommitDispatch()
	retryTurn := w.commitRetryTurn
	w.commitRetryTurn = false
	enqueue := !w.commitRetryQueued
	w.commitRetryQueued = true
	w.mu.Unlock()
	if !enqueue {
		if retryTurn {
			w.ports.commitRetries.finishRetryTurn(postCommitRetryTurnOverloaded)
		}
		return
	}
	if w.ports.commitRetries != nil {
		w.ports.commitRetries.enqueue(w)
		if retryTurn {
			w.ports.commitRetries.finishRetryTurn(postCommitRetryTurnOverloaded)
		}
		return
	}
	time.AfterFunc(postCommitRetryInterval, w.retryPostCommit)
}

// retryPostCommit returns FIFO ownership to the writer state machine. If the
// writer is already active, its current advance loop observes the cleared flag.
func (w *channelWriter) retryPostCommit() {
	defer w.ports.metrics.observePressure()
	w.mu.Lock()
	if !w.commitRetryQueued {
		w.mu.Unlock()
		return
	}
	w.commitRetryQueued = false
	hasRetryCommit := !w.state.commitInflight && w.state.commitBacklog() > 0
	w.commitRetryTurn = hasRetryCommit
	more := w.hasRunnableWorkLocked()
	if !hasRetryCommit {
		w.commitRetryTurn = false
	}
	w.mu.Unlock()
	if !hasRetryCommit {
		w.ports.commitRetries.finishRetryTurn(postCommitRetryTurnProgress)
	}
	if !more {
		return
	}
	if !w.tryActivate() {
		return
	}
	if w.ports.schedule != nil {
		w.ports.schedule(w)
		return
	}
	go w.advance()
}

func (w *channelWriter) applyCommitCompletion(event commitCompletedEvent) {
	failures := append([]commitCompletedItem(nil), event.failures...)
	releaseReservations := 0
	w.mu.Lock()
	backlogBefore := w.state.commitBacklog()
	if len(event.items) > 0 {
		w.state.recordSubscriberCache(event.subscriberCache)
		w.state.finishCommit(len(event.items))
		releaseReservations = len(event.items)
	} else {
		w.state.finishCommitFailure()
	}
	w.ports.metrics.addPostCommitBacklog(w.state.commitBacklog() - backlogBefore)
	w.mu.Unlock()
	w.ports.handoff.release(releaseReservations)
	w.ports.metrics.observePressure()
	for _, item := range event.items {
		if item.err != nil {
			failures = append(failures, item)
		}
	}
	for _, item := range failures {
		observePostCommitFailure(w.ports.append.observer, postCommitFailureFromItem(event, item))
	}
}

// rescheduleIfNeeded re-activates the writer only when completion handling made
// more work available. This avoids scheduling an empty advance after every
// append completion on the SEND hot path.
func (w *channelWriter) rescheduleIfNeeded() {
	w.mu.Lock()
	more := w.hasRunnableWorkLocked()
	w.mu.Unlock()
	if !more {
		return
	}
	if w.tryActivate() {
		w.ports.schedule(w)
	}
}
