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
}

func (s *reactorDetailSink) RecordSendTrace(sendtrace.Event) {}

func (s *reactorDetailSink) KeepSendTraceDetail(key sendtrace.DetailKey) sendtrace.DetailDecision {
	if s.decisions == nil {
		return sendtrace.DetailDecision{}
	}
	return s.decisions[key.TraceID]
}

func (s *reactorDetailSink) SendTraceDetailLimits() sendtrace.DetailLimits {
	return s.limits
}

func TestAppendTraceBatchSelectsOnlyDetailKeptItems(t *testing.T) {
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
}
