package reviewagent_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestBuildNextStateCreatesCanonicalLegalSuccessor(t *testing.T) {
	t.Parallel()

	previous := testReviewingState()
	plan := reviewagent.ReconcilePlan{
		Action:         reviewagent.ActionComplete,
		Reason:         "review completed",
		Generation:     previous.Generation,
		DesiredPhase:   contract.PhaseApproved,
		EvidenceDigest: digest("a"),
		ResultDigest:   digest("b"),
	}
	next, err := reviewagent.BuildNextState(
		&previous,
		plan,
		time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, uint64(2), next.Sequence)
	require.Equal(t, contract.PhaseApproved, next.Phase)
	require.Equal(t, digest("a"), next.EvidenceDigest)
	require.Equal(t, digest("b"), next.ResultDigest)
	require.NotEmpty(t, next.PreviousStateDigest)
	require.NoError(t, contract.ValidateReviewState(next))
}

func TestBuildNextStateRejectsDecisionWithoutTrustedArtifacts(t *testing.T) {
	t.Parallel()

	previous := testReviewingState()
	_, err := reviewagent.BuildNextState(
		&previous,
		reviewagent.ReconcilePlan{
			Action:       reviewagent.ActionRecordInconclusive,
			Reason:       "diff pagination incomplete",
			Generation:   previous.Generation,
			DesiredPhase: contract.PhaseInconclusive,
		},
		time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "evidence")
}

func TestBuildNextStateRejectsIllegalPhaseRegression(t *testing.T) {
	t.Parallel()

	previous := testReviewingState()
	previous.Phase = contract.PhaseApproved
	previous.EvidenceDigest = digest("a")
	previous.ResultDigest = digest("b")

	_, err := reviewagent.BuildNextState(
		&previous,
		reviewagent.ReconcilePlan{
			Action:       reviewagent.ActionAppendState,
			Reason:       "invalid regression",
			Generation:   previous.Generation,
			DesiredPhase: contract.PhaseReviewing,
		},
		time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
	)
	require.EqualError(t, err, "illegal Review state transition")
}
