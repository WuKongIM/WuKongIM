package channelwrite

import (
	"context"
	"sync"
	"sync/atomic"
)

type reactor struct {
	id                int
	mailbox           chan any
	effects           chan prepareEffect
	appendEffects     chan appendEffect
	commitEffects     chan commitEffect
	cursorEffects     chan cursorEffect
	completions       chan reactorEvent
	effectSlots       chan struct{}
	limits            channelStateLimits
	ports             preparePorts
	appendPorts       appendPorts
	commitPorts       commitPorts
	cursorPorts       cursorPorts
	effectWorkerCount int

	mu     sync.Mutex
	states map[string]*channelState

	nextPrepareSeq   map[string]uint64
	nextDrainSeq     map[string]uint64
	completedPrepare map[string]map[uint64]prepareCompletedEvent
	pendingPrepare   []pendingPrepareEffect
	pendingAppend    []appendEffect
	pendingCommit    []commitEffect
	pendingCursor    []cursorEffect

	pendingAppendItems  atomic.Int64
	appendInflightItems atomic.Int64
	postCommitBacklog   atomic.Int64
	prepareWorkerBusy   atomic.Int64
	appendWorkerBusy    atomic.Int64
	commitWorkerBusy    atomic.Int64
	cursorWorkerBusy    atomic.Int64

	startOnce  sync.Once
	closeOnce  sync.Once
	done       chan struct{}
	effectWG   sync.WaitGroup
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

type pendingPrepareEffect struct {
	effect prepareEffect
	ack    chan error
}

func newReactor(id int, mailboxSize int, limits channelStateLimits, effectWorkerCount int, ports preparePorts, appendPorts appendPorts, commitPorts commitPorts, cursorPorts cursorPorts) *reactor {
	stopCtx, stopCancel := context.WithCancel(context.Background())
	return &reactor{
		id:                id,
		mailbox:           make(chan any, mailboxSize),
		effects:           make(chan prepareEffect, mailboxSize),
		appendEffects:     make(chan appendEffect, mailboxSize),
		commitEffects:     make(chan commitEffect, mailboxSize),
		cursorEffects:     make(chan cursorEffect, mailboxSize),
		completions:       make(chan reactorEvent, mailboxSize),
		effectSlots:       make(chan struct{}, mailboxSize),
		limits:            limits,
		ports:             ports,
		appendPorts:       appendPorts,
		commitPorts:       commitPorts,
		cursorPorts:       cursorPorts,
		effectWorkerCount: effectWorkerCount,
		states:            make(map[string]*channelState),
		nextPrepareSeq:    make(map[string]uint64),
		nextDrainSeq:      make(map[string]uint64),
		completedPrepare:  make(map[string]map[uint64]prepareCompletedEvent),
		done:              make(chan struct{}),
		stopCtx:           stopCtx,
		stopCancel:        stopCancel,
	}
}

func (r *reactor) start() {
	r.startOnce.Do(func() {
		r.startEffectWorkers()
		go r.run()
	})
}

func (r *reactor) run() {
	defer close(r.done)
	mailbox := r.mailbox
	completions := r.completions
	effects := r.effects
	appendEffects := r.appendEffects
	commitEffects := r.commitEffects
	cursorEffects := r.cursorEffects
	for mailbox != nil || completions != nil || len(r.pendingPrepare) > 0 || len(r.pendingAppend) > 0 || len(r.pendingCommit) > 0 || len(r.pendingCursor) > 0 {
		if r.stopCtx.Err() != nil && len(r.pendingCommit) > 0 {
			r.releasePendingCommitEffects(r.pendingCommit)
			r.pendingCommit = nil
		}
		if r.stopCtx.Err() != nil && len(r.pendingCursor) > 0 {
			r.releasePendingCursorEffects(r.pendingCursor)
			r.pendingCursor = nil
		}
		if mailbox == nil && len(r.pendingPrepare) == 0 && effects != nil {
			close(effects)
			effects = nil
		}
		if mailbox == nil && len(r.pendingAppend) == 0 && !r.hasAppendInflight() && appendEffects != nil {
			close(appendEffects)
			appendEffects = nil
		}
		if mailbox == nil && len(r.pendingCommit) == 0 && !r.hasCommitInflight() && commitEffects != nil {
			close(commitEffects)
			commitEffects = nil
		}
		if mailbox == nil && len(r.pendingCursor) == 0 && !r.hasCursorInflight() && cursorEffects != nil {
			close(cursorEffects)
			cursorEffects = nil
		}
		var effectOut chan prepareEffect
		var nextEffect pendingPrepareEffect
		if len(r.pendingPrepare) > 0 && effects != nil {
			effectOut = effects
			nextEffect = r.pendingPrepare[0]
		}
		var appendOut chan appendEffect
		var nextAppend appendEffect
		if len(r.pendingAppend) > 0 && appendEffects != nil {
			appendOut = appendEffects
			nextAppend = r.pendingAppend[0]
		}
		var commitOut chan commitEffect
		var nextCommit commitEffect
		if len(r.pendingCommit) > 0 && commitEffects != nil {
			commitOut = commitEffects
			nextCommit = r.pendingCommit[0]
		}
		var cursorOut chan cursorEffect
		var nextCursor cursorEffect
		if len(r.pendingCursor) > 0 && cursorEffects != nil {
			cursorOut = cursorEffects
			nextCursor = r.pendingCursor[0]
		}
		select {
		case effectOut <- nextEffect.effect:
			select {
			case nextEffect.ack <- nil:
			default:
			}
			r.pendingPrepare = r.pendingPrepare[1:]
			r.observeEffectWorkerPressure("prepare")
		case appendOut <- nextAppend:
			r.pendingAppend = r.pendingAppend[1:]
			r.observeEffectWorkerPressure("append")
		case commitOut <- nextCommit:
			r.pendingCommit = r.pendingCommit[1:]
			r.observeEffectWorkerPressure("post_commit")
		case cursorOut <- nextCursor:
			r.pendingCursor = r.pendingCursor[1:]
			r.observeEffectWorkerPressure("replay")
		case event, ok := <-mailbox:
			if !ok {
				mailbox = nil
				continue
			}
			if submit, ok := event.(submitLocalEvent); ok {
				r.pendingPrepare = append(r.pendingPrepare, submit.pendingPrepareEffect(r.nextPrepareSequence(targetKey(submit.target))))
				select {
				case submit.ack <- nil:
				default:
				}
				continue
			}
			if reactorEvent, ok := event.(reactorEvent); ok {
				reactorEvent.apply(r)
			}
		case event, ok := <-completions:
			if !ok {
				completions = nil
				continue
			}
			event.apply(r)
		}
	}
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

func (r *reactor) releasePendingCursorEffects(effects []cursorEffect) {
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
		state.cancelCursorReplayDispatch()
	}
}

func (r *reactor) startEffectWorkers() {
	workerCount := r.effectWorkerCount
	if workerCount <= 0 {
		workerCount = 1
	}
	r.observeAllEffectWorkerPressure()
	r.effectWG.Add(workerCount * 4)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer r.effectWG.Done()
			for effect := range r.effects {
				r.addEffectWorkerBusy("prepare", 1)
				r.completions <- effect.run(r.stopCtx, r.ports)
				r.addEffectWorkerBusy("prepare", -1)
			}
		}()
		go func() {
			defer r.effectWG.Done()
			for effect := range r.appendEffects {
				r.addEffectWorkerBusy("append", 1)
				r.completions <- effect.run(r.stopCtx, r.appendPorts)
				r.addEffectWorkerBusy("append", -1)
			}
		}()
		go func() {
			defer r.effectWG.Done()
			for effect := range r.commitEffects {
				r.addEffectWorkerBusy("post_commit", 1)
				r.completions <- effect.run(r.stopCtx, r.commitPorts)
				r.addEffectWorkerBusy("post_commit", -1)
			}
		}()
		go func() {
			defer r.effectWG.Done()
			for effect := range r.cursorEffects {
				r.addEffectWorkerBusy("replay", 1)
				r.completions <- effect.run(r.stopCtx, r.cursorPorts)
				r.addEffectWorkerBusy("replay", -1)
			}
		}()
	}
	go func() {
		r.effectWG.Wait()
		close(r.completions)
	}()
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

func (r *reactor) hasCursorInflight() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, state := range r.states {
		if state.replayInflight {
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
	r.observeEffectWorkerPressure("prepare")
	r.observeEffectWorkerPressure("append")
	r.observeEffectWorkerPressure("post_commit")
	r.observeEffectWorkerPressure("replay")
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
		WorkerCapacity: r.effectWorkerCapacity(),
		QueueDepth:     queueDepth,
		QueueCapacity:  queueCapacity,
	}
}

func (r *reactor) effectWorkerCapacity() int {
	if r.effectWorkerCount > 0 {
		return r.effectWorkerCount
	}
	return 1
}

func (r *reactor) effectWorkerBusy(stage string) *atomic.Int64 {
	switch stage {
	case "append":
		return &r.appendWorkerBusy
	case "post_commit":
		return &r.commitWorkerBusy
	case "replay":
		return &r.cursorWorkerBusy
	default:
		return &r.prepareWorkerBusy
	}
}

func (r *reactor) effectQueuePressure(stage string) (int, int) {
	switch stage {
	case "append":
		return len(r.appendEffects), cap(r.appendEffects)
	case "post_commit":
		return len(r.commitEffects), cap(r.commitEffects)
	case "replay":
		return len(r.cursorEffects), cap(r.cursorEffects)
	default:
		return len(r.effects), cap(r.effects)
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
