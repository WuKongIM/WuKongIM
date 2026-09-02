package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSendErrorMappingPreservesStableHTTPContract(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
		wantMapped  bool
	}{
		{name: "channel missing", err: messageusecase.ErrChannelNotFound, wantStatus: http.StatusNotFound, wantMessage: "channel not found", wantMapped: true},
		{name: "not leader", err: messageusecase.ErrNotLeader, wantStatus: http.StatusServiceUnavailable, wantMessage: "retry required", wantMapped: true},
		{name: "stale route", err: messageusecase.ErrStaleRoute, wantStatus: http.StatusServiceUnavailable, wantMessage: "retry required", wantMapped: true},
		{name: "route not ready", err: messageusecase.ErrRouteNotReady, wantStatus: http.StatusServiceUnavailable, wantMessage: "retry required", wantMapped: true},
		{name: "invalid person channel", err: runtimechannelid.ErrInvalidPersonChannel, wantStatus: http.StatusBadRequest, wantMessage: "invalid channel id", wantMapped: true},
		{name: "invalid agent channel", err: runtimechannelid.ErrInvalidAgentChannel, wantStatus: http.StatusBadRequest, wantMessage: "invalid channel id", wantMapped: true},
		{name: "request subscribers need sync once", err: messageusecase.ErrRequestSubscribersRequireSyncOnce, wantStatus: http.StatusBadRequest, wantMessage: "request subscribers require sync_once", wantMapped: true},
		{name: "request subscribers conflict", err: messageusecase.ErrRequestSubscribersConflictChannel, wantStatus: http.StatusBadRequest, wantMessage: "request subscribers cannot include channel_id", wantMapped: true},
		{name: "request subscribers missing", err: messageusecase.ErrRequestSubscribersRequired, wantStatus: http.StatusBadRequest, wantMessage: "request subscribers required", wantMapped: true},
		{name: "canceled", err: context.Canceled, wantStatus: http.StatusRequestTimeout, wantMessage: "request canceled", wantMapped: true},
		{name: "deadline", err: context.DeadlineExceeded, wantStatus: http.StatusRequestTimeout, wantMessage: "request timeout", wantMapped: true},
		{name: "wrapped known error", err: fmt.Errorf("append: %w", messageusecase.ErrChannelNotFound), wantStatus: http.StatusNotFound, wantMessage: "channel not found", wantMapped: true},
		{name: "unknown", err: errors.New("storage failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, message, mapped := mapSendError(tt.err)
			if status != tt.wantStatus || message != tt.wantMessage || mapped != tt.wantMapped {
				t.Fatalf("mapSendError(%v) = (%d, %q, %t), want (%d, %q, %t)", tt.err, status, message, mapped, tt.wantStatus, tt.wantMessage, tt.wantMapped)
			}
		})
	}
}

func TestMessageReasonMappingPreservesWireReasonCodes(t *testing.T) {
	tests := []struct {
		name   string
		reason messageusecase.Reason
		want   frame.ReasonCode
	}{
		{name: "success", reason: messageusecase.ReasonSuccess, want: frame.ReasonSuccess},
		{name: "auth", reason: messageusecase.ReasonAuthFail, want: frame.ReasonAuthFail},
		{name: "channel missing", reason: messageusecase.ReasonChannelNotExist, want: frame.ReasonChannelNotExist},
		{name: "node mismatch", reason: messageusecase.ReasonNodeNotMatch, want: frame.ReasonNodeNotMatch},
		{name: "subscriber missing", reason: messageusecase.ReasonSubscriberNotExist, want: frame.ReasonSubscriberNotExist},
		{name: "denylisted", reason: messageusecase.ReasonInBlacklist, want: frame.ReasonInBlacklist},
		{name: "not allowed", reason: messageusecase.ReasonNotAllowSend, want: frame.ReasonNotAllowSend},
		{name: "not allowlisted", reason: messageusecase.ReasonNotInWhitelist, want: frame.ReasonNotInWhitelist},
		{name: "channel banned", reason: messageusecase.ReasonBan, want: frame.ReasonBan},
		{name: "channel disbanded", reason: messageusecase.ReasonDisband, want: frame.ReasonDisband},
		{name: "sender banned", reason: messageusecase.ReasonSendBan, want: frame.ReasonSendBan},
		{name: "invalid request", reason: messageusecase.ReasonInvalidRequest, want: frame.ReasonPayloadDecodeError},
		{name: "unsupported", reason: messageusecase.ReasonUnsupported, want: frame.ReasonPayloadDecodeError},
		{name: "system error", reason: messageusecase.ReasonSystemError, want: frame.ReasonSystemError},
		{name: "future unknown", reason: messageusecase.Reason(255), want: frame.ReasonSystemError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapMessageReason(tt.reason); got != tt.want {
				t.Fatalf("mapMessageReason(%d) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}
