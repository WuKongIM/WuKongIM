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
