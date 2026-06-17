package gateway

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func mapReason(reason message.Reason) frame.ReasonCode {
	switch reason {
	case message.ReasonSuccess:
		return frame.ReasonSuccess
	case message.ReasonAuthFail:
		return frame.ReasonAuthFail
	case message.ReasonChannelNotExist:
		return frame.ReasonChannelNotExist
	case message.ReasonNodeNotMatch:
		return frame.ReasonNodeNotMatch
	case message.ReasonSubscriberNotExist:
		return frame.ReasonSubscriberNotExist
	case message.ReasonInBlacklist:
		return frame.ReasonInBlacklist
	case message.ReasonNotAllowSend:
		return frame.ReasonNotAllowSend
	case message.ReasonNotInWhitelist:
		return frame.ReasonNotInWhitelist
	case message.ReasonBan:
		return frame.ReasonBan
	case message.ReasonDisband:
		return frame.ReasonDisband
	case message.ReasonSendBan:
		return frame.ReasonSendBan
	case message.ReasonInvalidRequest, message.ReasonUnsupported:
		return frame.ReasonPayloadDecodeError
	default:
		return frame.ReasonSystemError
	}
}

func reasonForError(err error) message.Reason {
	switch {
	case err == nil:
		return message.ReasonSuccess
	case errors.Is(err, message.ErrChannelNotFound):
		return message.ReasonChannelNotExist
	case errors.Is(err, message.ErrNotLeader), errors.Is(err, message.ErrStaleRoute), errors.Is(err, message.ErrRouteNotReady):
		return message.ReasonNodeNotMatch
	case errors.Is(err, message.ErrInvalidCommand):
		return message.ReasonInvalidRequest
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return message.ReasonSystemError
	default:
		return message.ReasonSystemError
	}
}

func sendackErrorClassForError(err error) string {
	switch {
	case err == nil:
		return sendackErrorClassNone
	case errors.Is(err, message.ErrChannelNotFound):
		return sendackErrorClassChannelNotFound
	case errors.Is(err, message.ErrNotLeader):
		return sendackErrorClassNotLeader
	case errors.Is(err, message.ErrStaleRoute):
		return sendackErrorClassStaleRoute
	case errors.Is(err, message.ErrRouteNotReady):
		return sendackErrorClassRouteNotReady
	case errors.Is(err, message.ErrInvalidCommand):
		return sendackErrorClassInvalidRequest
	case errors.Is(err, context.Canceled):
		return sendackErrorClassCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return sendackErrorClassTimeout
	default:
		return sendackErrorClassOther
	}
}
