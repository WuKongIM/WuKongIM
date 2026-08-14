package reviewagent_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

func TestReviewDocumentsShareOneGenerationIdentity(t *testing.T) {
	t.Parallel()

	generation := validGeneration()
	context := reviewagent.ReviewContext{
		SchemaVersion:      1,
		Generation:         generation,
		PolicyDigest:       digest("1"),
		PromptDigest:       digest("2"),
		OutputSchemaDigest: digest("3"),
		Title:              "Fix delivery race",
		Body:               "Preserve ordering.",
		ChangedFiles: []reviewagent.ChangedFile{{
			Path:          "internal/runtime/delivery/queue.go",
			Status:        reviewagent.FileStatusModified,
			Mode:          "100644",
			Type:          "text",
			Patch:         "@@ -1 +1 @@",
			PatchDigest:   contentDigest("@@ -1 +1 @@"),
			Content:       "package delivery\n",
			ContentDigest: contentDigest("package delivery\n"),
			Additions:     7,
			Deletions:     2,
		}},
		ContextDocuments: []reviewagent.ContextDocumentBlob{{
			Path:       "AGENTS.md",
			Scope:      ".",
			BlobSHA:    strings.Repeat("f", 40),
			BlobDigest: digest("5"),
			Content:    "Repository instructions.",
		}},
		MandatoryChecks: []string{"go-unit"},
	}
	evidence := reviewagent.ReviewEvidence{
		SchemaVersion: 1,
		Generation:    generation,
		Complete:      true,
		Checks: []reviewagent.CheckEvidence{{
			Name:          "go-unit",
			CommandDigest: digest("6"),
			Outcome:       reviewagent.CheckOutcomePassed,
			ExitCode:      0,
			DurationMS:    1200,
			StdoutDigest:  digest("7"),
			StderrDigest:  digest("8"),
		}},
		CreatedAt: time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC),
	}
	state := reviewagent.ReviewState{
		SchemaVersion:       1,
		Generation:          generation,
		Sequence:            1,
		Phase:               reviewagent.PhaseReviewing,
		Reason:              "lease acquired",
		PreviousStateDigest: "",
		EvidenceDigest:      "",
		ResultDigest:        "",
		StartedAt:           time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC),
		UpdatedAt:           time.Date(2026, 7, 30, 2, 1, 0, 0, time.UTC),
	}

	require.NoError(t, reviewagent.ValidateReviewContext(context))
	contextJSON, err := json.Marshal(context)
	require.NoError(t, err)
	require.Contains(t, string(contextJSON), `"context_documents"`)
	require.NotContains(t, string(contextJSON), `"instructions"`)
	require.NoError(t, reviewagent.ValidateReviewEvidence(evidence))
	require.NoError(t, reviewagent.ValidateReviewState(state))

	contextDigest, err := reviewagent.ReviewContextDigest(context)
	require.NoError(t, err)
	evidenceDigest, err := reviewagent.ReviewEvidenceDigest(evidence)
	require.NoError(t, err)
	stateDigest, err := reviewagent.ReviewStateDigest(state)
	require.NoError(t, err)
	require.NotEqual(t, contextDigest, evidenceDigest)
	require.NotEqual(t, evidenceDigest, stateDigest)

	evidence.Generation.HeadSHA = strings.Repeat("9", 40)
	require.NoError(t, reviewagent.ValidateReviewEvidence(evidence))
	require.NotEqual(
		t,
		reviewagent.MustGenerationDigest(generation),
		reviewagent.MustGenerationDigest(evidence.Generation),
	)
}

func TestStateCanonicalEncodingRejectsInvalidSuccessor(t *testing.T) {
	t.Parallel()

	state := reviewagent.ReviewState{
		SchemaVersion:  1,
		Generation:     validGeneration(),
		Sequence:       2,
		Phase:          reviewagent.PhaseApproved,
		DecisionSource: reviewagent.DecisionSourceModel,
		Reason:         "review complete",
		EvidenceDigest: digest("a"),
		ResultDigest:   digest("b"),
		StartedAt: time.Date(
			2026, 7, 30, 2, 55, 0, 0, time.UTC,
		),
		UpdatedAt: time.Date(
			2026, 7, 30, 3, 0, 0, 0, time.UTC,
		),
	}
	_, err := reviewagent.CanonicalReviewState(state)
	require.EqualError(
		t,
		err,
		"successor Review state lacks a predecessor digest",
	)
}

func TestStateRejectsArtifactFreeChangesRequiredWithoutConflictSource(
	t *testing.T,
) {
	t.Parallel()

	state := reviewagent.ReviewState{
		SchemaVersion:  1,
		Generation:     validGeneration(),
		Sequence:       1,
		Phase:          reviewagent.PhaseChangesRequired,
		DecisionSource: reviewagent.DecisionSourceModel,
		Reason:         "untrusted deterministic rejection",
		StartedAt: time.Date(
			2026, 7, 30, 2, 55, 0, 0, time.UTC,
		),
		UpdatedAt: time.Date(
			2026, 7, 30, 3, 0, 0, 0, time.UTC,
		),
	}
	err := reviewagent.ValidateReviewState(state)
	require.EqualError(t, err, "model Review decision lacks evidence or result")

	state.DecisionSource = reviewagent.DecisionSourceMergeConflict
	require.NoError(t, reviewagent.ValidateReviewState(state))
}

func digest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func contentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}
