//go:build e2e

package suite

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// BackupStatusDTO is the public Manager backup status subset used by e2e scenarios.
type BackupStatusDTO struct {
	Health          string                 `json:"health"`
	FailureCategory string                 `json:"failure_category"`
	Active          *BackupJobDTO          `json:"active"`
	Latest          *BackupRestorePointDTO `json:"latest"`
}

// BackupJobDTO is the public Manager backup job subset used by e2e scenarios.
type BackupJobDTO struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	HashSlotCount       uint16 `json:"hash_slot_count"`
	RestorePointID      string `json:"restore_point_id"`
	CompletedPartitions int    `json:"completed_partitions"`
	FailureCategory     string `json:"failure_category"`
}

// BackupRestorePointDTO is the public Manager restore-point subset used by e2e scenarios.
type BackupRestorePointDTO struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	PrimaryVerified   bool   `json:"primary_verified"`
	SecondaryVerified bool   `json:"secondary_verified"`
}

// BackupVerificationDTO is the public Manager verification subset used by e2e scenarios.
type BackupVerificationDTO struct {
	RestorePointID    string `json:"restore_point_id"`
	PrimaryVerified   bool   `json:"primary_verified"`
	SecondaryVerified bool   `json:"secondary_verified"`
	ManifestSHA256    string `json:"manifest_sha256"`
}

// BackupSourceConfig describes one tagged e2e source repository policy.
type BackupSourceConfig struct {
	// RepositoryRoot is the external file-repository root shared by source nodes.
	RepositoryRoot string
	// RepositoryID is the stable repository identity.
	RepositoryID string
	// SourceGeneration identifies the source-cluster incarnation.
	SourceGeneration string
	// ObjectPrefix is the shared primary and secondary object prefix.
	ObjectPrefix string
	// HashSlotCount bounds the logical partitions in the scenario.
	HashSlotCount uint16
	// ChannelReplicaCount configures Channel placement durability when non-zero.
	ChannelReplicaCount uint16
	// MaxParallelPartitions bounds concurrent partition captures.
	MaxParallelPartitions int
}

type backupRestorePointListDTO struct {
	Items []BackupRestorePointDTO `json:"items"`
}

// BackupSourceNodeConfig returns one node's product backup configuration.
func BackupSourceNodeConfig(config BackupSourceConfig, nodeID uint64) map[string]string {
	overrides := map[string]string{
		"WK_CLUSTER_HASH_SLOT_COUNT":           strconv.FormatUint(uint64(config.HashSlotCount), 10),
		"WK_BACKUP_ENABLED":                    "true",
		"WK_BACKUP_REPOSITORY_ID":              config.RepositoryID,
		"WK_BACKUP_SOURCE_GENERATION":          config.SourceGeneration,
		"WK_BACKUP_STAGING_DIR":                filepath.Join(config.RepositoryRoot, fmt.Sprintf("source-staging-%d", nodeID)),
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
		"WK_BACKUP_MAX_PARALLEL_PARTITIONS":    strconv.Itoa(config.MaxParallelPartitions),
		"WK_BACKUP_OBJECT_LOCK_DAYS":           "7",
		"WK_BACKUP_PRIMARY_ENDPOINT":           "https://primary.e2e.invalid",
		"WK_BACKUP_PRIMARY_REGION":             "e2e-primary",
		"WK_BACKUP_PRIMARY_BUCKET":             "primary",
		"WK_BACKUP_PRIMARY_PREFIX":             config.ObjectPrefix,
		"WK_BACKUP_SECONDARY_ENDPOINT":         "https://secondary.e2e.invalid",
		"WK_BACKUP_SECONDARY_REGION":           "e2e-secondary",
		"WK_BACKUP_SECONDARY_BUCKET":           "secondary",
		"WK_BACKUP_SECONDARY_PREFIX":           config.ObjectPrefix,
	}
	if config.ChannelReplicaCount > 0 {
		overrides["WK_CLUSTER_CHANNEL_REPLICA_N"] = strconv.FormatUint(uint64(config.ChannelReplicaCount), 10)
	}
	return overrides
}

// BackupSourceNodeEnv returns the explicit tagged e2e repository environment.
func BackupSourceNodeEnv(repositoryRoot string) []string {
	return []string{
		"WUKONGIM_BACKUP_E2E_FILE_ROOT=" + repositoryRoot,
		"AWS_ACCESS_KEY_ID=e2e",
		"AWS_SECRET_ACCESS_KEY=e2e-secret",
		"AWS_EC2_METADATA_DISABLED=true",
	}
}

// TriggerBackup submits one public Manager backup request.
func (m *ManagerClient) TriggerBackup(ctx context.Context, kind string) (BackupJobDTO, error) {
	var job BackupJobDTO
	_, err := PostJSON(ctx, m.baseURL+"/manager/backups/trigger", map[string]any{"kind": kind}, &job)
	return job, err
}

// WaitBackupActive waits for one exact active job through public Manager status.
func (m *ManagerClient) WaitBackupActive(t testing.TB, jobID string, timeout time.Duration) BackupJobDTO {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last BackupStatusDTO
	var lastErr error
	for time.Now().Before(deadline) {
		var current BackupStatusDTO
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := GetJSON(ctx, m.baseURL+"/manager/backups/status", &current)
		cancel()
		last = current
		if err == nil && current.Active != nil && current.Active.ID == jobID {
			return *current.Active
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("active backup job not observed: job_id=%s active=%+v err=%v\n%s", jobID, last.Active, lastErr, m.cluster.DumpDiagnostics())
	return BackupJobDTO{}
}

// WaitBackupActiveOrPublished waits until the exact job is active or its restore point is published.
func (m *ManagerClient) WaitBackupActiveOrPublished(t testing.TB, jobID, restorePointID string, timeout time.Duration) (*BackupJobDTO, *BackupRestorePointDTO) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastStatus BackupStatusDTO
	var lastRestorePoints backupRestorePointListDTO
	var lastErr error
	for time.Now().Before(deadline) {
		var currentStatus BackupStatusDTO
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, statusErr := GetJSON(ctx, m.baseURL+"/manager/backups/status", &currentStatus)
		cancel()
		lastStatus = currentStatus
		if statusErr == nil && currentStatus.Active != nil && currentStatus.Active.ID == jobID && currentStatus.Active.RestorePointID == restorePointID {
			active := *currentStatus.Active
			return &active, nil
		}

		var currentRestorePoints backupRestorePointListDTO
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		_, restorePointsErr := GetJSON(ctx, m.baseURL+"/manager/backups/restore-points", &currentRestorePoints)
		cancel()
		lastRestorePoints = currentRestorePoints
		if restorePointsErr == nil {
			for _, point := range currentRestorePoints.Items {
				if point.ID == restorePointID {
					published := point
					return nil, &published
				}
			}
		}
		lastErr = fmt.Errorf("status: %v; restore points: %v", statusErr, restorePointsErr)
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("backup job or restore point not observed: job_id=%s restore_point_id=%s active=%+v items=%+v err=%v\n%s", jobID, restorePointID, lastStatus.Active, lastRestorePoints.Items, lastErr, m.cluster.DumpDiagnostics())
	return nil, nil
}

// WaitBackupRestorePointPublished waits for a healthy newly published restore point.
func (m *ManagerClient) WaitBackupRestorePointPublished(t testing.TB, previousID, kind string, timeout time.Duration) BackupRestorePointDTO {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last BackupStatusDTO
	var lastErr error
	for time.Now().Before(deadline) {
		var current BackupStatusDTO
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := GetJSON(ctx, m.baseURL+"/manager/backups/status", &current)
		cancel()
		last = current
		if err == nil && current.Health == "healthy" && current.Active == nil && current.Latest != nil && current.Latest.ID != previousID && current.Latest.Kind == kind {
			return *current.Latest
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("restore point not published: previous_id=%s kind=%s health=%s failure=%s active=%+v latest=%+v err=%v\n%s", previousID, kind, last.Health, last.FailureCategory, last.Active, last.Latest, lastErr, m.cluster.DumpDiagnostics())
	return BackupRestorePointDTO{}
}

// WaitBackupRestorePointByID waits for one exact restore point in public inventory.
func (m *ManagerClient) WaitBackupRestorePointByID(t testing.TB, restorePointID string, timeout time.Duration) BackupRestorePointDTO {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last backupRestorePointListDTO
	var lastErr error
	for time.Now().Before(deadline) {
		var current backupRestorePointListDTO
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := GetJSON(ctx, m.baseURL+"/manager/backups/restore-points", &current)
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
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("restore point not found: restore_point_id=%s items=%+v err=%v\n%s", restorePointID, last.Items, lastErr, m.cluster.DumpDiagnostics())
	return BackupRestorePointDTO{}
}

// VerifyBackupRestorePoint verifies one exact restore point through public Manager.
func (m *ManagerClient) VerifyBackupRestorePoint(ctx context.Context, restorePointID string) (BackupVerificationDTO, error) {
	var verification BackupVerificationDTO
	path := "/manager/backups/restore-points/" + url.PathEscape(restorePointID) + "/verify"
	_, err := PostJSON(ctx, m.baseURL+path, map[string]any{}, &verification)
	return verification, err
}

// WaitControllerLeader waits for an observed Controller Leader outside excludedNodeID.
func (c *StartedCluster) WaitControllerLeader(t testing.TB, nodeIDs []uint64, excludedNodeID uint64, timeout time.Duration) uint64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastLeader uint64
	var lastErr error
	for time.Now().Before(deadline) {
		for _, nodeID := range nodeIDs {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			status, err := c.ManagerClient(t, nodeID).ControllerRaftStatus(ctx, nodeID)
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			lastLeader = status.LeaderID
			if status.Role == "leader" && lastLeader == nodeID && lastLeader != excludedNodeID {
				return lastLeader
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("controller leader not observed: nodes=%v excluded=%d last=%d err=%v\n%s", nodeIDs, excludedNodeID, lastLeader, lastErr, c.DumpDiagnostics())
	return 0
}

// WaitBackupCoordinator waits for one node to expose the expected coordinator metrics.
func (c *StartedCluster) WaitBackupCoordinator(t testing.TB, nodeID uint64, active bool, timeout time.Duration) {
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
		leader, leaderErr := FetchMetricValue(ctx, c.MustNode(nodeID).APIAddr(), "wukongim_backup_controller_leader", nil)
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		jobActive, activeErr := FetchMetricValue(ctx, c.MustNode(nodeID).APIAddr(), "wukongim_backup_job_active", nil)
		cancel()
		lastLeader, lastActive = leader, jobActive
		if leaderErr == nil && activeErr == nil && leader == 1 && jobActive == wantActive {
			return
		}
		lastErr = fmt.Errorf("leader metric: %v; active metric: %v", leaderErr, activeErr)
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("node %d backup coordinator did not reach leader=1 active=%v: leader=%v active=%v err=%v\n%s", nodeID, active, lastLeader, lastActive, lastErr, c.DumpDiagnostics())
}

// WaitNodesReady waits for every listed node to report public readiness.
func (c *StartedCluster) WaitNodesReady(t testing.TB, nodeIDs []uint64, timeout time.Duration) {
	t.Helper()
	if c.lastReadyz == nil {
		c.lastReadyz = make(map[uint64]HTTPObservation, len(nodeIDs))
	}
	for _, nodeID := range nodeIDs {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		observation, err := waitHTTPReadyDetailed(ctx, c.MustNode(nodeID).APIAddr(), "/readyz")
		cancel()
		c.lastReadyz[nodeID] = observation
		if err != nil {
			t.Fatalf("surviving node %d did not recover readiness: %v\n%s", nodeID, err, c.DumpDiagnostics())
		}
	}
}
