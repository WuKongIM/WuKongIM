package meta

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOnboardingJobEncodeDecodeRoundTripAndCRUD(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	job := sampleOnboardingJob("onboard-a", time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC))

	encoded := encodeOnboardingJob(job)
	decoded, err := decodeOnboardingJob(encodeOnboardingJobKey(job.JobID), encoded)
	require.NoError(t, err)
	require.Equal(t, job, decoded)

	require.NoError(t, store.UpsertOnboardingJob(ctx, job))
	got, err := store.GetOnboardingJob(ctx, job.JobID)
	require.NoError(t, err)
	require.Equal(t, job, got)
}

func TestOnboardingJobValidationRejectsInvalidJobs(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		job  NodeOnboardingJob
	}{
		{name: "empty job id", job: func() NodeOnboardingJob {
			job := sampleOnboardingJob("onboard-invalid", now)
			job.JobID = ""
			return job
		}()},
		{name: "unknown status", job: func() NodeOnboardingJob {
			job := sampleOnboardingJob("onboard-invalid", now)
			job.Status = OnboardingJobStatus("paused")
			return job
		}()},
		{name: "zero target", job: func() NodeOnboardingJob {
			job := sampleOnboardingJob("onboard-invalid", now)
			job.TargetNodeID = 0
			return job
		}()},
		{name: "invalid move status", job: func() NodeOnboardingJob {
			job := sampleOnboardingJob("onboard-invalid", now)
			job.Moves[0].Status = OnboardingMoveStatus("blocked")
			return job
		}()},
		{name: "invalid current task", job: func() NodeOnboardingJob {
			job := sampleOnboardingJob("onboard-invalid", now)
			job.CurrentTask = &ReconcileTask{SlotID: 2, Kind: TaskKindUnknown, Step: TaskStepAddLearner}
			return job
		}()},
		{name: "empty blocked reason scope", job: func() NodeOnboardingJob {
			job := sampleOnboardingJob("onboard-invalid", now)
			job.Plan.BlockedReasons[0].Scope = ""
			return job
		}()},
		{name: "current move index past last move", job: func() NodeOnboardingJob {
			job := sampleOnboardingJob("onboard-invalid", now)
			job.CurrentMoveIndex = len(job.Moves)
			return job
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.UpsertOnboardingJob(ctx, tt.job)
			require.ErrorIs(t, err, ErrInvalidArgument)
		})
	}
}

func TestOnboardingJobListCursorBasicsAndRunningJobs(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	require.NoError(t, store.UpsertOnboardingJob(ctx, sampleOnboardingJobWithStatus("onboard-c", OnboardingJobStatusCompleted, now)))
	require.NoError(t, store.UpsertOnboardingJob(ctx, sampleOnboardingJobWithStatus("onboard-a", OnboardingJobStatusPlanned, now)))
	require.NoError(t, store.UpsertOnboardingJob(ctx, sampleOnboardingJobWithStatus("onboard-b", OnboardingJobStatusRunning, now)))

	firstPage, cursor, hasMore, err := store.ListOnboardingJobs(ctx, 2, "")
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Equal(t, "onboard-b", cursor)
	require.Equal(t, []string{"onboard-a", "onboard-b"}, onboardingJobIDs(firstPage))

	secondPage, nextCursor, hasMore, err := store.ListOnboardingJobs(ctx, 2, cursor)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, nextCursor)
	require.Equal(t, []string{"onboard-c"}, onboardingJobIDs(secondPage))

	running, err := store.ListRunningOnboardingJobs(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"onboard-b"}, onboardingJobIDs(running))
}

func TestOnboardingGuardedStartNoopsWhenAnotherJobIsRunning(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	require.NoError(t, store.UpsertOnboardingJob(ctx, sampleOnboardingJobWithStatus("onboard-running", OnboardingJobStatusRunning, now)))
	candidate := sampleOnboardingJobWithStatus("onboard-candidate", OnboardingJobStatusRunning, now)
	expected := OnboardingJobStatusPlanned

	wrote, err := store.GuardedUpsertOnboardingJob(ctx, candidate, &expected, nil, nil)
	require.NoError(t, err)
	require.False(t, wrote)

	_, err = store.GetOnboardingJob(ctx, candidate.JobID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestOnboardingGuardedUpsertWritesJobAssignmentAndTaskAtomically(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	job := sampleOnboardingJobWithStatus("onboard-atomic", OnboardingJobStatusPlanned, now)
	require.NoError(t, store.UpsertOnboardingJob(ctx, job))

	job.Status = OnboardingJobStatusRunning
	job.StartedAt = now.Add(time.Minute)
	job.CurrentTask = &ReconcileTask{SlotID: 2, Kind: TaskKindRebalance, Step: TaskStepAddLearner}
	assignment := SlotAssignment{SlotID: 2, DesiredPeers: []uint64{2, 3, 4}, ConfigEpoch: 4, BalanceVersion: 8}
	task := ReconcileTask{SlotID: 2, Kind: TaskKindRebalance, Step: TaskStepAddLearner, SourceNode: 1, TargetNode: 4}
	expected := OnboardingJobStatusPlanned

	wrote, err := store.GuardedUpsertOnboardingJob(ctx, job, &expected, &assignment, &task)
	require.NoError(t, err)
	require.True(t, wrote)

	gotJob, err := store.GetOnboardingJob(ctx, job.JobID)
	require.NoError(t, err)
	require.Equal(t, OnboardingJobStatusRunning, gotJob.Status)
	require.NotNil(t, gotJob.CurrentTask)

	gotAssignment, err := store.GetAssignment(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, []uint64{2, 3, 4}, gotAssignment.DesiredPeers)

	gotTask, err := store.GetTask(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, TaskKindRebalance, gotTask.Kind)
}

func TestOnboardingSnapshotExportImportPreservesJobs(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	job := sampleOnboardingJob("onboard-snapshot", now)

	require.NoError(t, store.UpsertOnboardingJob(ctx, job))
	snapshot, err := store.ExportSnapshot(ctx)
	require.NoError(t, err)

	entries, err := decodeSnapshot(snapshot)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, recordPrefixOnboardingJob, entries[0].Key[0])

	restored := openTestStore(t)
	require.NoError(t, restored.ImportSnapshot(ctx, snapshot))
	got, err := restored.GetOnboardingJob(ctx, job.JobID)
	require.NoError(t, err)
	require.Equal(t, job, got)
}

func sampleOnboardingJob(jobID string, now time.Time) NodeOnboardingJob {
	return NodeOnboardingJob{
		JobID:           jobID,
		TargetNodeID:    4,
		RetryOfJobID:    "onboard-previous",
		Status:          OnboardingJobStatusPlanned,
		CreatedAt:       now,
		UpdatedAt:       now.Add(time.Second),
		PlanVersion:     1,
		PlanFingerprint: "fingerprint-a",
		Plan: NodeOnboardingPlan{
			TargetNodeID: 4,
			Summary: NodeOnboardingPlanSummary{
				CurrentTargetSlotCount:   0,
				PlannedTargetSlotCount:   1,
				CurrentTargetLeaderCount: 0,
				PlannedLeaderGain:        1,
			},
			Moves: []NodeOnboardingPlanMove{{
				SlotID:                 2,
				SourceNodeID:           1,
				TargetNodeID:           4,
				Reason:                 "replica_balance",
				DesiredPeersBefore:     []uint64{1, 2, 3},
				DesiredPeersAfter:      []uint64{2, 3, 4},
				CurrentLeaderID:        1,
				LeaderTransferRequired: true,
			}},
			BlockedReasons: []NodeOnboardingBlockedReason{{
				Code:    "leader_warmup",
				Scope:   "cluster",
				SlotID:  2,
				NodeID:  4,
				Message: "leader observation is warming up",
			}},
		},
		Moves: []NodeOnboardingMove{{
			SlotID:                 2,
			SourceNodeID:           1,
			TargetNodeID:           4,
			Status:                 OnboardingMoveStatusPending,
			TaskKind:               TaskKindRebalance,
			TaskSlotID:             2,
			DesiredPeersBefore:     []uint64{1, 2, 3},
			DesiredPeersAfter:      []uint64{2, 3, 4},
			LeaderBefore:           1,
			LeaderTransferRequired: true,
		}},
		CurrentMoveIndex: -1,
		ResultCounts: OnboardingResultCounts{
			Pending: 1,
		},
	}
}

func sampleOnboardingJobWithStatus(jobID string, status OnboardingJobStatus, now time.Time) NodeOnboardingJob {
	job := sampleOnboardingJob(jobID, now)
	job.RetryOfJobID = ""
	job.Status = status
	if status == OnboardingJobStatusRunning {
		job.StartedAt = now.Add(time.Minute)
	}
	if status == OnboardingJobStatusCompleted || status == OnboardingJobStatusFailed || status == OnboardingJobStatusCancelled {
		job.CompletedAt = now.Add(2 * time.Minute)
	}
	return job
}

func onboardingJobIDs(jobs []NodeOnboardingJob) []string {
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.JobID)
	}
	return ids
}
