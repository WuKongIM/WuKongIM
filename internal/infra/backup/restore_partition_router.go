package backup

import (
	"context"
	"fmt"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

// RestoreInstallClusterNode exposes current membership for restore dispatch.
type RestoreInstallClusterNode interface {
	NodeID() uint64
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
	RouteHashSlot(uint16) (clusterpkg.Route, error)
}

// RemoteRestoreInstallClient installs one partition on another restore-mode node.
type RemoteRestoreInstallClient interface {
	InstallBackupRestorePartition(context.Context, uint64, backupusecase.RestorePlan, uint16) (backupusecase.RestorePartition, error)
}

// RestorePartitionInstaller installs one logical restore partition.
type RestorePartitionInstaller interface {
	InstallPartition(context.Context, backupusecase.RestorePlan, uint16) (backupusecase.RestorePartition, error)
}

// ClusterRestorePartitionInstallerOptions configures replica-aware installation.
type ClusterRestorePartitionInstallerOptions struct {
	Node   RestoreInstallClusterNode
	Local  RestorePartitionInstaller
	Remote RemoteRestoreInstallClient
}

// ClusterRestorePartitionInstaller dispatches each partition to exactly one
// current target Slot Leader. Followers converge from that Leader without
// repository reads or key-authority calls.
type ClusterRestorePartitionInstaller struct {
	node   RestoreInstallClusterNode
	local  RestorePartitionInstaller
	remote RemoteRestoreInstallClient
}

// NewClusterRestorePartitionInstaller creates a membership-aware installer.
func NewClusterRestorePartitionInstaller(options ClusterRestorePartitionInstallerOptions) (*ClusterRestorePartitionInstaller, error) {
	if options.Node == nil || options.Local == nil || options.Remote == nil || options.Node.NodeID() == 0 {
		return nil, fmt.Errorf("backup cluster restore installer: invalid options")
	}
	return &ClusterRestorePartitionInstaller{node: options.Node, local: options.Local, remote: options.Remote}, nil
}

// Assignment resolves and fences the current target Slot Leader.
func (i *ClusterRestorePartitionInstaller) Assignment(
	ctx context.Context,
	plan backupusecase.RestorePlan,
	hashSlot uint16,
) (backupusecase.RestorePartitionAssignment, error) {
	if i == nil || hashSlot >= plan.HashSlotCount {
		return backupusecase.RestorePartitionAssignment{}, backupusecase.ErrInvalidRequest
	}
	snapshot, err := i.node.LocalControlSnapshot(ctx)
	if err != nil {
		return backupusecase.RestorePartitionAssignment{}, err
	}
	if snapshot.ClusterID != plan.TargetClusterID ||
		snapshot.HashSlots.Count != plan.HashSlotCount {
		return backupusecase.RestorePartitionAssignment{},
			fmt.Errorf("backup cluster restore installer: target topology fence mismatch")
	}
	placement, err := newRestoreReplicaPlacement(
		snapshot, plan.HashSlotCount, i.node.NodeID(),
	)
	if err != nil {
		return backupusecase.RestorePartitionAssignment{},
			fmt.Errorf("backup cluster restore installer: %w", err)
	}
	nodeIDs, err := placement.nodeIDs(hashSlot)
	if err != nil {
		return backupusecase.RestorePartitionAssignment{},
			fmt.Errorf("backup cluster restore installer: %w", err)
	}
	configEpoch, err := placement.configEpoch(hashSlot)
	if err != nil {
		return backupusecase.RestorePartitionAssignment{},
			fmt.Errorf("backup cluster restore installer: %w", err)
	}
	route, err := i.node.RouteHashSlot(hashSlot)
	if err != nil {
		return backupusecase.RestorePartitionAssignment{}, err
	}
	expectedSlotID := placement.slotByHashSlot[hashSlot]
	if route.HashSlot != hashSlot || route.SlotID != expectedSlotID ||
		route.Leader == 0 || route.LeaderTerm == 0 || route.ConfigEpoch == 0 ||
		route.ConfigEpoch != configEpoch ||
		!containsRestoreNode(nodeIDs, route.Leader) ||
		!sameCheckpointRestoreNodeSet(route.Peers, nodeIDs) {
		return backupusecase.RestorePartitionAssignment{},
			fmt.Errorf("backup cluster restore installer: current Slot Leader fence is invalid")
	}
	return backupusecase.RestorePartitionAssignment{
		HashSlot: hashSlot, TargetSlotID: route.SlotID,
		LeaderNodeID: route.Leader, LeaderTerm: route.LeaderTerm,
		ConfigEpoch: route.ConfigEpoch, ReplicaCount: uint32(len(nodeIDs)),
	}, nil
}

// InstallPartition installs one logical partition only on its current target
// Slot Leader.
func (i *ClusterRestorePartitionInstaller) InstallPartition(ctx context.Context, plan backupusecase.RestorePlan, hashSlot uint16) (backupusecase.RestorePartition, error) {
	assignment, err := i.Assignment(ctx, plan, hashSlot)
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	progress := plan.Partitions[hashSlot]
	if progress.TargetSlotID != assignment.TargetSlotID ||
		progress.LeaderNodeID != assignment.LeaderNodeID ||
		progress.LeaderTerm != assignment.LeaderTerm ||
		progress.ConfigEpoch != assignment.ConfigEpoch ||
		progress.ReplicaCount != assignment.ReplicaCount {
		return backupusecase.RestorePartition{},
			backupusecase.ErrStateConflict
	}
	var report backupusecase.RestorePartition
	if assignment.LeaderNodeID == i.node.NodeID() {
		report, err = i.local.InstallPartition(ctx, plan, hashSlot)
	} else {
		report, err = i.remote.InstallBackupRestorePartition(
			ctx, assignment.LeaderNodeID, plan, hashSlot,
		)
	}
	if err != nil {
		return backupusecase.RestorePartition{}, fmt.Errorf(
			"backup cluster restore installer: Leader node %d: %w",
			assignment.LeaderNodeID, err,
		)
	}
	if report.HashSlot != hashSlot ||
		report.EvidenceVersion != backupartifact.RestoreEvidenceVersion ||
		(report.MessageCount == 0) != (report.MaxMessageID == 0) ||
		!report.Installed || report.FailureCategory != "" {
		return backupusecase.RestorePartition{}, fmt.Errorf(
			"backup cluster restore installer: Leader node %d returned invalid report",
			assignment.LeaderNodeID,
		)
	}
	report.TargetSlotID = assignment.TargetSlotID
	report.LeaderNodeID = assignment.LeaderNodeID
	report.LeaderTerm = assignment.LeaderTerm
	report.ConfigEpoch = assignment.ConfigEpoch
	report.ReplicaCount = assignment.ReplicaCount
	report.UpdatedAtUnixMillis = 0
	return report, nil
}

func containsRestoreNode(nodeIDs []uint64, target uint64) bool {
	for _, nodeID := range nodeIDs {
		if nodeID == target {
			return true
		}
	}
	return false
}
