//go:build e2e && legacy_e2e

package dynamic_node_join

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
	largeSnapshotEnabledEnv      = "WK_E2E_LARGE_SNAPSHOT"
	largeSnapshotRowsEnv         = "WK_E2E_SNAPSHOT_ROWS"
	largeSnapshotPayloadBytesEnv = "WK_E2E_SNAPSHOT_PAYLOAD_BYTES"
	largeSnapshotBatchRowsEnv    = "WK_E2E_SNAPSHOT_BATCH_ROWS"
)

func TestLargeSnapshotNodeOverridesDoNotDelayStartupElection(t *testing.T) {
	overrides := largeSnapshotNodeOverrides("join-secret")
	require.NotContains(t, overrides, "WK_CLUSTER_ELECTION_TICK")
	require.Equal(t, "join-secret", overrides["WK_CLUSTER_JOIN_TOKEN"])
	require.Equal(t, "true", overrides["WK_TEST_MODE"])
	require.Equal(t, "2m", overrides["WK_CLUSTER_MANAGED_SLOT_CATCH_UP_TIMEOUT"])
}

func TestLargeSnapshotUserBatchesSplitRows(t *testing.T) {
	batches := largeSnapshotUserBatches(205, 100)
	require.Equal(t, []largeSnapshotUserBatch{
		{Index: 0, Count: 100},
		{Index: 1, Count: 100},
		{Index: 2, Count: 5},
	}, batches)
}

func TestLargeSnapshotUserBatchesRejectsInvalidBatchSize(t *testing.T) {
	require.Panics(t, func() { largeSnapshotUserBatches(10, 0) })
}

func TestDynamicNodeJoinLargeSlotSnapshotOnboarding(t *testing.T) {
	if os.Getenv(largeSnapshotEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run the large Slot snapshot onboarding e2e", largeSnapshotEnabledEnv)
	}

	const joinToken = "join-secret"
	rows := e2eEnvInt(t, largeSnapshotRowsEnv, 1200)
	payloadBytes := e2eEnvInt(t, largeSnapshotPayloadBytesEnv, 64*1024)
	batchRows := e2eEnvInt(t, largeSnapshotBatchRowsEnv, 100)
	require.Positive(t, batchRows, "invalid %s", largeSnapshotBatchRowsEnv)
	require.Greater(t, int64(rows)*int64(payloadBytes), int64(64<<20), "increase %s or %s so the Slot snapshot exceeds the transport frame limit", largeSnapshotRowsEnv, largeSnapshotPayloadBytesEnv)

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithNodeConfigOverrides(1, largeSnapshotNodeOverrides(joinToken)),
		suite.WithNodeConfigOverrides(2, largeSnapshotNodeOverrides(joinToken)),
		suite.WithNodeConfigOverrides(3, largeSnapshotNodeOverrides(joinToken)),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.ResolveSlotTopology(ctx, 1)
	require.NoError(t, err, cluster.DumpDiagnostics())

	observer := cluster.MustNode(1)
	t.Logf("generating Slot snapshot seed data: rows=%d payload_bytes=%d batch_rows=%d", rows, payloadBytes, batchRows)
	generatedRows := 0
	for _, batch := range largeSnapshotUserBatches(rows, batchRows) {
		generated, body, err := suite.GenerateSlotSnapshotUsers(ctx, *observer, suite.SlotSnapshotUsersRequest{
			Prefix:       fmt.Sprintf("large-snapshot-user-b%03d", batch.Index),
			Count:        batch.Count,
			PayloadBytes: payloadBytes,
			Seed:         fmt.Sprintf("large-slot-snapshot-%03d", batch.Index),
			DeviceFlag:   1,
			DeviceLevel:  1,
		})
		require.NoError(t, err, largeSnapshotDiagnostics(cluster, body, nil))
		require.Equal(t, batch.Count, generated.Count)
		require.Equal(t, payloadBytes, generated.PayloadBytes)
		generatedRows += generated.Count
		_, err = cluster.ResolveSlotTopology(ctx, 1)
		require.NoError(t, err, cluster.DumpDiagnostics())
	}
	require.Equal(t, rows, generatedRows)

	topology, err := cluster.ResolveSlotTopology(ctx, 1)
	require.NoError(t, err, cluster.DumpDiagnostics())
	t.Logf("compacting Slot 1 on peers=%v", slotPeerIDs(topology))
	for _, nodeID := range slotPeerIDs(topology) {
		compacted, body, err := suite.CompactSlotRaftLog(ctx, *observer, nodeID, 1)
		require.NoError(t, err, largeSnapshotDiagnostics(cluster, body, &compacted))
		requireSlotCompactionCoversApplied(t, compacted, nodeID)
	}

	joined := s.StartDynamicJoinNode(cluster, 4, joinToken)
	require.NoError(t, suite.WaitNodeReady(ctx, *joined), cluster.DumpDiagnostics())

	joinedBefore, body, err := suite.WaitForManagerNode(ctx, *observer, joined.Spec.ID, func(node suite.ManagerNode) bool {
		return node.Status == "alive" && node.Addr == joined.Spec.ClusterAddr && node.Controller.Role == "none" && node.SlotStats.Count == 0
	})
	require.NoError(t, err, onboardingDiagnostics(cluster, body, nil))
	require.Zero(t, joinedBefore.SlotStats.Count)

	planned, body, err := suite.CreateNodeOnboardingPlan(ctx, *observer, joined.Spec.ID)
	require.NoError(t, err, onboardingDiagnostics(cluster, body, nil))
	require.NotEmpty(t, planned.Plan.Moves, onboardingDiagnostics(cluster, body, &planned))

	running, body, err := suite.StartNodeOnboardingJob(ctx, *observer, planned.JobID)
	require.NoError(t, err, onboardingDiagnostics(cluster, body, &planned))
	require.Equal(t, "running", running.Status)
	t.Logf("waiting for onboarding job %s after large snapshot compaction", planned.JobID)

	completed, body, err := suite.WaitForNodeOnboardingJob(ctx, *observer, planned.JobID, func(job suite.ManagerNodeOnboardingJob) bool {
		return job.Status == "completed"
	})
	require.NoError(t, err, onboardingDiagnostics(cluster, body, &running))
	require.Empty(t, completed.LastError)
	require.Positive(t, completed.ResultCounts.Completed, onboardingDiagnostics(cluster, body, &completed))

	joinedAfter, body, err := suite.WaitForManagerNode(ctx, *observer, joined.Spec.ID, func(node suite.ManagerNode) bool {
		return node.Status == "alive" && node.SlotStats.Count > 0
	})
	require.NoError(t, err, onboardingDiagnostics(cluster, body, &completed))
	require.Positive(t, joinedAfter.SlotStats.Count)
}

func largeSnapshotNodeOverrides(joinToken string) map[string]string {
	return map[string]string{
		"WK_CLUSTER_JOIN_TOKEN":                    joinToken,
		"WK_CLUSTER_MANAGED_SLOT_CATCH_UP_TIMEOUT": "2m",
		"WK_TEST_MODE":                             "true",
	}
}

// largeSnapshotUserBatch describes one bounded test-data API call.
type largeSnapshotUserBatch struct {
	Index int
	Count int
}

func largeSnapshotUserBatches(rows, batchRows int) []largeSnapshotUserBatch {
	if batchRows <= 0 {
		panic("large snapshot batch rows must be positive")
	}
	if rows <= 0 {
		return nil
	}
	batches := make([]largeSnapshotUserBatch, 0, (rows+batchRows-1)/batchRows)
	for remaining, index := rows, 0; remaining > 0; index++ {
		count := batchRows
		if remaining < count {
			count = remaining
		}
		batches = append(batches, largeSnapshotUserBatch{Index: index, Count: count})
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

func slotPeerIDs(topology suite.SlotTopology) []uint64 {
	peers := []uint64{topology.LeaderNodeID}
	peers = append(peers, topology.FollowerNodeIDs...)
	return peers
}

func requireSlotCompactionCoversApplied(t *testing.T, resp suite.ManagerSlotRaftCompactionResponse, nodeID uint64) {
	t.Helper()
	require.Equal(t, 1, resp.Total)
	require.Equal(t, 1, resp.Succeeded)
	require.Len(t, resp.Items, 1)
	item := resp.Items[0]
	require.Equal(t, nodeID, item.NodeID)
	require.True(t, item.Success, item.Error)
	require.True(t, item.Compacted || item.SkippedReason == "up_to_date", "compaction item = %+v", item)
	require.NotZero(t, item.AfterSnapshotIndex)
	require.GreaterOrEqual(t, item.AfterSnapshotIndex, item.AppliedIndex)
}

func largeSnapshotDiagnostics(cluster *suite.StartedCluster, body []byte, compaction *suite.ManagerSlotRaftCompactionResponse) string {
	compactionBody := ""
	if compaction != nil {
		compactionBody = fmt.Sprintf("\nslot compaction: total=%d succeeded=%d failed=%d items=%+v", compaction.Total, compaction.Succeeded, compaction.Failed, compaction.Items)
	}
	return fmt.Sprintf("%s\nlarge snapshot body: %s%s", cluster.DumpDiagnostics(), string(body), compactionBody)
}
