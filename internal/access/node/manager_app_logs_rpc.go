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

// ManagerAppLogRPCServiceID is the cluster RPC service for selected-node ordinary application log reads.
const ManagerAppLogRPCServiceID uint8 = clusternet.RPCManagerAppLogs

// HandleManagerAppLogRPC handles one encoded manager ordinary application log RPC payload.
func (a *Adapter) HandleManagerAppLogRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerAppLogRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager app log rpc decode failed",
			wklog.Event("internalv2.access.node.manager_app_log_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerAppLogs == nil {
		return encodeManagerAppLogResponse(managerAppLogRPCResponse{Status: rpcStatusRejected})
	}
	switch req.Op {
	case managerAppLogOpSources:
		page, err := a.managerAppLogs.ApplicationLogSources(ctx, managementusecase.ApplicationLogSourcesRequest{
			NodeID: req.NodeID,
		})
		status := managerAppLogRPCStatusForError(err)
		a.logManagerAppLogError(req, status, err)
		return encodeManagerAppLogResponse(managerAppLogRPCResponse{Status: status, Sources: page})
	case managerAppLogOpEntries:
		page, err := a.managerAppLogs.ApplicationLogEntries(ctx, managementusecase.ApplicationLogEntriesRequest{
			NodeID:  req.NodeID,
			Source:  req.Source,
			Limit:   req.Limit,
			Cursor:  req.Cursor,
			Keyword: req.Keyword,
			Levels:  req.Levels,
		})
		status := managerAppLogRPCStatusForError(err)
		a.logManagerAppLogError(req, status, err)
		return encodeManagerAppLogResponse(managerAppLogRPCResponse{Status: status, Entries: page})
	default:
		err := fmt.Errorf("internalv2/access/node: unknown manager app log op %q", req.Op)
		a.rpcLogger().Warn("manager app log rpc unknown operation",
			wklog.Event("internalv2.access.node.manager_app_log_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

// GetManagerApplicationLogSources reads one node's ordinary application log sources.
func (c *Client) GetManagerApplicationLogSources(ctx context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	resp, err := c.callManagerAppLog(ctx, req.NodeID, managerAppLogRPCRequest{
		Op:     managerAppLogOpSources,
		NodeID: req.NodeID,
	})
	if err != nil {
		return managementusecase.ApplicationLogSourcesResponse{}, err
	}
	if err := managerAppLogRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.ApplicationLogSourcesResponse{}, err
	}
	return resp.Sources, nil
}

// GetManagerApplicationLogEntries reads one page from a selected node's ordinary application log source.
func (c *Client) GetManagerApplicationLogEntries(ctx context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	resp, err := c.callManagerAppLog(ctx, req.NodeID, managerAppLogRPCRequest{
		Op:      managerAppLogOpEntries,
		NodeID:  req.NodeID,
		Source:  req.Source,
		Limit:   req.Limit,
		Cursor:  req.Cursor,
		Keyword: req.Keyword,
		Levels:  req.Levels,
	})
	if err != nil {
		return managementusecase.ApplicationLogEntriesResponse{}, err
	}
	if err := managerAppLogRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.ApplicationLogEntriesResponse{}, err
	}
	return resp.Entries, nil
}

func (c *Client) callManagerAppLog(ctx context.Context, nodeID uint64, req managerAppLogRPCRequest) (managerAppLogRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerAppLogRPCResponse{}, fmt.Errorf("internalv2/access/node: manager app log rpc client not configured")
	}
	body, err := encodeManagerAppLogRequest(req)
	if err != nil {
		return managerAppLogRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerAppLogRPCServiceID, body)
	if err != nil {
		return managerAppLogRPCResponse{}, err
	}
	return decodeManagerAppLogResponse(respBody)
}

func managerAppLogRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrNotFound):
		return rpcStatusNotFound
	case errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusRejected
	case errors.Is(err, managementusecase.ErrApplicationLogReaderUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func managerAppLogRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusNotFound:
		return metadb.ErrNotFound
	case rpcStatusRejected:
		return managementusecase.ErrApplicationLogReaderUnavailable
	default:
		return fmt.Errorf("internalv2/access/node: unknown manager app log rpc status %q", status)
	}
}

func (a *Adapter) logManagerAppLogError(req managerAppLogRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager app log rpc rejected",
		wklog.Event("internalv2.access.node.manager_app_log_rejected"),
		wklog.String("op", req.Op),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.String("source", req.Source),
		wklog.Int("limit", req.Limit),
		wklog.String("cursor", req.Cursor),
		wklog.String("keyword", req.Keyword),
		wklog.Int("levels", len(req.Levels)),
		wklog.Error(err),
	)
}
