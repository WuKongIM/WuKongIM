package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transportBenchmarkHarness struct {
	server     *Server
	raftClient *Client
	rpcClient  *Client
	raftSent   atomic.Uint64
	raftRecv   atomic.Uint64
}

func TestNewTransportBenchmarkHarnessStartsServerAndClients(t *testing.T) {
	h := newTransportBenchmarkHarness(t)
	defer h.Close()

	if h.server.Listener() == nil {
		t.Fatal("expected active listener")
	}
	if h.raftClient == nil {
		t.Fatal("expected raft client to be initialized")
	}
	if h.rpcClient == nil {
		t.Fatal("expected rpc client to be initialized")
	}
}

func TestTransportBenchmarkHarnessSmoke(t *testing.T) {
	h := newTransportBenchmarkHarness(t)
	defer h.Close()

	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[:8], uint64(time.Now().UnixNano()))
	binary.BigEndian.PutUint64(payload[8:], 1)

	if err := h.raftClient.Send(transportStressNodeID, 0, transportStressMsgType, payload); err != nil {
		t.Fatalf("raftClient.Send() error = %v", err)
	}
	resp, err := h.rpcClient.RPC(context.Background(), transportStressNodeID, 0, []byte("ping"))
	if err != nil {
		t.Fatalf("rpcClient.RPC() error = %v", err)
	}
	if string(resp) != "ok" {
		t.Fatalf("rpcClient.RPC() resp = %q, want %q", resp, "ok")
	}

	requireEventually(t, func() bool {
		return h.raftRecv.Load() == 1
	})
}

func BenchmarkTransportSend(b *testing.B) {
	for _, size := range []int{16, 256} {
		b.Run(fmt.Sprintf("payload=%dB", size), func(b *testing.B) {
			h := newTransportBenchmarkHarness(b)
			defer h.Close()
			payload := bytes.Repeat([]byte("a"), size)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := waitForTransportBenchmarkCapacity(context.Background(), h, 128); err != nil {
					b.Fatal(err)
				}
				if err := h.raftClient.Send(transportStressNodeID, 0, transportStressMsgType, payload); err != nil {
					b.Fatal(err)
				}
				h.raftSent.Add(1)
			}
			b.StopTimer()
			if err := waitForTransportBenchmarkDrain(context.Background(), h); err != nil {
				b.Fatal(err)
			}
		})
	}
}

func BenchmarkTransportRPC(b *testing.B) {
	for _, size := range []int{16, 256} {
		b.Run(fmt.Sprintf("payload=%dB", size), func(b *testing.B) {
			h := newTransportBenchmarkHarness(b)
			defer h.Close()
			payload := bytes.Repeat([]byte("r"), size)
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := h.rpcClient.RPC(ctx, transportStressNodeID, 0, payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkTransportSendParallel(b *testing.B) {
	for _, size := range []int{16, 256} {
		b.Run(fmt.Sprintf("payload=%dB", size), func(b *testing.B) {
			h := newTransportBenchmarkHarness(b)
			defer h.Close()
			payload := bytes.Repeat([]byte("a"), size)

			b.ReportAllocs()
			b.ResetTimer()
			runTransportSendParallel(b, h, payload)
		})
	}
}

func BenchmarkTransportRPCParallel(b *testing.B) {
	for _, size := range []int{16, 256} {
		b.Run(fmt.Sprintf("payload=%dB", size), func(b *testing.B) {
			h := newTransportBenchmarkHarness(b)
			defer h.Close()
			payload := bytes.Repeat([]byte("r"), size)

			b.ReportAllocs()
			b.ResetTimer()
			runTransportRPCParallel(b, h, payload)
		})
	}
}

func runTransportSendParallel(b *testing.B, h *transportBenchmarkHarness, payload []byte) {
	b.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		errMu  sync.Mutex
		runErr error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if runErr == nil {
			runErr = err
			cancel()
		}
		errMu.Unlock()
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ctx.Err() != nil {
				return
			}
			if err := waitForTransportBenchmarkCapacity(ctx, h, 128); err != nil {
				recordErr(err)
				return
			}
			if err := h.raftClient.Send(transportStressNodeID, 0, transportStressMsgType, payload); err != nil {
				recordErr(err)
				return
			}
			h.raftSent.Add(1)
		}
	})

	b.StopTimer()
	if runErr == nil {
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer drainCancel()
		if err := waitForTransportBenchmarkDrain(drainCtx, h); err != nil {
			runErr = err
		}
	}
	if runErr != nil {
		b.Fatal(runErr)
	}
}

func runTransportRPCParallel(b *testing.B, h *transportBenchmarkHarness, payload []byte) {
	b.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		errMu  sync.Mutex
		runErr error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if runErr == nil {
			runErr = err
			cancel()
		}
		errMu.Unlock()
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ctx.Err() != nil {
				return
			}
			if _, err := h.rpcClient.RPC(ctx, transportStressNodeID, 0, payload); err != nil {
				recordErr(err)
				return
			}
		}
	})

	b.StopTimer()
	if runErr != nil {
		b.Fatal(runErr)
	}
}

func newTransportBenchmarkHarness(tb testing.TB) *transportBenchmarkHarness {
	tb.Helper()

	server := NewServer()
	h := &transportBenchmarkHarness{server: server}
	server.Handle(transportStressMsgType, func(body []byte) {
		h.raftRecv.Add(1)
	})
	server.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		return []byte("ok"), nil
	})
	if err := server.Start("127.0.0.1:0"); err != nil {
		tb.Fatalf("server.Start() error = %v", err)
	}

	discovery := staticDiscovery{addrs: map[NodeID]string{
		transportStressNodeID: server.Listener().Addr().String(),
	}}
	raftPool := NewPool(PoolConfig{
		Discovery:   discovery,
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{1024, 1024, 1024},
		DefaultPri:  PriorityRaft,
	})
	rpcPool := NewPool(PoolConfig{
		Discovery:   discovery,
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{1024, 1024, 1024},
		DefaultPri:  PriorityRPC,
	})

	h.raftClient = NewClient(raftPool)
	h.rpcClient = NewClient(rpcPool)
	return h
}

func (h *transportBenchmarkHarness) Close() {
	if h == nil {
		return
	}
	if h.raftClient != nil {
		h.raftClient.Stop()
	}
	if h.rpcClient != nil {
		h.rpcClient.Stop()
	}
	if h.server != nil {
		h.server.Stop()
	}
}

func waitForTransportBenchmarkCapacity(ctx context.Context, h *transportBenchmarkHarness, maxInflight uint64) error {
	if maxInflight == 0 {
		return nil
	}
	for {
		sent := h.raftSent.Load()
		recv := h.raftRecv.Load()
		if sent <= recv || sent-recv < maxInflight {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Microsecond):
		}
	}
}

func waitForTransportBenchmarkDrain(ctx context.Context, h *transportBenchmarkHarness) error {
	for {
		if h.raftRecv.Load() >= h.raftSent.Load() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Microsecond):
		}
	}
}
