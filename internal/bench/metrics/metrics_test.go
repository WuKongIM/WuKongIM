package metrics

import (
	"encoding/json"
	"errors"
	"fmt"
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

func TestAggregateHistogramPercentilesAreMaxWorkerPercentiles(t *testing.T) {
	agg, err := Aggregate([]WorkerSnapshot{
		{WorkerID: "w1", Metrics: SnapshotData{Histograms: map[string]HistogramSummary{"sendack_latency_seconds": {Count: 10, P50Seconds: 0.010, P95Seconds: 0.020, P99Seconds: 0.030}}}},
		{WorkerID: "w2", Metrics: SnapshotData{Histograms: map[string]HistogramSummary{"sendack_latency_seconds": {Count: 10, P50Seconds: 0.040, P95Seconds: 0.050, P99Seconds: 0.060}}}},
	})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	hist := agg.Histograms["sendack_latency_seconds"]
	if hist.P50Seconds != 0.040 || hist.P95Seconds != 0.050 || hist.P99Seconds != 0.060 {
		t.Fatalf("histogram percentiles = p50:%v p95:%v p99:%v, want max worker percentiles", hist.P50Seconds, hist.P95Seconds, hist.P99Seconds)
	}
}

func TestAggregateBoundsErrorSamplesGlobally(t *testing.T) {
	workers := make([]WorkerSnapshot, 0, 40)
	for i := 0; i < 40; i++ {
		workers = append(workers, WorkerSnapshot{
			WorkerID: fmt.Sprintf("w%02d", i),
			Metrics:  SnapshotData{Errors: []ErrorSample{{Name: "send", Message: fmt.Sprintf("err-%02d", i)}}},
		})
	}

	agg, err := Aggregate(workers)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if len(agg.Errors) != 32 {
		t.Fatalf("len(errors) = %d, want 32", len(agg.Errors))
	}
	if agg.Errors[0].Message != "err-08" || agg.Errors[31].Message != "err-39" {
		t.Fatalf("unexpected bounded errors: first=%q last=%q", agg.Errors[0].Message, agg.Errors[31].Message)
	}
}

func TestParsePrometheusTextAndAnalyzeWukongIMV2GatewayPressure(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
# HELP wukongim_gateway_async_send_queue_depth queued SEND frames
wukongim_gateway_async_send_queue_depth{node_id="1",node_name="node-1"} 0
wukongim_gateway_async_send_queue_capacity{node_id="1",node_name="node-1"} 100
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{node_id="1",node_name="node-1",protocol="wkproto",le="0.01"} 0
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{node_id="1",node_name="node-1",protocol="wkproto",le="0.05"} 0
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{node_id="1",node_name="node-1",protocol="wkproto",le="0.1"} 0
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{node_id="1",node_name="node-1",protocol="wkproto",le="+Inf"} 0
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="store_append"} 0
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth{node_id="1",node_name="node-1"} 70
wukongim_gateway_async_send_queue_capacity{node_id="1",node_name="node-1"} 100
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{node_id="1",node_name="node-1",protocol="wkproto",le="0.01"} 10
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{node_id="1",node_name="node-1",protocol="wkproto",le="0.05"} 80
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{node_id="1",node_name="node-1",protocol="wkproto",le="0.1"} 100
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{node_id="1",node_name="node-1",protocol="wkproto",le="+Inf"} 100
wukongim_channelv2_reactor_mailbox_depth{node_id="1",node_name="node-1",reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{node_id="1",node_name="node-1",pool="store_append"} 0
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMV2Prometheus(before, after)
	if report.Classification != WukongIMV2BottleneckGateway {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMV2BottleneckGateway, report)
	}
	if report.GatewayQueueRatio != 0.7 {
		t.Fatalf("gateway queue ratio = %v, want 0.7", report.GatewayQueueRatio)
	}
	if report.GatewayDispatchWaitP99Seconds <= 0 {
		t.Fatalf("gateway dispatch p99 = %v, want > 0", report.GatewayDispatchWaitP99Seconds)
	}
}

func TestAnalyzeWukongIMV2PrometheusClassifiesChannelV2Pressure(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_channelv2_append_duration_seconds_bucket{commit_mode="local",le="0.01"} 0
wukongim_channelv2_append_duration_seconds_bucket{commit_mode="local",le="0.05"} 0
wukongim_channelv2_append_duration_seconds_bucket{commit_mode="local",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_resolve",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_resolve",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_resolve",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append",result="ok",le="+Inf"} 0
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 9
wukongim_channelv2_worker_queue_depth{pool="store_append"} 3
wukongim_channelv2_append_duration_seconds_bucket{commit_mode="local",le="0.01"} 10
wukongim_channelv2_append_duration_seconds_bucket{commit_mode="local",le="0.05"} 100
wukongim_channelv2_append_duration_seconds_bucket{commit_mode="local",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_resolve",result="ok",le="0.01"} 20
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_resolve",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_resolve",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="0.01"} 5
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append",result="ok",le="0.01"} 10
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append",result="ok",le="+Inf"} 100
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMV2Prometheus(before, after)
	if report.Classification != WukongIMV2BottleneckChannelV2 {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMV2BottleneckChannelV2, report)
	}
	if report.ChannelV2ReactorMailboxDepthMax != 9 {
		t.Fatalf("reactor mailbox depth = %v, want 9", report.ChannelV2ReactorMailboxDepthMax)
	}
	if report.ChannelV2WorkerQueueDepthMax != 3 {
		t.Fatalf("worker queue depth = %v, want 3", report.ChannelV2WorkerQueueDepthMax)
	}
	if report.ChannelV2MetaResolveP99Seconds <= 0 || report.ChannelV2MetaApplyP99Seconds <= 0 || report.ChannelV2RuntimeAppendP99Seconds <= 0 {
		t.Fatalf("channel stage p99s not parsed: %+v", report)
	}
}

func TestAnalyzeWukongIMV2PrometheusClassifiesStorageCommitPressure(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_storage_commit_queue_depth{store="message"} 0
wukongim_storage_commit_batch_records_bucket{store="message",le="1"} 0
wukongim_storage_commit_batch_records_bucket{store="message",le="4"} 0
wukongim_storage_commit_batch_records_bucket{store="message",le="+Inf"} 0
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="0.01"} 0
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="0.05"} 0
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="+Inf"} 0
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_storage_commit_queue_depth{store="message"} 5
wukongim_storage_commit_batch_records_bucket{store="message",le="1"} 10
wukongim_storage_commit_batch_records_bucket{store="message",le="4"} 100
wukongim_storage_commit_batch_records_bucket{store="message",le="+Inf"} 100
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="0.01"} 10
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="0.05"} 100
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="+Inf"} 100
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMV2Prometheus(before, after)
	if report.Classification != WukongIMV2BottleneckStorageCommit {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMV2BottleneckStorageCommit, report)
	}
	if report.StorageCommitQueueDepthMax != 5 {
		t.Fatalf("storage commit queue depth = %v, want 5", report.StorageCommitQueueDepthMax)
	}
	if report.StorageCommitP99Seconds <= 0 {
		t.Fatalf("storage commit p99 = %v, want > 0", report.StorageCommitP99Seconds)
	}
	if report.StorageCommitBatchRecordsP50 <= 0 {
		t.Fatalf("storage commit batch records p50 = %v, want > 0", report.StorageCommitBatchRecordsP50)
	}
}
