//go:build e2e

package controller_leader_outage

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeBackupCompletesWhileControllerLeaderRemainsOffline(t *testing.T) {
	repositoryRoot := t.TempDir()
	backupConfig := suite.BackupSourceConfig{
		RepositoryRoot: repositoryRoot, RepositoryID: "e2e-controller-outage-repository",
		SourceGeneration: "controller-outage-source-generation", ObjectPrefix: "controller-outage-cluster",
		HashSlotCount: 64, ChannelReplicaCount: 2, MaxParallelPartitions: 1,
	}
	options := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(options,
			suite.WithNodeConfigOverrides(nodeID, suite.BackupSourceNodeConfig(backupConfig, nodeID)),
			suite.WithNodeEnv(nodeID, suite.BackupSourceNodeEnv(repositoryRoot)...),
		)
	}
	cluster := suite.New(t).StartThreeNodeCluster(options...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	nodeIDs := []uint64{1, 2, 3}
	baseline := cluster.ManagerClient(t, 1).WaitBackupRestorePointPublished(t, "", "materialized_full", 45*time.Second)
	failedNodeID := cluster.WaitControllerLeader(t, nodeIDs, 0, 15*time.Second)
	failedManager := cluster.ManagerClient(t, failedNodeID)
	cluster.WaitBackupCoordinator(t, failedNodeID, false, 10*time.Second)
	triggerCtx, cancelTrigger := context.WithTimeout(context.Background(), 5*time.Second)
	triggered, err := failedManager.TriggerBackup(triggerCtx, "incremental")
	cancelTrigger()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, "incremental", triggered.Kind)
	require.Equal(t, uint16(64), triggered.HashSlotCount)
	require.NotEmpty(t, triggered.RestorePointID)
	active := failedManager.WaitBackupActive(t, triggered.ID, 10*time.Second)
	require.Equal(t, triggered.RestorePointID, active.RestorePointID)
	require.Less(t, active.CompletedPartitions, int(triggered.HashSlotCount), "Controller Leader must stop before capture completes")

	failedNodeID = cluster.WaitControllerLeader(t, nodeIDs, 0, 10*time.Second)
	failedManager = cluster.ManagerClient(t, failedNodeID)
	active = failedManager.WaitBackupActive(t, triggered.ID, 10*time.Second)
	require.Equal(t, triggered.RestorePointID, active.RestorePointID)
	require.True(t, nodeLeadsSlot(failedManager.MustSlots(t), failedNodeID), "Controller Leader must also lead a Slot immediately before the fault")
	cluster.WaitBackupCoordinator(t, failedNodeID, true, 10*time.Second)
	require.NoError(t, cluster.MustNode(failedNodeID).Stop(), cluster.DumpDiagnostics())

	survivors := nodeIDsExcept(nodeIDs, failedNodeID)
	cluster.WaitNodesReady(t, survivors, 45*time.Second)
	newLeader := cluster.WaitControllerLeader(t, survivors, failedNodeID, 20*time.Second)
	require.NotEqual(t, failedNodeID, newLeader)
	newManager := cluster.ManagerClient(t, newLeader)
	resumed, published := newManager.WaitBackupActiveOrPublished(t, triggered.ID, triggered.RestorePointID, 10*time.Second)
	if resumed != nil {
		require.Equal(t, triggered.RestorePointID, resumed.RestorePointID)
		cluster.WaitBackupCoordinator(t, newLeader, true, 10*time.Second)
	} else {
		require.NotNil(t, published)
		require.Equal(t, triggered.RestorePointID, published.ID)
		cluster.WaitBackupCoordinator(t, newLeader, false, 10*time.Second)
	}

	incremental := newManager.WaitBackupRestorePointByID(t, triggered.RestorePointID, 60*time.Second)
	require.NotEqual(t, baseline.ID, incremental.ID)
	require.Equal(t, "incremental", incremental.Kind)
	require.True(t, incremental.PrimaryVerified)
	require.True(t, incremental.SecondaryVerified)
	verifyCtx, cancelVerify := context.WithTimeout(context.Background(), 10*time.Second)
	verification, err := newManager.VerifyBackupRestorePoint(verifyCtx, incremental.ID)
	cancelVerify()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, incremental.ID, verification.RestorePointID)
	require.True(t, verification.PrimaryVerified)
	require.True(t, verification.SecondaryVerified)
	require.Len(t, verification.ManifestSHA256, 64)
	cluster.WaitNodesReady(t, survivors, 20*time.Second)
}

func nodeLeadsSlot(slots []suite.SlotDTO, nodeID uint64) bool {
	for _, slot := range slots {
		if slot.Runtime.LeaderID == nodeID {
			return true
		}
	}
	return false
}

func nodeIDsExcept(nodeIDs []uint64, excluded uint64) []uint64 {
	result := make([]uint64, 0, len(nodeIDs)-1)
	for _, nodeID := range nodeIDs {
		if nodeID != excluded {
			result = append(result, nodeID)
		}
	}
	return result
}
