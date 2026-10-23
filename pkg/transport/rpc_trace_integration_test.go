//go:build integration

package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

type traceDiscovery string

func (d traceDiscovery) Resolve(NodeID) (string, error) { return string(d), nil }

type traceObserver struct {
	mu       sync.Mutex
	samples  []rpctrace.Sample
	queue    chan struct{}
	block    <-chan struct{}
	blocked  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func (o *traceObserver) ObserveTransport(e Event) {
	if e.Name == "service_admission" && e.Result == "ok" && e.Items > 0 && o.queue != nil {
		select {
		case o.queue <- struct{}{}:
		default:
		}
	}
	if e.RPCTrace == nil {
		return
	}
	if o.canceled != nil && e.RPCTrace.Side == rpctrace.SideServer && e.RPCTrace.Result == rpctrace.ResultCanceled {
		select {
		case o.canceled <- struct{}{}:
		default:
		}
	}
	if o.block != nil {
		o.once.Do(func() { close(o.blocked) })
		<-o.block
	}
	o.mu.Lock()
	o.samples = append(o.samples, e.RPCTrace.Clone())
	o.mu.Unlock()
}
func (o *traceObserver) snapshot() []rpctrace.Sample {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]rpctrace.Sample(nil), o.samples...)
}
func traceOptions() ServiceOptions {
	return ServiceOptions{Concurrency: 1, QueueSize: 32, MaxQueueBytes: 4096, Timeout: 3 * time.Second, QueueTimeout: 3 * time.Second}
}
func tracePair(t *testing.T, enabled bool, every uint32, opts ServiceOptions, handler Handler, obs *traceObserver) (*Client, *Server) {
	t.Helper()
	s, err := NewServer(ServerConfig{RPCTracing: enabled, Observer: obs})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Handle(1, handler, opts); err != nil {
		s.Stop()
		t.Fatal(err)
	}
	if err = s.ListenAndServe("127.0.0.1:0"); err != nil {
		s.Stop()
		t.Fatal(err)
	}
	c, err := NewClient(ClientConfig{RPCTraceEvery: every, RequestBudgets: true, PoolSize: 2, Discovery: traceDiscovery(s.Addr()), Observer: obs})
	if err != nil {
		s.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Stop(); s.Stop() })
	return c, s
}
func traceCall(ctx context.Context, c *Client, shard uint64) ([]byte, error) {
	return c.Call(ctx, 2, shard, PriorityRPC, 1, []byte("unchanged"))
}
func traceWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("bounded trace fixture did not progress")
	}
}
func traceStage(t *testing.T, s rpctrace.Sample, stage rpctrace.Stage) rpctrace.SpanSample {
	t.Helper()
	for _, span := range s.Spans {
		if span.Stage == stage {
			return span
		}
	}
	t.Fatalf("missing stage %v in %+v", stage, s)
	return rpctrace.SpanSample{}
}

func TestRPCTraceDisabledAndConnectionIdentity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			obs := &traceObserver{}
			var calls atomic.Int32
			every := uint32(0)
			if enabled {
				every = 1
			}
			c, s := tracePair(t, enabled, every, traceOptions(), func(ctx context.Context, b []byte) ([]byte, error) {
				calls.Add(1)
				if (rpctrace.From(ctx) != nil) != enabled {
					return nil, errors.New("handler trace enablement mismatch")
				}
				return b, nil
			}, obs)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for i := 0; i < 3; i++ {
				if i == 2 {
					c.ClosePeer(2)
				}
				b, err := traceCall(ctx, c, uint64(i%2))
				if err != nil || string(b) != "unchanged" {
					t.Fatalf("call %d: %q %v", i, b, err)
				}
			}
			c.Stop()
			s.Stop()
			if calls.Load() != 3 {
				t.Fatalf("executions=%d", calls.Load())
			}
			got := obs.snapshot()
			if !enabled {
				if len(got) != 0 {
					t.Fatalf("disabled samples=%d", len(got))
				}
				return
			}
			if len(got) != 6 {
				t.Fatalf("samples=%d want6", len(got))
			}
			identities := map[string]int{}
			for _, sample := range got {
				if sample.TraceID == "" || sample.Unbound || sample.Result != rpctrace.ResultOK {
					t.Fatalf("invalid sample %+v", sample)
				}
				identities[sample.TraceID]++
				stages := []rpctrace.Stage{rpctrace.StageServiceQueue, rpctrace.StageHandler, rpctrace.StageResponseEncode, rpctrace.StageResponseQueue, rpctrace.StageResponseWrite}
				if sample.Side == rpctrace.SideClient {
					stages = []rpctrace.Stage{rpctrace.StagePeerAcquire, rpctrace.StageCapability, rpctrace.StageClientQueue, rpctrace.StageRequestWrite}
				}
				for _, stage := range stages {
					if span := traceStage(t, sample, stage); span.Incomplete {
						t.Fatalf("successful stage incomplete: %+v", span)
					}
				}
			}
			if len(identities) != 3 {
				t.Fatalf("connection identity collisions: %v", identities)
			}
			for _, n := range identities {
				if n != 2 {
					t.Fatal("client/server did not join")
				}
			}
			if c.traceRecorder.Active() != 0 || s.traceRecorder.Active() != 0 {
				t.Fatal("recorder leaked")
			}
		})
	}
}

func TestRPCTraceMutationContextAndNestedSelection(t *testing.T) {
	obs := &traceObserver{}
	leaf, leafServer := tracePair(t, true, 32, traceOptions(), func(ctx context.Context, b []byte) ([]byte, error) {
		if rpctrace.From(ctx) == nil {
			return nil, errors.New("nested remote not sampled")
		}
		return b, nil
	}, obs)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := traceCall(ctx, leaf, 0); err != nil {
		t.Fatal(err)
	}
	entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var parent string
	var mutationErr error
	c, s := tracePair(t, true, 1, traceOptions(), func(ctx context.Context, b []byte) ([]byte, error) {
		parent = rpctrace.From(ctx).ID()
		close(entered)
		<-release
		if ctx.Err() != nil {
			mutationErr = ctx.Err()
		} else {
			_, mutationErr = traceCall(ctx, leaf, 0)
		}
		close(completed)
		return b, mutationErr
	}, obs)
	callCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := traceCall(callCtx, c, 0); done <- err }()
	traceWait(t, entered)
	stop()
	if err := <-done; !errors.Is(err, ErrCanceled) {
		t.Fatalf("cancel=%v", err)
	}
	close(release)
	traceWait(t, completed)
	c.Stop()
	s.Stop()
	leaf.Stop()
	leafServer.Stop()
	if mutationErr != nil {
		t.Fatalf("started mutation changed: %v", mutationErr)
	}
	found := false
	for _, sample := range obs.snapshot() {
		if sample.Side == rpctrace.SideClient && sample.ParentTraceID == parent && parent != "" {
			found = true
			if sample.Unbound {
				t.Fatal("nested sample unbound")
			}
		}
	}
	if !found {
		t.Fatal("selected parent did not force child trace")
	}
	if c.traceRecorder.Active() != 0 || s.traceRecorder.Active() != 0 || leaf.traceRecorder.Active() != 0 || leafServer.traceRecorder.Active() != 0 {
		t.Fatal("recorder leaked after cancellation")
	}
}

func TestRPCTraceQueueCancelAdmissionAndHandlerResults(t *testing.T) {
	obs := &traceObserver{queue: make(chan struct{}, 32), canceled: make(chan struct{}, 8)}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	opts := traceOptions()
	opts.QueueSize = 1
	c, s := tracePair(t, true, 1, opts, func(_ context.Context, b []byte) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return b, nil
	}, obs)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := traceCall(ctx, c, 0); first <- err }()
	traceWait(t, obs.queue)
	traceWait(t, entered)
	qctx, qcancel := context.WithCancel(ctx)
	second := make(chan error, 1)
	go func() { _, err := traceCall(qctx, c, 0); second <- err }()
	traceWait(t, obs.queue)
	if _, err := traceCall(ctx, c, 0); !errors.Is(err, ErrBusy) {
		t.Fatalf("full queue error=%v", err)
	}
	qcancel()
	if err := <-second; !errors.Is(err, ErrCanceled) {
		t.Fatalf("queued cancel=%v", err)
	}
	traceWait(t, obs.canceled)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	c.Stop()
	s.Stop()
	if calls.Load() != 1 {
		t.Fatalf("canceled queued handler executed: %d", calls.Load())
	}
	if c.traceRecorder.Active() != 0 || s.traceRecorder.Active() != 0 {
		t.Fatal("queue error leaked trace")
	}
	for _, result := range []rpctrace.Result{rpctrace.ResultBusy, rpctrace.ResultCanceled} {
		found := false
		for _, sample := range obs.snapshot() {
			if sample.Side == rpctrace.SideClient && sample.Result == result {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing result %v", result)
		}
	}
	for name, handler := range map[string]Handler{"busy": func(context.Context, []byte) ([]byte, error) { return nil, ErrBusy }, "timeout": func(context.Context, []byte) ([]byte, error) { return nil, ErrTimeout }, "panic": func(context.Context, []byte) ([]byte, error) { panic("fixture") }} {
		t.Run(name, func(t *testing.T) {
			o := &traceObserver{}
			cc, ss := tracePair(t, true, 1, traceOptions(), handler, o)
			if _, err := traceCall(ctx, cc, 0); err == nil {
				t.Fatal("handler failure was lost")
			}
			cc.Stop()
			ss.Stop()
			if len(o.snapshot()) != 2 || cc.traceRecorder.Active() != 0 || ss.traceRecorder.Active() != 0 {
				t.Fatal("handler failure trace incomplete")
			}
		})
	}
}

func TestRPCTraceActiveCapAndSlowObserver(t *testing.T) {
	gate := make(chan struct{})
	block := make(chan struct{})
	obs := &traceObserver{block: block, blocked: make(chan struct{})}
	entered := make(chan struct{}, 129)
	opts := traceOptions()
	opts.Concurrency = 129
	opts.QueueSize = 256
	opts.MaxQueueBytes = 1 << 20
	opts.MaxRetainedBytes = 2 << 20
	c, s := tracePair(t, true, 1, opts, func(_ context.Context, b []byte) ([]byte, error) { entered <- struct{}{}; <-gate; return b, nil }, obs)
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
		select {
		case <-block:
		default:
			close(block)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	results := make(chan error, 129)
	for i := 0; i < 129; i++ {
		go func() { _, err := traceCall(ctx, c, 0); results <- err }()
	}
	for i := 0; i < 129; i++ {
		traceWait(t, entered)
	}
	if c.traceRecorder.Active() != 128 || c.traceRecorder.Dropped() != 1 {
		t.Fatalf("client cap active=%d dropped=%d", c.traceRecorder.Active(), c.traceRecorder.Dropped())
	}
	close(gate)
	for i := 0; i < 129; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	traceWait(t, obs.blocked)
	// The observer is blocked, but a subsequent business RPC must still complete.
	if _, err := traceCall(ctx, c, 1); err != nil {
		t.Fatal(err)
	}
	close(block)
	c.Stop()
	s.Stop()
	if c.traceRecorder.Active() != 0 || s.traceRecorder.Active() != 0 {
		t.Fatal("cap leaked trace")
	}
}

func TestRPCTracePeerFailureIsUnbound(t *testing.T) {
	obs := &traceObserver{}
	c, err := NewClient(ClientConfig{RPCTraceEvery: 1, Observer: obs, Discovery: traceDiscovery("unused"), Dialer: func(string, string, time.Duration) (net.Conn, error) { return nil, errors.New("fixture dial failure") }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	if _, err = traceCall(context.Background(), c, 0); err == nil {
		t.Fatal("dial succeeded")
	}
	c.Stop()
	got := obs.snapshot()
	if len(got) != 1 || !got[0].Unbound || !strings.HasPrefix(got[0].TraceID, "local/") || c.traceRecorder.Active() != 0 {
		t.Fatalf("unbound failure=%+v", got)
	}
}

func TestRPCTraceCapabilityWireAndLegacy(t *testing.T) {
	for _, mode := range []string{"disabled", "legacy", "echo", "wrong", "busy", "generic"} {
		t.Run(mode, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			done := make(chan error, 1)
			go func() {
				p, err := ln.Accept()
				if err != nil {
					done <- err
					return
				}
				defer p.Close()
				_ = p.SetDeadline(time.Now().Add(5 * time.Second))
				read := func() (wire.Frame, error) { return wire.ReadFrame(p, 4096) }
				reply := func(f wire.Frame, status byte, b []byte) error {
					return wire.WriteFrame(p, wire.Frame{Header: wire.Header{Kind: FrameKindRPCResponse, Priority: PriorityControl, ServiceID: f.Header.ServiceID, RequestID: f.Header.RequestID}, Body: NewOwnedBuffer(append([]byte{status}, b...), nil)}, 4096)
				}
				// Budget negotiation precedes optional trace negotiation, both remain v1.
				f, err := read()
				if err != nil {
					done <- err
					return
				}
				if f.Header.Kind != FrameKindRPCRequest || f.Header.ServiceID != 65535 {
					f.Body.Release()
					done <- errors.New("invalid budget control frame")
					return
				}
				body := append([]byte(nil), f.Body.Bytes()...)
				f.Body.Release()
				if err = reply(f, wire.ResponseOK, body); err != nil {
					done <- err
					return
				}
				if mode != "disabled" {
					f, err = read()
					if err != nil {
						done <- err
						return
					}
					if f.Header.Kind != FrameKindRPCRequest || f.Header.ServiceID != 65534 || f.Header.RequestID>>63 != 0 {
						f.Body.Release()
						done <- errors.New("trace capability is not untraced v1")
						return
					}
					body = append([]byte(nil), f.Body.Bytes()...)
					f.Body.Release()
					status := byte(wire.ResponseOK)
					switch mode {
					case "legacy":
						status = wire.ResponseServiceNotFound
						body = nil
					case "generic":
						status = wire.ResponseErr
						body = []byte("transport: service 65534 not found")
					case "busy":
						status = wire.ResponseBusy
						body = nil
					case "wrong":
						body = []byte("wrong echo")
					}
					if err = reply(f, status, body); err != nil {
						done <- err
						return
					}
					if mode == "wrong" || mode == "busy" || mode == "generic" {
						f, err = read()
						f.Body.Release()
						if !errors.Is(err, io.EOF) {
							done <- errors.New("business frame followed failed trace negotiation")
						} else {
							done <- nil
						}
						return
					}
				}
				for i := 0; i < 2; i++ {
					f, err = read()
					if err != nil {
						done <- err
						return
					}
					valid := f.Header.ServiceID == 1 && f.Header.Kind == FrameKindRPCBudgetRequest && string(f.Body.Bytes()) == "unchanged" && (f.Header.RequestID>>63 == 1) == (mode == "echo")
					body = append([]byte(nil), f.Body.Bytes()...)
					f.Body.Release()
					if !valid {
						done <- errors.New("business wire bytes/identity changed")
						return
					}
					if err = reply(f, wire.ResponseOK, body); err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}()
			every := uint32(1)
			if mode == "disabled" {
				every = 0
			}
			c, err := NewClient(ClientConfig{RPCTraceEvery: every, RequestBudgets: true, PoolSize: 1, Discovery: traceDiscovery(ln.Addr().String())})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = traceCall(ctx, c, 0)
			if mode == "wrong" || mode == "busy" || mode == "generic" {
				if err == nil {
					t.Fatal("invalid trace negotiation accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = traceCall(ctx, c, 0); err != nil {
					t.Fatal(err)
				}
			}
			c.Stop()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// delayedWriteReturn reports bytes to the peer before returning from the caller's
// write. This deterministically exercises response-before-write-completion.
type delayedWriteReturn struct {
	net.Conn
	remaining int
	gate      <-chan struct{}
	blocked   chan struct{}
	once      sync.Once
}

func (c *delayedWriteReturn) Write(p []byte) (int, error) {
	if len(p) == 24 && binary.BigEndian.Uint16(p[6:8]) == 1 && (FrameKind(p[4]) == FrameKindRPCRequest || FrameKind(p[4]) == FrameKindRPCBudgetRequest) {
		c.remaining = int(binary.BigEndian.Uint32(p[16:20]))
		return c.Conn.Write(p)
	}
	n, err := c.Conn.Write(p)
	if c.remaining > 0 {
		c.remaining -= n
		if c.remaining <= 0 && err == nil {
			c.once.Do(func() { close(c.blocked) })
			<-c.gate
		}
	}
	return n, err
}
func TestRPCTraceResponseBeforeWriteCompletionIsExplicit(t *testing.T) {
	obs := &traceObserver{}
	unused, s := tracePair(t, true, 0, traceOptions(), func(_ context.Context, b []byte) ([]byte, error) { return b, nil }, obs)
	unused.Stop()
	gate, blocked := make(chan struct{}), make(chan struct{})
	c, err := NewClient(ClientConfig{RPCTraceEvery: 1, RequestBudgets: true, PoolSize: 1, Observer: obs, Discovery: traceDiscovery(s.Addr()), Dialer: func(network, addr string, timeout time.Duration) (net.Conn, error) {
		p, err := net.DialTimeout(network, addr, timeout)
		if err != nil {
			return nil, err
		}
		return &delayedWriteReturn{Conn: p, gate: gate, blocked: blocked}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
		c.Stop()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if b, err := traceCall(ctx, c, 0); err != nil || string(b) != "unchanged" {
		t.Fatalf("early response=%q,%v", b, err)
	}
	traceWait(t, blocked)
	close(gate)
	c.Stop()
	s.Stop()
	for _, sample := range obs.snapshot() {
		if sample.Side == rpctrace.SideClient {
			span := traceStage(t, sample, rpctrace.StageRequestWrite)
			if !span.Incomplete {
				t.Fatal("unobserved write completion invented")
			}
			if sample.Result != rpctrace.ResultOK {
				t.Fatal("observation changed successful RPC")
			}
			return
		}
	}
	t.Fatal("missing client sample")
}

func TestRPCTraceReservedRegistration(t *testing.T) {
	s, err := NewServer(ServerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if err = s.Handle(65534, func(context.Context, []byte) ([]byte, error) { return nil, nil }, traceOptions()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("reserved registration=%v", err)
	}
}
