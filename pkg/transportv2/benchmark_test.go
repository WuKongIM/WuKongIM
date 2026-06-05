package transportv2_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/testkit"
)

func BenchmarkTransportV2RPC(b *testing.B) {
	benchmarkTransportV2RPC(b, bytes.Repeat([]byte("r"), 64))
}

func BenchmarkTransportV2RPCPayloadSizes(b *testing.B) {
	cases := []struct {
		name string
		size int
	}{
		{name: "64B", size: 64},
		{name: "1KiB", size: 1 << 10},
		{name: "64KiB", size: 64 << 10},
	}
	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			benchmarkTransportV2RPC(b, bytes.Repeat([]byte("p"), tc.size))
		})
	}
}

func BenchmarkTransportV2RPCParallel(b *testing.B) {
	h := testkit.NewHarness(b, func(ctx context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})
	defer h.Close()

	payload := bytes.Repeat([]byte("p"), 256)
	ctx := context.Background()
	if _, err := h.Client.Call(ctx, testkit.ServerNodeID, 0, transportv2.PriorityRPC, testkit.ServiceID, payload); err != nil {
		b.Fatalf("warm-up Call() error = %v", err)
	}

	var shardKeys atomic.Uint64
	var errCount atomic.Int64
	var firstErr atomic.Value
	var once sync.Once

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			shardKey := shardKeys.Add(1)
			if _, err := h.Client.Call(ctx, testkit.ServerNodeID, shardKey, transportv2.PriorityRPC, testkit.ServiceID, payload); err != nil {
				once.Do(func() {
					firstErr.Store(err.Error())
				})
				errCount.Add(1)
				return
			}
		}
	})
	b.StopTimer()

	if errCount.Load() > 0 {
		b.Fatalf("Call() errors = %d, first = %v", errCount.Load(), firstErr.Load())
	}
}

func BenchmarkTransportV2SendWithBackpressure(b *testing.B) {
	h := testkit.NewHarness(b, func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, nil
	})
	defer h.Close()

	payload := bytes.Repeat([]byte("s"), 256)
	ctx := context.Background()
	if err := h.Client.Send(ctx, testkit.ServerNodeID, 0, transportv2.PriorityControl, testkit.ServiceID, payload); err != nil {
		b.Fatalf("warm-up Send() error = %v", err)
	}

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sendWithBackpressure(b, h.Client, ctx, uint64(i), payload)
	}
}

func benchmarkTransportV2RPC(b *testing.B, payload []byte) {
	b.Helper()
	h := testkit.NewHarness(b, func(ctx context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})
	defer h.Close()

	ctx := context.Background()
	if _, err := h.Client.Call(ctx, testkit.ServerNodeID, 0, transportv2.PriorityRPC, testkit.ServiceID, payload); err != nil {
		b.Fatalf("warm-up Call() error = %v", err)
	}

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.Client.Call(ctx, testkit.ServerNodeID, uint64(i), transportv2.PriorityRPC, testkit.ServiceID, payload); err != nil {
			b.Fatalf("Call() error = %v", err)
		}
	}
}

func sendWithBackpressure(b *testing.B, client *transportv2.Client, ctx context.Context, shardKey uint64, payload []byte) {
	b.Helper()
	for {
		err := client.Send(ctx, testkit.ServerNodeID, shardKey, transportv2.PriorityControl, testkit.ServiceID, payload)
		if err == nil {
			return
		}
		if !errors.Is(err, transportv2.ErrQueueFull) {
			b.Fatalf("Send() error = %v", err)
		}
		runtime.Gosched()
	}
}
