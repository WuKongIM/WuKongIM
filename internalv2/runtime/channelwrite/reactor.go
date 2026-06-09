package channelwrite

import (
	"context"
	"sync"
)

type reactor struct {
	id                int
	mailbox           chan any
	effects           chan prepareEffect
	completions       chan reactorEvent
	limits            channelStateLimits
	ports             preparePorts
	effectWorkerCount int

	mu     sync.Mutex
	states map[string]*channelState

	startOnce sync.Once
	closeOnce sync.Once
	done      chan struct{}
	effectWG  sync.WaitGroup
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

func newReactor(id int, mailboxSize int, limits channelStateLimits, effectWorkerCount int, ports preparePorts) *reactor {
	return &reactor{
		id:                id,
		mailbox:           make(chan any, mailboxSize),
		effects:           make(chan prepareEffect, mailboxSize),
		completions:       make(chan reactorEvent, mailboxSize),
		limits:            limits,
		ports:             ports,
		effectWorkerCount: effectWorkerCount,
		states:            make(map[string]*channelState),
		done:              make(chan struct{}),
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
	var pendingEffects []pendingPrepareEffect
	for mailbox != nil || completions != nil || len(pendingEffects) > 0 {
		if mailbox == nil && len(pendingEffects) == 0 && effects != nil {
			close(effects)
			effects = nil
		}
		var effectOut chan prepareEffect
		var nextEffect pendingPrepareEffect
		if len(pendingEffects) > 0 {
			effectOut = effects
			nextEffect = pendingEffects[0]
		}
		select {
		case effectOut <- nextEffect.effect:
			select {
			case nextEffect.ack <- nil:
			default:
			}
			pendingEffects = pendingEffects[1:]
		case event, ok := <-mailbox:
			if !ok {
				mailbox = nil
				continue
			}
			if submit, ok := event.(submitLocalEvent); ok {
				pendingEffects = append(pendingEffects, submit.pendingPrepareEffect())
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

func (r *reactor) startEffectWorkers() {
	workerCount := r.effectWorkerCount
	if workerCount <= 0 {
		workerCount = 1
	}
	r.effectWG.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer r.effectWG.Done()
			for effect := range r.effects {
				r.completions <- effect.run(r.ports)
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
		return nil, ErrBackpressured
	}

	return ack, nil
}

func (e submitLocalEvent) pendingPrepareEffect() pendingPrepareEffect {
	return pendingPrepareEffect{
		effect: prepareEffect{
			target: e.target,
			items:  e.items,
			future: e.future,
		},
		ack: e.ack,
	}
}
