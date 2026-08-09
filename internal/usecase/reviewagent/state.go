package reviewagent

import (
	"errors"
	"reflect"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// BuildNextState materializes one legal plan as canonical durable state. It
// never invents missing evidence for a decision.
func BuildNextState(
	previous *contract.ReviewState,
	plan ReconcilePlan,
	now time.Time,
) (contract.ReviewState, error) {
	if now.IsZero() || now.Location() != time.UTC {
		return contract.ReviewState{}, errors.New(
			"Review state time must use UTC",
		)
	}
	if err := contract.ValidateGenerationIdentity(plan.Generation); err != nil {
		return contract.ReviewState{}, err
	}
	if plan.Reason == "" {
		return contract.ReviewState{}, errors.New("Review state reason is required")
	}
	next := contract.ReviewState{
		SchemaVersion:      1,
		Generation:         plan.Generation,
		Sequence:           1,
		Phase:              plan.DesiredPhase,
		DecisionSource:     plan.DecisionSource,
		Reason:             plan.Reason,
		InteractionRequest: plan.InteractionRequest,
		EvidenceDigest:     plan.EvidenceDigest,
		ResultDigest:       plan.ResultDigest,
		ExplanationDigest:  plan.ExplanationDigest,
		ExplanationReply:   plan.ExplanationReply,
		PriorFindings: append(
			[]contract.Finding(nil),
			plan.PriorFindings...,
		),
		Budget:            plan.NextBudget,
		StartedAt:         now,
		SessionDeadlineAt: plan.DeadlineAt,
		UpdatedAt:         now,
	}
	if next.EvidenceDigest == "" {
		next.EvidenceDigest = plan.ReuseEvidenceDigest
	}
	if previous != nil {
		if err := contract.ValidateReviewState(*previous); err != nil {
			return contract.ReviewState{}, err
		}
		if !legalTransition(*previous, next) {
			return contract.ReviewState{}, errors.New(
				"illegal Review state transition",
			)
		}
		previousDigest, err := contract.ReviewStateDigest(*previous)
		if err != nil {
			return contract.ReviewState{}, err
		}
		next.Sequence = previous.Sequence + 1
		next.PreviousStateDigest = previousDigest
		if next.Generation.HeadSHA == previous.Generation.HeadSHA &&
			reflect.DeepEqual(
				plan.NextBudget,
				contract.InteractionBudget{},
			) {
			next.Budget = previous.Budget
		}
		if contract.MustGenerationDigest(next.Generation) ==
			contract.MustGenerationDigest(previous.Generation) &&
			len(plan.PriorFindings) == 0 &&
			(plan.DesiredPhase == contract.PhaseReviewing ||
				plan.Action == ActionCompleteExplanation ||
				plan.Action == ActionExplain) {
			next.PriorFindings = append(
				[]contract.Finding(nil),
				previous.PriorFindings...,
			)
		}
		if plan.Action == ActionCompleteExplanation ||
			plan.Action == ActionExplain {
			next.DecisionSource = previous.DecisionSource
		}
		if plan.Action == ActionExplain &&
			next.ExplanationDigest == "" {
			next.ExplanationDigest = previous.ExplanationDigest
			next.ExplanationReply = previous.ExplanationReply
		}
		if contract.MustGenerationDigest(next.Generation) ==
			contract.MustGenerationDigest(previous.Generation) {
			next.StartedAt = previous.StartedAt
			if next.SessionDeadlineAt.IsZero() &&
				plan.Action != ActionRetryAndEnqueue {
				next.SessionDeadlineAt = previous.SessionDeadlineAt
			}
		}
	}
	if err := contract.ValidateReviewState(next); err != nil {
		return contract.ReviewState{}, err
	}
	return next, nil
}

func legalTransition(
	previous contract.ReviewState,
	next contract.ReviewState,
) bool {
	sameGeneration := contract.MustGenerationDigest(previous.Generation) ==
		contract.MustGenerationDigest(next.Generation)
	if sameGeneration {
		if previous.Phase == next.Phase {
			return true
		}
		switch previous.Phase {
		case contract.PhaseAwaitingReady:
			return next.Phase == contract.PhaseQueued ||
				next.Phase == contract.PhaseReviewing ||
				next.Phase == contract.PhaseInconclusive ||
				next.Phase == contract.PhaseClosed
		case contract.PhaseQueued:
			return next.Phase == contract.PhaseReviewing ||
				next.Phase == contract.PhaseAwaitingReady ||
				next.Phase == contract.PhaseCanceled ||
				next.Phase == contract.PhaseSuperseded ||
				next.Phase == contract.PhaseClosed
		case contract.PhaseReviewing:
			return decisionPhase(next.Phase) ||
				next.Phase == contract.PhaseQueued ||
				next.Phase == contract.PhaseAwaitingReady ||
				next.Phase == contract.PhaseCanceled ||
				next.Phase == contract.PhaseSuperseded ||
				next.Phase == contract.PhaseClosed
		case contract.PhaseApproved,
			contract.PhaseChangesRequired,
			contract.PhaseInconclusive:
			return next.Phase == contract.PhaseAwaitingReady ||
				next.Phase == contract.PhaseClosed
		case contract.PhaseCanceled,
			contract.PhaseSuperseded:
			return next.Phase == contract.PhaseAwaitingReady ||
				next.Phase == contract.PhaseClosed
		default:
			return false
		}
	}
	if next.Generation.Generation != previous.Generation.Generation+1 ||
		next.Generation.Repository != previous.Generation.Repository ||
		next.Generation.PullRequest != previous.Generation.PullRequest {
		return false
	}
	switch next.Phase {
	case contract.PhaseAwaitingReady,
		contract.PhaseQueued,
		contract.PhaseReviewing,
		contract.PhaseChangesRequired,
		contract.PhaseInconclusive,
		contract.PhaseClosed:
		return true
	default:
		return false
	}
}
