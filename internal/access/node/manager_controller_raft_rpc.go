package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerControllerRaftRPCServiceID is the cluster RPC service for node-local Controller Raft operations.
const ManagerControllerRaftRPCServiceID uint8 = clusternet.RPCManagerControllerRaft

// HandleManagerControllerRaftRPC handles one encoded manager Controller Raft RPC payload.
func (a *Adapter) HandleManagerControllerRaftRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerControllerRaftRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager controller raft rpc decode failed",
			wklog.Event("internal.access.node.manager_controller_raft_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerControllerRaft == nil {
		return encodeManagerControllerRaftResponse(managerControllerRaftRPCResponse{Status: rpcStatusRejected})
	}
	switch req.Op {
	case managerControllerRaftOpStatus:
		status, err := a.managerControllerRaft.ControllerRaftStatus(ctx, req.NodeID)
		rpcStatus := managerControllerRaftRPCStatusForError(err)
		a.logManagerControllerRaftError(req, rpcStatus, err)
		return encodeManagerControllerRaftResponse(managerControllerRaftRPCResponse{Status: rpcStatus, RaftStatus: status})
	case managerControllerRaftOpCompact:
		result, err := a.managerControllerRaft.CompactControllerRaftLog(ctx, req.NodeID)
		rpcStatus := managerControllerRaftRPCStatusForError(err)
		a.logManagerControllerRaftError(req, rpcStatus, err)
		return encodeManagerControllerRaftResponse(managerControllerRaftRPCResponse{Status: rpcStatus, Compaction: result})
	default:
		err := fmt.Errorf("internal/access/node: unknown manager controller raft op %q", req.Op)
		a.rpcLogger().Warn("manager controller raft rpc unknown operation",
			wklog.Event("internal.access.node.manager_controller_raft_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

// GetManagerControllerRaftStatus reads one node's local Controller Raft status.
func (c *Client) GetManagerControllerRaftStatus(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftStatus, error) {
	resp, err := c.callManagerControllerRaft(ctx, nodeID, managerControllerRaftRPCRequest{Op: managerControllerRaftOpStatus, NodeID: nodeID})
	if err != nil {
		return managementusecase.ControllerRaftStatus{}, err
	}
	if err := managerControllerRaftRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.ControllerRaftStatus{}, err
	}
	return resp.RaftStatus, nil
}

// CompactManagerControllerRaftLog forces one node's local Controller Raft compaction.
func (c *Client) CompactManagerControllerRaftLog(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftCompactionResult, error) {
	resp, err := c.callManagerControllerRaft(ctx, nodeID, managerControllerRaftRPCRequest{Op: managerControllerRaftOpCompact, NodeID: nodeID})
	if err != nil {
		return managementusecase.ControllerRaftCompactionResult{}, err
	}
	if err := managerControllerRaftRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.ControllerRaftCompactionResult{}, err
	}
	return resp.Compaction, nil
}

func (c *Client) callManagerControllerRaft(ctx context.Context, nodeID uint64, req managerControllerRaftRPCRequest) (managerControllerRaftRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerControllerRaftRPCResponse{}, fmt.Errorf("internal/access/node: manager controller raft rpc client not configured")
	}
	body, err := encodeManagerControllerRaftRequest(req)
	if err != nil {
		return managerControllerRaftRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerControllerRaftRPCServiceID, body)
	if err != nil {
		return managerControllerRaftRPCResponse{}, err
	}
	return decodeManagerControllerRaftResponse(respBody)
}

func managerControllerRaftRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, managementusecase.ErrControllerRaftOperatorUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func managerControllerRaftRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusRejected:
		return managementusecase.ErrControllerRaftOperatorUnavailable
	default:
		return fmt.Errorf("internal/access/node: unknown manager controller raft rpc status %q", status)
	}
}

func (a *Adapter) logManagerControllerRaftError(req managerControllerRaftRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager controller raft rpc rejected",
		wklog.Event("internal.access.node.manager_controller_raft_rejected"),
		wklog.String("op", req.Op),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.Error(err),
	)
}
