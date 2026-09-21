package app

import (
	"testing"
	"time"

	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestTransportAggregatesKeepExactCountsSeparateFromLatencySamples(t *testing.T) {
	reg := obsmetrics.NewWithLogicalSlots(1, "node-1", 256)
	observer := transportMetricsObserver{metrics: reg}
	event := transport.Event{Name: "service_task", ServiceID: 1, Result: "ok", Count: 64, Duration: time.Millisecond}
	// A counter batch carries only its explicitly supplied latency observation.
	observer.ObserveTransport(event)
	observer.ObserveTransport(transport.Event{Name: "client_rpc", NodeID: 2, ServiceID: 1, Result: "ok", Count: 64, Duration: 2 * time.Millisecond})
	observer.ObserveTransport(transport.Event{Name: "write_batch", Count: 10, Items: 4, Bytes: 2560, Capacity: 4})
	observer.ObserveTransport(transport.Event{Name: "observer_dropped", Count: 3})
	families, err := reg.PrometheusRegistry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]float64{
		"wukongim_transport_rpc_total":                       64,
		"wukongim_transport_rpc_client_total":                64,
		"wukongim_transport_write_batches_total":             10,
		"wukongim_transport_write_frames_total":              40,
		"wukongim_transport_write_payload_bytes_total":       2560,
		"wukongim_transport_write_frame_limit_batches_total": 10,
		"wukongim_transport_observer_dropped_total":          3,
	} {
		family := requireAppMetricFamily(t, families, name)
		if got := family.Metric[0].GetCounter().GetValue(); got != want {
			t.Fatalf("%s=%v want %v", name, got, want)
		}
	}
	duration := requireAppMetricFamily(t, families, "wukongim_transport_rpc_duration_seconds")
	if got := duration.Metric[0].GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("latency count=%d; counter aggregation must not invent samples", got)
	}
}
