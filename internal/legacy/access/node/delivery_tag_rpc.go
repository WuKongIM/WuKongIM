package node

import (
	"context"
	"fmt"

	deliverytagruntime "github.com/WuKongIM/WuKongIM/internal/legacy/runtime/deliverytag"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	deliveryTagOpGet    = "get"
	deliveryTagOpUpdate = "update"

	rpcStatusRetryable          = "retryable"
	rpcStatusTagNotCurrent      = "tag_not_current"
	rpcStatusTagVersionMismatch = "tag_version_mismatch"
)

func (a *Adapter) handleDeliveryTagRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeDeliveryTagRequest(body)
	if err != nil {
		return nil, err
	}
	if a.channelLog == nil {
		return nil, fmt.Errorf("access/node: delivery tag channel log required")
	}

	status, err := a.channelLog.Status(channel.ChannelID{
		ID:   req.ChannelID,
		Type: req.ChannelType,
	})
	if err != nil {
		return nil, err
	}
	if leader := uint64(status.Leader); leader == 0 {
		return encodeDeliveryTagResponse(DeliveryTagResponse{
			Status: rpcStatusRetryable,
			Result: rpcStatusTagNotCurrent,
		})
	} else if leader != a.localNodeID {
		return encodeDeliveryTagResponse(DeliveryTagResponse{
			Status:   rpcStatusNotLeader,
			LeaderID: leader,
		})
	}

	if a.deliveryTag == nil {
		return nil, fmt.Errorf("access/node: delivery tag authority required")
	}

	switch req.Op {
	case deliveryTagOpGet, "":
		resp, err := a.deliveryTag.GetDeliveryTag(ctx, req)
		if err != nil {
			return nil, err
		}
		return encodeDeliveryTagResponse(normalizeDeliveryTagResponse(req, filterDeliveryTagResponse(resp, req.TargetNodeID), uint64(status.Leader)))
	case deliveryTagOpUpdate:
		resp, err := a.deliveryTag.UpdateDeliveryTag(ctx, req)
		if err != nil {
			return nil, err
		}
		return encodeDeliveryTagResponse(normalizeDeliveryTagResponse(req, filterDeliveryTagResponse(resp, req.TargetNodeID), uint64(status.Leader)))
	default:
		return nil, fmt.Errorf("access/node: unknown delivery tag op %q", req.Op)
	}
}

func filterDeliveryTagResponse(resp DeliveryTagResponse, targetNodeID uint64) DeliveryTagResponse {
	if targetNodeID == 0 || len(resp.Tag.Partitions) == 0 {
		return resp
	}
	filtered := resp
	filtered.Tag.Partitions = nil
	for _, partition := range resp.Tag.Partitions {
		if partition.NodeID == targetNodeID {
			filtered.Tag.Partitions = []deliverytagruntime.NodePartition{partition}
			return filtered
		}
	}
	filtered.Tag.Partitions = []deliverytagruntime.NodePartition{{NodeID: targetNodeID}}
	return filtered
}

func normalizeDeliveryTagResponse(req DeliveryTagRequest, resp DeliveryTagResponse, leaderID uint64) DeliveryTagResponse {
	if resp.Status == "" {
		resp.Status = rpcStatusOK
	}
	if leaderID != 0 && resp.LeaderID == 0 {
		resp.LeaderID = leaderID
	}
	if resp.Status == rpcStatusOK {
		resp = normalizeDeliveryTagFence(req, resp)
	}
	if resp.Status == rpcStatusTagNotCurrent || resp.Status == rpcStatusTagVersionMismatch {
		if resp.Result == "" {
			resp.Result = resp.Status
		}
		resp.Status = rpcStatusRetryable
	}
	return resp
}

func normalizeDeliveryTagFence(req DeliveryTagRequest, resp DeliveryTagResponse) DeliveryTagResponse {
	if req.TagKey != "" && resp.Tag.Key != "" && resp.Tag.Key != req.TagKey {
		resp.Status = rpcStatusRetryable
		resp.Result = rpcStatusTagNotCurrent
		return resp
	}
	if req.TagVersion != 0 && resp.Tag.TagVersion != 0 && resp.Tag.TagVersion != req.TagVersion {
		resp.Status = rpcStatusRetryable
		resp.Result = rpcStatusTagVersionMismatch
	}
	return resp
}

func encodeDeliveryTagResponse(resp DeliveryTagResponse) ([]byte, error) {
	return encodeDeliveryTagResponseBinary(resp)
}

func decodeDeliveryTagResponse(body []byte) (DeliveryTagResponse, error) {
	return decodeDeliveryTagResponseBinary(body)
}

func (c *Client) GetDeliveryTag(ctx context.Context, nodeID uint64, req DeliveryTagRequest) (DeliveryTagResponse, error) {
	req.Op = deliveryTagOpGet
	return c.callDeliveryTagRPC(ctx, nodeID, req)
}

func (c *Client) UpdateDeliveryTag(ctx context.Context, nodeID uint64, req DeliveryTagRequest) (DeliveryTagResponse, error) {
	req.Op = deliveryTagOpUpdate
	return c.callDeliveryTagRPC(ctx, nodeID, req)
}

func (c *Client) callDeliveryTagRPC(ctx context.Context, nodeID uint64, req DeliveryTagRequest) (DeliveryTagResponse, error) {
	if c == nil || c.cluster == nil {
		return DeliveryTagResponse{}, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeDeliveryTagRequestBinary(req)
	if err != nil {
		return DeliveryTagResponse{}, err
	}
	resp, err := c.callDeliveryTagDirect(ctx, nodeID, body)
	if err != nil {
		return DeliveryTagResponse{}, err
	}
	if resp.Status == rpcStatusNotLeader && resp.LeaderID != 0 && resp.LeaderID != nodeID {
		return c.callDeliveryTagDirect(ctx, resp.LeaderID, body)
	}
	return resp, nil
}

func (c *Client) callDeliveryTagDirect(ctx context.Context, nodeID uint64, body []byte) (DeliveryTagResponse, error) {
	if c.cluster == nil {
		return DeliveryTagResponse{}, fmt.Errorf("access/node: cluster not configured")
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, deliveryTagRPCServiceID, body)
	if err != nil {
		return DeliveryTagResponse{}, err
	}
	return decodeDeliveryTagResponse(respBody)
}

func deliveryTagRPCStatus(resp DeliveryTagResponse) string {
	if resp.Status != "" {
		return resp.Status
	}
	return rpcStatusOK
}

func deliveryTagRPCResult(resp DeliveryTagResponse) string {
	if resp.Result != "" {
		return resp.Result
	}
	return deliveryTagRPCStatus(resp)
}

func logDeliveryTagRPC(logger wklog.Logger, msg string, fields ...wklog.Field) {
	if logger == nil {
		return
	}
	logger.Info(msg, fields...)
}
