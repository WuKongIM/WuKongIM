package cluster

import (
	"context"
	"errors"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

// ManagementLeaderTransferNode exposes Controller-backed Slot leader transfer intents.
type ManagementLeaderTransferNode interface {
	// RequestSlotLeaderTransfer submits a Slot leader-transfer intent to cluster control.
	RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error)
}

// ManagementLeaderTransferAdapter adapts cluster control writes to management usecases.
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

// ManagementSlotReplicaMoveNode exposes Controller-backed staged Slot replica move intents.
type ManagementSlotReplicaMoveNode interface {
	// RequestSlotReplicaMove submits a staged Slot replica move intent to cluster control.
	RequestSlotReplicaMove(context.Context, control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error)
}

// ManagementSlotReplicaMoveAdapter adapts cluster control writes to onboarding usecases.
type ManagementSlotReplicaMoveAdapter struct {
	node ManagementSlotReplicaMoveNode
}

// NewManagementSlotReplicaMoveAdapter creates a Slot replica move writer.
func NewManagementSlotReplicaMoveAdapter(node ManagementSlotReplicaMoveNode) *ManagementSlotReplicaMoveAdapter {
	return &ManagementSlotReplicaMoveAdapter{node: node}
}

// RequestSlotReplicaMove submits a validated staged Slot replica move request.
func (a *ManagementSlotReplicaMoveAdapter) RequestSlotReplicaMove(ctx context.Context, req control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	if a == nil || a.node == nil {
		return control.SlotReplicaMoveResult{}, managementusecase.ErrNodeOnboardingUnavailable
	}
	return a.node.RequestSlotReplicaMove(ctx, req)
}

// ManagementSlotRuntimeStatusReader adapts cluster local Slot Raft status to management usecases.
type ManagementSlotRuntimeStatusReader struct {
	operator *ManagementSlotRaftOperator
}

// NewManagementSlotRuntimeStatusReader creates a Slot runtime status reader.
func NewManagementSlotRuntimeStatusReader(node ManagementSlotRaftNode) *ManagementSlotRuntimeStatusReader {
	return &ManagementSlotRuntimeStatusReader{operator: NewManagementSlotRaftOperator(node)}
}

// SlotRuntimeStatus returns the currently observed leader and voter set for a Slot.
func (r *ManagementSlotRuntimeStatusReader) SlotRuntimeStatus(ctx context.Context, slotID uint32, candidates []uint64) (managementusecase.SlotRuntimeStatus, error) {
	if r == nil || r.operator == nil {
		return managementusecase.SlotRuntimeStatus{}, managementusecase.ErrSlotRuntimeStatusUnavailable
	}
	for _, candidate := range candidates {
		if candidate == 0 {
			continue
		}
		status, err := r.operator.SlotRaftStatus(ctx, candidate, slotID)
		if errors.Is(err, cluster.ErrSlotNotFound) {
			continue
		}
		if err != nil {
			return managementusecase.SlotRuntimeStatus{}, err
		}
		return managementusecase.SlotRuntimeStatus{
			SlotID:        slotID,
			LeaderID:      status.LeaderID,
			CurrentVoters: append([]uint64(nil), status.CurrentVoters...),
		}, nil
	}
	return managementusecase.SlotRuntimeStatus{}, managementusecase.ErrSlotRuntimeStatusUnavailable
}
