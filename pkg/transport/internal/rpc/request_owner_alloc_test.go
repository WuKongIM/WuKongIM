//go:build !race

package rpc

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

// TestServiceRequestOwnershipAllocations exercises actual queue admission and
// executor completion with reusable caller resources. The owner must not need
// a second allocation when transferring from the FIFO to a worker.
func TestServiceRequestOwnershipAllocations(t *testing.T) {
	finished := make(chan struct{}, 1)
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) { return nil, nil }, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64}, nil)
	defer svc.Stop()
	req := Request{Payload: core.NewOwnedBuffer([]byte("work"), nil), Finish: func() { finished <- struct{}{} }}
	call := func() {
		if err := svc.Enqueue(req); err != nil {
			t.Fatal(err)
		}
		<-finished
	}
	for i := 0; i < 100; i++ {
		call()
	}
	allocations := testing.AllocsPerRun(1000, call)
	if allocations > 1 {
		t.Fatalf("queue-to-executor ownership allocated %.2f objects/request, want at most 1", allocations)
	}
}
