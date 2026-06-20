# Slot Leader Transfer Batch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add backend v0 for manual batch Slot leader-transfer planning and execution on top of the existing ControllerV2 single-Slot `leader_transfer` task path.

**Architecture:** Keep ControllerV2 unchanged. Add a focused planner in `internalv2/usecase/management` that reads the local control snapshot plus live Slot runtime status, returns deterministic candidate/skip rows and `plan_id`, and executes by re-running the planner then submitting ordinary single-Slot `leader_transfer` task intents through the existing `SlotLeaderTransferWriter`. Batch execution preserves the operator-selected `SourceNodeID` in task requests so PreferredLeader correction can complete when Raft has already elected a legal non-source leader. Add manager HTTP routes that only parse JSON, enforce slot read/write permissions, map errors, and marshal DTOs.

**Tech Stack:** Go, internalv2 manager usecase, gin manager HTTP server, clusterv2 control snapshots, existing ControllerV2 task writer, unit tests with `go test`.

---

## Scope

This plan implements the backend v0 slice only:

- `POST /manager/slots/leader-transfer-plan`
- `POST /manager/slots/leader-transfer-batch`
- usecase planner and executor
- manager HTTP tests and FLOW updates

This plan deliberately does not implement a web UI, automatic node-failure triggers, durable batch history, or new ControllerV2 command kinds. Those are separate follow-up slices after the backend contract is stable.

## File Structure

- Create `internalv2/usecase/management/slot_leader_transfer_batch.go`
  - Owns batch request/response DTOs, skip/status constants, deterministic plan ID generation, candidate selection, and batch execution.
- Create `internalv2/usecase/management/slot_leader_transfer_batch_test.go`
  - Usecase TDD coverage for planning, target selection, stale-plan rejection, digest mismatch, and writer reuse.
- Modify `internalv2/usecase/management/slot_leader_transfer.go`
  - Keep existing single-Slot behavior; only share small helpers if needed.
- Modify `internalv2/usecase/management/FLOW.md`
  - Document the batch plan/execute flow next to the existing single-Slot flow.
- Create `internalv2/access/manager/slot_leader_transfer_batch.go`
  - Owns manager HTTP request/response DTOs and handlers for the two new routes.
- Create `internalv2/access/manager/slot_leader_transfer_batch_test.go`
  - HTTP TDD coverage for route parsing, status codes, and permissions.
- Modify `internalv2/access/manager/server.go`
  - Extend the `Management` interface and register read/write routes in the existing slot permission groups.
- Modify `internalv2/access/manager/server_test.go`
  - Extend `managerNodesStub` with plan/execute sinks and responses.
- Modify `internalv2/access/manager/FLOW.md`
  - Add the two backend v0 routes and clarify that HTTP does not call Slot Raft directly.

## Existing Anchors

- Single-Slot usecase: `internalv2/usecase/management/slot_leader_transfer.go`
- Single-Slot HTTP route: `internalv2/access/manager/slot_leader_transfer.go`
- Single-Slot usecase tests: `internalv2/usecase/management/slot_leader_transfer_test.go`
- Single-Slot HTTP tests: `internalv2/access/manager/slot_leader_transfer_test.go`
- Existing route groups: `internalv2/access/manager/server.go`
- Existing active task DTO mapping: `internalv2/usecase/management/slots.go` and `internalv2/access/manager/slots.go`
- Existing executor proof that preferred target is soft: `pkg/clusterv2/tasks/leader_transfer_test.go::TestLeaderTransferExecutorCompletesOnLegalNonTargetLeader`

---

### Task 1: Add Usecase Batch DTOs And Planner Failing Tests

**Files:**
- Create: `internalv2/usecase/management/slot_leader_transfer_batch.go`
- Create: `internalv2/usecase/management/slot_leader_transfer_batch_test.go`

- [ ] **Step 1: Write the failing planner DTO/test file**

Create `internalv2/usecase/management/slot_leader_transfer_batch_test.go` with these first tests:

```go
package management

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestPlanSlotLeaderTransfersSelectsCandidatesAndSkipsInStableOrder(t *testing.T) {
	generatedAt := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	statusReader := &fakeSlotRuntimeStatusReader{
		statuses: map[uint32]SlotRuntimeStatus{
			1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			2: {SlotID: 2, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}},
			3: {SlotID: 3, LeaderID: 1, CurrentVoters: []uint64{1, 3}},
		},
	}
	app := New(Options{
		Cluster:           fakeNodeSnapshotReader{snapshot: batchLeaderTransferSnapshot()},
		SlotRuntimeStatus: statusReader,
		Now:               func() time.Time { return generatedAt },
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		SlotIDs:      []uint32{1, 2, 3},
		MaxTasks:     8,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if !got.GeneratedAt.Equal(generatedAt) || got.StateRevision != 22 || got.SourceNodeID != 1 || got.TargetPolicy != SlotLeaderTransferTargetPolicyLeastLeaders || got.MaxTasks != 8 {
		t.Fatalf("identity = %#v, want generated/revision/source/policy/max", got)
	}
	if got.PlanID == "" {
		t.Fatalf("plan_id empty")
	}
	if got.Summary.Scanned != 3 || got.Summary.Candidates != 1 || got.Summary.Skipped != 2 || got.Summary.WouldCreate != 1 || got.Summary.ExistingTasks != 0 {
		t.Fatalf("summary = %#v, want scanned 3 candidate 1 skipped 2 would_create 1", got.Summary)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want one candidate", got.Candidates)
	}
	candidate := got.Candidates[0]
	if candidate.SlotID != 1 || candidate.SourceNodeID != 1 || candidate.TargetNodeID != 2 || candidate.PreferredLeader != 1 || candidate.ActualLeader != 1 || candidate.ConfigEpoch != 7 || candidate.Action != SlotLeaderTransferBatchActionCreate {
		t.Fatalf("candidate = %#v, want slot 1 source 1 target 2 create", candidate)
	}
	if !sameUint64Slice(candidate.DesiredPeers, []uint64{1, 2, 3}) || !sameUint64Slice(candidate.CurrentVoters, []uint64{1, 2, 3}) {
		t.Fatalf("candidate peers/voters = %#v", candidate)
	}
	if len(got.Skipped) != 2 || got.Skipped[0].SlotID != 2 || got.Skipped[0].Reason != SlotLeaderTransferSkipSourceNotLeaderOrPreferred || got.Skipped[1].SlotID != 3 || got.Skipped[1].Reason != SlotLeaderTransferSkipTargetNotCurrentVoter {
		t.Fatalf("skipped = %#v, want slot 2 source_not_leader_or_preferred then slot 3 target_not_current_voter", got.Skipped)
	}
}

func TestPlanSlotLeaderTransfersCorrectsPreferredLeaderAfterActualMoved(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = []control.SlotAssignment{{
		SlotID:          1,
		DesiredPeers:    []uint64{1, 2, 3},
		ConfigEpoch:     7,
		PreferredLeader: 1,
	}}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 3, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		MaxTasks:     4,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].ActualLeader != 3 || got.Candidates[0].TargetNodeID != 2 {
		t.Fatalf("candidates = %#v, want preferred correction from source 1 to target 2 while actual leader is 3", got.Candidates)
	}
}

func TestPlanSlotLeaderTransfersReportsExistingMatchingTasks(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Tasks = []control.ReconcileTask{batchLeaderTransferTask(1, 1, 2, 7, 22)}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				3: {SlotID: 3, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		MaxTasks:     4,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if got.Summary.ExistingTasks != 1 || got.Summary.WouldCreate != 2 {
		t.Fatalf("summary = %#v, want one existing and two would_create", got.Summary)
	}
	if got.Candidates[0].Action != SlotLeaderTransferBatchActionExisting || got.Candidates[0].ExistingTaskID == "" {
		t.Fatalf("first candidate = %#v, want existing action with task id", got.Candidates[0])
	}
}
```

- [ ] **Step 2: Run the targeted test and verify it fails on missing API**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestPlanSlotLeaderTransfers' -count=1
```

Expected: compile failure mentioning undefined `PlanSlotLeaderTransfers`, `SlotLeaderTransferBatchPlanRequest`, and constants such as `SlotLeaderTransferTargetPolicyLeastLeaders`.

- [ ] **Step 3: Add the DTO shell and constants**

Create `internalv2/usecase/management/slot_leader_transfer_batch.go` with this shell:

```go
package management

import (
	"context"
	"errors"
	"time"
)

const (
	// DefaultSlotLeaderTransferBatchMaxTasks is the default task creation bound.
	DefaultSlotLeaderTransferBatchMaxTasks = 32
	// MaxSlotLeaderTransferBatchMaxTasks is the largest accepted batch creation bound.
	MaxSlotLeaderTransferBatchMaxTasks = 128

	// SlotLeaderTransferTargetPolicyLeastLeaders balances targets by projected leader count.
	SlotLeaderTransferTargetPolicyLeastLeaders = "least_leaders"

	// SlotLeaderTransferBatchActionCreate means execution should create a task.
	SlotLeaderTransferBatchActionCreate = "create"
	// SlotLeaderTransferBatchActionExisting means a matching active task already exists.
	SlotLeaderTransferBatchActionExisting = "existing"
)

const (
	SlotLeaderTransferSkipSlotNotAllowed              = "slot_not_allowed"
	SlotLeaderTransferSkipAssignmentMissing           = "assignment_missing"
	SlotLeaderTransferSkipSinglePeerSlot              = "single_peer_slot"
	SlotLeaderTransferSkipSourceNotDesiredPeer        = "source_not_desired_peer"
	SlotLeaderTransferSkipRuntimeUnavailable          = "runtime_unavailable"
	SlotLeaderTransferSkipLeaderUnknown               = "leader_unknown"
	SlotLeaderTransferSkipSourceNotLeaderOrPreferred  = "source_not_leader_or_preferred"
	SlotLeaderTransferSkipQuorumUnavailable           = "quorum_unavailable"
	SlotLeaderTransferSkipActiveTaskConflict          = "active_task_conflict"
	SlotLeaderTransferSkipMatchingTaskExists          = "matching_task_exists"
	SlotLeaderTransferSkipTargetInvalid               = "target_invalid"
	SlotLeaderTransferSkipTargetNotAliveDataNode      = "target_not_alive_data_node"
	SlotLeaderTransferSkipTargetNotDesiredPeer        = "target_not_desired_peer"
	SlotLeaderTransferSkipTargetNotCurrentVoter       = "target_not_current_voter"
	SlotLeaderTransferSkipAlreadyOnTarget             = "already_on_target"
	SlotLeaderTransferSkipMaxTasksReached             = "max_tasks_reached"
)

var (
	// ErrSlotLeaderTransferPlanStale reports that an execution request used an old state revision.
	ErrSlotLeaderTransferPlanStale = errors.New("internalv2/usecase/management: slot leader transfer plan stale")
	// ErrSlotLeaderTransferPlanMismatch reports that an execution request used a mismatched plan digest.
	ErrSlotLeaderTransferPlanMismatch = errors.New("internalv2/usecase/management: slot leader transfer plan mismatch")
)

// SlotLeaderTransferBatchPlanRequest contains read-only batch planning criteria.
type SlotLeaderTransferBatchPlanRequest struct {
	// SourceNodeID is the node whose Slot leadership should move away.
	SourceNodeID uint64
	// TargetNodeID is an optional explicit preferred target for all candidates.
	TargetNodeID uint64
	// SlotIDs is an optional physical Slot allow-list.
	SlotIDs []uint32
	// MaxTasks bounds candidate task creation in one batch.
	MaxTasks int
	// TargetPolicy selects a target when TargetNodeID is zero.
	TargetPolicy string
}

// SlotLeaderTransferBatchPlanResponse is the read-only plan preview.
type SlotLeaderTransferBatchPlanResponse struct {
	// GeneratedAt records when the plan was assembled.
	GeneratedAt time.Time
	// StateRevision is the Controller state revision used to build the plan.
	StateRevision uint64
	// PlanID is a deterministic digest of request criteria and selected candidates.
	PlanID string
	// SourceNodeID is the requested source node.
	SourceNodeID uint64
	// TargetPolicy is the normalized target policy.
	TargetPolicy string
	// MaxTasks is the normalized task bound.
	MaxTasks int
	// Summary contains aggregate plan counts.
	Summary SlotLeaderTransferBatchPlanSummary
	// Candidates contains ordered task candidates and matching existing tasks.
	Candidates []SlotLeaderTransferBatchCandidate
	// Skipped contains ordered non-candidate Slots with stable reasons.
	Skipped []SlotLeaderTransferBatchSkip
}

// SlotLeaderTransferBatchPlanSummary contains aggregate plan counts.
type SlotLeaderTransferBatchPlanSummary struct {
	Scanned       int
	Candidates   int
	Skipped      int
	ExistingTasks int
	WouldCreate  int
}

// SlotLeaderTransferBatchCandidate is one planned Slot transfer row.
type SlotLeaderTransferBatchCandidate struct {
	SlotID          uint32
	SourceNodeID    uint64
	TargetNodeID    uint64
	PreferredLeader uint64
	ActualLeader    uint64
	DesiredPeers    []uint64
	CurrentVoters   []uint64
	ConfigEpoch     uint64
	ExistingTaskID  string
	Action          string
}

// SlotLeaderTransferBatchSkip explains why one Slot was not a candidate.
type SlotLeaderTransferBatchSkip struct {
	SlotID  uint32
	Reason  string
	Message string
}

func (a *App) PlanSlotLeaderTransfers(ctx context.Context, req SlotLeaderTransferBatchPlanRequest) (SlotLeaderTransferBatchPlanResponse, error) {
	return SlotLeaderTransferBatchPlanResponse{}, nil
}
```

- [ ] **Step 4: Run the targeted test and verify the failure moves to behavior**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestPlanSlotLeaderTransfers' -count=1
```

Expected: compile succeeds, tests fail on empty plan fields such as `plan_id`, `summary`, or `candidates`.

### Task 2: Implement Planner Selection, Skip Reasons, And Deterministic Plan ID

**Files:**
- Modify: `internalv2/usecase/management/slot_leader_transfer_batch.go`
- Modify: `internalv2/usecase/management/slot_leader_transfer_batch_test.go`

- [ ] **Step 1: Add the shared test fixtures**

Append these helpers to `internalv2/usecase/management/slot_leader_transfer_batch_test.go`:

```go
func batchLeaderTransferSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 22,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "n1", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "n2", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "n3", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
			{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, PreferredLeader: 2},
			{SlotID: 3, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 9, PreferredLeader: 1},
		},
	}
}

func batchLeaderTransferTask(slotID uint32, source, target, epoch, revision uint64) control.ReconcileTask {
	return control.ReconcileTask{
		TaskID:           "slot-" + strconv.FormatUint(uint64(slotID), 10) + "-leader-transfer-" + strconv.FormatUint(epoch, 10) + "-r" + strconv.FormatUint(revision, 10),
		SlotID:           slotID,
		Kind:             control.TaskKindLeaderTransfer,
		Step:             control.TaskStepTransferLeader,
		SourceNode:       source,
		TargetNode:       target,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: control.TaskCompletionPolicySingleObserver,
		ConfigEpoch:      epoch,
		Status:           control.TaskStatusPending,
	}
}
```

Add `strconv` to the test imports.

- [ ] **Step 2: Add validation and stable planning helpers**

Replace the stub in `slot_leader_transfer_batch.go` with these helper signatures and the described behavior:

```go
func (a *App) PlanSlotLeaderTransfers(ctx context.Context, req SlotLeaderTransferBatchPlanRequest) (SlotLeaderTransferBatchPlanResponse, error) {
	plan, err := a.planSlotLeaderTransfers(ctx, req)
	if err != nil {
		return SlotLeaderTransferBatchPlanResponse{}, err
	}
	return plan.response, nil
}

type slotLeaderTransferPlan struct {
	request  SlotLeaderTransferBatchPlanRequest
	response SlotLeaderTransferBatchPlanResponse
}

func (a *App) planSlotLeaderTransfers(ctx context.Context, req SlotLeaderTransferBatchPlanRequest) (slotLeaderTransferPlan, error)
func normalizeSlotLeaderTransferBatchPlanRequest(req SlotLeaderTransferBatchPlanRequest) (SlotLeaderTransferBatchPlanRequest, error)
func slotAllowSet(slotIDs []uint32) map[uint32]struct{}
func activeTaskBySlot(tasks []control.ReconcileTask) map[uint32]control.ReconcileTask
func aliveDataNodes(snapshot control.Snapshot) map[uint64]control.Node
func projectedLeaderCounts(statuses map[uint32]SlotRuntimeStatus) map[uint64]int
func selectSlotLeaderTransferTarget(req SlotLeaderTransferBatchPlanRequest, assignment control.SlotAssignment, runtime SlotRuntimeStatus, aliveData map[uint64]control.Node, projected map[uint64]int) (uint64, string)
func appendSlotLeaderTransferSkip(out []SlotLeaderTransferBatchSkip, slotID uint32, reason string) []SlotLeaderTransferBatchSkip
func slotLeaderTransferPlanID(req SlotLeaderTransferBatchPlanRequest, revision uint64, candidates []SlotLeaderTransferBatchCandidate) string
```

Implementation rules:

- `normalizeSlotLeaderTransferBatchPlanRequest` rejects zero `SourceNodeID`.
- `MaxTasks == 0` becomes `DefaultSlotLeaderTransferBatchMaxTasks`.
- `MaxTasks < 0` or `MaxTasks > MaxSlotLeaderTransferBatchMaxTasks` returns `metadb.ErrInvalidArgument`.
- Empty `TargetPolicy` becomes `least_leaders`.
- Any non-`least_leaders` policy returns `metadb.ErrInvalidArgument`.
- Sort and de-duplicate `SlotIDs`; zero slot IDs return `metadb.ErrInvalidArgument`.
- `planSlotLeaderTransfers` returns `ErrSlotLeaderTransferUnavailable` when `a == nil` or `a.cluster == nil`.
- `planSlotLeaderTransfers` returns `ErrSlotRuntimeStatusUnavailable` when `a.slotRuntimeStatus == nil`.
- Scan `snapshot.Slots` sorted by `SlotID`.
- `Summary.Scanned` counts every sorted assignment considered after the allow-list filter.
- For skipped allow-list IDs that do not exist in assignments, append `assignment_missing` rows after assignment scan, sorted by slot ID.
- A candidate with matching active `leader_transfer` task for the same slot/source/target/epoch/peers is action `existing`.
- A slot with a different active task for the same slot is skipped as `active_task_conflict`.
- Once `WouldCreate` reaches `MaxTasks`, additional create-worthy slots are skipped as `max_tasks_reached`; existing matching tasks still appear as candidates and count as `ExistingTasks`.
- `PlanID` is SHA-256 over normalized request, revision, ordered candidate slot IDs, target nodes, action, existing task ID, and config epochs. Prefix with `slot-leader-transfer:<revision>:` plus the first 16 hex bytes.

- [ ] **Step 3: Run the planner tests**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestPlanSlotLeaderTransfers' -count=1
```

Expected: planner tests pass.

- [ ] **Step 4: Add focused target-policy tests**

Append these tests:

```go
func TestPlanSlotLeaderTransfersLeastLeadersBalancesProjectedCounts(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = []control.SlotAssignment{
		{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
		{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, PreferredLeader: 1},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		MaxTasks:     8,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})

	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}
	if len(got.Candidates) != 2 || got.Candidates[0].TargetNodeID != 2 || got.Candidates[1].TargetNodeID != 3 {
		t.Fatalf("targets = %#v, want balanced targets 2 then 3", got.Candidates)
	}
}

func TestPlanSlotLeaderTransfersValidatesRequestAndUnavailablePorts(t *testing.T) {
	app := New(Options{})
	if _, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("zero source error = %v, want invalid argument", err)
	}
	if _, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1, MaxTasks: MaxSlotLeaderTransferBatchMaxTasks + 1}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("too large max_tasks error = %v, want invalid argument", err)
	}
	if _, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1, TargetPolicy: "unknown"}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("unknown policy error = %v, want invalid argument", err)
	}
	if _, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1}); !errors.Is(err, ErrSlotLeaderTransferUnavailable) {
		t.Fatalf("missing cluster error = %v, want unavailable", err)
	}
	app = New(Options{Cluster: fakeNodeSnapshotReader{snapshot: batchLeaderTransferSnapshot()}})
	if _, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{SourceNodeID: 1}); !errors.Is(err, ErrSlotRuntimeStatusUnavailable) {
		t.Fatalf("missing runtime error = %v, want runtime unavailable", err)
	}
}
```

Add `errors` and `metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"` to the test imports.

- [ ] **Step 5: Run the full management usecase package tests**

Run:

```bash
go test ./internalv2/usecase/management -count=1
```

Expected: PASS.

### Task 3: Add Batch Execute Usecase And Reuse Single-Slot Writer

**Files:**
- Modify: `internalv2/usecase/management/slot_leader_transfer_batch.go`
- Modify: `internalv2/usecase/management/slot_leader_transfer_batch_test.go`

- [ ] **Step 1: Write failing execute tests**

Append these tests:

```go
func TestExecuteSlotLeaderTransferBatchRejectsStaleRevisionBeforeWrites(t *testing.T) {
	writer := &fakeSlotLeaderTransferWriter{}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: batchLeaderTransferSnapshot()},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}}},
		},
		LeaderTransfer: writer,
	})

	_, err := app.ExecuteSlotLeaderTransferBatch(context.Background(), SlotLeaderTransferBatchExecuteRequest{
		SourceNodeID:  1,
		TargetNodeID:  2,
		MaxTasks:      8,
		TargetPolicy:  SlotLeaderTransferTargetPolicyLeastLeaders,
		StateRevision: 21,
		PlanID:        "slot-leader-transfer:21:stale",
	})

	if !errors.Is(err, ErrSlotLeaderTransferPlanStale) {
		t.Fatalf("ExecuteSlotLeaderTransferBatch() error = %v, want stale", err)
	}
	if len(writer.requests) != 0 {
		t.Fatalf("writer requests = %#v, want no writes for stale plan", writer.requests)
	}
}

func TestExecuteSlotLeaderTransferBatchRejectsPlanMismatchBeforeWrites(t *testing.T) {
	writer := &fakeSlotLeaderTransferWriter{}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: batchLeaderTransferSnapshot()},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}}},
		},
		LeaderTransfer: writer,
	})
	plan, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		MaxTasks:     8,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})
	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}

	_, err = app.ExecuteSlotLeaderTransferBatch(context.Background(), SlotLeaderTransferBatchExecuteRequest{
		SourceNodeID:  1,
		TargetNodeID:  2,
		MaxTasks:      8,
		TargetPolicy:  SlotLeaderTransferTargetPolicyLeastLeaders,
		StateRevision: plan.StateRevision,
		PlanID:        plan.PlanID + "-wrong",
	})

	if !errors.Is(err, ErrSlotLeaderTransferPlanMismatch) {
		t.Fatalf("ExecuteSlotLeaderTransferBatch() error = %v, want plan mismatch", err)
	}
	if len(writer.requests) != 0 {
		t.Fatalf("writer requests = %#v, want no writes for mismatch", writer.requests)
	}
}

func TestExecuteSlotLeaderTransferBatchCreatesAndReportsPerSlotResults(t *testing.T) {
	writer := &fakeSlotLeaderTransferWriter{
		result: control.SlotLeaderTransferResult{
			Created: true,
			Task:    &control.ReconcileTask{TaskID: "created-transfer", Kind: control.TaskKindLeaderTransfer, Status: control.TaskStatusPending},
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: batchLeaderTransferSnapshot()},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
				3: {SlotID: 3, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
		LeaderTransfer: writer,
	})
	plan, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		MaxTasks:     2,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})
	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}

	got, err := app.ExecuteSlotLeaderTransferBatch(context.Background(), SlotLeaderTransferBatchExecuteRequest{
		SourceNodeID:  1,
		TargetNodeID:  2,
		MaxTasks:      2,
		TargetPolicy:  SlotLeaderTransferTargetPolicyLeastLeaders,
		StateRevision: plan.StateRevision,
		PlanID:        plan.PlanID,
	})

	if err != nil {
		t.Fatalf("ExecuteSlotLeaderTransferBatch() error = %v", err)
	}
	if got.Summary.Requested != 2 || got.Summary.Created != 2 || got.Summary.Failed != 0 {
		t.Fatalf("summary = %#v, want two created", got.Summary)
	}
	if len(writer.requests) != 2 || writer.requests[0].SlotID != 1 || writer.requests[1].SlotID != 2 {
		t.Fatalf("writer requests = %#v, want stable slot order 1 then 2", writer.requests)
	}
	if len(got.Results) != 2 || got.Results[0].Status != SlotLeaderTransferBatchResultCreated || got.Results[0].TaskID == "" {
		t.Fatalf("results = %#v, want created rows with task ids", got.Results)
	}
}

func TestExecuteSlotLeaderTransferBatchUsesRequestedSourceForPreferredCorrection(t *testing.T) {
	snapshot := batchLeaderTransferSnapshot()
	snapshot.Slots = []control.SlotAssignment{{
		SlotID:          1,
		DesiredPeers:    []uint64{1, 2, 3},
		ConfigEpoch:     7,
		PreferredLeader: 1,
	}}
	writer := &fakeSlotLeaderTransferWriter{
		result: control.SlotLeaderTransferResult{
			Created: true,
			Task:    &control.ReconcileTask{TaskID: "preferred-correction", Kind: control.TaskKindLeaderTransfer, Status: control.TaskStatusPending},
		},
	}
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{1: {SlotID: 1, LeaderID: 3, CurrentVoters: []uint64{1, 2, 3}}},
		},
		LeaderTransfer: writer,
	})
	plan, err := app.PlanSlotLeaderTransfers(context.Background(), SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		MaxTasks:     4,
		TargetPolicy: SlotLeaderTransferTargetPolicyLeastLeaders,
	})
	if err != nil {
		t.Fatalf("PlanSlotLeaderTransfers() error = %v", err)
	}

	_, err = app.ExecuteSlotLeaderTransferBatch(context.Background(), SlotLeaderTransferBatchExecuteRequest{
		SourceNodeID:  1,
		TargetNodeID:  2,
		MaxTasks:      4,
		TargetPolicy:  SlotLeaderTransferTargetPolicyLeastLeaders,
		StateRevision: plan.StateRevision,
		PlanID:        plan.PlanID,
	})

	if err != nil {
		t.Fatalf("ExecuteSlotLeaderTransferBatch() error = %v", err)
	}
	if len(writer.requests) != 1 || writer.requests[0].SourceNode != 1 || writer.requests[0].TargetNode != 2 {
		t.Fatalf("writer requests = %#v, want source preserved as requested source 1 and target 2", writer.requests)
	}
}
```

- [ ] **Step 2: Run execute tests and verify missing API failure**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestExecuteSlotLeaderTransferBatch' -count=1
```

Expected: compile failure mentioning undefined `ExecuteSlotLeaderTransferBatch`, `SlotLeaderTransferBatchExecuteRequest`, and result constants.

- [ ] **Step 3: Add execute DTOs and constants**

Add these definitions to `slot_leader_transfer_batch.go`:

```go
const (
	// SlotLeaderTransferBatchResultCreated reports a new task write.
	SlotLeaderTransferBatchResultCreated = "created"
	// SlotLeaderTransferBatchResultExisting reports an already active matching task.
	SlotLeaderTransferBatchResultExisting = "existing"
	// SlotLeaderTransferBatchResultAlreadyLeader reports a single-Slot no-op.
	SlotLeaderTransferBatchResultAlreadyLeader = "already_leader"
	// SlotLeaderTransferBatchResultFailed reports a per-slot write failure after global fencing passed.
	SlotLeaderTransferBatchResultFailed = "failed"
)

// SlotLeaderTransferBatchExecuteRequest contains fenced batch execution criteria.
type SlotLeaderTransferBatchExecuteRequest struct {
	SourceNodeID  uint64
	TargetNodeID  uint64
	SlotIDs       []uint32
	MaxTasks      int
	TargetPolicy  string
	StateRevision uint64
	PlanID        string
}

// SlotLeaderTransferBatchExecuteResponse reports accepted and skipped execution rows.
type SlotLeaderTransferBatchExecuteResponse struct {
	GeneratedAt   time.Time
	StateRevision uint64
	PlanID        string
	Summary       SlotLeaderTransferBatchExecuteSummary
	Results       []SlotLeaderTransferBatchExecuteResult
}

// SlotLeaderTransferBatchExecuteSummary contains aggregate execution counts.
type SlotLeaderTransferBatchExecuteSummary struct {
	Requested     int
	Created       int
	Existing      int
	AlreadyLeader int
	Skipped       int
	Failed        int
}

// SlotLeaderTransferBatchExecuteResult is one per-candidate execution result.
type SlotLeaderTransferBatchExecuteResult struct {
	SlotID       uint32
	TargetNodeID uint64
	Status       string
	TaskID       string
	Message      string
}
```

- [ ] **Step 4: Implement execute by re-running the planner and submitting ordinary single-Slot task intents**

Add `ExecuteSlotLeaderTransferBatch`:

```go
func (a *App) ExecuteSlotLeaderTransferBatch(ctx context.Context, req SlotLeaderTransferBatchExecuteRequest) (SlotLeaderTransferBatchExecuteResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLeaderTransferBatchExecuteResponse{}, err
	}
	if req.StateRevision == 0 || req.PlanID == "" {
		return SlotLeaderTransferBatchExecuteResponse{}, metadb.ErrInvalidArgument
	}
	plan, err := a.planSlotLeaderTransfers(ctx, SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: req.SourceNodeID,
		TargetNodeID: req.TargetNodeID,
		SlotIDs:      append([]uint32(nil), req.SlotIDs...),
		MaxTasks:     req.MaxTasks,
		TargetPolicy: req.TargetPolicy,
	})
	if err != nil {
		return SlotLeaderTransferBatchExecuteResponse{}, err
	}
	if plan.response.StateRevision != req.StateRevision {
		return SlotLeaderTransferBatchExecuteResponse{}, ErrSlotLeaderTransferPlanStale
	}
	if plan.response.PlanID != req.PlanID {
		return SlotLeaderTransferBatchExecuteResponse{}, ErrSlotLeaderTransferPlanMismatch
	}
	needsWriter := false
	for _, candidate := range plan.response.Candidates {
		if candidate.Action == SlotLeaderTransferBatchActionCreate {
			needsWriter = true
			break
		}
	}
	if needsWriter && a.leaderTransfer == nil {
		return SlotLeaderTransferBatchExecuteResponse{}, ErrSlotLeaderTransferUnavailable
	}
	out := SlotLeaderTransferBatchExecuteResponse{
		GeneratedAt:   a.now(),
		StateRevision: plan.response.StateRevision,
		PlanID:        plan.response.PlanID,
	}
	for _, candidate := range plan.response.Candidates {
		out.Summary.Requested++
		if candidate.Action == SlotLeaderTransferBatchActionExisting {
			out.Summary.Existing++
			out.Results = append(out.Results, SlotLeaderTransferBatchExecuteResult{
				SlotID:       candidate.SlotID,
				TargetNodeID: candidate.TargetNodeID,
				Status:       SlotLeaderTransferBatchResultExisting,
				TaskID:       candidate.ExistingTaskID,
				Message:      SlotLeaderTransferMessageExistingTask,
			})
			continue
		}
		writeResult, err := a.leaderTransfer.RequestSlotLeaderTransfer(ctx, control.SlotLeaderTransferRequest{
			SlotID:        candidate.SlotID,
			SourceNode:    candidate.SourceNodeID,
			TargetNode:    candidate.TargetNodeID,
			TargetPeers:   append([]uint64(nil), candidate.DesiredPeers...),
			ConfigEpoch:   candidate.ConfigEpoch,
			StateRevision: plan.response.StateRevision,
		})
		if err != nil {
			out.Summary.Failed++
			out.Results = append(out.Results, SlotLeaderTransferBatchExecuteResult{
				SlotID:       candidate.SlotID,
				TargetNodeID: candidate.TargetNodeID,
				Status:       SlotLeaderTransferBatchResultFailed,
				Message:      err.Error(),
			})
			continue
		}
		row := SlotLeaderTransferBatchExecuteResult{
			SlotID:       candidate.SlotID,
			TargetNodeID: candidate.TargetNodeID,
			Message:      SlotLeaderTransferMessageExistingTask,
		}
		if writeResult.Task != nil {
			row.TaskID = writeResult.Task.TaskID
		}
		if writeResult.Created {
			out.Summary.Created++
			row.Status = SlotLeaderTransferBatchResultCreated
			row.Message = SlotLeaderTransferMessageCreated
		} else {
			out.Summary.Existing++
			row.Status = SlotLeaderTransferBatchResultExisting
		}
		out.Results = append(out.Results, row)
	}
	return out, nil
}
```

- [ ] **Step 5: Run execute tests**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestExecuteSlotLeaderTransferBatch|TestPlanSlotLeaderTransfers' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run all management usecase tests**

Run:

```bash
go test ./internalv2/usecase/management -count=1
```

Expected: PASS.

### Task 4: Add Manager HTTP Plan/Execute Routes

**Files:**
- Create: `internalv2/access/manager/slot_leader_transfer_batch.go`
- Create: `internalv2/access/manager/slot_leader_transfer_batch_test.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Extend the manager `Management` interface**

In `internalv2/access/manager/server.go`, add methods immediately after `RequestSlotLeaderTransfer`:

```go
	// PlanSlotLeaderTransfers builds a read-only batch Slot leader transfer plan.
	PlanSlotLeaderTransfers(ctx context.Context, req managementusecase.SlotLeaderTransferBatchPlanRequest) (managementusecase.SlotLeaderTransferBatchPlanResponse, error)
	// ExecuteSlotLeaderTransferBatch executes a fenced batch Slot leader transfer plan.
	ExecuteSlotLeaderTransferBatch(ctx context.Context, req managementusecase.SlotLeaderTransferBatchExecuteRequest) (managementusecase.SlotLeaderTransferBatchExecuteResponse, error)
```

- [ ] **Step 2: Register the routes in existing slot permission groups**

In `registerRoutes`, add:

```go
	slots.POST("/slots/leader-transfer-plan", s.handleSlotLeaderTransferBatchPlan)
```

inside the existing `cluster.slot:r` group, after `slots.GET("/slots", ...)`.

Add:

```go
	slotWrites.POST("/slots/leader-transfer-batch", s.handleSlotLeaderTransferBatchExecute)
```

inside the existing `cluster.slot:w` group, after single-Slot leader transfer.

- [ ] **Step 3: Extend `managerNodesStub`**

In `internalv2/access/manager/server_test.go`, add fields:

```go
	slotLeaderTransferBatchPlanResponse    managementusecase.SlotLeaderTransferBatchPlanResponse
	slotLeaderTransferBatchExecuteResponse managementusecase.SlotLeaderTransferBatchExecuteResponse
	slotLeaderTransferBatchPlanReqSink     *managementusecase.SlotLeaderTransferBatchPlanRequest
	slotLeaderTransferBatchExecuteReqSink  *managementusecase.SlotLeaderTransferBatchExecuteRequest
	slotLeaderTransferBatchPlanErr         error
	slotLeaderTransferBatchExecuteErr      error
```

Add methods:

```go
func (s managerNodesStub) PlanSlotLeaderTransfers(_ context.Context, req managementusecase.SlotLeaderTransferBatchPlanRequest) (managementusecase.SlotLeaderTransferBatchPlanResponse, error) {
	if s.slotLeaderTransferBatchPlanReqSink != nil {
		*s.slotLeaderTransferBatchPlanReqSink = req
	}
	return s.slotLeaderTransferBatchPlanResponse, s.slotLeaderTransferBatchPlanErr
}

func (s managerNodesStub) ExecuteSlotLeaderTransferBatch(_ context.Context, req managementusecase.SlotLeaderTransferBatchExecuteRequest) (managementusecase.SlotLeaderTransferBatchExecuteResponse, error) {
	if s.slotLeaderTransferBatchExecuteReqSink != nil {
		*s.slotLeaderTransferBatchExecuteReqSink = req
	}
	return s.slotLeaderTransferBatchExecuteResponse, s.slotLeaderTransferBatchExecuteErr
}
```

- [ ] **Step 4: Write failing HTTP tests**

Create `internalv2/access/manager/slot_leader_transfer_batch_test.go`:

```go
package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerSlotLeaderTransferBatchPlanReturnsPreview(t *testing.T) {
	generatedAt := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	var seen managementusecase.SlotLeaderTransferBatchPlanRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.slot", Actions: []string{"r"}}},
		}}),
		Management: managerNodesStub{
			slotLeaderTransferBatchPlanReqSink: &seen,
			slotLeaderTransferBatchPlanResponse: managementusecase.SlotLeaderTransferBatchPlanResponse{
				GeneratedAt:   generatedAt,
				StateRevision: 22,
				PlanID:        "slot-leader-transfer:22:abc",
				SourceNodeID:  1,
				TargetPolicy:  managementusecase.SlotLeaderTransferTargetPolicyLeastLeaders,
				MaxTasks:      8,
				Summary: managementusecase.SlotLeaderTransferBatchPlanSummary{
					Scanned: 2, Candidates: 1, Skipped: 1, WouldCreate: 1,
				},
				Candidates: []managementusecase.SlotLeaderTransferBatchCandidate{{
					SlotID: 1, SourceNodeID: 1, TargetNodeID: 2, PreferredLeader: 1, ActualLeader: 1,
					DesiredPeers: []uint64{1, 2, 3}, CurrentVoters: []uint64{1, 2, 3}, ConfigEpoch: 7,
					Action: managementusecase.SlotLeaderTransferBatchActionCreate,
				}},
				Skipped: []managementusecase.SlotLeaderTransferBatchSkip{{
					SlotID: 2, Reason: managementusecase.SlotLeaderTransferSkipSourceNotLeaderOrPreferred, Message: "source is not current or preferred leader",
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-plan", strings.NewReader(`{"source_node_id":1,"target_node_id":2,"slot_ids":[1,2],"max_tasks":8,"target_policy":"least_leaders"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen.SourceNodeID != 1 || seen.TargetNodeID != 2 || seen.MaxTasks != 8 || len(seen.SlotIDs) != 2 {
		t.Fatalf("request = %#v, want parsed plan request", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at":"2026-06-20T10:00:00Z",
		"state_revision":22,
		"plan_id":"slot-leader-transfer:22:abc",
		"source_node_id":1,
		"target_policy":"least_leaders",
		"max_tasks":8,
		"summary":{"scanned":2,"candidates":1,"skipped":1,"existing_tasks":0,"would_create":1},
		"candidates":[{"slot_id":1,"source_node_id":1,"target_node_id":2,"preferred_leader":1,"actual_leader":1,"desired_peers":[1,2,3],"current_voters":[1,2,3],"config_epoch":7,"existing_task_id":"","action":"create"}],
		"skipped":[{"slot_id":2,"reason":"source_not_leader_or_preferred","message":"source is not current or preferred leader"}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerSlotLeaderTransferBatchExecuteReturnsAcceptedWhenCreated(t *testing.T) {
	var seen managementusecase.SlotLeaderTransferBatchExecuteRequest
	srv := New(Options{
		Management: managerNodesStub{
			slotLeaderTransferBatchExecuteReqSink: &seen,
			slotLeaderTransferBatchExecuteResponse: managementusecase.SlotLeaderTransferBatchExecuteResponse{
				GeneratedAt:   time.Date(2026, 6, 20, 10, 0, 2, 0, time.UTC),
				StateRevision: 22,
				PlanID:        "slot-leader-transfer:22:abc",
				Summary:       managementusecase.SlotLeaderTransferBatchExecuteSummary{Requested: 1, Created: 1},
				Results: []managementusecase.SlotLeaderTransferBatchExecuteResult{{
					SlotID: 1, TargetNodeID: 2, Status: managementusecase.SlotLeaderTransferBatchResultCreated, TaskID: "slot-1-leader-transfer-7-r22", Message: managementusecase.SlotLeaderTransferMessageCreated,
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-batch", strings.NewReader(`{"source_node_id":1,"target_node_id":2,"max_tasks":8,"target_policy":"least_leaders","state_revision":22,"plan_id":"slot-leader-transfer:22:abc"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if seen.StateRevision != 22 || seen.PlanID != "slot-leader-transfer:22:abc" {
		t.Fatalf("execute request = %#v, want revision and plan id", seen)
	}
}
```

- [ ] **Step 5: Run HTTP tests and verify missing handler failures**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerSlotLeaderTransferBatch' -count=1
```

Expected: compile failure mentioning undefined handler or DTO functions.

- [ ] **Step 6: Implement HTTP DTOs and handlers**

Create `internalv2/access/manager/slot_leader_transfer_batch.go`:

```go
package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerSlotLeaderTransferBatchPlanRequest is the JSON body for batch planning.
type ManagerSlotLeaderTransferBatchPlanRequest struct {
	SourceNodeID uint64   `json:"source_node_id"`
	TargetNodeID uint64   `json:"target_node_id"`
	SlotIDs      []uint32 `json:"slot_ids"`
	MaxTasks     int      `json:"max_tasks"`
	TargetPolicy string   `json:"target_policy"`
}

// ManagerSlotLeaderTransferBatchExecuteRequest is the JSON body for fenced batch execution.
type ManagerSlotLeaderTransferBatchExecuteRequest struct {
	SourceNodeID  uint64   `json:"source_node_id"`
	TargetNodeID  uint64   `json:"target_node_id"`
	SlotIDs       []uint32 `json:"slot_ids"`
	MaxTasks      int      `json:"max_tasks"`
	TargetPolicy  string   `json:"target_policy"`
	StateRevision uint64   `json:"state_revision"`
	PlanID        string   `json:"plan_id"`
}

type ManagerSlotLeaderTransferBatchPlanResponse struct {
	GeneratedAt   string                                           `json:"generated_at"`
	StateRevision uint64                                           `json:"state_revision"`
	PlanID        string                                           `json:"plan_id"`
	SourceNodeID  uint64                                           `json:"source_node_id"`
	TargetPolicy  string                                           `json:"target_policy"`
	MaxTasks      int                                              `json:"max_tasks"`
	Summary       ManagerSlotLeaderTransferBatchPlanSummary         `json:"summary"`
	Candidates    []ManagerSlotLeaderTransferBatchCandidateResponse `json:"candidates"`
	Skipped       []ManagerSlotLeaderTransferBatchSkipResponse      `json:"skipped"`
}

type ManagerSlotLeaderTransferBatchPlanSummary struct {
	Scanned       int `json:"scanned"`
	Candidates   int `json:"candidates"`
	Skipped      int `json:"skipped"`
	ExistingTasks int `json:"existing_tasks"`
	WouldCreate  int `json:"would_create"`
}

type ManagerSlotLeaderTransferBatchCandidateResponse struct {
	SlotID          uint32   `json:"slot_id"`
	SourceNodeID    uint64   `json:"source_node_id"`
	TargetNodeID    uint64   `json:"target_node_id"`
	PreferredLeader uint64   `json:"preferred_leader"`
	ActualLeader    uint64   `json:"actual_leader"`
	DesiredPeers    []uint64 `json:"desired_peers"`
	CurrentVoters   []uint64 `json:"current_voters"`
	ConfigEpoch     uint64   `json:"config_epoch"`
	ExistingTaskID  string   `json:"existing_task_id"`
	Action          string   `json:"action"`
}

type ManagerSlotLeaderTransferBatchSkipResponse struct {
	SlotID  uint32 `json:"slot_id"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type ManagerSlotLeaderTransferBatchExecuteResponse struct {
	GeneratedAt   string                                           `json:"generated_at"`
	StateRevision uint64                                           `json:"state_revision"`
	PlanID        string                                           `json:"plan_id"`
	Summary       ManagerSlotLeaderTransferBatchExecuteSummary      `json:"summary"`
	Results       []ManagerSlotLeaderTransferBatchExecuteResultBody `json:"results"`
}

type ManagerSlotLeaderTransferBatchExecuteSummary struct {
	Requested     int `json:"requested"`
	Created       int `json:"created"`
	Existing      int `json:"existing"`
	AlreadyLeader int `json:"already_leader"`
	Skipped       int `json:"skipped"`
	Failed        int `json:"failed"`
}

type ManagerSlotLeaderTransferBatchExecuteResultBody struct {
	SlotID       uint32 `json:"slot_id"`
	TargetNodeID uint64 `json:"target_node_id"`
	Status       string `json:"status"`
	TaskID       string `json:"task_id"`
	Message      string `json:"message"`
}

func (s *Server) handleSlotLeaderTransferBatchPlan(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body ManagerSlotLeaderTransferBatchPlanRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	response, err := s.management.PlanSlotLeaderTransfers(c.Request.Context(), managementusecase.SlotLeaderTransferBatchPlanRequest{
		SourceNodeID: body.SourceNodeID,
		TargetNodeID: body.TargetNodeID,
		SlotIDs:      append([]uint32(nil), body.SlotIDs...),
		MaxTasks:     body.MaxTasks,
		TargetPolicy: body.TargetPolicy,
	})
	if err != nil {
		writeSlotLeaderTransferBatchError(c, err)
		return
	}
	c.JSON(http.StatusOK, slotLeaderTransferBatchPlanResponseDTO(response))
}

func (s *Server) handleSlotLeaderTransferBatchExecute(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body ManagerSlotLeaderTransferBatchExecuteRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	response, err := s.management.ExecuteSlotLeaderTransferBatch(c.Request.Context(), managementusecase.SlotLeaderTransferBatchExecuteRequest{
		SourceNodeID:  body.SourceNodeID,
		TargetNodeID:  body.TargetNodeID,
		SlotIDs:       append([]uint32(nil), body.SlotIDs...),
		MaxTasks:      body.MaxTasks,
		TargetPolicy:  body.TargetPolicy,
		StateRevision: body.StateRevision,
		PlanID:        body.PlanID,
	})
	if err != nil {
		writeSlotLeaderTransferBatchError(c, err)
		return
	}
	status := http.StatusOK
	if response.Summary.Created > 0 {
		status = http.StatusAccepted
	}
	c.JSON(status, slotLeaderTransferBatchExecuteResponseDTO(response))
}

func writeSlotLeaderTransferBatchError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
	case errors.Is(err, managementusecase.ErrSlotLeaderTransferPlanStale),
		errors.Is(err, managementusecase.ErrSlotLeaderTransferPlanMismatch),
		errors.Is(err, managementusecase.ErrSlotLeaderTransferConflict):
		jsonError(c, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, managementusecase.ErrSlotLeaderTransferUnavailable),
		errors.Is(err, managementusecase.ErrSlotRuntimeStatusUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "service_unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
```

Add DTO mapping helpers below the handlers. The helpers must deep-copy slices:

```go
func slotLeaderTransferBatchPlanResponseDTO(response managementusecase.SlotLeaderTransferBatchPlanResponse) ManagerSlotLeaderTransferBatchPlanResponse {
	candidates := make([]ManagerSlotLeaderTransferBatchCandidateResponse, 0, len(response.Candidates))
	for _, item := range response.Candidates {
		candidates = append(candidates, ManagerSlotLeaderTransferBatchCandidateResponse{
			SlotID:          item.SlotID,
			SourceNodeID:    item.SourceNodeID,
			TargetNodeID:    item.TargetNodeID,
			PreferredLeader: item.PreferredLeader,
			ActualLeader:    item.ActualLeader,
			DesiredPeers:    append([]uint64(nil), item.DesiredPeers...),
			CurrentVoters:   append([]uint64(nil), item.CurrentVoters...),
			ConfigEpoch:     item.ConfigEpoch,
			ExistingTaskID:  item.ExistingTaskID,
			Action:          item.Action,
		})
	}
	skipped := make([]ManagerSlotLeaderTransferBatchSkipResponse, 0, len(response.Skipped))
	for _, item := range response.Skipped {
		skipped = append(skipped, ManagerSlotLeaderTransferBatchSkipResponse{
			SlotID:  item.SlotID,
			Reason:  item.Reason,
			Message: item.Message,
		})
	}
	return ManagerSlotLeaderTransferBatchPlanResponse{
		GeneratedAt:   managerTimeString(response.GeneratedAt),
		StateRevision: response.StateRevision,
		PlanID:        response.PlanID,
		SourceNodeID:  response.SourceNodeID,
		TargetPolicy:  response.TargetPolicy,
		MaxTasks:      response.MaxTasks,
		Summary: ManagerSlotLeaderTransferBatchPlanSummary{
			Scanned:       response.Summary.Scanned,
			Candidates:   response.Summary.Candidates,
			Skipped:      response.Summary.Skipped,
			ExistingTasks: response.Summary.ExistingTasks,
			WouldCreate:  response.Summary.WouldCreate,
		},
		Candidates: candidates,
		Skipped:    skipped,
	}
}

func slotLeaderTransferBatchExecuteResponseDTO(response managementusecase.SlotLeaderTransferBatchExecuteResponse) ManagerSlotLeaderTransferBatchExecuteResponse {
	results := make([]ManagerSlotLeaderTransferBatchExecuteResultBody, 0, len(response.Results))
	for _, item := range response.Results {
		results = append(results, ManagerSlotLeaderTransferBatchExecuteResultBody{
			SlotID:       item.SlotID,
			TargetNodeID: item.TargetNodeID,
			Status:       item.Status,
			TaskID:       item.TaskID,
			Message:      item.Message,
		})
	}
	return ManagerSlotLeaderTransferBatchExecuteResponse{
		GeneratedAt:   managerTimeString(response.GeneratedAt),
		StateRevision: response.StateRevision,
		PlanID:        response.PlanID,
		Summary: ManagerSlotLeaderTransferBatchExecuteSummary{
			Requested:     response.Summary.Requested,
			Created:       response.Summary.Created,
			Existing:      response.Summary.Existing,
			AlreadyLeader: response.Summary.AlreadyLeader,
			Skipped:       response.Summary.Skipped,
			Failed:        response.Summary.Failed,
		},
		Results: results,
	}
}
```

- [ ] **Step 7: Add error and permission tests**

Append these tests to `slot_leader_transfer_batch_test.go`:

```go
func TestManagerSlotLeaderTransferBatchMapsErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid", err: metadb.ErrInvalidArgument, status: http.StatusBadRequest, code: "bad_request"},
		{name: "stale", err: managementusecase.ErrSlotLeaderTransferPlanStale, status: http.StatusConflict, code: "conflict"},
		{name: "mismatch", err: managementusecase.ErrSlotLeaderTransferPlanMismatch, status: http.StatusConflict, code: "conflict"},
		{name: "unavailable", err: managementusecase.ErrSlotLeaderTransferUnavailable, status: http.StatusServiceUnavailable, code: "service_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{slotLeaderTransferBatchPlanErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-plan", strings.NewReader(`{"source_node_id":1}`))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"`+tt.code+`","message":"`+tt.code+`"}`) {
				t.Fatalf("body = %s, want code %s", rec.Body.String(), tt.code)
			}
		})
	}
}

func TestManagerSlotLeaderTransferBatchPermissions(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.slot", Actions: []string{"r"}}},
		}}),
		Management: managerNodesStub{},
	})
	token := "Bearer " + mustIssueTestToken(t, srv, "reader")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-plan", strings.NewReader(`{"source_node_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan status = %d, want OK for slot reader; body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-batch", strings.NewReader(`{"source_node_id":1,"state_revision":1,"plan_id":"p"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("execute status = %d, want forbidden for slot reader; body=%s", rec.Code, rec.Body.String())
	}
}
```

Add missing imports in the test file: `metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"`.

- [ ] **Step 8: Run HTTP tests**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerSlotLeaderTransferBatch|TestManagerSlotLeaderTransfer' -count=1
```

Expected: PASS.

- [ ] **Step 9: Run both backend packages**

Run:

```bash
go test ./internalv2/usecase/management ./internalv2/access/manager -count=1
```

Expected: PASS.

### Task 5: Update FLOW Documentation And Confirm No ControllerV2 Change

**Files:**
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Update management FLOW**

In `internalv2/usecase/management/FLOW.md`, extend the responsibility paragraph so it lists:

```text
Slot leader-transfer batch planning/execution used by
`POST /manager/slots/leader-transfer-plan` and
`POST /manager/slots/leader-transfer-batch`
```

After the existing "Slot Leader Transfer Flow" section, add:

````markdown
## Slot Leader Transfer Batch Flow

```text
manager HTTP handler
  -> management.App.PlanSlotLeaderTransfers
  -> ControlSnapshotReader.LocalControlSnapshot
  -> SlotRuntimeStatusReader.SlotRuntimeStatus for candidate Slots
  -> deterministic plan_id, candidate rows, and skip rows

manager HTTP handler
  -> management.App.ExecuteSlotLeaderTransferBatch
  -> recompute plan and verify state_revision + plan_id
  -> management.App.RequestSlotLeaderTransfer per create candidate
  -> existing SlotLeaderTransferWriter.RequestSlotLeaderTransfer
  -> Controller-backed single-Slot leader_transfer tasks
```

The batch flow is a planner and producer for ordinary single-Slot
`leader_transfer` tasks. It does not introduce a durable batch task, does not
call Slot Raft directly, and does not require a new ControllerV2 command kind.
Planning is read-only. Execution is fenced by the observed Controller state
revision and deterministic plan digest before any task write occurs. Partial
per-Slot write failures after the global fence are returned as per-result rows;
accepted task writes are not rolled back.
````

- [ ] **Step 2: Update manager HTTP FLOW**

In `internalv2/access/manager/FLOW.md`, add these routes to the route list:

```text
POST /manager/slots/leader-transfer-plan (read-only batch Slot leader-transfer plan; requires cluster.slot:r when Auth.On=true)
POST /manager/slots/leader-transfer-batch (fenced batch Slot leader-transfer execution; requires cluster.slot:w when Auth.On=true)
```

In the Slot route explanation paragraph, add:

```text
`/manager/slots/leader-transfer-plan` parses batch criteria and returns a
read-only candidate/skip preview with a state revision and deterministic
plan id. `/manager/slots/leader-transfer-batch` parses the same criteria plus
the revision and plan id, delegates stale-plan checks to the management
usecase, and returns `202 Accepted` only when at least one ordinary
single-Slot task is created. Both routes keep Slot Raft mutation below the
existing task executor path.
```

- [ ] **Step 3: Verify no ControllerV2 code changed**

Run:

```bash
git diff --name-only | rg '^pkg/controllerv2/' || true
```

Expected: no output.

- [ ] **Step 4: Run targeted package tests**

Run:

```bash
go test ./internalv2/usecase/management ./internalv2/access/manager ./pkg/clusterv2/tasks -count=1
```

Expected: PASS.

### Task 6: Final Verification And Commit

**Files:**
- Verify all changed files from Tasks 1-5.

- [ ] **Step 1: Run formatting**

Run:

```bash
gofmt -w internalv2/usecase/management/slot_leader_transfer_batch.go internalv2/usecase/management/slot_leader_transfer_batch_test.go internalv2/access/manager/slot_leader_transfer_batch.go internalv2/access/manager/slot_leader_transfer_batch_test.go internalv2/access/manager/server.go internalv2/access/manager/server_test.go
```

Expected: command exits 0.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internalv2/usecase/management ./internalv2/access/manager ./pkg/clusterv2/tasks -count=1
```

Expected: PASS.

- [ ] **Step 3: Run broader v2-adjacent tests**

Run:

```bash
go test ./internalv2/... ./pkg/clusterv2/... ./pkg/controllerv2/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Check diff hygiene**

Run:

```bash
git diff --check
git status --short
```

Expected: `git diff --check` exits 0. `git status --short` shows only files intentionally changed by this implementation.

- [ ] **Step 5: Commit**

Run:

```bash
git add internalv2/usecase/management/slot_leader_transfer_batch.go \
  internalv2/usecase/management/slot_leader_transfer_batch_test.go \
  internalv2/usecase/management/slot_leader_transfer.go \
  internalv2/usecase/management/FLOW.md \
  internalv2/access/manager/slot_leader_transfer_batch.go \
  internalv2/access/manager/slot_leader_transfer_batch_test.go \
  internalv2/access/manager/server.go \
  internalv2/access/manager/server_test.go \
  internalv2/access/manager/FLOW.md
git commit -m "feat: add slot leader transfer batch backend"
```

Expected: commit succeeds. Do not stage unrelated generated files.
