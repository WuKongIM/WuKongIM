//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"
)

// Failure inventory: a full execution pool must not freeze sparse arrivals into
// tiny queued batches; retained work must still obey the target size, detach
// canceled callers, and drain without adding an execution permit.
func TestPersonDirectoryBatcherCollectsWhileExecutionIsBusy(t *testing.T) {
	node := &recordingPersonDirectoryBatchNode{}
	batcher := newPersonDirectoryBatcher(node, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = batcher.Stop(context.Background()) })
	// Occupy the actual execution gate so collection expiry cannot submit.
	for range cap(batcher.active) {
		batcher.active <- struct{}{}
	}
	held := true
	defer func() {
		if held {
			for range cap(batcher.active) {
				<-batcher.active
			}
		}
	}()
	waiters := make([]*personDirectoryWaiter, 0, 8)
	for i := range 8 {
		waiter, err := batcher.admit(ctx, testPersonDirectoryMutation(i))
		if err != nil {
			t.Fatal(err)
		}
		waiters = append(waiters, waiter)
		// Each arrival is later than the ordinary 1 ms collection window.
		time.Sleep(5 * time.Millisecond)
	}
	batcher.detach(waiters[7])
	for range cap(batcher.active) {
		<-batcher.active
	}
	held = false
	for _, waiter := range waiters[:7] {
		select {
		case result := <-waiter.result:
			if result.err != nil {
				t.Fatal(result.err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := batcher.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if !equalInts(node.batchSizes, []int{7}) {
		t.Fatalf("queued batch sizes = %v, want one seven-item batch", node.batchSizes)
	}
}
