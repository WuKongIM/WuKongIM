package multiraft

import (
	"context"
	"sync"
	"time"
)

type future struct {
	done chan struct{}

	once sync.Once
	mu   sync.Mutex
	// completionObserver is intentionally singular so observation remains
	// bounded by the accepted proposal's existing runtime lifecycle.
	completionObserver           FutureCompletionObserver
	completionObserverRegistered bool
	resolved                     bool
	observers                    []ProposalStageObserver
	createdAt                    time.Time
	trackedAt                    time.Time
	result                       Result
	err                          error
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

// ObserveCompletion registers the future's single terminal observer. When the
// future already resolved, it invokes observer synchronously before returning.
func (f *future) ObserveCompletion(observer FutureCompletionObserver) bool {
	if observer == nil {
		return false
	}
	f.mu.Lock()
	if f.completionObserverRegistered {
		f.mu.Unlock()
		return false
	}
	f.completionObserverRegistered = true
	f.completionObserver = observer
	resolved := f.resolved
	result, err := f.result, f.err
	f.mu.Unlock()
	if resolved {
		observer.ObserveFutureCompletion(result, err)
	}
	return true
}

func (f *future) resolve(result Result, err error) {
	f.once.Do(func() {
		f.mu.Lock()
		f.result = result
		f.err = err
		f.resolved = true
		observer := f.completionObserver
		f.mu.Unlock()
		if observer != nil {
			observer.ObserveFutureCompletion(result, err)
		}
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
