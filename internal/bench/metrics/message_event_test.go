package metrics

import (
	"strings"
	"testing"
)

func TestAnalyzeWukongIMPrometheusReportsMessageEventStreamPressure(t *testing.T) {
	before, err := ParsePrometheusText(strings.NewReader(`
wukongim_message_event_stream_cache_sessions 0
wukongim_message_event_stream_cache_open_lanes 0
wukongim_message_event_stream_cache_payload_bytes 0
wukongim_message_event_stream_cache_max_sessions 1024
wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 10
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="ok"} 1
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="cache_miss"} 0
wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="backpressured"} 0
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 1
wukongim_message_event_propose_duration_seconds_bucket{path="finish_batch",result="ok",le="0.01"} 0
wukongim_message_event_propose_duration_seconds_bucket{path="finish_batch",result="ok",le="0.05"} 1
wukongim_message_event_propose_duration_seconds_bucket{path="finish_batch",result="ok",le="+Inf"} 1
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="0.01"} 1
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="0.05"} 1
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="+Inf"} 1
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="0.01"} 0
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="0.05"} 1
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="+Inf"} 1
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_fsm_commit",le="0.01"} 1
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_fsm_commit",le="0.05"} 1
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_fsm_commit",le="+Inf"} 1
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="4"} 1
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="8"} 1
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="+Inf"} 1
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(before): %v", err)
	}
	after, err := ParsePrometheusText(strings.NewReader(`
wukongim_message_event_stream_cache_sessions 7
wukongim_message_event_stream_cache_open_lanes 11
wukongim_message_event_stream_cache_payload_bytes 4096
wukongim_message_event_stream_cache_max_sessions 1024
wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 30
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="ok"} 5
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="cache_miss"} 2
wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="backpressured"} 3
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 5
wukongim_message_event_propose_duration_seconds_bucket{path="finish_batch",result="ok",le="0.01"} 1
wukongim_message_event_propose_duration_seconds_bucket{path="finish_batch",result="ok",le="0.05"} 4
wukongim_message_event_propose_duration_seconds_bucket{path="finish_batch",result="ok",le="+Inf"} 4
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="0.01"} 3
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="0.05"} 5
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="+Inf"} 5
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="0.01"} 1
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="0.05"} 5
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="+Inf"} 5
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_fsm_commit",le="0.01"} 2
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_fsm_commit",le="0.05"} 5
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_fsm_commit",le="+Inf"} 5
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="4"} 1
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="8"} 5
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="+Inf"} 5
`))
	if err != nil {
		t.Fatalf("ParsePrometheusText(after): %v", err)
	}

	report := AnalyzeWukongIMPrometheus(before, after)

	if report.MessageEventStreamCacheSessionsMax != 7 || report.MessageEventStreamCacheOpenLanesMax != 11 {
		t.Fatalf("cache gauges not parsed: %+v", report)
	}
	if report.MessageEventStreamCachePayloadBytesMax != 4096 || report.MessageEventStreamCacheMaxSessionsMax != 1024 {
		t.Fatalf("cache payload/max gauges not parsed: %+v", report)
	}
	if report.MessageEventAppendCountByPath["cache"] != 23 {
		t.Fatalf("cache append count = %.0f, want 23", report.MessageEventAppendCountByPath["cache"])
	}
	if report.MessageEventAppendCountByPath["finish_batch"] != 6 {
		t.Fatalf("finish_batch append count = %.0f, want 6", report.MessageEventAppendCountByPath["finish_batch"])
	}
	if report.MessageEventAppendCountByResult["cache_miss"] != 2 {
		t.Fatalf("cache_miss count = %.0f, want 2", report.MessageEventAppendCountByResult["cache_miss"])
	}
	if report.MessageEventAppendCountByEventType["stream.delta"] != 23 {
		t.Fatalf("stream.delta count = %.0f, want 23", report.MessageEventAppendCountByEventType["stream.delta"])
	}
	if report.MessageEventProposeCountByPath["finish_batch"] != 4 {
		t.Fatalf("finish_batch propose count = %.0f, want 4", report.MessageEventProposeCountByPath["finish_batch"])
	}
	if report.MessageEventProposeBatchEventsP99ByPath["finish_batch"] < 7 {
		t.Fatalf("finish_batch batch p99 = %.3f, want >= 7", report.MessageEventProposeBatchEventsP99ByPath["finish_batch"])
	}
	if report.MessageEventAppendStageDurationP99SecondsByPathStage["finish_batch"]["finish_batch_build"] <= 0 {
		t.Fatalf("finish_batch append stage p99 missing: %#v", report.MessageEventAppendStageDurationP99SecondsByPathStage)
	}
	if report.MessageEventProposeStageDurationP99SecondsByPathStage["finish_batch"]["slot_propose_wait"] <= 0 {
		t.Fatalf("finish_batch propose wait p99 missing: %#v", report.MessageEventProposeStageDurationP99SecondsByPathStage)
	}
	if report.MessageEventProposeStageDurationP99SecondsByPathStage["finish_batch"]["slot_fsm_commit"] <= 0 {
		t.Fatalf("finish_batch slot fsm commit p99 missing: %#v", report.MessageEventProposeStageDurationP99SecondsByPathStage)
	}
	if report.MessageEventBackpressuredCount != 3 || report.MessageEventCacheMissCount != 2 {
		t.Fatalf("backpressure/cache_miss = %.0f/%.0f, want 3/2", report.MessageEventBackpressuredCount, report.MessageEventCacheMissCount)
	}
}
