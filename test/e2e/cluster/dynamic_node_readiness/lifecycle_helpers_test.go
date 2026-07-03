//go:build e2e

package dynamic_node_readiness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func readinessOverrides() map[string]string {
	return map[string]string{
		"WK_BENCH_API_ENABLE":                    "true",
		"WK_METRICS_ENABLE":                      "true",
		"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL": "500ms",
		"WK_CLUSTER_NODE_HEALTH_REPORT_TTL":      "30s",
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
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		nodes, err := manager.ListNodes(reqCtx)
		cancelReq()
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
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		nodes, err := manager.ListNodes(reqCtx)
		cancelReq()
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
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		var resp struct {
			Total int             `json:"total"`
			Items []suite.SlotDTO `json:"items"`
		}
		_, err := suite.GetJSON(reqCtx, "http://"+managerNode.ManagerAddr()+"/manager/slots", &resp)
		cancelReq()
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

func eventuallyStartOnboarding(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, maxSlotMoves uint32, timeout time.Duration) suite.NodeOnboardingStartDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeOnboardingStatusDTO
		lastHTTP   int
		lastBody   []byte
		lastErr    error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 10*time.Second)
		out, status, body, err := manager.StartOnboarding(reqCtx, nodeID, maxSlotMoves)
		cancelReq()
		if err == nil && status/100 == 2 && out.Created > 0 {
			return out
		}
		lastHTTP = status
		lastBody = append(lastBody[:0], body...)
		lastErr = err
		if status != 0 && status != http.StatusConflict && status != http.StatusServiceUnavailable {
			t.Fatalf("start onboarding node %d returned %d: %s\n%s", nodeID, status, strings.TrimSpace(string(body)), cluster.DumpDiagnostics())
		}
		statusCtx, cancelStatus := context.WithTimeout(ctx, 5*time.Second)
		if onboardingStatus, statusErr := manager.NodeOnboardingStatus(statusCtx, nodeID); statusErr == nil {
			lastStatus = onboardingStatus
			if onboardingStatus.Summary.TotalActive > 0 {
				cancelStatus()
				return suite.NodeOnboardingStartDTO{
					StateRevision: onboardingStatus.StateRevision,
					TargetNodeID:  nodeID,
					MaxSlotMoves:  maxSlotMoves,
					Created:       1,
				}
			}
		}
		cancelStatus()

		select {
		case <-ctx.Done():
			t.Fatalf("start onboarding node %d timed out: lastHTTP=%d lastBody=%s lastErr=%v lastStatus=%#v\n%s", nodeID, lastHTTP, strings.TrimSpace(string(lastBody)), lastErr, lastStatus, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func eventuallyOnboardingSafe(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeOnboardingStatusDTO
		lastErr    error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		status, err := manager.NodeOnboardingStatus(reqCtx, nodeID)
		cancelReq()
		if err == nil {
			lastStatus = status
			if status.Summary.TotalActive == 0 {
				return
			}
			lastErr = fmt.Errorf("onboarding active=%d pending=%d running=%d failed=%d", status.Summary.TotalActive, status.Summary.Pending, status.Summary.Running, status.Summary.Failed)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			diagnostics := describeOnboardingSlotDiagnostics(cluster, lastStatus.Tasks)
			t.Fatalf("node %d onboarding did not become safe: last=%#v lastErr=%v slotDiagnostics=%s\n%s", nodeID, lastStatus, lastErr, diagnostics, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func describeOnboardingSlotDiagnostics(cluster *suite.StartedCluster, tasks []suite.NodeOnboardingStatusTaskDTO) string {
	if len(tasks) == 0 {
		return "<no tasks>"
	}
	lines := make([]string, 0)
	for _, task := range tasks {
		slotID := task.SlotID
		if slotID == 0 {
			slotID = slotIDFromTaskID(task.TaskID)
		}
		if slotID == 0 {
			lines = append(lines, fmt.Sprintf("task=%s slot=<unknown> step=%s source=%d target=%d", task.TaskID, task.Step, task.SourceNode, task.TargetNode))
			continue
		}
		nodeIDs := append([]uint64{task.SourceNode}, task.TargetPeers...)
		slices.Sort(nodeIDs)
		nodeIDs = slices.Compact(nodeIDs)
		for _, nodeID := range nodeIDs {
			if nodeID == 0 {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			row, err := fetchSlotRowForNode(ctx, cluster, slotID, nodeID)
			cancel()
			if err != nil {
				lines = append(lines, fmt.Sprintf("task=%s slot=%d node=%d err=%v", task.TaskID, slotID, nodeID, err))
				continue
			}
			lines = append(lines, fmt.Sprintf("task=%s slot=%d node=%d assignment=%v task=%#v runtime_voters=%v node_log=%#v", task.TaskID, slotID, nodeID, row.Assignment.DesiredPeers, row.Task, row.Runtime.CurrentVoters, row.NodeLog))
		}
	}
	if len(lines) == 0 {
		return "<empty>"
	}
	return strings.Join(lines, " | ")
}

func slotIDFromTaskID(taskID string) uint32 {
	var slotID uint32
	if _, err := fmt.Sscanf(taskID, "slot-%d-", &slotID); err != nil {
		return 0
	}
	return slotID
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
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		status, err := manager.NodeScaleInStatus(reqCtx, nodeID)
		cancelReq()
		if err == nil {
			last = status
			if !status.BlockedBySlots &&
				!status.BlockedBySlotLeadership &&
				!status.BlockedBySlotRuntime &&
				!status.BlockedByControlRevision &&
				!status.BlockedByHealth &&
				!status.BlockedByStaleRevision &&
				status.SlotReplicaCount == 0 &&
				status.SlotLeaderCount == 0 {
				return status
			}
			lastErr = fmt.Errorf("blocked_by_slots=%t blocked_by_slot_leadership=%t blocked_by_slot_runtime=%t blocked_by_control_revision=%t blocked_by_health=%t blocked_by_stale_revision=%t slot_replica_count=%d slot_leader_count=%d active_task_count=%d failed_task_count=%d blocked_reasons=%v",
				status.BlockedBySlots,
				status.BlockedBySlotLeadership,
				status.BlockedBySlotRuntime,
				status.BlockedByControlRevision,
				status.BlockedByHealth,
				status.BlockedByStaleRevision,
				status.SlotReplicaCount,
				status.SlotLeaderCount,
				status.ActiveTaskCount,
				status.FailedTaskCount,
				status.BlockedReasons,
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
		reqCtx, cancelReq := context.WithTimeout(ctx, 5*time.Second)
		status, err := manager.NodeScaleInStatus(reqCtx, nodeID)
		cancelReq()
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

func eventuallyStartScaleIn(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeScaleInStartDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastHTTP   int
		lastBody   []byte
		lastErr    error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 10*time.Second)
		status, body, err := manager.StartScaleInStatus(reqCtx, nodeID)
		cancelReq()
		if err == nil && status/100 == 2 {
			var out suite.NodeScaleInStartDTO
			if decodeErr := json.Unmarshal(body, &out); decodeErr != nil {
				t.Fatalf("decode scale-in start node %d response: %v body=%s\n%s", nodeID, decodeErr, strings.TrimSpace(string(body)), cluster.DumpDiagnostics())
			}
			return out
		}
		lastHTTP = status
		lastBody = append(lastBody[:0], body...)
		lastErr = err
		if status != 0 && status != http.StatusConflict && status != http.StatusServiceUnavailable {
			t.Fatalf("start scale-in node %d returned %d: %s\n%s", nodeID, status, strings.TrimSpace(string(body)), cluster.DumpDiagnostics())
		}
		statusCtx, cancelStatus := context.WithTimeout(ctx, 2*time.Second)
		if scaleStatus, statusErr := manager.NodeScaleInStatus(statusCtx, nodeID); statusErr == nil {
			lastStatus = scaleStatus
			if scaleStatus.JoinState == "leaving" {
				cancelStatus()
				return suite.NodeScaleInStartDTO{Changed: false, NodeID: nodeID, JoinState: scaleStatus.JoinState, Revision: scaleStatus.StateRevision}
			}
		}
		cancelStatus()

		select {
		case <-ctx.Done():
			t.Fatalf("start scale-in node %d timed out: lastHTTP=%d lastBody=%s lastErr=%v lastStatus=%#v\n%s", nodeID, lastHTTP, strings.TrimSpace(string(lastBody)), lastErr, lastStatus, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func eventuallyAdvanceScaleIn(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, maxSlotMoves uint32, candidate suite.NodeScaleInCandidateDTO, timeout time.Duration) suite.NodeScaleInAdvanceDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastHTTP   int
		lastBody   []byte
		lastErr    error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 10*time.Second)
		out, status, body, err := manager.AdvanceScaleIn(reqCtx, nodeID, maxSlotMoves)
		cancelReq()
		if err == nil && status/100 == 2 {
			return out
		}
		lastHTTP = status
		lastBody = append(lastBody[:0], body...)
		lastErr = err
		if status != 0 && status != http.StatusConflict && status != http.StatusServiceUnavailable {
			t.Fatalf("advance scale-in node %d returned %d: %s\n%s", nodeID, status, strings.TrimSpace(string(body)), cluster.DumpDiagnostics())
		}
		statusCtx, cancelStatus := context.WithTimeout(ctx, 5*time.Second)
		if scaleStatus, statusErr := manager.NodeScaleInStatus(statusCtx, nodeID); statusErr == nil {
			lastStatus = scaleStatus
			if scaleStatus.ActiveTaskCount > 0 {
				cancelStatus()
				return suite.NodeScaleInAdvanceDTO{
					StateRevision: scaleStatus.StateRevision,
					NodeID:        nodeID,
					Created:       1,
					Candidates:    []suite.NodeScaleInCandidateDTO{candidate},
				}
			}
		}
		cancelStatus()

		select {
		case <-ctx.Done():
			t.Fatalf("advance scale-in node %d timed out: lastHTTP=%d lastBody=%s lastErr=%v lastStatus=%#v\n%s", nodeID, lastHTTP, strings.TrimSpace(string(lastBody)), lastErr, lastStatus, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func eventuallyRemoveScaleInNode(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, timeout time.Duration) suite.NodeScaleInRemoveDTO {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastHTTP   int
		lastBody   []byte
		lastErr    error
	)
	for {
		reqCtx, cancelReq := context.WithTimeout(ctx, 10*time.Second)
		status, body, err := manager.RemoveScaleInStatus(reqCtx, nodeID)
		cancelReq()
		if err == nil && status/100 == 2 {
			var out suite.NodeScaleInRemoveDTO
			if decodeErr := json.Unmarshal(body, &out); decodeErr != nil {
				t.Fatalf("decode scale-in remove node %d response: %v body=%s\n%s", nodeID, decodeErr, strings.TrimSpace(string(body)), cluster.DumpDiagnostics())
			}
			return out
		}
		lastHTTP = status
		lastBody = append(lastBody[:0], body...)
		lastErr = err
		if status != 0 && status != http.StatusConflict && status != http.StatusServiceUnavailable {
			t.Fatalf("remove scale-in node %d returned %d: %s\n%s", nodeID, status, strings.TrimSpace(string(body)), cluster.DumpDiagnostics())
		}
		statusCtx, cancelStatus := context.WithTimeout(ctx, 5*time.Second)
		if scaleStatus, statusErr := manager.NodeScaleInStatus(statusCtx, nodeID); statusErr == nil {
			lastStatus = scaleStatus
			if scaleStatus.JoinState == "removed" {
				lastErr = fmt.Errorf("node is already removed; retrying idempotent remove POST for public API evidence")
			}
		}
		cancelStatus()

		select {
		case <-ctx.Done():
			t.Fatalf("remove scale-in node %d timed out: lastHTTP=%d lastBody=%s lastErr=%v lastStatus=%#v\n%s", nodeID, lastHTTP, strings.TrimSpace(string(lastBody)), lastErr, lastStatus, cluster.DumpDiagnostics())
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

func ensureSlotLeaderForReplicaMoveSource(t testing.TB, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	initial, err := fetchSlotRowForNode(ctx, cluster, slotID, sourceNodeID)
	if err == nil && initial.NodeLog != nil && initial.NodeLog.LeaderID == sourceNodeID {
		waitSlotLeaderOnSource(t, ctx, cluster, slotID, sourceNodeID, nil)
		return
	}
	transferResp := postSlotLeaderTransferToSource(t, ctx, cluster, slotID, sourceNodeID)
	waitSlotLeaderOnSource(t, ctx, cluster, slotID, sourceNodeID, &transferResp)
}

func postSlotLeaderTransferToSource(t testing.TB, ctx context.Context, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64) managerSlotLeaderTransferResponse {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	req := map[string]any{"target_node": sourceNodeID}
	nodeErrors := make(map[string]string)
	for {
		for _, node := range cluster.Nodes {
			var out managerSlotLeaderTransferResponse
			status, body, err := postManagerJSON(ctx, fmt.Sprintf("http://%s/manager/slots/%d/leader-transfer", node.ManagerAddr(), slotID), req, &out)
			if err == nil && status/100 == 2 {
				return out
			}
			nodeErrors[managerNodeErrorKey(node)] = formatManagerPostError(status, body, err)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("no manager node accepted Slot leader transfer slot=%d source=%d req=%#v errors=%s\n%s", slotID, sourceNodeID, req, formatNodeErrors(nodeErrors), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func waitSlotLeaderOnSource(t testing.TB, ctx context.Context, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64, transferResp *managerSlotLeaderTransferResponse) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	const stableWindow = time.Second
	var (
		firstObserved time.Time
		lastRow       managerSlotItem
		lastErr       error
	)
	for {
		row, err := fetchSlotRowForNode(ctx, cluster, slotID, sourceNodeID)
		if err == nil {
			lastRow = row
			if checkErr := checkSlotLeaderOnSource(row, sourceNodeID); checkErr == nil {
				if firstObserved.IsZero() {
					firstObserved = time.Now()
				}
				if time.Since(firstObserved) >= stableWindow {
					return
				}
			} else {
				firstObserved = time.Time{}
				lastErr = checkErr
			}
		} else {
			firstObserved = time.Time{}
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("slot %d source %d did not become live leader with no active task: lastRow=%#v transferResp=%#v lastErr=%v\n%s", slotID, sourceNodeID, lastRow, transferResp, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func fetchSlotRowForNode(ctx context.Context, cluster *suite.StartedCluster, slotID uint32, nodeID uint64) (managerSlotItem, error) {
	var out managerSlotsResponse
	_, err := suite.GetJSON(ctx, fmt.Sprintf("http://%s/manager/slots?node_id=%d", cluster.MustNode(nodeID).ManagerAddr(), nodeID), &out)
	if err != nil {
		return managerSlotItem{}, err
	}
	for _, item := range out.Items {
		if item.SlotID == slotID {
			return item, nil
		}
	}
	return managerSlotItem{}, fmt.Errorf("slot %d missing from manager slots response total=%d items=%#v", slotID, out.Total, out.Items)
}

func checkSlotLeaderOnSource(item managerSlotItem, sourceNodeID uint64) error {
	if item.Task != nil {
		return fmt.Errorf("slot %d has active task=%#v", item.SlotID, item.Task)
	}
	if item.NodeLog == nil {
		return fmt.Errorf("slot %d node log status missing", item.SlotID)
	}
	if item.NodeLog.LeaderID != sourceNodeID {
		return fmt.Errorf("slot %d leader=%d, want source=%d", item.SlotID, item.NodeLog.LeaderID, sourceNodeID)
	}
	return nil
}

func postManagerJSON(ctx context.Context, url string, body any, out any) (int, []byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, respBody, fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, respBody, fmt.Errorf("decode POST %s: %w body=%s", url, err, strings.TrimSpace(string(respBody)))
		}
	}
	return resp.StatusCode, respBody, nil
}

func managerNodeErrorKey(node suite.StartedNode) string {
	return fmt.Sprintf("node %d (%s)", node.Spec.ID, node.ManagerAddr())
}

func formatManagerPostError(status int, body []byte, err error) string {
	statusText := "status=<none>"
	if status != 0 {
		statusText = fmt.Sprintf("status=%d", status)
	}
	bodyText := strings.TrimSpace(string(body))
	if bodyText != "" {
		statusText += " body=" + bodyText
	}
	if err == nil {
		return statusText
	}
	return fmt.Sprintf("%s err=%v", statusText, err)
}

func formatNodeErrors(nodeErrors map[string]string) string {
	if len(nodeErrors) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(nodeErrors))
	for key := range nodeErrors {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s: %s", key, nodeErrors[key]))
	}
	return strings.Join(lines, "; ")
}

type managerSlotsResponse struct {
	Total int               `json:"total"`
	Items []managerSlotItem `json:"items"`
}

type managerSlotItem struct {
	SlotID     uint32                 `json:"slot_id"`
	Assignment managerSlotAssignment  `json:"assignment"`
	Task       *managerSlotTask       `json:"task,omitempty"`
	Runtime    managerSlotRuntime     `json:"runtime"`
	NodeLog    *managerSlotNodeStatus `json:"node_log,omitempty"`
}

type managerSlotAssignment struct {
	DesiredPeers []uint64 `json:"desired_peers"`
}

type managerSlotRuntime struct {
	CurrentVoters []uint64 `json:"current_voters"`
}

type managerSlotTask struct {
	TaskID     string `json:"task_id"`
	Kind       string `json:"kind"`
	Step       string `json:"step"`
	Status     string `json:"status"`
	SourceNode uint64 `json:"source_node,omitempty"`
	TargetNode uint64 `json:"target_node,omitempty"`
}

type managerSlotNodeStatus struct {
	NodeID       uint64 `json:"node_id"`
	LeaderID     uint64 `json:"leader_id"`
	Role         string `json:"role"`
	CommitIndex  uint64 `json:"commit_index"`
	AppliedIndex uint64 `json:"applied_index"`
}

type managerSlotLeaderTransferResponse struct {
	SlotID          uint32           `json:"slot_id"`
	TargetNode      uint64           `json:"target_node"`
	PreferredLeader uint64           `json:"preferred_leader"`
	ActualLeader    uint64           `json:"actual_leader"`
	Created         bool             `json:"created"`
	Task            *managerSlotTask `json:"task,omitempty"`
	Message         string           `json:"message"`
}
