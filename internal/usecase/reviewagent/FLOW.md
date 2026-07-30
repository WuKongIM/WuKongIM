# Review Agent Usecase Flow

`internal/usecase/reviewagent` owns deterministic pull-request lifecycle,
command authorization, scheduler, and projection planning. It accepts only
fresh facts, protected policy, validated signed state, and current UTC time.
It performs no GitHub, Git, filesystem, shell, network, or model operation.

```text
fresh PR facts + signal + per-PR state + scheduler state
  -> ReconcilePullRequest
  -> one bounded ReconcilePlan

exact comment + fresh actor permission
  -> ParseCommand
  -> status | explain | reconsider | retry | cancel

validated durable decision + fresh governance facts
  -> PlanPublication
  -> status comment + formal Review + Review Agent Verdict
```

Only adapters execute plans. A model result never chooses a GitHub Check
conclusion directly.
