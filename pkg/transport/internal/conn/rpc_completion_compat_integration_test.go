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

// completionCompatFixture uses the existing connection, wire and observer seams;
// the pending index is inspected only through its unchanged count observation.
type completionCompatFixture struct {
	connection   *Conn
	peer         net.Conn
	trace        *rpctrace.Trace
	recorder     *rpctrace.Recorder
	released     atomic.Int32
	callerJoined chan struct{}
}

func newCompletionCompatFixture(t *testing.T, sampled bool, limits core.Limits, observer core.Observer) *completionCompatFixture {
	t.Helper()
	raw, peer := net.Pipe()
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	cfg := receiveBridgeConfig(sampled, observer)
	cfg.Limits = limits
	f := &completionCompatFixture{connection: New(raw, cfg, nil), peer: peer}
	if sampled {
		f.connection.AcceptRPCTrace([16]byte{11})
		f.trace, f.recorder = receiveBridgeTrace(t)
	}
	f.connection.Start()
	t.Cleanup(func() {
		peer.Close()
		completionCompatClose(t, f.connection)
		if f.callerJoined != nil {
			waitClosed(t, f.callerJoined)
		}
		if f.trace != nil {
			f.trace.Finish(rpctrace.ResultError)
		}
	})
	return f
}

func (f *completionCompatFixture) call(ctx context.Context) <-chan receiveBridgeResult {
	if f.trace != nil {
		ctx = rpctrace.With(ctx, f.trace)
	}
	result := make(chan receiveBridgeResult, 1)
	f.callerJoined = make(chan struct{})
	go func() {
		defer close(f.callerJoined)
		body, err := f.connection.Call(ctx, Outbound{Priority: core.PriorityRPC, ServiceID: 7,
			Payload: core.NewOwnedBuffer([]byte("request"), func([]byte) { f.released.Add(1) })})
		result <- receiveBridgeResult{body: body, err: err}
	}()
	return result
}

// settle joins both connection actors before checking final ownership and traces.
func (f *completionCompatFixture) settle(t *testing.T, result receiveBridgeResult, wantReceive bool) {
	t.Helper()
	waitClosed(t, f.callerJoined)
	f.peer.Close()
	completionCompatClose(t, f.connection)
	if pending := f.connection.pending.Len(); pending != 0 || f.released.Load() != 1 {
		t.Fatalf("completion ownership: pending=%d payload_releases=%d", pending, f.released.Load())
	}
	if f.trace != nil {
		sample := f.trace.Finish(core.RPCTraceResult(result.err))
		receiveBridgeSpans(t, sample, wantReceive)
		if f.recorder.Active() != 0 {
			t.Fatalf("completion retained %d trace owners", f.recorder.Active())
		}
	}
	t.Logf("RPC_COMPLETION_COMPAT sampled=%t error=%v pending=0 payload_releases=1 caller_joined=true connection_joined=true receive=%t", f.trace != nil, result.err, wantReceive)
}

func completionCompatClose(t *testing.T, connection *Conn) {
	t.Helper()
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		connection.Close(core.ErrStopped)
	}()
	waitClosed(t, joined)
}

func TestRPCCompletionSendFailureKeepsPrecedenceOverPreparedResponse(t *testing.T) {
	for _, sampled := range []bool{false, true} {
		t.Run(map[bool]string{false: "unobserved", true: "sampled"}[sampled], func(t *testing.T) {
			entered, release, published := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce, publishOnce sync.Once
			var held atomic.Bool
			releaseCaller := func() { releaseOnce.Do(func() { close(release) }) }
			observer := receiveBridgeObserver(func(event core.Event) {
				if event.Name == "scheduler_admission" && event.Result == "full" {
					enterOnce.Do(func() {
						held.Store(true)
						close(entered)
						<-release
					})
				}
				// This callback follows actual response processing, regardless of
				// whether the new owner still occupies its pending index entry.
				if event.Name == "pending_rpc" && held.Load() {
					publishOnce.Do(func() { close(published) })
				}
			})
			limits := testLimits()
			limits.MaxQueuedBytesPerConn = 1
			f := newCompletionCompatFixture(t, sampled, limits, observer)
			t.Cleanup(releaseCaller)
			done := f.call(context.Background())
			waitClosed(t, entered)
			requestID := uint64(1)
			if sampled {
				requestID |= rpcTraceSampleBit
			}
			// The request is registered but Send's byte-limit rejection has not
			// returned. Inject a legal response through the real reader only.
			header := wire.Header{RequestID: requestID}
			receiveBridgeReply(t, f.peer, header, 7, []byte{wire.ResponseOK, 'o', 'k'})
			waitClosed(t, published)
			select {
			case <-done:
				t.Fatal("Call returned before its Send failure barrier was released")
			default:
			}
			releaseCaller()
			result := receiveBridgeAwait(t, done)
			if !errors.Is(result.err, core.ErrQueueFull) || len(result.body) != 0 {
				t.Fatalf("prepared response replaced Send failure: body=%q err=%v", result.body, result.err)
			}
			f.settle(t, result, false)
		})
	}
}

// negotiate exercises the actual capability request and response; it never
// assigns private capability state or registers a pending entry directly.
func (f *completionCompatFixture) negotiate(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done, joined := make(chan error, 1), make(chan struct{})
	t.Cleanup(func() { waitClosed(t, joined) })
	go func() {
		defer close(joined)
		done <- f.connection.NegotiateRequestBudgets(ctx)
	}()
	_ = f.peer.SetDeadline(time.Now().Add(3 * time.Second))
	probe, err := wire.ReadFrame(f.peer, testLimits().MaxFrameBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	header, payload := probe.Header, string(probe.Body.Bytes())
	probe.Body.Release()
	if header.Kind != core.FrameKindRPCRequest || header.ServiceID != wire.CapabilityServiceID || payload != wire.CapabilityRequestBudgets {
		t.Fatalf("unexpected capability request: header=%+v payload=%q", header, payload)
	}
	body := append([]byte{wire.ResponseOK}, []byte(wire.CapabilityRequestBudgets)...)
	receiveBridgeReply(t, f.peer, header, wire.CapabilityServiceID, body)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("budget negotiation failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("budget negotiation did not finish within fixture deadline")
	}
	waitClosed(t, joined)
}

func TestRPCCompletionExplicitCancelKeepsErrorAndNegotiatedCancelFrame(t *testing.T) {
	for _, budget := range []bool{false, true} {
		for _, sampled := range []bool{false, true} {
			name := map[bool]string{false: "plain", true: "budget"}[budget] + "/" + map[bool]string{false: "unobserved", true: "sampled"}[sampled]
			t.Run(name, func(t *testing.T) {
				f := newCompletionCompatFixture(t, sampled, testLimits(), nil)
				if budget {
					f.negotiate(t)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				t.Cleanup(cancel)
				done := f.call(ctx)
				header := receiveBridgeRequest(t, f.peer)
				if budget && (header.Kind != core.FrameKindRPCBudgetRequest || header.BudgetMillis == 0) {
					t.Fatalf("business request did not preserve negotiated budget: %+v", header)
				}
				if !budget && header.Kind != core.FrameKindRPCRequest {
					t.Fatalf("unnegotiated request changed wire kind: %+v", header)
				}
				cancel()
				if budget {
					frame, err := wire.ReadFrame(f.peer, testLimits().MaxFrameBodyBytes)
					if err != nil {
						t.Fatalf("healthy negotiated connection lost best-effort cancellation: %v", err)
					}
					cancelHeader, bodyLen := frame.Header, frame.Body.Len()
					frame.Body.Release()
					if cancelHeader.Kind != core.FrameKindRPCCancel || cancelHeader.RequestID != header.RequestID || cancelHeader.ServiceID != header.ServiceID || cancelHeader.Priority != core.PriorityControl || bodyLen != 0 {
						t.Fatalf("cancellation ownership/identity changed: request=%+v cancel=%+v body_len=%d", header, cancelHeader, bodyLen)
					}
				}
				result := receiveBridgeAwait(t, done)
				if !errors.Is(result.err, core.ErrCanceled) || errors.Is(result.err, context.DeadlineExceeded) || len(result.body) != 0 {
					t.Fatalf("explicit cancellation changed result: body=%q err=%v", result.body, result.err)
				}
				f.settle(t, result, false)
			})
		}
	}
}

func TestRPCCompletionConnectionCloseKeepsDeliveredResponsePreference(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		for _, sampled := range []bool{false, true} {
			name := map[bool]string{false: "missing", true: "delivered"}[delivered] + "/" + map[bool]string{false: "unobserved", true: "sampled"}[sampled]
			t.Run(name, func(t *testing.T) {
				entered, release, published := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var enterOnce, releaseOnce, publishOnce sync.Once
				var held atomic.Bool
				releaseCaller := func() { releaseOnce.Do(func() { close(release) }) }
				observer := receiveBridgeObserver(func(event core.Event) {
					if delivered && event.Name == "scheduler_admission" && event.Result == "ok" {
						enterOnce.Do(func() {
							held.Store(true)
							close(entered)
							<-release
						})
					}
					if delivered && event.Name == "pending_rpc" && held.Load() {
						publishOnce.Do(func() { close(published) })
					}
				})
				f := newCompletionCompatFixture(t, sampled, testLimits(), observer)
				t.Cleanup(releaseCaller)
				done := f.call(context.Background())
				if delivered {
					waitClosed(t, entered)
				}
				header := receiveBridgeRequest(t, f.peer)
				if delivered {
					receiveBridgeReply(t, f.peer, header, 7, []byte{wire.ResponseOK, 'o', 'k'})
					waitClosed(t, published)
				}
				// Close must finish before releasing the delivered caller, making
				// both shutdown and its earlier response observable together.
				completionCompatClose(t, f.connection)
				releaseCaller()
				result := receiveBridgeAwait(t, done)
				if delivered {
					if result.err != nil || string(result.body) != "ok" {
						t.Fatalf("connection close replaced delivered response: body=%q err=%v", result.body, result.err)
					}
				} else if !errors.Is(result.err, core.ErrStopped) || len(result.body) != 0 {
					t.Fatalf("missing response changed stop result: body=%q err=%v", result.body, result.err)
				}
				f.settle(t, result, delivered && sampled)
			})
		}
	}
}
