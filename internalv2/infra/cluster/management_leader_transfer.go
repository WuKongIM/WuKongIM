package cluster

import (
	"context"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// ManagementLeaderTransferNode exposes Controller-backed Slot leader transfer intents.
type ManagementLeaderTransferNode interface {
	// RequestSlotLeaderTransfer submits a Slot leader-transfer intent to cluster control.
	RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error)
}

// ManagementSlotRuntimeStatusNode exposes node-local Slot Raft runtime status.
type ManagementSlotRuntimeStatusNode interface {
	// LocalSlotRaftStatus reads this node's live Slot Raft status.
	LocalSlotRaftStatus(context.Context, uint32) (clusterv2.SlotRaftStatus, error)
}

// ManagementLeaderTransferAdapter adapts clusterv2 control writes to management usecases.
type ManagementLeaderTransferAdapter struct {
	node ManagementLeaderTransferNode
}

// NewManagementLeaderTransferAdapter creates a Slot leader-transfer writer.
func NewManagementLeaderTransferAdapter(node ManagementLeaderTransferNode) *ManagementLeaderTransferAdapter {
	return &ManagementLeaderTransferAdapter{node: node}
}

// RequestSlotLeaderTransfer submits a validated Slot leader-transfer request.
func (a *ManagementLeaderTransferAdapter) RequestSlotLeaderTransfer(ctx context.Context, req control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	if a == nil || a.node == nil {
		return control.SlotLeaderTransferResult{}, managementusecase.ErrSlotLeaderTransferUnavailable
	}
	return a.node.RequestSlotLeaderTransfer(ctx, req)
}

// ManagementSlotRuntimeStatusReader adapts clusterv2 local Slot Raft status to management usecases.
type ManagementSlotRuntimeStatusReader struct {
	node ManagementSlotRuntimeStatusNode
}

// NewManagementSlotRuntimeStatusReader creates a Slot runtime status reader.
func NewManagementSlotRuntimeStatusReader(node ManagementSlotRuntimeStatusNode) *ManagementSlotRuntimeStatusReader {
	return &ManagementSlotRuntimeStatusReader{node: node}
}

// SlotRuntimeStatus returns the currently observed leader and voter set for a Slot.
func (r *ManagementSlotRuntimeStatusReader) SlotRuntimeStatus(ctx context.Context, slotID uint32) (managementusecase.SlotRuntimeStatus, error) {
	if r == nil || r.node == nil {
		return managementusecase.SlotRuntimeStatus{}, managementusecase.ErrSlotRuntimeStatusUnavailable
	}
	status, err := r.node.LocalSlotRaftStatus(ctx, slotID)
	if err != nil {
		return managementusecase.SlotRuntimeStatus{}, err
	}
	return managementusecase.SlotRuntimeStatus{
		SlotID:        status.SlotID,
		LeaderID:      status.LeaderID,
		CurrentVoters: append([]uint64(nil), status.CurrentVoters...),
	}, nil
}
