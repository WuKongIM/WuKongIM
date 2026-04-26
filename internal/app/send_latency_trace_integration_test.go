//go:build integration
// +build integration

package app

import (
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
