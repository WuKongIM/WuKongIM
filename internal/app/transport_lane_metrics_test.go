package app

import (
	"testing"

	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestTransportMetricsKeepLaneBytesSeparateFromTotal(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	observer := &transportMetricsObserver{metrics: reg}
	for _, event := range []transport.Event{
		{Name: "sent_bytes", Priority: transport.PriorityRaft, Kind: transport.FrameKindRPCRequest, Bytes: 100, Count: 3},
		{Name: "sent_bytes", Priority: transport.PriorityRaft, Kind: transport.FrameKindRPCResponse, Bytes: 25},
		{Name: "sent_bytes", Priority: transport.PriorityRPC, Kind: transport.FrameKindRPCRequest, Bytes: 50},
		{Name: "received_bytes", Priority: transport.PriorityRaft, Kind: transport.FrameKindRPCRequest, Bytes: 150},
		{Name: "sent_bytes", Priority: transport.Priority(255), Kind: transport.FrameKindData, Bytes: 7},
	} {
		observer.ObserveTransport(event)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	lanes := requireAppMetricFamily(t, families, "wukongim_transport_lane_payload_bytes_total")
	if len(lanes.Metric) != 10 {
		t.Fatalf("lane metric cardinality = %d, want ten fixed series", len(lanes.Metric))
	}
	for _, tc := range []struct {
		direction, priority string
		want                float64
	}{
		{"send", "raft", 125}, {"receive", "raft", 150}, {"send", "rpc", 50},
		{"send", "none", 7}, {"send", "bulk", 0}, {"receive", "control", 0},
	} {
		got := findAppMetricByLabels(t, lanes, map[string]string{"direction": tc.direction, "priority": tc.priority}).GetCounter().GetValue()
		if got != tc.want {
			t.Fatalf("%s/%s = %v, want %v", tc.direction, tc.priority, got, tc.want)
		}
	}
	old := requireAppMetricFamily(t, families, "wukongim_transport_sent_bytes_total")
	var total float64
	for _, m := range old.Metric {
		total += m.GetCounter().GetValue()
	}
	if total != 182 {
		t.Fatalf("legacy byte total changed: %v", total)
	}
}
