package reviewagentgithub_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	reviewagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func sha256Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestReviewStateStoreVerifiesWholeChainAndExactTarget(t *testing.T) {
	t.Parallel()

	initial := reviewStateFixture(1)
	next := reviewStateFixture(2)
	digest, err := contract.ReviewStateDigest(initial)
	require.NoError(t, err)
	next.PreviousStateDigest = digest
	next.Phase = contract.PhaseReviewing
	next.UpdatedAt = initial.UpdatedAt.Add(time.Minute)
	initialBody, err := contract.CanonicalReviewState(initial)
	require.NoError(t, err)
	nextBody, err := contract.CanonicalReviewState(next)
	require.NoError(t, err)

	port := &stateCommitStub{
		head: strings.Repeat("c", 40),
		records: map[string]reviewagentgithub.StateCommitRecord{
			strings.Repeat("c", 40): trustedRecord(
				strings.Repeat("c", 40),
				strings.Repeat("b", 40),
				"review(state): pr 42 sequence 2",
				".review-agent-state/pr-42.json",
				nextBody,
			),
			strings.Repeat("b", 40): trustedRecord(
				strings.Repeat("b", 40),
				strings.Repeat("a", 40),
				"review(state): pr 42 sequence 1",
				".review-agent-state/pr-42.json",
				initialBody,
			),
		},
	}
	store, err := reviewagentgithub.NewReviewStateStore(
		"WuKongIM/WuKongIM",
		"wukongim-review-state-writer[bot]",
		port,
	)
	require.NoError(t, err)

	loaded, found, err := store.Load(context.Background(), 42)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, next, loaded.State)
	require.Equal(t, "review-state/pr-42", port.ref)

	port.publishResult = reviewagentgithub.StateCommitResult{
		CommitSHA:   strings.Repeat("d", 40),
		ParentSHA:   port.head,
		Path:        ".review-agent-state/pr-42.json",
		AuthorLogin: "wukongim-review-state-writer[bot]",
		AuthorType:  "Bot", Verified: true, SignedByGitHub: true,
	}
	body, err := contract.CanonicalReviewState(next)
	require.NoError(t, err)
	port.publishResult.ContentDigest = sha256Digest(body)
	published, err := store.Advance(
		context.Background(),
		next,
		port.head,
		true,
	)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("d", 40), published)
	require.Equal(t, "review-state/pr-42", port.request.Branch)
	require.Equal(t, ".review-agent-state/pr-42.json", port.request.Path)
}

func TestReviewStateStoreRejectsUnsignedOrDiscontinuousChain(t *testing.T) {
	t.Parallel()

	state := reviewStateFixture(1)
	body, err := contract.CanonicalReviewState(state)
	require.NoError(t, err)
	head := strings.Repeat("b", 40)
	record := trustedRecord(
		head,
		strings.Repeat("a", 40),
		"review(state): pr 42 sequence 1",
		".review-agent-state/pr-42.json",
		body,
	)
	record.Verified = false
	port := &stateCommitStub{
		head:    head,
		records: map[string]reviewagentgithub.StateCommitRecord{head: record},
	}
	store, err := reviewagentgithub.NewReviewStateStore(
		"WuKongIM/WuKongIM",
		"wukongim-review-state-writer[bot]",
		port,
	)
	require.NoError(t, err)

	_, _, err = store.Load(context.Background(), 42)
	require.EqualError(t, err, "Review state commit is untrusted")
}

func TestReviewStateStoreLoadsHighSequenceFromRollingCheckpoint(t *testing.T) {
	t.Parallel()

	previous := reviewStateFixture(9999)
	previous.PreviousStateDigest = "sha256:" + strings.Repeat("1", 64)
	latest := reviewStateFixture(10000)
	digest, err := contract.ReviewStateDigest(previous)
	require.NoError(t, err)
	latest.PreviousStateDigest = digest
	latest.UpdatedAt = previous.UpdatedAt.Add(time.Minute)
	previousBody, err := contract.CanonicalReviewState(previous)
	require.NoError(t, err)
	latestBody, err := contract.CanonicalReviewState(latest)
	require.NoError(t, err)
	previousCommit := strings.Repeat("b", 40)
	head := strings.Repeat("c", 40)
	port := &stateCommitStub{
		head: head,
		records: map[string]reviewagentgithub.StateCommitRecord{
			head: trustedRecord(
				head,
				previousCommit,
				"review(state): pr 42 sequence 10000",
				".review-agent-state/pr-42.json",
				latestBody,
			),
			previousCommit: trustedRecord(
				previousCommit,
				strings.Repeat("d", 40),
				"review(state): pr 42 sequence 9999",
				".review-agent-state/pr-42.json",
				previousBody,
			),
		},
	}
	store, err := reviewagentgithub.NewReviewStateStore(
		"WuKongIM/WuKongIM",
		"wukongim-review-state-writer[bot]",
		port,
	)
	require.NoError(t, err)

	loaded, found, err := store.Load(context.Background(), 42)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, latest, loaded.State)
	require.Equal(t, 2, port.reads)
}

func TestSchedulerStoreVerifiesSourceAnchor(t *testing.T) {
	t.Parallel()

	limits := usecase.SchedulerLimits{
		MaxActive: 3, MaxPerPullRequest: 1, MaxFirstTimeExternal: 1,
	}
	state := usecase.SchedulerState{
		SchemaVersion: 1,
		SourceSHA:     strings.Repeat("a", 40),
		Sequence:      1,
		UpdatedAt: time.Date(
			2026,
			7,
			30,
			1,
			0,
			0,
			0,
			time.UTC,
		),
	}
	body, err := usecase.CanonicalSchedulerState(state, limits)
	require.NoError(t, err)
	head := strings.Repeat("b", 40)
	port := &stateCommitStub{
		head: head,
		records: map[string]reviewagentgithub.StateCommitRecord{
			head: trustedRecord(
				head,
				state.SourceSHA,
				"review(scheduler): sequence 1",
				".review-agent-state/scheduler.json",
				body,
			),
		},
	}
	store, err := reviewagentgithub.NewSchedulerStore(
		"wukongim-review-state-writer[bot]",
		port,
		limits,
	)
	require.NoError(t, err)

	loaded, found, err := store.Load(context.Background())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, state, loaded.State)
	require.Equal(t, "review-state/scheduler", port.ref)
}

func TestSchedulerStoreLoadsHighSequenceFromRollingCheckpoint(t *testing.T) {
	t.Parallel()

	limits := usecase.SchedulerLimits{
		MaxActive: 3, MaxPerPullRequest: 1, MaxFirstTimeExternal: 1,
	}
	previous := usecase.SchedulerState{
		SchemaVersion:       1,
		SourceSHA:           strings.Repeat("a", 40),
		Sequence:            9999,
		PreviousStateDigest: "sha256:" + strings.Repeat("1", 64),
		UpdatedAt: time.Date(
			2026, 7, 30, 1, 0, 0, 0, time.UTC,
		),
	}
	previousDigest, err := usecase.SchedulerStateDigest(previous, limits)
	require.NoError(t, err)
	latest := previous
	latest.Sequence = 10000
	latest.PreviousStateDigest = previousDigest
	latest.UpdatedAt = previous.UpdatedAt.Add(time.Minute)
	previousBody, err := usecase.CanonicalSchedulerState(previous, limits)
	require.NoError(t, err)
	latestBody, err := usecase.CanonicalSchedulerState(latest, limits)
	require.NoError(t, err)
	previousCommit := strings.Repeat("b", 40)
	head := strings.Repeat("c", 40)
	port := &stateCommitStub{
		head: head,
		records: map[string]reviewagentgithub.StateCommitRecord{
			head: trustedRecord(
				head,
				previousCommit,
				"review(scheduler): sequence 10000",
				".review-agent-state/scheduler.json",
				latestBody,
			),
			previousCommit: trustedRecord(
				previousCommit,
				strings.Repeat("d", 40),
				"review(scheduler): sequence 9999",
				".review-agent-state/scheduler.json",
				previousBody,
			),
		},
	}
	store, err := reviewagentgithub.NewSchedulerStore(
		"wukongim-review-state-writer[bot]",
		port,
		limits,
	)
	require.NoError(t, err)

	loaded, found, err := store.Load(context.Background())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, latest, loaded.State)
	require.Equal(t, 2, port.reads)
}

func TestSchedulerStoreLoadsLegacyDuplicateEmptyCheckpointForRepair(
	t *testing.T,
) {
	t.Parallel()

	limits := usecase.SchedulerLimits{
		MaxActive: 3, MaxPerPullRequest: 1, MaxFirstTimeExternal: 1,
	}
	previous := schedulerStateFixture(4)
	previousBody, err := usecase.CanonicalSchedulerState(previous, limits)
	require.NoError(t, err)
	legacyLatest := previous
	legacyLatest.Queue = []usecase.QueueEntry{}
	legacyLatest.Active = []usecase.Lease{}
	legacyBody, err := json.Marshal(legacyLatest)
	require.NoError(t, err)
	require.NotEqual(t, previousBody, legacyBody)

	previousCommit := strings.Repeat("b", 40)
	head := strings.Repeat("c", 40)
	port := &stateCommitStub{
		head: head,
		records: map[string]reviewagentgithub.StateCommitRecord{
			head: trustedRecord(
				head,
				previousCommit,
				"review(scheduler): sequence 4",
				".review-agent-state/scheduler.json",
				legacyBody,
			),
			previousCommit: trustedRecord(
				previousCommit,
				strings.Repeat("d", 40),
				"review(scheduler): sequence 4",
				".review-agent-state/scheduler.json",
				previousBody,
			),
		},
	}
	store, err := reviewagentgithub.NewSchedulerStore(
		"wukongim-review-state-writer[bot]",
		port,
		limits,
	)
	require.NoError(t, err)

	loaded, found, err := store.Load(context.Background())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, head, loaded.HeadSHA)
	require.Equal(t, previous, loaded.State)
}

func TestSchedulerStoreLoadsStrictSuccessorAfterLegacyRepairCheckpoint(
	t *testing.T,
) {
	t.Parallel()

	limits := usecase.SchedulerLimits{
		MaxActive: 3, MaxPerPullRequest: 1, MaxFirstTimeExternal: 1,
	}
	legacyPrevious := schedulerStateFixture(4)
	legacyPrevious.Queue = []usecase.QueueEntry{}
	legacyPrevious.Active = []usecase.Lease{}
	legacyBody, err := json.Marshal(legacyPrevious)
	require.NoError(t, err)
	previousDigest, err := usecase.SchedulerStateDigest(
		legacyPrevious,
		limits,
	)
	require.NoError(t, err)
	latest := schedulerStateFixture(5)
	latest.PreviousStateDigest = previousDigest
	latest.UpdatedAt = legacyPrevious.UpdatedAt.Add(time.Minute)
	latestBody, err := usecase.CanonicalSchedulerState(latest, limits)
	require.NoError(t, err)

	previousCommit := strings.Repeat("b", 40)
	head := strings.Repeat("c", 40)
	port := &stateCommitStub{
		head: head,
		records: map[string]reviewagentgithub.StateCommitRecord{
			head: trustedRecord(
				head,
				previousCommit,
				"review(scheduler): sequence 5",
				".review-agent-state/scheduler.json",
				latestBody,
			),
			previousCommit: trustedRecord(
				previousCommit,
				strings.Repeat("d", 40),
				"review(scheduler): sequence 4",
				".review-agent-state/scheduler.json",
				legacyBody,
			),
		},
	}
	store, err := reviewagentgithub.NewSchedulerStore(
		"wukongim-review-state-writer[bot]",
		port,
		limits,
	)
	require.NoError(t, err)

	loaded, found, err := store.Load(context.Background())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, latest, loaded.State)
}

func TestSchedulerStoreRejectsLegacyNonDuplicateCheckpoint(t *testing.T) {
	t.Parallel()

	limits := usecase.SchedulerLimits{
		MaxActive: 3, MaxPerPullRequest: 1, MaxFirstTimeExternal: 1,
	}
	previous := schedulerStateFixture(4)
	previousBody, err := usecase.CanonicalSchedulerState(previous, limits)
	require.NoError(t, err)
	previousDigest, err := usecase.SchedulerStateDigest(previous, limits)
	require.NoError(t, err)
	legacyLatest := schedulerStateFixture(5)
	legacyLatest.PreviousStateDigest = previousDigest
	legacyLatest.Queue = []usecase.QueueEntry{}
	legacyLatest.Active = []usecase.Lease{}
	legacyLatest.UpdatedAt = previous.UpdatedAt.Add(time.Minute)
	legacyBody, err := json.Marshal(legacyLatest)
	require.NoError(t, err)

	previousCommit := strings.Repeat("b", 40)
	head := strings.Repeat("c", 40)
	port := &stateCommitStub{
		head: head,
		records: map[string]reviewagentgithub.StateCommitRecord{
			head: trustedRecord(
				head,
				previousCommit,
				"review(scheduler): sequence 5",
				".review-agent-state/scheduler.json",
				legacyBody,
			),
			previousCommit: trustedRecord(
				previousCommit,
				strings.Repeat("d", 40),
				"review(scheduler): sequence 4",
				".review-agent-state/scheduler.json",
				previousBody,
			),
		},
	}
	store, err := reviewagentgithub.NewSchedulerStore(
		"wukongim-review-state-writer[bot]",
		port,
		limits,
	)
	require.NoError(t, err)

	_, _, err = store.Load(context.Background())
	require.EqualError(
		t,
		err,
		"Review scheduler rolling checkpoint is not contiguous",
	)
}

type stateCommitStub struct {
	head          string
	ref           string
	records       map[string]reviewagentgithub.StateCommitRecord
	request       reviewagentgithub.StateCommitRequest
	publishResult reviewagentgithub.StateCommitResult
	reads         int
}

func (stub *stateCommitStub) PublishStateCommit(
	_ context.Context,
	request reviewagentgithub.StateCommitRequest,
) (reviewagentgithub.StateCommitResult, error) {
	stub.request = request
	return stub.publishResult, nil
}

func (stub *stateCommitStub) StateRefHead(
	_ context.Context,
	ref string,
) (string, bool, error) {
	stub.ref = ref
	return stub.head, stub.head != "", nil
}

func (stub *stateCommitStub) ReadStateCommit(
	_ context.Context,
	commitSHA string,
	_ string,
) (reviewagentgithub.StateCommitRecord, error) {
	stub.reads++
	return stub.records[commitSHA], nil
}

func trustedRecord(
	sha string,
	parent string,
	message string,
	path string,
	content []byte,
) reviewagentgithub.StateCommitRecord {
	return reviewagentgithub.StateCommitRecord{
		CommitSHA: sha, ParentSHA: parent, Message: message,
		Path: path, Content: content,
		AuthorLogin: "wukongim-review-state-writer[bot]",
		AuthorType:  "Bot", Verified: true, SignedByGitHub: true,
	}
}

func schedulerStateFixture(sequence uint64) usecase.SchedulerState {
	return usecase.SchedulerState{
		SchemaVersion:       1,
		SourceSHA:           strings.Repeat("a", 40),
		Sequence:            sequence,
		PreviousStateDigest: "sha256:" + strings.Repeat("1", 64),
		UpdatedAt: time.Date(
			2026, 7, 31, 1, 0, 0, 0, time.UTC,
		),
	}
}

func reviewStateFixture(sequence uint64) contract.ReviewState {
	return contract.ReviewState{
		SchemaVersion: 1,
		Generation: contract.GenerationIdentity{
			Repository:   "WuKongIM/WuKongIM",
			PullRequest:  42,
			HeadSHA:      strings.Repeat("1", 40),
			BaseSHA:      strings.Repeat("2", 40),
			TestMergeSHA: strings.Repeat("3", 40),
			IntentDigest: "sha256:" + strings.Repeat("4", 64),
			Generation:   1,
			StateParentSHA: strings.Repeat(
				"a",
				40,
			),
		},
		Sequence: sequence,
		Phase:    contract.PhaseQueued,
		Reason:   "queued",
		StartedAt: time.Date(
			2026,
			7,
			30,
			0,
			55,
			0,
			0,
			time.UTC,
		),
		UpdatedAt: time.Date(
			2026,
			7,
			30,
			1,
			0,
			0,
			0,
			time.UTC,
		),
	}
}
