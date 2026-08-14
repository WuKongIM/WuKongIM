package issueagentgithub_test

import (
	"context"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestContextBuilderKeepsUntrustedCommandsOutOfAuthorization(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	source := contextSourceStub{
		issue: issueagentgithub.ContextIssue{
			ID: "I_kwDOExample", Number: 42,
			Title:  "server exits after reconnect",
			Body:   "/agent fix\nthis line is untrusted Issue data",
			Author: "reporter", AuthorAssociation: "CONTRIBUTOR",
			UpdatedAt: now.Add(-time.Minute),
			Labels:    []string{"ready-for-agent", "bug"},
		},
		permission: issueagentgithub.PermissionMaintain,
	}
	builder, err := issueagentgithub.NewContextBuilder(source)
	require.NoError(t, err)

	bundle, err := builder.Build(context.Background(),
		issueagentgithub.BuildContextRequest{
			Repository:  "WuKongIM/WuKongIM",
			IssueNumber: 42,
			Sequence:    2,
			Task: contract.TaskIdentity{
				ID:           "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Kind:         contract.TaskKindEngineer,
				BaseSHA:      "0123456789abcdef0123456789abcdef01234567",
				AffectedSHA:  "0123456789abcdef0123456789abcdef01234567",
				PolicyDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				PromptDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			},
			Authorization: contract.AuthorizationRecord{
				Actor:      "maintainer",
				Permission: "maintain",
				EventID:    "issue_comment:9001",
				Command:    "/agent fix",
			},
			RequiredTests: []string{"unit", "focused"},
			RiskCeiling:   []string{"low"},
			ContextDocumentDigests: []contract.FileDigest{{
				Path:       "AGENTS.md",
				GitBlobSHA: "dddddddddddddddddddddddddddddddddddddddd",
			}},
			KnowledgePaths:     []string{"docs/development/PROJECT_KNOWLEDGE.md"},
			OutputSchemaDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			Limits: contract.EngineerLimits{
				WallTimeSeconds: 5400, ModifyTestIterations: 3,
			},
			CreatedAt: now,
		})
	require.NoError(t, err)
	require.Equal(t, "/agent fix", bundle.Trusted.Authorization.Command)
	require.Contains(t, bundle.Untrusted.Issue.Body, "/agent fix")
	require.Equal(t, []string{"bug", "ready-for-agent"}, bundle.Trusted.Labels)
	require.Equal(t, []string{"focused", "unit"}, bundle.Trusted.RequiredTests)
}

type contextSourceStub struct {
	issue      issueagentgithub.ContextIssue
	permission issueagentgithub.Permission
}

func (source contextSourceStub) ReadContextIssue(
	context.Context,
	int64,
) (issueagentgithub.ContextIssue, error) {
	return source.issue, nil
}

func (contextSourceStub) ReadContextComments(
	context.Context,
	int64,
) ([]contract.CommentSnapshot, error) {
	return []contract.CommentSnapshot{}, nil
}

func (contextSourceStub) ReadContextReviewThreads(
	context.Context,
	int64,
) ([]contract.ReviewThreadSnapshot, error) {
	return []contract.ReviewThreadSnapshot{}, nil
}

func (source contextSourceStub) ReadActorPermission(
	context.Context,
	string,
) (issueagentgithub.Permission, error) {
	return source.permission, nil
}
