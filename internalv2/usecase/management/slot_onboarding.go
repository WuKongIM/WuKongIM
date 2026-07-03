package management

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// DefaultMaxSlotMoves is the default number of Slot replica moves created per onboarding request.
	DefaultMaxSlotMoves uint32 = 1
	// MaxSlotMoves is the hard cap for Slot replica moves created per onboarding request.
	MaxSlotMoves uint32 = 5
)

const (
	// NodeOnboardingSkipActiveTask reports a Slot skipped because an active Controller task already owns it.
	NodeOnboardingSkipActiveTask = "active_task"
	// NodeOnboardingSkipTargetAlreadyPeer reports a Slot skipped because the target already hosts it.
	NodeOnboardingSkipTargetAlreadyPeer = "target_already_peer"
	// NodeOnboardingSkipNoSourcePeer reports a Slot skipped because no existing peer can be replaced.
	NodeOnboardingSkipNoSourcePeer = "no_source_peer"
	// NodeOnboardingSkipMaxMovesReached reports a Slot skipped after the request bound is reached.
	NodeOnboardingSkipMaxMovesReached = "max_slot_moves_reached"
	// NodeOnboardingSkipControlConflict reports that onboarding stopped after concurrent control-state changes.
	NodeOnboardingSkipControlConflict = "control_conflict"
)

var (
	// ErrNodeOnboardingUnavailable reports that node onboarding dependencies are unavailable.
	ErrNodeOnboardingUnavailable = errors.New("internalv2/usecase/management: node onboarding unavailable")
	// ErrNodeOnboardingTargetNotActive reports that the target is not a schedulable active data node.
	ErrNodeOnboardingTargetNotActive = errors.New("internalv2/usecase/management: node onboarding target is not schedulable active data node")
	// ErrNodeOnboardingConflict reports a concurrent control-state change during onboarding writes.
	ErrNodeOnboardingConflict = errors.New("internalv2/usecase/management: node onboarding conflict")
)

// SlotReplicaMoveWriter submits Controller-backed staged Slot replica move intents.
type SlotReplicaMoveWriter interface {
	// RequestSlotReplicaMove submits a validated staged Slot replica move request.
	RequestSlotReplicaMove(context.Context, control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error)
}

// NodeOnboardingPlanRequest describes a bounded Slot onboarding preview request.
type NodeOnboardingPlanRequest struct {
	// TargetNodeID is the schedulable active data node that should receive Slot replicas.
	TargetNodeID uint64
	// MaxSlotMoves bounds the number of Slot replica move candidates.
	MaxSlotMoves uint32
}

// NodeOnboardingStartRequest describes a bounded Slot onboarding execute request.
type NodeOnboardingStartRequest struct {
	// TargetNodeID is the schedulable active data node that should receive Slot replicas.
	TargetNodeID uint64
	// MaxSlotMoves bounds the number of Slot replica move tasks to create.
	MaxSlotMoves uint32
}

// NodeOnboardingAdvanceRequest describes a bounded manual onboarding advance request.
type NodeOnboardingAdvanceRequest struct {
	// TargetNodeID is the schedulable active data node that should receive Slot replicas.
	TargetNodeID uint64
	// MaxSlotMoves bounds the number of additional Slot replica move tasks to create.
	MaxSlotMoves uint32
}

// NodeOnboardingStatusRequest selects one target node's onboarding task status.
type NodeOnboardingStatusRequest struct {
	// TargetNodeID is the active data node being onboarded.
	TargetNodeID uint64
}

// NodeOnboardingCandidate describes one planned Slot replica move.
type NodeOnboardingCandidate struct {
	// SlotID is the physical Slot selected for a move.
	SlotID uint32
	// SourceNodeID is the current desired peer that will be replaced.
	SourceNodeID uint64
	// TargetNodeID is the onboarding target node.
	TargetNodeID uint64
	// TargetPeers is the desired peer set after replacing SourceNodeID with TargetNodeID.
	TargetPeers []uint64
	// ConfigEpoch fences the move to the observed Slot assignment.
	ConfigEpoch uint64
}

// NodeOnboardingSkip describes one Slot excluded from an onboarding plan.
type NodeOnboardingSkip struct {
	// SlotID is the physical Slot that was skipped.
	SlotID uint32
	// Reason is a stable low-cardinality skip reason.
	Reason string
	// Message is a short operator-facing explanation.
	Message string
}

// NodeOnboardingPlanResponse is a bounded deterministic Slot onboarding preview.
type NodeOnboardingPlanResponse struct {
	// GeneratedAt records when the plan was assembled.
	GeneratedAt time.Time
	// StateRevision fences the plan to the observed control snapshot.
	StateRevision uint64
	// TargetNodeID is the schedulable active data node that should receive Slot replicas.
	TargetNodeID uint64
	// MaxSlotMoves is the normalized request bound.
	MaxSlotMoves uint32
	// Candidates contains Slot move candidates in stable Slot ID order.
	Candidates []NodeOnboardingCandidate
	// Skipped contains excluded Slots in stable Slot ID order.
	Skipped []NodeOnboardingSkip
}

// NodeOnboardingTaskResult describes one submitted Slot replica move task.
type NodeOnboardingTaskResult struct {
	// SlotID is the physical Slot submitted to the Controller.
	SlotID uint32
	// Created reports whether a new durable task was accepted.
	Created bool
	// Task contains the created or existing task when returned by control.
	Task *SlotTask
}

// NodeOnboardingStartResponse reports bounded task creation results.
type NodeOnboardingStartResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt time.Time
	// StateRevision fences the request to the observed control snapshot.
	StateRevision uint64
	// TargetNodeID is the active data node that should receive Slot replicas.
	TargetNodeID uint64
	// MaxSlotMoves is the normalized request bound.
	MaxSlotMoves uint32
	// Created is the number of newly accepted tasks.
	Created uint32
	// Results contains one row per submitted candidate.
	Results []NodeOnboardingTaskResult
	// Skipped contains excluded Slots from the underlying plan.
	Skipped []NodeOnboardingSkip
}

// NodeOnboardingStatusSummary contains aggregate active task counts.
type NodeOnboardingStatusSummary struct {
	// TotalActive is the number of active replica-move tasks for the target.
	TotalActive int
	// Pending is the number of pending replica-move tasks.
	Pending int
	// Running is the number of running replica-move tasks.
	Running int
	// Failed is the number of failed replica-move tasks.
	Failed int
}

// NodeOnboardingStatusResponse summarizes active Slot replica move tasks for one target.
type NodeOnboardingStatusResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt time.Time
	// StateRevision is the observed control snapshot revision.
	StateRevision uint64
	// TargetNodeID is the selected onboarding target.
	TargetNodeID uint64
	// Summary contains aggregate active task counts.
	Summary NodeOnboardingStatusSummary
	// Tasks contains active replica-move tasks for the target in Slot ID order.
	Tasks []SlotTask
}

// PlanNodeOnboarding returns a deterministic bounded Slot replica move preview.
func (a *App) PlanNodeOnboarding(ctx context.Context, req NodeOnboardingPlanRequest) (NodeOnboardingPlanResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeOnboardingPlanResponse{}, err
	}
	if req.TargetNodeID == 0 {
		return NodeOnboardingPlanResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.cluster == nil {
		return NodeOnboardingPlanResponse{}, ErrNodeOnboardingUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeOnboardingPlanResponse{}, err
	}
	if !nodeOnboardingTargetSchedulable(snapshot.Nodes, req.TargetNodeID) {
		return NodeOnboardingPlanResponse{}, ErrNodeOnboardingTargetNotActive
	}
	maxMoves := normalizeNodeOnboardingMaxMoves(req.MaxSlotMoves)
	response := NodeOnboardingPlanResponse{
		GeneratedAt:   a.now(),
		StateRevision: snapshot.Revision,
		TargetNodeID:  req.TargetNodeID,
		MaxSlotMoves:  maxMoves,
		Candidates:    make([]NodeOnboardingCandidate, 0, maxMoves),
		Skipped:       make([]NodeOnboardingSkip, 0),
	}

	assignments := append([]control.SlotAssignment(nil), snapshot.Slots...)
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].SlotID < assignments[j].SlotID })
	projectedReplicas := nodeOnboardingReplicaCounts(assignments)
	applyNodeOnboardingActiveTaskProjection(snapshot.Tasks, projectedReplicas)
	for _, assignment := range assignments {
		if _, ok := findActiveSlotTask(snapshot.Tasks, assignment.SlotID); ok {
			response.Skipped = append(response.Skipped, nodeOnboardingSkip(assignment.SlotID, NodeOnboardingSkipActiveTask, "slot already has an active task"))
			continue
		}
		if containsUint64(assignment.DesiredPeers, req.TargetNodeID) {
			response.Skipped = append(response.Skipped, nodeOnboardingSkip(assignment.SlotID, NodeOnboardingSkipTargetAlreadyPeer, "target node already hosts the slot"))
			continue
		}
		source, ok := bestReplaceableNodeOnboardingPeer(assignment.DesiredPeers, req.TargetNodeID, projectedReplicas)
		if !ok {
			response.Skipped = append(response.Skipped, nodeOnboardingSkip(assignment.SlotID, NodeOnboardingSkipNoSourcePeer, "slot has no replaceable source peer"))
			continue
		}
		if uint32(len(response.Candidates)) >= maxMoves {
			response.Skipped = append(response.Skipped, nodeOnboardingSkip(assignment.SlotID, NodeOnboardingSkipMaxMovesReached, "maximum slot moves reached"))
			continue
		}
		targetPeers := replaceNodeOnboardingPeer(assignment.DesiredPeers, source, req.TargetNodeID)
		response.Candidates = append(response.Candidates, NodeOnboardingCandidate{
			SlotID:       assignment.SlotID,
			SourceNodeID: source,
			TargetNodeID: req.TargetNodeID,
			TargetPeers:  targetPeers,
			ConfigEpoch:  assignment.ConfigEpoch,
		})
		projectedReplicas[source]--
		projectedReplicas[req.TargetNodeID]++
	}
	return cloneNodeOnboardingPlan(response), nil
}

// StartNodeOnboarding creates a bounded set of staged Slot replica move tasks.
func (a *App) StartNodeOnboarding(ctx context.Context, req NodeOnboardingStartRequest) (NodeOnboardingStartResponse, error) {
	return a.executeNodeOnboarding(ctx, req.TargetNodeID, req.MaxSlotMoves)
}

// AdvanceNodeOnboarding creates another bounded set of staged Slot replica move tasks.
func (a *App) AdvanceNodeOnboarding(ctx context.Context, req NodeOnboardingAdvanceRequest) (NodeOnboardingStartResponse, error) {
	return a.executeNodeOnboarding(ctx, req.TargetNodeID, req.MaxSlotMoves)
}

// NodeOnboardingStatus returns active staged Slot move tasks for one target.
func (a *App) NodeOnboardingStatus(ctx context.Context, req NodeOnboardingStatusRequest) (NodeOnboardingStatusResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeOnboardingStatusResponse{}, err
	}
	if req.TargetNodeID == 0 {
		return NodeOnboardingStatusResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.cluster == nil {
		return NodeOnboardingStatusResponse{}, ErrNodeOnboardingUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeOnboardingStatusResponse{}, err
	}
	return nodeOnboardingStatusFromSnapshot(snapshot, req.TargetNodeID, a.now()), nil
}

func nodeOnboardingStatusFromSnapshot(snapshot control.Snapshot, targetNodeID uint64, generatedAt time.Time) NodeOnboardingStatusResponse {
	response := NodeOnboardingStatusResponse{
		GeneratedAt:   generatedAt,
		StateRevision: snapshot.Revision,
		TargetNodeID:  targetNodeID,
		Tasks:         make([]SlotTask, 0),
	}
	tasks := append([]control.ReconcileTask(nil), snapshot.Tasks...)
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].SlotID != tasks[j].SlotID {
			return tasks[i].SlotID < tasks[j].SlotID
		}
		return tasks[i].TaskID < tasks[j].TaskID
	})
	for _, task := range tasks {
		if task.Kind != control.TaskKindSlotReplicaMove || task.TargetNode != targetNodeID {
			continue
		}
		response.Tasks = append(response.Tasks, *slotTaskFromControl(task))
		switch task.Status {
		case control.TaskStatusPending:
			response.Summary.Pending++
		case control.TaskStatusRunning:
			response.Summary.Running++
		case control.TaskStatusFailed:
			response.Summary.Failed++
		}
	}
	response.Summary.TotalActive = len(response.Tasks)
	response.Tasks = cloneSlotTasks(response.Tasks)
	return response
}

func (a *App) executeNodeOnboarding(ctx context.Context, targetNodeID uint64, maxSlotMoves uint32) (NodeOnboardingStartResponse, error) {
	maxMoves := normalizeNodeOnboardingMaxMoves(maxSlotMoves)
	response := NodeOnboardingStartResponse{
		TargetNodeID: targetNodeID,
		MaxSlotMoves: maxMoves,
		Results:      make([]NodeOnboardingTaskResult, 0, maxMoves),
	}
	retryBudget := int(maxMoves) * 2
	for response.Created < maxMoves {
		plan, err := a.PlanNodeOnboarding(ctx, NodeOnboardingPlanRequest{TargetNodeID: targetNodeID, MaxSlotMoves: 1})
		if err != nil {
			return NodeOnboardingStartResponse{}, err
		}
		if response.GeneratedAt.IsZero() {
			response.GeneratedAt = plan.GeneratedAt
		}
		response.StateRevision = plan.StateRevision
		response.TargetNodeID = plan.TargetNodeID
		response.Skipped = append([]NodeOnboardingSkip(nil), plan.Skipped...)
		if len(plan.Candidates) == 0 {
			return response, nil
		}
		if a == nil || a.slotReplicaMove == nil {
			return NodeOnboardingStartResponse{}, ErrNodeOnboardingUnavailable
		}
		candidate := plan.Candidates[0]
		result, err := a.slotReplicaMove.RequestSlotReplicaMove(ctx, control.SlotReplicaMoveRequest{
			SlotID:        candidate.SlotID,
			SourceNode:    candidate.SourceNodeID,
			TargetNode:    candidate.TargetNodeID,
			TargetPeers:   append([]uint64(nil), candidate.TargetPeers...),
			ConfigEpoch:   candidate.ConfigEpoch,
			StateRevision: plan.StateRevision,
		})
		if err != nil {
			if nodeOnboardingRetryableWriteError(err) {
				if retryBudget <= 0 {
					if response.Created > 0 {
						response.Skipped = append([]NodeOnboardingSkip{
							nodeOnboardingSkip(candidate.SlotID, NodeOnboardingSkipControlConflict, "control state changed while creating onboarding tasks"),
						}, response.Skipped...)
						return response, nil
					}
					return NodeOnboardingStartResponse{}, ErrNodeOnboardingConflict
				}
				retryBudget--
				continue
			}
			return NodeOnboardingStartResponse{}, err
		}
		if result.Created {
			response.Created++
		}
		response.Results = append(response.Results, NodeOnboardingTaskResult{
			SlotID:  candidate.SlotID,
			Created: result.Created,
			Task:    slotTaskFromControlPtr(result.Task),
		})
		if !result.Created {
			return response, nil
		}
	}
	return response, nil
}

func normalizeNodeOnboardingMaxMoves(maxMoves uint32) uint32 {
	if maxMoves == 0 {
		return DefaultMaxSlotMoves
	}
	if maxMoves > MaxSlotMoves {
		return MaxSlotMoves
	}
	return maxMoves
}

func nodeOnboardingTargetSchedulable(nodes []control.Node, targetNodeID uint64) bool {
	for _, node := range nodes {
		if node.NodeID == targetNodeID {
			return control.NodeSchedulableForPlacement(node)
		}
	}
	return false
}

func nodeOnboardingReplicaCounts(assignments []control.SlotAssignment) map[uint64]int {
	counts := make(map[uint64]int)
	for _, assignment := range assignments {
		for _, peer := range assignment.DesiredPeers {
			if peer != 0 {
				counts[peer]++
			}
		}
	}
	return counts
}

func applyNodeOnboardingActiveTaskProjection(tasks []control.ReconcileTask, counts map[uint64]int) {
	for _, task := range tasks {
		if task.Kind != control.TaskKindSlotReplicaMove {
			continue
		}
		if task.SourceNode != 0 {
			counts[task.SourceNode]--
		}
		if task.TargetNode != 0 {
			counts[task.TargetNode]++
		}
	}
}

func nodeOnboardingRetryableWriteError(err error) bool {
	return cv2.IsExpectedRevisionMismatch(err) || cv2.IsTaskPhaseMismatch(err) || errors.Is(err, cv2.ErrSlotActiveTaskConflict)
}

func bestReplaceableNodeOnboardingPeer(peers []uint64, targetNodeID uint64, projectedReplicas map[uint64]int) (uint64, bool) {
	var best uint64
	bestCount := -1
	for _, peer := range peers {
		if peer == 0 || peer == targetNodeID {
			continue
		}
		count := projectedReplicas[peer]
		if best == 0 || count > bestCount || (count == bestCount && peer < best) {
			best = peer
			bestCount = count
		}
	}
	return best, best != 0
}

func replaceNodeOnboardingPeer(peers []uint64, sourceNodeID uint64, targetNodeID uint64) []uint64 {
	out := append([]uint64(nil), peers...)
	for i, peer := range out {
		if peer == sourceNodeID {
			out[i] = targetNodeID
			break
		}
	}
	return out
}

func nodeOnboardingSkip(slotID uint32, reason string, message string) NodeOnboardingSkip {
	return NodeOnboardingSkip{SlotID: slotID, Reason: reason, Message: message}
}

func cloneNodeOnboardingPlan(plan NodeOnboardingPlanResponse) NodeOnboardingPlanResponse {
	plan.Candidates = cloneNodeOnboardingCandidates(plan.Candidates)
	plan.Skipped = append([]NodeOnboardingSkip(nil), plan.Skipped...)
	return plan
}

func cloneNodeOnboardingCandidates(items []NodeOnboardingCandidate) []NodeOnboardingCandidate {
	out := make([]NodeOnboardingCandidate, len(items))
	for i, item := range items {
		out[i] = item
		out[i].TargetPeers = append([]uint64(nil), item.TargetPeers...)
	}
	return out
}

func cloneSlotTasks(items []SlotTask) []SlotTask {
	out := make([]SlotTask, len(items))
	for i, item := range items {
		out[i] = item
		out[i].TargetPeers = append([]uint64(nil), item.TargetPeers...)
		out[i].Participants = append([]SlotTaskParticipant(nil), item.Participants...)
	}
	return out
}
