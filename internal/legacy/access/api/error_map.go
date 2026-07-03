package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func mapSendError(err error) (int, string, bool) {
	switch {
	case errors.Is(err, channel.ErrChannelNotFound):
		return http.StatusNotFound, "channel not found", true
	case errors.Is(err, channel.ErrChannelDeleting):
		return http.StatusConflict, "channel deleting", true
	case errors.Is(err, channel.ErrProtocolUpgradeRequired):
		return http.StatusUpgradeRequired, "protocol upgrade required", true
	case errors.Is(err, channel.ErrIdempotencyConflict):
		return http.StatusConflict, "idempotency conflict", true
	case errors.Is(err, channel.ErrMessageSeqExhausted):
		return http.StatusConflict, "message seq exhausted", true
	case errors.Is(err, channel.ErrStaleMeta), errors.Is(err, channel.ErrNotLeader):
		return http.StatusServiceUnavailable, "retry required", true
	case errors.Is(err, runtimechannelid.ErrInvalidPersonChannel), errors.Is(err, runtimechannelid.ErrInvalidAgentChannel):
		return http.StatusBadRequest, "invalid channel id", true
	case errors.Is(err, message.ErrRequestSubscribersRequireSyncOnce):
		return http.StatusBadRequest, "request subscribers require sync_once", true
	case errors.Is(err, message.ErrRequestSubscribersConflictChannel):
		return http.StatusBadRequest, "request subscribers cannot include channel_id", true
	case errors.Is(err, message.ErrRequestSubscribersRequired):
		return http.StatusBadRequest, "request subscribers required", true
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "request canceled", true
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout, "request timeout", true
	default:
		return 0, "", false
	}
}
