//go:build e2e

package suite

import (
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

// ManagerConnection mirrors the subset of the manager connections response used by e2e.
type ManagerConnection struct {
	UID string `json:"uid"`
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
