package conn

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

func TestRPCTraceIdentityBindingAndCounterOverflow(t *testing.T) {
	c := New(nil, Config{Limits: core.Limits{MaxFrameBodyBytes: 4096, MaxQueuedItemsPerConn: 8, MaxQueuedBytesPerConn: 4096, MaxBatchFrames: 8, MaxBatchBytes: 4096}}, nil)
	var token [16]byte
	token[0] = 1
	if _, ok := c.TraceIdentity(1<<63 | 1); ok {
		t.Fatal("unnegotiated sample bit accepted")
	}
	if !c.AcceptRPCTrace(token) || !c.AcceptRPCTrace(token) {
		t.Fatal("stable token rejected")
	}
	other := token
	other[1] = 2
	if c.AcceptRPCTrace(other) {
		t.Fatal("connection token rebound")
	}
	if got, ok := c.TraceIdentity(1<<63 | 1); !ok || got != token {
		t.Fatal("negotiated identity missing")
	}
	if _, ok := c.TraceIdentity(1); ok {
		t.Fatal("unsampled request admitted")
	}
	c.nextRequestID.Store(1<<63 - 1)
	if _, err := c.Call(context.Background(), Outbound{}); !errors.Is(err, core.ErrInvalidFrame) {
		t.Fatalf("counter overflow=%v", err)
	}
	if c.pending.Len() != 0 {
		t.Fatal("overflow entered pending table")
	}
}

func TestRPCTraceExhaustedCounterNeverWraps(t *testing.T) {
	c := New(nil, Config{Limits: core.Limits{MaxFrameBodyBytes: 4096, MaxQueuedItemsPerConn: 8, MaxQueuedBytesPerConn: 4096, MaxBatchFrames: 8, MaxBatchBytes: 4096}}, nil)
	c.nextRequestID.Store(^uint64(0))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 2; i++ {
		if _, err := c.Call(ctx, Outbound{}); !errors.Is(err, core.ErrInvalidFrame) {
			t.Fatalf("exhausted attempt %d: %v", i, err)
		}
	}
}

func TestRPCTraceCapabilityEnvelopeIsClosed(t *testing.T) {
	token := [16]byte{1}
	valid := append([]byte("wukongim/rpc-trace/1"), token[:]...)
	for _, payload := range [][]byte{nil, valid[:len(valid)-1], append(append([]byte(nil), valid...), 0), append([]byte("wukongim/rpc-trace/2"), token[:]...), append([]byte("wukongim/rpc-trace/1"), make([]byte, 16)...)} {
		c := New(nil, Config{}, nil)
		if c.AcceptRPCTracePayload(payload) {
			t.Fatal("malformed trace capability accepted")
		}
	}
	c := New(nil, Config{}, nil)
	if !c.AcceptRPCTracePayload(valid) {
		t.Fatal("valid trace capability rejected")
	}
}
