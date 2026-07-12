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

func TestLabelsValidateAcceptsLowCardinalityReason(t *testing.T) {
	if err := (Labels{"phase": "run", "reason": "pending_window_expired"}).Validate(); err != nil {
		t.Fatalf("expected reason label to be accepted: %v", err)
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

func TestParsePrometheusTextAndAnalyzeWukongIMGatewayPressure(t *testing.T) {
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

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.Classification != WukongIMBottleneckGateway {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMBottleneckGateway, report)
	}
	if report.GatewayQueueRatio != 0.7 {
		t.Fatalf("gateway queue ratio = %v, want 0.7", report.GatewayQueueRatio)
	}
	if report.GatewayDispatchWaitP99Seconds <= 0 {
		t.Fatalf("gateway dispatch p99 = %v, want > 0", report.GatewayDispatchWaitP99Seconds)
	}
}

func TestParsePrometheusTextAllowsSpacesInsideQuotedLabelValues(t *testing.T) {
	snapshot, err := ParsePrometheusText(strings.NewReader(`
wukongim_runtime_pool_admission_total{component="transport",queue="controller raft",result="ok"} 757
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText: %v", err)
	}
	if len(snapshot.Samples) != 1 {
		t.Fatalf("len(samples) = %d, want 1", len(snapshot.Samples))
	}
	sample := snapshot.Samples[0]
	if sample.Name != "wukongim_runtime_pool_admission_total" {
		t.Fatalf("name = %q", sample.Name)
	}
	if sample.Labels["queue"] != "controller raft" {
		t.Fatalf("queue label = %q", sample.Labels["queue"])
	}
	if sample.Value != 757 {
		t.Fatalf("value = %v, want 757", sample.Value)
	}
}

func TestAnalyzeWukongIMPrometheusReportsMessageAppendErrorCounters(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_message_append_errors_total{path="channelplane",class="route_not_ready"} 2
wukongim_message_append_errors_total{path="channelplane",class="timeout"} 1
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_message_append_errors_total{path="channelplane",class="route_not_ready"} 5
wukongim_message_append_errors_total{path="channelplane",class="timeout"} 3
wukongim_message_append_errors_total{path="channelplane",class="append_failed"} 4
wukongim_message_append_errors_total{path="channelplane",class="short_result"} 1
wukongim_message_append_errors_total{path="channelplane",class="invalid_config"} 2
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.MessageAppendErrorCount != 12 {
		t.Fatalf("message append errors = %v, want 12", report.MessageAppendErrorCount)
	}
	if report.MessageAppendRouteNotReadyErrCount != 3 || report.MessageAppendTimeoutErrCount != 2 || report.MessageAppendAppendFailedErrCount != 4 || report.MessageAppendShortResultErrCount != 1 || report.MessageAppendInvalidConfigErrCount != 2 {
		t.Fatalf("message append class counters = route:%v timeout:%v append_failed:%v short_result:%v invalid_config:%v", report.MessageAppendRouteNotReadyErrCount, report.MessageAppendTimeoutErrCount, report.MessageAppendAppendFailedErrCount, report.MessageAppendShortResultErrCount, report.MessageAppendInvalidConfigErrCount)
	}
	if !strings.Contains(strings.Join(report.Reasons, "\n"), "message append route_not_ready errors were observed") {
		t.Fatalf("expected message append reason, got %#v", report.Reasons)
	}
}

func TestAnalyzeWukongIMPrometheusReportsGatewaySendackCounters(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_sendacks_total{reason="system_error",source="batch_result_error",class="timeout"} 1
wukongim_gateway_sendacks_total{reason="success",source="batch_result",class="none"} 10
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_sendacks_total{reason="system_error",source="batch_result_error",class="timeout"} 4
wukongim_gateway_sendacks_total{reason="system_error",source="batch_result_error",class="canceled"} 2
wukongim_gateway_sendacks_total{reason="system_error",source="batch_missing_request_context",class="missing_request_context"} 2
wukongim_gateway_sendacks_total{reason="success",source="batch_result",class="none"} 30
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.GatewaySendackSystemErrorCount != 7 {
		t.Fatalf("gateway system sendacks = %v, want 7", report.GatewaySendackSystemErrorCount)
	}
	if report.GatewaySendackBatchResultErrorCount != 5 || report.GatewaySendackBatchMissingRequestContextCount != 2 {
		t.Fatalf("gateway system sources = result_err:%v missing_ctx:%v, want 5 and 2", report.GatewaySendackBatchResultErrorCount, report.GatewaySendackBatchMissingRequestContextCount)
	}
	if report.GatewaySendackBatchResultTimeoutCount != 3 || report.GatewaySendackBatchResultCanceledCount != 2 {
		t.Fatalf("gateway result error classes = timeout:%v canceled:%v, want 3 and 2", report.GatewaySendackBatchResultTimeoutCount, report.GatewaySendackBatchResultCanceledCount)
	}
	if !strings.Contains(strings.Join(report.Reasons, "\n"), "gateway system-error sendacks were observed") {
		t.Fatalf("expected gateway sendack reason, got %#v", report.Reasons)
	}
}

func TestAnalyzeWukongIMPrometheusReportsChannelRuntimePullHintCounters(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_channelv2_pull_hint_total{reason="append",result="submitted",error="none"} 1
wukongim_channelv2_pull_hint_total{reason="resume",result="submitted",error="none"} 2
wukongim_channelv2_pull_hint_total{reason="append",result="ok",error="none"} 1
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="stale_meta"} 1
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="not_ready"} 0
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="channel_not_found"} 0
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="canceled"} 0
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="timeout"} 0
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="remote_error"} 0
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="other"} 0
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_resolve",result="err",error="channel_not_found"} 0
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_hint",result="ok",error="none"} 0
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="await",result="ok",error="none"} 0
wukongim_channelv2_pending_meta_current{reactor_id="0"} 1
wukongim_channelv2_pending_meta_total{event="created",error="none"} 1
wukongim_channelv2_pending_meta_total{event="converted",error="none"} 0
wukongim_channelv2_pending_meta_total{event="released",error="timeout"} 2
wukongim_channelv2_pending_meta_total{event="released",error="not_ready"} 0
wukongim_channelv2_need_meta_pull_total{result="submitted",error="none"} 4
wukongim_channelv2_need_meta_pull_total{result="ok",error="none"} 2
wukongim_channelv2_need_meta_pull_total{result="retry",error="other"} 1
wukongim_channelv2_need_meta_pull_total{result="err",error="timeout"} 2
wukongim_channelv2_need_meta_pull_total{result="err",error="not_ready"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.01"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.05"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="+Inf"} 0
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_channelv2_pull_hint_total{reason="append",result="submitted",error="none"} 6
wukongim_channelv2_pull_hint_total{reason="resume",result="submitted",error="none"} 5
wukongim_channelv2_pull_hint_total{reason="append",result="ok",error="none"} 8
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="stale_meta"} 3
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="not_ready"} 4
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="channel_not_found"} 5
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="canceled"} 6
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="timeout"} 7
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="remote_error"} 8
wukongim_channelv2_pull_hint_total{reason="resume",result="err",error="other"} 6
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_resolve",result="err",error="channel_not_found"} 9
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_hint",result="ok",error="none"} 11
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="await",result="ok",error="none"} 10
wukongim_channelv2_pending_meta_current{reactor_id="0"} 4
wukongim_channelv2_pending_meta_total{event="created",error="none"} 9
wukongim_channelv2_pending_meta_total{event="converted",error="none"} 5
wukongim_channelv2_pending_meta_total{event="released",error="timeout"} 5
wukongim_channelv2_pending_meta_total{event="released",error="not_ready"} 2
wukongim_channelv2_need_meta_pull_total{result="submitted",error="none"} 14
wukongim_channelv2_need_meta_pull_total{result="ok",error="none"} 7
wukongim_channelv2_need_meta_pull_total{result="retry",error="other"} 4
wukongim_channelv2_need_meta_pull_total{result="err",error="timeout"} 6
wukongim_channelv2_need_meta_pull_total{result="err",error="not_ready"} 2
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.01"} 2
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.05"} 5
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="+Inf"} 5
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.ChannelRuntimePullHintSubmittedCount != 8 {
		t.Fatalf("submitted = %v, want 8", report.ChannelRuntimePullHintSubmittedCount)
	}
	if report.ChannelRuntimePullHintOKCount != 7 {
		t.Fatalf("ok = %v, want 7", report.ChannelRuntimePullHintOKCount)
	}
	if report.ChannelRuntimePullHintErrCount != 38 {
		t.Fatalf("err = %v, want 38", report.ChannelRuntimePullHintErrCount)
	}
	if report.ChannelRuntimePullHintStaleMetaErrCount != 2 || report.ChannelRuntimePullHintNotReadyErrCount != 4 || report.ChannelRuntimePullHintChannelNotFoundErrCount != 5 || report.ChannelRuntimePullHintCanceledErrCount != 6 || report.ChannelRuntimePullHintTimeoutErrCount != 7 || report.ChannelRuntimePullHintRemoteErrCount != 8 || report.ChannelRuntimePullHintOtherErrCount != 6 {
		t.Fatalf("error breakdown not parsed: %+v", report)
	}
	if report.ChannelRuntimePullHintReceiveOKCount != 21 || report.ChannelRuntimePullHintReceiveErrCount != 9 || report.ChannelRuntimePullHintReceiveMetaResolveErrCount != 9 || report.ChannelRuntimePullHintReceiveChannelNotFoundErrCount != 9 {
		t.Fatalf("receive breakdown not parsed: %+v", report)
	}
	if report.ChannelRuntimePullHintReceiveMetaHintOKCount != 11 {
		t.Fatalf("receive meta hint ok = %v, want 11", report.ChannelRuntimePullHintReceiveMetaHintOKCount)
	}
	if report.ChannelRuntimePendingMetaCurrentMax != 4 ||
		report.ChannelRuntimePendingMetaCreatedCount != 8 ||
		report.ChannelRuntimePendingMetaConvertedCount != 5 ||
		report.ChannelRuntimePendingMetaReleasedCount != 5 ||
		report.ChannelRuntimePendingMetaTimeoutReleaseCount != 3 {
		t.Fatalf("pending meta breakdown not parsed: %+v", report)
	}
	if report.ChannelRuntimeNeedMetaPullSubmittedCount != 10 ||
		report.ChannelRuntimeNeedMetaPullOKCount != 5 ||
		report.ChannelRuntimeNeedMetaPullRetryCount != 3 ||
		report.ChannelRuntimeNeedMetaPullErrCount != 6 ||
		report.ChannelRuntimeNeedMetaPullTimeoutErrCount != 4 ||
		report.ChannelRuntimeNeedMetaPullNotReadyErrCount != 2 {
		t.Fatalf("need meta pull breakdown not parsed: %+v", report)
	}
	if report.ChannelRuntimeNeedMetaPullRPCP99Seconds <= 0 {
		t.Fatalf("need meta pull RPC p99 = %v, want > 0", report.ChannelRuntimeNeedMetaPullRPCP99Seconds)
	}
	if report.Classification != WukongIMBottleneckChannelRuntime {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMBottleneckChannelRuntime, report)
	}
}

func TestAnalyzeWukongIMPrometheusClassifiesChannelRuntimePressure(t *testing.T) {
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
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_reserve_wait",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_reserve_wait",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_reserve_wait",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_submit",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_submit",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_submit",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_wait",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_wait",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_wait",result="ok",le="+Inf"} 0
wukongim_channelv2_append_batch_wait_duration_seconds_bucket{le="0.01"} 0
wukongim_channelv2_append_batch_wait_duration_seconds_bucket{le="0.05"} 0
wukongim_channelv2_append_batch_wait_duration_seconds_bucket{le="+Inf"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="store_append_wait",commit_mode="quorum",result="ok",le="0.01"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="store_append_wait",commit_mode="quorum",result="ok",le="0.05"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="store_append_wait",commit_mode="quorum",result="ok",le="+Inf"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="post_store_commit_wait",commit_mode="quorum",result="ok",le="0.1"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="post_store_commit_wait",commit_mode="quorum",result="ok",le="0.5"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="post_store_commit_wait",commit_mode="quorum",result="ok",le="+Inf"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_follower_pull_wait",commit_mode="quorum",result="ok",le="0.1"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_follower_pull_wait",commit_mode="quorum",result="ok",le="0.5"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_follower_pull_wait",commit_mode="quorum",result="ok",le="+Inf"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_ack_offset_wait",commit_mode="quorum",result="ok",le="0.1"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_ack_offset_wait",commit_mode="quorum",result="ok",le="0.5"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_ack_offset_wait",commit_mode="quorum",result="ok",le="+Inf"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_hw_advance_wait",commit_mode="quorum",result="ok",le="0.1"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_hw_advance_wait",commit_mode="quorum",result="ok",le="0.5"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_hw_advance_wait",commit_mode="quorum",result="ok",le="+Inf"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_final_complete_wait",commit_mode="quorum",result="ok",le="0.1"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_final_complete_wait",commit_mode="quorum",result="ok",le="0.5"} 0
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_final_complete_wait",commit_mode="quorum",result="ok",le="+Inf"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_hint_to_submit",result="ok",le="0.01"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_hint_to_submit",result="ok",le="0.05"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_hint_to_submit",result="ok",le="+Inf"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_rpc",result="ok",le="0.01"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_rpc",result="ok",le="0.05"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_rpc",result="ok",le="+Inf"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_store_apply",result="ok",le="0.01"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_store_apply",result="ok",le="0.05"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_store_apply",result="ok",le="+Inf"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_apply_to_ack_return",result="ok",le="0.01"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_apply_to_ack_return",result="ok",le="0.05"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_apply_to_ack_return",result="ok",le="+Inf"} 0
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
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_reserve_wait",result="ok",le="0.01"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_reserve_wait",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_reserve_wait",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_submit",result="ok",le="0.01"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_submit",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_submit",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_wait",result="ok",le="0.01"} 10
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_wait",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="runtime_append_wait",result="ok",le="+Inf"} 100
wukongim_channelv2_append_batch_wait_duration_seconds_bucket{le="0.01"} 25
wukongim_channelv2_append_batch_wait_duration_seconds_bucket{le="0.05"} 100
wukongim_channelv2_append_batch_wait_duration_seconds_bucket{le="+Inf"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="store_append_wait",commit_mode="quorum",result="ok",le="0.01"} 10
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="store_append_wait",commit_mode="quorum",result="ok",le="0.05"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="store_append_wait",commit_mode="quorum",result="ok",le="+Inf"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="post_store_commit_wait",commit_mode="quorum",result="ok",le="0.1"} 5
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="post_store_commit_wait",commit_mode="quorum",result="ok",le="0.5"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="post_store_commit_wait",commit_mode="quorum",result="ok",le="+Inf"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_follower_pull_wait",commit_mode="quorum",result="ok",le="0.1"} 40
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_follower_pull_wait",commit_mode="quorum",result="ok",le="0.5"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_follower_pull_wait",commit_mode="quorum",result="ok",le="+Inf"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_ack_offset_wait",commit_mode="quorum",result="ok",le="0.1"} 10
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_ack_offset_wait",commit_mode="quorum",result="ok",le="0.5"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_ack_offset_wait",commit_mode="quorum",result="ok",le="+Inf"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_hw_advance_wait",commit_mode="quorum",result="ok",le="0.1"} 20
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_hw_advance_wait",commit_mode="quorum",result="ok",le="0.5"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_hw_advance_wait",commit_mode="quorum",result="ok",le="+Inf"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_final_complete_wait",commit_mode="quorum",result="ok",le="0.1"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_final_complete_wait",commit_mode="quorum",result="ok",le="0.5"} 100
wukongim_channelv2_append_wait_stage_duration_seconds_bucket{stage="quorum_final_complete_wait",commit_mode="quorum",result="ok",le="+Inf"} 100
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_hint_to_submit",result="ok",le="0.01"} 10
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_hint_to_submit",result="ok",le="0.05"} 100
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_hint_to_submit",result="ok",le="+Inf"} 100
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_rpc",result="ok",le="0.01"} 20
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_rpc",result="ok",le="0.05"} 100
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_pull_rpc",result="ok",le="+Inf"} 100
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_store_apply",result="ok",le="0.01"} 30
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_store_apply",result="ok",le="0.05"} 100
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_store_apply",result="ok",le="+Inf"} 100
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_apply_to_ack_return",result="ok",le="0.01"} 40
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_apply_to_ack_return",result="ok",le="0.05"} 100
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_apply_to_ack_return",result="ok",le="+Inf"} 100
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.Classification != WukongIMBottleneckChannelRuntime {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMBottleneckChannelRuntime, report)
	}
	if report.ChannelRuntimeReactorMailboxDepthMax != 9 {
		t.Fatalf("reactor mailbox depth = %v, want 9", report.ChannelRuntimeReactorMailboxDepthMax)
	}
	if report.ChannelRuntimeWorkerQueueDepthMax != 3 {
		t.Fatalf("worker queue depth = %v, want 3", report.ChannelRuntimeWorkerQueueDepthMax)
	}
	if report.ChannelRuntimeMetaResolveP99Seconds <= 0 || report.ChannelRuntimeMetaApplyP99Seconds <= 0 || report.ChannelRuntimeFacadeAppendP99Seconds <= 0 || report.ChannelRuntimeFacadeAppendReserveWaitP99Seconds <= 0 || report.ChannelRuntimeFacadeAppendSubmitP99Seconds <= 0 || report.ChannelRuntimeFacadeAppendWaitP99Seconds <= 0 || report.ChannelRuntimeAppendBatchWaitP99Seconds <= 0 || report.ChannelRuntimeAppendStoreWaitP99Seconds <= 0 || report.ChannelRuntimeAppendPostStoreCommitWaitP99Seconds <= 0 || report.ChannelRuntimeAppendQuorumFollowerPullWaitP99Seconds <= 0 || report.ChannelRuntimeAppendQuorumAckOffsetWaitP99Seconds <= 0 || report.ChannelRuntimeAppendQuorumHWAdvanceWaitP99Seconds <= 0 || report.ChannelRuntimeAppendQuorumFinalCompleteP99Seconds <= 0 || report.ChannelRuntimeReplicationPullHintToSubmitP99Seconds <= 0 || report.ChannelRuntimeReplicationPullRPCP99Seconds <= 0 || report.ChannelRuntimeReplicationStoreApplyP99Seconds <= 0 || report.ChannelRuntimeReplicationApplyToAckReturnP99Seconds <= 0 {
		t.Fatalf("channel stage p99s not parsed: %+v", report)
	}
}

func TestAnalyzeWukongIMPrometheusAcceptsPromotedChannelRuntimeMetricNames(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channel_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channel_worker_queue_depth{pool="store_append"} 0
wukongim_channel_append_duration_seconds_bucket{commit_mode="local",le="0.01"} 0
wukongim_channel_append_duration_seconds_bucket{commit_mode="local",le="0.05"} 0
wukongim_channel_append_duration_seconds_bucket{commit_mode="local",le="+Inf"} 0
wukongim_channel_pull_hint_total{reason="append",result="submitted",error="none"} 1
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channel_reactor_mailbox_depth{reactor_id="0",priority="normal"} 7
wukongim_channel_worker_queue_depth{pool="store_append"} 4
wukongim_channel_append_duration_seconds_bucket{commit_mode="local",le="0.01"} 10
wukongim_channel_append_duration_seconds_bucket{commit_mode="local",le="0.05"} 100
wukongim_channel_append_duration_seconds_bucket{commit_mode="local",le="+Inf"} 100
wukongim_channel_pull_hint_total{reason="append",result="submitted",error="none"} 6
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.Classification != WukongIMBottleneckChannelRuntime {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMBottleneckChannelRuntime, report)
	}
	if report.ChannelRuntimeReactorMailboxDepthMax != 7 || report.ChannelRuntimeWorkerQueueDepthMax != 4 {
		t.Fatalf("channel runtime gauges not parsed: mailbox=%v queue=%v", report.ChannelRuntimeReactorMailboxDepthMax, report.ChannelRuntimeWorkerQueueDepthMax)
	}
	if report.ChannelRuntimeAppendP99Seconds <= 0 {
		t.Fatalf("channel append p99 = %v, want > 0", report.ChannelRuntimeAppendP99Seconds)
	}
	if report.ChannelRuntimePullHintSubmittedCount != 5 {
		t.Fatalf("pull hint submitted delta = %v, want 5", report.ChannelRuntimePullHintSubmittedCount)
	}
}

func TestAnalyzeWukongIMPrometheusReportsPullBatchShapeAndWaits(t *testing.T) {
	legacyBefore := `
wukongim_channelv2_pull_batch_items_bucket{result="ok",le="2"} 0
wukongim_channelv2_pull_batch_items_bucket{result="ok",le="4"} 0
wukongim_channelv2_pull_batch_items_bucket{result="ok",le="+Inf"} 0
wukongim_channelv2_pull_batch_records_bucket{result="ok",le="8"} 0
wukongim_channelv2_pull_batch_records_bucket{result="ok",le="16"} 0
wukongim_channelv2_pull_batch_records_bucket{result="ok",le="+Inf"} 0
wukongim_channelv2_pull_batch_payload_bytes_bucket{result="ok",le="256"} 0
wukongim_channelv2_pull_batch_payload_bytes_bucket{result="ok",le="512"} 0
wukongim_channelv2_pull_batch_payload_bytes_bucket{result="ok",le="+Inf"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="submit",result="ok",le="0.001"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="submit",result="ok",le="0.005"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="submit",result="ok",le="+Inf"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="await",result="ok",le="0.1"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="await",result="ok",le="0.25"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="await",result="ok",le="+Inf"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="max_sequential_await",result="ok",le="0.05"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="max_sequential_await",result="ok",le="0.1"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="max_sequential_await",result="ok",le="+Inf"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="total",result="ok",le="0.1"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="total",result="ok",le="0.5"} 0
wukongim_channelv2_pull_batch_duration_seconds_bucket{stage="total",result="ok",le="+Inf"} 0
wukongim_runtime_pool_wait_duration_seconds_bucket{component="channel",pool="channelv2-rpc",queue="worker",priority="none",result="ok",le="0.05"} 0
wukongim_runtime_pool_wait_duration_seconds_bucket{component="channel",pool="channelv2-rpc",queue="worker",priority="none",result="ok",le="0.1"} 0
wukongim_runtime_pool_wait_duration_seconds_bucket{component="channel",pool="channelv2-rpc",queue="worker",priority="none",result="ok",le="+Inf"} 0
wukongim_channelv2_leader_pull_stage_duration_seconds_bucket{stage="mailbox_wait",le="0.1"} 0
wukongim_channelv2_leader_pull_stage_duration_seconds_bucket{stage="mailbox_wait",le="+Inf"} 0
wukongim_channelv2_leader_pull_stage_duration_seconds_bucket{stage="ack_apply",le="0.005"} 0
wukongim_channelv2_leader_pull_stage_duration_seconds_bucket{stage="ack_apply",le="+Inf"} 0
wukongim_channelv2_leader_pull_stage_duration_seconds_bucket{stage="handler",le="0.25"} 0
wukongim_channelv2_leader_pull_stage_duration_seconds_bucket{stage="handler",le="+Inf"} 0
wukongim_channelv2_leader_pull_completed_waiters_bucket{le="1"} 0
wukongim_channelv2_leader_pull_completed_waiters_bucket{le="2"} 0
wukongim_channelv2_leader_pull_completed_waiters_bucket{le="+Inf"} 0
`
	legacyAfter := strings.ReplaceAll(legacyBefore, "} 0", "} 10")

	for _, tc := range []struct {
		name   string
		before string
		after  string
	}{
		{name: "legacy", before: legacyBefore, after: legacyAfter},
		{
			name:   "promoted",
			before: strings.ReplaceAll(legacyBefore, "wukongim_channelv2_", "wukongim_channel_"),
			after:  strings.ReplaceAll(legacyAfter, "wukongim_channelv2_", "wukongim_channel_"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := ParsePrometheusText(strings.NewReader(tc.before))
			if err != nil {
				t.Fatalf("ParsePrometheusText(before): %v", err)
			}
			after, err := ParsePrometheusText(strings.NewReader(tc.after))
			if err != nil {
				t.Fatalf("ParsePrometheusText(after): %v", err)
			}

			report := AnalyzeWukongIMPrometheus(before, after)
			if report.ChannelRuntimePullBatchItemsP50 <= 0 || report.ChannelRuntimePullBatchItemsP99 <= report.ChannelRuntimePullBatchItemsP50 {
				t.Fatalf("pull batch item quantiles not parsed: %+v", report)
			}
			if report.ChannelRuntimePullBatchRecordsP50 <= 0 || report.ChannelRuntimePullBatchRecordsP99 <= report.ChannelRuntimePullBatchRecordsP50 {
				t.Fatalf("pull batch record quantiles not parsed: %+v", report)
			}
			if report.ChannelRuntimePullBatchPayloadBytesP50 <= 0 || report.ChannelRuntimePullBatchPayloadBytesP99 <= report.ChannelRuntimePullBatchPayloadBytesP50 {
				t.Fatalf("pull batch payload byte quantiles not parsed: %+v", report)
			}
			if report.ChannelRuntimePullBatchSubmitP99Seconds <= 0 || report.ChannelRuntimePullBatchAwaitP99Seconds <= 0 || report.ChannelRuntimePullBatchMaxSequentialAwaitP99Seconds <= 0 || report.ChannelRuntimePullBatchTotalP99Seconds <= 0 {
				t.Fatalf("pull batch wait p99s not parsed: %+v", report)
			}
			if report.ChannelRuntimeRPCWorkerQueueWaitP99Seconds <= 0 {
				t.Fatalf("RPC worker queue wait p99 not parsed: %+v", report)
			}
			if report.ChannelRuntimeLeaderPullMailboxWaitP99Seconds <= 0 || report.ChannelRuntimeLeaderPullAckApplyP99Seconds <= 0 || report.ChannelRuntimeLeaderPullHandlerP99Seconds <= 0 {
				t.Fatalf("leader Pull stage p99s not parsed: %+v", report)
			}
			if report.ChannelRuntimeLeaderPullCompletedWaitersP50 <= 0 || report.ChannelRuntimeLeaderPullCompletedWaitersP99 <= report.ChannelRuntimeLeaderPullCompletedWaitersP50 {
				t.Fatalf("leader Pull completed waiter quantiles not parsed: %+v", report)
			}
		})
	}
}

func TestAnalyzeWukongIMPrometheusReportsChannelRuntimeMetaResolveBreakdown(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_slot_read",result="miss",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_slot_read",result="miss",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_slot_read",result="miss",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_build",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_build",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_build",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_local",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_local",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_local",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_forward",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_forward",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_forward",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_submit",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_submit",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_submit",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_wait",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_wait",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_wait",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_write",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_write",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_write",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_final_read",result="miss",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_final_read",result="miss",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_final_read",result="miss",le="+Inf"} 0
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_slot_read",result="miss",le="0.01"} 20
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_slot_read",result="miss",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_slot_read",result="miss",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_build",result="ok",le="0.01"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_build",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_build",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose",result="ok",le="0.01"} 10
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_local",result="ok",le="0.01"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_local",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_local",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_forward",result="ok",le="0.01"} 10
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_forward",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_propose_forward",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_submit",result="ok",le="0.01"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_submit",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_submit",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_wait",result="ok",le="0.01"} 10
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_wait",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_propose_wait",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_write",result="ok",le="0.01"} 10
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_write",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_write",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_final_read",result="miss",le="0.01"} 50
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_final_read",result="miss",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_final_read",result="miss",le="+Inf"} 100
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.Classification != WukongIMBottleneckChannelRuntime {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMBottleneckChannelRuntime, report)
	}
	if report.ChannelRuntimeMetaSlotReadP99Seconds <= 0 || report.ChannelRuntimeMetaCreateBuildP99Seconds <= 0 || report.ChannelRuntimeMetaCreateProposeP99Seconds <= 0 || report.ChannelRuntimeMetaCreateProposeLocalP99Seconds <= 0 || report.ChannelRuntimeMetaCreateProposeForwardP99Seconds <= 0 || report.ChannelRuntimeMetaCreateSlotProposeSubmitP99Seconds <= 0 || report.ChannelRuntimeMetaCreateSlotProposeWaitP99Seconds <= 0 || report.ChannelRuntimeMetaCreateWriteP99Seconds <= 0 || report.ChannelRuntimeMetaFinalReadP99Seconds <= 0 {
		t.Fatalf("channel meta breakdown p99s not parsed: %+v", report)
	}
}

func TestAnalyzeWukongIMPrometheusReportsChannelRuntimeSlotFutureBreakdown(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_control_wait",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_control_wait",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_control_wait",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_raft_commit_wait",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_raft_commit_wait",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_raft_commit_wait",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_apply",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_apply",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_apply",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_commit",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_commit",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_commit",result="ok",le="+Inf"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_mark_applied",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_mark_applied",result="ok",le="0.05"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_mark_applied",result="ok",le="+Inf"} 0
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_control_wait",result="ok",le="0.01"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_control_wait",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_control_wait",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_raft_commit_wait",result="ok",le="0.01"} 10
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_raft_commit_wait",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_raft_commit_wait",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_apply",result="ok",le="0.01"} 20
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_apply",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_apply",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_commit",result="ok",le="0.01"} 40
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_commit",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_fsm_commit",result="ok",le="+Inf"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_mark_applied",result="ok",le="0.01"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_mark_applied",result="ok",le="0.05"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_create_slot_mark_applied",result="ok",le="+Inf"} 100
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.Classification != WukongIMBottleneckChannelRuntime {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMBottleneckChannelRuntime, report)
	}
	if report.ChannelRuntimeMetaCreateSlotControlWaitP99Seconds <= 0 || report.ChannelRuntimeMetaCreateSlotRaftCommitWaitP99Seconds <= 0 || report.ChannelRuntimeMetaCreateSlotFSMApplyP99Seconds <= 0 || report.ChannelRuntimeMetaCreateSlotFSMCommitP99Seconds <= 0 || report.ChannelRuntimeMetaCreateSlotMarkAppliedP99Seconds <= 0 {
		t.Fatalf("channel Slot future breakdown p99s not parsed: %+v", report)
	}
}

func TestAnalyzeWukongIMPrometheusClassifiesStorageCommitPressure(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_channelv2_worker_queue_depth{pool="store_apply"} 0
wukongim_channelv2_worker_inflight{pool="store_append"} 0
wukongim_channelv2_worker_inflight{pool="store_apply"} 0
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 0
wukongim_channelv2_worker_inflight_peak{pool="store_apply"} 0
wukongim_storage_commit_queue_depth{store="message"} 0
wukongim_storage_commit_batch_records_bucket{store="message",le="1"} 0
wukongim_storage_commit_batch_records_bucket{store="message",le="4"} 0
wukongim_storage_commit_batch_records_bucket{store="message",le="+Inf"} 0
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="0.01"} 0
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="0.05"} 0
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="+Inf"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="0.01"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="0.25"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="1"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="5"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="10"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="+Inf"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="0.01"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="0.25"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="1"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="5"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="10"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="+Inf"} 0
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_channelv2_worker_queue_depth{pool="store_apply"} 0
wukongim_channelv2_worker_inflight{pool="store_append"} 256
wukongim_channelv2_worker_inflight{pool="store_apply"} 256
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 256
wukongim_channelv2_worker_inflight_peak{pool="store_apply"} 256
wukongim_storage_commit_queue_depth{store="message"} 5
wukongim_storage_commit_batch_records_bucket{store="message",le="1"} 10
wukongim_storage_commit_batch_records_bucket{store="message",le="4"} 100
wukongim_storage_commit_batch_records_bucket{store="message",le="+Inf"} 100
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="0.01"} 10
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="0.05"} 100
wukongim_storage_commit_batch_duration_seconds_bucket{store="message",stage="commit",result="ok",le="+Inf"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="0.01"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="0.25"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="1"} 195
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="5"} 198
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="10"} 199
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="+Inf"} 200
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="0.01"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="0.25"} 50
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="1"} 97
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="5"} 98
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="10"} 98
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="apply",result="ok",le="+Inf"} 100
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.Classification != WukongIMBottleneckStorageCommit {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMBottleneckStorageCommit, report)
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
	if report.StorageCommitRequestP99Seconds <= 0 || report.StorageCommitRequestOKP99Seconds <= 0 {
		t.Fatalf("storage commit request p99s not parsed: %+v", report)
	}
	if report.ChannelRuntimeWorkerInflightByPool["store_append"] != 256 || report.ChannelRuntimeWorkerInflightByPool["store_apply"] != 256 {
		t.Fatalf("worker inflight by pool not parsed: %+v", report.ChannelRuntimeWorkerInflightByPool)
	}
	if report.ChannelRuntimeWorkerInflightPeakByPool["store_append"] != 256 || report.ChannelRuntimeWorkerInflightPeakByPool["store_apply"] != 256 {
		t.Fatalf("worker inflight peak by pool not parsed: %+v", report.ChannelRuntimeWorkerInflightPeakByPool)
	}
	if report.StorageCommitRequestOver1sCount != 8 || report.StorageCommitRequestOver5sCount != 4 || report.StorageCommitRequestOver10sCount != 3 {
		t.Fatalf("storage commit request tail counts not parsed: %+v", report)
	}
	if report.StorageCommitRequestOver10sCountByLane["append"] != 1 || report.StorageCommitRequestOver10sCountByLane["apply"] != 2 {
		t.Fatalf("storage commit request tail counts by lane not parsed: %+v", report.StorageCommitRequestOver10sCountByLane)
	}
	if report.StorageCommitRequestP99SecondsByLane["append"] <= 0 || report.StorageCommitRequestP99SecondsByLane["apply"] <= 0 {
		t.Fatalf("storage commit request p99 by lane not parsed: %+v", report.StorageCommitRequestP99SecondsByLane)
	}
}

func TestAnalyzeWukongIMPrometheusClassifiesControllerRaftStepPressure(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_controller_raft_step_queue_depth{node_id="1"} 0
wukongim_controller_raft_step_queue_capacity{node_id="1"} 1024
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="ok",le="0.01"} 0
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="ok",le="0.05"} 0
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="ok",le="+Inf"} 0
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="err",le="0.01"} 0
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="err",le="0.25"} 0
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="err",le="+Inf"} 0
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_controller_raft_step_queue_depth{node_id="1"} 900
wukongim_controller_raft_step_queue_capacity{node_id="1"} 1024
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="ok",le="0.01"} 10
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="ok",le="0.05"} 100
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="ok",le="+Inf"} 100
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="err",le="0.01"} 0
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="err",le="0.25"} 4
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{node_id="1",result="err",le="+Inf"} 4
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)
	if report.Classification != WukongIMBottleneckControllerRaft {
		t.Fatalf("classification = %q, want %q: %+v", report.Classification, WukongIMBottleneckControllerRaft, report)
	}
	if report.ControllerRaftStepQueueDepth != 900 || report.ControllerRaftStepQueueCapacity != 1024 {
		t.Fatalf("controller queue = %.0f/%.0f, want 900/1024", report.ControllerRaftStepQueueDepth, report.ControllerRaftStepQueueCapacity)
	}
	if report.ControllerRaftStepEnqueueErrCount != 4 {
		t.Fatalf("controller err count = %v, want 4", report.ControllerRaftStepEnqueueErrCount)
	}
	if report.ControllerRaftStepEnqueueOKP99Seconds <= 0 || report.ControllerRaftStepEnqueueErrP99Seconds <= 0 {
		t.Fatalf("controller enqueue p99s not parsed: %+v", report)
	}
}
