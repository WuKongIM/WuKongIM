package planner

import (
	"context"
	"fmt"
	"math/bits"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

const (
	reasonNoMissingSlot         = "no_missing_slot"
	reasonInvalidReplicaCount   = "invalid_replica_count"
	reasonInsufficientDataNodes = "insufficient_data_nodes"
)

// BootstrapPlanner creates initial slot assignments and bootstrap tasks.
type BootstrapPlanner struct{}

// NewBootstrapPlanner returns a planner for minimal initial slot placement.
func NewBootstrapPlanner() *BootstrapPlanner {
	return &BootstrapPlanner{}
}

// Next returns the next bootstrap assignment command for the lowest missing slot.
func (p *BootstrapPlanner) Next(ctx context.Context, view View) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	st := view.State
	replicaCount := int(st.Config.ReplicaCount)
	if replicaCount <= 0 {
		return Decision{Kind: DecisionKindBlocked, Reason: reasonInvalidReplicaCount}, nil
	}

	slotID, ok := lowestMissingSlot(st)
	if !ok {
		return Decision{Kind: DecisionKindNone, Reason: reasonNoMissingSlot}, nil
	}

	candidates := eligibleDataNodes(st.Nodes)
	if len(candidates) < replicaCount {
		return Decision{Kind: DecisionKindBlocked, Reason: reasonInsufficientDataNodes}, nil
	}

	peerLoad := desiredPeerLoad(st.Slots)
	leaderLoad := preferredLeaderLoad(st.Slots)
	selected := selectPeers(slotID, candidates, peerLoad, replicaCount)
	leader := selectPreferredLeader(slotID, selected, len(candidates), leaderLoad)
	peers := selectedNodeIDs(selected)
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })

	configEpoch := uint64(1)
	expectedRevision := st.Revision
	assignment := state.SlotAssignment{
		SlotID:          slotID,
		DesiredPeers:    peers,
		ConfigEpoch:     configEpoch,
		PreferredLeader: leader,
	}
	task := state.ReconcileTask{
		TaskID:      fmt.Sprintf("slot-%d-bootstrap-%d", slotID, configEpoch),
		SlotID:      slotID,
		Kind:        state.TaskKindBootstrap,
		Step:        state.TaskStepCreateSlot,
		TargetNode:  leader,
		TargetPeers: append([]uint64(nil), peers...),
		ConfigEpoch: configEpoch,
		Status:      state.TaskStatusPending,
	}
	return Decision{
		Kind: DecisionKindCommand,
		Command: command.Command{
			Kind:             command.KindUpsertSlotAssignmentAndTask,
			IssuedAt:         view.Now,
			ExpectedRevision: &expectedRevision,
			Assignment:       &assignment,
			Task:             &task,
		},
	}, nil
}

type candidateNode struct {
	nodeID uint64
	weight uint64
	load   uint64
	tie    uint64
}

func eligibleDataNodes(nodes []state.Node) []candidateNode {
	candidates := make([]candidateNode, 0, len(nodes))
	for _, node := range nodes {
		if !node.HasRole(state.NodeRoleData) ||
			node.JoinState != state.NodeJoinStateActive ||
			node.Status != state.NodeStatusAlive {
			continue
		}
		weight := node.CapacityWeight
		if weight == 0 {
			weight = 1
		}
		candidates = append(candidates, candidateNode{nodeID: node.NodeID, weight: uint64(weight)})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].nodeID < candidates[j].nodeID })
	return candidates
}

func lowestMissingSlot(st state.ClusterState) (uint32, bool) {
	assigned := make(map[uint32]struct{}, len(st.Slots))
	for _, assignment := range st.Slots {
		assigned[assignment.SlotID] = struct{}{}
	}
	activeTask := make(map[uint32]struct{}, len(st.Tasks))
	for _, task := range st.Tasks {
		activeTask[task.SlotID] = struct{}{}
	}
	for slotID := uint32(1); slotID <= st.Config.SlotCount; slotID++ {
		if _, ok := assigned[slotID]; ok {
			continue
		}
		if _, ok := activeTask[slotID]; ok {
			continue
		}
		return slotID, true
	}
	return 0, false
}

func desiredPeerLoad(assignments []state.SlotAssignment) map[uint64]uint64 {
	load := make(map[uint64]uint64)
	for _, assignment := range assignments {
		for _, peerID := range assignment.DesiredPeers {
			load[peerID]++
		}
	}
	return load
}

func preferredLeaderLoad(assignments []state.SlotAssignment) map[uint64]uint64 {
	load := make(map[uint64]uint64)
	for _, assignment := range assignments {
		if assignment.PreferredLeader != 0 {
			load[assignment.PreferredLeader]++
		}
	}
	return load
}

func selectPeers(slotID uint32, candidates []candidateNode, load map[uint64]uint64, replicaCount int) []candidateNode {
	ranked := make([]candidateNode, len(candidates))
	copy(ranked, candidates)
	candidateCount := uint64(len(candidates))
	for i := range ranked {
		ranked[i].load = load[ranked[i].nodeID]
		ranked[i].tie = stableTie(slotID, ranked[i].nodeID, candidateCount)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if cmp := compareWeightedLoad(ranked[i], ranked[j]); cmp != 0 {
			return cmp < 0
		}
		if ranked[i].tie != ranked[j].tie {
			return ranked[i].tie < ranked[j].tie
		}
		return ranked[i].nodeID < ranked[j].nodeID
	})
	selected := make([]candidateNode, replicaCount)
	copy(selected, ranked[:replicaCount])
	return selected
}

func selectPreferredLeader(slotID uint32, selected []candidateNode, candidateCount int, load map[uint64]uint64) uint64 {
	ranked := make([]candidateNode, len(selected))
	copy(ranked, selected)
	for i := range ranked {
		ranked[i].load = load[ranked[i].nodeID]
		ranked[i].tie = stableTie(slotID, ranked[i].nodeID, uint64(candidateCount))
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].load != ranked[j].load {
			return ranked[i].load < ranked[j].load
		}
		if ranked[i].tie != ranked[j].tie {
			return ranked[i].tie < ranked[j].tie
		}
		return ranked[i].nodeID < ranked[j].nodeID
	})
	return ranked[0].nodeID
}

func selectedNodeIDs(selected []candidateNode) []uint64 {
	out := make([]uint64, len(selected))
	for i := range selected {
		out[i] = selected[i].nodeID
	}
	return out
}

func stableTie(slotID uint32, nodeID uint64, candidateCount uint64) uint64 {
	if candidateCount == 0 {
		return 0
	}
	return (uint64(slotID) + nodeID) % candidateCount
}

func compareWeightedLoad(a, b candidateNode) int {
	leftHi, leftLo := bits.Mul64(a.load, b.weight)
	rightHi, rightLo := bits.Mul64(b.load, a.weight)
	if leftHi != rightHi {
		if leftHi < rightHi {
			return -1
		}
		return 1
	}
	if leftLo < rightLo {
		return -1
	}
	if leftLo > rightLo {
		return 1
	}
	return 0
}
