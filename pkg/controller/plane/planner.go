package plane

import (
	"context"
	"sort"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

type Planner struct {
	cfg PlannerConfig
}

func NewPlanner(cfg PlannerConfig) *Planner {
	if cfg.RebalanceSkewThreshold <= 0 {
		cfg.RebalanceSkewThreshold = 2
	}
	return &Planner{cfg: cfg}
}

func (p *Planner) ReconcileSlot(_ context.Context, state PlannerState, slotID uint32) (Decision, error) {
	decision := Decision{SlotID: slotID}

	assignment, hasAssignment := state.Assignments[slotID]
	view, hasView := state.Runtime[slotID]
	migrating := state.slotMigrating(slotID)
	if hasView && !view.HasQuorum {
		decision.Degraded = true
		decision.Assignment = assignment
		return decision, nil
	}
	if task, ok := state.Tasks[slotID]; ok {
		decision.Assignment = assignment
		if migrating && task.Kind != controllermeta.TaskKindBootstrap {
			return decision, nil
		}
		if task.Status == controllermeta.TaskStatusFailed {
			return decision, nil
		}
		if taskRunnable(state.Now, task) {
			decision.Task = &task
		}
		return decision, nil
	}
	if !hasAssignment && !hasView {
		peers := p.selectBootstrapPeers(state)
		if len(peers) < p.cfg.ReplicaN {
			return decision, nil
		}
		decision.Assignment = controllermeta.SlotAssignment{
			SlotID:       slotID,
			DesiredPeers: peers,
			ConfigEpoch:  1,
		}
		decision.Task = &controllermeta.ReconcileTask{
			SlotID:     slotID,
			Kind:       controllermeta.TaskKindBootstrap,
			Step:       controllermeta.TaskStepAddLearner,
			TargetNode: peers[0],
		}
		return decision, nil
	}
	if !hasAssignment {
		return decision, nil
	}
	if migrating {
		decision.Assignment = assignment
		return decision, nil
	}

	deadOrDraining := p.firstPeerNeedingRepair(state, assignment.DesiredPeers)
	if deadOrDraining == 0 {
		decision.Assignment = assignment
		return decision, nil
	}

	target := p.selectRepairTarget(state, assignment.DesiredPeers)
	if target == 0 {
		decision.Assignment = assignment
		return decision, nil
	}

	desiredPeers := replacePeer(assignment.DesiredPeers, deadOrDraining, target)
	decision.Assignment = controllermeta.SlotAssignment{
		SlotID:         assignment.SlotID,
		DesiredPeers:   desiredPeers,
		ConfigEpoch:    assignment.ConfigEpoch + 1,
		BalanceVersion: assignment.BalanceVersion,
	}
	decision.Task = &controllermeta.ReconcileTask{
		SlotID:     slotID,
		Kind:       controllermeta.TaskKindRepair,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: deadOrDraining,
		TargetNode: target,
	}
	return decision, nil
}

func (p *Planner) NextDecision(ctx context.Context, state PlannerState) (Decision, error) {
	for slotID := uint32(1); slotID <= p.cfg.SlotCount; slotID++ {
		decision, err := p.ReconcileSlot(ctx, state, slotID)
		if err != nil {
			return Decision{}, err
		}
		if decision.Task != nil {
			return decision, nil
		}
	}

	return p.nextRebalanceDecision(state), nil
}

func (p *Planner) nextRebalanceDecision(state PlannerState) Decision {
	loads := slotLoads(state.Assignments)
	minNode, minLoad, maxNode, maxLoad := loadExtremes(state, loads)
	if minNode == 0 || maxNode == 0 || maxLoad-minLoad < p.cfg.RebalanceSkewThreshold {
		return Decision{}
	}

	candidates := make([]controllermeta.SlotAssignment, 0, len(state.Assignments))
	for _, assignment := range state.Assignments {
		if state.slotMigrating(assignment.SlotID) {
			continue
		}
		if containsPeer(assignment.DesiredPeers, maxNode) && !containsPeer(assignment.DesiredPeers, minNode) {
			if _, ok := state.Runtime[assignment.SlotID]; !ok {
				continue
			}
			candidates = append(candidates, assignment)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].BalanceVersion == candidates[j].BalanceVersion {
			return candidates[i].SlotID < candidates[j].SlotID
		}
		return candidates[i].BalanceVersion < candidates[j].BalanceVersion
	})
	for _, assignment := range candidates {
		if view, ok := state.Runtime[assignment.SlotID]; ok && !view.HasQuorum {
			continue
		}
		if task, ok := state.Tasks[assignment.SlotID]; ok {
			if task.Status == controllermeta.TaskStatusFailed || !taskRunnable(state.Now, task) {
				continue
			}
			decision := Decision{
				SlotID:     assignment.SlotID,
				Assignment: assignment,
				Task:       &task,
			}
			return decision
		}
		if p.firstPeerNeedingRepair(state, assignment.DesiredPeers) != 0 {
			continue
		}
		decision := Decision{
			SlotID: assignment.SlotID,
			Assignment: controllermeta.SlotAssignment{
				SlotID:         assignment.SlotID,
				DesiredPeers:   replacePeer(assignment.DesiredPeers, maxNode, minNode),
				ConfigEpoch:    assignment.ConfigEpoch + 1,
				BalanceVersion: assignment.BalanceVersion + 1,
			},
		}
		decision.Task = &controllermeta.ReconcileTask{
			SlotID:     assignment.SlotID,
			Kind:       controllermeta.TaskKindRebalance,
			Step:       controllermeta.TaskStepAddLearner,
			SourceNode: maxNode,
			TargetNode: minNode,
		}
		return decision
	}
	return Decision{}
}

func (s PlannerState) slotMigrating(slotID uint32) bool {
	if len(s.MigratingSlots) == 0 {
		return false
	}
	_, ok := s.MigratingSlots[slotID]
	return ok
}

func (p *Planner) selectBootstrapPeers(state PlannerState) []uint64 {
	loads := slotLoads(state.Assignments)
	type candidate struct {
		nodeID uint64
		load   int
	}
	candidates := make([]candidate, 0, len(state.Nodes))
	for nodeID, node := range state.Nodes {
		if node.Status != controllermeta.NodeStatusAlive {
			continue
		}
		candidates = append(candidates, candidate{nodeID: nodeID, load: loads[nodeID]})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].load == candidates[j].load {
			return candidates[i].nodeID < candidates[j].nodeID
		}
		return candidates[i].load < candidates[j].load
	})

	if len(candidates) > p.cfg.ReplicaN {
		candidates = candidates[:p.cfg.ReplicaN]
	}
	peers := make([]uint64, 0, len(candidates))
	for _, candidate := range candidates {
		peers = append(peers, candidate.nodeID)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })
	return peers
}

func (p *Planner) firstPeerNeedingRepair(state PlannerState, peers []uint64) uint64 {
	for _, peer := range peers {
		node, ok := state.Nodes[peer]
		if !ok || node.Status == controllermeta.NodeStatusDead || node.Status == controllermeta.NodeStatusDraining {
			return peer
		}
	}
	return 0
}

func (p *Planner) selectRepairTarget(state PlannerState, peers []uint64) uint64 {
	loads := slotLoads(state.Assignments)
	type candidate struct {
		nodeID uint64
		load   int
	}
	candidates := make([]candidate, 0, len(state.Nodes))
	for nodeID, node := range state.Nodes {
		if node.Status != controllermeta.NodeStatusAlive {
			continue
		}
		if containsPeer(peers, nodeID) {
			continue
		}
		candidates = append(candidates, candidate{nodeID: nodeID, load: loads[nodeID]})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].load == candidates[j].load {
			return candidates[i].nodeID < candidates[j].nodeID
		}
		return candidates[i].load < candidates[j].load
	})
	if len(candidates) == 0 {
		return 0
	}
	return candidates[0].nodeID
}

func slotLoads(assignments map[uint32]controllermeta.SlotAssignment) map[uint64]int {
	loads := make(map[uint64]int)
	for _, assignment := range assignments {
		for _, peer := range assignment.DesiredPeers {
			loads[peer]++
		}
	}
	return loads
}

func loadExtremes(state PlannerState, loads map[uint64]int) (minNode uint64, minLoad int, maxNode uint64, maxLoad int) {
	first := true
	for nodeID, node := range state.Nodes {
		if node.Status != controllermeta.NodeStatusAlive {
			continue
		}
		load := loads[nodeID]
		if first {
			minNode, minLoad = nodeID, load
			maxNode, maxLoad = nodeID, load
			first = false
			continue
		}
		if load < minLoad || (load == minLoad && nodeID < minNode) {
			minNode, minLoad = nodeID, load
		}
		if load > maxLoad || (load == maxLoad && nodeID < maxNode) {
			maxNode, maxLoad = nodeID, load
		}
	}
	return minNode, minLoad, maxNode, maxLoad
}

func containsPeer(peers []uint64, nodeID uint64) bool {
	for _, peer := range peers {
		if peer == nodeID {
			return true
		}
	}
	return false
}

func replacePeer(peers []uint64, source, target uint64) []uint64 {
	next := make([]uint64, 0, len(peers))
	for _, peer := range peers {
		if peer == source {
			continue
		}
		next = append(next, peer)
	}
	next = append(next, target)
	sort.Slice(next, func(i, j int) bool { return next[i] < next[j] })
	return next
}

func taskRunnable(now time.Time, task controllermeta.ReconcileTask) bool {
	switch task.Status {
	case controllermeta.TaskStatusPending:
		return true
	case controllermeta.TaskStatusRetrying:
		return !task.NextRunAt.After(now)
	default:
		return false
	}
}
