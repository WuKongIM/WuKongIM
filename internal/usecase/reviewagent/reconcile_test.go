package reviewagent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestReconcilePullRequestEventMatrix(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	policy := testPolicy()
	ready := testFacts()
	openSlots := testScheduler()

	tests := []struct {
		name   string
		facts  reviewagent.PullRequestFacts
		state  *contract.ReviewState
		signal reviewagent.Signal
		want   reviewagent.PlanAction
		phase  contract.Phase
		reason string
	}{
		{
			name:   "opened ready PR dispatches",
			facts:  ready,
			signal: reviewagent.Signal{Kind: reviewagent.SignalOpened, RunID: 101},
			want:   reviewagent.ActionAcquireAndDispatch,
			phase:  contract.PhaseReviewing,
		},
		{
			name: "draft waits without model",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.Draft = true
				return facts
			}(),
			signal: reviewagent.Signal{Kind: reviewagent.SignalOpened, RunID: 102},
			want:   reviewagent.ActionAppendState,
			phase:  contract.PhaseAwaitingReady,
		},
		{
			name: "wrong base fails closed",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.BaseRef = "release"
				return facts
			}(),
			signal: reviewagent.Signal{Kind: reviewagent.SignalOpened, RunID: 103},
			want:   reviewagent.ActionRecordInconclusive,
			phase:  contract.PhaseInconclusive,
			reason: "unsupported base branch",
		},
		{
			name: "missing test merge fails closed",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.TestMergeSHA = ""
				return facts
			}(),
			signal: reviewagent.Signal{Kind: reviewagent.SignalOpened, RunID: 104},
			want:   reviewagent.ActionRecordInconclusive,
			phase:  contract.PhaseInconclusive,
			reason: "test-merge revision is unavailable",
		},
		{
			name: "merge conflict fails closed",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.Mergeability = reviewagent.MergeabilityConflicting
				return facts
			}(),
			signal: reviewagent.Signal{Kind: reviewagent.SignalOpened, RunID: 105},
			want:   reviewagent.ActionRecordInconclusive,
			phase:  contract.PhaseInconclusive,
			reason: "pull request is not cleanly mergeable",
		},
		{
			name: "oversize fails closed",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.ChangedBytes = policy.MaxChangedBytes + 1
				return facts
			}(),
			signal: reviewagent.Signal{Kind: reviewagent.SignalOpened, RunID: 106},
			want:   reviewagent.ActionRecordInconclusive,
			phase:  contract.PhaseInconclusive,
			reason: "changed-byte budget exceeded",
		},
		{
			name: "closed PR records closure",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.Open = false
				return facts
			}(),
			signal: reviewagent.Signal{Kind: reviewagent.SignalClosed, RunID: 107},
			want:   reviewagent.ActionAppendState,
			phase:  contract.PhaseClosed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := reviewagent.ReconcilePullRequest(
				reviewagent.ReconcileInput{
					Facts:     test.facts,
					State:     test.state,
					Scheduler: openSlots,
					Signal:    test.signal,
					Policy:    policy,
					Now:       now,
				},
			)
			require.NoError(t, err)
			require.Equal(t, test.want, plan.Action)
			require.Equal(t, test.phase, plan.DesiredPhase)
			if test.reason != "" {
				require.Equal(t, test.reason, plan.Reason)
			}
		})
	}
}

func TestReconcilePullRequestSupersedesStaleGeneration(t *testing.T) {
	t.Parallel()

	facts := testFacts()
	old := testReviewingState()
	old.Generation.HeadSHA = strings.Repeat("9", 40)
	old.Generation.TestMergeSHA = strings.Repeat("8", 40)

	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     facts,
		State:     &old,
		Scheduler: testSchedulerWithLease(old.Generation, 201, false),
		Signal:    reviewagent.Signal{Kind: reviewagent.SignalSynchronize, RunID: 202},
		Policy:    testPolicy(),
		Now:       time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionSupersedeAndDispatch, plan.Action)
	require.Equal(t, uint64(2), plan.Generation.Generation)
	require.Equal(t, facts.HeadSHA, plan.Generation.HeadSHA)
	require.Equal(t, int64(201), plan.CancelRunID)
	require.True(t, plan.Dispatch)
}

func TestReconcilePullRequestReusesExactEvidenceAfterIntentEdit(t *testing.T) {
	t.Parallel()

	facts := testFacts()
	old := testReviewingState()
	old.Phase = contract.PhaseApproved
	old.EvidenceDigest = digest("a")
	old.ResultDigest = digest("b")
	old.Generation.IntentDigest = digest("c")

	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     facts,
		State:     &old,
		Scheduler: testScheduler(),
		Signal:    reviewagent.Signal{Kind: reviewagent.SignalEdited, RunID: 301},
		Policy:    testPolicy(),
		Now:       time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionSupersedeAndDispatch, plan.Action)
	require.Equal(t, digest("a"), plan.ReuseEvidenceDigest)
	require.Equal(t, uint64(2), plan.Generation.Generation)
}

func TestReconcilePullRequestRejectsStaleCompletion(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	completionGeneration := state.Generation
	completionGeneration.HeadSHA = strings.Repeat("9", 40)

	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &state,
		Scheduler: testSchedulerWithLease(state.Generation, 401, false),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalCompletion,
			RunID: 401,
			Completion: &reviewagent.Completion{
				Generation:     completionGeneration,
				Decision:       contract.DecisionApproved,
				EvidenceDigest: digest("d"),
				ResultDigest:   digest("e"),
			},
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, plan.Action)
	require.Equal(t, "stale completion", plan.Reason)
}

func TestReconcilePullRequestSeparatesInteractionEffects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	decided := testReviewingState()
	decided.Phase = contract.PhaseChangesRequired
	decided.EvidenceDigest = digest("a")
	decided.ResultDigest = digest("b")

	status, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &decided,
		Scheduler: testScheduler(),
		Signal: reviewagent.Signal{
			Kind:    reviewagent.SignalCommand,
			RunID:   701,
			Command: &reviewagent.Command{Kind: reviewagent.CommandStatus},
		},
		Policy: testPolicy(),
		Now:    now,
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRespondStatus, status.Action)
	require.NotEmpty(t, status.StatusBody)
	require.False(t, status.Dispatch)
	require.False(t, status.DispatchExplanation)

	explain, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &decided,
		Scheduler: testScheduler(),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalCommand,
			RunID: 702,
			Command: &reviewagent.Command{
				Kind:    reviewagent.CommandExplain,
				Payload: "Why is the race blocking?",
			},
		},
		Policy: testPolicy(),
		Now:    now,
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionExplain, explain.Action)
	require.True(t, explain.DispatchExplanation)
	require.False(t, explain.Dispatch)
	require.Equal(t, decided.Generation, explain.Generation)
	require.Equal(t, decided.ResultDigest, explain.ResultDigest)

	reconsider, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts:     testFacts(),
			State:     &decided,
			Scheduler: testScheduler(),
			Signal: reviewagent.Signal{
				Kind:  reviewagent.SignalCommand,
				RunID: 703,
				Command: &reviewagent.Command{
					Kind:    reviewagent.CommandReconsider,
					Payload: "The queue is guarded by the lifecycle lock.",
				},
			},
			Policy: testPolicy(),
			Now:    now,
		},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionReconsiderAndDispatch, reconsider.Action)
	require.True(t, reconsider.Dispatch)
	require.Equal(t, uint64(2), reconsider.Generation.Generation)
	require.Equal(t, decided.Generation.HeadSHA, reconsider.Generation.HeadSHA)
	require.Equal(t, decided.EvidenceDigest, reconsider.ReuseEvidenceDigest)
}

func TestReconcilePullRequestEnforcesSameHeadInteractionBudgets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	state := testReviewingState()
	state.Phase = contract.PhaseInconclusive
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	state.Budget.ReconsiderationsUsed = 2
	state.Budget.ExplanationsUsed = 3

	tests := []reviewagent.Command{
		{Kind: reviewagent.CommandReconsider, Payload: "Try again."},
		{Kind: reviewagent.CommandExplain, Payload: "Why?"},
	}
	for _, command := range tests {
		_, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
			Facts:     testFacts(),
			State:     &state,
			Scheduler: testScheduler(),
			Signal: reviewagent.Signal{
				Kind:    reviewagent.SignalCommand,
				RunID:   704,
				Command: &command,
			},
			Policy: testPolicy(),
			Now:    now,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "budget")
	}
}

func TestReconcilePullRequestRetryAndCancelRemainMaintainerPlans(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	inconclusive := testReviewingState()
	inconclusive.Phase = contract.PhaseInconclusive
	inconclusive.EvidenceDigest = digest("a")
	inconclusive.ResultDigest = digest("b")

	retry, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &inconclusive,
		Scheduler: testScheduler(),
		Signal: reviewagent.Signal{
			Kind:    reviewagent.SignalCommand,
			RunID:   705,
			Command: &reviewagent.Command{Kind: reviewagent.CommandRetry},
		},
		Policy: testPolicy(),
		Now:    now,
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRetryAndDispatch, retry.Action)
	require.True(t, retry.Dispatch)

	reviewing := testReviewingState()
	cancel, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &reviewing,
		Scheduler: testSchedulerWithLease(reviewing.Generation, 706, false),
		Signal: reviewagent.Signal{
			Kind:    reviewagent.SignalCommand,
			RunID:   706,
			Command: &reviewagent.Command{Kind: reviewagent.CommandCancel},
		},
		Policy: testPolicy(),
		Now:    now,
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionCancel, cancel.Action)
	require.Equal(t, contract.PhaseCanceled, cancel.DesiredPhase)
	require.Empty(t, cancel.NextScheduler.Active)
}

func testPolicy() reviewagent.Policy {
	return reviewagent.Policy{
		SupportedBaseBranches:         []string{"main"},
		MaxChangedFiles:               5000,
		MaxChangedBytes:               64 << 20,
		MaxChangedLines:               200000,
		MaxReconsiderationsPerHead:    2,
		MaxInfrastructureRetries:      1,
		MaxExplanationSessionsPerHead: 3,
		MaxExplanationResponseBytes:   256 << 10,
		Scheduler: reviewagent.SchedulerLimits{
			MaxActive:            3,
			MaxPerPullRequest:    1,
			MaxFirstTimeExternal: 1,
		},
	}
}

func testFacts() reviewagent.PullRequestFacts {
	return reviewagent.PullRequestFacts{
		Repository:        "WuKongIM/WuKongIM",
		PullRequest:       42,
		BaseRef:           "main",
		HeadSHA:           strings.Repeat("a", 40),
		BaseSHA:           strings.Repeat("b", 40),
		TestMergeSHA:      strings.Repeat("c", 40),
		IntentDigest:      digest("d"),
		StateParentSHA:    strings.Repeat("e", 40),
		Open:              true,
		Mergeability:      reviewagent.MergeabilityClean,
		ChangedFiles:      4,
		ChangedBytes:      4096,
		ChangedLines:      80,
		AuthorLogin:       "contributor",
		AuthorAssociation: "CONTRIBUTOR",
	}
}

func testReviewingState() contract.ReviewState {
	return contract.ReviewState{
		SchemaVersion: 1,
		Generation: contract.GenerationIdentity{
			Repository:     "WuKongIM/WuKongIM",
			PullRequest:    42,
			HeadSHA:        strings.Repeat("a", 40),
			BaseSHA:        strings.Repeat("b", 40),
			TestMergeSHA:   strings.Repeat("c", 40),
			IntentDigest:   digest("d"),
			Generation:     1,
			StateParentSHA: strings.Repeat("e", 40),
		},
		Sequence:  1,
		Phase:     contract.PhaseReviewing,
		Reason:    "reviewing",
		UpdatedAt: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC),
	}
}

func digest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
