# Controller Raft Log Compaction Design

**Date:** 2026-05-05
**Status:** Design approved

## Overview

Add default-on local snapshot compaction for the Controller Raft log.

The first version focuses on bounding local Controller Raft log growth and making recovery work from a Controller metadata snapshot. It must also handle `raft.Ready.Snapshot` safely because default-on compaction can cause Raft to deliver snapshots to lagging peers. Full catch-up product work, observability, and operator workflows for long-lagging followers are intentionally deferred to a later design.

## Goals

- Enable Controller Raft log compaction by default.
- Reuse the existing Controller metadata snapshot format in `pkg/controller/meta`.
- Persist Raft snapshots through the existing scoped `pkg/raftlog` storage.
- Restore Controller metadata from the latest persisted snapshot during Controller Raft startup.
- Keep the implementation local to clear package boundaries:
  - `pkg/controller/plane` owns state-machine snapshot and restore.
  - `pkg/controller/raft` owns compaction decisions and Raft snapshot metadata.
  - `pkg/raftlog` owns durable snapshot persistence and entry trimming.
  - `pkg/cluster`, `internal/app`, and `cmd/wukongim` only pass configuration.
- Preserve the manager-facing Controller log page as a node-local log view whose `FirstIndex` reflects compaction.

## Non-Goals

- Productizing full leader-to-follower snapshot catch-up workflows for stale Controller Raft peers.
- Keeping a retained tail of entries before the snapshot index.
- Preserving old full Controller Raft log history after compaction.
- Changing Slot Raft compaction behavior.
- Changing Controller voter membership semantics.

## Current State

`pkg/controller/raft.Service` currently rejects snapshots:

- startup returns `ErrSnapshotUnsupported` when `raftlog` contains a snapshot
- `processReady` fails if etcd raft emits a snapshot in `Ready`, after `persistReady` has already persisted it

At the same time, lower layers already provide most primitives needed by this feature:

- `pkg/controller/meta.Store.ExportSnapshot` exports Controller metadata with magic, version, entries, and CRC.
- `pkg/controller/meta.Store.ImportSnapshot` replaces Controller metadata from that snapshot.
- `pkg/raftlog.Save(PersistentState{Snapshot: ...})` persists a Raft snapshot and deletes log entries covered by the snapshot.
- `raft.MemoryStorage.CreateSnapshot` and `Compact` can keep the in-memory Raft storage consistent after local compaction.

## Proposed Approach

Use local Controller state snapshots as the compaction boundary:

```text
Controller Raft applies committed entries
  -> StateMachine.Apply mutates Controller metadata
  -> raftlog.MarkApplied(lastApplied)
  -> compaction policy checks applied growth
  -> StateMachine.Snapshot exports Controller metadata
  -> controller raft builds raftpb.Snapshot at lastApplied
  -> raftlog.Save persists the snapshot and trims entries <= lastApplied
  -> MemoryStorage.CreateSnapshot records the same in-memory snapshot
  -> MemoryStorage.Compact trims in-memory entries <= lastApplied
```

The snapshot index is always the latest applied index chosen for compaction. V1 does not keep `retainEntries` because retaining entries before the state snapshot complicates restore and follower catch-up semantics. If operators need full local history during development, they can explicitly disable compaction.

## Configuration

Add these config keys. They use `WK_` names and must be present in `wukongim.conf.example`.

```conf
# Enables local Controller Raft log snapshot compaction. Enabled by default.
WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED=true

# Number of newly applied Controller Raft entries after the last snapshot before creating a new snapshot.
WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES=10000

# Minimum interval between Controller Raft compaction checks.
WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL=30s
```

Validation rules:

- `Enabled` defaults to `true`.
- `TriggerEntries` defaults to `10000` and must be greater than zero when enabled.
- `CheckInterval` defaults to `30s` and must be greater than zero when enabled.
- Explicitly setting `Enabled=false` disables all Controller Raft compaction checks.

## Package Design

### `pkg/controller/plane`

Add snapshot methods to `StateMachine`:

```go
// Snapshot exports the durable Controller state at the current applied point.
func (sm *StateMachine) Snapshot(ctx context.Context) ([]byte, error)

// Restore replaces the durable Controller state with a previously exported snapshot.
func (sm *StateMachine) Restore(ctx context.Context, data []byte) error
```

Implementation:

- `Snapshot` validates `sm` and `sm.store`, then calls `Store.ExportSnapshot`.
- `Restore` validates `sm` and `sm.store`, then calls `Store.ImportSnapshot`.
- These methods do not know Raft indices or terms. `pkg/controller/raft` attaches Raft metadata.

### `pkg/controller/raft`

Add compaction options to `Config`:

```go
type LogCompactionConfig struct {
    // Enabled controls local Controller Raft snapshot compaction.
    Enabled bool
    // TriggerEntries is the applied-entry delta required before taking another snapshot.
    TriggerEntries uint64
    // CheckInterval is the minimum wall-clock interval between compaction checks.
    CheckInterval time.Duration
}
```

`Service.Start` behavior changes:

1. load `InitialState`
2. load `Snapshot`
3. if snapshot is non-empty, apply it to `MemoryStorage`
4. restore Controller metadata from `snapshot.Data`
5. load entries from `FirstIndex` to `LastIndex`
6. set hard state
7. create `RawNode` with:
   - `Applied=snapshot.Metadata.Index` when a snapshot exists
   - `Applied=state.AppliedIndex` when no snapshot exists

`run` tracks:

- latest applied index
- latest applied conf state
- last successful local snapshot index
- last compaction check time

After `MarkApplied(lastApplied)` succeeds, `maybeCompactControllerLog` checks:

```text
if compaction disabled: skip
if lastApplied == 0: skip
if now - lastCheck < CheckInterval: skip
if lastApplied - lastSnapshotIndex < TriggerEntries: skip
take snapshot at lastApplied
```

Snapshot creation:

1. call `StateMachine.Snapshot(ctx)` to get Controller metadata bytes
2. get the term at `lastApplied` from memory storage
3. build a `raftpb.Snapshot` with `Index=lastApplied`, the recorded term, the latest applied conf state, and metadata bytes
4. persist `raftlog.Save(PersistentState{Snapshot: &snapshot})`
5. call `memory.CreateSnapshot(lastApplied, &confState, data)` if memory has not already created it
6. call `memory.Compact(lastApplied)`
7. record `lastSnapshotIndex = lastApplied`

The durable `raftlog.Save` must happen before mutating in-memory snapshot state. If durable save fails, the service logs a warning and can retry the same index later without having created an in-memory-only snapshot.

Config changes:

- `rawNode.ApplyConfChange(cc)` returns the current Raft conf state.
- Store that returned value as the latest applied conf state so future snapshots contain the correct voter set.
- The custom `loadedMemoryStorage.confState` should also be kept current or replaced with a single source of truth for snapshot metadata.

Ready snapshot handling:

- If `ready.Snapshot` is non-empty, persist it, restore `StateMachine` from `ready.Snapshot.Data`, set `lastApplied` to `ready.Snapshot.Metadata.Index`, mark that index applied, and advance Raft.
- This is the minimum safe behavior required once local compaction is enabled by default.
- Broader stale-peer catch-up UX and observability remain future work.

Error handling:

- startup restore errors fail `Start`.
- `Ready.Snapshot` restore or `MarkApplied` errors remain fatal, matching normal state recovery behavior.
- local compaction `raftlog.Save` / `CreateSnapshot` / `Compact` errors are non-fatal warnings.
- compaction errors are logged as warnings and do not stop the Controller Raft loop.
- a later compaction check retries after `CheckInterval`.

### `pkg/raftlog`

Prefer not to change the public storage interface in V1.

Existing `Save(PersistentState{Snapshot: ...})` already persists the snapshot and trims covered entries. Add targeted tests for controller scope if needed, but keep Controller-specific policy outside `pkg/raftlog`.

### `pkg/cluster` and app wiring

Extend cluster/app config structs to carry the compaction config:

- `internal/app.ClusterConfig`
- `pkg/cluster.Config`
- `pkg/controller/raft.Config`

`newControllerHost` passes the cluster config into `controllerraft.NewService`.

`cmd/wukongim/config.go` parses the new keys, applies defaults, validates values, and records explicit flags if the current config pattern needs them.

## Recovery Flow

### Normal restart with snapshot

```text
open controller raft DB
open controller meta DB
load raft snapshot
restore controller meta from snapshot data
load post-snapshot raft entries
create RawNode with snapshot index as the applied point
continue processing new Raft traffic
```

Expected properties:

- Controller metadata reflects at least the snapshot index before Raft starts.
- Entries after the snapshot index are still available and are replayed by Raft after startup.
- `FirstIndex` becomes `snapshot.Metadata.Index + 1`.

Important recovery invariant:

- When a snapshot exists, startup must not set `RawNode`'s applied index to the persisted `AppliedIndex` if that value is greater than the snapshot index.
- The service restores Controller metadata to the snapshot point, then lets Raft replay committed entries after the snapshot.
- This avoids losing post-snapshot Controller metadata changes after restart.

### Crash during compaction

Safe crash points:

- after `MarkApplied`, before snapshot export: restart uses existing metadata and old logs.
- after snapshot export, before snapshot persist: restart ignores the unpersisted snapshot.
- after snapshot persist, before memory compact: restart uses the persisted snapshot and trimmed durable log.
- after memory compact: normal compacted state.

Compaction never acknowledges a client proposal. Proposal acknowledgement remains tied to normal apply completion.

## Manager Log Inspection

The manager Controller log page already reads a node-local Controller Raft log page and returns `FirstIndex`, `LastIndex`, `CommitIndex`, and `AppliedIndex`.

V1 behavior after compaction:

- entries older than `FirstIndex` are not returned
- `FirstIndex` communicates the local compaction floor
- decoded command inspection continues to work for available entries
- no historical placeholder rows are generated for compacted entries

Future UI polish can add a "compacted before index N" message, but it is not required for V1.

## Tests

### Unit tests: `pkg/controller/plane`

- `TestStateMachineSnapshotRestoreRoundTrip`
  - create nodes, assignments, tasks, hash slot table, and onboarding jobs
  - snapshot from one store
  - restore into a new store
  - assert all durable records match

- `TestStateMachineRestoreRejectsCorruptSnapshot`
  - pass invalid bytes
  - assert an error is returned

### Unit tests: `pkg/controller/raft`

- `TestServiceCompactsControllerLogAfterAppliedThreshold`
  - use a single-node cluster
  - configure a low trigger threshold and short check interval
  - propose enough commands
  - assert persisted snapshot index is greater than zero
  - assert `FirstIndex == SnapshotIndex + 1`

- `TestServiceRestartRestoresControllerSnapshot`
  - compact a single-node cluster
  - stop and restart it
  - assert Controller metadata is still present
  - assert new proposals still apply

- `TestServiceCompactionFailureDoesNotFailProposalLoop`
  - inject a snapshot or storage failure
  - assert proposals still complete
  - assert a later compaction can retry

- `TestServiceStartRestoresPreexistingSnapshot`
  - pre-seed raftlog with a Controller snapshot and post-snapshot entries
  - start service
  - assert restore happened before serving proposals

- `TestServiceIncomingReadySnapshotRestoresControllerMeta`
  - deliver or simulate a Raft snapshot through `Ready`
  - assert the Controller state machine restores it before marking the snapshot index applied

- `TestServiceSnapshotRestartReplaysPostSnapshotEntries`
  - snapshot at index N, apply entries N+1..M, restart
  - assert metadata includes entries after N

### Unit tests: `pkg/raftlog`

- Add a controller-scope equivalent of snapshot trim round-trip if coverage is missing:
  - save Controller entries
  - save Controller snapshot
  - assert covered entries are trimmed
  - reopen and assert metadata remains correct

### Config tests

- default config enables compaction
- explicit config can disable compaction
- invalid non-positive trigger/check interval fails validation when enabled
- `wukongim.conf.example` includes the new keys with English comments

## Documentation

Update `pkg/controller/FLOW.md`:

- remove the statement that Controller Raft snapshots are unsupported
- document local snapshot compaction under the Raft section
- note that full network snapshot catch-up is a future extension

If implementation reveals durable operational rules, add a concise note to `docs/development/PROJECT_KNOWLEDGE.md`.

## Open Follow-Up: Full Snapshot Catch-Up

The deferred B phase should cover:

- leader behavior when a follower requests entries older than the compaction floor
- snapshot message transport validation
- follower restore from network-delivered snapshots
- stale follower rejoin tests
- manager/health visibility for peers stuck behind the snapshot floor

This follow-up should be designed separately because it productizes distributed recovery behavior, while V1 only adds the minimum `Ready.Snapshot` handling required for safe default-on local compaction.
