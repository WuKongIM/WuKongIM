package channelwrite

import (
	"context"
	"sync"
)

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
