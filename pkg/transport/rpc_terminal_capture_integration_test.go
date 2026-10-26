//go:build integration

package transport

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func TestRPCTerminalCaptureJoinsActualCallWithoutChangingResult(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorder_disabled", true: "captured_despite_sampling"}[enabled], func(t *testing.T) {
			obs := &traceObserver{}
			every := uint32(0)
			if enabled {
				every = 32
			}
			c, s := tracePair(t, enabled, every, traceOptions(), func(_ context.Context, b []byte) ([]byte, error) { return b, nil }, obs)
			if _, err := traceCall(context.Background(), c, 0); err != nil {
				t.Fatal(err)
			}
			ctx, capture := rpctrace.WithTerminalCapture(context.Background())
			body, err := traceCall(ctx, c, 0)
			if err != nil || string(body) != "unchanged" {
				t.Fatalf("business result=%q/%v", body, err)
			}
			got := capture.Snapshot()
			if !enabled {
				if got.State != rpctrace.TerminalTraceRecorderDisabled || got.TraceID != "" || got.Unbound {
					t.Fatalf("disabled capture=%+v", got)
				}
				return
			}
			if got.State != rpctrace.TerminalTraceBound || got.TraceID == "" || got.Unbound {
				t.Fatalf("actual wire identity missing: %+v", got)
			}
			c.Stop()
			s.Stop()
			joined := 0
			for _, sample := range obs.snapshot() {
				if sample.TraceID == got.TraceID {
					joined++
				}
			}
			if joined != 2 {
				t.Fatalf("captured ID joins %d actual endpoint samples, want2", joined)
			}
		})
	}
}

func TestRPCTerminalCaptureExhaustionAndPreDispatchRemainExplicit(t *testing.T) {
	obs := &traceObserver{}
	c, err := NewClient(ClientConfig{RPCTraceEvery: 32, Observer: obs, Discovery: traceDiscovery("unused"), Dialer: func(string, string, time.Duration) (net.Conn, error) { return nil, errors.New("fixture dial failure") }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	var held []*rpctrace.Trace
	for i := 0; i < 128; i++ {
		held = append(held, c.traceRecorder.Start(context.Background(), rpctrace.SideClient, 1))
	}
	ctx, exhausted := rpctrace.WithTerminalCapture(context.Background())
	if _, err := traceCall(ctx, c, 0); !errors.Is(err, ErrDialFailed) {
		t.Fatalf("dial error changed: %v", err)
	}
	if got := exhausted.Snapshot(); got.State != rpctrace.TerminalTraceRecorderExhausted || got.TraceID != "" {
		t.Fatalf("exhausted=%+v", got)
	}
	for _, trace := range held {
		trace.Finish(rpctrace.ResultOK)
	}
	ctx, preDispatch := rpctrace.WithTerminalCapture(context.Background())
	if _, err := traceCall(ctx, c, 0); !errors.Is(err, ErrDialFailed) {
		t.Fatalf("unbound dial error changed: %v", err)
	}
	if got := preDispatch.Snapshot(); got.State != rpctrace.TerminalTraceUnbound || got.TraceID == "" || !got.Unbound {
		t.Fatalf("unbound=%+v", got)
	}
}

func TestRPCTerminalCaptureCancellationPreservesOriginalError(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	obs := &traceObserver{}
	c, _ := tracePair(t, true, 32, traceOptions(), func(_ context.Context, b []byte) ([]byte, error) { close(entered); <-release; return b, nil }, obs)
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	ctx, capture := rpctrace.WithTerminalCapture(ctx)
	done := make(chan error, 1)
	go func() { _, err := traceCall(ctx, c, 0); done <- err }()
	traceWait(t, entered)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("cancel error changed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled call did not return")
	}
	if got := capture.Snapshot(); got.State != rpctrace.TerminalTraceBound || got.TraceID == "" || got.Unbound {
		t.Fatalf("canceled wire call=%+v", got)
	}
}
