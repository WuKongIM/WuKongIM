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

// Expiry must preserve the earlier deadline without canceling its parent or
// retaining an admission behind a handler that cannot currently complete.
func TestQueueExpiryDeadlineOrdering(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		queueTimeout, callerTimeout time.Duration
		cancel                      bool
	}{
		{name: "queue_only", queueTimeout: 10 * time.Millisecond},
		{name: "caller_first", queueTimeout: time.Hour, callerTimeout: 10 * time.Millisecond},
		{name: "queue_first", queueTimeout: 10 * time.Millisecond, callerTimeout: time.Hour},
		{name: "explicit_cancel", queueTimeout: time.Hour, callerTimeout: time.Hour, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started, unblock := make(chan struct{}), make(chan struct{})
			svc := NewService(1, func(context.Context, []byte) ([]byte, error) { close(started); <-unblock; return nil, nil }, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 4096, QueueTimeout: tc.queueTimeout}, nil)
			defer func() { close(unblock); svc.Stop() }()
			if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("running"))}); err != nil {
				t.Fatal(err)
			}
			waitClosed(t, started)
			ctx := context.Background()
			cancel := func() {}
			if tc.callerTimeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, tc.callerTimeout)
			}
			defer cancel()
			released, finished := make(chan struct{}), make(chan struct{})
			reply := make(chan Response, 1)
			if err := svc.Enqueue(Request{Context: ctx, Payload: core.NewOwnedBuffer([]byte("queued"), func([]byte) { close(released) }), Reply: reply, Finish: func() { close(finished) }}); err != nil {
				t.Fatal(err)
			}
			if tc.cancel {
				cancel()
			}
			want := core.ErrTimeout
			if tc.cancel {
				want = core.ErrCanceled
			}
			if got := waitResponse(t, reply).Err; !errors.Is(got, want) {
				t.Fatalf("expiry=%v, want %v", got, want)
			}
			waitClosed(t, finished)
			waitClosed(t, released)
			if tc.name == "queue_first" && ctx.Err() != nil {
				t.Fatalf("queue expiry canceled parent: %v", ctx.Err())
			}
			svc.mu.Lock()
			items, retained := svc.queuedItems, svc.retainedItems
			svc.mu.Unlock()
			if items != 0 || retained != 1 {
				t.Fatalf("expired admission retained: queued=%d retained=%d", items, retained)
			}
		})
	}
}

// Canceling the head must preserve the next request's original admission
// deadline; a late timer callback must not expire a newer admission early.
func TestQueueDeadlineAdvancesAfterHeadCancellation(t *testing.T) {
	started, unblock := make(chan struct{}), make(chan struct{})
	const timeout = 80 * time.Millisecond
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		close(started)
		<-unblock
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 2, MaxQueueBytes: 4096, QueueTimeout: timeout}, nil)
	defer func() { close(unblock); svc.Stop() }()
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("running"))}); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	headReply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Context: ctx, Reply: headReply}); err != nil {
		t.Fatal(err)
	}
	// Give the two queue deadlines a distinct ordering without relying on a
	// scheduler-sensitive upper time bound for receiving their replies.
	time.Sleep(20 * time.Millisecond)
	tailReply, tailFinished := make(chan Response, 1), make(chan struct{})
	tailStart := time.Now()
	if err := svc.Enqueue(Request{Reply: tailReply, Finish: func() { close(tailFinished) }}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := waitResponse(t, headReply).Err; !errors.Is(err, core.ErrCanceled) {
		t.Fatalf("head cancellation: %v", err)
	}
	if err := waitResponse(t, tailReply).Err; !errors.Is(err, core.ErrTimeout) {
		t.Fatalf("tail deadline: %v", err)
	}
	if elapsed := time.Since(tailStart); elapsed < timeout {
		t.Fatalf("tail expired early after %s", elapsed)
	}
	waitClosed(t, tailFinished)

	freshReply, freshFinished := make(chan Response, 1), make(chan struct{})
	freshStart := time.Now()
	if err := svc.Enqueue(Request{Reply: freshReply, Finish: func() { close(freshFinished) }}); err != nil {
		t.Fatal(err)
	}
	// Exercise the same callback body as an already-started previous timer.
	svc.expireQueueDeadline()
	if err := waitResponse(t, freshReply).Err; !errors.Is(err, core.ErrTimeout) {
		t.Fatalf("new admission deadline: %v", err)
	}
	if elapsed := time.Since(freshStart); elapsed < timeout {
		t.Fatalf("late callback expired a new admission after %s", elapsed)
	}
	waitClosed(t, freshFinished)
}
