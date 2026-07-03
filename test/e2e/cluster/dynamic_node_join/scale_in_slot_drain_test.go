//go:build e2e

package dynamic_node_join

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestScaleInAdvancesSlotReplicasBeforeRemove(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2e-scale-in-slot-drain-token"
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
	require.NotNil(t, node4)

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
	activeSender := requireGatewaySender(t, cluster, node4, "scale-in-slot-drain-onboarding")
	onboardingStart := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), onboardingStart.Created, cluster.DumpDiagnostics())
	requireGatewaySendDuringOnboardingActive(t, cluster, manager, activeSender, 4, "scale-in-slot-drain-onboarding", 1, 20*time.Second)
	require.NoError(t, activeSender.Close(), cluster.DumpDiagnostics())
	manager.EventuallyOnboardingSafe(t, 4, 45*time.Second)
	requireSlotsContainDesiredPeer(t, eventuallySlotsContainDesiredPeer(t, cluster, 1, 4, 45*time.Second), 4)

	start := manager.MustStartScaleIn(t, 4)
	require.Equal(t, uint64(4), start.NodeID)
	require.Equal(t, "leaving", start.JoinState)
	preDrainStatus := eventuallyScaleInSlotBlockersVisible(t, cluster, manager, 4, 30*time.Second)
	require.True(t, preDrainStatus.BlockedBySlots)
	require.False(t, preDrainStatus.SafeToRemove)
	require.Positive(t, preDrainStatus.SlotReplicaCount)

	eventuallySetScaleInDrain(t, cluster, manager, 4, true, 30*time.Second)

	statusCtx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
	status, err := manager.NodeScaleInStatus(statusCtx, 4)
	cancelStatus()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, status.BlockedBySlots, "scale-in status should block while node 4 still owns Slot replicas: %#v", status)
	require.False(t, status.SafeToRemove, "scale-in status should not be safe while node 4 still owns Slot replicas: %#v", status)
	require.Positive(t, status.SlotReplicaCount, "scale-in status should count node 4 Slot replicas: %#v", status)
	require.True(t, status.GatewayDraining, "scale-in status should report drain mode after enabling drain: %#v", status)
	require.False(t, status.AcceptingNewSessions, "scale-in status should reject new sessions after enabling drain: %#v", status)

	scaleInPlan := eventuallyScaleInPlanReady(t, cluster, manager, 4, 1, 30*time.Second)
	require.Len(t, scaleInPlan.Candidates, 1, cluster.DumpDiagnostics())
	requireScaleInCandidateMovesAwayFromNode(t, scaleInPlan.Candidates[0], 4)

	advance := manager.MustAdvanceScaleIn(t, 4, 1)
	require.Equal(t, uint32(1), advance.Created, cluster.DumpDiagnostics())
	require.Len(t, advance.Candidates, 1, cluster.DumpDiagnostics())
	requireScaleInCandidateMovesAwayFromNode(t, advance.Candidates[0], 4)

	drained := eventuallyScaleInSlotsDrained(t, cluster, manager, 4, 45*time.Second)
	require.False(t, drained.BlockedBySlots)
	require.Zero(t, drained.SlotReplicaCount)

	safe := manager.EventuallyScaleInSafeToRemove(t, 4, 45*time.Second)
	require.True(t, safe.SafeToRemove)
	require.False(t, safe.BlockedBySlots)
	require.False(t, safe.BlockedByTasks)
	require.False(t, safe.BlockedByRuntimeDrain)
	require.Zero(t, safe.SlotReplicaCount)

	removed := manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, uint64(4), removed.NodeID)
	require.Equal(t, "removed", removed.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)
}

func requireSlotsContainDesiredPeer(t testing.TB, slots []suite.SlotDTO, nodeID uint64) {
	t.Helper()

	for _, slot := range slots {
		if slices.Contains(slot.Assignment.DesiredPeers, nodeID) {
			return
		}
	}
	t.Fatalf("manager Slot inventory does not contain node %d in any desired peer set: %#v", nodeID, slots)
}

func eventuallySlotsContainDesiredPeer(t testing.TB, cluster *suite.StartedCluster, managerNodeID uint64, nodeID uint64, timeout time.Duration) []suite.SlotDTO {
	t.Helper()

	managerNode := cluster.MustNode(managerNodeID)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastSlots []suite.SlotDTO
		lastErr   error
	)
	for {
		var resp scaleInSlotDrainSlotsResponse
		_, err := suite.GetJSON(ctx, "http://"+managerNode.Spec.ManagerAddr+"/manager/slots", &resp)
		if err == nil {
			lastSlots = append([]suite.SlotDTO(nil), resp.Items...)
			if scaleInSlotDrainSlotsStable(resp) && slotsContainDesiredPeer(resp.Items, nodeID) {
				return resp.Items
			}
			lastErr = fmt.Errorf("total=%d items=%d contains_node=%t stable=%t", resp.Total, len(resp.Items), slotsContainDesiredPeer(resp.Items, nodeID), scaleInSlotDrainSlotsStable(resp))
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("manager Slot inventory did not include node %d as a desired peer: last=%#v lastErr=%v\n%s", nodeID, lastSlots, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func slotsContainDesiredPeer(slots []suite.SlotDTO, nodeID uint64) bool {
	for _, slot := range slots {
		if slices.Contains(slot.Assignment.DesiredPeers, nodeID) {
			return true
		}
	}
	return false
}

func scaleInSlotDrainSlotsStable(resp scaleInSlotDrainSlotsResponse) bool {
	if resp.Total == 0 || resp.Total != len(resp.Items) {
		return false
	}
	for _, item := range resp.Items {
		if item.Task != nil || len(item.Assignment.DesiredPeers) == 0 {
			return false
		}
	}
	return true
}

type scaleInSlotDrainSlotsResponse struct {
	Total int             `json:"total"`
	Items []suite.SlotDTO `json:"items"`
}

func eventuallyScaleInSlotBlockersVisible(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastErr    error
	)
	for {
		status, err := manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			if status.JoinState == "leaving" &&
				status.BlockedBySlots &&
				!status.SafeToRemove &&
				status.SlotReplicaCount > 0 {
				return status
			}
			lastErr = fmt.Errorf("join_state=%q blocked_by_slots=%t safe_to_remove=%t slot_replica_count=%d",
				status.JoinState,
				status.BlockedBySlots,
				status.SafeToRemove,
				status.SlotReplicaCount,
			)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in Slot blockers did not become visible: last=%#v lastErr=%v\n%s", nodeID, lastStatus, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func eventuallyScaleInSlotsDrained(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastErr    error
	)
	for {
		status, err := manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			if !status.BlockedBySlots &&
				!status.BlockedBySlotLeadership &&
				!status.BlockedBySlotRuntime &&
				status.SlotReplicaCount == 0 &&
				status.SlotLeaderCount == 0 {
				return status
			}
			lastErr = fmt.Errorf("blocked_by_slots=%t blocked_by_slot_leadership=%t blocked_by_slot_runtime=%t slot_replica_count=%d slot_leader_count=%d active_task_count=%d failed_task_count=%d",
				status.BlockedBySlots,
				status.BlockedBySlotLeadership,
				status.BlockedBySlotRuntime,
				status.SlotReplicaCount,
				status.SlotLeaderCount,
				status.ActiveTaskCount,
				status.FailedTaskCount,
			)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in Slot blockers did not drain: last=%#v lastErr=%v\n%s", nodeID, lastStatus, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func eventuallyScaleInPlanReady(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, maxSlotMoves uint32, timeout time.Duration) suite.NodeScaleInPlanDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastPlan   suite.NodeScaleInPlanDTO
		lastErr    error
	)
	for {
		status, err := manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			plan := manager.MustPlanScaleIn(t, nodeID, maxSlotMoves)
			lastPlan = plan
			if !plan.BlockedByStatus && len(plan.Candidates) > 0 {
				return plan
			}
			lastErr = fmt.Errorf("blocked_by_status=%t candidates=%d blocked_by_control_revision=%t blocked_by_tasks=%t blocked_by_slot_runtime=%t unknown_runtime=%t",
				plan.BlockedByStatus,
				len(plan.Candidates),
				status.BlockedByControlRevision,
				status.BlockedByTasks,
				status.BlockedBySlotRuntime,
				status.UnknownRuntime,
			)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in plan did not become ready: status=%#v plan=%#v lastErr=%v\n%s", nodeID, lastStatus, lastPlan, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func eventuallySetScaleInDrain(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, drain bool, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus int
		lastBody   []byte
		lastErr    error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 2*time.Second)
		status, body, err := manager.SetScaleInDrainStatus(reqCtx, nodeID, drain)
		cancelReq()
		if err == nil && status/100 == 2 {
			return
		}
		lastStatus = status
		lastBody = append(lastBody[:0], body...)
		lastErr = err
		if err == nil && status != 409 {
			t.Fatalf("set scale-in drain node %d returned %d: %s\n%s", nodeID, status, string(body), cluster.DumpDiagnostics())
		}

		select {
		case <-ctx.Done():
			t.Fatalf("set scale-in drain node %d timed out: lastStatus=%d lastBody=%s lastErr=%v\n%s", nodeID, lastStatus, string(lastBody), lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireScaleInCandidateMovesAwayFromNode(t testing.TB, candidate suite.NodeScaleInCandidateDTO, nodeID uint64) {
	t.Helper()

	require.Equal(t, nodeID, candidate.SourceNodeID)
	require.NotEqual(t, nodeID, candidate.TargetNodeID)
	require.Contains(t, candidate.DesiredPeers, nodeID)
	require.NotContains(t, candidate.TargetPeers, nodeID)
}
