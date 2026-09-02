package reviewagentgithub_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
	"github.com/stretchr/testify/require"
)

func TestPublisherRepairsExistingGenerationWithoutDuplicateProjections(t *testing.T) {
	t.Parallel()

	state, result := approvedStateAndResult(t)
	stateHead := strings.Repeat("f", 40)
	generationDigest := contract.MustGenerationDigest(state.Generation)
	botLogin := "wukongim-review-agent[bot]"
	snapshot := github.PullRequestSnapshot{
		Facts: usecase.PullRequestFacts{
			Repository:   state.Generation.Repository,
			PullRequest:  state.Generation.PullRequest,
			HeadSHA:      state.Generation.HeadSHA,
			BaseSHA:      state.Generation.BaseSHA,
			TestMergeSHA: state.Generation.TestMergeSHA,
			IntentDigest: state.Generation.IntentDigest,
			Open:         true, Mergeability: usecase.MergeabilityClean,
			AuthorLogin: "external", AuthorAssociation: "CONTRIBUTOR",
		},
		Author: "external",
		IssueComments: []github.IssueComment{{
			ID: 201, Author: botLogin,
			Body: "<!-- review-agent-status:pr-42 -->\n\nold status",
		}},
		Reviews: []github.Review{{
			ID: 202, Author: botLogin, CommitID: state.Generation.HeadSHA,
			Body: "<!-- review-agent-review:" + generationDigest + " -->\n\nold review",
		}},
		Checks: []github.CheckRun{{
			ID: 203, Name: "Review Agent Verdict",
			ExternalID: "review-agent/" + generationDigest,
			AppSlug:    "wukongim-review-agent",
		}},
	}
	writer := &idempotencyProjectionWriter{}
	publisher, err := github.NewReviewPublisher(
		"WuKongIM/WuKongIM",
		"wukongim-review-agent",
		botLogin,
		fixedReviewStateReader{head: stateHead, state: state},
		fixedReviewFactsReader{
			snapshot: snapshot, permission: github.PermissionRead,
		},
		writer,
	)
	require.NoError(t, err)

	for range 2 {
		publication, publishErr := publisher.PublishDecision(
			context.Background(),
			github.ReviewPublicationRequest{
				ExpectedStateHead: stateHead, State: state, Result: &result,
			},
		)
		require.NoError(t, publishErr)
		require.Equal(t, github.ReviewPublication{
			StatusCommentID: 201, ReviewID: 202, CheckRunID: 203,
		}, publication)
	}

	require.Equal(t, []int64{201, 201}, writer.statusUpdates)
	require.Equal(t, []int64{203, 203}, writer.checkUpdates)
	require.Contains(t, writer.latestStatusBody, "<!-- review-agent-status:pr-42 -->")
	require.Equal(t, usecase.CheckSuccess, writer.latestConclusion)
	require.Zero(t, writer.creates, "retries must reuse the App-owned identities")
	require.Zero(t, writer.merges, "an untrusted contributor remains human-merge only")
}

type idempotencyProjectionWriter struct {
	creates          int
	merges           int
	statusUpdates    []int64
	checkUpdates     []int64
	latestStatusBody string
	latestConclusion usecase.CheckConclusion
}

func (writer *idempotencyProjectionWriter) CreateIssueComment(
	context.Context,
	int64,
	string,
) (int64, error) {
	writer.creates++
	return 0, errors.New("unexpected duplicate status comment")
}

func (writer *idempotencyProjectionWriter) UpdateIssueComment(
	_ context.Context,
	id int64,
	body string,
) error {
	writer.statusUpdates = append(writer.statusUpdates, id)
	writer.latestStatusBody = body
	return nil
}

func (writer *idempotencyProjectionWriter) CreateReview(
	context.Context,
	int64,
	string,
	usecase.FormalReview,
	string,
	[]github.InlineReviewComment,
) (int64, error) {
	writer.creates++
	return 0, errors.New("unexpected duplicate formal Review")
}

func (writer *idempotencyProjectionWriter) CreateCheckRun(
	context.Context,
	string,
	string,
	usecase.CheckConclusion,
	string,
	string,
) (int64, error) {
	writer.creates++
	return 0, errors.New("unexpected duplicate Check Run")
}

func (writer *idempotencyProjectionWriter) UpdateCheckRun(
	_ context.Context,
	id int64,
	conclusion usecase.CheckConclusion,
	_ string,
	_ string,
) error {
	writer.checkUpdates = append(writer.checkUpdates, id)
	writer.latestConclusion = conclusion
	return nil
}

func (writer *idempotencyProjectionWriter) CreateLifecycleCheckRun(
	context.Context,
	string,
	string,
	string,
	*usecase.CheckConclusion,
	string,
	string,
) (int64, error) {
	writer.creates++
	return 0, errors.New("unexpected duplicate lifecycle Check Run")
}

func (writer *idempotencyProjectionWriter) UpdateLifecycleCheckRun(
	context.Context,
	int64,
	string,
	*usecase.CheckConclusion,
	string,
	string,
) error {
	return errors.New("unexpected lifecycle Check update")
}

func (writer *idempotencyProjectionWriter) MergePullRequest(
	context.Context,
	int64,
	string,
) error {
	writer.merges++
	return errors.New("unexpected automatic merge")
}
