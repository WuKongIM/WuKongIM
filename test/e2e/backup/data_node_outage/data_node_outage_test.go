//go:build e2e

package data_node_outage

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeBackupCompletesWhileDataNodeRemainsOffline(t *testing.T) {
	repositoryRoot := t.TempDir()
	backupConfig := suite.BackupSourceConfig{
		RepositoryRoot: repositoryRoot, RepositoryID: "e2e-data-outage-repository",
		SourceGeneration: "data-outage-source-generation", ObjectPrefix: "data-outage-cluster",
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
	ctx, cancel := context.WithTimeout(context.Background(), suite.BackupClusterReadyTimeout)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	nodeIDs := []uint64{1, 2, 3}
	controllerLeader := cluster.WaitControllerLeader(t, nodeIDs, 0, 15*time.Second)
	manager := cluster.ManagerClient(t, controllerLeader)
	baseline := manager.WaitBackupRestorePointPublished(t, "", "materialized_full", suite.BackupPublicationTimeout)
	triggerCtx, cancelTrigger := context.WithTimeout(context.Background(), 5*time.Second)
	triggered, err := manager.TriggerBackup(triggerCtx, "incremental")
	cancelTrigger()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, "incremental", triggered.Kind)
	require.Equal(t, uint16(64), triggered.HashSlotCount)
	require.NotEmpty(t, triggered.RestorePointID)
	active := manager.WaitBackupActive(t, triggered.ID, 10*time.Second)
	require.Equal(t, triggered.RestorePointID, active.RestorePointID)
	require.Less(t, active.CompletedPartitions, int(triggered.HashSlotCount), "data node must stop before capture completes")

	controllerLeader = cluster.WaitControllerLeader(t, nodeIDs, 0, 10*time.Second)
	failedNodeID := firstSlotLeaderExcept(cluster.ManagerClient(t, controllerLeader).MustSlots(t), controllerLeader)
	require.NotZero(t, failedNodeID, "a non-Controller-Leader node must lead at least one Slot immediately before the fault")
	require.NoError(t, cluster.MustNode(failedNodeID).Stop(), cluster.DumpDiagnostics())
	survivors := nodeIDsExcept(nodeIDs, failedNodeID)
	cluster.WaitNodesReady(t, survivors, 20*time.Second)
	controllerLeader = cluster.WaitControllerLeader(t, survivors, failedNodeID, 15*time.Second)
	manager = cluster.ManagerClient(t, controllerLeader)
	resumed := manager.WaitBackupActive(t, triggered.ID, 10*time.Second)
	require.Equal(t, triggered.RestorePointID, resumed.RestorePointID)

	incremental := manager.WaitBackupRestorePointByID(t, triggered.RestorePointID, 60*time.Second)
	require.NotEqual(t, baseline.ID, incremental.ID)
	require.Equal(t, "incremental", incremental.Kind)
	require.True(t, incremental.PrimaryVerified)
	require.True(t, incremental.SecondaryVerified)
	verifyCtx, cancelVerify := context.WithTimeout(context.Background(), 10*time.Second)
	verification, err := manager.VerifyBackupRestorePoint(verifyCtx, incremental.ID)
	cancelVerify()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, incremental.ID, verification.RestorePointID)
	require.True(t, verification.PrimaryVerified)
	require.True(t, verification.SecondaryVerified)
	require.Len(t, verification.ManifestSHA256, 64)
	cluster.WaitNodesReady(t, survivors, 20*time.Second)
}

func firstSlotLeaderExcept(slots []suite.SlotDTO, excluded uint64) uint64 {
	for _, slot := range slots {
		if slot.Runtime.LeaderID != 0 && slot.Runtime.LeaderID != excluded {
			return slot.Runtime.LeaderID
		}
	}
	return 0
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
