# Channel Replica Migration And Leader Transfer Design

**Date:** 2026-05-11  
**Status:** Proposed  
**Scope:** V1 manual channel leader transfer and one-replica replacement for channel ISR groups

## Overview

Channel 层当前已经具备每个 channel 一个 ISR group、权威 `ChannelRuntimeMeta`、leader repair、reconcile probe、long-poll replication、slot/hash-slot migration 等基础能力。V1 的目标是在不改变 channel 所属 slot/hash-slot 归属的前提下，为 channel 集群增加两个运维能力：

1. **Channel leader transfer**: 只切换某个 channel 的 ISR leader，不改变 `Replicas` / `ISR`。
2. **Channel replica replacement**: 将某个 channel 的一个 ISR 副本从 `source` 节点替换到 `target` 节点。

V1 采用手动管理 API 触发、权威任务持久化、节点内 executor 推进的设计。后续自动调度只需要批量创建同一种任务，不需要另建迁移协议。

## Goals

- 支持手动把 channel leader 从当前 leader 转移到指定 ISR 节点。
- 支持手动把 channel 的一个副本从 source 节点替换到 target 节点。
- 迁移过程可恢复、幂等、可观测，并能在 executor 或 slot leader 切换后继续推进。
- 副本迁移大部分阶段保持写入开放，仅在 cutover 窗口短暂冻结该 channel 的写入。
- 保持 `ChannelRuntimeMeta` 是 channel membership 的唯一权威真相。
- 保持单节点集群语义，不引入绕过集群的独立业务分支。
- 为后续 draining、热点均衡、自动调度预留统一 task 接口。

## Non-Goals

- V1 不迁移 channel 的 slot/hash-slot 权威归属。
- V1 不支持同时替换多个副本。
- V1 不支持扩副本数、缩副本数或修改 `MinISR`。
- V1 不把 learner 提升为 leader；如果 source 是 leader，先把 leader 转到现有安全 ISR 节点。
- V1 不把迁移流程嵌入 append/fetch 请求路径。
- V1 不在 promote 后自动反向回滚；需要撤销时创建反向迁移任务。

## Current Foundation

现有代码中可复用的关键基础包括：

- `pkg/channel`:
  - `Meta.Replicas` 和 `Meta.ISR` 已分离。
  - leader 复制目标按 `Replicas` 派生。
  - HW quorum 推进按 `ISR` 和 `MinISR` 计算。
  - `ReconcileProbe` 能返回 `LEO`、`CheckpointHW`、`OffsetEpoch`。
  - leader promotion / reconcile 已有 quorum-safe prefix 逻辑。
- `pkg/slot/meta`:
  - `ChannelRuntimeMeta` 是 channel runtime metadata 的权威存储模型。
  - `UpsertChannelRuntimeMeta` 已有 epoch/lease/retention 单调保护。
- `internal/runtime/channelmeta`:
  - resolver 负责权威 meta refresh、leader repair、liveness 判断和本地 runtime apply。
- `pkg/cluster`:
  - hash-slot migration 已证明 task + worker + phase transition 模式可行。
- `internal/usecase/management` / `internal/access/manager`:
  - 已有 slot leader transfer、slot rebalance 等管理入口模式可参考。

重要观察：现有 `pkg/channel/runtime.leaderLaneTargetsFor(meta)` 按 `Replicas` 复制，而 `pkg/channel/replica.quorumProgressCandidate` 按 `ISR` 推进 HW。因此 V1 可以正式定义：

```text
Learner replicas = ChannelRuntimeMeta.Replicas - ChannelRuntimeMeta.ISR
```

leader 会向 learner 复制数据，但 learner 不参与 commit quorum，也不能成为 leader。

## Architecture

### Layers

```text
internal/access/manager
  -> HTTP DTO and routing only

internal/usecase/management
  -> manual operation validation, dry-run result, task create/abort/query use cases

internal/runtime/channelmigration
  -> node-local executor, task observation, phase advancement, retry, progress reporting

pkg/slot/meta + pkg/slot/fsm + pkg/slot/proxy
  -> authoritative ChannelMigrationTask storage and fenced ChannelRuntimeMeta commands

pkg/channel
  -> generic channel ISR primitives: learner replication, write fence checks, drain, probe/status
```

### Ownership Rules

- `internal/access/manager` only adapts HTTP requests and responses.
- `internal/usecase/management` owns manager-facing validation and dry-run reporting.
- `internal/runtime/channelmigration` owns task execution policy, but does not become the source of truth.
- `pkg/slot/meta` owns durable task and meta state.
- `pkg/channel` owns only reusable channel ISR primitives. It must not import management or migration task packages.
- `internal/app` remains the composition root and wires executor dependencies.

## Authoritative Task Model

Add a durable `ChannelMigrationTask` under the slot that owns the channel's runtime metadata.

Recommended fields:

```go
type ChannelMigrationTask struct {
    TaskID string
    Kind   ChannelMigrationKind
    Status ChannelMigrationStatus
    Phase  ChannelMigrationPhase

    ChannelID   string
    ChannelType int64

    SourceNode    uint64
    TargetNode    uint64
    DesiredLeader uint64

    BaseChannelEpoch uint64
    BaseLeaderEpoch  uint64

    FenceToken   string
    FenceUntilMS int64

    CutoverLEO uint64
    CutoverHW  uint64

    Attempt     uint32
    NextRunAtMS int64
    LastError   string

    CreatedAtMS int64
    UpdatedAtMS int64
    CompletedAtMS int64

    Progress ChannelMigrationProgress
}
```

Task status:

```text
Pending -> Running -> Completed
                 \-> Failed
                 \-> Aborted
```

Task kinds:

```text
LeaderTransfer
ReplicaReplace
```

Core invariants:

- At most one active task per channel.
- Every phase advance checks `TaskID`, expected current phase, expected status, and expected meta epoch.
- Duplicate phase advances are idempotent no-ops.
- Stale phase advances must not overwrite newer task or metadata state.
- Completed/failed/aborted tasks are retained for a TTL, then garbage collected.

## ChannelRuntimeMeta Write Fence

Add a narrow write fence to `ChannelRuntimeMeta`:

```go
WriteFenceToken   string
WriteFenceReason  uint8
WriteFenceUntilMS int64
```

Projection to `channel.Meta` includes the same fields, ideally wrapped as a small struct:

```go
type WriteFence struct {
    Token   string
    Reason  WriteFenceReason
    Until   time.Time
}
```

Append behavior:

- No fence: normal append.
- Fence exists and has not expired: return a retryable error, preferably `channel.ErrWriteFenced`.
- Fence exists but is expired: local append treats it as inactive; executor later clears or renews the authoritative fence.

Fence rules:

- Fence token is normally `TaskID`.
- Only the task that set a fence may clear it.
- Fence has a short TTL, for example 10-30 seconds.
- Executor renews the fence while in cutover phases.
- `PromoteAndRemove` must require a still-valid matching fence token.

## Leader Transfer State Machine

Leader transfer changes only channel leader identity.

```text
Pending
  -> Validate
  -> ProbeTarget
  -> WriteFence
  -> DrainLeader
  -> CommitLeaderMeta
  -> VerifyNewLeader
  -> ClearFence
  -> Completed
```

### Validate

Read authoritative `ChannelRuntimeMeta` and require:

- `Status == Active`.
- target is in `ISR`.
- target is in `Replicas`.
- target is active/alive data node and not draining.
- no other active task exists for the same channel.
- if target is already leader, complete idempotently.

### ProbeTarget

Use existing promotion safety concepts:

- target must have durable state for the channel.
- target must prove at least the current committed prefix.
- target should preferably be caught up to leader LEO before entering the fence window.

V1 can implement this via `ReconcileProbe` plus current authoritative meta. If needed, a later iteration can add a dedicated `ChannelMigrationProbe` response with richer fields.

### WriteFence

Set `WriteFenceToken=TaskID` on authoritative `ChannelRuntimeMeta` through a fenced slot Raft command.

### DrainLeader

Current channel leader executes a node-local drain primitive:

```go
FenceAndDrain(ctx, channelKey, token) (DrainResult, error)
```

Required semantics:

- Only current channel leader can drain.
- Prevent new append requests from entering the pipeline.
- Wait for already accepted append work to complete or fail.
- Return stable `LEO`, `HW`, `CheckpointHW`, `ChannelEpoch`, and `LeaderEpoch`.
- Fail on lease expiration, meta change, role change, or context timeout.

### CommitLeaderMeta

Commit authoritative metadata update:

```text
Leader = target
LeaderEpoch = LeaderEpoch + 1
LeaseUntilMS = now + lease
ChannelEpoch unchanged
Replicas unchanged
ISR unchanged
WriteFence remains set
```

### VerifyNewLeader

Wait until target applies the new meta, becomes leader, and reaches `CommitReady=true` after normal reconcile.

### ClearFence

Clear the matching fence token and complete the task.

## Replica Replacement State Machine

Replica replacement changes exactly one replica.

```text
Pending
  -> Validate
  -> TransferLeaderIfNeeded
  -> AddLearner
  -> BootstrapTarget
  -> WarmCatchUp
  -> CutoverFence
  -> FinalCatchUp
  -> PromoteAndRemove
  -> VerifyMembership
  -> ClearFence
  -> Completed
```

### Validate

Read authoritative `ChannelRuntimeMeta` and require:

- `Status == Active`.
- source is in `Replicas`.
- source is in `ISR`.
- target is not in `Replicas`.
- target is active/alive data node and not draining.
- `MinISR` remains valid after replacement.
- no other active task exists for the same channel.
- target is not the current leader.

V1 keeps `MinISR` unchanged and does not change replica count after completion.

### TransferLeaderIfNeeded

If source is not the current leader, skip.

If source is current leader:

- choose a desired leader from existing `ISR - source`.
- run the leader transfer state machine internally.
- reread authoritative meta after transfer.
- continue only if source is no longer leader.

V1 deliberately avoids direct source-leader-to-target-learner cutover because target is not yet an ISR member.

### AddLearner

Commit authoritative metadata update:

```text
Replicas = Replicas + target
ISR unchanged
ChannelEpoch = ChannelEpoch + 1
LeaderEpoch unchanged
WriteFence unchanged/empty
```

After this step, target is a learner. Existing channel runtime should replicate to target because replication targets are derived from `Replicas`, while commit quorum remains based on `ISR`.

### BootstrapTarget

Target activation may be one of two forms:

1. **Fetch catch-up**: target starts from an offset that the leader can still serve.
2. **Snapshot bootstrap**: target needs a channel snapshot because leader has trimmed the needed prefix.

The design should include snapshot bootstrap as the long-term correct path. Implementation can be phased, but production-safe migration must not silently promote a target that cannot catch up.

Snapshot payload must cover the durable channel state needed for recovery and idempotency:

- message rows or an equivalent channel state snapshot,
- checkpoint,
- epoch history,
- retention state,
- idempotency state or enough data to rebuild it safely.

### WarmCatchUp

Writes stay open.

Executor periodically probes leader and target and records:

- leader LEO,
- leader HW,
- target LEO,
- target CheckpointHW,
- target OffsetEpoch,
- lag records,
- stable-since timestamp.

When lag is below threshold for a stable window, continue to cutover.

### CutoverFence

Set `WriteFenceToken=TaskID`, then ask the current leader to `FenceAndDrain`.

Store returned `CutoverLEO` and `CutoverHW` in the task. If the fence expires before final catch-up completes, the task must discard the old cutover point and return to `WarmCatchUp`.

### FinalCatchUp

Because writes are fenced, target must reach:

```text
target LEO >= CutoverLEO
target CheckpointHW >= CutoverHW
```

This proves target has all records that were committed before cutover.

### PromoteAndRemove

Commit authoritative metadata update atomically:

```text
Replicas = Replicas + target - source
ISR = ISR + target - source
ChannelEpoch = ChannelEpoch + 1
LeaderEpoch unchanged
Leader unchanged
WriteFence remains set
```

Preconditions:

- matching fence token is still active,
- task phase is `PromoteAndRemove`,
- current meta still contains source and target in the expected roles,
- source is not leader,
- target final catch-up condition is satisfied.

### VerifyMembership

Verify:

- leader applies the new meta and is `CommitReady=true`,
- target applies the new meta and can answer probe as an ISR follower,
- source is no longer a valid replication peer in the new meta,
- no stale fence was overwritten.

### ClearFence

Clear the matching fence token and complete the task.

## Failure Recovery

Recovery strategy is retry-oriented before cutover and forward-only after promote.

| Phase | Recovery behavior |
| --- | --- |
| Before `AddLearner` | No membership side effect; retry or fail task. |
| After `AddLearner`, before `PromoteAndRemove` | Target is learner only; continue, or abort by removing target from `Replicas`. |
| During fence phases | Renew valid fence and continue, or if expired return to `WarmCatchUp`. |
| After `PromoteAndRemove` | Continue `VerifyMembership` / `ClearFence`; do not auto-roll back. |
| Clear fence failure | Keep retrying; TTL is only a safety net, not the normal completion path. |

Node failure behavior:

- source dead before promote: continue only if current leader and target safety can still be proven; otherwise fail closed.
- source leader dead: rely on existing leader repair/transfer safety; if no safe leader exists, fail closed.
- target dead before promote: abort learner and fail/abort task.
- target dead after promote: target is already ISR; rely on repair or a new reverse migration task.

## Concurrency Control

- One active channel migration task per channel.
- Global and per-target concurrency limits in `channelmigration` executor.
- Draining workflows may create many tasks, but executor advances them under limits.
- All writes to task and meta go through slot Raft commands with expected task/phase/epoch fences.
- Leader repair may run while migration exists, but it must not clear migration fences.
- If leader repair changes `LeaderEpoch`, migration executor must reread meta and revalidate before continuing.
- `channelMetaSync` only applies authoritative meta; it does not decide migration phase.

## Manager API

V1 management API:

```text
POST /channels/{channel_type}/{channel_id}/leader/transfer
body: { "target_node_id": 2, "dry_run": false }

POST /channels/{channel_type}/{channel_id}/replicas/migrate
body: { "source_node_id": 1, "target_node_id": 4, "dry_run": false }

GET /channels/{channel_type}/{channel_id}/migration

POST /channels/{channel_type}/{channel_id}/migration/{task_id}/abort
```

Dry-run returns validation status, blockers, expected phase sequence, and estimated safety constraints without creating a task.

## Observability

Expose task details:

- `task_id`, `kind`, `status`, `phase`, `channel_id`, `channel_type`, `source`, `target`, `desired_leader`,
- `base_channel_epoch`, `base_leader_epoch`, `current_channel_epoch`, `current_leader_epoch`,
- `leader_leo`, `leader_hw`, `target_leo`, `target_checkpoint_hw`, `lag_records`, `stable_since`,
- `fence_active`, `fence_until_ms`, `fence_reason`,
- `attempt`, `next_run_at_ms`, `last_error`,
- timestamps for creation, update, completion.

Metrics:

- active channel migration tasks,
- failed channel migration tasks,
- migration duration,
- leader transfer duration,
- fence duration and fence timeout count,
- catch-up lag,
- learner abort count,
- promote success/failure count.

Logs should include structured events for every phase transition and every fenced meta update.

## Testing Strategy

### Unit Tests

`pkg/slot/meta`:

- encode/decode `ChannelMigrationTask`.
- enforce one active task per channel.
- create/advance/complete/abort idempotency.
- fenced meta update rejects stale task, stale phase, stale epoch, expired or mismatched fence token.
- `ChannelRuntimeMeta` monotonic rules preserve retention and do not let stale migrations regress epochs.

`pkg/channel/handler`:

- append returns retryable write-fenced error when active fence exists.
- expired fence is ignored locally.
- status/fetch remain readable under write fence.

`pkg/channel/runtime` / `pkg/channel/replica`:

- `Replicas - ISR` learner receives replication but does not affect HW quorum.
- adding learner does not reduce write availability.
- promoting learner into ISR after catch-up keeps HW monotonic.
- `FenceAndDrain` rejects new appends and waits for in-flight append work.
- `FenceAndDrain` fails on role change, lease expiration, or meta epoch mismatch.

`internal/runtime/channelmigration`:

- leader transfer phase sequence.
- replica replacement phase sequence.
- source-is-leader path runs transfer before add learner.
- fence expiry returns task to `WarmCatchUp`.
- abort after `AddLearner` removes learner.
- after `PromoteAndRemove`, executor does not auto-roll back.

### Integration Tests

- three-node channel leader transfer with writes before and after transfer.
- three-node replica replacement where source is follower.
- three-node replica replacement where source is leader.
- target catch-up under concurrent writes, with short cutover freeze.
- executor restart during `WarmCatchUp` and `CutoverFence`.
- slot leader change during active task.
- target failure before promote.
- source removal after promote and stale source traffic ignored.

Integration tests that need real timing, multiple processes, or long catch-up should use `go test -tags=integration ./...` and stay out of normal unit test runs.

## Rollout Plan

A safe implementation sequence:

1. Add write fence fields and projection to `channel.Meta`.
2. Add append write-fence handling and tests.
3. Add `ChannelMigrationTask` metadata, codec, and slot FSM commands.
4. Add management dry-run and task create/query/abort use cases.
5. Add leader transfer executor.
6. Add learner semantics tests and replica replacement executor without snapshot bootstrap.
7. Add snapshot bootstrap support or explicitly gate migrations that require snapshot.
8. Add manager API routes and UI-ready DTOs.
9. Add integration tests and operational metrics.

## Open Questions

- Should V1 ship without snapshot bootstrap by rejecting target catch-up that requires a snapshot, or should snapshot bootstrap be part of the first production cut?
- What default fence TTL and catch-up stable-window values should be configured?
- Should `ErrWriteFenced` be surfaced to clients distinctly, or mapped to `ErrNotReady` at external protocol boundaries?
- How long should completed tasks be retained before GC?

## Decision Summary

- V1 uses durable `ChannelMigrationTask` rather than direct ad-hoc meta edits.
- V1 defines `Replicas - ISR` as learner replicas.
- V1 keeps writes open during learner catch-up and freezes writes only during cutover.
- V1 changes `LeaderEpoch` for leader transfer and `ChannelEpoch` for membership changes.
- V1 supports manual operations first and reserves auto scheduling for later.
