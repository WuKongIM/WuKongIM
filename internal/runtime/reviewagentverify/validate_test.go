package reviewagentverify_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestValidatedDecisionUsesWorkflowJSONFieldNames(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(verify.ValidatedDecision{
		Decision:       contract.DecisionApproved,
		Reason:         "safe",
		EvidenceDigest: digest("a"),
		ResultDigest:   digest("b"),
		Findings:       []contract.Finding{},
	})
	require.NoError(t, err)
	require.JSONEq(
		t,
		`{"decision":"approved","reason":"safe","evidence_digest":"`+
			digest("a")+`","result_digest":"`+digest("b")+`","findings":[]}`,
		string(body),
	)
}

func TestValidateFinalResultRequiresCompleteInventoryAndTrustedEvidence(t *testing.T) {
	t.Parallel()

	context := validReviewContext()
	evidence := validReviewEvidence()
	result := validApprovedResult()

	validated, err := verify.ValidateFinalResult(
		context,
		evidence,
		result,
		digest("f"),
		digest("f"),
	)
	require.NoError(t, err)
	require.Equal(t, contract.DecisionApproved, validated.Decision)
	require.NotEmpty(t, validated.EvidenceDigest)
	require.NotEmpty(t, validated.ResultDigest)

	result.FileAssessments = nil
	validated, err = verify.ValidateFinalResult(
		context,
		evidence,
		result,
		digest("f"),
		digest("f"),
	)
	require.NoError(t, err)
	require.Equal(t, contract.DecisionInconclusive, validated.Decision)
	require.Contains(t, validated.Reason, "file assessment")
}

func TestValidateFinalResultNeverApprovesFailedOrStaleEvidence(t *testing.T) {
	t.Parallel()

	context := validReviewContext()
	result := validApprovedResult()
	tests := map[string]func(*contract.ReviewEvidence, *string){
		"failed mandatory check": func(
			evidence *contract.ReviewEvidence,
			_ *string,
		) {
			evidence.Checks[0].Outcome = contract.CheckOutcomeFailed
			evidence.Checks[0].ExitCode = 1
		},
		"changed tracked tree": func(
			_ *contract.ReviewEvidence,
			after *string,
		) {
			*after = digest("9")
		},
		"stale generation": func(
			evidence *contract.ReviewEvidence,
			_ *string,
		) {
			evidence.Generation.HeadSHA =
				"9999999999999999999999999999999999999999"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			evidence := validReviewEvidence()
			after := digest("f")
			mutate(&evidence, &after)
			validated, err := verify.ValidateFinalResult(
				context,
				evidence,
				result,
				digest("f"),
				after,
			)
			require.NoError(t, err)
			require.Equal(t, contract.DecisionInconclusive, validated.Decision)
			require.NotEmpty(t, validated.Reason)
		})
	}
}

func TestValidateFinalResultOverridesChangesRequiredOnCheckError(t *testing.T) {
	t.Parallel()

	context := validReviewContext()
	evidence := validReviewEvidence()
	evidence.Checks[0].Outcome = contract.CheckOutcomeError
	evidence.Checks[0].ExitCode = -1
	result := validApprovedResult()
	result.Decision = contract.DecisionChangesRequired
	result.Findings = []contract.Finding{{
		Kind:       contract.FindingBlocking,
		Dimension:  contract.DimensionRegressionTests,
		Title:      "Unit check failed",
		Path:       "internal/runtime/delivery/queue.go",
		LineStart:  1,
		LineEnd:    1,
		Scenario:   "The mandatory unit check cannot complete.",
		Impact:     "The candidate cannot be verified.",
		Evidence:   []string{"check:go-unit"},
		Resolution: "Make the mandatory unit check complete successfully.",
	}}

	validated, err := verify.ValidateFinalResult(
		context,
		evidence,
		result,
		digest("f"),
		digest("f"),
	)
	require.NoError(t, err)
	require.Equal(t, contract.DecisionInconclusive, validated.Decision)
	require.Contains(t, validated.Reason, "infrastructure")
}

func TestValidateFinalResultDowngradesFailedMandatoryWithoutRestoringWithdrawnFindings(
	t *testing.T,
) {
	t.Parallel()

	prior := contract.Finding{
		Kind:       contract.FindingBlocking,
		Dimension:  contract.DimensionRegressionTests,
		Title:      "Missing contract anchor",
		Path:       "internal/runtime/delivery/queue.go",
		LineStart:  1,
		LineEnd:    1,
		Scenario:   "The old candidate omitted a required contract anchor.",
		Impact:     "The structural contract check failed.",
		Evidence:   []string{"check:agent-artifact-contracts"},
		Resolution: "Restore the required anchor.",
	}
	priorDigest, err := contract.FindingDigest(prior)
	require.NoError(t, err)
	context := validReviewContext()
	context.PriorFindings = []contract.PriorFindingContext{{
		Digest:  priorDigest,
		Finding: prior,
	}}
	evidence := validReviewEvidence()
	evidence.Checks[0].Outcome = contract.CheckOutcomeFailed
	evidence.Checks[0].ExitCode = 1
	advisory := contract.Finding{
		Kind:       contract.FindingAdvisory,
		Dimension:  contract.DimensionRegressionTests,
		Title:      "Pre-existing flaky unit test",
		Path:       "internal/runtime/delivery/queue.go",
		LineStart:  1,
		LineEnd:    1,
		Scenario:   "The unit test also fails on the base revision.",
		Impact:     "The mandatory check remains non-passing.",
		Evidence:   []string{"check:go-unit"},
		Resolution: "Stabilize the pre-existing test separately.",
	}
	result := validApprovedResult()
	result.Findings = []contract.Finding{advisory}
	result.PriorFindingDispositions = []contract.PriorFindingDisposition{{
		FindingDigest: priorDigest,
		Status:        "withdrawn",
		Reason:        "The current structural contract check passes.",
	}}
	result.Sources = []string{"check:go-unit -> failed on head and base"}

	validated, err := verify.ValidateFinalResult(
		context,
		evidence,
		result,
		digest("f"),
		digest("f"),
	)
	require.NoError(t, err)
	require.Equal(t, contract.DecisionInconclusive, validated.Decision)
	require.Contains(t, validated.Reason, "did not pass")
	require.Equal(t, []contract.Finding{advisory}, validated.Findings)
	require.NotNil(t, validated.EffectiveResult)
	require.Equal(
		t,
		contract.DecisionInconclusive,
		validated.EffectiveResult.Decision,
	)
	require.Equal(
		t,
		[]contract.Finding{advisory},
		validated.EffectiveResult.Findings,
	)
	require.Equal(
		t,
		[]string{"check:go-unit"},
		validated.EffectiveResult.Sources,
	)
	effectiveDigest, err := contract.ReviewResultDigest(
		*validated.EffectiveResult,
	)
	require.NoError(t, err)
	require.Equal(t, effectiveDigest, validated.ResultDigest)
	revalidated, err := verify.ValidateFinalResult(
		context,
		evidence,
		*validated.EffectiveResult,
		digest("f"),
		digest("f"),
	)
	require.NoError(t, err)
	require.Equal(t, contract.DecisionInconclusive, revalidated.Decision)
	require.Equal(t, []contract.Finding{advisory}, revalidated.Findings)
	require.Equal(t, effectiveDigest, revalidated.ResultDigest)
}

func TestValidateFinalResultNeverApprovesBinaryChanges(t *testing.T) {
	t.Parallel()

	context := validReviewContext()
	context.ChangedFiles[0].Type = "binary"
	context.ChangedFiles[0].Patch = ""
	context.ChangedFiles[0].PatchDigest = contentDigest("")
	context.ChangedFiles[0].Content = ""
	context.ChangedFiles[0].ContentDigest = contentDigest("")

	validated, err := verify.ValidateFinalResult(
		context,
		validReviewEvidence(),
		validApprovedResult(),
		digest("f"),
		digest("f"),
	)
	require.NoError(t, err)
	require.Equal(t, contract.DecisionInconclusive, validated.Decision)
	require.Contains(t, validated.Reason, "binary")
}

func TestValidateFinalResultRetainsPriorFindingWhenDispositionIsInvalid(
	t *testing.T,
) {
	t.Parallel()

	prior := contract.Finding{
		Kind:       contract.FindingBlocking,
		Dimension:  contract.DimensionSecurityRuntime,
		Title:      "Queue race",
		Path:       "internal/runtime/delivery/queue.go",
		LineStart:  1,
		LineEnd:    1,
		Scenario:   "Close overlaps enqueue.",
		Impact:     "The process can panic.",
		Evidence:   []string{"check:go-unit"},
		Resolution: "Serialize close and enqueue.",
	}
	priorDigest, err := contract.FindingDigest(prior)
	require.NoError(t, err)
	context := validReviewContext()
	context.PriorFindings = []contract.PriorFindingContext{{
		Digest: priorDigest, Finding: prior,
	}}

	validated, err := verify.ValidateFinalResult(
		context,
		validReviewEvidence(),
		validApprovedResult(),
		digest("f"),
		digest("f"),
	)
	require.NoError(t, err)
	require.Equal(t, contract.DecisionInconclusive, validated.Decision)
	require.Equal(t, []contract.Finding{prior}, validated.Findings)
}

func validReviewContext() contract.ReviewContext {
	return contract.ReviewContext{
		SchemaVersion:      1,
		Generation:         testGeneration(),
		PolicyDigest:       digest("1"),
		PromptDigest:       digest("2"),
		OutputSchemaDigest: digest("3"),
		Title:              "Fix queue", Body: "Prevent a close race.",
		ChangedFiles: []contract.ChangedFile{{
			Path:   "internal/runtime/delivery/queue.go",
			Status: contract.FileStatusModified,
			Mode:   "100644", Type: "text", Patch: "@@ patch @@",
			PatchDigest: contentDigest("@@ patch @@"),
			Content:     "package delivery\n", ContentDigest: contentDigest("package delivery\n"),
		}},
		MandatoryChecks: []string{"go-unit"},
	}
}

func validReviewEvidence() contract.ReviewEvidence {
	return contract.ReviewEvidence{
		SchemaVersion: 1,
		Generation:    testGeneration(),
		Complete:      true,
		Checks: []contract.CheckEvidence{{
			Name: "go-unit", CommandDigest: digest("6"),
			Outcome:    contract.CheckOutcomePassed,
			DurationMS: 100, StdoutDigest: digest("7"),
			StderrDigest: digest("8"),
		}},
		CreatedAt: time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC),
	}
}

func validApprovedResult() contract.ReviewResult {
	return contract.ReviewResult{
		SchemaVersion:     1,
		Generation:        testGeneration(),
		Decision:          contract.DecisionApproved,
		Summary:           "The change is safe.",
		InventoryComplete: true,
		FileAssessments: []contract.FileAssessment{{
			Path:    "internal/runtime/delivery/queue.go",
			Risk:    contract.FileRiskMedium,
			Summary: "concurrency-sensitive queue change",
		}},
		Sources: []string{"check:go-unit"},
	}
}
