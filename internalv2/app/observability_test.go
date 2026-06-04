package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
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

func TestNewWiresDiagnosticsStoreAndSendTraceSink(t *testing.T) {
	cfg := Config{
		Cluster: clusterv2.Config{NodeID: 7},
		Observability: ObservabilityConfig{
			Diagnostics: DiagnosticsConfig{
				SampleRate: 1,
			},
		},
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(false, true, false)

	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.diagnostics == nil {
		t.Fatal("diagnostics store was not wired")
	}
	if app.diagnosticsTracking == nil {
		t.Fatal("diagnostics tracking rules were not wired")
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-internalv2-new",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-internalv2-new"})
	if result.Status != diagnostics.StatusOK {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusOK, result)
	}
	if result.NodeID != 7 {
		t.Fatalf("diagnostics node id = %d, want 7", result.NodeID)
	}
	if len(result.Events) != 1 || result.Events[0].TraceID != "trace-internalv2-new" {
		t.Fatalf("diagnostics events = %#v, want trace-internalv2-new", result.Events)
	}
}

func TestDiagnosticsTrackingRulesDriveSampling(t *testing.T) {
	cfg := Config{
		NodeID: 5,
		Observability: ObservabilityConfig{
			Diagnostics: DiagnosticsConfig{
				SampleRate: 0,
			},
		},
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(false, true, false)
	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rule, err := app.AddDiagnosticsTrackingRule(context.Background(), diagnostics.TrackingRuleInput{
		ID:         "sender-u1",
		Target:     diagnostics.TrackingTargetSenderUID,
		UID:        "u1",
		TTL:        time.Minute,
		SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("AddDiagnosticsTrackingRule() error = %v", err)
	}
	if rule.ID != "sender-u1" {
		t.Fatalf("tracking rule id = %q, want sender-u1", rule.ID)
	}
	rules, err := app.ListDiagnosticsTrackingRules(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticsTrackingRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].ID != "sender-u1" {
		t.Fatalf("tracking rules = %#v, want sender-u1", rules)
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-tracked-u1",
		FromUID: "u1",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})
	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-tracked-u1"})
	if result.Status != diagnostics.StatusOK {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusOK, result)
	}

	if err := app.DeleteDiagnosticsTrackingRule(context.Background(), "sender-u1"); err != nil {
		t.Fatalf("DeleteDiagnosticsTrackingRule() error = %v", err)
	}
	rules, err = app.ListDiagnosticsTrackingRules(context.Background())
	if err != nil {
		t.Fatalf("ListDiagnosticsTrackingRules() after delete error = %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("tracking rules after delete = %#v, want empty", rules)
	}
}

func TestStopRestoresDiagnosticsSendTraceSink(t *testing.T) {
	cfg := Config{
		NodeID: 3,
		Observability: ObservabilityConfig{
			Diagnostics: DiagnosticsConfig{
				SampleRate: 1,
			},
		},
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(false, true, false)
	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.diagnostics == nil {
		t.Fatal("diagnostics store was not wired")
	}

	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-after-internalv2-stop",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-after-internalv2-stop"})
	if result.Status != diagnostics.StatusNotFound {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusNotFound, result)
	}
}

func TestNewRestoresDiagnosticsSinkOnConstructionError(t *testing.T) {
	previous := &recordingInternalV2SendTraceSink{}
	restorePrevious := sendtrace.SetSink(previous)
	t.Cleanup(restorePrevious)

	cfg := Config{
		Gateway: GatewayConfig{
			Listeners: []gateway.ListenerOptions{{
				Network:   "tcp",
				Address:   "127.0.0.1:0",
				Transport: "gnet",
				Protocol:  "wkproto",
			}},
		},
		Observability: ObservabilityConfig{
			Diagnostics: DiagnosticsConfig{
				SampleRate: 1,
			},
		},
	}
	cfg.Observability.SetDiagnosticsExplicitFlags(false, true, false)

	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err == nil {
		t.Fatal("New() error = nil, want invalid gateway listener error")
	}
	if app != nil {
		t.Fatalf("New() app = %#v, want nil on construction error", app)
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-after-new-error",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})
	events := previous.snapshot()
	if len(events) != 1 || events[0].TraceID != "trace-after-new-error" {
		t.Fatalf("previous sink events = %#v, want restored trace-after-new-error", events)
	}
}

func TestDisabledDiagnosticsLeavesStoreUnwired(t *testing.T) {
	cfg := Config{NodeID: 11}
	cfg.Observability.Diagnostics.Enabled = false
	cfg.Observability.SetDiagnosticsExplicitFlags(true, false, false)

	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.diagnostics != nil {
		t.Fatal("diagnostics store was wired when diagnostics were disabled")
	}

	sendtrace.Record(sendtrace.Event{
		TraceID: "trace-disabled-internalv2",
		Stage:   sendtrace.StageMessageSendDurable,
		Result:  sendtrace.ResultOK,
	})

	result := app.QueryDiagnostics(context.Background(), diagnostics.Query{TraceID: "trace-disabled-internalv2"})
	if result.Status != diagnostics.StatusNotFound {
		t.Fatalf("diagnostics status = %s, want %s result=%#v", result.Status, diagnostics.StatusNotFound, result)
	}
	if result.NodeID != 11 {
		t.Fatalf("diagnostics node id = %d, want fallback node id 11", result.NodeID)
	}
}

func TestDiagnosticsConfigDefaultsAndValidation(t *testing.T) {
	cfg := Config{}
	cfg.Observability = defaultObservabilityConfig(cfg.Observability)
	if err := validateObservabilityConfig(cfg.Observability); err != nil {
		t.Fatalf("validateObservabilityConfig(default) error = %v", err)
	}
	if !cfg.Observability.Diagnostics.Enabled {
		t.Fatal("diagnostics enabled default = false, want true")
	}
	if cfg.Observability.Diagnostics.BufferSize != 50000 {
		t.Fatalf("diagnostics buffer size = %d, want 50000", cfg.Observability.Diagnostics.BufferSize)
	}
	if cfg.Observability.Diagnostics.SampleRate != 0.01 {
		t.Fatalf("diagnostics sample rate = %v, want 0.01", cfg.Observability.Diagnostics.SampleRate)
	}
	if cfg.Observability.Diagnostics.SlowThreshold != 500*time.Millisecond {
		t.Fatalf("diagnostics slow threshold = %v, want 500ms", cfg.Observability.Diagnostics.SlowThreshold)
	}
	if cfg.Observability.Diagnostics.ErrorSampleRate != 1 {
		t.Fatalf("diagnostics error sample rate = %v, want 1", cfg.Observability.Diagnostics.ErrorSampleRate)
	}

	explicit := ObservabilityConfig{
		Diagnostics: DiagnosticsConfig{
			Enabled:         false,
			SampleRate:      0,
			ErrorSampleRate: 0,
		},
	}
	explicit.SetDiagnosticsExplicitFlags(true, true, true)
	explicit = defaultObservabilityConfig(explicit)
	if explicit.Diagnostics.Enabled {
		t.Fatal("explicit diagnostics enabled=false was overwritten")
	}
	if explicit.Diagnostics.SampleRate != 0 {
		t.Fatalf("explicit diagnostics sample rate = %v, want 0", explicit.Diagnostics.SampleRate)
	}
	if explicit.Diagnostics.ErrorSampleRate != 0 {
		t.Fatalf("explicit diagnostics error sample rate = %v, want 0", explicit.Diagnostics.ErrorSampleRate)
	}

	tests := []struct {
		name string
		cfg  DiagnosticsConfig
	}{
		{name: "sample rate low", cfg: DiagnosticsConfig{SampleRate: -0.1}},
		{name: "sample rate high", cfg: DiagnosticsConfig{SampleRate: 1.1}},
		{name: "sample rate nan", cfg: DiagnosticsConfig{SampleRate: math.NaN()}},
		{name: "error sample rate low", cfg: DiagnosticsConfig{ErrorSampleRate: -0.1}},
		{name: "error sample rate high", cfg: DiagnosticsConfig{ErrorSampleRate: 1.1}},
		{name: "error sample rate inf", cfg: DiagnosticsConfig{ErrorSampleRate: math.Inf(1)}},
		{name: "debug match sample rate low", cfg: DiagnosticsConfig{DebugMatches: []DiagnosticsDebugMatchConfig{{SampleRate: -0.1}}}},
		{name: "debug match sample rate high", cfg: DiagnosticsConfig{DebugMatches: []DiagnosticsDebugMatchConfig{{SampleRate: 1.1}}}},
		{name: "debug match ttl", cfg: DiagnosticsConfig{DebugMatches: []DiagnosticsDebugMatchConfig{{TTLSeconds: -1}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observability := ObservabilityConfig{Diagnostics: tt.cfg}
			if err := validateObservabilityConfig(observability); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validateObservabilityConfig() error = %v, want %v", err, ErrInvalidConfig)
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

type recordingInternalV2SendTraceSink struct {
	events []sendtrace.Event
}

func (s *recordingInternalV2SendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.events = append(s.events, event)
}

func (s *recordingInternalV2SendTraceSink) snapshot() []sendtrace.Event {
	return append([]sendtrace.Event(nil), s.events...)
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
