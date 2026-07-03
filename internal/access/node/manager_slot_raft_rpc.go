package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerSlotRaftRPCServiceID is the cluster RPC service for node-local Slot Raft operations.
const ManagerSlotRaftRPCServiceID uint8 = clusternet.RPCManagerSlotRaft

// HandleManagerSlotRaftRPC handles one encoded manager Slot Raft RPC payload.
func (a *Adapter) HandleManagerSlotRaftRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerSlotRaftRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager slot raft rpc decode failed",
			wklog.Event("internalv2.access.node.manager_slot_raft_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerSlotRaft == nil {
		return encodeManagerSlotRaftResponse(managerSlotRaftRPCResponse{Status: rpcStatusRejected})
	}
	switch req.Op {
	case managerSlotRaftOpStatus:
		status, err := a.managerSlotRaft.SlotRaftStatus(ctx, req.NodeID, req.SlotID)
		rpcStatus := managerSlotRaftRPCStatusForError(err)
		a.logManagerSlotRaftError(req, rpcStatus, err)
		return encodeManagerSlotRaftResponse(managerSlotRaftRPCResponse{Status: rpcStatus, Raft: status})
	case managerSlotRaftOpCompact:
		result, err := a.managerSlotRaft.CompactSlotRaftLog(ctx, req.NodeID, req.SlotID)
		status := managerSlotRaftRPCStatusForError(err)
		a.logManagerSlotRaftError(req, status, err)
		return encodeManagerSlotRaftResponse(managerSlotRaftRPCResponse{Status: status, Compaction: result})
	default:
		err := fmt.Errorf("internalv2/access/node: unknown manager slot raft op %q", req.Op)
		a.rpcLogger().Warn("manager slot raft rpc unknown operation",
			wklog.Event("internalv2.access.node.manager_slot_raft_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

// GetManagerSlotRaftStatus reads one node's local Slot Raft status.
func (c *Client) GetManagerSlotRaftStatus(ctx context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotNodeLogStatus, error) {
	if c == nil || c.node == nil {
		return managementusecase.SlotNodeLogStatus{}, fmt.Errorf("internalv2/access/node: manager slot raft rpc client not configured")
	}
	body, err := encodeManagerSlotRaftRequest(managerSlotRaftRPCRequest{
		Op:     managerSlotRaftOpStatus,
		NodeID: nodeID,
		SlotID: slotID,
	})
	if err != nil {
		return managementusecase.SlotNodeLogStatus{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerSlotRaftRPCServiceID, body)
	if err != nil {
		return managementusecase.SlotNodeLogStatus{}, err
	}
	resp, err := decodeManagerSlotRaftResponse(respBody)
	if err != nil {
		return managementusecase.SlotNodeLogStatus{}, err
	}
	if err := managerSlotRaftRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.SlotNodeLogStatus{}, err
	}
	return resp.Raft, nil
}

// CompactManagerSlotRaftLog forces one node's local Slot Raft compaction.
func (c *Client) CompactManagerSlotRaftLog(ctx context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotRaftCompactionResult, error) {
	if c == nil || c.node == nil {
		return managementusecase.SlotRaftCompactionResult{}, fmt.Errorf("internalv2/access/node: manager slot raft rpc client not configured")
	}
	body, err := encodeManagerSlotRaftRequest(managerSlotRaftRPCRequest{
		Op:     managerSlotRaftOpCompact,
		NodeID: nodeID,
		SlotID: slotID,
	})
	if err != nil {
		return managementusecase.SlotRaftCompactionResult{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerSlotRaftRPCServiceID, body)
	if err != nil {
		return managementusecase.SlotRaftCompactionResult{}, err
	}
	resp, err := decodeManagerSlotRaftResponse(respBody)
	if err != nil {
		return managementusecase.SlotRaftCompactionResult{}, err
	}
	if err := managerSlotRaftRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.SlotRaftCompactionResult{}, err
	}
	return resp.Compaction, nil
}

func managerSlotRaftRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrNotFound):
		return rpcStatusNotFound
	case errors.Is(err, cluster.ErrSlotNotFound):
		return rpcStatusNotFound
	case errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusRejected
	case errors.Is(err, managementusecase.ErrSlotRaftOperatorUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func managerSlotRaftRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusNotFound:
		return cluster.ErrSlotNotFound
	case rpcStatusRejected:
		return managementusecase.ErrSlotRaftOperatorUnavailable
	default:
		return fmt.Errorf("internalv2/access/node: unknown manager slot raft rpc status %q", status)
	}
}

func (a *Adapter) logManagerSlotRaftError(req managerSlotRaftRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager slot raft rpc rejected",
		wklog.Event("internalv2.access.node.manager_slot_raft_rejected"),
		wklog.String("op", req.Op),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.Uint64("slotID", uint64(req.SlotID)),
		wklog.Error(err),
	)
}
