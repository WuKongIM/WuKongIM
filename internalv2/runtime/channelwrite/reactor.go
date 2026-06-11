package channelwrite

import (
	"context"
	"sync"
	"sync/atomic"
)

type reactor struct {
	id                 int
	mailbox            chan any
	completions        chan reactorEvent
	effectSlots        chan struct{}
	limits             channelStateLimits
	scheduler          *effectScheduler
	ports              preparePorts
	appendPorts        appendPorts
	commitPorts        commitPorts
	prepareCapacity    int
	appendCapacity     int
	postCommitCapacity int

	mu     sync.Mutex
	states map[string]*channelState

	nextPrepareSeq   map[string]uint64
	nextDrainSeq     map[string]uint64
	completedPrepare map[string]map[uint64]prepareCompletedEvent
	pendingPrepare   []pendingPrepareEffect
	pendingAppend    []appendEffect
	pendingCommit    []commitEffect
	effectInflight   int

	// prepareEffectQueue mirrors len(pendingPrepare) for cross-goroutine observations.
	prepareEffectQueue atomic.Int64
	// appendEffectQueue mirrors len(pendingAppend) for cross-goroutine observations.
	appendEffectQueue atomic.Int64
	// commitEffectQueue mirrors len(pendingCommit) for cross-goroutine observations.
	commitEffectQueue   atomic.Int64
	pendingAppendItems  atomic.Int64
	appendInflightItems atomic.Int64
	postCommitBacklog   atomic.Int64
	prepareWorkerBusy   atomic.Int64
	appendWorkerBusy    atomic.Int64
	commitWorkerBusy    atomic.Int64

	startOnce  sync.Once
	closeOnce  sync.Once
	done       chan struct{}
	stopCtx    context.Context
	stopCancel context.CancelFunc
}

type reactorEvent interface {
	apply(*reactor)
}

type submitLocalEvent struct {
	target AuthorityTarget
	items  []SendBatchItem
	future *Future
	ack    chan error
}

type subscriberMutationEvent struct {
	update SubscriberMutationUpdate
	ack    chan error
}

type pendingPrepareEffect struct {
	effect prepareEffect
	ack    chan error
}

func newReactor(id int, mailboxSize int, limits channelStateLimits, scheduler *effectScheduler, ports preparePorts, appendPorts appendPorts, commitPorts commitPorts) *reactor {
	stopCtx, stopCancel := context.WithCancel(context.Background())
	return &reactor{
		id:                 id,
		mailbox:            make(chan any, mailboxSize),
		completions:        make(chan reactorEvent, mailboxSize),
		effectSlots:        make(chan struct{}, mailboxSize),
		limits:             limits,
		scheduler:          scheduler,
		ports:              ports,
		appendPorts:        appendPorts,
		commitPorts:        commitPorts,
		prepareCapacity:    scheduler.prepareWorkerCapacity(),
		appendCapacity:     scheduler.appendWorkerCapacity(),
		postCommitCapacity: scheduler.postCommitWorkerCapacity(),
		states:             make(map[string]*channelState),
		nextPrepareSeq:     make(map[string]uint64),
		nextDrainSeq:       make(map[string]uint64),
		completedPrepare:   make(map[string]map[uint64]prepareCompletedEvent),
		done:               make(chan struct{}),
		stopCtx:            stopCtx,
		stopCancel:         stopCancel,
	}
}

func (r *reactor) start() {
	r.startOnce.Do(func() {
		r.observeAllEffectWorkerPressure()
		go r.run()
	})
}

func (r *reactor) run() {
	defer close(r.done)
	mailbox := r.mailbox
	for mailbox != nil || r.hasPendingEffects() || r.effectInflight > 0 {
		if r.stopCtx.Err() != nil {
			r.releasePendingCommitEffects(r.pendingCommit)
			r.addEffectQueueDepth(effectStagePostCommit, -len(r.pendingCommit))
			r.pendingCommit = nil
		}
		r.dispatchPendingEffects()
		completions := (<-chan reactorEvent)(nil)
		if r.effectInflight > 0 {
			completions = r.completions
		}
		wakeC := (<-chan struct{})(nil)
		if r.hasPendingEffects() {
			wakeC = r.scheduler.wakeC()
		}
		select {
		case event, ok := <-mailbox:
			if !ok {
				mailbox = nil
				continue
			}
			if submit, ok := event.(submitLocalEvent); ok {
				r.pendingPrepare = append(r.pendingPrepare, submit.pendingPrepareEffect(r.nextPrepareSequence(targetKey(submit.target))))
				r.addEffectQueueDepth(effectStagePrepare, 1)
				select {
				case submit.ack <- nil:
				default:
				}
				continue
			}
			if mutation, ok := event.(subscriberMutationEvent); ok {
				r.applySubscriberMutation(mutation)
				continue
			}
			if reactorEvent, ok := event.(reactorEvent); ok {
				reactorEvent.apply(r)
			}
		case event, ok := <-completions:
			if !ok {
				continue
			}
			if r.effectInflight > 0 {
				r.effectInflight--
			}
			event.apply(r)
		case <-wakeC:
		}
	}
}

func (r *reactor) hasPendingEffects() bool {
	return len(r.pendingPrepare) > 0 || len(r.pendingAppend) > 0 || len(r.pendingCommit) > 0
}

func (r *reactor) dispatchPendingEffects() {
	for {
		dispatched := false
		if len(r.pendingPrepare) > 0 {
			next := r.pendingPrepare[0]
			ok, err := r.scheduler.trySubmitPrepare(r, next.effect)
			if err != nil {
				r.pendingPrepare = r.pendingPrepare[1:]
				r.addEffectQueueDepth(effectStagePrepare, -1)
				r.ackPrepareDispatch(next, nil)
				prepareScheduleErrorCompletion(next.effect, err).apply(r)
				dispatched = true
			} else if ok {
				r.pendingPrepare = r.pendingPrepare[1:]
				r.addEffectQueueDepth(effectStagePrepare, -1)
				r.effectInflight++
				r.ackPrepareDispatch(next, nil)
				r.observeEffectWorkerPressure(effectStagePrepare)
				dispatched = true
			}
		}
		if len(r.pendingAppend) > 0 {
			next := r.pendingAppend[0]
			ok, err := r.scheduler.trySubmitAppend(r, next)
			if err != nil {
				r.pendingAppend = r.pendingAppend[1:]
				r.addEffectQueueDepth(effectStageAppend, -1)
				appendScheduleErrorCompletion(next, err).apply(r)
				dispatched = true
			} else if ok {
				r.pendingAppend = r.pendingAppend[1:]
				r.addEffectQueueDepth(effectStageAppend, -1)
				r.effectInflight++
				r.observeEffectWorkerPressure(effectStageAppend)
				dispatched = true
			}
		}
		if len(r.pendingCommit) > 0 && r.stopCtx.Err() == nil {
			next := r.pendingCommit[0]
			ok, err := r.scheduler.trySubmitPostCommit(r, next)
			if err != nil {
				r.pendingCommit = r.pendingCommit[1:]
				r.addEffectQueueDepth(effectStagePostCommit, -1)
				commitScheduleErrorCompletion(next, err).apply(r)
				dispatched = true
			} else if ok {
				r.pendingCommit = r.pendingCommit[1:]
				r.addEffectQueueDepth(effectStagePostCommit, -1)
				r.effectInflight++
				r.observeEffectWorkerPressure(effectStagePostCommit)
				dispatched = true
			}
		}
		if !dispatched {
			return
		}
	}
}

func (r *reactor) ackPrepareDispatch(effect pendingPrepareEffect, err error) {
	select {
	case effect.ack <- err:
	default:
	}
}

func (r *reactor) complete(event reactorEvent) {
	r.completions <- event
}

func (r *reactor) releasePendingCommitEffects(effects []commitEffect) {
	if len(effects) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, effect := range effects {
		state := r.states[effect.key]
		if state == nil {
			continue
		}
		state.cancelCommitDispatch()
	}
}

func (r *reactor) close() {
	r.closeOnce.Do(func() {
		r.stopCancel()
		close(r.mailbox)
	})
}

func (r *reactor) wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *reactor) enqueue(ctx context.Context, target AuthorityTarget, items []SendBatchItem, future *Future) (<-chan error, error) {
	if err := contextErr(ctx); err != nil {
		observeLocalAdmission(r.appendPorts.observer, LocalAdmissionObservation{ReactorID: r.id, Result: errorClass(err), Items: len(items)})
		return nil, err
	}

	select {
	case r.effectSlots <- struct{}{}:
	default:
		observeLocalAdmission(r.appendPorts.observer, LocalAdmissionObservation{ReactorID: r.id, Result: channelWriteResultBackpressured, Items: len(items)})
		r.observePressure()
		return nil, ErrBackpressured
	}
	future.setOnDone(r.releaseEffectSlot)

	ack := make(chan error, 1)
	event := submitLocalEvent{
		target: target,
		items:  items,
		future: future,
		ack:    ack,
	}

	select {
	case r.mailbox <- event:
	default:
		r.releaseEffectSlot()
		observeLocalAdmission(r.appendPorts.observer, LocalAdmissionObservation{ReactorID: r.id, Result: channelWriteResultBackpressured, Items: len(items)})
		r.observePressure()
		return nil, ErrBackpressured
	}

	observeLocalAdmission(r.appendPorts.observer, LocalAdmissionObservation{ReactorID: r.id, Result: "accepted", Items: len(items)})
	r.observePressure()
	return ack, nil
}

func (r *reactor) nextPrepareSequence(key string) uint64 {
	seq := r.nextPrepareSeq[key]
	r.nextPrepareSeq[key] = seq + 1
	return seq
}

func (r *reactor) releaseEffectSlot() {
	select {
	case <-r.effectSlots:
	default:
	}
	r.observePressure()
}

func (r *reactor) hasAppendInflight() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, state := range r.states {
		if state.appendInflight > 0 {
			return true
		}
	}
	return false
}

func (r *reactor) hasCommitInflight() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, state := range r.states {
		if state.commitInflight {
			return true
		}
	}
	return false
}

func (e submitLocalEvent) pendingPrepareEffect(seq uint64) pendingPrepareEffect {
	key := targetKey(e.target)
	return pendingPrepareEffect{
		effect: prepareEffect{
			target: e.target,
			items:  e.items,
			future: e.future,
			key:    key,
			seq:    seq,
		},
		ack: e.ack,
	}
}

func (r *reactor) observePressure() {
	if r == nil {
		return
	}
	observer := reactorPressureObserver(r.appendPorts.observer)
	if observer == nil {
		return
	}
	observer.SetChannelWriteReactorPressure(r.pressureObservation())
}

func (r *reactor) observePressureLocked() {
	if r == nil {
		return
	}
	observer := reactorPressureObserver(r.appendPorts.observer)
	if observer == nil {
		return
	}
	observer.SetChannelWriteReactorPressure(r.pressureObservation())
}

func (r *reactor) pressureObservation() ReactorPressureObservation {
	return ReactorPressureObservation{
		ReactorID:           r.id,
		MailboxDepth:        len(r.mailbox),
		MailboxCapacity:     cap(r.mailbox),
		EffectSlotsUsed:     len(r.effectSlots),
		EffectSlotsCapacity: cap(r.effectSlots),
		PendingAppendItems:  int(r.pendingAppendItems.Load()),
		AppendInflightItems: int(r.appendInflightItems.Load()),
		PostCommitBacklog:   int(r.postCommitBacklog.Load()),
	}
}

func (r *reactor) observeAllEffectWorkerPressure() {
	r.observeEffectWorkerPressure(effectStagePrepare)
	r.observeEffectWorkerPressure(effectStageAppend)
	r.observeEffectWorkerPressure(effectStagePostCommit)
}

func (r *reactor) observeEffectWorkerPressure(stage string) {
	if r == nil {
		return
	}
	observeEffectWorkerPressure(r.appendPorts.observer, r.effectWorkerPressureObservation(stage))
}

func (r *reactor) effectWorkerPressureObservation(stage string) EffectWorkerPressureObservation {
	queueDepth, queueCapacity := r.effectQueuePressure(stage)
	return EffectWorkerPressureObservation{
		ReactorID:      r.id,
		Stage:          stage,
		WorkerInflight: int(r.effectWorkerBusy(stage).Load()),
		WorkerCapacity: r.effectWorkerCapacity(stage),
		QueueDepth:     queueDepth,
		QueueCapacity:  queueCapacity,
	}
}

func (r *reactor) effectWorkerCapacity(stage string) int {
	switch stage {
	case effectStageAppend:
		return r.appendCapacity
	case effectStagePostCommit:
		return r.postCommitCapacity
	default:
		return r.prepareCapacity
	}
}

func (r *reactor) effectWorkerBusy(stage string) *atomic.Int64 {
	switch stage {
	case effectStageAppend:
		return &r.appendWorkerBusy
	case effectStagePostCommit:
		return &r.commitWorkerBusy
	default:
		return &r.prepareWorkerBusy
	}
}

func (r *reactor) effectQueuePressure(stage string) (int, int) {
	switch stage {
	case effectStageAppend:
		return int(r.appendEffectQueue.Load()), cap(r.effectSlots)
	case effectStagePostCommit:
		return int(r.commitEffectQueue.Load()), cap(r.effectSlots)
	default:
		return int(r.prepareEffectQueue.Load()), cap(r.effectSlots)
	}
}

func (r *reactor) addEffectQueueDepth(stage string, delta int) {
	if r == nil || delta == 0 {
		return
	}
	switch stage {
	case effectStageAppend:
		r.appendEffectQueue.Add(int64(delta))
	case effectStagePostCommit:
		r.commitEffectQueue.Add(int64(delta))
	default:
		r.prepareEffectQueue.Add(int64(delta))
	}
}

func (r *reactor) addEffectWorkerBusy(stage string, delta int) {
	if r == nil || delta == 0 {
		return
	}
	r.effectWorkerBusy(stage).Add(int64(delta))
	r.observeEffectWorkerPressure(stage)
}

func (r *reactor) addPendingAppendItems(delta int) {
	if r == nil || delta == 0 {
		return
	}
	r.pendingAppendItems.Add(int64(delta))
}

func (r *reactor) addAppendInflightItems(delta int) {
	if r == nil || delta == 0 {
		return
	}
	r.appendInflightItems.Add(int64(delta))
}

func (r *reactor) addPostCommitBacklog(delta int) {
	if r == nil || delta == 0 {
		return
	}
	r.postCommitBacklog.Add(int64(delta))
}
