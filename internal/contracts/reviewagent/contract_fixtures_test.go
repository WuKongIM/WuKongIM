package reviewagent_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

func validFinding() reviewagent.Finding {
	return reviewagent.Finding{
		Kind:       reviewagent.FindingBlocking,
		Dimension:  reviewagent.DimensionRegressionTests,
		Title:      "retry can duplicate a delivery",
		Path:       "internal/runtime/delivery/queue.go",
		LineStart:  81,
		LineEnd:    84,
		Scenario:   "the retry is accepted after the first write succeeds",
		Impact:     "a recipient observes the same message twice",
		Evidence:   []string{"check:go-unit", "path:queue.go:81"},
		Resolution: "deduplicate before publishing the retry",
	}
}

func validContext(t *testing.T) reviewagent.ReviewContext {
	t.Helper()

	finding := validFinding()
	findingDigest, err := reviewagent.FindingDigest(finding)
	require.NoError(t, err)
	return reviewagent.ReviewContext{
		SchemaVersion:      1,
		Generation:         validGeneration(),
		PolicyDigest:       digest("1"),
		PromptDigest:       digest("2"),
		OutputSchemaDigest: digest("3"),
		ReviewReason:       "head changed",
		Title:              "Prevent duplicate delivery",
		Body:               "Keep retry handling idempotent.",
		LinkedIssues: []reviewagent.LinkedIssue{{
			Number: 7,
			State:  "open",
			Title:  "Duplicate message after retry",
			Body:   "A successful write can be retried.",
		}},
		ReviewThreads: []reviewagent.ReviewThreadContext{{
			ID:   "PRRT_kwDO-thread-1",
			Path: "internal/runtime/delivery/queue.go",
			Line: 81,
		}},
		Discussion: []reviewagent.DiscussionItem{
			{
				Kind:       reviewagent.DiscussionFormalReview,
				ID:         11,
				Author:     "maintainer",
				AuthorType: "User",
				Body:       "Please add a regression test.",
				State:      "CHANGES_REQUESTED",
				CommitSHA:  validGeneration().HeadSHA,
			},
			{
				Kind:       reviewagent.DiscussionIssueComment,
				ID:         12,
				Author:     "reporter",
				AuthorType: "User",
				Body:       "The reproducer is deterministic.",
			},
			{
				Kind:        reviewagent.DiscussionReviewComment,
				ID:          13,
				Author:      "reviewer",
				AuthorType:  "User",
				Body:        "This write needs an idempotency guard.",
				Path:        "internal/runtime/delivery/queue.go",
				Line:        81,
				Side:        "RIGHT",
				InReplyToID: 0,
			},
		},
		PriorFindings: []reviewagent.PriorFindingContext{{
			Digest:  findingDigest,
			Finding: finding,
		}},
		ChangedFiles: []reviewagent.ChangedFile{
			{
				Path:          "internal/runtime/delivery/queue.go",
				Status:        reviewagent.FileStatusModified,
				Mode:          "100644",
				Type:          "text",
				Patch:         "@@ -81 +81 @@\n-old\n+new",
				PatchDigest:   contentDigest("@@ -81 +81 @@\n-old\n+new"),
				Content:       "package delivery\n",
				ContentDigest: contentDigest("package delivery\n"),
				Additions:     1,
				Deletions:     1,
			},
			{
				Path:          "resources/fixture.bin",
				Status:        reviewagent.FileStatusAdded,
				Mode:          "100644",
				Type:          "binary",
				PatchDigest:   contentDigest(""),
				ContentDigest: contentDigest(""),
			},
		},
		ContextDocuments: []reviewagent.ContextDocumentBlob{{
			Path:       "AGENTS.md",
			Scope:      ".",
			BlobSHA:    validGeneration().BaseSHA,
			BlobDigest: digest("4"),
			Content:    "Repository instructions.",
		}},
		MandatoryChecks: []string{"go-unit", "agent-artifact-contracts"},
	}
}

func validEvidence() reviewagent.ReviewEvidence {
	return reviewagent.ReviewEvidence{
		SchemaVersion: 1,
		Generation:    validGeneration(),
		Complete:      true,
		Checks: []reviewagent.CheckEvidence{
			{
				Name:          "go-unit",
				CommandDigest: digest("1"),
				Outcome:       reviewagent.CheckOutcomePassed,
				ExitCode:      0,
				DurationMS:    25,
				StdoutDigest:  digest("2"),
				StderrDigest:  digest("3"),
				Stdout:        "ok",
			},
			{
				Name:          "lint",
				CommandDigest: digest("4"),
				Outcome:       reviewagent.CheckOutcomeFailed,
				ExitCode:      1,
				DurationMS:    10,
				StdoutDigest:  digest("5"),
				StderrDigest:  digest("6"),
				Stderr:        "lint finding",
			},
			{
				Name:          "policy",
				CommandDigest: digest("7"),
				Outcome:       reviewagent.CheckOutcomeError,
				ExitCode:      -1,
				DurationMS:    5,
				StdoutDigest:  digest("8"),
				StderrDigest:  digest("9"),
			},
		},
		CreatedAt: time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
	}
}

func validReviewingState() reviewagent.ReviewState {
	return reviewagent.ReviewState{
		SchemaVersion: 1,
		Generation:    validGeneration(),
		Sequence:      1,
		Phase:         reviewagent.PhaseReviewing,
		Reason:        "review lease acquired",
		Budget: reviewagent.InteractionBudget{
			AutomaticReviewsUsed:      1,
			InfrastructureRetriesUsed: 1,
			ResponseBytesUsed:         2048,
		},
		StartedAt:         time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
		SessionDeadlineAt: time.Date(2026, 8, 1, 8, 30, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 8, 1, 8, 1, 0, 0, time.UTC),
	}
}
