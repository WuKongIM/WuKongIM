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
	doneSet []bool
	remain  int
	closed  bool
	onDone  func()
}

func newFuture(itemCount int) *Future {
	if itemCount < 0 {
		itemCount = 0
	}
	future := &Future{
		done:    make(chan struct{}),
		results: make([]SendBatchItemResult, itemCount),
		doneSet: make([]bool, itemCount),
		remain:  itemCount,
	}
	if itemCount == 0 {
		future.once.Do(func() {
			future.closed = true
			close(future.done)
		})
	}
	return future
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
		f.doneSet = make([]bool, len(f.results))
		for i := range f.doneSet {
			f.doneSet[i] = true
		}
		f.remain = 0
		f.closed = true
		onDone := f.onDone
		f.mu.Unlock()
		close(f.done)
		if onDone != nil {
			onDone()
		}
	})
}

func (f *Future) completeItem(index int, result SendBatchItemResult) {
	if f == nil || index < 0 {
		return
	}
	closeDone := false
	f.mu.Lock()
	if index < len(f.results) && !f.doneSet[index] {
		f.results[index] = result
		f.doneSet[index] = true
		f.remain--
		closeDone = f.remain == 0
	}
	f.mu.Unlock()
	if closeDone {
		f.finish()
	}
}

func (f *Future) completeItems(results []SendBatchItemResult, terminal func(int) bool) {
	if f == nil {
		return
	}
	for i, result := range results {
		if terminal == nil || terminal(i) {
			f.completeItem(i, result)
		}
	}
}

func (f *Future) setOnDone(fn func()) {
	if f == nil {
		return
	}
	callNow := false
	f.mu.Lock()
	f.onDone = fn
	callNow = f.closed && fn != nil
	f.mu.Unlock()
	if callNow {
		fn()
	}
}

func (f *Future) finish() {
	if f == nil {
		return
	}
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		onDone := f.onDone
		f.mu.Unlock()
		close(f.done)
		if onDone != nil {
			onDone()
		}
	})
}

func (f *Future) snapshot() []SendBatchItemResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SendBatchItemResult(nil), f.results...)
}
