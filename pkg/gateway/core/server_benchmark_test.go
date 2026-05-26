package core_test

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	benchmarkListenerName      = "listener-a"
	benchmarkProtocolName      = "benchproto"
	sendDispatchBenchmarkBurst = 64
)

func TestServerIdleMonitorUsesSharedWorker(t *testing.T) {
	const sessions = 32

	withoutIdleMonitor := measureGatewaySessionGoroutines(t, sessions, -1)
	withIdleMonitor := measureGatewaySessionGoroutines(t, sessions, 3*time.Minute)

	if withIdleMonitor > withoutIdleMonitor+8 {
		t.Fatalf("idle monitor appears to scale per session: disabled added %d goroutines, enabled added %d", withoutIdleMonitor, withIdleMonitor)
	}
}

func TestServerAsyncSendDispatchUsesBoundedWorkers(t *testing.T) {
	const frames = 128

	release := make(chan struct{})
	handler := &blockingAsyncGatewayHandler{release: release}
	proto := &benchmarkGatewayProtocol{
		name: benchmarkProtocolName,
		frame: &frame.SendPacket{
			ClientSeq:   1,
			ClientMsgNo: "bench-client-message",
			ChannelID:   "bench-channel",
			ChannelType: 2,
			Payload:     make([]byte, 128),
		},
		ownsDecodedFrames: true,
	}
	srv, transportFactory := newBenchmarkGatewayServer(t, handler, proto, gateway.SessionOptions{
		IdleTimeout: -1,
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	base := runtime.NumGoroutine()
	conn := transportFactory.MustOpen(benchmarkListenerName, 1)

	for i := 0; i < frames; i++ {
		if err := conn.EmitData([]byte("x")); err != nil {
			t.Fatalf("emit data failed: %v", err)
		}
	}
	waitForTesting(t, func() bool {
		return handler.started.Load() > 0
	})
	time.Sleep(20 * time.Millisecond)

	added := runtime.NumGoroutine() - base
	if max := runtime.GOMAXPROCS(0) + 16; added > max {
		close(release)
		_ = srv.Stop()
		t.Fatalf("async dispatch appears to spawn per frame: added %d goroutines for %d frames, max %d", added, frames, max)
	}

	close(release)
	waitForTesting(t, func() bool {
		return handler.processed.Load() == frames
	})
	if err := srv.Stop(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
}

func BenchmarkServerOpenIdleSessionBatch(b *testing.B) {
	const sessionsPerBatch = 1024

	for _, tc := range []struct {
		name        string
		idleTimeout time.Duration
	}{
		{name: "idle_monitor_disabled", idleTimeout: -1},
		{name: "idle_monitor_enabled", idleTimeout: 3 * time.Minute},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(sessionsPerBatch, "sessions/op")

			for i := 0; i < b.N; i++ {
				b.StopTimer()
				handler := &benchmarkGatewayHandler{}
				proto := &benchmarkGatewayProtocol{name: benchmarkProtocolName}
				srv, transportFactory := newBenchmarkGatewayServer(b, handler, proto, gateway.SessionOptions{
					IdleTimeout: tc.idleTimeout,
				})
				if err := srv.Start(); err != nil {
					b.Fatalf("start failed: %v", err)
				}
				b.StartTimer()

				baseConnID := uint64(i*sessionsPerBatch + 1)
				for j := 0; j < sessionsPerBatch; j++ {
					transportFactory.MustOpen(benchmarkListenerName, baseConnID+uint64(j))
				}
				b.StopTimer()
				if err := srv.Stop(); err != nil {
					b.Fatalf("stop failed: %v", err)
				}
				b.StartTimer()
			}
		})
	}
}

func BenchmarkServerSendDispatch(b *testing.B) {
	b.StopTimer()
	handler := &benchmarkGatewayHandler{}
	proto := &benchmarkGatewayProtocol{
		name: benchmarkProtocolName,
		frame: &frame.SendPacket{
			ClientSeq:   1,
			ClientMsgNo: "bench-client-message",
			ChannelID:   "bench-channel",
			ChannelType: 2,
			Payload:     make([]byte, 128),
		},
		ownsDecodedFrames: true,
	}
	srv, transportFactory := newBenchmarkGatewayServer(b, handler, proto, gateway.SessionOptions{
		IdleTimeout: -1,
	})
	if err := srv.Start(); err != nil {
		b.Fatalf("start failed: %v", err)
	}

	conn := transportFactory.MustOpen(benchmarkListenerName, 1)
	payload := []byte("x")

	b.ReportAllocs()
	b.StartTimer()
	for sent := 0; sent < b.N; {
		// Keep a single-channel burst below the minimum shard capacity so the
		// benchmark measures dispatch cost instead of queue-overflow closure.
		burst := min(sendDispatchBenchmarkBurst, b.N-sent)
		handler.expectFrames(burst)
		for i := 0; i < burst; i++ {
			if err := conn.EmitData(payload); err != nil {
				b.Fatalf("emit data failed: %v", err)
			}
		}
		handler.waitFrames()
		sent += burst
	}
	b.StopTimer()
	if err := srv.Stop(); err != nil {
		b.Fatalf("stop failed: %v", err)
	}
}

func measureGatewaySessionGoroutines(t *testing.T, sessions int, idleTimeout time.Duration) int {
	t.Helper()

	base := runtime.NumGoroutine()
	handler := &benchmarkGatewayHandler{}
	proto := &benchmarkGatewayProtocol{name: benchmarkProtocolName}
	srv, transportFactory := newBenchmarkGatewayServer(t, handler, proto, gateway.SessionOptions{
		IdleTimeout: idleTimeout,
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	for i := 0; i < sessions; i++ {
		transportFactory.MustOpen(benchmarkListenerName, uint64(i+1))
	}
	waitForTesting(t, func() bool {
		return srv.SessionSummary().GatewaySessions == sessions
	})
	added := runtime.NumGoroutine() - base
	if err := srv.Stop(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	waitForTesting(t, func() bool {
		return runtime.NumGoroutine() <= base+4
	})
	return added
}

func newBenchmarkGatewayServer(b testingHelper, handler gateway.Handler, proto *benchmarkGatewayProtocol, sessOpts gateway.SessionOptions) (*core.Server, *testkit.FakeTransportFactory) {
	b.Helper()

	transportFactory := testkit.NewFakeTransportFactory("fake-transport")
	registry := core.NewRegistry()
	if err := registry.RegisterTransport(transportFactory); err != nil {
		b.Fatalf("register transport failed: %v", err)
	}
	if err := registry.RegisterProtocol(proto); err != nil {
		b.Fatalf("register protocol failed: %v", err)
	}

	srv, err := core.NewServer(registry, &gateway.Options{
		Handler:        handler,
		DefaultSession: sessOpts,
		Listeners: []gateway.ListenerOptions{{
			Name:      benchmarkListenerName,
			Network:   "tcp",
			Address:   "127.0.0.1:9000",
			Transport: transportFactory.Name(),
			Protocol:  proto.Name(),
		}},
	})
	if err != nil {
		b.Fatalf("new server failed: %v", err)
	}
	return srv, transportFactory
}

type benchmarkGatewayProtocol struct {
	name              string
	frame             frame.Frame
	frames            []frame.Frame
	ownsDecodedFrames bool
}

func (p *benchmarkGatewayProtocol) Name() string {
	return p.name
}

func (p *benchmarkGatewayProtocol) OwnsDecodedFrames() bool {
	return p != nil && p.ownsDecodedFrames
}

func (p *benchmarkGatewayProtocol) Decode(_ session.Session, in []byte) ([]frame.Frame, int, error) {
	if p.frame == nil || len(in) == 0 {
		return nil, 0, nil
	}
	if len(p.frames) == 0 {
		p.frames = make([]frame.Frame, 1)
	}
	p.frames[0] = p.frame
	return p.frames, len(in), nil
}

func (p *benchmarkGatewayProtocol) Encode(session.Session, frame.Frame, session.OutboundMeta) ([]byte, error) {
	return nil, nil
}

func (p *benchmarkGatewayProtocol) OnOpen(session.Session) error {
	return nil
}

func (p *benchmarkGatewayProtocol) OnClose(session.Session) error {
	return nil
}

type benchmarkGatewayHandler struct {
	frameWG sync.WaitGroup
	frames  atomic.Uint64
}

func (h *benchmarkGatewayHandler) expectFrames(n int) {
	h.frameWG.Add(n)
}

func (h *benchmarkGatewayHandler) waitFrames() {
	h.frameWG.Wait()
}

func (h *benchmarkGatewayHandler) OnListenerError(string, error) {}

func (h *benchmarkGatewayHandler) OnSessionOpen(gateway.Context) error {
	return nil
}

func (h *benchmarkGatewayHandler) OnFrame(gateway.Context, frame.Frame) error {
	h.frames.Add(1)
	h.frameWG.Done()
	return nil
}

func (h *benchmarkGatewayHandler) OnSessionClose(gateway.Context) error {
	return nil
}

func (h *benchmarkGatewayHandler) OnSessionError(gateway.Context, error) {}

type blockingAsyncGatewayHandler struct {
	started   atomic.Uint64
	processed atomic.Uint64
	release   <-chan struct{}
}

func (h *blockingAsyncGatewayHandler) OnListenerError(string, error) {}

func (h *blockingAsyncGatewayHandler) OnSessionOpen(gateway.Context) error {
	return nil
}

func (h *blockingAsyncGatewayHandler) OnFrame(gateway.Context, frame.Frame) error {
	h.started.Add(1)
	<-h.release
	h.processed.Add(1)
	return nil
}

func (h *blockingAsyncGatewayHandler) OnSessionClose(gateway.Context) error {
	return nil
}

func (h *blockingAsyncGatewayHandler) OnSessionError(gateway.Context, error) {}

type testingHelper interface {
	Helper()
	Fatalf(string, ...any)
}

func waitForTesting(t testingHelper, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met before timeout")
}
