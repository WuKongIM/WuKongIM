package reviewagent

import (
	"errors"
	"fmt"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// FormalReview is the only Review event the publisher may submit.
type FormalReview string

const (
	FormalReviewApprove        FormalReview = "APPROVE"
	FormalReviewRequestChanges FormalReview = "REQUEST_CHANGES"
	FormalReviewComment        FormalReview = "COMMENT"
)

// CheckConclusion is the trusted mapping for Review Agent Verdict.
type CheckConclusion string

const (
	CheckSuccess        CheckConclusion = "success"
	CheckFailure        CheckConclusion = "failure"
	CheckActionRequired CheckConclusion = "action_required"
)

// PublicationFacts are freshly re-read immediately before publication.
type PublicationFacts struct {
	HumanChangesRequested bool
	AuthorAssociation     string
	AuthorPermission      Permission
	Mergeability          Mergeability
}

// PublicationPlan contains the projections and bounded merge decision
// supported by the protected Review Publisher.
type PublicationPlan struct {
	CheckName              string
	ExternalID             string
	StatusMarker           string
	Review                 FormalReview
	Conclusion             CheckConclusion
	HumanReviewStillBlocks bool
	AutomaticMerge         bool
	HumanMergeRequired     bool
}

// PlanPublication maps validated durable state to GitHub projections and
// automatic-merge eligibility. It performs no GitHub or branch effect.
func PlanPublication(
	state contract.ReviewState,
	facts PublicationFacts,
) (PublicationPlan, error) {
	if err := contract.ValidateReviewState(state); err != nil {
		return PublicationPlan{}, err
	}
	generationDigest := contract.MustGenerationDigest(state.Generation)
	plan := PublicationPlan{
		CheckName:  "Review Agent Verdict",
		ExternalID: "review-agent/" + generationDigest,
		StatusMarker: fmt.Sprintf(
			"<!-- review-agent-status:pr-%d -->",
			state.Generation.PullRequest,
		),
		HumanReviewStillBlocks: facts.HumanChangesRequested,
	}
	switch state.Phase {
	case contract.PhaseApproved:
		plan.Review = FormalReviewApprove
		plan.Conclusion = CheckSuccess
		trustedAuthor := facts.AuthorAssociation == "MEMBER" ||
			facts.AuthorAssociation == "OWNER" ||
			facts.AuthorPermission == PermissionAdmin
		plan.AutomaticMerge = !facts.HumanChangesRequested &&
			facts.Mergeability == MergeabilityClean &&
			trustedAuthor
		plan.HumanMergeRequired = !trustedAuthor
	case contract.PhaseChangesRequired:
		plan.Review = FormalReviewRequestChanges
		plan.Conclusion = CheckFailure
	case contract.PhaseInconclusive:
		plan.Review = FormalReviewComment
		plan.Conclusion = CheckActionRequired
	default:
		return PublicationPlan{}, errors.New(
			"Review state has no publishable decision",
		)
	}
	return plan, nil
}
