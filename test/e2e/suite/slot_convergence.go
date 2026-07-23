//go:build e2e

package suite

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const slotLeaderConvergencePollInterval = 100 * time.Millisecond

// ActualSlotLeader identifies the Raft-elected leader for one logical Slot.
type ActualSlotLeader struct {
	// SlotID is the stable logical Slot identifier.
	SlotID uint32
	// LeaderID is the non-zero node selected by the Slot Raft group.
	LeaderID uint64
}

// SlotLeaderConvergence records the cross-node Slot leader state that remained
// unchanged for the requested stability window.
type SlotLeaderConvergence struct {
	// Leaders contains the actual Raft leaders ordered by logical Slot ID.
	Leaders []ActualSlotLeader
	// Fingerprint identifies the stable leaders, voters, and control assignment.
	Fingerprint string
	// WaitDuration is the total time spent proving convergence.
	WaitDuration time.Duration
	// StableDuration is how long the final fingerprint remained unchanged.
	StableDuration time.Duration
}

type slotLeaderConvergenceSnapshot struct {
	Leaders     []ActualSlotLeader
	Fingerprint string
}

type slotLeaderAggregate struct {
	reference    SlotDTO
	observations map[uint64]SlotDTO
}

type slotLeaderInventorySet struct {
	full    map[uint64]managerSlotsResponse
	byVoter map[uint64]managerSlotsResponse
}

type slotLeaderConvergenceTracker struct {
	stableWindow time.Duration
	fingerprint  string
	stableSince  time.Time
}

func newSlotLeaderConvergenceTracker(stableWindow time.Duration) *slotLeaderConvergenceTracker {
	return &slotLeaderConvergenceTracker{stableWindow: stableWindow}
}

func (t *slotLeaderConvergenceTracker) Observe(now time.Time, fingerprint string) (time.Duration, bool) {
	if strings.TrimSpace(fingerprint) == "" {
		t.Reset()
		return 0, false
	}
	if t.fingerprint != fingerprint || t.stableSince.IsZero() {
		t.fingerprint = fingerprint
		t.stableSince = now
		return 0, t.stableWindow <= 0
	}
	stableFor := now.Sub(t.stableSince)
	if stableFor < 0 {
		t.stableSince = now
		return 0, false
	}
	return stableFor, stableFor >= t.stableWindow
}

func (t *slotLeaderConvergenceTracker) Reset() {
	t.fingerprint = ""
	t.stableSince = time.Time{}
}

// WaitSlotLeadersStable waits until every node reports the same valid
// Raft-elected Slot leaders and that inventory remains unchanged for
// stableWindow. The cluster must be started with WithManagerHTTP.
func (c *StartedCluster) WaitSlotLeadersStable(ctx context.Context, stableWindow time.Duration) (SlotLeaderConvergence, error) {
	if c == nil {
		return SlotLeaderConvergence{}, fmt.Errorf("started cluster is nil")
	}
	if len(c.Nodes) == 0 {
		return SlotLeaderConvergence{}, fmt.Errorf("started cluster has no nodes")
	}
	if stableWindow < 0 {
		return SlotLeaderConvergence{}, fmt.Errorf("stable window must not be negative")
	}

	nodeIDs := make([]uint64, 0, len(c.Nodes))
	seenNodeIDs := make(map[uint64]struct{}, len(c.Nodes))
	for _, node := range c.Nodes {
		if strings.TrimSpace(node.ManagerAddr()) == "" {
			return SlotLeaderConvergence{}, fmt.Errorf("node %d manager HTTP is disabled", node.Spec.ID)
		}
		if _, exists := seenNodeIDs[node.Spec.ID]; exists {
			return SlotLeaderConvergence{}, fmt.Errorf("duplicate node ID %d", node.Spec.ID)
		}
		seenNodeIDs[node.Spec.ID] = struct{}{}
		nodeIDs = append(nodeIDs, node.Spec.ID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })

	startedAt := time.Now()
	tracker := newSlotLeaderConvergenceTracker(stableWindow)
	ticker := time.NewTicker(slotLeaderConvergencePollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		inventories, err := c.readSlotLeaderInventories(ctx)
		if err == nil {
			var snapshot slotLeaderConvergenceSnapshot
			snapshot, err = validateSlotLeaderConvergence(inventories, nodeIDs)
			if err == nil {
				stableFor, ready := tracker.Observe(time.Now(), snapshot.Fingerprint)
				if ready {
					return SlotLeaderConvergence{
						Leaders:        append([]ActualSlotLeader(nil), snapshot.Leaders...),
						Fingerprint:    snapshot.Fingerprint,
						WaitDuration:   time.Since(startedAt),
						StableDuration: stableFor,
					}, nil
				}
				lastErr = fmt.Errorf(
					"actual Raft leader inventory is valid but stable for only %s of %s",
					stableFor,
					stableWindow,
				)
			}
		}
		if err != nil {
			lastErr = err
			tracker.Reset()
		}

		select {
		case <-ctx.Done():
			return SlotLeaderConvergence{}, fmt.Errorf(
				"actual Raft leaders did not stabilize: %w (last observation: %v)",
				ctx.Err(),
				lastErr,
			)
		case <-ticker.C:
		}
	}
}

func (c *StartedCluster) readSlotLeaderInventories(ctx context.Context) (slotLeaderInventorySet, error) {
	inventories := slotLeaderInventorySet{
		full:    make(map[uint64]managerSlotsResponse, len(c.Nodes)),
		byVoter: make(map[uint64]managerSlotsResponse, len(c.Nodes)),
	}
	for _, node := range c.Nodes {
		var full managerSlotsResponse
		fullURL := fmt.Sprintf("http://%s/manager/slots", node.ManagerAddr())
		if _, err := GetJSON(ctx, fullURL, &full); err != nil {
			return slotLeaderInventorySet{}, fmt.Errorf("read node %d full Slot inventory: %w", node.Spec.ID, err)
		}
		inventories.full[node.Spec.ID] = full

		var byVoter managerSlotsResponse
		voterURL := fmt.Sprintf("http://%s/manager/slots?node_id=%d", node.ManagerAddr(), node.Spec.ID)
		if _, err := GetJSON(ctx, voterURL, &byVoter); err != nil {
			return slotLeaderInventorySet{}, fmt.Errorf("read node %d voter Slot inventory: %w", node.Spec.ID, err)
		}
		inventories.byVoter[node.Spec.ID] = byVoter
	}
	return inventories, nil
}

func validateSlotLeaderConvergence(
	inventories slotLeaderInventorySet,
	nodeIDs []uint64,
) (slotLeaderConvergenceSnapshot, error) {
	if len(nodeIDs) == 0 {
		return slotLeaderConvergenceSnapshot{}, fmt.Errorf("expected node IDs are empty")
	}
	knownNodeIDs := make(map[uint64]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID == 0 {
			return slotLeaderConvergenceSnapshot{}, fmt.Errorf("expected node IDs contain zero")
		}
		if _, duplicate := knownNodeIDs[nodeID]; duplicate {
			return slotLeaderConvergenceSnapshot{}, fmt.Errorf("expected node IDs contain duplicate %d", nodeID)
		}
		knownNodeIDs[nodeID] = struct{}{}
	}
	if len(inventories.full) != len(nodeIDs) {
		return slotLeaderConvergenceSnapshot{}, fmt.Errorf(
			"full node inventories = %d, want %d",
			len(inventories.full),
			len(nodeIDs),
		)
	}
	if len(inventories.byVoter) != len(nodeIDs) {
		return slotLeaderConvergenceSnapshot{}, fmt.Errorf(
			"voter node inventories = %d, want %d",
			len(inventories.byVoter),
			len(nodeIDs),
		)
	}

	fullSlots, err := validateFullSlotInventories(inventories.full, nodeIDs, knownNodeIDs)
	if err != nil {
		return slotLeaderConvergenceSnapshot{}, err
	}

	aggregates := make(map[uint32]*slotLeaderAggregate)
	for _, nodeID := range nodeIDs {
		inventory, ok := inventories.byVoter[nodeID]
		if !ok {
			return slotLeaderConvergenceSnapshot{}, fmt.Errorf("node %d voter Slot inventory is missing", nodeID)
		}
		if inventory.Total != len(inventory.Items) {
			return slotLeaderConvergenceSnapshot{}, fmt.Errorf(
				"node %d logical slots total/items = %d/%d",
				nodeID,
				inventory.Total,
				len(inventory.Items),
			)
		}

		items := append([]SlotDTO(nil), inventory.Items...)
		sort.Slice(items, func(i, j int) bool { return items[i].SlotID < items[j].SlotID })
		var previousSlotID uint32
		for index, item := range items {
			if index > 0 && item.SlotID == previousSlotID {
				return slotLeaderConvergenceSnapshot{}, fmt.Errorf("node %d has duplicate logical Slot %d", nodeID, item.SlotID)
			}
			previousSlotID = item.SlotID
			if err := validateNodeSlotLeader(nodeID, item, knownNodeIDs); err != nil {
				return slotLeaderConvergenceSnapshot{}, err
			}

			aggregate := aggregates[item.SlotID]
			if aggregate == nil {
				aggregate = &slotLeaderAggregate{
					reference:    item,
					observations: make(map[uint64]SlotDTO, len(item.Assignment.DesiredPeers)),
				}
				aggregates[item.SlotID] = aggregate
			} else if err := validateSlotAssignmentAgreement(aggregate.reference, item, nodeID); err != nil {
				return slotLeaderConvergenceSnapshot{}, err
			}
			aggregate.observations[nodeID] = item
		}
	}
	if len(aggregates) != len(fullSlots) {
		return slotLeaderConvergenceSnapshot{}, fmt.Errorf(
			"voter-observed logical Slots = %d, full control inventory has %d",
			len(aggregates),
			len(fullSlots),
		)
	}
	for slotID, fullSlot := range fullSlots {
		aggregate := aggregates[slotID]
		if aggregate == nil {
			return slotLeaderConvergenceSnapshot{}, fmt.Errorf(
				"full control Slot %d has no voter observation",
				slotID,
			)
		}
		if err := validateSlotAssignmentAgreement(fullSlot, aggregate.reference, aggregate.reference.NodeLog.NodeID); err != nil {
			return slotLeaderConvergenceSnapshot{}, fmt.Errorf("full/voter control state: %w", err)
		}
	}

	slotIDs := make([]uint32, 0, len(aggregates))
	for slotID := range aggregates {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })

	leaders := make([]ActualSlotLeader, 0, len(slotIDs))
	var fingerprint strings.Builder
	for _, slotID := range slotIDs {
		aggregate := aggregates[slotID]
		voters, err := normalizedKnownNodeSet(
			aggregate.reference.Assignment.DesiredPeers,
			knownNodeIDs,
			fmt.Sprintf("Slot %d desired peers", slotID),
		)
		if err != nil {
			return slotLeaderConvergenceSnapshot{}, err
		}
		if len(aggregate.observations) != len(voters) {
			return slotLeaderConvergenceSnapshot{}, fmt.Errorf(
				"Slot %d voter observations = %d, want %d from %v",
				slotID,
				len(aggregate.observations),
				len(voters),
				voters,
			)
		}

		leaderID := aggregate.reference.NodeLog.LeaderID
		for _, voterID := range voters {
			observation, ok := aggregate.observations[voterID]
			if !ok {
				return slotLeaderConvergenceSnapshot{}, fmt.Errorf(
					"Slot %d is missing actual leader observation from voter %d",
					slotID,
					voterID,
				)
			}
			if observation.NodeLog.LeaderID != leaderID {
				return slotLeaderConvergenceSnapshot{}, fmt.Errorf(
					"Slot %d actual leader disagreement: voter %d reports %d, voter %d reports %d",
					slotID,
					aggregate.reference.NodeLog.NodeID,
					leaderID,
					voterID,
					observation.NodeLog.LeaderID,
				)
			}
		}

		leaders = append(leaders, ActualSlotLeader{SlotID: slotID, LeaderID: leaderID})
		fmt.Fprintf(
			&fingerprint,
			"%d:%d:%d:%d:%d:%v;",
			slotID,
			leaderID,
			aggregate.reference.Assignment.PreferredLeaderID,
			aggregate.reference.Assignment.ConfigEpoch,
			aggregate.reference.Assignment.BalanceVersion,
			voters,
		)
	}
	return slotLeaderConvergenceSnapshot{
		Leaders:     leaders,
		Fingerprint: fingerprint.String(),
	}, nil
}

func validateFullSlotInventories(
	inventories map[uint64]managerSlotsResponse,
	nodeIDs []uint64,
	knownNodeIDs map[uint64]struct{},
) (map[uint32]SlotDTO, error) {
	var reference []SlotDTO
	for index, nodeID := range nodeIDs {
		inventory, ok := inventories[nodeID]
		if !ok {
			return nil, fmt.Errorf("node %d full Slot inventory is missing", nodeID)
		}
		if inventory.Total != len(inventory.Items) {
			return nil, fmt.Errorf(
				"node %d full logical slots total/items = %d/%d",
				nodeID,
				inventory.Total,
				len(inventory.Items),
			)
		}
		if len(inventory.Items) == 0 {
			return nil, fmt.Errorf("node %d full logical Slot inventory is empty", nodeID)
		}

		items := append([]SlotDTO(nil), inventory.Items...)
		sort.Slice(items, func(i, j int) bool { return items[i].SlotID < items[j].SlotID })
		var previousSlotID uint32
		for itemIndex, item := range items {
			if itemIndex > 0 && item.SlotID == previousSlotID {
				return nil, fmt.Errorf("node %d full inventory has duplicate logical Slot %d", nodeID, item.SlotID)
			}
			previousSlotID = item.SlotID
			if item.Task != nil {
				return nil, fmt.Errorf("node %d full Slot %d still has active task %#v", nodeID, item.SlotID, item.Task)
			}
			desiredPeers, err := normalizedKnownNodeSet(
				item.Assignment.DesiredPeers,
				knownNodeIDs,
				fmt.Sprintf("node %d full Slot %d desired peers", nodeID, item.SlotID),
			)
			if err != nil {
				return nil, err
			}
			if item.Assignment.PreferredLeaderID != 0 &&
				!containsUint64Value(desiredPeers, item.Assignment.PreferredLeaderID) {
				return nil, fmt.Errorf(
					"node %d full Slot %d preferred leader %d is outside desired peers %v",
					nodeID,
					item.SlotID,
					item.Assignment.PreferredLeaderID,
					desiredPeers,
				)
			}
		}

		if index == 0 {
			reference = items
			continue
		}
		if len(items) != len(reference) {
			return nil, fmt.Errorf(
				"node %d full logical Slots = %d, reference has %d",
				nodeID,
				len(items),
				len(reference),
			)
		}
		for itemIndex, item := range items {
			if item.SlotID != reference[itemIndex].SlotID {
				return nil, fmt.Errorf(
					"node %d full Slot ID at index %d = %d, want %d",
					nodeID,
					itemIndex,
					item.SlotID,
					reference[itemIndex].SlotID,
				)
			}
			if err := validateSlotAssignmentAgreement(reference[itemIndex], item, nodeID); err != nil {
				return nil, fmt.Errorf("full control state: %w", err)
			}
		}
	}

	result := make(map[uint32]SlotDTO, len(reference))
	for _, item := range reference {
		result[item.SlotID] = item
	}
	return result, nil
}

func validateNodeSlotLeader(nodeID uint64, item SlotDTO, knownNodeIDs map[uint64]struct{}) error {
	if item.Task != nil {
		return fmt.Errorf("node %d Slot %d still has active task %#v", nodeID, item.SlotID, item.Task)
	}
	desiredPeers, err := normalizedKnownNodeSet(
		item.Assignment.DesiredPeers,
		knownNodeIDs,
		fmt.Sprintf("node %d Slot %d desired peers", nodeID, item.SlotID),
	)
	if err != nil {
		return err
	}
	if !containsUint64Value(desiredPeers, nodeID) {
		return fmt.Errorf("node %d reported Slot %d without being a desired peer %v", nodeID, item.SlotID, desiredPeers)
	}
	if item.NodeLog == nil {
		return fmt.Errorf("node %d Slot %d local Raft observation is missing", nodeID, item.SlotID)
	}
	if item.NodeLog.NodeID != nodeID {
		return fmt.Errorf(
			"node %d Slot %d Raft observation belongs to node %d",
			nodeID,
			item.SlotID,
			item.NodeLog.NodeID,
		)
	}
	if item.NodeLog.LeaderID == 0 || !containsUint64Value(desiredPeers, item.NodeLog.LeaderID) {
		return fmt.Errorf(
			"node %d Slot %d actual leader = %d, desired peers %v",
			nodeID,
			item.SlotID,
			item.NodeLog.LeaderID,
			desiredPeers,
		)
	}
	raftVoters, err := normalizedKnownNodeSet(
		item.NodeLog.CurrentVoters,
		knownNodeIDs,
		fmt.Sprintf("node %d Slot %d Raft voters", nodeID, item.SlotID),
	)
	if err != nil {
		return err
	}
	if !sameUint64Set(raftVoters, desiredPeers) {
		return fmt.Errorf(
			"node %d Slot %d Raft voters = %v, want %v",
			nodeID,
			item.SlotID,
			raftVoters,
			desiredPeers,
		)
	}
	runtimeVoters, err := normalizedKnownNodeSet(
		item.Runtime.CurrentVoters,
		knownNodeIDs,
		fmt.Sprintf("node %d Slot %d runtime voters", nodeID, item.SlotID),
	)
	if err != nil {
		return err
	}
	if !sameUint64Set(runtimeVoters, desiredPeers) || !item.Runtime.HasQuorum {
		return fmt.Errorf(
			"node %d Slot %d runtime voters/quorum = %v/%v, want %v/true",
			nodeID,
			item.SlotID,
			runtimeVoters,
			item.Runtime.HasQuorum,
			desiredPeers,
		)
	}
	if nodeID == item.NodeLog.LeaderID && item.NodeLog.Role != "leader" {
		return fmt.Errorf(
			"node %d Slot %d reports itself as actual leader with role %q",
			nodeID,
			item.SlotID,
			item.NodeLog.Role,
		)
	}
	if nodeID != item.NodeLog.LeaderID && item.NodeLog.Role != "follower" {
		return fmt.Errorf(
			"node %d Slot %d reports leader %d with role %q",
			nodeID,
			item.SlotID,
			item.NodeLog.LeaderID,
			item.NodeLog.Role,
		)
	}
	if item.NodeLog.AppliedIndex > item.NodeLog.CommitIndex {
		return fmt.Errorf(
			"node %d Slot %d applied index %d exceeds commit index %d",
			nodeID,
			item.SlotID,
			item.NodeLog.AppliedIndex,
			item.NodeLog.CommitIndex,
		)
	}
	return nil
}

func validateSlotAssignmentAgreement(reference, observed SlotDTO, nodeID uint64) error {
	if !sameUint64Set(observed.Assignment.DesiredPeers, reference.Assignment.DesiredPeers) ||
		observed.Assignment.PreferredLeaderID != reference.Assignment.PreferredLeaderID ||
		observed.Assignment.ConfigEpoch != reference.Assignment.ConfigEpoch ||
		observed.Assignment.BalanceVersion != reference.Assignment.BalanceVersion {
		return fmt.Errorf(
			"node %d Slot %d assignment disagrees with reference",
			nodeID,
			observed.SlotID,
		)
	}
	return nil
}

func normalizedKnownNodeSet(values []uint64, knownNodeIDs map[uint64]struct{}, label string) ([]uint64, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%s are empty", label)
	}
	normalized := append([]uint64(nil), values...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	var previous uint64
	for index, nodeID := range normalized {
		if nodeID == 0 {
			return nil, fmt.Errorf("%s contain zero", label)
		}
		if _, known := knownNodeIDs[nodeID]; !known {
			return nil, fmt.Errorf("%s contain unknown node %d", label, nodeID)
		}
		if index > 0 && nodeID == previous {
			return nil, fmt.Errorf("%s contain duplicate node %d", label, nodeID)
		}
		previous = nodeID
	}
	return normalized, nil
}

func containsUint64Value(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
