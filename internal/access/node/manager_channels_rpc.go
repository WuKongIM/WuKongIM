package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerChannelRPCServiceID is the cluster RPC service for node-local manager channel lists.
const ManagerChannelRPCServiceID uint8 = clusternet.RPCManagerChannels

// HandleManagerChannelRPC handles one encoded manager channel list RPC payload.
func (a *Adapter) HandleManagerChannelRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerChannelRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager channel rpc decode failed",
			wklog.Event("internal.access.node.manager_channel_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerChannels == nil {
		return encodeManagerChannelResponse(managerChannelRPCResponse{Status: rpcStatusRejected})
	}
	page, err := a.managerChannels.ListBusinessChannels(ctx, managementusecase.ListBusinessChannelsRequest{
		NodeID:     req.NodeID,
		Limit:      req.Limit,
		Cursor:     req.Cursor,
		TypeFilter: req.TypeFilter,
		Keyword:    req.Keyword,
	})
	status := managerChannelRPCStatusForError(err)
	a.logManagerChannelError(req, status, err)
	return encodeManagerChannelResponse(managerChannelRPCResponse{Status: status, Page: page})
}

// ListManagerBusinessChannels reads one node's manager channel page.
func (c *Client) ListManagerBusinessChannels(ctx context.Context, req managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error) {
	resp, err := c.callManagerChannel(ctx, req.NodeID, managerChannelRPCRequest{
		NodeID:     req.NodeID,
		Limit:      req.Limit,
		TypeFilter: req.TypeFilter,
		Keyword:    req.Keyword,
		Cursor:     req.Cursor,
	})
	if err != nil {
		return managementusecase.ListBusinessChannelsResponse{}, err
	}
	if err := managerChannelRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.ListBusinessChannelsResponse{}, err
	}
	return resp.Page, nil
}

func (c *Client) callManagerChannel(ctx context.Context, nodeID uint64, req managerChannelRPCRequest) (managerChannelRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerChannelRPCResponse{}, fmt.Errorf("internal/access/node: manager channel rpc client not configured")
	}
	body, err := encodeManagerChannelRequest(req)
	if err != nil {
		return managerChannelRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerChannelRPCServiceID, body)
	if err != nil {
		return managerChannelRPCResponse{}, err
	}
	return decodeManagerChannelResponse(respBody)
}

func managerChannelRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusRejected
	case errors.Is(err, managementusecase.ErrBusinessChannelReaderUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func managerChannelRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusRejected:
		return managementusecase.ErrBusinessChannelReaderUnavailable
	default:
		return fmt.Errorf("internal/access/node: unknown manager channel rpc status %q", status)
	}
}

func (a *Adapter) logManagerChannelError(req managerChannelRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager channel rpc rejected",
		wklog.Event("internal.access.node.manager_channel_rejected"),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.Int("limit", req.Limit),
		wklog.Int64("typeFilter", req.TypeFilter),
		wklog.String("keyword", req.Keyword),
		wklog.Error(err),
	)
}
