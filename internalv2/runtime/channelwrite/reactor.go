package channelwrite

import (
	"context"
	"sync"
)

type reactor struct {
	id      int
	mailbox chan reactorEvent
	limits  channelStateLimits
	clock   Clock

	mu     sync.Mutex
	states map[string]*channelState

	startOnce sync.Once
	closeOnce sync.Once
	done      chan struct{}
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

func newReactor(id int, mailboxSize int, limits channelStateLimits, clock Clock) *reactor {
	return &reactor{
		id:      id,
		mailbox: make(chan reactorEvent, mailboxSize),
		limits:  limits,
		clock:   clock,
		states:  make(map[string]*channelState),
		done:    make(chan struct{}),
	}
}

func (r *reactor) start() {
	r.startOnce.Do(func() {
		go r.run()
	})
}

func (r *reactor) run() {
	defer close(r.done)
	for event := range r.mailbox {
		event.apply(r)
	}
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

func (e submitLocalEvent) apply(r *reactor) {
	r.mu.Lock()
	key := targetKey(e.target)
	if _, ok := r.states[key]; !ok {
		r.states[key] = newChannelState(e.target, r.limits)
	}
	r.mu.Unlock()

	e.future.complete(notAppendedResults(len(e.items)))
	select {
	case e.ack <- nil:
	default:
	}
}
