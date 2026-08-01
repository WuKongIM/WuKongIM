package issueagentgithub_test

import (
	"context"
	"errors"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestCandidateBaseSynchronizerRebasesExactAppHeadOntoCurrentMain(t *testing.T) {
	t.Parallel()

	oldHead := fortyHex("1")
	firstHead := fortyHex("d")
	oldBase := fortyHex("2")
	currentMain := fortyHex("3")
	mainTree := fortyHex("4")
	oldBaseTree := fortyHex("5")
	candidateTree := fortyHex("6")
	resultTree := fortyHex("7")
	newHead := fortyHex("8")
	stateHead := fortyHex("9")
	newStateHead := fortyHex("a")
	stateTree := fortyHex("c")
	oldBlob := fortyHex("b")
	content := []byte("portable command\n")
	newBlob := testGitBlobSHA(content)
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	state := readyBaseSyncState(oldHead, now.Add(-time.Minute))
	state.Budget.ReviewIterations = 1
	states := &baseSyncStateStore{
		loaded:      issueagentgithub.LoadedState{HeadSHA: stateHead, State: state},
		publication: issueagentgithub.StatePublication{HeadSHA: newStateHead},
	}
	github := &baseSyncGitHub{
		pull: issueagentgithub.PullRequestFacts{
			Number: 84, State: "open", Draft: false, BaseRef: "main",
			BaseSHA: oldBase, HeadRef: "agent/issue-42", HeadSHA: oldHead,
		},
		commits: map[string]issueagentgithub.CommitFacts{
			oldHead: {
				SHA: oldHead, TreeSHA: candidateTree, Parents: []string{firstHead},
				Message: "fix(agent): resolve issue #42", Verified: true,
				VerificationReason: "valid",
			},
			firstHead: {
				SHA: firstHead, TreeSHA: fortyHex("e"), Parents: []string{oldBase},
				Message: "fix(agent): resolve issue #42", Verified: true,
				VerificationReason: "valid",
			},
			oldBase:     {SHA: oldBase, TreeSHA: oldBaseTree},
			currentMain: {SHA: currentMain, TreeSHA: mainTree},
			stateHead:   {SHA: stateHead, TreeSHA: stateTree},
		},
		attributions: map[string]issueagentgithub.CommitAttributionFacts{
			oldHead:   exactAppAttribution(oldHead),
			firstHead: exactAppAttribution(firstHead),
		},
		compare: []issueagentgithub.CompareFileFacts{{
			Path: "docs/fix.md", Status: "modified", SHA: newBlob,
		}},
		entries: map[string]issueagentgithub.TreeEntryFacts{
			oldBaseTree + ":docs/fix.md": {
				Path: "docs/fix.md", Type: "blob", Mode: "100644", SHA: oldBlob,
			},
			mainTree + ":docs/fix.md": {
				Path: "docs/fix.md", Type: "blob", Mode: "100644", SHA: oldBlob,
			},
			candidateTree + ":docs/fix.md": {
				Path: "docs/fix.md", Type: "blob", Mode: "100644", SHA: newBlob,
			},
		},
		blobs:      map[string][]byte{newBlob: content},
		resultTree: resultTree,
		newHead:    newHead,
	}
	synchronizer, err := issueagentgithub.NewCandidateBaseSynchronizer(
		"WuKongIM/WuKongIM", "issue-agent[bot]", states, github,
	)
	require.NoError(t, err)

	result, err := synchronizer.Synchronize(context.Background(),
		issueagentgithub.CandidateBaseSyncRequest{
			IssueNumber:         42,
			ExpectedStateHead:   stateHead,
			CurrentMainSHA:      currentMain,
			IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Now:                 now,
		})
	require.NoError(t, err)
	require.Equal(t, newHead, result.HeadSHA)
	require.Equal(t, newStateHead, result.StateHeadSHA)
	require.Equal(t, oldHead, github.rebasePlan.ExpectedOldHeadSHA)
	require.Equal(t, currentMain, github.rebasePlan.CurrentMainSHA)
	require.Equal(t, resultTree, github.rebasePlan.ExpectedResultTreeSHA)
	require.Equal(t, "fix(agent): resolve issue #42", github.rebasePlan.Message)
	require.Len(t, github.rebasePlan.ChangeSet.Files, 1)
	require.Equal(t, 2, github.compareMaxCommits)
	require.Equal(t, newHead, states.advance.State.Work.HeadSHA)
	require.Equal(t, currentMain, states.advance.State.SourceSHA)
	require.Equal(t, uint32(1), states.advance.State.Budget.BaseSyncs)
	require.False(t, states.advance.State.Work.Draft)
	require.Equal(t, states.advance.State, result.State)
}

func TestCandidateBaseSynchronizerRejectsCandidatePathChangedOnMain(t *testing.T) {
	t.Parallel()

	oldHead := fortyHex("1")
	oldBase := fortyHex("2")
	currentMain := fortyHex("3")
	oldBaseTree := fortyHex("4")
	mainTree := fortyHex("5")
	candidateTree := fortyHex("6")
	stateHead := fortyHex("7")
	oldBlob := fortyHex("8")
	mainBlob := fortyHex("9")
	newBlob := fortyHex("a")
	state := readyBaseSyncState(oldHead, time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC))
	states := &baseSyncStateStore{
		loaded: issueagentgithub.LoadedState{HeadSHA: stateHead, State: state},
	}
	github := &baseSyncGitHub{
		pull: issueagentgithub.PullRequestFacts{
			Number: 84, State: "open", BaseRef: "main", BaseSHA: oldBase,
			HeadRef: state.Work.Branch, HeadSHA: oldHead,
		},
		commits: map[string]issueagentgithub.CommitFacts{
			oldHead: {
				SHA: oldHead, TreeSHA: candidateTree, Parents: []string{oldBase},
				Message: "fix(agent): resolve issue #42", Verified: true,
				VerificationReason: "valid",
			},
			oldBase:     {SHA: oldBase, TreeSHA: oldBaseTree},
			currentMain: {SHA: currentMain, TreeSHA: mainTree},
		},
		attributions: map[string]issueagentgithub.CommitAttributionFacts{
			oldHead: exactAppAttribution(oldHead),
		},
		compare: []issueagentgithub.CompareFileFacts{{
			Path: "docs/fix.md", Status: "modified", SHA: newBlob,
		}},
		entries: map[string]issueagentgithub.TreeEntryFacts{
			oldBaseTree + ":docs/fix.md": {
				Path: "docs/fix.md", Type: "blob", Mode: "100644", SHA: oldBlob,
			},
			mainTree + ":docs/fix.md": {
				Path: "docs/fix.md", Type: "blob", Mode: "100644", SHA: mainBlob,
			},
		},
	}
	synchronizer, err := issueagentgithub.NewCandidateBaseSynchronizer(
		state.Repository, "issue-agent[bot]", states, github,
	)
	require.NoError(t, err)

	_, err = synchronizer.Synchronize(context.Background(),
		issueagentgithub.CandidateBaseSyncRequest{
			IssueNumber: 42, ExpectedStateHead: stateHead,
			CurrentMainSHA: currentMain, IssueSnapshotDigest: state.IssueSnapshotDigest,
			Now: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
		})
	require.EqualError(t, err, "current main changed a candidate path")
	var rejection issueagentgithub.CandidateBaseSyncRejection
	require.ErrorAs(t, err, &rejection)
	require.Empty(t, github.rebasePlan.Branch)
	require.Empty(t, states.advance.State.Repository)

	transientStates := &baseSyncStateStore{loaded: issueagentgithub.LoadedState{
		HeadSHA: stateHead, State: state,
	}}
	transientGitHub := *github
	transientGitHub.commitErrors = map[string]error{
		oldHead: errors.New("temporary commit read failure"),
	}
	transientSynchronizer, err := issueagentgithub.NewCandidateBaseSynchronizer(
		state.Repository, "issue-agent[bot]", transientStates, &transientGitHub,
	)
	require.NoError(t, err)
	_, err = transientSynchronizer.Synchronize(context.Background(),
		issueagentgithub.CandidateBaseSyncRequest{
			IssueNumber: 42, ExpectedStateHead: stateHead,
			CurrentMainSHA: currentMain, IssueSnapshotDigest: state.IssueSnapshotDigest,
			Now: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
		})
	require.EqualError(t, err, "temporary commit read failure")
	require.False(t, errors.As(err, &rejection))

	comparisonStates := &baseSyncStateStore{loaded: issueagentgithub.LoadedState{
		HeadSHA: stateHead, State: state,
	}}
	comparisonGitHub := *github
	comparisonGitHub.compareErr = candidateComparisonRejectionStub{
		reason: "Git compare contains an unsupported file status",
	}
	comparisonSynchronizer, err := issueagentgithub.NewCandidateBaseSynchronizer(
		state.Repository, "issue-agent[bot]", comparisonStates, &comparisonGitHub,
	)
	require.NoError(t, err)
	_, err = comparisonSynchronizer.Synchronize(context.Background(),
		issueagentgithub.CandidateBaseSyncRequest{
			IssueNumber: 42, ExpectedStateHead: stateHead,
			CurrentMainSHA: currentMain, IssueSnapshotDigest: state.IssueSnapshotDigest,
			Now: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
		})
	require.EqualError(t, err, "Git compare contains an unsupported file status")
	var comparisonRejection issueagentgithub.CandidateBaseSyncRejection
	require.ErrorAs(t, err, &comparisonRejection)

	wrongSourceState := state
	wrongSourceState.SourceSHA = fortyHex("f")
	wrongSourceStates := &baseSyncStateStore{loaded: issueagentgithub.LoadedState{
		HeadSHA: stateHead, State: wrongSourceState,
	}}
	wrongSourceSynchronizer, err := issueagentgithub.NewCandidateBaseSynchronizer(
		state.Repository, "issue-agent[bot]", wrongSourceStates, github,
	)
	require.NoError(t, err)
	_, err = wrongSourceSynchronizer.Synchronize(context.Background(),
		issueagentgithub.CandidateBaseSyncRequest{
			IssueNumber: 42, ExpectedStateHead: stateHead,
			CurrentMainSHA: currentMain, IssueSnapshotDigest: state.IssueSnapshotDigest,
			Now: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
		})
	require.EqualError(t, err,
		"Agent candidate history does not reach its signed source")
	require.ErrorAs(t, err, &rejection)
}

func TestCandidateBaseSynchronizerRecoversExactSwappedHeadBeforeStateWrite(t *testing.T) {
	t.Parallel()

	oldHead := fortyHex("1")
	oldBase := fortyHex("2")
	currentMain := fortyHex("3")
	latestMain := fortyHex("d")
	oldBaseTree := fortyHex("4")
	mainTree := fortyHex("5")
	candidateTree := fortyHex("6")
	resultTree := fortyHex("7")
	newHead := fortyHex("8")
	stateHead := fortyHex("9")
	stateTree := fortyHex("a")
	newStateHead := fortyHex("b")
	oldBlob := fortyHex("c")
	content := []byte("portable command\n")
	newBlob := testGitBlobSHA(content)
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	state := readyBaseSyncState(oldHead, now.Add(-time.Minute))
	states := &baseSyncStateStore{
		loaded:      issueagentgithub.LoadedState{HeadSHA: stateHead, State: state},
		publication: issueagentgithub.StatePublication{HeadSHA: newStateHead},
	}
	github := &baseSyncGitHub{
		pull: issueagentgithub.PullRequestFacts{
			Number: 84, State: "open", BaseRef: "main", BaseSHA: currentMain,
			HeadRef: state.Work.Branch, HeadSHA: newHead,
		},
		commits: map[string]issueagentgithub.CommitFacts{
			oldHead: {
				SHA: oldHead, TreeSHA: candidateTree, Parents: []string{oldBase},
				Message: "fix(agent): resolve issue #42", Verified: true,
				VerificationReason: "valid",
			},
			oldBase:     {SHA: oldBase, TreeSHA: oldBaseTree},
			currentMain: {SHA: currentMain, TreeSHA: mainTree},
			newHead: {
				SHA: newHead, TreeSHA: resultTree, Parents: []string{currentMain},
				Message: "fix(agent): resolve issue #42", Verified: true,
				VerificationReason: "valid",
			},
			stateHead: {SHA: stateHead, TreeSHA: stateTree},
		},
		attributions: map[string]issueagentgithub.CommitAttributionFacts{
			oldHead: exactAppAttribution(oldHead),
			newHead: exactAppAttribution(newHead),
		},
		compare: []issueagentgithub.CompareFileFacts{{
			Path: "docs/fix.md", Status: "modified", SHA: newBlob,
		}},
		entries: map[string]issueagentgithub.TreeEntryFacts{
			oldBaseTree + ":docs/fix.md": {
				Path: "docs/fix.md", Type: "blob", Mode: "100644", SHA: oldBlob,
			},
			mainTree + ":docs/fix.md": {
				Path: "docs/fix.md", Type: "blob", Mode: "100644", SHA: oldBlob,
			},
			candidateTree + ":docs/fix.md": {
				Path: "docs/fix.md", Type: "blob", Mode: "100644", SHA: newBlob,
			},
		},
		blobs:      map[string][]byte{newBlob: content},
		resultTree: resultTree,
	}
	synchronizer, err := issueagentgithub.NewCandidateBaseSynchronizer(
		state.Repository, "issue-agent[bot]", states, github,
	)
	require.NoError(t, err)

	result, err := synchronizer.Synchronize(context.Background(),
		issueagentgithub.CandidateBaseSyncRequest{
			IssueNumber: 42, ExpectedStateHead: stateHead,
			CurrentMainSHA: latestMain, IssueSnapshotDigest: state.IssueSnapshotDigest,
			Now: now,
		})
	require.NoError(t, err)
	require.Equal(t, newHead, result.HeadSHA)
	require.Empty(t, github.rebasePlan.Branch,
		"recovery must not create another candidate or move the ref again")
	require.Equal(t, newHead, states.advance.State.Work.HeadSHA)
	require.Equal(t, currentMain, states.advance.State.SourceSHA,
		"recovery records the exact published base before chasing newer main")

	externalStates := &baseSyncStateStore{
		loaded: issueagentgithub.LoadedState{HeadSHA: stateHead, State: state},
	}
	externalGitHub := *github
	externalAttribution := exactAppAttribution(newHead)
	externalAttribution.AuthorLogin = "maintainer"
	externalAttribution.AuthorType = "User"
	externalGitHub.attributions = map[string]issueagentgithub.CommitAttributionFacts{
		oldHead: exactAppAttribution(oldHead), newHead: externalAttribution,
	}
	externalGitHub.rebasePlan = issueagentgithub.RebasePlan{}
	externalSynchronizer, err := issueagentgithub.NewCandidateBaseSynchronizer(
		state.Repository, "issue-agent[bot]", externalStates, &externalGitHub,
	)
	require.NoError(t, err)
	_, err = externalSynchronizer.Synchronize(context.Background(),
		issueagentgithub.CandidateBaseSyncRequest{
			IssueNumber: 42, ExpectedStateHead: stateHead,
			CurrentMainSHA: latestMain, IssueSnapshotDigest: state.IssueSnapshotDigest,
			Now: now,
		})
	require.EqualError(t, err,
		"Agent pull request head changed outside the Publisher")
	var externalRejection issueagentgithub.CandidateBaseSyncRejection
	require.ErrorAs(t, err, &externalRejection)
	require.Empty(t, externalGitHub.rebasePlan.Branch)
	require.Empty(t, externalStates.advance.State.Repository)

	notReviewable := state
	notReviewable.State = contract.IssueStateNeedsHuman
	blockedStates := &baseSyncStateStore{loaded: issueagentgithub.LoadedState{
		HeadSHA: stateHead, State: notReviewable,
	}}
	blockedGitHub := *github
	blockedGitHub.rebasePlan = issueagentgithub.RebasePlan{}
	blockedSynchronizer, err := issueagentgithub.NewCandidateBaseSynchronizer(
		state.Repository, "issue-agent[bot]", blockedStates, &blockedGitHub,
	)
	require.NoError(t, err)
	_, err = blockedSynchronizer.Synchronize(context.Background(),
		issueagentgithub.CandidateBaseSyncRequest{
			IssueNumber: 42, ExpectedStateHead: stateHead,
			CurrentMainSHA: latestMain, IssueSnapshotDigest: state.IssueSnapshotDigest,
			Now: now,
		})
	require.EqualError(t, err,
		"Candidate base synchronization state is not reviewable")
	require.Empty(t, blockedGitHub.rebasePlan.Branch)
}

func readyBaseSyncState(head string, now time.Time) contract.IssueAgentState {
	return contract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 6,
		State:               contract.IssueStateReadyForReview,
		Reason:              "ready",
		PreviousStateDigest: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           fortyHex("2"),
		Work: &contract.IssueWork{
			Branch: "agent/issue-42", HeadSHA: head,
			PullRequest: 84, Draft: false,
		},
		StatusCommentID: 77,
		ContextDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CandidateDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		EvidenceDigest:  "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		UpdatedAt:       now,
	}
}

func exactAppAttribution(sha string) issueagentgithub.CommitAttributionFacts {
	return issueagentgithub.CommitAttributionFacts{
		SHA: sha, AuthorLogin: "issue-agent[bot]", AuthorType: "Bot",
		SignatureValid: true, SignatureState: "VALID", WasSignedByGitHub: true,
	}
}

type baseSyncStateStore struct {
	loaded      issueagentgithub.LoadedState
	publication issueagentgithub.StatePublication
	advance     issueagentgithub.StateAdvanceRequest
}

func (store *baseSyncStateStore) Load(
	context.Context,
	int64,
) (issueagentgithub.LoadedState, bool, error) {
	return store.loaded, true, nil
}

func (store *baseSyncStateStore) Advance(
	_ context.Context,
	request issueagentgithub.StateAdvanceRequest,
) (issueagentgithub.StatePublication, error) {
	store.advance = request
	store.loaded = issueagentgithub.LoadedState{
		HeadSHA: store.publication.HeadSHA, State: request.State,
	}
	return store.publication, nil
}

type baseSyncGitHub struct {
	pull              issueagentgithub.PullRequestFacts
	commits           map[string]issueagentgithub.CommitFacts
	commitErrors      map[string]error
	attributions      map[string]issueagentgithub.CommitAttributionFacts
	compare           []issueagentgithub.CompareFileFacts
	compareErr        error
	compareMaxCommits int
	entries           map[string]issueagentgithub.TreeEntryFacts
	blobs             map[string][]byte
	resultTree        string
	newHead           string
	rebasePlan        issueagentgithub.RebasePlan
	resultChangeSet   contract.ChangeSet
}

func (github *baseSyncGitHub) PullRequest(
	context.Context,
	int64,
) (issueagentgithub.PullRequestFacts, error) {
	return github.pull, nil
}

func (github *baseSyncGitHub) Commit(
	_ context.Context,
	sha string,
) (issueagentgithub.CommitFacts, error) {
	if err := github.commitErrors[sha]; err != nil {
		return issueagentgithub.CommitFacts{}, err
	}
	return github.commits[sha], nil
}

func (github *baseSyncGitHub) CommitAttribution(
	_ context.Context,
	sha string,
) (issueagentgithub.CommitAttributionFacts, error) {
	return github.attributions[sha], nil
}

func (github *baseSyncGitHub) CompareCandidate(
	_ context.Context,
	_ string,
	_ string,
	maxCommits int,
) ([]issueagentgithub.CompareFileFacts, error) {
	github.compareMaxCommits = maxCommits
	return github.compare, github.compareErr
}

func (github *baseSyncGitHub) ResolveTreePath(
	_ context.Context,
	tree string,
	path string,
) (issueagentgithub.TreeEntryFacts, bool, error) {
	entry, found := github.entries[tree+":"+path]
	return entry, found, nil
}

func (github *baseSyncGitHub) ReadGitBlob(
	_ context.Context,
	sha string,
	_ int64,
) ([]byte, error) {
	return github.blobs[sha], nil
}

func (github *baseSyncGitHub) BuildResultTree(
	_ context.Context,
	_ string,
	changeSet contract.ChangeSet,
) (string, error) {
	github.resultChangeSet = changeSet
	return github.resultTree, nil
}

func (github *baseSyncGitHub) PublishRebasedCommit(
	_ context.Context,
	plan issueagentgithub.RebasePlan,
) (issueagentgithub.PublishedCommit, error) {
	github.rebasePlan = plan
	github.pull.HeadSHA = github.newHead
	return issueagentgithub.PublishedCommit{
		CommitSHA: github.newHead, TreeSHA: github.resultTree,
	}, nil
}

type candidateComparisonRejectionStub struct {
	reason string
}

func (rejection candidateComparisonRejectionStub) Error() string {
	return rejection.reason
}

func (candidateComparisonRejectionStub) CandidateComparisonRejected() {}
