# Issue Agent Usecase Flow

`internal/usecase/issueagent` owns the provider-neutral workflow state machine,
maintainer authorization, retry and capacity policy, scheduling, and
reconciliation. It receives current GitHub facts through narrow ports and
returns immutable typed plans. It performs no HTTP, Git, shell, filesystem,
environment, model-provider, or clock calls.

```text
current GitHub snapshot + verified Issue checkpoint + injected time
  -> authorization and command parsing
  -> legal state transition
  -> capacity and budget selection
  -> immutable Plan with exact predecessor and operation identity
  -> trusted Publisher re-read and application
```

Events are hints only. Every plan is derived from a current snapshot.
Duplicated, reordered, missing, or stale events must converge to the same plan.
One Issue generation fences all in-flight work, and checkpoint sequence remains
strictly increasing across generations.

Before authorization, deterministic Intake parses only the four required Bug
Issue Form facts. It may propose `needs-triage` or `needs-info` and a bounded
request for missing facts. It never resolves a version, runs a model or command,
creates a branch, or opens a pull request.
