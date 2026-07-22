package backup

import (
	"context"
	"fmt"
	"sync"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

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

// ClusterRestorePartitionInstallerOptions configures replica-aware installation.
type ClusterRestorePartitionInstallerOptions struct {
	Node   RestoreInstallClusterNode
	Local  RestorePartitionInstaller
	Remote RemoteRestoreInstallClient
}

// ClusterRestorePartitionInstaller installs each partition only on the desired
// replicas of its target physical Slot.
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

// InstallPartition installs one logical partition on its bounded target Slot
// replica set and accepts only identical logical results.
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
	placement, err := newRestoreReplicaPlacement(snapshot, plan.HashSlotCount, i.node.NodeID())
	if err != nil {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: %w", err)
	}
	nodeIDs, err := placement.nodeIDs(hashSlot)
	if err != nil {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: %w", err)
	}

	type installResult struct {
		nodeID uint64
		report backupusecase.RestorePartition
		err    error
	}
	results := make(chan installResult, len(nodeIDs))
	work := make(chan uint64)
	var wait sync.WaitGroup
	workers := len(nodeIDs)
	if workers > maxRestoreInstallParallel {
		workers = maxRestoreInstallParallel
	}
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for nodeID := range work {
				var report backupusecase.RestorePartition
				var installErr error
				if nodeID == i.node.NodeID() {
					report, installErr = i.local.InstallPartition(ctx, plan, hashSlot)
				} else {
					report, installErr = i.remote.InstallBackupRestorePartition(ctx, nodeID, plan, hashSlot)
				}
				results <- installResult{nodeID: nodeID, report: report, err: installErr}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, nodeID := range nodeIDs {
			select {
			case work <- nodeID:
			case <-ctx.Done():
				return
			}
		}
	}()
	wait.Wait()
	close(results)
	var canonical *backupusecase.RestorePartition
	completed := 0
	for result := range results {
		completed++
		if result.err != nil {
			return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: node %d: %w", result.nodeID, result.err)
		}
		if result.report.HashSlot != hashSlot || result.report.EvidenceVersion != backupartifact.PartitionEvidenceVersion ||
			(result.report.MessageCount == 0) != (result.report.MaxMessageID == 0) || !result.report.Installed || result.report.FailureCategory != "" {
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
	if completed != len(nodeIDs) {
		if err := ctx.Err(); err != nil {
			return backupusecase.RestorePartition{}, err
		}
		return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: replica installation incomplete")
	}
	if canonical == nil {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup cluster restore installer: no node report")
	}
	canonical.UpdatedAtUnixMillis = 0
	return *canonical, nil
}

func sameLogicalRestoreReport(left, right backupusecase.RestorePartition) bool {
	return left.HashSlot == right.HashSlot && left.EvidenceVersion == right.EvidenceVersion && left.Installed == right.Installed && left.Verified == right.Verified &&
		left.PlainBytes == right.PlainBytes && left.MetadataRecordCount == right.MetadataRecordCount && left.MessageCount == right.MessageCount &&
		left.MaxMessageID == right.MaxMessageID && left.MetadataSHA256 == right.MetadataSHA256 && left.FailureCategory == right.FailureCategory
}
