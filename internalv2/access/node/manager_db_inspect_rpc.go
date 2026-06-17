package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	dbinspect "github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerDBInspectRPCServiceID is the clusterv2 RPC service for node-local manager DB inspect reads.
const ManagerDBInspectRPCServiceID uint8 = clusternet.RPCManagerDBInspect

const (
	rpcStatusInvalidRequest = "invalid_request"
	rpcStatusInvalidCursor  = "invalid_cursor"
	rpcStatusUnavailable    = "unavailable"
)

// HandleManagerDBInspectRPC handles one encoded manager DB inspect RPC payload.
func (a *Adapter) HandleManagerDBInspectRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerDBInspectRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager db inspect rpc decode failed",
			wklog.Event("internalv2.access.node.manager_db_inspect_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerDBInspect == nil {
		return encodeManagerDBInspectResponse(managerDBInspectRPCResponse{Status: rpcStatusUnavailable})
	}
	page, err := a.managerDBInspect.QueryDBInspect(ctx, managementusecase.DBInspectQueryRequest{
		NodeID: req.NodeID,
		Query:  req.Query,
	})
	status := managerDBInspectRPCStatusForError(err)
	a.logManagerDBInspectError(req, status, err)
	return encodeManagerDBInspectResponse(managerDBInspectRPCResponse{Status: status, Page: page})
}

// NodeDBInspectQuery reads one node's DB inspect query result.
func (c *Client) NodeDBInspectQuery(ctx context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	resp, err := c.callManagerDBInspect(ctx, req.NodeID, managerDBInspectRPCRequest{
		NodeID: req.NodeID,
		Query:  req.Query,
	})
	if err != nil {
		return managementusecase.DBInspectQueryResponse{}, err
	}
	if err := managerDBInspectRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.DBInspectQueryResponse{}, err
	}
	return resp.Page, nil
}

func (c *Client) callManagerDBInspect(ctx context.Context, nodeID uint64, req managerDBInspectRPCRequest) (managerDBInspectRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerDBInspectRPCResponse{}, fmt.Errorf("internalv2/access/node: manager db inspect rpc client not configured")
	}
	body, err := encodeManagerDBInspectRequest(req)
	if err != nil {
		return managerDBInspectRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerDBInspectRPCServiceID, body)
	if err != nil {
		return managerDBInspectRPCResponse{}, err
	}
	return decodeManagerDBInspectResponse(respBody)
}

func managerDBInspectRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, dbinspect.ErrCursorMismatch):
		return rpcStatusInvalidCursor
	case errors.Is(err, dbinspect.ErrInvalidQuery), errors.Is(err, dbinspect.ErrUnsupportedQuery), errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusInvalidRequest
	case errors.Is(err, managementusecase.ErrDBInspectUnavailable):
		return rpcStatusUnavailable
	default:
		return rpcStatusRejected
	}
}

func managerDBInspectRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusInvalidRequest:
		return metadb.ErrInvalidArgument
	case rpcStatusInvalidCursor:
		return dbinspect.ErrCursorMismatch
	case rpcStatusUnavailable:
		return managementusecase.ErrDBInspectUnavailable
	case rpcStatusRejected:
		return fmt.Errorf("internalv2/access/node: manager db inspect rpc rejected")
	default:
		return fmt.Errorf("internalv2/access/node: unknown manager db inspect rpc status %q", status)
	}
}

func (a *Adapter) logManagerDBInspectError(req managerDBInspectRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager db inspect rpc rejected",
		wklog.Event("internalv2.access.node.manager_db_inspect_rejected"),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.Error(err),
	)
}
