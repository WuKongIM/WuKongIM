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
	resolved := f.resolved
	result, err := f.result, f.err
	if !resolved {
		f.completionObserver = observer
	}
	f.mu.Unlock()
	if resolved {
		observer.ObserveFutureCompletion(result, err)
	}
	return true
}

// futureCompletion carries bounded terminal callback work captured during
// resolution. Callers that own Runtime or Slot locks must dispatch after unlock.
type futureCompletion struct {
	done     chan struct{}
	observer FutureCompletionObserver
	result   Result
	err      error
}

func (c futureCompletion) dispatch() {
	if c.done == nil {
		return
	}
	close(c.done)
	if c.observer != nil {
		c.observer.ObserveFutureCompletion(c.result, c.err)
	}
}

func dispatchFutureCompletions(completions []futureCompletion) {
	for _, completion := range completions {
		completion.dispatch()
	}
}

func (f *future) resolve(result Result, err error) futureCompletion {
	var completion futureCompletion
	if f == nil {
		return completion
	}
	f.once.Do(func() {
		f.mu.Lock()
		f.result = result
		f.err = err
		f.resolved = true
		completion = futureCompletion{
			done:     f.done,
			observer: f.completionObserver,
			result:   result,
			err:      err,
		}
		f.completionObserver = nil
		f.mu.Unlock()
	})
	return completion
}

func (f *future) resolveAndDispatch(result Result, err error) {
	f.resolve(result, err).dispatch()
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
