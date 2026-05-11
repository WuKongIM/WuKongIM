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
    FenceVersion uint64
    FenceUntilMS int64

    EmbeddedLeaderTransfer bool
    EmbeddedDesiredLeader  uint64

    OwnerNodeID       uint64
    OwnerLeaseUntilMS int64

    CutoverLEO uint64
    CutoverHW  uint64

    DrainedLeaderNode        uint64
    DrainedRuntimeGeneration uint64
    DrainedChannelEpoch      uint64
    DrainedLeaderEpoch       uint64
    DrainedFenceVersion      uint64

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
- Task and `ChannelRuntimeMeta` changes that must become visible together are committed by one slot Raft command and one slot meta write batch.

### Executor Ownership

Only the current slot leader should drive active channel migration tasks for channels owned by that slot. Non-owner nodes only serve local RPC primitives such as probe, target bootstrap, and leader drain.

Task claiming uses `OwnerNodeID` and `OwnerLeaseUntilMS`:

- The slot leader claims a runnable task before performing non-idempotent local side effects.
- Claim/renewal is a slot Raft command guarded by `TaskID`, expected phase, and expected owner lease.
- If the owner crashes or slot leadership changes, the task becomes runnable after the owner lease expires.
- A new owner must reread authoritative task and `ChannelRuntimeMeta`, then reconcile the observed meta/task pair before continuing.
- Owner lease expiry and fence TTL expiry never clear a write fence or release write admission. TTL expiry only makes the task eligible to commit a matching-token recovery command that increments `WriteFenceVersion`.

### Combined Task And Metadata Commands

The implementation must not rely on independent task updates plus independent meta upserts for phase transitions that affect routing or write admission. Introduce explicit slot FSM commands that mutate task and meta atomically when needed:

- `CreateChannelMigrationTask`: create the task if no active task exists for the channel.
- `ClaimChannelMigrationTask`: set or renew `OwnerNodeID` / `OwnerLeaseUntilMS`.
- `AdvanceChannelMigrationTask`: advance phases that have no metadata side effect.
- `SetChannelWriteFence`: set or renew the fence, increment `WriteFenceVersion`, and advance the task to the fenced phase in the same batch.
- `ResetChannelWriteFenceToPreCutover`: clear or supersede an expired cutover fence, increment `WriteFenceVersion`, clear `CutoverLEO`, `CutoverHW`, and all `Drained*` fields, and return the task to the task-kind-specific pre-cutover phase in the same batch. Standalone leader transfer returns to `ProbeTarget` or `WriteFence`; replica replacement returns to `WarmCatchUp`; embedded leader transfer returns to its embedded pre-fence phase.
- `CommitChannelLeaderTransfer`: update `Leader` / `LeaderEpoch` / lease and advance the task in the same batch.
- `AddChannelLearner`: add target to `Replicas`, increment `ChannelEpoch`, and advance the task in the same batch.
- `PromoteLearnerAndRemoveReplica`: replace source with target in `Replicas` and `ISR`, increment `ChannelEpoch`, and advance the task in the same batch.
- `ClearChannelWriteFence`: clear only the matching fence token, increment `WriteFenceVersion`, and complete/advance the task in the same batch.
- `AbortChannelMigration`: mark the task aborted and, when safe, remove an unpromoted learner, increment `ChannelEpoch`, clear the matching fence, and advance the task in the same batch.

Every command carries expected values for `TaskID`, status, phase, `ChannelEpoch`, `LeaderEpoch`, leader, replicas/ISR role of source and target, owner lease, and fence token as applicable. A command that observes the target state already applied must be an idempotent no-op or an idempotent phase advance; it must not return a fatal apply error merely because a previous owner committed the same transition.

### Metadata Epoch Boundary

`ChannelEpoch` remains the structural channel replication metadata version, so `AddLearner` and `PromoteAndRemove` increment it. Because current channel epoch also participates in durable epoch lineage and `OffsetEpoch` proofs, any same-leader `ChannelEpoch` increase that can be followed by appends must create a durable epoch boundary before the leader admits new appends or migration proofs under the new epoch.

Required channel-layer primitive:

- When a current leader applies a higher `ChannelEpoch` without changing leader, it gates appends, writes `EpochPoint{Epoch: newEpoch, StartOffset: currentLEO}` durably, publishes the new epoch only after that write succeeds, then reopens appends if no write fence is active.
- Followers and learners must be able to persist an epoch boundary even when no records are fetched after the metadata change; heartbeat-only epoch boundary propagation is required for proofs on idle channels.
- If durable epoch boundary persistence fails, the local runtime stays `CommitReady=false` / not appendable until retry or fresh metadata resolves the state.

## ChannelRuntimeMeta Write Fence

Add a narrow write fence to `ChannelRuntimeMeta`:

```go
WriteFenceToken   string
WriteFenceVersion uint64
WriteFenceReason  uint8
WriteFenceUntilMS int64
```

Projection to `channel.Meta` includes the same fields, ideally wrapped as a small struct:

```go
type WriteFence struct {
    Token   string
    Version uint64
    Reason  WriteFenceReason
    Until   time.Time
}
```

Append behavior before cutover drain:

- No fence: normal append.
- Fence exists and has not expired: return a retryable error, preferably `channel.ErrWriteFenced`.
- Fence exists but is expired: local append must perform or request an authoritative refresh and an authoritative task-state check before treating it as inactive. It may reopen only after applying authoritative metadata that proves the fence is absent or superseded by a newer inactive version, or after confirming no active task has a matching `DrainedFenceVersion` / cutover proof for the same token and version.

Append behavior after a successful `FenceAndDrain`:

- The local runtime is fail-closed for that channel and rejects appends regardless of wall-clock fence expiry.
- Same-version TTL expiry never releases fail-closed mode.
- Only applying authoritative metadata from a committed clear/reset/superseding fence command with a newer `WriteFenceVersion` may leave fail-closed mode.
- A process restart must refresh authoritative metadata and check authoritative task state before serving the channel as appendable if the last observed state had an active or recently drained fence. Metadata alone is insufficient when an active task may still hold a matching `DrainedFenceVersion` / cutover proof.

Fence rules:

- Fence token is normally `TaskID`.
- `WriteFenceVersion` is monotonic per channel and changes on every set, renew, or clear.
- Only the task that set a fence may clear it.
- Fence has a short TTL, for example 10-30 seconds, but TTL expiry is a recovery signal, not permission to reuse an old drain proof.
- Executor renews the fence while in cutover phases by incrementing `WriteFenceVersion`.
- Any renewal after `FenceAndDrain` invalidates the stored drain proof and requires a fresh authoritative fenced-meta apply plus a new `FenceAndDrain` before final commit.
- `CommitLeaderMeta` and `PromoteAndRemove` must require a still-valid matching fence token and `WriteFenceVersion == DrainedFenceVersion`.
- Set/renew/clear fence commands invalidate channel meta activation caches for that channel.
- Cutover phases must verify the current channel leader has applied the authoritative fenced meta and has executed `FenceAndDrain` for the same token, fence version, and local runtime generation.

Fence enforcement must happen in both handler admission and replica-loop admission. A stale handler meta cache must not be sufficient to append if the replica has an active local fence or a drained fail-closed fence, and a stale local runtime must not be allowed to proceed into cutover without a fresh authoritative fenced meta apply.

## Leader Transfer State Machine

Leader transfer changes only channel leader identity.

```text
Pending
  -> Validate
  -> ProbeTarget
  -> WriteFence
  -> DrainLeader
  -> FinalTargetCatchUp
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
- Return stable `LEO`, `HW`, `CheckpointHW`, `ChannelEpoch`, `LeaderEpoch`, `WriteFenceVersion`, and local runtime generation.
- Fail on lease expiration, meta change, role change, or context timeout.

The drain result is stored in the task as the cutover proof seed. Later metadata commits must verify the same fence token, fence version, leader epoch, and drained runtime generation. If the authoritative fence is renewed or otherwise changes version after drain, the old drain proof is invalid and the task must reapply the fence locally and drain again.

### FinalTargetCatchUp

After drain, writes are fenced and the old leader has captured `CutoverHW` / `CutoverLEO`. The executor must re-evaluate the desired target under the same authoritative `ChannelEpoch` and `LeaderEpoch` before committing the leader change.

The target proof must show:

- `CheckpointHW >= CutoverHW`.
- The target's epoch lineage is compatible with the current leader at `CutoverHW` and does not require truncation below `CutoverHW`.
- No pending `TruncateTo` or `ErrSnapshotRequired` blocks the target from serving the committed prefix.
- If the target is behind but can still catch up through normal fetch, keep the fence and wait.
- If the target requires snapshot bootstrap and snapshot bootstrap is unavailable, fail closed before `CommitLeaderMeta`.

This phase prevents a lagging target from becoming leader and reconciling away writes that were acknowledged by the old leader after the initial `ProbeTarget` phase.

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

The same slot Raft command also advances the task phase. It must verify the post-drain target proof and the still-valid matching fence token/version.

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

If source is current leader, the `ReplicaReplace` task uses embedded leader-transfer subphases in the same durable task. It must not create a nested `LeaderTransfer` task because V1 allows only one active task per channel.

Embedded behavior:

- choose a desired leader from existing `ISR - source`;
- set `EmbeddedLeaderTransfer=true` and `EmbeddedDesiredLeader` on the same task;
- execute the same validation, write-fence, drain, final target proof, and `CommitLeaderMeta` safety rules as standalone leader transfer;
- after the new leader is verified and the embedded fence is cleared, advance the same `ReplicaReplace` task back to `AddLearner` rather than completing a separate task;
- reread authoritative meta after transfer and continue only if source is no longer leader.

V1 deliberately avoids direct source-leader-to-target-learner cutover because target is not yet an ISR member.

### AddLearner

Commit authoritative metadata update and task phase advance in one slot Raft command:

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

The state machine must make this gate deterministic. If snapshot bootstrap is not implemented or is disabled for V1, any migration that reaches `ErrSnapshotRequired` fails or remains blocked with a `NeedsSnapshotBootstrap` reason before promotion. It must never promote a target that cannot prove the committed prefix.

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

Store returned `CutoverLEO` and `CutoverHW` in the task. If the fence expires before final catch-up completes, the task must not locally discard the old cutover point or reopen writes. The task owner must commit `ResetChannelWriteFenceToPreCutover`, which increments `WriteFenceVersion`, clears `CutoverLEO`, `CutoverHW`, and all `Drained*` fields, and returns the task to the correct pre-cutover phase for its kind: standalone leader transfer returns to `ProbeTarget` or `WriteFence`, replica replacement returns to `WarmCatchUp`, and embedded leader transfer returns to its embedded pre-fence phase. The old leader leaves fail-closed mode only after applying that newer-version authoritative reset.

### FinalCatchUp

Because writes are fenced, target must produce a final proof under the current authoritative `ChannelEpoch` and `LeaderEpoch`:

```text
target LEO >= CutoverLEO
target CheckpointHW >= CutoverHW
```

Watermarks alone are not enough. The proof must also show compatible epoch lineage at the cutover point, no pending truncation below `CutoverHW`, and no `ErrSnapshotRequired`. If the target has a divergent local tail, it must reconcile/truncate safely before this phase succeeds. If it needs a snapshot and snapshot bootstrap is unavailable, fail closed before promotion.

### PromoteAndRemove

Commit authoritative metadata update and task phase advance atomically:

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
- target final catch-up proof is satisfied, including compatible epoch lineage and no pending truncation/snapshot requirement.
- the stored drain result still matches the current leader epoch, fence version, and runtime generation observed by the leader.

### VerifyMembership

Verify:

- leader applies the new meta and is `CommitReady=true`,
- target applies the new meta and can answer probe as an ISR follower,
- source is no longer a valid replication peer in the new meta,
- no stale fence was overwritten.

### ClearFence

Clear the matching fence token and complete the task.

## Leader Repair And Lease Renewal Interaction

Leader repair and lease renewal must be fence-aware:

- Repair commands must preserve `WriteFenceToken`, `WriteFenceVersion`, `WriteFenceReason`, and `WriteFenceUntilMS` unless they are the matching migration task command that clears the fence.
- Lease renewal must not clear or shorten an active migration fence.
- If an active cutover fence exists, repair may change `Leader` only through a command that preserves the fence and records that the migration task must revalidate its phase.
- Migration executor must reread task and meta after any `LeaderEpoch` change.
- `resolveMonotonicChannelRuntimeMeta` and repair-specific store commands must preserve fence fields in the same way retention fields are preserved today.

## Failure Recovery

Recovery strategy is retry-oriented before cutover and forward-only after promote.

| Phase | Recovery behavior |
| --- | --- |
| Before `AddLearner` | No membership side effect; retry or fail task. |
| After `AddLearner`, before `PromoteAndRemove` | Target is learner only; continue, or abort by removing target from `Replicas` with `ChannelEpoch++` and the same durable epoch-boundary gate. |
| During fence phases | Renew valid fence and continue, or if expired commit `ResetChannelWriteFenceToPreCutover` with `WriteFenceVersion++`, clear cutover/drain proofs, and only then return to the task-kind-specific pre-cutover phase. |
| After `PromoteAndRemove` | Continue `VerifyMembership` / `ClearFence`; do not auto-roll back. |
| Clear fence failure | Keep retrying; TTL is only a safety net, not the normal completion path. |

Node failure behavior:

- source dead before promote: continue only if current leader and target safety can still be proven; otherwise fail closed.
- source leader dead: rely on existing leader repair/transfer safety; if no safe leader exists, fail closed.
- abort after learner add: removing the learner is a structural metadata change and must increment `ChannelEpoch`, preserve/clear only matching fence fields, and trigger same-leader epoch boundary persistence before appends reopen.
- target dead before promote: abort learner and fail/abort task.
- target dead after promote: target is already ISR; rely on repair or a new reverse migration task.

## Concurrency Control

- One active channel migration task per channel.
- Global and per-target concurrency limits in `channelmigration` executor.
- Draining workflows may create many tasks, but executor advances them under limits.
- All writes to task and meta go through slot Raft commands with expected task/phase/epoch fences.
- Only the claimed owner may perform local side effects for a task; other nodes serve local RPC primitives.
- Leader repair may run while migration exists, but it must preserve migration fences and force the task owner to revalidate.
- If leader repair changes `LeaderEpoch`, migration executor must reread meta and revalidate before continuing.
- `channelMetaSync` only applies authoritative meta; it does not decide migration phase.

## Configuration

If implementation adds tunables, they must use `WK_` keys, include detailed English comments on config fields, and update `wukongim.conf.example` in the same change.

Expected V1 tunables:

- channel migration fence TTL,
- target catch-up stable window and lag threshold,
- global active channel migration concurrency,
- per-source and per-target migration concurrency,
- task owner lease TTL,
- completed task retention TTL.

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
- create/claim/advance/complete/abort idempotency.
- combined task+meta commands are atomic and recoverable after a crash at every phase.
- commit commands such as `CommitChannelLeaderTransfer` and `PromoteLearnerAndRemoveReplica` reject stale task, stale phase, stale epoch, expired fence, or mismatched fence token/version.
- recovery commands such as `ResetChannelWriteFenceToPreCutover`, `ClearChannelWriteFence`, and safe abort accept an expired matching fence only with expected task/phase and `WriteFenceVersion++`.
- repair and lease renewal preserve active write fence fields, including `WriteFenceVersion`.
- `ChannelRuntimeMeta` monotonic rules preserve retention and write fence fields and do not let stale migrations regress epochs or fence versions.

`pkg/channel/handler`:

- append returns retryable write-fenced error when active fence exists.
- expired pre-drain fence triggers authoritative refresh and task-state check before append admission.
- drained/fail-closed runtime keeps rejecting appends until it applies a committed no-fence or newer-version reset/clear and no active task has a matching drain proof.
- status/fetch remain readable under write fence.

`pkg/channel/runtime` / `pkg/channel/replica`:

- `Replicas - ISR` learner receives replication but does not affect HW quorum.
- adding learner does not reduce write availability.
- same-leader `ChannelEpoch` increase persists an epoch boundary before reopening appends.
- heartbeat-only epoch boundary propagation works for idle channels.
- promoting learner into ISR after catch-up keeps HW monotonic.
- `FenceAndDrain` rejects new appends and waits for in-flight append work.
- replica-loop fence prevents stale handler metadata from admitting appends.
- `FenceAndDrain` fails on role change, lease expiration, or meta epoch mismatch.

`internal/runtime/channelmigration`:

- leader transfer phase sequence, including post-drain `FinalTargetCatchUp`.
- fence renewal after drain invalidates old drain proof and forces re-drain.
- lagging-target leader transfer does not lose a write committed by old leader and another ISR replica.
- replica replacement phase sequence.
- source-is-leader path uses embedded transfer subphases in the same `ReplicaReplace` task before add learner.
- duplicate executor claim race is resolved by owner lease CAS.
- stale cached append during active fence is rejected.
- fence expiry returns task to its kind-specific pre-cutover phase only through committed `ResetChannelWriteFenceToPreCutover` with `WriteFenceVersion++`.
- abort after `AddLearner` removes learner with `ChannelEpoch++` and epoch-boundary persistence.
- after `PromoteAndRemove`, executor does not auto-roll back.

### Integration Tests

- three-node channel leader transfer with writes before and after transfer.
- three-node replica replacement where source is follower.
- three-node replica replacement where source is leader.
- target catch-up under concurrent writes, with short cutover freeze.
- executor restart during `WarmCatchUp` and `CutoverFence`.
- crash/restart after every combined task+meta Raft command.
- slot leader change during active task and owner handoff after owner lease expiry.
- target failure before promote.
- leader restart while write fence is active or while local drained fail-closed mode was active.
- concurrent leader repair/lease renewal during active migration preserves fence and forces revalidation.
- `ChannelEpoch` boundary and `OffsetEpoch` proofs remain correct across AddLearner, abort-after-learner, and PromoteAndRemove.
- snapshot-required migration is rejected or blocked before promotion when snapshot bootstrap is unavailable.
- single-node cluster operations return deterministic no-op or validation errors without a separate non-cluster branch.
- source removal after promote and stale source traffic ignored.

Integration tests that need real timing, multiple processes, or long catch-up should use `go test -tags=integration ./...` and stay out of normal unit test runs.

## Rollout Plan

A safe implementation sequence:

1. Add write fence fields and projection to `channel.Meta`, including cache invalidation on set/clear.
2. Add append write-fence handling in handler and replica loop, plus `FenceAndDrain` tests.
3. Add same-leader channel epoch boundary persistence and heartbeat-only boundary propagation.
4. Add `ChannelMigrationTask` metadata, codec, owner lease, and combined task+meta slot FSM commands.
5. Add management dry-run and task create/query/abort use cases.
6. Add leader transfer executor with post-drain final target proof.
7. Add learner semantics tests and replica replacement executor with final lineage proof.
8. Add snapshot bootstrap support or explicitly gate migrations that require snapshot.
9. Add manager API routes and UI-ready DTOs.
10. Add integration tests and operational metrics.

## Open Questions

- Should the first production cut include snapshot bootstrap, or should it ship with a hard `NeedsSnapshotBootstrap` gate that blocks affected migrations before promotion?
- What default fence TTL, owner lease TTL, and catch-up stable-window values should be configured?
- Should `ErrWriteFenced` be surfaced to clients distinctly, or mapped to `ErrNotReady` at external protocol boundaries?
- How long should completed tasks be retained before GC?

## Decision Summary

- V1 uses durable `ChannelMigrationTask` rather than direct ad-hoc meta edits.
- V1 defines `Replicas - ISR` as learner replicas.
- V1 keeps writes open during learner catch-up and freezes writes only during cutover.
- V1 changes `LeaderEpoch` for leader transfer and `ChannelEpoch` for membership changes.
- V1 supports manual operations first and reserves auto scheduling for later.
