package replica

import (
	"context"
	"sync"
)

type durableLane struct {
	once sync.Once
	ch   chan struct{}
}

func (l *durableLane) init() {
	l.once.Do(func() {
		l.ch = make(chan struct{}, 1)
		l.ch <- struct{}{}
	})
}

func (l *durableLane) Lock() {
	l.init()
	<-l.ch
}

func (l *durableLane) Unlock() {
	l.init()
	select {
	case l.ch <- struct{}{}:
	default:
		panic("replica: durable lane unlock without matching lock")
	}
}

func (l *durableLane) LockContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	l.init()
	select {
	case <-l.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *replica) initDurableLane() {
	r.durableMu.init()
}

func (r *replica) acquireDurableLane(ctx context.Context) (func(), error) {
	if err := r.durableMu.LockContext(ctx); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() { r.durableMu.Unlock() })
	}, nil
}
