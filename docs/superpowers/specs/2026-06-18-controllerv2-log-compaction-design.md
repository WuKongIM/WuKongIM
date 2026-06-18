# Controller V2 Log Compaction Design

**Date:** 2026-06-18
**Status:** Design approved for implementation planning

## Overview

Add safe distributed log entry compaction for `pkg/controllerv2` and expose a manual compaction action in `web`.

The feature is distributed in the operational sense: every Controller voter keeps its own local Controller Raft log bounded by snapshots, and the manager can trigger compaction on one node or fan out to all Controller voters. The compaction itself is not a replicated Controller command because it does not mutate `cluster-state.json`; it is local storage maintenance for each Raft participant.

## Goals

- Add an explicit manual compaction API to `pkg/controllerv2/raft`.
- Keep the compaction boundary tied to state already materialized into the Controller state machine.
- Reuse the existing automatic snapshot and WAL compaction path so manual and automatic compaction cannot drift.
- Restore state machine data when etcd raft delivers a `Ready.Snapshot`.
- Surface Controller Raft status and last compaction result through `internalv2` manager APIs.
- Let the web Controller page trigger compaction for the selected node or all Controller voters.
- Keep the v0 scope narrow: no new config keys, no destructive rebuild action, and no legacy `internal` control-plane path.

## Non-goals

- Compaction for slot Raft logs or channel logs.
- A replicated Raft command for compaction.
- Manual snapshot resend or follower rebuild tooling.
- Long-running background job tracking beyond the immediate per-node result and latest status.
- New `WK_` configuration keys in v0.

## Current State

`pkg/controllerv2/raft` already has automatic snapshot compaction:

- `applyScheduler.onApplied` calls `maybeSnapshot(ctx, store, applied)`.
- `maybeSnapshot` exports `StateMachine.Snapshot`, persists a snapshot through `raftstore.Store.SaveSnapshot`, and calls `store.Compact(ctx, applied - SnapshotCatchUpEntries)`.
- Startup recovery can restore from a persisted snapshot when the state file is empty.

The missing pieces are:

- no public manual `CompactLog` operation;
- no structured compaction status;
- no manager/API operation surface;
- no node RPC for remote status or compaction;
- `Ready.Snapshot` handling currently advances apply bookkeeping but does not restore the state machine from the snapshot data.

`web/src/pages/controller/page.tsx` already has the intended manual compaction controls and client methods. The backend contract needs to be implemented so the UI becomes real instead of speculative.

## Design Choice

### Recommended approach: local operation plus manager fan-out

Each node exposes a local Controller Raft status and local compaction operation. Manager routes either call the selected node or fan out to all Controller voters through a dedicated node RPC.

Benefits:

- matches Raft storage semantics;
- avoids encoding local disk maintenance as replicated cluster state;
- keeps status reads node-local, so the UI can inspect the node it selected;
- supports partial success when one Controller voter is down.

### Rejected approach: replicated Controller command

A Raft command would be wrong because compaction does not belong in `cluster-state.json`. It would also only apply after the same log being compacted, which makes lagging-node behavior harder to reason about.

### Rejected approach: HTTP-only local button

Only adding a local POST endpoint would be fast, but it would not solve the distributed operator workflow. The user would need to visit each node manually, and the web page could not report per-voter failures.

## Core Semantics

Add a manual compaction entrypoint to `pkg/controllerv2/raft.Service`:

```go
// CompactLog forces a local Controller Raft snapshot at the latest materialized applied index.
func (s *Service) CompactLog(ctx context.Context) (LogCompactionResult, error)
```

The compaction target is the durable materialized applied index, not only `RawNode.Status().Applied`. The safe source is the same boundary used by the apply scheduler after it has applied entries to the state machine and recorded applied progress in the store.

Manual compaction rules:

- if the service has no applied index, return a skipped result with `no_applied_index`;
- if the state machine snapshot has no materialized revision, return `no_materialized_state`;
- if the latest snapshot already covers the applied index, return `up_to_date`;
- otherwise snapshot at the applied index and compact old WAL segments using the existing `SnapshotCatchUpEntries` retention rule.

Manual compaction bypasses only the automatic trigger thresholds (`SnapshotCount` and `SnapshotMinInterval`). It does not bypass the applied-state safety boundary.

## Shared Snapshot Helper

Refactor automatic and manual compaction onto a shared helper in `pkg/controllerv2/raft`:

```go
type snapshotTrigger string

const (
    snapshotTriggerAutomatic snapshotTrigger = "automatic"
    snapshotTriggerManual    snapshotTrigger = "manual"
)
```

The helper owns:

- reading the latest applied index;
- exporting `StateMachine.Snapshot`;
- validating the materialized state revision;
- resolving the snapshot term;
- calling `raftstore.Store.SaveSnapshot`;
- calling `raftstore.Store.Compact`;
- recording status.

Automatic compaction keeps its current threshold checks before calling the helper. Manual compaction calls the helper directly.

## Snapshot Restore

Handle non-empty `Ready.Snapshot` before marking the snapshot applied:

1. Decode the snapshot data into `state.ClusterState`.
2. Ensure `ClusterState.AppliedRaftIndex` is at least the snapshot metadata index.
3. Call `StateMachine.Restore(ctx, restoredState)`.
4. Persist/mark the snapshot index as applied using the existing store bookkeeping.
5. Record restore status.

Restore failure remains fatal to the Raft service because continuing with mismatched raft and state-machine data is unsafe.

## Status Model

Extend `pkg/controllerv2/raft.Status` with operational fields:

```go
type LogCompactionStatus struct {
    LastTrigger string
    LastAttemptAt time.Time
    LastSuccessAt time.Time
    LastAppliedIndex uint64
    BeforeSnapshotIndex uint64
    AfterSnapshotIndex uint64
    Compacted bool
    SkippedReason string
    LastError string
}

type SnapshotRestoreStatus struct {
    LastSnapshotIndex uint64
    LastSnapshotTerm uint64
    LastRestoredAt time.Time
    LastError string
}

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

Status reads should include local log watermarks:

- `first_index`
- `last_index`
- `snapshot_index`
- `snapshot_term`
- `commit_index`
- `applied_index`

`NeedsSnapshot` is derived on leaders when a peer's next index is below the local first index.

## clusterv2 Boundary

Expose narrow Controller Raft operations from `pkg/clusterv2/control` and `pkg/clusterv2`:

```go
func (n *Node) LocalControllerRaftStatus(ctx context.Context) (control.ControllerRaftStatus, error)
func (n *Node) LocalCompactControllerRaftLog(ctx context.Context) (control.ControllerRaftCompactionResult, error)
```

The methods delegate to the local control runtime and do not route through the Controller leader.

## Node RPC

Add a dedicated internalv2 node RPC service for manager Controller Raft diagnostics and operations. Do not extend the existing manager log RPC codec, because that codec is fixed to the log-entry response shape.

Service ID:

```go
RPCManagerControllerRaft
```

Operations:

- `status`
- `compact`

The response carries typed status/result fields plus a normal error string/status code. Remote reads and compactions must target the requested node directly.

## Management Usecase

Add a management port separate from `LogReader`:

```go
type ControllerRaftOperator interface {
    ControllerRaftStatus(ctx context.Context, nodeID uint64) (ControllerRaftStatus, error)
    CompactControllerRaftLog(ctx context.Context, nodeID uint64) (ControllerRaftCompactionResult, error)
    CompactControllerRaftLogs(ctx context.Context) (ControllerRaftCompactionSummary, error)
}
```

The all-node operation gets Controller voters from the current cluster state and calls each target node. A partial failure should not abort the whole response; it should be recorded on the per-node item.

## Manager HTTP API

Add these routes in `internalv2/access/manager`:

```text
GET  /manager/nodes/:node_id/controller-raft
POST /manager/nodes/:node_id/controller-raft/compact
POST /manager/controller-raft/compact
```

Permissions:

- `cluster.controller:r` for status
- `cluster.controller:w` for compaction

Response shape should stay aligned with the existing web client types:

```json
{
  "node_id": 1,
  "result": {
    "compacted": true,
    "skipped_reason": "",
    "before_snapshot_index": 100,
    "after_snapshot_index": 120
  }
}
```

For all-node compaction:

```json
{
  "results": [
    {
      "node_id": 1,
      "success": true,
      "compacted": true,
      "skipped_reason": ""
    },
    {
      "node_id": 2,
      "success": false,
      "error": "target unavailable"
    }
  ]
}
```

## Web UI

Use the existing Controller page as the integration point:

- keep the selected-node status and log inspection flow;
- keep the current-node and all-Controller-voters compaction scopes;
- require `cluster.controller:w` for the compaction action;
- refresh status and log entries after a compaction request returns;
- display per-node success, skipped reason, and error for all-node compaction.

The page should describe the all-node scope as Controller voters, not every cluster node.

## Testing Strategy

Backend tests:

- `pkg/controllerv2/raft`: manual compaction below automatic threshold, skip reasons, status recording, and `Ready.Snapshot` restore.
- `pkg/clusterv2/control`: local status and local compaction delegation.
- `internalv2/access/node`: RPC codec round trip and status/error mapping.
- `internalv2/usecase/management`: selected-node operation, all-voter fan-out, invalid node IDs, and partial failure aggregation.
- `internalv2/access/manager`: route registration, read/write permission split, response shape, and HTTP error mapping.

Web tests:

- compaction button visibility by permission;
- current-node compaction calls the node endpoint;
- all-voter compaction calls the fan-out endpoint;
- successful compaction refreshes status/log data;
- partial failures are rendered without hiding successful node results.

Targeted verification commands:

```bash
go test ./pkg/controllerv2/... ./pkg/clusterv2/... ./internalv2/usecase/management ./internalv2/access/node ./internalv2/access/manager
cd web && bun test src/pages/controller/page.test.tsx
```

## Rollout Notes

No config migration is needed in v0. The feature uses existing snapshot defaults in `pkg/controllerv2/raft.Config`.

If future operations need tunable thresholds, add `WK_` config keys and update `wukongim.conf.example` in the same change.

## Self-review

- Scope is limited to `controllerv2`, `clusterv2`, `internalv2`, and `web`.
- The design does not introduce a new service layer or legacy `internal` dependency.
- Manual compaction is local storage maintenance and not a replicated command.
- Snapshot restore is included as a required safety fix.
- No incomplete markers or unresolved API names remain in this spec.
