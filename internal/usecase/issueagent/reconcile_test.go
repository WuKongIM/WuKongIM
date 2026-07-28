package issueagent_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestReconcileDerivesWorkFromCurrentCheckpointNotEventPayload(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	input := issueagentusecase.ReconcileInput{
		Now:                 now,
		ChainStatus:         issueagentusecase.ChainValid,
		Checkpoint:          reconcileCheckpoint(issueagent.StateAuthorized),
		CheckpointCommentID: 101,
		CheckpointDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	policy := issueagentusecase.ReconcilePolicy{
		Enabled:     true,
		RolloutMode: issueagentusecase.RolloutReproduction,
	}

	first, err := issueagentusecase.Reconcile(input, policy)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.OperationResolveVersions, first.Operation)
	require.True(t, first.WriteAllowed)

	duplicateEventPlan, err := issueagentusecase.Reconcile(input, policy)
	require.NoError(t, err)
	require.Equal(t, first, duplicateEventPlan)
}

func TestReconcileRejectsBrokenChainAndStaleWorkerArtifact(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	policy := issueagentusecase.ReconcilePolicy{
		Enabled:     true,
		RolloutMode: issueagentusecase.RolloutGeneral,
	}

	broken, err := issueagentusecase.Reconcile(issueagentusecase.ReconcileInput{
		Now:         now,
		ChainStatus: issueagentusecase.ChainInvalid,
	}, policy)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.OperationAlertAuditFailure, broken.Operation)
	require.False(t, broken.WriteAllowed)

	stale, err := issueagentusecase.Reconcile(issueagentusecase.ReconcileInput{
		Now:                 now,
		ChainStatus:         issueagentusecase.ChainValid,
		Checkpoint:          reconcileCheckpoint(issueagent.StateReproducing),
		CheckpointCommentID: 102,
		CheckpointDigest:    "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Lease: &issueagentusecase.LeaseFacts{
			OperationID: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			TaskDigest:  "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			Generation:  1,
			ExpiresAt:   now.Add(time.Hour),
		},
		Artifacts: []issueagentusecase.WorkerArtifact{{
			RunID:       900,
			OperationID: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			TaskDigest:  "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			Generation:  1,
		}},
	}, policy)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.OperationWait, stale.Operation)
}

func TestReconcilePublishesOnlyCurrentUnexpiredWorkerResult(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	operationID := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	taskDigest := "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	input := issueagentusecase.ReconcileInput{
		Now:                 now,
		ChainStatus:         issueagentusecase.ChainValid,
		Checkpoint:          reconcileCheckpoint(issueagent.StateReproducing),
		CheckpointCommentID: 102,
		CheckpointDigest:    "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Lease: &issueagentusecase.LeaseFacts{
			OperationID: operationID,
			TaskDigest:  taskDigest,
			Generation:  1,
			ExpiresAt:   now.Add(time.Hour),
		},
		Artifacts: []issueagentusecase.WorkerArtifact{{
			RunID:       900,
			OperationID: operationID,
			TaskDigest:  taskDigest,
			Generation:  1,
		}},
	}
	policy := issueagentusecase.ReconcilePolicy{
		Enabled:     true,
		RolloutMode: issueagentusecase.RolloutGeneral,
	}

	plan, err := issueagentusecase.Reconcile(input, policy)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.OperationPublishWorkerResult, plan.Operation)
	require.Equal(t, int64(900), plan.ArtifactRunID)
	require.Equal(t, int64(102), plan.ExpectedCheckpointCommentID)

	input.Artifacts = nil
	input.Lease.ExpiresAt = now.Add(-time.Second)
	expired, err := issueagentusecase.Reconcile(input, policy)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.OperationExpireLease, expired.Operation)
}

func TestReconcileRecoversMissedExactMergeEvent(t *testing.T) {
	t.Parallel()

	checkpoint := reconcileCheckpoint(issueagent.StateReadyForReview)
	checkpoint.Work = &issueagent.Work{
		Branch: "agent/issue-42", HeadSHA: "0123456789abcdef0123456789abcdef01234567",
		PRNumber: 9,
	}
	input := issueagentusecase.ReconcileInput{
		Now:                 time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		ChainStatus:         issueagentusecase.ChainValid,
		Checkpoint:          checkpoint,
		CheckpointCommentID: 102,
		CheckpointDigest:    "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Merge: &issueagentusecase.MergeFacts{
			PRNumber: 9, HeadSHA: checkpoint.Work.HeadSHA, Merged: true,
		},
	}
	policy := issueagentusecase.ReconcilePolicy{
		Enabled: true, RolloutMode: issueagentusecase.RolloutGeneral,
	}
	plan, err := issueagentusecase.Reconcile(input, policy)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.OperationRecordMerge, plan.Operation)
	require.True(t, plan.WriteAllowed)

	input.Merge.HeadSHA = "89abcdef0123456789abcdef0123456789abcdef"
	_, err = issueagentusecase.Reconcile(input, policy)
	require.Error(t, err)
}

func TestReconcileShadowModeNeverProducesWriteAuthority(t *testing.T) {
	t.Parallel()

	plan, err := issueagentusecase.Reconcile(issueagentusecase.ReconcileInput{
		Now:         time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		ChainStatus: issueagentusecase.ChainValid,
		Checkpoint:  reconcileCheckpoint(issueagent.StateAuthorized),
	}, issueagentusecase.ReconcilePolicy{
		Enabled:     true,
		RolloutMode: issueagentusecase.RolloutShadow,
	})
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.OperationReportOnly, plan.Operation)
	require.False(t, plan.WriteAllowed)
}

func TestReconcileIntakeModeOnlyAdmitsDeterministicIntake(t *testing.T) {
	t.Parallel()

	plan, err := issueagentusecase.Reconcile(issueagentusecase.ReconcileInput{
		Now:         time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		ChainStatus: issueagentusecase.ChainMissing,
	}, issueagentusecase.ReconcilePolicy{
		Enabled:     true,
		RolloutMode: issueagentusecase.RolloutIntake,
	})
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.OperationIntakeIssue, plan.Operation)
	require.True(t, plan.WriteAllowed)

	authorized := reconcileCheckpoint(issueagent.StateAuthorized)
	blocked, err := issueagentusecase.Reconcile(issueagentusecase.ReconcileInput{
		Now:                 time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		ChainStatus:         issueagentusecase.ChainValid,
		Checkpoint:          authorized,
		CheckpointCommentID: 10,
		CheckpointDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}, issueagentusecase.ReconcilePolicy{
		Enabled:     true,
		RolloutMode: issueagentusecase.RolloutIntake,
	})
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.OperationWait, blocked.Operation)
	require.False(t, blocked.WriteAllowed)
}

func reconcileCheckpoint(state issueagent.State) *issueagent.Checkpoint {
	return &issueagent.Checkpoint{
		SchemaVersion: 1,
		Repository:    "WuKongIM/WuKongIM",
		IssueNumber:   42,
		Generation:    1,
		Sequence:      2,
		State:         state,
	}
}
