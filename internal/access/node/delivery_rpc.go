package node

import (
	"context"
	"fmt"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// DeliveryPushRPCServiceID is the cluster RPC service for owner-node delivery batches.
const DeliveryPushRPCServiceID uint8 = clusternet.RPCDeliveryPush

// DeliveryFanoutRPCServiceID is the cluster RPC service for authority-node fanout tasks.
const DeliveryFanoutRPCServiceID uint8 = clusternet.RPCDeliveryFanout

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
	result, err := a.delivery.Push(ctx, req.Command)
	if err != nil {
		a.rpcLogger().Warn("delivery push rpc rejected",
			wklog.Event("internal.access.node.delivery_push_rejected"),
			wklog.Uint64("ownerNodeID", req.Command.OwnerNodeID),
			wklog.ChannelID(req.Command.Envelope.ChannelID),
			wklog.ChannelType(int64(req.Command.Envelope.ChannelType)),
			wklog.Uint64("messageID", req.Command.Envelope.MessageID),
			wklog.MessageSeq(req.Command.Envelope.MessageSeq),
			wklog.Int("routes", len(req.Command.Routes)),
			wklog.Error(err),
		)
		return encodeDeliveryPushResponse(deliveryPushResponse{Status: rpcStatusRejected})
	}
	return encodeDeliveryPushResponse(deliveryPushResponse{Status: rpcStatusOK, Result: result})
}

// HandleDeliveryFanoutRPC handles one encoded delivery fanout task RPC payload.
func (a *Adapter) HandleDeliveryFanoutRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeDeliveryFanoutRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("delivery fanout rpc decode failed",
			wklog.Event("internal.access.node.delivery_fanout_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.deliveryFanout == nil {
		return encodeDeliveryFanoutResponse(deliveryFanoutResponse{Status: rpcStatusRejected})
	}
	if err := a.deliveryFanout.RunTask(ctx, req.Task); err != nil {
		a.rpcLogger().Warn("delivery fanout rpc rejected",
			wklog.Event("internal.access.node.delivery_fanout_rejected"),
			wklog.Uint64("targetNodeID", req.Task.Partition.LeaderNodeID),
			wklog.Int("partitionID", int(req.Task.Partition.ID)),
			wklog.ChannelID(req.Task.Envelope.ChannelID),
			wklog.ChannelType(int64(req.Task.Envelope.ChannelType)),
			wklog.Uint64("messageID", req.Task.Envelope.MessageID),
			wklog.MessageSeq(req.Task.Envelope.MessageSeq),
			wklog.Attempt(req.Task.Attempt),
			wklog.Error(err),
		)
		return encodeDeliveryFanoutResponse(deliveryFanoutResponse{Status: rpcStatusRejected})
	}
	return encodeDeliveryFanoutResponse(deliveryFanoutResponse{Status: rpcStatusOK})
}

// PushBatch forwards one owner-node delivery batch to nodeID.
func (c *Client) PushBatch(ctx context.Context, nodeID uint64, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	if c == nil || c.node == nil {
		return runtimedelivery.PushResult{}, fmt.Errorf("internal/access/node: delivery rpc client not configured")
	}
	body, err := encodeDeliveryPushRequest(deliveryPushRequest{Command: cmd})
	if err != nil {
		return runtimedelivery.PushResult{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, DeliveryPushRPCServiceID, body)
	if err != nil {
		return runtimedelivery.PushResult{}, err
	}
	resp, err := decodeDeliveryPushResponse(respBody)
	if err != nil {
		return runtimedelivery.PushResult{}, err
	}
	switch resp.Status {
	case rpcStatusOK:
		return resp.Result, nil
	case rpcStatusRejected:
		return runtimedelivery.PushResult{}, fmt.Errorf("internal/access/node: delivery rpc rejected")
	default:
		return runtimedelivery.PushResult{}, fmt.Errorf("internal/access/node: unknown delivery rpc status %q", resp.Status)
	}
}

// ForwardFanoutTask forwards one partition-scoped fanout task to nodeID.
func (c *Client) ForwardFanoutTask(ctx context.Context, nodeID uint64, task runtimedelivery.FanoutTask) error {
	if c == nil || c.node == nil {
		return fmt.Errorf("internal/access/node: delivery fanout rpc client not configured")
	}
	body, err := encodeDeliveryFanoutRequest(deliveryFanoutRequest{Task: task})
	if err != nil {
		return err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, DeliveryFanoutRPCServiceID, body)
	if err != nil {
		return err
	}
	resp, err := decodeDeliveryFanoutResponse(respBody)
	if err != nil {
		return err
	}
	switch resp.Status {
	case rpcStatusOK:
		return nil
	case rpcStatusRejected:
		return fmt.Errorf("internal/access/node: delivery fanout rpc rejected")
	default:
		return fmt.Errorf("internal/access/node: unknown delivery fanout rpc status %q", resp.Status)
	}
}
