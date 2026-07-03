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

// ManagerConnectionRPCServiceID is the cluster RPC service for owner-node manager connection inventory.
const ManagerConnectionRPCServiceID uint8 = clusternet.RPCManagerConnection

// HandleManagerConnectionRPC handles one encoded manager connection inventory RPC payload.
func (a *Adapter) HandleManagerConnectionRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerConnectionRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager connection rpc decode failed",
			wklog.Event("internal.access.node.manager_connection_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerConnections == nil {
		return encodeManagerConnectionResponse(managerConnectionRPCResponse{Status: rpcStatusRejected})
	}
	switch req.Op {
	case managerConnectionOpList:
		items, err := a.managerConnections.ListConnections(ctx, managementusecase.ListConnectionsRequest{NodeID: req.NodeID, Limit: req.Limit})
		status := managerConnectionRPCStatusForError(err)
		a.logManagerConnectionError(req, status, err)
		return encodeManagerConnectionResponse(managerConnectionRPCResponse{Status: status, Connections: items})
	case managerConnectionOpGet:
		item, err := a.managerConnections.GetConnection(ctx, managementusecase.GetConnectionRequest{NodeID: req.NodeID, SessionID: req.SessionID})
		status := managerConnectionRPCStatusForError(err)
		a.logManagerConnectionError(req, status, err)
		return encodeManagerConnectionResponse(managerConnectionRPCResponse{Status: status, Connection: item})
	case managerConnectionOpRuntimeSummary:
		summary, err := a.managerConnections.NodeRuntimeSummary(ctx, req.NodeID)
		status := managerConnectionRPCStatusForError(err)
		a.logManagerConnectionError(req, status, err)
		return encodeManagerConnectionResponse(managerConnectionRPCResponse{Status: status, Summary: summary})
	case managerConnectionOpSetDrainMode:
		drain, err := a.managerConnections.SetNodeDrainMode(ctx, managementusecase.SetNodeDrainModeRequest{NodeID: req.NodeID, Draining: req.Draining})
		status := managerConnectionRPCStatusForError(err)
		a.logManagerConnectionError(req, status, err)
		return encodeManagerConnectionResponse(managerConnectionRPCResponse{Status: status, Summary: managerConnectionDrainSummary(drain)})
	default:
		err := fmt.Errorf("internal/access/node: unknown manager connection op %q", req.Op)
		a.rpcLogger().Warn("manager connection rpc unknown operation",
			wklog.Event("internal.access.node.manager_connection_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

func managerConnectionDrainSummary(resp managementusecase.SetNodeDrainModeResponse) managementusecase.NodeRuntimeSummary {
	return managementusecase.NodeRuntimeSummary{
		NodeID:               resp.NodeID,
		ActiveOnline:         resp.ActiveOnline,
		ClosingOnline:        resp.ClosingOnline,
		TotalOnline:          resp.TotalOnline,
		GatewaySessions:      resp.GatewaySessions,
		PendingActivations:   resp.PendingActivations,
		AcceptingNewSessions: resp.AcceptingNewSessions,
		Draining:             resp.Draining,
		Unknown:              resp.Unknown,
	}
}

// ListManagerConnections reads owner-node connection inventory from nodeID.
func (c *Client) ListManagerConnections(ctx context.Context, nodeID uint64, limit int) ([]managementusecase.Connection, error) {
	resp, err := c.callManagerConnection(ctx, nodeID, managerConnectionRPCRequest{Op: managerConnectionOpList, NodeID: nodeID, Limit: limit})
	if err != nil {
		return nil, err
	}
	if err := managerConnectionRPCErrorForStatus(resp.Status); err != nil {
		return nil, err
	}
	return append([]managementusecase.Connection(nil), resp.Connections...), nil
}

// GetManagerConnection reads one owner-node connection detail from nodeID.
func (c *Client) GetManagerConnection(ctx context.Context, nodeID, sessionID uint64) (managementusecase.ConnectionDetail, error) {
	resp, err := c.callManagerConnection(ctx, nodeID, managerConnectionRPCRequest{Op: managerConnectionOpGet, NodeID: nodeID, SessionID: sessionID})
	if err != nil {
		return managementusecase.ConnectionDetail{}, err
	}
	if err := managerConnectionRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.ConnectionDetail{}, err
	}
	return resp.Connection, nil
}

// GetManagerRuntimeSummary reads one owner-node runtime summary from nodeID.
func (c *Client) GetManagerRuntimeSummary(ctx context.Context, nodeID uint64) (managementusecase.NodeRuntimeSummary, error) {
	resp, err := c.callManagerConnection(ctx, nodeID, managerConnectionRPCRequest{Op: managerConnectionOpRuntimeSummary, NodeID: nodeID})
	if err != nil {
		return managementusecase.NodeRuntimeSummary{}, err
	}
	if err := managerConnectionRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.NodeRuntimeSummary{}, err
	}
	return resp.Summary, nil
}

// SetManagerDrainMode toggles gateway drain mode on one owner node.
func (c *Client) SetManagerDrainMode(ctx context.Context, nodeID uint64, draining bool) (managementusecase.NodeRuntimeSummary, error) {
	resp, err := c.callManagerConnection(ctx, nodeID, managerConnectionRPCRequest{Op: managerConnectionOpSetDrainMode, NodeID: nodeID, Draining: draining})
	if err != nil {
		return managementusecase.NodeRuntimeSummary{}, err
	}
	if err := managerConnectionRPCDrainErrorForStatus(resp.Status); err != nil {
		return managementusecase.NodeRuntimeSummary{}, err
	}
	return resp.Summary, nil
}

func (c *Client) callManagerConnection(ctx context.Context, nodeID uint64, req managerConnectionRPCRequest) (managerConnectionRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerConnectionRPCResponse{}, fmt.Errorf("internal/access/node: manager connection rpc client not configured")
	}
	body, err := encodeManagerConnectionRequest(req)
	if err != nil {
		return managerConnectionRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerConnectionRPCServiceID, body)
	if err != nil {
		return managerConnectionRPCResponse{}, err
	}
	return decodeManagerConnectionResponse(respBody)
}

func managerConnectionRPCStatusForError(err error) string {
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
		return rpcStatusInvalidArgument
	case errors.Is(err, managementusecase.ErrConnectionReaderUnavailable), errors.Is(err, managementusecase.ErrNodeScaleInUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func managerConnectionRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusNotFound:
		return metadb.ErrNotFound
	case rpcStatusInvalidArgument:
		return metadb.ErrInvalidArgument
	case rpcStatusRejected:
		return managementusecase.ErrConnectionReaderUnavailable
	default:
		return fmt.Errorf("internal/access/node: unknown manager connection rpc status %q", status)
	}
}

func managerConnectionRPCDrainErrorForStatus(status string) error {
	if status == rpcStatusRejected {
		return managementusecase.ErrNodeScaleInUnavailable
	}
	return managerConnectionRPCErrorForStatus(status)
}

func (a *Adapter) logManagerConnectionError(req managerConnectionRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager connection rpc rejected",
		wklog.Event("internal.access.node.manager_connection_rejected"),
		wklog.String("op", req.Op),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.SessionID(req.SessionID),
		wklog.Error(err),
	)
}
