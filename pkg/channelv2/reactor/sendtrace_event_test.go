package reactor

import (
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
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

type recordingTraceSink struct {
	reactorDetailSink
	events []sendtrace.Event
}

func (s *recordingTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.events = append(s.events, event)
}

func TestReactorRecordsLeaderQueueAndLocalDurableTrace(t *testing.T) {
	factory := newCountingStoreFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	traceSink := &recordingTraceSink{
		reactorDetailSink: reactorDetailSink{
			limits: sendtrace.DetailLimits{MaxItemsPerBatch: 4},
			decisions: map[string]sendtrace.DetailDecision{
				"trace-leader": {Keep: true, Reason: "debug"},
			},
		},
	}
	restore := sendtrace.SetSink(traceSink)
	t.Cleanup(restore)

	meta := testMeta("deep-trace-leader", 1, 1)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	future := NewFuture()
	event := appendEventWithFuture(meta, 10, "payload", future)
	event.Append.TraceID = "trace-leader"
	event.Append.ChannelKey = "channel/1/ZGVlcC10cmFjZS1sZWFkZXI"
	event.Append.Attempt = 2
	event.Append.Messages[0].TraceID = "trace-leader"
	event.Append.Messages[0].ChannelKey = event.Append.ChannelKey
	event.Append.Messages[0].ClientMsgNo = "client-1"
	event.Append.Messages[0].FromUID = "u1"

	r.handleAppend(event)
	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	result := awaitFutureResult(t, future)

	require.NoError(t, result.Err)
	requireTraceEvent(t, traceSink.events, sendtrace.StageReplicaLeaderQueueWait, "trace-leader", 1)
	requireTraceEvent(t, traceSink.events, sendtrace.StageReplicaLeaderLocalDurable, "trace-leader", 1)
}

func requireTraceEvent(t *testing.T, events []sendtrace.Event, stage sendtrace.Stage, traceID string, messageSeq uint64) {
	t.Helper()
	for _, event := range events {
		if event.Stage != stage || event.TraceID != traceID {
			continue
		}
		require.Equal(t, messageSeq, event.MessageSeq)
		require.Equal(t, sendtrace.ResultOK, event.Result)
		require.Equal(t, 2, event.Attempt)
		require.Equal(t, "client-1", event.ClientMsgNo)
		require.Equal(t, "u1", event.FromUID)
		return
	}
	t.Fatalf("trace event %s/%s not found in %#v", stage, traceID, events)
}

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

func TestAppendTraceBatchSelectionPreservesRestoredSidecars(t *testing.T) {
	sink := &reactorDetailSink{
		limits: sendtrace.DetailLimits{MaxItemsPerBatch: 4},
		decisions: map[string]sendtrace.DetailDecision{
			"trace-keep": {Keep: true, Reason: "debug"},
		},
	}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)

	q := newAppendQueue(appendQueueConfig{MaxRecords: 8})
	require.NoError(t, q.push(appendRequest{
		opID: 1,
		req: ch.AppendBatchRequest{
			ChannelID: ch.ChannelID{ID: "room", Type: 1},
			Messages:  []ch.Message{{TraceID: "trace-keep", ClientMsgNo: "client-1"}},
		},
		records: []ch.Record{{ID: 1}},
	}))
	batch := q.popBatch(10, nil)
	batch.trace = selectAppendTraceBatch(batch)
	require.Len(t, batch.trace.items, 1)

	q.restoreFront(batch)
	sink.decisions["trace-keep"] = sendtrace.DetailDecision{}
	retry := q.popBatch(11, nil)
	retry.trace = selectAppendTraceBatch(retry)

	require.Len(t, retry.trace.items, 1)
	require.Equal(t, "trace-keep", retry.trace.items[0].traceID)
	require.Equal(t, 0, retry.trace.items[0].requestIdx)
	require.Equal(t, 0, retry.trace.items[0].recordIdx)
	require.Len(t, sink.keys, 1)
}

func TestReactorRecordsLeaderTraceRecordCountPerRequest(t *testing.T) {
	traceSink := &recordingTraceSink{}
	restore := sendtrace.SetSink(traceSink)
	t.Cleanup(restore)

	now := time.Now()
	req := appendRequest{
		enqueuedAt: now.Add(-2 * time.Millisecond),
		records:    []ch.Record{{ID: 2}},
	}
	timing := appendTiming{
		storeSubmittedAt: now.Add(-time.Millisecond),
		traceItems: []appendTraceItem{{
			traceID:        "trace-second",
			clientMsgNo:    "client-2",
			requestIdx:     1,
			recordIdx:      2,
			localRecordIdx: 0,
		}},
		recordCount: 1,
	}
	batch := appendBatch{
		requests: []appendRequest{
			{records: []ch.Record{{ID: 1}, {ID: 2}}},
			req,
		},
		records: []ch.Record{{ID: 1}, {ID: 2}, {ID: 3}},
	}

	var r Reactor
	r.recordLeaderQueueAndLocalDurableTrace(req, timing, batch, now, 3, nil)

	requireTraceEventRecordCount(t, traceSink.events, sendtrace.StageReplicaLeaderQueueWait, "trace-second", 1)
	requireTraceEventRecordCount(t, traceSink.events, sendtrace.StageReplicaLeaderLocalDurable, "trace-second", 1)
}

func requireTraceEventRecordCount(t *testing.T, events []sendtrace.Event, stage sendtrace.Stage, traceID string, recordCount int) {
	t.Helper()
	for _, event := range events {
		if event.Stage == stage && event.TraceID == traceID {
			require.Equal(t, recordCount, event.RecordCount)
			return
		}
	}
	t.Fatalf("trace event %s/%s not found in %#v", stage, traceID, events)
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
