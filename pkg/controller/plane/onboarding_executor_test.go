package plane

import (
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestValidateNodeOnboardingStartRejectsChangedAssignment(t *testing.T) {
	job := plannedOnboardingJobForExecutor()
	input := executableOnboardingInputForJob(job)
	assignment := input.Assignments[job.Moves[0].SlotID]
	assignment.ConfigEpoch++
	input.Assignments[job.Moves[0].SlotID] = assignment

	result := ValidateNodeOnboardingStart(input, job)

	require.ErrorIs(t, result.Err, ErrOnboardingPlanStale)
}

func TestValidateNodeOnboardingStartRejectsBlockedPlan(t *testing.T) {
	job := plannedOnboardingJobForExecutor()
	job.Plan.BlockedReasons = []controllermeta.NodeOnboardingBlockedReason{{
		Code:  "slot_task_running",
		Scope: "slot",
	}}

	result := ValidateNodeOnboardingStart(executableOnboardingInputForJob(job), job)

	require.ErrorIs(t, result.Err, ErrOnboardingPlanNotExecutable)
}

func TestValidateNodeOnboardingStartAcceptsCompatiblePlan(t *testing.T) {
	job := plannedOnboardingJobForExecutor()

	result := ValidateNodeOnboardingStart(executableOnboardingInputForJob(job), job)

	require.NoError(t, result.Err)
}

func TestNextNodeOnboardingActionStartsPendingMove(t *testing.T) {
	job := runningOnboardingJobWithPendingMoveForExecutor()
	input := executableOnboardingInputForJob(job)

	action := NextNodeOnboardingAction(input, job)

	require.Equal(t, OnboardingActionStartMove, action.Kind)
	require.Equal(t, controllermeta.TaskKindRebalance, action.Task.Kind)
	require.Equal(t, controllermeta.TaskStepAddLearner, action.Task.Step)
	require.Equal(t, job.Moves[0].DesiredPeersAfter, action.Assignment.DesiredPeers)
	require.Equal(t, controllermeta.OnboardingMoveStatusRunning, action.Job.Moves[0].Status)
}

func TestNextNodeOnboardingActionSkipsAlreadySatisfiedPendingMove(t *testing.T) {
	job := runningOnboardingJobWithPendingMoveForExecutor()
	input := executableOnboardingInputForJob(job)
	assignment := input.Assignments[job.Moves[0].SlotID]
	assignment.DesiredPeers = append([]uint64(nil), job.Moves[0].DesiredPeersAfter...)
	input.Assignments[job.Moves[0].SlotID] = assignment

	action := NextNodeOnboardingAction(input, job)

	require.Equal(t, OnboardingActionSkipMove, action.Kind)
	require.Equal(t, controllermeta.OnboardingMoveStatusSkipped, action.Job.Moves[0].Status)
	require.Equal(t, 1, action.Job.ResultCounts.Skipped)
}

func TestNextNodeOnboardingActionCompletesRunningMoveAfterAssignmentConverges(t *testing.T) {
	job := runningOnboardingJobWithActiveMoveForExecutor()
	input := executableOnboardingInputForJob(job)
	assignment := input.Assignments[job.Moves[0].SlotID]
	assignment.DesiredPeers = append([]uint64(nil), job.Moves[0].DesiredPeersAfter...)
	input.Assignments[job.Moves[0].SlotID] = assignment

	action := NextNodeOnboardingAction(input, job)

	require.Equal(t, OnboardingActionCompleteMove, action.Kind)
	require.Equal(t, controllermeta.OnboardingMoveStatusCompleted, action.Job.Moves[0].Status)
	require.Nil(t, action.Job.CurrentTask)
}

func TestNextNodeOnboardingActionRequestsLeaderTransferBeforeCompleting(t *testing.T) {
	job := runningOnboardingJobWithActiveMoveForExecutor()
	job.Moves[0].LeaderTransferRequired = true
	input := executableOnboardingInputForJob(job)
	assignment := input.Assignments[job.Moves[0].SlotID]
	assignment.DesiredPeers = append([]uint64(nil), job.Moves[0].DesiredPeersAfter...)
	input.Assignments[job.Moves[0].SlotID] = assignment
	view := input.Runtime[job.Moves[0].SlotID]
	view.LeaderID = job.Moves[0].SourceNodeID
	input.Runtime[job.Moves[0].SlotID] = view

	action := NextNodeOnboardingAction(input, job)

	require.Equal(t, OnboardingActionNone, action.Kind)
	require.True(t, action.LeaderTransferRequired)
	require.Equal(t, job.Moves[0].SlotID, action.Move.SlotID)
}

func TestNextNodeOnboardingActionFailsWhenAssignmentIsUnexpected(t *testing.T) {
	job := runningOnboardingJobWithActiveMoveForExecutor()
	input := executableOnboardingInputForJob(job)
	assignment := input.Assignments[job.Moves[0].SlotID]
	assignment.DesiredPeers = []uint64{1, 4, 5}
	input.Assignments[job.Moves[0].SlotID] = assignment

	action := NextNodeOnboardingAction(input, job)

	require.Equal(t, OnboardingActionFailJob, action.Kind)
	require.Equal(t, controllermeta.OnboardingJobStatusFailed, action.Job.Status)
	require.Contains(t, action.Job.LastError, "unexpected assignment")
}

func TestNextNodeOnboardingActionCompletesJobWhenAllMovesTerminal(t *testing.T) {
	job := runningOnboardingJobWithActiveMoveForExecutor()
	job.Moves[0].Status = controllermeta.OnboardingMoveStatusCompleted
	job.CurrentMoveIndex = -1
	job.CurrentTask = nil
	job.ResultCounts = controllermeta.OnboardingResultCounts{Completed: 1}
	input := executableOnboardingInputForJob(job)

	action := NextNodeOnboardingAction(input, job)

	require.Equal(t, OnboardingActionCompleteJob, action.Kind)
	require.Equal(t, controllermeta.OnboardingJobStatusCompleted, action.Job.Status)
	require.False(t, action.Job.CompletedAt.IsZero())
}

func plannedOnboardingJobForExecutor() controllermeta.NodeOnboardingJob {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	move := controllermeta.NodeOnboardingPlanMove{
		SlotID:             2,
		SourceNodeID:       1,
		TargetNodeID:       4,
		Reason:             "replica_balance",
		DesiredPeersBefore: []uint64{1, 2, 3},
		DesiredPeersAfter:  []uint64{2, 3, 4},
		CurrentLeaderID:    2,
	}
	job := controllermeta.NodeOnboardingJob{
		JobID:        "onboard-20260426-000001",
		TargetNodeID: 4,
		Status:       controllermeta.OnboardingJobStatusPlanned,
		CreatedAt:    now,
		UpdatedAt:    now,
		PlanVersion:  1,
		Plan: controllermeta.NodeOnboardingPlan{
			TargetNodeID: 4,
			Summary: controllermeta.NodeOnboardingPlanSummary{
				PlannedTargetSlotCount: 1,
			},
			Moves: []controllermeta.NodeOnboardingPlanMove{move},
		},
		Moves: []controllermeta.NodeOnboardingMove{{
			SlotID:             move.SlotID,
			SourceNodeID:       move.SourceNodeID,
			TargetNodeID:       move.TargetNodeID,
			Status:             controllermeta.OnboardingMoveStatusPending,
			TaskKind:           controllermeta.TaskKindRebalance,
			TaskSlotID:         move.SlotID,
			DesiredPeersBefore: append([]uint64(nil), move.DesiredPeersBefore...),
			DesiredPeersAfter:  append([]uint64(nil), move.DesiredPeersAfter...),
		}},
		CurrentMoveIndex: -1,
		ResultCounts:     controllermeta.OnboardingResultCounts{Pending: 1},
	}
	input := executableOnboardingInputForJob(job)
	job.PlanFingerprint = OnboardingPlanFingerprint(onboardingFingerprintInputFromExecution(input, job.Plan))
	return job
}

func runningOnboardingJobWithPendingMoveForExecutor() controllermeta.NodeOnboardingJob {
	job := plannedOnboardingJobForExecutor()
	job.Status = controllermeta.OnboardingJobStatusRunning
	job.StartedAt = job.CreatedAt.Add(time.Minute)
	job.UpdatedAt = job.StartedAt
	return job
}

func runningOnboardingJobWithActiveMoveForExecutor() controllermeta.NodeOnboardingJob {
	job := runningOnboardingJobWithPendingMoveForExecutor()
	task := controllermeta.ReconcileTask{
		SlotID:     job.Moves[0].SlotID,
		Kind:       controllermeta.TaskKindRebalance,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: job.Moves[0].SourceNodeID,
		TargetNode: job.Moves[0].TargetNodeID,
		Status:     controllermeta.TaskStatusPending,
	}
	job.CurrentMoveIndex = 0
	job.Moves[0].Status = controllermeta.OnboardingMoveStatusRunning
	job.Moves[0].StartedAt = job.StartedAt
	job.CurrentTask = &task
	job.ResultCounts = controllermeta.OnboardingResultCounts{Running: 1}
	return job
}

func executableOnboardingInputForJob(job controllermeta.NodeOnboardingJob) OnboardingPlanInput {
	input := onboardingInputWithNodes(job.TargetNodeID, 1, 2, 3)
	for _, move := range job.Moves {
		input.Assignments[move.SlotID] = controllermeta.SlotAssignment{
			SlotID:         move.SlotID,
			DesiredPeers:   append([]uint64(nil), move.DesiredPeersBefore...),
			ConfigEpoch:    1,
			BalanceVersion: 1,
		}
		input.Runtime[move.SlotID] = controllermeta.SlotRuntimeView{
			SlotID:              move.SlotID,
			CurrentPeers:        append([]uint64(nil), move.DesiredPeersBefore...),
			LeaderID:            2,
			HealthyVoters:       uint32(len(move.DesiredPeersBefore)),
			HasQuorum:           true,
			ObservedConfigEpoch: 1,
			LastReportAt:        time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		}
	}
	input.Now = time.Date(2026, 4, 26, 12, 5, 0, 0, time.UTC)
	return input
}
