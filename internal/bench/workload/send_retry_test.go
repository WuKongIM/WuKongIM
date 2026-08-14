package workload

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestPersonRetryAcceptsLateSuccessFromEarlierAttemptAndReconcilesEvidence(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.readErrors = append(raw.readErrors, context.DeadlineExceeded)
	raw.sendacks = append(raw.sendacks, &frame.SendackPacket{
		ClientSeq:   1,
		ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-run-ch7-msg7",
		ReasonCode:  frame.ReasonSuccess,
	})
	clients := WrapPersonClientsForConcurrentReads(map[string]PersonClient{
		"u1": raw,
		"u2": newRecordingPersonClient(),
	})
	registry := metrics.NewRegistry()
	w, err := NewPersonWorkload(PersonConfig{
		RunID: "run-a", ProfileName: "profile-a", TrafficName: "traffic-a",
		SenderUID: "u1", RecipientUID: "u2", ClientMsgPrefix: "bench-msg",
		RetryEnabled: true, Metrics: registry,
		sleep: func(context.Context, time.Duration) error { return nil },
	}, clients)
	require.NoError(t, err)

	require.NoError(t, w.SendOne(context.Background(), 7))
	require.Len(t, raw.sentFrames, 2)
	require.Equal(t, raw.sentFrames[0].ClientMsgNo, raw.sentFrames[1].ClientMsgNo)
	require.NotEqual(t, raw.sentFrames[0].ClientSeq, raw.sentFrames[1].ClientSeq)
	labels := personSendLabels("run", "profile-a", "traffic-a")
	require.Equal(t, uint64(1), registry.CounterValue("logical_identity_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("logical_sent_total", labels))
	require.Equal(t, uint64(2), registry.CounterValue("send_attempt_total", labels))
	require.Equal(t, uint64(2), registry.CounterValue("attempt_record_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("retry_attempt_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("sendack_success_total", labels))
	require.Zero(t, registry.CounterValue("person_send_success_total", labels), "report success must not double count the same SENDACK")
	require.Zero(t, registry.CounterValue("logical_terminal_error_total", labels))
	require.Zero(t, registry.CounterValue("retry_exhausted_total", labels))
	require.Zero(t, registry.CounterValue("client_msg_no_mismatch_total", labels))
	require.Zero(t, registry.GaugeValue("logical_remaining", labels))
	require.Equal(t, float64(4), registry.GaugeValue("configured_maximum_attempts", labels))
	require.Equal(t, float64(2), registry.GaugeValue("maximum_observed_attempts", labels))
}

func TestPersonRetryStopsImmediatelyForNonRetriableSendack(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.sendacks = append(raw.sendacks, &frame.SendackPacket{
		ClientSeq:   1,
		ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-run-ch7-msg7",
		ReasonCode:  frame.ReasonNotAllowSend,
	})
	clients := WrapPersonClientsForConcurrentReads(map[string]PersonClient{
		"u1": raw,
		"u2": newRecordingPersonClient(),
	})
	registry := metrics.NewRegistry()
	w, err := NewPersonWorkload(PersonConfig{
		RunID: "run-a", ProfileName: "profile-a", TrafficName: "traffic-a",
		SenderUID: "u1", RecipientUID: "u2", ClientMsgPrefix: "bench-msg",
		RetryEnabled: true, Metrics: registry,
		sleep: func(context.Context, time.Duration) error { return nil },
	}, clients)
	require.NoError(t, err)

	err = w.SendOne(context.Background(), 7)
	require.Error(t, err)
	require.Len(t, raw.sentFrames, 1)
	labels := personSendLabels("run", "profile-a", "traffic-a")
	require.Equal(t, uint64(1), registry.CounterValue("logical_terminal_error_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("logical_correctness_error_total", labels))
	require.Zero(t, registry.CounterValue("retry_attempt_total", labels))
	require.Zero(t, registry.CounterValue("retry_exhausted_total", labels))
	require.Zero(t, registry.GaugeValue("logical_remaining", labels))
}

func TestPersonRetryExhaustsAfterExactlyFourAttempts(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.readErrors = []error{context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded}
	clients := WrapPersonClientsForConcurrentReads(map[string]PersonClient{
		"u1": raw,
		"u2": newRecordingPersonClient(),
	})
	registry := metrics.NewRegistry()
	var delays []time.Duration
	w, err := NewPersonWorkload(PersonConfig{
		RunID: "run-a", ProfileName: "profile-a", TrafficName: "traffic-a",
		SenderUID: "u1", RecipientUID: "u2", ClientMsgPrefix: "bench-msg",
		RetryEnabled: true, Metrics: registry,
		sleep: func(_ context.Context, delay time.Duration) error { delays = append(delays, delay); return nil },
	}, clients)
	require.NoError(t, err)

	err = w.SendOne(context.Background(), 7)
	require.Error(t, err)
	require.Len(t, raw.sentFrames, 4)
	require.Equal(t, []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}, delays)
	for attempt := 1; attempt < len(raw.sentFrames); attempt++ {
		require.Equal(t, raw.sentFrames[0].ClientMsgNo, raw.sentFrames[attempt].ClientMsgNo)
		require.NotEqual(t, raw.sentFrames[attempt-1].ClientSeq, raw.sentFrames[attempt].ClientSeq)
	}
	labels := personSendLabels("run", "profile-a", "traffic-a")
	require.Equal(t, uint64(4), registry.CounterValue("send_attempt_total", labels))
	require.Equal(t, uint64(3), registry.CounterValue("retry_attempt_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("retry_exhausted_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("logical_terminal_error_total", labels))
	require.Zero(t, registry.GaugeValue("logical_remaining", labels))
	require.Equal(t, float64(4), registry.GaugeValue("maximum_observed_attempts", labels))
}

func TestPersonRetryIgnoresLateRetriableRejectionFromOlderAttempt(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.readErrors = append(raw.readErrors, context.DeadlineExceeded)
	raw.sendacks = append(raw.sendacks,
		&frame.SendackPacket{ClientSeq: 1, ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-run-ch7-msg7", ReasonCode: frame.ReasonRateLimit},
		&frame.SendackPacket{ClientSeq: 2, ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-run-ch7-msg7", ReasonCode: frame.ReasonSuccess},
	)
	clients := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw, "u2": newRecordingPersonClient()})
	registry := metrics.NewRegistry()
	w, err := NewPersonWorkload(PersonConfig{
		RunID: "run-a", ProfileName: "profile-a", TrafficName: "traffic-a",
		SenderUID: "u1", RecipientUID: "u2", ClientMsgPrefix: "bench-msg",
		RetryEnabled: true, Metrics: registry, sleep: func(context.Context, time.Duration) error { return nil },
	}, clients)
	require.NoError(t, err)

	require.NoError(t, w.SendOne(context.Background(), 7))
	require.Len(t, raw.sentFrames, 2, "old rejection must not schedule a third attempt")
	labels := personSendLabels("run", "profile-a", "traffic-a")
	require.Equal(t, uint64(1), registry.CounterValue("retry_attempt_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("sendack_success_total", labels))
}

func TestPersonRetryTreatsClientMsgNoMismatchAsCorrectnessFailure(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.sendacks = append(raw.sendacks, &frame.SendackPacket{ClientSeq: 1, ClientMsgNo: "wrong", ReasonCode: frame.ReasonSuccess})
	clients := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw, "u2": newRecordingPersonClient()})
	registry := metrics.NewRegistry()
	w, err := NewPersonWorkload(PersonConfig{
		RunID: "run-a", ProfileName: "profile-a", TrafficName: "traffic-a",
		SenderUID: "u1", RecipientUID: "u2", ClientMsgPrefix: "bench-msg",
		RetryEnabled: true, Metrics: registry, sleep: func(context.Context, time.Duration) error { return nil },
	}, clients)
	require.NoError(t, err)

	require.Error(t, w.SendOne(context.Background(), 7))
	labels := personSendLabels("run", "profile-a", "traffic-a")
	require.Equal(t, uint64(1), registry.CounterValue("client_msg_no_mismatch_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("logical_correctness_error_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("logical_terminal_error_total", labels))
}

func TestPersonRetryRecoversTransportAdmissionFailure(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.sendErrors = append(raw.sendErrors, errors.New("send queue full"))
	raw.autoSendack = true
	clients := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw, "u2": newRecordingPersonClient()})
	registry := metrics.NewRegistry()
	w, err := NewPersonWorkload(PersonConfig{
		RunID: "run-a", ProfileName: "profile-a", TrafficName: "traffic-a",
		SenderUID: "u1", RecipientUID: "u2", ClientMsgPrefix: "bench-msg",
		RetryEnabled: true, Metrics: registry, sleep: func(context.Context, time.Duration) error { return nil },
	}, clients)
	require.NoError(t, err)

	require.NoError(t, w.SendOne(context.Background(), 7))
	require.Len(t, raw.sentFrames, 1)
	require.Equal(t, uint64(2), registry.CounterValue("send_attempt_total", personSendLabels("run", "profile-a", "traffic-a")))
}

func TestPersonRetrySeparatesParentCancellationFromTerminalFailure(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.readErrors = append(raw.readErrors, context.Canceled)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	clients := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw, "u2": newRecordingPersonClient()})
	registry := metrics.NewRegistry()
	w, err := NewPersonWorkload(PersonConfig{
		RunID: "run-a", ProfileName: "profile-a", TrafficName: "traffic-a",
		SenderUID: "u1", RecipientUID: "u2", ClientMsgPrefix: "bench-msg",
		RetryEnabled: true, Metrics: registry, sleep: func(context.Context, time.Duration) error { return nil },
	}, clients)
	require.NoError(t, err)

	require.ErrorIs(t, w.SendOne(ctx, 7), context.Canceled)
	labels := personSendLabels("run", "profile-a", "traffic-a")
	require.Zero(t, registry.CounterValue("logical_terminal_error_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("logical_shutdown_cancellation_total", labels))
	require.Zero(t, registry.GaugeValue("logical_remaining", labels))
}

func TestGroupRetryUsesTheSameStableIdentityContract(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.sendacks = append(raw.sendacks,
		&frame.SendackPacket{ClientSeq: 1, ClientMsgNo: "bench-msg-run-a-group-profile-group-send-run-ch0-msg0", ReasonCode: frame.ReasonRateLimit},
		&frame.SendackPacket{ClientSeq: 2, ClientMsgNo: "bench-msg-run-a-group-profile-group-send-run-ch0-msg0", ReasonCode: frame.ReasonSuccess},
	)
	clients := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u-0": raw})
	registry := metrics.NewRegistry()
	w, err := NewGroupWorkload(GroupConfig{
		RunID: "run-a", ProfileName: "group-profile", TrafficName: "group-send", ClientMsgPrefix: "bench-msg",
		RetryEnabled: true,
		Channels:     []GroupChannel{{ChannelIndex: 0, ChannelID: "run-a-group-profile-0", OnlineMembers: []string{"u-0"}}},
		Metrics:      registry, sleep: func(context.Context, time.Duration) error { return nil },
	}, clients)
	require.NoError(t, err)

	require.NoError(t, w.SendOne(context.Background(), 0, 0))
	require.Len(t, raw.sentFrames, 2)
	require.Equal(t, raw.sentFrames[0].ClientMsgNo, raw.sentFrames[1].ClientMsgNo)
	labels := groupSendLabels("run", "group-profile", "group-send")
	require.Equal(t, uint64(1), registry.CounterValue("logical_identity_total", labels))
	require.Equal(t, uint64(2), registry.CounterValue("send_attempt_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("retry_attempt_total", labels))
	require.Equal(t, uint64(1), registry.CounterValue("sendack_success_total", labels))
}

func TestWarmupRetryTailAllowsEveryAttemptAndFixedDelay(t *testing.T) {
	got := warmupOperationTailTimeout(7*time.Second, 11*time.Second, true)
	require.Equal(t, 30*time.Second+600*time.Millisecond, got)
}

func TestWarmupRetryKeepsPerAttemptAckTimeoutWithinSharedFourAttemptTail(t *testing.T) {
	w := &PersonWorkload{cfg: PersonConfig{
		WarmupDuration: time.Minute,
		AckTimeout:     7 * time.Second,
		RecvTimeout:    11 * time.Second,
		RetryEnabled:   true,
	}}
	restore := w.useWarmupTimeouts()
	defer restore()

	require.Equal(t, 7*time.Second, w.cfg.AckTimeout)
	require.WithinDuration(t, time.Now().Add(time.Minute+30*time.Second+600*time.Millisecond), w.warmupOperationDeadline, time.Second)
}

func TestSequentialPersonRunRecordsPlannedAndDispatched(t *testing.T) {
	sender := newRecordingPersonClient()
	sender.autoSendack = true
	w, err := NewPersonWorkload(PersonConfig{
		RunID: "run-a", ProfileName: "profile-a", TrafficName: "traffic-a",
		SenderUID: "u1", RecipientUID: "u2", RetryEnabled: true,
		RunDuration: 30 * time.Millisecond, Rate: model.Rate{PerSecond: 100},
		Metrics: metrics.NewRegistry(), sleep: func(context.Context, time.Duration) error { return nil },
	}, map[string]PersonClient{"u1": sender, "u2": newRecordingPersonClient()})
	require.NoError(t, err)

	require.NoError(t, w.Run(context.Background()))
	labels := personSendLabels("run", "profile-a", "traffic-a")
	require.Equal(t, uint64(3), w.Metrics().CounterValue("workload_scheduler_planned_total", labels))
	require.Equal(t, uint64(3), w.Metrics().CounterValue("workload_scheduler_dispatched_total", labels))
	require.Equal(t, uint64(3), w.Metrics().CounterValue("logical_sent_total", labels))
}
