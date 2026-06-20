# Slot Raft Core / Apply Pipeline Design

## Context

The current slot Multi-Raft runtime drives Raft input, ticking, Ready
persistence, message sending, FSM apply, applied-index marking, and maintenance
from the same slot worker loop.

The hot path today is roughly:

```text
processRequests
  -> processControls
  -> processTick
  -> processReady
       -> persist Ready
       -> send Raft messages
       -> apply committed entries to the slot FSM
       -> MarkApplied
       -> Advance
       -> status refresh and compaction checks
```

This keeps the implementation compact, but it couples Raft liveness to business
apply latency. During the three-node `wkcli sim` smoke run, foreground sends
continued, while slot propose pressure, conversation active flush retries, and
slow slot apply produced visible slot leader instability in the monitor. The
apply gap was not the dominant signal, but seconds-level apply latency was
enough to make the shared worker miss timely Raft work.

Single-node deployment remains a single-node cluster. This design must not add a
business branch that bypasses cluster semantics.

## Problem

Raft proposals should not block Raft heartbeats. More generally, slow business
apply must not delay the Raft control path:

- inbound Raft messages,
- ticks,
- heartbeat responses,
- leader lease maintenance,
- Ready persistence and outbound Raft messages,
- membership and snapshot barriers.

The current worker loop can spend too much time inside FSM apply, applied-index
storage, or maintenance after entries are already committed. That work is
necessary for correctness and `SENDACK` completion, but it does not need to
consume the same scheduling budget as heartbeats.

## Goals

- Keep Raft core liveness independent from business FSM apply latency.
- Preserve the existing cluster-only deployment semantics, including
  single-node cluster behavior.
- Preserve `SENDACK` semantics: a foreground proposal future resolves only after
  the committed entry has been applied and the durable applied index has been
  marked.
- Preserve Raft correctness for normal entries, configuration changes, snapshot
  restore, restart replay, and compaction.
- Introduce explicit proposal admission and quality-of-service classes so
  background projections cannot starve foreground sends.
- Keep the first migration compatible with the current `multiraft.Runtime`
  callers where practical.
- Build for large production shapes: 100k-member groups, many active channels,
  and many online users.

## Non-Goals

- Do not treat election timeout tuning as the long-term fix.
- Do not bypass the slot Raft path for local or single-node deployments.
- Do not rewrite `channelv2`, `controllerv2`, or the gateway send pipeline as
  part of this change.
- Do not make background projections, such as conversation active flushes,
  part of the foreground `SENDACK` boundary.
- Do not introduce a broad generic service framework around Raft.

## Architecture

Split each slot runtime into a Raft core and an apply pipeline:

```text
RaftCore
  owns RawNode and Raft storage interaction
  handles Step, Tick, Propose, Ready persistence, outbound Raft messages
  hands committed work to ApplyPipeline

ApplyPipeline
  owns ordered business apply per slot
  applies committed entries to the slot FSM
  marks durable applied indexes
  resolves proposal futures after apply success
```

Only `RaftCore` may call `RawNode`. The RawNode remains single-thread owned to
avoid data races and subtle ordering bugs. The split moves slow business apply
out of the core loop, not into a second RawNode caller.

## Raft Core

`RaftCore` should process a small set of prioritized mailboxes:

- High priority: inbound Raft `Step` messages and local ticks.
- High priority: membership, snapshot, and shutdown controls.
- Normal priority: foreground local proposals.
- Low priority: background proposals and maintenance-triggered proposals.

The core loop budget should be bounded. A heavy stream of local proposals must
not prevent the core from draining inbound Raft messages or ticking.

The Ready path is split into control-path work and apply-path work:

```text
Ready
  -> persist log entries, HardState, and snapshot metadata
  -> send outbound Raft messages after persistence
  -> hand committed entries to ApplyPipeline
  -> Advance for normal committed entries after durable handoff
```

For normal entries, the handoff boundary is durable log persistence plus enqueue
into the apply pipeline. The apply pipeline can be in-memory because committed
but unapplied entries are replayable from the Raft log after restart using the
durable applied index.

The initial implementation should preserve the current external API:

```text
Runtime.Propose(...)
Runtime.Step(...)
Runtime.Stop(...)
```

Internally, these calls enqueue to the owning `RaftCore` instead of executing
directly on the old monolithic worker.

## Apply Pipeline

The apply pipeline must preserve per-slot ordering. Two implementation options
are acceptable:

- one goroutine per active slot,
- a sharded worker pool with one serial mailbox per slot.

The recommended production shape is a sharded worker pool with per-slot
single-flight execution. This avoids unbounded goroutine growth if the slot
count grows, while still guaranteeing one in-flight apply task per slot.

For each slot, apply order is:

```text
committed entries in index order
  -> decode existing slot FSM commands
  -> apply through the current BatchStateMachine
  -> MarkApplied after successful FSM commit
  -> resolve proposal futures
```

The proposal future must not resolve at Raft commit time. It resolves after
business apply and durable applied-index marking. If apply fails, the future
returns the apply error even though the Raft log entry was committed. Recovery
then follows the existing replay contract.

## Barrier Handling

Normal entries may be handed off and advanced independently of business apply.
Snapshot restore and configuration changes need stronger barriers.

### Snapshot Restore

Snapshot restore is rare but correctness-critical:

```text
RaftCore
  -> persist snapshot metadata
  -> enqueue snapshot restore barrier
  -> wait for ApplyPipeline to restore FSM and applied index
  -> Advance after restore completes
```

The core may continue draining inbound messages while the barrier is pending,
but it must not let later committed entries apply before the snapshot restore is
complete.

### Configuration Change

Configuration changes also form a barrier:

```text
ApplyPipeline
  -> apply all prior normal entries
  -> report conf-change entry ready

RaftCore
  -> ApplyConfChange on the owning RawNode goroutine
  -> update in-memory storage view and membership state
  -> release subsequent entries
```

This keeps `RawNode.ApplyConfChange` single-thread owned and prevents a later
configuration change from crossing an earlier one.

### Compaction

Compaction should leave the Raft control hot path:

- automatic compaction becomes a maintenance task gated by durable applied
  index,
- manual compaction enters the high-priority control mailbox but schedules IO
  outside the tick path where possible,
- compaction must never hold the core loop long enough to delay ticks or
  inbound Raft messages.

## Proposal Admission

Add explicit proposal classes:

- Foreground: user send, channel metadata required to complete a user-facing
  operation, and other work that decides synchronous caller success.
- Background: conversation active projection, repair, refresh, telemetry, and
  maintenance-derived writes.

Each slot should track admission limits:

- pending foreground proposal count,
- pending background proposal count,
- pending proposal bytes,
- uncommitted Raft log bytes,
- apply backlog entries and bytes,
- oldest queued apply age,
- core tick delay.

When the slot is under pressure:

1. throttle or reject background proposals first,
2. preserve a small foreground reserve,
3. return typed backpressure errors to callers,
4. make background workers retry with jittered backoff and fresh route
   resolution.

This prevents a projection backlog from turning into foreground send failures
or Raft heartbeat starvation.

## Conversation Active Behavior

Conversation active flushes are background projection work. They should remain
coalesced and retryable:

- dirty rows stay in the local cache until successfully flushed,
- `not_leader`, `stale_route`, and route-not-ready responses trigger fresh
  route resolution,
- background backpressure triggers jittered backoff,
- repeated failures must not hot-loop against the slot propose path,
- the foreground proposal budget is never consumed by conversation active
  flushes.

This accepts temporary projection lag under load. The user-visible send boundary
stays the foreground channel/slot commit boundary, not conversation projection
completion.

## Recovery Model

The recovery model is:

```text
Ready persisted to Raft log
  -> committed entries handed to ApplyPipeline
  -> applied index marked only after FSM apply success
  -> restart replays committed entries above applied index
```

Because the Raft log is durable before handoff, the apply queue does not need to
be durable. If a node crashes after handoff but before apply, restart replays
the committed unapplied entries.

FSM commands must remain idempotent or replay-safe at the applied-index
boundary. The implementation should audit existing slot FSM commands before the
split lands. The durable applied index is the source of truth for replay, not
the in-memory proposal future state.

Live proposal futures may fail on shutdown. That does not lose committed work:
committed but unapplied entries are still recovered from Raft log replay.

## Observability

Add metrics that separate Raft core liveness from business apply latency:

- `wukongim_slot_raft_core_loop_duration_seconds`
- `wukongim_slot_raft_tick_delay_seconds`
- `wukongim_slot_ready_persist_duration_seconds`
- `wukongim_slot_ready_send_duration_seconds`
- `wukongim_slot_apply_queue_depth`
- `wukongim_slot_apply_backlog_entries`
- `wukongim_slot_apply_backlog_bytes`
- `wukongim_slot_apply_oldest_age_seconds`
- `wukongim_slot_apply_barrier_wait_seconds`
- `wukongim_slot_proposal_admission_total{class,result}`
- `wukongim_slot_background_throttle_total{source}`

The monitor's slot leader stability card should also distinguish real leader
changes from local leader-unknown recovery. A transition from a known leader to
another known leader is a leader change. A transition through unknown should be
reported separately so the UI can explain observation gaps without overstating
elections.

## Testing Strategy

Add targeted tests before and during the refactor:

- Slow apply does not block Raft heartbeats: inject an FSM apply delay longer
  than the old election budget and assert the leader remains stable.
- Proposal futures wait for apply: assert a committed proposal future is not
  resolved until FSM apply and `MarkApplied` complete.
- Conf-change barrier: assert entries after a configuration change do not apply
  before `ApplyConfChange` is processed on the Raft core goroutine.
- Snapshot barrier: assert restore blocks later applies and advances only after
  the FSM restore path completes.
- Restart replay: crash after Ready handoff but before apply, restart, and
  verify committed unapplied entries are replayed from the Raft log.
- Background throttling: saturate apply backlog, assert conversation active
  flushes receive retryable backpressure while foreground proposals retain
  reserved capacity.
- Three-node smoke: run the existing `wkcli sim` smoke flow and assert send
  errors remain zero, core tick delay stays bounded, and slot leader changes are
  near zero after warmup.

Unit tests should keep simulated time short. Long wall-clock smoke scenarios
belong in integration or script-level validation.

## Migration Plan

1. Add observability around the current monolithic path: core loop duration,
   tick delay, Ready phase durations, and apply latency. This locks the baseline
   and helps prove the split.
2. Introduce `ApplyPipeline` behind the existing `multiraft.Runtime` API for
   normal entries only. Keep snapshot and configuration changes on the old
   synchronous path until their barriers are implemented.
3. Move normal committed-entry apply to the pipeline and make proposal futures
   resolve from apply completion.
4. Add snapshot and configuration-change barriers, then remove the old
   synchronous special cases.
5. Add proposal admission classes and background throttling. Convert
   conversation active flushes to low-priority retryable proposals.
6. Move compaction and maintenance out of the Raft core hot loop.
7. Update package `FLOW.md` files after behavior changes land.

Each phase should be independently testable and should avoid broad API churn
outside `pkg/slot/multiraft` until the runtime boundary is stable.

## Acceptance Criteria

- Artificially slow slot FSM apply no longer causes Raft heartbeat starvation
  or leader churn.
- Foreground proposal futures still resolve only after committed business apply
  and durable applied-index marking.
- Background projection lag is visible and throttled instead of consuming the
  foreground proposal budget.
- Restart after Ready handoff replays committed unapplied entries correctly.
- Snapshot restore and configuration changes preserve Raft ordering and
  membership correctness.
- The three-node `wkcli sim` smoke workload keeps `send_errors` at zero and
  shows stable slot leadership after warmup.
- No code path bypasses cluster semantics for local or single-node-cluster
  deployments.

## Open Design Decisions

### Apply Worker Shape

Use a sharded apply worker pool with per-slot serial mailboxes. This is more
predictable than one goroutine per slot if slot count grows, and it still gives
active slots independent apply scheduling.

### Advance Timing

For normal entries, allow `RawNode.Advance` after durable Ready persistence and
apply-queue handoff. Keep snapshot and configuration changes as explicit
barriers. This matches the goal of keeping the core moving while preserving the
ordering rules that actually require core and apply synchronization.

This decision must be validated against the exact etcd/raft Ready contract used
by the current dependency before implementation lands.

### Background Error Contract

Introduce typed retryable errors for background callers:

- `ErrProposalBackpressure`
- `ErrBackgroundProposalThrottled`
- `ErrApplyBacklogHigh`

Foreground callers should see backpressure only after foreground reserves are
exhausted. Background callers should treat these errors as normal retry signals,
not as permanent projection failures.
