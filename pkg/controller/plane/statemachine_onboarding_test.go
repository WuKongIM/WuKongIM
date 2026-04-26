package plane

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

func TestStateMachineAppliesOnboardingJobUpdate(t *testing.T) {
	ctx := context.Background()
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	job := sampleStateMachineOnboardingJob("job-1", controllermeta.OnboardingJobStatusPlanned)

	err := sm.Apply(ctx, Command{
		Kind: CommandKindNodeOnboardingJobUpdate,
		NodeOnboarding: &NodeOnboardingJobUpdate{
			Job: &job,
		},
	})
	require.NoError(t, err)

	got, err := store.GetOnboardingJob(ctx, "job-1")
	require.NoError(t, err)
	require.Equal(t, controllermeta.OnboardingJobStatusPlanned, got.Status)
}

func TestStateMachineAppliesOnboardingMoveStartAtomically(t *testing.T) {
	ctx := context.Background()
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	job := sampleStateMachineOnboardingJob("job-1", controllermeta.OnboardingJobStatusPlanned)
	require.NoError(t, store.UpsertOnboardingJob(ctx, job))

	job.Status = controllermeta.OnboardingJobStatusRunning
	job.StartedAt = job.CreatedAt.Add(time.Minute)
	job.CurrentMoveIndex = 0
	job.Moves[0].Status = controllermeta.OnboardingMoveStatusRunning
	task := controllermeta.ReconcileTask{
		SlotID:     2,
		Kind:       controllermeta.TaskKindRebalance,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: 1,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusPending,
	}
	job.CurrentTask = &task
	assignment := controllermeta.SlotAssignment{
		SlotID:         2,
		DesiredPeers:   []uint64{2, 3, 4},
		ConfigEpoch:    4,
		BalanceVersion: 8,
	}
	expected := controllermeta.OnboardingJobStatusPlanned

	err := sm.Apply(ctx, Command{
		Kind: CommandKindNodeOnboardingJobUpdate,
		NodeOnboarding: &NodeOnboardingJobUpdate{
			Job:            &job,
			ExpectedStatus: &expected,
			Assignment:     &assignment,
			Task:           &task,
		},
	})
	require.NoError(t, err)

	gotJob, err := store.GetOnboardingJob(ctx, "job-1")
	require.NoError(t, err)
	require.Equal(t, controllermeta.OnboardingJobStatusRunning, gotJob.Status)
	require.NotNil(t, gotJob.CurrentTask)

	gotAssignment, err := store.GetAssignment(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, []uint64{2, 3, 4}, gotAssignment.DesiredPeers)

	gotTask, err := store.GetTask(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, controllermeta.TaskKindRebalance, gotTask.Kind)
}

func TestStateMachineOnboardingStartNoOpsWhenAnotherJobIsRunning(t *testing.T) {
	ctx := context.Background()
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	running := sampleStateMachineOnboardingJob("job-running", controllermeta.OnboardingJobStatusRunning)
	require.NoError(t, store.UpsertOnboardingJob(ctx, running))
	planned := sampleStateMachineOnboardingJob("job-planned", controllermeta.OnboardingJobStatusPlanned)
	require.NoError(t, store.UpsertOnboardingJob(ctx, planned))

	next := planned
	next.Status = controllermeta.OnboardingJobStatusRunning
	next.StartedAt = planned.CreatedAt.Add(time.Minute)
	expected := controllermeta.OnboardingJobStatusPlanned

	err := sm.Apply(ctx, Command{
		Kind: CommandKindNodeOnboardingJobUpdate,
		NodeOnboarding: &NodeOnboardingJobUpdate{
			Job:            &next,
			ExpectedStatus: &expected,
		},
	})
	require.NoError(t, err)

	got, err := store.GetOnboardingJob(ctx, "job-planned")
	require.NoError(t, err)
	require.Equal(t, controllermeta.OnboardingJobStatusPlanned, got.Status)
}

func sampleStateMachineOnboardingJob(jobID string, status controllermeta.OnboardingJobStatus) controllermeta.NodeOnboardingJob {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	job := controllermeta.NodeOnboardingJob{
		JobID:           jobID,
		TargetNodeID:    4,
		Status:          status,
		CreatedAt:       now,
		UpdatedAt:       now,
		PlanVersion:     1,
		PlanFingerprint: "fingerprint",
		Plan: controllermeta.NodeOnboardingPlan{
			TargetNodeID: 4,
			Summary: controllermeta.NodeOnboardingPlanSummary{
				PlannedTargetSlotCount: 1,
			},
			Moves: []controllermeta.NodeOnboardingPlanMove{{
				SlotID:             2,
				SourceNodeID:       1,
				TargetNodeID:       4,
				Reason:             "replica_balance",
				DesiredPeersBefore: []uint64{1, 2, 3},
				DesiredPeersAfter:  []uint64{2, 3, 4},
				CurrentLeaderID:    1,
			}},
		},
		Moves: []controllermeta.NodeOnboardingMove{{
			SlotID:             2,
			SourceNodeID:       1,
			TargetNodeID:       4,
			Status:             controllermeta.OnboardingMoveStatusPending,
			TaskKind:           controllermeta.TaskKindRebalance,
			TaskSlotID:         2,
			DesiredPeersBefore: []uint64{1, 2, 3},
			DesiredPeersAfter:  []uint64{2, 3, 4},
		}},
		CurrentMoveIndex: -1,
		ResultCounts: controllermeta.OnboardingResultCounts{
			Pending: 1,
		},
	}
	if status == controllermeta.OnboardingJobStatusRunning {
		job.StartedAt = now.Add(time.Minute)
	}
	return job
}
