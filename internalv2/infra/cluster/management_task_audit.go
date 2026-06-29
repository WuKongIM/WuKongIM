package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// ManagementTaskAuditNode exposes Controller Raft status and task-audit peer RPC.
type ManagementTaskAuditNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	// LocalControllerRaftStatus reads this node's local Controller Raft status.
	LocalControllerRaftStatus(context.Context) (clusterv2.ControllerRaftStatus, error)
	// LocalControlSnapshot returns the latest locally visible control snapshot.
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
}

// ManagementTaskAuditReader routes retained ControllerV2 task audit reads to the Controller leader.
type ManagementTaskAuditReader struct {
	node   ManagementTaskAuditNode
	local  managementusecase.ControllerTaskAuditReader
	remote *accessnode.Client
}

// NewManagementTaskAuditReader creates a cluster-routed ControllerV2 task audit reader.
func NewManagementTaskAuditReader(node ManagementTaskAuditNode, local managementusecase.ControllerTaskAuditReader) *ManagementTaskAuditReader {
	return &ManagementTaskAuditReader{
		node:   node,
		local:  local,
		remote: accessnode.NewClient(node),
	}
}

// ListControllerTaskAudits returns retained task histories from the Controller leader.
func (r *ManagementTaskAuditReader) ListControllerTaskAudits(ctx context.Context, req managementusecase.ControllerTaskAuditListRequest) (managementusecase.ControllerTaskAuditListResponse, error) {
	targetNodeID, err := r.controllerLeaderNodeID(ctx)
	if err != nil {
		return managementusecase.ControllerTaskAuditListResponse{}, err
	}
	if targetNodeID == r.node.NodeID() {
		if r.local == nil {
			return managementusecase.ControllerTaskAuditListResponse{}, managementusecase.ErrControllerTaskAuditUnavailable
		}
		return r.local.ListControllerTaskAudits(ctx, req)
	}
	return r.remote.ListManagerControllerTaskAudits(ctx, targetNodeID, req)
}

// ControllerTaskAuditEvents returns one retained task timeline from the Controller leader.
func (r *ManagementTaskAuditReader) ControllerTaskAuditEvents(ctx context.Context, taskID string) (managementusecase.ControllerTaskAuditEventsResponse, error) {
	targetNodeID, err := r.controllerLeaderNodeID(ctx)
	if err != nil {
		return managementusecase.ControllerTaskAuditEventsResponse{}, err
	}
	if targetNodeID == r.node.NodeID() {
		if r.local == nil {
			return managementusecase.ControllerTaskAuditEventsResponse{}, managementusecase.ErrControllerTaskAuditUnavailable
		}
		return r.local.ControllerTaskAuditEvents(ctx, taskID)
	}
	return r.remote.ManagerControllerTaskAuditEvents(ctx, targetNodeID, taskID)
}

func (r *ManagementTaskAuditReader) controllerLeaderNodeID(ctx context.Context) (uint64, error) {
	if r == nil || r.node == nil || r.remote == nil {
		return 0, managementusecase.ErrControllerTaskAuditUnavailable
	}
	status, err := r.node.LocalControllerRaftStatus(ctx)
	if err != nil {
		snapshot, snapshotErr := r.node.LocalControlSnapshot(ctx)
		if snapshotErr == nil && snapshot.ControllerID != 0 {
			return snapshot.ControllerID, nil
		}
		return 0, err
	}
	if status.LeaderID != 0 {
		return status.LeaderID, nil
	}
	if status.Role == "leader" && status.NodeID != 0 {
		return status.NodeID, nil
	}
	if status.NodeID != 0 {
		return status.NodeID, nil
	}
	return r.node.NodeID(), nil
}
