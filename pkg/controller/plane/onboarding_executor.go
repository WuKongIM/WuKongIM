package plane

import (
	"errors"
	"fmt"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

var (
	// ErrOnboardingPlanNotExecutable means the reviewed plan has no safe executable moves.
	ErrOnboardingPlanNotExecutable = errors.New("controller: onboarding plan not executable")
	// ErrOnboardingPlanStale means the current controller leader view no longer matches the reviewed plan.
	ErrOnboardingPlanStale = errors.New("controller: onboarding plan stale")
)

// OnboardingStartValidationResult reports whether a planned onboarding job can start.
type OnboardingStartValidationResult struct {
	// Err is nil when the current strict controller view is compatible with the reviewed plan.
	Err error
}

// OnboardingActionKind identifies the next single state transition for a running onboarding job.
type OnboardingActionKind uint8

const (
	// OnboardingActionNone means no durable job transition is currently available.
	OnboardingActionNone OnboardingActionKind = iota
	// OnboardingActionStartMove starts the next pending Slot rebalance move.
	OnboardingActionStartMove
	// OnboardingActionSkipMove skips a move whose target assignment is already satisfied.
	OnboardingActionSkipMove
	// OnboardingActionCompleteMove marks the active move completed.
	OnboardingActionCompleteMove
	// OnboardingActionFailJob marks the move and job failed.
	OnboardingActionFailJob
	// OnboardingActionCompleteJob marks the job completed after all moves are terminal.
	OnboardingActionCompleteJob
)

// OnboardingAction describes one durable command or external leader-transfer request.
type OnboardingAction struct {
	// Kind is the action type selected for this tick.
	Kind OnboardingActionKind
	// Job is the updated job state to persist for durable actions.
	Job controllermeta.NodeOnboardingJob
	// Move is the move selected for this action.
	Move controllermeta.NodeOnboardingMove
	// MoveIndex is the index of Move in Job.Moves.
	MoveIndex int
	// Assignment is the updated assignment to persist when starting a move.
	Assignment controllermeta.SlotAssignment
	// Task is the reconcile task to persist when starting a move.
	Task controllermeta.ReconcileTask
	// LeaderTransferRequired asks the cluster layer to transfer Slot leadership before completion.
	LeaderTransferRequired bool
}

// ValidateNodeOnboardingStart verifies that a planned job still matches the leader's strict view.
func ValidateNodeOnboardingStart(input OnboardingPlanInput, job controllermeta.NodeOnboardingJob) OnboardingStartValidationResult {
	if job.Status != controllermeta.OnboardingJobStatusPlanned {
		return OnboardingStartValidationResult{Err: ErrOnboardingPlanStale}
	}
	if len(job.Plan.BlockedReasons) > 0 || len(job.Moves) == 0 || len(job.Plan.Moves) == 0 {
		return OnboardingStartValidationResult{Err: ErrOnboardingPlanNotExecutable}
	}
	if input.RunningJobExists {
		return OnboardingStartValidationResult{Err: ErrOnboardingPlanStale}
	}
	if err := validateOnboardingTarget(input, job.TargetNodeID); err != nil {
		return OnboardingStartValidationResult{Err: err}
	}
	for _, move := range job.Plan.Moves {
		if err := validateOnboardingMoveCompatibility(input, move); err != nil {
			return OnboardingStartValidationResult{Err: err}
		}
	}
	if job.PlanFingerprint == "" || job.PlanFingerprint != OnboardingPlanFingerprint(onboardingFingerprintInputFromExecution(input, job.Plan)) {
		return OnboardingStartValidationResult{Err: ErrOnboardingPlanStale}
	}
	return OnboardingStartValidationResult{}
}

// NextNodeOnboardingAction returns the next single transition for a running onboarding job.
func NextNodeOnboardingAction(input OnboardingPlanInput, job controllermeta.NodeOnboardingJob) OnboardingAction {
	if job.Status != controllermeta.OnboardingJobStatusRunning {
		return OnboardingAction{Kind: OnboardingActionNone, Job: cloneOnboardingJob(job), MoveIndex: -1}
	}
	if len(job.Moves) == 0 {
		return failOnboardingJob(input, job, -1, "onboarding job has no moves")
	}
	index := currentOnboardingMoveIndex(job)
	if index < 0 {
		if onboardingMovesSuccessful(job.Moves) {
			next := cloneOnboardingJob(job)
			next.Status = controllermeta.OnboardingJobStatusCompleted
			next.UpdatedAt = input.Now
			next.CompletedAt = input.Now
			next.CurrentMoveIndex = -1
			next.CurrentTask = nil
			next.ResultCounts = countOnboardingMoves(next.Moves)
			return OnboardingAction{Kind: OnboardingActionCompleteJob, Job: next, MoveIndex: -1}
		}
		return OnboardingAction{Kind: OnboardingActionNone, Job: cloneOnboardingJob(job), MoveIndex: -1}
	}

	move := job.Moves[index]
	if err := validateRunningOnboardingMoveNodes(input, move); err != nil {
		return failOnboardingJob(input, job, index, err.Error())
	}
	assignment, ok := input.Assignments[move.SlotID]
	if !ok {
		return failOnboardingJob(input, job, index, "slot assignment is missing")
	}
	before := sortedPeers(move.DesiredPeersBefore)
	after := sortedPeers(move.DesiredPeersAfter)
	currentPeers := sortedPeers(assignment.DesiredPeers)

	switch move.Status {
	case controllermeta.OnboardingMoveStatusPending:
		switch {
		case peersEqual(currentPeers, after):
			return skipOnboardingMove(input, job, index)
		case !peersEqual(currentPeers, before):
			return failOnboardingJob(input, job, index, fmt.Sprintf("unexpected assignment for slot %d", move.SlotID))
		default:
			return startOnboardingMove(input, job, index, assignment)
		}
	case controllermeta.OnboardingMoveStatusRunning:
		if task, ok := input.Tasks[move.SlotID]; ok && task.Status == controllermeta.TaskStatusFailed {
			if task.LastError == "" {
				task.LastError = "slot reconcile task failed"
			}
			return failOnboardingJob(input, job, index, task.LastError)
		}
		switch {
		case peersEqual(currentPeers, before):
			return OnboardingAction{Kind: OnboardingActionNone, Job: cloneOnboardingJob(job), Move: move, MoveIndex: index}
		case peersEqual(currentPeers, after):
			view, hasView := input.Runtime[move.SlotID]
			if move.LeaderTransferRequired && (!hasView || !view.HasQuorum) {
				return failOnboardingJob(input, job, index, "slot runtime view is not available for leader transfer")
			}
			if move.LeaderTransferRequired && view.LeaderID != move.TargetNodeID {
				return OnboardingAction{
					Kind:                   OnboardingActionNone,
					Job:                    cloneOnboardingJob(job),
					Move:                   move,
					MoveIndex:              index,
					LeaderTransferRequired: true,
				}
			}
			return completeOnboardingMove(input, job, index, view.LeaderID)
		default:
			return failOnboardingJob(input, job, index, fmt.Sprintf("unexpected assignment for slot %d", move.SlotID))
		}
	default:
		return OnboardingAction{Kind: OnboardingActionNone, Job: cloneOnboardingJob(job), Move: move, MoveIndex: index}
	}
}

func validateOnboardingTarget(input OnboardingPlanInput, targetNodeID uint64) error {
	target, ok := input.Nodes[targetNodeID]
	if !ok || !nodeSchedulableForData(target) {
		return ErrOnboardingPlanStale
	}
	return nil
}

func validateOnboardingMoveCompatibility(input OnboardingPlanInput, move controllermeta.NodeOnboardingPlanMove) error {
	source, ok := input.Nodes[move.SourceNodeID]
	if !ok || !nodeSchedulableForData(source) {
		return ErrOnboardingPlanStale
	}
	assignment, ok := input.Assignments[move.SlotID]
	if !ok || !peersEqual(sortedPeers(assignment.DesiredPeers), sortedPeers(move.DesiredPeersBefore)) {
		return ErrOnboardingPlanStale
	}
	view, ok := input.Runtime[move.SlotID]
	if !ok || !view.HasQuorum {
		return ErrOnboardingPlanStale
	}
	if _, ok := input.Tasks[move.SlotID]; ok {
		return ErrOnboardingPlanStale
	}
	if _, ok := input.MigratingSlots[move.SlotID]; ok {
		return ErrOnboardingPlanStale
	}
	return nil
}

func validateRunningOnboardingMoveNodes(input OnboardingPlanInput, move controllermeta.NodeOnboardingMove) error {
	if err := validateOnboardingTarget(input, move.TargetNodeID); err != nil {
		return fmt.Errorf("target node %d is no longer schedulable", move.TargetNodeID)
	}
	source, ok := input.Nodes[move.SourceNodeID]
	if !ok || !nodeSchedulableForData(source) {
		return fmt.Errorf("source node %d is no longer schedulable", move.SourceNodeID)
	}
	if _, ok := input.MigratingSlots[move.SlotID]; ok {
		return fmt.Errorf("slot %d is protected by hash-slot migration", move.SlotID)
	}
	return nil
}

func onboardingFingerprintInputFromExecution(input OnboardingPlanInput, plan controllermeta.NodeOnboardingPlan) OnboardingPlanFingerprintInput {
	return OnboardingPlanFingerprintInput{
		TargetNode:     input.Nodes[plan.TargetNodeID],
		Plan:           plan,
		Assignments:    input.Assignments,
		Runtime:        input.Runtime,
		Tasks:          input.Tasks,
		MigratingSlots: input.MigratingSlots,
	}
}

func currentOnboardingMoveIndex(job controllermeta.NodeOnboardingJob) int {
	if job.CurrentMoveIndex >= 0 && job.CurrentMoveIndex < len(job.Moves) {
		status := job.Moves[job.CurrentMoveIndex].Status
		if status == controllermeta.OnboardingMoveStatusPending || status == controllermeta.OnboardingMoveStatusRunning {
			return job.CurrentMoveIndex
		}
	}
	for i, move := range job.Moves {
		if move.Status == controllermeta.OnboardingMoveStatusPending || move.Status == controllermeta.OnboardingMoveStatusRunning {
			return i
		}
	}
	return -1
}

func startOnboardingMove(input OnboardingPlanInput, job controllermeta.NodeOnboardingJob, index int, current controllermeta.SlotAssignment) OnboardingAction {
	next := cloneOnboardingJob(job)
	move := next.Moves[index]
	move.Status = controllermeta.OnboardingMoveStatusRunning
	move.StartedAt = input.Now
	next.Moves[index] = move
	next.CurrentMoveIndex = index
	next.UpdatedAt = input.Now

	assignment := controllermeta.SlotAssignment{
		SlotID:         current.SlotID,
		DesiredPeers:   sortedPeers(move.DesiredPeersAfter),
		ConfigEpoch:    current.ConfigEpoch + 1,
		BalanceVersion: current.BalanceVersion + 1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:     move.SlotID,
		Kind:       controllermeta.TaskKindRebalance,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: move.SourceNodeID,
		TargetNode: move.TargetNodeID,
		Status:     controllermeta.TaskStatusPending,
	}
	next.CurrentTask = &task
	next.ResultCounts = countOnboardingMoves(next.Moves)
	return OnboardingAction{
		Kind:       OnboardingActionStartMove,
		Job:        next,
		Move:       move,
		MoveIndex:  index,
		Assignment: assignment,
		Task:       task,
	}
}

func skipOnboardingMove(input OnboardingPlanInput, job controllermeta.NodeOnboardingJob, index int) OnboardingAction {
	next := cloneOnboardingJob(job)
	move := next.Moves[index]
	move.Status = controllermeta.OnboardingMoveStatusSkipped
	move.CompletedAt = input.Now
	next.Moves[index] = move
	next.CurrentMoveIndex = -1
	next.CurrentTask = nil
	next.UpdatedAt = input.Now
	next.ResultCounts = countOnboardingMoves(next.Moves)
	return OnboardingAction{Kind: OnboardingActionSkipMove, Job: next, Move: move, MoveIndex: index}
}

func completeOnboardingMove(input OnboardingPlanInput, job controllermeta.NodeOnboardingJob, index int, leaderAfter uint64) OnboardingAction {
	next := cloneOnboardingJob(job)
	move := next.Moves[index]
	move.Status = controllermeta.OnboardingMoveStatusCompleted
	move.CompletedAt = input.Now
	move.LeaderAfter = leaderAfter
	next.Moves[index] = move
	next.CurrentMoveIndex = -1
	next.CurrentTask = nil
	next.UpdatedAt = input.Now
	next.ResultCounts = countOnboardingMoves(next.Moves)
	return OnboardingAction{Kind: OnboardingActionCompleteMove, Job: next, Move: move, MoveIndex: index}
}

func failOnboardingJob(input OnboardingPlanInput, job controllermeta.NodeOnboardingJob, index int, message string) OnboardingAction {
	next := cloneOnboardingJob(job)
	if index >= 0 && index < len(next.Moves) {
		move := next.Moves[index]
		move.Status = controllermeta.OnboardingMoveStatusFailed
		move.CompletedAt = input.Now
		move.LastError = message
		next.Moves[index] = move
	}
	next.Status = controllermeta.OnboardingJobStatusFailed
	next.LastError = message
	next.UpdatedAt = input.Now
	next.CompletedAt = input.Now
	next.CurrentTask = nil
	next.ResultCounts = countOnboardingMoves(next.Moves)
	action := OnboardingAction{Kind: OnboardingActionFailJob, Job: next, MoveIndex: index}
	if index >= 0 && index < len(next.Moves) {
		action.Move = next.Moves[index]
	}
	return action
}

func onboardingMovesSuccessful(moves []controllermeta.NodeOnboardingMove) bool {
	if len(moves) == 0 {
		return false
	}
	for _, move := range moves {
		if move.Status != controllermeta.OnboardingMoveStatusCompleted && move.Status != controllermeta.OnboardingMoveStatusSkipped {
			return false
		}
	}
	return true
}

func countOnboardingMoves(moves []controllermeta.NodeOnboardingMove) controllermeta.OnboardingResultCounts {
	var counts controllermeta.OnboardingResultCounts
	for _, move := range moves {
		switch move.Status {
		case controllermeta.OnboardingMoveStatusPending:
			counts.Pending++
		case controllermeta.OnboardingMoveStatusRunning:
			counts.Running++
		case controllermeta.OnboardingMoveStatusCompleted:
			counts.Completed++
		case controllermeta.OnboardingMoveStatusFailed:
			counts.Failed++
		case controllermeta.OnboardingMoveStatusSkipped:
			counts.Skipped++
		}
	}
	return counts
}

func peersEqual(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	left = sortedPeers(left)
	right = sortedPeers(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func cloneOnboardingJob(job controllermeta.NodeOnboardingJob) controllermeta.NodeOnboardingJob {
	next := job
	next.Plan.Moves = append([]controllermeta.NodeOnboardingPlanMove(nil), job.Plan.Moves...)
	for i := range next.Plan.Moves {
		next.Plan.Moves[i].DesiredPeersBefore = append([]uint64(nil), job.Plan.Moves[i].DesiredPeersBefore...)
		next.Plan.Moves[i].DesiredPeersAfter = append([]uint64(nil), job.Plan.Moves[i].DesiredPeersAfter...)
	}
	next.Plan.BlockedReasons = append([]controllermeta.NodeOnboardingBlockedReason(nil), job.Plan.BlockedReasons...)
	next.Moves = append([]controllermeta.NodeOnboardingMove(nil), job.Moves...)
	for i := range next.Moves {
		next.Moves[i].DesiredPeersBefore = append([]uint64(nil), job.Moves[i].DesiredPeersBefore...)
		next.Moves[i].DesiredPeersAfter = append([]uint64(nil), job.Moves[i].DesiredPeersAfter...)
	}
	if job.CurrentTask != nil {
		task := *job.CurrentTask
		next.CurrentTask = &task
	}
	return next
}
