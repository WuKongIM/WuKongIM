package transportv2_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/testkit"
)

func BenchmarkTransportV2RPC(b *testing.B) {
	benchmarkTransportV2RPC(b, bytes.Repeat([]byte("r"), 64))
}

func BenchmarkTransportV2RPCOwned(b *testing.B) {
	benchmarkTransportV2RPCOwned(b, bytes.Repeat([]byte("r"), 64))
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

func BenchmarkTransportV2RPCPoolSizes(b *testing.B) {
	cases := []struct {
		name     string
		poolSize int
	}{
		{name: "Pool1", poolSize: 1},
		{name: "Pool4", poolSize: 4},
		{name: "Pool8", poolSize: 8},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			payload := bytes.Repeat([]byte("c"), 256)
			h := newBenchmarkHarness(b, benchmarkHarnessConfig{
				poolSize: tc.poolSize,
				services: []benchmarkServiceRegistration{
					{
						serviceID: testkit.ServiceID,
						handler: func(context.Context, []byte) ([]byte, error) {
							return payload, nil
						},
						opts: benchmarkServiceOptions(max(1, tc.poolSize*2)),
					},
				},
			})
			defer h.Close()

			benchmarkTransportV2RPCWithHarness(b, h, testkit.ServiceID, payload)
		})
	}
}

func BenchmarkTransportV2RPCMultiServiceParallel(b *testing.B) {
	payload := bytes.Repeat([]byte("m"), 256)
	serviceIDs := []uint16{7, 8, 9, 10}
	services := make([]benchmarkServiceRegistration, 0, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		services = append(services, benchmarkServiceRegistration{
			serviceID: serviceID,
			handler: func(context.Context, []byte) ([]byte, error) {
				return payload, nil
			},
			opts: benchmarkServiceOptions(2),
		})
	}

	h := newBenchmarkHarness(b, benchmarkHarnessConfig{
		poolSize: 4,
		services: services,
	})
	defer h.Close()

	ctx := context.Background()
	if _, err := h.Client.Call(ctx, testkit.ServerNodeID, 0, transportv2.PriorityRPC, serviceIDs[0], payload); err != nil {
		b.Fatalf("warm-up Call() error = %v", err)
	}

	var calls atomic.Uint64
	var errCount atomic.Int64
	var firstErr atomic.Value
	var once sync.Once

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			call := calls.Add(1)
			serviceID := serviceIDs[int(call)%len(serviceIDs)]
			if _, err := h.Client.Call(ctx, testkit.ServerNodeID, call, transportv2.PriorityRPC, serviceID, payload); err != nil {
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

func BenchmarkTransportV2SendOwnedWithBackpressure(b *testing.B) {
	h := testkit.NewHarness(b, func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, nil
	})
	defer h.Close()

	payload := bytes.Repeat([]byte("s"), 256)
	ctx := context.Background()
	if err := h.Client.SendOwned(ctx, testkit.ServerNodeID, 0, transportv2.PriorityControl, testkit.ServiceID, transportv2.NewOwnedBuffer(payload, nil)); err != nil {
		b.Fatalf("warm-up SendOwned() error = %v", err)
	}

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sendOwnedWithBackpressure(b, h.Client, ctx, uint64(i), payload)
	}
}

func BenchmarkTransportV2SendParallelWithBackpressure(b *testing.B) {
	payload := bytes.Repeat([]byte("p"), 256)
	var handled atomic.Int64
	h := newBenchmarkHarness(b, benchmarkHarnessConfig{
		poolSize: 4,
		services: []benchmarkServiceRegistration{
			{
				serviceID: testkit.ServiceID,
				handler: func(context.Context, []byte) ([]byte, error) {
					handled.Add(1)
					return nil, nil
				},
				opts: benchmarkServiceOptions(max(1, runtime.GOMAXPROCS(0))),
			},
		},
	})
	defer h.Close()

	ctx := context.Background()
	if err := h.Client.Send(ctx, testkit.ServerNodeID, 0, transportv2.PriorityControl, testkit.ServiceID, payload); err != nil {
		b.Fatalf("warm-up Send() error = %v", err)
	}
	benchmarkWaitAtomicAtLeast(b, &handled, 1)

	var shardKeys atomic.Uint64
	var accepted atomic.Int64
	var errCount atomic.Int64
	var firstErr atomic.Value
	var once sync.Once

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			shardKey := shardKeys.Add(1)
			if err := sendWithBackpressureResult(h.Client, ctx, shardKey, payload); err != nil {
				once.Do(func() {
					firstErr.Store(err.Error())
				})
				errCount.Add(1)
				return
			}
			accepted.Add(1)
		}
	})
	b.StopTimer()

	if errCount.Load() > 0 {
		b.Fatalf("Send() errors = %d, first = %v", errCount.Load(), firstErr.Load())
	}
	// Send is one-way: successful client admission does not imply the server handler accepted
	// every frame under service pressure.
	b.ReportMetric(float64(accepted.Load()), "accepted")
	b.ReportMetric(float64(handled.Load()), "handled")
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

func benchmarkTransportV2RPCOwned(b *testing.B, payload []byte) {
	b.Helper()
	h := testkit.NewHarness(b, func(ctx context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})
	defer h.Close()

	ctx := context.Background()
	if _, err := h.Client.CallOwned(ctx, testkit.ServerNodeID, 0, transportv2.PriorityRPC, testkit.ServiceID, transportv2.NewOwnedBuffer(payload, nil)); err != nil {
		b.Fatalf("warm-up CallOwned() error = %v", err)
	}

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.Client.CallOwned(ctx, testkit.ServerNodeID, uint64(i), transportv2.PriorityRPC, testkit.ServiceID, transportv2.NewOwnedBuffer(payload, nil)); err != nil {
			b.Fatalf("CallOwned() error = %v", err)
		}
	}
}

func benchmarkTransportV2RPCWithHarness(b *testing.B, h *testkit.Harness, serviceID uint16, payload []byte) {
	b.Helper()
	ctx := context.Background()
	if _, err := h.Client.Call(ctx, testkit.ServerNodeID, 0, transportv2.PriorityRPC, serviceID, payload); err != nil {
		b.Fatalf("warm-up Call() error = %v", err)
	}

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.Client.Call(ctx, testkit.ServerNodeID, uint64(i), transportv2.PriorityRPC, serviceID, payload); err != nil {
			b.Fatalf("Call() error = %v", err)
		}
	}
}

func sendWithBackpressure(b *testing.B, client *transportv2.Client, ctx context.Context, shardKey uint64, payload []byte) {
	b.Helper()
	if err := sendWithBackpressureResult(client, ctx, shardKey, payload); err != nil {
		b.Fatalf("Send() error = %v", err)
	}
}

func sendOwnedWithBackpressure(b *testing.B, client *transportv2.Client, ctx context.Context, shardKey uint64, payload []byte) {
	b.Helper()
	for {
		err := client.SendOwned(ctx, testkit.ServerNodeID, shardKey, transportv2.PriorityControl, testkit.ServiceID, transportv2.NewOwnedBuffer(payload, nil))
		if err == nil {
			return
		}
		if !errors.Is(err, transportv2.ErrQueueFull) {
			b.Fatalf("SendOwned() error = %v", err)
		}
		runtime.Gosched()
	}
}

func sendWithBackpressureResult(client *transportv2.Client, ctx context.Context, shardKey uint64, payload []byte) error {
	for {
		err := client.Send(ctx, testkit.ServerNodeID, shardKey, transportv2.PriorityControl, testkit.ServiceID, payload)
		if err == nil {
			return nil
		}
		if !errors.Is(err, transportv2.ErrQueueFull) {
			return err
		}
		runtime.Gosched()
	}
}

type benchmarkServiceRegistration struct {
	serviceID uint16
	handler   transportv2.Handler
	opts      transportv2.ServiceOptions
}

type benchmarkHarnessConfig struct {
	poolSize int
	limits   transportv2.Limits
	services []benchmarkServiceRegistration
}

func newBenchmarkHarness(b *testing.B, cfg benchmarkHarnessConfig) *testkit.Harness {
	b.Helper()
	limits := cfg.limits
	if limits == (transportv2.Limits{}) {
		limits = transportv2.DefaultLimits()
	}
	services := cfg.services
	if len(services) == 0 {
		services = []benchmarkServiceRegistration{
			{
				serviceID: testkit.ServiceID,
				handler: func(context.Context, []byte) ([]byte, error) {
					return nil, nil
				},
				opts: benchmarkServiceOptions(4),
			},
		}
	}

	server, err := transportv2.NewServer(transportv2.ServerConfig{
		NodeID: testkit.ServerNodeID,
		Limits: limits,
	})
	if err != nil {
		b.Fatalf("NewServer() error = %v", err)
	}
	for _, service := range services {
		handler := service.handler
		if handler == nil {
			handler = func(context.Context, []byte) ([]byte, error) {
				return nil, nil
			}
		}
		opts := service.opts
		if opts == (transportv2.ServiceOptions{}) {
			opts = benchmarkServiceOptions(4)
		}
		if err := server.Handle(service.serviceID, handler, opts); err != nil {
			server.Stop()
			b.Fatalf("Handle(service %d) error = %v", service.serviceID, err)
		}
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		server.Stop()
		b.Fatalf("ListenAndServe() error = %v", err)
	}

	poolSize := cfg.poolSize
	if poolSize <= 0 {
		poolSize = transportv2.DefaultPoolSize
	}
	client, err := transportv2.NewClient(transportv2.ClientConfig{
		NodeID:    testkit.ClientNodeID,
		Discovery: testkit.StaticDiscovery{testkit.ServerNodeID: server.Addr()},
		PoolSize:  poolSize,
		Limits:    limits,
	})
	if err != nil {
		server.Stop()
		b.Fatalf("NewClient() error = %v", err)
	}

	return &testkit.Harness{Server: server, Client: client}
}

func benchmarkServiceOptions(concurrency int) transportv2.ServiceOptions {
	if concurrency <= 0 {
		concurrency = 1
	}
	return transportv2.ServiceOptions{
		Concurrency:   concurrency,
		QueueSize:     4096,
		MaxQueueBytes: 16 << 20,
	}
}

func benchmarkWaitAtomicAtLeast(b *testing.B, value *atomic.Int64, want int64) {
	b.Helper()
	deadline := time.After(5 * time.Second)
	for value.Load() < want {
		select {
		case <-deadline:
			b.Fatalf("timed out waiting for handled count %d, want at least %d", value.Load(), want)
		default:
			runtime.Gosched()
		}
	}
}
