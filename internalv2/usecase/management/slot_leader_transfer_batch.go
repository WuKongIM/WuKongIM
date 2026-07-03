package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// DefaultSlotLeaderTransferBatchMaxTasks is the default create-task cap for one batch plan.
	DefaultSlotLeaderTransferBatchMaxTasks = 32
	// MaxSlotLeaderTransferBatchMaxTasks is the largest create-task cap accepted by the planner.
	MaxSlotLeaderTransferBatchMaxTasks = 128

	// SlotLeaderTransferTargetPolicyLeastLeaders selects the eligible target with the fewest projected leaders.
	SlotLeaderTransferTargetPolicyLeastLeaders = "least_leaders"

	// SlotLeaderTransferBatchActionCreate reports that execute would create a new task.
	SlotLeaderTransferBatchActionCreate = "create"
	// SlotLeaderTransferBatchActionExisting reports that a matching active task already exists.
	SlotLeaderTransferBatchActionExisting = "existing"

	// SlotLeaderTransferBatchResultCreated reports that execute created a transfer task.
	SlotLeaderTransferBatchResultCreated = "created"
	// SlotLeaderTransferBatchResultExisting reports that execute found or reused an existing task.
	SlotLeaderTransferBatchResultExisting = "existing"
	// SlotLeaderTransferBatchResultAlreadyLeader reports that the Slot is already led by the target.
	SlotLeaderTransferBatchResultAlreadyLeader = "already_leader"
	// SlotLeaderTransferBatchResultFailed reports that one Slot failed during execute.
	SlotLeaderTransferBatchResultFailed = "failed"

	// SlotLeaderTransferBatchSkipSlotNotAllowed reports that a Slot is outside the request allow-list.
	SlotLeaderTransferBatchSkipSlotNotAllowed = "slot_not_allowed"
	// SlotLeaderTransferBatchSkipAssignmentMissing reports that the requested Slot has no assignment.
	SlotLeaderTransferBatchSkipAssignmentMissing = "assignment_missing"
	// SlotLeaderTransferBatchSkipSinglePeerSlot reports that the Slot cannot transfer with one desired peer.
	SlotLeaderTransferBatchSkipSinglePeerSlot = "single_peer_slot"
	// SlotLeaderTransferBatchSkipSourceNotDesiredPeer reports that the source is not assigned to the Slot.
	SlotLeaderTransferBatchSkipSourceNotDesiredPeer = "source_not_desired_peer"
	// SlotLeaderTransferBatchSkipRuntimeUnavailable reports that live Slot runtime status could not be read.
	SlotLeaderTransferBatchSkipRuntimeUnavailable = "runtime_unavailable"
	// SlotLeaderTransferBatchSkipLeaderUnknown reports that the Slot runtime leader is unknown.
	SlotLeaderTransferBatchSkipLeaderUnknown = "leader_unknown"
	// SlotLeaderTransferBatchSkipSourceNotLeaderOrPreferred reports that source is neither actual nor preferred leader.
	SlotLeaderTransferBatchSkipSourceNotLeaderOrPreferred = "source_not_leader_or_preferred"
	// SlotLeaderTransferBatchSkipQuorumUnavailable reports that current voters cannot form a quorum.
	SlotLeaderTransferBatchSkipQuorumUnavailable = "quorum_unavailable"
	// SlotLeaderTransferBatchSkipActiveTaskConflict reports that a different active task already owns the Slot.
	SlotLeaderTransferBatchSkipActiveTaskConflict = "active_task_conflict"
	// SlotLeaderTransferBatchSkipMatchingTaskExists reports that a matching task was found but cannot be reused.
	SlotLeaderTransferBatchSkipMatchingTaskExists = "matching_task_exists"
	// SlotLeaderTransferBatchSkipTargetInvalid reports that no valid target can be selected.
	SlotLeaderTransferBatchSkipTargetInvalid = "target_invalid"
	// SlotLeaderTransferBatchSkipTargetNotActiveDataNode reports that the target is absent or not active data-capable.
	// The stable wire value keeps its legacy "alive" name for API compatibility.
	SlotLeaderTransferBatchSkipTargetNotActiveDataNode = "target_not_alive_data_node"
	// SlotLeaderTransferBatchSkipTargetNotAliveDataNode is a legacy-named alias for active data lifecycle validation.
	SlotLeaderTransferBatchSkipTargetNotAliveDataNode = SlotLeaderTransferBatchSkipTargetNotActiveDataNode
	// SlotLeaderTransferBatchSkipTargetNotDesiredPeer reports that the target is outside desired peers.
	SlotLeaderTransferBatchSkipTargetNotDesiredPeer = "target_not_desired_peer"
	// SlotLeaderTransferBatchSkipTargetNotCurrentVoter reports that the target is not in current Slot voters.
	SlotLeaderTransferBatchSkipTargetNotCurrentVoter = "target_not_current_voter"
	// SlotLeaderTransferBatchSkipAlreadyOnTarget reports that the Slot already has the requested target as leader.
	SlotLeaderTransferBatchSkipAlreadyOnTarget = "already_on_target"
	// SlotLeaderTransferBatchSkipMaxTasksReached reports that the create-task cap was reached.
	SlotLeaderTransferBatchSkipMaxTasksReached = "max_tasks_reached"
)

var (
	// ErrSlotLeaderTransferPlanStale reports that execute observed a newer control-state revision.
	ErrSlotLeaderTransferPlanStale = errors.New("internalv2/usecase/management: slot leader transfer plan stale")
	// ErrSlotLeaderTransferPlanMismatch reports that execute received a plan that does not match the request.
	ErrSlotLeaderTransferPlanMismatch = errors.New("internalv2/usecase/management: slot leader transfer plan mismatch")
)

// SlotLeaderTransferBatchPlanRequest describes a manager batch planning request.
type SlotLeaderTransferBatchPlanRequest struct {
	// SourceNodeID is the node whose Slot leadership should be moved away.
	SourceNodeID uint64
	// TargetNodeID is the explicit desired target node, or zero to use TargetPolicy.
	TargetNodeID uint64
	// SlotIDs optionally restricts planning to the listed physical Slots.
	SlotIDs []uint32
	// MaxTasks caps how many new leader-transfer tasks execute may create.
	MaxTasks int
	// TargetPolicy selects targets when TargetNodeID is zero.
	TargetPolicy string
}

// SlotLeaderTransferBatchPlanResponse is the deterministic batch planning result.
type SlotLeaderTransferBatchPlanResponse struct {
	// GeneratedAt records when this plan was assembled.
	GeneratedAt time.Time
	// StateRevision is the control-state revision used by the planner.
	StateRevision uint64
	// PlanID is the deterministic identity of the normalized plan.
	PlanID string
	// SourceNodeID is the node whose Slot leadership should move away.
	SourceNodeID uint64
	// TargetPolicy is the normalized target-selection policy.
	TargetPolicy string
	// MaxTasks is the normalized create-task cap.
	MaxTasks int
	// Summary contains aggregate counts for candidates and skipped Slots.
	Summary SlotLeaderTransferBatchPlanSummary
	// Candidates contains ordered Slots that execute can create or reuse.
	Candidates []SlotLeaderTransferBatchCandidate
	// Skipped contains ordered Slots that could not be planned.
	Skipped []SlotLeaderTransferBatchSkip
}

// SlotLeaderTransferBatchPlanSummary contains aggregate plan counters.
type SlotLeaderTransferBatchPlanSummary struct {
	// Scanned counts assignments considered after allow-list filtering.
	Scanned int
	// Candidates counts Slots included in the plan.
	Candidates int
	// Skipped counts Slots excluded from the plan.
	Skipped int
	// ExistingTasks counts candidates backed by matching active tasks.
	ExistingTasks int
	// WouldCreate counts candidates that would create new tasks.
	WouldCreate int
}

// SlotLeaderTransferBatchCandidate describes one Slot that can be transferred.
type SlotLeaderTransferBatchCandidate struct {
	// SlotID is the physical Slot identifier.
	SlotID uint32
	// SourceNodeID is the node whose leadership is being moved away.
	SourceNodeID uint64
	// TargetNodeID is the selected target leader node.
	TargetNodeID uint64
	// PreferredLeader is the control-plane preferred leader from the assignment.
	PreferredLeader uint64
	// ActualLeader is the live Slot Raft leader observed during planning.
	ActualLeader uint64
	// DesiredPeers is the desired Slot replica set for the assignment epoch.
	DesiredPeers []uint64
	// CurrentVoters is the live Slot Raft voter set observed during planning.
	CurrentVoters []uint64
	// ConfigEpoch fences the candidate to the observed Slot assignment epoch.
	ConfigEpoch uint64
	// ExistingTaskID is set when Action is existing.
	ExistingTaskID string
	// Action reports whether execute would create or reuse a task.
	Action string
}

// SlotLeaderTransferBatchSkip describes one Slot excluded from the plan.
type SlotLeaderTransferBatchSkip struct {
	// SlotID is the physical Slot identifier.
	SlotID uint32
	// Reason is a stable machine-readable skip reason.
	Reason string
	// Message is a short operator-facing explanation.
	Message string
}

// SlotLeaderTransferBatchExecuteRequest describes a fenced batch execute request.
type SlotLeaderTransferBatchExecuteRequest struct {
	// SourceNodeID is the node whose Slot leadership should be moved away.
	SourceNodeID uint64
	// TargetNodeID is the explicit desired target node, or zero to use TargetPolicy.
	TargetNodeID uint64
	// SlotIDs optionally restricts execution to the listed physical Slots.
	SlotIDs []uint32
	// MaxTasks caps how many new leader-transfer tasks execute may create.
	MaxTasks int
	// TargetPolicy selects targets when TargetNodeID is zero.
	TargetPolicy string
	// StateRevision is the control-state revision observed by the accepted plan.
	StateRevision uint64
	// PlanID is the deterministic identity of the accepted plan.
	PlanID string
}

// SlotLeaderTransferBatchExecuteResponse reports per-Slot batch execute outcomes.
type SlotLeaderTransferBatchExecuteResponse struct {
	// GeneratedAt records when this execute response was assembled.
	GeneratedAt time.Time
	// StateRevision is the control-state revision used by the recomputed plan.
	StateRevision uint64
	// PlanID is the deterministic identity of the recomputed plan.
	PlanID string
	// Summary contains aggregate execute counters.
	Summary SlotLeaderTransferBatchExecuteSummary
	// Results contains one ordered row for each executed candidate.
	Results []SlotLeaderTransferBatchExecuteResult
}

// SlotLeaderTransferBatchExecuteSummary contains aggregate execute counters.
type SlotLeaderTransferBatchExecuteSummary struct {
	// Requested counts candidate rows considered by execute.
	Requested int
	// Created counts new transfer tasks accepted by the writer.
	Created int
	// Existing counts candidates or writer responses backed by existing tasks.
	Existing int
	// AlreadyLeader counts no-op rows where the target already leads the Slot.
	AlreadyLeader int
	// Skipped counts rows that execute skipped without a writer call.
	Skipped int
	// Failed counts per-Slot writer failures.
	Failed int
}

// SlotLeaderTransferBatchExecuteResult describes one Slot execute outcome.
type SlotLeaderTransferBatchExecuteResult struct {
	// SlotID is the physical Slot identifier.
	SlotID uint32
	// TargetNodeID is the target leader node for this Slot.
	TargetNodeID uint64
	// Status is a stable machine-readable execute status.
	Status string
	// TaskID is the Controller task identifier when one is available.
	TaskID string
	// Message is a short operator-facing result explanation.
	Message string
}

// PlanSlotLeaderTransfers validates and plans a batch of Slot leader transfers.
func (a *App) PlanSlotLeaderTransfers(ctx context.Context, req SlotLeaderTransferBatchPlanRequest) (SlotLeaderTransferBatchPlanResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLeaderTransferBatchPlanResponse{}, err
	}
	normalized, allowedSlots, err := normalizeSlotLeaderTransferBatchPlanRequest(req)
	if err != nil {
		return SlotLeaderTransferBatchPlanResponse{}, err
	}
	if a == nil || a.cluster == nil {
		return SlotLeaderTransferBatchPlanResponse{}, ErrSlotLeaderTransferUnavailable
	}
	if a.slotRuntimeStatus == nil {
		return SlotLeaderTransferBatchPlanResponse{}, ErrSlotRuntimeStatusUnavailable
	}
	return a.planSlotLeaderTransfers(ctx, normalized, allowedSlots)
}

// ExecuteSlotLeaderTransferBatch re-plans and submits fenced Slot leader-transfer candidates.
func (a *App) ExecuteSlotLeaderTransferBatch(ctx context.Context, req SlotLeaderTransferBatchExecuteRequest) (SlotLeaderTransferBatchExecuteResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLeaderTransferBatchExecuteResponse{}, err
	}
	if req.StateRevision == 0 || req.PlanID == "" {
		return SlotLeaderTransferBatchExecuteResponse{}, metadb.ErrInvalidArgument
	}

	plan, err := a.PlanSlotLeaderTransfers(ctx, SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: req.SourceNodeID,
		TargetNodeID: req.TargetNodeID,
		SlotIDs:      append([]uint32(nil), req.SlotIDs...),
		MaxTasks:     req.MaxTasks,
		TargetPolicy: req.TargetPolicy,
	})
	if err != nil {
		return SlotLeaderTransferBatchExecuteResponse{}, err
	}
	if plan.StateRevision != req.StateRevision {
		return SlotLeaderTransferBatchExecuteResponse{}, ErrSlotLeaderTransferPlanStale
	}
	if plan.PlanID != req.PlanID {
		return SlotLeaderTransferBatchExecuteResponse{}, ErrSlotLeaderTransferPlanMismatch
	}

	hasCreate := false
	for _, candidate := range plan.Candidates {
		if candidate.Action == SlotLeaderTransferBatchActionCreate {
			hasCreate = true
			break
		}
	}
	if hasCreate && (a == nil || a.leaderTransfer == nil) {
		return SlotLeaderTransferBatchExecuteResponse{}, ErrSlotLeaderTransferUnavailable
	}

	response := SlotLeaderTransferBatchExecuteResponse{
		GeneratedAt:   a.now(),
		StateRevision: plan.StateRevision,
		PlanID:        plan.PlanID,
		Results:       make([]SlotLeaderTransferBatchExecuteResult, 0, len(plan.Candidates)),
	}
	for _, candidate := range plan.Candidates {
		if err := ctxErr(ctx); err != nil {
			return SlotLeaderTransferBatchExecuteResponse{}, err
		}
		switch candidate.Action {
		case SlotLeaderTransferBatchActionExisting:
			response.Summary.Requested++
			response.Summary.Existing++
			response.Results = append(response.Results, SlotLeaderTransferBatchExecuteResult{
				SlotID:       candidate.SlotID,
				TargetNodeID: candidate.TargetNodeID,
				Status:       SlotLeaderTransferBatchResultExisting,
				TaskID:       candidate.ExistingTaskID,
				Message:      SlotLeaderTransferMessageExistingTask,
			})
		case SlotLeaderTransferBatchActionCreate:
			response.Summary.Requested++
			result, err := a.leaderTransfer.RequestSlotLeaderTransfer(ctx, control.SlotLeaderTransferRequest{
				SlotID:        candidate.SlotID,
				SourceNode:    candidate.SourceNodeID,
				TargetNode:    candidate.TargetNodeID,
				TargetPeers:   append([]uint64(nil), candidate.DesiredPeers...),
				ConfigEpoch:   candidate.ConfigEpoch,
				StateRevision: plan.StateRevision,
			})
			if err != nil {
				response.Summary.Failed++
				response.Results = append(response.Results, SlotLeaderTransferBatchExecuteResult{
					SlotID:       candidate.SlotID,
					TargetNodeID: candidate.TargetNodeID,
					Status:       SlotLeaderTransferBatchResultFailed,
					Message:      err.Error(),
				})
				continue
			}
			status := SlotLeaderTransferBatchResultExisting
			message := SlotLeaderTransferMessageExistingTask
			if result.Created {
				status = SlotLeaderTransferBatchResultCreated
				message = SlotLeaderTransferMessageCreated
				response.Summary.Created++
			} else {
				response.Summary.Existing++
			}
			response.Results = append(response.Results, SlotLeaderTransferBatchExecuteResult{
				SlotID:       candidate.SlotID,
				TargetNodeID: candidate.TargetNodeID,
				Status:       status,
				TaskID:       slotLeaderTransferBatchTaskID(result.Task),
				Message:      message,
			})
		}
	}
	return response, nil
}

func (a *App) planSlotLeaderTransfers(ctx context.Context, req SlotLeaderTransferBatchPlanRequest, allowedSlots []uint32) (SlotLeaderTransferBatchPlanResponse, error) {
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return SlotLeaderTransferBatchPlanResponse{}, err
	}

	response := SlotLeaderTransferBatchPlanResponse{
		GeneratedAt:   a.now(),
		StateRevision: snapshot.Revision,
		SourceNodeID:  req.SourceNodeID,
		TargetPolicy:  req.TargetPolicy,
		MaxTasks:      req.MaxTasks,
	}

	assignments := append([]control.SlotAssignment(nil), snapshot.Slots...)
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].SlotID < assignments[j].SlotID })
	allowSet := uint32Set(allowedSlots)
	seenAllowed := make(map[uint32]struct{}, len(allowedSlots))
	projectedLeaders := make(map[uint64]int)
	rows := make([]slotLeaderTransferBatchPlanRow, 0, len(assignments))

	for _, assignment := range assignments {
		if len(allowSet) > 0 {
			if _, ok := allowSet[assignment.SlotID]; !ok {
				continue
			}
			seenAllowed[assignment.SlotID] = struct{}{}
		}
		response.Summary.Scanned++
		runtime, runtimeErr := a.slotRuntimeStatus.SlotRuntimeStatus(ctx, assignment.SlotID, append([]uint64(nil), assignment.DesiredPeers...))
		if runtimeErr == nil && runtime.LeaderID != 0 {
			projectedLeaders[runtime.LeaderID]++
		}
		rows = append(rows, slotLeaderTransferBatchPlanRow{assignment: assignment, runtime: runtime, runtimeErr: runtimeErr})
	}

	for _, row := range rows {
		planOneSlotLeaderTransfer(snapshot, req, row, projectedLeaders, &response)
	}

	for _, slotID := range allowedSlots {
		if _, ok := seenAllowed[slotID]; ok {
			continue
		}
		appendBatchSkip(&response, slotID, SlotLeaderTransferBatchSkipAssignmentMissing, "slot assignment is missing")
	}

	response.Summary.Candidates = len(response.Candidates)
	response.Summary.Skipped = len(response.Skipped)
	response.PlanID = slotLeaderTransferBatchPlanID(req, snapshot.Revision, response.Candidates)
	response.Candidates = cloneSlotLeaderTransferBatchCandidates(response.Candidates)
	response.Skipped = append([]SlotLeaderTransferBatchSkip(nil), response.Skipped...)
	return response, nil
}

type slotLeaderTransferBatchPlanRow struct {
	assignment control.SlotAssignment
	runtime    SlotRuntimeStatus
	runtimeErr error
}

func planOneSlotLeaderTransfer(snapshot control.Snapshot, req SlotLeaderTransferBatchPlanRequest, row slotLeaderTransferBatchPlanRow, projectedLeaders map[uint64]int, response *SlotLeaderTransferBatchPlanResponse) {
	assignment := row.assignment
	if len(assignment.DesiredPeers) < 2 {
		appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipSinglePeerSlot, "slot has fewer than two desired peers")
		return
	}
	if !containsUint64(assignment.DesiredPeers, req.SourceNodeID) {
		appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipSourceNotDesiredPeer, "source node is not a desired peer")
		return
	}

	if row.runtimeErr != nil {
		appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipRuntimeUnavailable, "slot runtime status is unavailable")
		return
	}
	runtime := row.runtime
	if runtime.LeaderID == 0 {
		appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipLeaderUnknown, "slot leader is unknown")
		return
	}
	if runtime.LeaderID != req.SourceNodeID && assignment.PreferredLeader != req.SourceNodeID {
		appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipSourceNotLeaderOrPreferred, "source node is neither actual nor preferred leader")
		return
	}
	if !containsUint64(runtime.CurrentVoters, runtime.LeaderID) || len(runtime.CurrentVoters) < int(quorumSize(len(assignment.DesiredPeers))) {
		appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipQuorumUnavailable, "current voters cannot prove quorum")
		return
	}

	activeTask, hasActiveTask := findActiveSlotTask(snapshot.Tasks, assignment.SlotID)
	reusableActiveTask := false
	retryableFailedTask := false
	if hasActiveTask {
		reusableActiveTask = canReuseLeaderTransferTask(activeTask, req.SourceNodeID, req.TargetNodeID, assignment)
		retryableFailedTask = canRetryFailedLeaderTransferTask(activeTask, req.SourceNodeID, req.TargetNodeID, assignment)
		if !reusableActiveTask && !retryableFailedTask {
			appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipActiveTaskConflict, "different active task already owns the slot")
			return
		}
	}

	targetNode := req.TargetNodeID
	if hasActiveTask && req.TargetNodeID == 0 && activeTask.Kind == control.TaskKindLeaderTransfer {
		targetNode = activeTask.TargetNode
	}
	if targetNode == 0 {
		var reason string
		targetNode, reason = selectLeastLeadersTarget(snapshot, assignment, runtime, req.SourceNodeID, projectedLeaders)
		if targetNode == 0 {
			appendBatchSkip(response, assignment.SlotID, reason, "no eligible target node found")
			return
		}
	} else if reason := validateBatchTarget(snapshot, assignment, runtime, req.SourceNodeID, targetNode); reason != "" {
		appendBatchSkip(response, assignment.SlotID, reason, batchTargetSkipMessage(reason))
		return
	}

	candidate := SlotLeaderTransferBatchCandidate{
		SlotID:          assignment.SlotID,
		SourceNodeID:    req.SourceNodeID,
		TargetNodeID:    targetNode,
		PreferredLeader: assignment.PreferredLeader,
		ActualLeader:    runtime.LeaderID,
		DesiredPeers:    append([]uint64(nil), assignment.DesiredPeers...),
		CurrentVoters:   append([]uint64(nil), runtime.CurrentVoters...),
		ConfigEpoch:     assignment.ConfigEpoch,
	}

	if reusableActiveTask {
		candidate.Action = SlotLeaderTransferBatchActionExisting
		candidate.ExistingTaskID = activeTask.TaskID
		response.Summary.ExistingTasks++
		appendBatchCandidate(response, candidate)
		applyProjectedLeaderSelection(projectedLeaders, candidate)
		return
	}

	if runtime.LeaderID == req.SourceNodeID && runtime.LeaderID == targetNode {
		appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipAlreadyOnTarget, "slot is already led by target node")
		return
	}

	if response.Summary.WouldCreate >= req.MaxTasks {
		appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipMaxTasksReached, "maximum create-task count reached")
		return
	}
	candidate.Action = SlotLeaderTransferBatchActionCreate
	response.Summary.WouldCreate++
	appendBatchCandidate(response, candidate)
	applyProjectedLeaderSelection(projectedLeaders, candidate)
}

func normalizeSlotLeaderTransferBatchPlanRequest(req SlotLeaderTransferBatchPlanRequest) (SlotLeaderTransferBatchPlanRequest, []uint32, error) {
	if req.SourceNodeID == 0 {
		return SlotLeaderTransferBatchPlanRequest{}, nil, metadb.ErrInvalidArgument
	}
	if req.MaxTasks == 0 {
		req.MaxTasks = DefaultSlotLeaderTransferBatchMaxTasks
	}
	if req.MaxTasks < 0 || req.MaxTasks > MaxSlotLeaderTransferBatchMaxTasks {
		return SlotLeaderTransferBatchPlanRequest{}, nil, metadb.ErrInvalidArgument
	}
	if req.TargetPolicy == "" {
		req.TargetPolicy = SlotLeaderTransferTargetPolicyLeastLeaders
	}
	if req.TargetPolicy != SlotLeaderTransferTargetPolicyLeastLeaders {
		return SlotLeaderTransferBatchPlanRequest{}, nil, metadb.ErrInvalidArgument
	}

	slotIDs := append([]uint32(nil), req.SlotIDs...)
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	deduped := slotIDs[:0]
	var previous uint32
	for i, slotID := range slotIDs {
		if slotID == 0 {
			return SlotLeaderTransferBatchPlanRequest{}, nil, metadb.ErrInvalidArgument
		}
		if i > 0 && slotID == previous {
			continue
		}
		deduped = append(deduped, slotID)
		previous = slotID
	}
	req.SlotIDs = append([]uint32(nil), deduped...)
	return req, append([]uint32(nil), deduped...), nil
}

func uint32Set(items []uint32) map[uint32]struct{} {
	if len(items) == 0 {
		return nil
	}
	out := make(map[uint32]struct{}, len(items))
	for _, item := range items {
		out[item] = struct{}{}
	}
	return out
}

func canReuseLeaderTransferTask(task control.ReconcileTask, sourceNode, requestedTarget uint64, assignment control.SlotAssignment) bool {
	if task.Status != control.TaskStatusPending && task.Status != control.TaskStatusRunning {
		return false
	}
	return leaderTransferTaskMatches(task, sourceNode, requestedTarget, assignment)
}

func canRetryFailedLeaderTransferTask(task control.ReconcileTask, sourceNode, requestedTarget uint64, assignment control.SlotAssignment) bool {
	if task.Status != control.TaskStatusFailed {
		return false
	}
	return leaderTransferTaskMatches(task, sourceNode, requestedTarget, assignment)
}

func leaderTransferTaskMatches(task control.ReconcileTask, sourceNode, requestedTarget uint64, assignment control.SlotAssignment) bool {
	if task.Kind != control.TaskKindLeaderTransfer {
		return false
	}
	if task.SourceNode != sourceNode || task.ConfigEpoch != assignment.ConfigEpoch {
		return false
	}
	if requestedTarget != 0 && task.TargetNode != requestedTarget {
		return false
	}
	return sameUint64Set(task.TargetPeers, assignment.DesiredPeers)
}

func validateBatchTarget(snapshot control.Snapshot, assignment control.SlotAssignment, runtime SlotRuntimeStatus, sourceNode, targetNode uint64) string {
	if targetNode == 0 || targetNode == sourceNode {
		return SlotLeaderTransferBatchSkipTargetInvalid
	}
	if !containsUint64(assignment.DesiredPeers, targetNode) {
		return SlotLeaderTransferBatchSkipTargetNotDesiredPeer
	}
	if !targetIsActiveDataNode(snapshot, targetNode) {
		return SlotLeaderTransferBatchSkipTargetNotActiveDataNode
	}
	if !containsUint64(runtime.CurrentVoters, targetNode) {
		return SlotLeaderTransferBatchSkipTargetNotCurrentVoter
	}
	return ""
}

func selectLeastLeadersTarget(snapshot control.Snapshot, assignment control.SlotAssignment, runtime SlotRuntimeStatus, sourceNode uint64, projectedLeaders map[uint64]int) (uint64, string) {
	var selected uint64
	selectedCount := 0
	blockReason := SlotLeaderTransferBatchSkipTargetInvalid
	for _, peer := range sortedUint64s(assignment.DesiredPeers) {
		if peer == sourceNode {
			continue
		}
		if !containsUint64(runtime.CurrentVoters, peer) {
			blockReason = SlotLeaderTransferBatchSkipTargetNotCurrentVoter
			continue
		}
		if !targetIsActiveDataNode(snapshot, peer) {
			blockReason = SlotLeaderTransferBatchSkipTargetNotActiveDataNode
			continue
		}
		count := projectedLeaders[peer]
		if selected == 0 || count < selectedCount || (count == selectedCount && peer < selected) {
			selected = peer
			selectedCount = count
		}
	}
	if selected == 0 {
		return 0, blockReason
	}
	return selected, ""
}

func targetIsActiveDataNode(snapshot control.Snapshot, nodeID uint64) bool {
	for _, node := range snapshot.Nodes {
		if node.NodeID != nodeID {
			continue
		}
		return isActiveDataNode(node)
	}
	return false
}

// applyProjectedLeaderSelection keeps actual-leader counts intact for preferred-only corrections.
func applyProjectedLeaderSelection(projectedLeaders map[uint64]int, candidate SlotLeaderTransferBatchCandidate) {
	if candidate.TargetNodeID == 0 {
		return
	}
	if candidate.ActualLeader == candidate.SourceNodeID {
		projectedLeaders[candidate.SourceNodeID]--
		projectedLeaders[candidate.TargetNodeID]++
		return
	}
	if candidate.TargetNodeID == candidate.ActualLeader {
		return
	}
	projectedLeaders[candidate.TargetNodeID]++
}

func appendBatchCandidate(response *SlotLeaderTransferBatchPlanResponse, candidate SlotLeaderTransferBatchCandidate) {
	response.Candidates = append(response.Candidates, cloneSlotLeaderTransferBatchCandidate(candidate))
}

func appendBatchSkip(response *SlotLeaderTransferBatchPlanResponse, slotID uint32, reason, message string) {
	response.Skipped = append(response.Skipped, SlotLeaderTransferBatchSkip{SlotID: slotID, Reason: reason, Message: message})
}

func batchTargetSkipMessage(reason string) string {
	switch reason {
	case SlotLeaderTransferBatchSkipTargetNotActiveDataNode:
		return "target node is not an active data node"
	case SlotLeaderTransferBatchSkipTargetNotDesiredPeer:
		return "target node is not a desired peer"
	case SlotLeaderTransferBatchSkipTargetNotCurrentVoter:
		return "target node is not a current voter"
	default:
		return "target node is invalid"
	}
}

func slotLeaderTransferBatchPlanID(req SlotLeaderTransferBatchPlanRequest, revision uint64, candidates []SlotLeaderTransferBatchCandidate) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "source=%d\n", req.SourceNodeID)
	fmt.Fprintf(hash, "target=%d\n", req.TargetNodeID)
	fmt.Fprintf(hash, "policy=%s\n", req.TargetPolicy)
	fmt.Fprintf(hash, "max=%d\n", req.MaxTasks)
	fmt.Fprintf(hash, "slots=%s\n", uint32ListKey(req.SlotIDs))
	fmt.Fprintf(hash, "revision=%d\n", revision)
	for _, candidate := range candidates {
		fmt.Fprintf(hash, "candidate=%d,%d,%s,%s,%d\n", candidate.SlotID, candidate.TargetNodeID, candidate.Action, candidate.ExistingTaskID, candidate.ConfigEpoch)
	}
	sum := hash.Sum(nil)
	return fmt.Sprintf("slot-leader-transfer:%d:%s", revision, hex.EncodeToString(sum[:16]))
}

func uint32ListKey(items []uint32) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%d", item))
	}
	return strings.Join(parts, ",")
}

func sameUint64Set(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := sortedUint64s(a)
	sortedB := sortedUint64s(b)
	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}

func sortedUint64s(items []uint64) []uint64 {
	out := append([]uint64(nil), items...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func cloneSlotLeaderTransferBatchCandidates(items []SlotLeaderTransferBatchCandidate) []SlotLeaderTransferBatchCandidate {
	if len(items) == 0 {
		return nil
	}
	out := make([]SlotLeaderTransferBatchCandidate, len(items))
	for i, item := range items {
		out[i] = cloneSlotLeaderTransferBatchCandidate(item)
	}
	return out
}

func cloneSlotLeaderTransferBatchCandidate(item SlotLeaderTransferBatchCandidate) SlotLeaderTransferBatchCandidate {
	item.DesiredPeers = append([]uint64(nil), item.DesiredPeers...)
	item.CurrentVoters = append([]uint64(nil), item.CurrentVoters...)
	return item
}

func slotLeaderTransferBatchTaskID(task *control.ReconcileTask) string {
	if task == nil {
		return ""
	}
	return task.TaskID
}
