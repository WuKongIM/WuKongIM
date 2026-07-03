package cluster

import (
	"context"
	"errors"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/plane"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func (c *Cluster) advanceNodeOnboardingOnce(ctx context.Context, state slotcontroller.PlannerState) (running bool, advanced bool) {
	if c == nil || c.controllerMeta == nil {
		return false, false
	}
	runningJobs, err := c.controllerMeta.ListRunningOnboardingJobs(ctx)
	if err != nil || len(runningJobs) == 0 {
		return false, false
	}
	job := runningJobs[0]
	input := slotcontroller.OnboardingPlanInput{
		TargetNodeID:     job.TargetNodeID,
		Nodes:            state.Nodes,
		Assignments:      state.Assignments,
		Runtime:          state.Runtime,
		Tasks:            state.Tasks,
		MigratingSlots:   state.MigratingSlots,
		RunningJobExists: false,
		Now:              state.Now,
	}
	action := slotcontroller.NextNodeOnboardingAction(input, job)
	expected := controllermeta.OnboardingJobStatusRunning
	switch action.Kind {
	case slotcontroller.OnboardingActionStartMove:
		err = c.proposeNodeOnboardingJobUpdate(ctx, slotcontroller.NodeOnboardingJobUpdate{
			Job:            &action.Job,
			ExpectedStatus: &expected,
			Assignment:     &action.Assignment,
			Task:           &action.Task,
		})
	case slotcontroller.OnboardingActionSkipMove, slotcontroller.OnboardingActionCompleteMove,
		slotcontroller.OnboardingActionFailJob, slotcontroller.OnboardingActionCompleteJob:
		err = c.proposeNodeOnboardingJobUpdate(ctx, slotcontroller.NodeOnboardingJobUpdate{
			Job:            &action.Job,
			ExpectedStatus: &expected,
		})
	case slotcontroller.OnboardingActionNone:
		if action.LeaderTransferRequired {
			err = c.TransferSlotLeader(ctx, action.Move.SlotID, multiraft.NodeID(action.Move.TargetNodeID))
			if err != nil {
				if onboardingLeaderTransferRetryable(err) {
					return true, false
				}
				failed := failClusterOnboardingJob(state, job, action.MoveIndex, err.Error())
				_ = c.proposeNodeOnboardingJobUpdate(ctx, slotcontroller.NodeOnboardingJobUpdate{
					Job:            &failed,
					ExpectedStatus: &expected,
				})
			}
			return true, true
		}
		return true, false
	default:
		return true, false
	}
	return true, err == nil
}

func onboardingLeaderTransferRetryable(err error) bool {
	return errors.Is(err, ErrNoLeader) ||
		errors.Is(err, ErrNotLeader) ||
		errors.Is(err, ErrSlotNotFound) ||
		errors.Is(err, multiraft.ErrSlotNotFound) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
}

func (c *Cluster) currentOnboardingLockedSlots(ctx context.Context) map[uint32]struct{} {
	if c == nil || c.controllerMeta == nil {
		return nil
	}
	jobs, err := c.controllerMeta.ListRunningOnboardingJobs(ctx)
	if err != nil || len(jobs) == 0 {
		return nil
	}
	locked := make(map[uint32]struct{})
	for _, move := range jobs[0].Moves {
		if move.Status == controllermeta.OnboardingMoveStatusPending || move.Status == controllermeta.OnboardingMoveStatusRunning {
			locked[move.SlotID] = struct{}{}
		}
	}
	return locked
}

func failClusterOnboardingJob(state slotcontroller.PlannerState, job controllermeta.NodeOnboardingJob, index int, message string) controllermeta.NodeOnboardingJob {
	next := cloneClusterOnboardingJob(job)
	if index >= 0 && index < len(next.Moves) {
		move := next.Moves[index]
		move.Status = controllermeta.OnboardingMoveStatusFailed
		move.CompletedAt = state.Now
		move.LastError = message
		next.Moves[index] = move
	}
	next.Status = controllermeta.OnboardingJobStatusFailed
	next.LastError = message
	next.CompletedAt = state.Now
	next.UpdatedAt = state.Now
	next.CurrentTask = nil
	next.ResultCounts = countClusterOnboardingMoves(next.Moves)
	return next
}
