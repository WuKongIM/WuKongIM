# ChannelV2 Lifecycle Simplification Design

**Date:** 2026-05-24
**Status:** Design approved by user, pending implementation plan

## Overview

Refactor `pkg/channelv2` runtime lifecycle handling without changing its current
semantics.

The current channel runtime already has the necessary safety rules: metadata
fencing, append cancellation of idle eviction, follower stop acknowledgements,
leader checkpointing, and final eviction checks. The problem is that these rules
are spread across reactor handlers, scheduler helpers, replication callbacks,
and lifecycle helpers. This makes the lifecycle hard to explain and hard to
test, especially around idle eviction and concurrent appends.

This design keeps the protocol behavior and makes the lifecycle explicit. The
reactor remains the single writer. Blocking I/O still goes through worker
pools. The refactor introduces a small lifecycle model that consumes event
snapshots and produces actions for the reactor to execute.

## Goals

- Preserve current channelv2 semantics.
- Make runtime lifecycle phases explicit and minimal.
- Keep ordinary append, fetch, pull, apply, ACK, and HW behavior unchanged.
- Centralize idle eviction, follower stop, leader checkpoint, and final recheck
  decisions.
- Make lifecycle guards testable without running the full reactor.
- Keep the implementation incremental and avoid a rewrite of the replication
  pipeline.

## Non-Goals

- Removing follower stop or leader idle eviction semantics.
- Replacing the reactor mailbox model.
- Replacing `machine.ChannelState` or the append queue.
- Changing wire protocol DTOs in `pkg/channelv2/transport`.
- Changing production integration outside `pkg/channelv2`.
- Introducing a bypass around cluster semantics. A single node remains a
  single-node cluster.

## Current Pain Points

- `lifecycle.Phase` is not the real source of truth. Several states are inferred
  from timers and auxiliary booleans.
- Leader lifecycle logic is split across `lifecycle.go`,
  `replication_runtime.go`, `scheduler.go`, and `reactor.go`.
- Follower stop lifecycle is mixed with ordinary pull/apply/ack retry logic.
- Eviction safety requires understanding append reservations, append submit
  sequences, waiters, pull waiters, checkpoints, and replication state at once.
- Most lifecycle tests must exercise reactor internals because there is no
  small pure model to test.

## Target Architecture

```text
service facade
  -> reactor group / mailbox routing
    -> runtimeChannel
      -> machine.ChannelState
      -> appendQueue
      -> replicationState
      -> runtimeLifecycle
    -> worker effects
```

Responsibilities:

- `machine.ChannelState`: durable log, append/fetch validation, LEO/HW, quorum
  completion, and metadata application.
- `appendQueue`: admitted append batching and store append flush timing.
- `replicationState`: ordinary follower pull/apply/ack pipeline and retries.
- `runtimeLifecycle`: runtime loading role, idle slow down, follower stop,
  checkpointing, final recheck, and runtime eviction decisions.
- `reactor`: serializes events, builds lifecycle views, applies lifecycle
  actions, submits worker tasks, and completes futures.

The lifecycle model must not call stores, transports, futures, or mailboxes
directly. It only returns decisions.

## Runtime Lifecycle Model

### Loaded vs Unloaded

`Unloaded` is not represented inside `runtimeChannel`. A channel is unloaded
when the owning reactor has no `channels[key]` entry. Loading remains driven by
`ApplyMeta` or lazy metadata resolution before append or pull hint activation.

### Metadata Reload

`Reloading` is also not a long-lived runtime state. When `ApplyMeta` changes the
metadata fence, the reactor synchronously:

```text
fence stale waiters
  -> reset lifecycle and replication transient state
  -> apply new metadata to machine.ChannelState
  -> choose leader or follower lifecycle by local role
  -> schedule the next due event
```

The lifecycle model emits reset and scheduling decisions, but the reactor still
executes waiter completion and state mutation.

## Lifecycle Phases

Only states that change control flow are stored. Time-derived concepts such as
cooling are guards, not phases.

### Leader Phases

```text
Serving
StoppingFollowers
Checkpointing
FinalRecheck
```

`Serving` includes hot and idle-but-not-evicting leader runtime. Pull slow down
is derived from `IdleSlowdownAfter` and `IdleSince`, not from a stored phase.

### Follower Phases

```text
Replicating
StopCheckpointing
StopAcking
```

Ordinary pull/apply/ack retry state stays in `replicationState`. The follower
lifecycle only owns the stop-and-evict path.

## Runtime View

The reactor builds an immutable view before asking the lifecycle model for a
decision.

```go
type RuntimeView struct {
    Key             ch.ChannelKey
    Role            ch.Role
    Status          ch.Status
    LEO             uint64
    HW              uint64
    ActivityVersion uint64
    IdleSince       time.Time
    PendingWork     PendingWorkView
    Followers       []FollowerView
    AppendFence     AppendFenceView
}
```

`PendingWorkView` summarizes waiters, append queue state, in-flight append,
pull waiters, replication in-flight work, checkpoint work, and retry timers.

`AppendFenceView` contains append reservation count and append submit sequence.
It is only meaningful during leader final recheck.

The view allows guards such as `CanOfferFollowerStop`,
`AllFollowersCaughtUp`, `AllFollowersStopped`, and `SafeToEvict` to be tested as
pure functions.

## Events

Lifecycle events align with existing reactor handlers.

```text
MetaApplied
AppendAdmitted
AppendStored
FollowerPullServed
FollowerAcked
PullHintReceived
PullResultReceived
StopCheckpointDone
LeaderCheckpointDone
FinalRecheck
Tick
```

Event rules:

- `MetaApplied` resets stale lifecycle state if the metadata fence changed.
- `AppendAdmitted` cancels any leader eviction attempt immediately.
- `AppendStored` updates activity and can request pull hints for lagging
  followers.
- `FollowerPullServed` may return `PullControlStop` only when the leader is
  idle-expired, no runtime work is pending, HW covers LEO, the follower is
  caught up, and the request is for `LEO + 1`.
- `FollowerAcked` advances leader-side follower progress and may move the
  leader toward checkpointing when all followers stopped for the current
  activity version.
- `PullHintReceived` cancels obsolete follower stop state and resumes ordinary
  replication.
- `PullResultReceived` can move a follower into stop checkpointing only when
  the leader returned `PullControlStop` and local LEO/HW are caught up.
- `StopCheckpointDone` sends a stopped ACK or retries checkpointing.
- `LeaderCheckpointDone` moves to final recheck only if the activity version is
  still current and all eviction guards still pass.
- `FinalRecheck` is the only event allowed to evict a leader runtime.
- `Tick` drives idle checks and retry timers.

## Actions

The lifecycle model returns actions for the reactor to apply.

```text
ScheduleLifecycle
ScheduleReplication
SendPullHint
StartFollowerStopCheckpoint
SendStoppedAck
StartLeaderCheckpoint
QueueLeaderFinalRecheck
EvictRuntime
ResetEviction
FailStaleWaiters
```

Action application stays in the reactor layer because actions may need worker
pools, futures, mailbox priority, or store handles.

## Leader Transitions

```text
Serving
  -- append admitted/stored --> Serving
  -- idle expired && HW==LEO && followers caught up && no pending work
     --> StoppingFollowers

StoppingFollowers
  -- follower pull at LEO+1 --> offer PullControlStop
  -- all followers stopped ACK current ActivityVersion --> Checkpointing
  -- append admitted or metadata fence --> Serving

Checkpointing
  -- checkpoint ok and guards still pass --> FinalRecheck
  -- checkpoint failed or backpressured --> Checkpointing retry
  -- append admitted or metadata fence --> Serving

FinalRecheck
  -- append reservation exists --> retry final recheck later
  -- append submit sequence changed --> queue another final recheck
  -- no pending work and append sequence unchanged --> EvictRuntime
  -- append admitted or metadata fence --> Serving
```

Important invariants:

- `ActivityVersion` fences follower stopped ACKs and leader checkpoints.
- New append activity resets stop offers, stopped ACK state, and checkpoint
  readiness for stale activity.
- A leader runtime may be evicted only from `FinalRecheck`.
- The final recheck must run at normal priority so same-channel appends that
  were already submitted can be observed before eviction.

## Follower Transitions

```text
Replicating
  -- PullControlContinue + records --> apply -> ack -> Replicating
  -- PullControlContinue + no records --> park until NextPullAfter
  -- PullControlStop && local LEO/HW caught up && no replication inflight
     --> StopCheckpointing

StopCheckpointing
  -- checkpoint ok --> StopAcking
  -- checkpoint failed or backpressured --> StopCheckpointing retry
  -- newer PullHint or metadata fence --> Replicating

StopAcking
  -- stopped ACK ok --> EvictRuntime
  -- stopped ACK stale metadata --> Replicating
  -- stopped ACK retryable error --> StopAcking retry
```

Important invariants:

- A follower must not accept stop control while pull, apply, ack, or checkpoint
  work is in flight.
- A follower must not unload until its stopped ACK for the matching activity
  version has reached the leader.
- Newer pull hints cancel stale stopping state.

## Package Layout

Add focused lifecycle files under `pkg/channelv2/reactor`:

```text
lifecycle_model.go       - pure types: phases, events, actions, views
lifecycle_leader.go      - leader lifecycle transitions
lifecycle_follower.go    - follower stop lifecycle transitions
lifecycle_apply.go       - reactor action execution helpers
runtime_snapshot.go      - runtimeChannel -> RuntimeView
lifecycle_test.go        - pure model tests
```

Existing files remain but lose lifecycle-specific decisions:

- `reactor.go`: mailbox handler dispatch and high-level event application.
- `replication_runtime.go`: ordinary pull/apply/ack flow.
- `scheduler.go`: due heap and rescheduling, not lifecycle semantics.
- `lifecycle.go`: either removed or reduced to compatibility wrappers during
  migration.

## Migration Plan

1. Introduce lifecycle types and `RuntimeView` without changing behavior.
2. Move pure guard functions into the lifecycle model and preserve current
   names through wrappers if useful.
3. Move leader idle eviction decisions behind lifecycle events and actions.
4. Move follower stop/checkpoint/stopped-ACK decisions behind lifecycle events
   and actions.
5. Remove unused or misleading phases such as cooling if they are fully derived
   by time guards.
6. Update `FLOW.md` after code and tests reflect the new lifecycle structure.

## Testing Strategy

### Pure Lifecycle Tests

- Leader append admission cancels checkpoint and final recheck state.
- Leader idle with caught-up followers enters follower stopping.
- Stopped ACK with stale activity version does not advance leader eviction.
- Leader checkpoint success with changed activity version does not evict.
- Final recheck with append reservation does not evict.
- Final recheck with changed append sequence queues another final recheck.
- Follower ignores `PullControlStop` when local LEO/HW are behind.
- Follower stopped ACK success emits runtime eviction.
- Metadata fence resets leader and follower lifecycle state.

### Reactor Integration Tests

- Single-node cluster idle leader runtime can evict.
- Multi-replica followers stop and ACK before leader eviction.
- Concurrent append during leader final recheck does not lose the append.
- Metadata leader change fences stale checkpoint and worker results.
- Pull hint after follower stop offer resumes replication for newer activity.

## Risks

- Moving lifecycle logic may accidentally change timing. Mitigation: migrate in
  small steps and keep existing reactor tests passing at each step.
- Pure lifecycle actions can become too broad. Mitigation: keep actions limited
  to observable side effects and keep data mutation in the reactor.
- Snapshot construction can hide state if it is incomplete. Mitigation: make
  `PendingWorkView` explicit and cover every field currently checked by
  `hasPendingRuntimeWork` and `safeToEvictRuntime`.

## Acceptance Criteria

- Current channelv2 append, fetch, replication, idle eviction, and cancellation
  tests pass.
- New lifecycle tests cover leader and follower phase transitions.
- `FLOW.md` describes the explicit lifecycle model and matches code.
- Public `channelv2.Cluster` behavior is unchanged.
- No new bypass around cluster semantics is introduced.
