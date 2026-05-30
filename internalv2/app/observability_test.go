package app

import (
	"context"
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
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
		{name: "stale_meta", err: fmt.Errorf("wrapped: %w", ch.ErrStaleMeta), want: "stale_meta"},
		{name: "channel_not_found", err: ch.ErrChannelNotFound, want: "channel_not_found"},
		{name: "not_leader", err: ch.ErrNotLeader, want: "not_leader"},
		{name: "invalid_config", err: ch.ErrInvalidConfig, want: "invalid_config"},
		{name: "closed", err: ch.ErrClosed, want: "closed"},
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "remote_canceled", err: fmt.Errorf("nodetransport: remote error: %s", context.Canceled), want: "canceled"},
		{name: "timeout", err: context.DeadlineExceeded, want: "timeout"},
		{name: "remote_timeout", err: fmt.Errorf("nodetransport: remote error: %s", context.DeadlineExceeded), want: "timeout"},
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
