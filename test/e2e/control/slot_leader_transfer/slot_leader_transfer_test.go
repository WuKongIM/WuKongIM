//go:build e2e

package slot_leader_transfer

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

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeSlotLeaderTransferCompletesAndClearsTask(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(suite.WithManagerHTTP())

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitHTTPReady(readyCtx), cluster.DumpDiagnostics())

	initialCtx, cancelInitial := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelInitial()
	initial := requireSlotsReady(t, initialCtx, cluster, cluster.MustNode(1))

	slotID, source, target := chooseTransfer(t, initial)
	require.NotEqual(t, source, target)

	postCtx, cancelPost := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelPost()
	accepted := postLeaderTransfer(t, postCtx, cluster, slotID, target)
	require.True(t, accepted.Created, "same-target no-op or existing task was not expected: %#v", accepted)
	require.Equal(t, slotID, accepted.SlotID)
	require.Equal(t, target, accepted.TargetNode)
	require.NotZero(t, accepted.ActualLeader)

	finalCtx, cancelFinal := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFinal()
	final := requireLeaderMoved(t, finalCtx, cluster, cluster.MustNode(1), slotID, accepted.ActualLeader)

	require.NotNil(t, final.NodeLog)
	require.NotZero(t, final.NodeLog.LeaderID)
	require.NotEqual(t, accepted.ActualLeader, final.NodeLog.LeaderID)
	require.Contains(t, final.Assignment.DesiredPeers, final.NodeLog.LeaderID)
}

func TestThreeNodeSlotLeaderTransferBatchPlanExecuteCompletesAndClearsTask(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(suite.WithManagerHTTP())

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitHTTPReady(readyCtx), cluster.DumpDiagnostics())

	initialCtx, cancelInitial := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelInitial()
	initial := requireSlotsReady(t, initialCtx, cluster, cluster.MustNode(1))

	slotID, source, target := chooseTransfer(t, initial)
	require.NotEqual(t, source, target)
	req := map[string]any{
		"source_node_id": source,
		"target_node_id": target,
		"slot_ids":       []uint32{slotID},
		"max_tasks":      1,
		"target_policy":  "least_leaders",
	}

	planCtx, cancelPlan := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelPlan()
	plan := postLeaderTransferBatchPlan(t, planCtx, cluster, req)
	require.NotZero(t, plan.StateRevision)
	require.NotEmpty(t, plan.PlanID)
	require.Equal(t, 1, plan.Summary.Candidates)
	require.Equal(t, 1, plan.Summary.WouldCreate)
	require.Len(t, plan.Candidates, 1)
	candidate := plan.Candidates[0]
	require.Equal(t, slotID, candidate.SlotID)
	require.Equal(t, source, candidate.SourceNodeID)
	require.Equal(t, target, candidate.TargetNodeID)
	require.NotZero(t, candidate.ActualLeader)
	require.Equal(t, "create", candidate.Action)
	require.Contains(t, candidate.DesiredPeers, source)
	require.Contains(t, candidate.DesiredPeers, target)
	require.Contains(t, candidate.DesiredPeers, candidate.ActualLeader)

	req["state_revision"] = plan.StateRevision
	req["plan_id"] = plan.PlanID
	executeCtx, cancelExecute := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelExecute()
	executed := postLeaderTransferBatchExecute(t, executeCtx, cluster, req)
	require.Equal(t, plan.StateRevision, executed.StateRevision)
	require.Equal(t, plan.PlanID, executed.PlanID)
	require.Equal(t, 1, executed.Summary.Requested)
	require.Equal(t, 1, executed.Summary.Created)
	require.Equal(t, 0, executed.Summary.Failed)
	require.Len(t, executed.Results, 1)
	require.Equal(t, slotID, executed.Results[0].SlotID)
	require.Equal(t, "created", executed.Results[0].Status)
	require.NotEmpty(t, executed.Results[0].TaskID)

	finalCtx, cancelFinal := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFinal()
	final := requireLeaderMoved(t, finalCtx, cluster, cluster.MustNode(1), slotID, source)

	require.NotNil(t, final.NodeLog)
	require.NotZero(t, final.NodeLog.LeaderID)
	require.NotEqual(t, source, final.NodeLog.LeaderID)
	require.Contains(t, final.Assignment.DesiredPeers, final.NodeLog.LeaderID)
}

func requireSlotsReady(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, node *suite.StartedNode) managerSlotsResponse {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last managerSlotsResponse
	var lastErr error
	for {
		var out managerSlotsResponse
		_, err := suite.GetJSON(ctx, fmt.Sprintf("http://%s/manager/slots?node_id=%d", node.ManagerAddr(), node.Spec.ID), &out)
		if err == nil {
			if checkErr := checkSlotsReady(out); checkErr == nil {
				return out
			} else {
				last = out
				lastErr = checkErr
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("manager slots did not become ready: last=%#v lastErr=%v\n%s", last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func checkSlotsReady(resp managerSlotsResponse) error {
	if resp.Total != 3 || len(resp.Items) != 3 {
		return fmt.Errorf("manager slots total=%d items=%d, want 3", resp.Total, len(resp.Items))
	}
	for _, item := range resp.Items {
		if item.Task != nil {
			return fmt.Errorf("slot %d active task=%#v", item.SlotID, item.Task)
		}
		if len(item.Assignment.DesiredPeers) < 2 {
			return fmt.Errorf("slot %d desired peers=%v, want at least 2", item.SlotID, item.Assignment.DesiredPeers)
		}
		if item.NodeLog == nil {
			return fmt.Errorf("slot %d node log status missing", item.SlotID)
		}
		if item.NodeLog.LeaderID == 0 {
			return fmt.Errorf("slot %d node log leader missing: %#v", item.SlotID, item.NodeLog)
		}
		if !containsUint64(item.Assignment.DesiredPeers, item.NodeLog.LeaderID) {
			return fmt.Errorf("slot %d leader %d not in desired peers %v", item.SlotID, item.NodeLog.LeaderID, item.Assignment.DesiredPeers)
		}
	}
	return nil
}

func chooseTransfer(t *testing.T, slots managerSlotsResponse) (uint32, uint64, uint64) {
	t.Helper()

	for _, item := range slots.Items {
		if item.NodeLog == nil || item.NodeLog.LeaderID == 0 {
			continue
		}
		source := item.NodeLog.LeaderID
		for _, peer := range item.Assignment.DesiredPeers {
			if peer != source {
				return item.SlotID, source, peer
			}
		}
	}
	t.Fatalf("no transferable slot found in manager slots response: %#v", slots)
	return 0, 0, 0
}

func postLeaderTransferBatchPlan(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, req map[string]any) managerSlotLeaderTransferBatchPlanResponse {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	nodeErrors := make(map[string]string)
	for {
		for _, node := range cluster.Nodes {
			var out managerSlotLeaderTransferBatchPlanResponse
			status, _, err := postManagerJSON(ctx, fmt.Sprintf("http://%s/manager/slots/leader-transfer-plan", node.ManagerAddr()), req, &out)
			if err == nil && status == http.StatusOK {
				return out
			}
			nodeErrors[managerNodeErrorKey(node)] = formatManagerPostError(status, err)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("no manager node returned slot leader-transfer batch plan req=%#v errors=%s\n%s", req, formatNodeErrors(nodeErrors), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func postLeaderTransferBatchExecute(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, req map[string]any) managerSlotLeaderTransferBatchExecuteResponse {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	nodeErrors := make(map[string]string)
	for {
		for _, node := range cluster.Nodes {
			var out managerSlotLeaderTransferBatchExecuteResponse
			status, _, err := postManagerJSON(ctx, fmt.Sprintf("http://%s/manager/slots/leader-transfer-batch", node.ManagerAddr()), req, &out)
			if err == nil && status == http.StatusAccepted {
				return out
			}
			nodeErrors[managerNodeErrorKey(node)] = formatManagerPostError(status, err)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("no manager node accepted slot leader-transfer batch req=%#v errors=%s\n%s", req, formatNodeErrors(nodeErrors), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
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

func formatManagerPostError(status int, err error) string {
	if err == nil {
		return fmt.Sprintf("unexpected status %d", status)
	}
	return err.Error()
}

func postLeaderTransfer(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, slotID uint32, target uint64) managerSlotLeaderTransferResponse {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	nodeErrors := make(map[string]string)
	for {
		for _, node := range cluster.Nodes {
			var out managerSlotLeaderTransferResponse
			_, err := suite.PostJSON(ctx, fmt.Sprintf("http://%s/manager/slots/%d/leader-transfer", node.ManagerAddr(), slotID), map[string]any{
				"target_node": target,
			}, &out)
			if err == nil {
				return out
			}
			nodeErrors[managerNodeErrorKey(node)] = err.Error()
		}

		select {
		case <-ctx.Done():
			t.Fatalf("no manager node accepted slot leader transfer slot=%d target=%d errors=%s\n%s", slotID, target, formatNodeErrors(nodeErrors), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func managerNodeErrorKey(node suite.StartedNode) string {
	return fmt.Sprintf("node %d (%s)", node.Spec.ID, node.ManagerAddr())
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

func requireLeaderMoved(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, node *suite.StartedNode, slotID uint32, source uint64) managerSlotItem {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last managerSlotItem
	var lastErr error
	for {
		var out managerSlotsResponse
		_, err := suite.GetJSON(ctx, fmt.Sprintf("http://%s/manager/slots?node_id=%d", node.ManagerAddr(), node.Spec.ID), &out)
		if err == nil {
			item, ok := findSlot(out.Items, slotID)
			if !ok {
				lastErr = fmt.Errorf("slot %d missing from response: %#v", slotID, out.Items)
			} else {
				last = item
				if checkLeaderMoved(item, source) == nil {
					return item
				}
				lastErr = checkLeaderMoved(item, source)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("slot leader did not move from source %d: last=%#v lastErr=%v\n%s", source, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func checkLeaderMoved(item managerSlotItem, source uint64) error {
	if item.Task != nil {
		return fmt.Errorf("slot %d still has active task: %#v", item.SlotID, item.Task)
	}
	if item.NodeLog == nil {
		return fmt.Errorf("slot %d node log status missing", item.SlotID)
	}
	if item.NodeLog.LeaderID == 0 {
		return fmt.Errorf("slot %d leader missing: %#v", item.SlotID, item.NodeLog)
	}
	if item.NodeLog.LeaderID == source {
		return fmt.Errorf("slot %d leader still source %d", item.SlotID, source)
	}
	if !containsUint64(item.Assignment.DesiredPeers, item.NodeLog.LeaderID) {
		return fmt.Errorf("slot %d leader %d not in desired peers %v", item.SlotID, item.NodeLog.LeaderID, item.Assignment.DesiredPeers)
	}
	return nil
}

func findSlot(items []managerSlotItem, slotID uint32) (managerSlotItem, bool) {
	for _, item := range items {
		if item.SlotID == slotID {
			return item, true
		}
	}
	return managerSlotItem{}, false
}

func containsUint64(items []uint64, target uint64) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
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

type managerSlotLeaderTransferBatchPlanResponse struct {
	StateRevision uint64                                    `json:"state_revision"`
	PlanID        string                                    `json:"plan_id"`
	Summary       managerSlotLeaderTransferBatchPlanSummary `json:"summary"`
	Candidates    []managerSlotLeaderTransferBatchCandidate `json:"candidates"`
}

type managerSlotLeaderTransferBatchPlanSummary struct {
	Candidates  int `json:"candidates"`
	WouldCreate int `json:"would_create"`
}

type managerSlotLeaderTransferBatchCandidate struct {
	SlotID       uint32   `json:"slot_id"`
	SourceNodeID uint64   `json:"source_node_id"`
	TargetNodeID uint64   `json:"target_node_id"`
	ActualLeader uint64   `json:"actual_leader"`
	DesiredPeers []uint64 `json:"desired_peers"`
	Action       string   `json:"action"`
}

type managerSlotLeaderTransferBatchExecuteResponse struct {
	StateRevision uint64                                       `json:"state_revision"`
	PlanID        string                                       `json:"plan_id"`
	Summary       managerSlotLeaderTransferBatchExecuteSummary `json:"summary"`
	Results       []managerSlotLeaderTransferBatchResult       `json:"results"`
}

type managerSlotLeaderTransferBatchExecuteSummary struct {
	Requested int `json:"requested"`
	Created   int `json:"created"`
	Failed    int `json:"failed"`
}

type managerSlotLeaderTransferBatchResult struct {
	SlotID uint32 `json:"slot_id"`
	Status string `json:"status"`
	TaskID string `json:"task_id"`
}
