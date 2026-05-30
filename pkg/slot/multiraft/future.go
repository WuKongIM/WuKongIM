package multiraft

import (
	"context"
	"sync"
	"time"
)

type future struct {
	done chan struct{}

	once      sync.Once
	observers []ProposalStageObserver
	createdAt time.Time
	trackedAt time.Time
	result    Result
	err       error
}

func newFuture(observers []ProposalStageObserver) *future {
	return &future{
		done:      make(chan struct{}),
		observers: append([]ProposalStageObserver(nil), observers...),
		createdAt: time.Now(),
	}
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

func (f *future) observeStage(stage string, err error, d time.Duration) {
	if f == nil {
		return
	}
	observeProposalStage(f.observers, stage, err, d)
}

func (f *future) observeStageSince(stage string, err error, started time.Time) {
	if started.IsZero() {
		return
	}
	f.observeStage(stage, err, time.Since(started))
}
