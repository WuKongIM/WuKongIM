package reviewagentverify

import (
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// ValidatedDecision is the trusted effective decision after cross-document
// and immutable-tree validation.
type ValidatedDecision struct {
	Decision       contract.Decision
	Reason         string
	EvidenceDigest string
	ResultDigest   string
}

// ValidateFinalResult prevents advisory model output from outranking missing,
// stale, failed, contradictory, or tree-mutating trusted evidence.
func ValidateFinalResult(
	context contract.ReviewContext,
	evidence contract.ReviewEvidence,
	result contract.ReviewResult,
	beforeTreeDigest string,
	afterTreeDigest string,
) (ValidatedDecision, error) {
	if err := contract.ValidateReviewContext(context); err != nil {
		return ValidatedDecision{}, err
	}
	inconclusive := func(reason string) (ValidatedDecision, error) {
		return ValidatedDecision{
			Decision: contract.DecisionInconclusive,
			Reason:   reason,
		}, nil
	}
	if err := contract.ValidateReviewEvidence(evidence); err != nil {
		return inconclusive("trusted evidence is invalid")
	}
	if err := contract.ValidateReviewResult(result); err != nil {
		return inconclusive("model result is invalid or lacks a file assessment")
	}
	contextGeneration := contract.MustGenerationDigest(context.Generation)
	if contract.MustGenerationDigest(evidence.Generation) != contextGeneration ||
		contract.MustGenerationDigest(result.Generation) != contextGeneration {
		return inconclusive("Review documents name a stale generation")
	}
	if beforeTreeDigest == "" || beforeTreeDigest != afterTreeDigest {
		return inconclusive("tracked candidate tree changed during review")
	}
	if !evidence.Complete {
		return inconclusive("trusted check evidence is incomplete")
	}
	assessed := make(map[string]struct{}, len(result.FileAssessments))
	for _, assessment := range result.FileAssessments {
		assessed[assessment.Path] = struct{}{}
	}
	if len(assessed) != len(context.ChangedFiles) {
		return inconclusive("model result lacks a file assessment")
	}
	for _, file := range context.ChangedFiles {
		if _, exists := assessed[file.Path]; !exists {
			return inconclusive("model result lacks a file assessment")
		}
	}
	checks := make(map[string]contract.CheckEvidence, len(evidence.Checks))
	for _, check := range evidence.Checks {
		checks[check.Name] = check
	}
	for _, mandatory := range context.MandatoryChecks {
		check, exists := checks[mandatory]
		if !exists {
			return inconclusive("mandatory trusted check is missing")
		}
		if result.Decision == contract.DecisionApproved &&
			check.Outcome != contract.CheckOutcomePassed {
			return inconclusive("mandatory trusted check did not pass")
		}
	}
	for _, source := range result.Sources {
		if !strings.HasPrefix(source, "check:") {
			continue
		}
		if _, exists := checks[strings.TrimPrefix(source, "check:")]; !exists {
			return inconclusive("model result cites unknown check evidence")
		}
	}
	evidenceDigest, err := contract.ReviewEvidenceDigest(evidence)
	if err != nil {
		return ValidatedDecision{}, err
	}
	resultDigest, err := contract.ReviewResultDigest(result)
	if err != nil {
		return ValidatedDecision{}, err
	}
	return ValidatedDecision{
		Decision:       result.Decision,
		Reason:         result.Summary,
		EvidenceDigest: evidenceDigest,
		ResultDigest:   resultDigest,
	}, nil
}
