package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// DefaultSlotLeaderTransferBatchMaxTasks is the default create-task cap for one batch plan.
	DefaultSlotLeaderTransferBatchMaxTasks = 32
	// MaxSlotLeaderTransferBatchMaxTasks is the largest create-task cap accepted by the planner.
	MaxSlotLeaderTransferBatchMaxTasks = 128
	// slotLeaderTransferBatchRetryDelay bounds polling pressure while the write-side Controller snapshot catches up.
	slotLeaderTransferBatchRetryDelay = 25 * time.Millisecond
	// slotLeaderTransferBatchCatchUpWindow bounds how long a batch waits for its
	// local Controller replica to observe already committed write-side revisions.
	slotLeaderTransferBatchCatchUpWindow = 2 * time.Second
	// slotLeaderTransferBatchRetryLimit bounds stale-snapshot retries across the whole batch.
	slotLeaderTransferBatchRetryLimit = int(slotLeaderTransferBatchCatchUpWindow / slotLeaderTransferBatchRetryDelay)

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
	ErrSlotLeaderTransferPlanStale = errors.New("internal/usecase/management: slot leader transfer plan stale")
	// ErrSlotLeaderTransferPlanMismatch reports that execute received a plan that does not match the request.
	ErrSlotLeaderTransferPlanMismatch = errors.New("internal/usecase/management: slot leader transfer plan mismatch")
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
	baseline, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return SlotLeaderTransferBatchExecuteResponse{}, err
	}
	if baseline.Revision != plan.StateRevision {
		return SlotLeaderTransferBatchExecuteResponse{}, ErrSlotLeaderTransferPlanStale
	}
	executionFence, err := newSlotLeaderTransferBatchExecutionFence(baseline, plan.Candidates)
	if err != nil {
		return SlotLeaderTransferBatchExecuteResponse{}, fmt.Errorf("%w: %v", ErrSlotLeaderTransferPlanMismatch, err)
	}

	response := SlotLeaderTransferBatchExecuteResponse{
		GeneratedAt:   a.now(),
		StateRevision: plan.StateRevision,
		PlanID:        plan.PlanID,
		Results:       make([]SlotLeaderTransferBatchExecuteResult, 0, len(plan.Candidates)),
	}
	retryBudget := slotLeaderTransferBatchRetryLimit
	for _, candidate := range plan.Candidates {
		if err := ctxErr(ctx); err != nil {
			return SlotLeaderTransferBatchExecuteResponse{}, err
		}
		switch candidate.Action {
		case SlotLeaderTransferBatchActionExisting:
			response.Summary.Requested++
			for {
				refreshed, _, caughtUp, err := a.refreshSlotLeaderTransferBatchCandidate(ctx, candidate, plan.TargetPolicy, executionFence)
				if err != nil {
					if contextErr := ctxErr(ctx); contextErr != nil {
						return SlotLeaderTransferBatchExecuteResponse{}, contextErr
					}
					appendSlotLeaderTransferBatchFailure(&response, candidate, err)
					break
				}
				if !caughtUp {
					if retryBudget <= 0 {
						appendSlotLeaderTransferBatchFailure(&response, candidate, executionFence.close("Controller snapshot did not catch up within the batch retry budget"))
						break
					}
					retryBudget--
					if err := waitSlotLeaderTransferBatchRetry(ctx); err != nil {
						return SlotLeaderTransferBatchExecuteResponse{}, err
					}
					continue
				}
				if refreshed.Action != SlotLeaderTransferBatchActionExisting || refreshed.ExistingTaskID != candidate.ExistingTaskID {
					appendSlotLeaderTransferBatchFailure(&response, candidate, fmt.Errorf("slot %d approved existing task changed before execution", candidate.SlotID))
					break
				}
				response.Summary.Existing++
				response.Results = append(response.Results, SlotLeaderTransferBatchExecuteResult{
					SlotID:       refreshed.SlotID,
					TargetNodeID: refreshed.TargetNodeID,
					Status:       SlotLeaderTransferBatchResultExisting,
					TaskID:       refreshed.ExistingTaskID,
					Message:      SlotLeaderTransferMessageExistingTask,
				})
				break
			}
		case SlotLeaderTransferBatchActionCreate:
			response.Summary.Requested++
			for {
				refreshed, stateRevision, caughtUp, err := a.refreshSlotLeaderTransferBatchCandidate(ctx, candidate, plan.TargetPolicy, executionFence)
				if err != nil {
					if contextErr := ctxErr(ctx); contextErr != nil {
						return SlotLeaderTransferBatchExecuteResponse{}, contextErr
					}
					appendSlotLeaderTransferBatchFailure(&response, candidate, err)
					break
				}
				if !caughtUp {
					if retryBudget <= 0 {
						appendSlotLeaderTransferBatchFailure(&response, candidate, executionFence.close("Controller snapshot did not catch up within the batch retry budget"))
						break
					}
					retryBudget--
					if err := waitSlotLeaderTransferBatchRetry(ctx); err != nil {
						return SlotLeaderTransferBatchExecuteResponse{}, err
					}
					continue
				}
				if refreshed.Action == SlotLeaderTransferBatchActionExisting {
					response.Summary.Existing++
					response.Results = append(response.Results, SlotLeaderTransferBatchExecuteResult{
						SlotID:       refreshed.SlotID,
						TargetNodeID: refreshed.TargetNodeID,
						Status:       SlotLeaderTransferBatchResultExisting,
						TaskID:       refreshed.ExistingTaskID,
						Message:      SlotLeaderTransferMessageExistingTask,
					})
					break
				}

				result, err := a.leaderTransfer.RequestSlotLeaderTransfer(ctx, control.SlotLeaderTransferRequest{
					SlotID:        refreshed.SlotID,
					SourceNode:    refreshed.SourceNodeID,
					TargetNode:    refreshed.TargetNodeID,
					TargetPeers:   append([]uint64(nil), refreshed.DesiredPeers...),
					ConfigEpoch:   refreshed.ConfigEpoch,
					StateRevision: stateRevision,
				})
				if err != nil && controller.IsExpectedRevisionMismatch(err) {
					if retryBudget <= 0 {
						appendSlotLeaderTransferBatchFailure(&response, candidate, executionFence.close("Controller revision did not catch up within the batch retry budget"))
						break
					}
					retryBudget--
					if err := waitSlotLeaderTransferBatchRetry(ctx); err != nil {
						return SlotLeaderTransferBatchExecuteResponse{}, err
					}
					continue
				}
				if err != nil {
					appendSlotLeaderTransferBatchFailure(&response, candidate, err)
					break
				}
				if err := executionFence.recordWriterResult(refreshed, result, stateRevision); err != nil {
					appendSlotLeaderTransferBatchFailure(&response, candidate, err)
					break
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
					SlotID:       refreshed.SlotID,
					TargetNodeID: refreshed.TargetNodeID,
					Status:       status,
					TaskID:       slotLeaderTransferBatchTaskID(result.Task),
					Message:      message,
				})
				break
			}
		}
	}
	return response, nil
}

func (a *App) refreshSlotLeaderTransferBatchCandidate(ctx context.Context, approved SlotLeaderTransferBatchCandidate, targetPolicy string, executionFence *slotLeaderTransferBatchExecutionFence) (SlotLeaderTransferBatchCandidate, uint64, bool, error) {
	snapshot, err := a.slotLeaderTransferBatchControlSnapshot(ctx)
	if err != nil {
		return SlotLeaderTransferBatchCandidate{}, 0, false, err
	}
	caughtUp, err := executionFence.observe(snapshot)
	if err != nil {
		return SlotLeaderTransferBatchCandidate{}, 0, false, err
	}
	if !caughtUp {
		return SlotLeaderTransferBatchCandidate{}, 0, false, nil
	}
	if approved.Action == SlotLeaderTransferBatchActionExisting {
		if err := executionFence.validateExistingCandidate(approved); err != nil {
			return SlotLeaderTransferBatchCandidate{}, 0, false, err
		}
	}
	plan, err := a.PlanSlotLeaderTransfers(ctx, SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: approved.SourceNodeID,
		TargetNodeID: approved.TargetNodeID,
		SlotIDs:      []uint32{approved.SlotID},
		MaxTasks:     1,
		TargetPolicy: targetPolicy,
	})
	if err != nil {
		return SlotLeaderTransferBatchCandidate{}, 0, false, err
	}
	if len(plan.Candidates) != 1 {
		message := "slot is no longer eligible for the approved transfer"
		if len(plan.Skipped) > 0 && plan.Skipped[0].Message != "" {
			message = plan.Skipped[0].Message
		}
		return SlotLeaderTransferBatchCandidate{}, 0, false, fmt.Errorf("slot %d no longer matches the approved plan: %s", approved.SlotID, message)
	}
	refreshed := plan.Candidates[0]
	if !sameSlotLeaderTransferBatchCandidateFence(approved, refreshed) {
		return SlotLeaderTransferBatchCandidate{}, 0, false, fmt.Errorf("slot %d changed after the batch plan was approved", approved.SlotID)
	}
	return refreshed, executionFence.expectedRevision(), true, nil
}

func (a *App) slotLeaderTransferBatchControlSnapshot(ctx context.Context) (control.Snapshot, error) {
	if reader, ok := a.leaderTransfer.(SlotLeaderTransferControlSnapshotReader); ok {
		return reader.SlotLeaderTransferControlSnapshot(ctx)
	}
	return a.cluster.LocalControlSnapshot(ctx)
}

func sameSlotLeaderTransferBatchCandidateFence(approved, refreshed SlotLeaderTransferBatchCandidate) bool {
	return approved.SlotID == refreshed.SlotID &&
		approved.SourceNodeID == refreshed.SourceNodeID &&
		approved.TargetNodeID == refreshed.TargetNodeID &&
		approved.PreferredLeader == refreshed.PreferredLeader &&
		approved.ConfigEpoch == refreshed.ConfigEpoch &&
		sameUint64Set(approved.DesiredPeers, refreshed.DesiredPeers)
}

// slotLeaderTransferBatchExecutionFence projects only mutations caused by tasks
// that were already approved by the batch. Any other durable Controller change
// permanently closes the fence for the remaining candidates.
type slotLeaderTransferBatchExecutionFence struct {
	expected       control.Snapshot
	allowedTaskIDs map[string]struct{}
	conflictErr    error
}

func newSlotLeaderTransferBatchExecutionFence(snapshot control.Snapshot, candidates []SlotLeaderTransferBatchCandidate) (*slotLeaderTransferBatchExecutionFence, error) {
	fence := &slotLeaderTransferBatchExecutionFence{
		expected:       snapshot.Clone(),
		allowedTaskIDs: make(map[string]struct{}),
	}
	for _, candidate := range candidates {
		if candidate.Action != SlotLeaderTransferBatchActionExisting {
			continue
		}
		index := slotLeaderTransferBatchTaskIndexByID(snapshot.Tasks, candidate.ExistingTaskID)
		if index < 0 || !slotLeaderTransferBatchTaskMatchesCandidate(snapshot.Tasks[index], candidate) {
			return nil, fmt.Errorf("existing task for slot %d changed before execution", candidate.SlotID)
		}
		status := snapshot.Tasks[index].Status
		if status != control.TaskStatusPending && status != control.TaskStatusRunning {
			return nil, fmt.Errorf("existing task for slot %d is no longer active", candidate.SlotID)
		}
		fence.allowedTaskIDs[candidate.ExistingTaskID] = struct{}{}
	}
	return fence, nil
}

func (f *slotLeaderTransferBatchExecutionFence) expectedRevision() uint64 {
	if f == nil {
		return 0
	}
	return f.expected.Revision
}

func (f *slotLeaderTransferBatchExecutionFence) observe(current control.Snapshot) (bool, error) {
	if f == nil {
		return false, errors.New("slot leader transfer batch execution fence is unavailable")
	}
	if f.conflictErr != nil {
		return false, f.conflictErr
	}
	if current.Revision < f.expected.Revision {
		return false, nil
	}

	projected := f.expected.Clone()
	if current.Revision > projected.Revision {
		taskIDs := make([]string, 0, len(f.allowedTaskIDs))
		for taskID := range f.allowedTaskIDs {
			taskIDs = append(taskIDs, taskID)
		}
		sort.Strings(taskIDs)
		for _, taskID := range taskIDs {
			expectedIndex := slotLeaderTransferBatchTaskIndexByID(projected.Tasks, taskID)
			if expectedIndex < 0 {
				continue
			}
			currentIndex := slotLeaderTransferBatchTaskIndexByID(current.Tasks, taskID)
			if currentIndex < 0 {
				projected.Tasks = append(projected.Tasks[:expectedIndex], projected.Tasks[expectedIndex+1:]...)
				projected.Revision++
				continue
			}
			expectedTask := projected.Tasks[expectedIndex]
			currentTask := current.Tasks[currentIndex]
			if reflect.DeepEqual(expectedTask, currentTask) {
				continue
			}
			if !slotLeaderTransferBatchTaskFailureTransition(expectedTask, currentTask) {
				return f.reject("approved task %s changed outside its allowed lifecycle", taskID)
			}
			projected.Tasks[expectedIndex] = cloneSlotLeaderTransferBatchTask(currentTask)
			projected.Revision++
		}
	}
	if projected.Revision != current.Revision || !sameSlotLeaderTransferBatchControlState(projected, current) {
		return f.reject("Controller state changed outside the approved batch task lifecycle")
	}
	f.expected = current.Clone()
	return true, nil
}

func (f *slotLeaderTransferBatchExecutionFence) recordWriterResult(candidate SlotLeaderTransferBatchCandidate, result control.SlotLeaderTransferResult, stateRevision uint64) error {
	if f == nil {
		return errors.New("slot leader transfer batch execution fence is unavailable")
	}
	if f.conflictErr != nil {
		return f.conflictErr
	}
	if f.expected.Revision != stateRevision {
		return fmt.Errorf("slot %d write used revision %d while fenced state is revision %d", candidate.SlotID, stateRevision, f.expected.Revision)
	}
	if result.Task == nil {
		return fmt.Errorf("slot %d writer accepted a transfer without returning its task identity", candidate.SlotID)
	}
	task := cloneSlotLeaderTransferBatchTask(*result.Task)
	if !slotLeaderTransferBatchTaskMatchesCandidate(task, candidate) {
		return fmt.Errorf("slot %d writer returned a task outside the approved candidate fence", candidate.SlotID)
	}
	if existingIndex := slotLeaderTransferBatchTaskIndexByID(f.expected.Tasks, task.TaskID); existingIndex >= 0 {
		if result.Created || !reflect.DeepEqual(f.expected.Tasks[existingIndex], task) {
			return fmt.Errorf("slot %d writer returned an inconsistent existing task", candidate.SlotID)
		}
		f.allowedTaskIDs[task.TaskID] = struct{}{}
		return nil
	}

	assignmentIndex := slotLeaderTransferBatchAssignmentIndex(f.expected.Slots, candidate.SlotID)
	if assignmentIndex < 0 {
		return fmt.Errorf("slot %d assignment disappeared from the fenced state", candidate.SlotID)
	}
	if activeIndex := slotLeaderTransferBatchTaskIndexBySlot(f.expected.Tasks, candidate.SlotID); activeIndex >= 0 {
		return fmt.Errorf("slot %d active task changed before the approved write", candidate.SlotID)
	}
	if task.Status != control.TaskStatusPending || task.Attempt != 0 || task.LastError != "" || len(task.ParticipantProgress) != 0 {
		return fmt.Errorf("slot %d writer returned a non-canonical new task", candidate.SlotID)
	}
	f.expected.Slots[assignmentIndex].PreferredLeader = candidate.TargetNodeID
	f.expected.Tasks = append(f.expected.Tasks, task)
	f.expected.Revision++
	f.allowedTaskIDs[task.TaskID] = struct{}{}
	return nil
}

func (f *slotLeaderTransferBatchExecutionFence) reject(message string, args ...any) (bool, error) {
	f.conflictErr = fmt.Errorf("slot leader transfer batch fence rejected: "+message, args...)
	return false, f.conflictErr
}

func (f *slotLeaderTransferBatchExecutionFence) close(message string) error {
	_, err := f.reject(message)
	return err
}

func (f *slotLeaderTransferBatchExecutionFence) validateExistingCandidate(candidate SlotLeaderTransferBatchCandidate) error {
	if f == nil {
		return errors.New("slot leader transfer batch execution fence is unavailable")
	}
	index := slotLeaderTransferBatchTaskIndexByID(f.expected.Tasks, candidate.ExistingTaskID)
	if index < 0 || !slotLeaderTransferBatchTaskMatchesCandidate(f.expected.Tasks[index], candidate) {
		return fmt.Errorf("slot %d approved existing task is no longer active", candidate.SlotID)
	}
	status := f.expected.Tasks[index].Status
	if status != control.TaskStatusPending && status != control.TaskStatusRunning {
		return fmt.Errorf("slot %d approved existing task is no longer reusable", candidate.SlotID)
	}
	return nil
}

func sameSlotLeaderTransferBatchControlState(left, right control.Snapshot) bool {
	return reflect.DeepEqual(normalizeSlotLeaderTransferBatchControlState(left), normalizeSlotLeaderTransferBatchControlState(right))
}

func normalizeSlotLeaderTransferBatchControlState(snapshot control.Snapshot) control.Snapshot {
	normalized := snapshot.Clone()
	normalized.Revision = 0
	normalized.ControllerID = 0
	normalized.HashSlots.Revision = 0
	normalized.ChannelDataPlaneLease = control.ChannelDataPlaneLease{}
	for index := range normalized.Nodes {
		// Health reports do not advance logical Controller Revision and are not
		// used by this planner's active-membership target eligibility checks.
		normalized.Nodes[index].Health = control.NodeHealth{}
		sort.Slice(normalized.Nodes[index].Roles, func(i, j int) bool {
			return normalized.Nodes[index].Roles[i] < normalized.Nodes[index].Roles[j]
		})
	}
	for index := range normalized.Slots {
		sort.Slice(normalized.Slots[index].DesiredPeers, func(i, j int) bool {
			return normalized.Slots[index].DesiredPeers[i] < normalized.Slots[index].DesiredPeers[j]
		})
	}
	for index := range normalized.Tasks {
		sort.Slice(normalized.Tasks[index].TargetPeers, func(i, j int) bool {
			return normalized.Tasks[index].TargetPeers[i] < normalized.Tasks[index].TargetPeers[j]
		})
		sort.Slice(normalized.Tasks[index].ObservedVoters, func(i, j int) bool {
			return normalized.Tasks[index].ObservedVoters[i] < normalized.Tasks[index].ObservedVoters[j]
		})
		sort.Slice(normalized.Tasks[index].ObservedLearners, func(i, j int) bool {
			return normalized.Tasks[index].ObservedLearners[i] < normalized.Tasks[index].ObservedLearners[j]
		})
	}
	sort.Slice(normalized.Nodes, func(i, j int) bool { return normalized.Nodes[i].NodeID < normalized.Nodes[j].NodeID })
	sort.Slice(normalized.Slots, func(i, j int) bool { return normalized.Slots[i].SlotID < normalized.Slots[j].SlotID })
	sort.Slice(normalized.Tasks, func(i, j int) bool { return normalized.Tasks[i].TaskID < normalized.Tasks[j].TaskID })
	sort.Slice(normalized.HashSlots.Ranges, func(i, j int) bool { return normalized.HashSlots.Ranges[i].From < normalized.HashSlots.Ranges[j].From })
	if normalized.OpsMCP != nil {
		sort.Slice(normalized.OpsMCP.Credentials, func(i, j int) bool {
			return normalized.OpsMCP.Credentials[i].ID < normalized.OpsMCP.Credentials[j].ID
		})
	}
	return normalized
}

func slotLeaderTransferBatchTaskFailureTransition(expected, current control.ReconcileTask) bool {
	if current.Status != control.TaskStatusFailed || current.Attempt != expected.Attempt+1 {
		return false
	}
	normalized := cloneSlotLeaderTransferBatchTask(current)
	normalized.Status = expected.Status
	normalized.Attempt = expected.Attempt
	normalized.LastError = expected.LastError
	return reflect.DeepEqual(expected, normalized)
}

func slotLeaderTransferBatchTaskMatchesCandidate(task control.ReconcileTask, candidate SlotLeaderTransferBatchCandidate) bool {
	return task.TaskID != "" &&
		task.SlotID == candidate.SlotID &&
		task.Kind == control.TaskKindLeaderTransfer &&
		task.Step == control.TaskStepTransferLeader &&
		task.SourceNode == candidate.SourceNodeID &&
		task.TargetNode == candidate.TargetNodeID &&
		task.CompletionPolicy == control.TaskCompletionPolicySingleObserver &&
		task.ConfigEpoch == candidate.ConfigEpoch &&
		sameUint64Set(task.TargetPeers, candidate.DesiredPeers)
}

func slotLeaderTransferBatchTaskIndexByID(tasks []control.ReconcileTask, taskID string) int {
	for index := range tasks {
		if tasks[index].TaskID == taskID {
			return index
		}
	}
	return -1
}

func slotLeaderTransferBatchTaskIndexBySlot(tasks []control.ReconcileTask, slotID uint32) int {
	for index := range tasks {
		if tasks[index].SlotID == slotID {
			return index
		}
	}
	return -1
}

func slotLeaderTransferBatchAssignmentIndex(assignments []control.SlotAssignment, slotID uint32) int {
	for index := range assignments {
		if assignments[index].SlotID == slotID {
			return index
		}
	}
	return -1
}

func cloneSlotLeaderTransferBatchTask(task control.ReconcileTask) control.ReconcileTask {
	task.TargetPeers = append([]uint64(nil), task.TargetPeers...)
	task.ParticipantProgress = append([]control.TaskParticipantProgress(nil), task.ParticipantProgress...)
	task.ObservedVoters = append([]uint64(nil), task.ObservedVoters...)
	task.ObservedLearners = append([]uint64(nil), task.ObservedLearners...)
	return task
}

func appendSlotLeaderTransferBatchFailure(response *SlotLeaderTransferBatchExecuteResponse, candidate SlotLeaderTransferBatchCandidate, err error) {
	response.Summary.Failed++
	response.Results = append(response.Results, SlotLeaderTransferBatchExecuteResult{
		SlotID:       candidate.SlotID,
		TargetNodeID: candidate.TargetNodeID,
		Status:       SlotLeaderTransferBatchResultFailed,
		Message:      err.Error(),
	})
}

func waitSlotLeaderTransferBatchRetry(ctx context.Context) error {
	timer := time.NewTimer(slotLeaderTransferBatchRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
	if hasActiveTask {
		reusableActiveTask = canReuseLeaderTransferTask(activeTask, req.SourceNodeID, req.TargetNodeID, assignment)
		if !reusableActiveTask && leaderTransferTaskMatches(activeTask, req.SourceNodeID, req.TargetNodeID, assignment) {
			appendBatchSkip(response, assignment.SlotID, SlotLeaderTransferBatchSkipMatchingTaskExists, "matching non-active leader transfer task already exists")
			return
		}
		if !reusableActiveTask {
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
