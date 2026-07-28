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
This includes checking every active Agent work head and PR projection at
execution/publication/reconciliation boundaries, routing exact pending
Publisher effects and a unique current-lease Worker Artifact back to their
identity-verifying Publisher, and recovering a missed PR-close event from a
fresh exact review-ready `base=main` PR projection.
Artifact publication uses one typed boundary plan: structural work-object
drift wins over head identity, a changed head wins over reversible Draft
projection, and only a success Artifact with a pending commit may enter exact
App-commit identity recovery.
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

Moving-main planning accepts only the current exact Agent head and three
same-assertion passes from one trusted validation run. It closes only the Agent
Draft PR when main already passes. Conflict planning permits one signed
mechanical-rebase attempt counter. The effect binds an independently computed
exact merge tree and deterministic message; GitHub creates an App-authored,
signed commit whose parent is current main, then atomically swaps the Agent ref
only when its exact prior OID still matches. A
semantic or repeated conflict returns a typed human-queue decision without
overwriting an external head. Active-work external heads are recorded in the
signed Work state before human handoff. Missing, closed-unmerged, or retargeted
work objects use a separate human transition, while reversible Draft projection
drift uses projection repair. Exact adopt-head commands resume at Draft-PR
creation, diagnosis, or validation according to preserved facts.
Terminal state label projection removes
`ready-for-agent`, preventing the all-state recovery inventory from retaining
completed Issues; reconciliation separately repairs an interrupted label
projection from the durable checkpoint.
