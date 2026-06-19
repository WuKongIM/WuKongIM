# Slot Leader Transfer Task V0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement manual single-Slot leader transfer for `wukongimv2` through ControllerV2 durable tasks, while preserving Slot Raft as the only source of actual leadership truth.

**Architecture:** Manager HTTP accepts an operator intent and delegates to `internalv2/usecase/management`. The usecase validates a real Slot runtime observation, then asks `pkg/clusterv2/control` to write a ControllerV2 `leader_transfer` task plus `PreferredLeader`; a `pkg/clusterv2/tasks` executor asks Slot Raft to transfer leadership and completes the task only after observing a legal actual non-source leader.

**Tech Stack:** Go, ControllerV2 state/FSM/Raft commands, `pkg/clusterv2/control` typed RPC forwarding, `pkg/clusterv2/tasks`, Slot Multi-Raft status, `internalv2` manager usecase and HTTP API, e2ev2 `cmd/wukongimv2` black-box tests.

---

## Scope Boundaries

Implement only the v0 workflow from `docs/superpowers/specs/2026-06-19-slot-leader-transfer-task-v0-design.md`:

- manual API: `POST /manager/slots/:slot_id/leader-transfer`
- request body: `{ "target_node": 2 }`
- task creation returns `202 Accepted`; idempotent no-op returns `200 OK`
- one physical Slot per request
- one `leader_transfer` task per Slot
- `PreferredLeader` is a durable recommendation, not forced leadership
- target log freshness is not preflighted by management code; Raft decides whether transfer can happen
- completion is based on actual Slot Raft observation: any legal non-source leader satisfies the task
- completed tasks are removed by `complete_task`
- failed tasks stay active with bounded error text

Do not add:

- automatic leader balancing
- batch transfer API
- node-failure-triggered transfer
- per-peer match-index preflight
- new config keys
- legacy `internal` dependencies

## File Structure

- `pkg/controllerv2/state/types.go`: add `TaskKindLeaderTransfer` and `TaskStepTransferLeader`.
- `pkg/controllerv2/state/normalize.go`: default leader-transfer completion policy to `single_observer`.
- `pkg/controllerv2/state/validate.go`: validate leader-transfer task invariants against the Slot assignment.
- `pkg/controllerv2/types.go`: re-export the new task kind and step.
- `pkg/controllerv2/runtime_leader_transfer.go`: add the root Runtime facade for task creation.
- `pkg/controllerv2/*_test.go`: cover state validation, command codec, FSM completion/failure, and runtime facade shape.
- `pkg/clusterv2/control/leader_transfer.go`: add control-layer request/result types and validation.
- `pkg/clusterv2/control/{controller.go,runtime.go,static.go,codec.go,transport.go}`: expose and forward leader-transfer task creation.
- `pkg/clusterv2/control/*_test.go`: cover static recording, typed RPC forwarding, and ControllerV2 mapping.
- `pkg/clusterv2/tasks/{executor.go,leader_transfer.go}`: add composite executor and leader-transfer executor.
- `pkg/clusterv2/tasks/leader_transfer_test.go`: cover ownership, legal non-target completion, no-op followers, and timeout failure.
- `pkg/clusterv2/slots/types.go`: expose `TransferLeadership`.
- `pkg/clusterv2/default_slots.go`: wire bootstrap and leader-transfer executors together.
- `pkg/clusterv2/node_logs.go`: include `CurrentVoters` in local Slot Raft status for management preflight.
- `internalv2/usecase/management/slot_leader_transfer.go`: add manager usecase DTOs, ports, errors, and validation.
- `internalv2/usecase/management/nodes.go`: wire the new usecase ports into `Options` and `App`.
- `internalv2/access/manager/{server.go,slot_leader_transfer.go}`: add manager interface method, route, request/response DTOs, and error mapping.
- `internalv2/infra/cluster/management_leader_transfer.go`: adapt clusterv2 control intent and local Slot runtime status to management ports.
- `internalv2/app/wiring.go`: wire management leader-transfer ports when the cluster exposes them.
- `test/e2ev2/control/slot_leader_transfer/{AGENTS.md,slot_leader_transfer_test.go}`: add process-level smoke coverage.
- `pkg/controllerv2/FLOW.md`, `pkg/clusterv2/FLOW.md`, `internalv2/usecase/management/FLOW.md`, `internalv2/access/manager/FLOW.md`, `internalv2/app/FLOW.md`, `test/e2ev2/AGENTS.md`: update only the changed flow descriptions.

---

### Task 1: ControllerV2 Schema, Runtime Facade, and Control Intent

**Files:**
- Modify: `pkg/controllerv2/state/types.go`
- Modify: `pkg/controllerv2/state/normalize.go`
- Modify: `pkg/controllerv2/state/validate.go`
- Modify: `pkg/controllerv2/types.go`
- Create: `pkg/controllerv2/runtime_leader_transfer.go`
- Modify: `pkg/controllerv2/{runtime_test.go,command/codec_test.go,fsm/fsm_test.go,fsm/mutations.go}`
- Create: `pkg/clusterv2/control/leader_transfer.go`
- Modify: `pkg/clusterv2/control/{controller.go,runtime.go,static.go,codec.go,transport.go}`
- Modify: `pkg/clusterv2/control/{control_test.go,runtime_test.go,transport_test.go,controllerv2_test.go,snapshot.go}`

- [ ] **Step 1: Write failing ControllerV2 tests**

Add tests with these names and assertions:

```go
func TestClusterStateValidateAcceptsLeaderTransferTask(t *testing.T)
func TestClusterStateValidateRejectsInvalidLeaderTransferTask(t *testing.T)
func TestApplyLeaderTransferTaskUpsertAndComplete(t *testing.T)
func TestApplyLeaderTransferTaskFailKeepsActiveTask(t *testing.T)
func TestRuntimeRequestSlotLeaderTransfer(t *testing.T)
```

The valid task fixture must use:

```go
state.ReconcileTask{
	TaskID:           "slot-1-leader-transfer-7-r9",
	SlotID:           1,
	Kind:             state.TaskKindLeaderTransfer,
	Step:             state.TaskStepTransferLeader,
	SourceNode:       1,
	TargetNode:       2,
	TargetPeers:      []uint64{1, 2, 3},
	CompletionPolicy: state.TaskCompletionPolicySingleObserver,
	ConfigEpoch:      7,
	Status:           state.TaskStatusPending,
}
```

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/state ./pkg/controllerv2/fsm ./pkg/controllerv2 -run 'LeaderTransfer|RequestSlotLeaderTransfer' -count=1
```

Expected: FAIL because the kind, step, and runtime facade do not exist.

- [ ] **Step 2: Add ControllerV2 task constants, normalization, and validation**

In `pkg/controllerv2/state/types.go`:

```go
// TaskKindLeaderTransfer records an operator-requested Slot Raft leadership transfer.
TaskKindLeaderTransfer TaskKind = "leader_transfer"

// TaskStepTransferLeader asks Slot Raft to move leadership away from the observed source.
TaskStepTransferLeader TaskStep = "transfer_leader"
```

In `pkg/controllerv2/state/normalize.go`:

```go
if task.Kind == TaskKindLeaderTransfer && task.CompletionPolicy == "" {
	task.CompletionPolicy = TaskCompletionPolicySingleObserver
}
```

In `pkg/controllerv2/state/validate.go`, the `TaskKindLeaderTransfer` branch must reject:

```go
task.Step != TaskStepTransferLeader
task.SourceNode == 0 || task.TargetNode == 0
task.SourceNode == task.TargetNode
!containsUint64(assignment.DesiredPeers, task.SourceNode)
!containsUint64(assignment.DesiredPeers, task.TargetNode)
!reflect.DeepEqual(task.TargetPeers, assignment.DesiredPeers)
task.ConfigEpoch != assignment.ConfigEpoch
task.TargetNode != assignment.PreferredLeader
task.CompletionPolicy != TaskCompletionPolicySingleObserver
len(task.ParticipantProgress) != 0
```

Re-export the constants from `pkg/controllerv2/types.go`.

- [ ] **Step 3: Keep stale revision handling bootstrap-only**

In `pkg/controllerv2/fsm/mutations.go`, update `KindUpsertSlotAssignmentAndTask` so bootstrap keeps the existing stale-revision helper and leader-transfer uses normal CAS rejection:

```go
if cmd.Task.Kind == state.TaskKindBootstrap {
	if stale, handled := handleBootstrapRevisionMismatch(next, cmd); handled {
		return stale
	}
} else if cmd.ExpectedRevision != nil && *cmd.ExpectedRevision != currentRevision {
	return reject(ReasonExpectedRevisionMismatch)
}
```

- [ ] **Step 4: Add ControllerV2 runtime facade**

Create `pkg/controllerv2/runtime_leader_transfer.go`:

```go
package controllerv2

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// SlotLeaderTransferRequest describes a durable operator intent to transfer one Slot leader.
type SlotLeaderTransferRequest struct {
	// SlotID is the physical Slot affected by the transfer.
	SlotID uint32
	// SourceNode is the actual Slot leader observed before task creation.
	SourceNode uint64
	// TargetNode is the preferred leader requested by the operator.
	TargetNode uint64
	// TargetPeers is the Slot assignment peer set tied to this task.
	TargetPeers []uint64
	// ConfigEpoch fences the task to the current Slot assignment.
	ConfigEpoch uint64
	// StateRevision is the ControllerV2 state revision used as CAS.
	StateRevision uint64
}

// RequestSlotLeaderTransfer proposes a leader_transfer task through ControllerV2 Raft.
func (r *Runtime) RequestSlotLeaderTransfer(ctx context.Context, req SlotLeaderTransferRequest) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if req.SlotID == 0 || req.SourceNode == 0 || req.TargetNode == 0 || req.ConfigEpoch == 0 || req.StateRevision == 0 {
		return fmt.Errorf("controllerv2: invalid slot leader transfer request")
	}
	peers := append([]uint64(nil), req.TargetPeers...)
	expected := req.StateRevision
	return r.proposeTaskCommand(ctx, command.Command{
		Kind:             command.KindUpsertSlotAssignmentAndTask,
		ExpectedRevision: &expected,
		Assignment: &state.SlotAssignment{
			SlotID:          req.SlotID,
			DesiredPeers:    peers,
			ConfigEpoch:     req.ConfigEpoch,
			PreferredLeader: req.TargetNode,
		},
		Task: &state.ReconcileTask{
			TaskID:           fmt.Sprintf("slot-%d-leader-transfer-%d-r%d", req.SlotID, req.ConfigEpoch, req.StateRevision),
			SlotID:           req.SlotID,
			Kind:             state.TaskKindLeaderTransfer,
			Step:             state.TaskStepTransferLeader,
			SourceNode:       req.SourceNode,
			TargetNode:       req.TargetNode,
			TargetPeers:      peers,
			CompletionPolicy: state.TaskCompletionPolicySingleObserver,
			ConfigEpoch:      req.ConfigEpoch,
			Status:           state.TaskStatusPending,
		},
	})
}
```

- [ ] **Step 5: Add clusterv2 control intent and typed RPC forwarding**

Create `pkg/clusterv2/control/leader_transfer.go` with:

```go
// SlotLeaderTransferRequest describes one Controller-backed Slot leader transfer intent.
type SlotLeaderTransferRequest struct {
	SlotID        uint32
	SourceNode    uint64
	TargetNode    uint64
	TargetPeers   []uint64
	ConfigEpoch   uint64
	StateRevision uint64
}

// SlotLeaderTransferResult is returned after a leader-transfer intent is accepted.
type SlotLeaderTransferResult struct {
	Created bool
	Task    *ReconcileTask
}
```

Add `RequestSlotLeaderTransfer(context.Context, SlotLeaderTransferRequest) (SlotLeaderTransferResult, error)` to `pkg/clusterv2/control.Controller`, `StaticController`, and `Runtime`.

Extend the existing control task RPC instead of creating a new service:

```go
const TaskActionLeaderTransfer TaskAction = "leader_transfer"

type TaskRequest struct {
	Action         TaskAction                `json:"action"`
	Result         cv2.TaskResult            `json:"result,omitempty"`
	Progress       cv2.TaskProgress          `json:"progress,omitempty"`
	LeaderTransfer SlotLeaderTransferRequest `json:"leader_transfer,omitempty"`
}
```

Extend `TaskApplier` and `NewTaskHandler`:

```go
// RequestSlotLeaderTransfer submits a Controller-backed Slot leader transfer intent.
RequestSlotLeaderTransfer(context.Context, SlotLeaderTransferRequest) (SlotLeaderTransferResult, error)

case TaskActionLeaderTransfer:
	_, err := applier.RequestSlotLeaderTransfer(ctx, req.LeaderTransfer)
	return nil, err
```

In `Runtime.RequestSlotLeaderTransfer`, first call `r.backend.RequestSlotLeaderTransfer`. When `shouldForwardTaskWrite(err)` is true, call:

```go
return SlotLeaderTransferResult{}, r.forwardTaskRequest(ctx, TaskRequest{
	Action:         TaskActionLeaderTransfer,
	LeaderTransfer: req,
})
```

After a successful local write, return a result containing the deterministic task ID `slot-%d-leader-transfer-%d-r%d`.

- [ ] **Step 6: Run focused ControllerV2/control tests**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/state ./pkg/controllerv2/command ./pkg/controllerv2/fsm ./pkg/controllerv2 ./pkg/clusterv2/control -run 'LeaderTransfer|TaskRequest|RequestSlotLeaderTransfer' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 1**

Run:

```bash
git add pkg/controllerv2 pkg/clusterv2/control
git commit -m "feat: add controller slot leader transfer intent"
```

---

### Task 2: Slot Leader Transfer Executor

**Files:**
- Create: `pkg/clusterv2/tasks/executor.go`
- Create: `pkg/clusterv2/tasks/leader_transfer.go`
- Create: `pkg/clusterv2/tasks/leader_transfer_test.go`
- Modify: `pkg/clusterv2/slots/types.go`
- Modify: `pkg/clusterv2/default_slots.go`
- Modify: `pkg/clusterv2/node_snapshot_test.go`
- Modify: `pkg/clusterv2/node_logs.go`
- Modify: `pkg/clusterv2/node_logs_test.go`

- [ ] **Step 1: Write failing executor and status tests**

Create `pkg/clusterv2/tasks/leader_transfer_test.go` with these tests:

```go
func TestLeaderTransferExecutorLeaderCallsTransfer(t *testing.T)
func TestLeaderTransferExecutorCompletesOnLegalNonTargetLeader(t *testing.T)
func TestLeaderTransferExecutorNonLeaderDoesNotTransfer(t *testing.T)
func TestLeaderTransferExecutorFailsOnTimeout(t *testing.T)
```

Use task snapshots with:

```go
control.ReconcileTask{
	TaskID:           "slot-1-leader-transfer-7-r1",
	SlotID:           1,
	Kind:             control.TaskKindLeaderTransfer,
	Step:             control.TaskStepTransferLeader,
	SourceNode:       1,
	TargetNode:       2,
	TargetPeers:      []uint64{1, 2, 3},
	CompletionPolicy: control.TaskCompletionPolicySingleObserver,
	ConfigEpoch:      7,
	Status:           control.TaskStatusPending,
}
```

The fake runtime must expose:

```go
func (f *fakeLeaderTransferRuntime) Status(multiraft.SlotID) (multiraft.Status, error)
func (f *fakeLeaderTransferRuntime) TransferLeadership(context.Context, multiraft.SlotID, multiraft.NodeID) error
```

Also add `TestSlotRaftStatusFromRuntimeIncludesCurrentVoters` in `pkg/clusterv2/node_logs_test.go`:

```go
got := slotRaftStatusFromRuntime(2, 9, multiraft.Status{
	SlotID:        9,
	NodeID:        2,
	LeaderID:      1,
	Role:          multiraft.RoleFollower,
	CurrentVoters: []multiraft.NodeID{1, 2, 3},
})
require.Equal(t, []uint64{1, 2, 3}, got.CurrentVoters)
```

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/tasks ./pkg/clusterv2 -run 'TestLeaderTransferExecutor|TestSlotRaftStatusFromRuntimeIncludesCurrentVoters' -count=1
```

Expected: FAIL because the executor, transfer method, and `CurrentVoters` field do not exist.

- [ ] **Step 2: Expose current voters in local Slot Raft status**

In `pkg/clusterv2/node_logs.go`, extend `SlotRaftStatus`:

```go
// CurrentVoters is the current Slot Raft voter set observed by the runtime.
CurrentVoters []uint64
```

Update `slotRaftStatusFromRuntime`:

```go
CurrentVoters: slotRaftVotersFromRuntime(status.CurrentVoters),
```

Add:

```go
func slotRaftVotersFromRuntime(voters []multiraft.NodeID) []uint64 {
	out := make([]uint64, 0, len(voters))
	for _, voter := range voters {
		out = append(out, uint64(voter))
	}
	return out
}
```

- [ ] **Step 3: Add composite executor**

Create `pkg/clusterv2/tasks/executor.go`:

```go
package tasks

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// Executor reconciles active Controller tasks against local runtime state.
type Executor interface {
	Reconcile(context.Context, control.Snapshot) error
}

// CompositeExecutor runs several task executors in order.
type CompositeExecutor struct {
	executors []Executor
}

// NewCompositeExecutor creates an executor that delegates to each non-nil executor.
func NewCompositeExecutor(executors ...Executor) *CompositeExecutor {
	out := make([]Executor, 0, len(executors))
	for _, executor := range executors {
		if executor != nil {
			out = append(out, executor)
		}
	}
	return &CompositeExecutor{executors: out}
}

// Reconcile runs each child executor against the same snapshot.
func (e *CompositeExecutor) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if e == nil {
		return nil
	}
	for _, executor := range e.executors {
		if err := executor.Reconcile(ctx, snapshot); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Add leader-transfer executor**

Create `pkg/clusterv2/tasks/leader_transfer.go` with this behavior:

```go
type LeaderTransferRuntime interface {
	Status(multiraft.SlotID) (multiraft.Status, error)
	TransferLeadership(context.Context, multiraft.SlotID, multiraft.NodeID) error
}

func legalCompletedLeader(task control.ReconcileTask, assignment control.SlotAssignment, status multiraft.Status) bool {
	leader := uint64(status.LeaderID)
	if leader == 0 || leader == task.SourceNode {
		return false
	}
	if !containsNode(assignment.DesiredPeers, leader) || !containsVoter(status.CurrentVoters, leader) {
		return false
	}
	return len(status.CurrentVoters) >= len(assignment.DesiredPeers)/2+1
}
```

`Reconcile` must:

1. ignore non-`leader_transfer` tasks
2. complete immediately when `legalCompletedLeader` is already true
3. do nothing when this node is not the current Slot leader
4. call `TransferLeadership(ctx, slotID, targetNode)` only from the current Slot leader
5. poll status until a legal non-source leader appears
6. call `CompleteTask` on success
7. call `FailTask` with `leader transfer timed out` on bounded timeout

- [ ] **Step 5: Wire Slot runtime transfer and composite executor**

In `pkg/clusterv2/slots/types.go`, add to `Runtime`:

```go
// TransferLeadership asks Slot Raft to transfer leadership to target.
TransferLeadership(context.Context, multiraft.SlotID, multiraft.NodeID) error
```

In `pkg/clusterv2/default_slots.go`, wire:

```go
n.tasks = tasks.NewCompositeExecutor(
	tasks.NewBootstrapExecutor(tasks.BootstrapExecutorConfig{
		LocalNode: n.cfg.NodeID,
		Slots:     manager,
		Status:    runtime,
		Writer:    n.control,
	}),
	tasks.NewLeaderTransferExecutor(tasks.LeaderTransferExecutorConfig{
		LocalNode: n.cfg.NodeID,
		Runtime:   runtime,
		Writer:    n.control,
	}),
)
```

- [ ] **Step 6: Run task/clusterv2 tests**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/tasks ./pkg/clusterv2/slots ./pkg/clusterv2 -run 'TestLeaderTransferExecutor|TestBootstrapExecutor|TestNodeControlWatchTaskChangeRunsTaskExecutor|TestControlSnapshotChangesDetectTasks|TestSlotRaftStatusFromRuntimeIncludesCurrentVoters' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

Run:

```bash
git add pkg/clusterv2/tasks pkg/clusterv2/slots/types.go pkg/clusterv2/default_slots.go pkg/clusterv2/node_snapshot_test.go pkg/clusterv2/node_logs.go pkg/clusterv2/node_logs_test.go
git commit -m "feat: execute slot leader transfer tasks"
```

---

### Task 3: Management Usecase and Manager HTTP Route

**Files:**
- Create: `internalv2/usecase/management/slot_leader_transfer.go`
- Create: `internalv2/usecase/management/slot_leader_transfer_test.go`
- Modify: `internalv2/usecase/management/nodes.go`
- Modify: `internalv2/access/manager/server.go`
- Create: `internalv2/access/manager/slot_leader_transfer.go`
- Create: `internalv2/access/manager/slot_leader_transfer_test.go`
- Modify: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Write failing management usecase tests**

Create tests:

```go
func TestRequestSlotLeaderTransferCreatesControllerTask(t *testing.T)
func TestRequestSlotLeaderTransferAlreadyLeaderIsNoop(t *testing.T)
func TestRequestSlotLeaderTransferRejectsInvalidTargetAndUnavailablePorts(t *testing.T)
```

The success test must prove the writer receives:

```go
control.SlotLeaderTransferRequest{
	SlotID:        1,
	SourceNode:    1,
	TargetNode:    2,
	TargetPeers:   []uint64{1, 2, 3},
	ConfigEpoch:   7,
	StateRevision: 9,
}
```

The already-leader test must return `Created=false`, `Message="already leader"`, and must not call the writer.

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management -run TestRequestSlotLeaderTransfer -count=1
```

Expected: FAIL because usecase types and ports do not exist.

- [ ] **Step 2: Add management usecase ports and DTOs**

In `internalv2/usecase/management/nodes.go`, add to `Options` and `App`:

```go
// LeaderTransfer creates Controller-backed Slot leader transfer tasks.
LeaderTransfer SlotLeaderTransferWriter
// SlotRuntimeStatus reads local Slot Raft runtime status for transfer preflight.
SlotRuntimeStatus SlotRuntimeStatusReader
```

Create `internalv2/usecase/management/slot_leader_transfer.go`:

```go
var (
	// ErrSlotLeaderTransferUnavailable reports that task creation is not wired.
	ErrSlotLeaderTransferUnavailable = errors.New("internalv2/usecase/management: slot leader transfer unavailable")
	// ErrSlotRuntimeStatusUnavailable reports that local Slot Raft status is not wired.
	ErrSlotRuntimeStatusUnavailable = errors.New("internalv2/usecase/management: slot runtime status unavailable")
	// ErrSlotLeaderTransferSlotNotFound reports that the requested Slot is absent from the control snapshot.
	ErrSlotLeaderTransferSlotNotFound = errors.New("internalv2/usecase/management: slot leader transfer slot not found")
	// ErrSlotLeaderTransferConflict reports that an active task or unsafe runtime state blocks transfer creation.
	ErrSlotLeaderTransferConflict = errors.New("internalv2/usecase/management: slot leader transfer conflict")
)

const (
	SlotLeaderTransferMessageCreated       = "leader transfer task created"
	SlotLeaderTransferMessageAlreadyLeader = "already leader"
	SlotLeaderTransferMessageExistingTask  = "leader transfer task already exists"
)

type SlotLeaderTransferWriter interface {
	RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error)
}

type SlotRuntimeStatusReader interface {
	SlotRuntimeStatus(context.Context, uint32) (SlotRuntimeStatus, error)
}

type SlotRuntimeStatus struct {
	SlotID        uint32
	LeaderID      uint64
	CurrentVoters []uint64
}

type SlotLeaderTransferRequest struct {
	SlotID     uint32
	TargetNode uint64
}

type SlotLeaderTransferResponse struct {
	GeneratedAt     time.Time
	SlotID          uint32
	TargetNode      uint64
	PreferredLeader uint64
	ActualLeader    uint64
	Created         bool
	Task            *SlotTask
	Message         string
}
```

- [ ] **Step 3: Implement management validation and intent creation**

`RequestSlotLeaderTransfer` must:

```go
if req.SlotID == 0 || req.TargetNode == 0 {
	return SlotLeaderTransferResponse{}, metadb.ErrInvalidArgument
}
if a == nil || a.cluster == nil || a.leaderTransfer == nil {
	return SlotLeaderTransferResponse{}, ErrSlotLeaderTransferUnavailable
}
if a.slotRuntimeStatus == nil {
	return SlotLeaderTransferResponse{}, ErrSlotRuntimeStatusUnavailable
}
snapshot, err := a.cluster.LocalControlSnapshot(ctx)
assignment, ok := findControlSlotAssignment(snapshot.Slots, req.SlotID)
runtime, err := a.slotRuntimeStatus.SlotRuntimeStatus(ctx, req.SlotID)
```

Then return `ErrSlotLeaderTransferSlotNotFound` when `ok == false`.

After reading runtime status, inspect `snapshot.Tasks` for the requested Slot:

```go
existing := activeTaskForSlot(snapshot.Tasks, req.SlotID)
if existing != nil && existing.Kind == control.TaskKindLeaderTransfer && existing.TargetNode == req.TargetNode {
	return SlotLeaderTransferResponse{GeneratedAt: a.now(), SlotID: req.SlotID, TargetNode: req.TargetNode, PreferredLeader: assignment.PreferredLeader, ActualLeader: runtime.LeaderID, Created: false, Task: slotTaskFromControl(*existing), Message: SlotLeaderTransferMessageExistingTask}, nil
}
if existing != nil {
	return SlotLeaderTransferResponse{}, ErrSlotLeaderTransferConflict
}
```

Reject with `metadb.ErrInvalidArgument` when:

```go
len(assignment.DesiredPeers) < 2
!containsUint64(assignment.DesiredPeers, req.TargetNode)
!targetNodeAliveData(snapshot.Nodes, req.TargetNode)
```

Reject with `ErrSlotLeaderTransferConflict` when:

```go
runtime.LeaderID == 0
!containsUint64(runtime.CurrentVoters, runtime.LeaderID)
!containsUint64(runtime.CurrentVoters, req.TargetNode)
len(runtime.CurrentVoters) < len(assignment.DesiredPeers)/2+1
```

When `runtime.LeaderID == req.TargetNode`, return:

```go
SlotLeaderTransferResponse{
	GeneratedAt:     a.now(),
	SlotID:          req.SlotID,
	TargetNode:      req.TargetNode,
	PreferredLeader: assignment.PreferredLeader,
	ActualLeader:    runtime.LeaderID,
	Created:         false,
	Message:         SlotLeaderTransferMessageAlreadyLeader,
}
```

Otherwise call `a.leaderTransfer.RequestSlotLeaderTransfer` using the snapshot revision and assignment config epoch, and map `result.Task` with `slotTaskFromControl`.

- [ ] **Step 4: Write failing manager HTTP route tests**

Create tests:

```go
func TestManagerSlotLeaderTransferReturnsAcceptedTask(t *testing.T)
func TestManagerSlotLeaderTransferMapsErrors(t *testing.T)
func TestManagerSlotLeaderTransferRequiresSlotWritePermission(t *testing.T)
```

The success request is:

```go
POST /manager/slots/1/leader-transfer
{"target_node":2}
```

The created response must use HTTP `202 Accepted` and contain:

```json
{"slot_id":1,"target_node":2,"preferred_leader":2,"actual_leader":1,"created":true,"message":"leader transfer task created"}
```

Error mapping:

```go
metadb.ErrInvalidArgument                          -> 400 bad_request
management.ErrSlotLeaderTransferSlotNotFound       -> 404 not_found
management.ErrSlotLeaderTransferConflict           -> 409 conflict
management.ErrSlotLeaderTransferUnavailable        -> 503 service_unavailable
management.ErrSlotRuntimeStatusUnavailable         -> 503 service_unavailable
clusterv2.ErrNotStarted/ErrNotLeader/ErrStopping   -> 503 service_unavailable
```

Run:

```bash
GOWORK=off go test ./internalv2/access/manager -run TestManagerSlotLeaderTransfer -count=1
```

Expected: FAIL because the route and interface method do not exist.

- [ ] **Step 5: Add manager interface, route, and handler**

In `internalv2/access/manager/server.go`, add:

```go
// RequestSlotLeaderTransfer records one manual Slot leader transfer intent.
RequestSlotLeaderTransfer(context.Context, managementusecase.SlotLeaderTransferRequest) (managementusecase.SlotLeaderTransferResponse, error)
```

Register:

```go
slotWrites.POST("/slots/:slot_id/leader-transfer", s.handleSlotLeaderTransfer)
```

Create `internalv2/access/manager/slot_leader_transfer.go` with:

```go
type SlotLeaderTransferRequestDTO struct {
	// TargetNode is the requested preferred Slot leader.
	TargetNode uint64 `json:"target_node"`
}

type SlotLeaderTransferResponseDTO struct {
	GeneratedAt     string       `json:"generated_at"`
	SlotID          uint32       `json:"slot_id"`
	TargetNode      uint64       `json:"target_node"`
	PreferredLeader uint64       `json:"preferred_leader"`
	ActualLeader    uint64       `json:"actual_leader"`
	Created         bool         `json:"created"`
	Task            *SlotTaskDTO `json:"task,omitempty"`
	Message         string       `json:"message"`
}

func (s *Server) handleSlotLeaderTransfer(c *gin.Context) {
	slotID, err := parseRequiredLogSlotID(c.Param("slot_id"))
	var body SlotLeaderTransferRequestDTO
	if err != nil || c.ShouldBindJSON(&body) != nil || body.TargetNode == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot leader transfer request")
		return
	}
	resp, err := s.management.RequestSlotLeaderTransfer(c.Request.Context(), managementusecase.SlotLeaderTransferRequest{SlotID: slotID, TargetNode: body.TargetNode})
	if err != nil {
		writeSlotLeaderTransferError(c, err)
		return
	}
	status := http.StatusOK
	if resp.Created {
		status = http.StatusAccepted
	}
	c.JSON(status, slotLeaderTransferDTO(resp))
}
```

Extend `managerNodesStub` with request sink, response, and error fields, then implement `RequestSlotLeaderTransfer`.

- [ ] **Step 6: Run management and manager tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/management ./internalv2/access/manager -run 'TestRequestSlotLeaderTransfer|TestManagerSlotLeaderTransfer|TestManagerSlots|TestManagerSlotRaftCompact' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

Run:

```bash
git add internalv2/usecase/management internalv2/access/manager
git commit -m "feat: expose manager slot leader transfer intent"
```

---

### Task 4: Cluster Infra and App Wiring

**Files:**
- Create: `internalv2/infra/cluster/management_leader_transfer.go`
- Create: `internalv2/infra/cluster/management_leader_transfer_test.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing infra adapter tests**

Create `internalv2/infra/cluster/management_leader_transfer_test.go`:

```go
func TestManagementLeaderTransferAdapterUsesControlIntent(t *testing.T)
func TestManagementSlotRuntimeStatusReaderUsesLocalSlotStatus(t *testing.T)
```

The first test must verify `control.SlotLeaderTransferRequest.TargetNode == 2`.
The second test must verify conversion from:

```go
clusterv2.SlotRaftStatus{
	NodeID:        1,
	SlotID:        1,
	LeaderID:      1,
	CurrentVoters: []uint64{1, 2, 3},
}
```

to:

```go
management.SlotRuntimeStatus{
	SlotID:        1,
	LeaderID:      1,
	CurrentVoters: []uint64{1, 2, 3},
}
```

Run:

```bash
GOWORK=off go test ./internalv2/infra/cluster -run 'TestManagement.*LeaderTransfer|TestManagementSlotRuntimeStatus' -count=1
```

Expected: FAIL because the adapters do not exist.

- [ ] **Step 2: Implement infra adapters**

Create `internalv2/infra/cluster/management_leader_transfer.go`:

```go
package cluster

import (
	"context"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// ManagementLeaderTransferNode exposes Controller-backed leader transfer creation.
type ManagementLeaderTransferNode interface {
	RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error)
}

// ManagementSlotRuntimeStatusNode exposes local Slot Raft status needed before task creation.
type ManagementSlotRuntimeStatusNode interface {
	LocalSlotRaftStatus(context.Context, uint32) (clusterv2.SlotRaftStatus, error)
}

// ManagementLeaderTransferAdapter adapts clusterv2 control intents to management usecases.
type ManagementLeaderTransferAdapter struct {
	node ManagementLeaderTransferNode
}

func NewManagementLeaderTransferAdapter(node ManagementLeaderTransferNode) *ManagementLeaderTransferAdapter {
	return &ManagementLeaderTransferAdapter{node: node}
}

func (a *ManagementLeaderTransferAdapter) RequestSlotLeaderTransfer(ctx context.Context, req control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	if a == nil || a.node == nil {
		return control.SlotLeaderTransferResult{}, managementusecase.ErrSlotLeaderTransferUnavailable
	}
	return a.node.RequestSlotLeaderTransfer(ctx, req)
}

// ManagementSlotRuntimeStatusReader adapts local Slot Raft status to management usecases.
type ManagementSlotRuntimeStatusReader struct {
	node ManagementSlotRuntimeStatusNode
}

func NewManagementSlotRuntimeStatusReader(node ManagementSlotRuntimeStatusNode) *ManagementSlotRuntimeStatusReader {
	return &ManagementSlotRuntimeStatusReader{node: node}
}

func (r *ManagementSlotRuntimeStatusReader) SlotRuntimeStatus(ctx context.Context, slotID uint32) (managementusecase.SlotRuntimeStatus, error) {
	if r == nil || r.node == nil {
		return managementusecase.SlotRuntimeStatus{}, managementusecase.ErrSlotRuntimeStatusUnavailable
	}
	status, err := r.node.LocalSlotRaftStatus(ctx, slotID)
	if err != nil {
		return managementusecase.SlotRuntimeStatus{}, err
	}
	return managementusecase.SlotRuntimeStatus{
		SlotID:        status.SlotID,
		LeaderID:      status.LeaderID,
		CurrentVoters: append([]uint64(nil), status.CurrentVoters...),
	}, nil
}
```

- [ ] **Step 3: Write failing app wiring test**

In `internalv2/app/app_test.go`, add:

```go
func TestManagerServerRequestsSlotLeaderTransferFromClusterControl(t *testing.T)
```

The fake cluster must expose:

```go
func (f *fakeManagerCluster) RequestSlotLeaderTransfer(_ context.Context, req control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	f.slotLeaderTransferRequest = req
	return control.SlotLeaderTransferResult{
		Created: true,
		Task: &control.ReconcileTask{
			TaskID:      "slot-1-leader-transfer-7-r9",
			SlotID:      req.SlotID,
			Kind:        control.TaskKindLeaderTransfer,
			Step:        control.TaskStepTransferLeader,
			SourceNode:  req.SourceNode,
			TargetNode:  req.TargetNode,
			TargetPeers: append([]uint64(nil), req.TargetPeers...),
			ConfigEpoch: req.ConfigEpoch,
			Status:      control.TaskStatusPending,
		},
	}, nil
}
```

The test must start a manager server with `cluster.slot:w`, POST `{"target_node":2}` to `/manager/slots/1/leader-transfer`, and assert:

```go
cluster.slotLeaderTransferRequest.SourceNode == 1
cluster.slotLeaderTransferRequest.TargetNode == 2
cluster.slotLeaderTransferRequest.StateRevision == 9
```

- [ ] **Step 4: Wire app management ports**

In `internalv2/app/wiring.go`, update `newManagerManagement`:

```go
if slotRaftNode, ok := a.cluster.(clusterinfra.ManagementSlotRaftNode); ok {
	opts.SlotRaft = clusterinfra.NewManagementSlotRaftOperator(slotRaftNode)
	opts.SlotRuntimeStatus = clusterinfra.NewManagementSlotRuntimeStatusReader(slotRaftNode)
}
if leaderTransferNode, ok := a.cluster.(clusterinfra.ManagementLeaderTransferNode); ok {
	opts.LeaderTransfer = clusterinfra.NewManagementLeaderTransferAdapter(leaderTransferNode)
}
```

- [ ] **Step 5: Run infra/app tests**

Run:

```bash
GOWORK=off go test ./internalv2/infra/cluster ./internalv2/app -run 'TestManagement.*LeaderTransfer|TestManagementSlotRuntimeStatus|TestManagerServerRequestsSlotLeaderTransferFromClusterControl' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 4**

Run:

```bash
git add internalv2/infra/cluster/management_leader_transfer.go internalv2/infra/cluster/management_leader_transfer_test.go internalv2/app/wiring.go internalv2/app/app_test.go
git commit -m "feat: wire slot leader transfer manager ports"
```

---

### Task 5: E2EV2 Smoke, FLOW Docs, and Final Verification

**Files:**
- Create: `test/e2ev2/control/slot_leader_transfer/AGENTS.md`
- Create: `test/e2ev2/control/slot_leader_transfer/slot_leader_transfer_test.go`
- Modify: `test/e2ev2/AGENTS.md`
- Modify: `pkg/controllerv2/FLOW.md`
- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Add scenario AGENTS**

Create `test/e2ev2/control/slot_leader_transfer/AGENTS.md`:

```markdown
# test/e2ev2/control/slot_leader_transfer AGENTS

This scenario verifies manual Slot leader transfer through a real multi-node
`cmd/wukongimv2` cluster.

## Assertions

- Start real `cmd/wukongimv2` processes through `test/e2ev2/suite`.
- Trigger transfer only through public manager HTTP.
- Observe task completion and actual Slot Raft leadership through
  `/manager/slots?node_id=...`.
- Treat `target_node` as preferred leadership only; success is any legal
  non-source Slot Raft leader selected by Raft.
```

- [ ] **Step 2: Add failing e2ev2 smoke test**

Create `test/e2ev2/control/slot_leader_transfer/slot_leader_transfer_test.go` with one test:

```go
func TestThreeNodeSlotLeaderTransferCompletesAndClearsTask(t *testing.T)
```

The test flow must be:

```go
s := suite.New(t)
cluster := s.StartThreeNodeCluster(suite.WithManagerHTTP())
require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
initial := requireSlotsReady(t, ctx, cluster, cluster.MustNode(1))
slotID, source, target := chooseTransfer(initial)
accepted := postLeaderTransfer(t, ctx, cluster, slotID, target)
final := requireLeaderMoved(t, ctx, cluster, cluster.MustNode(1), slotID, source)
```

`chooseTransfer` must choose the first desired peer different from `node_log.leader_id`.

`postLeaderTransfer` must try each manager node until one accepts the request:

```go
_, err := suite.PostJSON(ctx,
	fmt.Sprintf("http://%s/manager/slots/%d/leader-transfer", node.ManagerAddr(), slotID),
	map[string]any{"target_node": target},
	&out,
)
```

`requireLeaderMoved` must poll `/manager/slots?node_id=1` until:

```go
item.Task == nil
item.NodeLog != nil
item.NodeLog.LeaderID != 0
item.NodeLog.LeaderID != source
```

The response DTOs must be local to this scenario package and must not import `internalv2`.

- [ ] **Step 3: Update e2ev2 catalog**

In `test/e2ev2/AGENTS.md`, add:

```markdown
| `control` | `test/e2ev2/control/slot_leader_transfer` | Prove a static three-node `cmd/wukongimv2` cluster accepts a manual Slot leader-transfer manager request, clears the ControllerV2 task, and observes a legal non-source Slot Raft leader. | `GOWORK=off go test -tags=e2e ./test/e2ev2/control/slot_leader_transfer -count=1 -timeout 2m` |
```

- [ ] **Step 4: Update FLOW docs**

Update only the relevant paragraphs:

```text
pkg/controllerv2/FLOW.md:
- ControllerV2 task state now includes leader_transfer tasks.
- Completed leader_transfer tasks are removed by complete_task; failed tasks remain active.

pkg/clusterv2/FLOW.md:
- Manager/control leader-transfer creation uses the existing control task RPC forwarding path when the receiving node is not Controller leader.
- Slot leader-transfer execution calls Slot Raft TransferLeadership from the current Slot leader and completes on any legal non-source actual leader.

internalv2/usecase/management/FLOW.md:
- Add Slot Leader Transfer Management Flow from manager handler to management usecase to clusterv2 control intent.
- Note that management validates current leader/voters only and does not inspect target match index.

internalv2/access/manager/FLOW.md:
- Add POST /manager/slots/:slot_id/leader-transfer requiring cluster.slot:w.
- Document response fields `target_node`, `preferred_leader`, `actual_leader`, `created`, and `message`.

internalv2/app/FLOW.md:
- Document wiring of LeaderTransfer and SlotRuntimeStatus ports when clusterv2 exposes them.
```

- [ ] **Step 5: Run focused unit tests**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/state ./pkg/controllerv2/command ./pkg/controllerv2/fsm ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2/tasks ./pkg/clusterv2/slots ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/infra/cluster ./internalv2/app -run 'LeaderTransfer|SlotLeaderTransfer|TaskRequest|SlotRaftStatusFromRuntimeIncludesCurrentVoters|TestManagerSlots|TestManagerSlotRaftCompact|TestBootstrapExecutor|TestNodeControlWatchTaskChangeRunsTaskExecutor|TestControlSnapshotChangesDetectTasks' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run e2ev2 smoke**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/control/slot_leader_transfer -count=1 -timeout 2m
```

Expected: PASS.

- [ ] **Step 7: Run final static checks**

Run:

```bash
gofmt -w pkg/controllerv2 pkg/clusterv2 internalv2/usecase/management internalv2/access/manager internalv2/infra/cluster internalv2/app test/e2ev2/control/slot_leader_transfer
git diff --check
rg -n "T[O]DO|T[B]D|FIX[M]E|standalone|单机|单节点部署|github.com/WuKongIM/WuKongIM/internal/" pkg/controllerv2 pkg/clusterv2 internalv2 test/e2ev2/control/slot_leader_transfer
```

Expected: `gofmt` changes only touched files, `git diff --check` exits 0, and the `rg` output contains no newly introduced forbidden wording or legacy imports.

- [ ] **Step 8: Commit Task 5**

Run:

```bash
git add test/e2ev2/control/slot_leader_transfer test/e2ev2/AGENTS.md pkg/controllerv2/FLOW.md pkg/clusterv2/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md internalv2/app/FLOW.md
git commit -m "test: cover slot leader transfer e2e"
```

- [ ] **Step 9: Final review**

Run:

```bash
git status --short
git log --oneline -5
```

Expected: only intentional changes remain, and the five task commits are visible at the top of the branch.
