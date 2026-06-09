package channelwrite

import (
	"context"
	"errors"
	"sync"
)

// ErrNotAppended marks an admitted skeleton submission that has not run durable append.
var ErrNotAppended = errors.New("internalv2/channelwrite: not appended")

// Future represents the eventual item-aligned result of an admitted send batch.
type Future struct {
	done chan struct{}
	once sync.Once

	mu      sync.Mutex
	results []SendBatchItemResult
}

func newFuture(itemCount int) *Future {
	if itemCount < 0 {
		itemCount = 0
	}
	return &Future{
		done:    make(chan struct{}),
		results: make([]SendBatchItemResult, itemCount),
	}
}

// Wait blocks until the batch completes or the context expires.
// The Task 2 skeleton completes admitted batches with item-level ErrNotAppended.
func (f *Future) Wait(ctx context.Context) ([]SendBatchItemResult, error) {
	if f == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-f.done:
		return f.snapshot(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *Future) complete(results []SendBatchItemResult) {
	if f == nil {
		return
	}
	f.once.Do(func() {
		f.mu.Lock()
		f.results = append([]SendBatchItemResult(nil), results...)
		f.mu.Unlock()
		close(f.done)
	})
}

func (f *Future) snapshot() []SendBatchItemResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SendBatchItemResult(nil), f.results...)
}

func notAppendedResults(itemCount int) []SendBatchItemResult {
	if itemCount <= 0 {
		return nil
	}
	results := make([]SendBatchItemResult, itemCount)
	for i := range results {
		results[i].Err = ErrNotAppended
	}
	return results
}
