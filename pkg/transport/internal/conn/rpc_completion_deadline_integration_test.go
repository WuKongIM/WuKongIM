//go:build integration

package conn

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

func TestRPCCompletionDeadlineAppliesToObservedAndUnobservedResponses(t *testing.T) {
	for _, observed := range []bool{false, true} {
		for _, timely := range []bool{false, true} {
			t.Run(map[bool]string{false: "plain", true: "observed"}[observed]+"/"+map[bool]string{false: "late", true: "timely"}[timely], func(t *testing.T) {
				admitted, notified, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var releaseOnce, notifiedOnce sync.Once
				releaseCaller := func() { releaseOnce.Do(func() { close(release) }) }
				var published time.Time
				observer := receiveBridgeObserver(func(event core.Event) {
					if event.Name == "scheduler_admission" && event.Result == "ok" {
						close(admitted)
						<-release
					}
					if event.Name == "pending_rpc" && event.Inflight == 0 {
						notifiedOnce.Do(func() { published = time.Now(); close(notified) })
					}
				})
				raw, peer := net.Pipe()
				c := New(raw, receiveBridgeConfig(observed, observer), nil)
				var trace *rpctrace.Trace
				if observed {
					c.AcceptRPCTrace([16]byte{10})
					trace, _ = receiveBridgeTrace(t)
				}
				c.Start()
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				deadline, _ := ctx.Deadline()
				defer func() { releaseCaller(); cancel(); peer.Close(); c.Close(nil) }()
				var released atomic.Int32
				done := receiveBridgeCall(c, ctx, trace, &released)
				waitClosed(t, admitted)
				header := receiveBridgeRequest(t, peer)
				if !timely {
					<-ctx.Done()
				}
				receiveBridgeReply(t, peer, header, 7, []byte{wire.ResponseOK, 'o', 'k'})
				waitClosed(t, notified)
				if timely != published.Before(deadline) {
					t.Fatal("controlled publication setup invalid")
				}
				<-ctx.Done()
				releaseCaller()
				got := receiveBridgeAwait(t, done)
				if timely && (got.err != nil || string(got.body) != "ok") || !timely && !errors.Is(got.err, context.DeadlineExceeded) {
					t.Fatalf("publication deadline decision changed: timely=%v error=%v", timely, got.err)
				}
				if trace != nil {
					receiveBridgeSpans(t, trace.Finish(core.RPCTraceResult(got.err)), timely)
				}
				peer.Close()
				c.Close(nil)
				if c.pending.Len() != 0 || released.Load() != 1 {
					t.Fatal("completion retained pending/payload owner")
				}
			})
		}
	}
}
