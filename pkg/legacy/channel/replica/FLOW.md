# pkg/legacy/channel/replica FLOW

## 1. Responsibilities

`pkg/legacy/channel/replica` implements the per-channel ISR replica state machine used by the legacy channel package. It is used for both multi-node clusters and a single-node cluster; there is no separate non-cluster write path.

The package owns:

- leader append admission, group commit, quorum waiters, and HW advancement;
- follower fetch/cursor progress and follower-side `ApplyFetch` persistence;
- role/meta transitions, leader lease fencing, tombstone/close fencing;
- logical retention boundary application, local durable adoption/reset, and
  safe physical prefix trim effects;
- epoch lineage, divergence detection, leader reconcile, and promotion dry-run safety rules;
- checkpoint, snapshot, epoch-history, and log durability coordination;
- immutable `Status()` publication for runtime and transport readers.

It does not choose channel placement, store control-plane metadata, or route network RPCs. Those live in `controller`, `slot`, `runtime`, and `transport`.

## 2. File Map

| File | Responsibility |
|------|----------------|
| `types.go` | Public `Replica` interface, store interfaces, config, probe source. |
| `replica.go` | Constructor, public facade, lifecycle wiring, immutable status publication. |
| `loop.go` | Command/result envelopes and replica wrappers around the configured loop driver. |
| `loop_driver.go` | Dedicated per-replica loop driver preserving the legacy execution model. |
| `execution_pool.go` | Shared pooled execution workers, effect queues, centralized timer scheduling, and observer hooks. |
| `pooled_loop_driver.go` | Per-replica pooled mailbox preserving single-writer loop ownership on shared workers. |
| `commands.go` | Command, event, result, and durable-effect envelopes. |
| `lifecycle_pipeline.go` | Live loop dispatcher plus meta, role, close, tombstone, begin-epoch effects. |
| `meta.go` | Meta normalization, validation, and loop-owned commit helpers. |
| `append.go` | Append facade and timeout diagnostics only. |
| `append_pipeline.go` | Append queue, batching, durable append worker, waiter ownership. |
| `progress.go` | Public cursor/legacy ACK facade. |
| `progress_pipeline.go` | Cursor/fetch progress, HW advancement, waiters, checkpoint scheduling. |
| `checkpoint_writer.go` | Checkpoint effect worker and fenced checkpoint result handling. |
| `fetch.go` | Shared visible-HW helper. |
| `fetch_pipeline.go` | Leader fetch progress, read-log effect, fenced record clipping. |
| `replication.go` | Fetched-record index validation helper. |
| `follower_apply.go` | Follower `ApplyFetch` planning, durable apply/truncate/checkpoint result publication. |
| `reconcile.go` | Reconcile facade and probe orchestration. |
| `reconcile_coordinator.go` | Reconcile quorum rules, pending proof state, durable reconcile effects. |
| `snapshot_pipeline.go` | Snapshot validation, durable install effect, state publication. |
| `recovery.go` | Startup durable recovery before loop/effect workers start. |
| `retention.go` | Logical retention apply facade, durable adoption/trim effects, retention views. |
| `migration.go` | Migration fence-and-drain facade and loop-owned drain proof generation. |
| `durable_store.go` | Durable adapter, optional combined store contracts, recovery validation. |
| `history.go` | In-memory epoch-history append and trim helpers used by loop-owned transitions. |
| `epoch_lineage.go` | Pure epoch-lineage and divergence decisions. |
| `invariant.go` | Runtime/durable invariant checks used by tests and selected transitions. |
| `machine.go`, `state.go` | Pure machine harness retained for focused state-machine tests. |
| `promotion_*.go` | Dry-run leader promotion evaluator using the same reconcile safety rules. |

## 3. Public And Optional APIs

Primary public interface:

```go
type Replica interface {
    ApplyMeta(meta channel.Meta) error
    BecomeLeader(meta channel.Meta) error
    BecomeFollower(meta channel.Meta) error
    Tombstone() error
    Close() error
    InstallSnapshot(ctx context.Context, snap channel.Snapshot) error
    Append(ctx context.Context, batch []channel.Record) (channel.CommitResult, error)
    Fetch(ctx context.Context, req channel.ReplicaFetchRequest) (channel.ReplicaFetchResult, error)
    ApplyFetch(ctx context.Context, req channel.ReplicaApplyFetchRequest) error
    ApplyProgressAck(ctx context.Context, req channel.ReplicaProgressAckRequest) error
    ApplyReconcileProof(ctx context.Context, proof channel.ReplicaReconcileProof) error
    ApplyRetentionBoundary(ctx context.Context, throughSeq uint64) error
    FenceAndDrain(ctx context.Context, req channel.FenceAndDrainRequest) (channel.DrainResult, error)
    RetentionView() (channel.RetentionView, error)
    Status() channel.ReplicaState
}
```

Important optional store extensions are detected by `durable_store.go`:

- `BeginEpoch(ctx, point, expectedLEO)` fences epoch-history publication to durable LEO.
- `StoreApplyFetchWithEpoch(req, epochPoint)` persists follower records, checkpoint, and epoch history together.
- `TruncateLogAndHistory(ctx, to)` truncates records and epoch history in one durable mutation.
- `StoreCheckpointMonotonic(ctx, checkpoint, visibleHW, leo)` rejects checkpoint regressions at the write boundary.
- `InstallSnapshotAtomically(ctx, snap, checkpoint, epochPoint)` publishes snapshot payload, checkpoint, and epoch lineage together.
- `LoadRetentionState`, `AdoptRetentionBoundary`, and `TrimMessagesThrough` keep local retention adoption and physical deletion durable.

Runtime-facing helpers implemented by the concrete replica:

- `ApplyFollowerCursor(ctx, req)` is the epoch-aware steady-state ACK path used by long-poll cursor deltas.
- `SetLeaderLocalAppendNotifier(fn)` wakes runtime replication after leader LEO publication.
- `SetLeaderHWAdvanceNotifier(fn)` wakes runtime readers/replication after HW advancement.

The split-store fallback remains for tests and compatibility. It validates before mutation where possible and recovery rejects unsafe partial states instead of silently trusting them.

## 4. State Ownership

Live mutable fields are owned by the loop/pipeline handlers while holding `r.mu`. External facades submit commands and wait for replies; they do not mutate replica state directly.

| State | Owner |
|-------|-------|
| `meta`, `state`, `roleGeneration`, `closed` | lifecycle loop handlers and fenced effect results. |
| `progress`, `waiters` | progress/append/reconcile pipelines. |
| `retentionProgress` | retention view progress for the current leader epoch; local starts at LEO and followers start at `RetentionThroughSeq`. |
| `appendRequests`, `appendPending`, `appendInFlight*` | append pipeline only. |
| `pendingCheckpoint`, `checkpointQueued`, `checkpointInFlight`, `pendingCheckpointEffectID` | progress scheduler and checkpoint writer result handler. |
| `pendingFollowerApplyEffectID`, `pendingSnapshotEffectID`, `pendingLeaderEpochEffectID`, `pendingReconcileEffectID`, `pendingRetentionAdoptEffectID`, `pendingRetentionTrimEffectID` | corresponding effect pipeline. |
| `reconcilePending` | leader reconcile pipeline; tracks ISR proof still needed before CommitReady can be restored. |
| `epochHistory` | startup recovery before workers, then loop-owned epoch/snapshot/apply/reconcile transitions. |
| `statePointer` | immutable snapshot publication in `publishStateLocked`; unchanged states are skipped and `Status()` reads the latest snapshot lock-free. |
| `durableMu` durable lane | serializes log-mutating durable effects outside the loop through a cancellable single-token lane. |

`append.go`, `progress.go`, `fetch.go`, `replication.go`, and `reconcile.go` are facade/read-only helper files. The only read of loop-owned fields in `append.go` is timeout diagnostics, and it snapshots waiter data while still holding `r.mu.RLock()`.

## 5. Event Loop And Effects

Replica loop work is selected by `ReplicaConfig.Execution.Mode`:

- `pooled` is the default and uses a shared `ExecutionPool`; each replica owns a bounded mailbox and workers drain one replica at a time with a turn budget.
- `dedicated` keeps the legacy per-replica loop goroutine plus per-replica append/checkpoint effect workers and remains a rollback mode.

Both modes preserve per-channel single-writer semantics: all commands/results for one replica still pass through `applyLoopEvent()` serially, and all log/history/checkpoint/snapshot mutations remain fenced by the same effect ids, channel key, epoch, leader epoch, role generation, and `durableMu` durable-lane checks.

Dedicated mode consumes two channels:

1. `loopCommands`: synchronous facade commands with a reply channel.
2. `loopResults`: asynchronous worker results and timer events.

Pooled mode enqueues the same command/result envelopes into the replica mailbox and schedules append flush/checkpoint retry timers through the shared pool scheduler instead of spawning a timer goroutine per channel.

`applyLoopEvent()` dispatches to the appropriate pipeline. Most handlers mutate memory under `r.mu`, publish a new immutable state snapshot, and optionally emit effects.

Durable effects run outside the loop so storage I/O does not block command processing:

- dedicated append effects go through `appendEffects` and `startAppendEffectWorker()`;
- dedicated checkpoint effects go through `checkpointEffects` and `startCheckpointEffectWorker()`;
- pooled append/checkpoint effects go through shared execution-pool effect queues while preserving each replica's `durableMu` durable lane and result fencing;
- begin-epoch, follower apply, snapshot install, and leader reconcile durable effects are executed by the caller path after a loop command returns the effect;
- retention adoption/reset and physical trim effects are executed by the caller path after a loop command returns them;
- all log/history/checkpoint/snapshot mutations enter the `durableMu` durable lane so one replica has a single durable mutation lane;
- read-log effects do not enter `durableMu`, but their result is fenced by channel key, epoch, role generation, and captured leader LEO.

Every durable result carries an `EffectID` plus channel/epoch/role-generation fences; leader-scoped results also carry leader or leader-epoch fences. Durable effects validate their fence before queuing and revalidate after acquiring the `durableMu` durable lane, immediately before mutating storage. Stale results are discarded or reported as `ErrStaleMeta`; they must not publish newer state into an old role.

## 6. Core Flows

### 6.1 Startup Recovery

`NewReplica()` builds the durable adapter, publishes an initial follower snapshot, then calls `recoverFromStores()` before any loop/effect worker starts.

Recovery loads checkpoint, epoch history, snapshot presence, local retention state, and LEO through `durable.Recover()`. It rejects corrupt combinations such as checkpoint HW above LEO, incompatible checkpoint epoch, snapshot checkpoint without payload, or history beyond LEO. `LocalRetentionThroughSeq` and `PhysicalRetentionThroughSeq` are restored from local durable state, and `LEO` is raised to at least the retained LEO floor. Recovery does not initialize logical `RetentionThroughSeq` from local retention state; authoritative metadata applies that boundary later. The recovered runtime state is follower, with `HW == CheckpointHW == checkpoint.HW`, `LEO == durable/retained LEO`, and `CommitReady == (LEO == CheckpointHW)`. A local tail above checkpoint is retained as provisional and must be reconciled before leader appends are accepted.

### 6.2 Apply Meta And Role Changes

`ApplyMeta`, `BecomeLeader`, `BecomeFollower`, `Tombstone`, and `Close` all submit loop commands.

- `ApplyMeta` normalizes replica/ISR lists, validates channel/epoch/leader fences, bumps `roleGeneration`, and restarts leader reconcile if the local role is leader/fenced leader. Same-leader meta refresh can execute the local/probe reconcile effect immediately so checkpoint-only lag does not leave `CommitReady=false`.
- Membership is intentionally asymmetric during migration: `learner = Replicas - ISR`. Learners may receive replication/fetch traffic through the runtime, but HW quorum, `MinISR` write admission, leader reconcile quorum, and promotion dry-run candidates are computed from `ISR` only until authoritative slot metadata promotes the learner into `ISR`.
- A same-leader higher `ChannelEpoch` first gates append admission with `CommitReady=false`, persists `EpochPoint{Epoch: newEpoch, StartOffset: currentLEO}` through `BeginEpoch`, and only then publishes the new epoch. If the durable boundary fails, the replica stays not-ready so a later authoritative retry can reattempt the boundary.
- Migration membership changes that keep the same leader must use that same-leader epoch boundary before the new membership is considered append-ready; publishing the higher epoch before durable `EpochPoint` storage would make later reconcile unable to distinguish pre-cutover and post-cutover tails.
- `BecomeLeader` validates that the local node is the new leader, the replica has recovered, LEO is not below HW, and the lease is still valid. If a new epoch point is needed, it first emits `beginLeaderEpochEffect`; only the fenced durable result publishes leader state.
- `BecomeFollower` applies meta, changes role, cancels reconcile/begin-epoch effects, and fails all outstanding appends with `ErrNotLeader`.
- `Tombstone` marks the replica tombstoned and rejects future mutating operations.
- `Close` marks `closed`, fails outstanding append work exactly once, closes workers, and waits for loop/append/checkpoint goroutines to stop.

### 6.3 Leader Append

`Append()` clones records, records commit mode from context, keeps diagnostics-only trace metadata on the append request, and submits `machineAppendRequestCommand`. `AppendOwned()` is reserved for trusted in-process callers that transfer ownership of the batch slice, skipping this boundary clone while preserving loop-owned append semantics.

Loop admission requires leader role, non-expired lease, `CommitReady=true`, no local `WriteFence` token, no drained fail-closed marker, and `len(ISR) >= MinISR`. A present write-fence token returns `ErrWriteFenced` even after its wall-clock TTL; a drained marker also returns `ErrWriteFenced` until a newer fence version is applied. Empty batches complete immediately. Non-empty batches enter `appendPending` and are flushed by size/count or the group-commit timer.

A flush emits one `appendLeaderBatchEffect`. The append worker serializes durable mutation with the `durableMu` durable lane, calls `AppendLeaderBatch`, syncs the log, verifies the returned LEO range, and sends `machineLeaderAppendCommittedEvent` back to the loop.

`FenceAndDrain()` is the migration cutover primitive. It runs through the same single-writer loop and requires local leader or fenced-leader role plus matching channel epoch, leader epoch, and leader. The request carries the authoritative migration fence; the replica accepts it when local metadata already matches it, has not observed that newer fence yet, or still has an older fence from the same task token, but rejects another active or newer local fence. This lets scale-in drain a leader even after Slot ownership moved away from the source and the fence metadata has not propagated locally. It fails queued appends with `ErrWriteFenced`, waits for in-flight durable appends, quorum waiters, uncommitted local tail, reconcile effects, and pending checkpoint writes to settle, then records an in-memory drained marker and returns a `DrainResult` proof containing LEO/HW/checkpoint/fence metadata. Applying metadata with a higher `WriteFenceVersion` clears the drained marker; same-version clears or TTL expiry do not reopen appends.

On a matching result the loop publishes already-synced same-leader appends even if fenced metadata was applied after the durable write, then publishes the new LEO and local progress and wakes the runtime via `onLeaderLocalAppend`. Append effects use the current same-leader lease when they revalidate before durable write and again when the durable result returns, so a lease-only renewal does not leave durable LEO ahead of runtime LEO. If leader recovery/reconcile has already adopted the same in-flight durable range, the returning effect completes its original waiter idempotently rather than reporting a base-offset corruption. `CommitModeLocal` callers complete after durable append. Quorum callers become waiters and complete only after HW reaches their target. Context cancellation removes queued or waiting requests and completes the waiter once; in-flight durable appends are fenced before write when their result returns with an error.

Leader append sendtrace events are emitted for queue wait, local durable, and quorum wait stages. Each event carries stable result/error metadata and the relevant range so diagnostics queries can distinguish normal success, cancellation, timeout, and durable/commit errors without inspecting payload bytes.

### 6.4 Leader Fetch And Cursor Progress

`Fetch()` first submits `machineFetchProgressCommand`. The loop validates leader/fenced-leader role, channel key, epoch, fetch budget, snapshot boundary, and epoch-lineage safety.

The loop treats follower `FetchOffset`/`OffsetEpoch` as an ACK cursor:

- unknown or legacy zero offset epoch is capped to current HW when history exists;
- known epochs cap at the next epoch boundary;
- future epochs return `ErrStaleMeta`;
- cursors behind the retained-through offset `max(RetentionThroughSeq, LogStartOffset)` return a typed `RetentionReset` when logical retention is the dominant floor, including `RetentionThroughSeq`, `RetainedThroughOffset`, and `MinAvailableSeq`;
- when `LogStartOffset` dominates the retained-through offset, cursors behind it still return `ErrSnapshotRequired`;
- cursors exactly at the retained-through offset may fetch the next visible record;
- divergent cursors return `TruncateTo` instead of advancing unsafe progress.

If records are needed, the loop emits a read-log effect with captured leader LEO. The facade reads from `LogStore`, then submits `machineReadLogResultCommand`; the loop rechecks fences and the current retained-through offset, returning `RetentionReset` if the fetch cursor fell behind a newly applied dominant retention boundary, and clips records so fetch never exposes records above the LEO captured before the read.

`ApplyFollowerCursor()` submits the same safe cursor path without reading records. `ApplyProgressAck()` is legacy compatibility; it has no `OffsetEpoch`, so it is handled as a zero-epoch cursor and cannot advance beyond safe lineage rules.

### 6.5 HW And Checkpoint Progress

Leader progress uses `quorumProgressCandidate()`: sort ISR match offsets, default missing ISR entries to current HW, and choose the `MinISR`-th highest offset. A candidate below HW is corrupt; a candidate equal to HW is a no-op; a candidate above LEO is corrupt. Learner progress is deliberately excluded from this calculation until the slot layer promotes the learner into `ISR`.

When HW advances, the loop:

1. updates runtime `state.HW`;
2. queues the latest checkpoint if `checkpoint.HW > CheckpointHW`;
3. publishes state;
4. completes append waiters whose target is now committed;
5. calls `onLeaderHWAdvance` after releasing the lock.

Checkpoint writes are coalesced. A newer pending checkpoint replaces an older queued one. Store failures mark `CommitReady=false`, keep the pending checkpoint, and retry after `checkpointRetryDelay`. A successful fenced result advances `CheckpointHW` and can restore `CommitReady` when no reconcile or newer checkpoint remains.

### 6.6 Follower Apply

`ApplyFetch()` submits `machineApplyFetchCommand`. The loop accepts follower and fenced-leader roles, fences channel/epoch/leader, rejects concurrent follower apply effects, validates optional `TruncateTo`, trims already-applied duplicate record prefixes, and validates the remaining fetched record indexes are contiguous from `baseLEO+1`.

Record batches may create a new epoch point, append records, and optionally persist a checkpoint up to `min(LeaderHW, newLEO)`. Heartbeats may also persist an epoch-only boundary when metadata has advanced but no records exist yet, then move LEO/HW safely and store a checkpoint without records. Durable work runs under the `durableMu` durable lane through `ApplyFollowerBatch`, `BeginEpoch`, `TruncateLogAndHistory`, or `StoreCheckpointMonotonic`.

The fenced result publishes LEO, HW, CheckpointHW, OffsetEpoch, and CommitReady. If a durable truncate committed before the result became stale, the loop still reflects that truncation so runtime LEO does not remain above local durable log. If a follower metadata refresh only bumps the local role generation while the durable apply is already in progress, the committed durable result is still published when channel key, channel epoch, and leader are unchanged; epoch or leader changes continue to fence the stale result.

Follower apply sendtrace records include result/error metadata for durable apply/truncate/checkpoint work, including bounded error summaries and stable error codes for diagnostics.

### 6.7 Leader Reconcile

Leader promotion starts in `finishBecomeLeaderLocked()`. If `LEO > HW` or `CheckpointHW < HW` or `CommitReady=false`, the leader enters reconcile and sets `CommitReady=false`. If the local tail is above HW, peer proofs are required from ISR members; otherwise local checkpoint reconcile can finish without peer probes.

`ApplyReconcileProof()` validates channel key, epoch, leader epoch, lease, retained-through offset, and proof lineage, then updates peer progress. Proof below `max(RetentionThroughSeq, LogStartOffset)` is rejected/ignored like a snapshot-below-floor proof, so retained-away data cannot prove leadership. Missing ISR members count only as HW, never as local LEO. When enough proof exists, `completeLeaderReconcileLocked()` computes the quorum-safe prefix. It either publishes immediately when no durable work is needed or emits `leaderReconcileDurableEffect` to truncate unsafe tail and/or store checkpoint.

The durable reconcile result is fenced by effect id, channel key, epoch, leader epoch, role generation, and lease. Lease expiry is published by the loop result path as `FencedLeader` with `CommitReady=false`. A successful result publishes safe LEO/HW/CheckpointHW, clears pending reconcile, restores `CommitReady=true`, and notifies HW advance when HW increased.

### 6.8 Snapshot Install

`InstallSnapshot()` validates snapshot channel/epoch/end offset under the loop. Snapshot end must not go below runtime HW or current log start, and log LEO must already cover the snapshot end. The effect installs payload, checkpoint, and epoch point through `InstallSnapshotAtomically` when available, then reloads durable view.

A successful fenced result publishes follower state with `LogStartOffset == HW == CheckpointHW == snapshot.EndOffset`, updates epoch history, clears reconcile/begin-epoch state, fails outstanding appends, and bumps `roleGeneration`.

Channel replica migration V1 does not use replica snapshot install as an automatic catch-up path. The migration proof evaluator rejects promotion with `ErrSnapshotRequired` when the target's `LogStartOffset` is above the cutover HW, so that target must not enter `ISR` until snapshot transfer/install support has caught it up or the operator resolves the gap.

### 6.9 Promotion Dry-run

`EvaluateLeaderPromotion()` is a pure helper for app-side leader repair. It evaluates a candidate durable view plus peer proofs using the same epoch-lineage, retained-through offset, and reconcile quorum rules. It requires local durable state, never projects beyond local LEO, ignores proofs below the authoritative retention floor, and treats missing ISR proof as current HW only, so absent/stale peers can help preserve already committed prefix but cannot prove the candidate's local tail. The report states whether the candidate can lead plus the safe HW/truncate offset it would publish after real reconcile.

### 6.10 Retention Boundary Apply

`ApplyRetentionBoundary()` applies an authoritative retained-through sequence. `throughSeq == 0` is a no-op, and lower boundaries never reduce the logical fence. When the fence advances, the loop publishes `RetentionThroughSeq` and recomputes `MinAvailableSeq = EffectiveMinAvailableSeq(RetentionThroughSeq, LogStartOffset)` immediately, before physical deletion.

Durable adoption/reset is emitted when the boundary is ahead of `LocalRetentionThroughSeq`. The adoption effect calls the store under the `durableMu` durable lane, advances the committed replay cursor, and may raise runtime `LEO` to the retained boundary when the local log is behind; that reset leaves `CommitReady=false` until normal replication/reconcile catches up. Store failures leave the logical fence published and allow the same boundary to retry.

Physical trim is separate and may lag adoption. A trim effect is emitted only when `CommitReady=true`, `throughSeq <= CheckpointHW`, `throughSeq <= HW`, `throughSeq <= LEO`, and `throughSeq > PhysicalRetentionThroughSeq`. Successful trim publishes the durable physical boundary returned by the store. Retention never mutates checkpoint `LogStartOffset`; snapshots remain the owner of that floor.

Adoption and trim effects are fenced by the latest pending retention effect id as well as channel key, epoch, and role generation. Stale results from older retention attempts are ignored, so a delayed failure cannot overwrite or report after a newer success.

`RetentionView()` reports leader/epoch/lease, HW, CheckpointHW, LEO, commit readiness, logical/local/physical retention boundaries, min available sequence, and `MinISRMatchOffset`. The min ISR retention progress is the minimum across current ISR members, not the quorum candidate. Unknown followers in the current leader epoch start at `RetentionThroughSeq`, and leader metadata changes that alter epoch, leader epoch, leader, or ISR reset follower retention progress to that floor. They are not treated as caught up until observed fetch/cursor progress in the current metadata generation advances them.

## 7. Invariants

After recovery and while the loop is running, transitions must preserve:

- `LogStartOffset <= CheckpointHW <= HW <= LEO`.
- `OffsetEpoch == offsetEpochForLEO(epochHistory, LEO)` when history exists; zero history requires zero offset epoch.
- Runtime `HW` and `CheckpointHW` never decrease in one process lifetime.
- Tail truncation never truncates below HW or CheckpointHW.
- Snapshot install advances `LogStartOffset`, `HW`, and `CheckpointHW` together.
- `PhysicalRetentionThroughSeq` never exceeds `LocalRetentionThroughSeq`; local retention adoption may be ahead of logical retention after recovery until authoritative metadata is applied.
- Leader append is accepted only for a valid leader lease, `CommitReady=true`, and sufficient ISR.
- Fetch/cursor/reconcile/promotion progress never advances beyond a divergence-safe match offset.
- Tombstone and close fence all mutating operations and complete pending appends exactly once.

`checkReplicaInvariant()` encodes the watermarks, offset epoch, truncation, and snapshot publication rules used by focused tests and selected transitions.

## 8. Error Semantics

| Error | Meaning |
|-------|---------|
| `ErrNotLeader` | Operation requires an active leader/follower role but the replica is closed, follower for leader-only APIs, or otherwise fenced. |
| `ErrLeaseExpired` | Leader lease expired; write/reconcile paths fence the role as `FencedLeader`. |
| `ErrNotReady` | A required single in-flight effect is already running, or leader reconcile/checkpoint safety has not made appends safe. |
| `ErrWriteFenced` | Authoritative migration metadata has fenced new appends until a newer clear/reset/superseding fence is applied. |
| `ErrInsufficientISR` | Current ISR cannot satisfy `MinISR`. |
| `ErrStaleMeta` | Channel key, epoch, leader, leader epoch, effect id, or role generation is stale. |
| `ErrCorruptState` | Durable state, remote proof, store result, truncation, checkpoint, or invariant is impossible/unsafe. |
| `ErrSnapshotRequired` | Requested cursor/fetch/proof offset is behind the snapshot-dominated retained-through offset. |
| `ErrTombstoned` | Replica was tombstoned and rejects mutating operations. |

Context cancellation is returned to append/fetch/apply callers when their own context expires before the loop/effect path completes.

## 9. Troubleshooting

- If appends return `ErrNotReady`, check `Status().CommitReady`, `CheckpointHW`, and pending reconcile/checkpoint failures. A leader with retained local tail must reconcile before accepting writes.
- If appends time out waiting for quorum, inspect ISR progress and follower cursor lineage. Timeout logs include role, lease state, HW, LEO, CheckpointHW, ISR, progress, and target range.
- If a follower is asked to truncate, compare its `OffsetEpoch` with leader epoch history. Divergent known epochs are capped to the next epoch boundary; unknown/zero epochs are capped to HW when history exists.
- If recovery fails with `ErrCorruptState`, inspect checkpoint, epoch history, snapshot payload, and LEO together. Partial durable states are intentionally rejected.
- If checkpoint write fails, `CommitReady` can become false even though runtime HW advanced. The checkpoint writer retries and restores readiness only after the durable checkpoint catches up and no reconcile remains.
- If stale durable results appear, compare `EffectID`, `ChannelKey`, `Epoch`, `LeaderEpoch`, and `RoleGeneration`. Stale results should not mutate published state except committed truncation reflection.
- If a single-node cluster leader cannot append, verify `MinISR <= len(ISR)`, the local node is in ISR, the leader lease is in the future, and reconcile has restored `CommitReady`.
