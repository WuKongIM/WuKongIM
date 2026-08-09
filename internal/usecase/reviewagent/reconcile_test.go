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
			name:   "opened ready PR waits for administrator",
			facts:  ready,
			signal: reviewagent.Signal{Kind: reviewagent.SignalOpened, RunID: 101},
			want:   reviewagent.ActionNoop,
			reason: "awaiting an administrator @review-agent review command",
		},
		{
			name:   "administrator review command dispatches",
			facts:  ready,
			signal: adminReviewSignal(102),
			want:   reviewagent.ActionAcquireAndDispatch,
			phase:  contract.PhaseReviewing,
		},
		{
			name: "administrator cannot start a draft review",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.Draft = true
				return facts
			}(),
			signal: adminReviewSignal(103),
			want:   reviewagent.ActionNoop,
			reason: "administrator review command requires an open, ready pull request",
		},
		{
			name: "wrong base fails closed",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.BaseRef = "release"
				return facts
			}(),
			signal: adminReviewSignal(104),
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
			signal: adminReviewSignal(105),
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
			signal: adminReviewSignal(106),
			want:   reviewagent.ActionRecordChangesRequired,
			phase:  contract.PhaseChangesRequired,
			reason: "pull request has merge conflicts",
		},
		{
			name: "oversize fails closed",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.ChangedBytes = policy.MaxChangedBytes + 1
				return facts
			}(),
			signal: adminReviewSignal(107),
			want:   reviewagent.ActionRecordInconclusive,
			phase:  contract.PhaseInconclusive,
			reason: "changed-byte budget exceeded",
		},
		{
			name: "closed PR without prior review remains untouched",
			facts: func() reviewagent.PullRequestFacts {
				facts := ready
				facts.Open = false
				return facts
			}(),
			signal: reviewagent.Signal{Kind: reviewagent.SignalClosed, RunID: 108},
			want:   reviewagent.ActionNoop,
			reason: "awaiting an administrator @review-agent review command",
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
			if test.phase != "" {
				require.Equal(t, test.phase, plan.DesiredPhase)
			}
			if test.reason != "" {
				require.Equal(t, test.reason, plan.Reason)
			}
		})
	}
}

func TestReconcilePullRequestCancelsStaleGenerationWithoutRedispatch(t *testing.T) {
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
	require.Equal(t, reviewagent.ActionCancel, plan.Action)
	require.Equal(t, old.Generation, plan.Generation)
	require.Equal(t, int64(201), plan.CancelRunID)
	require.False(t, plan.Dispatch)
	require.Empty(t, plan.NextScheduler.Active)
}

func TestReconcilePullRequestRejectsOldCompletionAfterHeadChanges(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	facts := testFacts()
	facts.HeadSHA = strings.Repeat("9", 40)
	facts.TestMergeSHA = strings.Repeat("8", 40)
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     facts,
		State:     &state,
		Scheduler: testSchedulerWithLease(state.Generation, 203, false),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalCompletion,
			RunID: 203,
			Completion: &reviewagent.Completion{
				Generation:     state.Generation,
				Decision:       contract.DecisionApproved,
				EvidenceDigest: digest("a"),
				ResultDigest:   digest("b"),
			},
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, plan.Action)
	require.Equal(t, contract.PhaseReviewing, plan.DesiredPhase)
	require.Equal(t, state.Generation, plan.Generation)
}

func TestReconcilePullRequestSupersedesOldFailedWorkerFacts(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	facts := testFacts()
	facts.HeadSHA = strings.Repeat("9", 40)
	facts.TestMergeSHA = strings.Repeat("8", 40)
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     facts,
		State:     &state,
		Scheduler: testSchedulerWithLease(state.Generation, 204, false),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalWorkerFailure,
			RunID: 204,
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionCancel, plan.Action)
	require.Equal(t, state.Generation, plan.Generation)
	require.Equal(t, int64(204), plan.CancelRunID)
}

func TestReconcilePullRequestCarriesFindingsAcrossSynchronize(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseChangesRequired
	state.DecisionSource = contract.DecisionSourceModel
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	state.PriorFindings = []contract.Finding{testFinding()}
	facts := testFacts()
	facts.HeadSHA = strings.Repeat("9", 40)
	facts.TestMergeSHA = strings.Repeat("8", 40)
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: facts, State: &state, Scheduler: testScheduler(),
		Signal: adminReviewSignal(205),
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionSupersedeAndDispatch, plan.Action)
	require.Equal(t, state.PriorFindings, plan.PriorFindings)
}

func TestReconcilePullRequestAcquiresItsPersistedQueuedGeneration(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseQueued
	scheduler, err := reviewagent.Enqueue(
		testScheduler(),
		reviewagent.QueueEntry{
			Generation: state.Generation,
			EnqueuedAt: time.Date(2026, 7, 30, 3, 30, 0, 0, time.UTC),
		},
		testPolicy().Scheduler,
	)
	require.NoError(t, err)

	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &state,
		Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalManual,
			RunID: 211,
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionAcquireAndDispatch, plan.Action)
	require.Equal(t, contract.PhaseReviewing, plan.DesiredPhase)
	require.True(t, plan.Dispatch)
	require.Equal(t, int64(211), plan.LeaseRunID)
	require.Empty(t, plan.NextScheduler.Queue)
	require.Len(t, plan.NextScheduler.Active, 1)
}

func TestReconcilePullRequestRemovesUnauthorizedSchedulerFirstWork(t *testing.T) {
	t.Parallel()

	generation := testReviewingState().Generation
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		Scheduler: testSchedulerWithLease(generation, 221, false),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalManual,
			RunID: 222,
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, plan.Action)
	require.False(t, plan.Dispatch)
	require.Equal(t, int64(221), plan.CancelRunID)
	require.Empty(t, plan.NextScheduler.Active)
}

func TestReconcilePullRequestManualRecoveryReusesActiveLease(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &state,
		Scheduler: testSchedulerWithLease(state.Generation, 231, false),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalManual,
			RunID: 232,
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, plan.Action)
	require.True(t, plan.Dispatch)
	require.Equal(t, int64(231), plan.LeaseRunID)
}

func TestReconcilePullRequestNeverAcquiresAnotherPullRequestLease(t *testing.T) {
	t.Parallel()

	older := generationForPR(7)
	scheduler, err := reviewagent.Enqueue(
		testScheduler(),
		reviewagent.QueueEntry{
			Generation: older,
			EnqueuedAt: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC),
		},
		testPolicy().Scheduler,
	)
	require.NoError(t, err)

	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		Scheduler: scheduler,
		Signal:    adminReviewSignal(241),
		Policy:    testPolicy(),
		Now:       time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionEnqueue, plan.Action)
	require.Equal(t, contract.PhaseQueued, plan.DesiredPhase)
	require.False(t, plan.Dispatch)
	require.Equal(t, int64(7), plan.NextPullRequest)
	require.Empty(t, plan.NextScheduler.Active)
	require.Len(t, plan.NextScheduler.Queue, 2)
}

func TestReconcilePullRequestCompletionWakesNextEligiblePR(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	scheduler := testSchedulerWithLease(state.Generation, 251, false)
	var err error
	scheduler, err = reviewagent.Enqueue(
		scheduler,
		reviewagent.QueueEntry{
			Generation: generationForPR(8),
			EnqueuedAt: time.Date(2026, 7, 30, 3, 30, 0, 0, time.UTC),
		},
		testPolicy().Scheduler,
	)
	require.NoError(t, err)

	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &state,
		Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalCompletion,
			RunID: 251,
			Completion: &reviewagent.Completion{
				Generation:     state.Generation,
				Decision:       contract.DecisionApproved,
				EvidenceDigest: digest("d"),
				ResultDigest:   digest("e"),
			},
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionComplete, plan.Action)
	require.Equal(t, int64(8), plan.NextPullRequest)
	require.Empty(t, plan.NextScheduler.Active)
	require.Len(t, plan.NextScheduler.Queue, 1)
}

func TestReconcilePullRequestDraftReleasesActiveLease(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.PriorFindings = []contract.Finding{testFinding()}
	state.Budget.AutomaticReviewsUsed = 1
	facts := testFacts()
	facts.Draft = true
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     facts,
		State:     &state,
		Scheduler: testSchedulerWithLease(state.Generation, 261, false),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalConvertedDraft,
			RunID: 262,
		},
		Policy: testPolicy(),
		Now:    now,
	})
	require.NoError(t, err)
	require.Equal(t, contract.PhaseAwaitingReady, plan.DesiredPhase)
	require.Equal(t, int64(261), plan.CancelRunID)
	require.Empty(t, plan.NextScheduler.Active)
	draft, err := reviewagent.BuildNextState(&state, plan, now)
	require.NoError(t, err)
	require.Equal(t, state.PriorFindings, draft.PriorFindings)
	require.Equal(t, uint32(1), draft.Budget.AutomaticReviewsUsed)

	readyPlan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &draft,
		Scheduler: plan.NextScheduler,
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalReadyForReview,
			RunID: 263,
		},
		Policy: testPolicy(),
		Now:    now.Add(time.Minute),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, readyPlan.Action)
	require.False(t, readyPlan.Dispatch)
	require.Equal(t, draft.Generation, readyPlan.Generation)
	require.Contains(t, readyPlan.Reason, "administrator")
}

func TestReconcilePullRequestIntentEditWaitsForAdministratorReview(
	t *testing.T,
) {
	t.Parallel()

	facts := testFacts()
	old := testReviewingState()
	old.Phase = contract.PhaseApproved
	old.DecisionSource = contract.DecisionSourceModel
	old.EvidenceDigest = digest("a")
	old.ResultDigest = digest("b")
	old.Generation.IntentDigest = digest("c")
	old.PriorFindings = []contract.Finding{testFinding()}
	old.Budget.AutomaticReviewsUsed = 1

	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     facts,
		State:     &old,
		Scheduler: testScheduler(),
		Signal:    reviewagent.Signal{Kind: reviewagent.SignalEdited, RunID: 301},
		Policy:    testPolicy(),
		Now:       time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, plan.Action)
	require.False(t, plan.Dispatch)
	require.Equal(t, old.Generation, plan.Generation)
	require.Contains(t, plan.Reason, "administrator")
}

func TestReconcilePullRequestReopenPreservesFindingsWithoutSecondAutomaticReview(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	decided := testReviewingState()
	decided.Phase = contract.PhaseChangesRequired
	decided.DecisionSource = contract.DecisionSourceModel
	decided.EvidenceDigest = digest("a")
	decided.ResultDigest = digest("b")
	decided.PriorFindings = []contract.Finding{testFinding()}
	decided.Budget.AutomaticReviewsUsed = 1
	closedFacts := testFacts()
	closedFacts.Open = false

	closePlan, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: closedFacts, State: &decided,
			Scheduler: testScheduler(),
			Signal: reviewagent.Signal{
				Kind: reviewagent.SignalClosed, RunID: 271,
			},
			Policy: testPolicy(), Now: now,
		},
	)
	require.NoError(t, err)
	closed, err := reviewagent.BuildNextState(&decided, closePlan, now)
	require.NoError(t, err)
	require.Equal(t, contract.PhaseClosed, closed.Phase)
	require.Equal(t, decided.PriorFindings, closed.PriorFindings)

	reopenPlan, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: testFacts(), State: &closed,
			Scheduler: closePlan.NextScheduler,
			Signal: reviewagent.Signal{
				Kind: reviewagent.SignalReopened, RunID: 272,
			},
			Policy: testPolicy(), Now: now.Add(time.Minute),
		},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, reopenPlan.Action)
	require.False(t, reopenPlan.Dispatch)
	require.Equal(t, closed.Generation, reopenPlan.Generation)
	require.Contains(t, reopenPlan.Reason, "administrator")
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

func TestReconcilePullRequestCompletesExplanationWithoutChangingVerdict(
	t *testing.T,
) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseChangesRequired
	state.DecisionSource = contract.DecisionSourceModel
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	state.Budget.ExplanationsUsed = 1
	reply := "The queue race remains because close can still overlap enqueue."
	explanationDigest := testExplanationDigest(t, state.Generation, reply)
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &state,
		Scheduler: testSchedulerWithLease(state.Generation, 411, false),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalCompletion,
			RunID: 411,
			Completion: &reviewagent.Completion{
				Generation:        state.Generation,
				ExplanationDigest: explanationDigest,
				ExplanationReply:  reply,
				ResponseBytes:     uint64(len([]byte(reply))),
			},
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionCompleteExplanation, plan.Action)
	require.Equal(t, contract.PhaseChangesRequired, plan.DesiredPhase)
	require.Equal(t, state.ResultDigest, plan.ResultDigest)
	require.Equal(t, state.EvidenceDigest, plan.ReuseEvidenceDigest)
	require.Equal(t, explanationDigest, plan.ExplanationDigest)
	require.Equal(t, reply, plan.ExplanationReply)
	require.Equal(
		t,
		uint64(len([]byte(reply))),
		plan.NextBudget.ResponseBytesUsed,
	)
	require.Empty(t, plan.NextScheduler.Active)

	next, err := reviewagent.BuildNextState(&state, plan, plan.NextScheduler.UpdatedAt)
	require.NoError(t, err)
	require.Equal(t, contract.PhaseChangesRequired, next.Phase)
	require.Equal(t, explanationDigest, next.ExplanationDigest)
	require.Equal(t, reply, next.ExplanationReply)
}

func TestReconcilePullRequestRetriesInfrastructureFailureOnce(
	t *testing.T,
) {
	t.Parallel()

	state := testReviewingState()
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &state,
		Scheduler: testSchedulerWithLease(state.Generation, 421, false),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalCompletion,
			RunID: 421,
			Completion: &reviewagent.Completion{
				Generation:            state.Generation,
				Decision:              contract.DecisionInconclusive,
				InfrastructureFailure: true,
			},
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRetryAndDispatch, plan.Action)
	require.Equal(t, contract.PhaseReviewing, plan.DesiredPhase)
	require.True(t, plan.Dispatch)
	require.Equal(t, int64(421), plan.LeaseRunID)
	require.Equal(t, uint32(1), plan.NextBudget.InfrastructureRetriesUsed)
	require.Equal(
		t,
		time.Date(2026, 7, 30, 5, 30, 0, 0, time.UTC),
		plan.DeadlineAt,
	)
	require.Empty(t, plan.EvidenceDigest)
	require.Empty(t, plan.ResultDigest)
	next, err := reviewagent.BuildNextState(
		&state,
		plan,
		time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	require.Equal(t, plan.DeadlineAt, next.SessionDeadlineAt)
}

func TestReconcilePullRequestFailsClosedAfterInfrastructureRetry(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Budget.InfrastructureRetriesUsed = 1
	state.PriorFindings = []contract.Finding{testFinding()}
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &state,
		Scheduler: testSchedulerWithLease(state.Generation, 422, false),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalCompletion,
			RunID: 422,
			Completion: &reviewagent.Completion{
				Generation:            state.Generation,
				Decision:              contract.DecisionInconclusive,
				InfrastructureFailure: true,
			},
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionComplete, plan.Action)
	require.Equal(t, contract.PhaseInconclusive, plan.DesiredPhase)
	require.Empty(t, plan.NextScheduler.Active)
	require.Contains(t, plan.Reason, "budget exhausted")
	require.Equal(t, state.PriorFindings, plan.PriorFindings)
}

func TestReconcilePullRequestRecoversFailedWorkerExactlyOnce(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	state := testReviewingState()
	scheduler := testSchedulerWithLease(state.Generation, 430, false)

	first, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), State: &state, Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind: reviewagent.SignalWorkerFailure, RunID: 430,
		},
		Policy: testPolicy(), Now: now,
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRetryAndDispatch, first.Action)
	require.True(t, first.Dispatch)
	require.Equal(t, uint32(1), first.NextBudget.InfrastructureRetriesUsed)

	state.Budget = first.NextBudget
	persisted, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: testFacts(), State: &state, Scheduler: scheduler,
			Signal: reviewagent.Signal{
				Kind: reviewagent.SignalWorkerFailure, RunID: 430,
				WorkerAttempt: 0,
			},
			Policy: testPolicy(), Now: now,
		},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, persisted.Action)
	require.True(t, persisted.Dispatch)
	require.Equal(t, int64(430), persisted.LeaseRunID)

	exhausted, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: testFacts(), State: &state, Scheduler: scheduler,
			Signal: reviewagent.Signal{
				Kind: reviewagent.SignalWorkerFailure, RunID: 430,
				WorkerAttempt: 1,
			},
			Policy: testPolicy(), Now: now,
		},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionComplete, exhausted.Action)
	require.Equal(t, contract.PhaseInconclusive, exhausted.DesiredPhase)
	require.Equal(
		t,
		contract.DecisionSourceInfrastructure,
		exhausted.DecisionSource,
	)
	require.Empty(t, exhausted.NextScheduler.Active)
}

func TestReconcilePullRequestRecoversSchedulerReleaseBeforeTerminalState(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	state := testReviewingState()
	state.SessionDeadlineAt = now.Add(time.Minute)
	state.PriorFindings = []contract.Finding{testFinding()}
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), State: &state, Scheduler: testScheduler(),
		Signal: reviewagent.Signal{
			Kind: reviewagent.SignalWorkerFailure, RunID: 433,
		},
		Policy: testPolicy(),
		Now:    now,
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRetryAndDispatch, plan.Action)
	require.True(t, plan.Dispatch)
	require.Equal(t, now.Add(90*time.Minute), plan.DeadlineAt)
	require.Equal(t, uint32(1), plan.NextBudget.InfrastructureRetriesUsed)
	require.Equal(t, state.PriorFindings, plan.PriorFindings)
	require.Len(t, plan.NextScheduler.Active, 1)
}

func TestReconcilePullRequestQueuesReleasedWorkerWithoutOldAttemptDeadline(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	state := testReviewingState()
	state.SessionDeadlineAt = now.Add(time.Minute)
	scheduler := testScheduler()
	for index := 0; index < testPolicy().Scheduler.MaxActive; index++ {
		scheduler.Active = append(scheduler.Active, reviewagent.Lease{
			Generation: generationForPR(int64(100 + index)),
			RunID:      int64(500 + index),
			AcquiredAt: now.Add(-time.Minute),
		})
	}
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), State: &state, Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind: reviewagent.SignalWorkerFailure, RunID: 437,
		},
		Policy: testPolicy(), Now: now,
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRetryAndEnqueue, plan.Action)
	require.False(t, plan.Dispatch)

	queued, err := reviewagent.BuildNextState(&state, plan, now)
	require.NoError(t, err)
	require.True(t, queued.SessionDeadlineAt.IsZero())

	available := plan.NextScheduler
	available.Active = nil
	later := now.Add(10 * time.Minute)
	resumed, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: testFacts(), State: &queued, Scheduler: available,
			Signal: reviewagent.Signal{
				Kind: reviewagent.SignalManual, RunID: 438,
			},
			Policy: testPolicy(), Now: later,
		},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionAcquireAndDispatch, resumed.Action)
	require.Equal(t, later.Add(90*time.Minute), resumed.DeadlineAt)
}

func TestReconcilePullRequestRejectsQueuedRetryWithoutFullBoundedAttempt(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	state := testReviewingState()
	state.SessionDeadlineAt = now.Add(time.Minute)
	state.PriorFindings = []contract.Finding{testFinding()}
	scheduler := testScheduler()
	for index := 0; index < testPolicy().Scheduler.MaxActive; index++ {
		scheduler.Active = append(scheduler.Active, reviewagent.Lease{
			Generation: generationForPR(int64(200 + index)),
			RunID:      int64(600 + index),
			AcquiredAt: now.Add(-time.Minute),
		})
	}
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), State: &state, Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind: reviewagent.SignalWorkerFailure, RunID: 439,
		},
		Policy: testPolicy(), Now: now,
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRetryAndEnqueue, plan.Action)
	queued, err := reviewagent.BuildNextState(&state, plan, now)
	require.NoError(t, err)

	available := plan.NextScheduler
	available.Active = nil
	later := now.Add(time.Hour)
	acquired, lease, err := reviewagent.AcquireNext(
		available,
		440,
		later,
		testPolicy().Scheduler,
	)
	require.NoError(t, err)
	require.NotNil(t, lease)
	rejected, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: testFacts(), State: &queued, Scheduler: acquired,
			Signal: reviewagent.Signal{
				Kind: reviewagent.SignalManual, RunID: 440,
			},
			Policy: testPolicy(), Now: later,
		},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRecordInconclusive, rejected.Action)
	require.Equal(t, contract.PhaseInconclusive, rejected.DesiredPhase)
	require.False(t, rejected.Dispatch)
	require.Empty(t, rejected.NextScheduler.Active)
	require.Equal(t, state.PriorFindings, rejected.PriorFindings)
}

func TestReconcilePullRequestExpiresPersistedWorkerRetry(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Budget.InfrastructureRetriesUsed = 1
	scheduler := testSchedulerWithLease(state.Generation, 434, false)
	scheduler.Active[0].AcquiredAt = time.Date(
		2026, 7, 30, 2, 0, 0, 0, time.UTC,
	)
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), State: &state, Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind:          reviewagent.SignalWorkerFailure,
			RunID:         434,
			WorkerAttempt: 0,
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionComplete, plan.Action)
	require.Equal(t, contract.PhaseInconclusive, plan.DesiredPhase)
	require.False(t, plan.Dispatch)
	require.Empty(t, plan.NextScheduler.Active)
}

func TestReconcilePullRequestDropsSchedulerFirstLeaseWithoutReviewCommand(t *testing.T) {
	t.Parallel()

	generation := testReviewingState().Generation
	scheduler := testSchedulerWithLease(generation, 435, false)
	scheduler.Active[0].AcquiredAt = time.Date(
		2026, 7, 30, 2, 0, 0, 0, time.UTC,
	)
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind: reviewagent.SignalManual, RunID: 436,
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, plan.Action)
	require.False(t, plan.Dispatch)
	require.Empty(t, plan.NextScheduler.Active)
	require.Equal(t, int64(435), plan.CancelRunID)
}

func TestReconcilePullRequestExpiresManualRecoveryAtSignedDeadline(
	t *testing.T,
) {
	t.Parallel()

	state := testReviewingState()
	scheduler := testSchedulerWithLease(state.Generation, 431, false)
	scheduler.Active[0].AcquiredAt = time.Date(
		2026, 7, 30, 2, 0, 0, 0, time.UTC,
	)
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), State: &state, Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind: reviewagent.SignalManual, RunID: 999,
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionComplete, plan.Action)
	require.Equal(t, contract.PhaseInconclusive, plan.DesiredPhase)
	require.Equal(
		t,
		contract.DecisionSourceInfrastructure,
		plan.DecisionSource,
	)
	require.Empty(t, plan.NextScheduler.Active)
}

func TestReconcilePullRequestRejectsLateExplanationCompletion(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseChangesRequired
	state.DecisionSource = contract.DecisionSourceModel
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	scheduler := testSchedulerWithLease(state.Generation, 432, false)
	scheduler.Active[0].AcquiredAt = time.Date(
		2026, 7, 30, 2, 0, 0, 0, time.UTC,
	)
	_, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), State: &state, Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind: reviewagent.SignalCompletion, RunID: 432,
			Completion: &reviewagent.Completion{
				Generation:        state.Generation,
				ExplanationDigest: digest("c"),
				ResponseBytes:     512,
			},
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.EqualError(
		t,
		err,
		"Review explanation completion exceeded its wall-time limit",
	)
}

func TestReconcilePullRequestRejectsLateGenerationResult(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	scheduler := testSchedulerWithLease(state.Generation, 423, false)
	scheduler.Active[0].AcquiredAt = time.Date(
		2026, 7, 30, 2, 30, 0, 0, time.UTC,
	)
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), State: &state, Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalCompletion,
			RunID: 423,
			Completion: &reviewagent.Completion{
				Generation:     state.Generation,
				Decision:       contract.DecisionApproved,
				EvidenceDigest: digest("a"),
				ResultDigest:   digest("b"),
			},
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionComplete, plan.Action)
	require.Equal(t, contract.PhaseInconclusive, plan.DesiredPhase)
	require.Empty(t, plan.EvidenceDigest)
	require.Empty(t, plan.ResultDigest)
	require.Empty(t, plan.NextScheduler.Active)
	require.Contains(t, plan.Reason, "wall-time")
}

func TestReconcilePullRequestSeparatesInteractionEffects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	decided := testReviewingState()
	decided.Phase = contract.PhaseChangesRequired
	decided.DecisionSource = contract.DecisionSourceModel
	decided.EvidenceDigest = digest("a")
	decided.ResultDigest = digest("b")
	decided.PriorFindings = []contract.Finding{testFinding()}

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
	require.True(t, explain.Dispatch)
	require.Equal(t, int64(702), explain.LeaseRunID)
	require.Equal(t, decided.Generation, explain.Generation)
	require.Equal(t, decided.ResultDigest, explain.ResultDigest)
	require.Equal(t, decided.Reason, explain.Reason)
	require.Equal(
		t,
		"Why is the race blocking?",
		explain.InteractionRequest,
	)

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
	require.Equal(t, decided.PriorFindings, reconsider.PriorFindings)
}

func TestReconcilePullRequestHonorsReconsiderForEligibleCurrentHead(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*reviewagent.PullRequestFacts){
		"control revision changed": func(facts *reviewagent.PullRequestFacts) {
			facts.StateParentSHA = strings.Repeat("f", 40)
		},
		"intent changed": func(facts *reviewagent.PullRequestFacts) {
			facts.IntentDigest = digest("f")
		},
		"base and test merge changed": func(facts *reviewagent.PullRequestFacts) {
			facts.BaseSHA = strings.Repeat("1", 40)
			facts.TestMergeSHA = strings.Repeat("2", 40)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			state := testReviewingState()
			state.Phase = contract.PhaseInconclusive
			state.DecisionSource = contract.DecisionSourcePolicy
			state.Reason = "Review request budget exhausted for current head"
			state.Budget.AutomaticReviewsUsed = 1

			facts := testFacts()
			mutate(&facts)
			plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
				Facts:     facts,
				State:     &state,
				Scheduler: testScheduler(),
				Signal: reviewagent.Signal{
					Kind:  reviewagent.SignalCommand,
					RunID: 706,
					Command: &reviewagent.Command{
						Kind:    reviewagent.CommandReconsider,
						Payload: "The protected model transport is repaired.",
					},
				},
				Policy: testPolicy(),
				Now:    time.Date(2026, 8, 3, 2, 20, 0, 0, time.UTC),
			})
			require.NoError(t, err)
			require.Equal(t, reviewagent.ActionReconsiderAndDispatch, plan.Action)
			require.True(t, plan.Dispatch)
			require.Equal(t, uint64(2), plan.Generation.Generation)
			require.Equal(t, facts.HeadSHA, plan.Generation.HeadSHA)
			require.Equal(t, facts.BaseSHA, plan.Generation.BaseSHA)
			require.Equal(t, facts.TestMergeSHA, plan.Generation.TestMergeSHA)
			require.Equal(t, facts.IntentDigest, plan.Generation.IntentDigest)
			require.Equal(t, facts.StateParentSHA, plan.Generation.StateParentSHA)
			require.Equal(t, uint32(1), plan.NextBudget.AutomaticReviewsUsed)
			require.Equal(t, uint32(1), plan.NextBudget.ReconsiderationsUsed)
		})
	}
}

func TestReconcilePullRequestDoesNotReconsiderIneligibleCurrentHead(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseInconclusive
	state.DecisionSource = contract.DecisionSourcePolicy
	state.Reason = "Review request budget exhausted for current head"
	state.Budget.AutomaticReviewsUsed = 1

	facts := testFacts()
	facts.StateParentSHA = strings.Repeat("f", 40)
	facts.Mergeability = reviewagent.MergeabilityConflicting
	facts.TestMergeSHA = ""
	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     facts,
		State:     &state,
		Scheduler: testScheduler(),
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalCommand,
			RunID: 707,
			Command: &reviewagent.Command{
				Kind:    reviewagent.CommandReconsider,
				Payload: "Please review the current head again.",
			},
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 8, 3, 2, 20, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRecordChangesRequired, plan.Action)
	require.Equal(t, contract.PhaseChangesRequired, plan.DesiredPhase)
	require.False(t, plan.Dispatch)
	require.Equal(t, uint32(0), plan.NextBudget.ReconsiderationsUsed)
}

func TestReconcilePullRequestBoundsAndRecoversPendingExplanation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	decided := testReviewingState()
	decided.Phase = contract.PhaseChangesRequired
	decided.DecisionSource = contract.DecisionSourceModel
	decided.EvidenceDigest = digest("a")
	decided.ResultDigest = digest("b")
	decided.PriorFindings = []contract.Finding{testFinding()}
	command := &reviewagent.Command{
		Kind:    reviewagent.CommandExplain,
		Payload: "Why is the queue race blocking?",
	}
	start, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts: testFacts(), State: &decided, Scheduler: testScheduler(),
		Signal: reviewagent.Signal{
			Kind: reviewagent.SignalCommand, RunID: 711, Command: command,
		},
		Policy: testPolicy(), Now: now,
	})
	require.NoError(t, err)
	pending, err := reviewagent.BuildNextState(&decided, start, now)
	require.NoError(t, err)
	require.Equal(t, command.Payload, pending.InteractionRequest)

	for _, duplicate := range []*reviewagent.Command{
		command,
		{
			Kind:    reviewagent.CommandExplain,
			Payload: "Explain a different concern.",
		},
		{
			Kind:    reviewagent.CommandReconsider,
			Payload: "Please review the same head again.",
		},
		{
			Kind: reviewagent.CommandRetry,
		},
	} {
		plan, reconcileErr := reviewagent.ReconcilePullRequest(
			reviewagent.ReconcileInput{
				Facts: testFacts(), State: &pending,
				Scheduler: start.NextScheduler,
				Signal: reviewagent.Signal{
					Kind:  reviewagent.SignalCommand,
					RunID: 712, Command: duplicate,
				},
				Policy: testPolicy(), Now: now.Add(time.Minute),
			},
		)
		require.NoError(t, reconcileErr)
		require.Equal(t, reviewagent.ActionNoop, plan.Action)
		require.False(t, plan.Dispatch)
	}

	recoveredPlan, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: testFacts(), State: &pending,
			Scheduler: testScheduler(),
			Signal: reviewagent.Signal{
				Kind: reviewagent.SignalManual, RunID: 713,
			},
			Policy: testPolicy(), Now: now.Add(2 * time.Minute),
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		reviewagent.ActionRetryAndDispatch,
		recoveredPlan.Action,
	)
	require.True(t, recoveredPlan.DispatchExplanation)
	require.Equal(t, pending.SessionDeadlineAt, recoveredPlan.DeadlineAt)
	require.Equal(
		t,
		uint32(1),
		recoveredPlan.NextBudget.InfrastructureRetriesUsed,
	)
	recovered, err := reviewagent.BuildNextState(
		&pending,
		recoveredPlan,
		now.Add(2*time.Minute),
	)
	require.NoError(t, err)

	exhaustedPlan, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: testFacts(), State: &recovered,
			Scheduler: recoveredPlan.NextScheduler,
			Signal: reviewagent.Signal{
				Kind:  reviewagent.SignalWorkerFailure,
				RunID: 713, WorkerAttempt: 1,
			},
			Policy: testPolicy(), Now: now.Add(3 * time.Minute),
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		reviewagent.ActionCompleteExplanation,
		exhaustedPlan.Action,
	)
	require.Empty(t, exhaustedPlan.InteractionRequest)
	require.NotEmpty(t, exhaustedPlan.ExplanationDigest)
	require.NotEmpty(t, exhaustedPlan.ExplanationReply)
	require.Empty(t, exhaustedPlan.NextScheduler.Active)
	completed, err := reviewagent.BuildNextState(
		&recovered,
		exhaustedPlan,
		now.Add(3*time.Minute),
	)
	require.NoError(t, err)
	require.Empty(t, completed.InteractionRequest)
	require.Equal(
		t,
		exhaustedPlan.ExplanationReply,
		completed.ExplanationReply,
	)
}

func TestReconcilePullRequestEnforcesSameHeadInteractionBudgets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	state := testReviewingState()
	state.Phase = contract.PhaseInconclusive
	state.DecisionSource = contract.DecisionSourceModel
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	state.Budget.ReconsiderationsUsed = 2
	state.Budget.ExplanationsUsed = 3

	tests := []reviewagent.Command{
		{Kind: reviewagent.CommandReconsider, Payload: "Try again."},
		{Kind: reviewagent.CommandExplain, Payload: "Why?"},
	}
	for _, command := range tests {
		plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
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
		require.NoError(t, err)
		require.Equal(t, reviewagent.ActionNoop, plan.Action)
		require.Contains(t, plan.Reason, "budget")
	}
}

func TestReconcilePullRequestLimitsAdministratorReviewRequestsPerHead(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	state := testReviewingState()
	state.Phase = contract.PhaseApproved
	state.DecisionSource = contract.DecisionSourceModel
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	state.Budget.AutomaticReviewsUsed = 1

	edited := testFacts()
	edited.IntentDigest = digest("e")
	exhausted, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: edited, State: &state, Scheduler: testScheduler(),
			Signal: adminReviewSignal(705),
			Policy: testPolicy(), Now: now,
		},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRecordInconclusive, exhausted.Action)
	require.Equal(t, contract.PhaseInconclusive, exhausted.DesiredPhase)
	require.False(t, exhausted.Dispatch)
	require.Equal(t, uint32(1), exhausted.NextBudget.AutomaticReviewsUsed)

	newHead := edited
	newHead.HeadSHA = strings.Repeat("9", 40)
	newHead.TestMergeSHA = strings.Repeat("8", 40)
	dispatched, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: newHead, State: &state, Scheduler: testScheduler(),
			Signal: adminReviewSignal(706),
			Policy: testPolicy(), Now: now,
		},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionSupersedeAndDispatch, dispatched.Action)
	require.True(t, dispatched.Dispatch)
	require.Equal(t, uint32(1), dispatched.NextBudget.AutomaticReviewsUsed)
}

func TestReconcilePullRequestRetryAndCancelRemainMaintainerPlans(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	inconclusive := testReviewingState()
	inconclusive.Phase = contract.PhaseInconclusive
	inconclusive.DecisionSource = contract.DecisionSourceModel
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

func TestReconcilePullRequestRepairsCanceledStateWithoutRedispatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	state := testReviewingState()
	state.Phase = contract.PhaseCanceled
	scheduler := testSchedulerWithLease(state.Generation, 706, false)

	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &state,
		Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalManual,
			RunID: 707,
		},
		Policy: testPolicy(),
		Now:    now,
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRepairProjection, plan.Action)
	require.Equal(t, contract.PhaseCanceled, plan.DesiredPhase)
	require.Equal(t, int64(706), plan.CancelRunID)
	require.Empty(t, plan.NextScheduler.Active)
	require.False(t, plan.Dispatch)
	require.False(t, plan.DispatchExplanation)
}

func TestReconcilePullRequestRepairsTerminalStateBeforeScheduler(t *testing.T) {
	t.Parallel()

	state := testReviewingState()
	state.Phase = contract.PhaseChangesRequired
	state.DecisionSource = contract.DecisionSourceModel
	state.EvidenceDigest = digest("a")
	state.ResultDigest = digest("b")
	scheduler := testSchedulerWithLease(state.Generation, 708, false)

	plan, err := reviewagent.ReconcilePullRequest(reviewagent.ReconcileInput{
		Facts:     testFacts(),
		State:     &state,
		Scheduler: scheduler,
		Signal: reviewagent.Signal{
			Kind:  reviewagent.SignalManual,
			RunID: 709,
		},
		Policy: testPolicy(),
		Now:    time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionRepairProjection, plan.Action)
	require.Empty(t, plan.NextScheduler.Active)
	require.False(t, plan.Dispatch)
}

func TestReconcilePullRequestSeparatesGovernanceRefreshFromObservation(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 6, 30, 0, 0, time.UTC)
	approved := testReviewingState()
	approved.Phase = contract.PhaseApproved
	approved.DecisionSource = contract.DecisionSourceModel
	approved.EvidenceDigest = digest("a")
	approved.ResultDigest = digest("b")

	governance, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: testFacts(), State: &approved,
			Scheduler: testScheduler(),
			Signal: reviewagent.Signal{
				Kind:  reviewagent.SignalGovernance,
				RunID: 707,
			},
			Policy: testPolicy(), Now: now,
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		reviewagent.ActionRepairProjection,
		governance.Action,
	)
	require.False(t, governance.Dispatch)

	observed, err := reviewagent.ReconcilePullRequest(
		reviewagent.ReconcileInput{
			Facts: testFacts(), State: &approved,
			Scheduler: testScheduler(),
			Signal: reviewagent.Signal{
				Kind:  reviewagent.SignalObserved,
				RunID: 708,
			},
			Policy: testPolicy(), Now: now,
		},
	)
	require.NoError(t, err)
	require.Equal(t, reviewagent.ActionNoop, observed.Action)
	require.False(t, observed.Dispatch)
}

func testPolicy() reviewagent.Policy {
	return reviewagent.Policy{
		SupportedBaseBranches:         []string{"main"},
		MaxChangedFiles:               5000,
		MaxChangedBytes:               64 << 20,
		MaxChangedLines:               200000,
		MaxGenerationDuration:         90 * time.Minute,
		MaxAutomaticReviewsPerHead:    1,
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

func adminReviewSignal(runID int64) reviewagent.Signal {
	return reviewagent.Signal{
		Kind:  reviewagent.SignalCommand,
		RunID: runID,
		Command: &reviewagent.Command{
			Kind: reviewagent.CommandReview,
		},
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
		StartedAt: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC),
	}
}

func digest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func testExplanationDigest(
	t *testing.T,
	generation contract.GenerationIdentity,
	reply string,
) string {
	t.Helper()
	resultDigest, err := contract.ExplanationResultDigest(
		contract.ExplanationResult{
			SchemaVersion: 1,
			Generation:    generation,
			Reply:         reply,
		},
	)
	require.NoError(t, err)
	return resultDigest
}

func testFinding() contract.Finding {
	return contract.Finding{
		Kind:       contract.FindingBlocking,
		Dimension:  contract.DimensionIntentCorrectness,
		Title:      "Queue close race",
		Path:       "internal/runtime/delivery/queue.go",
		LineStart:  10,
		LineEnd:    10,
		Scenario:   "Close and enqueue overlap.",
		Impact:     "A message can be lost.",
		Evidence:   []string{"diff:queue.go:10"},
		Resolution: "Serialize close and enqueue.",
	}
}
