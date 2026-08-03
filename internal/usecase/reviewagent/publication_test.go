package reviewagent_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestPlanPublicationMapsTrustedStateToSoleVerdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		phase      contract.Phase
		review     reviewagent.FormalReview
		conclusion reviewagent.CheckConclusion
	}{
		{
			phase:      contract.PhaseApproved,
			review:     reviewagent.FormalReviewApprove,
			conclusion: reviewagent.CheckSuccess,
		},
		{
			phase:      contract.PhaseChangesRequired,
			review:     reviewagent.FormalReviewRequestChanges,
			conclusion: reviewagent.CheckFailure,
		},
		{
			phase:      contract.PhaseInconclusive,
			review:     reviewagent.FormalReviewComment,
			conclusion: reviewagent.CheckActionRequired,
		},
	}
	for _, test := range tests {
		state := testReviewingState()
		state.Phase = test.phase
		state.DecisionSource = contract.DecisionSourceModel
		state.EvidenceDigest = digest("a")
		state.ResultDigest = digest("b")
		plan, err := reviewagent.PlanPublication(
			state,
			reviewagent.PublicationFacts{},
		)
		require.NoError(t, err)
		require.Equal(t, "Review Agent Verdict", plan.CheckName)
		require.Equal(t, test.review, plan.Review)
		require.Equal(t, test.conclusion, plan.Conclusion)
		require.NotEmpty(t, plan.ExternalID)
		require.NotEmpty(t, plan.StatusMarker)
	}
}

func TestPlanPublicationDoesNotOverrideHumanChangesRequest(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseApproved
	state.DecisionSource = contract.DecisionSourceModel
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	plan, err := reviewagent.PlanPublication(
		state,
		reviewagent.PublicationFacts{HumanChangesRequested: true},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.CheckSuccess, plan.Conclusion)
	require.True(t, plan.HumanReviewStillBlocks)
}

func TestPlanPublicationAllowsAutomaticMergeForApprovedMember(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseApproved
	state.DecisionSource = contract.DecisionSourceModel
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	plan, err := reviewagent.PlanPublication(
		state,
		reviewagent.PublicationFacts{
			AuthorAssociation: "MEMBER",
			Mergeability:      reviewagent.MergeabilityClean,
		},
	)
	require.NoError(t, err)
	require.True(t, plan.AutomaticMerge)
}

func TestPlanPublicationAllowsAutomaticMergeForRepositoryAdmin(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseApproved
	state.DecisionSource = contract.DecisionSourceModel
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	plan, err := reviewagent.PlanPublication(
		state,
		reviewagent.PublicationFacts{
			AuthorAssociation: "COLLABORATOR",
			AuthorPermission:  reviewagent.PermissionAdmin,
			Mergeability:      reviewagent.MergeabilityClean,
		},
	)
	require.NoError(t, err)
	require.True(t, plan.AutomaticMerge)
}

func TestPlanPublicationRequiresTrustedAuthorAndSafeMergeState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		phase contract.Phase
		facts reviewagent.PublicationFacts
	}{
		{
			name:  "external contributor",
			phase: contract.PhaseApproved,
			facts: reviewagent.PublicationFacts{
				AuthorAssociation: "CONTRIBUTOR",
				AuthorPermission:  reviewagent.PermissionRead,
				Mergeability:      reviewagent.MergeabilityClean,
			},
		},
		{
			name:  "write collaborator",
			phase: contract.PhaseApproved,
			facts: reviewagent.PublicationFacts{
				AuthorAssociation: "COLLABORATOR",
				AuthorPermission:  reviewagent.PermissionWrite,
				Mergeability:      reviewagent.MergeabilityClean,
			},
		},
		{
			name:  "human changes requested",
			phase: contract.PhaseApproved,
			facts: reviewagent.PublicationFacts{
				HumanChangesRequested: true,
				AuthorAssociation:     "MEMBER",
				Mergeability:          reviewagent.MergeabilityClean,
			},
		},
		{
			name:  "mergeability unresolved",
			phase: contract.PhaseApproved,
			facts: reviewagent.PublicationFacts{
				AuthorAssociation: "MEMBER",
				Mergeability:      reviewagent.MergeabilityUnknown,
			},
		},
		{
			name:  "changes required",
			phase: contract.PhaseChangesRequired,
			facts: reviewagent.PublicationFacts{
				AuthorAssociation: "MEMBER",
				Mergeability:      reviewagent.MergeabilityClean,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := testReviewingState()
			state.Phase = test.phase
			state.DecisionSource = contract.DecisionSourceModel
			state.EvidenceDigest = digest("a")
			state.ResultDigest = digest("b")
			plan, err := reviewagent.PlanPublication(state, test.facts)
			require.NoError(t, err)
			require.False(t, plan.AutomaticMerge)
		})
	}
}

func TestPlanPublicationMapsDeterministicConflictWithoutArtifacts(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseChangesRequired
	state.DecisionSource = contract.DecisionSourceMergeConflict
	state.Reason = "pull request has merge conflicts"
	plan, err := reviewagent.PlanPublication(
		state,
		reviewagent.PublicationFacts{},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.FormalReviewRequestChanges, plan.Review)
	require.Equal(t, reviewagent.CheckFailure, plan.Conclusion)
}
