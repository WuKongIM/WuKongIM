//go:build integration

package conn

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/sched"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

func TestCoalescingInterruptedByUrgentOrShutdown(t *testing.T) {
	for _, stop := range []bool{false, true} {
		limits := testLimits()
		limits.WriteBatchMaxWait = time.Hour
		c := New(newDeadlineConn(), Config{Limits: limits}, nil)
		entered := make(chan struct{})
		old := waitForWriteBatch
		waitForWriteBatch = func(ctx context.Context, urgent <-chan struct{}, delay time.Duration) {
			close(entered)
			waitForBatch(ctx, urgent, delay)
		}
		done := make(chan []sched.Item, 1)
		go func() {
			batch, _ := c.collectAvailableWriteItems([]sched.Item{{Priority: core.PriorityRPC}}, nil)
			done <- batch
		}()
		<-entered
		if stop {
			c.shutdown(core.ErrStopped)
		} else if err := c.scheduler.Enqueue(context.Background(), sched.Item{Priority: core.PriorityRaft}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("coalescing ignored urgent work/shutdown")
		}
		waitForWriteBatch = old
		c.Close(nil)
	}
}

func TestLateResponseDoesNotCopyPayload(t *testing.T) {
	c := New(newDeadlineConn(), Config{Limits: testLimits()}, nil)
	payload := make([]byte, 1<<20)
	allocs := testing.AllocsPerRun(100, func() {
		c.handleRPCResponse(wire.Frame{Header: wire.Header{RequestID: 99}, Body: core.NewOwnedBuffer(payload, nil)})
	})
	if allocs != 0 {
		t.Fatalf("late response allocations=%v, want zero", allocs)
	}
}
