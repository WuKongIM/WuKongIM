package metrics

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLabelsValidateRejectsForbiddenUID(t *testing.T) {
	err := Labels{"worker_id": "w1", "uid": "u1"}.Validate()
	if err == nil {
		t.Fatal("expected uid label to be rejected")
	}
	if got := err.Error(); !strings.Contains(got, "uid") {
		t.Fatalf("expected error to mention uid, got %q", got)
	}
}

func TestRegistrySnapshotIncludesCountersGaugesAndHistogramSummaries(t *testing.T) {
	r := NewRegistry()
	r.AddCounter("sendack_total", Labels{"worker_id": "w1", "phase": "run"}, 2)
	r.SetGauge("active_connections", Labels{"worker_id": "w1"}, 10)
	r.AddGauge("active_connections", Labels{"worker_id": "w1"}, 2)
	r.ObserveLatency("sendack_latency_seconds", Labels{"worker_id": "w1"}, 10*time.Millisecond)
	r.ObserveLatency("sendack_latency_seconds", Labels{"worker_id": "w1"}, 30*time.Millisecond)

	snap := r.Collect()
	counterKey := `sendack_total{phase=run,worker_id=w1}`
	if snap.Counters[counterKey] != 2 {
		t.Fatalf("counter = %d, want 2", snap.Counters[counterKey])
	}
	gaugeKey := `active_connections{worker_id=w1}`
	if snap.Gauges[gaugeKey] != 12 {
		t.Fatalf("gauge = %v, want 12", snap.Gauges[gaugeKey])
	}
	histKey := `sendack_latency_seconds{worker_id=w1}`
	hist := snap.Histograms[histKey]
	if hist.Count != 2 || hist.MinSeconds != 0.01 || hist.MaxSeconds != 0.03 || hist.P50Seconds != 0.01 || hist.P99Seconds != 0.03 {
		t.Fatalf("unexpected histogram summary: %+v", hist)
	}
	if _, err := json.Marshal(snap); err != nil {
		t.Fatalf("snapshot should be JSON friendly: %v", err)
	}
}

func TestRegistryBoundsErrorSamples(t *testing.T) {
	r := NewRegistry()
	r.SetMaxErrorSamples(2)
	r.RecordErrorSample("send", errors.New("first"))
	r.RecordErrorSample("send", errors.New("second"))
	r.RecordErrorSample("send", errors.New("third"))

	samples := r.ErrorSamples()
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}
	if samples[0].Message != "second" || samples[1].Message != "third" {
		t.Fatalf("unexpected samples: %+v", samples)
	}
}

func TestAggregateRejectsUnsafeLabelsFromWorkerSnapshots(t *testing.T) {
	_, err := Aggregate([]WorkerSnapshot{{
		WorkerID: "w1",
		Metrics: SnapshotData{Counters: map[string]uint64{
			`sendack_total{uid=u1,worker_id=w1}`: 1,
		}},
	}})
	if err == nil {
		t.Fatal("expected unsafe worker snapshot labels to be rejected")
	}
	if got := err.Error(); !strings.Contains(got, "uid") {
		t.Fatalf("expected error to mention uid, got %q", got)
	}
}
