package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/stretchr/testify/require"
)

func TestSendTraceAdapterRecordsKeptEvent(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 8})
	adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 1}))

	adapter.RecordSendTrace(sendtrace.Event{
		TraceID:     "trace-1",
		Stage:       sendtrace.StageMessageSendDurable,
		At:          time.Unix(1, 0),
		NodeID:      1,
		ClientMsgNo: "c1",
		MessageSeq:  9,
		Result:      sendtrace.ResultOK,
	})

	result := store.Query(context.Background(), Query{TraceID: "trace-1"})
	require.Equal(t, StatusOK, result.Status)
	require.Len(t, result.Events, 1)
	require.Equal(t, Stage(sendtrace.StageMessageSendDurable), result.Events[0].Stage)
}

func TestSendTraceAdapterPreservesService(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 8})
	adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 1}))

	adapter.RecordSendTrace(sendtrace.Event{TraceID: "trace-forward", Stage: sendtrace.StageChannelAppendForward, Service: "channel_append", Result: sendtrace.ResultOK})

	result := store.Query(context.Background(), Query{TraceID: "trace-forward"})
	require.Equal(t, "channel_append", result.Events[0].Service)
}

func TestSendTraceAdapterPreservesBatchMetrics(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 8})
	adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 1}))

	adapter.RecordSendTrace(sendtrace.Event{
		TraceID:      "trace-batch",
		Stage:        sendtrace.StageStoreCommitPebbleSync,
		Result:       sendtrace.ResultOK,
		RequestCount: 3,
		RecordCount:  17,
		ByteCount:    4096,
		QueueDepth:   12,
	})

	result := store.Query(context.Background(), Query{TraceID: "trace-batch"})
	require.Equal(t, StatusOK, result.Status)
	require.Len(t, result.Events, 1)
	require.Equal(t, 3, result.Events[0].RequestCount)
	require.Equal(t, 17, result.Events[0].RecordCount)
	require.Equal(t, 4096, result.Events[0].ByteCount)
	require.Equal(t, 12, result.Events[0].QueueDepth)
}

func TestSendTraceAdapterKeepsErrorEvenWhenSampleRateZero(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 8})
	adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 0}))

	adapter.RecordSendTrace(sendtrace.Event{TraceID: "trace-err", Stage: sendtrace.StageGatewayMessagesSend, Result: sendtrace.ResultError, ErrorCode: "invalid_frame"})

	result := store.Query(context.Background(), Query{TraceID: "trace-err"})
	require.Equal(t, StatusError, result.Status)
	require.Len(t, result.Events, 1)
	require.Equal(t, ErrorCodeInvalidFrame, result.Events[0].ErrorCode)
}

func TestSendTraceAdapterUsesSenderTrackingAndRedactsUID(t *testing.T) {
	rules := NewTrackingRules(TrackingRulesOptions{})
	_, err := rules.Add(TrackingRuleInput{ID: "sender", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Hour, SampleRate: 1})
	require.NoError(t, err)

	store := NewStore(StoreOptions{NodeID: 1})
	adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 0, TrackingRules: rules}))
	adapter.RecordSendTrace(sendtrace.Event{TraceID: "trace-u1", FromUID: "u1", Stage: sendtrace.StageMessageSendDurable, Result: sendtrace.ResultOK})

	result := store.Query(context.Background(), Query{UID: "u1", Limit: 10})
	require.Equal(t, StatusOK, result.Status)
	require.Len(t, result.Events, 1)
	require.Equal(t, "debug", result.Events[0].SampleReason)
	require.Empty(t, result.Events[0].FromUID)
}

func TestSendTraceAdapterImplementsDetailSampler(t *testing.T) {
	adapter := NewSendTraceSink(NewStore(StoreOptions{Capacity: 8}), NewSampler(SamplerOptions{
		DeepSampleRate:       1,
		DeepSlowThreshold:    750 * time.Millisecond,
		DeepMaxItemsPerBatch: 4,
	}))

	decision := adapter.KeepSendTraceDetail(sendtrace.DetailKey{TraceID: "trace-detail"})
	limits := adapter.SendTraceDetailLimits()

	require.True(t, decision.Keep)
	require.Equal(t, "sample", decision.Reason)
	require.Equal(t, 750*time.Millisecond, limits.SlowThreshold)
	require.Equal(t, 4, limits.MaxItemsPerBatch)
}

func TestSendTraceAdapterDetailSamplerUsesTrackingRules(t *testing.T) {
	rules := NewTrackingRules(TrackingRulesOptions{})
	_, err := rules.Add(TrackingRuleInput{ID: "channel", Target: TrackingTargetChannel, ChannelKey: "channel/1/cm9vbQ", TTL: time.Minute, SampleRate: 1})
	require.NoError(t, err)
	adapter := NewSendTraceSink(NewStore(StoreOptions{Capacity: 8}), NewSampler(SamplerOptions{
		DeepSampleRate: 0,
		TrackingRules:  rules,
	}))

	decision := adapter.KeepSendTraceDetail(sendtrace.DetailKey{ChannelKey: "channel/1/cm9vbQ"})

	require.True(t, decision.Keep)
	require.Equal(t, "debug", decision.Reason)
}

func BenchmarkSendTraceAdapterUnsampled(b *testing.B) {
	store := NewStore(StoreOptions{Capacity: 1024})
	adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 0, SlowThreshold: time.Hour}))
	event := sendtrace.Event{TraceID: "trace-unsampled", Stage: sendtrace.StageMessageSendDurable, Result: sendtrace.ResultOK}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		adapter.RecordSendTrace(event)
	}
}

type fakeMetrics struct {
	recorded []recordedMetric
	dropped  []string
	usage    []float64
}

type recordedMetric struct {
	stage  string
	result string
}

func (m *fakeMetrics) RecordEvent(stage, result string) {
	m.recorded = append(m.recorded, recordedMetric{stage: stage, result: result})
}

func (m *fakeMetrics) RecordDropped(reason string) {
	m.dropped = append(m.dropped, reason)
}

func (m *fakeMetrics) SetBufferUsageRatio(v float64) {
	m.usage = append(m.usage, v)
}

func TestSendTraceAdapterMetricsRecordsSampledOutDrop(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 8})
	metrics := &fakeMetrics{}
	adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 0, SlowThreshold: time.Hour})).WithMetrics(metrics)

	adapter.RecordSendTrace(sendtrace.Event{TraceID: "trace-dropped", Stage: sendtrace.StageMessageSendDurable, Result: sendtrace.ResultOK})

	require.Equal(t, []string{"sampled_out"}, metrics.dropped)
	require.Empty(t, metrics.recorded)
	require.Empty(t, metrics.usage)
}

func TestSendTraceAdapterMetricsRecordsEventAndBufferUsage(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 8})
	metrics := &fakeMetrics{}
	adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 1})).WithMetrics(metrics)

	adapter.RecordSendTrace(sendtrace.Event{TraceID: "trace-recorded", Stage: sendtrace.StageMessageSendDurable, Result: sendtrace.ResultOK})

	require.Equal(t, []recordedMetric{{stage: string(Stage(sendtrace.StageMessageSendDurable)), result: string(ResultOK)}}, metrics.recorded)
	require.Empty(t, metrics.dropped)
	require.NotEmpty(t, metrics.usage)
	require.Greater(t, metrics.usage[len(metrics.usage)-1], float64(0))
}
