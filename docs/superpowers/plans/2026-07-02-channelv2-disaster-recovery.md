# ChannelV2 Disaster Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement ChannelV2 disaster recovery for a three-node cluster so one failed data node does not lose quorum-acknowledged messages, dead-leader channels recover writes, surviving-leader channels continue, and new placement fails closed when the configured replica count cannot be satisfied.

**Architecture:** Add durable write-fence admission first, then node-level data-plane lease guarding, then Slot-backed manual migration executors, then bounded automatic leader failover and replica repair. Keep the hot SEND path cheap: append admission reads local channel state and a node-level lease flag only; all scans, probes, and catch-up work run in bounded background workers.

**Tech Stack:** Go, `pkg/channelv2`, `pkg/clusterv2/channels`, `pkg/slot/fsm`, `pkg/db/meta`, `internalv2/usecase/management`, `internalv2/access/manager`, `test/e2ev2`.

---

## Scope Check

This is a multi-stage but strictly ordered plan. Do not implement automatic failover before fence admission, node lease, and the manual executor tests pass. The accepted design contract is:

1. N=3, replica count 3, `MinISR=2`.
2. RPO is 0 for messages that already returned SENDACK through ChannelV2 quorum commit.
3. A stale or isolated old leader must stop admitting writes via leader epoch fencing and node-level data-plane lease.
4. New channels fail closed when healthy candidates are fewer than the configured replica count.
5. Degraded existing channels may keep serving writes only when surviving ISR satisfies `MinISR`.
6. Automatic work is bounded, rate-limited, observable, and independent from SEND hot-path scans.

---

## File Structure

Modify these existing files:

- `pkg/channelv2/types.go` - add durable write-fence types and append admission guard contracts.
- `pkg/channelv2/errors.go` - add `ErrWriteFenced`.
- `pkg/channelv2/machine/channel.go` - carry write-fence state inside channel runtime state.
- `pkg/channelv2/machine/meta.go` - apply write-fence metadata without clearing in-flight appends.
- `pkg/channelv2/reactor/append.go` - enforce fence and node lease on new append admission.
- `pkg/channelv2/reactor/group.go` - thread append admission guard into reactors.
- `pkg/channelv2/service/service.go` - thread append admission guard from public service config.
- `pkg/channelv2/channel.go` - expose append admission guard config.
- `pkg/channelv2/bench_runtime.go` - extend runtime probe result with migration proof fields.
- `pkg/clusterv2/channels/projection.go` - project Slot/meta write fence into `channelv2.Meta`.
- `pkg/clusterv2/channels/meta.go` - expose fence fields and helper predicates from cached metadata.
- `pkg/clusterv2/channels/service.go` - invalidate meta cache on `ErrWriteFenced`, wire admission guard, and expose migration ports.
- `pkg/clusterv2/node_defaults.go` - pass the clusterv2 data-plane lease guard into ChannelV2.
- `pkg/clusterv2/node.go` - keep node-level data-plane lease state.
- `pkg/clusterv2/node_loops.go` - refresh data-plane lease from successful Slot/control visibility.
- `pkg/clusterv2/node_management.go` - expose lease and ChannelV2 repair state in management snapshots.
- `internalv2/infra/cluster/error_map.go` - map ChannelV2 fence errors to retryable usecase errors.
- `internalv2/usecase/management/channel_runtime_meta.go` - expose fence, degraded, repair, and lease visibility.
- `internalv2/access/manager/channel_runtime_meta.go` - return the new fields through manager JSON.
- `internalv2/app/wiring.go` - wire migration executors and observers into the clusterv2 configuration.
- `wukongim.conf.example` - add any new `WK_` repair configuration with English comments.

Create these new files:

- `pkg/clusterv2/channel_lease.go` - node-level ChannelV2 data-plane lease guard.
- `pkg/clusterv2/channel_lease_test.go` - lease guard unit tests.
- `pkg/clusterv2/channels/migration_store.go` - Slot-backed migration task facade.
- `pkg/clusterv2/channels/migration_store_test.go` - migration store tests with fake proposer/storage.
- `pkg/clusterv2/channels/migration_executor.go` - task runner, phase dispatch, backoff, and ownership fencing.
- `pkg/clusterv2/channels/migration_executor_test.go` - executor orchestration tests.
- `pkg/clusterv2/channels/migration_leader_transfer.go` - manual leader transfer flow.
- `pkg/clusterv2/channels/migration_leader_transfer_test.go` - leader transfer phase tests.
- `pkg/clusterv2/channels/migration_replica_replace.go` - manual replica replacement flow.
- `pkg/clusterv2/channels/migration_replica_replace_test.go` - replica replacement phase tests.
- `pkg/clusterv2/channels/repair_scanner.go` - bounded owned-slot scan and candidate creation.
- `pkg/clusterv2/channels/repair_scanner_test.go` - scan ownership, pagination, and duplicate suppression tests.
- `pkg/clusterv2/channels/failover_planner.go` - candidate selection for dead-leader failover.
- `pkg/clusterv2/channels/failover_planner_test.go` - failover proof tests.
- `pkg/clusterv2/channels/repair_planner.go` - replica replacement target selection.
- `pkg/clusterv2/channels/repair_planner_test.go` - target selection and fail-closed tests.
- `internalv2/usecase/management/channel_migration.go` - manager usecase for manual migration requests.
- `internalv2/usecase/management/channel_migration_test.go` - validation and permission-free usecase tests.
- `internalv2/access/manager/channel_migration.go` - HTTP handlers for manual migration actions.
- `internalv2/access/manager/channel_migration_test.go` - request/response handler tests.
- `test/e2ev2/message/channelv2_failover_test.go` - real three-node kill-node black-box coverage.

Update `FLOW.md` files if present and affected:

- `pkg/channelv2/FLOW.md`
- `pkg/clusterv2/FLOW.md`
- `pkg/clusterv2/channels/FLOW.md`
- `internalv2/usecase/management/FLOW.md`
- `internalv2/access/manager/FLOW.md`

---

## Task 1: Durable Write Fence Projection And Append Admission

**Purpose:** Make a Slot/meta write fence authoritative in ChannelV2, so a migration or failover task can stop new writes before changing leadership or membership.

- [ ] Add a failing machine-level test in `pkg/channelv2/machine/meta_test.go`:

```go
func TestApplyMetaCopiesWriteFenceWithoutClearingInflight(t *testing.T) {
	st := newTestLeaderState(t)
	st.PendingAppends[1] = ch.AppendBatchRequest{ChannelID: st.ID}
	st.InflightAppend = &ch.AppendBatchRequest{ChannelID: st.ID}

	meta := testLeaderMeta(st.ID)
	meta.WriteFence = ch.WriteFence{
		Token:   "migration-1",
		Version: 7,
		Reason: ch.WriteFenceReasonLeaderTransfer,
	}

	st.ApplyMeta(meta)

	require.Equal(t, meta.WriteFence, st.WriteFence)
	require.Len(t, st.PendingAppends, 1)
	require.NotNil(t, st.InflightAppend)
}
```

- [ ] Add a failing reactor append test in `pkg/channelv2/reactor/append_test.go`:

```go
func TestValidateAppendEventRejectsWriteFencedLeader(t *testing.T) {
	r, rc := newTestLeaderReactorChannel(t)
	rc.StateForTest().WriteFence = ch.WriteFence{
		Token:   "failover-1",
		Version: 1,
		Reason: ch.WriteFenceReasonFailover,
	}

	err := r.validateAppendEvent(context.Background(), rc, testAppendEvent(rc))

	require.ErrorIs(t, err, ch.ErrWriteFenced)
}
```

- [ ] Run the focused tests and confirm they fail for missing symbols:

```bash
GOWORK=off go test ./pkg/channelv2/machine ./pkg/channelv2/reactor -run 'TestApplyMetaCopiesWriteFenceWithoutClearingInflight|TestValidateAppendEventRejectsWriteFencedLeader'
```

Expected: FAIL because `WriteFence`, `WriteFenceReasonLeaderTransfer`, and `ErrWriteFenced` are not defined.

- [ ] Add concrete fence types in `pkg/channelv2/types.go`:

```go
// WriteFenceReason identifies the control-plane operation that is blocking new writes.
type WriteFenceReason uint8

const (
	WriteFenceReasonUnknown WriteFenceReason = iota
	WriteFenceReasonLeaderTransfer
	WriteFenceReasonReplicaReplace
	WriteFenceReasonFailover
)

// WriteFence is durable control-plane metadata that prevents new leader appends.
type WriteFence struct {
	Token   string
	Version uint64
	Reason  WriteFenceReason
	Until   time.Time
}

// Set reports whether the authoritative metadata currently fences writes.
func (f WriteFence) Set() bool {
	return f.Token != "" && f.Version != 0
}
```

- [ ] Add `WriteFence WriteFence` to `Meta` in `pkg/channelv2/types.go`.

- [ ] Add `ErrWriteFenced` to `pkg/channelv2/errors.go`:

```go
ErrWriteFenced = errors.New("channelv2: write fenced")
```

- [ ] Add `WriteFence ch.WriteFence` to `ChannelState` in `pkg/channelv2/machine/channel.go` with an English field comment.

- [ ] In `pkg/channelv2/machine/meta.go`, copy `meta.WriteFence` into state. Do not include fence changes in the branch that clears append state, because accepted in-flight appends are drained by the migration executor.

- [ ] In `pkg/channelv2/reactor/append.go`, convert append validation to a reactor method and block new appends when `rc.state.WriteFence.Set()`:

```go
if rc.state.WriteFence.Set() {
	return ch.ErrWriteFenced
}
```

- [ ] Project the fence in `pkg/clusterv2/channels/projection.go` from `metadb.ChannelRuntimeMeta` into `ch.Meta`.

- [ ] In `pkg/clusterv2/channels/service.go`, when a local append returns `ch.ErrWriteFenced`, invalidate the channel meta cache and reload authoritative meta once. If the reloaded meta is still fenced, return `ch.ErrWriteFenced`. If the reloaded meta is clear, retry the append once.

- [ ] Map `ch.ErrWriteFenced` in `internalv2/infra/cluster/error_map.go` to `internalv2/contracts/channelappend.ErrRouteNotReady`. Add a test that `errors.Is(mapped, channelappend.ErrRouteNotReady)` holds.

- [ ] Run focused tests:

```bash
GOWORK=off go test ./pkg/channelv2/machine ./pkg/channelv2/reactor ./pkg/clusterv2/channels ./internalv2/infra/cluster
```

Expected: PASS.

---

## Task 2: Node-Level Data-Plane Lease Guard

**Purpose:** Prevent an isolated old leader from admitting ChannelV2 writes when the node can no longer maintain fresh Slot/control visibility.

- [ ] Add failing unit tests in `pkg/clusterv2/channel_lease_test.go`:

```go
func TestChannelDataPlaneLeaseGuardAllowsFreshLease(t *testing.T) {
	clock := newManualClock(time.Unix(100, 0))
	guard := newChannelDataPlaneLeaseGuard(clock.Now, 3*time.Second)
	guard.MarkVisible(clock.Now())

	err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{})

	require.NoError(t, err)
}

func TestChannelDataPlaneLeaseGuardRejectsExpiredLease(t *testing.T) {
	clock := newManualClock(time.Unix(100, 0))
	guard := newChannelDataPlaneLeaseGuard(clock.Now, 3*time.Second)
	guard.MarkVisible(clock.Now())
	clock.Advance(4 * time.Second)

	err := guard.AllowChannelAppend(context.Background(), ch.AppendAdmissionRequest{})

	require.ErrorIs(t, err, ch.ErrNotReady)
}
```

- [ ] Add ChannelV2 append admission guard contracts in `pkg/channelv2/types.go`:

```go
// AppendAdmissionRequest describes a leader append admission decision.
type AppendAdmissionRequest struct {
	ChannelID   ChannelID
	ChannelKey  ChannelKey
	Epoch       uint64
	LeaderEpoch uint64
	Leader      NodeID
}

// AppendAdmissionGuard can fail closed before a leader append enters the reactor.
type AppendAdmissionGuard interface {
	AllowChannelAppend(context.Context, AppendAdmissionRequest) error
}

// AppendAdmissionGuardFunc adapts a function to AppendAdmissionGuard.
type AppendAdmissionGuardFunc func(context.Context, AppendAdmissionRequest) error

func (fn AppendAdmissionGuardFunc) AllowChannelAppend(ctx context.Context, req AppendAdmissionRequest) error {
	return fn(ctx, req)
}
```

- [ ] Thread `AppendAdmissionGuard` through `pkg/channelv2/channel.go`, `pkg/channelv2/service/service.go`, and `pkg/channelv2/reactor/group.go`.

- [ ] In `pkg/channelv2/reactor/append.go`, call the guard after local role/epoch/fence checks and before accepting a new append:

```go
if r.appendAdmissionGuard != nil {
	err := r.appendAdmissionGuard.AllowChannelAppend(ctx, ch.AppendAdmissionRequest{
		ChannelID:   rc.state.ID,
		ChannelKey:  rc.state.Key,
		Epoch:       rc.state.Epoch,
		LeaderEpoch: rc.state.LeaderEpoch,
		Leader:      rc.state.Leader,
	})
	if err != nil {
		return err
	}
}
```

- [ ] Implement `pkg/clusterv2/channel_lease.go`:

```go
type channelDataPlaneLeaseGuard struct {
	now       func() time.Time
	ttl       time.Duration
	lastOKNanos atomic.Int64
}

func newChannelDataPlaneLeaseGuard(now func() time.Time, ttl time.Duration) *channelDataPlaneLeaseGuard {
	return &channelDataPlaneLeaseGuard{now: now, ttl: ttl}
}

func (g *channelDataPlaneLeaseGuard) MarkVisible(at time.Time) {
	g.lastOKNanos.Store(at.UnixNano())
}

func (g *channelDataPlaneLeaseGuard) AllowChannelAppend(ctx context.Context, req ch.AppendAdmissionRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	last := g.lastOKNanos.Load()
	if last == 0 {
		return ch.ErrNotReady
	}
	if g.now().Sub(time.Unix(0, last)) > g.ttl {
		return ch.ErrNotReady
	}
	return nil
}
```

- [ ] In `pkg/clusterv2/node.go`, add a `channelDataPlaneLease *channelDataPlaneLeaseGuard` field with an English comment.

- [ ] In `pkg/clusterv2/node_loops.go`, call `MarkVisible` only after the existing Slot/control visibility path succeeds. Use the same health/report loop that already proves the node can reach the authoritative cluster plane.

- [ ] In `pkg/clusterv2/node_defaults.go`, pass `node.channelDataPlaneLease` into `channels.Config` and onward to ChannelV2.

- [ ] In `pkg/clusterv2/node_management.go`, include the lease freshness timestamp and TTL in the local control snapshot.

- [ ] Run focused tests:

```bash
GOWORK=off go test ./pkg/channelv2/... ./pkg/clusterv2 -run 'TestChannelDataPlaneLeaseGuard|Test.*Admission'
```

Expected: PASS.

---

## Task 3: Slot-Backed Migration Store Facade

**Purpose:** Provide a typed facade for durable migration tasks and fence commands using the existing Slot FSM commands.

- [ ] Read the existing command contracts before editing:

```bash
sed -n '1,260p' pkg/slot/fsm/channel_migration_cmds.go
rg -n "ChannelMigration|WriteFence" pkg/db/meta pkg/slot pkg/clusterv2
```

- [ ] Add failing tests in `pkg/clusterv2/channels/migration_store_test.go` covering:

1. Creating a task uses the guarded create command and rejects duplicate active task results.
2. Claiming a task writes owner node and owner epoch.
3. Advancing a phase includes expected task version.
4. Setting and clearing write fence uses the same task token.
5. Reads are scoped to the hash slot that owns the channel runtime meta.

- [ ] Create `pkg/clusterv2/channels/migration_store.go` with explicit ports:

```go
type MigrationCommandProposer interface {
	ProposeChannelMigrationCommand(ctx context.Context, slotID uint32, command []byte) error
}

type MigrationTaskReader interface {
	GetChannelMigrationTask(ctx context.Context, channelID string, channelType uint8) (metadb.ChannelMigrationTask, error)
	ListActiveChannelMigrationTasks(ctx context.Context, slotID uint32, limit int) ([]metadb.ChannelMigrationTask, error)
}

type MigrationStore struct {
	localNode uint64
	slots     SlotResolver
	proposer  MigrationCommandProposer
	reader    MigrationTaskReader
}
```

- [ ] Implement these methods with concrete error returns:

```go
func (s *MigrationStore) CreateLeaderTransfer(ctx context.Context, req LeaderTransferRequest) (metadb.ChannelMigrationTask, error)
func (s *MigrationStore) CreateReplicaReplace(ctx context.Context, req ReplicaReplaceRequest) (metadb.ChannelMigrationTask, error)
func (s *MigrationStore) Claim(ctx context.Context, taskID uint64, expectedVersion uint64) error
func (s *MigrationStore) Advance(ctx context.Context, taskID uint64, expectedVersion uint64, phase metadb.ChannelMigrationPhase, status metadb.ChannelMigrationStatus, reason string) error
func (s *MigrationStore) SetWriteFence(ctx context.Context, task metadb.ChannelMigrationTask, reason ch.WriteFenceReason) error
func (s *MigrationStore) ClearWriteFence(ctx context.Context, task metadb.ChannelMigrationTask) error
func (s *MigrationStore) Abort(ctx context.Context, task metadb.ChannelMigrationTask, reason string) error
```

- [ ] Wire the facade in `pkg/clusterv2/channels/service.go` without exposing Slot FSM details to `internalv2`.

- [ ] Run:

```bash
GOWORK=off go test ./pkg/clusterv2/channels -run 'TestMigrationStore'
```

Expected: PASS.

---

## Task 4: Runtime Probe And Drain Primitives

**Purpose:** Let migration code prove target readiness, catch-up state, and old-leader drain without touching SEND hot path.

- [ ] Extend `pkg/channelv2/bench_runtime.go` `RuntimeProbeResult` with fields that migration needs:

```go
LeaderEpoch uint64
ChannelEpoch uint64
Role ch.Role
Status ch.Status
LEO uint64
HW uint64
CheckpointHW uint64
WriteFence ch.WriteFence
InflightAppend bool
PendingAppendCount int
```

- [ ] Add a failing test in `pkg/channelv2/service/bench_runtime_test.go` or the existing runtime probe test file asserting the probe returns `InflightAppend`, `PendingAppendCount`, and fence state.

- [ ] Implement probe population from `ChannelState` snapshots. Keep the snapshot copy bounded and do not allocate per pending append item; only report count and booleans.

- [ ] Add a `DrainChannel` method at the ChannelV2 service boundary:

```go
type DrainChannelRequest struct {
	ChannelID    ChannelID
	ChannelType  uint8
	LeaderEpoch  uint64
	FenceVersion uint64
	Timeout      time.Duration
}

type DrainChannelResult struct {
	Drained bool
	LEO     uint64
	HW      uint64
}
```

- [ ] Implement drain by polling the runtime snapshot until:

1. The channel is still fenced with the requested fence version.
2. No in-flight append exists.
3. Pending append count is zero.
4. `HW >= LEO` for the local leader state.

- [ ] Return `ch.ErrStaleMeta` if leader epoch or fence version changes during drain.

- [ ] Run:

```bash
GOWORK=off go test ./pkg/channelv2/... -run 'Test.*Probe|Test.*Drain'
```

Expected: PASS.

---

## Task 5: Manual Leader Transfer Executor

**Purpose:** Provide a controlled, testable path to move a ChannelV2 leader from one live node to another live ISR member.

- [ ] Add orchestration tests in `pkg/clusterv2/channels/migration_leader_transfer_test.go` for the exact phase order:

```text
Validate -> ProbeTarget -> SetWriteFence -> DrainOldLeader -> FinalTargetCatchUp -> CommitLeaderMeta -> VerifyNewLeader -> ClearFence -> Completed
```

- [ ] Create `pkg/clusterv2/channels/migration_executor.go` with a single task loop:

```go
type MigrationExecutor struct {
	localNode uint64
	store     *MigrationStore
	runtime   MigrationRuntime
	meta      RuntimeMetaReader
	observer  MigrationObserver
	clock     func() time.Time
}
```

- [ ] Define `MigrationRuntime` with the exact operations the executor uses:

```go
type MigrationRuntime interface {
	ProbeChannel(ctx context.Context, nodeID uint64, channelID string, channelType uint8) (ch.RuntimeProbeResult, error)
	DrainChannel(ctx context.Context, req ch.DrainChannelRequest) (ch.DrainChannelResult, error)
}
```

- [ ] Implement `RunOnce(ctx)` to claim only Slot-owned active tasks assigned to this node. If claim fails with a stale version, return nil and let another worker continue.

- [ ] Implement `migration_leader_transfer.go`:

1. `Validate` checks source is current meta leader, target is in `Replicas`, target is in `ISR`, `MinISR` is satisfied, and no current fence belongs to another active task.
2. `ProbeTarget` verifies target runtime has the same `ChannelEpoch`, current `LeaderEpoch`, status ready, and `HW >= meta.MaxMessageSeq`.
3. `SetWriteFence` writes a durable fence with the task token and `WriteFenceReasonLeaderTransfer`.
4. `DrainOldLeader` calls `DrainChannel` against the old leader with the task fence version.
5. `FinalTargetCatchUp` probes target again and requires `HW >= oldLeaderDrain.LEO`.
6. `CommitLeaderMeta` uses the existing Slot command to update `Leader=target`, increment `LeaderEpoch`, keep `Replicas`, keep `ISR`, keep `ChannelEpoch`.
7. `VerifyNewLeader` probes target until it reports leader role with the new `LeaderEpoch`.
8. `ClearFence` clears only the fence token owned by the task.
9. `Completed` advances task status and records old/new leader.

- [ ] Every phase advance must include the expected task version. On version conflict, stop the local run and return nil.

- [ ] Record observer events:

```go
observer.MigrationPhase(taskID, taskType, phase, status, reason)
observer.MigrationDuration(taskType, phase, duration)
observer.WriteFenceDuration(taskID, fenceVersion, duration)
```

- [ ] Run:

```bash
GOWORK=off go test ./pkg/clusterv2/channels -run 'TestLeaderTransfer|TestMigrationExecutor'
```

Expected: PASS.

---

## Task 6: Manual Replica Replacement Executor

**Purpose:** Add a controlled path to replace a failed or removed replica while preserving `MinISR` and quorum-safe commit.

- [ ] Add phase-order tests in `pkg/clusterv2/channels/migration_replica_replace_test.go`:

```text
Validate -> TransferLeaderIfSourceIsLeader -> AddLearner -> BootstrapTarget -> WarmCatchUp -> SetWriteFence -> FinalCatchUp -> PromoteLearnerAndRemoveSource -> VerifyMembership -> ClearFence -> Completed
```

- [ ] Implement `migration_replica_replace.go`:

1. `Validate` checks source is in `Replicas`, target is healthy and absent from `Replicas`, and no active task owns the same channel.
2. `TransferLeaderIfSourceIsLeader` creates or runs an embedded leader transfer to an ISR survivor before replacing the source.
3. `AddLearner` uses the existing Slot command to add target as learner/non-voting replica.
4. `BootstrapTarget` starts target replication from the authoritative channel log prefix.
5. `WarmCatchUp` probes target until lag is below the configured final-catch-up threshold.
6. `SetWriteFence` stops new writes using `WriteFenceReasonReplicaReplace`.
7. `FinalCatchUp` requires target `HW >= current meta.MaxMessageSeq`.
8. `PromoteLearnerAndRemoveSource` atomically updates replicas and ISR with the existing promote/remove command.
9. `VerifyMembership` probes target and at least `MinISR` survivors under the new meta.
10. `ClearFence` clears the task-owned fence.
11. `Completed` records source and target.

- [ ] Enforce fail-closed placement: if healthy replacement candidates are fewer than needed, do not reduce replica count and do not lower `MinISR`; advance task to blocked with reason `no_healthy_replacement`.

- [ ] Run:

```bash
GOWORK=off go test ./pkg/clusterv2/channels -run 'TestReplicaReplace|TestLeaderTransfer|TestMigrationExecutor'
```

Expected: PASS.

---

## Task 7: Manager Manual Migration API

**Purpose:** Give operators a safe manual path before automatic repair is enabled.

- [ ] Add usecase tests in `internalv2/usecase/management/channel_migration_test.go` for:

1. Leader transfer rejects target outside replicas.
2. Replica replacement rejects target already in replicas.
3. Duplicate active task maps to a conflict response.
4. Successful request returns task ID, channel ID, channel type, source, target, type, status, and phase.

- [ ] Implement `internalv2/usecase/management/channel_migration.go`:

```go
type ChannelMigrationUsecase struct {
	store ChannelMigrationStore
}

type LeaderTransferInput struct {
	ChannelID   string
	ChannelType uint8
	TargetNode  uint64
}

type ReplicaReplaceInput struct {
	ChannelID   string
	ChannelType uint8
	SourceNode  uint64
	TargetNode  uint64
}
```

- [ ] Keep all validation entry-independent in the usecase. HTTP handlers only decode, call the usecase, and encode.

- [ ] Add manager handlers in `internalv2/access/manager/channel_migration.go`:

```text
POST /api/v2/channel-migrations/leader-transfer
POST /api/v2/channel-migrations/replica-replace
GET  /api/v2/channel-migrations/active
GET  /api/v2/channel-migrations/:taskId
POST /api/v2/channel-migrations/:taskId/abort
```

- [ ] Extend `internalv2/access/manager/channel_runtime_meta.go` response with:

```go
WriteFenceToken   string `json:"write_fence_token,omitempty"`
WriteFenceVersion uint64 `json:"write_fence_version,omitempty"`
WriteFenceReason  string `json:"write_fence_reason,omitempty"`
ActiveTaskID      uint64 `json:"active_task_id,omitempty"`
Degraded          bool   `json:"degraded"`
DegradedReason    string `json:"degraded_reason,omitempty"`
```

- [ ] Run:

```bash
GOWORK=off go test ./internalv2/usecase/management ./internalv2/access/manager -run 'Test.*ChannelMigration|Test.*ChannelRuntimeMeta'
```

Expected: PASS.

---

## Task 8: Automatic Dead-Leader Failover Planner And Scheduler

**Purpose:** Automatically recover channels whose current ChannelV2 leader is stale, missing, down, removed, leaving, or not runtime-ready.

- [ ] Add planner tests in `pkg/clusterv2/channels/failover_planner_test.go`:

1. Selects a healthy ISR target with the highest compatible `HW`.
2. Rejects candidates not in ISR.
3. Rejects candidates with mismatched channel epoch or leader epoch.
4. Blocks when no candidate proves the safe prefix.
5. Blocks when an active migration task already exists.

- [ ] Implement `failover_planner.go` with explicit decision results:

```go
type FailoverDecision struct {
	Action       FailoverAction
	TargetNode   uint64
	BlockReason  string
	ObservedHW   uint64
	ObservedEpoch uint64
}

type FailoverAction uint8

const (
	FailoverActionNone FailoverAction = iota
	FailoverActionCreateLeaderTransfer
	FailoverActionBlocked
)
```

- [ ] Candidate requirements:

1. Node is healthy and active in control state.
2. Node is in current `ISR`.
3. Node runtime probe reports the same `ChannelEpoch`.
4. Node runtime probe reports the current or immediately previous `LeaderEpoch` accepted by the migration proof rules.
5. Node `HW >= meta.MaxMessageSeq` or the planner records `target_lagging`.
6. Candidate has no pending truncation below the safe prefix.

- [ ] Add scheduler tests in `pkg/clusterv2/channels/repair_scanner_test.go`:

1. Scans only hash slots owned by the local Slot leader.
2. Uses bounded page size and max pages.
3. Suppresses duplicate work when an active migration task exists.
4. Emits blocked reasons without creating tasks.
5. Respects per-node and per-slot rate limits.

- [ ] Implement `repair_scanner.go`:

```go
type RepairScannerConfig struct {
	Enabled          bool
	PageLimit        int
	MaxPagesPerTick  int
	MaxTasksPerTick  int
	TickInterval     time.Duration
}
```

- [ ] If adding `WK_` configuration, add English comments to the config struct and update `wukongim.conf.example` with defaults. Keep automatic failover disabled by default until e2ev2 coverage is green.

- [ ] Scheduler action for dead leader:

1. Read `ChannelRuntimeMeta` from bounded pages.
2. Identify stale leader using control node health, node state, runtime probe error, or lease-ready false.
3. Call failover planner.
4. Create a leader-transfer/failover task with `WriteFenceReasonFailover`.
5. Let the existing migration executor run the phases.

- [ ] Run:

```bash
GOWORK=off go test ./pkg/clusterv2/channels -run 'TestFailover|TestRepairScanner'
```

Expected: PASS.

---

## Task 9: Automatic Replica Repair Planner

**Purpose:** Automatically restore replica count after a data node is down, removed, or leaving, without reducing `MinISR`.

- [ ] Add tests in `pkg/clusterv2/channels/repair_planner_test.go`:

1. Does not create new channels when healthy placement candidates are fewer than replica count.
2. Existing channel with `MinISR` survivors is marked degraded and remains writable.
3. Existing channel without a healthy replacement records `no_healthy_replacement`.
4. Existing channel with a healthy target creates a replica replacement task.
5. Source leader triggers leader failover before replica replacement.

- [ ] Implement `repair_planner.go`:

```go
type ReplicaRepairDecision struct {
	Action      ReplicaRepairAction
	SourceNode  uint64
	TargetNode  uint64
	BlockReason string
}

type ReplicaRepairAction uint8

const (
	ReplicaRepairActionNone ReplicaRepairAction = iota
	ReplicaRepairActionCreateReplicaReplace
	ReplicaRepairActionWaitForLeaderFailover
	ReplicaRepairActionBlocked
)
```

- [ ] Integrate replica repair into the bounded `RepairScanner` after dead-leader detection. Leader recovery has priority over follower replacement for the same channel.

- [ ] Ensure placement uses the same candidate policy as new channel creation. If the placement layer currently creates channels with fewer replicas than configured, change that path to return a retryable placement error and add unit tests.

- [ ] Run:

```bash
GOWORK=off go test ./pkg/clusterv2/channels -run 'TestReplicaRepair|TestRepairScanner|TestFailover'
```

Expected: PASS.

---

## Task 10: Observability, Management Snapshots, And FLOW Docs

**Purpose:** Make failover state explainable before running destructive black-box tests.

- [ ] Add low-cardinality metrics or observer events for:

```text
channelv2_migration_active_tasks
channelv2_migration_phase_duration_seconds
channelv2_migration_blocked_total{reason}
channelv2_write_fence_active
channelv2_write_fence_duration_seconds
channelv2_repair_scan_pages_total
channelv2_repair_scan_backlog
channelv2_failover_total{result}
channelv2_replica_repair_total{result}
channelv2_data_plane_lease_ready
```

- [ ] Extend manager node/channel runtime surfaces so operators can distinguish:

1. Slot leader.
2. ChannelV2 data leader.
3. Preferred leader.
4. Actual runtime role.
5. Active migration task.
6. Write fence state.
7. Data-plane lease freshness.
8. Degraded reason.

- [ ] Add or update tests for JSON fields in `internalv2/access/manager`.

- [ ] Update affected `FLOW.md` files listed in the file structure section. Include the new order of operations: write fence, drain, commit meta, verify, clear fence.

- [ ] Run:

```bash
GOWORK=off go test ./pkg/clusterv2 ./pkg/clusterv2/channels ./internalv2/usecase/management ./internalv2/access/manager
```

Expected: PASS.

---

## Task 11: Three-Node e2ev2 Kill-Node Black-Box Test

**Purpose:** Prove the accepted user scenario with real `wukongimv2` binaries: start three nodes, send messages, kill one node, recover affected channels, continue sending.

- [ ] Inspect existing e2ev2 suite helpers before writing the test:

```bash
find test/e2ev2 -maxdepth 3 -type f | sort
sed -n '1,260p' test/e2ev2/suite/*.go
```

- [ ] Create `test/e2ev2/message/channelv2_failover_test.go`.

- [ ] Test case `TestChannelV2ThreeNodeLeaderFailoverAfterNodeKill`:

1. Start a three-node cluster with ChannelV2 enabled and replica count 3.
2. Create enough channels to ensure some are led by each node.
3. Send messages until every selected channel has at least one SENDACK.
4. Record acknowledged message IDs and channel sequence numbers.
5. Kill the node that currently leads at least one selected channel.
6. Wait for manager or metrics surface to show migration completion for affected channels.
7. Send more messages to the same channels.
8. Read back message history and assert every pre-kill SENDACK message is present exactly once.
9. Assert post-failover messages get SENDACK and monotonically increasing channel sequence.
10. Assert channels whose leader was not killed continued to accept messages during repair.

- [ ] Test case `TestChannelV2NewPlacementFailsClosedWhenReplicaCountUnavailable`:

1. Start three nodes with replica count 3.
2. Kill one node.
3. Create a new channel or send first message to a new channel.
4. Assert placement returns retryable not-ready or placement-unavailable.
5. Restart the node.
6. Assert new placement succeeds with three replicas.

- [ ] Keep long waits behind existing e2ev2 polling helpers. Do not use fixed sleeps except for a bounded retry interval already accepted by the suite.

- [ ] Run the focused e2ev2 package:

```bash
GOWORK=off go test ./test/e2ev2/message -run 'TestChannelV2ThreeNodeLeaderFailoverAfterNodeKill|TestChannelV2NewPlacementFailsClosedWhenReplicaCountUnavailable' -count=1
```

Expected: PASS.

---

## Task 12: Full Focused Verification And Commit Sequence

**Purpose:** Close the feature with evidence and without hiding unrelated worktree changes.

- [ ] Run the narrow unit test set:

```bash
GOWORK=off go test \
  ./pkg/channelv2/... \
  ./pkg/clusterv2 \
  ./pkg/clusterv2/channels \
  ./internalv2/infra/cluster \
  ./internalv2/usecase/management \
  ./internalv2/access/manager
```

Expected: PASS.

- [ ] Run the focused e2ev2 test:

```bash
GOWORK=off go test ./test/e2ev2/message -run 'TestChannelV2ThreeNodeLeaderFailoverAfterNodeKill|TestChannelV2NewPlacementFailsClosedWhenReplicaCountUnavailable' -count=1
```

Expected: PASS.

- [ ] Check the worktree:

```bash
git -c core.fsmonitor=false --no-pager status --short
```

Expected: only files touched by this plan plus intentional generated test artifacts.

- [ ] Commit in reviewable slices:

```bash
git add pkg/channelv2 pkg/clusterv2 internalv2 wukongim.conf.example
git commit -m "feat: add channelv2 write fencing and lease admission"

git add pkg/clusterv2/channels internalv2/usecase/management internalv2/access/manager
git commit -m "feat: add channelv2 migration executor"

git add pkg/clusterv2/channels internalv2 pkg/metrics docs
git commit -m "feat: add channelv2 automatic repair"

git add test/e2ev2/message/channelv2_failover_test.go
git commit -m "test: cover channelv2 node failure recovery"
```

Expected: each commit contains only related changes and passes its focused verification.

---

## Self-Review Checklist

- [ ] Fence metadata is durable, projected, enforced, and never cleared by local wall-clock expiry alone.
- [ ] Node data-plane lease blocks stale isolated leaders without per-channel renewals.
- [ ] Manual leader transfer works before automatic failover is enabled.
- [ ] Manual replica replacement works before automatic replica repair is enabled.
- [ ] Automatic scans are bounded by slot ownership, page limit, max pages, and task limits.
- [ ] New placement fails closed when healthy candidates are fewer than configured replica count.
- [ ] Existing degraded channels do not lower `MinISR`.
- [ ] Manager surfaces show precise blocker reasons and separate Slot leadership from ChannelV2 leadership.
- [ ] e2ev2 proves three-node kill-node recovery with real binaries.
- [ ] All affected `FLOW.md` files and `wukongim.conf.example` are aligned with code behavior.
