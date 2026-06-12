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
}

// submittedBatch is one admitted SubmitLocal call awaiting prepare+append.
type submittedBatch struct {
	target AuthorityTarget
	items  []SendBatchItem
	future *Future
}

// writerPorts are the dependencies a writer needs to advance its state machine.
type writerPorts struct {
	prepare    preparePorts
	append     appendPorts
	commit     commitPorts
	pool       *workerPool
	schedule   func(*channelWriter)
	runtimeCtx context.Context
	stopped    *atomic.Bool
	metrics    *groupMetrics
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
	w.scheduled.Store(false)
	w.mu.Lock()
	more := w.hasRunnableWorkLocked()
	w.mu.Unlock()
	if !more {
		w.lastIdleUnixNano.Store(time.Now().UnixNano())
	}
	return more
}

func (w *channelWriter) hasRunnableWorkLocked() bool {
	return len(w.inbox) > 0 || w.state.hasRunnableWork(w.ports.commit.hasPostCommitWork())
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
// blocking, submitting blocking append/commit effects to the shared pool.
// Exactly one goroutine runs advance for a given writer at a time.
func (w *channelWriter) advance() {
	if !w.ports.commit.hasPostCommitWork() {
		w.advanceAppendOnly()
		return
	}
	var appendEff appendEffect
	var commitEff commitEffect
	for {
		w.mu.Lock()
		w.drainInboxLocked()
		hasAppend := w.nextAppendLocked(&appendEff)
		hasCommit := w.nextCommitLocked(&commitEff)
		w.mu.Unlock()

		if hasAppend {
			w.runAppend(&appendEff)
		}
		if hasCommit {
			w.runCommit(&commitEff)
		}
		if !hasAppend && !hasCommit {
			if w.deactivate() && w.tryActivate() {
				continue // work arrived during the deactivate window; keep going
			}
			return
		}
	}
}

func (w *channelWriter) advanceAppendOnly() {
	var appendEff appendEffect
	for {
		w.mu.Lock()
		w.drainInboxLocked()
		hasAppend := w.nextAppendLocked(&appendEff)
		w.mu.Unlock()

		if hasAppend {
			w.runAppend(&appendEff)
			continue
		}
		if w.deactivate() && w.tryActivate() {
			continue // work arrived during the deactivate window; keep going
		}
		return
	}
}

// drainInboxLocked prepares inbox batches inline and admits prepared items to state.
func (w *channelWriter) drainInboxLocked() {
	if len(w.inbox) == 0 {
		return
	}
	inbox := w.inbox
	w.inbox = nil
	for _, batch := range inbox {
		outcome := prepareBatch(w.ports.runtimeCtx, batch.items, w.ports.prepare)
		w.admitPreparedLocked(batch, outcome)
	}
}

// admitPreparedLocked applies prepare results: completes terminal items on the
// future immediately and enqueues append-bound items, honoring canAdmit backpressure.
func (w *channelWriter) admitPreparedLocked(batch submittedBatch, outcome prepareOutcome) {
	for _, item := range outcome.canonicalResults {
		if !preparedCommandMatchesTarget(batch.target, item.command) {
			outcome.results[item.index] = SendBatchItemResult{Err: ErrStaleRoute}
		}
	}

	matching := make([]preparedSend, 0, len(outcome.prepared))
	matchingIndex := make(map[int]struct{}, len(outcome.prepared))
	for _, item := range outcome.prepared {
		if !preparedCommandMatchesTarget(batch.target, item.Command) {
			outcome.results[item.Index] = SendBatchItemResult{Err: ErrStaleRoute}
			continue
		}
		item.future = batch.future
		matching = append(matching, item)
		matchingIndex[item.Index] = struct{}{}
	}
	if len(matching) > 0 {
		w.state.refreshRecipientMetadata(batch.target)
		if w.state.canAdmit(len(matching)) {
			w.state.enqueuePrepared(matching)
			w.ports.metrics.addPendingAppendItems(len(matching))
			w.ports.metrics.observePressure()
		} else {
			for _, item := range matching {
				delete(matchingIndex, item.Index)
				outcome.results[item.Index] = SendBatchItemResult{Err: ErrChannelBusy}
			}
		}
	}
	batch.future.completeItems(outcome.results, func(index int) bool {
		_, pendingAppend := matchingIndex[index]
		return !pendingAppend
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
	_ = w.ports.pool.submit(func() {
		completion := snapshot.run(w.ports.runtimeCtx, w.ports.append)
		w.applyAppendCompletion(completion)
		w.rescheduleIfNeeded()
	})
}

func (w *channelWriter) applyAppendCompletion(event appendCompletedEvent) {
	var dispatch []appendCompletionDispatchItem
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
			if w.ports.commit.hasPostCommitWork() &&
				completion.traceErr == nil &&
				completion.result.Err == nil &&
				completion.result.Result.Reason == ReasonSuccess {
				w.state.enqueueCommitted(committedEnvelopeForAppend(completion.item, completion.appended))
				w.ports.metrics.addPostCommitBacklog(1)
			}
			dispatch = append(dispatch, appendCompletionDispatchItem{completion: completion, duration: next.duration})
		}
	}
	w.ports.metrics.observePressure()
	w.mu.Unlock()
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
	if w.runtimeStopped() {
		dropped := w.state.dropCommitBacklog()
		w.ports.metrics.addPostCommitBacklog(-dropped)
		w.ports.metrics.observePressure()
		return false
	}
	return w.state.nextCommitEffect(w.key, out)
}

func (w *channelWriter) runCommit(effect *commitEffect) {
	snapshot := *effect
	_ = w.ports.pool.submit(func() {
		completion := snapshot.run(w.ports.runtimeCtx, w.ports.commit)
		w.applyCommitCompletion(completion)
		w.rescheduleIfNeeded()
	})
}

func (w *channelWriter) applyCommitCompletion(event commitCompletedEvent) {
	var failures []commitCompletedItem
	w.mu.Lock()
	backlogBefore := w.state.commitBacklog()
	if len(event.items) > 0 {
		w.state.recordSubscriberCache(event.subscriberCache)
		w.state.finishCommit(len(event.items))
	} else {
		w.state.finishCommitFailure()
	}
	if w.runtimeStopped() {
		w.state.dropCommitBacklog()
	}
	w.ports.metrics.addPostCommitBacklog(w.state.commitBacklog() - backlogBefore)
	w.ports.metrics.observePressure()
	w.mu.Unlock()
	for _, item := range event.items {
		if item.err != nil {
			failures = append(failures, item)
		}
	}
	for _, item := range failures {
		observePostCommitFailure(w.ports.append.observer, postCommitFailureFromItem(event, item))
	}
}

func (w *channelWriter) runtimeStopped() bool {
	return w != nil && w.ports.stopped != nil && w.ports.stopped.Load()
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
