# ChannelV2 Lifecycle Controller Design

**Date:** 2026-05-29
**Status:** Design approved by user, pending implementation plan

## Overview

Refactor `pkg/channelv2/reactor` channel runtime lifecycle into a single
per-channel lifecycle controller without changing the external replication
protocol.

The current lifecycle behavior is useful and should stay: idle leaders offer
`PullControlStop`, followers checkpoint before sending a stopped ACK, leaders
checkpoint after all followers stop, and final leader eviction is fenced behind
append submission. The complexity comes from state ownership. The same stop and
eviction story is currently represented across `channelLifecycle`,
`runtimeLifecycle`, `followerLifecycle`, `replicationState`, scheduler helpers,
and worker completion handlers.

This design keeps append and replication hot-path semantics intact while moving
all stop, checkpoint, stopped ACK, pull-hint retry, and eviction state into one
controller owned by `runtimeChannel`.

This design supersedes the narrower 2026-05-24 lifecycle simplification design
for implementation planning. The older design kept leader and follower phases
separate; this design uses one lifecycle stage and one effect model.

## Goals

- Preserve current `Pull`, `PullHint`, `PullControlStop`, and stopped `Ack`
  protocol semantics.
- Preserve the single-reactor-writer model and existing worker pools.
- Keep ordinary append, pull, apply, ordinary ACK progress, and HW completion
  behavior unchanged.
- Move follower stop/checkpoint/stopped ACK state out of `replicationState`.
- Replace leader/follower lifecycle phase duplication with one lifecycle stage
  plus role checks from `machine.ChannelState`.
- Make lifecycle progress observable from one state object: stage, version,
  effect fences, retry times, and next due time.
- Keep final leader eviction fenced by append reservations and submit sequence.

## Non-Goals

- No wire protocol redesign.
- No removal of stopped ACK semantics.
- No shortcut for single-node deployments; a single node remains a single-node
  cluster.
- No replacement of `machine.ChannelState`, append queues, mailboxes, or worker
  pools.
- No broad rewrite of service, store, transport, or testkit packages.
- No migration, retention, snapshot install, or leader repair changes.

## Current Pain Points

- `replicationState` mixes hot follower replication with cold stop lifecycle:
  pull/apply/ordinary ACK fields live next to `stopping`, stop checkpoint,
  stopped ACK, and delete-after-ack state.
- Leader eviction is split between `channelLifecycle`, `runtimeLifecycle`,
  `followerLifecycle`, `scheduler.go`, and
  `leader_lifecycle_runtime.go`.
- Worker completion routing must know whether a checkpoint belongs to leader
  eviction or follower stop by checking unrelated state fields.
- A new append must reset stop offers, stopped followers, checkpoint readiness,
  pull-hint state, and explicit phases in several places.
- Tests often assert private boolean combinations instead of a small lifecycle
  state.

## Target Ownership

`runtimeChannel` keeps two separate runtime concerns:

```go
type runtimeChannel struct {
    state *machine.ChannelState

    // replication owns hot follower pull, apply, ordinary ACK, parking, and retry.
    replication replicationState

    // lifecycle owns stop, checkpoint, stopped ACK, pull-hint retry, and eviction.
    lifecycle channelRuntimeLifecycle
}
```

After the refactor, `replicationState` should only describe hot replication
work:

- pull RPC in flight and retry time
- pending pull response and store-apply in flight
- ordinary ACK in flight or retry
- parked follower delay
- latest accepted leader activity hint
- replication backoff and diagnostic error

The lifecycle controller owns:

- idle activity timestamps
- current activity version
- leader-side follower stop and pull-hint state
- follower-side accepted stop state
- leader and follower checkpoint effects
- stopped ACK effect
- final leader eviction recheck
- lifecycle due scheduling

## Lifecycle Stage

Use one lifecycle stage. The local role still comes from `rc.state.Role`.

```go
type lifecycleStage uint8

const (
    lifecycleLive lifecycleStage = iota + 1

    lifecycleLeaderStoppingFollowers
    lifecycleLeaderCheckpointing
    lifecycleLeaderReadyToEvict

    lifecycleFollowerCheckpointing
    lifecycleFollowerStoppedAcking
    lifecycleFollowerReadyToEvict
)
```

`lifecycleLive` means the runtime is loaded and serving its current role. It
covers hot, idle, and parked behavior. Idle slowdown remains derived from time
guards, not a stored stage.

Metadata fences and new append activity return the controller to
`lifecycleLive` and clear lifecycle effects that are no longer valid for the
current activity version.

## Controller State

The controller stores stage, activity, per-effect fences, and leader-visible
follower lifecycle state.

```go
type channelRuntimeLifecycle struct {
    stage lifecycleStage

    loadedAt     time.Time
    lastAppendAt time.Time
    version      uint64

    checkpoint lifecycleEffect
    stoppedAck  lifecycleEffect
    finalCheck  lifecycleEffect

    followers map[ch.NodeID]lifecycleFollower
    nextDue   time.Time
}

type lifecycleEffect struct {
    inflight bool
    opID     ch.OpID
    version  uint64
    retryAt  time.Time
    queued   bool
}

type lifecycleFollower struct {
    match uint64

    stopOfferedVersion uint64
    stoppedVersion     uint64

    hint       lifecycleEffect
    parked     bool
    lastPullAt time.Time
}
```

The exact Go names can change during implementation, but the important rule is
that lifecycle effects use one shape: in flight, fenced op id, activity
version, retry time, and queued status when a normal-priority mailbox event is
pending.

## Event Boundary

All lifecycle-related state changes should enter one driver:

```go
func (r *Reactor) driveLifecycle(rc *runtimeChannel, ev lifecycleEvent, now time.Time)
```

The driver:

1. Applies the event to lifecycle state.
2. Builds a runtime snapshot with pending work and append fence state.
3. Plans the next lifecycle actions.
4. Applies actions by submitting worker tasks, queueing mailbox events,
   scheduling due items, or evicting the runtime.

Lifecycle events:

```text
metaFence
appendAdmitted
appendStored
idleTick
leaderPullObserved
leaderStoppedAckReceived
followerStopControlReceived
storeCheckpointDone
stoppedAckDone
finalEvictReady
pullHintDone
```

The driver must be the only code path that mutates lifecycle stage or lifecycle
effect state.

## Planning Rules

Planning should be small and mostly pure. It should receive a view of the
runtime and return lifecycle actions.

```text
snapshot + event -> lifecycle plan -> reactor effects
```

Core actions:

```text
scheduleLifecycle
scheduleReplication
sendPullHint
startFollowerStopCheckpoint
sendStoppedAck
startLeaderCheckpoint
queueLeaderFinalRecheck
evictRuntime
resetLifecycle
```

Blocking store and RPC work still goes through worker pools. The planner never
calls stores, transports, mailboxes, or futures directly.

## Leader Flow

Leader runtime stays live until idle eviction guards pass:

```text
lifecycleLive
  -> idle expired, HW == LEO, all followers caught up, no pending work
  -> lifecycleLeaderStoppingFollowers
```

While stopping followers, the leader does not proactively send a stopped RPC.
It returns `PullControlStop` when a caught-up follower pulls at `LEO + 1`.
The lifecycle follower entry records the offered activity version.

When stopped ACKs arrive:

```text
leaderStoppedAckReceived
  -> validate activity version and match offset
  -> mark follower stopped for lifecycle.version
  -> if all followers stopped and no pending work, start leader checkpoint
```

After checkpoint success:

```text
lifecycleLeaderCheckpointing
  -> checkpoint done with current version and guards still true
  -> lifecycleLeaderReadyToEvict
  -> queue normal-priority final recheck
```

Final recheck is the only leader path allowed to evict:

```text
finalEvictReady
  -> append reservation exists: retry later
  -> append submit sequence changed: queue another final recheck
  -> pending work exists: retry later
  -> otherwise evict runtime
```

## Follower Flow

Ordinary follower replication remains in `replicationState`. A stop control
response is handed to lifecycle only after the pull result is validated.

```text
followerStopControlReceived
  -> local role is follower and status active
  -> local LEO >= leader LEO
  -> local HW >= leader HW
  -> no hot replication work blocks stopping
  -> lifecycleFollowerCheckpointing
  -> start follower checkpoint
```

If the follower is behind or has blocking hot replication work, lifecycle
returns to `lifecycleLive` and schedules ordinary replication.

After follower checkpoint success:

```text
lifecycleFollowerCheckpointing
  -> lifecycleFollowerStoppedAcking
  -> send stopped ACK with activity version
```

After stopped ACK success:

```text
lifecycleFollowerStoppedAcking
  -> lifecycleFollowerReadyToEvict
  -> evict runtime when no pending work remains
```

A newer pull hint or metadata fence cancels follower stop state and resumes
ordinary replication. A stale-meta stopped ACK result also cancels stop state
and marks the follower dirty for immediate pulling.

## Pull Hints

Pull hints remain best-effort lifecycle effects on the leader side. A follower
needs a pull hint when it trails leader LEO and is parked, stopped, stop
offered, or has not pulled yet.

The hint effect should be per follower and versioned:

- one hint in flight per follower
- newer activity while a hint is in flight records a pending version
- successful old hint completion sends the current hint if still needed
- failed hint completion schedules retry only while progress is still needed
- stopped ACK or caught-up pull retires obsolete hint effects

## Runtime Snapshot

The lifecycle planner needs a snapshot, not direct access to every runtime
field. The snapshot should include:

- local role and status
- LEO, HW, generation, epoch, leader epoch
- activity version and idle-since time
- leader-visible follower match, offered version, stopped version, hint effect
- pending append, waiter, pull waiter, replication, checkpoint, ACK, and final
  check work
- append reservation count and submit sequence for final recheck

The snapshot should replace ad hoc guard helpers where practical. Guards such
as `canOfferStop`, `allFollowersStopped`, `safeToEvict`, and
`followerStopBlocked` should be testable from the snapshot.

## Safety Invariants

- A stale worker completion must not mutate current lifecycle state.
- Store checkpoint and stopped ACK completions must match channel key,
  generation, epoch, leader epoch, op id, and activity version.
- A leader may return `PullControlStop` only for a caught-up follower requesting
  `LEO + 1` while idle eviction guards pass.
- A stopped ACK must match the current activity version and leader LEO.
- A new append cancels leader eviction and clears stop state for followers that
  need new data.
- Final leader eviction must remain fenced by append reservations and append
  submit sequence.
- Runtime eviction must only happen when pending work is empty for the relevant
  role and stage.

## Implementation Slices

1. Add the lifecycle controller types, snapshot helpers, and pure guard tests
   while keeping existing behavior.
2. Move follower stop/checkpoint/stopped ACK state from `replicationState` into
   lifecycle. Keep ordinary ACK state in replication.
3. Route stopped ACK submission and completion through lifecycle effects.
4. Move leader checkpoint/final recheck state into lifecycle effects.
5. Move pull-hint per-follower state into lifecycle follower entries.
6. Replace direct phase and boolean assertions in tests with stage/effect
   assertions.
7. Update `pkg/channelv2/FLOW.md` and `pkg/channelv2/reactor/FLOW.md` after
   code behavior and names settle.

## Test Plan

Focused unit tests:

- snapshot guard tests for stop offer, follower stop acceptance, and safe
  eviction
- lifecycle planning tests for leader idle to final eviction
- lifecycle planning tests for follower stop checkpoint to stopped ACK
- stale checkpoint and stale stopped ACK completion tests
- append-admitted cancellation tests
- pull-hint versioning and retry tests

Existing reactor tests should continue to cover:

- single-node cluster leader idle checkpoint and eviction
- follower stop checkpoint, stopped ACK, and reload
- stopped ACK stale metadata cancellation
- leader stopped ACK validation
- final recheck append reservation and submit sequence fencing
- append after stop offer sends pull hint immediately

Run targeted tests during implementation:

```bash
go test ./pkg/channelv2/reactor -count=1
go test ./pkg/channelv2/... -count=1
```

## Risks

- The lifecycle controller can become a large imperative function if planning
  and effects are not kept separate.
- Existing tests assert many old private fields; updating them will be noisy.
- Missing one fence field on checkpoint or stopped ACK completion could allow a
  stale result to affect the current runtime.
- Moving pull-hint state late in the refactor may temporarily leave lifecycle
  ownership split; implementation slices should keep intermediate states small.

## Review Notes

The intended simplification is not fewer lifecycle states. The simplification
is one owner for lifecycle truth. Hot replication answers "how do I fetch and
apply records?" Lifecycle answers "when can this loaded runtime stop or unload?"
