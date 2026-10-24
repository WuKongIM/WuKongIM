//go:build integration

package rpc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

func TestRequestClassificationPreservesDeadlineAndOwner(t *testing.T) {
	for _, mode := range []string{"caller_deadline", "service_deadline", "cancel", "stop", "mutation", "unknown", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64, Timeout: time.Second}
			if mode == "caller_deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
			} else if mode == "service_deadline" {
				opts.Timeout = 50 * time.Millisecond
			}
			var classified, invoked, released, finished atomic.Int32
			opts.CancelRunningFor = func(p []byte) bool {
				classified.Add(1)
				if mode == "panic" {
					panic("classification failed")
				}
				return len(p) == 1 && p[0] == 0
			}
			entered, proceed, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			observed := make(chan error, 1)
			deadline := make(chan time.Time, 1)
			observer := &recordingObserver{}
			svc := NewService(1, func(hctx context.Context, _ []byte) ([]byte, error) {
				invoked.Add(1)
				d, _ := hctx.Deadline()
				deadline <- d
				close(entered)
				if mode == "mutation" || mode == "unknown" {
					<-proceed
				} else {
					<-hctx.Done()
				}
				observed <- hctx.Err()
				return nil, hctx.Err()
			}, opts, observer)
			defer svc.Stop()
			defer close(proceed)
			payload := []byte{0}
			if mode == "mutation" {
				payload = []byte{1}
			}
			if mode == "unknown" {
				payload = nil
			}
			reply := make(chan Response, 1)
			before := time.Now()
			err := svc.Enqueue(Request{Context: ctx, Payload: core.NewOwnedBuffer(payload, func([]byte) { released.Add(1) }), Reply: reply, Finish: func() {
				if finished.Add(1) == 1 {
					close(done)
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			if mode != "panic" {
				waitClosed(t, entered)
				d := <-deadline
				if mode == "caller_deadline" {
					original, _ := ctx.Deadline()
					if !d.Equal(original) {
						t.Fatalf("request deadline extended: got %v want %v", d, original)
					}
				}
				if mode == "service_deadline" && (d.Before(before) || d.After(time.Now().Add(opts.Timeout))) {
					t.Fatal("service deadline was not applied")
				}
				switch mode {
				case "cancel":
					cancel()
				case "stop":
					svc.Stop()
				case "mutation", "unknown":
					cancel()
					proceed <- struct{}{}
				}
			}
			resp := waitResponse(t, reply)
			waitClosed(t, done)
			svc.Stop()
			if mode == "panic" {
				if resp.Err == nil || invoked.Load() != 0 {
					t.Fatal("panic executed handler or became successful")
				}
			} else {
				seen := <-observed
				if mode == "mutation" || mode == "unknown" {
					if seen != nil || resp.Err != nil {
						t.Fatal("non-read inherited caller cancellation")
					}
				} else if seen == nil || resp.Err == nil {
					t.Fatal("read cancellation missing")
				}
			}
			if released.Load() != 1 || finished.Load() != 1 || classified.Load() != 1 {
				t.Fatalf("release/finish/classify=%d/%d/%d", released.Load(), finished.Load(), classified.Load())
			}
			events := observer.snapshot()
			var retained core.Event
			for _, event := range events {
				if event.Name == "service_retained" {
					retained = event
				}
			}
			if retained.Items != 0 || retained.Bytes != 0 {
				t.Fatal("request retained ownership after completion")
			}
		})
	}
}

func TestRequestClassificationDoesNotRunForCanceledQueueOwner(t *testing.T) {
	var classified, released, finished, invoked atomic.Int32
	entered, release, queuedDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	opts := core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64, CancelRunningFor: func([]byte) bool { classified.Add(1); return false }}
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		invoked.Add(1)
		close(entered)
		<-release
		return nil, nil
	}, opts, nil)
	defer svc.Stop()
	defer close(release)
	firstReply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte{1}), Reply: firstReply}); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Context: ctx, Payload: core.NewOwnedBuffer([]byte{0}, func([]byte) { released.Add(1) }), Reply: reply, Finish: func() {
		if finished.Add(1) == 1 {
			close(queuedDone)
		}
	}}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if resp := waitResponse(t, reply); !errors.Is(resp.Err, core.ErrCanceled) {
		t.Fatalf("queued cancel=%v", resp.Err)
	}
	waitClosed(t, queuedDone)
	if classified.Load() != 1 || invoked.Load() != 1 || released.Load() != 1 || finished.Load() != 1 {
		t.Fatalf("classify/handler/release/finish=%d/%d/%d/%d", classified.Load(), invoked.Load(), released.Load(), finished.Load())
	}
	release <- struct{}{}
	if resp := waitResponse(t, firstReply); resp.Err != nil {
		t.Fatal(resp.Err)
	}
}
