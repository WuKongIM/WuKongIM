//go:build e2e

package dynamic_node_readiness

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestDynamicNodeLifecycleWithContinuousTraffic(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2ev2-stage9-readiness-token"
	overrides := readinessOverrides()
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
		suite.WithNodeConfigOverrides(4, overrides),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	eventuallyNodeHealthFresh(t, cluster, manager, 1, 120*time.Second)
	eventuallyNodeHealthFresh(t, cluster, manager, 2, 120*time.Second)
	eventuallyNodeHealthFresh(t, cluster, manager, 3, 120*time.Second)

	traffic := startTrafficWorker(t, cluster, cluster.MustNode(1), "stage9-readiness")
	defer stopTrafficWorker(t, traffic)

	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	require.NotNil(t, node4)
	manager.EventuallyNodeJoinState(t, 4, "joining", 30*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 30*time.Second)
	eventuallyNodeHealthFresh(t, cluster, manager, 4, 120*time.Second)
	requireNodeNotSchedulable(t, cluster, manager, 4)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 30*time.Second)
	eventuallyNodeSchedulable(t, cluster, manager, 4, 120*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	onboardingPlan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, onboardingPlan.Candidates, 1, cluster.DumpDiagnostics())
	onboardingCandidate := onboardingPlan.Candidates[0]
	ensureSlotLeaderForReplicaMoveSource(t, cluster, onboardingCandidate.SlotID, onboardingCandidate.SourceNodeID, 45*time.Second)
	onboardingPlan = manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, onboardingPlan.Candidates, 1, cluster.DumpDiagnostics())
	replannedOnboardingCandidate := onboardingPlan.Candidates[0]
	require.Equal(t, onboardingCandidate.SlotID, replannedOnboardingCandidate.SlotID, cluster.DumpDiagnostics())
	require.Equal(t, onboardingCandidate.SourceNodeID, replannedOnboardingCandidate.SourceNodeID, cluster.DumpDiagnostics())
	ensureSlotLeaderForReplicaMoveSource(t, cluster, replannedOnboardingCandidate.SlotID, replannedOnboardingCandidate.SourceNodeID, 45*time.Second)
	onboardingStart := eventuallyStartOnboarding(t, cluster, manager, 4, 1, 45*time.Second)
	require.Equal(t, uint32(1), onboardingStart.Created, cluster.DumpDiagnostics())
	eventuallyOnboardingSafe(t, cluster, manager, 4, 150*time.Second)
	require.NotEmpty(t, eventuallySlotsContainDesiredPeer(t, cluster, 1, 4, 90*time.Second))
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	start := eventuallyStartScaleIn(t, cluster, manager, 4, 45*time.Second)
	require.Equal(t, "leaving", start.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "leaving", 30*time.Second)
	eventuallySetScaleInDrain(t, cluster, manager, 4, true, 30*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	plan := eventuallyScaleInPlanReady(t, cluster, manager, 4, 1, 30*time.Second)
	require.NotEmpty(t, plan.Candidates, cluster.DumpDiagnostics())
	scaleInCandidate := plan.Candidates[0]
	requireScaleInCandidateMovesAwayFromNode(t, scaleInCandidate, 4)
	advance := eventuallyAdvanceScaleIn(t, cluster, manager, 4, 1, scaleInCandidate, 45*time.Second)
	if advance.Created > 0 {
		require.Len(t, advance.Candidates, 1, cluster.DumpDiagnostics())
		requireScaleInCandidateMovesAwayFromNode(t, advance.Candidates[0], 4)
	}
	drained := eventuallyScaleInSlotsDrained(t, cluster, manager, 4, 150*time.Second)
	require.False(t, drained.BlockedByHealth, "health must not block after fresh reports: %#v", drained)
	require.False(t, drained.BlockedByStaleRevision, "revision freshness must not block after reports: %#v", drained)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	safe := manager.EventuallyScaleInSafeToRemove(t, 4, 150*time.Second)
	require.True(t, safe.SafeToRemove, "status=%#v", safe)
	require.True(t, safe.HealthFresh, "status=%#v", safe)
	removed := eventuallyRemoveScaleInNode(t, cluster, manager, 4, 60*time.Second)
	require.Equal(t, "removed", removed.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "removed", 30*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	requireMetricSamples(t, cluster.MustNode(1),
		metricExpectation{name: "wukongim_node_lifecycle_nodes", labels: map[string]string{"join_state": "active", "status": "alive"}, minValue: 3},
		metricExpectation{name: "wukongim_node_lifecycle_nodes", labels: map[string]string{"join_state": "removed", "status": "down"}, minValue: 1},
		metricExpectation{name: "wukongim_node_health_freshness_nodes", labels: map[string]string{"freshness": "fresh", "status": "alive"}, minValue: 3},
		metricExpectation{name: "wukongim_node_lifecycle_attempts_total", labels: map[string]string{"operation": "activate", "result": "ok"}, minValue: 1},
		metricExpectation{name: "wukongim_node_lifecycle_attempts_total", labels: map[string]string{"operation": "scale_in_remove", "result": "ok"}, minValue: 1},
		metricExpectation{name: "wukongim_node_scale_in_blockers_total", labels: map[string]string{"reason": "target_health_stale"}, minValue: 0},
		metricExpectation{name: "wukongim_discovery_membership_revision", minValue: 1},
	)
}
