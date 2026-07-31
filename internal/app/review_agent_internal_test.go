package app

import (
	"context"
	"strings"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	reviewagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
	"github.com/stretchr/testify/require"
)

func TestResolveReviewCommandIgnoresOrdinaryStatusComment(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	command, found, err := resolveReviewCommand(
		context.Background(),
		nil,
		reviewagentgithub.PullRequestSnapshot{
			Author: "pull-request-author",
			IssueComments: []reviewagentgithub.IssueComment{{
				ID: 7, Author: "review-agent[bot]",
				Body:      "Review Agent is reviewing this pull request.",
				CreatedAt: now, UpdatedAt: now,
			}},
		},
		7,
	)
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, command)
}

func TestResolveReviewCommandIgnoresMalformedCommandPrefix(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	_, found, err := resolveReviewCommand(
		context.Background(),
		nil,
		reviewagentgithub.PullRequestSnapshot{
			Author: "pull-request-author",
			IssueComments: []reviewagentgithub.IssueComment{{
				ID: 7, Author: "pull-request-author",
				Body:      "@review-agent",
				CreatedAt: now, UpdatedAt: now,
			}},
		},
		7,
	)
	require.NoError(t, err)
	require.False(t, found)
}

func TestReviewDiscussionPreservesEveryGitHubSurface(t *testing.T) {
	t.Parallel()

	headSHA := strings.Repeat("a", 40)
	discussion := reviewDiscussion(reviewagentgithub.PullRequestSnapshot{
		Reviews: []reviewagentgithub.Review{{
			ID: 1, Author: "review-agent[bot]", AuthorType: "Bot",
			State: "CHANGES_REQUESTED", Body: "Fix the race.",
			CommitID: headSHA,
		}},
		IssueComments: []reviewagentgithub.IssueComment{{
			ID: 2, Author: "alice", AuthorType: "User",
			Body: "@review-agent reconsider fixed",
		}},
		ReviewComments: []reviewagentgithub.ReviewComment{{
			ID: 3, Author: "review-agent[bot]", AuthorType: "Bot",
			Body: "The queue is unsynchronized.", Path: "queue.go",
			Line: 7, Side: "RIGHT", InReplyToID: 0,
		}},
	})

	require.Equal(t, []contract.DiscussionItem{
		{
			Kind: contract.DiscussionFormalReview, ID: 1,
			Author: "review-agent[bot]", AuthorType: "Bot",
			Body: "Fix the race.", State: "CHANGES_REQUESTED",
			CommitSHA: headSHA,
		},
		{
			Kind: contract.DiscussionIssueComment, ID: 2,
			Author: "alice", AuthorType: "User",
			Body: "@review-agent reconsider fixed",
		},
		{
			Kind: contract.DiscussionReviewComment, ID: 3,
			Author: "review-agent[bot]", AuthorType: "Bot",
			Body: "The queue is unsynchronized.", Path: "queue.go",
			Line: 7, Side: "RIGHT", InReplyToID: 0,
		},
	}, discussion)
}

func TestSchedulerDigestChangedIgnoresEmptyCollections(t *testing.T) {
	t.Parallel()

	left := reviewagent.SchedulerState{
		SchemaVersion: 1,
		SourceSHA:     strings.Repeat("a", 40),
		Sequence:      1,
		UpdatedAt:     time.Date(2026, 7, 31, 4, 0, 0, 0, time.UTC),
	}
	right := left
	right.Queue = []reviewagent.QueueEntry{}
	right.Active = []reviewagent.Lease{}

	require.False(t, schedulerDigestChanged(
		left,
		right,
		reviewagent.SchedulerLimits{
			MaxActive: 3, MaxPerPullRequest: 1,
			MaxFirstTimeExternal: 1,
		},
	))
}
