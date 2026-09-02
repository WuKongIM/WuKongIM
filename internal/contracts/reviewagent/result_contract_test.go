package reviewagent_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

func TestReviewResultDigestBindsAdvisoryDecision(t *testing.T) {
	t.Parallel()

	result, err := reviewagent.DecodeReviewResult(
		strings.NewReader(validResultJSON()),
		32<<10,
	)
	require.NoError(t, err)
	first, err := reviewagent.ReviewResultDigest(result)
	require.NoError(t, err)
	second, err := reviewagent.ReviewResultDigest(result)
	require.NoError(t, err)
	require.Equal(t, first, second)

	result.Summary = "the same race remains in a second call site"
	changed, err := reviewagent.ReviewResultDigest(result)
	require.NoError(t, err)
	require.NotEqual(t, first, changed)

	result.InventoryComplete = false
	_, err = reviewagent.ReviewResultDigest(result)
	require.Error(t, err)
}

func TestReviewResultDecisionRequiresConsistentRisk(t *testing.T) {
	t.Parallel()

	base, err := reviewagent.DecodeReviewResult(
		strings.NewReader(validResultJSON()),
		32<<10,
	)
	require.NoError(t, err)

	tests := map[string]func(*reviewagent.ReviewResult){
		"approved advisory": func(result *reviewagent.ReviewResult) {
			result.Decision = reviewagent.DecisionApproved
			result.Findings[0].Kind = reviewagent.FindingAdvisory
		},
		"changes required blocking": func(*reviewagent.ReviewResult) {},
		"inconclusive with uncertainty": func(result *reviewagent.ReviewResult) {
			result.Decision = reviewagent.DecisionInconclusive
			result.UnresolvedUncertainty = "the external contract is unavailable"
		},
	}
	for name, configure := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := base
			result.Findings = append([]reviewagent.Finding(nil), base.Findings...)
			configure(&result)
			require.NoError(t, reviewagent.ValidateReviewResult(result))
		})
	}

	invalid := map[string]func(*reviewagent.ReviewResult){
		"unknown decision": func(result *reviewagent.ReviewResult) {
			result.Decision = "merge"
		},
		"approved blocking": func(result *reviewagent.ReviewResult) {
			result.Decision = reviewagent.DecisionApproved
		},
		"approved uncertainty": func(result *reviewagent.ReviewResult) {
			result.Decision = reviewagent.DecisionApproved
			result.Findings[0].Kind = reviewagent.FindingAdvisory
			result.UnresolvedUncertainty = "unknown"
		},
		"changes required advisory only": func(result *reviewagent.ReviewResult) {
			result.Findings[0].Kind = reviewagent.FindingAdvisory
		},
		"inconclusive without uncertainty": func(result *reviewagent.ReviewResult) {
			result.Decision = reviewagent.DecisionInconclusive
		},
		"duplicate path assessment": func(result *reviewagent.ReviewResult) {
			result.FileAssessments = append(
				result.FileAssessments,
				result.FileAssessments[0],
			)
		},
		"invalid file risk": func(result *reviewagent.ReviewResult) {
			result.FileAssessments[0].Risk = "critical"
		},
		"duplicate source": func(result *reviewagent.ReviewResult) {
			result.Sources = append(result.Sources, result.Sources[0])
		},
		"invalid disposition": func(result *reviewagent.ReviewResult) {
			result.PriorFindingDispositions = []reviewagent.PriorFindingDisposition{{
				FindingDigest: digest("1"),
				Status:        "ignored",
				Reason:        "not reviewed",
			}}
		},
		"duplicate disposition": func(result *reviewagent.ReviewResult) {
			disposition := reviewagent.PriorFindingDisposition{
				FindingDigest: digest("1"),
				Status:        "withdrawn",
				Reason:        "fixed",
			}
			result.PriorFindingDispositions = []reviewagent.PriorFindingDisposition{
				disposition,
				disposition,
			}
		},
	}
	for name, configure := range invalid {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := base
			result.Findings = append([]reviewagent.Finding(nil), base.Findings...)
			result.FileAssessments = append(
				[]reviewagent.FileAssessment(nil),
				base.FileAssessments...,
			)
			result.Sources = append([]string(nil), base.Sources...)
			configure(&result)
			require.Error(t, reviewagent.ValidateReviewResult(result))
		})
	}
}

func TestFindingLocationAndPriorDispositionAreFailClosed(t *testing.T) {
	t.Parallel()

	finding := validFinding()
	require.NoError(t, reviewagent.ValidateFinding(finding))

	unlocated := finding
	unlocated.Path = ""
	unlocated.LineStart = 0
	unlocated.LineEnd = 0
	require.NoError(t, reviewagent.ValidateFinding(unlocated))

	for name, mutate := range map[string]func(*reviewagent.Finding){
		"unlocated line": func(candidate *reviewagent.Finding) {
			candidate.Path = ""
		},
		"zero line": func(candidate *reviewagent.Finding) {
			candidate.LineStart = 0
		},
		"reversed range": func(candidate *reviewagent.Finding) {
			candidate.LineEnd = candidate.LineStart - 1
		},
		"unknown kind": func(candidate *reviewagent.Finding) {
			candidate.Kind = "fatal"
		},
		"unknown dimension": func(candidate *reviewagent.Finding) {
			candidate.Dimension = "performance"
		},
		"duplicate evidence": func(candidate *reviewagent.Finding) {
			candidate.Evidence = []string{"check:go-unit", "check:go-unit"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := finding
			mutate(&candidate)
			require.Error(t, reviewagent.ValidateFinding(candidate))
		})
	}

	priorDigest, err := reviewagent.FindingDigest(finding)
	require.NoError(t, err)
	result := reviewagent.ReviewResult{
		PriorFindingDispositions: []reviewagent.PriorFindingDisposition{{
			FindingDigest: priorDigest,
			Status:        "withdrawn",
			Reason:        "the retry is now deduplicated",
		}},
	}
	require.NoError(
		t,
		reviewagent.ValidatePriorFindingDispositions(
			[]reviewagent.Finding{finding},
			result,
		),
	)

	result.Findings = []reviewagent.Finding{finding}
	require.EqualError(
		t,
		reviewagent.ValidatePriorFindingDispositions(
			[]reviewagent.Finding{finding},
			result,
		),
		"withdrawn prior finding remains in findings",
	)
}
