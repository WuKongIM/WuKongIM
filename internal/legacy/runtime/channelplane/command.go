package channelplane

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

// AppendBatchRequest is the neutral channel-plane append request DTO.
type AppendBatchRequest = channel.AppendBatchRequest

// AppendBatchResult is the neutral channel-plane append result DTO.
type AppendBatchResult = channel.AppendBatchResult

type appendCommand struct {
	ctx                      context.Context
	req                      channel.AppendBatchRequest
	future                   *appendFuture
	routeInvalidationRetries int
}

func validateAppendBatchRequest(req channel.AppendBatchRequest) error {
	if req.ChannelID.ID == "" || req.ChannelID.Type == 0 || len(req.Messages) == 0 {
		return ErrInvalidRequest
	}
	return nil
}
