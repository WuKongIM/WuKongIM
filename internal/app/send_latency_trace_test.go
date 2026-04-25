package app

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	"github.com/stretchr/testify/require"
)

const sendTraceEnv = "WK_SEND_TRACE"

type sendTraceCollector struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (c *sendTraceCollector) RecordSendTrace(event sendtrace.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *sendTraceCollector) Snapshot() []sendtrace.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]sendtrace.Event, len(c.events))
	copy(out, c.events)
	return out
}

func (c *sendTraceCollector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = nil
}

type sendTraceBreakdown struct {
	ackLatency              time.Duration
	gatewayMessagesSend     time.Duration
	gatewayWriteSendack     time.Duration
	messageSendDurable      time.Duration
	channelLocalAppend      time.Duration
	leaderQueueWait         time.Duration
	leaderLocalDurable      time.Duration
	leaderQuorumWait        time.Duration
	followerApplyDurable    []time.Duration
	runtimeRetryScheduled   []time.Duration
	runtimeFetchRequestSend []time.Duration
	runtimeLanePollSend     []time.Duration
	runtimeCursorDeltaSend  []time.Duration
}

func TestSendTraceThreeNodeRecordsCriticalStages(t *testing.T) {
	if !envBool(sendTraceEnv, false) {
		t.Skipf("set %s=1 to enable send trace measurement", sendTraceEnv)
	}

	collector := &sendTraceCollector{}
	restore := sendtrace.SetSink(collector)
	defer restore()

	cfg := sendStressConfig{
		Mode:                 sendStressModeLatency,
		Workers:              1,
		Senders:              1,
		MessagesPerWorker:    1,
		DialTimeout:          3 * time.Second,
		AckTimeout:           15 * time.Second,
		MaxInflightPerWorker: 1,
	}

	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]
	targets := preloadSendStressTargets(t, harness, leader, cfg, 2, sendStressScenarioSingleHotChannel)
	require.Len(t, targets, 1)
	target := targets[0]
	conn := runSendStressClient(t, harness.apps[target.ConnectNodeID], target.SenderUID, cfg)

	client := sendStressWorkerClient{
		target:  target,
		conn:    conn,
		reader:  newSendStressFrameReader(conn),
		writeMu: &sync.Mutex{},
	}
	defer func() { _ = client.conn.Close() }()

	_, warmupFailure, ok := executeSendStressAttempt(client, 0, "warmup", -1, 1, "trace-warmup", []byte("warmup"), cfg.AckTimeout)
	require.True(t, ok, warmupFailure)

	time.Sleep(1100 * time.Millisecond)
	collector.Reset()

	record, failure, ok := executeSendStressAttempt(client, 0, "measure", 0, 2, "trace-measure-1", []byte("measure"), cfg.AckTimeout)
	require.True(t, ok, failure)

	breakdown, missing := buildSendTraceBreakdown(collector.Snapshot(), record)
	require.Empty(t, missing)
	require.NotEmpty(t, breakdown.followerApplyDurable)

	t.Logf("send trace: client_msg_no=%s message_seq=%d ack=%s gateway_send=%s gateway_ack=%s message_durable=%s local_append=%s leader_queue=%s leader_durable=%s leader_quorum=%s follower_apply=%v",
		record.ClientMsgNo,
		record.MessageSeq,
		breakdown.ackLatency,
		breakdown.gatewayMessagesSend,
		breakdown.gatewayWriteSendack,
		breakdown.messageSendDurable,
		breakdown.channelLocalAppend,
		breakdown.leaderQueueWait,
		breakdown.leaderLocalDurable,
		breakdown.leaderQuorumWait,
		breakdown.followerApplyDurable,
	)
	if len(breakdown.runtimeRetryScheduled) > 0 || len(breakdown.runtimeFetchRequestSend) > 0 || len(breakdown.runtimeLanePollSend) > 0 || len(breakdown.runtimeCursorDeltaSend) > 0 {
		t.Logf(
			"runtime trace: retry_scheduled=%v fetch_request_send=%v lane_poll_send=%v cursor_delta_send=%v",
			breakdown.runtimeRetryScheduled,
			breakdown.runtimeFetchRequestSend,
			breakdown.runtimeLanePollSend,
			breakdown.runtimeCursorDeltaSend,
		)
	}
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
	record := sendStressRecord{ClientMsgNo: "trace-1", MessageSeq: 7, AckLatency: 9 * time.Millisecond}
	events := []sendtrace.Event{
		{Stage: sendtrace.StageGatewayMessagesSend, ClientMsgNo: "trace-1", Duration: 2 * time.Millisecond},
		{Stage: sendtrace.StageGatewayWriteSendack, ClientMsgNo: "trace-1", Duration: 500 * time.Microsecond},
		{Stage: sendtrace.StageMessageSendDurable, ClientMsgNo: "trace-1", Duration: 8 * time.Millisecond},
		{Stage: sendtrace.StageChannelAppendLocal, MessageSeq: 7, Duration: 8 * time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderQueueWait, RangeStart: 7, RangeEnd: 7, Duration: time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderLocalDurable, RangeStart: 7, RangeEnd: 7, Duration: 2 * time.Millisecond},
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
		string(sendtrace.StageReplicaLeaderQuorumWait),
		string(sendtrace.StageReplicaFollowerApplyDurable),
	}, missing)
}

func TestSendLatencyTraceBreakdownCapturesLaneStages(t *testing.T) {
	record := sendStressRecord{ClientMsgNo: "trace-1", MessageSeq: 7, AckLatency: 9 * time.Millisecond}
	events := []sendtrace.Event{
		{Stage: sendtrace.StageGatewayMessagesSend, ClientMsgNo: "trace-1", Duration: 2 * time.Millisecond},
		{Stage: sendtrace.StageGatewayWriteSendack, ClientMsgNo: "trace-1", Duration: 500 * time.Microsecond},
		{Stage: sendtrace.StageMessageSendDurable, ClientMsgNo: "trace-1", Duration: 8 * time.Millisecond},
		{Stage: sendtrace.StageChannelAppendLocal, MessageSeq: 7, Duration: 8 * time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderQueueWait, RangeStart: 7, RangeEnd: 7, Duration: time.Millisecond},
		{Stage: sendtrace.StageReplicaLeaderLocalDurable, RangeStart: 7, RangeEnd: 7, Duration: 2 * time.Millisecond},
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
