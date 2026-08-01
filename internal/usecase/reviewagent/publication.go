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
}

// PublicationPlan contains only the projections supported by the Review App.
type PublicationPlan struct {
	CheckName              string
	ExternalID             string
	StatusMarker           string
	Review                 FormalReview
	Conclusion             CheckConclusion
	HumanReviewStillBlocks bool
}

// PlanPublication maps validated durable state to GitHub projections. It does
// not expose merge or branch effects.
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
