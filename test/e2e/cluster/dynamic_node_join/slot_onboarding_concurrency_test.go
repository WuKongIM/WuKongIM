//go:build e2e

package dynamic_node_join

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestConcurrentOnboardingStartCreatesAtMostOneTask(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2e-concurrent-onboarding-token"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)
	eventuallyNodeSchedulable(t, cluster, manager, 4, 45*time.Second)

	activeReadyCtx, cancelActiveReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelActiveReady()
	require.NoError(t, cluster.WaitClusterReady(activeReadyCtx), cluster.DumpDiagnostics())

	plan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1, cluster.DumpDiagnostics())

	results := runConcurrentOnboardingStarts(t, manager, 4, 1, 2)
	var totalCreated uint32
	createdSlots := make(map[uint32]struct{})
	for _, result := range results {
		require.NoError(t, result.err, cluster.DumpDiagnostics())
		requireConcurrentTaskCreateStatus(t, result.status, result.body, cluster.DumpDiagnostics())
		totalCreated += result.response.Created
		recordCreatedOnboardingSlots(t, createdSlots, result.response.Results, cluster.DumpDiagnostics())
	}
	require.Positive(t, totalCreated, "responses=%#v\n%s", results, cluster.DumpDiagnostics())
	require.LessOrEqual(t, totalCreated, uint32(len(results)), "responses=%#v\n%s", results, cluster.DumpDiagnostics())

	statusCtx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
	status, err := manager.NodeOnboardingStatus(statusCtx, 4)
	cancelStatus()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Zero(t, status.Summary.Failed, "onboarding status=%#v\n%s", status, cluster.DumpDiagnostics())
	if status.Summary.TotalActive > 0 {
		sender := requireGatewaySender(t, cluster, node4, "concurrent-onboarding-active")
		sendGatewayMessage(t, cluster, sender, "concurrent-onboarding-active", 0)
		require.NoError(t, sender.Close(), cluster.DumpDiagnostics())
	} else {
		requireGatewaySendLoop(t, cluster, node4, "concurrent-onboarding-cleared", 1)
	}
	manager.EventuallyOnboardingSafe(t, 4, 45*time.Second)
	require.NotEmpty(t, manager.MustSlots(t), cluster.DumpDiagnostics())
}

func TestConcurrentScaleInAdvanceCreatesAtMostOneSlotTask(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2e-concurrent-scale-in-token"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)
	eventuallyNodeSchedulable(t, cluster, manager, 4, 45*time.Second)

	activeReadyCtx, cancelActiveReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelActiveReady()
	require.NoError(t, cluster.WaitClusterReady(activeReadyCtx), cluster.DumpDiagnostics())

	onboardingPlan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, onboardingPlan.Candidates, 1, cluster.DumpDiagnostics())
	onboardingStart := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), onboardingStart.Created, cluster.DumpDiagnostics())
	manager.EventuallyOnboardingSafe(t, 4, 45*time.Second)
	requireSlotsContainDesiredPeer(t, eventuallySlotsContainDesiredPeer(t, cluster, 1, 4, 45*time.Second), 4)

	start := manager.MustStartScaleIn(t, 4)
	require.Equal(t, "leaving", start.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "leaving", 20*time.Second)
	eventuallySetScaleInDrain(t, cluster, manager, 4, true, 30*time.Second)
	plan := eventuallyScaleInPlanReady(t, cluster, manager, 4, 1, 30*time.Second)
	require.Len(t, plan.Candidates, 1, cluster.DumpDiagnostics())

	results := runConcurrentScaleInAdvances(t, manager, 4, 1, 2)
	var totalCreated uint32
	for _, result := range results {
		require.NoError(t, result.err, cluster.DumpDiagnostics())
		requireConcurrentTaskCreateStatus(t, result.status, result.body, cluster.DumpDiagnostics())
		totalCreated += result.response.Created
	}
	require.Equal(t, uint32(1), totalCreated, "responses=%#v\n%s", results, cluster.DumpDiagnostics())

	drained := eventuallyScaleInSlotsDrained(t, cluster, manager, 4, 45*time.Second)
	require.False(t, drained.BlockedBySlots)
	require.Zero(t, drained.FailedTaskCount)
	safe := manager.EventuallyScaleInSafeToRemove(t, 4, 45*time.Second)
	require.True(t, safe.SafeToRemove)
	removed := manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, "removed", removed.JoinState)
}

type onboardingStartResult struct {
	response suite.NodeOnboardingStartDTO
	status   int
	body     []byte
	err      error
}

type scaleInAdvanceResult struct {
	response suite.NodeScaleInAdvanceDTO
	status   int
	body     []byte
	err      error
}

func runConcurrentOnboardingStarts(t testing.TB, manager *suite.ManagerClient, nodeID uint64, maxSlotMoves uint32, count int) []onboardingStartResult {
	t.Helper()

	results := make(chan onboardingStartResult, count)
	for i := 0; i < count; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response, status, body, err := retryConcurrentOnboardingStart(ctx, manager, nodeID, maxSlotMoves)
			results <- onboardingStartResult{response: response, status: status, body: body, err: err}
		}()
	}
	return collectOnboardingStartResults(results, count)
}

func runConcurrentScaleInAdvances(t testing.TB, manager *suite.ManagerClient, nodeID uint64, maxSlotMoves uint32, count int) []scaleInAdvanceResult {
	t.Helper()

	results := make(chan scaleInAdvanceResult, count)
	for i := 0; i < count; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response, status, body, err := retryConcurrentScaleInAdvance(ctx, manager, nodeID, maxSlotMoves)
			results <- scaleInAdvanceResult{response: response, status: status, body: body, err: err}
		}()
	}
	return collectScaleInAdvanceResults(results, count)
}

func retryConcurrentOnboardingStart(ctx context.Context, manager *suite.ManagerClient, nodeID uint64, maxSlotMoves uint32) (suite.NodeOnboardingStartDTO, int, []byte, error) {
	for {
		response, status, body, err := manager.StartOnboarding(ctx, nodeID, maxSlotMoves)
		if err != nil || status != http.StatusServiceUnavailable {
			return response, status, body, err
		}
		if err := waitForConcurrentControlWriteRetry(ctx); err != nil {
			return response, status, body, nil
		}
	}
}

func retryConcurrentScaleInAdvance(ctx context.Context, manager *suite.ManagerClient, nodeID uint64, maxSlotMoves uint32) (suite.NodeScaleInAdvanceDTO, int, []byte, error) {
	for {
		response, status, body, err := manager.AdvanceScaleIn(ctx, nodeID, maxSlotMoves)
		if err != nil || status != http.StatusServiceUnavailable {
			return response, status, body, err
		}
		if err := waitForConcurrentControlWriteRetry(ctx); err != nil {
			return response, status, body, nil
		}
	}
}

func waitForConcurrentControlWriteRetry(ctx context.Context) error {
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func collectOnboardingStartResults(results <-chan onboardingStartResult, count int) []onboardingStartResult {
	out := make([]onboardingStartResult, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, <-results)
	}
	return out
}

func collectScaleInAdvanceResults(results <-chan scaleInAdvanceResult, count int) []scaleInAdvanceResult {
	out := make([]scaleInAdvanceResult, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, <-results)
	}
	return out
}

func requireConcurrentTaskCreateStatus(t testing.TB, status int, body []byte, diagnostics string) {
	t.Helper()

	switch status {
	case http.StatusOK, http.StatusAccepted:
		return
	case http.StatusConflict:
		requireScaleInConflict(t, status, body, diagnostics)
	default:
		t.Fatalf("unexpected concurrent task create status=%d body=%s\n%s", status, string(body), diagnostics)
	}
}

func recordCreatedOnboardingSlots(t testing.TB, seen map[uint32]struct{}, results []suite.NodeOnboardingTaskResultDTO, diagnostics string) {
	t.Helper()

	for _, result := range results {
		if !result.Created {
			continue
		}
		if _, ok := seen[result.SlotID]; ok {
			t.Fatalf("duplicate created onboarding task for Slot %d\n%s", result.SlotID, diagnostics)
		}
		seen[result.SlotID] = struct{}{}
	}
}

func (r onboardingStartResult) String() string {
	return fmt.Sprintf("{status:%d created:%d err:%v body:%s}", r.status, r.response.Created, r.err, string(r.body))
}

func (r scaleInAdvanceResult) String() string {
	return fmt.Sprintf("{status:%d created:%d skipped:%d err:%v body:%s}", r.status, r.response.Created, r.response.Skipped, r.err, string(r.body))
}
