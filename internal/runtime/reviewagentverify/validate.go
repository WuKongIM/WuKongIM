package reviewagentverify

import (
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

// ValidatedDecision is the trusted effective decision after cross-document
// and immutable-tree validation.
type ValidatedDecision struct {
	Decision       contract.Decision  `json:"decision"`
	Reason         string             `json:"reason"`
	EvidenceDigest string             `json:"evidence_digest"`
	ResultDigest   string             `json:"result_digest"`
	Findings       []contract.Finding `json:"findings"`
	// EffectiveResult is present when trusted evidence downgrades the advisory
	// model decision and therefore requires a newly bound publication result.
	EffectiveResult *contract.ReviewResult `json:"effective_result,omitempty"`
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
	priorFindings := make([]contract.Finding, 0, len(context.PriorFindings))
	for _, prior := range context.PriorFindings {
		priorFindings = append(priorFindings, prior.Finding)
	}
	inconclusive := func(
		reason string,
		evidenceDigest string,
		resultDigest string,
	) (ValidatedDecision, error) {
		return ValidatedDecision{
			Decision:       contract.DecisionInconclusive,
			Reason:         reason,
			EvidenceDigest: evidenceDigest,
			ResultDigest:   resultDigest,
			Findings: append(
				[]contract.Finding(nil),
				priorFindings...,
			),
		}, nil
	}
	for _, file := range context.ChangedFiles {
		if file.Type != string(FileTypeText) {
			return inconclusive(
				"binary or unsupported changes cannot be reviewed safely",
				"",
				"",
			)
		}
	}
	if err := contract.ValidateReviewEvidence(evidence); err != nil {
		return inconclusive("trusted evidence is invalid", "", "")
	}
	if err := contract.ValidateReviewResult(result); err != nil {
		return inconclusive(
			"model result is invalid or lacks a file assessment",
			"",
			"",
		)
	}
	if err := contract.ValidatePriorFindingDispositions(priorFindings, result); err != nil {
		return inconclusive(
			"model result does not explicitly disposition prior findings",
			"",
			"",
		)
	}
	evidenceDigest, err := contract.ReviewEvidenceDigest(evidence)
	if err != nil {
		return ValidatedDecision{}, err
	}
	resultDigest, err := contract.ReviewResultDigest(result)
	if err != nil {
		return ValidatedDecision{}, err
	}
	if formalReviewBodyBytes(result) > 64<<10 {
		return inconclusive(
			"model result exceeds the bounded formal Review projection",
			evidenceDigest,
			resultDigest,
		)
	}
	contextGeneration := contract.MustGenerationDigest(context.Generation)
	if contract.MustGenerationDigest(evidence.Generation) != contextGeneration ||
		contract.MustGenerationDigest(result.Generation) != contextGeneration {
		return inconclusive(
			"Review documents name a stale generation",
			evidenceDigest,
			resultDigest,
		)
	}
	if beforeTreeDigest == "" || beforeTreeDigest != afterTreeDigest {
		return inconclusive(
			"tracked candidate tree changed during review",
			evidenceDigest,
			resultDigest,
		)
	}
	if !evidence.Complete {
		return inconclusive(
			"trusted check evidence is incomplete",
			evidenceDigest,
			resultDigest,
		)
	}
	assessed := make(map[string]struct{}, len(result.FileAssessments))
	for _, assessment := range result.FileAssessments {
		assessed[assessment.Path] = struct{}{}
	}
	if len(assessed) != len(context.ChangedFiles) {
		return inconclusive(
			"model result lacks a file assessment",
			evidenceDigest,
			resultDigest,
		)
	}
	for _, file := range context.ChangedFiles {
		if _, exists := assessed[file.Path]; !exists {
			return inconclusive(
				"model result lacks a file assessment",
				evidenceDigest,
				resultDigest,
			)
		}
	}
	checks := make(map[string]contract.CheckEvidence, len(evidence.Checks))
	for _, check := range evidence.Checks {
		checks[check.Name] = check
	}
	for _, mandatory := range context.MandatoryChecks {
		check, exists := checks[mandatory]
		if !exists {
			return inconclusive(
				"mandatory trusted check is missing",
				evidenceDigest,
				resultDigest,
			)
		}
		if check.Outcome == contract.CheckOutcomeError {
			return inconclusive(
				"mandatory trusted check infrastructure failed",
				evidenceDigest,
				resultDigest,
			)
		}
		if result.Decision == contract.DecisionApproved &&
			check.Outcome != contract.CheckOutcomePassed {
			const reason = "mandatory trusted check did not pass"
			effective := result
			effective.Decision = contract.DecisionInconclusive
			effective.Summary = reason
			effective.Findings = append(
				[]contract.Finding(nil),
				result.Findings...,
			)
			effective.Sources = make([]string, 0, len(evidence.Checks))
			for _, trustedCheck := range evidence.Checks {
				effective.Sources = append(
					effective.Sources,
					"check:"+trustedCheck.Name,
				)
			}
			effective.UnresolvedUncertainty = reason
			effectiveDigest, digestErr := contract.ReviewResultDigest(effective)
			if digestErr != nil {
				return ValidatedDecision{}, digestErr
			}
			return ValidatedDecision{
				Decision:       contract.DecisionInconclusive,
				Reason:         reason,
				EvidenceDigest: evidenceDigest,
				ResultDigest:   effectiveDigest,
				Findings: append(
					[]contract.Finding(nil),
					result.Findings...,
				),
				EffectiveResult: &effective,
			}, nil
		}
	}
	for _, source := range result.Sources {
		if !strings.HasPrefix(source, "check:") {
			continue
		}
		if _, exists := checks[strings.TrimPrefix(source, "check:")]; !exists {
			return inconclusive(
				"model result cites unknown check evidence",
				evidenceDigest,
				resultDigest,
			)
		}
	}
	return ValidatedDecision{
		Decision:       result.Decision,
		Reason:         result.Summary,
		EvidenceDigest: evidenceDigest,
		ResultDigest:   resultDigest,
		Findings: append(
			[]contract.Finding(nil),
			result.Findings...,
		),
	}, nil
}

func formalReviewBodyBytes(result contract.ReviewResult) int {
	size := len(result.Summary) + 256
	for _, finding := range result.Findings {
		size += len(finding.Title) +
			len(finding.Scenario) +
			len(finding.Impact) +
			len(finding.Resolution) +
			128
	}
	return size
}
