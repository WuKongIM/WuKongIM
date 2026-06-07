package api

import (
	"context"
	"errors"
	"net/http"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func mapSendError(err error) (int, string, bool) {
	switch {
	case errors.Is(err, messageusecase.ErrChannelNotFound):
		return http.StatusNotFound, "channel not found", true
	case errors.Is(err, messageusecase.ErrNotLeader), errors.Is(err, messageusecase.ErrStaleRoute), errors.Is(err, messageusecase.ErrRouteNotReady):
		return http.StatusServiceUnavailable, "retry required", true
	case errors.Is(err, runtimechannelid.ErrInvalidPersonChannel), errors.Is(err, runtimechannelid.ErrInvalidAgentChannel):
		return http.StatusBadRequest, "invalid channel id", true
	case errors.Is(err, messageusecase.ErrRequestSubscribersRequireSyncOnce):
		return http.StatusBadRequest, "request subscribers require sync_once", true
	case errors.Is(err, messageusecase.ErrRequestSubscribersConflictChannel):
		return http.StatusBadRequest, "request subscribers cannot include channel_id", true
	case errors.Is(err, messageusecase.ErrRequestSubscribersRequired):
		return http.StatusBadRequest, "request subscribers required", true
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "request canceled", true
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout, "request timeout", true
	default:
		return 0, "", false
	}
}

func mapMessageReason(reason messageusecase.Reason) frame.ReasonCode {
	switch reason {
	case messageusecase.ReasonSuccess:
		return frame.ReasonSuccess
	case messageusecase.ReasonAuthFail:
		return frame.ReasonAuthFail
	case messageusecase.ReasonChannelNotExist:
		return frame.ReasonChannelNotExist
	case messageusecase.ReasonNodeNotMatch:
		return frame.ReasonNodeNotMatch
	case messageusecase.ReasonInvalidRequest, messageusecase.ReasonUnsupported:
		return frame.ReasonPayloadDecodeError
	default:
		return frame.ReasonSystemError
	}
}
