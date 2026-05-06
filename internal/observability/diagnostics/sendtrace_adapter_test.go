package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
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

func TestSendTraceAdapterKeepsErrorEvenWhenSampleRateZero(t *testing.T) {
	store := NewStore(StoreOptions{Capacity: 8})
	adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 0}))

	adapter.RecordSendTrace(sendtrace.Event{TraceID: "trace-err", Stage: sendtrace.StageGatewayMessagesSend, Result: sendtrace.ResultError, ErrorCode: "invalid_frame"})

	result := store.Query(context.Background(), Query{TraceID: "trace-err"})
	require.Equal(t, StatusError, result.Status)
	require.Len(t, result.Events, 1)
	require.Equal(t, ErrorCodeInvalidFrame, result.Events[0].ErrorCode)
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
