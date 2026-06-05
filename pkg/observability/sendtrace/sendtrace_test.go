package sendtrace

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *recordingSink) RecordSendTrace(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

type detailRecordingSink struct {
	recordingSink
	decision DetailDecision
	limits   DetailLimits
	keys     []DetailKey
}

func (s *detailRecordingSink) KeepSendTraceDetail(key DetailKey) DetailDecision {
	s.keys = append(s.keys, key)
	return s.decision
}

func (s *detailRecordingSink) SendTraceDetailLimits() DetailLimits {
	return s.limits
}

func TestChannelKeyFromID(t *testing.T) {
	tests := []struct {
		name        string
		channelID   string
		channelType uint8
		want        string
	}{
		{
			name:        "encodes diagnostics-safe channel key",
			channelID:   "room/a b",
			channelType: 2,
			want:        "channel/2/cm9vbS9hIGI",
		},
		{
			name:        "empty channel id returns empty key",
			channelID:   "",
			channelType: 2,
			want:        "",
		},
		{
			name:        "zero channel type returns empty key",
			channelID:   "room/a b",
			channelType: 0,
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ChannelKeyFromID(tt.channelID, tt.channelType))
		})
	}
}

func TestSetSinkRestoreHandlesNilPrevious(t *testing.T) {
	sink := &recordingSink{}
	restore := SetSink(sink)
	Record(Event{Stage: StageGatewayMessagesSend, TraceID: "trace-1", Result: ResultOK})
	restore()
	Record(Event{Stage: StageGatewayMessagesSend, TraceID: "trace-2", Result: ResultOK})

	require.Len(t, sink.events, 1)
	require.Equal(t, "trace-1", sink.events[0].TraceID)
}

func TestRecordCarriesFromUID(t *testing.T) {
	sink := &recordingSink{}
	restore := SetSink(sink)
	defer restore()

	Record(Event{Stage: StageGatewayMessagesSend, TraceID: "trace-1", FromUID: "u1", Result: ResultOK})

	require.Len(t, sink.events, 1)
	require.Equal(t, "u1", sink.events[0].FromUID)
}

func TestRecordCarriesBatchMetrics(t *testing.T) {
	sink := &recordingSink{}
	restore := SetSink(sink)
	defer restore()

	Record(Event{
		Stage:        StageStoreCommitPebbleSync,
		TraceID:      "trace-batch",
		RequestCount: 3,
		RecordCount:  17,
		ByteCount:    4096,
		QueueDepth:   12,
		Result:       ResultOK,
	})

	require.Len(t, sink.events, 1)
	require.Equal(t, 3, sink.events[0].RequestCount)
	require.Equal(t, 17, sink.events[0].RecordCount)
	require.Equal(t, 4096, sink.events[0].ByteCount)
	require.Equal(t, 12, sink.events[0].QueueDepth)
}

func TestDetailDecisionDisabledWithoutDetailSink(t *testing.T) {
	restore := SetSink(&recordingSink{})
	t.Cleanup(restore)

	decision, limits, ok := DetailDecisionFor(DetailKey{TraceID: "trace-1"})

	require.False(t, ok)
	require.False(t, decision.Keep)
	require.Zero(t, limits.MaxItemsPerBatch)
}

func TestDetailDecisionUsesActiveDetailSink(t *testing.T) {
	sink := &detailRecordingSink{
		decision: DetailDecision{Keep: true, Reason: "debug"},
		limits:   DetailLimits{SlowThreshold: time.Second, MaxItemsPerBatch: 3},
	}
	restore := SetSink(sink)
	t.Cleanup(restore)

	decision, limits, ok := DetailDecisionFor(DetailKey{
		TraceID:     "trace-1",
		ChannelKey:  "channel/1/cm9vbQ",
		ClientMsgNo: "c1",
		FromUID:     "u1",
	})

	require.True(t, ok)
	require.Equal(t, DetailDecision{Keep: true, Reason: "debug"}, decision)
	require.Equal(t, time.Second, limits.SlowThreshold)
	require.Equal(t, 3, limits.MaxItemsPerBatch)
	require.Equal(t, []DetailKey{{
		TraceID:     "trace-1",
		ChannelKey:  "channel/1/cm9vbQ",
		ClientMsgNo: "c1",
		FromUID:     "u1",
	}}, sink.keys)
}

func TestDetailLimitsNormalizeNegativeValues(t *testing.T) {
	sink := &detailRecordingSink{limits: DetailLimits{SlowThreshold: -time.Second, MaxItemsPerBatch: -1}}
	restore := SetSink(sink)
	t.Cleanup(restore)

	limits, ok := ActiveDetailLimits()

	require.True(t, ok)
	require.Zero(t, limits.SlowThreshold)
	require.Zero(t, limits.MaxItemsPerBatch)
}
