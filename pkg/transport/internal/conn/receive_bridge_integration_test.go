//go:build integration

package conn

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

type receiveBridgeObserver func(core.Event)

func (f receiveBridgeObserver) ObserveTransport(e core.Event) { f(e) }

// Reflection keeps the first behavior test compilable before the new optional
// setting exists. Missing instrumentation is asserted at the output seam.
func receiveBridgeConfig(enabled bool, observer core.Observer) Config {
	cfg := Config{Limits: testLimits(), Observer: observer}
	field := reflect.ValueOf(&cfg).Elem().FieldByName("ReceiveTracing")
	if field.IsValid() {
		field.SetBool(enabled)
	}
	return cfg
}

func receiveBridgeHasTiming(value any) bool {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	f := v.FieldByName("ReceiveTiming")
	return f.IsValid() && f.Kind() == reflect.Pointer && !f.IsNil()
}

type receiveBridgeResult struct {
	body []byte
	err  error
}

func receiveBridgeCall(c *Conn, ctx context.Context, trace *rpctrace.Trace, released *atomic.Int32) <-chan receiveBridgeResult {
	done := make(chan receiveBridgeResult, 1)
	go func() {
		body, err := c.Call(rpctrace.With(ctx, trace), Outbound{Priority: core.PriorityRPC, ServiceID: 7,
			Payload: core.NewOwnedBuffer([]byte("request"), func([]byte) { released.Add(1) })})
		done <- receiveBridgeResult{body: body, err: err}
	}()
	return done
}

func receiveBridgeAwait(t *testing.T, done <-chan receiveBridgeResult) receiveBridgeResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("receive bridge call did not complete within fixture deadline")
		return receiveBridgeResult{}
	}
}

func receiveBridgeTrace(t *testing.T) (*rpctrace.Trace, *rpctrace.Recorder) {
	t.Helper()
	recorder, err := rpctrace.NewRecorder(8)
	if err != nil {
		t.Fatal(err)
	}
	return recorder.Start(context.Background(), rpctrace.SideClient, 7), recorder
}

func receiveBridgeRequest(t *testing.T, peer net.Conn) wire.Header {
	t.Helper()
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	frame, err := wire.ReadFrame(peer, testLimits().MaxFrameBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Body.Release()
	if string(frame.Body.Bytes()) != "request" || frame.Header.ServiceID != 7 {
		t.Fatal("observation changed request bytes or service")
	}
	return frame.Header
}

func receiveBridgeReply(t *testing.T, peer net.Conn, header wire.Header, service uint16, body []byte) {
	t.Helper()
	frame := wire.Frame{Header: wire.Header{Kind: core.FrameKindRPCResponse, Priority: core.PriorityRPC,
		ServiceID: service, RequestID: header.RequestID}, Body: core.CopyOwnedBuffer(body)}
	defer frame.Body.Release()
	if err := wire.WriteFrame(peer, frame, testLimits().MaxFrameBodyBytes); err != nil {
		t.Fatal(err)
	}
}

func receiveBridgeSpans(t *testing.T, sample *rpctrace.Sample, want bool) [3]rpctrace.SpanSample {
	t.Helper()
	names := [3]rpctrace.Stage{"response_receive_observer", "response_dispatch", "response_resume"}
	var got [3]rpctrace.SpanSample
	count := 0
	for _, span := range sample.Spans {
		for i, name := range names {
			if span.Stage == name {
				got[i] = span
				count++
			}
		}
	}
	if !want {
		if count != 0 {
			t.Fatalf("unavailable receive bridge fabricated %d stages", count)
		}
		return got
	}
	if count != 3 {
		t.Fatalf("completed response has %d receive bridge stages, want 3", count)
	}
	for i, span := range got {
		if span.Stage != names[i] || span.Incomplete || span.Result != rpctrace.ResultOK || span.Duration < 0 {
			t.Fatalf("receive stage %d is not a completed local interval: %+v", i, span)
		}
		if i > 0 && got[i-1].Offset+got[i-1].Duration != span.Offset {
			t.Fatal("receive stages are not the same contiguous local timeline")
		}
	}
	if got[2].Offset+got[2].Duration > sample.Duration {
		t.Fatal("receive group extends beyond caller finish")
	}
	return got
}

func TestConnReceiveBridgeMeasuresSynchronousObserver(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseObserver := func() { once.Do(func() { close(release) }) }
	var received atomic.Int64
	observer := receiveBridgeObserver(func(e core.Event) {
		if e.Name == "received_bytes" && e.Kind == core.FrameKindRPCResponse {
			received.Add(int64(e.Bytes))
			close(entered)
			<-release
		}
	})
	raw, peer := net.Pipe()
	c := New(raw, receiveBridgeConfig(true, observer), nil)
	c.AcceptRPCTrace([16]byte{1})
	c.Start()
	t.Cleanup(func() { releaseObserver(); peer.Close(); c.Close(nil) })
	trace, recorder := receiveBridgeTrace(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var released atomic.Int32
	done := receiveBridgeCall(c, ctx, trace, &released)
	header := receiveBridgeRequest(t, peer)
	receiveBridgeReply(t, peer, header, 7, []byte{wire.ResponseOK, 'o', 'k'})
	waitClosed(t, entered)
	heldAt := time.Now()
	select {
	case <-done:
		t.Fatal("caller completed while the synchronous receive observer was held")
	case <-time.After(15 * time.Millisecond):
	}
	releasedAt := time.Now()
	releaseObserver()
	result := receiveBridgeAwait(t, done)
	if result.err != nil || string(result.body) != "ok" {
		t.Fatalf("business response changed: %q %v", result.body, result.err)
	}
	sample := trace.Finish(rpctrace.ResultOK)
	spans := receiveBridgeSpans(t, sample, true)
	if spans[0].Duration < releasedAt.Sub(heldAt) {
		t.Fatalf("observer interval %v excludes witnessed held duration %v", spans[0].Duration, releasedAt.Sub(heldAt))
	}
	c.Close(nil)
	if received.Load() != 3 || released.Load() != 1 || c.pending.Len() != 0 || recorder.Active() != 0 {
		t.Fatalf("ownership/bytes changed: received=%d releases=%d pending=%d traces=%d", received.Load(), released.Load(), c.pending.Len(), recorder.Active())
	}
}

func TestConnReceiveBridgeCarriesVerifiedServerObserverInterval(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseObserver := func() { once.Do(func() { close(release) }) }
	observer := receiveBridgeObserver(func(e core.Event) {
		if e.Name == "received_bytes" && e.Kind == core.FrameKindRPCRequest {
			close(entered)
			<-release
		}
	})
	raw, peer := net.Pipe()
	inbound := make(chan Inbound, 1)
	c := New(raw, receiveBridgeConfig(true, observer), DispatchFunc(func(_ context.Context, in Inbound) { inbound <- in }))
	c.AcceptRPCTrace([16]byte{9})
	c.Start()
	t.Cleanup(func() { releaseObserver(); peer.Close(); c.Close(nil) })
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	frame := wire.Frame{Header: wire.Header{Kind: core.FrameKindRPCRequest, Priority: core.PriorityRPC, ServiceID: 7, RequestID: rpcTraceSampleBit | 23}, Body: core.CopyOwnedBuffer([]byte("request"))}
	err := wire.WriteFrame(peer, frame, testLimits().MaxFrameBodyBytes)
	frame.Body.Release()
	if err != nil {
		t.Fatal(err)
	}
	waitClosed(t, entered)
	heldAt := time.Now()
	select {
	case in := <-inbound:
		in.Payload.Release()
		t.Fatal("server dispatch bypassed the synchronous receive observer")
	case <-time.After(15 * time.Millisecond):
	}
	releasedAt := time.Now()
	releaseObserver()
	in := waitInbound(t, inbound)
	defer in.Payload.Release()
	if string(in.Payload.Bytes()) != "request" || in.ServiceID != 7 || in.RequestID != rpcTraceSampleBit|23 {
		t.Fatal("receive observation changed server request")
	}
	if !receiveBridgeHasTiming(in) {
		t.Fatal("verified server request lost receive timing after real held observer")
	}
	timing := reflect.ValueOf(in).FieldByName("ReceiveTiming").Elem()
	frameReady := timing.FieldByName("FrameReady").Interface().(time.Time)
	observerDone := timing.FieldByName("ObserverDone").Interface().(time.Time)
	if frameReady.IsZero() || observerDone.Sub(frameReady) < releasedAt.Sub(heldAt) {
		t.Fatal("server receive timing excludes witnessed observer interval")
	}
	if !timing.FieldByName("Notified").Interface().(time.Time).IsZero() {
		t.Fatal("server request fabricated client notification")
	}
}

func TestConnReceiveBridgeSeparatesNotificationFromCallerResume(t *testing.T) {
	admission, release, notified := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce, notifyOnce sync.Once
	releaseCaller := func() { releaseOnce.Do(func() { close(release) }) }
	observer := receiveBridgeObserver(func(e core.Event) {
		if e.Name == "scheduler_admission" && e.Result == "ok" {
			close(admission)
			<-release
		}
		if e.Name == "pending_rpc" && e.Inflight == 0 {
			notifyOnce.Do(func() { close(notified) })
		}
	})
	raw, peer := net.Pipe()
	c := New(raw, receiveBridgeConfig(true, observer), nil)
	c.AcceptRPCTrace([16]byte{2})
	c.Start()
	t.Cleanup(func() { releaseCaller(); peer.Close(); c.Close(nil) })
	trace, _ := receiveBridgeTrace(t)
	var released atomic.Int32
	done := receiveBridgeCall(c, context.Background(), trace, &released)
	waitClosed(t, admission)
	header := receiveBridgeRequest(t, peer)
	receiveBridgeReply(t, peer, header, 7, []byte{wire.ResponseOK, 'o', 'k'})
	// pending_rpc is emitted after handleRPCResponse's buffered notification.
	waitClosed(t, notified)
	heldAt := time.Now()
	select {
	case <-done:
		t.Fatal("caller escaped its still-held scheduler admission callback")
	case <-time.After(15 * time.Millisecond):
	}
	releasedAt := time.Now()
	releaseCaller()
	result := receiveBridgeAwait(t, done)
	if result.err != nil || string(result.body) != "ok" {
		t.Fatalf("response=%q error=%v", result.body, result.err)
	}
	spans := receiveBridgeSpans(t, trace.Finish(rpctrace.ResultOK), true)
	if spans[2].Duration < releasedAt.Sub(heldAt) {
		t.Fatalf("resume interval %v excludes witnessed buffered wait %v", spans[2].Duration, releasedAt.Sub(heldAt))
	}
}

type receiveBridgeHeldWrite struct {
	net.Conn
	bytes   int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *receiveBridgeHeldWrite) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.bytes += n
	if err == nil && c.bytes >= wire.HeaderSize+len("request") {
		c.once.Do(func() { close(c.entered); <-c.release })
	}
	return n, err
}

func TestConnReceiveBridgeSurvivesResponseBeforeWriteCallback(t *testing.T) {
	raw, peer := net.Pipe()
	held := &receiveBridgeHeldWrite{Conn: raw, entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	releaseWrite := func() { once.Do(func() { close(held.release) }) }
	c := New(held, receiveBridgeConfig(true, nil), nil)
	c.AcceptRPCTrace([16]byte{3})
	c.Start()
	t.Cleanup(func() { releaseWrite(); peer.Close(); c.Close(nil) })
	trace, _ := receiveBridgeTrace(t)
	var released atomic.Int32
	done := receiveBridgeCall(c, context.Background(), trace, &released)
	header := receiveBridgeRequest(t, peer)
	waitClosed(t, held.entered)
	receiveBridgeReply(t, peer, header, 7, []byte{wire.ResponseOK, 'o', 'k'})
	result := receiveBridgeAwait(t, done)
	if result.err != nil || string(result.body) != "ok" {
		t.Fatalf("early response=%q error=%v", result.body, result.err)
	}
	sample := trace.Finish(rpctrace.ResultOK)
	receiveBridgeSpans(t, sample, true)
	for _, span := range sample.Spans {
		if span.Stage == rpctrace.StageResponseWait {
			t.Fatal("response-before-write callback invented response_wait")
		}
	}
	before := sample.Clone()
	releaseWrite()
	c.Close(nil)
	if !reflect.DeepEqual(*sample, before) || released.Load() != 1 {
		t.Fatal("late write callback mutated finished sample or payload release count")
	}
}

func TestConnReceiveBridgeTimeoutLateFrameIsUnknown(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			entered, release, crossed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			releaseObserver := func() { once.Do(func() { close(release) }) }
			observer := receiveBridgeObserver(func(e core.Event) {
				if e.Name == "received_bytes" && e.Kind == core.FrameKindRPCResponse {
					close(entered)
					<-release
				}
			})
			raw, peer := net.Pipe()
			c := New(raw, receiveBridgeConfig(true, observer), DispatchFunc(func(_ context.Context, in Inbound) { in.Payload.Release(); close(crossed) }))
			c.AcceptRPCTrace([16]byte{4})
			c.Start()
			t.Cleanup(func() { releaseObserver(); peer.Close(); c.Close(nil) })
			trace, _ := receiveBridgeTrace(t)
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			}
			defer cancel()
			var released atomic.Int32
			done := receiveBridgeCall(c, ctx, trace, &released)
			header := receiveBridgeRequest(t, peer)
			receiveBridgeReply(t, peer, header, 7, []byte{wire.ResponseOK, 'o', 'k'})
			waitClosed(t, entered)
			if !deadline {
				cancel()
			}
			result := receiveBridgeAwait(t, done)
			want := error(core.ErrCanceled)
			if deadline {
				want = context.DeadlineExceeded
			}
			if !errors.Is(result.err, want) {
				t.Fatalf("terminal error=%v want=%v", result.err, want)
			}
			sample := trace.Finish(core.RPCTraceResult(result.err))
			receiveBridgeSpans(t, sample, false)
			before := sample.Clone()
			releaseObserver()
			frame := wire.Frame{Header: wire.Header{Kind: core.FrameKindNotify, Priority: core.PriorityControl, ServiceID: 7}, Body: core.CopyOwnedBuffer([]byte("crossed"))}
			if err := wire.WriteFrame(peer, frame, testLimits().MaxFrameBodyBytes); err != nil {
				frame.Body.Release()
				t.Fatal(err)
			}
			frame.Body.Release()
			waitClosed(t, crossed)
			c.Close(nil)
			if !reflect.DeepEqual(*sample, before) || c.pending.Len() != 0 || released.Load() != 1 {
				t.Fatal("late response changed terminal sample or retained ownership")
			}
		})
	}
}

func TestConnReceiveBridgeGatesAndRemoteError(t *testing.T) {
	for _, tc := range []struct {
		name                string
		enabled, negotiated bool
		service             uint16
		status              byte
		want                bool
	}{
		{"disabled", false, true, 7, wire.ResponseOK, false},
		{"unnegotiated", true, false, 7, wire.ResponseOK, false},
		{"wrong_service", true, true, 8, wire.ResponseOK, false},
		{"remote_error", true, true, 7, wire.ResponseBusy, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, peer := net.Pipe()
			c := New(raw, receiveBridgeConfig(tc.enabled, nil), nil)
			if tc.negotiated {
				c.AcceptRPCTrace([16]byte{5})
			}
			c.Start()
			t.Cleanup(func() { peer.Close(); c.Close(nil) })
			trace, _ := receiveBridgeTrace(t)
			var released atomic.Int32
			done := receiveBridgeCall(c, context.Background(), trace, &released)
			header := receiveBridgeRequest(t, peer)
			receiveBridgeReply(t, peer, header, tc.service, []byte{tc.status, 'o', 'k'})
			result := receiveBridgeAwait(t, done)
			if tc.status == wire.ResponseOK {
				if result.err != nil || string(result.body) != "ok" {
					t.Fatalf("legacy response behavior changed: %q %v", result.body, result.err)
				}
			} else {
				var remote core.RemoteError
				if !errors.As(result.err, &remote) || remote.Code != core.RemoteErrorCodeBusy {
					t.Fatalf("remote error changed: %v", result.err)
				}
			}
			receiveBridgeSpans(t, trace.Finish(core.RPCTraceResult(result.err)), tc.want)
		})
	}
}

func TestConnReceiveBridgeSyntheticCloseRemainsUnknown(t *testing.T) {
	raw, peer := net.Pipe()
	c := New(raw, receiveBridgeConfig(true, nil), nil)
	c.AcceptRPCTrace([16]byte{8})
	c.Start()
	t.Cleanup(func() { peer.Close(); c.Close(nil) })
	trace, recorder := receiveBridgeTrace(t)
	var released atomic.Int32
	done := receiveBridgeCall(c, context.Background(), trace, &released)
	receiveBridgeRequest(t, peer)
	c.Close(core.ErrStopped)
	result := receiveBridgeAwait(t, done)
	if !errors.Is(result.err, core.ErrStopped) {
		t.Fatalf("synthetic shutdown error changed: %v", result.err)
	}
	receiveBridgeSpans(t, trace.Finish(rpctrace.ResultStopped), false)
	if c.pending.Len() != 0 || recorder.Active() != 0 || released.Load() != 1 {
		t.Fatal("synthetic close retained pending/trace/body ownership")
	}
}

func TestConnReceiveBridgeRejectsForgedAndNonRPCMetadata(t *testing.T) {
	for _, kind := range []core.FrameKind{core.FrameKindRPCRequest, core.FrameKindRPCBudgetRequest, core.FrameKindNotify, core.FrameKindData, core.FrameKindRPCResponse} {
		t.Run(string(rune('A'+kind)), func(t *testing.T) {
			for _, negotiated := range []bool{false, true} {
				raw, peer := net.Pipe()
				inbound := make(chan Inbound, 1)
				c := New(raw, receiveBridgeConfig(true, nil), DispatchFunc(func(_ context.Context, in Inbound) { inbound <- in }))
				if negotiated {
					c.AcceptRPCTrace([16]byte{6})
				}
				id := rpcTraceSampleBit | 17
				response := make(chan rpc.Response, 1)
				if kind == core.FrameKindRPCResponse {
					c.pending.Store(id, response)
				}
				c.Start()
				_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
				frame := wire.Frame{Header: wire.Header{Kind: kind, Priority: core.PriorityRPC, ServiceID: 7, RequestID: id}, Body: core.CopyOwnedBuffer([]byte{wire.ResponseOK})}
				err := wire.WriteFrame(peer, frame, testLimits().MaxFrameBodyBytes)
				frame.Body.Release()
				if err != nil {
					peer.Close()
					c.Close(nil)
					t.Fatal(err)
				}
				var has bool
				if kind == core.FrameKindRPCResponse {
					select {
					case got := <-response:
						has = receiveBridgeHasTiming(got)
					case <-time.After(3 * time.Second):
						peer.Close()
						c.Close(nil)
						t.Fatal("response missing")
					}
				} else {
					in := waitInbound(t, inbound)
					has = receiveBridgeHasTiming(in)
					in.Payload.Release()
				}
				peer.Close()
				c.Close(nil)
				want := negotiated && (kind == core.FrameKindRPCRequest || kind == core.FrameKindRPCBudgetRequest || kind == core.FrameKindRPCResponse)
				if has != want {
					t.Fatalf("kind=%d negotiated=%v timing=%v want=%v", kind, negotiated, has, want)
				}
			}
		})
	}
}
