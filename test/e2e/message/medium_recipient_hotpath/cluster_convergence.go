//go:build e2e

package medium_recipient_hotpath

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

const (
	mediumConvergenceStableWindow = 2 * time.Second
)

// mediumSlotsResponse is the manager Slot inventory used to prove that every
// node observes the same actual Raft leaders before the workload starts.
type mediumSlotsResponse struct {
	Total int             `json:"total"`
	Items []suite.SlotDTO `json:"items"`
}

// mediumSlotConvergence captures the stable actual Raft leader inventory and
// the bounded setup time required to observe it.
type mediumSlotConvergence struct {
	Leaders        []uint64
	Fingerprint    string
	WaitDuration   time.Duration
	StableDuration time.Duration
}

// mediumConvergenceSnapshot is one fully validated cross-node Slot
// observation. Leaders are ordered by logical Slot ID.
type mediumConvergenceSnapshot struct {
	Leaders     []uint64
	Fingerprint string
}

func waitForMediumSlotConvergence(ctx context.Context, cluster *suite.StartedCluster) (mediumSlotConvergence, error) {
	startedAt := time.Now()
	convergence, err := cluster.WaitSlotLeadersStable(ctx, mediumConvergenceStableWindow)
	if err != nil {
		return mediumSlotConvergence{}, err
	}
	inventories, err := readMediumSlotInventories(ctx, cluster)
	if err != nil {
		return mediumSlotConvergence{}, err
	}
	snapshot, err := validateMediumSlotConvergence(inventories)
	if err != nil {
		return mediumSlotConvergence{}, err
	}
	if err := validateMediumSlotStateMatchesStableProof(convergence, snapshot); err != nil {
		return mediumSlotConvergence{}, err
	}
	return mediumSlotConvergence{
		Leaders:        append([]uint64(nil), snapshot.Leaders...),
		Fingerprint:    snapshot.Fingerprint,
		WaitDuration:   time.Since(startedAt),
		StableDuration: convergence.StableDuration,
	}, nil
}

func validateMediumSlotStateMatchesStableProof(
	convergence suite.SlotLeaderConvergence,
	snapshot mediumConvergenceSnapshot,
) error {
	if snapshot.Fingerprint != convergence.Fingerprint {
		return fmt.Errorf(
			"strict Medium Slot state changed after the stability proof: stable=%q current=%q",
			convergence.Fingerprint,
			snapshot.Fingerprint,
		)
	}
	return nil
}

func readMediumSlotInventories(ctx context.Context, cluster *suite.StartedCluster) (map[uint64]mediumSlotsResponse, error) {
	inventories := make(map[uint64]mediumSlotsResponse, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		if strings.TrimSpace(node.ManagerAddr()) == "" {
			return nil, fmt.Errorf("node %d manager HTTP is disabled", node.Spec.ID)
		}
		var response mediumSlotsResponse
		url := fmt.Sprintf("http://%s/manager/slots?node_id=%d", node.ManagerAddr(), node.Spec.ID)
		if _, err := suite.GetJSON(ctx, url, &response); err != nil {
			return nil, fmt.Errorf("read node %d Slot inventory: %w", node.Spec.ID, err)
		}
		inventories[node.Spec.ID] = response
	}
	return inventories, nil
}

func validateMediumSlotConvergence(inventories map[uint64]mediumSlotsResponse) (mediumConvergenceSnapshot, error) {
	expectedPeers := []uint64{1, 2, 3}
	if len(inventories) != mediumReplicaCount {
		return mediumConvergenceSnapshot{}, fmt.Errorf("node inventories = %d, want %d", len(inventories), mediumReplicaCount)
	}

	var reference []suite.SlotDTO
	for nodeID := uint64(1); nodeID <= mediumReplicaCount; nodeID++ {
		inventory, ok := inventories[nodeID]
		if !ok {
			return mediumConvergenceSnapshot{}, fmt.Errorf("node %d Slot inventory is missing", nodeID)
		}
		if inventory.Total != mediumLogicalSlots || len(inventory.Items) != mediumLogicalSlots {
			return mediumConvergenceSnapshot{}, fmt.Errorf(
				"node %d logical slots total/items = %d/%d, want %d/%d",
				nodeID,
				inventory.Total,
				len(inventory.Items),
				mediumLogicalSlots,
				mediumLogicalSlots,
			)
		}

		items := append([]suite.SlotDTO(nil), inventory.Items...)
		sort.Slice(items, func(i, j int) bool { return items[i].SlotID < items[j].SlotID })
		if err := validateMediumNodeSlots(nodeID, items, expectedPeers); err != nil {
			return mediumConvergenceSnapshot{}, err
		}
		if nodeID == 1 {
			if err := validateMediumHashSlotCoverage(items); err != nil {
				return mediumConvergenceSnapshot{}, err
			}
			reference = items
			continue
		}
		if err := validateMediumSlotControlAgreement(reference, items, nodeID); err != nil {
			return mediumConvergenceSnapshot{}, err
		}
	}

	leaders := make([]uint64, len(reference))
	var fingerprint strings.Builder
	for slotIndex, item := range reference {
		leaderID := item.NodeLog.LeaderID
		for nodeID := uint64(2); nodeID <= mediumReplicaCount; nodeID++ {
			observed := sortedMediumSlots(inventories[nodeID].Items)[slotIndex]
			if observed.NodeLog.LeaderID != leaderID {
				return mediumConvergenceSnapshot{}, fmt.Errorf(
					"slot %d actual leader disagreement: node 1 reports %d, node %d reports %d",
					item.SlotID,
					leaderID,
					nodeID,
					observed.NodeLog.LeaderID,
				)
			}
		}
		leaders[slotIndex] = leaderID
		fmt.Fprintf(
			&fingerprint,
			"%d:%d:%d:%d:%d:%v;",
			item.SlotID,
			leaderID,
			item.Assignment.PreferredLeaderID,
			item.Assignment.ConfigEpoch,
			item.Assignment.BalanceVersion,
			expectedPeers,
		)
	}
	return mediumConvergenceSnapshot{
		Leaders:     leaders,
		Fingerprint: fingerprint.String(),
	}, nil
}

func validateMediumNodeSlots(nodeID uint64, items []suite.SlotDTO, expectedPeers []uint64) error {
	var previousSlotID uint32
	for index, item := range items {
		if index > 0 && item.SlotID == previousSlotID {
			return fmt.Errorf("node %d has duplicate logical slot %d", nodeID, item.SlotID)
		}
		previousSlotID = item.SlotID
		if item.Task != nil {
			return fmt.Errorf("node %d slot %d still has active task %#v", nodeID, item.SlotID, item.Task)
		}
		if !sameUint64Values(item.Assignment.DesiredPeers, expectedPeers) {
			return fmt.Errorf("node %d slot %d desired peers = %v, want %v", nodeID, item.SlotID, item.Assignment.DesiredPeers, expectedPeers)
		}
		if item.NodeLog == nil {
			return fmt.Errorf("node %d slot %d local Raft observation is missing", nodeID, item.SlotID)
		}
		if item.NodeLog.NodeID != nodeID {
			return fmt.Errorf("node %d slot %d Raft observation belongs to node %d", nodeID, item.SlotID, item.NodeLog.NodeID)
		}
		if item.NodeLog.LeaderID == 0 || !containsUint64(item.Assignment.DesiredPeers, item.NodeLog.LeaderID) {
			return fmt.Errorf("node %d slot %d actual leader = %d, desired peers %v", nodeID, item.SlotID, item.NodeLog.LeaderID, item.Assignment.DesiredPeers)
		}
		if !sameUint64Values(item.NodeLog.CurrentVoters, expectedPeers) {
			return fmt.Errorf("node %d slot %d current voters = %v, want %v", nodeID, item.SlotID, item.NodeLog.CurrentVoters, expectedPeers)
		}
		if !sameUint64Values(item.Runtime.CurrentVoters, expectedPeers) || !item.Runtime.HasQuorum {
			return fmt.Errorf(
				"node %d slot %d runtime voters/quorum = %v/%v, want %v/true",
				nodeID,
				item.SlotID,
				item.Runtime.CurrentVoters,
				item.Runtime.HasQuorum,
				expectedPeers,
			)
		}
		if nodeID == item.NodeLog.LeaderID && item.NodeLog.Role != "leader" {
			return fmt.Errorf("node %d slot %d reports itself as actual leader with role %q", nodeID, item.SlotID, item.NodeLog.Role)
		}
		if nodeID != item.NodeLog.LeaderID && item.NodeLog.Role != "follower" {
			return fmt.Errorf("node %d slot %d reports leader %d with role %q", nodeID, item.SlotID, item.NodeLog.LeaderID, item.NodeLog.Role)
		}
		if item.NodeLog.AppliedIndex > item.NodeLog.CommitIndex {
			return fmt.Errorf(
				"node %d slot %d applied index %d exceeds commit index %d",
				nodeID,
				item.SlotID,
				item.NodeLog.AppliedIndex,
				item.NodeLog.CommitIndex,
			)
		}
	}
	return nil
}

func validateMediumHashSlotCoverage(items []suite.SlotDTO) error {
	covered := make([]bool, mediumPhysicalHashSlots)
	for _, item := range items {
		if item.HashSlots == nil || item.HashSlots.Count != len(item.HashSlots.Items) {
			return fmt.Errorf("slot %d physical hash-slot inventory is incomplete", item.SlotID)
		}
		if len(item.HashSlots.Items) == 0 {
			return fmt.Errorf("logical slot %d physical hash-slot inventory is empty", item.SlotID)
		}
		for _, hashSlot := range item.HashSlots.Items {
			if int(hashSlot) >= len(covered) || covered[hashSlot] {
				return fmt.Errorf("slot %d physical hash-slot %d is out of range or duplicated", item.SlotID, hashSlot)
			}
			covered[hashSlot] = true
		}
	}
	for hashSlot, present := range covered {
		if !present {
			return fmt.Errorf("physical hash-slot coverage is missing hash slot %d", hashSlot)
		}
	}
	return nil
}

func validateMediumSlotControlAgreement(reference, observed []suite.SlotDTO, nodeID uint64) error {
	for index, expected := range reference {
		actual := observed[index]
		if actual.SlotID != expected.SlotID ||
			actual.Assignment.PreferredLeaderID != expected.Assignment.PreferredLeaderID ||
			actual.Assignment.ConfigEpoch != expected.Assignment.ConfigEpoch ||
			actual.Assignment.BalanceVersion != expected.Assignment.BalanceVersion ||
			!sameUint64Values(actual.Assignment.DesiredPeers, expected.Assignment.DesiredPeers) ||
			!sameHashSlots(actual.HashSlots, expected.HashSlots) {
			return fmt.Errorf(
				"node %d slot control state disagrees at index %d: got %#v, node 1 %#v",
				nodeID,
				index,
				actual,
				expected,
			)
		}
	}
	return nil
}

func sortedMediumSlots(items []suite.SlotDTO) []suite.SlotDTO {
	sorted := append([]suite.SlotDTO(nil), items...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SlotID < sorted[j].SlotID })
	return sorted
}

func sameUint64Values(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]uint64(nil), left...)
	rightCopy := append([]uint64(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i] < leftCopy[j] })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i] < rightCopy[j] })
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func sameHashSlots(left, right *suite.SlotHashSlotsDTO) bool {
	if left == nil || right == nil || left.Count != right.Count || len(left.Items) != len(right.Items) {
		return false
	}
	for index := range left.Items {
		if left.Items[index] != right.Items[index] {
			return false
		}
	}
	return true
}

func containsUint64(items []uint64, want uint64) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
