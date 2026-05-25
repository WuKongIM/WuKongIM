# ControllerV2 Raft Performance Refactor Design

## Status

Approved direction by user on 2026-05-25. This design intentionally does not preserve compatibility with the current ControllerV2 Raft storage format and does not include a migration path from the existing `pkg/raftlog` controller scope.

## Problem

`pkg/controllerv2/raft/service.go` currently keeps the Raft ready loop, durable log persistence, committed-entry apply, state-file persistence, and applied-index persistence in one synchronous path:

```text
RawNode Ready
  -> send leader messages
  -> persist hard state / entries / snapshot through pkg/raftlog Pebble scope
  -> apply each committed entry to cluster-state.json
  -> MarkApplied for each entry
  -> RawNode.Advance
```

This creates several performance issues:

1. **Apply blocks Raft progress.** `StateMachine.Apply` writes `cluster-state.json` and fsyncs the file and parent directory for each command. Because apply runs inside the RawNode loop, slow disk work delays ticks, inbound `Step`, outbound messages, and new proposals.
2. **Applied index is too chatty.** The service calls `MarkApplied` after every committed entry, creating high-frequency small durable writes.
3. **Log storage is not a WAL hot path.** The controller log is stored in the generic Pebble-backed `pkg/raftlog` scope. Each append updates LSM keys and log metadata, then syncs Pebble. This is more expensive than an append-only Raft WAL.
4. **Startup loads full history.** The storage adapter loads all entries from `FirstIndex` to `LastIndex` into `etcdraft.MemoryStorage`. Without regular snapshots and compaction, startup time and memory usage grow with total history.
5. **State file is used as both materialized state and per-entry journal.** The canonical `cluster-state.json` is rewritten for every changed or noop command, even though Raft already has a durable ordered log.

## Goals

- Keep ControllerV2 as a normal Raft cluster, including the single-node cluster deployment shape.
- Align the ControllerV2 ready/apply pipeline with the etcd shape:
  - Raft ready loop remains responsive.
  - Leader replication can run in parallel with local WAL persistence.
  - Committed apply runs through a FIFO scheduler outside the Raft loop.
- Replace the controller Pebble log with a dedicated append-only WAL and snapshot layout.
- Batch committed apply so `cluster-state.json` and applied metadata are persisted once per batch instead of once per entry.
- Add snapshot and compaction so startup replays only a bounded suffix.
- Preserve deterministic proposal completion semantics: a proposal returns after its entry has been committed, applied, and published locally; semantic rejects return to the matching proposal only.
- Fail closed on durable WAL/apply errors and mark the service degraded.

## Non-Goals

- No compatibility with the current `pkg/raftlog` controller scope.
- No on-disk migration from old ControllerV2 test data.
- No change to the older `pkg/controller` implementation.
- No high-frequency runtime observations in `cluster-state.json`.
- No bypass branch for a non-clustered single-node mode.

## References

- etcd `raftNode.start` ready pipeline: https://github.com/etcd-io/etcd/blob/main/server/etcdserver/raft.go#L173
- etcd scheduling of `applyAll`: https://github.com/etcd-io/etcd/blob/main/server/etcdserver/server.go#L839
- etcd stable storage contract: https://github.com/etcd-io/etcd/blob/main/server/storage/storage.go#L30
- etcd WAL save/sync behavior: https://github.com/etcd-io/etcd/blob/main/server/storage/wal/wal.go#L961

## Proposed Architecture

Split `pkg/controllerv2/raft` into four focused components:

```text
pkg/controllerv2/raft/
  service.go              public lifecycle/propose/step/status facade
  loop.go                 RawNode ready loop, transport, leader/status, Advance
  apply_scheduler.go      FIFO apply scheduler and batching
  proposal_tracker.go     local proposal waiter indexing and completion
  config.go               config, defaults, validation
  status.go               status model
  raftstore/
    store.go              ControllerV2 durable storage facade
    wal.go                append-only WAL segments
    snapshot.go           state snapshot persistence and release
    metadata.go           hard state, applied index, conf state, segment metadata
    memory.go             bounded in-memory raft.Storage backed by snapshot + WAL suffix
```

`Service` remains the public API but no longer applies commands inside the RawNode loop. It owns:

- `raftLoop`: one goroutine that drives `RawNode`.
- `applyScheduler`: one FIFO worker that applies committed entries in order.
- `raftstore.Store`: durable WAL/snapshot/applied metadata and bounded in-memory raft storage.
- `proposalTracker`: maps locally proposed entries to response channels.

## Ready Pipeline

The new ready path is:

```text
RawNode Ready
  -> build toApply from Snapshot + CommittedEntries
  -> enqueue toApply to applyScheduler
  -> leader: send replication messages before local WAL save
  -> raftstore.SaveReady(HardState, Entries, Snapshot)
  -> update bounded memory storage
  -> follower/candidate: send messages after local WAL save
  -> coordinate rare conf changes with the apply scheduler
  -> RawNode.Advance
```

Important details:

- The loop never calls `StateMachine.Apply` directly.
- The loop never calls `MarkApplied` per entry. Applied metadata advances from the apply scheduler by batch.
- `raftstore.SaveReady` blocks until hard state and entries that require sync are stable. This preserves Raft safety.
- Leader send-before-persist remains intentional. It mirrors etcd's optimization where leader disk write overlaps follower replication and disk writes.
- Follower/candidate outbound messages are sent after local WAL persistence, except any future snapshot-specific path that needs stricter notification ordering.
- `RawNode.Advance` is not blocked by normal business apply completion. Snapshot creation and log compaction are gated by apply progress so compaction never outruns durable WAL persistence.
- Configuration changes are the rare exception. Because `RawNode` is not goroutine-safe, `rawNode.ApplyConfChange` remains owned by `raftLoop`. When a Ready contains a conf-change entry, `raftLoop` persists WAL, sends the `toApply` job, then waits on a small conf-change barrier. The scheduler applies entries up to the conf change, sends the decoded change to the barrier, and waits for `raftLoop` to call `rawNode.ApplyConfChange` and return the resulting `ConfState`. This preserves Raft ordering without making ordinary command apply block the loop.

## Apply Scheduler

The scheduler is a single FIFO worker. Its job input is:

```go
type toApply struct {
    entries       []raftpb.Entry
    snapshot      raftpb.Snapshot
    walPersistedC <-chan struct{}
    raftAdvancedC <-chan struct{}
    confChangeC   chan confChangeRequest // set only when this Ready includes conf changes
}
```

The worker:

1. Receives committed batches in Raft order.
2. Restores snapshots before applying entries when `snapshot` is non-empty.
3. Coalesces adjacent entry batches while preserving order.
4. Flushes when any limit is reached:
   - `MaxApplyBatchEntries`
   - `MaxApplyBatchBytes`
   - `MaxApplyDelay`
5. Applies normal command entries through `fsm.ApplyBatch`.
6. For conf changes, sends `confChangeRequest` to `raftLoop`, waits for the returned `ConfState`, then continues. The FSM records only the cluster-state effects that should be reflected in `cluster-state.json`.
7. Persists apply metadata once per flush through `raftstore.MarkAppliedBatch(lastIndex)`.
8. Completes proposal waiters for indexes in the batch.

The scheduler is FIFO, not parallel. ControllerV2 state mutations are order-dependent and must remain deterministic. The performance win comes from decoupling from the Raft loop and batching durable side effects, not from parallel state mutation.

## FSM Batch API

Add a batch API to `pkg/controllerv2/fsm`:

```go
type AppliedCommand struct {
    Index uint64
    Term  uint64
    Command command.Command
}

type BatchApplyResult struct {
    Results []ApplyResult
    FinalState state.ClusterState
}

func (sm *StateMachine) ApplyBatch(ctx context.Context, entries []AppliedCommand) (BatchApplyResult, error)
```

`ApplyBatch` semantics:

- Lock once.
- Clone the current state once.
- Skip entries with `Index <= AppliedRaftIndex` as idempotent already-applied entries.
- Apply mutations sequentially to the working state.
- Record a per-entry `ApplyResult` so the proposal tracker can report semantic rejects accurately.
- Save `cluster-state.json` once if the final state is valid and initialized.
- Publish the final state after durable save succeeds.
- Return errors without partial in-memory publication.

Noop or pre-init rejected entries can advance `ApplyMeta` even when no valid `cluster-state.json` exists yet. That durable boundary belongs to `raftstore`, not the state file.

## WAL and Snapshot Storage

`raftstore.Store` replaces the controller use of `multiraft.Storage`. It implements the `etcdraft.Storage` reads needed by `RawNode`, plus explicit durable write methods.

### Directory Layout

```text
controller-raft/
  wal/
    0000000000000000-0000000000000001.wal
    0000000000000001-0000000000010000.wal
  snap/
    0000000000012345-0000000000000007.snap
  meta.json
```

`meta.json` contains:

- current hard state cache
- current durable applied index
- latest snapshot index and term
- latest conf state
- active WAL segment list
- format version

### WAL Records

WAL records are append-only and CRC protected:

- `EntryRecord`: one or more raft entries.
- `HardStateRecord`: raft hard state.
- `SnapshotRecord`: snapshot index/term/conf state marker.
- `ApplyMetaRecord`: durable applied index.
- `SegmentHeaderRecord`: format version, node ID, cluster marker.
- `CRCRecord`: rolling CRC continuity.

`SaveReady` appends entries then hard state in the same WAL transaction. It uses `raft.MustSync(newHardState, previousHardState, len(entries))` to decide whether to fsync, with an unconditional fsync for snapshot markers and segment cuts.

### Bounded In-Memory Storage

The old adapter loaded every historical entry into `MemoryStorage`. The new store keeps:

- latest snapshot metadata
- a bounded unstable/stable suffix needed by raft for `Term`, `Entries`, and `LastIndex`
- read-through access to WAL suffix for entries not retained in memory

On startup:

1. Load latest snapshot.
2. Replay WAL records after the snapshot marker.
3. Rebuild hard state, conf state, last index, applied index.
4. Initialize RawNode storage with snapshot + bounded suffix.
5. Ask the apply scheduler to replay committed entries from durable applied + 1 through hard state commit.

## Snapshot and Compaction

Snapshotting is driven by applied progress:

- `SnapshotCount`: number of newly applied entries after which to snapshot.
- `SnapshotCatchUpEntries`: suffix retained after snapshot so followers can catch up without snapshot transfer.
- `SnapshotMinInterval`: optional wall-clock lower bound to avoid excessive snapshots.

Snapshot flow:

```text
applyScheduler advances applied index
  -> service checks snapshot threshold
  -> StateMachine.Snapshot produces canonical state
  -> raftstore.SaveSnapshot writes snapshot file and WAL marker
  -> bounded storage applies snapshot
  -> raftstore.Compact(retain from applied - catchup)
  -> old WAL segments and snapshot files are released
```

Compaction must never delete entries above the last durable applied index. It also must retain enough suffix for normal follower catch-up.

## Error Handling

- WAL save failure: set run error, mark service degraded, fail queued and indexed local proposals, stop the Raft loop.
- Apply scheduler failure: mark service degraded, fail local proposals at and after the failed index, stop accepting new proposals until restart.
- Semantic rejection: complete only the matching proposal with `ProposalRejectedError`; continue applying later entries.
- Proposal context cancellation: remove the waiter if not yet indexed; if already indexed, allow apply to continue and drop the response when delivered.
- Leader loss: fail proposals that are still local-only or not yet committed with `ErrNotLeader`; committed entries must still apply.
- Snapshot restore failure: mark degraded and stop. Partial snapshot publication must be atomic and recoverable.

## Configuration

Add ControllerV2 Raft performance knobs with detailed English comments in `Config`:

- `RaftDir string`: root directory for ControllerV2 WAL and snapshots.
- `MaxApplyBatchEntries int`: maximum committed entries per FSM batch.
- `MaxApplyBatchBytes uint64`: maximum encoded command bytes per FSM batch.
- `MaxApplyDelay time.Duration`: maximum time an apply batch may wait for coalescing.
- `WALSegmentSize uint64`: WAL segment rollover size.
- `SnapshotCount uint64`: applied entries between snapshots.
- `SnapshotCatchUpEntries uint64`: retained log suffix after compaction.
- `SnapshotMinInterval time.Duration`: optional minimum time between snapshots.

Defaults should favor safety and simple operations:

- `MaxApplyBatchEntries`: 128
- `MaxApplyBatchBytes`: 4 MiB
- `MaxApplyDelay`: 2 ms
- `WALSegmentSize`: 64 MiB
- `SnapshotCount`: 10,000
- `SnapshotCatchUpEntries`: 5,000
- `SnapshotMinInterval`: 30 s

If any of these become user-facing application configuration fields, update `wukongim.conf.example` with matching `WK_` keys and English comments.

## Test Plan

Unit tests:

- `raftstore` WAL append/read/reopen preserves hard state, entries, applied index, snapshot marker, and conf state.
- WAL detects truncated/corrupt records and recovers only complete records.
- WAL segment cut and release keep the newest needed segment.
- Snapshot save is atomic across crash points.
- Bounded storage serves `FirstIndex`, `LastIndex`, `Entries`, `Term`, and `Snapshot` across snapshot + WAL suffix.
- `ApplyBatch` persists `cluster-state.json` once per batch and returns per-entry semantic results.
- `ApplyBatch` handles already-applied entries idempotently.
- Scheduler preserves FIFO order and flushes by count, bytes, and delay.
- Scheduler marks applied once per batch.
- Proposal tracker returns semantic reject only to the matching proposal.

Service tests:

- Leader still sends replication messages before local WAL persistence.
- Slow FSM apply does not block `Step`, tick, or WAL processing in the Raft loop.
- Mark-applied failure degrades service and stops later publication.
- Restart after statefile-save-before-apply-meta replays idempotently.
- Startup from snapshot + WAL suffix does not require full log history.
- Conf changes remain ordered with Raft `ApplyConfChange` and outbound messages.
- Single-node cluster bootstrap and propose still work.
- Three-controller voter state converges to identical checksum.

Benchmarks:

- `BenchmarkControllerRaftProposeSingleNode`
- `BenchmarkControllerRaftProposeThreeNode`
- `BenchmarkControllerRaftApplyBatch`
- `BenchmarkControllerRaftStartupWithLongHistory`
- `BenchmarkControllerRaftWALSaveReady`

Success criteria:

- Startup time is proportional to snapshot suffix, not total historical entries.
- Proposal throughput improves materially over current per-entry statefile + MarkApplied path.
- The Raft loop remains responsive under an injected slow `StateMachine.ApplyBatch`.
- `cluster-state.json` write count falls from one write per command to one write per apply batch.

## Rollout Plan

1. Add `raftstore` package and tests.
2. Add `fsm.ApplyBatch` while keeping the old `Apply` as a single-entry wrapper for tests and callers.
3. Add `applyScheduler` and `proposalTracker` tests.
4. Rewrite `Service` to use `raftLoop`, `raftstore`, and scheduler.
5. Replace controller storage construction in the ControllerV2 composition root.
6. Update `pkg/controllerv2/FLOW.md` to show the new pipeline:

```text
Raft Ready -> WAL SaveReady -> Apply Scheduler -> batched FSM save -> ApplyMeta -> snapshot/compact
```

7. Remove obsolete controller use of `pkg/raftlog.ForController` if no longer referenced.
8. Run targeted unit tests, then broader package tests.

## Open Decisions for Implementation

- Whether `raftstore` should live under `pkg/controllerv2/raft/raftstore` or `pkg/controllerv2/raftstore`. Prefer the nested package unless another package needs direct access.
- Whether snapshot payload should be exactly `state.ClusterState` JSON or a small envelope with checksum and schema version. Prefer an envelope for forward compatibility even though old data migration is out of scope.
- Whether `ApplyMetaRecord` should be in WAL only or also mirrored in `meta.json`. Prefer both: WAL is source of truth; `meta.json` is a startup accelerator that is validated against WAL tail.
