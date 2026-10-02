package conn

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/sched"
)

func TestLaneBytesCountSuccessfulWritesOnly(t *testing.T) {
	for _, fail := range []bool{false, true} {
		observer := &recordingObserver{}
		var raw net.Conn = newDeadlineConn()
		if fail {
			raw = &laneFailingConn{newDeadlineConn()}
		}
		c := New(raw, Config{Limits: testLimits(), Observer: observer}, nil)
		out := Outbound{Kind: core.FrameKindRPCBudgetRequest, Priority: core.PriorityRaft, Payload: core.CopyOwnedBuffer([]byte("abc"))}
		err := c.writeOutbound(out)
		out.Payload.Release()
		if (err != nil) != fail {
			t.Fatalf("write error = %v, fail=%v", err, fail)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var buffers net.Buffers
		_, _, _ = c.writeOutboundBatch([]sched.Item{{Bytes: 3, Value: Outbound{
			Kind: core.FrameKindRPCRequest, Priority: core.PriorityRaft,
			Payload: core.CopyOwnedBuffer([]byte("xyz")), writeCtx: ctx,
		}}}, nil, nil, &buffers)
		var bytes int
		for _, e := range observer.snapshot() {
			if e.Name == "sent_bytes" {
				if e.Priority != core.PriorityRaft {
					t.Fatalf("lost lane: %+v", e)
				}
				bytes += e.Bytes
			}
		}
		want := 3
		if fail {
			want = 0
		}
		if bytes != want {
			t.Fatalf("counted failed/canceled write: bytes=%d want=%d", bytes, want)
		}
	}
}

type laneFailingConn struct{ *deadlineConn }

func (*laneFailingConn) Write([]byte) (int, error) { return 0, errors.New("test write failure") }
