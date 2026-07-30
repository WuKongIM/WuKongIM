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
		DecisionSource: contract.DecisionSourceModel,
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
			Action:         reviewagent.ActionRecordInconclusive,
			Reason:         "untrusted approval",
			Generation:     previous.Generation,
			DesiredPhase:   contract.PhaseApproved,
			DecisionSource: contract.DecisionSourceModel,
		},
		time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "evidence")
}

func TestBuildNextStateAllowsDeterministicMergeConflictDecision(t *testing.T) {
	t.Parallel()

	previous := testReviewingState()
	next, err := reviewagent.BuildNextState(
		&previous,
		reviewagent.ReconcilePlan{
			Action:         reviewagent.ActionRecordChangesRequired,
			Reason:         "pull request has merge conflicts",
			Generation:     previous.Generation,
			DesiredPhase:   contract.PhaseChangesRequired,
			DecisionSource: contract.DecisionSourceMergeConflict,
		},
		time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, contract.PhaseChangesRequired, next.Phase)
	require.Empty(t, next.EvidenceDigest)
	require.Empty(t, next.ResultDigest)
	require.NoError(t, contract.ValidateReviewState(next))
}

func TestBuildNextStateRejectsIllegalPhaseRegression(t *testing.T) {
	t.Parallel()

	previous := testReviewingState()
	previous.Phase = contract.PhaseApproved
	previous.DecisionSource = contract.DecisionSourceModel
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

func TestBuildNextStateKeepsDecisionReasonDuringExplanation(t *testing.T) {
	t.Parallel()

	previous := testReviewingState()
	previous.Phase = contract.PhaseApproved
	previous.DecisionSource = contract.DecisionSourceModel
	previous.Reason = "All trusted evidence passed."
	previous.EvidenceDigest = digest("a")
	previous.ResultDigest = digest("b")

	pending, err := reviewagent.BuildNextState(
		&previous,
		reviewagent.ReconcilePlan{
			Action:              reviewagent.ActionExplain,
			Reason:              previous.Reason,
			InteractionRequest:  "Why is this safe?",
			Generation:          previous.Generation,
			DesiredPhase:        previous.Phase,
			ReuseEvidenceDigest: previous.EvidenceDigest,
			ResultDigest:        previous.ResultDigest,
		},
		time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, previous.Reason, pending.Reason)
	require.Equal(t, "Why is this safe?", pending.InteractionRequest)

	reply := "The signed evidence shows that the queue remains serialized."
	explanationDigest := testExplanationDigest(t, pending.Generation, reply)
	completed, err := reviewagent.BuildNextState(
		&pending,
		reviewagent.ReconcilePlan{
			Action:              reviewagent.ActionCompleteExplanation,
			Reason:              pending.Reason,
			Generation:          pending.Generation,
			DesiredPhase:        pending.Phase,
			ReuseEvidenceDigest: pending.EvidenceDigest,
			ResultDigest:        pending.ResultDigest,
			ExplanationDigest:   explanationDigest,
			ExplanationReply:    reply,
		},
		time.Date(2026, 7, 30, 8, 1, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, previous.Reason, completed.Reason)
	require.Empty(t, completed.InteractionRequest)
	require.Equal(t, explanationDigest, completed.ExplanationDigest)
	require.Equal(t, reply, completed.ExplanationReply)
}

func TestBuildNextStateCarriesPriorFindingsIntoReconsideration(
	t *testing.T,
) {
	t.Parallel()

	previous := testReviewingState()
	previous.Phase = contract.PhaseChangesRequired
	previous.DecisionSource = contract.DecisionSourceModel
	previous.EvidenceDigest = digest("a")
	previous.ResultDigest = digest("b")
	previous.PriorFindings = []contract.Finding{testFinding()}
	generation := previous.Generation
	generation.Generation++

	next, err := reviewagent.BuildNextState(
		&previous,
		reviewagent.ReconcilePlan{
			Action:        reviewagent.ActionReconsiderAndDispatch,
			Reason:        "explicit reconsideration",
			Generation:    generation,
			DesiredPhase:  contract.PhaseReviewing,
			PriorFindings: previous.PriorFindings,
			NextBudget:    previous.Budget,
		},
		time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, previous.PriorFindings, next.PriorFindings)
	require.NoError(t, contract.ValidateReviewState(next))
}
