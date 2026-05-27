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
