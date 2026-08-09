# Review Agent Usecase Flow

`internal/usecase/reviewagent` owns deterministic pull-request lifecycle,
command authorization, scheduler, and projection planning. It accepts only
fresh facts, protected policy, validated signed state, and current UTC time.
It performs no GitHub, Git, filesystem, shell, network, or model operation.

```text
fresh PR facts + signal + per-PR state + scheduler state
  -> ReconcilePullRequest
  -> one bounded ReconcilePlan
  -> fresh human-review state repairs projections without a model

exact comment + fresh actor permission
  -> non-command comments become observed no-ops
  -> exact @review-agent prefix -> ParseCommand
  -> review | status | explain | reconsider | retry | cancel
  -> only fresh admin authority may start or mutate model work

PR lifecycle/review/manual signal without an admin review command
  -> may cancel stale work or repair existing projections
  -> must not create a review generation or model session

validated durable decision + fresh publication facts
  -> PlanPublication
  -> status comment + formal Review + Review Agent Verdict
  -> auto-merge eligibility only for clean approved admin/member PRs
     without a human REQUEST_CHANGES Review
```

Each review infrastructure attempt receives one signed 90-minute deadline. The
single automatic retry remains in the same generation but receives a fresh
attempt deadline, so it cannot inherit a budget shorter than the protected
baseline/reviewer/publication reservation. A retry waiting in the scheduler
has no active attempt deadline; the fresh deadline starts only when its lease
is reacquired. The signed generation `StartedAt` derives the 180-minute cap;
if that cap cannot contain a complete fresh attempt, reconciliation fails
closed instead of dispatching a shortened or unbounded retry.

Only adapters execute plans. A model result never chooses a GitHub Check
conclusion directly.
