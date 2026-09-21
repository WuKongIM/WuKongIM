//go:build integration

package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

func TestQueueDeadlineReleasesPayloadBeforeBlockedHandlerFinishes(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	released := make(chan struct{})
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) { close(started); <-block; return nil, nil }, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, QueueTimeout: 20 * time.Millisecond}, nil)
	defer func() { close(block); svc.Stop() }()
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("first"))}); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, started)
	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("expired"), func([]byte) { close(released) }), Reply: reply}); err != nil {
		t.Fatal(err)
	}
	if resp := waitResponse(t, reply); !errors.Is(resp.Err, core.ErrTimeout) {
		t.Fatalf("expiry=%v", resp.Err)
	}
	waitClosed(t, released)
	svc.mu.Lock()
	queued, bytes := svc.queuedItems, svc.queuedBytes
	svc.mu.Unlock()
	if queued != 0 || bytes != 0 {
		t.Fatalf("expired request retained FIFO capacity: %d/%d", queued, bytes)
	}
}

func TestQueueBudgetIncludesWaitingForSharedExecutor(t *testing.T) {
	pool, err := NewExecutor(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, unblock := make(chan struct{}), make(chan struct{})
	defer pool.Stop()
	if err := pool.Submit(&serviceTask{runFunc: func() { close(started); <-unblock }}); err != nil {
		t.Fatal(err)
	}
	defer close(unblock)
	waitClosed(t, started)
	released := make(chan struct{})
	svc := NewServiceWithExecutor(1, func(context.Context, []byte) ([]byte, error) {
		t.Error("expired request executed")
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, QueueTimeout: 20 * time.Millisecond}, nil, pool)
	defer svc.Stop()
	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("expired"), func([]byte) { close(released) }), Reply: reply}); err != nil {
		t.Fatal(err)
	}
	if resp := waitResponse(t, reply); !errors.Is(resp.Err, core.ErrTimeout) {
		t.Fatalf("expiry=%v", resp.Err)
	}
	waitClosed(t, released)
}
