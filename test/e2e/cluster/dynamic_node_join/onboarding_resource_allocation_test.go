//go:build e2e

package dynamic_node_join

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestDynamicNodeJoinOnboardingResourceAllocation(t *testing.T) {
	const joinToken = "join-secret"

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithNodeConfigOverrides(1, map[string]string{"WK_CLUSTER_JOIN_TOKEN": joinToken}),
		suite.WithNodeConfigOverrides(2, map[string]string{"WK_CLUSTER_JOIN_TOKEN": joinToken}),
		suite.WithNodeConfigOverrides(3, map[string]string{"WK_CLUSTER_JOIN_TOKEN": joinToken}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.ResolveSlotTopology(ctx, 1)
	require.NoError(t, err, cluster.DumpDiagnostics())

	joined := s.StartDynamicJoinNode(cluster, 4, joinToken)
	require.NoError(t, suite.WaitNodeReady(ctx, *joined), cluster.DumpDiagnostics())

	observer := cluster.MustNode(1)
	joinedBefore, body, err := suite.WaitForManagerNode(ctx, *observer, joined.Spec.ID, func(node suite.ManagerNode) bool {
		return node.Status == "alive" && node.Addr == joined.Spec.ClusterAddr && node.Controller.Role == "none" && node.SlotStats.Count == 0
	})
	require.NoError(t, err, onboardingDiagnostics(cluster, body, nil))
	require.Zero(t, joinedBefore.SlotStats.Count)

	candidates, body, err := suite.FetchNodeOnboardingCandidates(ctx, *observer)
	require.NoError(t, err, onboardingDiagnostics(cluster, body, nil))
	require.Contains(t, nodeOnboardingCandidateIDs(candidates.Items), joined.Spec.ID)

	planned, body, err := suite.CreateNodeOnboardingPlan(ctx, *observer, joined.Spec.ID)
	require.NoError(t, err, onboardingDiagnostics(cluster, body, nil))
	require.Equal(t, joined.Spec.ID, planned.TargetNodeID)
	require.Equal(t, "planned", planned.Status)
	require.NotEmpty(t, planned.Plan.Moves, onboardingDiagnostics(cluster, body, &planned))
	require.Positive(t, planned.ResultCounts.Pending, onboardingDiagnostics(cluster, body, &planned))

	running, body, err := suite.StartNodeOnboardingJob(ctx, *observer, planned.JobID)
	require.NoError(t, err, onboardingDiagnostics(cluster, body, &planned))
	require.Equal(t, "running", running.Status)

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

func nodeOnboardingCandidateIDs(items []suite.ManagerNodeOnboardingCandidate) []uint64 {
	ids := make([]uint64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.NodeID)
	}
	return ids
}

func onboardingDiagnostics(cluster *suite.StartedCluster, body []byte, job *suite.ManagerNodeOnboardingJob) string {
	jobBody := ""
	if job != nil {
		jobBody = fmt.Sprintf("\nonboarding job: id=%s status=%s target=%d counts=%+v error=%q", job.JobID, job.Status, job.TargetNodeID, job.ResultCounts, job.LastError)
	}
	return fmt.Sprintf("%s\nmanager onboarding body: %s%s", cluster.DumpDiagnostics(), string(body), jobBody)
}
