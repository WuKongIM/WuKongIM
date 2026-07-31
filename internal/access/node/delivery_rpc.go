package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// DeliveryPushRPCServiceID is the cluster RPC service for owner-node delivery batches.
const DeliveryPushRPCServiceID uint8 = clusternet.RPCDeliveryPush

// HandleDeliveryPushRPC handles one encoded delivery push RPC payload.
func (a *Adapter) HandleDeliveryPushRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeDeliveryPushRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("delivery push rpc decode failed",
			wklog.Event("internal.access.node.delivery_push_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.delivery == nil {
		return encodeDeliveryPushResponse(deliveryPushResponse{Status: rpcStatusRejected})
	}
	result, err := a.delivery.PushOwner(ctx, req.Command)
	if err != nil {
		a.rpcLogger().Warn("delivery push rpc rejected",
			wklog.Event("internal.access.node.delivery_push_rejected"),
			wklog.Uint64("ownerNodeID", req.Command.OwnerNodeID),
			wklog.ChannelID(req.Command.Event.ChannelID),
			wklog.ChannelType(int64(req.Command.Event.ChannelType)),
			wklog.Uint64("messageID", req.Command.Event.MessageID),
			wklog.MessageSeq(req.Command.Event.MessageSeq),
			wklog.Int("routes", len(req.Command.Routes)),
			wklog.Error(err),
		)
		return encodeDeliveryPushResponse(deliveryPushResponse{Status: rpcStatusRejected})
	}
	return encodeDeliveryPushResponse(deliveryPushResponse{Status: rpcStatusOK, Result: result})
}

// PushOwner forwards one owner-node delivery batch to its owner node.
func (c *Client) PushOwner(ctx context.Context, cmd onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
	if c == nil || c.node == nil {
		return onlinedelivery.OwnerPushResult{}, fmt.Errorf("internal/access/node: delivery rpc client not configured")
	}
	body, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: cmd})
	if err != nil {
		return onlinedelivery.OwnerPushResult{}, err
	}
	respBody, err := c.node.CallRPC(ctx, cmd.OwnerNodeID, DeliveryPushRPCServiceID, body)
	if err != nil {
		return onlinedelivery.OwnerPushResult{}, err
	}
	resp, err := decodeDeliveryPushResponse(respBody)
	if err != nil {
		return onlinedelivery.OwnerPushResult{}, err
	}
	switch resp.Status {
	case rpcStatusOK:
		return resp.Result, nil
	case rpcStatusRejected:
		return onlinedelivery.OwnerPushResult{}, fmt.Errorf("internal/access/node: delivery rpc rejected")
	default:
		return onlinedelivery.OwnerPushResult{}, fmt.Errorf("internal/access/node: unknown delivery rpc status %q", resp.Status)
	}
}
