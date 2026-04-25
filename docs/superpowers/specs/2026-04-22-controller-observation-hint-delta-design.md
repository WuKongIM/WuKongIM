# Controller Observation Hint + Delta Design

**Date:** 2026-04-22
**Status:** Draft for review

## Background

After the first idle-CPU optimization round, the main steady-state waste is no longer
per-slot `get_task` reads or local controller self-RPC. Fresh runtime evidence on the
three-node docker-compose cluster shows:

- idle CPU still stays around `4.5% ~ 7%` per node, sometimes spiking higher
- steady-state controller RPC deltas are still dominated by:
  - `list_assignments`
  - `list_nodes`
  - `list_runtime_views`
  - `list_tasks`
- follower CPU profiles now spend a meaningful share of idle cost in:
  - `pkg/cluster.(*Cluster).observeOnce`
  - `pkg/cluster.(*slotAgent).SyncAssignments`
  - `pkg/transport.(*MuxConn).readLoop`
  - `pkg/transport.(*priorityWriter).loop`
- controller-leader profiles still show periodic control-plane handler work in:
  - `pkg/cluster.(*Cluster).handleControllerRPC`
  - `pkg/cluster.(*controllerHandler).Handle`
  - metadata snapshot clone / list paths

The current architecture is functionally correct, but `pkg/cluster/cluster.go`
still binds several different responsibilities to one fixed-frequency controller
observation loop:

- assignment sync
- local reconcile
- migration progress observe
- leader planner tick

Even when the cluster is idle and no assignments or tasks change, followers keep
pulling the controller leader at a `200ms` cadence and the leader keeps serving
those reads. This makes idle CPU scale with observation frequency rather than
with actual cluster change rate.

## Goals

- Reduce steady-state idle CPU without materially slowing cluster convergence
- Keep the practical convergence target at roughly `<= 1s` after assignment/task/runtime changes
- Preserve current cluster semantics and fail-closed safety behavior
- Keep controller leader as the source of truth
- Avoid introducing any single-node-only bypass semantics
- Reuse as much of the current controller / observation / reconciler structure as possible

## Non-goals

- This round does not redesign planner decision rules
- This round does not redesign reconcile task semantics
- This round does not require mixed-version rolling-upgrade compatibility
- This round does not replace all polling with a pure event bus
- This round does not remove low-frequency safety polling entirely

## Problem Statement

Today the control-plane main loop is effectively:

1. follower periodically calls `RefreshAssignments`
2. follower then runs reconcile
3. reconcile periodically reads nodes / runtime views / tasks
4. leader periodically runs planner from full control-plane state

This means the steady-state cost is still driven by repeated "ask if anything
changed" traffic instead of actual changes. The result is:

- unnecessary transport read/write syscalls
- repeated controller handler entry
- repeated metadata snapshot cloning or store fallback
- repeated reconcile scans over unchanged local state

The design problem is not that polling is always wrong. The design problem is that
the current system mixes:

- fast-path convergence triggers
- safety revalidation
- migration-specific work
- leader planner recomputation

into one always-hot loop.

## Proposed Architecture

Adopt a hybrid design:

- **leader actively emits best-effort observation hints**
- **followers wake up and fetch one batched delta**
- **followers reconcile from the updated local cache**
- **a low-frequency slow sync loop remains as the correctness backstop**

This keeps correctness on the existing "leader is source of truth + follower pull
confirmation" model, while moving steady-state convergence away from fixed-period
multi-read polling.

### 1. Split the current controller observation loop by responsibility

Replace the current single hot observation loop with separate loops:

- `heartbeatLoop`
  - unchanged in purpose
  - still reports node heartbeat and hash slot table version

- `runtimeObservationLoop`
  - unchanged in purpose
  - still reports runtime-view deltas to the controller leader

- `wakeReconcileLoop` (new)
  - follower-side fast path
  - consumes leader hints or local dirty signals
  - fetches one observation delta and runs reconcile

- `slowSyncLoop` (new)
  - follower-side low-frequency fallback
  - validates that hints were not lost and revisions did not drift forever
  - should run in the multi-second range, not `200ms`

- `migrationProgressLoop` (new split)
  - only runs at high frequency when active migrations exist
  - remains mostly idle otherwise

- `leaderPlannerLoop` (new behavior)
  - leader computes decisions on dirty-trigger + short debounce
  - still keeps a low-frequency safety tick

### 2. Introduce best-effort `ObservationHint`

When the controller leader observes a meaningful control-plane change, it sends a
small best-effort hint to followers.

Suggested fields:

```go
type ObservationHint struct {
    LeaderID         uint64
    LeaderGeneration uint64

    AssignmentRevision uint64
    TaskRevision       uint64
    NodeRevision       uint64
    RuntimeRevision    uint64

    Reason        ObservationHintReason
    AffectedSlots []uint32
    NeedFullSync  bool
    SentAt        time.Time
}
```

Hint properties:

- not durable
- not replayed
- may be dropped
- may be duplicated
- may be superseded by newer hints
- cannot be part of correctness assumptions

Its only job is to wake the follower quickly.

### 3. Replace multiple `list_*` reads with one batched `FetchObservationDelta`

Followers should stop fetching `assignments`, `nodes`, `runtime_views`, and `tasks`
via separate steady-state reads after every wake-up.

Instead, add one controller read RPC:

```go
type ObservationDeltaRequest struct {
    LeaderID         uint64
    LeaderGeneration uint64

    AssignmentRevision uint64
    TaskRevision       uint64
    NodeRevision       uint64
    RuntimeRevision    uint64

    RequestedSlots []uint32
    ForceFullSync  bool
}

type ObservationDeltaResponse struct {
    LeaderID         uint64
    LeaderGeneration uint64

    AssignmentRevision uint64
    TaskRevision       uint64
    NodeRevision       uint64
    RuntimeRevision    uint64

    FullSync bool

    Assignments []controllermeta.SlotAssignment
    Tasks       []controllermeta.ReconcileTask
    Nodes       []controllermeta.ClusterNode
    RuntimeViews []controllermeta.SlotRuntimeView

    DeletedTasks       []uint32
    DeletedRuntimeSlots []uint32
}
```

The leader may return:

- a true incremental delta
- a targeted full snapshot
- a full snapshot fallback when revision gaps are too large or leader state changed

### 4. Add explicit control-plane revisions

The controller leader should maintain monotonic revisions for the main read models:

- `assignmentRevision`
- `taskRevision`
- `nodeRevision`
- `runtimeRevision`

Revision bumps happen when the corresponding read model changes in a way that can
affect reconcile or planner behavior.

This lets followers answer "do I need to fetch anything?" cheaply and lets the
leader answer "can I send incremental data?" deterministically.

### 5. Make follower reconcile wake-driven, not timer-driven

Follower behavior becomes:

1. receive `ObservationHint`
2. compare revisions with local applied revisions
3. if something newer exists, enqueue wake
4. `wakeReconcileLoop` performs one in-flight `FetchObservationDelta`
5. update local caches
6. reconcile only the affected local slots when possible
7. if scope cannot be narrowed safely, fall back to reconciling all local assignments

Follower local state should keep:

- latest applied revisions
- a coalesced "wake pending" flag
- latest observed hint
- a singleflight/in-flight fetch guard

### 6. Make leader planner dirty-driven

The leader planner should stop depending mainly on a hot periodic full observation loop.

Instead, the leader marks planner dirty on:

- assignment changes
- reconcile task changes
- node health changes
- runtime report changes
- task result reports
- migration state changes
- controller leader acquisition

Dirty events pass through a short debounce window, then trigger planner evaluation.

A low-frequency safety tick remains so that planner progress still recovers even if
a dirty signal is missed.

## Detailed Data Flow

### Steady-state idle

1. node heartbeat continues at low frequency
2. runtime reports continue only when runtime view delta exists or full-sync interval fires
3. no leader hint is emitted because no control-plane revision changed
4. followers do not perform hot-path `list_*` reads
5. `slowSyncLoop` runs every few seconds as safety validation only

Expected result:

- idle controller RPC count is driven mostly by heartbeat and rare safety syncs
- `list_assignments/list_nodes/list_runtime_views/list_tasks` disappear from the hot idle path

### Assignment or task changed

1. leader updates metadata and bumps the corresponding revision
2. leader marks planner/reconcile dirty
3. leader emits an `ObservationHint`
4. follower wake loop fetches one observation delta
5. follower updates caches and runs reconcile quickly

### Runtime observation changed

1. node runtime report reaches leader
2. leader updates runtime observation state and bumps `runtimeRevision`
3. leader emits an `ObservationHint` to relevant followers, or all followers if scope is unclear
4. wake-driven reconcile fetches one delta and re-evaluates affected local slots

### Hint lost

1. follower never receives hint
2. no correctness issue occurs
3. low-frequency `slowSyncLoop` eventually notices revision mismatch
4. follower fetches delta or full snapshot and reconciles

### Leader changed

1. old leader hints become stale immediately
2. follower rejects hints whose `LeaderID` / `LeaderGeneration` do not match the current controller leader
3. follower requests delta from the new leader
4. if revision lineage does not match, leader forces `FullSync=true`

## Failure Semantics and Safety Guards

### 1. Hint is acceleration only

Hints must never be required for correctness.

If hints are dropped, duplicated, delayed, or reordered:

- convergence may be delayed
- correctness must remain intact

### 2. Leader identity guard

Hints and deltas must carry:

- `LeaderID`
- `LeaderGeneration`

Followers must reject stale hints and stale delta responses from old leaders.

### 3. Revision-gap fallback

If follower state is too old or revision history cannot be served incrementally, the
leader must force a full-sync response rather than attempting to replay a long chain
of deltas.

### 4. Follower singleflight

Only one delta fetch should run at a time on a follower.

If more hints arrive while one fetch is in flight:

- record the newest observed revisions
- record that another wake is pending
- avoid queueing an arbitrary number of redundant sync jobs

### 5. Reconcile latest state only

The follower should reconcile toward the latest known state, not replay every hint
in order. This keeps convergence state-based rather than event-log-based.

### 6. Planner safety tick

Even after moving the leader planner to dirty-trigger + debounce, keep a low-frequency
safety tick so missed dirty notifications cannot stall planning indefinitely.

### 7. Migration-specific isolation

Migration progression should retain high responsiveness only while there is active
migration work. Idle clusters should not pay that cost continuously.

## Protocol and Implementation Shape

### Follower-side changes

Add a follower-local wake controller, for example:

```go
type observationWakeState struct {
    mu sync.Mutex

    fetching bool
    pending  bool

    latestHint ObservationHint

    assignmentRevision uint64
    taskRevision       uint64
    nodeRevision       uint64
    runtimeRevision    uint64
}
```

Responsibilities:

- accept hints
- coalesce duplicate wake requests
- drive one in-flight delta fetch
- update local revisions after successful apply

### Leader-side changes

The controller leader needs:

- revision bookkeeping
- hint emission
- delta building from current metadata and observation snapshots
- scope narrowing by `AffectedSlots` when safe

This should prefer current leader-local snapshots and only fall back to the store
when snapshots are not ready or marked dirty.

### Reconcile cache apply

Followers should maintain read-model caches that can be updated incrementally:

- assignments cache
- tasks cache
- nodes cache
- runtime-view cache

The delta-apply code should:

- upsert changed entities
- delete tombstoned entities
- update local revisions only after successful apply

## Rollout Plan

Use staged rollout instead of a single rewrite.

### Stage 1: Split the current hot loop

Refactor the current `observerLoop` responsibilities into separate loops without
changing read protocol yet.

Purpose:

- reduce coupling
- create clear insertion points for wake-up and slow-sync logic

### Stage 2: Add `ObservationHint`

Introduce leader-to-follower hints first, but keep the existing follower read path
behind the wake-up.

Purpose:

- prove that wake-driven convergence reduces idle control-plane traffic
- limit risk before introducing a new batched read RPC

### Stage 3: Add `FetchObservationDelta`

Once wake-driven convergence is stable, replace the steady-state `list_*` burst with
one batched delta fetch.

Purpose:

- collapse multiple reads into one read
- reduce controller handler churn
- reduce transport read/write idle cost further

### Stage 4: De-rate hot safety polling

After hint + delta is proven stable, lower the old hot polling cadence into a true
slow-sync safety loop.

Purpose:

- preserve correctness fallback
- remove the remaining idle CPU tax from hot periodic control-plane reads

## Testing Strategy

### Unit tests

- stale hint is rejected after leader change
- duplicate hints coalesce into one delta fetch
- revision gap forces full-sync fallback
- delta apply handles upsert + tombstone correctly
- follower singleflight does not queue unbounded duplicate wake work
- planner dirty debounce still produces decisions

### Integration tests

- assignment change wakes follower and starts reconcile within target window
- task creation wakes follower and results in task execution/reconcile
- lost hint still converges through slow sync
- leader switch invalidates old hints and recovers on new leader
- active migration keeps fast progress path while idle cluster sleeps

### Runtime verification

Re-run the three-node docker-compose idle scenario and verify:

- 5-second deltas of `list_assignments`, `list_nodes`, `list_runtime_views`, and `list_tasks` fall materially
- `get_task` remains near zero in steady state
- idle CPU drops below the current post-optimization baseline
- pprof shows reduced hot-path cost in:
  - `pkg/cluster.(*Cluster).observeOnce`
  - `pkg/cluster.(*controllerHandler).Handle`
  - `pkg/transport.(*MuxConn).readLoop`
  - `pkg/transport.(*priorityWriter).loop`

## Alternatives Considered

### 1. Keep polling and only add backoff

This is the lowest-risk option, but it still leaves follower-driven steady-state
reads as the main architecture. It likely reduces idle CPU only partially.

### 2. Pure event-driven design

This could reduce idle cost further, but it introduces much more complexity in:

- event durability
- replay
- leader-switch behavior
- missed-event recovery
- operational reasoning

That is not justified for this round.

### 3. Push full snapshots from leader

This would reduce follower pull cost but makes the leader responsible for much more
delivery-state management. It also complicates correctness semantics more than a
pull-confirmed delta approach.

## Recommendation

Adopt the hybrid hint + delta design.

It best matches the target of:

- materially lower idle CPU
- no meaningful loss of convergence responsiveness
- preserving current controller correctness model
- allowing staged rollout and measurable A/B verification
