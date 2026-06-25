//go:build e2e

package suite

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

	"github.com/stretchr/testify/require"
)

const managerPollInterval = 100 * time.Millisecond

// ManagerClient is a small public-manager HTTP client for e2ev2 scenarios.
type ManagerClient struct {
	baseURL string
	cluster *StartedCluster
	node    *StartedNode
}

// ManagerClient returns a manager HTTP client rooted at one started node.
func (c *StartedCluster) ManagerClient(t testing.TB, nodeID uint64) *ManagerClient {
	t.Helper()
	require.NotNil(t, c, "started cluster is nil")
	node := c.MustNode(nodeID)
	require.NotEmpty(t, node.Spec.ManagerAddr, "node %d manager HTTP is not enabled", nodeID)
	return &ManagerClient{
		baseURL: "http://" + node.Spec.ManagerAddr,
		cluster: c,
		node:    node,
	}
}

// MustSlots returns a stable manager Slot inventory after bootstrap tasks clear.
func (m *ManagerClient) MustSlots(t testing.TB) []SlotDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(managerPollInterval)
	defer ticker.Stop()

	var (
		lastResp managerSlotsResponse
		lastErr  error
	)
	for {
		var resp managerSlotsResponse
		_, err := GetJSON(ctx, m.baseURL+"/manager/slots", &resp)
		if err == nil {
			if checkErr := stableSlotInventory(resp); checkErr == nil {
				items := append([]SlotDTO(nil), resp.Items...)
				sort.Slice(items, func(i, j int) bool { return items[i].SlotID < items[j].SlotID })
				return items
			} else {
				lastResp = resp
				lastErr = checkErr
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("manager slots did not stabilize: last=%#v lastErr=%v\n%s", lastResp, lastErr, m.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

// EventuallyNodeJoinState waits until manager node inventory shows the requested lifecycle state.
func (m *ManagerClient) EventuallyNodeJoinState(t testing.TB, nodeID uint64, state string, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(managerPollInterval)
	defer ticker.Stop()

	var (
		lastResp managerNodesResponse
		lastErr  error
	)
	for {
		var resp managerNodesResponse
		_, err := GetJSON(ctx, m.baseURL+"/manager/nodes", &resp)
		if err == nil {
			lastResp = resp
			if node, ok := findManagerNode(resp, nodeID); ok {
				if node.Membership.JoinState == state {
					return
				}
				lastErr = fmt.Errorf("node %d join_state=%q, want %q", nodeID, node.Membership.JoinState, state)
			} else {
				lastErr = fmt.Errorf("node %d missing from manager inventory", nodeID)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d did not reach join_state=%q: last=%#v lastErr=%v\n%s", nodeID, state, lastResp, lastErr, m.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

// EventuallyNodeReadiness waits until the target node satisfies public app readiness.
func (m *ManagerClient) EventuallyNodeReadiness(t testing.TB, nodeID uint64, ready bool, timeout time.Duration) {
	t.Helper()

	node, ok := m.cluster.Node(nodeID)
	require.True(t, ok, "node %d not found in started cluster", nodeID)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if ready {
		observation, err := waitHTTPReadyDetailed(ctx, node.Spec.APIAddr, "/readyz")
		m.cluster.lastReadyz[nodeID] = observation
		require.NoError(t, err, m.cluster.DumpDiagnostics())
		return
	}

	if err := WaitHTTPReady(ctx, node.Spec.APIAddr, "/readyz"); err == nil {
		t.Fatalf("node %d unexpectedly became ready\n%s", nodeID, m.cluster.DumpDiagnostics())
	}
}

// MustActivateNode activates a joining node through manager HTTP.
func (m *ManagerClient) MustActivateNode(t testing.TB, nodeID uint64) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	ticker := time.NewTicker(managerPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		status, body, err := postJSONStatus(ctx, fmt.Sprintf("%s/manager/nodes/%d/activate", m.baseURL, nodeID), nil, nil)
		cancel()
		if err == nil && status/100 == 2 {
			return
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("activate node %d returned %d: %s", nodeID, status, strings.TrimSpace(string(body)))
			if status == http.StatusConflict {
				lastErr = fmt.Errorf("%w; local_tcp=%s", lastErr, m.localNodeTCPStatus(nodeID))
			}
			if status != http.StatusConflict && status != http.StatusServiceUnavailable {
				t.Fatalf("%v\n%s", lastErr, m.cluster.DumpDiagnostics())
			}
		}

		if time.Now().After(deadline) {
			t.Fatalf("activate node %d timed out: lastErr=%v\n%s", nodeID, lastErr, m.cluster.DumpDiagnostics())
		}
		select {
		case <-time.After(time.Until(deadline)):
			t.Fatalf("activate node %d timed out: lastErr=%v\n%s", nodeID, lastErr, m.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

// MustPlanOnboarding returns one bounded Slot onboarding preview through manager HTTP.
func (m *ManagerClient) MustPlanOnboarding(t testing.TB, nodeID uint64, maxSlotMoves uint32) NodeOnboardingPlanDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out NodeOnboardingPlanDTO
	status, body, err := postJSONStatus(ctx, fmt.Sprintf("%s/manager/nodes/%d/onboarding/plan", m.baseURL, nodeID), map[string]any{
		"max_slot_moves": maxSlotMoves,
	}, &out)
	if err != nil || status/100 != 2 {
		if err == nil {
			err = fmt.Errorf("plan onboarding node %d returned %d: %s", nodeID, status, strings.TrimSpace(string(body)))
		}
		t.Fatalf("%v\n%s", err, m.cluster.DumpDiagnostics())
	}
	return out
}

// MustStartOnboarding creates bounded Slot onboarding tasks through manager HTTP.
func (m *ManagerClient) MustStartOnboarding(t testing.TB, nodeID uint64, maxSlotMoves uint32) NodeOnboardingStartDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out NodeOnboardingStartDTO
	status, body, err := postJSONStatus(ctx, fmt.Sprintf("%s/manager/nodes/%d/onboarding/start", m.baseURL, nodeID), map[string]any{
		"max_slot_moves": maxSlotMoves,
	}, &out)
	if err != nil || status/100 != 2 {
		if err == nil {
			err = fmt.Errorf("start onboarding node %d returned %d: %s", nodeID, status, strings.TrimSpace(string(body)))
		}
		t.Fatalf("%v\n%s", err, m.cluster.DumpDiagnostics())
	}
	return out
}

// NodeOnboardingStatus returns the target node's active onboarding task status.
func (m *ManagerClient) NodeOnboardingStatus(ctx context.Context, nodeID uint64) (NodeOnboardingStatusDTO, error) {
	var out NodeOnboardingStatusDTO
	_, err := GetJSON(ctx, fmt.Sprintf("%s/manager/nodes/%d/onboarding/status", m.baseURL, nodeID), &out)
	if err != nil {
		return NodeOnboardingStatusDTO{}, err
	}
	return out, nil
}

// EventuallyOnboardingSafe waits until the target has no active onboarding tasks.
func (m *ManagerClient) EventuallyOnboardingSafe(t testing.TB, nodeID uint64, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(managerPollInterval)
	defer ticker.Stop()

	var (
		lastResp NodeOnboardingStatusDTO
		lastErr  error
	)
	for {
		resp, err := m.NodeOnboardingStatus(ctx, nodeID)
		if err == nil {
			lastResp = resp
			if resp.Summary.TotalActive == 0 {
				return
			}
			lastErr = fmt.Errorf("onboarding active=%d pending=%d running=%d failed=%d", resp.Summary.TotalActive, resp.Summary.Pending, resp.Summary.Running, resp.Summary.Failed)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d onboarding did not become safe: last=%#v lastErr=%v\n%s", nodeID, lastResp, lastErr, m.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

// MustStartScaleIn marks a data node leaving through manager HTTP.
func (m *ManagerClient) MustStartScaleIn(t testing.TB, nodeID uint64) NodeScaleInStartDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out NodeScaleInStartDTO
	status, body, err := postJSONStatus(ctx, fmt.Sprintf("%s/manager/nodes/%d/scale-in/start", m.baseURL, nodeID), nil, &out)
	if err != nil || status/100 != 2 {
		if err == nil {
			err = fmt.Errorf("start scale-in node %d returned %d: %s", nodeID, status, strings.TrimSpace(string(body)))
		}
		t.Fatalf("%v\n%s", err, m.cluster.DumpDiagnostics())
	}
	return out
}

// MustSetScaleInDrain sets gateway drain mode through manager HTTP.
func (m *ManagerClient) MustSetScaleInDrain(t testing.TB, nodeID uint64, drain bool) NodeScaleInDrainDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out NodeScaleInDrainDTO
	status, body, err := postJSONStatus(ctx, fmt.Sprintf("%s/manager/nodes/%d/scale-in/drain", m.baseURL, nodeID), map[string]any{
		"draining": drain,
	}, &out)
	if err != nil || status/100 != 2 {
		if err == nil {
			err = fmt.Errorf("set scale-in drain node %d returned %d: %s", nodeID, status, strings.TrimSpace(string(body)))
		}
		t.Fatalf("%v\n%s", err, m.cluster.DumpDiagnostics())
	}
	return out
}

// NodeScaleInStatus returns the target node scale-in status.
func (m *ManagerClient) NodeScaleInStatus(ctx context.Context, nodeID uint64) (NodeScaleInStatusDTO, error) {
	var out NodeScaleInStatusDTO
	_, err := GetJSON(ctx, fmt.Sprintf("%s/manager/nodes/%d/scale-in/status", m.baseURL, nodeID), &out)
	if err != nil {
		return NodeScaleInStatusDTO{}, err
	}
	return out, nil
}

// EventuallyScaleInSafeToRemove waits until scale-in status reports final removal safety.
func (m *ManagerClient) EventuallyScaleInSafeToRemove(t testing.TB, nodeID uint64, timeout time.Duration) NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(managerPollInterval)
	defer ticker.Stop()

	var (
		lastResp NodeScaleInStatusDTO
		lastErr  error
	)
	for {
		resp, err := m.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastResp = resp
			if resp.SafeToRemove {
				return resp
			}
			lastErr = fmt.Errorf("safe_to_remove=false status=%#v", resp)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in did not become safe to remove: last=%#v lastErr=%v\n%s", nodeID, lastResp, lastErr, m.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

// MustRemoveScaleInNode marks a fully drained node removed through manager HTTP.
func (m *ManagerClient) MustRemoveScaleInNode(t testing.TB, nodeID uint64) NodeScaleInRemoveDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out NodeScaleInRemoveDTO
	status, body, err := postJSONStatus(ctx, fmt.Sprintf("%s/manager/nodes/%d/scale-in/remove", m.baseURL, nodeID), nil, &out)
	if err != nil || status/100 != 2 {
		if err == nil {
			err = fmt.Errorf("remove scale-in node %d returned %d: %s", nodeID, status, strings.TrimSpace(string(body)))
		}
		t.Fatalf("%v\n%s", err, m.cluster.DumpDiagnostics())
	}
	return out
}

func (m *ManagerClient) localNodeTCPStatus(nodeID uint64) string {
	node, ok := m.cluster.Node(nodeID)
	if !ok {
		return "node_not_found"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := WaitTCPReady(ctx, node.Spec.ClusterAddr); err != nil {
		return err.Error()
	}
	return "ready"
}

// SameSlotAssignments reports whether two manager Slot inventories have the same desired assignments.
func SameSlotAssignments(a, b []SlotDTO) bool {
	left := append([]SlotDTO(nil), a...)
	right := append([]SlotDTO(nil), b...)
	sort.Slice(left, func(i, j int) bool { return left[i].SlotID < left[j].SlotID })
	sort.Slice(right, func(i, j int) bool { return right[i].SlotID < right[j].SlotID })
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].SlotID != right[i].SlotID {
			return false
		}
		if left[i].Assignment.PreferredLeaderID != right[i].Assignment.PreferredLeaderID ||
			left[i].Assignment.ConfigEpoch != right[i].Assignment.ConfigEpoch ||
			left[i].Assignment.BalanceVersion != right[i].Assignment.BalanceVersion {
			return false
		}
		if !slices.Equal(left[i].Assignment.DesiredPeers, right[i].Assignment.DesiredPeers) {
			return false
		}
	}
	return true
}

// SlotDTO is the manager-facing Slot row subset used by e2ev2 scenarios.
type SlotDTO struct {
	SlotID     uint32            `json:"slot_id"`
	Assignment SlotAssignmentDTO `json:"assignment"`
	Task       *SlotTaskDTO      `json:"task,omitempty"`
}

// SlotAssignmentDTO contains desired Slot placement fields returned by manager HTTP.
type SlotAssignmentDTO struct {
	DesiredPeers      []uint64 `json:"desired_peers"`
	PreferredLeaderID uint64   `json:"preferred_leader_id"`
	ConfigEpoch       uint64   `json:"config_epoch"`
	BalanceVersion    uint64   `json:"balance_version"`
}

// SlotTaskDTO is the active Slot task subset used only to wait for stable inventory.
type SlotTaskDTO struct {
	TaskID string `json:"task_id"`
	Kind   string `json:"kind"`
	Step   string `json:"step"`
	Status string `json:"status"`
}

type managerSlotsResponse struct {
	Total int       `json:"total"`
	Items []SlotDTO `json:"items"`
}

type managerNodesResponse struct {
	Total int              `json:"total"`
	Items []managerNodeDTO `json:"items"`
}

type managerNodeDTO struct {
	NodeID     uint64                   `json:"node_id"`
	Membership managerNodeMembershipDTO `json:"membership"`
}

type managerNodeMembershipDTO struct {
	JoinState string `json:"join_state"`
}

// NodeOnboardingPlanDTO is the manager onboarding preview subset used by e2ev2.
type NodeOnboardingPlanDTO struct {
	StateRevision uint64                         `json:"state_revision"`
	TargetNodeID  uint64                         `json:"target_node_id"`
	MaxSlotMoves  uint32                         `json:"max_slot_moves"`
	Candidates    []NodeOnboardingCandidateDTO   `json:"candidates"`
	Skipped       []NodeOnboardingSkippedSlotDTO `json:"skipped"`
}

// NodeOnboardingStartDTO is the manager onboarding start subset used by e2ev2.
type NodeOnboardingStartDTO struct {
	StateRevision uint64                         `json:"state_revision"`
	TargetNodeID  uint64                         `json:"target_node_id"`
	MaxSlotMoves  uint32                         `json:"max_slot_moves"`
	Created       uint32                         `json:"created"`
	Results       []NodeOnboardingTaskResultDTO  `json:"results"`
	Skipped       []NodeOnboardingSkippedSlotDTO `json:"skipped"`
}

// NodeOnboardingStatusDTO is the manager onboarding status subset used by e2ev2.
type NodeOnboardingStatusDTO struct {
	StateRevision uint64                        `json:"state_revision"`
	TargetNodeID  uint64                        `json:"target_node_id"`
	Summary       NodeOnboardingStatusSummary   `json:"summary"`
	Tasks         []NodeOnboardingStatusTaskDTO `json:"tasks"`
}

// NodeOnboardingCandidateDTO describes one Slot move preview.
type NodeOnboardingCandidateDTO struct {
	SlotID       uint32   `json:"slot_id"`
	SourceNodeID uint64   `json:"source_node_id"`
	TargetNodeID uint64   `json:"target_node_id"`
	TargetPeers  []uint64 `json:"target_peers"`
	ConfigEpoch  uint64   `json:"config_epoch"`
}

// NodeOnboardingSkippedSlotDTO describes one skipped Slot in onboarding responses.
type NodeOnboardingSkippedSlotDTO struct {
	SlotID  uint32 `json:"slot_id"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// NodeOnboardingTaskResultDTO describes one submitted onboarding task.
type NodeOnboardingTaskResultDTO struct {
	SlotID  uint32                       `json:"slot_id"`
	Created bool                         `json:"created"`
	Task    *NodeOnboardingStatusTaskDTO `json:"task,omitempty"`
}

// NodeOnboardingStatusSummary contains aggregate active onboarding task counts.
type NodeOnboardingStatusSummary struct {
	TotalActive int `json:"total_active"`
	Pending     int `json:"pending"`
	Running     int `json:"running"`
	Failed      int `json:"failed"`
}

// NodeOnboardingStatusTaskDTO is an active onboarding task row.
type NodeOnboardingStatusTaskDTO struct {
	TaskID      string   `json:"task_id"`
	SlotID      uint32   `json:"slot_id"`
	Kind        string   `json:"kind"`
	Step        string   `json:"step"`
	Status      string   `json:"status"`
	SourceNode  uint64   `json:"source_node"`
	TargetNode  uint64   `json:"target_node"`
	TargetPeers []uint64 `json:"target_peers"`
	ConfigEpoch uint64   `json:"config_epoch"`
}

// NodeScaleInStatusDTO is the manager scale-in status subset used by e2ev2.
type NodeScaleInStatusDTO struct {
	NodeID                  uint64 `json:"node_id"`
	JoinState               string `json:"join_state"`
	StateRevision           uint64 `json:"state_revision"`
	SafeToProceed           bool   `json:"safe_to_proceed"`
	SafeToRemove            bool   `json:"safe_to_remove"`
	BlockedBySlots          bool   `json:"blocked_by_slots"`
	BlockedByTasks          bool   `json:"blocked_by_tasks"`
	BlockedByChannels       bool   `json:"blocked_by_channels"`
	BlockedByRuntimeDrain   bool   `json:"blocked_by_runtime_drain"`
	RuntimeUnknown          bool   `json:"runtime_unknown"`
	UnknownChannelInventory bool   `json:"unknown_channel_inventory"`
	GatewayDraining         bool   `json:"gateway_draining"`
	AcceptingNewSessions    bool   `json:"accepting_new_sessions"`
	SlotReplicaCount        int    `json:"slot_replica_count"`
	ChannelLeaderCount      int    `json:"channel_leader_count"`
	ChannelReplicaCount     int    `json:"channel_replica_count"`
	ChannelISRCount         int    `json:"channel_isr_count"`
	GatewaySessions         int    `json:"gateway_sessions"`
	ActiveOnline            int    `json:"active_online"`
	ClosingOnline           int    `json:"closing_online"`
	TotalOnline             int    `json:"total_online"`
	PendingActivations      int    `json:"pending_activations"`
}

// NodeScaleInStartDTO is the manager leaving transition subset used by e2ev2.
type NodeScaleInStartDTO struct {
	Changed   bool   `json:"changed"`
	NodeID    uint64 `json:"node_id"`
	JoinState string `json:"join_state"`
	Revision  uint64 `json:"revision"`
}

// NodeScaleInDrainDTO is the manager gateway drain response subset used by e2ev2.
type NodeScaleInDrainDTO struct {
	NodeID               uint64 `json:"node_id"`
	Draining             bool   `json:"draining"`
	AcceptingNewSessions bool   `json:"accepting_new_sessions"`
	GatewaySessions      int    `json:"gateway_sessions"`
	ActiveOnline         int    `json:"active_online"`
	ClosingOnline        int    `json:"closing_online"`
	TotalOnline          int    `json:"total_online"`
	PendingActivations   int    `json:"pending_activations"`
	Unknown              bool   `json:"unknown"`
}

// NodeScaleInRemoveDTO is the manager removed transition subset used by e2ev2.
type NodeScaleInRemoveDTO struct {
	Changed   bool   `json:"changed"`
	NodeID    uint64 `json:"node_id"`
	JoinState string `json:"join_state"`
	Revision  uint64 `json:"revision"`
}

func stableSlotInventory(resp managerSlotsResponse) error {
	if resp.Total == 0 || len(resp.Items) == 0 {
		return fmt.Errorf("empty slot inventory total=%d items=%d", resp.Total, len(resp.Items))
	}
	if resp.Total != len(resp.Items) {
		return fmt.Errorf("slot inventory total=%d items=%d", resp.Total, len(resp.Items))
	}
	for _, item := range resp.Items {
		if item.Task != nil {
			return fmt.Errorf("slot %d still has active task %#v", item.SlotID, item.Task)
		}
		if len(item.Assignment.DesiredPeers) == 0 {
			return fmt.Errorf("slot %d has empty desired peers", item.SlotID)
		}
	}
	return nil
}

func findManagerNode(resp managerNodesResponse, nodeID uint64) (managerNodeDTO, bool) {
	for _, item := range resp.Items {
		if item.NodeID == nodeID {
			return item, true
		}
	}
	return managerNodeDTO{}, false
}

func postJSONStatus(ctx context.Context, url string, body any, out any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if out != nil && resp.StatusCode/100 == 2 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, respBody, fmt.Errorf("decode POST %s: %w body=%s", url, err, strings.TrimSpace(string(respBody)))
		}
	}
	return resp.StatusCode, respBody, nil
}
