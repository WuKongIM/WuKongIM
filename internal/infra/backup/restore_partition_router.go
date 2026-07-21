package backup

import (
	"context"
	"fmt"
	"sort"
	"sync"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

const maxRestoreInstallNodes = 1024

// RestoreInstallClusterNode exposes current membership for restore dispatch.
type RestoreInstallClusterNode interface {
	NodeID() uint64
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
}

// RemoteRestoreInstallClient installs one partition on another restore-mode node.
type RemoteRestoreInstallClient interface {
	InstallBackupRestorePartition(context.Context, uint64, backupusecase.RestorePlan, uint16) (backupusecase.RestorePartition, error)
}

// RestorePartitionInstaller installs one logical restore partition.
type RestorePartitionInstaller interface {
	InstallPartition(context.Context, backupusecase.RestorePlan, uint16) (backupusecase.RestorePartition, error)
}

// ClusterRestorePartitionInstallerOptions configures all-node installation.
type ClusterRestorePartitionInstallerOptions struct {
	Node   RestoreInstallClusterNode
	Local  RestorePartitionInstaller
	Remote RemoteRestoreInstallClient
}

// ClusterRestorePartitionInstaller installs every partition on all current
// non-removed nodes. The qualified three-node topology therefore materializes
// the normal three durable message copies before activation.
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

// InstallPartition installs one logical partition concurrently on every
// current node and accepts only identical logical results.
func (i *ClusterRestorePartitionInstaller) InstallPartition(ctx context.Context, plan backupusecase.RestorePlan, hashSlot uint16) (backupusecase.RestorePartition, error) {
	if i == nil || hashSlot >= plan.HashSlotCount {
		return backupusecase.RestorePartition{}, backupusecase.ErrInvalidRequest
	}
	snapshot, err := i.node.LocalControlSnapshot(ctx)
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	if snapshot.ClusterID != plan.TargetClusterID || snapshot.HashSlots.Count != plan.HashSlotCount {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: target topology fence mismatch")
	}
	nodeIDs := make([]uint64, 0, len(snapshot.Nodes))
	seen := make(map[uint64]struct{}, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.JoinState == control.NodeJoinStateRemoved {
			continue
		}
		if node.NodeID == 0 {
			return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: invalid current node")
		}
		if _, exists := seen[node.NodeID]; exists {
			return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: duplicate current node")
		}
		seen[node.NodeID] = struct{}{}
		nodeIDs = append(nodeIDs, node.NodeID)
	}
	if len(nodeIDs) == 0 || len(nodeIDs) > maxRestoreInstallNodes {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: current node count is invalid")
	}
	if _, exists := seen[i.node.NodeID()]; !exists {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: local node is outside membership")
	}
	sort.Slice(nodeIDs, func(left, right int) bool { return nodeIDs[left] < nodeIDs[right] })

	type installResult struct {
		nodeID uint64
		report backupusecase.RestorePartition
		err    error
	}
	results := make(chan installResult, len(nodeIDs))
	var wait sync.WaitGroup
	for _, nodeID := range nodeIDs {
		nodeID := nodeID
		wait.Add(1)
		go func() {
			defer wait.Done()
			var report backupusecase.RestorePartition
			var installErr error
			if nodeID == i.node.NodeID() {
				report, installErr = i.local.InstallPartition(ctx, plan, hashSlot)
			} else {
				report, installErr = i.remote.InstallBackupRestorePartition(ctx, nodeID, plan, hashSlot)
			}
			results <- installResult{nodeID: nodeID, report: report, err: installErr}
		}()
	}
	wait.Wait()
	close(results)
	var canonical *backupusecase.RestorePartition
	for result := range results {
		if result.err != nil {
			return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: node %d: %w", result.nodeID, result.err)
		}
		if result.report.HashSlot != hashSlot || !result.report.Installed || result.report.FailureCategory != "" {
			return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: node %d returned invalid report", result.nodeID)
		}
		if canonical == nil {
			copy := result.report
			canonical = &copy
			continue
		}
		if !sameLogicalRestoreReport(*canonical, result.report) {
			return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: node reports differ")
		}
	}
	if canonical == nil {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: no node report")
	}
	canonical.UpdatedAtUnixMillis = 0
	return *canonical, nil
}

func sameLogicalRestoreReport(left, right backupusecase.RestorePartition) bool {
	return left.HashSlot == right.HashSlot && left.Installed == right.Installed && left.Verified == right.Verified &&
		left.PlainBytes == right.PlainBytes && left.MessageCount == right.MessageCount && left.MetadataSHA256 == right.MetadataSHA256 && left.FailureCategory == right.FailureCategory
}
