package channelappend

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
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

func TestWorkerPoolStopKeepsOwnershipUntilWorkerExits(t *testing.T) {
	baseline := goruntimeregistry.Default().Baseline()
	p := newWorkerPool(1)
	entered := make(chan struct{})
	release := make(chan struct{})
	if err := p.submit(func() {
		close(entered)
		<-release
	}); err != nil {
		t.Fatalf("submit error = %v", err)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := p.stop(ctx); err == nil {
		t.Fatal("stop error = nil, want caller deadline")
	}
	stats := p.poolStats()
	if stats.Goroutines == 0 || stats.BusyTasks != 1 {
		t.Fatalf("timed-out pool stats = %+v, want live worker ownership", stats)
	}
	close(release)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := goruntimeregistry.Default().Group(goruntimeregistry.ModuleChannelAppend).WaitFrom(waitCtx, baseline); err != nil {
		t.Fatalf("channelappend group Wait() error = %v", err)
	}
}
