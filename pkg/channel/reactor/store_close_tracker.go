package reactor

import "sync"

// storeCloseTracker owns fallback closes that could not transfer to a worker pool.
// Seal prevents WaitGroup Add from racing with shutdown Wait.
type storeCloseTracker struct {
	mu        sync.Mutex
	accepting bool
	wg        sync.WaitGroup
}

func newStoreCloseTracker() *storeCloseTracker {
	return &storeCloseTracker{accepting: true}
}

func (t *storeCloseTracker) start(closeFn func()) bool {
	if t == nil || closeFn == nil {
		return false
	}
	t.mu.Lock()
	if !t.accepting {
		t.mu.Unlock()
		return false
	}
	t.wg.Add(1)
	t.mu.Unlock()
	go func() {
		defer t.wg.Done()
		closeFn()
	}()
	return true
}

func (t *storeCloseTracker) sealAndWait() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.accepting = false
	t.mu.Unlock()
	t.wg.Wait()
}
