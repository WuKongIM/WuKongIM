package node

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/deliveryevents"
)

type deliveryOfflineRequest struct {
	Command deliveryevents.SessionClosed `json:"command"`
}

func (a *Adapter) handleDeliveryOfflineRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeDeliveryOfflineRequest(body)
	if err != nil {
		return nil, err
	}
	if a.deliveryOffline != nil {
		if err := a.deliveryOffline.SessionClosed(ctx, req.Command); err != nil {
			return nil, err
		}
	}
	return encodeDeliveryResponse(deliveryResponse{Status: rpcStatusOK})
}
