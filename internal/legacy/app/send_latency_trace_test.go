package app

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/stretchr/testify/require"
)

type sendTraceBreakdown struct {
	ackLatency              time.Duration
	gatewayMessagesSend     time.Duration
	gatewayWriteSendack     time.Duration
	messageSendDurable      time.Duration
	channelLocalAppend      time.Duration
	leaderQueueWait         time.Duration
	leaderLocalDurable      time.Duration
	leaderDurableMuWait     time.Duration
	leaderDurableAppend     time.Duration
	storeCommitQueueWait    []time.Duration
	storeCommitBatchCollect []time.Duration
	storeCommitPebbleSync   []time.Duration
	storeCommitPublish      []time.Duration
	leaderQuorumWait        time.Duration
	followerApplyDurable    []time.Duration
	runtimeRetryScheduled   []time.Duration
	runtimeFetchRequestSend []time.Duration
	runtimeLanePollSend     []time.Duration
	runtimeCursorDeltaSend  []time.Duration
}

type sendTraceStageSummary struct {
	Stage         sendtrace.Stage
	Count         int
	P50           time.Duration
	P95           time.Duration
	P99           time.Duration
	Max           time.Duration
	RequestCount  int
	RecordCount   int
	ByteCount     int
	QueueDepthMax int
}

func summarizeSendTraceStages(events []sendtrace.Event, stages ...sendtrace.Stage) []sendTraceStageSummary {
	summaries := make([]sendTraceStageSummary, 0, len(stages))
	for _, stage := range stages {
		durations := make([]time.Duration, 0)
		summary := sendTraceStageSummary{Stage: stage}
		for _, event := range events {
			if event.Stage != stage {
				continue
			}
			durations = append(durations, event.Duration)
			summary.RequestCount += event.RequestCount
			summary.RecordCount += event.RecordCount
			summary.ByteCount += event.ByteCount
			if event.QueueDepth > summary.QueueDepthMax {
				summary.QueueDepthMax = event.QueueDepth
			}
		}
		if len(durations) == 0 {
			continue
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		summary.Count = len(durations)
		summary.P50 = percentileSendStressDuration(durations, 0.50)
		summary.P95 = percentileSendStressDuration(durations, 0.95)
		summary.P99 = percentileSendStressDuration(durations, 0.99)
		summary.Max = durations[len(durations)-1]
		summaries = append(summaries, summary)
	}
	return summaries
}

func formatSendTraceStageSummary(summary sendTraceStageSummary) string {
	return fmt.Sprintf(
		"stage=%s count=%d p50=%s p95=%s p99=%s max=%s requests=%d records=%d bytes=%d queue_depth_max=%d",
		summary.Stage,
		summary.Count,
		summary.P50,
		summary.P95,
		summary.P99,
		summary.Max,
		summary.RequestCount,
		summary.RecordCount,
		summary.ByteCount,
		summary.QueueDepthMax,
	)
}

func buildSendTraceBreakdown(events []sendtrace.Event, record sendStressRecord) (sendTraceBreakdown, []string) {
	return buildSendTraceBreakdownForMode(events, record, false)
}

func buildSendTraceBreakdownForMode(events []sendtrace.Event, record sendStressRecord, requireLaneStages bool) (sendTraceBreakdown, []string) {
	breakdown := sendTraceBreakdown{ackLatency: record.AckLatency}
	missing := make([]string, 0)
	seq := record.MessageSeq
	for _, event := range events {
		switch event.Stage {
		case sendtrace.StageGatewayMessagesSend:
			if event.ClientMsgNo == record.ClientMsgNo {
				breakdown.gatewayMessagesSend = event.Duration
			}
		case sendtrace.StageGatewayWriteSendack:
			if event.ClientMsgNo == record.ClientMsgNo {
				breakdown.gatewayWriteSendack = event.Duration
			}
		case sendtrace.StageMessageSendDurable:
			if event.ClientMsgNo == record.ClientMsgNo {
				breakdown.messageSendDurable = event.Duration
			}
		case sendtrace.StageChannelAppendLocal:
			if event.MessageSeq == seq {
				breakdown.channelLocalAppend = event.Duration
			}
		case sendtrace.StageReplicaLeaderQueueWait:
			if event.ContainsMessageSeq(seq) {
				breakdown.leaderQueueWait = event.Duration
			}
		case sendtrace.StageReplicaLeaderLocalDurable:
			if event.ContainsMessageSeq(seq) {
				breakdown.leaderLocalDurable = event.Duration
			}
		case sendtrace.StageReplicaLeaderDurableMuWait:
			if event.ChannelKey == record.ChannelKey || event.ContainsMessageSeq(seq) {
				breakdown.leaderDurableMuWait = event.Duration
			}
		case sendtrace.StageReplicaLeaderDurableAppend:
			if event.ChannelKey == record.ChannelKey || event.ContainsMessageSeq(seq) {
				breakdown.leaderDurableAppend = event.Duration
			}
		case sendtrace.StageStoreCommitQueueWait:
			if event.ChannelKey == record.ChannelKey || event.ContainsMessageSeq(seq) {
				breakdown.storeCommitQueueWait = append(breakdown.storeCommitQueueWait, event.Duration)
			}
		case sendtrace.StageStoreCommitBatchCollect:
			if event.ChannelKey == record.ChannelKey || event.ContainsMessageSeq(seq) {
				breakdown.storeCommitBatchCollect = append(breakdown.storeCommitBatchCollect, event.Duration)
			}
		case sendtrace.StageStoreCommitPebbleSync:
			if event.ChannelKey == record.ChannelKey || event.ContainsMessageSeq(seq) {
				breakdown.storeCommitPebbleSync = append(breakdown.storeCommitPebbleSync, event.Duration)
			}
		case sendtrace.StageStoreCommitPublish:
			if event.ChannelKey == record.ChannelKey || event.ContainsMessageSeq(seq) {
				breakdown.storeCommitPublish = append(breakdown.storeCommitPublish, event.Duration)
			}
		case sendtrace.StageReplicaLeaderQuorumWait:
			if event.ContainsMessageSeq(seq) {
				breakdown.leaderQuorumWait = event.Duration
			}
		case sendtrace.StageReplicaFollowerApplyDurable:
			if event.ContainsMessageSeq(seq) {
				breakdown.followerApplyDurable = append(breakdown.followerApplyDurable, event.Duration)
			}
		case sendtrace.StageRuntimeFollowerRetryScheduled:
			breakdown.runtimeRetryScheduled = append(breakdown.runtimeRetryScheduled, event.Duration)
		case sendtrace.StageRuntimeFetchRequestSend:
			breakdown.runtimeFetchRequestSend = append(breakdown.runtimeFetchRequestSend, event.Duration)
		case sendtrace.StageRuntimeLanePollRequestSend:
			breakdown.runtimeLanePollSend = append(breakdown.runtimeLanePollSend, event.Duration)
		case sendtrace.StageRuntimeLaneCursorDeltaSend:
			breakdown.runtimeCursorDeltaSend = append(breakdown.runtimeCursorDeltaSend, event.Duration)
		}
	}
	sort.Slice(breakdown.followerApplyDurable, func(i, j int) bool {
		return breakdown.followerApplyDurable[i] < breakdown.followerApplyDurable[j]
	})
	sort.Slice(breakdown.storeCommitQueueWait, func(i, j int) bool {
		return breakdown.storeCommitQueueWait[i] < breakdown.storeCommitQueueWait[j]
	})
	sort.Slice(breakdown.storeCommitBatchCollect, func(i, j int) bool {
		return breakdown.storeCommitBatchCollect[i] < breakdown.storeCommitBatchCollect[j]
	})
	sort.Slice(breakdown.storeCommitPebbleSync, func(i, j int) bool {
		return breakdown.storeCommitPebbleSync[i] < breakdown.storeCommitPebbleSync[j]
	})
	sort.Slice(breakdown.storeCommitPublish, func(i, j int) bool {
		return breakdown.storeCommitPublish[i] < breakdown.storeCommitPublish[j]
	})
	sort.Slice(breakdown.runtimeRetryScheduled, func(i, j int) bool {
		return breakdown.runtimeRetryScheduled[i] < breakdown.runtimeRetryScheduled[j]
	})
	sort.Slice(breakdown.runtimeFetchRequestSend, func(i, j int) bool {
		return breakdown.runtimeFetchRequestSend[i] < breakdown.runtimeFetchRequestSend[j]
	})
	sort.Slice(breakdown.runtimeLanePollSend, func(i, j int) bool {
		return breakdown.runtimeLanePollSend[i] < breakdown.runtimeLanePollSend[j]
	})
	sort.Slice(breakdown.runtimeCursorDeltaSend, func(i, j int) bool {
		return breakdown.runtimeCursorDeltaSend[i] < breakdown.runtimeCursorDeltaSend[j]
	})
	if breakdown.gatewayMessagesSend <= 0 {
		missing = append(missing, string(sendtrace.StageGatewayMessagesSend))
	}
	if breakdown.gatewayWriteSendack <= 0 {
		missing = append(missing, string(sendtrace.StageGatewayWriteSendack))
	}
	if breakdown.messageSendDurable <= 0 {
		missing = append(missing, string(sendtrace.StageMessageSendDurable))
	}
	if breakdown.channelLocalAppend <= 0 {
		missing = append(missing, string(sendtrace.StageChannelAppendLocal))
	}
	if breakdown.leaderQueueWait <= 0 {
		missing = append(missing, string(sendtrace.StageReplicaLeaderQueueWait))
	}
	if breakdown.leaderLocalDurable <= 0 {
		missing = append(missing, string(sendtrace.StageReplicaLeaderLocalDurable))
	}
	if breakdown.leaderDurableMuWait <= 0 {
		missing = append(missing, string(sendtrace.StageReplicaLeaderDurableMuWait))
	}
	if breakdown.leaderDurableAppend <= 0 {
		missing = append(missing, string(sendtrace.StageReplicaLeaderDurableAppend))
	}
	if len(breakdown.storeCommitQueueWait) == 0 {
		missing = append(missing, string(sendtrace.StageStoreCommitQueueWait))
	}
	if len(breakdown.storeCommitBatchCollect) == 0 {
		missing = append(missing, string(sendtrace.StageStoreCommitBatchCollect))
	}
	if len(breakdown.storeCommitPebbleSync) == 0 {
		missing = append(missing, string(sendtrace.StageStoreCommitPebbleSync))
	}
	if len(breakdown.storeCommitPublish) == 0 {
		missing = append(missing, string(sendtrace.StageStoreCommitPublish))
	}
	if breakdown.leaderQuorumWait <= 0 {
		missing = append(missing, string(sendtrace.StageReplicaLeaderQuorumWait))
	}
	if len(breakdown.followerApplyDurable) == 0 {
		missing = append(missing, string(sendtrace.StageReplicaFollowerApplyDurable))
	}
	if requireLaneStages {
		if len(breakdown.runtimeLanePollSend) == 0 {
			missing = append(missing, string(sendtrace.StageRuntimeLanePollRequestSend))
		}
		if len(breakdown.runtimeCursorDeltaSend) == 0 {
			missing = append(missing, string(sendtrace.StageRuntimeLaneCursorDeltaSend))
		}
	}
	return breakdown, missing
}

func TestSendTraceBreakdownMatchesSeqRanges(t *testing.T) {
	record := sendStressRecord{ClientMsgNo: "trace-1", ChannelKey: "channel/2/ZzE", MessageSeq: 7, AckLatency: 9 * time.Millisecond}
	events := []sendtrace.Event{
		{Stage: sendtrace.StageGatewayMessagesSend, ClientMsgNo: "trace-1", Duration: 2 * time.Millisecond},
		{Stage: sendtrace.StageGatewayWriteSendack, ClientMsgNo: "trace-1", Duration: 500 * time.Microsecond},
		{Stage: sendtrace.StageMessageSendDurable, ClientMsgNo: "trace-1", Duration: 8 * time.Millisecond},
		{Stage: sendtrace.StageChannelAppendLocal, MessageSeq: 7, Duration: 8 * time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderQueueWait, RangeStart: 7, RangeEnd: 7, Duration: time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderLocalDurable, RangeStart: 7, RangeEnd: 7, Duration: 2 * time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderDurableMuWait, ChannelKey: "channel/2/ZzE", Duration: 200 * time.Microsecond},
		{Stage: sendtrace.StageReplicaLeaderDurableAppend, ChannelKey: "channel/2/ZzE", Duration: 1500 * time.Microsecond},
		{Stage: sendtrace.StageStoreCommitQueueWait, ChannelKey: "channel/2/ZzE", Duration: 300 * time.Microsecond},
		{Stage: sendtrace.StageStoreCommitBatchCollect, ChannelKey: "channel/2/ZzE", Duration: 200 * time.Microsecond},
		{Stage: sendtrace.StageStoreCommitPebbleSync, ChannelKey: "channel/2/ZzE", Duration: time.Millisecond},
		{Stage: sendtrace.StageStoreCommitPublish, ChannelKey: "channel/2/ZzE", Duration: 100 * time.Microsecond},
		{Stage: sendtrace.StageReplicaLeaderQuorumWait, RangeStart: 7, RangeEnd: 7, Duration: 5 * time.Millisecond},
		{Stage: sendtrace.StageReplicaFollowerApplyDurable, RangeStart: 7, RangeEnd: 7, Duration: 3 * time.Millisecond},
	}

	breakdown, missing := buildSendTraceBreakdown(events, record)
	require.Empty(t, missing)
	require.Equal(t, 9*time.Millisecond, breakdown.ackLatency)
	require.Equal(t, 2*time.Millisecond, breakdown.gatewayMessagesSend)
	require.Equal(t, 500*time.Microsecond, breakdown.gatewayWriteSendack)
	require.Equal(t, 8*time.Millisecond, breakdown.messageSendDurable)
	require.Equal(t, 8*time.Millisecond, breakdown.channelLocalAppend)
	require.Equal(t, time.Millisecond, breakdown.leaderQueueWait)
	require.Equal(t, 2*time.Millisecond, breakdown.leaderLocalDurable)
	require.Equal(t, 200*time.Microsecond, breakdown.leaderDurableMuWait)
	require.Equal(t, 1500*time.Microsecond, breakdown.leaderDurableAppend)
	require.Equal(t, []time.Duration{300 * time.Microsecond}, breakdown.storeCommitQueueWait)
	require.Equal(t, []time.Duration{200 * time.Microsecond}, breakdown.storeCommitBatchCollect)
	require.Equal(t, []time.Duration{time.Millisecond}, breakdown.storeCommitPebbleSync)
	require.Equal(t, []time.Duration{100 * time.Microsecond}, breakdown.storeCommitPublish)
	require.Equal(t, 5*time.Millisecond, breakdown.leaderQuorumWait)
	require.Equal(t, []time.Duration{3 * time.Millisecond}, breakdown.followerApplyDurable)
}

func TestSendTraceBreakdownReportsMissingStages(t *testing.T) {
	record := sendStressRecord{ClientMsgNo: "trace-1", MessageSeq: 7}
	_, missing := buildSendTraceBreakdown(nil, record)
	require.Equal(t, []string{
		string(sendtrace.StageGatewayMessagesSend),
		string(sendtrace.StageGatewayWriteSendack),
		string(sendtrace.StageMessageSendDurable),
		string(sendtrace.StageChannelAppendLocal),
		string(sendtrace.StageReplicaLeaderQueueWait),
		string(sendtrace.StageReplicaLeaderLocalDurable),
		string(sendtrace.StageReplicaLeaderDurableMuWait),
		string(sendtrace.StageReplicaLeaderDurableAppend),
		string(sendtrace.StageStoreCommitQueueWait),
		string(sendtrace.StageStoreCommitBatchCollect),
		string(sendtrace.StageStoreCommitPebbleSync),
		string(sendtrace.StageStoreCommitPublish),
		string(sendtrace.StageReplicaLeaderQuorumWait),
		string(sendtrace.StageReplicaFollowerApplyDurable),
	}, missing)
}

func TestSendLatencyTraceBreakdownCapturesLaneStages(t *testing.T) {
	record := sendStressRecord{ClientMsgNo: "trace-1", ChannelKey: "channel/2/ZzE", MessageSeq: 7, AckLatency: 9 * time.Millisecond}
	events := []sendtrace.Event{
		{Stage: sendtrace.StageGatewayMessagesSend, ClientMsgNo: "trace-1", Duration: 2 * time.Millisecond},
		{Stage: sendtrace.StageGatewayWriteSendack, ClientMsgNo: "trace-1", Duration: 500 * time.Microsecond},
		{Stage: sendtrace.StageMessageSendDurable, ClientMsgNo: "trace-1", Duration: 8 * time.Millisecond},
		{Stage: sendtrace.StageChannelAppendLocal, MessageSeq: 7, Duration: 8 * time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderQueueWait, RangeStart: 7, RangeEnd: 7, Duration: time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderLocalDurable, RangeStart: 7, RangeEnd: 7, Duration: 2 * time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderDurableMuWait, ChannelKey: "channel/2/ZzE", Duration: 200 * time.Microsecond},
		{Stage: sendtrace.StageReplicaLeaderDurableAppend, ChannelKey: "channel/2/ZzE", Duration: 1500 * time.Microsecond},
		{Stage: sendtrace.StageStoreCommitQueueWait, ChannelKey: "channel/2/ZzE", Duration: 300 * time.Microsecond},
		{Stage: sendtrace.StageStoreCommitBatchCollect, ChannelKey: "channel/2/ZzE", Duration: 200 * time.Microsecond},
		{Stage: sendtrace.StageStoreCommitPebbleSync, ChannelKey: "channel/2/ZzE", Duration: time.Millisecond},
		{Stage: sendtrace.StageStoreCommitPublish, ChannelKey: "channel/2/ZzE", Duration: 100 * time.Microsecond},
		{Stage: sendtrace.StageReplicaLeaderQuorumWait, RangeStart: 7, RangeEnd: 7, Duration: 5 * time.Millisecond},
		{Stage: sendtrace.StageReplicaFollowerApplyDurable, RangeStart: 7, RangeEnd: 7, Duration: 3 * time.Millisecond},
		{Stage: sendtrace.StageRuntimeLanePollRequestSend, Duration: 900 * time.Microsecond},
		{Stage: sendtrace.StageRuntimeLaneCursorDeltaSend, Duration: 700 * time.Microsecond},
	}

	breakdown, missing := buildSendTraceBreakdownForMode(events, record, true)
	require.Empty(t, missing)
	require.Equal(t, []time.Duration{900 * time.Microsecond}, breakdown.runtimeLanePollSend)
	require.Equal(t, []time.Duration{700 * time.Microsecond}, breakdown.runtimeCursorDeltaSend)
}

func TestSendLatencyTraceBreakdownReportsMissingLaneStagesInLongPollMode(t *testing.T) {
	record := sendStressRecord{ClientMsgNo: "trace-1", MessageSeq: 7}
	_, missing := buildSendTraceBreakdownForMode(nil, record, true)
	require.Contains(t, missing, string(sendtrace.StageRuntimeLanePollRequestSend))
	require.Contains(t, missing, string(sendtrace.StageRuntimeLaneCursorDeltaSend))
}

func TestSendTraceEventContainsMessageSeq(t *testing.T) {
	event := sendtrace.Event{MessageSeq: 11, RangeStart: 10, RangeEnd: 12}
	require.True(t, event.ContainsMessageSeq(11))
	require.True(t, event.ContainsMessageSeq(10))
	require.True(t, event.ContainsMessageSeq(12))
	require.False(t, event.ContainsMessageSeq(13))
}

func TestSummarizeSendTraceStagesAggregatesDurationsAndBatchStats(t *testing.T) {
	events := []sendtrace.Event{
		{Stage: sendtrace.StageStoreCommitPebbleSync, Duration: 3 * time.Millisecond, RequestCount: 2, RecordCount: 4, ByteCount: 40, QueueDepth: 1},
		{Stage: sendtrace.StageStoreCommitPebbleSync, Duration: time.Millisecond, RequestCount: 1, RecordCount: 2, ByteCount: 20, QueueDepth: 3},
		{Stage: sendtrace.StageStoreCommitPebbleSync, Duration: 2 * time.Millisecond, RequestCount: 1, RecordCount: 3, ByteCount: 30, QueueDepth: 2},
		{Stage: sendtrace.StageStoreCommitQueueWait, Duration: 500 * time.Microsecond, RequestCount: 1},
	}

	summaries := summarizeSendTraceStages(events, sendtrace.StageStoreCommitPebbleSync, sendtrace.StageStoreCommitQueueWait)

	require.Len(t, summaries, 2)
	require.Equal(t, sendtrace.StageStoreCommitPebbleSync, summaries[0].Stage)
	require.Equal(t, 3, summaries[0].Count)
	require.Equal(t, 2*time.Millisecond, summaries[0].P50)
	require.Equal(t, 3*time.Millisecond, summaries[0].P95)
	require.Equal(t, 3*time.Millisecond, summaries[0].Max)
	require.Equal(t, 4, summaries[0].RequestCount)
	require.Equal(t, 9, summaries[0].RecordCount)
	require.Equal(t, 90, summaries[0].ByteCount)
	require.Equal(t, 3, summaries[0].QueueDepthMax)
	require.Equal(t, sendtrace.StageStoreCommitQueueWait, summaries[1].Stage)
	require.Equal(t, 1, summaries[1].Count)
}

func Example_buildSendTraceBreakdown() {
	record := sendStressRecord{ClientMsgNo: "trace-1", MessageSeq: 7}
	breakdown, missing := buildSendTraceBreakdown([]sendtrace.Event{{
		Stage:       sendtrace.StageGatewayMessagesSend,
		ClientMsgNo: "trace-1",
		Duration:    2 * time.Millisecond,
	}}, record)
	fmt.Println(breakdown.gatewayMessagesSend > 0, len(missing) > 0)
	// Output: true true
}
