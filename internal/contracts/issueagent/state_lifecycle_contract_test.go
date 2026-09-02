package issueagent_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestIssueAgentStateAcceptsEveryAuthorizedLifecycleShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state issueagent.IssueAgentState
	}{
		{name: "triaging", state: validIssueAgentState(issueagent.IssueStateTriaging)},
		{name: "waiting for information", state: validIssueAgentState(issueagent.IssueStateWaitingForInformation)},
		{name: "waiting for authorization", state: validIssueAgentState(issueagent.IssueStateWaitingForAuthorization)},
		{name: "needs human", state: validIssueAgentState(issueagent.IssueStateNeedsHuman)},
		{name: "cancelled", state: validIssueAgentState(issueagent.IssueStateCancelled)},
	}

	engineering := validIssueAgentState(issueagent.IssueStateEngineering)
	engineeringTask := validTaskIdentity(issueagent.TaskKindEngineer)
	engineering.Task = &engineeringTask
	engineeringAuthorization := validAuthorization()
	engineering.Authorization = &engineeringAuthorization
	tests = append(tests, struct {
		name  string
		state issueagent.IssueAgentState
	}{name: "engineering", state: engineering})

	reviewing := validIssueAgentState(issueagent.IssueStateReviewing)
	reviewTask := validTaskIdentity(issueagent.TaskKindReview)
	reviewing.Task = &reviewTask
	reviewAuthorization := validAuthorization()
	reviewing.Authorization = &reviewAuthorization
	reviewing.Work = validIssueWork(true)
	reviewing.ReviewDigest = issueAgentDigest("c")
	tests = append(tests, struct {
		name  string
		state issueagent.IssueAgentState
	}{name: "reviewing", state: reviewing})

	for _, lifecycle := range []struct {
		name  string
		value issueagent.IssueState
		draft bool
	}{
		{name: "draft", value: issueagent.IssueStateDraft, draft: true},
		{name: "ready for review", value: issueagent.IssueStateReadyForReview},
	} {
		state := validIssueAgentState(lifecycle.value)
		state.Work = validIssueWork(lifecycle.draft)
		state.ContextDigest = issueAgentDigest("c")
		state.CandidateDigest = issueAgentDigest("d")
		state.EvidenceDigest = issueAgentDigest("e")
		tests = append(tests, struct {
			name  string
			state issueagent.IssueAgentState
		}{name: lifecycle.name, state: state})
	}

	completed := validIssueAgentState(issueagent.IssueStateCompleted)
	completed.Work = validIssueWork(false)
	tests = append(tests, struct {
		name  string
		state issueagent.IssueAgentState
	}{name: "completed", state: completed})

	takenOver := validIssueAgentState(issueagent.IssueStateTakenOver)
	takenOver.TakenOverBy = "maintainer"
	tests = append(tests, struct {
		name  string
		state issueagent.IssueAgentState
	}{name: "taken over", state: takenOver})

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, issueagent.ValidateIssueAgentState(test.state))
		})
	}
}

func TestIssueAgentStateRejectsLifecycleContradictions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state func() issueagent.IssueAgentState
	}{
		{
			name: "engineering without Engineer task",
			state: func() issueagent.IssueAgentState {
				return validIssueAgentState(issueagent.IssueStateEngineering)
			},
		},
		{
			name: "reviewing without exact work",
			state: func() issueagent.IssueAgentState {
				state := validIssueAgentState(issueagent.IssueStateReviewing)
				task := validTaskIdentity(issueagent.TaskKindReview)
				authorization := validAuthorization()
				state.Task, state.Authorization = &task, &authorization
				return state
			},
		},
		{
			name: "draft lacks published digests",
			state: func() issueagent.IssueAgentState {
				state := validIssueAgentState(issueagent.IssueStateDraft)
				state.Work = validIssueWork(true)
				return state
			},
		},
		{
			name: "ready work remains draft",
			state: func() issueagent.IssueAgentState {
				state := validIssueAgentState(issueagent.IssueStateReadyForReview)
				state.Work = validIssueWork(true)
				state.ContextDigest = issueAgentDigest("c")
				state.CandidateDigest = issueAgentDigest("d")
				state.EvidenceDigest = issueAgentDigest("e")
				return state
			},
		},
		{
			name: "completed without merged work",
			state: func() issueagent.IssueAgentState {
				return validIssueAgentState(issueagent.IssueStateCompleted)
			},
		},
		{
			name: "taken over without maintainer",
			state: func() issueagent.IssueAgentState {
				return validIssueAgentState(issueagent.IssueStateTakenOver)
			},
		},
		{
			name: "inactive state retains task",
			state: func() issueagent.IssueAgentState {
				state := validIssueAgentState(issueagent.IssueStateNeedsHuman)
				task := validTaskIdentity(issueagent.TaskKindEngineer)
				authorization := validAuthorization()
				state.Task, state.Authorization = &task, &authorization
				return state
			},
		},
		{
			name: "non-taken-over state names maintainer",
			state: func() issueagent.IssueAgentState {
				state := validIssueAgentState(issueagent.IssueStateTriaging)
				state.TakenOverBy = "maintainer"
				return state
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, issueagent.ValidateIssueAgentState(test.state()))
		})
	}
}

func TestIssueAgentStateRejectsBrokenChainAndAuthorityIdentities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*issueagent.IssueAgentState)
	}{
		{
			name: "initial state names predecessor",
			mutate: func(state *issueagent.IssueAgentState) {
				state.Sequence = 1
			},
		},
		{
			name: "successor omits predecessor",
			mutate: func(state *issueagent.IssueAgentState) {
				state.PreviousStateDigest = ""
			},
		},
		{
			name: "unknown lifecycle state",
			mutate: func(state *issueagent.IssueAgentState) {
				state.State = issueagent.IssueState("publishing")
			},
		},
		{
			name: "invalid optional digest",
			mutate: func(state *issueagent.IssueAgentState) {
				state.ContextDigest = "sha256:short"
			},
		},
		{
			name: "negative status comment identity",
			mutate: func(state *issueagent.IssueAgentState) {
				state.StatusCommentID = -1
			},
		},
		{
			name: "timestamp must be UTC",
			mutate: func(state *issueagent.IssueAgentState) {
				state.UpdatedAt = state.UpdatedAt.In(time.FixedZone("offset", 3600))
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := validIssueAgentState(issueagent.IssueStateTriaging)
			test.mutate(&state)
			require.Error(t, issueagent.ValidateIssueAgentState(state))
		})
	}

	t.Run("task requires validated authorization", func(t *testing.T) {
		t.Parallel()
		state := validIssueAgentState(issueagent.IssueStateEngineering)
		task := validTaskIdentity(issueagent.TaskKindEngineer)
		state.Task = &task
		require.Error(t, issueagent.ValidateIssueAgentState(state))

		authorization := validAuthorization()
		authorization.Permission = "read"
		state.Authorization = &authorization
		require.Error(t, issueagent.ValidateIssueAgentState(state))
	})

	t.Run("work identity is fixed to issue branch", func(t *testing.T) {
		t.Parallel()
		state := validIssueAgentState(issueagent.IssueStateCompleted)
		state.Work = validIssueWork(false)
		state.Work.Branch = "agent/issue-41"
		require.Error(t, issueagent.ValidateIssueAgentState(state))
	})
}

func TestIssueAgentStateDigestAndDecodeBindCanonicalChain(t *testing.T) {
	t.Parallel()

	want := validIssueAgentState(issueagent.IssueStateTriaging)
	body, err := json.Marshal(want)
	require.NoError(t, err)
	got, err := issueagent.DecodeIssueAgentState(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	require.Equal(t, want, got)

	digest, err := issueagent.IssueAgentStateDigest(got)
	require.NoError(t, err)
	got.Reason = "awaiting a reproducible report"
	changed, err := issueagent.IssueAgentStateDigest(got)
	require.NoError(t, err)
	require.NotEqual(t, digest, changed, "state reason must be part of the durable chain")
}
