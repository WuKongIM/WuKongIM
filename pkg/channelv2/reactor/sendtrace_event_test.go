package reactor

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/stretchr/testify/require"
)

type reactorDetailSink struct {
	decisions map[string]sendtrace.DetailDecision
	limits    sendtrace.DetailLimits
	keys      []sendtrace.DetailKey
}

func (s *reactorDetailSink) RecordSendTrace(sendtrace.Event) {}

func (s *reactorDetailSink) KeepSendTraceDetail(key sendtrace.DetailKey) sendtrace.DetailDecision {
	s.keys = append(s.keys, key)
	if s.decisions == nil {
		return sendtrace.DetailDecision{}
	}
	return s.decisions[key.TraceID]
}

func (s *reactorDetailSink) SendTraceDetailLimits() sendtrace.DetailLimits {
	return s.limits
}

type reactorRecordOnlySink struct{}

func (reactorRecordOnlySink) RecordSendTrace(sendtrace.Event) {}

func TestAppendTraceBatchSelectionSelectsOnlyDetailKeptItems(t *testing.T) {
	batch := appendBatch{
		requests: []appendRequest{{
			req: ch.AppendBatchRequest{
				ChannelID:  ch.ChannelID{ID: "room", Type: 1},
				ChannelKey: "channel/1/cm9vbQ",
				Attempt:    2,
				Messages: []ch.Message{
					{MessageID: 10, TraceID: "trace-drop", ChannelKey: "channel/1/cm9vbQ", ClientMsgNo: "drop", FromUID: "u1"},
					{MessageID: 11, TraceID: "trace-keep", ChannelKey: "channel/1/cm9vbQ", ClientMsgNo: "keep", FromUID: "u1"},
				},
			},
			records: []ch.Record{{ID: 10}, {ID: 11}},
		}},
		records: []ch.Record{{ID: 10}, {ID: 11}},
	}
	restore := sendtrace.SetSink(&reactorDetailSink{
		limits: sendtrace.DetailLimits{MaxItemsPerBatch: 8},
		decisions: map[string]sendtrace.DetailDecision{
			"trace-keep": {Keep: true, Reason: "debug"},
		},
	})
	t.Cleanup(restore)

	traceBatch := selectAppendTraceBatch(batch)

	require.Len(t, traceBatch.items, 1)
	require.Equal(t, "trace-keep", traceBatch.items[0].traceID)
	require.Equal(t, 0, traceBatch.items[0].requestIdx)
	require.Equal(t, 1, traceBatch.items[0].recordIdx)
	require.Equal(t, 1, traceBatch.items[0].localRecordIdx)
	require.Equal(t, 2, traceBatch.items[0].attempt)
}

func TestAppendTraceBatchSelectionHonorsMaxItems(t *testing.T) {
	batch := appendBatch{
		requests: []appendRequest{{
			req: ch.AppendBatchRequest{
				ChannelID: ch.ChannelID{ID: "room", Type: 1},
				Messages: []ch.Message{
					{TraceID: "trace-1", ClientMsgNo: "a"},
					{TraceID: "trace-2", ClientMsgNo: "b"},
				},
			},
			records: []ch.Record{{ID: 1}, {ID: 2}},
		}},
		records: []ch.Record{{ID: 1}, {ID: 2}},
	}
	restore := sendtrace.SetSink(&reactorDetailSink{
		limits: sendtrace.DetailLimits{MaxItemsPerBatch: 1},
		decisions: map[string]sendtrace.DetailDecision{
			"trace-1": {Keep: true, Reason: "sample"},
			"trace-2": {Keep: true, Reason: "sample"},
		},
	})
	t.Cleanup(restore)

	traceBatch := selectAppendTraceBatch(batch)

	require.Len(t, traceBatch.items, 1)
	require.Equal(t, "trace-1", traceBatch.items[0].traceID)
}

func TestAppendTraceBatchSelectionUsesRequestTraceIDAndDerivedChannelKey(t *testing.T) {
	sink := &reactorDetailSink{
		limits: sendtrace.DetailLimits{MaxItemsPerBatch: 4},
		decisions: map[string]sendtrace.DetailDecision{
			"trace-batch": {Keep: true, Reason: "debug"},
		},
	}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)

	batch := appendBatch{
		requests: []appendRequest{{
			req: ch.AppendBatchRequest{
				ChannelID: ch.ChannelID{ID: "room", Type: 1},
				TraceID:   "trace-batch",
				Messages:  []ch.Message{{ClientMsgNo: "client-1", FromUID: "u1"}},
			},
			records: []ch.Record{{ID: 1}},
		}},
		records: []ch.Record{{ID: 1}},
	}

	traceBatch := selectAppendTraceBatch(batch)

	require.Len(t, traceBatch.items, 1)
	require.Equal(t, "trace-batch", traceBatch.items[0].traceID)
	require.Equal(t, sendtrace.ChannelKeyFromID("room", 1), traceBatch.items[0].channelKey)
	require.Equal(t, "client-1", traceBatch.items[0].clientMsgNo)
	require.Equal(t, "u1", traceBatch.items[0].fromUID)
}

func TestAppendTraceBatchSelectionSkipsDetailDecisionForMessagesWithoutTraceID(t *testing.T) {
	sink := &reactorDetailSink{
		limits: sendtrace.DetailLimits{MaxItemsPerBatch: 4},
		decisions: map[string]sendtrace.DetailDecision{
			"trace-keep": {Keep: true, Reason: "debug"},
		},
	}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)

	batch := appendBatch{
		requests: []appendRequest{{
			req: ch.AppendBatchRequest{
				ChannelID: ch.ChannelID{ID: "room", Type: 1},
				Messages: []ch.Message{
					{ClientMsgNo: "skip"},
					{TraceID: "trace-keep", ChannelKey: "channel/1/cm9vbQ", ClientMsgNo: "keep"},
				},
			},
			records: []ch.Record{{ID: 1}, {ID: 2}},
		}},
		records: []ch.Record{{ID: 1}, {ID: 2}},
	}

	traceBatch := selectAppendTraceBatch(batch)

	require.Len(t, traceBatch.items, 1)
	require.Len(t, sink.keys, 1)
	require.Equal(t, "trace-keep", sink.keys[0].TraceID)
	require.Equal(t, "channel/1/cm9vbQ", sink.keys[0].ChannelKey)
}

func TestAppendTraceBatchSelectionDisabledDoesNotAllocate(t *testing.T) {
	batch := appendBatch{
		requests: []appendRequest{{
			req:     ch.AppendBatchRequest{Messages: []ch.Message{{TraceID: "trace-1"}}},
			records: []ch.Record{{ID: 1}},
		}},
		records: []ch.Record{{ID: 1}},
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_ = selectAppendTraceBatch(batch)
	})

	require.Zero(t, allocs)

	restore := sendtrace.SetSink(reactorRecordOnlySink{})
	t.Cleanup(restore)

	allocs = testing.AllocsPerRun(1000, func() {
		_ = selectAppendTraceBatch(batch)
	})

	require.Zero(t, allocs)
}
