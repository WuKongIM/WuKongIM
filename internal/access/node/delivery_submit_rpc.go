package node

import (
	"context"
	"encoding/json"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type deliverySubmitRequest struct {
	Envelope deliveryruntime.CommittedEnvelope `json:"envelope"`
}

func (a *Adapter) handleDeliverySubmitRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req deliverySubmitRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if a.logger != nil {
		a.logger.Info("delivery submit rpc received",
			wklog.Event("delivery.diag.submit_rpc"),
			wklog.String("channelID", req.Envelope.ChannelID),
			wklog.Int("channelType", int(req.Envelope.ChannelType)),
			wklog.Uint64("messageID", req.Envelope.MessageID),
			wklog.Uint64("messageSeq", req.Envelope.MessageSeq),
		)
	}
	if a.deliverySubmit != nil {
		if err := a.deliverySubmit.SubmitCommitted(ctx, req.Envelope); err != nil {
			return nil, err
		}
	}
	return encodeDeliveryResponse(deliveryResponse{Status: rpcStatusOK})
}
