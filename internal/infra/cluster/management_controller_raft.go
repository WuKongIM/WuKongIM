package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// ManagementControllerRaftNode exposes node-local and node-remote Controller Raft operations.
type ManagementControllerRaftNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	// LocalControllerRaftStatus reads this node's local Controller Raft status.
	LocalControllerRaftStatus(context.Context) (cluster.ControllerRaftStatus, error)
	// LocalCompactControllerRaftLog forces this node's local Controller Raft compaction.
	LocalCompactControllerRaftLog(context.Context) (cluster.ControllerRaftCompactionResult, error)
}

// ManagementControllerRaftOperator routes manager Controller Raft operations to selected nodes.
type ManagementControllerRaftOperator struct {
	node   ManagementControllerRaftNode
	remote *accessnode.Client
}

// NewManagementControllerRaftOperator creates a cluster-routed Controller Raft operator.
func NewManagementControllerRaftOperator(node ManagementControllerRaftNode) *ManagementControllerRaftOperator {
	return &ManagementControllerRaftOperator{
		node:   node,
		remote: accessnode.NewClient(node),
	}
}

// ControllerRaftStatus reads one node's local Controller Raft status.
func (o *ManagementControllerRaftOperator) ControllerRaftStatus(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftStatus, error) {
	if o == nil || o.node == nil || o.remote == nil {
		return managementusecase.ControllerRaftStatus{}, managementusecase.ErrControllerRaftOperatorUnavailable
	}
	if nodeID == o.node.NodeID() {
		status, err := o.node.LocalControllerRaftStatus(ctx)
		if err != nil {
			return managementusecase.ControllerRaftStatus{}, err
		}
		return controllerRaftStatusFromCluster(status), nil
	}
	return o.remote.GetManagerControllerRaftStatus(ctx, nodeID)
}

// CompactControllerRaftLog forces one node's local Controller Raft compaction.
func (o *ManagementControllerRaftOperator) CompactControllerRaftLog(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftCompactionResult, error) {
	if o == nil || o.node == nil || o.remote == nil {
		return managementusecase.ControllerRaftCompactionResult{}, managementusecase.ErrControllerRaftOperatorUnavailable
	}
	if nodeID == o.node.NodeID() {
		result, err := o.node.LocalCompactControllerRaftLog(ctx)
		if err != nil {
			return managementusecase.ControllerRaftCompactionResult{}, err
		}
		return controllerRaftCompactionFromCluster(result), nil
	}
	return o.remote.CompactManagerControllerRaftLog(ctx, nodeID)
}

func controllerRaftStatusFromCluster(status cluster.ControllerRaftStatus) managementusecase.ControllerRaftStatus {
	health := "healthy"
	if status.Degraded {
		health = "degraded"
	}
	return managementusecase.ControllerRaftStatus{
		NodeID:        status.NodeID,
		Role:          status.Role,
		LeaderID:      status.LeaderID,
		Term:          status.Term,
		Health:        health,
		FirstIndex:    status.FirstIndex,
		LastIndex:     status.LastIndex,
		CommitIndex:   status.CommitIndex,
		AppliedIndex:  status.AppliedIndex,
		Voters:        append([]uint64(nil), status.Voters...),
		Learners:      append([]uint64(nil), status.Learners...),
		SnapshotIndex: status.SnapshotIndex,
		SnapshotTerm:  status.SnapshotTerm,
		Compaction: managementusecase.ControllerRaftCompaction{
			Enabled:           status.Compaction.Enabled,
			TriggerEntries:    status.Compaction.TriggerEntries,
			CheckInterval:     status.Compaction.CheckInterval,
			LastSnapshotIndex: status.Compaction.AfterSnapshotIndex,
			LastCheckAt:       status.Compaction.LastAttemptAt,
			LastSnapshotAt:    status.Compaction.LastSuccessAt,
			LastError:         status.Compaction.LastError,
			LastErrorAt:       status.Compaction.LastErrorAt,
			Degraded:          status.Compaction.LastError != "",
		},
	}
}

func controllerRaftCompactionFromCluster(result cluster.ControllerRaftCompactionResult) managementusecase.ControllerRaftCompactionResult {
	return managementusecase.ControllerRaftCompactionResult{
		NodeID:              result.NodeID,
		AppliedIndex:        result.AppliedIndex,
		BeforeSnapshotIndex: result.BeforeSnapshotIndex,
		AfterSnapshotIndex:  result.AfterSnapshotIndex,
		Compacted:           result.Compacted,
		SkippedReason:       result.SkippedReason,
		Error:               result.Error,
	}
}
