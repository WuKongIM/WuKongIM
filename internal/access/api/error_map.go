package api

import (
	"context"
	"errors"
	"net/http"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
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
	case errors.Is(err, runtimechannelid.ErrInvalidPersonChannel):
		return http.StatusBadRequest, "invalid channel id", true
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "request canceled", true
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout, "request timeout", true
	default:
		return 0, "", false
	}
}
