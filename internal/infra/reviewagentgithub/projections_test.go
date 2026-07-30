package reviewagentgithub_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestPublisherProjectsDeterministicMergeConflict(t *testing.T) {
	t.Parallel()

	state := conflictState()
	stateHead := strings.Repeat("f", 40)
	writer := &recordingProjectionWriter{}
	publisher, err := github.NewReviewPublisher(
		"WuKongIM/WuKongIM",
		"wukongim-review-agent",
		"wukongim-review-agent[bot]",
		fixedReviewStateReader{head: stateHead, state: state},
		fixedReviewFactsReader{snapshot: github.PullRequestSnapshot{
			Facts: usecase.PullRequestFacts{
				Repository:   state.Generation.Repository,
				PullRequest:  state.Generation.PullRequest,
				HeadSHA:      state.Generation.HeadSHA,
				BaseSHA:      state.Generation.BaseSHA,
				TestMergeSHA: state.Generation.TestMergeSHA,
				IntentDigest: state.Generation.IntentDigest,
				Open:         true,
			},
		}},
		writer,
	)
	require.NoError(t, err)

	publication, err := publisher.PublishDecision(
		context.Background(),
		github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(11), publication.StatusCommentID)
	require.Equal(t, int64(12), publication.ReviewID)
	require.Equal(t, int64(13), publication.CheckRunID)
	require.Equal(t, usecase.FormalReviewRequestChanges, writer.review)
	require.Equal(t, "completed", writer.checkStatus)
	require.NotNil(t, writer.checkConclusion)
	require.Equal(t, usecase.CheckFailure, *writer.checkConclusion)
}

func TestPublisherUsesOnlyGitHubDiffLinesForInlineFindings(t *testing.T) {
	t.Parallel()

	state := conflictState()
	for _, line := range []uint64{1, 10} {
		state.PriorFindings = append(state.PriorFindings, contract.Finding{
			Kind:       contract.FindingBlocking,
			Dimension:  contract.DimensionIntentCorrectness,
			Title:      "Incorrect behavior",
			Path:       "internal/app/a.go",
			LineStart:  line,
			LineEnd:    line,
			Scenario:   "The changed code returns the wrong value.",
			Impact:     "The pull request does not satisfy its intent.",
			Evidence:   []string{"context:internal/app/a.go"},
			Resolution: "Return the intended value.",
		})
	}
	stateHead := strings.Repeat("f", 40)
	writer := &recordingProjectionWriter{}
	publisher, err := github.NewReviewPublisher(
		"WuKongIM/WuKongIM",
		"wukongim-review-agent",
		"wukongim-review-agent[bot]",
		fixedReviewStateReader{head: stateHead, state: state},
		fixedReviewFactsReader{snapshot: github.PullRequestSnapshot{
			Facts: usecase.PullRequestFacts{
				Repository:   state.Generation.Repository,
				PullRequest:  state.Generation.PullRequest,
				HeadSHA:      state.Generation.HeadSHA,
				BaseSHA:      state.Generation.BaseSHA,
				TestMergeSHA: state.Generation.TestMergeSHA,
				IntentDigest: state.Generation.IntentDigest,
				Open:         true,
			},
			CommentPatches: map[string]string{
				"internal/app/a.go": "@@ -1 +1 @@\n-old\n+new\n",
			},
		}},
		writer,
	)
	require.NoError(t, err)

	_, err = publisher.PublishDecision(
		context.Background(),
		github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
		},
	)
	require.NoError(t, err)
	require.Len(t, writer.inline, 1)
	require.Equal(t, 1, writer.inline[0].Line)
}

func TestPublisherRepairsPersistedExplanationFromLifecycleState(t *testing.T) {
	t.Parallel()

	state := conflictState()
	reply := "The merge conflict prevents a trustworthy merged-tree review."
	explanationDigest, err := contract.ExplanationResultDigest(
		contract.ExplanationResult{
			SchemaVersion: 1,
			Generation:    state.Generation,
			Reply:         reply,
		},
	)
	require.NoError(t, err)
	state.ExplanationDigest = explanationDigest
	state.ExplanationReply = reply
	stateHead := strings.Repeat("f", 40)
	writer := &recordingProjectionWriter{}
	publisher, err := github.NewReviewPublisher(
		"WuKongIM/WuKongIM",
		"wukongim-review-agent",
		"wukongim-review-agent[bot]",
		fixedReviewStateReader{head: stateHead, state: state},
		fixedReviewFactsReader{snapshot: github.PullRequestSnapshot{
			Facts: usecase.PullRequestFacts{
				Repository:   state.Generation.Repository,
				PullRequest:  state.Generation.PullRequest,
				HeadSHA:      state.Generation.HeadSHA,
				BaseSHA:      state.Generation.BaseSHA,
				TestMergeSHA: state.Generation.TestMergeSHA,
				IntentDigest: state.Generation.IntentDigest,
				Open:         true,
			},
		}},
		writer,
	)
	require.NoError(t, err)

	publication, err := publisher.PublishDecision(
		context.Background(),
		github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
		},
	)
	require.NoError(t, err)
	require.NotZero(t, publication.ExplanationCommentID)
	require.Condition(t, func() bool {
		marker := "<!-- review-agent-explanation:" +
			explanationDigest + " -->"
		for _, body := range writer.issueCommentBodies {
			if strings.Contains(body, marker) &&
				strings.Contains(body, reply) {
				return true
			}
		}
		return false
	})
}

type fixedReviewStateReader struct {
	head  string
	state contract.ReviewState
}

func (reader fixedReviewStateReader) Load(
	context.Context,
	int64,
) (github.LoadedReviewState, bool, error) {
	return github.LoadedReviewState{
		HeadSHA: reader.head,
		State:   reader.state,
	}, true, nil
}

type fixedReviewFactsReader struct {
	snapshot github.PullRequestSnapshot
}

func (reader fixedReviewFactsReader) ReadPullRequestMetadata(
	context.Context,
	int64,
) (github.PullRequestSnapshot, error) {
	return reader.snapshot, nil
}

type recordingProjectionWriter struct {
	review             usecase.FormalReview
	inline             []github.InlineReviewComment
	checkStatus        string
	checkConclusion    *usecase.CheckConclusion
	issueCommentBodies []string
}

func (writer *recordingProjectionWriter) InstallationAppSlug(
	context.Context,
) (string, error) {
	return "wukongim-review-agent", nil
}

func (writer *recordingProjectionWriter) CreateIssueComment(
	_ context.Context,
	_ int64,
	body string,
) (int64, error) {
	writer.issueCommentBodies = append(writer.issueCommentBodies, body)
	return 11, nil
}

func (writer *recordingProjectionWriter) UpdateIssueComment(
	context.Context,
	int64,
	string,
) error {
	return nil
}

func (writer *recordingProjectionWriter) CreateReview(
	_ context.Context,
	_ int64,
	_ string,
	review usecase.FormalReview,
	_ string,
	inline []github.InlineReviewComment,
) (int64, error) {
	writer.review = review
	writer.inline = append([]github.InlineReviewComment(nil), inline...)
	return 12, nil
}

func (writer *recordingProjectionWriter) CreateCheckRun(
	context.Context,
	string,
	string,
	usecase.CheckConclusion,
	string,
	string,
) (int64, error) {
	return 0, nil
}

func (writer *recordingProjectionWriter) UpdateCheckRun(
	context.Context,
	int64,
	usecase.CheckConclusion,
	string,
	string,
) error {
	return nil
}

func (writer *recordingProjectionWriter) CreateLifecycleCheckRun(
	_ context.Context,
	_ string,
	_ string,
	status string,
	conclusion *usecase.CheckConclusion,
	_ string,
	_ string,
) (int64, error) {
	writer.checkStatus = status
	writer.checkConclusion = conclusion
	return 13, nil
}

func (writer *recordingProjectionWriter) UpdateLifecycleCheckRun(
	context.Context,
	int64,
	string,
	*usecase.CheckConclusion,
	string,
	string,
) error {
	return nil
}

func conflictState() contract.ReviewState {
	return contract.ReviewState{
		SchemaVersion: 1,
		Generation: contract.GenerationIdentity{
			Repository:     "WuKongIM/WuKongIM",
			PullRequest:    42,
			HeadSHA:        strings.Repeat("a", 40),
			BaseSHA:        strings.Repeat("b", 40),
			TestMergeSHA:   strings.Repeat("c", 40),
			IntentDigest:   "sha256:" + strings.Repeat("d", 64),
			Generation:     1,
			StateParentSHA: strings.Repeat("e", 40),
		},
		Sequence:       1,
		Phase:          contract.PhaseChangesRequired,
		DecisionSource: contract.DecisionSourceMergeConflict,
		Reason:         "pull request has merge conflicts",
		StartedAt:      time.Date(2026, 7, 30, 7, 55, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
	}
}
