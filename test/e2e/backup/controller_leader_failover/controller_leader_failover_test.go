//go:build e2e

package controller_leader_failover

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type backupStatus struct {
	Health          string        `json:"health"`
	FailureCategory string        `json:"failure_category"`
	Active          *backupJob    `json:"active"`
	Latest          *restorePoint `json:"latest"`
}

type backupJob struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	HashSlotCount       uint16 `json:"hash_slot_count"`
	RestorePointID      string `json:"restore_point_id"`
	CompletedPartitions int    `json:"completed_partitions"`
	FailureCategory     string `json:"failure_category"`
}

type restorePoint struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	PrimaryVerified   bool   `json:"primary_verified"`
	SecondaryVerified bool   `json:"secondary_verified"`
}

type restorePointList struct {
	Items []restorePoint `json:"items"`
}

type backupVerification struct {
	RestorePointID    string `json:"restore_point_id"`
	PrimaryVerified   bool   `json:"primary_verified"`
	SecondaryVerified bool   `json:"secondary_verified"`
	ManifestSHA256    string `json:"manifest_sha256"`
}

func TestThreeNodeBackupControllerLeaderFailoverResumesIncremental(t *testing.T) {
	repositoryRoot := t.TempDir()
	options := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(options,
			suite.WithNodeConfigOverrides(nodeID, sourceBackupConfig(repositoryRoot, nodeID)),
			suite.WithNodeEnv(nodeID,
				"WUKONGIM_BACKUP_E2E_FILE_ROOT="+repositoryRoot,
				"AWS_ACCESS_KEY_ID=e2e",
				"AWS_SECRET_ACCESS_KEY=e2e-secret",
				"AWS_EC2_METADATA_DISABLED=true",
			),
		)
	}
	cluster := suite.New(t).StartThreeNodeCluster(options...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	baseline := waitForPublishedRestorePointOnAnyNode(t, cluster, []uint64{1}, "", "materialized_full", 30*time.Second)
	oldLeader := waitForControllerLeader(t, cluster, []uint64{1, 2, 3}, 0, 15*time.Second)
	waitForBackupCoordinator(t, cluster, oldLeader, false, 10*time.Second)

	triggered := triggerBackupOnNode(t, cluster, oldLeader, "incremental")
	require.Equal(t, "incremental", triggered.Kind)
	require.Equal(t, uint16(64), triggered.HashSlotCount)
	require.NotEmpty(t, triggered.RestorePointID)
	oldActive := waitForActiveBackupOnAnyNode(t, cluster, []uint64{oldLeader}, triggered.ID, 10*time.Second)
	require.Equal(t, triggered.RestorePointID, oldActive.RestorePointID)
	waitForBackupCoordinator(t, cluster, oldLeader, true, 10*time.Second)

	require.NoError(t, cluster.MustNode(oldLeader).Stop(), cluster.DumpDiagnostics())
	survivors := nodeIDsExcept([]uint64{1, 2, 3}, oldLeader)
	newLeader := waitForControllerLeader(t, cluster, survivors, oldLeader, 20*time.Second)
	require.NotEqual(t, oldLeader, newLeader)
	newActive := waitForActiveBackupOnAnyNode(t, cluster, []uint64{newLeader}, triggered.ID, 10*time.Second)
	require.Equal(t, triggered.RestorePointID, newActive.RestorePointID)
	waitForBackupCoordinator(t, cluster, newLeader, true, 10*time.Second)

	require.NoError(t, cluster.StartStoppedNode(oldLeader), cluster.DumpDiagnostics())
	rejoinCtx, cancelRejoin := context.WithTimeout(context.Background(), 30*time.Second)
	require.NoError(t, cluster.WaitClusterReady(rejoinCtx), cluster.DumpDiagnostics())
	cancelRejoin()
	require.Equal(t, newLeader, waitForControllerLeader(t, cluster, survivors, oldLeader, 10*time.Second))
	rejoinedActive := waitForActiveBackupOnAnyNode(t, cluster, []uint64{1, 2, 3}, triggered.ID, 10*time.Second)
	require.Equal(t, triggered.RestorePointID, rejoinedActive.RestorePointID)

	incremental := waitForRestorePointByIDOnAnyNode(t, cluster, []uint64{1, 2, 3}, triggered.RestorePointID, 45*time.Second)
	require.NotEqual(t, baseline.ID, incremental.ID)
	require.Equal(t, "incremental", incremental.Kind)
	require.True(t, incremental.PrimaryVerified)
	require.True(t, incremental.SecondaryVerified)
	verification := verifyBackupRestorePoint(t, cluster, newLeader, incremental.ID)
	require.Equal(t, incremental.ID, verification.RestorePointID)
	require.True(t, verification.PrimaryVerified)
	require.True(t, verification.SecondaryVerified)
	require.Len(t, verification.ManifestSHA256, 64)
}

func sourceBackupConfig(root string, nodeID uint64) map[string]string {
	return map[string]string{
		"WK_CLUSTER_HASH_SLOT_COUNT":           "64",
		"WK_BACKUP_ENABLED":                    "true",
		"WK_BACKUP_REPOSITORY_ID":              "e2e-failover-repository",
		"WK_BACKUP_SOURCE_GENERATION":          "failover-source-generation",
		"WK_BACKUP_STAGING_DIR":                filepath.Join(root, fmt.Sprintf("source-staging-%d", nodeID)),
		"WK_BACKUP_KMS_KEY_ID":                 "e2e-encryption-key",
		"WK_BACKUP_SIGNING_KEY_ID":             "e2e-signing-key",
		"WK_BACKUP_GARBAGE_COLLECTOR_ROLE_ARN": "arn:aws:iam::000000000000:role/e2e-gc",
		"WK_BACKUP_KMS_REGION":                 "e2e-kms",
		"WK_BACKUP_KMS_ENDPOINT":               "https://kms.e2e.invalid",
		"WK_BACKUP_INCREMENTAL_INTERVAL":       "500ms",
		"WK_BACKUP_RESTORE_POINT_INTERVAL":     "1h",
		"WK_BACKUP_SYNTHETIC_FULL_INTERVAL":    "24h",
		"WK_BACKUP_CHUNK_SIZE_BYTES":           "1048576",
		"WK_BACKUP_STAGING_MAX_BYTES":          "8388608",
		"WK_BACKUP_MAX_PARALLEL_PARTITIONS":    "1",
		"WK_BACKUP_OBJECT_LOCK_DAYS":           "7",
		"WK_BACKUP_PRIMARY_ENDPOINT":           "https://primary.e2e.invalid",
		"WK_BACKUP_PRIMARY_REGION":             "e2e-primary",
		"WK_BACKUP_PRIMARY_BUCKET":             "primary",
		"WK_BACKUP_PRIMARY_PREFIX":             "failover-cluster",
		"WK_BACKUP_SECONDARY_ENDPOINT":         "https://secondary.e2e.invalid",
		"WK_BACKUP_SECONDARY_REGION":           "e2e-secondary",
		"WK_BACKUP_SECONDARY_BUCKET":           "secondary",
		"WK_BACKUP_SECONDARY_PREFIX":           "failover-cluster",
	}
}

func waitForControllerLeader(t *testing.T, cluster *suite.StartedCluster, nodeIDs []uint64, previousLeader uint64, timeout time.Duration) uint64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastLeader uint64
	var lastErr error
	for time.Now().Before(deadline) {
		for _, nodeID := range nodeIDs {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			status, err := cluster.ManagerClient(t, nodeID).ControllerRaftStatus(ctx, nodeID)
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			lastLeader = status.LeaderID
			if status.Role == "leader" && lastLeader == nodeID && lastLeader != previousLeader {
				return lastLeader
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("controller leader did not change: nodes=%v previous=%d last=%d err=%v\n%s", nodeIDs, previousLeader, lastLeader, lastErr, cluster.DumpDiagnostics())
	return 0
}

func waitForBackupCoordinator(t *testing.T, cluster *suite.StartedCluster, nodeID uint64, active bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	wantActive := 0.0
	if active {
		wantActive = 1
	}
	var lastLeader, lastActive float64
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		leader, leaderErr := suite.FetchMetricValue(ctx, cluster.MustNode(nodeID).APIAddr(), "wukongim_backup_controller_leader", nil)
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		jobActive, activeErr := suite.FetchMetricValue(ctx, cluster.MustNode(nodeID).APIAddr(), "wukongim_backup_job_active", nil)
		cancel()
		lastLeader, lastActive = leader, jobActive
		if leaderErr == nil && activeErr == nil && leader == 1 && jobActive == wantActive {
			return
		}
		lastErr = fmt.Errorf("leader metric: %v; active metric: %v", leaderErr, activeErr)
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("node %d backup coordinator did not reach leader=1 active=%v: leader=%v active=%v err=%v\n%s", nodeID, active, lastLeader, lastActive, lastErr, cluster.DumpDiagnostics())
}

func triggerBackupOnNode(t *testing.T, cluster *suite.StartedCluster, nodeID uint64, kind string) backupJob {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var job backupJob
	_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(nodeID).ManagerAddr()+"/manager/backups/trigger", map[string]any{"kind": kind}, &job)
	require.NoError(t, err, cluster.DumpDiagnostics())
	return job
}

func waitForActiveBackupOnAnyNode(t *testing.T, cluster *suite.StartedCluster, nodeIDs []uint64, jobID string, timeout time.Duration) backupJob {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last backupStatus
	var lastErr error
	for time.Now().Before(deadline) {
		for _, nodeID := range nodeIDs {
			var current backupStatus
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := suite.GetJSON(ctx, "http://"+cluster.MustNode(nodeID).ManagerAddr()+"/manager/backups/status", &current)
			cancel()
			last = current
			if err == nil && current.Active != nil && current.Active.ID == jobID {
				return *current.Active
			}
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("active backup job not observed: nodes=%v job_id=%s active=%+v err=%v\n%s", nodeIDs, jobID, last.Active, lastErr, cluster.DumpDiagnostics())
	return backupJob{}
}

func waitForPublishedRestorePointOnAnyNode(t *testing.T, cluster *suite.StartedCluster, nodeIDs []uint64, previousID, kind string, timeout time.Duration) restorePoint {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last backupStatus
	var lastErr error
	for time.Now().Before(deadline) {
		for _, nodeID := range nodeIDs {
			var current backupStatus
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := suite.GetJSON(ctx, "http://"+cluster.MustNode(nodeID).ManagerAddr()+"/manager/backups/status", &current)
			cancel()
			last = current
			if err == nil && current.Health == "healthy" && current.Active == nil && current.Latest != nil && current.Latest.ID != previousID && current.Latest.Kind == kind {
				return *current.Latest
			}
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("restore point not published: nodes=%v previous_id=%s kind=%s health=%s failure=%s active=%+v latest=%+v err=%v\n%s", nodeIDs, previousID, kind, last.Health, last.FailureCategory, last.Active, last.Latest, lastErr, cluster.DumpDiagnostics())
	return restorePoint{}
}

func waitForRestorePointByIDOnAnyNode(t *testing.T, cluster *suite.StartedCluster, nodeIDs []uint64, restorePointID string, timeout time.Duration) restorePoint {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last restorePointList
	var lastErr error
	for time.Now().Before(deadline) {
		for _, nodeID := range nodeIDs {
			var current restorePointList
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := suite.GetJSON(ctx, "http://"+cluster.MustNode(nodeID).ManagerAddr()+"/manager/backups/restore-points", &current)
			cancel()
			last = current
			if err == nil {
				for _, point := range current.Items {
					if point.ID == restorePointID {
						return point
					}
				}
			}
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("restore point not found: nodes=%v restore_point_id=%s items=%+v err=%v\n%s", nodeIDs, restorePointID, last.Items, lastErr, cluster.DumpDiagnostics())
	return restorePoint{}
}

func verifyBackupRestorePoint(t *testing.T, cluster *suite.StartedCluster, nodeID uint64, restorePointID string) backupVerification {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var verification backupVerification
	_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(nodeID).ManagerAddr()+"/manager/backups/restore-points/"+restorePointID+"/verify", map[string]any{}, &verification)
	require.NoError(t, err, cluster.DumpDiagnostics())
	return verification
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
