//go:build e2e

package dynamic_node_faults

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestSlotReplicaMoveSurvivesDelayedLeaderTransfer(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-slot-move-token"
	node1Fail := suite.ReserveGofailEndpoint(t)
	node2Fail := suite.ReserveGofailEndpoint(t)
	node3Fail := suite.ReserveGofailEndpoint(t)
	node4Fail := suite.ReserveGofailEndpoint(t)
	failpoints := []suite.GofailEndpoint{node1Fail, node2Fail, node3Fail, node4Fail}

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeEnv(1, node1Fail.Env()),
		suite.WithNodeEnv(2, node2Fail.Env()),
		suite.WithNodeEnv(3, node3Fail.Env()),
		suite.WithNodeEnv(4, node4Fail.Env()),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

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

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	plan := manager.MustPlanOnboarding(t, 4, 1)
	require.Equal(t, uint64(4), plan.TargetNodeID, "plan=%#v\n%s", plan, cluster.DumpDiagnostics())
	require.Len(t, plan.Candidates, 1, cluster.DumpDiagnostics())
	candidate := plan.Candidates[0]
	require.Equal(t, uint64(4), candidate.TargetNodeID, "candidate=%#v\n%s", candidate, cluster.DumpDiagnostics())
	ensureSlotLeaderForReplicaMoveSource(t, cluster, candidate.SlotID, candidate.SourceNodeID, 45*time.Second)

	plan = manager.MustPlanOnboarding(t, 4, 1)
	require.Equal(t, uint64(4), plan.TargetNodeID, "replan=%#v\n%s", plan, cluster.DumpDiagnostics())
	require.Len(t, plan.Candidates, 1, cluster.DumpDiagnostics())
	replanCandidate := plan.Candidates[0]
	require.Equal(t, uint64(4), replanCandidate.TargetNodeID, "replan candidate=%#v\n%s", replanCandidate, cluster.DumpDiagnostics())
	require.Equal(t, candidate.SlotID, replanCandidate.SlotID, cluster.DumpDiagnostics())
	require.Equal(t, candidate.SourceNodeID, replanCandidate.SourceNodeID, cluster.DumpDiagnostics())
	ensureSlotLeaderForReplicaMoveSource(t, cluster, replanCandidate.SlotID, replanCandidate.SourceNodeID, 45*time.Second)

	for _, endpoint := range failpoints {
		listCtx, cancelList := context.WithTimeout(context.Background(), 10*time.Second)
		body, err := endpoint.WaitListed(listCtx, "wkSlotReplicaMoveTransferLeaderDelay")
		cancelList()
		require.NoError(t, err, "gofail body:\n%s\n%s", body, cluster.DumpDiagnostics())
	}

	for _, endpoint := range failpoints {
		failCtx, cancelFail := context.WithTimeout(context.Background(), 10*time.Second)
		err := endpoint.Enable(failCtx, "wkSlotReplicaMoveTransferLeaderDelay", `return("5s")`)
		cancelFail()
		require.NoError(t, err, cluster.DumpDiagnostics())
		defer func(endpoint suite.GofailEndpoint) {
			disableCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = endpoint.Disable(disableCtx, "wkSlotReplicaMoveTransferLeaderDelay")
		}(endpoint)
	}

	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, cluster.DumpDiagnostics())

	active := waitOnboardingTaskActive(t, cluster, manager, 4, 10*time.Second)
	require.Equal(t, 0, active.Summary.Failed, "active=%#v\n%s", active, cluster.DumpDiagnostics())
	waitGofailCountWhileOnboardingActive(t, cluster, manager, 4, failpoints, "wkSlotReplicaMoveTransferLeaderDelay", 1, 15*time.Second)
	manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)
	statusCtx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStatus()
	status, err := manager.NodeOnboardingStatus(statusCtx, 4)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, 0, status.Summary.Failed, "status=%#v\n%s", status, cluster.DumpDiagnostics())

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()
	require.NoError(t, sender.Connect(node4.GatewayAddr(), "gofail-slot-move-sender", "gofail-slot-move-device"), cluster.DumpDiagnostics())
}

func waitGofailCountWhileOnboardingActive(
	t testing.TB,
	cluster *suite.StartedCluster,
	manager *suite.ManagerClient,
	nodeID uint64,
	endpoints []suite.GofailEndpoint,
	failpoint string,
	min int,
	timeout time.Duration,
) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeOnboardingStatusDTO
		lastCounts []string
		lastErr    error
	)
	for {
		total := 0
		counts := make([]string, 0, len(endpoints))
		for _, endpoint := range endpoints {
			countCtx, countCancel := context.WithTimeout(ctx, 2*time.Second)
			count, err := endpoint.Count(countCtx, failpoint)
			countCancel()
			if err != nil {
				lastErr = err
				continue
			}
			total += count
			counts = append(counts, fmt.Sprintf("%s=%d", endpoint.Addr, count))
		}
		lastCounts = counts

		statusCtx, statusCancel := context.WithTimeout(ctx, 2*time.Second)
		status, err := manager.NodeOnboardingStatus(statusCtx, nodeID)
		statusCancel()
		if err != nil {
			lastErr = err
		} else {
			lastStatus = status
			if status.Summary.Failed > 0 {
				t.Fatalf("node %d onboarding failed before %s active proof was observed: counts=%v status=%s\n%s",
					nodeID,
					failpoint,
					counts,
					formatOnboardingStatus(status),
					cluster.DumpDiagnostics(),
				)
			}
			active := status.Summary.TotalActive > 0 &&
				(status.Summary.Pending > 0 || status.Summary.Running > 0 || len(status.Tasks) > 0)
			if total >= min && active {
				return total
			}
			if total >= min && !active {
				lastErr = fmt.Errorf("failpoint observed after onboarding task stopped being active: counts=%v status=%s", counts, formatOnboardingStatus(status))
			} else {
				lastErr = fmt.Errorf("waiting for %s count while onboarding active: counts=%v status=%s", failpoint, counts, formatOnboardingStatus(status))
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("failpoint %s was not observed while onboarding was active: counts=%v lastStatus=%s lastErr=%v\n%s",
				failpoint,
				lastCounts,
				formatOnboardingStatus(lastStatus),
				lastErr,
				cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func ensureSlotLeaderForReplicaMoveSource(t *testing.T, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64, timeout time.Duration) {
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

func postSlotLeaderTransferToSource(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64) managerSlotLeaderTransferResponse {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	req := map[string]any{
		"target_node": sourceNodeID,
	}
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
			t.Fatalf("no manager node accepted slot leader transfer slot=%d source=%d req=%#v errors=%s\n%s", slotID, sourceNodeID, req, formatNodeErrors(nodeErrors), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func waitSlotLeaderOnSource(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64, transferResp *managerSlotLeaderTransferResponse) {
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
	NodeLog    *managerSlotNodeStatus `json:"node_log,omitempty"`
}

type managerSlotAssignment struct {
	DesiredPeers []uint64 `json:"desired_peers"`
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
