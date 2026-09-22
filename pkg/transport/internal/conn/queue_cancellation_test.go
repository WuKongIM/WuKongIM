package conn

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Tests exercise actual inbound tracking, including a close that cancels its
// parent before walking the request table. No socket or wall-clock wait is needed.
func TestInboundQueueCancellation(t *testing.T) {
	for _, terminal := range []string{"cancel", "finish", "close", "stop", "cancel_before_watch", "parent_before_watch"} {
		t.Run(terminal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				c := &Conn{ctx: parent}
				ctx, finish, err := c.TrackInbound(1, 0)
				if err != nil {
					t.Fatal(err)
				}
				watch, ok := ctx.(interface{ WatchQueueCancellation(func()) func() bool })
				if !ok {
					t.Fatal("inbound queue cancellation hook missing")
				}
				if terminal == "cancel_before_watch" {
					c.CancelInbound(1)
				}
				if terminal == "parent_before_watch" {
					cancel()
				}
				var calls atomic.Int32
				stop := watch.WatchQueueCancellation(func() {
					if ctx.Err() != context.Canceled {
						t.Error("callback before context cancellation")
					}
					calls.Add(1)
				})
				if terminal == "stop" {
					if !stop() || stop() {
						t.Fatal("stop must detach exactly once")
					}
				}
				switch terminal {
				case "cancel", "cancel_before_watch", "stop":
					c.CancelInbound(1)
				case "finish":
					finish()
				case "close", "parent_before_watch":
					cancel()
					c.cancelInboundRequests()
				}
				synctest.Wait()
				want := int32(1)
				if terminal == "stop" {
					want = 0
				}
				if calls.Load() != want {
					t.Fatalf("callbacks=%d want=%d", calls.Load(), want)
				}
				if stop() {
					t.Fatal("terminal listener remained stoppable")
				}
				c.CancelInbound(1)
				finish()
				synctest.Wait()
				if calls.Load() != want {
					t.Fatal("callback repeated")
				}
				if len(c.inboundRequests) != 0 {
					t.Fatal("request retained after finish")
				}
			})
		})
	}
}

func TestInboundQueueCancellationStopRace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for i := 0; i < 200; i++ {
			parent, cancel := context.WithCancel(context.Background())
			c := &Conn{ctx: parent}
			ctx, finish, err := c.TrackInbound(1, 0)
			if err != nil {
				t.Fatal(err)
			}
			watch, ok := ctx.(interface{ WatchQueueCancellation(func()) func() bool })
			if !ok {
				t.Fatal("inbound queue cancellation hook missing")
			}
			var calls atomic.Int32
			stop := watch.WatchQueueCancellation(func() { calls.Add(1) })
			var wg sync.WaitGroup
			wg.Add(2)
			stopped := false
			go func() { defer wg.Done(); c.CancelInbound(1) }()
			go func() { defer wg.Done(); stopped = stop() }()
			wg.Wait()
			synctest.Wait()
			want := int32(1)
			if stopped {
				want = 0
			}
			if calls.Load() != want {
				t.Fatalf("stopped=%v callbacks=%d", stopped, calls.Load())
			}
			finish()
			cancel()
		}
	})
}

func TestInboundBudgetKeepsDeadlineContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type valueKey struct{}
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		c := &Conn{ctx: parent}
		ctx, finish, err := c.TrackInbound(1, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer finish()
		if _, ok := ctx.(interface{ WatchQueueCancellation(func()) func() bool }); ok {
			t.Fatal("budget context must use deadline cancellation")
		}
		child, stopChild := context.WithCancel(context.WithValue(ctx, valueKey{}, "kept"))
		defer stopChild()
		var calls atomic.Int32
		stop := context.AfterFunc(child, func() { calls.Add(1) })
		defer stop()
		<-child.Done()
		synctest.Wait()
		for _, current := range []context.Context{ctx, child} {
			if current.Err() != context.DeadlineExceeded || context.Cause(current) != context.DeadlineExceeded {
				t.Fatalf("deadline semantics: err=%v cause=%v", current.Err(), context.Cause(current))
			}
		}
		if child.Value(valueKey{}) != "kept" || calls.Load() != 1 {
			t.Fatal("standard child propagation lost")
		}
	})
}

// Repeated registrations and standard context children must remain independent
// of detaching the queue owner's single fast-path callback.
func TestInboundQueueCancellationContextCompatibility(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		c := &Conn{ctx: parent}
		ctx, finish, err := c.TrackInbound(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer finish()
		watch := ctx.(interface{ WatchQueueCancellation(func()) func() bool })
		var first, second, generic atomic.Int32
		stopFirst := watch.WatchQueueCancellation(func() { first.Add(1) })
		stopSecond := watch.WatchQueueCancellation(func() { second.Add(1) })
		stopGeneric := context.AfterFunc(ctx, func() { generic.Add(1) })
		defer stopGeneric()
		child, stopChild := context.WithCancel(ctx)
		defer stopChild()
		if !stopFirst() {
			t.Fatal("first listener could not detach")
		}
		c.CancelInbound(1)
		synctest.Wait()
		if first.Load() != 0 || second.Load() != 1 || generic.Load() != 1 {
			t.Fatalf("callbacks first=%d second=%d generic=%d", first.Load(), second.Load(), generic.Load())
		}
		if stopSecond() || stopFirst() {
			t.Fatal("terminal callback could still detach")
		}
		if child.Err() != context.Canceled || context.Cause(ctx) != context.Canceled {
			t.Fatal("standard cancellation semantics lost")
		}
	})
}
