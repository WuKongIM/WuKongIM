# Channel Leader Stability And Authoritative Repair Design

**Date:** 2026-04-24
**Status:** Draft for review

## Background

`ChannelRuntimeMeta` was introduced as the durable routing and runtime metadata for
channel replication. Bootstrap intentionally seeds initial channel placement from
the current slot topology so that the first sender can deterministically derive a
replica set and a leader without adding a second control-plane placement source.

That initial bootstrap rule is useful, but the current lifecycle behavior extends
the same "follow slot topology" rule too far:

- `RefreshChannelMeta()` can rewrite `Replicas`
- `RefreshChannelMeta()` can rewrite `ISR`
- `RefreshChannelMeta()` can rewrite `Leader` to the current slot leader
- slot leader changes trigger active-channel refreshes, which can make channel
  leader identity drift even when the original channel leader is still healthy

This means the persisted `ChannelRuntimeMeta.Leader` currently behaves more like a
projection of slot leadership than a stable channel-level leadership identity.

That behavior conflicts with the desired semantics:

- channel leader should change rarely
- slot leader change alone should not imply channel leader change
- when channel leader does change, the change must be authoritative and durable
- the new channel leader must not be elected from a replica that only holds older
  or minority-only data

## Goals

- Make `ChannelRuntimeMeta.Leader` stable across unrelated slot leader changes
- Keep `RefreshChannelMeta()` cheap on the send hot path
- Detect dead channel leaders from locally cached node liveness instead of from
  synchronous controller reads on every message send
- Route all channel leader failover decisions through the current slot leader
- Persist every successful channel leader repair through authoritative
  `UpsertChannelRuntimeMeta()` so restart does not drift back
- Select a new channel leader using the same quorum-safety model as channel
  reconcile, not by ad-hoc heuristics like "highest LEO"
- Preserve existing cluster-only semantics; single-node deployment remains a
  single-node cluster, not a separate bypass mode

## Non-goals

- This round does not redesign channel replica membership planning
- This round does not fully restore persisted `ISR` to a semantically precise
  "currently in-sync replicas only" set
- This round does not add a leaderless persisted runtime-meta state
- This round does not redesign slot placement or controller planner behavior
- This round does not require mixed-version rolling-upgrade compatibility

## Problem Statement

Today one lifecycle path mixes three responsibilities that should be separated:

1. bootstrap from slot topology
2. lease renewal for an already valid leader
3. channel leader failover when the current leader is invalid or dead

As a result:

- hot-path refresh can rewrite leader identity too aggressively
- slot leader changes can indirectly churn channel leader identity
- persisted `ISR` is flattened to slot peers, weakening its usefulness as a safe
  failover candidate set
- failover authority is ambiguous: the business caller detects the issue, but the
  current code path can locally derive a replacement leader before authoritative
  re-read

The design problem is therefore not just "how to detect dead". The design problem
is how to make channel leader failover:

- authoritative
- durable
- low-overhead on the steady-state send path
- safe with respect to quorum-proven data

## Existing Constraints

The redesign must respect the following current invariants and implementation
constraints:

- persisted runtime meta requires:
  - `Leader` is in `Replicas`
  - `Leader` is in `ISR`
- channel replica meta validation requires the same rule
- `ChannelRuntimeStatus` is too coarse for failover because it only exposes
  routing-level fields plus committed frontier, and it returns `ErrNotReady` when
  the replica is still reconciling
- existing reconcile proof RPC already exposes:
  - `OffsetEpoch`
  - `LogEndOffset`
  - `CheckpointHW`
- current reconcile logic may truncate an unsafe local tail and will not project a
  safe candidate beyond local `LEO`

These constraints imply two important v1 choices:

- repair cannot persist `Leader=0`
- repair should only elect a new leader from the currently persisted `ISR`,
  because both slot meta validation and channel replica validation already depend
  on that invariant

## Proposed Architecture

### 1. Separate membership from leadership

`ChannelRuntimeMeta` fields should be treated as two groups:

- **membership**
  - `Replicas`
  - `ISR`
  - `MinISR`
  - `ChannelEpoch`
- **leadership**
  - `Leader`
  - `LeaderEpoch`
  - `LeaseUntilMS`

`RefreshChannelMeta()` should no longer use one lifecycle helper that mutates both
groups together.

Instead:

- bootstrap remains the only "derive from slot topology" path
- lease renewal only touches leadership fields and only on the current leader node
- leader repair only touches leadership fields and only when the current leader is
  invalid or dead

Membership evolution remains a separate future concern.

### 2. Hot-path refresh behavior

The new `RefreshChannelMeta()` flow becomes:

1. read authoritative `ChannelRuntimeMeta`
2. on business activation miss, bootstrap once as today
3. optionally renew lease only if:
   - status is active
   - local node is the current channel leader
   - lease is expired or near expiry
4. consult local `nodeLivenessCache`
5. if the current leader is still acceptable, apply authoritative meta and return
6. otherwise call authoritative leader repair through the current slot leader
7. re-apply the authoritative repaired meta

The hot path must never interpret cache miss as leader death.

### 3. Local node liveness cache

`channelMetaSync` maintains a local `nodeLivenessCache` keyed by node ID.

Steady-state refresh uses this cache as follows:

- `Alive` -> no repair
- `Dead` -> repair
- `Draining` -> repair
- `Suspect` -> no repair in v1
- `Unknown` / cache miss -> no repair

This keeps the send path O(1) with no extra controller read per message.

### 4. Unified node liveness update path

The application layer should consume one unified callback:

```go
UpdateNodeLiveness(nodeID, from, to)
```

Population rules:

- the controller leader learns committed `NodeStatusUpdate` transitions directly
  from controller committed-command handling
- every other node, including controller followers, updates liveness by diffing
  `delta.Nodes` inside `SyncObservationDelta()`

This keeps app-layer liveness logic on one callback shape even though the
controller leader and every other node observe node status through different
transport paths.

### 5. Authoritative leader repair lives on the current slot leader

When `RefreshChannelMeta()` decides repair is needed, it must not choose a new
 leader locally.

Instead it calls a new slot-authoritative repair RPC:

```go
RepairChannelLeader(ctx, channelID, observedChannelEpoch, observedLeaderEpoch, reason)
```

The current slot leader is the authority for the channel key because it already
 owns the authoritative runtime-meta write path.

Repair responsibilities on the slot leader:

1. singleflight / serialize by channel ID
2. re-read authoritative runtime meta
3. re-check whether repair is still needed
4. collect candidate promotion reports
5. choose the best safe candidate
6. persist the new leader by authoritative `UpsertChannelRuntimeMeta()`
7. re-read the authoritative record and return it

### 6. Candidate evaluation is delegated to the candidate replica

The slot leader should not implement an independent copy of replica safety logic.

Instead it requests a candidate-local dry-run evaluation:

```go
EvaluateChannelLeaderCandidate(ctx, meta)
```

Each candidate replica:

1. loads local durable state
2. gathers reconcile proofs from peers
3. runs the same safe-prefix evaluator used by channel reconcile
4. returns a `PromotionReport`

This keeps failover correctness centered on one quorum-safety model.

### 7. Reuse reconcile proofs, do not invent another peer proof protocol

The candidate-local evaluator should reuse the existing reconcile proof RPC that
 already returns:

- `OffsetEpoch`
- `LogEndOffset`
- `CheckpointHW`

The new RPC added in this design is only:

- slot leader -> candidate replica: "evaluate yourself against this authoritative meta"

Peer proof collection between replicas continues to use the existing reconcile
 proof transport.

### 8. Failover selection rule

The new leader must not be chosen by "highest LEO" or "current slot leader".

Candidates should instead be ranked by:

1. `CanLead == true`
2. highest `ProjectedSafeHW`
3. highest `ProjectedTruncateTo`
4. `CommitReadyNow == true` preferred on ties
5. highest durable `CheckpointHW`
6. stable node ID tie-break

This means the chosen leader is the replica that can prove the largest
 quorum-safe prefix, not the replica that merely appears to have the longest tail.

### 9. Persist only leadership changes in v1

A successful leader repair only mutates:

- `Leader`
- `LeaderEpoch`
- `LeaseUntilMS`

It does not rewrite:

- `Replicas`
- `ISR`
- `MinISR`
- `ChannelEpoch`

That keeps repair semantics narrow and avoids reintroducing the current
 "one refresh rewrites the whole meta" drift problem.

## Data Structures

### App-layer liveness cache

```go
type nodeLivenessCache map[uint64]controllermeta.NodeStatus
```

### Repair RPC

```go
type ChannelLeaderRepairRequest struct {
    ChannelID            channel.ChannelID
    ObservedChannelEpoch uint64
    ObservedLeaderEpoch  uint64
    Reason               string
}

type ChannelLeaderRepairResult struct {
    Meta    metadb.ChannelRuntimeMeta
    Changed bool
}
```

### Candidate evaluate RPC

```go
type ChannelLeaderEvaluateRequest struct {
    Meta metadb.ChannelRuntimeMeta
}

type ChannelLeaderPromotionReport struct {
    NodeID               uint64
    Exists               bool
    ChannelEpoch         uint64
    LocalLEO             uint64
    LocalCheckpointHW    uint64
    LocalOffsetEpoch     uint64
    CommitReadyNow       bool
    ProjectedSafeHW      uint64
    ProjectedTruncateTo  uint64
    CanLead              bool
    Reason               string
}
```

### Replica dry-run evaluation input

```go
type DurableReplicaView struct {
    EpochHistory   []channel.EpochPoint
    LEO            uint64
    HW             uint64
    CheckpointHW   uint64
    OffsetEpoch    uint64
}
```

## Error Semantics

Add a new channel-level error:

```go
ErrNoSafeChannelLeader
```

Repair result rules:

- repair succeeds -> return repaired authoritative meta
- repair is no longer needed after authoritative re-read -> return latest
  authoritative meta, `Changed=false`
- no safe candidate exists -> return `ErrNoSafeChannelLeader`
- repair transport / store failure -> return the underlying retryable error

V1 should not fall back to returning stale old-leader metadata after repair failure.

## State Flow

### Steady-state refresh

```text
RefreshChannelMeta
-> authoritative get / bootstrap
-> maybe renew lease (leader-local only)
-> check nodeLivenessCache
-> leader still acceptable
-> apply authoritative meta
-> return
```

### Dead-leader repair

```text
RefreshChannelMeta
-> authoritative get
-> nodeLivenessCache says Dead/Draining
-> call RepairChannelLeader on current slot leader
-> slot leader rereads meta
-> evaluate current ISR candidates
-> select best safe candidate
-> UpsertChannelRuntimeMeta(Leader, LeaderEpoch+1, LeaseUntil)
-> reread authoritative meta
-> caller applies authoritative meta
```

### Post-repair runtime behavior

Persisting a new channel leader does not mean the new leader is immediately
 writable.

After apply:

- old leader becomes follower
- new leader becomes leader
- new leader may still enter `CommitReady=false`
- normal reconcile completes
- writes resume only when `CommitReady=true`

This design therefore changes **who may be selected** and **who is authorized to
 persist the decision**, not the downstream channel reconcile model.

## Implementation Plan

### Phase 0: lock behavior with tests

- add unit tests for:
  - no leader drift on slot leader change
  - liveness cache miss does not trigger repair
  - dead leader triggers repair and persists new `LeaderEpoch`
  - no safe candidate returns explicit error
  - concurrent refresh singleflights repair

### Phase 1: stop auto-rewriting leader and ISR in refresh

- remove leader / ISR / replica auto-alignment from refresh lifecycle helpers
- keep bootstrap semantics unchanged
- keep slot-leader-change-triggered active-channel refresh, but narrow it to
  authoritative re-read + local apply only

### Phase 2: add unified liveness cache plumbing

- add `OnNodeStatusChange` observer hook
- emit it from the controller leader committed path
- emit it from `SyncObservationDelta()` node diffs on every other node
- update `channelMetaSync.nodeLivenessCache` from one app entry point

### Phase 3: add node RPC skeletons

- add `channel_leader_repair`
- add `channel_leader_evaluate_candidate`
- wire client/server DTOs and redirect semantics

### Phase 4: add slot-leader repair coordinator

- add app repairer object with per-channel singleflight
- authoritative reread
- candidate collection
- candidate selection
- authoritative persistence

### Phase 5: add replica dry-run promotion evaluator

- extract a pure safe-prefix evaluator in `pkg/channel/replica`
- drive it from durable local state plus reconcile proofs
- return a `PromotionReport`

### Phase 6: connect the full refresh path

- `RefreshChannelMeta()` uses:
  - authoritative get
  - lease renewal
  - liveness check
  - authoritative repair when needed
  - apply authoritative meta

### Phase 7: update FLOW and wiki documentation

- `internal/FLOW.md`
- `pkg/channel/FLOW.md`
- `pkg/cluster/FLOW.md`
- `docs/wiki/channel/leader-switch-reconcile.md`

## Testing Plan

### Unit tests

- `internal/app/channelmeta_test.go`
- `internal/access/node/channel_leader_repair_rpc_test.go`
- `internal/access/node/channel_leader_evaluate_rpc_test.go`
- `pkg/channel/replica/promotion_evaluator_test.go`
- `pkg/cluster/observer_hooks_test.go`
- `pkg/cluster/agent_internal_integration_test.go`

### Integration tests

- multi-node `RefreshChannelMeta()` after slot leader change should preserve
  stable channel leader when the current leader is healthy
- dead channel leader should be repaired to a replica with the best projected safe
  prefix
- repaired leader should remain stable after process restart because the updated
  leader is durably persisted in authoritative runtime metadata

## Risks And Follow-up Work

### 1. Persisted ISR is still semantically weak

Because prior lifecycle behavior flattened `ISR` to slot peers, persisted `ISR`
 may still be wider than the true set of currently in-sync replicas.

V1 accepts that limitation and relies on candidate-local safe-prefix evaluation to
 reject stale replicas even inside that wider persisted `ISR`.

### 2. True ISR maintenance remains future work

Once leader stability and authoritative repair are in place, a later round can
 restore `ISR` to a stronger meaning and possibly narrow failover candidates from
 "persisted ISR + evaluator" to "trusted ISR + evaluator".

### 3. Suspect handling stays conservative in v1

This design intentionally does not trigger repair on `Suspect`.

That may delay failover slightly in some edge cases, but it avoids leader churn
 from transient observation jitter. A later round can add bounded suspect-based
 repair if real cluster evidence shows the need.
