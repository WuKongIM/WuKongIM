# Issue Agent Usecase Flow

`internal/usecase/issueagent` owns the provider-neutral workflow state machine,
minimal Bug intake, maintainer authorization, immutable version/reproduction
planning, diagnosis and risk policy, retry and repository-capacity accounting,
scheduling, command parsing, drift decisions, and validation planning. It
receives current GitHub facts through typed inputs and returns immutable typed
plans. It performs no HTTP, Git, shell, filesystem, environment, or
model-provider calls; time is supplied as data.

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
This includes recovering a missed PR-close event from a fresh exact
review-ready PR head and merged-state projection.
One Issue generation fences all in-flight work, and checkpoint sequence remains
strictly increasing across generations.

Before authorization, deterministic Intake parses only the four required Bug
Issue Form facts. It may propose `needs-triage` or `needs-info` and a bounded
request for missing facts. It never resolves a version, runs a model or command,
creates a branch, or opens a pull request.

Every Worker lease is fenced by repository, Issue, generation, sequence,
checkpoint digest, operation ID, phase, exact source SHAs, instruction and
prompt digests, path/argv allowlists, and resource bounds. Repository admission
fails closed when the complete bounded `ready-for-agent` inventory cannot be
verified, three leases are active, one heavy lease is active, or the rolling
24-hour reservation reaches 24 worker-hours.

The remediation path requires a signed causal diagnosis. Protected Agent paths
are always human-only; other high-risk classes require a fresh exact
`/agent approve-risk` authorization before a fix lease. Expired leases return
to their last durable phase boundary and consume bounded infrastructure retry
budget; they never accept late output.

Exact maintainer commands are planned from freshly checked permission and
current GitHub facts. Revision, cancellation, review repair, head adoption,
backport tracking, and signed chain recovery all advance generation. Review
repair freezes unresolved thread IDs; CI failures create at most two new fix
leases before returning to a human.
