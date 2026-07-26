package backup_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/stretchr/testify/require"
)

func TestClusterRestorePartitionInstallerDownloadsOnlyOnTargetSlotLeader(t *testing.T) {
	node := &fakeRestoreInstallClusterNode{nodeID: 2, snapshot: control.Snapshot{
		ClusterID: "cluster-b",
		HashSlots: control.HashSlotTable{Count: 256, Ranges: []control.HashSlotRange{{From: 0, To: 255, SlotID: 11}}},
		Slots: []control.SlotAssignment{{
			SlotID: 11, DesiredPeers: []uint64{3, 2}, ConfigEpoch: 1,
		}},
		Nodes: []control.Node{
			{NodeID: 3, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			{NodeID: 1, JoinState: control.NodeJoinStateRemoved},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			{NodeID: 4, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
		},
	}, route: clusterpkg.Route{HashSlot: 7, SlotID: 11, Leader: 3, LeaderTerm: 9, ConfigEpoch: 1, Peers: []uint64{2, 3}}}
	local := &fakeRestorePartitionInstaller{report: backupusecase.RestorePartition{HashSlot: 7, EvidenceVersion: backupartifact.RestoreEvidenceVersion, Installed: true, PlainBytes: 99, MessageCount: 4, MaxMessageID: 19, MetadataSHA256: strings.Repeat("a", 64)}}
	remote := &fakeRemoteRestorePartitionInstaller{report: local.report}
	installer, err := backupinfra.NewClusterRestorePartitionInstaller(backupinfra.ClusterRestorePartitionInstallerOptions{
		Node: node, Local: local, Remote: remote,
	})
	require.NoError(t, err)

	plan := routerRestorePlan(
		256, 7, backupusecase.RestorePartitionAssignment{
			HashSlot: 7, TargetSlotID: 11, LeaderNodeID: 3,
			LeaderTerm: 9, ConfigEpoch: 1, ReplicaCount: 2,
		},
	)
	report, err := installer.InstallPartition(
		context.Background(), plan, 7,
	)
	require.NoError(t, err)
	require.Equal(t, uint64(3), report.LeaderNodeID)
	require.Equal(t, uint32(2), report.ReplicaCount)
	require.Empty(t, local.calls)
	require.Equal(t, []uint64{3}, remote.nodeIDs)
}

func TestClusterRestorePartitionInstallerRejectsLeaderOutsideDesiredReplicas(t *testing.T) {
	node := &fakeRestoreInstallClusterNode{nodeID: 1, snapshot: control.Snapshot{
		ClusterID: "cluster-b", HashSlots: control.HashSlotTable{Count: 1, Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 1}}},
		Slots: []control.SlotAssignment{{
			SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1,
		}},
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
		},
	}, route: clusterpkg.Route{HashSlot: 0, SlotID: 1, Leader: 3, LeaderTerm: 9, ConfigEpoch: 1, Peers: []uint64{1, 3}}}
	local := &fakeRestorePartitionInstaller{report: backupusecase.RestorePartition{HashSlot: 0, EvidenceVersion: backupartifact.RestoreEvidenceVersion, Installed: true, PlainBytes: 9, MetadataSHA256: strings.Repeat("a", 64)}}
	remote := &fakeRemoteRestorePartitionInstaller{report: backupusecase.RestorePartition{HashSlot: 0, EvidenceVersion: backupartifact.RestoreEvidenceVersion, Installed: true, PlainBytes: 10, MetadataSHA256: strings.Repeat("a", 64)}}
	installer, err := backupinfra.NewClusterRestorePartitionInstaller(backupinfra.ClusterRestorePartitionInstallerOptions{Node: node, Local: local, Remote: remote})
	require.NoError(t, err)

	_, err = installer.InstallPartition(context.Background(), backupusecase.RestorePlan{ID: "plan-1", TargetClusterID: "cluster-b", HashSlotCount: 1}, 0)
	require.ErrorContains(t, err, "Leader fence is invalid")
}

func routerRestorePlan(
	hashSlotCount uint16,
	hashSlot uint16,
	assignment backupusecase.RestorePartitionAssignment,
) backupusecase.RestorePlan {
	partitions := make([]backupusecase.RestorePartition, hashSlotCount)
	for index := range partitions {
		partitions[index] = backupusecase.RestorePartition{
			HashSlot: uint16(index),
		}
	}
	partitions[hashSlot] = backupusecase.RestorePartition{
		HashSlot: hashSlot, TargetSlotID: assignment.TargetSlotID,
		LeaderNodeID: assignment.LeaderNodeID,
		LeaderTerm:   assignment.LeaderTerm,
		ConfigEpoch:  assignment.ConfigEpoch,
		ReplicaCount: assignment.ReplicaCount,
	}
	return backupusecase.RestorePlan{
		ID: "plan-1", TargetClusterID: "cluster-b",
		HashSlotCount: hashSlotCount, Partitions: partitions,
	}
}

func TestClusterRestorePartitionInstallerRejectsMixedControlEpochs(t *testing.T) {
	node := &fakeRestoreInstallClusterNode{nodeID: 1, snapshot: control.Snapshot{
		ClusterID: "cluster-b",
		HashSlots: control.HashSlotTable{
			Count: 1, Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 1}},
		},
		Slots: []control.SlotAssignment{{
			SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 4,
		}},
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
		},
	}, route: clusterpkg.Route{
		HashSlot: 0, SlotID: 1, Leader: 1, LeaderTerm: 9, ConfigEpoch: 5,
		Peers: []uint64{1, 2},
	}}
	installer, err := backupinfra.NewClusterRestorePartitionInstaller(
		backupinfra.ClusterRestorePartitionInstallerOptions{
			Node: node, Local: &fakeRestorePartitionInstaller{},
			Remote: &fakeRemoteRestorePartitionInstaller{},
		},
	)
	require.NoError(t, err)
	_, err = installer.Assignment(
		context.Background(),
		backupusecase.RestorePlan{
			TargetClusterID: "cluster-b", HashSlotCount: 1,
		},
		0,
	)
	require.ErrorContains(t, err, "Leader fence is invalid")
}

func TestClusterRestorePartitionInstallerRejectsWrongCurrentPeerSet(
	t *testing.T,
) {
	node := &fakeRestoreInstallClusterNode{
		nodeID: 1,
		snapshot: control.Snapshot{
			ClusterID: "cluster-b",
			HashSlots: control.HashSlotTable{
				Count: 1,
				Ranges: []control.HashSlotRange{{
					From: 0, To: 0, SlotID: 1,
				}},
			},
			Slots: []control.SlotAssignment{{
				SlotID: 1, DesiredPeers: []uint64{1, 2},
				ConfigEpoch: 4,
			}},
			Nodes: []control.Node{
				{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 3, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			},
		},
		route: clusterpkg.Route{
			HashSlot: 0, SlotID: 1, Leader: 1,
			LeaderTerm: 9, ConfigEpoch: 4,
			Peers: []uint64{1, 3},
		},
	}
	installer, err := backupinfra.NewClusterRestorePartitionInstaller(
		backupinfra.ClusterRestorePartitionInstallerOptions{
			Node: node, Local: &fakeRestorePartitionInstaller{},
			Remote: &fakeRemoteRestorePartitionInstaller{},
		},
	)
	require.NoError(t, err)
	_, err = installer.Assignment(
		context.Background(),
		backupusecase.RestorePlan{
			TargetClusterID: "cluster-b", HashSlotCount: 1,
		},
		0,
	)
	require.ErrorContains(t, err, "Leader fence is invalid")
}

type fakeRestoreInstallClusterNode struct {
	nodeID   uint64
	snapshot control.Snapshot
	route    clusterpkg.Route
}

func (f *fakeRestoreInstallClusterNode) NodeID() uint64 { return f.nodeID }
func (f *fakeRestoreInstallClusterNode) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return f.snapshot, nil
}
func (f *fakeRestoreInstallClusterNode) RouteHashSlot(hashSlot uint16) (clusterpkg.Route, error) {
	f.route.HashSlot = hashSlot
	return f.route, nil
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
