package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestChannelV2PullHintMetricLabels(t *testing.T) {
	reasonCases := []struct {
		name   string
		reason transport.PullHintReason
		want   string
	}{
		{name: "append", reason: transport.PullHintReasonAppend, want: "append"},
		{name: "resume", reason: transport.PullHintReasonResume, want: "resume"},
		{name: "unknown", reason: transport.PullHintReason(99), want: "unknown"},
	}
	for _, tc := range reasonCases {
		t.Run("reason_"+tc.name, func(t *testing.T) {
			if got := channelV2PullHintReasonLabel(tc.reason); got != tc.want {
				t.Fatalf("channelV2PullHintReasonLabel() = %q, want %q", got, tc.want)
			}
		})
	}

	errorCases := []struct {
		name string
		err  error
		want string
	}{
		{name: "none", want: "none"},
		{name: "not_ready", err: ch.ErrNotReady, want: "not_ready"},
		{name: "remote_not_ready", err: fmt.Errorf("nodetransport: remote error: %s", ch.ErrNotReady), want: "not_ready"},
		{name: "stale_meta", err: fmt.Errorf("wrapped: %w", ch.ErrStaleMeta), want: "stale_meta"},
		{name: "remote_stale_meta", err: fmt.Errorf("nodetransport: remote error: %s", ch.ErrStaleMeta), want: "stale_meta"},
		{name: "channel_not_found", err: ch.ErrChannelNotFound, want: "channel_not_found"},
		{name: "not_leader", err: ch.ErrNotLeader, want: "not_leader"},
		{name: "invalid_config", err: ch.ErrInvalidConfig, want: "invalid_config"},
		{name: "remote_invalid_config", err: fmt.Errorf("nodetransport: remote error: %s", ch.ErrInvalidConfig), want: "invalid_config"},
		{name: "closed", err: ch.ErrClosed, want: "closed"},
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "remote_canceled", err: fmt.Errorf("nodetransport: remote error: %s", context.Canceled), want: "canceled"},
		{name: "timeout", err: context.DeadlineExceeded, want: "timeout"},
		{name: "remote_timeout", err: fmt.Errorf("nodetransport: remote error: %s", context.DeadlineExceeded), want: "timeout"},
		{name: "remote_error", err: fmt.Errorf("nodetransport: remote error: unexpected"), want: "remote_error"},
		{name: "other", err: fmt.Errorf("boom"), want: "other"},
	}
	for _, tc := range errorCases {
		t.Run("error_"+tc.name, func(t *testing.T) {
			if got := channelV2PullHintErrorLabel(tc.err); got != tc.want {
				t.Fatalf("channelV2PullHintErrorLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChannelV2AppendWaitCancelLogLineIncludesSnapshotState(t *testing.T) {
	line := channelV2AppendWaitCancelLogLine(reactor.AppendWaitCancelSnapshot{
		ReactorID:             2,
		Key:                   ch.ChannelKey("1:room"),
		ChannelID:             ch.ChannelID{ID: "room", Type: 1},
		OpID:                  44,
		CommitMode:            ch.CommitModeQuorum,
		Role:                  ch.RoleLeader,
		Leader:                3,
		Epoch:                 5,
		LeaderEpoch:           6,
		LEO:                   9,
		HW:                    7,
		TargetOffset:          9,
		StoreSubmitted:        true,
		StoreCompleted:        true,
		FollowerPullServed:    true,
		AckOffsetObserved:     false,
		HWAdvanced:            false,
		Waiters:               1,
		PendingAppends:        1,
		PendingAppendOrder:    1,
		AppendQueuePending:    0,
		AppendQueueRecords:    0,
		AppendQueueBytes:      0,
		AppendInflight:        false,
		AppendInflightOpID:    0,
		AppendInflightWaiters: 0,
		AppendStoreBlocked:    false,
		PullWaiters:           3,
		FollowerStates:        "2:match=7,lag=2,hint_inflight=false,hint_retry=true",
		Err:                   context.DeadlineExceeded,
	})
	for _, want := range []string{
		"internalv2/app: channelv2 append waiter canceled",
		"reactor=2",
		"key=1:room",
		"channel_id=room",
		"channel_type=1",
		"op=44",
		"commit_mode=quorum",
		"role=leader",
		"leader=3",
		"epoch=5",
		"leader_epoch=6",
		"leo=9",
		"hw=7",
		"target=9",
		"store_completed=true",
		"ack_offset_observed=false",
		"waiters=1",
		"pending_appends=1",
		"pull_waiters=3",
		"follower_states=\"2:match=7,lag=2,hint_inflight=false,hint_retry=true\"",
		"err=context deadline exceeded",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q missing %q", line, want)
		}
	}
}

func TestMessageAppendMetricErrorLabels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "none", want: "none"},
		{name: "backpressured", err: messageusecase.ErrBackpressured, want: "backpressured"},
		{name: "route_not_ready", err: fmt.Errorf("%w: %w", messageusecase.ErrRouteNotReady, ch.ErrNotReady), want: "route_not_ready"},
		{name: "remote_not_ready", err: fmt.Errorf("%w: nodetransport: remote error: %s", messageusecase.ErrAppendFailed, ch.ErrNotReady), want: "route_not_ready"},
		{name: "stale_route", err: fmt.Errorf("%w: %w", messageusecase.ErrStaleRoute, ch.ErrStaleMeta), want: "stale_route"},
		{name: "not_leader", err: messageusecase.ErrNotLeader, want: "not_leader"},
		{name: "channel_not_found", err: messageusecase.ErrChannelNotFound, want: "channel_not_found"},
		{name: "short_result", err: messageusecase.ErrAppendResultMissing, want: "short_result"},
		{name: "invalid_config", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, ch.ErrInvalidConfig), want: "invalid_config"},
		{name: "remote_invalid_config", err: fmt.Errorf("%w: nodetransport: remote error: %s", messageusecase.ErrAppendFailed, ch.ErrInvalidConfig), want: "invalid_config"},
		{name: "closed", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, ch.ErrClosed), want: "closed"},
		{name: "too_many_channels", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, ch.ErrTooManyChannels), want: "too_many_channels"},
		{name: "cluster_not_started", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, clusterv2.ErrNotStarted), want: "not_started"},
		{name: "cluster_stopping", err: fmt.Errorf("%w: %w", messageusecase.ErrAppendFailed, clusterv2.ErrStopping), want: "closed"},
		{name: "timeout", err: fmt.Errorf("%w: %s", messageusecase.ErrAppendFailed, context.DeadlineExceeded), want: "timeout"},
		{name: "wrapped_remote_error", err: fmt.Errorf("%w: nodetransport: remote error: unexpected", messageusecase.ErrAppendFailed), want: "remote_error"},
		{name: "append_failed", err: messageusecase.ErrAppendFailed, want: "append_failed"},
		{name: "remote_error", err: fmt.Errorf("nodetransport: remote error: unexpected"), want: "remote_error"},
		{name: "other", err: fmt.Errorf("boom"), want: "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := messageAppendErrorLabel(tc.err); got != tc.want {
				t.Fatalf("messageAppendErrorLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAppendFailureLogLineIncludesRawError(t *testing.T) {
	err := fmt.Errorf("%w: storage worker returned boom", messageusecase.ErrAppendFailed)

	got := appendFailureLogLine("channelplane", err)

	if !strings.Contains(got, "channelplane") {
		t.Fatalf("appendFailureLogLine() = %q, want path", got)
	}
	if !strings.Contains(got, "storage worker returned boom") {
		t.Fatalf("appendFailureLogLine() = %q, want raw error", got)
	}
}

func TestMessageAppendErrorLogPolicyIncludesTimeout(t *testing.T) {
	cases := []struct {
		label string
		want  bool
	}{
		{label: "append_failed", want: true},
		{label: "timeout", want: true},
		{label: "route_not_ready", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if got := shouldLogMessageAppendError(tc.label); got != tc.want {
				t.Fatalf("shouldLogMessageAppendError(%q) = %v, want %v", tc.label, got, tc.want)
			}
		})
	}
}

func TestDeliveryObserverLogsAsyncErrorsWithoutMetrics(t *testing.T) {
	logger := &recordingAppLogger{}
	observer := deliveryMetricsObserver{logger: logger}

	observer.ObserveRetry(runtimedelivery.RetryEvent{
		Event:      runtimedelivery.DeliveryRetryEventDrop,
		Result:     runtimedelivery.DeliveryResultMaxAttempts,
		ErrorClass: runtimedelivery.DeliveryErrorClassRetryable,
		Attempt:    3,
		QueueDepth: 7,
	})
	observer.ObserveManagerTerminal(runtimedelivery.ManagerTerminalEvent{
		Result:     runtimedelivery.DeliveryResultError,
		ErrorClass: runtimedelivery.DeliveryErrorClassError,
		QueueDepth: 1,
	})

	requireAppLogEvent(t, logger, "WARN", "internalv2.app.delivery.retry_failed")
	requireAppLogEvent(t, logger, "WARN", "internalv2.app.delivery.manager_terminal_failed")
}

func requireAppLogEvent(t *testing.T, logger *recordingAppLogger, level, event string) {
	t.Helper()
	for _, entry := range logger.entries {
		if entry.level != level {
			continue
		}
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return
			}
		}
	}
	t.Fatalf("missing app log event level=%s event=%s entries=%#v", level, event, logger.entries)
}
