package issueagent_test

import (
	"testing"

	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestDriftRecoveryNeverOverwritesExternalHeadAndBoundsConflicts(t *testing.T) {
	t.Parallel()

	external := "234567890abcdef1234567890abcdef123456789"
	decision, err := issueagentusecase.PlanDriftRecovery(
		issueagentusecase.DriftFacts{
			ExpectedAgentHead: affectedSHA, CurrentAgentHead: external,
			CurrentMainSHA: baseSHA,
		},
	)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.DriftAwaitHeadAdoption, decision)

	for attempts, want := range map[int]issueagentusecase.DriftDecision{
		0: issueagentusecase.DriftMechanicalRebase,
		1: issueagentusecase.DriftReadyForHuman,
	} {
		decision, err = issueagentusecase.PlanDriftRecovery(
			issueagentusecase.DriftFacts{
				ExpectedAgentHead: affectedSHA, CurrentAgentHead: affectedSHA,
				CurrentMainSHA: baseSHA, Conflict: "mechanical",
				MechanicalTreeSHA: "34567890abcdef1234567890abcdef1234567890",
				ConflictAttempts:  attempts,
			},
		)
		require.NoError(t, err)
		require.Equal(t, want, decision)
	}
}

func TestMovingMainPassClosesOnlyDraftAndRecordsAlreadyFixed(t *testing.T) {
	t.Parallel()

	runs := observedRuns(20, baseSHA, issueagentusecase.RunPassed)
	decision, err := issueagentusecase.PlanDriftRecovery(
		issueagentusecase.DriftFacts{
			ExpectedAgentHead: affectedSHA, CurrentAgentHead: affectedSHA,
			CurrentMainSHA: baseSHA, MainRuns: runs,
			AssertionSHA256: assertionDigest, Topology: "three-node-cluster",
		},
	)
	require.NoError(t, err)
	require.Equal(t, issueagentusecase.DriftAlreadyFixedOnMain, decision)
	projection := issueagentusecase.ProjectAlreadyFixedOnMain()
	require.True(t, projection.CloseDraftPR)
	require.False(t, projection.CloseIssue)
}
