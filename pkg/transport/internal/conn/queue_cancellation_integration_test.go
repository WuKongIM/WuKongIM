//go:build integration

package conn

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/rpc"
)

// A tracked unbudgeted request must release its queue slot and payload while an
// earlier mutation blocks the worker, even when close first cancels the parent.
func TestInboundQueueCancellationReleasesBlockedAdmission(t *testing.T) {
	for _, closeConn := range []bool{false, true} {
		name := "cancel"
		if closeConn {
			name = "close"
		}
		t.Run(name, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := &Conn{ctx: parent}
			entered, unblock := make(chan struct{}), make(chan struct{})
			var first sync.Once
			svc := rpc.NewService(1, func(context.Context, []byte) ([]byte, error) {
				first.Do(func() { close(entered) })
				<-unblock
				return nil, nil
			}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, QueueTimeout: time.Hour}, nil)
			defer func() { close(unblock); svc.Stop() }()
			if err := svc.Enqueue(rpc.Request{}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("handler did not start")
			}
			ctx, finish, err := c.TrackInbound(1, 0)
			if err != nil {
				t.Fatal(err)
			}
			released, finished := make(chan struct{}), make(chan struct{})
			reply := make(chan rpc.Response, 1)
			req := rpc.Request{Context: ctx, Finish: func() { finish(); close(finished) }, Payload: core.NewOwnedBuffer([]byte("queued"), func([]byte) { close(released) }), Reply: reply}
			if err := svc.Enqueue(req); err != nil {
				t.Fatal(err)
			}
			if closeConn {
				cancel()
				c.cancelInboundRequests()
			} else {
				c.CancelInbound(1)
			}
			select {
			case resp := <-reply:
				if !errors.Is(resp.Err, core.ErrCanceled) {
					t.Fatal(resp.Err)
				}
			case <-time.After(time.Second):
				t.Fatal("queued cancellation did not complete")
			}
			for _, ch := range []chan struct{}{released, finished} {
				select {
				case <-ch:
				case <-time.After(time.Second):
					t.Fatal("owner retained")
				}
			}
			c.inboundMu.Lock()
			retained := len(c.inboundRequests)
			c.inboundMu.Unlock()
			if retained != 0 {
				t.Fatal("tracking retained")
			}
			// The canceled admission's single queue slot must be reusable before the
			// blocked handler returns. Stop releases this replacement after unblocking.
			if err := svc.Enqueue(rpc.Request{}); err != nil {
				t.Fatalf("queue slot not reclaimed: %v", err)
			}
		})
	}
}

// CancelRunning controls executing work even when the request uses the direct
// queue hook; a mutation must not inherit cancellation from its connection.
func TestInboundQueueCancellationPreservesRunningPolicy(t *testing.T) {
	for _, read := range []bool{false, true} {
		name := "mutation"
		if read {
			name = "read"
		}
		t.Run(name, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := &Conn{ctx: parent}
			entered, inspect, observed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			svc := rpc.NewService(1, func(ctx context.Context, _ []byte) ([]byte, error) {
				close(entered)
				<-inspect
				if read {
					<-ctx.Done()
				}
				observed <- ctx.Err()
				return nil, nil
			}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, Timeout: time.Hour, CancelRunning: read}, nil)
			defer svc.Stop()
			ctx, finish, err := c.TrackInbound(1, 0)
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan struct{})
			if err := svc.Enqueue(rpc.Request{Context: ctx, Finish: func() { finish(); close(finished) }}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("handler not entered")
			}
			cancel()
			c.cancelInboundRequests()
			close(inspect)
			select {
			case err := <-observed:
				if read && !errors.Is(err, context.Canceled) {
					t.Fatalf("read cancellation=%v", err)
				}
				if !read && err != nil {
					t.Fatalf("mutation canceled: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("running request did not complete")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("owner not released")
			}
		})
	}
}
