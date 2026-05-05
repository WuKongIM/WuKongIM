# Controller Raft Snapshot Catch-up Design

**Date:** 2026-05-06
**Status:** Draft approved for implementation planning

## Overview

Controller Raft log compaction is now default-on and can recover a lagging follower through Raft snapshots. The next step is to productize the recovery loop enough for operators and management APIs to understand whether a Controller voter is healthy, catching up through log append, needs a snapshot, is transferring a snapshot, or failed while restoring one.

This design intentionally stays below full manual operations. It adds status, diagnostics, and failure visibility, but does not add destructive rebuild actions or manual snapshot resend endpoints.

## Goals

- Expose Controller Raft compaction and catch-up status per node.
- Make local Controller log compaction visible through first/snapshot indexes.
- Let the leader report follower progress and whether a follower needs snapshot catch-up.
- Let a follower report the latest restored snapshot and restore failure state.
- Surface local compaction failures as degraded status without changing the warning-only retry behavior.
- Add manager-facing read APIs and DTOs for node detail / diagnostics.
- Preserve current Controller Raft data semantics and voter membership behavior.

## Non-goals

- Manual snapshot resend.
- Node data-directory deletion or rebuild operations.
- Automatic Controller voter membership changes.
- Slot Raft compaction status.
- UI-heavy operational workflows beyond exposing backend fields.

## Current State

The A implementation provides the core mechanics:

- local Controller Raft snapshots are created after `RawNode.Advance`
- snapshots are persisted to `pkg/raftlog` before memory compaction
- startup restores a persisted snapshot and replays post-snapshot entries
- `Ready.Snapshot` restores Controller metadata before marking the snapshot applied
- local compaction failures are logged as warnings and retried later

What is missing is product-level visibility:

- operators cannot tell whether a follower is inside the append range or requires a snapshot
- manager APIs do not expose snapshot indexes or compaction state
- restore failures are fatal but not represented as structured Controller Raft health
- compaction warning state is not queryable

## Proposed Approach

Add a read-only status model rooted in `pkg/controller/raft.Service.Status()` and expose it through `pkg/cluster`, management use cases, and manager HTTP APIs.

The model has two views:

1. **Local node status**: indexes, role, compaction config/state, last restored snapshot, last error.
2. **Leader peer progress**: per-follower progress from etcd raft when the local node is leader.

This keeps ownership clear:

- `pkg/controller/raft` owns volatile Raft-loop status and compaction/restore events.
- `pkg/cluster` joins service status with durable raftlog watermarks and transports remote reads.
- `internal/usecase/management` maps status into stable manager DTOs.
- `internal/access/manager` exposes read-only HTTP endpoints.

## Status Model

### Controller raft status

Add a controller raft status type with English comments:

```go
type Status struct {
    NodeID uint64
    Role string
    LeaderID uint64
    Term uint64

    FirstIndex uint64
    LastIndex uint64
    CommitIndex uint64
    AppliedIndex uint64
    SnapshotIndex uint64
    SnapshotTerm uint64

    Compaction LogCompactionStatus
    Restore SnapshotRestoreStatus
    Peers []PeerProgress
}
```

`Role` values: `leader`, `follower`, `candidate`, `unknown`.

### Compaction status

```go
type LogCompactionStatus struct {
    Enabled bool
    TriggerEntries uint64
    CheckInterval time.Duration
    LastSnapshotIndex uint64
    LastSnapshotAt time.Time
    LastCheckAt time.Time
    LastError string
    LastErrorAt time.Time
    Degraded bool
}
```

`Degraded=true` when the last local compaction attempt failed and no later success cleared it.

### Restore status

```go
type SnapshotRestoreStatus struct {
    LastSnapshotIndex uint64
    LastSnapshotTerm uint64
    LastRestoredAt time.Time
    LastError string
    LastErrorAt time.Time
    Failed bool
}
```

`Failed=true` when startup or `Ready.Snapshot` restore failed. If restore fails in the run loop, the service still exits with fatal error as it does now; the status is retained for diagnostics while the service object is alive.

### Peer progress

```go
type PeerProgress struct {
    NodeID uint64
    Match uint64
    Next uint64
    State string
    PendingSnapshot uint64
    RecentActive bool
    NeedsSnapshot bool
    SnapshotTransferring bool
}
```

`NeedsSnapshot` is derived on the leader as `Next < FirstIndex`.

`SnapshotTransferring` is true when `PendingSnapshot > 0` or the raft progress state is snapshot.

Followers return an empty `Peers` list because they do not own leader-side peer progress.

## Health Derivation

Management should expose a small summary string derived from status:

- `healthy`: service running, no compaction/restore failure, no follower requires snapshot from this leader
- `append_catchup`: leader sees follower lag but `Next >= FirstIndex`
- `snapshot_required`: leader sees follower `Next < FirstIndex` and no pending snapshot yet
- `snapshot_transferring`: leader sees pending snapshot / snapshot progress state
- `restore_failed`: local snapshot restore failed
- `compaction_degraded`: latest local compaction failed, service continues
- `unknown`: status unavailable or node not a Controller voter

A node list should include only summary fields to avoid making list views heavy:

```go
type NodeController struct {
    Role string
    Voter bool
    LeaderID uint64
    RaftHealth string
    FirstIndex uint64
    AppliedIndex uint64
    SnapshotIndex uint64
}
```

A node detail endpoint can return the full status including peer progress.

## API Design

### Cluster package

Add read methods to `pkg/cluster.API`:

```go
ControllerRaftStatusOnNode(ctx context.Context, nodeID uint64) (ControllerRaftStatus, error)
```

Local path:

- read `controllerHost.service.Status()`
- read `controllerHost.raftDB.ForController()` for `FirstIndex`, `LastIndex`, `InitialState`, and `Snapshot`
- merge volatile service status with durable watermarks

Remote path:

- add controller RPC kind, request, response codec
- call target node through existing controller RPC service

### Management use case

Add:

```go
GetControllerRaftStatus(ctx context.Context, nodeID uint64) (ControllerRaftStatusResponse, error)
```

Extend node list summary by calling status for local node and optionally Controller voters when cheap. If remote fan-out is too expensive for list views, keep list summary local-only and expose full status through node detail. The implementation plan should choose the cheaper path first unless UI requires all-node summaries.

### Manager HTTP

Add read-only endpoint:

```text
GET /manager/nodes/:node_id/controller-raft
```

Response includes the full status DTO. Errors follow existing manager mappings:

- invalid node id: `400`
- cluster not started / no leader / target unavailable: `503` where appropriate
- unexpected read errors: `500`

## Recovery Semantics

B does not change core recovery actions; it defines how they are observed.

- **Normal append catch-up:** leader peer `Next >= FirstIndex`.
- **Snapshot required:** leader peer `Next < FirstIndex`.
- **Snapshot transferring:** `PendingSnapshot > 0` or raft progress is snapshot.
- **Snapshot restored:** follower updates `Restore.LastSnapshotIndex` and `Restore.LastRestoredAt` after a successful startup or Ready snapshot restore.
- **Restore failed:** restore error remains fatal; status records `Restore.Failed=true` and `LastError`.
- **Compaction degraded:** local compaction error records status and logs warning; later success clears the degraded flag.

No endpoint should promise that a snapshot has fully completed until the follower has restored it and advanced applied state.

## Error Handling

- `Status()` must be safe before start, during run, and after stop.
- Missing raftlog data should return a normal error from cluster-level status reads, not panic.
- Restore failure status should be set before returning/terminating on the error path.
- Compaction failure status should be warning-only and must not fail proposals.
- Remote status RPC decode errors use existing cluster invalid-response error conventions.

## Testing Plan

### `pkg/controller/raft`

- `Status` before start / after start reports node id and role.
- Leader status includes peer progress.
- Compaction success updates `LastSnapshotIndex` and clears degraded state.
- Compaction failure records `Degraded=true` and later success clears it.
- Startup snapshot restore records restore index.
- `Ready.Snapshot` restore records restore index.

### `pkg/cluster`

- local `ControllerRaftStatusOnNode` merges service status with durable raftlog indexes.
- remote controller status RPC codec round trip preserves fields.
- leader peer progress derives `NeedsSnapshot` and `SnapshotTransferring`.
- remote target unavailable maps to existing transport errors.

### `internal/usecase/management`

- `GetControllerRaftStatus` maps cluster status to manager response.
- node list summary includes controller raft health fields without changing existing role/voter fields.

### `internal/access/manager`

- HTTP endpoint returns full status DTO.
- invalid node id returns 400.
- unavailable cluster read returns service unavailable using existing conventions.

## Rollout Notes

- Defaults do not change; Controller log compaction remains enabled by default.
- This is additive: new status fields and endpoint should not alter existing Controller Raft behavior.
- UI can consume the endpoint incrementally; backend should be useful even before UI changes.

## Future Work

- Manual snapshot resend.
- Operator-guided Controller voter data rebuild.
- Alerting thresholds for long-running `snapshot_required` / `snapshot_transferring` states.
- Rich UI workflow for Controller Raft catch-up diagnostics.
