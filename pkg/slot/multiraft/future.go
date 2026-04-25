package multiraft

import (
	"context"
	"sync"
)

type future struct {
	done chan struct{}

	once   sync.Once
	result Result
	err    error
}

func newFuture() *future {
	return &future{done: make(chan struct{})}
}

func (f *future) Wait(ctx context.Context) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-f.done:
		return f.result, f.err
	}
}

func (f *future) resolve(result Result, err error) {
	f.once.Do(func() {
		f.result = result
		f.err = err
		close(f.done)
	})
}
