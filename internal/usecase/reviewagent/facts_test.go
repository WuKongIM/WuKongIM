package reviewagent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestHumanChangesRequestedUsesLatestExactHeadHumanReview(
	t *testing.T,
) {
	t.Parallel()

	head := strings.Repeat("a", 40)
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	requested := reviewagent.HumanChangesRequested(
		[]reviewagent.ReviewFact{
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
		head,
	)

	require.False(t, requested)
}

func TestHumanChangesRequestedIgnoresBotsAndOtherHeads(t *testing.T) {
	t.Parallel()

	head := strings.Repeat("a", 40)
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	requested := reviewagent.HumanChangesRequested(
		[]reviewagent.ReviewFact{
			{
				Author: "bot", AuthorType: "Bot",
				State: "CHANGES_REQUESTED", CommitSHA: head,
				SubmittedAt: now,
			},
			{
				Author: "reviewer", AuthorType: "User",
				State: "CHANGES_REQUESTED", CommitSHA: strings.Repeat("b", 40),
				SubmittedAt: now,
			},
		},
		head,
	)

	require.False(t, requested)
}
