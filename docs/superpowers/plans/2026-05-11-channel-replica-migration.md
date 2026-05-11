# Channel Replica Migration Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement V1 manual channel leader transfer and one-replica replacement with durable tasks, write fencing, learner catch-up, and recoverable cutover safety.

**Architecture:** Add authoritative `ChannelMigrationTask` and write-fence metadata in the slot layer, project it into channel runtime, add channel ISR primitives for write fencing, drain, learner proof, and same-leader epoch boundaries, then build a node-local `internal/runtime/channelmigration` executor wired from `internal/app` and exposed through manager APIs. V1 gates migrations that require snapshot bootstrap with a deterministic `NeedsSnapshotBootstrap` blocker; full channel snapshot bootstrap is deferred behind this gate.

**Tech Stack:** Go, Pebble, etcd/raft-backed slot FSM, existing `pkg/channel` ISR runtime, manager HTTP adapters, unit tests plus targeted integration tests.

**Design doc:** `docs/superpowers/specs/2026-05-11-channel-replica-migration-design.md`

---

## Scope And Execution Notes

- This plan intentionally implements V1 without channel snapshot bootstrap. If target catch-up returns `ErrSnapshotRequired`, the migration blocks/fails before promote with a clear `NeedsSnapshotBootstrap` reason.
- Read package `FLOW.md` files before editing: `pkg/channel/FLOW.md`, `pkg/channel/replica/FLOW.md`, `pkg/slot/FLOW.md`, `internal/runtime/channelmeta/FLOW.md`, `internal/FLOW.md`.
- Keep cluster-only semantics: a single-node deployment is a single-node cluster. Do not introduce separate non-cluster branches.
- Any new config key must use `WK_`, have detailed English comments in Go config fields, and update `wukongim.conf.example` in the same change.
- Each task should end with a focused commit. Do not commit unrelated dirty files.

## File Structure

### New files

- `pkg/slot/meta/channel_migration_task.go` — durable task types, validation, normalization, primary-key store helpers, active-task query helpers.
- `pkg/slot/meta/channel_migration_task_test.go` — task codec/store/idempotency/active-task tests.
- `pkg/slot/fsm/channel_migration_cmds.go` — slot FSM commands for create/claim/advance/fence/reset/leader-transfer/add-learner/promote/clear/abort.
- `pkg/slot/fsm/channel_migration_cmds_test.go` — command encode/decode and state-machine atomicity tests.
- `pkg/slot/proxy/channel_migration_rpc.go` — authoritative/local proxy methods for channel migration task operations.
- `pkg/slot/proxy/channel_migration_codec.go` — RPC payload codec for migration operations if proxy RPC needs non-FSM DTOs.
- `pkg/slot/proxy/channel_migration_rpc_test.go` — proxy tests for local and authoritative paths.
- `pkg/channel/transport/migration_control.go` — node-to-node channel migration control RPC client/server for remote `FenceAndDrain` and proof primitives.
- `pkg/channel/transport/migration_control_test.go` — migration control RPC routing, encoding, and remote drain tests.
- `internal/runtime/channelmigration/types.go` — neutral task DTOs, phases, progress, errors, ports.
- `internal/runtime/channelmigration/metrics.go` — lightweight metrics/logging hooks for phase transitions, fences, retries, blockers, and task durations.
- `internal/runtime/channelmigration/executor.go` — task scan/claim/tick loop and phase dispatch.
- `internal/runtime/channelmigration/gc.go` — terminal-task retention cleanup driven by executor ticks.
- `internal/runtime/channelmigration/leader_transfer.go` — standalone and embedded leader-transfer phase implementation.
- `internal/runtime/channelmigration/replica_replace.go` — add learner, warm catch-up, cutover, final proof, promote/remove.
- `internal/runtime/channelmigration/proof.go` — target proof helpers using channel probe/evaluation ports.
- `internal/runtime/channelmigration/executor_test.go` — executor claim/recovery/fence expiry tests with fakes.
- `internal/runtime/channelmigration/leader_transfer_test.go` — leader transfer safety and lagging-target tests.
- `internal/runtime/channelmigration/replica_replace_test.go` — replica replacement phase tests.
- `internal/usecase/management/channel_migration.go` — manager use cases for dry-run, create, query, abort.
- `internal/usecase/management/channel_migration_test.go` — usecase validation and DTO mapping tests.
- `internal/access/manager/channel_migration.go` — HTTP handlers for manual channel migration APIs.
- `internal/access/manager/channel_migration_test.go` — route/JSON/status tests.
- `internal/app/channelmigration.go` — composition-root wiring and lifecycle registration.

### Modified files

- `pkg/channel/types.go` — add `WriteFence`, `WriteFenceReason`, `ErrWriteFenced`, drain/proof DTOs as needed.
- `pkg/channel/errors.go` — add `ErrWriteFenced`.
- `pkg/channel/handler/append.go` — reject active write fence before append.
- `pkg/channel/handler/meta.go` — clone/compare write fence fields.
- `pkg/channel/runtime/types.go` — add runtime ports for `FenceAndDrain`, migration proof, and epoch-boundary support.
- `pkg/channel/runtime/runtime.go` — apply/write fence projection, expose drain/proof methods, ensure meta apply gates on epoch boundary.
- `pkg/channel/runtime/backpressure.go` / `pkg/channel/runtime/replicator.go` / `pkg/channel/runtime/lanes.go` — propagate heartbeat epoch boundary if implementation requires lane protocol changes.
- `pkg/channel/replica/types.go` — add drain and epoch-boundary interfaces on `Replica`.
- `pkg/channel/replica/lifecycle_pipeline.go` — same-leader `ChannelEpoch` increase writes durable epoch boundary before reopening appends.
- `pkg/channel/replica/append_pipeline.go` — replica-loop write fence / drained fail-closed admission.
- `pkg/channel/replica/reconcile_coordinator.go` — ensure drain/fence proof does not conflict with reconcile state.
- `pkg/channel/replica/follower_apply.go` — persist heartbeat-only epoch boundary.
- `pkg/channel/replica/replication.go` / `pkg/channel/replica/progress.go` — keep learners replicating from `Replicas` while excluding them from ISR/HW quorum.
- `pkg/channel/replica/commands.go` — loop commands/events for fence, drain, and epoch-boundary result.
- `pkg/channel/store/durability.go` / `pkg/channel/store/history.go` — add a small durable helper for epoch boundary if existing `BeginEpoch` adapter cannot be reused directly.
- `pkg/channel/transport/transport.go` / `pkg/channel/transport/codec.go` — register migration control RPC service IDs and codecs.
- `pkg/slot/meta/channel_runtime_meta.go` — add write-fence fields, validation, monotonic preservation, encode/decode.
- `pkg/slot/fsm/command.go` — register migration commands.
- `pkg/slot/fsm/statemachine.go` — apply migration commands atomically to task + runtime meta.
- `pkg/slot/proxy/store.go` — expose migration methods and fenced runtime-meta commands.
- `internal/runtime/channelmeta/resolver.go` — project write fence to `channel.Meta`, invalidate activation cache on fence version changes.
- `internal/runtime/channelmeta/repair.go` — preserve write fence fields during leader repair/lease renewal.
- `internal/runtime/channelmeta/interfaces.go` — add migration/fence-aware store methods if needed.
- `internal/app/channelmeta.go` — wire projection and cache invalidation hooks if not contained in resolver.
- `internal/app/build.go` / `internal/app/channelcluster.go` — wire migration control RPC service and client with the existing channel transport.
- `internal/app/app.go` or lifecycle registration file — add `channelmigration` lifecycle component after channelmeta is ready.
- `internal/usecase/management/types.go` if needed — expose manager DTOs.
- `internal/access/manager/routes.go` — register channel migration routes.
- `pkg/cluster/config.go` or `internal/app/config.go` depending current config owner — add `WK_` migration tuning keys.
- `wukongim.conf.example` — document new config keys.
- `pkg/channel/FLOW.md`, `pkg/channel/replica/FLOW.md`, `pkg/slot/FLOW.md`, `internal/runtime/channelmeta/FLOW.md` — update after behavior changes.

---

## Task 1: Add Write-Fence Fields To Slot Metadata

**Files:**
- Modify: `pkg/slot/meta/channel_runtime_meta.go`
- Modify: `pkg/slot/meta/codec.go` or the current channel-runtime-meta codec helpers if separate
- Test: `pkg/slot/meta/channel_runtime_meta_test.go`

- [ ] **Step 1: Read flow and current codec**

Run:
```bash
sed -n '1,220p' pkg/slot/FLOW.md
sed -n '1,260p' pkg/slot/meta/channel_runtime_meta.go
rg -n "ChannelRuntimeMeta|channelRuntimeMeta" pkg/slot/meta
```

- [ ] **Step 2: Write failing tests for write-fence persistence and monotonic preservation**

Add tests like:
```go
func TestChannelRuntimeMetaWriteFenceRoundTrip(t *testing.T) {
    store := newTestShardStore(t)
    meta := validChannelRuntimeMeta("ch", 1)
    meta.WriteFenceToken = "task-1"
    meta.WriteFenceVersion = 3
    meta.WriteFenceReason = 1
    meta.WriteFenceUntilMS = 1710000000000

    require.NoError(t, store.UpsertChannelRuntimeMeta(context.Background(), meta))
    got, err := store.GetChannelRuntimeMeta(context.Background(), "ch", 1)
    require.NoError(t, err)
    require.Equal(t, meta.WriteFenceToken, got.WriteFenceToken)
    require.Equal(t, meta.WriteFenceVersion, got.WriteFenceVersion)
    require.Equal(t, meta.WriteFenceReason, got.WriteFenceReason)
    require.Equal(t, meta.WriteFenceUntilMS, got.WriteFenceUntilMS)
}

func TestChannelRuntimeMetaMonotonicPreservesWriteFence(t *testing.T) {
    existing := validChannelRuntimeMeta("ch", 1)
    existing.ChannelEpoch = 5
    existing.LeaderEpoch = 7
    existing.WriteFenceToken = "task-1"
    existing.WriteFenceVersion = 9
    existing.WriteFenceReason = 2
    existing.WriteFenceUntilMS = 1710000000000

    candidate := existing
    candidate.ChannelEpoch = 6
    candidate.WriteFenceToken = ""
    candidate.WriteFenceVersion = 0
    candidate.WriteFenceReason = 0
    candidate.WriteFenceUntilMS = 0

    got, write := resolveMonotonicChannelRuntimeMeta(existing, true, candidate)
    require.True(t, write)
    require.Equal(t, existing.WriteFenceToken, got.WriteFenceToken)
    require.Equal(t, existing.WriteFenceVersion, got.WriteFenceVersion)
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run:
```bash
go test ./pkg/slot/meta -run 'TestChannelRuntimeMetaWriteFence|TestChannelRuntimeMetaMonotonicPreservesWriteFence' -count=1
```
Expected: FAIL because fields/codec do not exist.

- [ ] **Step 4: Implement fields and codec**

Add English comments:
```go
type ChannelRuntimeMeta struct {
    // ...existing fields...
    // WriteFenceToken identifies the migration task currently fencing writes for this channel.
    WriteFenceToken string
    // WriteFenceVersion is a monotonic per-channel fence generation changed by set, renew, reset, or clear commands.
    WriteFenceVersion uint64
    // WriteFenceReason describes why writes are fenced for diagnostics and API responses.
    WriteFenceReason uint8
    // WriteFenceUntilMS is the wall-clock deadline for the current fence lease in milliseconds.
    WriteFenceUntilMS int64
}
```

Update binary encode/decode with a versioned field-mask compatible with existing codec style. Add preservation helper similar to retention:
```go
func preserveWriteFence(existing ChannelRuntimeMeta, candidate *ChannelRuntimeMeta) {
    if candidate.WriteFenceVersion < existing.WriteFenceVersion {
        candidate.WriteFenceToken = existing.WriteFenceToken
        candidate.WriteFenceVersion = existing.WriteFenceVersion
        candidate.WriteFenceReason = existing.WriteFenceReason
        candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS
        return
    }
    if candidate.WriteFenceVersion == existing.WriteFenceVersion && candidate.WriteFenceToken == "" && existing.WriteFenceToken != "" {
        candidate.WriteFenceToken = existing.WriteFenceToken
        candidate.WriteFenceReason = existing.WriteFenceReason
        candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS
    }
}
```

- [ ] **Step 5: Run tests to verify pass**

Run:
```bash
go test ./pkg/slot/meta -run 'TestChannelRuntimeMetaWriteFence|TestChannelRuntimeMetaMonotonicPreservesWriteFence' -count=1
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/slot/meta/channel_runtime_meta.go pkg/slot/meta/*test.go
git commit -m "feat: persist channel write fence metadata"
```

---

## Task 2: Add Durable ChannelMigrationTask Store

**Files:**
- Create: `pkg/slot/meta/channel_migration_task.go`
- Create: `pkg/slot/meta/channel_migration_task_test.go`
- Modify: `pkg/slot/meta/codec.go` or local codec helper file if needed

- [ ] **Step 1: Write failing task store tests**

Test cases:
```go
func TestChannelMigrationTaskCreateAndGet(t *testing.T) { /* create, load, compare fields */ }
func TestChannelMigrationTaskRejectsSecondActiveTaskForChannel(t *testing.T) { /* active uniqueness */ }
func TestChannelMigrationTaskClaimOwnerLease(t *testing.T) { /* owner CAS */ }
func TestChannelMigrationTaskAdvancePersistsProgressAndRetry(t *testing.T) { /* phase-only advance */ }
func TestChannelMigrationTaskBlockedNeedsSnapshotBootstrap(t *testing.T) { /* blocker is durable */ }
func TestChannelMigrationTaskTerminalAllowsNewTask(t *testing.T) { /* completed task does not block */ }
func TestChannelMigrationTaskGarbageCollectsTerminalAfterRetention(t *testing.T) { /* completed retention */ }
func TestChannelMigrationTaskEncodeDecodeFullFields(t *testing.T) { /* cutover/drained/fence embedded fields */ }
```

- [ ] **Step 2: Run tests to verify fail**

Run:
```bash
go test ./pkg/slot/meta -run 'TestChannelMigrationTask' -count=1
```
Expected: FAIL because APIs do not exist.

- [ ] **Step 3: Implement task types**

Suggested shape:
```go
type ChannelMigrationKind uint8
const (
    ChannelMigrationKindLeaderTransfer ChannelMigrationKind = 1
    ChannelMigrationKindReplicaReplace ChannelMigrationKind = 2
)

type ChannelMigrationStatus uint8
const (
    ChannelMigrationStatusPending ChannelMigrationStatus = 1
    ChannelMigrationStatusRunning ChannelMigrationStatus = 2
    ChannelMigrationStatusBlocked ChannelMigrationStatus = 3
    ChannelMigrationStatusCompleted ChannelMigrationStatus = 4
    ChannelMigrationStatusFailed ChannelMigrationStatus = 5
    ChannelMigrationStatusAborted ChannelMigrationStatus = 6
)

type ChannelMigrationPhase uint8
const (
    ChannelMigrationPhaseValidate ChannelMigrationPhase = 1
    ChannelMigrationPhaseProbeTarget ChannelMigrationPhase = 2
    ChannelMigrationPhaseWriteFence ChannelMigrationPhase = 3
    ChannelMigrationPhaseDrainLeader ChannelMigrationPhase = 4
    ChannelMigrationPhaseFinalTargetCatchUp ChannelMigrationPhase = 5
    ChannelMigrationPhaseCommitLeaderMeta ChannelMigrationPhase = 6
    ChannelMigrationPhaseVerifyNewLeader ChannelMigrationPhase = 7
    ChannelMigrationPhaseAddLearner ChannelMigrationPhase = 20
    ChannelMigrationPhaseBootstrapTarget ChannelMigrationPhase = 21
    ChannelMigrationPhaseWarmCatchUp ChannelMigrationPhase = 22
    ChannelMigrationPhaseCutoverFence ChannelMigrationPhase = 23
    ChannelMigrationPhasePromoteAndRemove ChannelMigrationPhase = 25
    ChannelMigrationPhaseVerifyMembership ChannelMigrationPhase = 26
    ChannelMigrationPhaseClearFence ChannelMigrationPhase = 27
)

type ChannelMigrationTask struct {
    // TaskID uniquely identifies one channel migration attempt.
    TaskID string
    // Kind selects leader transfer or one-replica replacement semantics.
    Kind ChannelMigrationKind
    // Status is the durable task lifecycle state.
    Status ChannelMigrationStatus
    // Phase is the resumable executor phase inside StatusRunning.
    Phase ChannelMigrationPhase
    ChannelID string
    ChannelType int64
    SourceNode uint64
    TargetNode uint64
    DesiredLeader uint64
    BaseChannelEpoch uint64
    BaseLeaderEpoch uint64
    FenceToken string
    FenceVersion uint64
    FenceUntilMS int64
    EmbeddedLeaderTransfer bool
    EmbeddedDesiredLeader uint64
    OwnerNodeID uint64
    OwnerLeaseUntilMS int64
    CutoverLEO uint64
    CutoverHW uint64
    DrainedLeaderNode uint64
    DrainedRuntimeGeneration uint64
    DrainedChannelEpoch uint64
    DrainedLeaderEpoch uint64
    DrainedFenceVersion uint64
    Attempt uint32
    NextRunAtMS int64
    BlockerCode string
    BlockerMessage string
    LastError string
    CreatedAtMS int64
    UpdatedAtMS int64
    CompletedAtMS int64
    Progress ChannelMigrationProgress
}
```

Store helpers:
```go
func (s *ShardStore) CreateChannelMigrationTask(ctx context.Context, task ChannelMigrationTask) error
func (s *ShardStore) GetChannelMigrationTask(ctx context.Context, channelID string, channelType int64, taskID string) (ChannelMigrationTask, error)
func (s *ShardStore) GetActiveChannelMigrationTask(ctx context.Context, channelID string, channelType int64) (ChannelMigrationTask, bool, error)
func (s *ShardStore) ListChannelMigrationTasks(ctx context.Context) ([]ChannelMigrationTask, error)
func (s *ShardStore) DeleteTerminalChannelMigrationTasksBefore(ctx context.Context, beforeMS int64, limit int) (int, error)
func (s *ShardStore) upsertChannelMigrationTaskLocked(batch *pebble.Batch, task ChannelMigrationTask) error
```

- [ ] **Step 4: Implement validation, key encoding, and retention indexes**

Use a new table ID or state key family consistent with `pkg/slot/meta` key layout. Active uniqueness can be enforced by scanning channel task keys or by an active-task index. Prefer a primary task key plus active index if tests show scan is expensive enough.
Add a terminal-task index by `CompletedAtMS` so completed/failed/aborted tasks can be retained for `ChannelMigrationCompletedRetentionTTL` and garbage collected without scanning all channel metadata.

- [ ] **Step 5: Run tests**

```bash
go test ./pkg/slot/meta -run 'TestChannelMigrationTask' -count=1
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/slot/meta/channel_migration_task.go pkg/slot/meta/channel_migration_task_test.go pkg/slot/meta/codec.go
git commit -m "feat: add channel migration task store"
```

---

## Task 3: Add Atomic Slot FSM Commands For Migration

**Files:**
- Create: `pkg/slot/fsm/channel_migration_cmds.go`
- Create: `pkg/slot/fsm/channel_migration_cmds_test.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/statemachine.go`
- Modify: `pkg/slot/FLOW.md` after implementation

- [ ] **Step 1: Read FSM command patterns**

Run:
```bash
sed -n '1,260p' pkg/slot/FLOW.md
sed -n '1,260p' pkg/slot/fsm/command.go
sed -n '1,220p' pkg/slot/fsm/migration_cmds.go
sed -n '1,260p' pkg/slot/fsm/statemachine.go
```

- [ ] **Step 2: Write failing command codec tests**

Test each command round-trip:
```go
func TestChannelMigrationCommandCodecRoundTrip(t *testing.T) {
    cases := []Command{
        NewCreateChannelMigrationTaskCommand(validTask()),
        NewClaimChannelMigrationTaskCommand(validClaim()),
        NewAdvanceChannelMigrationTaskCommand(validAdvanceRequest()),
        NewSetChannelWriteFenceCommand(validFenceRequest()),
        NewResetChannelWriteFenceToPreCutoverCommand(validResetRequest()),
        NewCommitChannelLeaderTransferCommand(validLeaderCommitRequest()),
        NewAddChannelLearnerCommand(validAddLearnerRequest()),
        NewPromoteLearnerAndRemoveReplicaCommand(validPromoteRequest()),
        NewClearChannelWriteFenceCommand(validClearRequest()),
        NewAbortChannelMigrationCommand(validAbortRequest()),
    }
    for _, cmd := range cases { assertRoundTrip(t, cmd) }
}
```

- [ ] **Step 3: Write failing atomicity tests**

Examples:
```go
func TestStateMachineAddChannelLearnerUpdatesTaskAndMetaAtomically(t *testing.T) { /* task phase + meta replicas epoch in one apply */ }
func TestStateMachinePromoteRejectsExpiredFence(t *testing.T) { /* commit command rejects expired fence */ }
func TestStateMachineResetAcceptsExpiredMatchingFenceAndBumpsVersion(t *testing.T) { /* recovery command */ }
func TestStateMachineAbortAfterLearnerIncrementsChannelEpoch(t *testing.T) { /* structural change */ }
func TestStateMachineAdvancePersistsProgressWithoutMetaMutation(t *testing.T) { /* progress, retry, blocker */ }
func TestStateMachineAdvanceRejectsStalePhase(t *testing.T) { /* no stale overwrite */ }
```

- [ ] **Step 4: Run tests to verify fail**

```bash
go test ./pkg/slot/fsm -run 'TestChannelMigration|TestStateMachine.*Channel' -count=1
```
Expected: FAIL.

- [ ] **Step 5: Implement command DTOs and encoders**

Define request structs with English comments and expected fences:
```go
type ChannelMigrationFenceExpect struct {
    TaskID string
    ExpectedPhase meta.ChannelMigrationPhase
    ExpectedChannelEpoch uint64
    ExpectedLeaderEpoch uint64
    ExpectedLeader uint64
    ExpectedFenceToken string
    ExpectedFenceVersion uint64
    NowMS int64
}

type ChannelMigrationAdvanceRequest struct {
    // TaskID identifies the migration task being advanced.
    TaskID string
    // ExpectedPhase guards against stale executors overwriting newer progress.
    ExpectedPhase meta.ChannelMigrationPhase
    // NextPhase is the phase to persist when the current phase has no metadata side effect.
    NextPhase meta.ChannelMigrationPhase
    // NextStatus optionally marks a task running, blocked, failed, or completed.
    NextStatus meta.ChannelMigrationStatus
    // Progress carries durable operator-visible phase progress such as lag and cutover proof IDs.
    Progress meta.ChannelMigrationProgress
    // CutoverProof optionally persists Cutover* and Drained* fields produced by a successful remote drain.
    CutoverProof meta.ChannelMigrationCutoverProof
    // NextRunAtMS stores retry backoff for resumable transient errors.
    NextRunAtMS int64
    // BlockerCode stores deterministic blockers such as NeedsSnapshotBootstrap.
    BlockerCode string
    // LastError stores the latest transient or terminal failure message.
    LastError string
    NowMS int64
}
```

Each command apply must:
- load current task and runtime meta under store lock;
- verify expected task/status/phase/owner/epoch/fence;
- mutate task and meta together in one batch;
- keep `AdvanceChannelMigrationTask` task-only: it may persist phase/progress/retry/blocker/failure state but must not mutate `ChannelRuntimeMeta`;
- persist `CutoverLEO`, `CutoverHW`, and `Drained*` proof fields through `AdvanceChannelMigrationTask` immediately after `FenceAndDrain`, because later final catch-up and commit phases rely on this durable proof after executor restart;
- treat already-applied target state as idempotent no-op or phase advance;
- distinguish commit commands from recovery commands for expired fences.

- [ ] **Step 6: Register commands**

Add command type IDs in `pkg/slot/fsm/command.go`. Keep them away from existing IDs. Update inspection helpers if present.

- [ ] **Step 7: Run tests**

```bash
go test ./pkg/slot/fsm -run 'TestChannelMigration|TestStateMachine.*Channel' -count=1
```
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/slot/fsm/channel_migration_cmds.go pkg/slot/fsm/channel_migration_cmds_test.go pkg/slot/fsm/command.go pkg/slot/fsm/statemachine.go pkg/slot/FLOW.md
git commit -m "feat: add channel migration fsm commands"
```

---

## Task 4: Expose Slot Proxy APIs For Channel Migration Tasks

**Files:**
- Create: `pkg/slot/proxy/channel_migration_rpc.go`
- Create: `pkg/slot/proxy/channel_migration_codec.go` if needed
- Create: `pkg/slot/proxy/channel_migration_rpc_test.go`
- Modify: `pkg/slot/proxy/store.go`

- [ ] **Step 1: Read proxy patterns**

Run:
```bash
sed -n '1,220p' pkg/slot/proxy/store.go
sed -n '1,220p' pkg/slot/proxy/runtime_meta_rpc.go
sed -n '1,220p' pkg/slot/proxy/authoritative_rpc.go
```

- [ ] **Step 2: Write failing proxy tests**

Cases:
- create task routes to authoritative slot leader;
- get active task works local and remote;
- claim task uses expected owner lease and rejects a caller that is not the current owning slot leader;
- advance task persists phase/progress/retry/blocker state through the authoritative slot Raft path;
- reset expired fence routes through slot Raft command;
- promote rejects stale meta from remote path.

- [ ] **Step 3: Run tests to verify fail**

```bash
go test ./pkg/slot/proxy -run 'TestChannelMigration' -count=1
```

- [ ] **Step 4: Implement proxy methods**

Add methods to `Store`:
```go
func (s *Store) CreateChannelMigrationTask(ctx context.Context, task metadb.ChannelMigrationTask) error
func (s *Store) GetActiveChannelMigrationTask(ctx context.Context, id string, typ int64) (metadb.ChannelMigrationTask, bool, error)
func (s *Store) ListRunnableChannelMigrationTasksForLocalLeaderSlots(ctx context.Context, nowMS int64, limit int) ([]metadb.ChannelMigrationTask, error)
func (s *Store) ClaimChannelMigrationTask(ctx context.Context, req metafsm.ChannelMigrationClaimRequest) error
func (s *Store) AdvanceChannelMigrationTask(ctx context.Context, req metafsm.ChannelMigrationAdvanceRequest) error
func (s *Store) SetChannelWriteFence(ctx context.Context, req metafsm.ChannelWriteFenceRequest) error
func (s *Store) ResetChannelWriteFenceToPreCutover(ctx context.Context, req metafsm.ChannelWriteFenceResetRequest) error
func (s *Store) CommitChannelLeaderTransfer(ctx context.Context, req metafsm.ChannelLeaderTransferCommitRequest) error
func (s *Store) AddChannelLearner(ctx context.Context, req metafsm.ChannelAddLearnerRequest) error
func (s *Store) PromoteLearnerAndRemoveReplica(ctx context.Context, req metafsm.ChannelPromoteLearnerRequest) error
func (s *Store) ClearChannelWriteFence(ctx context.Context, req metafsm.ChannelClearFenceRequest) error
func (s *Store) AbortChannelMigration(ctx context.Context, req metafsm.ChannelMigrationAbortRequest) error
func (s *Store) GarbageCollectTerminalChannelMigrationTasks(ctx context.Context, beforeMS int64, limit int) (int, error)
```

`ClaimChannelMigrationTask` must set `OwnerNodeID` to the local node that currently leads the channel's owning slot; do not accept an arbitrary remote owner ID. If this node is not the current slot leader, return `ErrNotLeader`/`ErrNotAuthoritative` before proposing the command.

- [ ] **Step 5: Run tests**

```bash
go test ./pkg/slot/proxy -run 'TestChannelMigration' -count=1
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/slot/proxy/channel_migration_rpc.go pkg/slot/proxy/channel_migration_codec.go pkg/slot/proxy/channel_migration_rpc_test.go pkg/slot/proxy/store.go
git commit -m "feat: expose channel migration slot proxy"
```

---

## Task 5: Project Write Fence Into Channel Metadata And Cache Invalidation

**Files:**
- Modify: `pkg/channel/types.go`
- Modify: `pkg/channel/errors.go`
- Modify: `pkg/channel/handler/meta.go`
- Modify: `internal/runtime/channelmeta/resolver.go`
- Modify: `internal/runtime/channelmeta/repair.go`
- Modify: `internal/runtime/channelmeta/FLOW.md`
- Test: `pkg/channel/types_test.go`
- Test: `pkg/channel/handler/meta_test.go`
- Test: `internal/runtime/channelmeta/resolver_test.go`
- Test: `internal/runtime/channelmeta/repair_test.go`

- [ ] **Step 1: Write failing projection tests**

Cases:
```go
func TestChannelMetaProjectionIncludesWriteFence(t *testing.T) { /* metadb -> channel.Meta */ }
func TestChannelMetaCacheInvalidatesOnFenceVersionChange(t *testing.T) { /* cached healthy meta not reused */ }
func TestLeaderRepairPreservesWriteFenceFields(t *testing.T) { /* repair upsert keeps fence */ }
```

- [ ] **Step 2: Run tests to verify fail**

```bash
go test ./internal/runtime/channelmeta ./pkg/channel/handler -run 'Test.*WriteFence|TestLeaderRepairPreservesWriteFence' -count=1
```

- [ ] **Step 3: Implement channel types**

```go
type WriteFenceReason uint8
const (
    WriteFenceReasonMigration WriteFenceReason = 1
)

type WriteFence struct {
    // Token identifies the task that owns the current write fence.
    Token string
    // Version is the monotonic fence generation applied from authoritative metadata.
    Version uint64
    // Reason explains why writes are currently fenced.
    Reason WriteFenceReason
    // Until is the fence lease deadline from authoritative metadata.
    Until time.Time
}

func (f WriteFence) Active(now time.Time) bool { return f.Token != "" && now.Before(f.Until) }
```

Add `WriteFence WriteFence` to `channel.Meta` and `ErrWriteFenced` to `pkg/channel/errors.go`.

- [ ] **Step 4: Implement projection and invalidation**

In `resolver.go`, project `WriteFence*` from `metadb.ChannelRuntimeMeta`. Activation cache reuse must fail if authoritative or cached fence version changes, or if any active fence exists.

- [ ] **Step 5: Preserve fences in repair**

Any repair candidate based on existing meta must copy write-fence fields. If repair uses store monotonic merge, tests should still verify explicit preservation.

- [ ] **Step 6: Run tests**

```bash
go test ./internal/runtime/channelmeta ./pkg/channel/handler -run 'Test.*WriteFence|TestLeaderRepairPreservesWriteFence' -count=1
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/channel/types.go pkg/channel/errors.go pkg/channel/handler/meta.go internal/runtime/channelmeta/resolver.go internal/runtime/channelmeta/repair.go internal/runtime/channelmeta/FLOW.md
git commit -m "feat: project channel write fences"
```

---

## Task 6: Enforce Write Fence In Append Admission

**Files:**
- Modify: `pkg/channel/handler/append.go`
- Modify: `pkg/channel/runtime/channel.go`
- Modify: `pkg/channel/replica/append_pipeline.go`
- Test: `pkg/channel/handler/append_test.go`
- Test: `pkg/channel/runtime/runtime_test.go`
- Test: `pkg/channel/replica/append_test.go`

- [ ] **Step 1: Write failing handler tests**

```go
func TestAppendRejectsActiveWriteFence(t *testing.T) { /* returns ErrWriteFenced */ }
func TestAppendExpiredPreDrainFenceRequiresRefresh(t *testing.T) { /* handler/runtime does not silently treat stale active task as open */ }
```

- [ ] **Step 2: Write failing replica-loop test**

```go
func TestReplicaFenceRejectsAppendDespiteStaleHandlerMeta(t *testing.T) {
    // Apply local runtime fence, call replica append directly, expect ErrWriteFenced.
}
```

- [ ] **Step 3: Run tests to verify fail**

```bash
go test ./pkg/channel/handler ./pkg/channel/runtime ./pkg/channel/replica -run 'Test.*WriteFence|TestReplicaFenceRejectsAppend' -count=1
```

- [ ] **Step 4: Implement admission checks**

Handler:
```go
if meta.WriteFence.Active(time.Now()) {
    return channel.AppendResult{}, channel.ErrWriteFenced
}
```

Runtime/replica must also check local fence state immediately before append enters durable pipeline. Do not rely on handler metadata alone.
If a fence has expired before drain, append admission must refresh or consult authoritative task state before reopening writes; local wall-clock expiry alone is not sufficient to admit writes.

- [ ] **Step 5: Map retry semantics**

If send retry logic treats only `ErrStaleMeta` / `ErrNotLeader` / `ErrNotReady` as refresh triggers, add `ErrWriteFenced` as retryable where appropriate in `internal/usecase/message` after reading its FLOW/docs if any.

- [ ] **Step 6: Run tests**

```bash
go test ./pkg/channel/handler ./pkg/channel/runtime ./pkg/channel/replica -run 'Test.*WriteFence|TestReplicaFenceRejectsAppend' -count=1
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/channel/handler/append.go pkg/channel/runtime/channel.go pkg/channel/replica/append_pipeline.go pkg/channel/**/*test.go
git commit -m "feat: reject fenced channel appends"
```

---

## Task 7: Add FenceAndDrain, Remote Drain RPC, And Fail-Closed Runtime Semantics

**Files:**
- Modify: `pkg/channel/types.go`
- Modify: `pkg/channel/runtime/types.go`
- Modify: `pkg/channel/runtime/runtime.go`
- Create: `pkg/channel/transport/migration_control.go`
- Create: `pkg/channel/transport/migration_control_test.go`
- Modify: `pkg/channel/transport/transport.go`
- Modify: `pkg/channel/transport/codec.go`
- Modify: `pkg/channel/replica/types.go`
- Modify: `pkg/channel/replica/commands.go`
- Modify: `pkg/channel/replica/append_pipeline.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/channelcluster.go` if the existing channel transport options flow requires it
- Test: `pkg/channel/runtime/runtime_test.go`
- Test: `pkg/channel/replica/append_test.go`
- Test: `pkg/channel/transport/migration_control_test.go`

- [ ] **Step 1: Write failing drain tests**

Cases:
```go
func TestFenceAndDrainRejectsNewAppends(t *testing.T) { /* active fence */ }
func TestFenceAndDrainWaitsForInflightAppend(t *testing.T) { /* in-flight completes before result */ }
func TestDrainedFailClosedIgnoresSameVersionTTLExpiry(t *testing.T) { /* still ErrWriteFenced */ }
func TestDrainedFailClosedReopensOnlyAfterNewerVersionClear(t *testing.T) { /* version++ clear */ }
func TestMigrationControlClientDrainsRemoteLeader(t *testing.T) { /* RPC invokes remote runtime service */ }
func TestMigrationControlRejectsDrainOnNonLeaderOrFenceMismatch(t *testing.T) { /* no false proof */ }
```

- [ ] **Step 2: Run tests to verify fail**

```bash
go test ./pkg/channel/runtime ./pkg/channel/replica ./pkg/channel/transport -run 'TestFenceAndDrain|TestDrainedFailClosed|TestMigrationControl' -count=1
```

- [ ] **Step 3: Implement DTOs**

```go
type DrainResult struct {
    ChannelKey channel.ChannelKey
    LEO uint64
    HW uint64
    CheckpointHW uint64
    ChannelEpoch uint64
    LeaderEpoch uint64
    WriteFenceVersion uint64
    RuntimeGeneration uint64
}
```

Runtime API:
```go
type MigrationRuntime interface {
    FenceAndDrain(ctx context.Context, key core.ChannelKey, token string, version uint64) (core.DrainResult, error)
}

type MigrationControlClient interface {
    FenceAndDrain(ctx context.Context, nodeID channel.NodeID, req FenceAndDrainRequest) (DrainResult, error)
}

type FenceAndDrainRequest struct {
    ChannelKey channel.ChannelKey
    TaskID string
    WriteFenceToken string
    WriteFenceVersion uint64
    ExpectedChannelEpoch uint64
    ExpectedLeaderEpoch uint64
    ExpectedLeader channel.NodeID
}
```

- [ ] **Step 4: Implement replica loop state**

Add in-memory drained marker with token/version. Same-version TTL never reopens. Applying meta with newer no-fence/reset clears it.
On restart after a successful drain, the runtime/executor must consult authoritative task state and matching durable drain proof before deciding whether writes can reopen; never infer reopen from a missing in-memory marker or expired fence TTL.

- [ ] **Step 5: Implement remote migration control RPC**

Register a channel transport RPC service, for example `RPCServiceMigrationControlDrain`, that:
- decodes `FenceAndDrainRequest`;
- invokes only the local channel runtime `MigrationRuntime.FenceAndDrain`;
- rejects if the local runtime is not the expected leader or has not applied the same channel epoch, leader epoch, fence token, and fence version;
- returns `DrainResult` with enough proof fields for the executor to persist `Cutover*` / `Drained*`.

The executor must use this client even when the current channel leader is remote. Local-node fast path can call the same service implementation directly, but must preserve the same validation semantics.

- [ ] **Step 6: Wire RPC service in app composition**

Add the migration control service/client to the existing channel transport wiring. Keep `internal/access/*` adapter-only if the existing node RPC layer is used; channel migration phase logic stays in `internal/runtime/channelmigration`.

- [ ] **Step 7: Run tests**

```bash
go test ./pkg/channel/runtime ./pkg/channel/replica ./pkg/channel/transport -run 'TestFenceAndDrain|TestDrainedFailClosed|TestMigrationControl' -count=1
```
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/channel/types.go pkg/channel/runtime/types.go pkg/channel/runtime/runtime.go pkg/channel/transport pkg/channel/replica/types.go pkg/channel/replica/commands.go pkg/channel/replica/append_pipeline.go internal/app/build.go internal/app/channelcluster.go pkg/channel/**/*test.go
git commit -m "feat: add channel fence and drain primitive"
```

---

## Task 8: Add Learner Quorum Tests And Same-Leader ChannelEpoch Boundary Support

**Files:**
- Modify: `pkg/channel/replica/replication.go`
- Modify: `pkg/channel/replica/progress.go`
- Modify: `pkg/channel/replica/lifecycle_pipeline.go`
- Modify: `pkg/channel/replica/commands.go`
- Modify: `pkg/channel/replica/follower_apply.go`
- Modify: `pkg/channel/runtime/types.go` if envelope changes are needed
- Modify: `pkg/channel/runtime/backpressure.go` / `pkg/channel/runtime/lanes.go` if heartbeat carries epoch boundary
- Modify: `pkg/channel/store/durability.go` or adapters in `internal/app/channelmeta.go`
- Test: `pkg/channel/replica/lifecycle_test.go`
- Test: `pkg/channel/replica/follower_apply_test.go`
- Test: `pkg/channel/replica/replication_test.go`
- Test: `pkg/channel/runtime/session_test.go` if lane protocol changes

- [ ] **Step 1: Write failing learner quorum tests**

```go
func TestLearnerInReplicasReceivesReplicationButNotHWQuorum(t *testing.T) {
    // Replicas={1,2,3,4}, ISR={1,2,3}; node 4 should fetch/replicate.
    // HW quorum calculation must ignore node 4 until promotion adds it to ISR.
}

func TestAddLearnerDoesNotReduceWriteAvailability(t *testing.T) {
    // Existing ISR quorum remains enough for appends while learner catches up.
}
```

- [ ] **Step 2: Write failing leader epoch-boundary test**

```go
func TestApplyMetaSameLeaderHigherChannelEpochPersistsEpochPointBeforeAppend(t *testing.T) {
    // leader at epoch 7, LEO 12; apply meta epoch 8 same leader.
    // expect append blocked until BeginEpoch(8, 12) durable effect completes.
}
```

- [ ] **Step 3: Write failing follower idle boundary test**

```go
func TestFollowerPersistsHeartbeatOnlyEpochBoundary(t *testing.T) {
    // follower receives meta/heartbeat for higher epoch with no records.
    // expect EpochHistory has EpochPoint{Epoch:newEpoch, StartOffset:LEO}.
}
```

- [ ] **Step 4: Run tests to verify fail**

```bash
go test ./pkg/channel/replica ./pkg/channel/runtime -run 'TestLearner|TestAddLearner|TestApplyMetaSameLeaderHigherChannelEpoch|TestFollowerPersistsHeartbeatOnlyEpochBoundary' -count=1
```

- [ ] **Step 5: Implement learner quorum semantics if tests reveal gaps**

Derive replication targets from `Replicas`, but derive write quorum/HW advancement from `ISR`. Do not let a learner block or reduce write availability before `PromoteLearnerAndRemoveReplica`.

- [ ] **Step 6: Implement durable boundary effect**

Reuse existing `BeginEpoch` durable adapter where possible. Same-leader epoch increase should:
- set `CommitReady=false` / append blocked;
- write epoch point at current LEO;
- publish new epoch only after durable success;
- retry or stay not-ready on failure.

- [ ] **Step 7: Implement heartbeat boundary propagation**

If current fetch/lane response lacks explicit epoch-boundary info, add a small optional field to the response envelope and codec. Keep backward compatibility only as needed for current tests; this repo appears to use direct cutover for internal protocol.

- [ ] **Step 8: Run tests**

```bash
go test ./pkg/channel/replica ./pkg/channel/runtime -run 'TestLearner|TestAddLearner|TestApplyMetaSameLeaderHigherChannelEpoch|TestFollowerPersistsHeartbeatOnlyEpochBoundary' -count=1
```
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/channel/replica pkg/channel/runtime pkg/channel/store internal/app/channelmeta.go
git commit -m "feat: persist channel epoch boundaries on meta changes"
```

---

## Task 9: Add Migration Proof Helpers

**Files:**
- Create: `internal/runtime/channelmigration/types.go`
- Create: `internal/runtime/channelmigration/proof.go`
- Create: `internal/runtime/channelmigration/proof_test.go`
- Modify: `internal/runtime/channelmeta/interfaces.go` if reusing evaluator ports
- Modify: `internal/access/node/channel_leader_evaluate_rpc.go` only if proof API needs extension

- [ ] **Step 1: Write failing proof tests**

Cases:
```go
func TestFinalTargetProofRejectsLagBelowCutoverHW(t *testing.T) { }
func TestFinalTargetProofRejectsDivergentOffsetEpoch(t *testing.T) { }
func TestFinalTargetProofRejectsSnapshotRequired(t *testing.T) { }
func TestFinalTargetProofAcceptsCheckpointAtCutoverHW(t *testing.T) { }
```

- [ ] **Step 2: Run tests to verify fail**

```bash
go test ./internal/runtime/channelmigration -run 'TestFinalTargetProof' -count=1
```

- [ ] **Step 3: Implement ports and proof evaluator**

Use neutral ports:
```go
type ProbeClient interface {
    ProbeChannel(ctx context.Context, nodeID channel.NodeID, meta channel.Meta) (ProbeReport, error)
}

type ProofEvaluator struct{}
func (ProofEvaluator) EvaluateFinalTargetProof(req FinalTargetProofRequest) (FinalTargetProof, error)
```

The evaluator must require current channel epoch/leader epoch, `CheckpointHW >= CutoverHW`, compatible lineage, and no truncate/snapshot blocker.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/runtime/channelmigration -run 'TestFinalTargetProof' -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/channelmigration/types.go internal/runtime/channelmigration/proof.go internal/runtime/channelmigration/proof_test.go
git commit -m "feat: evaluate channel migration target proofs"
```

---

## Task 10: Implement ChannelMigration Executor Claiming And Skeleton

**Files:**
- Create: `internal/runtime/channelmigration/executor.go`
- Create: `internal/runtime/channelmigration/metrics.go`
- Create: `internal/runtime/channelmigration/gc.go`
- Create: `internal/runtime/channelmigration/executor_test.go`
- Modify: `internal/runtime/channelmigration/types.go`

- [ ] **Step 1: Write failing executor ownership, claim, and limit tests**

Cases:
```go
func TestExecutorClaimsOnlyRunnableTask(t *testing.T) { }
func TestExecutorListsOnlyTasksForLocallyLedSlots(t *testing.T) { }
func TestExecutorRejectsClaimWhenLocalNodeIsNotSlotLeader(t *testing.T) { }
func TestExecutorStopsBeforeSideEffectsAfterSlotLeadershipLost(t *testing.T) { }
func TestExecutorOwnerLeaseHandoffAfterExpiry(t *testing.T) { }
func TestDuplicateExecutorRaceSingleOwner(t *testing.T) { }
func TestExecutorDoesNotClearFenceOnOwnerLeaseExpiry(t *testing.T) { }
func TestExecutorHonorsGlobalSourceAndTargetConcurrencyLimits(t *testing.T) { }
func TestExecutorGarbageCollectsTerminalTasksAfterRetention(t *testing.T) { }
func TestExecutorRecordsPhaseTransitionMetrics(t *testing.T) { }
```

- [ ] **Step 2: Run tests to verify fail**

```bash
go test ./internal/runtime/channelmigration -run 'TestExecutor|TestDuplicateExecutor|Test.*OwnerLease' -count=1
```

- [ ] **Step 3: Implement executor skeleton**

```go
type SlotLeadership interface {
    // IsLocalLeader reports whether this node currently leads the slot.
    IsLocalLeader(ctx context.Context, slotID uint32) (bool, error)
    // SlotForChannel resolves the authoritative slot for a channel ID.
    SlotForChannel(channelID string) uint32
}

type Store interface {
    ListRunnableTasksForLocalLeaderSlots(ctx context.Context, nowMS int64, limit int) ([]Task, error)
    ClaimChannelMigrationTask(ctx context.Context, req ClaimRequest) error
    AdvanceChannelMigrationTask(ctx context.Context, req AdvanceRequest) error
    GarbageCollectTerminalTasks(ctx context.Context, beforeMS int64, limit int) (int, error)
}

type Executor struct {
    store Store
    slots SlotLeadership
    metrics Metrics
    localNode channel.NodeID
    now func() time.Time
    cfg Config
    log wklog.Logger
}

func (e *Executor) Tick(ctx context.Context) error {
    tasks, err := e.store.ListRunnableTasksForLocalLeaderSlots(ctx, e.now().UnixMilli(), e.cfg.ScanLimit)
    if err != nil { return err }
    for _, task := range tasks {
        if ok, err := e.isStillLocalSlotLeader(ctx, task); err != nil || !ok { continue }
        if !e.limits.Allow(task) { continue }
        _ = e.runOne(ctx, task)
    }
    _ = e.gcTerminalTasks(ctx)
    return nil
}
```

Ports should live in `internal/runtime/channelmigration/types.go`, not in access/app packages.
Before every non-idempotent local side effect and before every commit command, re-check local slot leadership and owner lease. A lost slot-leader role must stop the task without clearing fences or local drained state.

- [ ] **Step 4: Implement no-op phase dispatch**

Initial dispatch can persist progress through `AdvanceChannelMigrationTask` and return explicit `ErrPhaseNotImplemented` for later task-specific phases, but claim/lease/slot-ownership behavior must pass.
Metrics should include active tasks, phase transitions, retries, blockers, fence duration, and task duration via a small interface that can be a no-op in tests.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/runtime/channelmigration -run 'TestExecutor|TestDuplicateExecutor|Test.*OwnerLease' -count=1
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/channelmigration/executor.go internal/runtime/channelmigration/metrics.go internal/runtime/channelmigration/gc.go internal/runtime/channelmigration/executor_test.go internal/runtime/channelmigration/types.go
git commit -m "feat: add channel migration executor skeleton"
```

---

## Task 11: Implement Leader Transfer Executor

**Files:**
- Create: `internal/runtime/channelmigration/leader_transfer.go`
- Create: `internal/runtime/channelmigration/leader_transfer_test.go`
- Modify: `internal/runtime/channelmigration/executor.go`

- [ ] **Step 1: Write failing standalone leader transfer tests**

Cases:
```go
func TestLeaderTransferHappyPath(t *testing.T) { }
func TestLeaderTransferRejectsTargetOutsideISR(t *testing.T) { }
func TestLeaderTransferLaggingTargetWaitsAfterDrain(t *testing.T) { }
func TestLeaderTransferDrainsRemoteCurrentLeaderThroughMigrationControlClient(t *testing.T) { }
func TestLeaderTransferPersistsProgressBetweenPhaseOnlySteps(t *testing.T) { }
func TestLeaderTransferRejectsCommitWhenFenceVersionChanged(t *testing.T) { }
func TestLeaderTransferFenceExpiryResetsToPreCutoverWithVersionBump(t *testing.T) { }
```

- [ ] **Step 2: Run tests to verify fail**

```bash
go test ./internal/runtime/channelmigration -run 'TestLeaderTransfer' -count=1
```

- [ ] **Step 3: Implement phases**

Implement sequence:
`Validate -> ProbeTarget -> WriteFence -> DrainLeader -> FinalTargetCatchUp -> CommitLeaderMeta -> VerifyNewLeader -> ClearFence -> Completed`.

Key requirements:
- Post-drain target proof must use `CutoverHW` and `DrainedFenceVersion`.
- `DrainLeader` must call a `MigrationControlClient` against the current channel leader node; do not assume the slot leader and channel leader are colocated.
- `Validate`, `ProbeTarget`, `DrainLeader` result recording, `FinalTargetCatchUp` wait/retry, `VerifyNewLeader`, and blocker/failure transitions must be persisted with `AdvanceChannelMigrationTask`.
- Expired fence reset uses `ResetChannelWriteFenceToPreCutover`, not local reopening.
- Commit command rejects expired fence.
- Every phase transition and fenced-meta command should emit structured logs/metrics with `task_id`, channel key, old/new phase, fence token/version, owner, and observed epochs.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/runtime/channelmigration -run 'TestLeaderTransfer' -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/channelmigration/leader_transfer.go internal/runtime/channelmigration/leader_transfer_test.go internal/runtime/channelmigration/executor.go
git commit -m "feat: execute channel leader transfers"
```

---

## Task 12: Implement Replica Replacement Executor With Learner Gate

**Files:**
- Create: `internal/runtime/channelmigration/replica_replace.go`
- Create: `internal/runtime/channelmigration/replica_replace_test.go`
- Modify: `internal/runtime/channelmigration/executor.go`

- [ ] **Step 1: Write failing replica replacement tests**

Cases:
```go
func TestReplicaReplaceFollowerSourceHappyPath(t *testing.T) { }
func TestReplicaReplaceSourceLeaderUsesEmbeddedTransfer(t *testing.T) { }
func TestReplicaReplaceAddLearnerKeepsISRUnchanged(t *testing.T) { }
func TestReplicaReplaceWarmCatchUpProgressSurvivesExecutorRestart(t *testing.T) { }
func TestReplicaReplaceFinalCatchUpRequiresLineageProof(t *testing.T) { }
func TestReplicaReplaceSnapshotRequiredBlocksBeforePromote(t *testing.T) { }
func TestReplicaReplaceAbortAfterLearnerIncrementsEpoch(t *testing.T) { }
```

- [ ] **Step 2: Run tests to verify fail**

```bash
go test ./internal/runtime/channelmigration -run 'TestReplicaReplace' -count=1
```

- [ ] **Step 3: Implement phases**

Implement sequence:
`Validate -> TransferLeaderIfNeeded -> AddLearner -> BootstrapTarget -> WarmCatchUp -> CutoverFence -> FinalCatchUp -> PromoteAndRemove -> VerifyMembership -> ClearFence -> Completed`.

For V1 `BootstrapTarget`:
- if normal fetch/probe can catch up, continue;
- if proof returns snapshot-required, call `AdvanceChannelMigrationTask` with status `Blocked`, blocker `NeedsSnapshotBootstrap`, retry/progress fields, and do not promote.

Persist phase-only progress with `AdvanceChannelMigrationTask` for `Validate`, `BootstrapTarget`, `WarmCatchUp`, `FinalCatchUp` waits, `VerifyMembership`, retry backoff, and terminal failure. `AddLearner`, `CutoverFence`, `PromoteAndRemove`, and `ClearFence` must use their combined task+metadata FSM commands instead of a separate task update.

- [ ] **Step 4: Implement embedded leader transfer**

Embedded transfer uses the same task fields and must clear `Cutover*`/`Drained*` after embedded transfer clear/reset before later replica cutover uses new proof.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/runtime/channelmigration -run 'TestReplicaReplace' -count=1
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/channelmigration/replica_replace.go internal/runtime/channelmigration/replica_replace_test.go internal/runtime/channelmigration/executor.go
git commit -m "feat: execute channel replica replacement"
```

---

## Task 13: Add Management Use Cases

**Files:**
- Create: `internal/usecase/management/channel_migration.go`
- Create: `internal/usecase/management/channel_migration_test.go`
- Modify: `internal/usecase/management/types.go` if needed

- [ ] **Step 1: Write failing usecase tests**

Cases:
```go
func TestTransferChannelLeaderDryRun(t *testing.T) { }
func TestMigrateChannelReplicaDryRunReportsBlockers(t *testing.T) { }
func TestMigrateChannelReplicaCreatesTask(t *testing.T) { }
func TestGetChannelMigrationReportsProgressAndNeedsSnapshotBootstrapBlocker(t *testing.T) { }
func TestAbortChannelMigrationRequiresMatchingTask(t *testing.T) { }
func TestSingleNodeClusterReplicaMigrationRejectedDeterministically(t *testing.T) { }
```

- [ ] **Step 2: Run tests to verify fail**

```bash
go test ./internal/usecase/management -run 'Test.*Channel.*Migration|TestTransferChannelLeader' -count=1
```

- [ ] **Step 3: Implement usecase methods**

Suggested API:
```go
type TransferChannelLeaderRequest struct {
    TargetNodeID uint64
    DryRun bool
}

type MigrateChannelReplicaRequest struct {
    SourceNodeID uint64
    TargetNodeID uint64
    DryRun bool
}

func (a *App) TransferChannelLeader(ctx context.Context, id channel.ChannelID, req TransferChannelLeaderRequest) (ChannelMigrationResult, error)
func (a *App) MigrateChannelReplica(ctx context.Context, id channel.ChannelID, req MigrateChannelReplicaRequest) (ChannelMigrationResult, error)
func (a *App) GetChannelMigration(ctx context.Context, id channel.ChannelID) (ChannelMigrationDetail, error)
func (a *App) AbortChannelMigration(ctx context.Context, id channel.ChannelID, taskID string) (ChannelMigrationDetail, error)
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/usecase/management -run 'Test.*Channel.*Migration|TestTransferChannelLeader' -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/management/channel_migration.go internal/usecase/management/channel_migration_test.go internal/usecase/management/types.go
git commit -m "feat: add channel migration management usecases"
```

---

## Task 14: Add Manager HTTP APIs

**Files:**
- Create: `internal/access/manager/channel_migration.go`
- Create: `internal/access/manager/channel_migration_test.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/server_test.go` if centralized stubs need methods

- [ ] **Step 1: Write failing route tests**

Routes:
```text
POST /channels/:channel_type/:channel_id/leader/transfer
POST /channels/:channel_type/:channel_id/replicas/migrate
GET  /channels/:channel_type/:channel_id/migration
POST /channels/:channel_type/:channel_id/migration/:task_id/abort
```

Tests verify JSON decode, usecase call, status mapping, dry-run, validation errors.

- [ ] **Step 2: Run tests to verify fail**

```bash
go test ./internal/access/manager -run 'Test.*Channel.*Migration|Test.*Channel.*LeaderTransfer' -count=1
```

- [ ] **Step 3: Implement handlers and DTOs**

Keep adapter-only:
```go
type channelLeaderTransferRequestDTO struct {
    TargetNodeID uint64 `json:"target_node_id"`
    DryRun bool `json:"dry_run"`
}
```

No business validation beyond parsing and required fields.

- [ ] **Step 4: Register routes**

Add routes in `routes.go` under authenticated manager write group consistent with slot operator routes.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/access/manager -run 'Test.*Channel.*Migration|Test.*Channel.*LeaderTransfer' -count=1
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/access/manager/channel_migration.go internal/access/manager/channel_migration_test.go internal/access/manager/routes.go internal/access/manager/server_test.go
git commit -m "feat: expose channel migration manager api"
```

---

## Task 15: Wire Executor In App Lifecycle And Config

**Files:**
- Create: `internal/app/channelmigration.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/lifecycle_components.go`
- Modify: `internal/app/config.go`
- Modify: `wukongim.conf.example`
- Test: `internal/app/channelmigration_test.go`
- Test: `internal/app/config_test.go`

- [ ] **Step 1: Write failing wiring/config tests**

Cases:
```go
func TestBuildWiresChannelMigrationAfterChannelMeta(t *testing.T) { }
func TestChannelMigrationConfigDefaults(t *testing.T) { }
func TestChannelMigrationConfigFromEnv(t *testing.T) { }
func TestChannelMigrationConfigIncludesSourceTargetLimitsAndRetention(t *testing.T) { }
```

- [ ] **Step 2: Run tests to verify fail**

```bash
go test ./internal/app -run 'Test.*ChannelMigration' -count=1
```

- [ ] **Step 3: Add config fields with English comments**

Example names:
```go
// ChannelMigrationFenceTTL controls how long a channel write fence lease remains valid before recovery must reset or renew it.
ChannelMigrationFenceTTL time.Duration
// ChannelMigrationOwnerLeaseTTL controls how long a task owner may hold executor ownership without renewal.
ChannelMigrationOwnerLeaseTTL time.Duration
// ChannelMigrationCatchUpStableWindow controls how long target lag must stay below threshold before cutover.
ChannelMigrationCatchUpStableWindow time.Duration
// ChannelMigrationCatchUpLagThreshold is the maximum record lag allowed before cutover.
ChannelMigrationCatchUpLagThreshold uint64
// ChannelMigrationMaxConcurrent limits active channel migration tasks on this node.
ChannelMigrationMaxConcurrent int
// ChannelMigrationMaxConcurrentPerSource is the per-source concurrency limit for active replacement tasks that drain or remove the same source node.
ChannelMigrationMaxConcurrentPerSource int
// ChannelMigrationMaxConcurrentPerTarget is the per-target concurrency limit for active tasks that bootstrap, catch up, or promote the same target node.
ChannelMigrationMaxConcurrentPerTarget int
// ChannelMigrationCompletedRetentionTTL controls how long terminal migration tasks remain queryable before garbage collection.
ChannelMigrationCompletedRetentionTTL time.Duration
// ChannelMigrationGCLimit controls how many terminal tasks one cleanup tick may delete.
ChannelMigrationGCLimit int
```

Add `WK_` env/config keys and update `wukongim.conf.example`.

- [ ] **Step 4: Wire lifecycle**

Start after channelmeta, channel transport migration-control RPC, and managed slots are ready. The executor must only drive locally led slots, so wiring needs the current slot-leadership/slot-resolver port. Stop gracefully before channel runtime cleanup.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/app -run 'Test.*ChannelMigration|Test.*Config' -count=1
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/channelmigration.go internal/app/*config* wukongim.conf.example internal/app/*test.go
git commit -m "feat: wire channel migration executor"
```

---

## Task 16: Update FLOW Docs And Project Knowledge

**Files:**
- Modify: `pkg/channel/FLOW.md`
- Modify: `pkg/channel/replica/FLOW.md`
- Modify: `pkg/slot/FLOW.md`
- Modify: `internal/runtime/channelmeta/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` only if a durable project rule was discovered

- [ ] **Step 1: Update package flow docs**

Document:
- learner = `Replicas - ISR`;
- learner receives replication but does not affect HW quorum or write availability until promotion;
- write fence and fail-closed drain semantics;
- same-leader epoch boundary requirement;
- channel migration task and atomic slot commands;
- snapshot-required gate for V1.

- [ ] **Step 2: Verify docs mention single-node cluster terminology**

Run:
```bash
rg -n "single node|standalone|local-only|bypass" pkg/channel/FLOW.md pkg/slot/FLOW.md internal/runtime/channelmeta/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
```
Expected: no new forbidden deployment terminology; use “single-node cluster” if needed.

- [ ] **Step 3: Commit**

```bash
git add pkg/channel/FLOW.md pkg/channel/replica/FLOW.md pkg/slot/FLOW.md internal/runtime/channelmeta/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: describe channel migration flows"
```

---

## Task 17: Add Integration Tests

**Files:**
- Add tests under existing integration/e2e test structure after reading relevant `AGENTS.md` and package patterns.
- Candidate: `test/e2e/message/channel_migration_test.go` or package-level integration tests under `internal/app` / `pkg/channel` with `integration` tag.

- [ ] **Step 1: Read test guidance**

Run:
```bash
rg --files -g AGENTS.md test
sed -n '1,220p' test/e2e/AGENTS.md
sed -n '1,220p' test/e2e/message/AGENTS.md
```

- [ ] **Step 2: Add integration tests behind tag**

Required scenarios:
- three-node channel leader transfer with writes before and after transfer;
- slot leader and channel leader on different nodes, proving remote `FenceAndDrain` path is used;
- leader transfer where target lags while old leader + another ISR committed a write;
- three-node replica replacement where source is follower;
- three-node replica replacement where source is leader using embedded transfer;
- executor restart during cutover fence;
- leader restart while fail-closed drained state was active;
- snapshot-required target is blocked before promotion.

- [ ] **Step 3: Run targeted integration tests only**

Run targeted command, not full integration suite:
```bash
go test -tags=integration ./test/e2e/message -run 'TestChannel(Migration|LeaderTransfer)' -count=1
```
Expected: PASS. If package path differs, adjust to actual test package.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/message/channel_migration_test.go
git commit -m "test: cover channel migration integration paths"
```

---

## Task 18: Final Verification

**Files:**
- No new code unless fixing test failures.

- [ ] **Step 1: Run focused unit suites**

```bash
go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy ./pkg/channel/... ./internal/runtime/channelmeta ./internal/runtime/channelmigration ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```
Expected: PASS.

- [ ] **Step 2: Run broader relevant tests**

```bash
go test ./internal/... ./pkg/... -count=1
```
Expected: PASS. If too slow or a known unrelated failure appears, capture exact output and investigate before claiming success.

- [ ] **Step 3: Run optional targeted integration**

```bash
go test -tags=integration ./test/e2e/message -run 'TestChannel(Migration|LeaderTransfer)' -count=1
```
Expected: PASS.

- [ ] **Step 4: Inspect git status**

```bash
git status --short
```
Expected: only intended files changed, no unrelated work included.

- [ ] **Step 5: Commit final fixes if any**

```bash
git add <fixed-files>
git commit -m "test: stabilize channel migration"
```
