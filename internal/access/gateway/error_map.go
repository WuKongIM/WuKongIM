package gateway

import (
	"context"
	"errors"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func mapSendErrorReason(err error) (frame.ReasonCode, bool) {
	switch {
	case errors.Is(err, channel.ErrChannelNotFound):
		return frame.ReasonChannelNotExist, true
	case errors.Is(err, channel.ErrChannelDeleting):
		return frame.ReasonChannelDeleting, true
	case errors.Is(err, channel.ErrProtocolUpgradeRequired):
		return frame.ReasonProtocolUpgradeRequired, true
	case errors.Is(err, channel.ErrIdempotencyConflict):
		return frame.ReasonIdempotencyConflict, true
	case errors.Is(err, channel.ErrMessageSeqExhausted):
		return frame.ReasonMessageSeqExhausted, true
	case errors.Is(err, channel.ErrStaleMeta), errors.Is(err, channel.ErrNotLeader):
		return frame.ReasonNodeNotMatch, true
	case errors.Is(err, runtimechannelid.ErrInvalidPersonChannel):
		return frame.ReasonChannelIDError, true
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return frame.ReasonSystemError, true
	default:
		return 0, false
	}
}
