//go:build integration

package transport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

type receivePrefixView struct {
	Duration         time.Duration `json:"duration"`
	ObserverDuration time.Duration `json:"observer_duration"`
}

func transportReceivePrefix(t *testing.T, sample rpctrace.Sample) *receivePrefixView {
	t.Helper()
	data, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Prefix *receivePrefixView `json:"receive_prefix"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	return view.Prefix
}

func transportReceiveStages(sample rpctrace.Sample) int {
	count := 0
	for _, span := range sample.Spans {
		switch string(span.Stage) {
		case "response_receive_observer", "response_dispatch", "response_resume":
			count++
		}
	}
	return count
}

func TestRPCReceiveBridgePublicPairPreservesIdentityAndBody(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			observer := &traceObserver{}
			every := uint32(0)
			if enabled {
				every = 1
			}
			entered := make(chan time.Time, 1)
			client, server := tracePair(t, enabled, every, traceOptions(), func(ctx context.Context, body []byte) ([]byte, error) {
				entered <- time.Now()
				return body, nil
			}, observer)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			body, err := traceCall(ctx, client, 0)
			if err != nil || string(body) != "unchanged" {
				t.Fatalf("business response changed: %q %v", body, err)
			}
			handlerAt := <-entered
			client.Stop()
			server.Stop()
			samples := observer.snapshot()
			if !enabled {
				if len(samples) != 0 {
					t.Fatalf("disabled tracing emitted %d samples", len(samples))
				}
				return
			}
			if len(samples) != 2 {
				t.Fatalf("sample count=%d want client+server", len(samples))
			}
			var clientID, serverID string
			for _, sample := range samples {
				if sample.Unbound || sample.Result != rpctrace.ResultOK {
					t.Fatal("successful pair is not bound and complete")
				}
				if sample.Side == rpctrace.SideClient {
					clientID = sample.TraceID
					if transportReceiveStages(sample) != 3 {
						t.Fatal("actual public client response lacks its three receive stages")
					}
					if transportReceivePrefix(t, sample) != nil {
						t.Fatal("client acquired a server pretrace prefix")
					}
				} else {
					serverID = sample.TraceID
					prefix := transportReceivePrefix(t, sample)
					if prefix == nil {
						t.Fatal("actual public server request lacks verified receive prefix")
					}
					if prefix.ObserverDuration < 0 || prefix.Duration < prefix.ObserverDuration {
						t.Fatalf("invalid nested receive prefix: %+v", prefix)
					}
					if sample.At.After(handlerAt) {
						t.Fatal("existing server trace origin moved after handler start")
					}
					for _, span := range sample.Spans {
						if span.Offset < 0 {
							t.Fatal("pretrace prefix changed existing span coordinate")
						}
					}
				}
			}
			if clientID == "" || clientID != serverID {
				t.Fatal("receive bridge changed negotiated same-request identity")
			}
			if client.traceRecorder.Active() != 0 || server.traceRecorder.Active() != 0 {
				t.Fatal("receive bridge retained recorder ownership")
			}
		})
	}
}

func TestRPCReceiveBridgeRecorderDropDoesNotFabricateServerPrefix(t *testing.T) {
	observer := &traceObserver{}
	client, server := tracePair(t, true, 1, traceOptions(), func(ctx context.Context, body []byte) ([]byte, error) {
		if rpctrace.From(ctx) != nil {
			t.Error("full recorder admitted an extra server trace")
		}
		return body, nil
	}, observer)
	var held []*rpctrace.Trace
	for i := 0; i < 128; i++ {
		trace := server.traceRecorder.Start(context.Background(), rpctrace.SideServer, 1)
		if trace == nil {
			t.Fatal("recorder fixture failed to occupy existing 128 credits")
		}
		held = append(held, trace)
	}
	t.Cleanup(func() {
		for _, trace := range held {
			trace.Finish(rpctrace.ResultOK)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	body, err := traceCall(ctx, client, 0)
	if err != nil || string(body) != "unchanged" {
		t.Fatalf("observation admission changed RPC result: %q %v", body, err)
	}
	for _, trace := range held {
		trace.Finish(rpctrace.ResultOK)
	}
	client.Stop()
	server.Stop()
	samples := observer.snapshot()
	if len(samples) != 1 || samples[0].Side != rpctrace.SideClient || samples[0].Unbound {
		t.Fatalf("recorder drop produced unexpected endpoint samples: count=%d", len(samples))
	}
	if transportReceiveStages(samples[0]) != 3 || transportReceivePrefix(t, samples[0]) != nil {
		t.Fatal("client receive observation missing or server prefix fabricated on dropped endpoint")
	}
	if server.traceRecorder.Dropped() != 1 || server.traceRecorder.Active() != 0 || client.traceRecorder.Active() != 0 {
		t.Fatal("recorder drop or ownership accounting changed")
	}
}
