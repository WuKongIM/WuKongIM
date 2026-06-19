package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// ManagementSlotRaftNode exposes node-local and node-remote Slot Raft operations.
type ManagementSlotRaftNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	// LocalSlotRaftStatus reads this node's local Slot Raft status.
	LocalSlotRaftStatus(context.Context, uint32) (clusterv2.SlotRaftStatus, error)
	// LocalCompactSlotRaftLog forces this node's local Slot Raft compaction.
	LocalCompactSlotRaftLog(context.Context, uint32) (clusterv2.SlotRaftCompactionResult, error)
}

// ManagementSlotRaftOperator routes manager Slot Raft operations to selected nodes.
type ManagementSlotRaftOperator struct {
	node   ManagementSlotRaftNode
	remote *accessnode.Client
}

// NewManagementSlotRaftOperator creates a cluster-routed Slot Raft operator.
func NewManagementSlotRaftOperator(node ManagementSlotRaftNode) *ManagementSlotRaftOperator {
	return &ManagementSlotRaftOperator{
		node:   node,
		remote: accessnode.NewClient(node),
	}
}

// SlotRaftStatus reads one node's local Slot Raft status.
func (o *ManagementSlotRaftOperator) SlotRaftStatus(ctx context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotNodeLogStatus, error) {
	if o == nil || o.node == nil || o.remote == nil {
		return managementusecase.SlotNodeLogStatus{}, managementusecase.ErrSlotRaftOperatorUnavailable
	}
	if nodeID == o.node.NodeID() {
		status, err := o.node.LocalSlotRaftStatus(ctx, slotID)
		if err != nil {
			return managementusecase.SlotNodeLogStatus{}, err
		}
		return slotRaftStatusFromCluster(status), nil
	}
	return o.remote.GetManagerSlotRaftStatus(ctx, nodeID, slotID)
}

// CompactSlotRaftLog forces one node's local Slot Raft compaction.
func (o *ManagementSlotRaftOperator) CompactSlotRaftLog(ctx context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotRaftCompactionResult, error) {
	if o == nil || o.node == nil || o.remote == nil {
		return managementusecase.SlotRaftCompactionResult{}, managementusecase.ErrSlotRaftOperatorUnavailable
	}
	if nodeID == o.node.NodeID() {
		result, err := o.node.LocalCompactSlotRaftLog(ctx, slotID)
		if err != nil {
			return managementusecase.SlotRaftCompactionResult{}, err
		}
		return slotRaftCompactionFromCluster(result), nil
	}
	return o.remote.CompactManagerSlotRaftLog(ctx, nodeID, slotID)
}

func slotRaftStatusFromCluster(status clusterv2.SlotRaftStatus) managementusecase.SlotNodeLogStatus {
	return managementusecase.SlotNodeLogStatus{
		NodeID:       status.NodeID,
		LeaderID:     status.LeaderID,
		Role:         status.Role,
		CommitIndex:  status.CommitIndex,
		AppliedIndex: status.AppliedIndex,
	}
}

func slotRaftCompactionFromCluster(result clusterv2.SlotRaftCompactionResult) managementusecase.SlotRaftCompactionResult {
	return managementusecase.SlotRaftCompactionResult{
		NodeID:              result.NodeID,
		SlotID:              result.SlotID,
		AppliedIndex:        result.AppliedIndex,
		BeforeSnapshotIndex: result.BeforeSnapshotIndex,
		AfterSnapshotIndex:  result.AfterSnapshotIndex,
		Compacted:           result.Compacted,
		SkippedReason:       result.SkippedReason,
	}
}
