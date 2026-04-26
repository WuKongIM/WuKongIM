package node

import (
	"context"
	"encoding/json"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
)

type deliveryAckRequest struct {
	Command deliveryevents.RouteAck `json:"command"`
}

func (a *Adapter) handleDeliveryAckRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req deliveryAckRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if a.deliveryAck != nil {
		if err := a.deliveryAck.AckRoute(ctx, req.Command); err != nil {
			return nil, err
		}
	}
	return encodeDeliveryResponse(deliveryResponse{Status: rpcStatusOK})
}
