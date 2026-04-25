package node

import (
	"context"
	"encoding/json"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

type deliveryOfflineRequest struct {
	Command message.SessionClosedCommand `json:"command"`
}

func (a *Adapter) handleDeliveryOfflineRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req deliveryOfflineRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if a.deliveryOffline != nil {
		if err := a.deliveryOffline.SessionClosed(ctx, req.Command); err != nil {
			return nil, err
		}
	}
	return encodeDeliveryResponse(deliveryResponse{Status: rpcStatusOK})
}
