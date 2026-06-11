package channelappend

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPoolRunsTasks(t *testing.T) {
	p := newWorkerPool(4)
	defer p.stop(context.Background())
	var wg sync.WaitGroup
	wg.Add(8)
	var count atomic.Int64
	for i := 0; i < 8; i++ {
		if err := p.submit(func() { count.Add(1); wg.Done() }); err != nil {
			t.Fatalf("submit error = %v", err)
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tasks did not complete")
	}
	if count.Load() != 8 {
		t.Fatalf("count = %d, want 8", count.Load())
	}
}
