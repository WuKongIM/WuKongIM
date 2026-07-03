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

// ManagerLogRPCServiceID is the cluster RPC service for node-local distributed log inspection.
const ManagerLogRPCServiceID uint8 = clusternet.RPCManagerLogs

// HandleManagerLogRPC handles one encoded manager distributed log RPC payload.
func (a *Adapter) HandleManagerLogRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerLogRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager log rpc decode failed",
			wklog.Event("internalv2.access.node.manager_log_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerLogs == nil {
		return encodeManagerLogResponse(managerLogRPCResponse{Status: rpcStatusRejected})
	}
	switch req.Op {
	case managerLogOpController:
		page, err := a.managerLogs.ControllerLogEntries(ctx, managementusecase.ListControllerLogEntriesRequest{
			NodeID: req.NodeID,
			Limit:  req.Limit,
			Cursor: req.Cursor,
		})
		status := managerLogRPCStatusForError(err)
		a.logManagerLogError(req, status, err)
		return encodeManagerLogResponse(managerLogRPCResponse{Status: status, Controller: page})
	case managerLogOpSlot:
		page, err := a.managerLogs.SlotLogEntries(ctx, managementusecase.ListSlotLogEntriesRequest{
			NodeID: req.NodeID,
			SlotID: req.SlotID,
			Limit:  req.Limit,
			Cursor: req.Cursor,
		})
		status := managerLogRPCStatusForError(err)
		a.logManagerLogError(req, status, err)
		return encodeManagerLogResponse(managerLogRPCResponse{Status: status, Slot: page})
	default:
		err := fmt.Errorf("internalv2/access/node: unknown manager log op %q", req.Op)
		a.rpcLogger().Warn("manager log rpc unknown operation",
			wklog.Event("internalv2.access.node.manager_log_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

// GetManagerControllerLogEntries reads one node's Controller Raft log page.
func (c *Client) GetManagerControllerLogEntries(ctx context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error) {
	resp, err := c.callManagerLog(ctx, req.NodeID, managerLogRPCRequest{
		Op:     managerLogOpController,
		NodeID: req.NodeID,
		Limit:  req.Limit,
		Cursor: req.Cursor,
	})
	if err != nil {
		return managementusecase.ControllerLogEntriesResponse{}, err
	}
	if err := managerLogRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.ControllerLogEntriesResponse{}, err
	}
	return resp.Controller, nil
}

// GetManagerSlotLogEntries reads one node's Slot Raft log page.
func (c *Client) GetManagerSlotLogEntries(ctx context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error) {
	resp, err := c.callManagerLog(ctx, req.NodeID, managerLogRPCRequest{
		Op:     managerLogOpSlot,
		NodeID: req.NodeID,
		SlotID: req.SlotID,
		Limit:  req.Limit,
		Cursor: req.Cursor,
	})
	if err != nil {
		return managementusecase.SlotLogEntriesResponse{}, err
	}
	if err := managerLogRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.SlotLogEntriesResponse{}, err
	}
	return resp.Slot, nil
}

func (c *Client) callManagerLog(ctx context.Context, nodeID uint64, req managerLogRPCRequest) (managerLogRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerLogRPCResponse{}, fmt.Errorf("internalv2/access/node: manager log rpc client not configured")
	}
	body, err := encodeManagerLogRequest(req)
	if err != nil {
		return managerLogRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerLogRPCServiceID, body)
	if err != nil {
		return managerLogRPCResponse{}, err
	}
	return decodeManagerLogResponse(respBody)
}

func managerLogRPCStatusForError(err error) string {
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
	case errors.Is(err, managementusecase.ErrLogReaderUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func managerLogRPCErrorForStatus(status string) error {
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
		return managementusecase.ErrLogReaderUnavailable
	default:
		return fmt.Errorf("internalv2/access/node: unknown manager log rpc status %q", status)
	}
}

func (a *Adapter) logManagerLogError(req managerLogRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager log rpc rejected",
		wklog.Event("internalv2.access.node.manager_log_rejected"),
		wklog.String("op", req.Op),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.Uint64("slotID", uint64(req.SlotID)),
		wklog.Int("limit", req.Limit),
		wklog.Uint64("cursor", req.Cursor),
		wklog.Error(err),
	)
}
