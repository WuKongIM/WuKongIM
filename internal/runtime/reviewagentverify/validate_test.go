package reviewagentverify_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

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
			PatchDigest: digest("4"), ContentDigest: digest("5"),
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
