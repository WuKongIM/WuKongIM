package sendtrace

import (
	"sync"
	"testing"

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
