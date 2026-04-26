package plane

import (
	"sort"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// OnboardingPlanner plans safe replica moves for an active target data node.
type OnboardingPlanner struct {
	cfg OnboardingPlannerConfig
}

// OnboardingPlannerConfig controls node onboarding plan generation.
type OnboardingPlannerConfig struct {
	// ReplicaN is reserved for validating replica-sized peer sets in later planner phases.
	ReplicaN int
}

// OnboardingPlanInput is the strict controller leader view used to build one onboarding plan.
type OnboardingPlanInput struct {
	// TargetNodeID is the active data node that should receive resources.
	TargetNodeID uint64
	// Nodes is the durable controller membership snapshot keyed by node id.
	Nodes map[uint64]controllermeta.ClusterNode
	// Assignments is the durable physical slot assignment snapshot keyed by slot id.
	Assignments map[uint32]controllermeta.SlotAssignment
	// Runtime is the leader-local runtime observation snapshot keyed by slot id.
	Runtime map[uint32]controllermeta.SlotRuntimeView
	// Tasks is the durable reconcile task snapshot keyed by slot id.
	Tasks map[uint32]controllermeta.ReconcileTask
	// MigratingSlots identifies physical slots protected by active hash-slot migration.
	MigratingSlots map[uint32]struct{}
	// RunningJobExists blocks a new onboarding plan while another job is running.
	RunningJobExists bool
	// Now is the planning timestamp reserved for future time-sensitive compatibility checks.
	Now time.Time
}

// NewOnboardingPlanner returns a planner for targeted node onboarding.
func NewOnboardingPlanner(cfg OnboardingPlannerConfig) *OnboardingPlanner {
	return &OnboardingPlanner{cfg: cfg}
}

// Plan returns a deterministic node onboarding plan or stable blocked reasons.
func (p *OnboardingPlanner) Plan(input OnboardingPlanInput) controllermeta.NodeOnboardingPlan {
	plan := controllermeta.NodeOnboardingPlan{TargetNodeID: input.TargetNodeID}
	blocked := newOnboardingBlockedReasons(&plan)

	target, ok := input.Nodes[input.TargetNodeID]
	if !ok {
		blocked.add("target_not_active", "node", 0, input.TargetNodeID, "target node is not active")
		return plan
	}
	if target.Role != controllermeta.NodeRoleData {
		blocked.add("target_not_data", "node", 0, input.TargetNodeID, "target node is not a data node")
		return plan
	}
	if target.JoinState != controllermeta.NodeJoinStateActive {
		blocked.add("target_not_active", "node", 0, input.TargetNodeID, "target node is not active")
		return plan
	}
	if target.Status == controllermeta.NodeStatusDraining {
		blocked.add("target_draining", "node", 0, input.TargetNodeID, "target node is draining")
		return plan
	}
	if target.Status != controllermeta.NodeStatusAlive {
		blocked.add("target_not_alive", "node", 0, input.TargetNodeID, "target node is not alive")
		return plan
	}
	if input.RunningJobExists {
		blocked.add("running_job_exists", "cluster", 0, 0, "another onboarding job is running")
		return plan
	}

	loads := slotLoads(input.Assignments)
	activeNodes := countActiveDataNodes(input.Nodes)
	currentTargetLoad := loads[input.TargetNodeID]
	currentTargetLeaders := currentTargetLeaderCount(input.TargetNodeID, input.Assignments, input.Runtime)
	plan.Summary.CurrentTargetSlotCount = currentTargetLoad
	plan.Summary.CurrentTargetLeaderCount = currentTargetLeaders
	plan.Summary.PlannedTargetSlotCount = currentTargetLoad

	if activeNodes == 0 {
		blocked.add("no_active_data_nodes", "cluster", 0, 0, "no active data nodes are available")
		return plan
	}

	totalReplicas := totalReplicaCount(input.Assignments)
	targetMin := totalReplicas / activeNodes
	targetMax := ceilDiv(totalReplicas, activeNodes)
	if currentTargetLoad >= targetMax {
		blocked.add("already_balanced", "node", 0, input.TargetNodeID, "target node is already balanced")
		return plan
	}

	simAssignments := cloneAssignments(input.Assignments)
	simLoads := cloneLoads(loads)
	movedSlots := make(map[uint32]struct{})

	for simLoads[input.TargetNodeID] < targetMax {
		candidates := p.onboardingCandidates(input, simAssignments, simLoads, movedSlots, targetMin, blocked)
		if len(candidates) == 0 {
			break
		}
		selected := candidates[0]
		plan.Moves = append(plan.Moves, controllermeta.NodeOnboardingPlanMove{
			SlotID:             selected.assignment.SlotID,
			SourceNodeID:       selected.sourceNodeID,
			TargetNodeID:       input.TargetNodeID,
			Reason:             "replica_balance",
			DesiredPeersBefore: selected.desiredPeersBefore,
			DesiredPeersAfter:  selected.desiredPeersAfter,
			CurrentLeaderID:    selected.view.LeaderID,
		})

		assignment := simAssignments[selected.assignment.SlotID]
		assignment.DesiredPeers = selected.desiredPeersAfter
		simAssignments[selected.assignment.SlotID] = assignment
		simLoads[selected.sourceNodeID]--
		simLoads[input.TargetNodeID]++
		movedSlots[selected.assignment.SlotID] = struct{}{}
	}

	leaderNeed := maxInt(0, ceilDiv(len(input.Assignments), activeNodes)-currentTargetLeaders)
	plannedLeaderGain := 0
	for i := range plan.Moves {
		if plan.Moves[i].CurrentLeaderID == plan.Moves[i].SourceNodeID && leaderNeed > 0 {
			plan.Moves[i].LeaderTransferRequired = true
			leaderNeed--
			plannedLeaderGain++
		}
	}
	plan.Summary.PlannedLeaderGain = plannedLeaderGain
	plan.Summary.PlannedTargetSlotCount = currentTargetLoad + len(plan.Moves)

	if len(plan.Moves) == 0 && len(plan.BlockedReasons) == 0 && currentTargetLoad < targetMax {
		blocked.add("no_safe_candidate", "cluster", 0, 0, "no safe slot candidate is available")
	}
	return plan
}

type onboardingCandidate struct {
	assignment         controllermeta.SlotAssignment
	sourceNodeID       uint64
	sourceLoad         int
	sourceIsLeader     bool
	desiredPeersBefore []uint64
	desiredPeersAfter  []uint64
	view               controllermeta.SlotRuntimeView
}

func (p *OnboardingPlanner) onboardingCandidates(input OnboardingPlanInput, assignments map[uint32]controllermeta.SlotAssignment, loads map[uint64]int, movedSlots map[uint32]struct{}, targetMin int, blocked *onboardingBlockedReasons) []onboardingCandidate {
	slotIDs := sortedAssignmentIDs(assignments)
	candidates := make([]onboardingCandidate, 0)
	for _, slotID := range slotIDs {
		assignment := assignments[slotID]
		if _, moved := movedSlots[assignment.SlotID]; moved {
			continue
		}
		if containsPeer(assignment.DesiredPeers, input.TargetNodeID) {
			continue
		}
		if _, migrating := input.MigratingSlots[assignment.SlotID]; migrating {
			blocked.add("slot_hash_migration_active", "slot", assignment.SlotID, 0, "slot is protected by hash-slot migration")
			continue
		}
		if task, ok := input.Tasks[assignment.SlotID]; ok {
			if task.Status == controllermeta.TaskStatusFailed {
				blocked.add("slot_task_failed", "slot", assignment.SlotID, 0, "slot has a failed reconcile task")
			} else {
				blocked.add("slot_task_running", "slot", assignment.SlotID, 0, "slot has a reconcile task in progress")
			}
			continue
		}
		view, ok := input.Runtime[assignment.SlotID]
		if !ok {
			blocked.add("no_runtime_view", "slot", assignment.SlotID, 0, "slot has no runtime view")
			continue
		}
		if !view.HasQuorum {
			blocked.add("slot_quorum_lost", "slot", assignment.SlotID, 0, "slot does not have quorum")
			continue
		}

		peersBefore := sortedPeers(assignment.DesiredPeers)
		for _, sourceNodeID := range peersBefore {
			source, ok := input.Nodes[sourceNodeID]
			if !ok || !nodeSchedulableForData(source) {
				continue
			}
			if loads[sourceNodeID] <= targetMin {
				continue
			}
			candidates = append(candidates, onboardingCandidate{
				assignment:         assignment,
				sourceNodeID:       sourceNodeID,
				sourceLoad:         loads[sourceNodeID],
				sourceIsLeader:     view.LeaderID == sourceNodeID,
				desiredPeersBefore: peersBefore,
				desiredPeersAfter:  replacePeer(peersBefore, sourceNodeID, input.TargetNodeID),
				view:               view,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.sourceIsLeader != right.sourceIsLeader {
			return left.sourceIsLeader
		}
		if left.sourceLoad != right.sourceLoad {
			return left.sourceLoad > right.sourceLoad
		}
		if left.assignment.BalanceVersion != right.assignment.BalanceVersion {
			return left.assignment.BalanceVersion < right.assignment.BalanceVersion
		}
		if left.assignment.SlotID != right.assignment.SlotID {
			return left.assignment.SlotID < right.assignment.SlotID
		}
		return left.sourceNodeID < right.sourceNodeID
	})
	return candidates
}

type onboardingBlockedReasons struct {
	plan *controllermeta.NodeOnboardingPlan
	seen map[onboardingBlockedReasonKey]struct{}
}

type onboardingBlockedReasonKey struct {
	code   string
	scope  string
	slotID uint32
	nodeID uint64
}

func newOnboardingBlockedReasons(plan *controllermeta.NodeOnboardingPlan) *onboardingBlockedReasons {
	return &onboardingBlockedReasons{plan: plan, seen: make(map[onboardingBlockedReasonKey]struct{})}
}

func (r *onboardingBlockedReasons) add(code, scope string, slotID uint32, nodeID uint64, message string) {
	key := onboardingBlockedReasonKey{code: code, scope: scope, slotID: slotID, nodeID: nodeID}
	if _, ok := r.seen[key]; ok {
		return
	}
	r.seen[key] = struct{}{}
	r.plan.BlockedReasons = append(r.plan.BlockedReasons, controllermeta.NodeOnboardingBlockedReason{
		Code:    code,
		Scope:   scope,
		SlotID:  slotID,
		NodeID:  nodeID,
		Message: message,
	})
}

func countActiveDataNodes(nodes map[uint64]controllermeta.ClusterNode) int {
	count := 0
	for _, node := range nodes {
		if nodeSchedulableForData(node) {
			count++
		}
	}
	return count
}

func currentTargetLeaderCount(targetNodeID uint64, assignments map[uint32]controllermeta.SlotAssignment, runtime map[uint32]controllermeta.SlotRuntimeView) int {
	count := 0
	for slotID := range assignments {
		if view, ok := runtime[slotID]; ok && view.LeaderID == targetNodeID {
			count++
		}
	}
	return count
}

func totalReplicaCount(assignments map[uint32]controllermeta.SlotAssignment) int {
	total := 0
	for _, assignment := range assignments {
		total += len(assignment.DesiredPeers)
	}
	return total
}

func ceilDiv(numerator, denominator int) int {
	if denominator <= 0 {
		return 0
	}
	if numerator == 0 {
		return 0
	}
	return (numerator + denominator - 1) / denominator
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func cloneAssignments(assignments map[uint32]controllermeta.SlotAssignment) map[uint32]controllermeta.SlotAssignment {
	cloned := make(map[uint32]controllermeta.SlotAssignment, len(assignments))
	for slotID, assignment := range assignments {
		assignment.DesiredPeers = append([]uint64(nil), assignment.DesiredPeers...)
		cloned[slotID] = assignment
	}
	return cloned
}

func cloneLoads(loads map[uint64]int) map[uint64]int {
	cloned := make(map[uint64]int, len(loads))
	for nodeID, load := range loads {
		cloned[nodeID] = load
	}
	return cloned
}

func sortedAssignmentIDs(assignments map[uint32]controllermeta.SlotAssignment) []uint32 {
	slotIDs := make([]uint32, 0, len(assignments))
	for slotID := range assignments {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	return slotIDs
}

func sortedPeers(peers []uint64) []uint64 {
	cloned := append([]uint64(nil), peers...)
	sort.Slice(cloned, func(i, j int) bool { return cloned[i] < cloned[j] })
	return cloned
}
