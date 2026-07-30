package issueagentgithub_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestStateStorePublishesCanonicalStateToPerIssueRef(t *testing.T) {
	t.Parallel()

	port := &stateCommitPortStub{}
	store, err := issueagentgithub.NewStateStore(
		"WuKongIM/WuKongIM", "wukongim-issue-agent[bot]", port,
	)
	require.NoError(t, err)
	state := contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            1,
		State:               contract.IssueStateWaitingForAuthorization,
		Reason:              "waiting for authorization",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           "0123456789abcdef0123456789abcdef01234567",
		UpdatedAt:           time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	}

	published, err := store.Advance(context.Background(),
		issueagentgithub.StateAdvanceRequest{
			State:             state,
			ExpectedParentSHA: state.SourceSHA,
			BaseTreeSHA:       "1234567890abcdef1234567890abcdef12345678",
			ExistingBranch:    false,
		})
	require.NoError(t, err)
	require.Equal(t, "fedcba9876543210fedcba9876543210fedcba98", published.HeadSHA)
	require.Equal(t, "agent-state/issue-42", port.request.Branch)
	require.Equal(t, ".issue-agent-state/issue-42.json", port.request.Path)
	require.Equal(t, state.SourceSHA, port.request.ExpectedParentSHA)

	canonical, err := contract.CanonicalIssueAgentState(state)
	require.NoError(t, err)
	require.Equal(t, canonical, port.request.Content)
}

func TestStateStoreRecoversExactSignedStateAfterAmbiguousPublish(t *testing.T) {
	t.Parallel()

	state := stateStoreRecoveryState()
	port := stateStoreRecoveryPort(t, state)
	port.publishErr = errors.New("transient post-publish verification failure")

	published, err := stateStoreForTest(t, port).Advance(
		context.Background(),
		stateStoreRecoveryRequest(state),
	)
	require.NoError(t, err)
	require.Equal(t, stateStoreRecoveryHeadSHA, published.HeadSHA)
}

func TestStateStoreRecoversExactSignedStateAfterAmbiguousTrustResult(t *testing.T) {
	t.Parallel()

	state := stateStoreRecoveryState()
	port := stateStoreRecoveryPort(t, state)
	sum := sha256.Sum256(port.records[stateStoreRecoveryHeadSHA].Content)
	port.publishResult = &issueagentgithub.StateCommitResult{
		CommitSHA: stateStoreRecoveryHeadSHA,
		ParentSHA: stateStoreRecoverySourceSHA,
		Path:      stateStoreRecoveryPath,
		ContentDigest: "sha256:" +
			hex.EncodeToString(sum[:]),
		AuthorLogin: "wukongim-issue-agent[bot]", AuthorType: "Bot",
	}

	published, err := stateStoreForTest(t, port).Advance(
		context.Background(),
		stateStoreRecoveryRequest(state),
	)
	require.NoError(t, err)
	require.Equal(t, stateStoreRecoveryHeadSHA, published.HeadSHA)
}

func TestStateStoreRejectsDifferentStateAfterAmbiguousPublish(t *testing.T) {
	t.Parallel()

	state := stateStoreRecoveryState()
	different := state
	different.Reason = "different durable state"
	publishErr := errors.New("transient post-publish verification failure")
	port := stateStoreRecoveryPort(t, different)
	port.publishErr = publishErr

	_, err := stateStoreForTest(t, port).Advance(
		context.Background(),
		stateStoreRecoveryRequest(state),
	)
	require.EqualError(t, err, publishErr.Error())
}

func TestStateStoreLoadsVerifiedAppendOnlyStateChain(t *testing.T) {
	t.Parallel()

	sourceSHA := "0123456789abcdef0123456789abcdef01234567"
	initial := contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            1,
		State:               contract.IssueStateWaitingForAuthorization,
		Reason:              "waiting for authorization",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           sourceSHA,
		UpdatedAt:           time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC),
	}
	initialDigest, err := contract.IssueAgentStateDigest(initial)
	require.NoError(t, err)
	successor := initial
	successor.Sequence = 2
	successor.State = contract.IssueStateEngineering
	successor.Reason = "engineering"
	successor.PreviousStateDigest = initialDigest
	successor.Task = &contract.TaskIdentity{
		ID:           "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Kind:         contract.TaskKindEngineer,
		BaseSHA:      sourceSHA,
		AffectedSHA:  sourceSHA,
		PolicyDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		PromptDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
	}
	successor.Authorization = &contract.AuthorizationRecord{
		Actor: "maintainer", Permission: "write",
		EventID: "issue:42", Command: "/agent fix",
	}
	successor.UpdatedAt = time.Date(2026, 7, 30, 1, 2, 0, 0, time.UTC)

	initialBody, err := contract.CanonicalIssueAgentState(initial)
	require.NoError(t, err)
	successorBody, err := contract.CanonicalIssueAgentState(successor)
	require.NoError(t, err)
	port := &stateCommitPortStub{
		head: "2222222222222222222222222222222222222222",
		records: map[string]issueagentgithub.StateCommitRecord{
			"2222222222222222222222222222222222222222": {
				CommitSHA:   "2222222222222222222222222222222222222222",
				ParentSHA:   "1111111111111111111111111111111111111111",
				Message:     "agent(state): issue 42 sequence 2",
				Path:        ".issue-agent-state/issue-42.json",
				Content:     successorBody,
				AuthorLogin: "wukongim-issue-agent[bot]",
				AuthorType:  "Bot", Verified: true, SignedByGitHub: true,
			},
			"1111111111111111111111111111111111111111": {
				CommitSHA:   "1111111111111111111111111111111111111111",
				ParentSHA:   sourceSHA,
				Message:     "agent(state): issue 42 sequence 1",
				Path:        ".issue-agent-state/issue-42.json",
				Content:     initialBody,
				AuthorLogin: "wukongim-issue-agent[bot]",
				AuthorType:  "Bot", Verified: true, SignedByGitHub: true,
			},
		},
	}
	store, err := issueagentgithub.NewStateStore(
		"WuKongIM/WuKongIM", "wukongim-issue-agent[bot]", port,
	)
	require.NoError(t, err)

	loaded, found, err := store.Load(context.Background(), 42)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, port.head, loaded.HeadSHA)
	require.Equal(t, successor, loaded.State)
}

const (
	stateStoreRecoverySourceSHA = "0123456789abcdef0123456789abcdef01234567"
	stateStoreRecoveryHeadSHA   = "fedcba9876543210fedcba9876543210fedcba98"
	stateStoreRecoveryTreeSHA   = "1234567890abcdef1234567890abcdef12345678"
	stateStoreRecoveryPath      = ".issue-agent-state/issue-42.json"
)

func stateStoreRecoveryState() contract.IssueAgentState {
	return contract.IssueAgentState{
		SchemaVersion:       2,
		Repository:          "WuKongIM/WuKongIM",
		IssueNumber:         42,
		Sequence:            1,
		State:               contract.IssueStateWaitingForAuthorization,
		Reason:              "waiting for authorization",
		IssueSnapshotDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceSHA:           stateStoreRecoverySourceSHA,
		UpdatedAt:           time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC),
	}
}

func stateStoreRecoveryPort(
	t *testing.T,
	state contract.IssueAgentState,
) *stateCommitPortStub {
	t.Helper()
	content, err := contract.CanonicalIssueAgentState(state)
	require.NoError(t, err)
	return &stateCommitPortStub{
		head: stateStoreRecoveryHeadSHA,
		records: map[string]issueagentgithub.StateCommitRecord{
			stateStoreRecoveryHeadSHA: {
				CommitSHA:   stateStoreRecoveryHeadSHA,
				ParentSHA:   stateStoreRecoverySourceSHA,
				Message:     "agent(state): issue 42 sequence 1",
				Path:        stateStoreRecoveryPath,
				Content:     content,
				AuthorLogin: "wukongim-issue-agent[bot]",
				AuthorType:  "Bot",
				Verified:    true, SignedByGitHub: true,
			},
		},
	}
}

func stateStoreForTest(
	t *testing.T,
	port *stateCommitPortStub,
) *issueagentgithub.StateStore {
	t.Helper()
	store, err := issueagentgithub.NewStateStore(
		"WuKongIM/WuKongIM",
		"wukongim-issue-agent[bot]",
		port,
	)
	require.NoError(t, err)
	return store
}

func stateStoreRecoveryRequest(
	state contract.IssueAgentState,
) issueagentgithub.StateAdvanceRequest {
	return issueagentgithub.StateAdvanceRequest{
		State:             state,
		ExpectedParentSHA: stateStoreRecoverySourceSHA,
		BaseTreeSHA:       stateStoreRecoveryTreeSHA,
		ExistingBranch:    false,
	}
}

type stateCommitPortStub struct {
	request       issueagentgithub.StateCommitRequest
	publishErr    error
	publishResult *issueagentgithub.StateCommitResult
	head          string
	records       map[string]issueagentgithub.StateCommitRecord
}

func (port *stateCommitPortStub) PublishStateCommit(
	_ context.Context,
	request issueagentgithub.StateCommitRequest,
) (issueagentgithub.StateCommitResult, error) {
	port.request = request
	if port.publishErr != nil {
		return issueagentgithub.StateCommitResult{}, port.publishErr
	}
	if port.publishResult != nil {
		return *port.publishResult, nil
	}
	sum := sha256.Sum256(request.Content)
	return issueagentgithub.StateCommitResult{
		CommitSHA:      "fedcba9876543210fedcba9876543210fedcba98",
		ParentSHA:      request.ExpectedParentSHA,
		Path:           request.Path,
		ContentDigest:  "sha256:" + hex.EncodeToString(sum[:]),
		AuthorLogin:    "wukongim-issue-agent[bot]",
		AuthorType:     "Bot",
		Verified:       true,
		SignedByGitHub: true,
	}, nil
}

func (port *stateCommitPortStub) StateRefHead(
	context.Context,
	string,
) (string, bool, error) {
	return port.head, port.head != "", nil
}

func (port *stateCommitPortStub) ReadStateCommit(
	_ context.Context,
	commitSHA string,
	_ string,
) (issueagentgithub.StateCommitRecord, error) {
	return port.records[commitSHA], nil
}
