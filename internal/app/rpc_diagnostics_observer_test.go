package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestRPCDispatchDiagnosticsObserverStoreQueryOwnsSnapshot(t *testing.T) {
	store := obsdiagnostics.NewStore(obsdiagnostics.StoreOptions{Capacity: 2})
	observer := &rpcDiagnosticsObserver{store: store, nodeID: 42}
	sample := &rpctrace.Sample{TraceID: "token/dispatch", Side: rpctrace.SideServer, ServiceID: 14, Operation: 11, Result: rpctrace.ResultTimeout,
		Dispatch: &rpctrace.DispatchSample{KnownMask: 1 << rpctrace.DispatchWorkerEnter, TerminalPath: rpctrace.DispatchPathWorkerRejected}}
	observer.ObserveTransport(transport.Event{Name: "rpc_trace", RPCTrace: sample})
	sample.Dispatch.KnownMask = 0
	query := obsdiagnostics.Query{TraceID: "token/dispatch", Stage: obsdiagnostics.StageRPCServer}
	first := store.Query(context.Background(), query)
	if len(first.Events) != 1 || first.Events[0].RPC.Dispatch == nil || first.Events[0].RPC.Dispatch.KnownMask != 1<<rpctrace.DispatchWorkerEnter {
		t.Fatal("observer/store retained live dispatch backing")
	}
	first.Events[0].RPC.Dispatch.Offsets[rpctrace.DispatchWorkerEnter] = time.Hour
	first.Events[0].RPC.Dispatch.TerminalPath = rpctrace.DispatchPathHandler
	second := store.Query(context.Background(), query)
	if second.Events[0].RPC.Dispatch.Offsets[rpctrace.DispatchWorkerEnter] != 0 || second.Events[0].RPC.Dispatch.TerminalPath != rpctrace.DispatchPathWorkerRejected {
		t.Fatal("query exposed mutable dispatch evidence")
	}
	data, err := json.Marshal(second)
	if err != nil || !strings.Contains(string(data), `"dispatch":`) || !strings.Contains(string(data), `"terminal_path":"worker_rejected"`) {
		t.Fatalf("dispatch was lost in diagnostics JSON: %s / %v", data, err)
	}
}

func TestRPCDiagnosticsObserverRetainsEndpointSample(t *testing.T) {
	for _, tc := range []struct {
		side   rpctrace.Side
		result rpctrace.Result
		stage  obsdiagnostics.Stage
		want   obsdiagnostics.Result
	}{
		{rpctrace.SideClient, rpctrace.ResultOK, obsdiagnostics.StageRPCClient, obsdiagnostics.ResultOK},
		{rpctrace.SideClient, rpctrace.ResultError, obsdiagnostics.StageRPCClient, obsdiagnostics.ResultError},
		{rpctrace.SideServer, rpctrace.ResultTimeout, obsdiagnostics.StageRPCServer, obsdiagnostics.ResultTimeout},
		{rpctrace.SideClient, rpctrace.ResultCanceled, obsdiagnostics.StageRPCClient, obsdiagnostics.ResultCanceled},
		{rpctrace.SideServer, rpctrace.ResultBusy, obsdiagnostics.StageRPCServer, obsdiagnostics.ResultError},
		{rpctrace.SideServer, rpctrace.ResultStopped, obsdiagnostics.StageRPCServer, obsdiagnostics.ResultError},
		{rpctrace.SideServer, rpctrace.ResultUnfinished, obsdiagnostics.StageRPCServer, obsdiagnostics.ResultPartial},
	} {
		t.Run(string(tc.side)+"/"+string(tc.result), func(t *testing.T) {
			store := obsdiagnostics.NewStore(obsdiagnostics.StoreOptions{Capacity: 2})
			observer := &rpcDiagnosticsObserver{store: store, nodeID: 42}
			sample := &rpctrace.Sample{TraceID: "token/1", Side: tc.side, ServiceID: 77, At: time.Unix(10, 0), Duration: time.Millisecond, Result: tc.result,
				Spans: []rpctrace.SpanSample{{Stage: rpctrace.StageHandler, Duration: time.Microsecond, Result: tc.result}}}
			peer := uint64(7)
			if tc.side == rpctrace.SideServer {
				peer = 0
			}
			observer.ObserveTransport(transport.Event{Name: "rpc_trace", NodeID: transport.NodeID(peer), RPCTrace: sample})
			sample.Spans[0].Duration = time.Hour
			got := store.Query(context.Background(), obsdiagnostics.Query{Stage: tc.stage, TraceID: "token/1"})
			if len(got.Events) != 1 {
				t.Fatalf("retained events = %#v", got.Events)
			}
			e := got.Events[0]
			if e.NodeID != 42 || e.PeerNodeID != peer || e.Result != tc.want || e.Duration != time.Millisecond || !e.At.Equal(time.Unix(10, 0)) || e.RPC == nil || e.RPC.Spans[0].Duration != time.Microsecond {
				t.Fatalf("RPC event mapping = %#v", e)
			}
		})
	}
}

func TestRPCDiagnosticsObserverIgnoresNonSamples(t *testing.T) {
	store := obsdiagnostics.NewStore(obsdiagnostics.StoreOptions{Capacity: 2})
	observer := &rpcDiagnosticsObserver{store: store, nodeID: 1}
	observer.ObserveTransport(transport.Event{Name: "rpc_trace"})
	observer.ObserveTransport(transport.Event{Name: "client_rpc", RPCTrace: &rpctrace.Sample{Side: rpctrace.SideClient}})
	observer.ObserveTransport(transport.Event{Name: "rpc_trace", RPCTrace: &rpctrace.Sample{Side: "invalid"}})
	(&rpcDiagnosticsObserver{}).ObserveTransport(transport.Event{Name: "rpc_trace", RPCTrace: &rpctrace.Sample{Side: rpctrace.SideClient}})
	if got := store.Query(context.Background(), obsdiagnostics.Query{}); len(got.Events) != 0 {
		t.Fatal("non-sample event was retained")
	}
}

func TestRPCDiagnosticsWiringIndependentOfMetrics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		every   uint32
		want    uint32
	}{
		{"disabled default", true, 0, 0},
		{"selected samples", true, 32, 32},
		{"global disabled", false, 32, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{cfg: Config{Observability: ObservabilityConfig{Diagnostics: DiagnosticsConfig{Enabled: tc.enabled, RPCSampleEvery: tc.every}}}}
			defer func() {
				if a.diagnosticsRestore != nil {
					a.diagnosticsRestore()
				}
			}()
			previousCalls := 0
			c := cluster.Config{NodeID: 42}
			c.Transport.Observer = rpcObserverCallback(func(transport.Event) { previousCalls++ })
			a.configureObservability(&c)
			if c.Transport.RPCTraceEvery != tc.want || a.metrics != nil {
				t.Fatalf("transport interval=%d, want=%d; metrics=%p", c.Transport.RPCTraceEvery, tc.want, a.metrics)
			}
			if c.Transport.Observer != nil {
				c.Transport.Observer.ObserveTransport(transport.Event{Name: "rpc_trace", RPCTrace: &rpctrace.Sample{TraceID: "token/1", Side: rpctrace.SideClient, Result: rpctrace.ResultOK}})
			}
			got := a.diagnostics.Query(context.Background(), obsdiagnostics.Query{Stage: obsdiagnostics.StageRPCClient})
			wantEvents := 0
			if tc.want != 0 {
				wantEvents = 1
			}
			if len(got.Events) != wantEvents {
				t.Fatalf("retained events=%d, want=%d", len(got.Events), wantEvents)
			}
			if previousCalls != 1 {
				t.Fatal("existing transport observer was replaced")
			}
		})
	}
}

type rpcObserverCallback func(transport.Event)

func (f rpcObserverCallback) ObserveTransport(event transport.Event) { f(event) }
