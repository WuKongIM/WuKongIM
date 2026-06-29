package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerTaskAuditRPCServiceID is the clusterv2 RPC service for retained ControllerV2 task audit reads.
const ManagerTaskAuditRPCServiceID uint8 = clusternet.RPCManagerTaskAudit

// HandleManagerTaskAuditRPC handles one encoded manager ControllerV2 task audit RPC payload.
func (a *Adapter) HandleManagerTaskAuditRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerTaskAuditRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager task audit rpc decode failed",
			wklog.Event("internalv2.access.node.manager_task_audit_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerTaskAudit == nil {
		return encodeManagerTaskAuditResponse(managerTaskAuditRPCResponse{Status: rpcStatusUnavailable})
	}
	switch req.Op {
	case managerTaskAuditOpList:
		page, err := a.managerTaskAudit.ListControllerTaskAudits(ctx, req.List)
		status := managerTaskAuditRPCStatusForError(err)
		a.logManagerTaskAuditError(req, status, err)
		return encodeManagerTaskAuditResponse(managerTaskAuditRPCResponse{Status: status, List: page})
	case managerTaskAuditOpEvents:
		events, err := a.managerTaskAudit.ControllerTaskAuditEvents(ctx, req.TaskID)
		status := managerTaskAuditRPCStatusForError(err)
		a.logManagerTaskAuditError(req, status, err)
		return encodeManagerTaskAuditResponse(managerTaskAuditRPCResponse{Status: status, Events: events})
	default:
		err := fmt.Errorf("internalv2/access/node: unknown manager task audit op %q", req.Op)
		a.rpcLogger().Warn("manager task audit rpc unknown operation",
			wklog.Event("internalv2.access.node.manager_task_audit_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

// ListManagerControllerTaskAudits reads retained ControllerV2 task histories from one node.
func (c *Client) ListManagerControllerTaskAudits(ctx context.Context, nodeID uint64, req managementusecase.ControllerTaskAuditListRequest) (managementusecase.ControllerTaskAuditListResponse, error) {
	resp, err := c.callManagerTaskAudit(ctx, nodeID, managerTaskAuditRPCRequest{Op: managerTaskAuditOpList, List: req})
	if err != nil {
		return managementusecase.ControllerTaskAuditListResponse{}, err
	}
	if err := managerTaskAuditRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.ControllerTaskAuditListResponse{}, err
	}
	return resp.List, nil
}

// ManagerControllerTaskAuditEvents reads one retained ControllerV2 task timeline from one node.
func (c *Client) ManagerControllerTaskAuditEvents(ctx context.Context, nodeID uint64, taskID string) (managementusecase.ControllerTaskAuditEventsResponse, error) {
	resp, err := c.callManagerTaskAudit(ctx, nodeID, managerTaskAuditRPCRequest{Op: managerTaskAuditOpEvents, TaskID: taskID})
	if err != nil {
		return managementusecase.ControllerTaskAuditEventsResponse{}, err
	}
	if err := managerTaskAuditRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.ControllerTaskAuditEventsResponse{}, err
	}
	return resp.Events, nil
}

func (c *Client) callManagerTaskAudit(ctx context.Context, nodeID uint64, req managerTaskAuditRPCRequest) (managerTaskAuditRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerTaskAuditRPCResponse{}, fmt.Errorf("internalv2/access/node: manager task audit rpc client not configured")
	}
	body, err := encodeManagerTaskAuditRequest(req)
	if err != nil {
		return managerTaskAuditRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerTaskAuditRPCServiceID, body)
	if err != nil {
		return managerTaskAuditRPCResponse{}, err
	}
	return decodeManagerTaskAuditResponse(respBody)
}

func managerTaskAuditRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusInvalidArgument
	case errors.Is(err, managementusecase.ErrControllerTaskAuditNotFound):
		return rpcStatusNotFound
	case errors.Is(err, managementusecase.ErrControllerTaskAuditUnavailable):
		return rpcStatusUnavailable
	default:
		return rpcStatusRejected
	}
}

func managerTaskAuditRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusInvalidArgument:
		return metadb.ErrInvalidArgument
	case rpcStatusNotFound:
		return managementusecase.ErrControllerTaskAuditNotFound
	case rpcStatusUnavailable:
		return managementusecase.ErrControllerTaskAuditUnavailable
	case rpcStatusRejected:
		return fmt.Errorf("internalv2/access/node: manager task audit rpc rejected")
	default:
		return fmt.Errorf("internalv2/access/node: unknown manager task audit rpc status %q", status)
	}
}

func (a *Adapter) logManagerTaskAuditError(req managerTaskAuditRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager task audit rpc rejected",
		wklog.Event("internalv2.access.node.manager_task_audit_rejected"),
		wklog.String("op", req.Op),
		wklog.String("status", status),
		wklog.String("taskID", req.TaskID),
		wklog.String("kind", req.List.Kind),
		wklog.String("taskStatus", req.List.Status),
		wklog.Uint64("nodeID", req.List.NodeID),
		wklog.Uint64("slotID", uint64(req.List.SlotID)),
		wklog.Error(err),
	)
}
