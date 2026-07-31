package reviewagent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestEvaluateGovernanceAppliesProtectedPathsAndReviewPrecedence(
	t *testing.T,
) {
	t.Parallel()

	head := strings.Repeat("a", 40)
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	facts := reviewagent.EvaluateGovernance(reviewagent.GovernanceInput{
		Files: []contract.ChangedFile{{
			Path:         "docs/new.md",
			PreviousPath: "custom/control/old.json",
		}},
		ControlPlanePrefixes: []string{"custom/control"},
		Reviews: []reviewagent.GovernanceReview{
			{
				Author: "owner", AuthorType: "User", State: "APPROVED",
				CommitSHA: head, SubmittedAt: now,
			},
			{
				Author: "reviewer", AuthorType: "User",
				State:     "CHANGES_REQUESTED",
				CommitSHA: head, SubmittedAt: now,
			},
			{
				Author: "reviewer", AuthorType: "User",
				State:     "APPROVED",
				CommitSHA: head, SubmittedAt: now.Add(time.Minute),
			},
		},
		HeadSHA: head, Author: "alice", OwnerLogins: []string{"OWNER"},
	})

	require.True(t, facts.ControlPlaneChanged)
	require.True(t, facts.OwnerApproved)
	require.False(t, facts.HumanChangesRequested)
}

func TestEvaluateGovernanceRejectsAuthorSelfApproval(t *testing.T) {
	t.Parallel()

	head := strings.Repeat("a", 40)
	facts := reviewagent.EvaluateGovernance(reviewagent.GovernanceInput{
		Files: []contract.ChangedFile{{Path: "docs/ordinary.md"}},
		Reviews: []reviewagent.GovernanceReview{{
			Author: "Alice", AuthorType: "User", State: "APPROVED",
			CommitSHA: head,
			SubmittedAt: time.Date(
				2026, 7, 30, 8, 0, 0, 0, time.UTC,
			),
		}},
		HeadSHA: head, Author: "alice", OwnerLogins: []string{"ALICE"},
	})

	require.False(t, facts.ControlPlaneChanged)
	require.False(t, facts.OwnerApproved)
}
