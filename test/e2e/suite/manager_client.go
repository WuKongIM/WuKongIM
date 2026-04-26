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
	"strings"
	"time"
)

// SlotTopology describes the externally observed slot leader/follower layout.
type SlotTopology struct {
	SlotID          uint32
	LeaderNodeID    uint64
	FollowerNodeIDs []uint64
	RawBody         string
}

// ManagerSlotDetail mirrors the subset of the manager slot detail response used by e2e.
type ManagerSlotDetail struct {
	SlotID  uint32           `json:"slot_id"`
	State   managerSlotState `json:"state"`
	Runtime managerSlotRun   `json:"runtime"`
}

type managerSlotState struct {
	Quorum string `json:"quorum"`
	Sync   string `json:"sync"`
}

type managerSlotRun struct {
	LeaderID     uint64   `json:"leader_id"`
	CurrentPeers []uint64 `json:"current_peers"`
	HasQuorum    bool     `json:"has_quorum"`
}

type managerConnectionsResponse struct {
	Items []ManagerConnection `json:"items"`
}

// ManagerNodesResponse mirrors the manager node list response used by e2e.
type ManagerNodesResponse struct {
	Total int           `json:"total"`
	Items []ManagerNode `json:"items"`
}

// ManagerNode mirrors the manager node fields used by e2e assertions.
type ManagerNode struct {
	NodeID          uint64                `json:"node_id"`
	Addr            string                `json:"addr"`
	Status          string                `json:"status"`
	LastHeartbeatAt time.Time             `json:"last_heartbeat_at"`
	IsLocal         bool                  `json:"is_local"`
	CapacityWeight  int                   `json:"capacity_weight"`
	Controller      ManagerNodeController `json:"controller"`
	SlotStats       ManagerNodeSlotStats  `json:"slot_stats"`
}

// ManagerNodeController contains controller role fields from /manager/nodes.
type ManagerNodeController struct {
	Role string `json:"role"`
}

// ManagerNodeSlotStats contains slot hosting counts from /manager/nodes.
type ManagerNodeSlotStats struct {
	Count       int `json:"count"`
	LeaderCount int `json:"leader_count"`
}

// ManagerNodeOnboardingCandidatesResponse mirrors the manager onboarding candidate list.
type ManagerNodeOnboardingCandidatesResponse struct {
	Total int                              `json:"total"`
	Items []ManagerNodeOnboardingCandidate `json:"items"`
}

// ManagerNodeOnboardingCandidate mirrors the manager onboarding candidate node DTO.
type ManagerNodeOnboardingCandidate struct {
	NodeID      uint64 `json:"node_id"`
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	Role        string `json:"role"`
	JoinState   string `json:"join_state"`
	Status      string `json:"status"`
	SlotCount   int    `json:"slot_count"`
	LeaderCount int    `json:"leader_count"`
	Recommended bool   `json:"recommended"`
}

// ManagerNodeOnboardingJob mirrors the manager onboarding job DTO used by e2e.
type ManagerNodeOnboardingJob struct {
	JobID        string                      `json:"job_id"`
	TargetNodeID uint64                      `json:"target_node_id"`
	Status       string                      `json:"status"`
	Plan         ManagerNodeOnboardingPlan   `json:"plan"`
	Moves        []ManagerNodeOnboardingMove `json:"moves"`
	ResultCounts ManagerNodeOnboardingCounts `json:"result_counts"`
	LastError    string                      `json:"last_error"`
}

// ManagerNodeOnboardingPlan mirrors the reviewed onboarding plan.
type ManagerNodeOnboardingPlan struct {
	TargetNodeID   uint64                               `json:"target_node_id"`
	Summary        ManagerNodeOnboardingPlanSummary     `json:"summary"`
	Moves          []ManagerNodeOnboardingPlanMove      `json:"moves"`
	BlockedReasons []ManagerNodeOnboardingBlockedReason `json:"blocked_reasons"`
}

// ManagerNodeOnboardingPlanSummary mirrors aggregate plan load effects.
type ManagerNodeOnboardingPlanSummary struct {
	CurrentTargetSlotCount   int `json:"current_target_slot_count"`
	PlannedTargetSlotCount   int `json:"planned_target_slot_count"`
	CurrentTargetLeaderCount int `json:"current_target_leader_count"`
	PlannedLeaderGain        int `json:"planned_leader_gain"`
}

// ManagerNodeOnboardingPlanMove mirrors one planned Slot move.
type ManagerNodeOnboardingPlanMove struct {
	SlotID                 uint32   `json:"slot_id"`
	SourceNodeID           uint64   `json:"source_node_id"`
	TargetNodeID           uint64   `json:"target_node_id"`
	DesiredPeersBefore     []uint64 `json:"desired_peers_before"`
	DesiredPeersAfter      []uint64 `json:"desired_peers_after"`
	LeaderTransferRequired bool     `json:"leader_transfer_required"`
}

// ManagerNodeOnboardingBlockedReason mirrors one planner blocked reason.
type ManagerNodeOnboardingBlockedReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ManagerNodeOnboardingMove mirrors one durable execution move.
type ManagerNodeOnboardingMove struct {
	SlotID    uint32 `json:"slot_id"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
}

// ManagerNodeOnboardingCounts mirrors job result counters.
type ManagerNodeOnboardingCounts struct {
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

// ManagerConnection mirrors the subset of the manager connections response used by e2e.
type ManagerConnection struct {
	UID string `json:"uid"`
}

// FetchNodeOnboardingCandidates fetches manager onboarding candidates from the started node.
func FetchNodeOnboardingCandidates(ctx context.Context, node StartedNode) (ManagerNodeOnboardingCandidatesResponse, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, "/manager/node-onboarding/candidates")
	if err != nil {
		return ManagerNodeOnboardingCandidatesResponse{}, nil, err
	}

	resp, err := decodeNodeOnboardingCandidatesResponse(body)
	if err != nil {
		return ManagerNodeOnboardingCandidatesResponse{}, body, err
	}
	return resp, body, nil
}

// CreateNodeOnboardingPlan creates a manager-reviewed onboarding plan for targetNodeID.
func CreateNodeOnboardingPlan(ctx context.Context, node StartedNode, targetNodeID uint64) (ManagerNodeOnboardingJob, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.ManagerAddr, "/manager/node-onboarding/plan", map[string]uint64{"target_node_id": targetNodeID})
	if err != nil {
		return ManagerNodeOnboardingJob{}, nil, err
	}

	job, err := decodeNodeOnboardingJobResponse(body)
	if err != nil {
		return ManagerNodeOnboardingJob{}, body, err
	}
	return job, body, nil
}

// StartNodeOnboardingJob starts a planned onboarding job through the manager API.
func StartNodeOnboardingJob(ctx context.Context, node StartedNode, jobID string) (ManagerNodeOnboardingJob, []byte, error) {
	body, err := postHTTPJSONBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/node-onboarding/jobs/%s/start", jobID), nil)
	if err != nil {
		return ManagerNodeOnboardingJob{}, nil, err
	}

	job, err := decodeNodeOnboardingJobResponse(body)
	if err != nil {
		return ManagerNodeOnboardingJob{}, body, err
	}
	return job, body, nil
}

// FetchNodeOnboardingJob fetches one onboarding job through the manager API.
func FetchNodeOnboardingJob(ctx context.Context, node StartedNode, jobID string) (ManagerNodeOnboardingJob, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/node-onboarding/jobs/%s", jobID))
	if err != nil {
		return ManagerNodeOnboardingJob{}, nil, err
	}

	job, err := decodeNodeOnboardingJobResponse(body)
	if err != nil {
		return ManagerNodeOnboardingJob{}, body, err
	}
	return job, body, nil
}

// WaitForNodeOnboardingJob waits until a manager onboarding job satisfies accept.
func WaitForNodeOnboardingJob(ctx context.Context, node StartedNode, jobID string, accept func(ManagerNodeOnboardingJob) bool) (ManagerNodeOnboardingJob, []byte, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var (
		lastErr  error
		lastBody []byte
	)
	for {
		job, body, err := FetchNodeOnboardingJob(ctx, node, jobID)
		if err == nil {
			lastBody = body
			if accept == nil || accept(job) {
				return job, body, nil
			}
			lastErr = fmt.Errorf("onboarding job %s did not satisfy expected state", jobID)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return ManagerNodeOnboardingJob{}, lastBody, lastErr
			}
			return ManagerNodeOnboardingJob{}, lastBody, ctx.Err()
		case <-ticker.C:
		}
	}
}

func decodeNodeOnboardingCandidatesResponse(body []byte) (ManagerNodeOnboardingCandidatesResponse, error) {
	var resp ManagerNodeOnboardingCandidatesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ManagerNodeOnboardingCandidatesResponse{}, err
	}
	return resp, nil
}

func decodeNodeOnboardingJobResponse(body []byte) (ManagerNodeOnboardingJob, error) {
	var job ManagerNodeOnboardingJob
	if err := json.Unmarshal(body, &job); err != nil {
		return ManagerNodeOnboardingJob{}, err
	}
	return job, nil
}

// FetchSlotDetail fetches one manager slot detail from the started node.
func FetchSlotDetail(ctx context.Context, node StartedNode, slotID uint32) (ManagerSlotDetail, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, fmt.Sprintf("/manager/slots/%d", slotID))
	if err != nil {
		return ManagerSlotDetail{}, nil, err
	}

	var detail ManagerSlotDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return ManagerSlotDetail{}, body, err
	}
	return detail, body, nil
}

// FetchConnections fetches local manager connections from the started node.
func FetchConnections(ctx context.Context, node StartedNode) ([]ManagerConnection, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, "/manager/connections")
	if err != nil {
		return nil, nil, err
	}

	var resp managerConnectionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, body, err
	}
	return resp.Items, body, nil
}

// FetchNodes fetches the manager node list from the started node.
func FetchNodes(ctx context.Context, node StartedNode) ([]ManagerNode, []byte, error) {
	body, err := fetchHTTPBody(ctx, node.Spec.ManagerAddr, "/manager/nodes")
	if err != nil {
		return nil, nil, err
	}

	resp, err := decodeManagerNodesResponse(body)
	if err != nil {
		return nil, body, err
	}
	return resp.Items, body, nil
}

func decodeManagerNodesResponse(body []byte) (ManagerNodesResponse, error) {
	var resp ManagerNodesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ManagerNodesResponse{}, err
	}
	return resp, nil
}

func managerNodeByID(items []ManagerNode, nodeID uint64) (ManagerNode, bool) {
	for _, item := range items {
		if item.NodeID == nodeID {
			return item, true
		}
	}
	return ManagerNode{}, false
}

// WaitForManagerNode waits until /manager/nodes contains a node accepted by the predicate.
func WaitForManagerNode(ctx context.Context, node StartedNode, nodeID uint64, accept func(ManagerNode) bool) (ManagerNode, []byte, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var (
		lastErr  error
		lastBody []byte
	)
	for {
		items, body, err := FetchNodes(ctx, node)
		if err == nil {
			lastBody = body
			managerNode, ok := managerNodeByID(items, nodeID)
			if ok && (accept == nil || accept(managerNode)) {
				return managerNode, body, nil
			}
			if ok {
				lastErr = fmt.Errorf("manager node %d did not satisfy expected state", nodeID)
			} else {
				lastErr = fmt.Errorf("manager node %d not present", nodeID)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return ManagerNode{}, lastBody, lastErr
			}
			return ManagerNode{}, lastBody, ctx.Err()
		case <-ticker.C:
		}
	}
}

// ResolveSlotTopology resolves the externally observed slot topology for one managed slot.
func (c *StartedCluster) ResolveSlotTopology(ctx context.Context, slotID uint32) (SlotTopology, error) {
	if c == nil {
		return SlotTopology{}, fmt.Errorf("started cluster is nil")
	}

	expectedNodeIDs := make([]uint64, 0, len(c.Nodes))
	for _, node := range c.Nodes {
		expectedNodeIDs = append(expectedNodeIDs, node.Spec.ID)
	}

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		for _, node := range c.Nodes {
			_, body, err := FetchSlotDetail(ctx, node, slotID)
			if err != nil {
				lastErr = err
				continue
			}
			c.lastSlotBodies[slotID] = string(body)
			topology, err := parseSlotTopology(slotID, expectedNodeIDs, body)
			if err == nil {
				return topology, nil
			}
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = fmt.Errorf("slot %d topology unavailable", slotID)
			}
			return SlotTopology{}, lastErr
		case <-ticker.C:
		}
	}
}

// WaitForSlotLeaderChange waits until the slot reports a new leader with quorum.
func (c *StartedCluster) WaitForSlotLeaderChange(ctx context.Context, slotID uint32, previousLeaderID uint64) (SlotTopology, error) {
	if c == nil {
		return SlotTopology{}, fmt.Errorf("started cluster is nil")
	}

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		for _, node := range c.Nodes {
			if node.Process == nil {
				continue
			}

			detail, body, err := FetchSlotDetail(ctx, node, slotID)
			if err != nil {
				lastErr = err
				continue
			}
			c.lastSlotBodies[slotID] = string(body)
			if detail.SlotID != slotID {
				lastErr = fmt.Errorf("slot %d leader change: got slot %d", slotID, detail.SlotID)
				continue
			}
			if detail.Runtime.LeaderID == 0 {
				lastErr = fmt.Errorf("slot %d leader change: missing leader", slotID)
				continue
			}
			if detail.Runtime.LeaderID == previousLeaderID {
				lastErr = fmt.Errorf("slot %d leader change: leader still %d", slotID, previousLeaderID)
				continue
			}
			if !detail.Runtime.HasQuorum {
				lastErr = fmt.Errorf("slot %d leader change: quorum not ready", slotID)
				continue
			}

			followers := make([]uint64, 0, len(detail.Runtime.CurrentPeers))
			for _, nodeID := range detail.Runtime.CurrentPeers {
				if nodeID != detail.Runtime.LeaderID {
					followers = append(followers, nodeID)
				}
			}
			return SlotTopology{
				SlotID:          detail.SlotID,
				LeaderNodeID:    detail.Runtime.LeaderID,
				FollowerNodeIDs: followers,
				RawBody:         string(body),
			}, nil
		}

		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = fmt.Errorf("slot %d leader change unavailable", slotID)
			}
			return SlotTopology{}, lastErr
		case <-ticker.C:
		}
	}
}

func fetchHTTPBody(ctx context.Context, addr, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manager endpoint %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func postHTTPJSONBody(ctx context.Context, addr, path string, payload any) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manager endpoint %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

func parseSlotTopology(slotID uint32, expectedNodeIDs []uint64, body []byte) (SlotTopology, error) {
	var detail ManagerSlotDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return SlotTopology{}, err
	}
	if detail.SlotID != slotID {
		return SlotTopology{}, fmt.Errorf("slot topology: got slot %d, want %d", detail.SlotID, slotID)
	}
	if detail.State.Quorum != "ready" {
		return SlotTopology{}, fmt.Errorf("slot topology: quorum=%s", detail.State.Quorum)
	}
	if detail.State.Sync != "matched" {
		return SlotTopology{}, fmt.Errorf("slot topology: sync=%s", detail.State.Sync)
	}
	if detail.Runtime.LeaderID == 0 {
		return SlotTopology{}, fmt.Errorf("slot topology: missing leader")
	}
	if !sameNodeSet(detail.Runtime.CurrentPeers, expectedNodeIDs) {
		return SlotTopology{}, fmt.Errorf("slot topology: peers=%v want=%v", detail.Runtime.CurrentPeers, expectedNodeIDs)
	}
	if !slices.Contains(detail.Runtime.CurrentPeers, detail.Runtime.LeaderID) {
		return SlotTopology{}, fmt.Errorf("slot topology: leader %d not in peers", detail.Runtime.LeaderID)
	}

	followers := make([]uint64, 0, len(detail.Runtime.CurrentPeers)-1)
	for _, nodeID := range detail.Runtime.CurrentPeers {
		if nodeID != detail.Runtime.LeaderID {
			followers = append(followers, nodeID)
		}
	}

	return SlotTopology{
		SlotID:          detail.SlotID,
		LeaderNodeID:    detail.Runtime.LeaderID,
		FollowerNodeIDs: followers,
		RawBody:         string(body),
	}, nil
}

func connectionsContainUID(items []ManagerConnection, uid string) bool {
	for _, item := range items {
		if item.UID == uid {
			return true
		}
	}
	return false
}

// ConnectionsContainUID waits until the node reports a local manager connection for the UID.
func ConnectionsContainUID(ctx context.Context, node StartedNode, uid string) (bool, error) {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		items, _, err := FetchConnections(ctx, node)
		if err == nil {
			if connectionsContainUID(items, uid) {
				return true, nil
			}
			lastErr = fmt.Errorf("uid %s not present in manager connections", uid)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return false, lastErr
			}
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}

func sameNodeSet(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}

	leftCopy := append([]uint64(nil), left...)
	rightCopy := append([]uint64(nil), right...)
	slices.Sort(leftCopy)
	slices.Sort(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}
