//go:build integration && !race

package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

// TestQueueDeadlineOwnershipAllocations measures actual admission and execution
// with queue deadlines enabled. A warm service must not allocate a timer per call.
func TestQueueDeadlineOwnershipAllocations(t *testing.T) {
	finished := make(chan struct{}, 1)
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) { return nil, nil }, core.ServiceOptions{
		Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64, QueueTimeout: time.Hour,
	}, nil)
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
	if allocations := testing.AllocsPerRun(1000, call); allocations > 1 {
		t.Fatalf("queue deadline allocated %.2f objects/request, want at most 1", allocations)
	}
}
