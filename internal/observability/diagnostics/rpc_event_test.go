package diagnostics

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func rpcStoredSample() *rpctrace.Sample {
	return &rpctrace.Sample{TraceID: "token/1", Side: rpctrace.SideClient, ServiceID: 77,
		At: time.Unix(10, 0), Duration: time.Millisecond, Result: rpctrace.ResultOK,
		Spans: []rpctrace.SpanSample{{Stage: rpctrace.StagePeerAcquire, Duration: time.Microsecond, Result: rpctrace.ResultOK}}}
}

func TestRPCDiagnosticsStoreOwnsRecordAndQuerySamples(t *testing.T) {
	s := NewStore(StoreOptions{Capacity: 2})
	input := rpcStoredSample()
	s.Record(Event{TraceID: input.TraceID, Stage: StageRPCClient, RPC: input})
	input.Spans[0].Duration = time.Hour
	input.ServiceID = 999
	first := s.Query(context.Background(), Query{Stage: StageRPCClient})
	if len(first.Events) != 1 || first.Events[0].RPC == nil {
		t.Fatalf("missing retained RPC: %#v", first.Events)
	}
	if first.Events[0].RPC.Spans[0].Duration != time.Microsecond || first.Events[0].RPC.ServiceID != 77 {
		t.Fatal("Record retained caller-owned sample backing")
	}
	first.Events[0].RPC.Spans[0].Duration = time.Minute
	first.Events[0].RPC.TraceID = "mutated"
	second := s.Query(context.Background(), Query{TraceID: "token/1"})
	if len(second.Events) != 1 || second.Events[0].RPC.Spans[0].Duration != time.Microsecond || second.Events[0].RPC.TraceID != "token/1" {
		t.Fatal("Query exposed the store's mutable RPC sample")
	}
}

func TestRPCDiagnosticsStoreStageFilteringAndBoundedRing(t *testing.T) {
	s := NewStore(StoreOptions{Capacity: 2})
	s.Record(Event{Stage: StageRPCClient, TraceID: "overwritten", RPC: rpcStoredSample()})
	server := rpcStoredSample()
	server.Side = rpctrace.SideServer
	s.Record(Event{Stage: StageRPCServer, TraceID: server.TraceID, RPC: server, FromUID: "private"})
	s.Record(Event{Stage: StageRPCClient, TraceID: "last"})
	client := s.Query(context.Background(), Query{Stage: StageRPCClient})
	if len(client.Events) != 1 || client.Events[0].RPC != nil {
		t.Fatal("nil RPC payload was not retained as nil")
	}
	if got := s.Query(context.Background(), Query{TraceID: "overwritten"}); len(got.Events) != 0 {
		t.Fatal("RPC samples escaped the existing bounded ring")
	}
	got := s.Query(context.Background(), Query{Stage: StageRPCServer})
	if len(got.Events) != 1 || got.Events[0].RPC == nil || got.Events[0].FromUID != "" {
		t.Fatalf("server stage query = %#v", got.Events)
	}
	data, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(data), `"rpc":`) || !strings.Contains(string(data), `"service_id":77`) || strings.Contains(string(data), "private") {
		t.Fatalf("RPC response serialization failed: %s / %v", data, err)
	}
}
