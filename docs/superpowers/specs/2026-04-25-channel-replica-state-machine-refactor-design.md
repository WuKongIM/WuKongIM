# Channel Replica State Machine Refactor Design

## 1. Goal

将 `pkg/channel/replica` 从“多把锁 + 多个后台 goroutine 协作”的实现，重构为“单写者状态机 + 纯函数安全规则 + 明确 durable transaction/effect”的实现，使单 channel replica 的复制语义、提交语义和故障恢复语义更容易证明、测试和维护。

重构后外部语义保持兼容：runtime/app 仍通过当前 `Replica` 接口完成 `ApplyMeta`、`BecomeLeader`、`BecomeFollower`、`Append`、`Fetch`、`ApplyFetch`、`ApplyProgressAck`、`InstallSnapshot`、`Status` 等调用；内部实现可以大改。

## 2. Problems To Fix

当前包已经具备较好的测试基础和职责拆分，但要达到“上等代码”还需要解决这些结构性问题：

- 状态写入入口过多：`state`、`progress`、`waiters`、`reconcilePending` 由 append collector、advance publisher、checkpoint publisher、外部 API 等路径共同修改。
- 并发边界不够硬：部分代码在释放 `r.mu` 后继续读 `r.state` 构造日志、trace 或 store request，容易引入 race 或使用不一致 snapshot。
- `ApplyFollowerCursor` 的 `OffsetEpoch` 语义没有被用于 divergence 校验，stale/divergent cursor delta 有推进 HW 的风险。
- legacy `ApplyProgressAck` 不携带 `OffsetEpoch`，如果继续直接推进 progress，会绕过 epoch lineage 安全检查。
- append cancellation/request pooling 的 ownership 不够显式，caller 和 collector 都可能观察同一个 request，但释放责任不清晰。
- checkpoint、reconcile、append waiter、leader notify 的状态变化分散，未来改动容易遗漏事件。
- Store 接口契约过于分散，难以从接口层看出哪些操作必须原子 durable、哪些可以异步。
- 包内缺少 `FLOW.md` 说明核心不变量、状态机流程、错误语义和排障方式。

## 3. Target Architecture

### 3.1 Facade Compatibility

`Replica` 对外接口保持不变。`NewReplica(cfg)` 返回的 concrete type 仍实现当前接口，runtime/app 层不需要跟随大规模修改。

必须保留的兼容面：

- `Replica` interface methods remain unchanged, including `ApplyProgressAck`.
- concrete replica continues to implement optional `ApplyFollowerCursor(ctx, channel.ReplicaFollowerCursorUpdate) error` for long-poll cursor delta.
- concrete replica continues to implement `SetLeaderLocalAppendNotifier(func())` and `SetLeaderHWAdvanceNotifier(func())` for runtime wakeups.
- `ApplyProgressAck` becomes a compatibility shim with safe zero-epoch semantics; it must never bypass epoch lineage checks or advance HW above the current safe frontier when lineage is known.
- Tests may still access concrete `*replica` through package-local helpers, but production callers should depend only on the public/optional interfaces above.

Facade 方法只负责：

1. 构造 command。
2. 通过 replica loop 提交 command。
3. 等待 result 或 ctx cancel。
4. 对 `Status` 使用 atomic snapshot 快速返回。

Facade 不直接修改 mutable replica state。

### 3.2 Single Writer Event Loop

新增内部 event loop，作为唯一 mutable state owner。

- 只有 loop 能修改：`meta`、`state`、`progress`、`waiters`、`append queue`、`checkpoint pending`、`reconcile pending`、`epochHistory`。
- 外部 API 和 IO worker 不能直接修改这些字段，只能向 loop 发送 command/result。
- loop 对每个 event 执行确定性的状态转移，并产出 effects。
- effects 由 dedicated worker 执行，执行结果再回到 loop。
- 所有 log-mutating durable effects for one replica are serialized through one durable lane. Read/probe/notification effects may run concurrently because they do not mutate the durable log.

示意：

```text
External API -> commandCh -> replica loop -> effects -> IO workers -> resultCh -> replica loop -> publish snapshot
```

### 3.3 State Machine + Effects

引入内部状态机模型：

```go
type machine struct {
    state replicaState
}

type event interface{}
type effect interface{}

func (m *machine) Apply(event) ([]effect, []completion)
```

状态机只做同步、确定性的状态变化，不做阻塞 IO。需要持久化或通知时返回 effect：

- `beginEpochEffect`
- `appendLeaderBatchEffect`
- `applyFollowerBatchEffect`
- `truncateLogAndHistoryEffect`
- `storeCheckpointEffect`
- `installSnapshotEffect`
- `probeReconcileEffect`
- `readLogEffect`
- `notifyLocalAppendEffect`
- `notifyCommitEffect`
- `publishStateEffect`
- `completeAppendEffect`

There is no separate leader append sync effect. Durable append/apply effects represent already-synced transactions when they report success.

### 3.4 Effect Fencing And Ordering

Every effect that can return to the loop must carry enough fencing data to prove it still belongs to the current replica state. Durable-state-mutating effects must be fenced before the write whenever the store can validate the fence, and must still be fenced again when the result returns:

- monotonic `effectID` allocated by the loop;
- channel key;
- channel epoch;
- leader epoch from meta when available;
- internal role generation incremented on role/meta/tombstone/close transitions;
- request ids covered by the effect;
- lease snapshot for leader append/reconcile effects that rely on a live lease.

The loop must discard stale results from older epochs, older role generations, closed/tombstoned replicas, or superseded truncate/snapshot operations. Discarding a stale result must also complete or fail the affected waiters exactly once.

Leader append must preserve the current safety check: a durable append result is not published unless the loop re-validates that the replica is still leader, not fenced, commit-ready, has sufficient ISR, and the captured lease has not expired.

### 3.5 Pure Safety Rules

将复制安全规则从有锁方法中抽成纯函数：

- epoch lineage：`offsetEpochForLEO`、`matchOffsetForEpoch`、`divergenceState`。
- quorum progress：根据 ISR progress 计算第 `MinISR` 高的 safe offset。
- leader promotion：复用 quorum + epoch lineage 计算 `ProjectedSafeHW`。
- reconcile candidate：根据 local LEO/HW、peer proofs、epoch lineage 计算 truncate/commit point。
- visible HW：`CommitReady=false` 时 replica fetch may expose only durable `CheckpointHW`; client read path should continue to return `ErrNotReady` through handler/runtime policy.

这些函数不访问 replica、store、logger，也不需要锁。

### 3.6 OffsetEpoch And Divergence Truth Table

All follower progress updates, fetch divergence checks, reconcile proofs, and promotion proofs must use the same lineage decision function.

Inputs:

- leader epoch history;
- leader `LogStartOffset`;
- leader `LEO`;
- current runtime `HW`;
- remote offset;
- remote `OffsetEpoch`.

Rules:

| Case | Behavior |
|------|----------|
| `remoteOffset < LogStartOffset` | Fetch returns `ErrSnapshotRequired`; cursor/progress does not advance and returns `ErrSnapshotRequired` for direct cursor calls. |
| epoch history empty and `OffsetEpoch == 0` | Legacy/no-lineage path: safe match is `min(remoteOffset, leaderLEO)`. |
| epoch history non-empty and `OffsetEpoch == 0` | Legacy ACK path is capped to `min(remoteOffset, currentHW)` and must not advance HW. Direct cursor should return nil after no-op/cap to preserve compatibility. |
| `OffsetEpoch` exists in leader history | safe match is `min(remoteOffset, nextEpochStart or leaderLEO)`. If `remoteOffset` is greater than safe match, fetch returns truncate-to safe match and cursor caps/no-ops at safe match. |
| `OffsetEpoch > latest leader epoch` | reject as stale/future with `ErrStaleMeta`; do not advance progress. |
| `OffsetEpoch` unknown and not greater than latest leader epoch | cannot prove lineage; safe match is capped to `min(remoteOffset, currentHW)`, never above runtime HW. Fetch may return truncate-to that safe prefix when it is at or above `LogStartOffset`; otherwise snapshot is required. |
| safe match would exceed `leaderLEO` | cap to `leaderLEO`; if that still violates invariants return `ErrCorruptState`. |

`ApplyProgressAck` is treated as `OffsetEpoch == 0`. It is kept for compatibility but is not a steady-state path for long-poll replication. Runtime should prefer optional `ApplyFollowerCursor` whenever available.

Required tests:

- known epoch truncates at next epoch start;
- future epoch is rejected and cannot advance HW;
- unknown epoch is capped to current HW;
- zero epoch legacy ACK cannot advance HW when history exists;
- offsets below `LogStartOffset` require snapshot/no progress;
- promotion and reconcile use the same safe match logic.

### 3.7 Durable IO Boundary

用更明确的 durable adapter 统一当前分散接口。初期可在包内通过 adapter 包装现有接口，避免 runtime/app 大改。生产 `pkg/channel/store.ChannelStore` should grow/implement the combined interfaces needed for atomic durable state updates; split-store fallback exists for compatibility and tests, but recovery must validate and reject/repair unsafe partial state.

Target contract:

```go
type durableReplicaStore interface {
    Recover(ctx context.Context) (durableView, error)
    BeginEpoch(ctx context.Context, point channel.EpochPoint, expectedLEO uint64) error
    AppendLeaderBatch(ctx context.Context, records []channel.Record) (oldLEO uint64, newLEO uint64, err error)
    ApplyFollowerBatch(ctx context.Context, req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (newLEO uint64, err error)
    TruncateLogAndHistory(ctx context.Context, to uint64) error
    StoreCheckpointMonotonic(ctx context.Context, checkpoint channel.Checkpoint, visibleHW uint64, leo uint64) error
    InstallSnapshotAtomically(ctx context.Context, snap channel.Snapshot, checkpoint channel.Checkpoint, epochPoint channel.EpochPoint) (leo uint64, err error)
}
```

Durability requirements:

- `BeginEpoch` durably writes an epoch boundary at `expectedLEO` and is idempotent for the same `(epoch,startOffset)`. It must reject if current LEO differs from `expectedLEO` or if history would become non-monotonic. This effect is serialized on the durable mutation lane before leader reconcile/append for that epoch proceeds.
- `AppendLeaderBatch` writes records durably and returns both previous and new LEO. Success means the batch is synced/durable. Leader epoch boundaries are written by `BeginEpoch`, not by a later append side effect.
- `ApplyFollowerBatch` atomically writes fetched records, optional checkpoint, and optional epoch boundary. Follower apply across a new epoch must not persist records without the epoch point, or the epoch point without the records, unless recovery can detect and repair the partial state.
- `TruncateLogAndHistory` must atomically make log LEO and epoch history consistent. If the backing store cannot provide that atomic operation initially, `Recover` must detect mismatched log/history and either repair to a safe prefix or return `ErrCorruptState`; silent acceptance is not allowed.
- `InstallSnapshotAtomically` must publish snapshot payload, checkpoint, log start offset, and epoch history expectation as one durable operation. If a split-store fallback is used, recovery must detect partial snapshot publication and reject/repair it before starting the loop.
- `StoreCheckpointMonotonic` is a durable-state mutation and is serialized on the same per-replica durable mutation lane as epoch, append/apply, truncate, and snapshot effects. It must reject `checkpoint.HW > visibleHW`, `checkpoint.HW > leo`, and checkpoint regressions before writing.
- Checkpoint persistence may lag runtime HW, but it cannot lead runtime HW or LEO. A stale checkpoint result may be discarded by the loop, but stale checkpoint writes must also be made harmless by serialization plus monotonic store-side validation.

Recovery validation must cover partial states after leader `BeginEpoch`, follower apply with an epoch point, truncate+history, and snapshot+checkpoint/history. The package must include tests that inject or simulate partial log/history/snapshot/checkpoint combinations and prove recovery rejects or repairs them to a safe prefix.

### 3.8 Snapshot Publication

`Status()` 读取 atomic published snapshot。

- loop 内每次状态变化后生成 immutable snapshot。
- 所有日志、trace、notifier 参数都使用 loop 内复制的 snapshot。
- 禁止解锁后读取 mutable state。

### 3.9 Cancellation And Ownership

append request/waiter 不再由多个 goroutine 隐式共享释放责任。

- facade 提交 append command 后得到 request id。
- ctx cancel 发送 cancel command，不直接移除内部队列。
- loop 决定 request 当前阶段：queued、durable in-flight、waiting quorum、completed。
- 每个 request 只有 loop 完成一次 result。
- Pooling is optional. Until ownership is simple and proven by tests, prefer removing request/waiter pooling over retaining complex lifecycle code.

## 4. Key Flows

### 4.1 Append

```text
Append(ctx, records)
  -> append command
  -> loop validates leader/lease/commit-ready/minISR
  -> group commit queue
  -> appendLeaderBatchEffect(records)
  -> durable append result with fencing data
  -> loop re-validates leadership, lease, commit-ready, and role generation
  -> publish LEO and local progress
  -> complete CommitModeLocal waiters immediately
  -> quorum-mode waiters registered by target offset
  -> maybe advance HW
  -> complete waiters whose target <= HW
  -> store checkpoint asynchronously
```

HW 推进先完成 append waiter，再异步 checkpoint。checkpoint 失败会进入 checkpoint-degraded state but must not roll back runtime HW.

### 4.2 Fetch

```text
Fetch(req)
  -> fetch command
  -> loop validates leader/epoch/channel/maxBytes/logStart
  -> divergenceState(req.FetchOffset, req.OffsetEpoch, leaderLEO)
  -> update follower progress with safe match offset only
  -> maybe advance HW
  -> return truncate/snapshot response or readLogEffect
```

Fetch 的 response 必须不暴露超过 published leader LEO 的 records。

### 4.3 Cursor Delta

```text
ApplyFollowerCursor(delta)
  -> cursor command
  -> validate channel epoch and replica id
  -> divergenceState(delta.MatchOffset, delta.OffsetEpoch, leaderLEO)
  -> reject, cap, or no-op stale/divergent cursor according to truth table
  -> update progress monotonically only when safe
  -> maybe advance HW
```

Cursor delta 必须复用与 Fetch/Reconcile/Promotion 一致的 epoch lineage 判断。

### 4.4 Follower Apply Fetch

```text
ApplyFetch(req)
  -> apply-fetch command
  -> validate follower/fenced role, epoch, leader
  -> optional truncateLogAndHistoryEffect
  -> validate contiguous indexes
  -> durable applyFollowerBatchEffect
  -> loop applies result event only if epoch/role generation still match
  -> loop updates LEO/HW/CheckpointHW/OffsetEpoch
  -> publish snapshot
```

### 4.5 Leader Reconcile

```text
BecomeLeader(meta)
  -> validate meta and lease
  -> beginEpochEffect if needed through durable transaction/effect
  -> seed progress(local=LEO, peers=HW)
  -> if local tail or checkpoint gap exists: CommitReady=false
  -> if proof needed: probeReconcileEffect
  -> apply proofs through same epoch lineage rules
  -> compute quorum-safe candidate
  -> optional truncateLogAndHistoryEffect if candidate < LEO
  -> store checkpoint
  -> CommitReady=true
  -> notify commit once if HW advanced
```

Safety rules:

- local `HW` is the only prefix that may be assumed safe without fresh peer proof.
- Any candidate above `HW` requires actual quorum proofs using epoch lineage.
- Missing/offline ISR peers count as `HW` at most, never as local `LEO`.
- `candidate < HW` is corrupt/no-lead.
- Lease expiry, newer meta, tombstone, or close during reconcile fences/discards pending reconcile effects.
- Effect order is: begin epoch boundary, collect/validate proofs, optional truncate, store checkpoint, publish `CommitReady=true`, then notify commit once.

### 4.6 Recovery

```text
NewReplica
  -> durable Recover()
  -> validate durable view
  -> state.Role=follower
  -> state.HW=CheckpointHW
  -> state.CheckpointHW=durable checkpoint HW
  -> state.CommitReady = LEO == CheckpointHW
  -> keep local tail above checkpoint until leader reconcile proves or truncates it
  -> start loop/workers only after recovery succeeds
```

Runtime monotonicity starts after recovery. Loading an older durable checkpoint during process startup is not considered an in-process `HW`/`CheckpointHW` decrease.

### 4.7 Snapshot Install

```text
InstallSnapshot(ctx, snap)
  -> snapshot command
  -> validate role/channel/epoch/end offset against current state
  -> reject if EndOffset < HW or EndOffset < LogStartOffset
  -> installSnapshotAtomicallyEffect
  -> loop applies result only if epoch/role generation still match
  -> advance LogStartOffset, HW, CheckpointHW, LEO/OffsetEpoch consistently
```

Snapshot install may advance `LogStartOffset`, `HW`, and `CheckpointHW` together. It must not lower runtime HW or CheckpointHW.

## 5. Invariants

The implementation must preserve these invariants after recovery has completed and the loop has started:

- `LogStartOffset <= CheckpointHW`.
- `CheckpointHW <= HW`.
- `HW <= LEO`.
- `OffsetEpoch == offsetEpochForLEO(EpochHistory, LEO)` whenever epoch history is known.
- Runtime `HW` never decreases in one process lifetime.
- Runtime `CheckpointHW` never decreases in one process lifetime.
- Tail truncation never truncates below `HW` and never lowers `CheckpointHW`.
- Snapshot install only advances `LogStartOffset`, `HW`, and `CheckpointHW` together.
- Leader append is accepted only when role is leader, lease is valid, `CommitReady=true`, and `len(ISR) >= MinISR`.
- Cursor/fetch/reconcile/promotion progress never advances beyond a divergence-safe match offset.
- Tombstoned replica rejects all mutating operations.
- Close completes all pending append waiters exactly once.

Debug builds/tests should run invariant checks after each state transition.

## 6. Error Semantics

- `ErrNotLeader`: replica is not a leader for leader-only operations, or closed append facade.
- `ErrLeaseExpired`: leader lease has expired; role becomes fenced leader.
- `ErrNotReady`: leader exists but reconcile/checkpoint safety has not made it append-ready.
- `ErrInsufficientISR`: current meta ISR length cannot satisfy MinISR.
- `ErrStaleMeta`: request epoch/channel/leader is stale relative to current state; also used for future `OffsetEpoch` cursor/proof input.
- `ErrCorruptState`: durable state or remote proof violates invariants.
- `ErrSnapshotRequired`: follower fetch/cursor offset is behind leader log start.

## 7. File Plan

Target package layout:

```text
pkg/channel/replica/
  replica.go                  facade and constructor
  loop.go                     single writer event loop
  commands.go                 command/result/effect types
  machine.go                  internal state transition machine
  state.go                    internal state and published snapshot
  invariant.go                invariant checks
  durable_store.go            durable adapter and store contracts, including epoch/checkpoint/snapshot transactions
  epoch_lineage.go            epoch/divergence pure functions
  progress_tracker.go         quorum progress pure functions + state helpers
  append_pipeline.go          append grouping and waiter flow
  fetch_pipeline.go           fetch and cursor handling
  follower_apply.go           ApplyFetch handling
  snapshot_pipeline.go        snapshot install handling
  reconcile_coordinator.go    leader reconcile flow
  checkpoint_writer.go        checkpoint effect handling
  waiters.go                  append waiter registry
  promotion_evaluator.go      dry-run promotion using shared rules
  FLOW.md                     final package flow and invariants
```

Some existing files may remain during migration, but final ownership should match this structure.

## 8. Testing Strategy

### 8.1 Red-Green Regression Tests

Before production changes, add failing tests for currently unsafe behavior:

- Divergent cursor delta does not advance HW.
- Future `OffsetEpoch` is rejected.
- Unknown `OffsetEpoch` cannot advance beyond current HW.
- Legacy zero-epoch `ApplyProgressAck` cannot advance HW when history exists.
- Cursor below `LogStartOffset` requires snapshot/no progress.
- Append context cancellation completes exactly once and does not leak waiter/request ownership.
- Durable append result arriving after `BecomeFollower`, `Tombstone`, `Close`, or lease expiry is fenced and completes waiters safely.

Existing passing tests for reconcile HW notification and checkpoint retry are characterization/regression tests, not RED tests.

### 8.2 Pure Function Tests

Add table-driven tests for:

- epoch lineage match/truncate decisions, including all truth-table cases;
- quorum candidate selection for MinISR 1/2/3;
- promotion evaluator using the same match-offset function;
- visible HW when `CommitReady=false`.

### 8.3 State Machine Tests

Use event-driven tests:

- start from explicit state;
- apply one event;
- assert next state, completions, and effects.

This should cover leader transition, append durable result, stale append result, cursor ack, follower apply fetch, checkpoint result, stale checkpoint result, reconcile proof, snapshot result, tombstone, and close.

### 8.4 Durability Tests

Add tests with real `pkg/channel/store` where feasible:

- append and reopen recovers LEO/checkpoint consistently;
- begin-epoch reopens with LEO/history consistent;
- apply-fetch-with-checkpoint-and-epoch reopens with records, checkpoint, and history together;
- truncate+history either commits both or recovery rejects/repairs partial state;
- snapshot+checkpoint/history reopens with consistent log start, HW, CheckpointHW, and epoch history;
- injected partial history/log and snapshot/checkpoint states are rejected or repaired to a safe prefix.

Long-running crash/fault injection tests should use integration tags if they become slow.

### 8.5 Concurrency Tests

Run package race tests with concurrent external API calls:

- append + cancel + close;
- fetch + cursor + checkpoint result;
- leader transfer while append waits;
- runtime long-poll cursor path proves `OffsetEpoch` reaches replica;
- notification effects never block the loop and fire exactly once for append HW advance and reconcile HW advance.

### 8.6 Verification Commands

Required before claiming completion:

```bash
go test ./pkg/channel/replica -count=10
go test -race ./pkg/channel/replica -count=1
go test ./pkg/channel -count=1
go test ./pkg/channel/runtime ./pkg/channel/transport ./pkg/channel/store -count=1
go test ./internal/app -run 'Test(Channel|ApplyMeta|Leader|Runtime|Replica|Send|Ack|Repair)' -count=1
```

If runtime long-poll race coverage is feasible within unit-test time:

```bash
go test -race ./pkg/channel/runtime -run 'Test.*LongPoll|Test.*Lane|Test.*Session' -count=1
```

Integration tests remain opt-in:

```bash
go test -tags=integration ./...
```

## 9. Migration Strategy

Implement in sequential slices that keep the package compiling after each step:

1. Preflight: record git status and do not overwrite user changes.
2. Add pure epoch/quorum functions and tests while old implementation still calls old helpers.
3. Add durable adapter contracts and invariant checker tests.
4. Add command/result/effect types and pure machine tests.
5. Add loop skeleton that initially owns no existing mutable field.
6. Move lifecycle/meta/recovery/snapshot ownership into loop or explicitly keep legacy ownership until the named migration task.
7. Move all progress/HW writers together: cursor, fetch ACK progress, append local progress, reconcile proof progress, HW advance.
8. Move checkpoint effect handling.
9. Move append queue/waiters/group commit.
10. Move fetch/read-log and follower apply.
11. Move reconcile/probe/truncate/checkpoint.
12. Remove obsolete locks/goroutines only after corresponding writers are gone.
13. Write final `pkg/channel/replica/FLOW.md` and update `pkg/channel/FLOW.md` if terminology changes.

Implementation tasks are sequential unless a later plan explicitly assigns disjoint file ownership. Subagents may review or implement isolated pure-function files, but must not edit shared migration files in parallel.

## 10. Non-goals

- Do not change cluster semantics; single node remains a single-node cluster.
- Do not redesign runtime lane scheduling.
- Do not change wire protocol unless a test proves cursor epoch data is insufficient.
- Do not add a new generalized service package.
- Do not make unit tests slow; long-running stress or real process tests belong under integration tags.
