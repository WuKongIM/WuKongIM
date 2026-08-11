package channelappend

import (
	"context"
	"sync"
)

const (
	futureItemCompleted uint8 = 1 << iota
	futurePostCommitReservationAcquired
	futurePostCommitReservationReleased
)

// Future represents the eventual item-aligned result of an admitted send batch.
type Future struct {
	done chan struct{}
	once sync.Once

	mu      sync.Mutex
	results []SendBatchItemResult
	// itemState keeps completion and post-commit reservation ownership aligned
	// with the corresponding result without another per-item allocation.
	itemState []uint8
	remain    int
	closed    bool
	onDone    func()
}

func newFuture(itemCount int) *Future {
	if itemCount < 0 {
		itemCount = 0
	}
	// The result buffers are owned by the Future for its full lifetime. Wait may
	// be called more than once, so returned results are snapshots rather than
	// aliases into these buffers.
	future := &Future{
		done:      make(chan struct{}),
		results:   make([]SendBatchItemResult, itemCount),
		itemState: make([]uint8, itemCount),
		remain:    itemCount,
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
		priorState := f.itemState
		f.itemState = make([]uint8, len(f.results))
		for i := range f.itemState {
			f.itemState[i] = futureItemCompleted
			if i < len(priorState) {
				f.itemState[i] |= priorState[i] & (futurePostCommitReservationAcquired | futurePostCommitReservationReleased)
			}
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
	if index < len(f.results) && f.itemState[index]&futureItemCompleted == 0 {
		f.results[index] = result
		f.itemState[index] |= futureItemCompleted
		f.remain--
		closeDone = f.remain == 0
	}
	f.mu.Unlock()
	if closeDone {
		f.finish()
	}
}

func (f *Future) acquirePostCommitReservation(index int) bool {
	if f == nil || index < 0 {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.itemState) || f.itemState[index]&futurePostCommitReservationAcquired != 0 {
		return false
	}
	f.itemState[index] |= futurePostCommitReservationAcquired
	return true
}

// claimPostCommitReservationRelease returns true only for the first terminal
// release of one admitted item. The ownership bits remain available after
// SENDACK, so delayed completion never needs a separate heap token.
func (f *Future) claimPostCommitReservationRelease(index int) bool {
	if f == nil || index < 0 {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.itemState) ||
		f.itemState[index]&futurePostCommitReservationAcquired == 0 ||
		f.itemState[index]&futurePostCommitReservationReleased != 0 {
		return false
	}
	f.itemState[index] |= futurePostCommitReservationReleased
	return true
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
