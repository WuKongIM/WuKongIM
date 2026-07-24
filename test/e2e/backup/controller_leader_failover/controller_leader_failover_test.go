//go:build e2e

package controller_leader_failover

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeBackupControllerLeaderFailoverResumesIncremental(t *testing.T) {
	repositoryRoot := t.TempDir()
	backupConfig := suite.BackupSourceConfig{
		RepositoryRoot: repositoryRoot, RepositoryID: "e2e-failover-repository",
		SourceGeneration: "failover-source-generation", ObjectPrefix: "failover-cluster",
		HashSlotCount: 64, MaxParallelPartitions: 1,
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

	baseline := cluster.ManagerClient(t, 1).WaitBackupRestorePointPublished(t, "", "materialized_full", suite.BackupPublicationTimeout)
	oldLeader := cluster.WaitControllerLeader(t, []uint64{1, 2, 3}, 0, 15*time.Second)
	cluster.WaitBackupCoordinator(t, oldLeader, false, 10*time.Second)
	oldManager := cluster.ManagerClient(t, oldLeader)
	triggerCtx, cancelTrigger := context.WithTimeout(context.Background(), 5*time.Second)
	triggered, err := oldManager.TriggerBackup(triggerCtx, "incremental")
	cancelTrigger()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, "incremental", triggered.Kind)
	require.Equal(t, uint16(64), triggered.HashSlotCount)
	require.NotEmpty(t, triggered.RestorePointID)
	oldActive := oldManager.WaitBackupActive(t, triggered.ID, 10*time.Second)
	require.Equal(t, triggered.RestorePointID, oldActive.RestorePointID)
	cluster.WaitBackupCoordinator(t, oldLeader, true, 10*time.Second)

	require.NoError(t, cluster.MustNode(oldLeader).Stop(), cluster.DumpDiagnostics())
	survivors := nodeIDsExcept([]uint64{1, 2, 3}, oldLeader)
	newLeader := cluster.WaitControllerLeader(t, survivors, oldLeader, 20*time.Second)
	require.NotEqual(t, oldLeader, newLeader)
	newManager := cluster.ManagerClient(t, newLeader)
	newActive := newManager.WaitBackupActive(t, triggered.ID, 10*time.Second)
	require.Equal(t, triggered.RestorePointID, newActive.RestorePointID)
	cluster.WaitBackupCoordinator(t, newLeader, true, 10*time.Second)

	require.NoError(t, cluster.StartStoppedNode(oldLeader), cluster.DumpDiagnostics())
	rejoinCtx, cancelRejoin := context.WithTimeout(context.Background(), suite.BackupClusterReadyTimeout)
	require.NoError(t, cluster.WaitClusterReady(rejoinCtx), cluster.DumpDiagnostics())
	cancelRejoin()
	require.Equal(t, newLeader, cluster.WaitControllerLeader(t, survivors, oldLeader, 10*time.Second))
	rejoinedActive, rejoinedPublished := newManager.WaitBackupActiveOrPublished(t, triggered.ID, triggered.RestorePointID, 10*time.Second)
	if rejoinedActive != nil {
		require.Equal(t, triggered.RestorePointID, rejoinedActive.RestorePointID)
	} else {
		require.NotNil(t, rejoinedPublished)
		require.Equal(t, triggered.RestorePointID, rejoinedPublished.ID)
	}

	incremental := newManager.WaitBackupRestorePointByID(t, triggered.RestorePointID, 45*time.Second)
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
