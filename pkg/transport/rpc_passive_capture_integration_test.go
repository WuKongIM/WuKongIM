//go:build integration

package transport

import (
	"context"
	"errors"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"net"
	"testing"
	"time"
)

// F1/F2: observing a terminal identity must not force or perturb native sampling.
func TestRPCPassiveTerminalCapturePreservesSamplingAndActiveControl(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "native_every3"}[enabled], func(t *testing.T) {
			obs := &traceObserver{}
			every := uint32(0)
			if enabled {
				every = 3
			}
			c, s := tracePair(t, enabled, every, traceOptions(), func(_ context.Context, b []byte) ([]byte, error) { return b, nil }, obs)
			identities := make([]rpctrace.TerminalIdentity, 6)
			for i := range identities {
				ctx, cap := rpctrace.WithPassiveTerminalCapture(context.Background())
				body, err := traceCall(ctx, c, 0)
				if err != nil || string(body) != "unchanged" {
					t.Fatalf("business result %q/%v", body, err)
				}
				identities[i] = cap.Snapshot()
				want := rpctrace.TerminalTraceUnknown
				if !enabled {
					want = rpctrace.TerminalTraceRecorderDisabled
				} else if i%3 == 0 {
					want = rpctrace.TerminalTraceBound
				}
				if identities[i].State != want {
					t.Fatalf("call%d passive state=%v want%v", i, identities[i], want)
				}
			}
			// Existing explicit active capture remains active even off the native cadence.
			if enabled {
				if _, err := traceCall(context.Background(), c, 0); err != nil {
					t.Fatal(err)
				}
				ctx, cap := rpctrace.WithTerminalCapture(context.Background())
				if _, err := traceCall(ctx, c, 0); err != nil {
					t.Fatal(err)
				}
				if cap.Snapshot().State != rpctrace.TerminalTraceBound {
					t.Fatal("active compatibility changed")
				}
			}
			c.Stop()
			s.Stop()
			count := 0
			for _, x := range obs.snapshot() {
				if x.Side == rpctrace.SideClient {
					count++
				}
			}
			want := 0
			if enabled {
				want = 4
			}
			if count != want {
				t.Fatalf("passive sampling changed native trace count=%d want%d", count, want)
			}
		})
	}
}

func TestRPCPassiveTerminalCaptureMissingExhaustedAndCancellation(t *testing.T) {
	obs := &traceObserver{}
	c, err := NewClient(ClientConfig{RPCTraceEvery: 1, Observer: obs, Discovery: traceDiscovery("unused"), Dialer: func(string, string, time.Duration) (net.Conn, error) { return nil, errors.New("fixture dial failure") }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	var held []*rpctrace.Trace
	for range 128 {
		held = append(held, c.traceRecorder.Start(context.Background(), rpctrace.SideClient, 1))
	}
	ctx, exhausted := rpctrace.WithPassiveTerminalCapture(context.Background())
	if _, err = traceCall(ctx, c, 0); !errors.Is(err, ErrDialFailed) {
		t.Fatal(err)
	}
	if exhausted.Snapshot().State != rpctrace.TerminalTraceRecorderExhausted {
		t.Fatalf("exhausted=%+v", exhausted.Snapshot())
	}
	for _, trace := range held {
		trace.Finish(rpctrace.ResultOK)
	}
	ctx, unbound := rpctrace.WithPassiveTerminalCapture(context.Background())
	if _, err = traceCall(ctx, c, 0); !errors.Is(err, ErrDialFailed) {
		t.Fatal(err)
	}
	if unbound.Snapshot().State != rpctrace.TerminalTraceUnbound {
		t.Fatalf("unbound=%+v", unbound.Snapshot())
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	pair, _ := tracePair(t, true, 1, traceOptions(), func(_ context.Context, b []byte) ([]byte, error) { close(entered); <-release; return b, nil }, &traceObserver{})
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, cap := rpctrace.WithPassiveTerminalCapture(parent)
	done := make(chan error, 1)
	go func() { _, e := traceCall(ctx, pair, 0); done <- e }()
	traceWait(t, entered)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCanceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled passive call failed to join")
	}
	if cap.Snapshot().State != rpctrace.TerminalTraceBound {
		t.Fatalf("canceled identity=%+v", cap.Snapshot())
	}
}

// A passive wrapper must preserve an existing active request and its terminal output.
func TestRPCPassiveTerminalCapturePreservesNestedActive(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "native_every3"}[enabled], func(t *testing.T) {
			every := uint32(0)
			if enabled {
				every = 3
			}
			c, _ := tracePair(t, enabled, every, traceOptions(), func(_ context.Context, b []byte) ([]byte, error) { return b, nil }, &traceObserver{})
			// Consume the native sampled position before the explicitly active call.
			if _, err := traceCall(context.Background(), c, 0); err != nil {
				t.Fatal(err)
			}
			ctx, active := rpctrace.WithTerminalCapture(context.Background())
			var previous rpctrace.TerminalIdentity
			for i := range 2 {
				wrapped, first := rpctrace.WithPassiveTerminalCapture(ctx)
				wrapped, passive := rpctrace.WithPassiveTerminalCapture(wrapped)
				_ = first // Only the nearest passive wrapper owns this exact call's observation.
				if _, err := traceCall(wrapped, c, 0); err != nil {
					t.Fatal(err)
				}
				got := passive.Snapshot()
				want := rpctrace.TerminalTraceRecorderDisabled
				if enabled {
					want = rpctrace.TerminalTraceBound
				}
				if got.State != want || active.Snapshot().State != want {
					t.Fatalf("call%d passive=%+v active=%+v want%v", i, got, active.Snapshot(), want)
				}
				if i == 0 {
					previous = got
				} else if enabled && (got.TraceID == previous.TraceID || active.Snapshot() != previous) {
					t.Fatal("fresh capture reused an identity or overwrote the single-use active result")
				}
			}
		})
	}
}
