# Send Path Dual Watermark Design

## Background

Current three-node durable send with `MinISR=2` keeps `sendack` on the critical path of leader checkpoint durability. The runtime first reaches quorum durability, then persists the leader checkpoint, then advances `HW`, then releases append waiters, and only then returns `sendack`.

The hot path today is:

1. Leader appends and syncs the message batch.
2. Followers append and sync fetched data.
3. Followers report progress.
4. Leader computes the next high watermark candidate.
5. Leader persists checkpoint state.
6. Leader advances `HW`, completes waiters, and returns `sendack`.

This design makes a single `HW` carry two different meanings at once:

- runtime-visible committed frontier
- locally persisted recovery frontier

Because those meanings are coupled, `sendack` must wait for leader-local checkpoint persistence even after quorum durability has already been achieved.

## Goal

Reach a design that allows `sendack` to return after durable quorum commit, without waiting for leader-local checkpoint persistence, while preserving crash recovery correctness and `MinISR=2` durable semantics.

## Non-Goals

- Do not weaken durable quorum commit semantics.
- Do not add a single-node-only shortcut; deployment semantics remain cluster-first.
- Do not redesign the whole replication transport or message storage format unless required by the chosen approach.
- Do not change user-facing send semantics to eventual durability.

## Current Constraint

The current recovery path treats `checkpoint.HW` as the only durable frontier. On restart, any log tail above checkpoint is truncated. Therefore, simply making checkpoint persistence asynchronous without changing recovery semantics would create a window where `sendack` has been returned but the acknowledged message is lost after leader crash.

## Options Considered

### Option A: Dual Watermark Runtime and Recovery Model

Split the current single `HW` meaning into two frontiers:

- `CommitHW`: quorum-durable committed frontier used by runtime behavior
- `CheckpointHW`: locally persisted frontier used by cold recovery

`sendack` would wait for `CommitHW` only. `CheckpointHW` would be advanced asynchronously by a coalescing checkpoint worker.

Pros:

- Removes leader-local checkpoint durability from the hot path.
- Preserves strong client semantics if recovery and election logic are updated correctly.
- Directly targets the observed bottleneck.

Cons:

- Requires recovery and leader-election semantics to be updated.
- Requires careful auditing of all places that currently assume `checkpoint.HW == committed frontier`.

### Option B: Replicated Commit Marker in the Channel Log

Introduce an explicit replicated durable commit marker so recovery can reconstruct committed frontier directly from durable replicated metadata instead of relying on local checkpoint persistence.

Pros:

- Recovery semantics become explicit.
- Avoids relying on local checkpoint for committed visibility.

Cons:

- Adds extra replicated metadata traffic and commit work.
- Risks moving the bottleneck rather than removing it.
- Larger storage and protocol surface change.

### Option C: Separate Replicated Commit Metadata Stream

Maintain a lightweight replicated metadata stream for committed frontier independent of the main message log.

Pros:

- Semantically clean separation.
- Flexible for future metadata features.

Cons:

- Largest scope.
- Operationally more complex.
- Overkill for the current bottleneck.

## Recommended Direction

Choose Option A: dual watermark runtime and recovery model.

Reasoning:

- It attacks the exact blocking point shown by pprof and block profiles.
- It keeps the current transport and message append path mostly intact.
- It avoids introducing a new replicated metadata subsystem before proving it is needed.

## Proposed Model

### Runtime Frontiers

Add the following concepts to replica state:

- `CommitHW`: highest offset known to be durably replicated by a quorum under the current epoch and `MinISR` rules
- `CheckpointHW`: highest offset whose checkpoint has been durably persisted on this node
- `CommitReady`: whether the replica has reconstructed a trustworthy committed frontier after startup or leadership transition

### Semantic Responsibilities

- `CommitHW`
  - drives append waiter completion
  - drives `sendack`
  - drives live committed reads
  - drives delivery and conversation projection eligibility
  - drives fetch responses that need the runtime committed frontier

- `CheckpointHW`
  - drives local restart lower-bound safety
  - is advanced by background checkpoint persistence
  - is not used directly for hot-path `sendack`

## Hot Path Changes

### Current Behavior

`advanceHWOnce()` persists checkpoint first, then updates `HW`, then wakes waiters.

### New Behavior

When quorum durability is reached:

1. Compute the next commit candidate.
2. Advance `CommitHW` immediately.
3. Complete append waiters waiting on that offset.
4. Return `sendack`.
5. Schedule asynchronous checkpoint persistence toward the latest `CommitHW`.
6. After checkpoint persistence succeeds, advance `CheckpointHW`.

### Checkpoint Writer Behavior

Checkpoint persistence should be last-value-wins and coalescing:

- if commit frontier advances from 101 to 102 to 103 while a checkpoint write is pending, only 103 must be durably stored
- intermediate checkpoint writes should be skipped when safe
- failure must not roll back `CommitHW`; it must trigger degraded-node handling and block unsafe leadership behavior until recovery state is re-established

## Recovery Model

This is the core of the design.

### Problem to Solve

A restarted node cannot treat `CheckpointHW` as the final committed frontier anymore, because acknowledged messages may exist above it.

### New Recovery Rules

On startup:

1. Load local `CheckpointHW`, log `LEO`, and epoch history.
2. Do not immediately truncate `[CheckpointHW, LEO)`.
3. Enter a provisional state with `CommitReady=false`.
4. Reconcile with quorum peers to determine the highest safe committed prefix.
5. Set `CommitHW` to that reconstructed value.
6. Truncate only the suffix that is not quorum-safe.
7. Persist a fresh checkpoint and advance `CheckpointHW`.
8. Mark `CommitReady=true`.

### Reconcile Requirement

The system must determine the highest safe prefix using majority-visible replicated state, not just local checkpoint state. Existing epoch and divergence logic should be reused so recovery can distinguish:

- safe tail that was quorum-durable but not checkpointed locally
- unsafe tail that only existed on a minority or stale leader

The source of truth for reconstruction is:

- the highest prefix whose offsets are confirmed by a quorum of replicas for the active epoch lineage
- constrained by epoch-divergence rules so a stale leader cannot re-expose minority-only tail data

One acceptable planning-level pseudocode definition is:

```text
candidateCommitHW =
  highest offset O such that
  quorum replicas can prove possession of prefix <= O
  and no epoch-divergence rule invalidates that prefix
```

## Leadership and Serving Rules

### Follower Startup

A recovering follower may keep data above `CheckpointHW` temporarily, but it must not advertise that tail as committed until reconcile completes.

### Leader Election

A newly elected leader must not serve committed reads or complete appends from reconstructed runtime state until `CommitReady=true` for the new epoch.

### Safety Rule

No node may expose `CommitHW` beyond what can be justified by quorum-visible replicated state for the active epoch.

## Startup and Serving State Table

| State | Meaning | Append | Committed Read / Seq Read | Fetch `CommittedSeq` / Status | Leadership Behavior |
|-------|---------|--------|----------------------------|-------------------------------|--------------------|
| `Recovering` | local store loaded, quorum frontier not reconstructed | reject | reject or serve nothing committed | report provisional / not-ready semantics only | cannot become serving leader |
| `CommitReady=false` | node has joined runtime but committed frontier is not yet trustworthy | reject new client appends | do not expose data above reconstructed safe lower bound | do not advertise runtime committed frontier as final | leader election may complete, but serving must stay gated |
| `Ready` | committed frontier reconstructed and safe | allow | use `CommitHW` | expose `CommitHW` | normal serving allowed |

### API Behavior While `CommitReady=false`

- append APIs must fail fast rather than guessing committed state
- committed-read APIs must not derive committed visibility from local `CheckpointHW`
- fetch/status/meta APIs must either expose an explicit not-ready condition or cap visibility to the safe reconstructed lower bound chosen by the implementation
- leadership transitions must not permit a newly elected leader to acknowledge writes before `CommitReady=true`

## Read Path Changes

Any live committed-read path that currently derives committed visibility from `LoadCheckpoint()` must be switched to runtime committed state.

That includes logic equivalent to:

- load single committed message by sequence
- load next/previous committed range
- fetch committed sequence bounds
- delivery or projection triggers keyed off committed visibility

`CheckpointHW` must no longer be treated as the hot-path committed source of truth.

## Failure Handling

### Checkpoint Persistence Lag

If checkpoint persistence falls behind:

- `sendack` should continue to depend on `CommitHW`, not `CheckpointHW`
- node health must track persistent checkpoint lag
- excessive lag should eventually prevent unsafe role transitions or force the node out of leadership eligibility

### Checkpoint Persistence Failure

If background checkpoint persistence repeatedly fails:

- do not revoke already acknowledged commits
- surface node health degradation
- prevent future unsafe leadership assumptions until checkpoint durability catches up or the node restarts and reconciles safely

## Testing Strategy

### Replica-Level Tests

Add and update focused tests for:

- append waiter completes when `CommitHW` advances even if checkpoint persistence is blocked
- progress ack can advance `CommitHW` independently of blocked checkpoint writes
- repeated commit advancement coalesces checkpoint writes to the latest value
- restart after acknowledged-but-not-checkpointed commit preserves quorum-safe messages
- restart after minority-only tail truncates unsafe suffix

### App-Level Integration Tests

Add multi-node tests for:

- `sendack` returns while leader checkpoint writer is blocked
- immediately crash leader after `sendack`, elect new leader, and verify acknowledged message survives
- restart old leader and verify it reconciles without losing quorum-safe acknowledged messages
- live committed reads after `sendack` observe the message without waiting for checkpoint persistence

### Read-Path Regression Tests

Add tests proving committed-read APIs use runtime committed frontier rather than local checkpoint reads.

## Implementation Boundaries

### Packages Expected to Change

Primary changes are expected in:

- `pkg/channel/replica/progress.go`
- `pkg/channel/replica/recovery.go`
- `pkg/channel/replica/replica.go`
- `pkg/channel/replica/fetch.go`
- `pkg/channel/handler/seq_read.go`
- `pkg/channel/store/checkpoint.go`

Secondary integration changes may be needed in higher-level send and app tests, but the core semantic shift should remain inside replica, store, and committed-read boundaries.

## Affected Call Sites to Audit

The first implementation plan must explicitly audit and update checkpoint-derived committed-state call sites, including at minimum:

- `pkg/channel/replica/progress.go`
- `pkg/channel/replica/recovery.go`
- `pkg/channel/replica/replica.go`
- `pkg/channel/replica/fetch.go`
- `pkg/channel/handler/seq_read.go`
- `pkg/channel/handler/fetch.go`
- `pkg/channel/handler/meta.go`
- `pkg/channel/store/checkpoint.go`
- `pkg/channel/store/commit.go`
- `pkg/channel/store/idempotency.go`

Reason:

- `handler` surfaces expose committed-read and committed-sequence semantics
- `store/commit.go` and `store/idempotency.go` currently mix checkpoint state with committed reconstruction and replay logic
- leaving any of these on old checkpoint semantics would create split-brain committed visibility during the refactor

## Risks

### Highest Risk

Recovery correctness becomes more subtle because committed frontier is no longer identical to local checkpoint frontier.

### Secondary Risks

- missed call sites still reading committed visibility from checkpoint state
- election or startup windows serving requests before `CommitReady=true`
- checkpoint lag growing without clear operational backpressure or health handling

## Mitigations

- keep the first implementation conservative: no serving committed semantics until reconcile completes
- add targeted crash-window tests before enabling the new fast-ack behavior broadly
- audit all committed-read call sites and convert them intentionally
- treat checkpoint lag as an explicit observable health metric

## Acceptance Criteria

The design is acceptable when all of the following are true:

- `sendack` no longer waits for leader-local checkpoint persistence
- acknowledged messages survive leader crash even if local checkpoint persistence was still pending at ack time
- cold restart reconciles quorum-safe tail correctly and truncates unsafe tail correctly
- committed reads after `sendack` reflect runtime committed state rather than checkpoint persistence delay
- durable quorum commit semantics remain stronger than or equal to the current external contract

## Recommendation for Planning

Plan this work as a semantic refactor, not as a small performance tweak. The first phase should lock the new safety contract with crash-window and recovery tests before implementation changes are allowed to remove checkpoint persistence from the `sendack` critical path.
