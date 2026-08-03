package issueagent_test

import (
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestBuildIssueStateCreatesCanonicalInitialProjection(t *testing.T) {
	t.Parallel()

	facts := issueagent.IssueSnapshotFacts{
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
	}
	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	state, err := issueagent.BuildIssueState(nil, facts, issueagent.IssueDecision{
		Kind:      issueagent.IssueDecisionWaitAuthorization,
		NextState: contract.IssueStateWaitingForAuthorization,
		Reason:    "waiting for authorization",
	}, now)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.Sequence)
	require.Equal(t, facts.IssueSnapshotDigest, state.IssueSnapshotDigest)
	require.Equal(t, now, state.UpdatedAt)
	require.NoError(t, contract.ValidateIssueAgentState(state))
}

func TestBuildNeedsHumanStateTerminatesTaskWithoutPublishingWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	current := contract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 2,
		State:               contract.IssueStateEngineering,
		Reason:              "engineering",
		PreviousStateDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Task: &contract.TaskIdentity{
			ID:           "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			Kind:         contract.TaskKindEngineer,
			BaseSHA:      "0123456789abcdef0123456789abcdef01234567",
			AffectedSHA:  "0123456789abcdef0123456789abcdef01234567",
			PolicyDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			PromptDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		},
		Authorization: &contract.AuthorizationRecord{
			Actor: "maintainer", Permission: "write",
			EventID: "issue:42", Command: "/agent fix",
		},
		UpdatedAt: now,
	}
	next, err := issueagent.BuildNeedsHumanState(
		current,
		"clean Verifier rejected the candidate",
		now.Add(time.Minute),
	)
	require.NoError(t, err)
	require.Equal(t, contract.IssueStateNeedsHuman, next.State)
	require.Nil(t, next.Task)
	require.Nil(t, next.Work)
	require.Equal(t, uint64(3), next.Sequence)
}

func TestBuildIssueStateNeedsHumanPreservesPublishedSource(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	current := contract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 6,
		State:               contract.IssueStateReadyForReview,
		PreviousStateDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Work: &contract.IssueWork{
			Branch: "agent/issue-42", HeadSHA: "1234567890abcdef1234567890abcdef12345678",
			PullRequest: 84, Draft: false,
		},
		ContextDigest:   "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		CandidateDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		EvidenceDigest:  "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		UpdatedAt:       now,
	}
	next, err := issueagent.BuildIssueState(
		&current,
		issueagent.IssueSnapshotFacts{
			Repository: current.Repository, IssueNumber: current.IssueNumber,
			IssueSnapshotDigest: current.IssueSnapshotDigest,
			SourceSHA:           "234567890abcdef1234567890abcdef123456789",
		},
		issueagent.IssueDecision{
			Kind:      issueagent.IssueDecisionNeedsHuman,
			NextState: contract.IssueStateNeedsHuman,
			Reason:    "automatic base synchronization stopped: overlap",
		},
		now.Add(time.Minute),
	)
	require.NoError(t, err)
	require.Equal(t, current.SourceSHA, next.SourceSHA)
	require.Equal(t, current.Work.HeadSHA, next.Work.HeadSHA)
}

func TestBuildBaseSyncedStateAdvancesExactReadyHead(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	current := contract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM",
		IssueNumber: 42, Sequence: 6,
		State:               contract.IssueStateReadyForReview,
		Reason:              "ready",
		PreviousStateDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		Budget:              contract.IssueBudget{BaseSyncs: 1},
		Work: &contract.IssueWork{
			Branch: "agent/issue-42", HeadSHA: "1234567890abcdef1234567890abcdef12345678",
			PullRequest: 84, Draft: false,
		},
		ContextDigest:   "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		CandidateDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		EvidenceDigest:  "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		ReviewDigest:    "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		UpdatedAt:       now,
	}
	newMain := "234567890abcdef1234567890abcdef123456789"
	newHead := "34567890abcdef1234567890abcdef1234567890"
	newSnapshot := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	next, err := issueagent.BuildBaseSyncedState(
		current, newMain, newHead, newSnapshot, now.Add(time.Minute),
	)
	require.NoError(t, err)
	require.Equal(t, uint64(7), next.Sequence)
	require.Equal(t, contract.IssueStateReadyForReview, next.State)
	require.Equal(t, newMain, next.SourceSHA)
	require.Equal(t, newHead, next.Work.HeadSHA)
	require.False(t, next.Work.Draft)
	require.Equal(t, uint32(2), next.Budget.BaseSyncs)
	require.Empty(t, next.ReviewDigest)
	require.Equal(t, newSnapshot, next.IssueSnapshotDigest)
	require.NoError(t, contract.ValidateIssueAgentState(next))
}
