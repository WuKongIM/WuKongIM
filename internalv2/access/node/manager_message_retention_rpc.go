package node

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerMessageRetentionRPCServiceID is the clusterv2 RPC service for channel-leader manager retention requests.
const ManagerMessageRetentionRPCServiceID uint8 = clusternet.RPCManagerMessageRetention

// HandleManagerMessageRetentionRPC handles one encoded manager message retention RPC payload.
func (a *Adapter) HandleManagerMessageRetentionRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerMessageRetentionRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager message retention rpc decode failed",
			wklog.Event("internalv2.access.node.manager_message_retention_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerMessageRetention == nil {
		return encodeManagerMessageRetentionResponse(managerMessageRetentionRPCResponse{Status: rpcStatusRejected})
	}
	result, err := a.managerMessageRetention.AdvanceMessageRetention(ctx, req.Request)
	status := managerMessageRetentionRPCStatusForError(err)
	a.logManagerMessageRetentionError(req, status, err)
	return encodeManagerMessageRetentionResponse(managerMessageRetentionRPCResponse{Status: status, Result: result})
}

// AdvanceManagerMessageRetention forwards one message retention request to the channel leader node.
func (c *Client) AdvanceManagerMessageRetention(ctx context.Context, nodeID uint64, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
	resp, err := c.callManagerMessageRetention(ctx, nodeID, managerMessageRetentionRPCRequest{Request: req})
	if err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	if err := managerMessageRetentionRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.AdvanceMessageRetentionResponse{}, err
	}
	return resp.Result, nil
}

func (c *Client) callManagerMessageRetention(ctx context.Context, nodeID uint64, req managerMessageRetentionRPCRequest) (managerMessageRetentionRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerMessageRetentionRPCResponse{}, fmt.Errorf("internalv2/access/node: manager message retention rpc client not configured")
	}
	body, err := encodeManagerMessageRetentionRequest(req)
	if err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerMessageRetentionRPCServiceID, body)
	if err != nil {
		return managerMessageRetentionRPCResponse{}, err
	}
	return decodeManagerMessageRetentionResponse(respBody)
}

func managerMessageRetentionRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, channelappend.ErrNotLeader):
		return rpcStatusNotLeader
	case errors.Is(err, channelappend.ErrStaleRoute):
		return rpcStatusStaleRoute
	case errors.Is(err, channelappend.ErrRouteNotReady):
		return rpcStatusRouteNotReady
	case errors.Is(err, metadb.ErrNotFound), errors.Is(err, channelappend.ErrChannelNotFound):
		return rpcStatusNotFound
	case errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusRejected
	case errors.Is(err, managementusecase.ErrMessageRetentionUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func managerMessageRetentionRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusNotLeader:
		return channelappend.ErrNotLeader
	case rpcStatusStaleRoute:
		return channelappend.ErrStaleRoute
	case rpcStatusRouteNotReady:
		return channelappend.ErrRouteNotReady
	case rpcStatusNotFound:
		return metadb.ErrNotFound
	case rpcStatusRejected:
		return managementusecase.ErrMessageRetentionUnavailable
	default:
		return fmt.Errorf("internalv2/access/node: unknown manager message retention rpc status %q", status)
	}
}

func (a *Adapter) logManagerMessageRetentionError(req managerMessageRetentionRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager message retention rpc rejected",
		wklog.Event("internalv2.access.node.manager_message_retention_rejected"),
		wklog.String("status", status),
		wklog.String("channelID", req.Request.ChannelID),
		wklog.Int64("channelType", req.Request.ChannelType),
		wklog.Uint64("throughSeq", req.Request.ThroughSeq),
		wklog.Error(err),
	)
}
