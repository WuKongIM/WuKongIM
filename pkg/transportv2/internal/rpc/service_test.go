package rpc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestServiceQueueFullReturnsBusy(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		close(started)
		<-block
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024})
	defer func() {
		close(block)
		svc.Stop()
	}()

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("running"))}); err != nil {
		t.Fatalf("Enqueue(running) error = %v", err)
	}
	waitClosed(t, started)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("queued"))}); err != nil {
		t.Fatalf("Enqueue(queued) error = %v", err)
	}

	var released atomic.Int32
	err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("full"), func([]byte) {
		released.Add(1)
	})})
	if !errors.Is(err, core.ErrBusy) {
		t.Fatalf("Enqueue(full) error = %v, want %v", err, core.ErrBusy)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1", got)
	}
}

func TestServiceTimeout(t *testing.T) {
	svc := NewService(1, func(ctx context.Context, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, Timeout: 10 * time.Millisecond})
	defer svc.Stop()

	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("slow")), Reply: reply}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	resp := waitResponse(t, reply)
	if !errors.Is(resp.Err, core.ErrTimeout) {
		t.Fatalf("response err = %v, want %v", resp.Err, core.ErrTimeout)
	}
}

func TestServiceMaxQueueBytesReturnsBusy(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		close(started)
		<-block
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 4, MaxQueueBytes: 7})
	defer func() {
		close(block)
		svc.Stop()
	}()

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("run"))}); err != nil {
		t.Fatalf("Enqueue(running) error = %v", err)
	}
	waitClosed(t, started)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("1234"))}); err != nil {
		t.Fatalf("Enqueue(queued) error = %v", err)
	}

	var released atomic.Int32
	err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("5678"), func([]byte) {
		released.Add(1)
	})})
	if !errors.Is(err, core.ErrBusy) {
		t.Fatalf("Enqueue(over bytes) error = %v, want %v", err, core.ErrBusy)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1", got)
	}
}

func TestServiceStopDrainsQueuedPayload(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	svc := NewService(1, func(ctx context.Context, _ []byte) ([]byte, error) {
		close(started)
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 4, MaxQueueBytes: 1024})

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("running"))}); err != nil {
		t.Fatalf("Enqueue(running) error = %v", err)
	}
	waitClosed(t, started)

	var released atomic.Int32
	if err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("queued"), func([]byte) {
		released.Add(1)
	})}); err != nil {
		t.Fatalf("Enqueue(queued) error = %v", err)
	}

	svc.Stop()
	close(block)

	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1 queued release", got)
	}
}

func TestServiceStopReturnsWhenHandlerIgnoresContext(t *testing.T) {
	started := make(chan struct{})
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		close(started)
		select {}
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024})

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("stuck"))}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	waitClosed(t, started)

	done := make(chan struct{})
	go func() {
		svc.Stop()
		close(done)
	}()
	waitClosed(t, done)
}

func TestServiceStopRepliesErrStoppedForQueuedRequest(t *testing.T) {
	started := make(chan struct{})
	svc := NewService(1, func(ctx context.Context, _ []byte) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 2, MaxQueueBytes: 1024})

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("running"))}); err != nil {
		t.Fatalf("Enqueue(running) error = %v", err)
	}
	waitClosed(t, started)

	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("queued")), Reply: reply}); err != nil {
		t.Fatalf("Enqueue(queued) error = %v", err)
	}

	svc.Stop()
	resp := waitResponse(t, reply)
	if !errors.Is(resp.Err, core.ErrStopped) {
		t.Fatalf("reply err = %v, want %v", resp.Err, core.ErrStopped)
	}
}

func TestServiceMaxPayloadReleases(t *testing.T) {
	var released atomic.Int32
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		t.Fatal("handler should not run for oversized payload")
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, MaxPayload: 3})
	defer svc.Stop()

	err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("large"), func([]byte) {
		released.Add(1)
	})})
	if !errors.Is(err, core.ErrMsgTooLarge) {
		t.Fatalf("Enqueue() error = %v, want %v", err, core.ErrMsgTooLarge)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1", got)
	}
}

func TestServiceReplyPayloadIsCopied(t *testing.T) {
	response := []byte("hello")
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		return response, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024})
	defer svc.Stop()

	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("request")), Reply: reply}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	resp := waitResponse(t, reply)

	response[0] = 'x'
	if got := string(resp.Payload); got != "hello" {
		t.Fatalf("reply payload = %q, want copied hello", got)
	}
}

func waitResponse(t *testing.T, ch <-chan Response) Response {
	t.Helper()

	select {
	case resp := <-ch:
		return resp
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response")
		return Response{}
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for close")
	}
}
