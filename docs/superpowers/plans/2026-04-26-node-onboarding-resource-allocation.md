# Node Onboarding Resource Allocation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a manager-confirmed node onboarding flow that creates a persisted plan for an Active newly joined data node, then serially moves Slot replicas and selected Slot Leaders onto that node.

**Architecture:** Persist `NodeOnboardingJob` in controller metadata and replicate all job mutations through controller Raft commands. Keep plan generation and execution safety in `pkg/controller/plane`, expose leader-forwarding operations in `pkg/cluster`, keep `internal/usecase/management` and `internal/access/manager` as DTO/HTTP adapters, and add a dedicated `/onboarding` web wizard. The executor advances one running job on the controller leader before ordinary planner rebalance work; Bootstrap/Repair remain available, while automatic Rebalance is paused during a running onboarding job.

**Tech Stack:** Go controller metadata/Raft/control-plane packages, Gin manager API, React 19 + TypeScript + Vite/Vitest web UI, black-box e2e harness under `test/e2e`.

---

## Existing Context And Safety Notes

- Read `pkg/controller/FLOW.md` before editing `pkg/controller/**`, `pkg/cluster/FLOW.md` before editing `pkg/cluster/**`, and `test/e2e/AGENTS.md` plus nested `AGENTS.md` files before editing e2e tests.
- Preserve the project rule that there is no separate standalone semantic; tests/docs must say “single-node cluster” when describing deployment shape.
- The current worktree has unrelated user changes in `internal/FLOW.md` and `pkg/cluster/FLOW.md.bak`. Do not revert them. If implementation requires updating `internal/FLOW.md`, merge carefully and mention that the file was already dirty before edits.
- Use `GOWORK=off` for Go commands unless an implementation worker verifies the repository expects another mode.
- Follow `@superpowers:test-driven-development` inside each task: write the failing test, run it red, implement the smallest green change, then refactor.
- Exported types and important struct fields added by this feature require concise English comments.
- Do not return application conflicts from `StateMachine.Apply`; controller Raft treats apply errors as fatal. State-machine handlers must validate for corrupt commands but treat stale/racy commands as idempotent no-ops, then caller code must re-read to confirm the requested transition happened.

## File Structure

### Controller metadata

- Create: `pkg/controller/meta/onboarding_types.go`
  - Responsibility: `NodeOnboardingJob`, `NodeOnboardingMove`, statuses, blocked reasons, plan summary, candidate DTO-like metadata, result-count helper types.
- Create: `pkg/controller/meta/onboarding_codec.go`
  - Responsibility: binary encode/decode for onboarding jobs and stable key helpers.
- Create: `pkg/controller/meta/onboarding_store.go`
  - Responsibility: CRUD/list helpers and atomic job + optional assignment/task writes.
- Modify: `pkg/controller/meta/codec.go`
  - Responsibility: add `recordPrefixOnboardingJob = 'o'`, snapshot key/value validation hooks.
- Modify: `pkg/controller/meta/snapshot.go`
  - Responsibility: include onboarding jobs in export/import delete ranges and snapshot collection.
- Test: `pkg/controller/meta/onboarding_store_test.go`
  - Responsibility: job encode/decode, CRUD, ordering, snapshot restore, atomic update coverage.

### Controller plane and Raft command codec

- Create: `pkg/controller/plane/onboarding_planner.go`
  - Responsibility: targeted onboarding planner, blocked-reason calculation, simulated load loop, deterministic sorting, leader-transfer marking.
- Create: `pkg/controller/plane/onboarding_fingerprint.go`
  - Responsibility: canonical plan fingerprint input and SHA-256 hex computation.
- Create: `pkg/controller/plane/onboarding_executor.go`
  - Responsibility: pure decision logic for planned start validation and running-job advancement.
- Modify: `pkg/controller/plane/commands.go`
  - Responsibility: add onboarding command payloads and command kinds.
- Modify: `pkg/controller/plane/statemachine.go`
  - Responsibility: apply onboarding job upsert/update commands and optional atomic assignment/task mutation.
- Modify: `pkg/controller/plane/planner.go`
  - Responsibility: add explicit pause/lock knobs for automatic Rebalance without changing Bootstrap/Repair behavior.
- Modify: `pkg/controller/raft/service.go`
  - Responsibility: encode/decode `CommandKindNodeOnboardingJobUpdate` in the controller Raft `commandEnvelope`.
- Test: `pkg/controller/plane/onboarding_planner_test.go`
- Test: `pkg/controller/plane/onboarding_fingerprint_test.go`
- Test: `pkg/controller/plane/onboarding_executor_test.go`
- Test: `pkg/controller/plane/statemachine_onboarding_test.go`
- Test: `pkg/controller/raft/service_test.go`

### Cluster operations and controller RPC

- Create: `pkg/cluster/onboarding.go`
  - Responsibility: public cluster methods for candidates, plan, start, list/get/retry jobs, leader-local execution helpers, error sentinels.
- Create: `pkg/cluster/onboarding_executor.go`
  - Responsibility: integrate plane executor with `controllerTickOnce`, propose job/assignment/task updates, and call slot leader transfer when required.
- Modify: `pkg/cluster/api.go`
  - Responsibility: add onboarding methods to `API`.
- Modify: `pkg/cluster/codec_control.go`
  - Responsibility: add controller RPC request/response kinds and wire codecs for onboarding operations.
- Modify: `pkg/cluster/controller_client.go`
  - Responsibility: add forwarding methods to `controllerAPI` and `controllerClient`.
- Modify: `pkg/cluster/controller_handler.go`
  - Responsibility: dispatch onboarding RPCs on controller leader.
- Modify: `pkg/cluster/controller_metadata_snapshot.go`
  - Responsibility: include onboarding jobs in leader-local metadata snapshots.
- Modify: `pkg/cluster/controller_host.go`
  - Responsibility: mark metadata snapshot dirty for onboarding commands and wake planner/executor.
- Modify: `pkg/cluster/cluster.go`
  - Responsibility: call onboarding executor before ordinary planner, pause ordinary Rebalance while a job is running.
- Test: `pkg/cluster/onboarding_test.go`
- Test: `pkg/cluster/controller_handler_onboarding_test.go`
- Test: `pkg/cluster/codec_control_test.go`

### Management usecase and manager HTTP

- Create: `internal/usecase/management/node_onboarding.go`
  - Responsibility: manager-facing onboarding DTOs and `App` methods.
- Modify: `internal/usecase/management/app.go`
  - Responsibility: add onboarding operations to `ClusterReader`.
- Test: `internal/usecase/management/node_onboarding_test.go`
- Create: `internal/access/manager/node_onboarding.go`
  - Responsibility: HTTP handlers, request parsing, response DTO mapping, error mapping.
- Modify: `internal/access/manager/server.go`
  - Responsibility: extend `Management` interface with onboarding methods.
- Modify: `internal/access/manager/routes.go`
  - Responsibility: add `/manager/node-onboarding/**` routes with the required permissions.
- Test: `internal/access/manager/node_onboarding_test.go`

### Web manager UI

- Modify: `web/src/lib/manager-api.types.ts`
  - Responsibility: onboarding response/input types.
- Modify: `web/src/lib/manager-api.ts`
  - Responsibility: onboarding API functions and jobs pagination path builder.
- Modify: `web/src/lib/navigation.ts`
  - Responsibility: add Runtime -> 扩容 navigation item.
- Modify: `web/src/app/router.tsx`
  - Responsibility: add `/onboarding` route.
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/i18n/messages/en.ts`
  - Responsibility: onboarding labels, blocked-reason text, errors, buttons.
- Create: `web/src/pages/onboarding/page.tsx`
  - Responsibility: candidate selection, plan review, start confirmation, polling progress, retry flow.
- Create: `web/src/pages/onboarding/page.test.tsx`
  - Responsibility: wizard behavior, errors, polling, retry, permission state.
- Modify: `web/src/pages/page-shells.test.tsx`
  - Responsibility: ensure the new route/page shell renders.

### E2E and docs

- Modify: `test/e2e/suite/manager_client.go`
  - Responsibility: POST helper plus onboarding DTOs and wait helpers.
- Create: `test/e2e/cluster/dynamic_node_join/onboarding_resource_allocation_test.go`
  - Responsibility: black-box dynamic join + onboarding resource allocation scenario.
- Modify: `pkg/controller/FLOW.md`
  - Responsibility: document onboarding job metadata, commands, and planner/executor coordination.
- Modify: `pkg/cluster/FLOW.md`
  - Responsibility: document manager onboarding operations, controller RPCs, and controller tick execution order.
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
  - Responsibility: add one short note that new Active data nodes require the manager onboarding job to explicitly receive Slot replicas/Leaders.

## Implementation Notes

### Stable enums and errors

Use these string statuses in DTOs and web types:

```text
job status: planned | running | failed | completed | cancelled
move status: pending | running | completed | failed | skipped
blocked reason code: target_not_active | target_not_alive | target_draining | target_not_data | running_job_exists | no_runtime_view | slot_quorum_lost | slot_task_running | slot_task_failed | slot_hash_migration_active | already_balanced | no_safe_candidate
```

Add cluster sentinel errors in `pkg/cluster/onboarding.go` and map them in manager HTTP:

```go
var (
    ErrOnboardingRunningJobExists  = errors.New("raftcluster: onboarding running job exists")
    ErrOnboardingPlanNotExecutable = errors.New("raftcluster: onboarding plan not executable")
    ErrOnboardingPlanStale         = errors.New("raftcluster: onboarding plan stale")
    ErrOnboardingInvalidJobState   = errors.New("raftcluster: onboarding invalid job state")
)
```

### Controller command shape

Add command kinds after `CommandKindNodeJoinActivate`:

```go
const (
    CommandKindNodeOnboardingJobUpdate CommandKind = 19
)
```

Use one update payload to keep the Raft command surface small:

```go
// NodeOnboardingJobUpdate persists a job transition and optional Slot task mutation atomically.
type NodeOnboardingJobUpdate struct {
    // Job is the full durable job state to upsert.
    Job *controllermeta.NodeOnboardingJob
    // ExpectedStatus is optional; when set, the state machine no-ops if the stored job has moved elsewhere.
    ExpectedStatus *controllermeta.OnboardingJobStatus
    // Assignment is written with Task when starting a move; nil means no assignment mutation.
    Assignment *controllermeta.SlotAssignment
    // Task is written with Assignment when starting a move; nil means no task mutation.
    Task *controllermeta.ReconcileTask
}
```

State-machine apply rules:

- Return `ErrInvalidArgument` only for malformed commands that would indicate a programming/serialization bug.
- If `ExpectedStatus` is set and the stored job status no longer matches, no-op rather than returning an error.
- If `Job.Status == running`, enforce the one-running-job invariant inside the metadata store lock before writing: any existing running job with a different `job_id` makes this command a no-op.
- If `Assignment` and `Task` are both present, write job + assignment + task in the same guarded Pebble batch.

### Planned start and retry semantics

- `CreateNodeOnboardingPlan(ctx, targetNodeID, retryOfJobID)` runs on the controller leader, builds a persisted `planned` job, proposes `NodeOnboardingJobUpdate`, then re-reads and returns the stored job.
- `StartNodeOnboardingJob(ctx, jobID)` runs compatibility checks against the current controller leader snapshot before proposing a `running` job update with `ExpectedStatus=planned`. After proposal it re-reads the job; if this job is not `running`, re-read running jobs and return `ErrOnboardingRunningJobExists` when another job won the Raft race, otherwise return `ErrOnboardingPlanStale`.
- `RetryNodeOnboardingJob(ctx, jobID)` only accepts `failed`, reads the failed job target, then calls the same plan creation path with `RetryOfJobID` set.
- `POST /manager/node-onboarding/plan` creates a `planned` job even when the target is inactive/alive-blocked or slot-level blocked reasons exist; invalid request bodies and missing target nodes remain HTTP errors and create no job.

### Executor order in `controllerTickOnce`

Controller leader tick order must be:

```text
1. skip if not local controller leader or warmup incomplete
2. snapshot metadata + runtime observations + hash-slot migration protections
3. if a running onboarding job exists:
   a. advance exactly one onboarding state transition if possible
   b. run ordinary planner with automatic Rebalance disabled and current onboarding Slot locked
4. if no running onboarding job exists:
   a. run ordinary planner as today
```

The executor should propose at most one controller command per tick. If it starts a move, it writes the assignment replacement, creates `TaskKindRebalance`, marks the move `running`, and returns. If it observes a completed move and `leader_transfer_required=true`, it calls `TransferSlotLeader(ctx, slotID, target)` before marking the move `completed`; if transfer fails, mark the move and job failed.

## Task 1: Add Controller Metadata Model, Codec, Store, And Snapshot

**Files:**
- Create: `pkg/controller/meta/onboarding_types.go`
- Create: `pkg/controller/meta/onboarding_codec.go`
- Create: `pkg/controller/meta/onboarding_store.go`
- Modify: `pkg/controller/meta/codec.go`
- Modify: `pkg/controller/meta/snapshot.go`
- Test: `pkg/controller/meta/onboarding_store_test.go`

- [ ] **Step 1: Write failing metadata tests**

Add tests covering encode/decode round trip, invalid job validation, CRUD list order, running-job listing, and snapshot restore:

```go
func TestOnboardingJobStoreRoundTripAndSnapshot(t *testing.T) {
    ctx := context.Background()
    store := openTestStore(t)
    now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
    job := sampleOnboardingJob("onboard-20260426-000001", now)

    require.NoError(t, store.UpsertOnboardingJob(ctx, job))
    got, err := store.GetOnboardingJob(ctx, job.JobID)
    require.NoError(t, err)
    require.Equal(t, job, got)

    snapshot, err := store.ExportSnapshot(ctx)
    require.NoError(t, err)

    restored := openTestStore(t)
    require.NoError(t, restored.ImportSnapshot(ctx, snapshot))
    restoredJob, err := restored.GetOnboardingJob(ctx, job.JobID)
    require.NoError(t, err)
    require.Equal(t, job, restoredJob)
}

func TestListRunningOnboardingJobs(t *testing.T) {
    ctx := context.Background()
    store := openTestStore(t)
    require.NoError(t, store.UpsertOnboardingJob(ctx, sampleOnboardingJobWithStatus("a", meta.OnboardingJobStatusPlanned)))
    require.NoError(t, store.UpsertOnboardingJob(ctx, sampleOnboardingJobWithStatus("b", meta.OnboardingJobStatusRunning)))

    jobs, err := store.ListRunningOnboardingJobs(ctx)
    require.NoError(t, err)
    require.Len(t, jobs, 1)
    require.Equal(t, "b", jobs[0].JobID)
}
```

- [ ] **Step 2: Run tests and verify red**

Run: `GOWORK=off go test ./pkg/controller/meta -run 'TestOnboardingJob(StoreRoundTripAndSnapshot|ListRunning)' -count=1 -v`

Expected: FAIL because onboarding job types and store methods do not exist.

- [ ] **Step 3: Add onboarding types with comments**

Implement in `onboarding_types.go`:

```go
// OnboardingJobStatus records the durable lifecycle state of a node onboarding job.
type OnboardingJobStatus string

const (
    OnboardingJobStatusPlanned   OnboardingJobStatus = "planned"
    OnboardingJobStatusRunning   OnboardingJobStatus = "running"
    OnboardingJobStatusFailed    OnboardingJobStatus = "failed"
    OnboardingJobStatusCompleted OnboardingJobStatus = "completed"
    OnboardingJobStatusCancelled OnboardingJobStatus = "cancelled"
)

// NodeOnboardingJob is the durable audit and execution state for moving Slot resources to one target node.
type NodeOnboardingJob struct {
    JobID            string
    TargetNodeID     uint64
    RetryOfJobID     string
    Status           OnboardingJobStatus
    CreatedAt        time.Time
    UpdatedAt        time.Time
    StartedAt        time.Time
    CompletedAt      time.Time
    PlanVersion      uint32
    PlanFingerprint  string
    Plan             NodeOnboardingPlan
    Moves            []NodeOnboardingMove
    CurrentMoveIndex int
    CurrentTask      *ReconcileTask
    LastError        string
}
```

Also add `NodeOnboardingPlan`, `NodeOnboardingPlanSummary`, `NodeOnboardingPlanMove`, `NodeOnboardingBlockedReason`, `NodeOnboardingMove`, `OnboardingMoveStatus`, and `OnboardingResultCounts` with English field comments.

- [ ] **Step 4: Implement validation, cloning, and codec**

In `codec.go`, add `recordPrefixOnboardingJob byte = 'o'` and include it in `validateSnapshotKey`.

In `onboarding_codec.go`, implement:

```go
func encodeOnboardingJobKey(jobID string) []byte
func decodeOnboardingJobKey(key []byte) (string, error)
func encodeOnboardingJob(job NodeOnboardingJob) []byte
func decodeOnboardingJob(key, data []byte) (NodeOnboardingJob, error)
```

Use the same project-local binary style as existing metadata records: record version byte, length-prefixed strings/slices, big-endian integers, UnixNano timestamps. Normalize peer slices in plan/move records before persistence.

- [ ] **Step 5: Implement store helpers and atomic write**

In `onboarding_store.go`, implement:

```go
func (s *Store) UpsertOnboardingJob(ctx context.Context, job NodeOnboardingJob) error
func (s *Store) UpsertOnboardingJobAssignmentTask(ctx context.Context, job NodeOnboardingJob, assignment SlotAssignment, task ReconcileTask) error
func (s *Store) GuardedUpsertOnboardingJob(ctx context.Context, job NodeOnboardingJob, expectedStatus *OnboardingJobStatus, assignment *SlotAssignment, task *ReconcileTask) (bool, error)
func (s *Store) GetOnboardingJob(ctx context.Context, jobID string) (NodeOnboardingJob, error)
func (s *Store) ListOnboardingJobs(ctx context.Context, limit int, cursor string) ([]NodeOnboardingJob, string, bool, error)
func (s *Store) ListRunningOnboardingJobs(ctx context.Context) ([]NodeOnboardingJob, error)
```

List jobs by key order for store-level determinism. Cluster/management API layers must return jobs ordered by `created_at desc, job_id desc`; use an opaque cursor encoding the last returned `(created_at, job_id)` pair so pagination remains compatible with the public API ordering.

- [ ] **Step 6: Include jobs in snapshots**

Update `snapshot.go` import/export prefix lists to include `recordPrefixOnboardingJob`. Update `validateSnapshotValue` in `codec.go` to decode onboarding records.

- [ ] **Step 7: Run metadata tests and commit**

Run: `GOWORK=off go test ./pkg/controller/meta -count=1`

Expected: PASS.

Commit:

```bash
git add pkg/controller/meta/onboarding_types.go pkg/controller/meta/onboarding_codec.go pkg/controller/meta/onboarding_store.go pkg/controller/meta/codec.go pkg/controller/meta/snapshot.go pkg/controller/meta/onboarding_store_test.go
git commit -m "feat: persist node onboarding jobs"
```

## Task 2: Implement Targeted Onboarding Planner And Fingerprint

**Files:**
- Create: `pkg/controller/plane/onboarding_planner.go`
- Create: `pkg/controller/plane/onboarding_fingerprint.go`
- Test: `pkg/controller/plane/onboarding_planner_test.go`
- Test: `pkg/controller/plane/onboarding_fingerprint_test.go`

- [ ] **Step 1: Write failing planner tests**

Cover the approved cases:

```go
func TestPlanNodeOnboardingMovesTargetTowardTargetMaxUsingSimulatedLoad(t *testing.T) {
    state := onboardingPlannerStateWithFourActiveNodes()
    plan := plane.NewOnboardingPlanner(plane.OnboardingPlannerConfig{ReplicaN: 3}).Plan(state, 4)

    require.Empty(t, plan.BlockedReasons)
    require.Len(t, plan.Moves, 3)
    require.Equal(t, uint64(4), plan.TargetNodeID)
    require.Equal(t, 3, plan.Summary.PlannedTargetSlotCount)
    requireUniqueSlots(t, plan.Moves)
}

func TestPlanNodeOnboardingSkipsUnsafeSlotsAndReportsReasons(t *testing.T) {
    state := onboardingPlannerStateWithQuorumLostTaskAndMigration()
    plan := plane.NewOnboardingPlanner(plane.OnboardingPlannerConfig{ReplicaN: 3}).Plan(state, 4)

    require.Empty(t, plan.Moves)
    requireBlockedCodes(t, plan.BlockedReasons, "slot_quorum_lost", "slot_task_running", "slot_hash_migration_active")
}

func TestPlanNodeOnboardingMarksOnlyNeededLeaderTransfers(t *testing.T) {
    state := onboardingPlannerStateWithSourceLeaders()
    plan := plane.NewOnboardingPlanner(plane.OnboardingPlannerConfig{ReplicaN: 3}).Plan(state, 4)

    require.Equal(t, 1, plan.Summary.PlannedLeaderGain)
    require.True(t, plan.Moves[0].LeaderTransferRequired)
}

func TestPlanNodeOnboardingReportsRunningJobExists(t *testing.T) {
    state := onboardingPlannerStateWithFourActiveNodes()
    state.RunningJobExists = true

    plan := plane.NewOnboardingPlanner(plane.OnboardingPlannerConfig{ReplicaN: 3}).Plan(state, 4)

    require.Empty(t, plan.Moves)
    requireBlockedCodes(t, plan.BlockedReasons, "running_job_exists")
}
```

- [ ] **Step 2: Write failing fingerprint tests**

Add tests proving identical semantic inputs produce the same SHA-256 hex and any included compatibility field changes it:

```go
func TestOnboardingPlanFingerprintChangesWhenAssignmentEpochChanges(t *testing.T) {
    input := sampleFingerprintInput()
    first := plane.OnboardingPlanFingerprint(input)
    input.Moves[0].AssignmentConfigEpoch++
    second := plane.OnboardingPlanFingerprint(input)
    require.NotEqual(t, first, second)
}

func TestOnboardingPlanFingerprintUsesLexicographicCanonicalKeys(t *testing.T) {
    input := sampleFingerprintInput()
    raw := plane.CanonicalOnboardingFingerprintJSON(input)
    require.Contains(t, string(raw), `{"assignment_balance_version":`)
    require.NotContains(t, string(raw), "\n")
}
```

- [ ] **Step 3: Run tests and verify red**

Run: `GOWORK=off go test ./pkg/controller/plane -run 'TestPlanNodeOnboarding|TestOnboardingPlanFingerprint' -count=1 -v`

Expected: FAIL because planner/fingerprint APIs do not exist.

- [ ] **Step 4: Implement planner input and eligibility helpers**

Add:

```go
// OnboardingPlanner creates deterministic Slot replica move plans for one target node.
type OnboardingPlanner struct { cfg OnboardingPlannerConfig }

type OnboardingPlannerConfig struct { ReplicaN int }

type OnboardingPlanInput struct {
    TargetNodeID uint64
    Nodes map[uint64]controllermeta.ClusterNode
    Assignments map[uint32]controllermeta.SlotAssignment
    Runtime map[uint32]controllermeta.SlotRuntimeView
    Tasks map[uint32]controllermeta.ReconcileTask
    MigratingSlots map[uint32]struct{}
    RunningJobExists bool
    Now time.Time
}
```

Reuse existing package-private helpers where possible: `nodeSchedulableForData`, `nodeActiveDataMember`, `containsPeer`, `replacePeer`, `slotMigrating` semantics.

- [ ] **Step 5: Implement simulated load loop and sorting**

Implement exactly:

```text
target_min=floor(total_replicas/active_nodes)
target_max=ceil(total_replicas/active_nodes)
desired_target_load=target_max
stop when simulated target load >= target_max
same Slot at most once
sort source-is-current-leader desc, source load desc, BalanceVersion asc, SlotID asc
```

Add blocked reasons for target, running-job, and slot safety conditions. When `RunningJobExists=true`, return `moves=[]` and `running_job_exists` so `POST /manager/node-onboarding/plan` persists a blocked planned job instead of an executable-looking plan. Return partial moves plus reasons if some slots are blocked; return `already_balanced` when target load is already at `target_max`; return `no_safe_candidate` only when target is below target_max and no move can be selected.

- [ ] **Step 6: Implement leader-transfer marking**

Compute:

```text
desired_target_leader_count=ceil(total_physical_slot_count/active_nodes)
leader_need=max(0, desired_target_leader_count-current_target_leader_count)
```

Mark only selected moves whose current leader is the source while `leader_need > 0`.

- [ ] **Step 7: Implement canonical fingerprint**

In `onboarding_fingerprint.go`, implement the spec's canonical JSON deliberately: dictionary/object keys sorted lexicographically, arrays in plan move order, integers as decimal JSON numbers, times as RFC3339Nano strings, no insignificant whitespace, then return `hex.EncodeToString(sha256.Sum256(raw))`. Do not rely on Go struct field declaration order as a substitute for lexicographic field-name ordering.

- [ ] **Step 8: Run planner tests and commit**

Run: `GOWORK=off go test ./pkg/controller/plane -run 'TestPlanNodeOnboarding|TestOnboardingPlanFingerprint' -count=1`

Expected: PASS.

Commit:

```bash
git add pkg/controller/plane/onboarding_planner.go pkg/controller/plane/onboarding_fingerprint.go pkg/controller/plane/onboarding_planner_test.go pkg/controller/plane/onboarding_fingerprint_test.go
git commit -m "feat: plan node onboarding resource moves"
```

## Task 3: Add Controller Commands, Raft Serialization, State-Machine Application, And Planner Pause Knobs

**Files:**
- Modify: `pkg/controller/plane/commands.go`
- Modify: `pkg/controller/plane/statemachine.go`
- Modify: `pkg/controller/plane/planner.go`
- Modify: `pkg/controller/raft/service.go`
- Test: `pkg/controller/plane/statemachine_onboarding_test.go`
- Test: `pkg/controller/plane/planner_test.go`
- Test: `pkg/controller/raft/service_test.go`

- [ ] **Step 1: Write failing state-machine and Raft command codec tests**

Add tests for job-only update and atomic job + assignment + task update:

```go
func TestStateMachineAppliesOnboardingJobUpdate(t *testing.T) {
    sm, store := newStateMachineTestEnv(t)
    job := sampleOnboardingJobWithStatus("job-1", controllermeta.OnboardingJobStatusPlanned)

    err := sm.Apply(context.Background(), plane.Command{
        Kind: plane.CommandKindNodeOnboardingJobUpdate,
        NodeOnboarding: &plane.NodeOnboardingJobUpdate{Job: &job},
    })
    require.NoError(t, err)

    got, err := store.GetOnboardingJob(context.Background(), "job-1")
    require.NoError(t, err)
    require.Equal(t, controllermeta.OnboardingJobStatusPlanned, got.Status)
}

func TestStateMachineAppliesOnboardingMoveStartAtomically(t *testing.T) {
    sm, store := newStateMachineTestEnv(t)
    job, assignment, task := sampleRunningMoveUpdate()
    expected := controllermeta.OnboardingJobStatusRunning

    err := sm.Apply(context.Background(), plane.Command{
        Kind: plane.CommandKindNodeOnboardingJobUpdate,
        NodeOnboarding: &plane.NodeOnboardingJobUpdate{Job: &job, ExpectedStatus: &expected, Assignment: &assignment, Task: &task},
    })
    require.NoError(t, err)

    _, err = store.GetOnboardingJob(context.Background(), job.JobID)
    require.NoError(t, err)
    _, err = store.GetAssignment(context.Background(), assignment.SlotID)
    require.NoError(t, err)
    _, err = store.GetTask(context.Background(), task.SlotID)
    require.NoError(t, err)
}

func TestStateMachineOnboardingStartNoOpsWhenAnotherJobIsRunning(t *testing.T) {
    sm, store := newStateMachineTestEnv(t)
    require.NoError(t, store.UpsertOnboardingJob(context.Background(), sampleOnboardingJobWithStatus("running-job", controllermeta.OnboardingJobStatusRunning)))
    planned := sampleOnboardingJobWithStatus("planned-job", controllermeta.OnboardingJobStatusPlanned)
    require.NoError(t, store.UpsertOnboardingJob(context.Background(), planned))
    job := planned
    job.Status = controllermeta.OnboardingJobStatusRunning
    expected := controllermeta.OnboardingJobStatusPlanned

    err := sm.Apply(context.Background(), plane.Command{
        Kind: plane.CommandKindNodeOnboardingJobUpdate,
        NodeOnboarding: &plane.NodeOnboardingJobUpdate{Job: &job, ExpectedStatus: &expected},
    })
    require.NoError(t, err)

    got, err := store.GetOnboardingJob(context.Background(), "planned-job")
    require.NoError(t, err)
    require.Equal(t, controllermeta.OnboardingJobStatusPlanned, got.Status)
}

func TestEncodeDecodeCommandRoundTripsNodeOnboardingJobUpdate(t *testing.T) {
    expected := controllermeta.OnboardingJobStatusPlanned
    job, assignment, task := sampleRunningMoveUpdate()

    data, err := encodeCommand(slotcontroller.Command{
        Kind: slotcontroller.CommandKindNodeOnboardingJobUpdate,
        NodeOnboarding: &slotcontroller.NodeOnboardingJobUpdate{
            Job: &job,
            ExpectedStatus: &expected,
            Assignment: &assignment,
            Task: &task,
        },
    })
    require.NoError(t, err)

    decoded, err := decodeCommand(data)
    require.NoError(t, err)
    require.Equal(t, slotcontroller.CommandKindNodeOnboardingJobUpdate, decoded.Kind)
    require.NotNil(t, decoded.NodeOnboarding)
    require.Equal(t, job.JobID, decoded.NodeOnboarding.Job.JobID)
    require.Equal(t, expected, *decoded.NodeOnboarding.ExpectedStatus)
    require.Equal(t, assignment.DesiredPeers, decoded.NodeOnboarding.Assignment.DesiredPeers)
    require.Equal(t, task.Kind, decoded.NodeOnboarding.Task.Kind)
}
```

- [ ] **Step 2: Write failing planner-pause tests**

Add a test proving Bootstrap/Repair still happen but automatic Rebalance is disabled when requested:

```go
func TestPlannerSkipsAutomaticRebalanceWhenPaused(t *testing.T) {
    state := plannerStateWithRebalanceSkew()
    state.PauseRebalance = true
    decision, err := plane.NewPlanner(plane.PlannerConfig{SlotCount: 4, ReplicaN: 3}).NextDecision(context.Background(), state)
    require.NoError(t, err)
    require.Zero(t, decision.SlotID)
}
```

- [ ] **Step 3: Run tests and verify red**

Run: `GOWORK=off go test ./pkg/controller/plane ./pkg/controller/raft -run 'TestStateMachineAppliesOnboarding|TestStateMachineOnboardingStartNoOps|TestEncodeDecodeCommandRoundTripsNodeOnboarding|TestPlannerSkipsAutomaticRebalanceWhenPaused' -count=1 -v`

Expected: FAIL because command, command serialization, and pause fields do not exist.

- [ ] **Step 4: Add command payload and state-machine branch**

In `commands.go`, add `CommandKindNodeOnboardingJobUpdate` and `NodeOnboardingJobUpdate`, then add `NodeOnboarding *NodeOnboardingJobUpdate` to `Command`.

In `statemachine.go`, add:

```go
case CommandKindNodeOnboardingJobUpdate:
    if cmd.NodeOnboarding == nil || cmd.NodeOnboarding.Job == nil {
        return controllermeta.ErrInvalidArgument
    }
    return sm.applyNodeOnboardingJobUpdate(ctx, *cmd.NodeOnboarding)
```

`applyNodeOnboardingJobUpdate` must call `GuardedUpsertOnboardingJob` so expected-status checks, stale no-ops, optional assignment/task writes, and the single-running-job guard happen atomically under the metadata store lock. It must not return conflict/stale application errors.

- [ ] **Step 5: Update controller Raft command serialization**

In `pkg/controller/raft/service.go`, extend `commandEnvelope` with `NodeOnboarding *nodeOnboardingJobUpdateEnvelope` and implement explicit clone/encode/decode helpers for:

- full `NodeOnboardingJob`
- `NodeOnboardingPlan`, plan moves, blocked reasons, summary
- `NodeOnboardingMove`
- optional `ExpectedStatus`
- optional assignment/task nested inside the update

Do not rely on `json.Marshal` over interface-like or partially unexported values; preserve the existing manual envelope pattern so `encodeCommand`/`decodeCommand` round trip the complete command.

- [ ] **Step 6: Add planner pause and lock fields**

Add to `PlannerState` in its existing file:

```go
// PauseRebalance disables opportunistic automatic Rebalance decisions while keeping Bootstrap/Repair active.
PauseRebalance bool
// LockedSlots prevents ordinary planner decisions for slots owned by an external coordinator.
LockedSlots map[uint32]struct{}
```

Update `NextDecision` to skip `nextRebalanceDecision` when `PauseRebalance=true`. Update `ReconcileSlot` to return no new task for `LockedSlots[slotID]` unless an existing task already exists and is runnable.

- [ ] **Step 7: Run controller plane/Raft tests and commit**

Run: `GOWORK=off go test ./pkg/controller/plane ./pkg/controller/raft -count=1`

Expected: PASS.

Commit:

```bash
git add pkg/controller/plane/commands.go pkg/controller/plane/statemachine.go pkg/controller/plane/planner.go pkg/controller/raft/service.go pkg/controller/plane/statemachine_onboarding_test.go pkg/controller/plane/planner_test.go pkg/controller/raft/service_test.go
git commit -m "feat: apply node onboarding controller commands"
```

## Task 4: Implement Onboarding Start Validation And Running Executor Decisions

**Files:**
- Create: `pkg/controller/plane/onboarding_executor.go`
- Test: `pkg/controller/plane/onboarding_executor_test.go`

- [ ] **Step 1: Write failing executor tests for start compatibility**

Cover success and stale cases:

```go
func TestValidateNodeOnboardingStartRejectsChangedAssignment(t *testing.T) {
    job := plannedOnboardingJob()
    input := executableOnboardingInputForJob(job)
    assignment := input.Assignments[job.Moves[0].SlotID]
    assignment.ConfigEpoch++
    input.Assignments[job.Moves[0].SlotID] = assignment

    result := plane.ValidateNodeOnboardingStart(input, job)
    require.ErrorIs(t, result.Err, plane.ErrOnboardingPlanStale)
}

func TestValidateNodeOnboardingStartRejectsBlockedPlan(t *testing.T) {
    job := plannedOnboardingJob()
    job.Plan.BlockedReasons = []controllermeta.NodeOnboardingBlockedReason{{Code: "slot_task_running"}}
    result := plane.ValidateNodeOnboardingStart(executableOnboardingInputForJob(job), job)
    require.ErrorIs(t, result.Err, plane.ErrOnboardingPlanNotExecutable)
}
```

- [ ] **Step 2: Write failing executor tests for running advancement**

Cover pending move start, skipped move, completed move, required leader transfer, and failure when assignment is neither before nor after:

```go
func TestNextNodeOnboardingActionStartsPendingMove(t *testing.T) {
    job := runningOnboardingJobWithPendingMove()
    input := executableOnboardingInputForJob(job)

    action := plane.NextNodeOnboardingAction(input, job)
    require.Equal(t, plane.OnboardingActionStartMove, action.Kind)
    require.Equal(t, controllermeta.TaskKindRebalance, action.Task.Kind)
    require.Equal(t, controllermeta.TaskStepAddLearner, action.Task.Step)
    require.Equal(t, job.Moves[0].DesiredPeersAfter, action.Assignment.DesiredPeers)
}
```

- [ ] **Step 3: Run tests and verify red**

Run: `GOWORK=off go test ./pkg/controller/plane -run 'TestValidateNodeOnboardingStart|TestNextNodeOnboardingAction' -count=1 -v`

Expected: FAIL because executor APIs do not exist.

- [ ] **Step 4: Add executor errors and action types**

Add package-level sentinel errors in `pkg/controller/plane`:

```go
var (
    ErrOnboardingPlanNotExecutable = errors.New("controller: onboarding plan not executable")
    ErrOnboardingPlanStale = errors.New("controller: onboarding plan stale")
)
```

Add:

```go
type OnboardingActionKind uint8
const (
    OnboardingActionNone OnboardingActionKind = iota
    OnboardingActionStartMove
    OnboardingActionSkipMove
    OnboardingActionCompleteMove
    OnboardingActionFailJob
    OnboardingActionCompleteJob
)
```

- [ ] **Step 5: Implement start compatibility exactly from the spec**

`ValidateNodeOnboardingStart(input, job)` must verify target/source status, assignment before state and epoch/balance versions, runtime quorum, no running/retrying/failed task, no migration protection, no blocked reasons, non-empty moves, and no other running job.

- [ ] **Step 6: Implement running action generation**

`NextNodeOnboardingAction(input, job)` must:

- Find `current_move_index` or first pending/running move.
- For pending: skip if assignment already equals `desired_peers_after`; otherwise create `TaskKindRebalance` and updated assignment from `desired_peers_after`.
- For running: wait if assignment equals before; complete if assignment equals after and leader check is satisfied; request explicit leader transfer via action metadata when `leader_transfer_required=true` and leader is not target yet; fail if assignment differs from both.
- Mark job failed when source/target become invalid during running.

- [ ] **Step 7: Run executor tests and commit**

Run: `GOWORK=off go test ./pkg/controller/plane -run 'TestValidateNodeOnboardingStart|TestNextNodeOnboardingAction' -count=1`

Expected: PASS.

Commit:

```bash
git add pkg/controller/plane/onboarding_executor.go pkg/controller/plane/onboarding_executor_test.go
git commit -m "feat: decide node onboarding execution steps"
```

## Task 5: Expose Cluster Onboarding Operations And Controller RPC

**Files:**
- Create: `pkg/cluster/onboarding.go`
- Modify: `pkg/cluster/api.go`
- Modify: `pkg/cluster/codec_control.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/controller_metadata_snapshot.go`
- Modify: `pkg/cluster/controller_host.go`
- Test: `pkg/cluster/onboarding_test.go`
- Test: `pkg/cluster/controller_handler_onboarding_test.go`
- Test: `pkg/cluster/codec_control_test.go`

- [ ] **Step 1: Write failing cluster operation tests**

Add leader-local tests with a test cluster/controller host store proving plan creation persists a planned job and blocked plans are persisted:

```go
func TestCreateNodeOnboardingPlanPersistsPlannedJob(t *testing.T) {
    c := newOnboardingControllerLeaderCluster(t)
    seedOnboardingPlannerState(t, c.controllerMeta)

    job, err := c.CreateNodeOnboardingPlan(context.Background(), 4, "")
    require.NoError(t, err)
    require.Equal(t, controllermeta.OnboardingJobStatusPlanned, job.Status)
    require.NotEmpty(t, job.PlanFingerprint)
    require.NotEmpty(t, job.Moves)
}

func TestStartNodeOnboardingJobRejectsBlockedPlan(t *testing.T) {
    c := newOnboardingControllerLeaderCluster(t)
    job := blockedPlannedJob(t, c.controllerMeta, 4)

    _, err := c.StartNodeOnboardingJob(context.Background(), job.JobID)
    require.ErrorIs(t, err, cluster.ErrOnboardingPlanNotExecutable)
}

func TestStartNodeOnboardingJobMapsRaftRaceToRunningJobExists(t *testing.T) {
    c := newOnboardingControllerLeaderCluster(t)
    job := executablePlannedJob(t, c.controllerMeta, 4)
    require.NoError(t, c.controllerMeta.UpsertOnboardingJob(context.Background(), sampleOnboardingJobWithStatus("other", controllermeta.OnboardingJobStatusRunning)))

    _, err := c.StartNodeOnboardingJob(context.Background(), job.JobID)
    require.ErrorIs(t, err, cluster.ErrOnboardingRunningJobExists)
}
```

- [ ] **Step 2: Write failing codec and handler tests**

Extend `codec_control_test.go` to encode/decode onboarding request/response kinds. Add handler tests for follower redirect and leader success:

```go
func TestControllerHandlerCreateOnboardingPlanRequiresLeader(t *testing.T) {
    h := newFollowerControllerHandler(t)
    body := encodeRequest(t, controllerRPCCreateOnboardingPlan, controllerRPCRequest{OnboardingPlan: &nodeOnboardingPlanRequest{TargetNodeID: 4}})

    respBody, err := h.Handle(context.Background(), body)
    require.NoError(t, err)
    resp := decodeResponse(t, controllerRPCCreateOnboardingPlan, respBody)
    require.True(t, resp.NotLeader)
}
```

- [ ] **Step 3: Run tests and verify red**

Run: `GOWORK=off go test ./pkg/cluster -run 'Test(CreateNodeOnboarding|StartNodeOnboarding|ControllerHandler.*Onboarding|ControllerCodec.*Onboarding)' -count=1 -v`

Expected: FAIL because cluster onboarding APIs and codecs do not exist.

- [ ] **Step 4: Add public API methods and request types**

Add to `pkg/cluster/api.go`:

```go
ListNodeOnboardingCandidates(ctx context.Context) ([]NodeOnboardingCandidate, error)
CreateNodeOnboardingPlan(ctx context.Context, targetNodeID uint64, retryOfJobID string) (controllermeta.NodeOnboardingJob, error)
StartNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error)
ListNodeOnboardingJobs(ctx context.Context, limit int, cursor string) ([]controllermeta.NodeOnboardingJob, string, bool, error)
GetNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error)
RetryNodeOnboardingJob(ctx context.Context, jobID string) (controllermeta.NodeOnboardingJob, error)
```

Implement candidate computation from leader strict nodes/assignments/runtime views: include active data nodes, slot/leader counts, and `Recommended=true` when slot count is below average or zero.

- [ ] **Step 5: Implement leader-local plan/start/list/get/retry methods**

In `onboarding.go`, implement leader-local helpers that build a strict snapshot from controller metadata, `controllerHost` metadata snapshot when clean, observation runtime views, tasks, existing onboarding jobs, and active hash-slot migrations. Set `OnboardingPlanInput.RunningJobExists` from `ListRunningOnboardingJobs`; plan creation must persist a blocked `planned` job with `running_job_exists` when another job is running. Generate deterministic job IDs using UTC timestamp plus a monotonically increasing suffix derived from existing jobs, e.g. `onboard-20260426-000001`. List jobs for manager APIs in `created_at desc, job_id desc` order and encode pagination cursors from the last returned `(created_at, job_id)` pair.

- [ ] **Step 6: Add controller RPC kinds and wire codecs**

Add string kinds:

```go
controllerRPCListOnboardingCandidates = "list_onboarding_candidates"
controllerRPCCreateOnboardingPlan     = "create_onboarding_plan"
controllerRPCStartOnboardingJob       = "start_onboarding_job"
controllerRPCListOnboardingJobs       = "list_onboarding_jobs"
controllerRPCGetOnboardingJob         = "get_onboarding_job"
controllerRPCRetryOnboardingJob       = "retry_onboarding_job"
```

Add request payloads for `target_node_id`, `job_id`, `limit`, `cursor`, and response payloads for candidates, one job, or job page. Use the existing binary codec style, not ad-hoc JSON inside the RPC payload.

- [ ] **Step 7: Implement controller client and handler forwarding**

Extend `controllerAPI`, `controllerClient`, and `controllerHandler`. Handler must redirect when not controller leader and must call leader-local methods only on the leader. For conflicts, return an encoded error code in the onboarding RPC response payload so the client can map to sentinel errors without crashing the RPC layer.

- [ ] **Step 8: Include onboarding jobs in metadata snapshots**

Update `controllerMetadataSnapshot` with `OnboardingJobs []controllermeta.NodeOnboardingJob` and indexes by job ID/status. Update `loadControllerMetadataSnapshot` to read jobs. Update `shouldMarkMetadataSnapshotDirty` and `shouldEnqueueMetadataSnapshotReload` for onboarding commands.

- [ ] **Step 9: Run cluster operation and codec tests, then commit**

Run: `GOWORK=off go test ./pkg/cluster -run 'Test(CreateNodeOnboarding|StartNodeOnboarding|ControllerHandler.*Onboarding|ControllerCodec.*Onboarding)' -count=1`

Expected: PASS.

Commit:

```bash
git add pkg/cluster/onboarding.go pkg/cluster/api.go pkg/cluster/codec_control.go pkg/cluster/controller_client.go pkg/cluster/controller_handler.go pkg/cluster/controller_metadata_snapshot.go pkg/cluster/controller_host.go pkg/cluster/onboarding_test.go pkg/cluster/controller_handler_onboarding_test.go pkg/cluster/codec_control_test.go
git commit -m "feat: expose node onboarding controller operations"
```

## Task 6: Integrate Running Job Executor Into Cluster Tick

**Files:**
- Create: `pkg/cluster/onboarding_executor.go`
- Modify: `pkg/cluster/cluster.go`
- Test: `pkg/cluster/onboarding_test.go`
- Test: `pkg/cluster/cluster_test.go`

- [ ] **Step 1: Write failing tick integration tests**

Add tests proving a running job starts one move, pauses ordinary Rebalance, and fails when source becomes invalid:

```go
func TestControllerTickStartsOneOnboardingMoveAndSkipsOrdinaryRebalance(t *testing.T) {
    c := newOnboardingControllerLeaderCluster(t)
    job := seedRunningOnboardingJobWithPendingMove(t, c.controllerMeta)
    seedRebalanceSkewThatWouldNormallyPlan(t, c.controllerMeta)

    c.controllerTickOnce(context.Background())

    got, err := c.controllerMeta.GetOnboardingJob(context.Background(), job.JobID)
    require.NoError(t, err)
    require.Equal(t, controllermeta.OnboardingMoveStatusRunning, got.Moves[0].Status)
    task, err := c.controllerMeta.GetTask(context.Background(), got.Moves[0].SlotID)
    require.NoError(t, err)
    require.Equal(t, controllermeta.TaskKindRebalance, task.Kind)
}
```

- [ ] **Step 2: Run tests and verify red**

Run: `GOWORK=off go test ./pkg/cluster -run 'TestControllerTick.*Onboarding' -count=1 -v`

Expected: FAIL because `controllerTickOnce` does not inspect onboarding jobs.

- [ ] **Step 3: Implement `advanceNodeOnboardingOnce`**

In `onboarding_executor.go`, implement:

```go
func (c *Cluster) advanceNodeOnboardingOnce(ctx context.Context, state slotcontroller.PlannerState) (running bool, advanced bool)
```

It should load the single running job, call `plane.NextNodeOnboardingAction`, propose `CommandKindNodeOnboardingJobUpdate`, and return `running=true` whenever a running job exists even if no transition was possible.

- [ ] **Step 4: Handle explicit leader transfer**

When the plane action says leader transfer is required, call:

```go
err := c.TransferSlotLeader(ctx, action.Move.SlotID, multiraft.NodeID(action.Move.TargetNodeID))
```

Then refresh runtime view on the next tick before marking completed. If transfer call fails or the refreshed leader is not target within the controller request timeout, mark move/job failed with `last_error`.

- [ ] **Step 5: Update `controllerTickOnce` order**

Before ordinary planner decision:

```go
running, advanced := c.advanceNodeOnboardingOnce(ctx, state)
if advanced {
    return
}
if running {
    state.PauseRebalance = true
    state.LockedSlots = currentOnboardingLockedSlots(...)
}
```

Then run `planner.NextDecision` as today. This preserves Bootstrap/Repair for other slots while disabling opportunistic Rebalance during onboarding.

- [ ] **Step 6: Run cluster tests and commit**

Run: `GOWORK=off go test ./pkg/cluster -run 'TestControllerTick.*Onboarding|TestCreateNodeOnboarding|TestStartNodeOnboarding' -count=1`

Expected: PASS.

Commit:

```bash
git add pkg/cluster/onboarding_executor.go pkg/cluster/cluster.go pkg/cluster/onboarding_test.go pkg/cluster/cluster_test.go
git commit -m "feat: advance node onboarding jobs from controller tick"
```

## Task 7: Add Management Usecase DTOs And Manager HTTP API

**Files:**
- Create: `internal/usecase/management/node_onboarding.go`
- Modify: `internal/usecase/management/app.go`
- Test: `internal/usecase/management/node_onboarding_test.go`
- Create: `internal/access/manager/node_onboarding.go`
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Test: `internal/access/manager/node_onboarding_test.go`

- [ ] **Step 1: Write failing management usecase tests**

Add fake cluster tests for candidates, plan/start/retry delegation, DTO result counts, and stale/blocked errors:

```go
func TestManagementCreatesNodeOnboardingPlan(t *testing.T) {
    fake := &fakeNodeOnboardingCluster{plannedJob: sampleManagementOnboardingJob()}
    app := management.New(management.Options{Cluster: fake})

    got, err := app.CreateNodeOnboardingPlan(context.Background(), management.CreateNodeOnboardingPlanRequest{TargetNodeID: 4})
    require.NoError(t, err)
    require.Equal(t, "planned", got.Job.Status)
    require.Equal(t, 1, got.Job.ResultCounts.Pending)
    require.True(t, fake.createPlanCalled)
}
```

- [ ] **Step 2: Write failing manager HTTP tests**

Add route tests for permissions and error mapping:

```go
func TestManagerNodeOnboardingPlanRoute(t *testing.T) {
    srv := newManagerServerWithOnboardingFake(t)
    reqBody := strings.NewReader(`{"target_node_id":4}`)
    rr := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/manager/node-onboarding/plan", reqBody)
    req.Header.Set("Content-Type", "application/json")

    srv.Engine().ServeHTTP(rr, req)

    require.Equal(t, http.StatusOK, rr.Code)
    require.Contains(t, rr.Body.String(), `"status":"planned"`)
}

func TestManagerNodeOnboardingStartMapsPlanStale(t *testing.T) {
    srv := newManagerServerWithOnboardingError(t, cluster.ErrOnboardingPlanStale)
    rr := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodPost, "/manager/node-onboarding/jobs/job-1/start", nil)

    srv.Engine().ServeHTTP(rr, req)

    require.Equal(t, http.StatusConflict, rr.Code)
    require.Contains(t, rr.Body.String(), `"error":"plan_stale"`)
}
```

- [ ] **Step 3: Run tests and verify red**

Run: `GOWORK=off go test ./internal/usecase/management ./internal/access/manager -run 'Test.*NodeOnboarding' -count=1 -v`

Expected: FAIL because usecase methods/routes do not exist.

- [ ] **Step 4: Add management DTOs and interface methods**

Extend `ClusterReader` with the cluster onboarding API methods and update all existing management test fakes that implement `ClusterReader` so unrelated tests still compile. Add usecase methods:

```go
func (a *App) ListNodeOnboardingCandidates(ctx context.Context) (NodeOnboardingCandidatesResponse, error)
func (a *App) CreateNodeOnboardingPlan(ctx context.Context, req CreateNodeOnboardingPlanRequest) (NodeOnboardingJobResponse, error)
func (a *App) StartNodeOnboardingJob(ctx context.Context, jobID string) (NodeOnboardingJobResponse, error)
func (a *App) ListNodeOnboardingJobs(ctx context.Context, req ListNodeOnboardingJobsRequest) (NodeOnboardingJobsResponse, error)
func (a *App) GetNodeOnboardingJob(ctx context.Context, jobID string) (NodeOnboardingJobResponse, error)
func (a *App) RetryNodeOnboardingJob(ctx context.Context, jobID string) (NodeOnboardingJobResponse, error)
```

`ListNodeOnboardingJobs` must preserve the public ordering from the cluster layer: `created_at desc, job_id desc`, with the opaque cursor passed through unchanged.

- [ ] **Step 5: Add HTTP handlers and permission groups**

Routes:

```go
read := s.engine.Group("/manager/node-onboarding") // cluster.slot:r and candidates also cluster.node:r
write := s.engine.Group("/manager/node-onboarding") // cluster.slot:w
read.GET("/candidates", s.handleNodeOnboardingCandidates)
write.POST("/plan", s.handleNodeOnboardingPlan)
write.POST("/jobs/:job_id/start", s.handleNodeOnboardingStart)
read.GET("/jobs", s.handleNodeOnboardingJobs)
read.GET("/jobs/:job_id", s.handleNodeOnboardingJob)
write.POST("/jobs/:job_id/retry", s.handleNodeOnboardingRetry)
```

For candidates requiring both `cluster.node:r` and `cluster.slot:r`, either chain both permission middlewares or add a small helper that requires all listed permissions.

Map errors exactly:

```text
400 invalid_request
404 not_found
409 running_job_exists | plan_not_executable | plan_stale | invalid_job_state
503 service_unavailable
```

- [ ] **Step 6: Run management/manager tests and commit**

Run: `GOWORK=off go test ./internal/usecase/management ./internal/access/manager -run 'Test.*NodeOnboarding' -count=1`

Expected: PASS.

Commit:

```bash
git add internal/usecase/management/node_onboarding.go internal/usecase/management/app.go internal/usecase/management/node_onboarding_test.go internal/access/manager/node_onboarding.go internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/node_onboarding_test.go
git commit -m "feat: add manager node onboarding API"
```

## Task 8: Build The Web Onboarding Wizard

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Create: `web/src/pages/onboarding/page.tsx`
- Create: `web/src/pages/onboarding/page.test.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`

- [ ] **Step 1: Write failing web API type tests if an API test file exists; otherwise start with page tests**

Add page tests with mocked manager API functions:

```tsx
test("generates a plan, starts the same planned job, and polls to completion", async () => {
  getNodeOnboardingCandidatesMock.mockResolvedValueOnce({ items: [candidateNode4] })
  createNodeOnboardingPlanMock.mockResolvedValueOnce({ job: plannedJob })
  startNodeOnboardingJobMock.mockResolvedValueOnce({ job: runningJob })
  getNodeOnboardingJobMock.mockResolvedValueOnce({ job: completedJob })

  const user = userEvent.setup()
  renderOnboardingPage()

  await user.click(await screen.findByRole("button", { name: /select node 4/i }))
  await user.click(screen.getByRole("button", { name: /generate plan/i }))
  expect(await screen.findByText(/Slot 2/i)).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: /confirm execution/i }))
  expect(startNodeOnboardingJobMock).toHaveBeenCalledWith("onboard-20260426-000001")
  expect(await screen.findByText(/completed/i)).toBeInTheDocument()
})
```

Add tests for blocked plan cannot start, `plan_stale` prompts regeneration, failed job retry creates a new planned job, and Chinese blocked reason text renders.

- [ ] **Step 2: Run tests and verify red**

Run from `web`: `yarn test src/pages/onboarding/page.test.tsx --runInBand`

If Vitest rejects `--runInBand`, run: `yarn test src/pages/onboarding/page.test.tsx`

Expected: FAIL because page/API functions do not exist.

- [ ] **Step 3: Add TypeScript API types and functions**

Add types for candidates, blocked reasons, plan summary, plan moves, jobs, move statuses, result counts, and inputs. Add functions:

```ts
export function getNodeOnboardingCandidates()
export function createNodeOnboardingPlan(targetNodeId: number)
export function startNodeOnboardingJob(jobId: string)
export function getNodeOnboardingJobs(params?: { limit?: number; cursor?: string })
export function getNodeOnboardingJob(jobId: string)
export function retryNodeOnboardingJob(jobId: string)
```

- [ ] **Step 4: Add route, navigation, and i18n**

Use a non-purple visual direction that matches the existing monochrome admin shell. Add `/onboarding` route and Runtime group navigation label “扩容” / “Onboarding”. Include stable i18n messages for every blocked reason code.

- [ ] **Step 5: Implement wizard page**

Page behavior:

1. Load candidates on mount.
2. Select candidate node.
3. Generate plan, which creates and stores a `planned` job.
4. Disable start if `moves.length === 0` or `blocked_reasons.length > 0`.
5. Start only the stored `job_id`; never regenerate silently on start.
6. Poll `GET /manager/node-onboarding/jobs/:job_id` every 2 seconds while status is `running`.
7. Stop polling on unmount, completed, failed, or planned.
8. Retry failed job by calling retry endpoint, displaying the returned new `planned` job, and requiring explicit confirmation again.

- [ ] **Step 6: Run web tests and build**

Run from `web`:

```bash
yarn test src/pages/onboarding/page.test.tsx
yarn test
yarn build
```

Expected: PASS.

Commit:

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/navigation.ts web/src/app/router.tsx web/src/i18n/messages/zh-CN.ts web/src/i18n/messages/en.ts web/src/pages/onboarding/page.tsx web/src/pages/onboarding/page.test.tsx web/src/pages/page-shells.test.tsx
git commit -m "feat: add node onboarding wizard"
```

## Task 9: Add Black-Box E2E Coverage For Dynamic Join Onboarding

**Files:**
- Modify: `test/e2e/suite/manager_client.go`
- Create: `test/e2e/cluster/dynamic_node_join/onboarding_resource_allocation_test.go`

- [ ] **Step 1: Write failing e2e manager client unit tests if helper tests exist**

Extend `test/e2e/suite/manager_client_test.go` with decoding tests for onboarding job payloads:

```go
func TestDecodeNodeOnboardingJobResponse(t *testing.T) {
    body := []byte(`{"job":{"job_id":"job-1","target_node_id":4,"status":"planned","result_counts":{"pending":1}}}`)
    got, err := decodeNodeOnboardingJobResponse(body)
    require.NoError(t, err)
    require.Equal(t, "job-1", got.Job.JobID)
    require.Equal(t, 1, got.Job.ResultCounts.Pending)
}
```

- [ ] **Step 2: Add e2e helper methods**

In `manager_client.go`, add `postHTTPBody` and helpers:

```go
func CreateNodeOnboardingPlan(ctx context.Context, node StartedNode, targetNodeID uint64) (ManagerNodeOnboardingJobResponse, []byte, error)
func StartNodeOnboardingJob(ctx context.Context, node StartedNode, jobID string) (ManagerNodeOnboardingJobResponse, []byte, error)
func FetchNodeOnboardingJob(ctx context.Context, node StartedNode, jobID string) (ManagerNodeOnboardingJobResponse, []byte, error)
func WaitForNodeOnboardingJob(ctx context.Context, node StartedNode, jobID string, accept func(ManagerNodeOnboardingJob) bool) (ManagerNodeOnboardingJob, []byte, error)
```

- [ ] **Step 3: Write failing e2e scenario**

Create `TestDynamicNodeJoinOnboardingResourceAllocation`:

```go
func TestDynamicNodeJoinOnboardingResourceAllocation(t *testing.T) {
    const joinToken = "join-secret"
    s := suite.New(t)
    cluster := s.StartThreeNodeCluster(
        suite.WithNodeConfigOverrides(1, map[string]string{"WK_CLUSTER_JOIN_TOKEN": joinToken}),
        suite.WithNodeConfigOverrides(2, map[string]string{"WK_CLUSTER_JOIN_TOKEN": joinToken}),
        suite.WithNodeConfigOverrides(3, map[string]string{"WK_CLUSTER_JOIN_TOKEN": joinToken}),
    )

    ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
    defer cancel()
    require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

    joined := s.StartDynamicJoinNode(cluster, 4, joinToken)
    require.NoError(t, suite.WaitNodeReady(ctx, *joined), cluster.DumpDiagnostics())
    _, _, err := suite.WaitForManagerNode(ctx, *cluster.MustNode(1), 4, func(node suite.ManagerNode) bool {
        return node.Status == "alive" && node.SlotStats.Count == 0
    })
    require.NoError(t, err, cluster.DumpDiagnostics())

    planned, body, err := suite.CreateNodeOnboardingPlan(ctx, *cluster.MustNode(1), 4)
    require.NoError(t, err, string(body))
    require.Equal(t, "planned", planned.Job.Status)
    require.NotEmpty(t, planned.Job.Moves)

    started, body, err := suite.StartNodeOnboardingJob(ctx, *cluster.MustNode(1), planned.Job.JobID)
    require.NoError(t, err, string(body))
    require.Equal(t, "running", started.Job.Status)

    completed, body, err := suite.WaitForNodeOnboardingJob(ctx, *cluster.MustNode(1), planned.Job.JobID, func(job suite.ManagerNodeOnboardingJob) bool {
        return job.Status == "completed"
    })
    require.NoError(t, err, managerDiagnostics(cluster, body))
    require.Greater(t, completed.ResultCounts.Completed+completed.ResultCounts.Skipped, 0)

    node4, body, err := suite.WaitForManagerNode(ctx, *cluster.MustNode(1), 4, func(node suite.ManagerNode) bool {
        return node.SlotStats.Count > 0
    })
    require.NoError(t, err, managerDiagnostics(cluster, body))
    require.Greater(t, node4.SlotStats.Count, 0)

    sendDynamicJoinSmokeMessages(t, cluster, *joined)
}
```

Reuse the existing message send helper pattern from `dynamic_node_join_test.go`.

- [ ] **Step 4: Run the focused e2e and verify red before backend is complete**

Run: `GOWORK=off go test -tags=e2e ./test/e2e/cluster/dynamic_node_join -run TestDynamicNodeJoinOnboardingResourceAllocation -count=1 -v`

Expected before implementation is wired: FAIL because `/manager/node-onboarding/plan` returns 404 or job does not complete.

- [ ] **Step 5: Run the e2e after implementation is complete**

Run: `GOWORK=off go test -tags=e2e ./test/e2e/cluster/dynamic_node_join -run TestDynamicNodeJoinOnboardingResourceAllocation -count=1 -v`

Expected: PASS within the 120 second test timeout.

Commit:

```bash
git add test/e2e/suite/manager_client.go test/e2e/suite/manager_client_test.go test/e2e/cluster/dynamic_node_join/onboarding_resource_allocation_test.go
git commit -m "test: cover node onboarding resource allocation e2e"
```

## Task 10: Update Flow Docs, Project Knowledge, And Run Full Relevant Verification

**Files:**
- Modify: `pkg/controller/FLOW.md`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Optional careful modify: `internal/FLOW.md` only if implementation changed the documented internal manager/usecase flow and after preserving pre-existing user edits.

- [ ] **Step 1: Update `pkg/controller/FLOW.md`**

Document:

- New onboarding job record prefix `o`.
- New `NodeOnboardingJobUpdate` command.
- Targeted onboarding planner vs ordinary planner.
- Tick coordination: running onboarding job advances before ordinary planner and pauses automatic Rebalance.

- [ ] **Step 2: Update `pkg/cluster/FLOW.md`**

Document:

- Cluster API methods for onboarding.
- Controller RPC forwarding for onboarding operations.
- `controllerTickOnce` ordering and leader-local execution.
- Relationship with existing Slot Rebalance and hash-slot migration.

- [ ] **Step 3: Update project knowledge briefly**

Append one concise bullet to `docs/development/PROJECT_KNOWLEDGE.md`:

```md
- Active dynamic-join data nodes do not automatically receive Slot replicas; operators use the manager Node Onboarding job to persist, review, start, and audit Slot replica/Leader allocation.
```

- [ ] **Step 4: Run Go unit test verification**

Run:

```bash
GOWORK=off go test ./pkg/controller/meta ./pkg/controller/plane ./pkg/cluster ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 5: Run web verification**

Run from `web`:

```bash
yarn test
yarn build
```

Expected: PASS.

- [ ] **Step 6: Run focused e2e verification**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/cluster/dynamic_node_join -run 'TestDynamicNodeJoin(OnboardingResourceAllocation)?$' -count=1 -v
```

Expected: PASS for the original dynamic join test and the new onboarding resource allocation test.

- [ ] **Step 7: Commit docs and verification fixes**

Commit:

```bash
git add pkg/controller/FLOW.md pkg/cluster/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: describe node onboarding allocation flow"
```

## Final Acceptance Criteria

- `POST /manager/node-onboarding/plan` creates a persisted `planned` job using only controller leader strict state.
- Blocked-but-valid target states create a planned job with `blocked_reasons`; invalid body and missing target do not create jobs.
- `POST /manager/node-onboarding/jobs/:job_id/start` starts only the reviewed job, rejects stale or blocked plans, and never silently regenerates a plan.
- Controller leader executes at most one running onboarding job, one move at a time, through existing `TaskKindRebalance` slot migration steps.
- New target node receives Slot replicas up to the target balanced load when safe candidates exist.
- Moves marked `leader_transfer_required=true` explicitly transfer and verify Slot Leader on the target.
- Ordinary automatic Rebalance is paused while a job is running; Bootstrap/Repair still win safety conflicts.
- Failed jobs stop without rollback; retry creates a new planned job linked by `retry_of_job_id`.
- Web `/onboarding` supports candidate selection, plan review, explicit start, polling progress, failure display, and retry-confirmation flow.
- Focused Go tests, web tests/build, and the new e2e pass.
