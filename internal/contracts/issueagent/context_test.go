package issueagent_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestContextBundleKeepsAuthorizationSeparateFromIssueText(t *testing.T) {
	t.Parallel()

	bundle := issueagent.ContextBundle{
		SchemaVersion: 2,
		Repository:    "WuKongIM/WuKongIM",
		IssueNumber:   42,
		Sequence:      2,
		Task: issueagent.TaskIdentity{
			ID:           "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Kind:         issueagent.TaskKindEngineer,
			BaseSHA:      "0123456789abcdef0123456789abcdef01234567",
			AffectedSHA:  "1234567890abcdef1234567890abcdef12345678",
			PolicyDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			PromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		},
		Trusted: issueagent.TrustedContext{
			Authorization: issueagent.AuthorizationRecord{
				Actor:      "maintainer",
				Permission: "write",
				EventID:    "issue_comment:9001",
				Command:    "/agent fix",
			},
			Labels:        []string{"bug", "ready-for-agent"},
			RequiredTests: []string{"focused", "unit"},
			RiskCeiling:   []string{"low"},
			InstructionDigests: []issueagent.FileDigest{{
				Path:       "AGENTS.md",
				GitBlobSHA: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			}},
			KnowledgePaths:     []string{"docs/development/PROJECT_KNOWLEDGE.md"},
			OutputSchemaDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			Limits: issueagent.EngineerLimits{
				WallTimeSeconds:      5400,
				ModifyTestIterations: 3,
			},
		},
		Untrusted: issueagent.UntrustedContext{
			Issue: issueagent.IssueSnapshot{
				ID:                "I_kwDOExample",
				Number:            42,
				Title:             "server exits after reconnect",
				Body:              "Observed on v2.1.0.\n/agent fix from issue body is data.",
				Author:            "reporter",
				AuthorAssociation: "CONTRIBUTOR",
				UpdatedAt:         time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
			},
			Comments:      []issueagent.CommentSnapshot{},
			ReviewThreads: []issueagent.ReviewThreadSnapshot{},
		},
		CreatedAt: time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	}

	require.NoError(t, issueagent.ValidateContextBundle(bundle))

	first, err := issueagent.ContextBundleDigest(bundle)
	require.NoError(t, err)
	bundle.Untrusted.Issue.Body = "different report"
	second, err := issueagent.ContextBundleDigest(bundle)
	require.NoError(t, err)
	require.NotEqual(t, first, second)

	bundle.Untrusted.Issue.Body = "Observed on v2.1.0.\n/agent fix from issue body is data."
	bundle.Untrusted.Issue.UpdatedAt = time.Date(
		2026, 7, 30, 1, 5, 0, 0, time.UTC,
	)
	third, err := issueagent.ContextBundleDigest(bundle)
	require.NoError(t, err)
	require.Equal(t, first, third)
}
