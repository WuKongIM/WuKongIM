//go:build integration && !race

package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

// TestReadCancellationOwnershipAllocations includes admission, queue watching,
// execution timeout, service-stop propagation and terminal ownership cleanup.
func TestReadCancellationOwnershipAllocations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 1)
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) { return nil, nil }, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64, QueueTimeout: time.Hour, Timeout: time.Hour, CancelRunning: true}, nil)
	defer svc.Stop()
	req := Request{Context: ctx, Payload: core.NewOwnedBuffer([]byte("work"), nil), Finish: func() { done <- struct{}{} }}
	call := func() {
		if err := svc.Enqueue(req); err != nil {
			t.Fatal(err)
		}
		<-done
	}
	for i := 0; i < 100; i++ {
		call()
	}
	if n := testing.AllocsPerRun(1000, call); n > 10 {
		t.Fatalf("read lifecycle allocated %.2f objects/request, want at most 10", n)
	}
}
