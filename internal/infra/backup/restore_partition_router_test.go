package backup_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/stretchr/testify/require"
)

func TestClusterRestorePartitionInstallerInstallsOnlyTargetSlotReplicas(t *testing.T) {
	node := &fakeRestoreInstallClusterNode{nodeID: 2, snapshot: control.Snapshot{
		ClusterID: "cluster-b",
		HashSlots: control.HashSlotTable{Count: 256, Ranges: []control.HashSlotRange{{From: 0, To: 255, SlotID: 11}}},
		Slots:     []control.SlotAssignment{{SlotID: 11, DesiredPeers: []uint64{3, 2}}},
		Nodes: []control.Node{
			{NodeID: 3, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			{NodeID: 1, JoinState: control.NodeJoinStateRemoved},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			{NodeID: 4, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
		},
	}}
	local := &fakeRestorePartitionInstaller{report: backupusecase.RestorePartition{HashSlot: 7, EvidenceVersion: backupartifact.PartitionEvidenceVersion, Installed: true, PlainBytes: 99, MessageCount: 4, MaxMessageID: 19, MetadataSHA256: strings.Repeat("a", 64)}}
	remote := &fakeRemoteRestorePartitionInstaller{report: local.report}
	installer, err := backupinfra.NewClusterRestorePartitionInstaller(backupinfra.ClusterRestorePartitionInstallerOptions{
		Node: node, Local: local, Remote: remote,
	})
	require.NoError(t, err)

	report, err := installer.InstallPartition(context.Background(), backupusecase.RestorePlan{
		ID: "plan-1", TargetClusterID: "cluster-b", HashSlotCount: 256,
	}, 7)
	require.NoError(t, err)
	require.Equal(t, local.report, report)
	require.Equal(t, []uint16{7}, local.calls)
	require.Equal(t, []uint64{3}, remote.nodeIDs)
}

func TestClusterRestorePartitionInstallerRejectsDifferentNodeResults(t *testing.T) {
	node := &fakeRestoreInstallClusterNode{nodeID: 1, snapshot: control.Snapshot{
		ClusterID: "cluster-b", HashSlots: control.HashSlotTable{Count: 1, Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 1}}},
		Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2}}},
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
		},
	}}
	local := &fakeRestorePartitionInstaller{report: backupusecase.RestorePartition{HashSlot: 0, EvidenceVersion: backupartifact.PartitionEvidenceVersion, Installed: true, PlainBytes: 9, MetadataSHA256: strings.Repeat("a", 64)}}
	remote := &fakeRemoteRestorePartitionInstaller{report: backupusecase.RestorePartition{HashSlot: 0, EvidenceVersion: backupartifact.PartitionEvidenceVersion, Installed: true, PlainBytes: 10, MetadataSHA256: strings.Repeat("a", 64)}}
	installer, err := backupinfra.NewClusterRestorePartitionInstaller(backupinfra.ClusterRestorePartitionInstallerOptions{Node: node, Local: local, Remote: remote})
	require.NoError(t, err)

	_, err = installer.InstallPartition(context.Background(), backupusecase.RestorePlan{ID: "plan-1", TargetClusterID: "cluster-b", HashSlotCount: 1}, 0)
	require.ErrorContains(t, err, "node reports differ")
}

type fakeRestoreInstallClusterNode struct {
	nodeID   uint64
	snapshot control.Snapshot
}

func (f *fakeRestoreInstallClusterNode) NodeID() uint64 { return f.nodeID }
func (f *fakeRestoreInstallClusterNode) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return f.snapshot, nil
}

type fakeRestorePartitionInstaller struct {
	mu     sync.Mutex
	report backupusecase.RestorePartition
	calls  []uint16
}

func (f *fakeRestorePartitionInstaller) InstallPartition(_ context.Context, _ backupusecase.RestorePlan, hashSlot uint16) (backupusecase.RestorePartition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, hashSlot)
	return f.report, nil
}

type fakeRemoteRestorePartitionInstaller struct {
	mu      sync.Mutex
	report  backupusecase.RestorePartition
	nodeIDs []uint64
}

func (f *fakeRemoteRestorePartitionInstaller) InstallBackupRestorePartition(_ context.Context, nodeID uint64, _ backupusecase.RestorePlan, _ uint16) (backupusecase.RestorePartition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodeIDs = append(f.nodeIDs, nodeID)
	return f.report, nil
}
