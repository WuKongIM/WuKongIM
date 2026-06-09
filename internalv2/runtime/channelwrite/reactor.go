package channelwrite

import (
	"context"
	"sync"
)

type reactor struct {
	id                int
	mailbox           chan any
	effects           chan prepareEffect
	appendEffects     chan appendEffect
	commitEffects     chan commitEffect
	completions       chan reactorEvent
	effectSlots       chan struct{}
	limits            channelStateLimits
	ports             preparePorts
	appendPorts       appendPorts
	commitPorts       commitPorts
	effectWorkerCount int

	mu     sync.Mutex
	states map[string]*channelState

	nextPrepareSeq   map[string]uint64
	nextDrainSeq     map[string]uint64
	completedPrepare map[string]map[uint64]prepareCompletedEvent
	pendingPrepare   []pendingPrepareEffect
	pendingAppend    []appendEffect
	pendingCommit    []commitEffect

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

func newReactor(id int, mailboxSize int, limits channelStateLimits, effectWorkerCount int, ports preparePorts, appendPorts appendPorts, commitPorts commitPorts) *reactor {
	stopCtx, stopCancel := context.WithCancel(context.Background())
	return &reactor{
		id:                id,
		mailbox:           make(chan any, mailboxSize),
		effects:           make(chan prepareEffect, mailboxSize),
		appendEffects:     make(chan appendEffect, mailboxSize),
		commitEffects:     make(chan commitEffect, mailboxSize),
		completions:       make(chan reactorEvent, mailboxSize),
		effectSlots:       make(chan struct{}, mailboxSize),
		limits:            limits,
		ports:             ports,
		appendPorts:       appendPorts,
		commitPorts:       commitPorts,
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
	for mailbox != nil || completions != nil || len(r.pendingPrepare) > 0 || len(r.pendingAppend) > 0 || len(r.pendingCommit) > 0 {
		if r.stopCtx.Err() != nil && len(r.pendingCommit) > 0 {
			r.releasePendingCommitEffects(r.pendingCommit)
			r.pendingCommit = nil
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
		select {
		case effectOut <- nextEffect.effect:
			select {
			case nextEffect.ack <- nil:
			default:
			}
			r.pendingPrepare = r.pendingPrepare[1:]
		case appendOut <- nextAppend:
			r.pendingAppend = r.pendingAppend[1:]
		case commitOut <- nextCommit:
			r.pendingCommit = r.pendingCommit[1:]
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

func (r *reactor) startEffectWorkers() {
	workerCount := r.effectWorkerCount
	if workerCount <= 0 {
		workerCount = 1
	}
	r.effectWG.Add(workerCount * 3)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer r.effectWG.Done()
			for effect := range r.effects {
				r.completions <- effect.run(r.stopCtx, r.ports)
			}
		}()
		go func() {
			defer r.effectWG.Done()
			for effect := range r.appendEffects {
				r.completions <- effect.run(r.stopCtx, r.appendPorts)
			}
		}()
		go func() {
			defer r.effectWG.Done()
			for effect := range r.commitEffects {
				r.completions <- effect.run(r.stopCtx, r.commitPorts)
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
		return nil, err
	}

	select {
	case r.effectSlots <- struct{}{}:
	default:
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
		return nil, ErrBackpressured
	}

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
