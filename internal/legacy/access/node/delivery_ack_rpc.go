package node

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/deliveryevents"
)

type deliveryAckRequest struct {
	Command  deliveryevents.RouteAck   `json:"command"`
	Commands []deliveryevents.RouteAck `json:"commands,omitempty"`
}

func (a *Adapter) handleDeliveryAckRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeDeliveryAckRequest(body)
	if err != nil {
		return nil, err
	}
	if a.deliveryAck != nil {
		for _, cmd := range req.ackCommands() {
			if err := a.deliveryAck.AckRoute(ctx, cmd); err != nil {
				return nil, err
			}
		}
	}
	return encodeDeliveryResponse(deliveryResponse{Status: rpcStatusOK})
}

func (r deliveryAckRequest) ackCommands() []deliveryevents.RouteAck {
	if len(r.Commands) > 0 {
		return r.Commands
	}
	return []deliveryevents.RouteAck{r.Command}
}
