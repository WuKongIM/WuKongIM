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

func TestPublisherRejectsDetachedDecisionPayloadsBeforeProjection(t *testing.T) {
	t.Parallel()

	stateHead := strings.Repeat("f", 40)
	otherHead := strings.Repeat("9", 40)

	t.Run("publisher repository", func(t *testing.T) {
		state := conflictState()
		state.Generation.Repository = "other/repository"
		publisher, writer := publicationBoundaryPublisher(
			t,
			stateHead,
			state,
			publicationSnapshot(state),
		)

		_, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
		})
		require.EqualError(t, err, "Review publication generation is inconsistent")
		requireNoProjectionWrites(t, writer)
	})

	t.Run("invalid result", func(t *testing.T) {
		state, _ := approvedStateAndResult(t)
		invalid := contract.ReviewResult{}
		publisher, writer := publicationBoundaryPublisher(
			t,
			stateHead,
			state,
			publicationSnapshot(state),
		)

		_, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
			Result:            &invalid,
		})
		require.EqualError(t, err, "unsupported Review result schema version")
		requireNoProjectionWrites(t, writer)
	})

	t.Run("result generation", func(t *testing.T) {
		state, result := approvedStateAndResult(t)
		result.Generation.HeadSHA = otherHead
		publisher, writer := publicationBoundaryPublisher(
			t,
			stateHead,
			state,
			publicationSnapshot(state),
		)

		_, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
			Result:            &result,
		})
		require.EqualError(t, err, "Review publication generation is inconsistent")
		requireNoProjectionWrites(t, writer)
	})

	t.Run("result digest", func(t *testing.T) {
		state, result := approvedStateAndResult(t)
		state.ResultDigest = "sha256:" + strings.Repeat("9", 64)
		publisher, writer := publicationBoundaryPublisher(
			t,
			stateHead,
			state,
			publicationSnapshot(state),
		)

		_, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
			Result:            &result,
		})
		require.EqualError(t, err, "Review publication result digest is inconsistent")
		requireNoProjectionWrites(t, writer)
	})

	t.Run("multiple payloads", func(t *testing.T) {
		state, result := approvedStateAndResult(t)
		explanation := contract.ExplanationResult{
			SchemaVersion: 1,
			Generation:    state.Generation,
			Reply:         "Explain the exact approved generation.",
		}
		publisher, writer := publicationBoundaryPublisher(
			t,
			stateHead,
			state,
			publicationSnapshot(state),
		)

		_, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
			Result:            &result,
			Explanation:       &explanation,
		})
		require.EqualError(t, err, "Review publication contains multiple payloads")
		requireNoProjectionWrites(t, writer)
	})

	t.Run("invalid explanation", func(t *testing.T) {
		state := conflictState()
		invalid := contract.ExplanationResult{}
		publisher, writer := publicationBoundaryPublisher(
			t,
			stateHead,
			state,
			publicationSnapshot(state),
		)

		_, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
			Explanation:       &invalid,
		})
		require.EqualError(t, err, "unsupported Review explanation schema version")
		requireNoProjectionWrites(t, writer)
	})

	t.Run("explanation generation", func(t *testing.T) {
		state, explanation := explanationPublication(t)
		explanation.Generation.HeadSHA = otherHead
		publisher, writer := publicationBoundaryPublisher(
			t,
			stateHead,
			state,
			publicationSnapshot(state),
		)

		_, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
			Explanation:       &explanation,
		})
		require.EqualError(t, err, "Review explanation generation is inconsistent")
		requireNoProjectionWrites(t, writer)
	})

	t.Run("explanation digest", func(t *testing.T) {
		state, explanation := explanationPublication(t)
		state.ExplanationDigest = "sha256:" + strings.Repeat("9", 64)
		publisher, writer := publicationBoundaryPublisher(
			t,
			stateHead,
			state,
			publicationSnapshot(state),
		)

		_, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
			Explanation:       &explanation,
		})
		require.EqualError(t, err, "Review explanation digest is inconsistent")
		requireNoProjectionWrites(t, writer)
	})
}

func TestPublisherFailsClosedBeforeWritingWhenAuthorityChanges(t *testing.T) {
	t.Parallel()

	state := conflictState()
	stateHead := strings.Repeat("f", 40)
	snapshot := publicationSnapshot(state)

	t.Run("signed ref head", func(t *testing.T) {
		writer := &recordingProjectionWriter{}
		publisher, err := github.NewReviewPublisher(
			"WuKongIM/WuKongIM",
			"wukongim-review-agent",
			"wukongim-review-agent[bot]",
			fixedReviewStateReader{head: strings.Repeat("8", 40), state: state},
			fixedReviewFactsReader{snapshot: snapshot},
			writer,
		)
		require.NoError(t, err)

		_, err = publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
		})
		require.EqualError(t, err, "Review publication signed state is stale")
		requireNoProjectionWrites(t, writer)
	})

	t.Run("signed state content", func(t *testing.T) {
		stored := state
		stored.Reason = "the independently read state changed"
		writer := &recordingProjectionWriter{}
		publisher, err := github.NewReviewPublisher(
			"WuKongIM/WuKongIM",
			"wukongim-review-agent",
			"wukongim-review-agent[bot]",
			fixedReviewStateReader{head: stateHead, state: stored},
			fixedReviewFactsReader{snapshot: snapshot},
			writer,
		)
		require.NoError(t, err)

		_, err = publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
		})
		require.EqualError(t, err, "Review publication signed state content changed")
		requireNoProjectionWrites(t, writer)
	})

	t.Run("metadata read", func(t *testing.T) {
		factsErr := errors.New("metadata unavailable")
		writer := &recordingProjectionWriter{}
		publisher, err := github.NewReviewPublisher(
			"WuKongIM/WuKongIM",
			"wukongim-review-agent",
			"wukongim-review-agent[bot]",
			fixedReviewStateReader{head: stateHead, state: state},
			failingPublicationFacts{err: factsErr},
			writer,
		)
		require.NoError(t, err)

		_, err = publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
		})
		require.ErrorIs(t, err, factsErr)
		requireNoProjectionWrites(t, writer)
	})

	t.Run("pull request generation", func(t *testing.T) {
		stale := snapshot
		stale.Facts.HeadSHA = strings.Repeat("8", 40)
		publisher, writer := publicationBoundaryPublisher(t, stateHead, state, stale)

		_, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
			ExpectedStateHead: stateHead,
			State:             state,
		})
		require.EqualError(t, err, "Review publication pull request is stale")
		requireNoProjectionWrites(t, writer)
	})
}

func TestPublisherProjectsOnlyTheExactValidatedExplanation(t *testing.T) {
	t.Parallel()

	state, explanation := explanationPublication(t)
	stateHead := strings.Repeat("f", 40)
	publisher, writer := publicationBoundaryPublisher(
		t,
		stateHead,
		state,
		publicationSnapshot(state),
	)

	publication, err := publisher.PublishDecision(context.Background(), github.ReviewPublicationRequest{
		ExpectedStateHead: stateHead,
		State:             state,
		Explanation:       &explanation,
	})
	require.NoError(t, err)
	require.Equal(t, int64(11), publication.ExplanationCommentID)
	require.Len(t, writer.issueCommentBodies, 1)
	require.Equal(
		t,
		"<!-- review-agent-explanation:"+state.ExplanationDigest+" -->\n\n"+explanation.Reply,
		writer.issueCommentBodies[0],
	)
}

type failingPublicationFacts struct {
	err error
}

func (facts failingPublicationFacts) ReadPullRequestMetadata(
	context.Context,
	int64,
) (github.PullRequestSnapshot, error) {
	return github.PullRequestSnapshot{}, facts.err
}

func (failingPublicationFacts) ActorPermission(
	context.Context,
	string,
) (github.Permission, error) {
	return github.PermissionRead, nil
}

func publicationBoundaryPublisher(
	t *testing.T,
	stateHead string,
	state contract.ReviewState,
	snapshot github.PullRequestSnapshot,
) (*github.ReviewPublisher, *recordingProjectionWriter) {
	t.Helper()
	writer := &recordingProjectionWriter{}
	publisher, err := github.NewReviewPublisher(
		"WuKongIM/WuKongIM",
		"wukongim-review-agent",
		"wukongim-review-agent[bot]",
		fixedReviewStateReader{head: stateHead, state: state},
		fixedReviewFactsReader{snapshot: snapshot},
		writer,
	)
	require.NoError(t, err)
	return publisher, writer
}

func publicationSnapshot(state contract.ReviewState) github.PullRequestSnapshot {
	return github.PullRequestSnapshot{Facts: usecase.PullRequestFacts{
		Repository:        state.Generation.Repository,
		PullRequest:       state.Generation.PullRequest,
		HeadSHA:           state.Generation.HeadSHA,
		BaseSHA:           state.Generation.BaseSHA,
		TestMergeSHA:      state.Generation.TestMergeSHA,
		IntentDigest:      state.Generation.IntentDigest,
		Open:              true,
		Mergeability:      usecase.MergeabilityConflicting,
		AuthorLogin:       "contributor",
		AuthorAssociation: "CONTRIBUTOR",
	}}
}

func explanationPublication(
	t *testing.T,
) (contract.ReviewState, contract.ExplanationResult) {
	t.Helper()
	state := conflictState()
	explanation := contract.ExplanationResult{
		SchemaVersion: 1,
		Generation:    state.Generation,
		Reply:         "The exact signed generation is blocked by a merge conflict.",
	}
	digest, err := contract.ExplanationResultDigest(explanation)
	require.NoError(t, err)
	state.ExplanationDigest = digest
	state.ExplanationReply = explanation.Reply
	return state, explanation
}

func requireNoProjectionWrites(t *testing.T, writer *recordingProjectionWriter) {
	t.Helper()
	require.Empty(t, writer.issueCommentBodies)
	require.Empty(t, writer.reviewBody)
	require.Empty(t, writer.checkStatus)
	require.Empty(t, writer.mergedHead)
}
