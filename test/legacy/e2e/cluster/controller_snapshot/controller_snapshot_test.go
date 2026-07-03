//go:build e2e && legacy_e2e

package controller_snapshot

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/legacy/e2e/suite"
	"github.com/stretchr/testify/require"
)

const (
	controllerSnapshotEnabledEnv      = "WK_E2E_CONTROLLER_SNAPSHOT"
	controllerSnapshotRowsEnv         = "WK_E2E_CONTROLLER_SNAPSHOT_ROWS"
	controllerSnapshotPayloadBytesEnv = "WK_E2E_CONTROLLER_SNAPSHOT_PAYLOAD_BYTES"
	controllerSnapshotBatchRowsEnv    = "WK_E2E_CONTROLLER_SNAPSHOT_BATCH_ROWS"
)

func TestControllerSnapshotNodeOverridesEnableTestModeAndFastCompaction(t *testing.T) {
	overrides := controllerSnapshotNodeOverrides()
	require.Equal(t, "true", overrides["WK_TEST_MODE"])
	require.Equal(t, "true", overrides["WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED"])
	require.Equal(t, "1", overrides["WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES"])
	require.Equal(t, "1ms", overrides["WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL"])
}

func TestControllerSnapshotJobBatchesSplitRows(t *testing.T) {
	batches := controllerSnapshotJobBatches(205, 100)
	require.Equal(t, []controllerSnapshotJobBatch{
		{Index: 0, Count: 100},
		{Index: 1, Count: 100},
		{Index: 2, Count: 5},
	}, batches)
}

func TestControllerFollowerRestoresLargeSnapshot(t *testing.T) {
	if os.Getenv(controllerSnapshotEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run the large Controller snapshot restore e2e", controllerSnapshotEnabledEnv)
	}

	rows := e2eEnvInt(t, controllerSnapshotRowsEnv, 700)
	payloadBytes := e2eEnvInt(t, controllerSnapshotPayloadBytesEnv, 128*1024)
	batchRows := e2eEnvInt(t, controllerSnapshotBatchRowsEnv, 50)
	require.Positive(t, batchRows, "invalid %s", controllerSnapshotBatchRowsEnv)
	require.Greater(t, int64(rows)*int64(payloadBytes), int64(64<<20), "increase %s or %s so the Controller snapshot exceeds the transport frame limit", controllerSnapshotRowsEnv, controllerSnapshotPayloadBytesEnv)

	s := suite.New(t)
	overrides := controllerSnapshotNodeOverrides()
	cluster := s.StartThreeNodeCluster(
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	observer := cluster.MustNode(1)
	leaderStatus, body, err := suite.WaitForControllerRaftLeader(ctx, *observer, []uint64{1, 2, 3})
	require.NoError(t, err, controllerSnapshotDiagnostics(cluster, body, nil, nil))
	require.Equal(t, "leader", leaderStatus.Role)

	leader := cluster.MustNode(leaderStatus.NodeID)
	laggingID := firstNonLeaderNodeID(cluster, leaderStatus.NodeID)
	require.NotZero(t, laggingID)
	lagging := cluster.MustNode(laggingID)
	laggingBefore, body, err := suite.FetchControllerRaftStatus(ctx, *observer, laggingID)
	require.NoError(t, err, controllerSnapshotDiagnostics(cluster, body, nil, nil))
	require.NoError(t, lagging.Stop(), cluster.DumpDiagnostics())

	t.Logf("generating Controller snapshot seed jobs: rows=%d payload_bytes=%d batch_rows=%d leader=%d lagging=%d", rows, payloadBytes, batchRows, leaderStatus.NodeID, laggingID)
	generatedRows := 0
	var generated suite.ControllerSnapshotJobsResponse
	for _, batch := range controllerSnapshotJobBatches(rows, batchRows) {
		generated, body, err = suite.GenerateControllerSnapshotJobs(ctx, *leader, suite.ControllerSnapshotJobsRequest{
			Prefix:       fmt.Sprintf("controller-snapshot-b%03d", batch.Index),
			TargetNodeID: leaderStatus.NodeID,
			Count:        batch.Count,
			PayloadBytes: payloadBytes,
			Seed:         fmt.Sprintf("large-controller-snapshot-%03d", batch.Index),
		})
		require.NoError(t, err, controllerSnapshotDiagnostics(cluster, body, nil, nil))
		require.Equal(t, batch.Count, generated.Count)
		require.Equal(t, payloadBytes, generated.PayloadBytes)
		generatedRows += generated.Count
	}
	require.Equal(t, rows, generatedRows)
	require.NotEmpty(t, generated.LastJobID)

	compacted, body, err := suite.CompactControllerRaftLog(ctx, *leader, leaderStatus.NodeID)
	require.NoError(t, err, controllerSnapshotDiagnostics(cluster, body, &compacted, nil))
	requireControllerCompactionCoversLag(t, compacted, leaderStatus.NodeID, laggingBefore.AppliedIndex)

	s.RestartNode(lagging)
	require.NoError(t, suite.WaitNodeReady(ctx, *lagging), cluster.DumpDiagnostics())

	var restored suite.ManagerControllerRaftStatus
	restored, body, err = suite.WaitForControllerRaftStatus(ctx, *lagging, laggingID, func(status suite.ManagerControllerRaftStatus) bool {
		return status.Role == "follower" &&
			status.Health == "healthy" &&
			status.Restore.LastSnapshotIndex >= compacted.Items[0].AfterSnapshotIndex &&
			status.AppliedIndex >= compacted.Items[0].AfterSnapshotIndex
	})
	require.NoError(t, err, controllerSnapshotDiagnostics(cluster, body, &compacted, &restored))
	require.Greater(t, restored.Restore.LastSnapshotIndex, laggingBefore.Restore.LastSnapshotIndex)
}

func controllerSnapshotNodeOverrides() map[string]string {
	return map[string]string{
		"WK_TEST_MODE": "true",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED":         "true",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES": "1",
		"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL":  "1ms",
		"WK_CLUSTER_CONTROLLER_REQUEST_TIMEOUT":                "30s",
		"WK_CLUSTER_CONTROLLER_LEADER_WAIT_TIMEOUT":            "30s",
		"WK_CLUSTER_MANAGED_SLOT_CATCH_UP_TIMEOUT":             "2m",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES":       "10000",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL":        "30s",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_ENABLED":               "true",
	}
}

// controllerSnapshotJobBatch describes one bounded test-data API call.
type controllerSnapshotJobBatch struct {
	Index int
	Count int
}

func controllerSnapshotJobBatches(rows, batchRows int) []controllerSnapshotJobBatch {
	if batchRows <= 0 {
		panic("controller snapshot batch rows must be positive")
	}
	if rows <= 0 {
		return nil
	}
	batches := make([]controllerSnapshotJobBatch, 0, (rows+batchRows-1)/batchRows)
	for remaining, index := rows, 0; remaining > 0; index++ {
		count := batchRows
		if remaining < count {
			count = remaining
		}
		batches = append(batches, controllerSnapshotJobBatch{Index: index, Count: count})
		remaining -= count
	}
	return batches
}

func e2eEnvInt(t *testing.T, key string, fallback int) int {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	require.NoError(t, err, "invalid %s", key)
	return value
}

func firstNonLeaderNodeID(cluster *suite.StartedCluster, leaderID uint64) uint64 {
	for _, node := range cluster.Nodes {
		if node.Spec.ID != leaderID {
			return node.Spec.ID
		}
	}
	return 0
}

func requireControllerCompactionCoversLag(t *testing.T, resp suite.ManagerControllerRaftCompactionResponse, nodeID uint64, laggingApplied uint64) {
	t.Helper()
	require.Equal(t, 1, resp.Total)
	require.Equal(t, 1, resp.Succeeded)
	require.Len(t, resp.Items, 1)
	item := resp.Items[0]
	require.Equal(t, nodeID, item.NodeID)
	require.True(t, item.Success, item.Error)
	require.True(t, item.Compacted || item.SkippedReason == "up_to_date", "compaction item = %+v", item)
	require.Greater(t, item.AfterSnapshotIndex, laggingApplied)
}

func controllerSnapshotDiagnostics(cluster *suite.StartedCluster, body []byte, compaction *suite.ManagerControllerRaftCompactionResponse, status *suite.ManagerControllerRaftStatus) string {
	compactionBody := ""
	if compaction != nil {
		compactionBody = fmt.Sprintf("\ncontroller compaction: total=%d succeeded=%d failed=%d items=%+v", compaction.Total, compaction.Succeeded, compaction.Failed, compaction.Items)
	}
	statusBody := ""
	if status != nil {
		statusBody = fmt.Sprintf("\ncontroller status: node=%d role=%s health=%s applied=%d restore=%+v", status.NodeID, status.Role, status.Health, status.AppliedIndex, status.Restore)
	}
	return fmt.Sprintf("%s\ncontroller snapshot body: %s%s%s", cluster.DumpDiagnostics(), string(body), compactionBody, statusBody)
}
