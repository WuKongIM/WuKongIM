//go:build e2e

package dynamic_node_readiness

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func readinessOverrides() map[string]string {
	return map[string]string{
		"WK_BENCH_API_ENABLE":                    "true",
		"WK_METRICS_ENABLE":                      "true",
		"WK_TOP_API_ENABLE":                      "true",
		"WK_TOP_COLLECT_INTERVAL":                "100ms",
		"WK_TOP_HISTORY_WINDOW":                  "2s",
		"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL": "500ms",
		"WK_CLUSTER_NODE_HEALTH_REPORT_TTL":      "3s",
		"WK_DELIVERY_ENABLE":                     "true",
	}
}

func eventuallyNodeHealthFresh(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		last    suite.NodeDTO
		lastErr error
	)
	for {
		nodes, err := manager.ListNodes(ctx)
		if err == nil {
			for _, node := range nodes.Items {
				if node.NodeID != nodeID {
					continue
				}
				last = node
				if node.Health.Fresh &&
					node.Health.Freshness == "fresh" &&
					node.Health.Status == "alive" &&
					node.Health.RuntimeReady {
					return node
				}
				lastErr = fmt.Errorf("health=%#v membership=%#v", node.Health, node.Membership)
			}
			if last.NodeID == 0 {
				lastErr = fmt.Errorf("node %d missing from manager inventory", nodeID)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d health did not become fresh: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func eventuallyNodeSchedulable(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		last    suite.NodeDTO
		lastErr error
	)
	for {
		nodes, err := manager.ListNodes(ctx)
		if err == nil {
			for _, node := range nodes.Items {
				if node.NodeID != nodeID {
					continue
				}
				last = node
				if node.Membership.JoinState == "active" &&
					node.Membership.Schedulable &&
					node.Health.Fresh &&
					node.Health.Status == "alive" &&
					node.Health.RuntimeReady {
					return node
				}
				lastErr = fmt.Errorf("membership=%#v health=%#v", node.Membership, node.Health)
			}
			if last.NodeID == 0 {
				lastErr = fmt.Errorf("node %d missing from manager inventory", nodeID)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d did not become schedulable: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireNodeNotSchedulable(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nodes, err := manager.ListNodes(ctx)
	require.NoError(t, err, cluster.DumpDiagnostics())
	for _, node := range nodes.Items {
		if node.NodeID != nodeID {
			continue
		}
		require.False(t, node.Membership.Schedulable, "joining node must not be schedulable before activation: %#v", node)
		return
	}
	t.Fatalf("node %d missing from manager inventory\n%s", nodeID, cluster.DumpDiagnostics())
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
		var resp struct {
			Total int             `json:"total"`
			Items []suite.SlotDTO `json:"items"`
		}
		_, err := suite.GetJSON(ctx, "http://"+managerNode.ManagerAddr()+"/manager/slots", &resp)
		if err == nil {
			lastSlots = append([]suite.SlotDTO(nil), resp.Items...)
			if resp.Total == len(resp.Items) && slotsContainDesiredPeer(resp.Items, nodeID) {
				return resp.Items
			}
			lastErr = fmt.Errorf("total=%d items=%d contains_node=%t", resp.Total, len(resp.Items), slotsContainDesiredPeer(resp.Items, nodeID))
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("manager Slot inventory did not include node %d: last=%#v lastErr=%v\n%s", nodeID, lastSlots, lastErr, cluster.DumpDiagnostics())
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

func eventuallyScaleInSlotsDrained(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		last    suite.NodeScaleInStatusDTO
		lastErr error
	)
	for {
		status, err := manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			last = status
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
			t.Fatalf("node %d scale-in Slots did not drain: last=%#v lastErr=%v\n%s", nodeID, last, lastErr, cluster.DumpDiagnostics())
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
			lastErr = fmt.Errorf("blocked_by_status=%t candidates=%d blocked_by_health=%t blocked_by_stale_revision=%t blocked_by_tasks=%t blocked_by_slot_runtime=%t unknown_runtime=%t",
				plan.BlockedByStatus,
				len(plan.Candidates),
				status.BlockedByHealth,
				status.BlockedByStaleRevision,
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
