package chatlifecycle

import (
	"errors"
	"sort"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

var errClusterObservation = errors.New("chat lifecycle cluster observation is inconsistent")

type mergedClusterSlot struct {
	slotID          uint32
	leaderID        uint64
	progressHealthy bool
}

type mergedClusterObservation struct {
	slots        []mergedClusterSlot
	leaderCounts map[uint64]int
}

type clusterSlotAccumulator struct {
	leaderID        uint64
	replicas        []uint64
	voters          []uint64
	reports         int
	leaderReported  bool
	progressHealthy bool
}

// mergeClusterObservations accepts only a complete, stable view from every declared node.
// Desired replica sets and voters are compared as sets; leader progress remains leader-authoritative.
func mergeClusterObservations(snapshots []target.DebugCluster, cfg Config) (mergedClusterObservation, error) {
	expectedNodes := len(cfg.Observation.ServiceNodes)
	expectedSlots := cfg.Workload.Topology.LogicalSlotGroups
	expectedReplicas := cfg.Workload.Topology.SlotReplicas
	if len(snapshots) != expectedNodes || expectedNodes == 0 || expectedSlots <= 0 || expectedReplicas <= 0 {
		return mergedClusterObservation{}, errClusterObservation
	}

	nodeIDs := make(map[uint64]struct{}, expectedNodes)
	accumulators := make(map[uint32]*clusterSlotAccumulator, expectedSlots)
	for _, snapshot := range snapshots {
		if snapshot.NodeID == 0 || snapshot.StateRevision == 0 || len(snapshot.Slots) != expectedSlots {
			return mergedClusterObservation{}, errClusterObservation
		}
		if _, duplicate := nodeIDs[snapshot.NodeID]; duplicate {
			return mergedClusterObservation{}, errClusterObservation
		}
		nodeIDs[snapshot.NodeID] = struct{}{}
		seenSlots := make(map[uint32]struct{}, expectedSlots)
		for _, slot := range snapshot.Slots {
			if _, duplicate := seenSlots[slot.SlotID]; duplicate || slot.LeaderID == 0 || slot.Term == 0 || slot.AppliedIndex > slot.CommitIndex {
				return mergedClusterObservation{}, errClusterObservation
			}
			seenSlots[slot.SlotID] = struct{}{}
			replicas, ok := normalizedNodeSet(slot.Replicas, expectedReplicas)
			if !ok {
				return mergedClusterObservation{}, errClusterObservation
			}
			voters, ok := normalizedNodeSet(slot.Voters, expectedReplicas)
			if !ok || !equalNodeSets(replicas, voters) || !containsNode(voters, slot.LeaderID) {
				return mergedClusterObservation{}, errClusterObservation
			}

			accumulator := accumulators[slot.SlotID]
			if accumulator == nil {
				accumulator = &clusterSlotAccumulator{leaderID: slot.LeaderID, replicas: replicas, voters: voters}
				accumulators[slot.SlotID] = accumulator
			} else if accumulator.leaderID != slot.LeaderID || !equalNodeSets(accumulator.replicas, replicas) || !equalNodeSets(accumulator.voters, voters) {
				return mergedClusterObservation{}, errClusterObservation
			}
			accumulator.reports++
			if snapshot.NodeID == slot.LeaderID {
				if accumulator.leaderReported {
					return mergedClusterObservation{}, errClusterObservation
				}
				accumulator.leaderReported = true
				accumulator.progressHealthy = healthyLeaderProgress(slot, voters, cfg.Thresholds.Cluster.MaxHotReplicaLagEntries)
			} else if len(slot.ReplicaProgress) != 0 {
				return mergedClusterObservation{}, errClusterObservation
			}
		}
	}
	if len(accumulators) != expectedSlots {
		return mergedClusterObservation{}, errClusterObservation
	}

	merged := mergedClusterObservation{
		slots:        make([]mergedClusterSlot, 0, expectedSlots),
		leaderCounts: make(map[uint64]int, expectedNodes),
	}
	for nodeID := range nodeIDs {
		merged.leaderCounts[nodeID] = 0
	}
	for slotID, accumulator := range accumulators {
		if accumulator.reports != expectedNodes || !accumulator.leaderReported {
			return mergedClusterObservation{}, errClusterObservation
		}
		merged.slots = append(merged.slots, mergedClusterSlot{
			slotID: slotID, leaderID: accumulator.leaderID, progressHealthy: accumulator.progressHealthy,
		})
		merged.leaderCounts[accumulator.leaderID]++
	}
	sort.Slice(merged.slots, func(i, j int) bool { return merged.slots[i].slotID < merged.slots[j].slotID })
	return merged, nil
}

func healthyLeaderProgress(slot target.ClusterSlot, voters []uint64, maxLag uint64) bool {
	if len(slot.ReplicaProgress) != len(voters) {
		return false
	}
	seen := make(map[uint64]struct{}, len(voters))
	for _, progress := range slot.ReplicaProgress {
		if _, duplicate := seen[progress.NodeID]; duplicate || !containsNode(voters, progress.NodeID) || progress.MatchIndex > slot.CommitIndex {
			return false
		}
		seen[progress.NodeID] = struct{}{}
		lag := slot.CommitIndex - progress.MatchIndex
		if progress.LagEntries != lag || lag > maxLag || progress.State != "StateReplicate" {
			return false
		}
	}
	return true
}

func normalizedNodeSet(nodes []uint64, expected int) ([]uint64, bool) {
	if len(nodes) != expected {
		return nil, false
	}
	normalized := append([]uint64(nil), nodes...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	for index, nodeID := range normalized {
		if nodeID == 0 || index > 0 && normalized[index-1] == nodeID {
			return nil, false
		}
	}
	return normalized, true
}

func equalNodeSets(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsNode(nodes []uint64, nodeID uint64) bool {
	index := sort.Search(len(nodes), func(index int) bool { return nodes[index] >= nodeID })
	return index < len(nodes) && nodes[index] == nodeID
}

func leaderImbalanced(counts map[uint64]int, slots, limitPercent int) bool {
	if len(counts) == 0 || slots <= 0 || limitPercent < 0 {
		return true
	}
	nodes := len(counts)
	for _, count := range counts {
		delta := count*nodes - slots
		if delta < 0 {
			delta = -delta
		}
		// Compare each node with the exact rational share (slots/nodes), avoiding
		// a hard-coded 4/4/4 split and floating-point rounding.
		if delta*100 > limitPercent*slots {
			return true
		}
	}
	return false
}

func hotSlotProgressHealthy(observation mergedClusterObservation, declared []uint32) (healthy, valid bool) {
	if len(declared) == 0 {
		for _, slot := range observation.slots {
			if !slot.progressHealthy {
				return false, true
			}
		}
		return true, true
	}
	if len(declared) > len(observation.slots) {
		return false, false
	}
	selected := make(map[uint32]struct{}, len(declared))
	for _, slotID := range declared {
		if _, duplicate := selected[slotID]; duplicate {
			return false, false
		}
		selected[slotID] = struct{}{}
	}
	for _, slot := range observation.slots {
		if _, ok := selected[slot.slotID]; !ok {
			continue
		}
		if !slot.progressHealthy {
			return false, true
		}
		delete(selected, slot.slotID)
	}
	return len(selected) == 0, len(selected) == 0
}
