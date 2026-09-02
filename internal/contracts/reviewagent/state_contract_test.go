package reviewagent_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

func TestReviewStateCanonicalRoundTripPreservesAuthority(t *testing.T) {
	t.Parallel()

	state := validReviewingState()
	state.Sequence = 2
	state.PreviousStateDigest = digest("a")
	state.Phase = reviewagent.PhaseApproved
	state.DecisionSource = reviewagent.DecisionSourceModel
	state.InteractionRequest = "explain finding 1"
	state.EvidenceDigest = digest("b")
	state.ResultDigest = digest("c")
	state.ExplanationDigest = digest("d")
	state.ExplanationReply = "The evidence reproduces the duplicate delivery."
	state.PriorFindings = []reviewagent.Finding{validFinding()}

	body, err := reviewagent.CanonicalReviewState(state)
	require.NoError(t, err)
	decoded, err := reviewagent.DecodeReviewState(
		bytes.NewReader(body),
		int64(len(body)),
	)
	require.NoError(t, err)
	require.Equal(t, state, decoded)

	digestBefore, err := reviewagent.ReviewStateDigest(state)
	require.NoError(t, err)
	digestAfter, err := reviewagent.ReviewStateDigest(decoded)
	require.NoError(t, err)
	require.Equal(t, digestBefore, digestAfter)

	decoded.Reason = "review completed after a fresh head check"
	changedDigest, err := reviewagent.ReviewStateDigest(decoded)
	require.NoError(t, err)
	require.NotEqual(t, digestBefore, changedDigest)
}

func TestReviewStateAcceptsOnlyAuthorizedPhaseSourceCombinations(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*reviewagent.ReviewState){
		"awaiting ready": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseAwaitingReady
		},
		"queued": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseQueued
		},
		"reviewing": func(state *reviewagent.ReviewState) {},
		"canceled": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseCanceled
		},
		"superseded": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseSuperseded
		},
		"closed": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseClosed
		},
		"approved model": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseApproved
			state.DecisionSource = reviewagent.DecisionSourceModel
			state.EvidenceDigest = digest("1")
			state.ResultDigest = digest("2")
		},
		"changes required model": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseChangesRequired
			state.DecisionSource = reviewagent.DecisionSourceModel
			state.EvidenceDigest = digest("1")
			state.ResultDigest = digest("2")
		},
		"changes required merge conflict": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseChangesRequired
			state.DecisionSource = reviewagent.DecisionSourceMergeConflict
		},
		"inconclusive model": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseInconclusive
			state.DecisionSource = reviewagent.DecisionSourceModel
			state.EvidenceDigest = digest("1")
			state.ResultDigest = digest("2")
		},
		"inconclusive policy": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseInconclusive
			state.DecisionSource = reviewagent.DecisionSourcePolicy
		},
		"inconclusive infrastructure": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseInconclusive
			state.DecisionSource = reviewagent.DecisionSourceInfrastructure
		},
	}
	for name, configure := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := validReviewingState()
			configure(&state)
			require.NoError(t, reviewagent.ValidateReviewState(state))
		})
	}
}

func TestReviewStateRejectsContradictoryAuthority(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*reviewagent.ReviewState){
		"schema": func(state *reviewagent.ReviewState) {
			state.SchemaVersion = 2
		},
		"generation": func(state *reviewagent.ReviewState) {
			state.Generation.StateParentSHA = ""
		},
		"sequence": func(state *reviewagent.ReviewState) {
			state.Sequence = 0
		},
		"phase": func(state *reviewagent.ReviewState) {
			state.Phase = "publishing"
		},
		"reason": func(state *reviewagent.ReviewState) {
			state.Reason = ""
		},
		"interaction on non-decision": func(state *reviewagent.ReviewState) {
			state.InteractionRequest = "explain"
		},
		"initial predecessor": func(state *reviewagent.ReviewState) {
			state.PreviousStateDigest = digest("1")
		},
		"successor without predecessor": func(state *reviewagent.ReviewState) {
			state.Sequence = 2
		},
		"artifact digest": func(state *reviewagent.ReviewState) {
			state.EvidenceDigest = "sha256:short"
		},
		"explanation on non-decision": func(state *reviewagent.ReviewState) {
			state.ExplanationDigest = digest("1")
			state.ExplanationReply = "reply"
		},
		"explanation digest without reply": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseInconclusive
			state.DecisionSource = reviewagent.DecisionSourcePolicy
			state.ExplanationDigest = digest("1")
		},
		"too many prior findings": func(state *reviewagent.ReviewState) {
			state.PriorFindings = make(
				[]reviewagent.Finding,
				reviewagent.MaxFindings+1,
			)
		},
		"invalid prior finding": func(state *reviewagent.ReviewState) {
			state.PriorFindings = []reviewagent.Finding{{}}
		},
		"approved without model source": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseApproved
			state.DecisionSource = reviewagent.DecisionSourcePolicy
			state.EvidenceDigest = digest("1")
			state.ResultDigest = digest("2")
		},
		"model changes without artifacts": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseChangesRequired
			state.DecisionSource = reviewagent.DecisionSourceModel
		},
		"merge conflict with artifacts": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseChangesRequired
			state.DecisionSource = reviewagent.DecisionSourceMergeConflict
			state.ResultDigest = digest("2")
		},
		"changes required policy source": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseChangesRequired
			state.DecisionSource = reviewagent.DecisionSourcePolicy
		},
		"model inconclusive without artifacts": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseInconclusive
			state.DecisionSource = reviewagent.DecisionSourceModel
		},
		"policy inconclusive with artifacts": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseInconclusive
			state.DecisionSource = reviewagent.DecisionSourcePolicy
			state.EvidenceDigest = digest("1")
		},
		"inconclusive merge source": func(state *reviewagent.ReviewState) {
			state.Phase = reviewagent.PhaseInconclusive
			state.DecisionSource = reviewagent.DecisionSourceMergeConflict
		},
		"non-decision source": func(state *reviewagent.ReviewState) {
			state.DecisionSource = reviewagent.DecisionSourceModel
		},
		"non-decision result": func(state *reviewagent.ReviewState) {
			state.ResultDigest = digest("1")
		},
		"started local time": func(state *reviewagent.ReviewState) {
			state.StartedAt = time.Date(
				2026, 8, 1, 8, 0, 0, 0,
				time.FixedZone("local", 3600),
			)
		},
		"updated before start": func(state *reviewagent.ReviewState) {
			state.UpdatedAt = state.StartedAt.Add(-time.Second)
		},
		"deadline at start": func(state *reviewagent.ReviewState) {
			state.SessionDeadlineAt = state.StartedAt
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := validReviewingState()
			mutate(&state)
			require.Error(t, reviewagent.ValidateReviewState(state))
		})
	}
}

func TestDecodeReviewStateRejectsUnknownAndOversizedInput(t *testing.T) {
	t.Parallel()

	body, err := reviewagent.CanonicalReviewState(validReviewingState())
	require.NoError(t, err)
	unknown := strings.Replace(
		string(body),
		`"sequence":1`,
		`"sequence":1,"force_publish":true`,
		1,
	)
	_, err = reviewagent.DecodeReviewState(
		strings.NewReader(unknown),
		int64(len(unknown)),
	)
	require.Error(t, err)

	_, err = reviewagent.DecodeReviewState(
		bytes.NewReader(body),
		int64(len(body)-1),
	)
	require.EqualError(t, err, "JSON input exceeds byte limit")
}
