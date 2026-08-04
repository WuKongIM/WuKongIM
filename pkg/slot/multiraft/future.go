package multiraft

import (
	"context"
	"sync"
	"time"
)

type futureCompletionState uint8

const (
	futureCompletionPending futureCompletionState = iota
	futureCompletionTerminalPendingDispatch
	futureCompletionDispatchSafe
)

type future struct {
	done chan struct{}

	once sync.Once
	mu   sync.Mutex
	// completionObserver is intentionally singular so observation remains
	// bounded by the accepted proposal's existing runtime lifecycle.
	completionObserver           FutureCompletionObserver
	completionObserverRegistered bool
	completionState              futureCompletionState
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

// ObserveCompletion registers the future's single terminal observer. Once
// dispatch is safe, it invokes a late observer synchronously without retaining it.
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
	dispatchSafe := f.completionState == futureCompletionDispatchSafe
	result, err := f.result, f.err
	if !dispatchSafe {
		f.completionObserver = observer
	}
	f.mu.Unlock()
	if dispatchSafe {
		observer.ObserveFutureCompletion(result, err)
	}
	return true
}

// futureCompletion carries bounded terminal callback work captured during
// resolution. Callers that own Runtime or Slot locks must dispatch after unlock.
type futureCompletion struct {
	future *future
}

func (c futureCompletion) dispatch() {
	if c.future == nil {
		return
	}
	f := c.future
	f.mu.Lock()
	if f.completionState != futureCompletionTerminalPendingDispatch {
		f.mu.Unlock()
		return
	}
	f.completionState = futureCompletionDispatchSafe
	observer := f.completionObserver
	f.completionObserver = nil
	result, err := f.result, f.err
	done := f.done
	f.mu.Unlock()

	close(done)
	if observer != nil {
		observer.ObserveFutureCompletion(result, err)
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
		f.completionState = futureCompletionTerminalPendingDispatch
		completion = futureCompletion{future: f}
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
