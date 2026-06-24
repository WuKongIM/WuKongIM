# internalv2 Dynamic Node Lifecycle Stage 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add bounded Slot replica onboarding so activated dynamic data nodes can receive existing physical Slot replicas safely.

**Architecture:** Stage 3 keeps `DesiredPeers` as the committed voter set and represents the target as task-local staged state until learner catch-up and promotion are proven. The manager creates bounded onboarding tasks; `pkg/clusterv2/tasks` executes Slot Raft config changes through the Slot leader and commits the final assignment only after observed voters match the target set.

**Tech Stack:** Go, `pkg/slot/multiraft` config changes, `pkg/clusterv2/slots`, ControllerV2 task FSM, clusterv2 tasks executor, internalv2 manager onboarding APIs, e2ev2 SEND smoke.

---

## Scope

This plan implements only the "Stage 3: Slot Onboarding" section of:

- `docs/superpowers/specs/2026-06-24-internalv2-dynamic-node-lifecycle-design.md`
- Master index: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Previous stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage2.md`
- Next stage: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-stage4.md`

Stage 3 does not mark nodes `leaving`, does not drain gateway connections, and does not migrate historical Channel replicas. It moves physical Slot replicas only.

## Entry Gate

- [ ] Stage 2 is implemented and committed.
- [ ] Dynamic join e2ev2 passes:

```bash
go test ./test/e2ev2/cluster/dynamic_node_join -run TestDynamicJoinFourthDataNode -count=1
```

## File Structure

- Modify `pkg/slot/multiraft/types.go`
  - Extends `Status` with learners, Raft conf state, per-peer progress, and config-applied index.
- Modify `pkg/slot/multiraft/slot.go`
  - Populates the extended status fields from Raft status.
- Modify `pkg/slot/multiraft/step_test.go` and `pkg/slot/multiraft/control_test.go`
  - Verifies immutable learner/progress/conf-state status and config-change sequencing.
- Modify `pkg/clusterv2/slots/types.go`
  - Adds `ChangeConfig` to the runtime contract and exposes a richer status snapshot.
- Modify `pkg/clusterv2/slots/runtime.go`
  - Delegates `ChangeConfig` to `multiraft.Runtime`.
- Modify `pkg/clusterv2/slots/manager.go`
  - Adds `OpenLearner` so task executors can open target storage without calling `BootstrapSlot`.
- Modify `pkg/clusterv2/control/snapshot.go`
  - Adds `TaskKindSlotReplicaMove`, task steps, task-local target peer fields, and durable phase fence fields.
- Modify `pkg/controllerv2/state/types.go`, `pkg/controllerv2/state/validate.go`, `pkg/controllerv2/command/command.go`, `pkg/controllerv2/fsm/*`
  - Adds the `slot_replica_move` task kind, phase advance command, and final assignment update command while preserving `DesiredPeers` semantics.
- Create `pkg/controllerv2/runtime_slot_replica_move.go`
  - Adds `RequestSlotReplicaMove` to create a staged task without changing `DesiredPeers`.
- Create `pkg/clusterv2/control/slot_replica_move.go`
  - Adds control facade request/result types and forwarding.
- Create `pkg/clusterv2/tasks/slot_replica_move.go`
  - Executes AddLearner, PromoteLearner, optional leader transfer, RemoveVoter, and final assignment commit.
- Modify `pkg/clusterv2/default_slots.go`
  - Registers the Slot replica move executor in the composite executor.
- Create `internalv2/usecase/management/slot_onboarding.go`
  - Adds bounded onboarding plan/start/status/advance/cancel usecase methods.
- Create `internalv2/access/manager/slot_onboarding.go`
  - Adds manager onboarding routes.
- Modify `internalv2/access/manager/server.go`
  - Registers onboarding routes under `cluster.slot:w` for mutations and `cluster.slot:r` for status.
- Modify `internalv2/*/FLOW.md`
  - Documents task creation, executor ownership, and route responsibility.
- Create or extend `test/e2ev2/cluster/dynamic_node_join/slot_onboarding_test.go`
  - Verifies SEND stays available while one Slot replica moves.

## Task 1: Extend Slot Runtime Status And Config Contract

**Files:**
- Modify: `pkg/slot/multiraft/types.go`
- Modify: `pkg/slot/multiraft/slot.go`
- Modify: `pkg/slot/multiraft/step_test.go`
- Modify: `pkg/clusterv2/slots/types.go`
- Modify: `pkg/clusterv2/slots/runtime.go`

- [ ] **Step 1: Write failing multiraft status tests**

Add to `pkg/slot/multiraft/step_test.go`:

```go
func TestRuntimeStatusIncludesLearnersConfStateAndProgress(t *testing.T) {
	rt, slotID := newSingleNodeRuntime(t)
	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{Type: AddLearner, NodeID: 2})
	if err != nil {
		t.Fatalf("ChangeConfig(AddLearner) error = %v", err)
	}
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("ChangeConfig(AddLearner).Wait() error = %v", err)
	}

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !containsNodeID(st.CurrentLearners, 2) {
		t.Fatalf("CurrentLearners = %v, want learner 2", st.CurrentLearners)
	}
	if len(st.ConfState.Learners) == 0 {
		t.Fatalf("ConfState = %#v, want learners", st.ConfState)
	}
	if _, ok := st.Progress[2]; !ok {
		t.Fatalf("Progress = %#v, want peer 2", st.Progress)
	}
	if st.ConfigAppliedIndex == 0 {
		t.Fatalf("ConfigAppliedIndex = 0, want non-zero")
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./pkg/slot/multiraft -run TestRuntimeStatusIncludesLearnersConfStateAndProgress -count=1
```

Expected: FAIL because the extended `Status` fields are not defined.

- [ ] **Step 3: Extend multiraft status fields**

In `pkg/slot/multiraft/types.go`, extend `Status`:

```go
type PeerProgress struct {
	// Match is the highest log index known replicated on the peer.
	Match uint64
	// Next is the next log index the leader will send to the peer.
	Next uint64
	// State is the Raft progress state name for operator diagnostics.
	State string
}

type Status struct {
	SlotID           SlotID
	NodeID           NodeID
	LeaderID         NodeID
	CurrentVoters    []NodeID
	// CurrentLearners is the Raft learner set currently observed by this runtime.
	CurrentLearners  []NodeID
	// ConfState is the latest Raft configuration state observed by storage or Ready processing.
	ConfState        raftpb.ConfState
	Term             uint64
	CommitIndex      uint64
	AppliedIndex     uint64
	// ConfigAppliedIndex is the applied index at which the latest conf state was observed.
	ConfigAppliedIndex uint64
	// Progress stores leader-observed replication progress by peer.
	Progress         map[NodeID]PeerProgress
	Role             Role
}
```

Populate those fields in `pkg/slot/multiraft/slot.go` wherever `g.status` is refreshed from Raft status. Clone slices, maps, and `raftpb.ConfState` before publishing status.

- [ ] **Step 4: Extend clusterv2 Slot runtime contract**

In `pkg/clusterv2/slots/types.go`, add to `Runtime`:

```go
	// ChangeConfig submits a Slot Raft membership change through the local runtime.
	ChangeConfig(context.Context, multiraft.SlotID, multiraft.ConfigChange) (multiraft.Future, error)
```

In `pkg/clusterv2/slots/runtime.go`, add:

```go
// ChangeConfig submits a Slot Raft membership change.
func (a *Adapter) ChangeConfig(ctx context.Context, slotID multiraft.SlotID, change multiraft.ConfigChange) (multiraft.Future, error) {
	return a.runtime.ChangeConfig(ctx, slotID, change)
}
```

- [ ] **Step 5: Run Slot runtime tests and verify GREEN**

Run:

```bash
go test ./pkg/slot/multiraft ./pkg/clusterv2/slots -run 'TestRuntimeStatusIncludesLearnersConfStateAndProgress|TestChangeConfig' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Slot runtime contract**

```bash
git add pkg/slot/multiraft pkg/clusterv2/slots
git commit -m "feat: expose slot config progress status"
```

## Task 2: Add ControllerV2 Slot Replica Move Task Intent

**Files:**
- Modify: `pkg/controllerv2/state/types.go`
- Modify: `pkg/controllerv2/state/validate.go`
- Modify: `pkg/controllerv2/command/command.go`
- Modify: `pkg/controllerv2/fsm/mutation_handlers.go`
- Create: `pkg/controllerv2/runtime_slot_replica_move.go`
- Modify: `pkg/controllerv2/runtime_test.go`
- Modify: `pkg/clusterv2/control/snapshot.go`
- Create: `pkg/clusterv2/control/slot_replica_move.go`
- Modify: `pkg/clusterv2/control/codec.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/transport.go`

- [ ] **Step 1: Write failing ControllerV2 task tests**

Add to `pkg/controllerv2/runtime_test.go`:

```go
func TestRuntimeRequestSlotReplicaMoveCreatesTaskWithoutChangingDesiredPeers(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-slot-move")
	joinAndActivateNode(t, runtime, 4, "n4")
	before, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	assignment := before.Slots[0]
	source := assignment.DesiredPeers[0]

	result, err := runtime.RequestSlotReplicaMove(context.Background(), SlotReplicaMoveRequest{
		SlotID:        assignment.SlotID,
		SourceNode:    source,
		TargetNode:    4,
		TargetPeers:   replacePeer(assignment.DesiredPeers, source, 4),
		ConfigEpoch:   assignment.ConfigEpoch,
		StateRevision: before.Revision,
	})
	if err != nil {
		t.Fatalf("RequestSlotReplicaMove() error = %v", err)
	}
	if !result.Created || result.Task.Kind != TaskKindSlotReplicaMove {
		t.Fatalf("RequestSlotReplicaMove() = %#v, want slot_replica_move task", result)
	}

	after := waitForState(t, runtime, func(st ClusterState) bool {
		for _, task := range st.Tasks {
			if task.Kind == TaskKindSlotReplicaMove && task.TargetNode == 4 {
				return true
			}
		}
		return false
	})
	if !sameUint64Set(after.Slots[0].DesiredPeers, assignment.DesiredPeers) {
		t.Fatalf("DesiredPeers = %v, want unchanged %v", after.Slots[0].DesiredPeers, assignment.DesiredPeers)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./pkg/controllerv2 -run TestRuntimeRequestSlotReplicaMoveCreatesTaskWithoutChangingDesiredPeers -count=1
```

Expected: FAIL because `TaskKindSlotReplicaMove` and `RequestSlotReplicaMove` are not defined.

- [ ] **Step 3: Add task kind, steps, phase fences, and request method**

In `pkg/controllerv2/state/types.go`, add:

```go
	// TaskKindSlotReplicaMove moves one physical Slot voter from SourceNode to TargetNode.
	TaskKindSlotReplicaMove TaskKind = "slot_replica_move"
```

Add task steps:

```go
	TaskStepAddLearner      TaskStep = "add_learner"
	TaskStepPromoteLearner  TaskStep = "promote_learner"
	TaskStepRemoveVoter     TaskStep = "remove_voter"
	TaskStepCommitAssignment TaskStep = "commit_assignment"
```

Extend `state.ReconcileTask` with persistent phase fences:

```go
	// PhaseIndex advances after each externally observed Slot Raft config step.
	PhaseIndex uint32 `json:"phase_index,omitempty"`
	// ObservedConfigIndex is the Slot Raft applied index that proved the current phase.
	ObservedConfigIndex uint64 `json:"observed_config_index,omitempty"`
	// ObservedVoters stores the voter set observed for the current phase.
	ObservedVoters []uint64 `json:"observed_voters,omitempty"`
	// ObservedLearners stores the learner set observed for the current phase.
	ObservedLearners []uint64 `json:"observed_learners,omitempty"`
```

Create `pkg/controllerv2/runtime_slot_replica_move.go` with:

```go
// SlotReplicaMoveRequest describes a staged physical Slot replica move.
type SlotReplicaMoveRequest struct {
	SlotID        uint32
	SourceNode    uint64
	TargetNode    uint64
	TargetPeers   []uint64
	ConfigEpoch   uint64
	StateRevision uint64
}

// SlotReplicaMoveResult is returned after a move task intent is accepted.
type SlotReplicaMoveResult struct {
	Created bool
	Task    *ReconcileTask
}

// RequestSlotReplicaMove creates a staged move task without changing DesiredPeers.
func (r *Runtime) RequestSlotReplicaMove(ctx context.Context, req SlotReplicaMoveRequest) (SlotReplicaMoveResult, error) {
	taskID := fmt.Sprintf("slot-%d-replica-move-%d-to-%d-r%d", req.SlotID, req.SourceNode, req.TargetNode, req.StateRevision)
	task := state.ReconcileTask{
		TaskID:           taskID,
		SlotID:           req.SlotID,
		Kind:             state.TaskKindSlotReplicaMove,
		Step:             state.TaskStepAddLearner,
		SourceNode:       req.SourceNode,
		TargetNode:       req.TargetNode,
		TargetPeers:      append([]uint64(nil), req.TargetPeers...),
		CompletionPolicy: state.TaskCompletionPolicySingleObserver,
		ConfigEpoch:      req.ConfigEpoch,
		Status:           state.TaskStatusPending,
		PhaseIndex:       0,
	}
	expectedRevision := req.StateRevision
	if err := r.proposeTaskCommand(ctx, command.Command{
		Kind:             command.KindUpsertTask,
		ExpectedRevision: &expectedRevision,
		Task:             &task,
	}); err != nil {
		return SlotReplicaMoveResult{}, err
	}
	return SlotReplicaMoveResult{Created: true, Task: (*ReconcileTask)(&task)}, nil
}
```

If the command package keeps a single task upsert path, add `KindUpsertTask` and a matching FSM handler that only writes `Tasks`; it must not change `Slots`.

- [ ] **Step 4: Add fenced phase advance and final assignment commit commands**

Add a ControllerV2 command for phase progress:

```go
// KindAdvanceSlotReplicaMovePhase records the next safe Slot replica move phase.
KindAdvanceSlotReplicaMovePhase Kind = "advance_slot_replica_move_phase"
```

The command payload must carry:

```go
type SlotReplicaMovePhaseAdvance struct {
	TaskID              string
	SlotID              uint32
	ConfigEpoch         uint64
	Attempt             uint32
	ExpectedPhaseIndex  uint32
	NextStep            state.TaskStep
	ObservedConfigIndex uint64
	ObservedVoters      []uint64
	ObservedLearners    []uint64
}
```

The FSM handler must reject stale phase advances when task ID, attempt, config epoch, expected phase index, or task kind does not match. It must increment `PhaseIndex`, set `Step`, and persist the observed config index/voters/learners.

Add a ControllerV2 command for the executor's final phase:

```go
// KindCommitSlotReplicaMove atomically replaces the Slot assignment and completes the move task.
KindCommitSlotReplicaMove Kind = "commit_slot_replica_move"
```

The FSM handler must:

- require the active task ID, kind `slot_replica_move`, and matching attempt;
- require observed voters to equal `TargetPeers`;
- require the task step to be `commit_assignment`;
- require `ObservedConfigIndex` to be non-zero;
- replace `DesiredPeers` with `TargetPeers`;
- increment `ConfigEpoch`;
- remove the completed task.

- [ ] **Step 5: Add crash-resume FSM tests**

Add tests to `pkg/controllerv2/fsm/fsm_test.go`:

```go
func TestApplyAdvanceSlotReplicaMovePhasePersistsFence(t *testing.T) {
	sm := newLoadedStateMachine(t)
	task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
	applyOK(t, sm, 2, command.Command{Kind: command.KindUpsertTask, Task: &task})

	result, err := sm.Apply(context.Background(), 3, command.Command{
		Kind: command.KindAdvanceSlotReplicaMovePhase,
		SlotReplicaMovePhase: &command.SlotReplicaMovePhaseAdvance{
			TaskID: task.TaskID, SlotID: 1, ConfigEpoch: 7, Attempt: 0, ExpectedPhaseIndex: 0,
			NextStep: state.TaskStepPromoteLearner, ObservedConfigIndex: 33,
			ObservedVoters: []uint64{1, 2, 3}, ObservedLearners: []uint64{4},
		},
	})
	if err != nil || !result.Changed {
		t.Fatalf("Apply(advance phase) result=%#v err=%v, want changed", result, err)
	}
	got := sm.Snapshot(context.Background()).Tasks[0]
	if got.Step != state.TaskStepPromoteLearner || got.PhaseIndex != 1 || got.ObservedConfigIndex != 33 {
		t.Fatalf("task = %#v, want persisted promote phase fence", got)
	}
}

func TestApplyAdvanceSlotReplicaMovePhaseRejectsStalePhase(t *testing.T) {
	sm := newLoadedStateMachine(t)
	task := stagedSlotReplicaMoveTask("slot-1-replica-move-1-to-4-r9")
	task.PhaseIndex = 1
	applyOK(t, sm, 2, command.Command{Kind: command.KindUpsertTask, Task: &task})

	result, err := sm.Apply(context.Background(), 3, command.Command{
		Kind: command.KindAdvanceSlotReplicaMovePhase,
		SlotReplicaMovePhase: &command.SlotReplicaMovePhaseAdvance{
			TaskID: task.TaskID, SlotID: 1, ConfigEpoch: 7, Attempt: 0, ExpectedPhaseIndex: 0,
			NextStep: state.TaskStepRemoveVoter,
		},
	})
	if err != nil {
		t.Fatalf("Apply(stale phase) error = %v", err)
	}
	if !result.Rejected {
		t.Fatalf("Apply(stale phase) = %#v, want rejected", result)
	}
}
```

- [ ] **Step 6: Add clusterv2 control facade and forwarding**

Mirror the Stage 2 forwarding pattern:

```go
type SlotReplicaMoveRequest struct {
	SlotID        uint32
	SourceNode    uint64
	TargetNode    uint64
	TargetPeers   []uint64
	ConfigEpoch   uint64
	StateRevision uint64
}

type SlotReplicaMoveResult struct {
	Created bool
	Task    *ReconcileTask
}
```

Extend the Stage 2 generic control-write path instead of using task-result RPC:

- add `ControlWriteActionSlotReplicaMove = "slot_replica_move"` to `pkg/clusterv2/control/codec.go`;
- add `SlotReplicaMove *SlotReplicaMoveRequest` and `SlotReplicaMove *SlotReplicaMoveResult` to `ControlWriteRequest` and `ControlWriteResponse`;
- add `RequestSlotReplicaMove(context.Context, SlotReplicaMoveRequest) (SlotReplicaMoveResult, error)` to `ControlWriteApplier`;
- add `Runtime.RequestSlotReplicaMove` using `forwardControlWrite` when the local node is not Controller leader.

Do not add Slot replica move creation to `TaskApplier`; `TaskApplier` remains for task result/progress writes.

- [ ] **Step 7: Run ControllerV2 and control tests**

Run:

```bash
go test ./pkg/controllerv2 ./pkg/clusterv2/control -run 'TestRuntimeRequestSlotReplicaMove|TestApplyAdvanceSlotReplicaMovePhase|TestControlWriteRequest.*SlotReplicaMove' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit task intent layer**

```bash
git add pkg/controllerv2 pkg/clusterv2/control
git commit -m "feat: add staged slot replica move intent"
```

## Task 3: Implement Slot Replica Move Executor

**Files:**
- Create: `pkg/clusterv2/tasks/slot_replica_move.go`
- Create: `pkg/clusterv2/tasks/slot_replica_move_test.go`
- Modify: `pkg/clusterv2/default_slots.go`

- [ ] **Step 1: Write failing executor tests**

Create `pkg/clusterv2/tasks/slot_replica_move_test.go`:

```go
package tasks

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestSlotReplicaMoveExecutorAddsLearnerBeforeDesiredPeersChange(t *testing.T) {
	runtime := &fakeSlotReplicaMoveRuntime{status: multiraft.Status{
		SlotID:        1,
		NodeID:        1,
		LeaderID:      1,
		CurrentVoters: []multiraft.NodeID{1, 2, 3},
		Role:          multiraft.RoleLeader,
	}}
	writer := &fakeSlotReplicaMoveWriter{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 1, Runtime: runtime, Writer: writer})

	snap := control.Snapshot{
		Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1}},
		Tasks: []control.ReconcileTask{{
			TaskID:      "slot-1-replica-move-1-to-4-r9",
			SlotID:      1,
			Kind:        control.TaskKindSlotReplicaMove,
			Step:        control.TaskStepAddLearner,
			SourceNode:  1,
			TargetNode:  4,
			TargetPeers: []uint64{2, 3, 4},
			ConfigEpoch: 7,
			Status:      control.TaskStatusPending,
		}},
	}
	if err := executor.Reconcile(context.Background(), snap); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := runtime.changes[0]; got.Type != multiraft.AddLearner || got.NodeID != 4 {
		t.Fatalf("first change = %#v, want AddLearner target 4", got)
	}
	if writer.completed {
		t.Fatal("completed before learner catch-up")
	}
}

func TestSlotReplicaMoveExecutorResumesFromPersistedPromotePhase(t *testing.T) {
	runtime := &fakeSlotReplicaMoveRuntime{status: multiraft.Status{
		SlotID:          1,
		NodeID:          1,
		LeaderID:        1,
		CurrentVoters:   []multiraft.NodeID{1, 2, 3},
		CurrentLearners: []multiraft.NodeID{4},
		CommitIndex:     44,
		Progress: map[multiraft.NodeID]multiraft.PeerProgress{
			4: {Match: 44},
		},
		Role: multiraft.RoleLeader,
	}}
	writer := &fakeSlotReplicaMoveWriter{}
	executor := NewSlotReplicaMoveExecutor(SlotReplicaMoveExecutorConfig{LocalNode: 1, Runtime: runtime, MoveWriter: writer})

	task := control.ReconcileTask{
		TaskID: "slot-1-replica-move-1-to-4-r9", SlotID: 1, Kind: control.TaskKindSlotReplicaMove,
		Step: control.TaskStepPromoteLearner, SourceNode: 1, TargetNode: 4,
		TargetPeers: []uint64{2, 3, 4}, ConfigEpoch: 7, PhaseIndex: 1,
	}
	if err := executor.Reconcile(context.Background(), control.Snapshot{Tasks: []control.ReconcileTask{task}}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := runtime.changes[0]; got.Type != multiraft.PromoteLearner || got.NodeID != 4 {
		t.Fatalf("change = %#v, want PromoteLearner target 4", got)
	}
	if writer.phase.ExpectedPhaseIndex != 1 || writer.phase.NextStep != control.TaskStepRemoveVoter {
		t.Fatalf("phase = %#v, want remove-voter phase fence", writer.phase)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./pkg/clusterv2/tasks -run TestSlotReplicaMoveExecutorAddsLearnerBeforeDesiredPeersChange -count=1
```

Expected: FAIL because the executor is not defined.

- [ ] **Step 3: Implement executor phases**

Create `pkg/clusterv2/tasks/slot_replica_move.go`:

```go
type SlotReplicaMoveRuntime interface {
	Status(multiraft.SlotID) (multiraft.Status, error)
	ChangeConfig(context.Context, multiraft.SlotID, multiraft.ConfigChange) (multiraft.Future, error)
	TransferLeadership(context.Context, multiraft.SlotID, multiraft.NodeID) error
}

type SlotReplicaMoveWriter interface {
	AdvanceSlotReplicaMovePhase(context.Context, control.SlotReplicaMovePhaseAdvance) error
	CommitSlotReplicaMove(context.Context, control.SlotReplicaMoveCommit) error
	FailTask(context.Context, cv2.TaskResult) error
}

type SlotReplicaMoveExecutorConfig struct {
	LocalNode uint64
	Runtime   SlotReplicaMoveRuntime
	MoveWriter SlotReplicaMoveWriter
	PollMax   int
	PollDelay  time.Duration
}

type SlotReplicaMoveExecutor struct {
	cfg SlotReplicaMoveExecutorConfig
}

func NewSlotReplicaMoveExecutor(cfg SlotReplicaMoveExecutorConfig) *SlotReplicaMoveExecutor {
	if cfg.PollMax == 0 {
		cfg.PollMax = 30
	}
	if cfg.PollDelay == 0 {
		cfg.PollDelay = 10 * time.Millisecond
	}
	return &SlotReplicaMoveExecutor{cfg: cfg}
}
```

`Reconcile` must:

- execute only `TaskKindSlotReplicaMove`;
- run only on the current Slot leader;
- use `task.Step` and `task.PhaseIndex` as the source of truth after restart;
- call `ChangeConfig(AddLearner)` while target is absent from voters, then call `MoveWriter.AdvanceSlotReplicaMovePhase` with `NextStep: TaskStepPromoteLearner`;
- wait until `CurrentLearners` contains target and target progress has caught up to `CommitIndex`, then call `ChangeConfig(PromoteLearner)` and persist `NextStep: TaskStepRemoveVoter`;
- transfer leadership away from `SourceNode` when the source is still leader, then call `ChangeConfig(RemoveVoter)` and persist `NextStep: TaskStepCommitAssignment`;
- call `MoveWriter.CommitSlotReplicaMove` only after observed voters equal `TargetPeers`, `ObservedConfigIndex` is non-zero, and the task step is `commit_assignment`;
- call `FailTask` with a bounded error when a phase cannot prove its fence.

- [ ] **Step 4: Wire executor into default slots**

In `pkg/clusterv2/default_slots.go`, add to the composite executor:

```go
tasks.NewSlotReplicaMoveExecutor(tasks.SlotReplicaMoveExecutorConfig{
	LocalNode: n.cfg.NodeID,
	Runtime:   runtime,
	MoveWriter: n.control,
}),
```

- [ ] **Step 5: Run task tests**

Run:

```bash
go test ./pkg/clusterv2/tasks -run 'TestSlotReplicaMoveExecutor' -count=1
go test ./pkg/clusterv2 -run TestNode -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit executor**

```bash
git add pkg/clusterv2/tasks pkg/clusterv2/default_slots.go
git commit -m "feat: execute staged slot replica moves"
```

## Task 4: Add Bounded Manager Onboarding APIs

**Files:**
- Create: `internalv2/usecase/management/slot_onboarding.go`
- Create: `internalv2/usecase/management/slot_onboarding_test.go`
- Create: `internalv2/access/manager/slot_onboarding.go`
- Create: `internalv2/access/manager/slot_onboarding_test.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/app/*`

- [ ] **Step 1: Write failing usecase plan test**

Create `internalv2/usecase/management/slot_onboarding_test.go`:

```go
func TestPlanNodeOnboardingSelectsBoundedSlotMoves(t *testing.T) {
	snap := control.Snapshot{
		Revision: 12,
		Nodes: []control.Node{
			{NodeID: 1, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1},
			{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, PreferredLeader: 2},
		},
	}
	app := NewApp(Options{Control: fakeControlSnapshotReader{snap: snap}})

	plan, err := app.PlanNodeOnboarding(context.Background(), NodeOnboardingPlanRequest{TargetNodeID: 4, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("PlanNodeOnboarding() error = %v", err)
	}
	if len(plan.Candidates) != 1 || plan.Candidates[0].TargetNodeID != 4 {
		t.Fatalf("plan = %#v, want one target-node candidate", plan)
	}
	if plan.StateRevision != 12 {
		t.Fatalf("StateRevision = %d, want 12", plan.StateRevision)
	}
}
```

- [ ] **Step 2: Implement usecase APIs**

Create `internalv2/usecase/management/slot_onboarding.go` with:

```go
const (
	DefaultMaxSlotMoves = 1
	MaxSlotMoves        = 5
)

type SlotReplicaMoveWriter interface {
	RequestSlotReplicaMove(context.Context, control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error)
}

type NodeOnboardingPlanRequest struct {
	TargetNodeID uint64
	MaxSlotMoves uint32
}

type NodeOnboardingCandidate struct {
	SlotID        uint32
	SourceNodeID  uint64
	TargetNodeID  uint64
	TargetPeers   []uint64
	ConfigEpoch   uint64
}

type NodeOnboardingPlanResponse struct {
	StateRevision uint64
	Candidates    []NodeOnboardingCandidate
	Skipped       []NodeOnboardingSkip
}
```

The planner must reject non-active targets, cap `MaxSlotMoves` to `1..5`, skip Slots with active tasks, and choose candidates in stable Slot ID order for the first implementation.

- [ ] **Step 3: Add manager routes**

Register:

```text
POST /manager/nodes/:node_id/onboarding/plan
POST /manager/nodes/:node_id/onboarding/start
GET  /manager/nodes/:node_id/onboarding/status
POST /manager/nodes/:node_id/onboarding/advance
POST /manager/nodes/:node_id/onboarding/cancel
```

`start` and `advance` call the same bounded executor path and create at most `max_slot_moves` new tasks per request. `cancel` marks pending onboarding tasks failed with reason `operator_cancelled` through the Controller task writer.

- [ ] **Step 4: Run manager tests**

Run:

```bash
go test ./internalv2/usecase/management -run 'TestPlanNodeOnboarding|TestStartNodeOnboarding|TestNodeOnboardingStatus' -count=1
go test ./internalv2/access/manager -run 'TestManagerNodeOnboarding' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit manager onboarding APIs**

```bash
git add internalv2/usecase/management internalv2/access/manager internalv2/app
git commit -m "feat: add bounded slot onboarding APIs"
```

## Task 5: Add e2ev2 SEND Continuity During One Slot Move

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_join/slot_onboarding_test.go`
- Modify: `test/e2ev2/suite/*`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Write e2ev2 scenario**

Create `test/e2ev2/cluster/dynamic_node_join/slot_onboarding_test.go`:

```go
func TestSlotReplicaMoveKeepsSendAvailable(t *testing.T) {
	cluster := suite.StartCluster(t, suite.ClusterConfig{Nodes: 3})
	manager := cluster.ManagerClient(t, 1)
	sender := cluster.Client(t, "sender")
	receiver := cluster.Client(t, "receiver")
	channel := suite.NewTestChannel(t, sender, receiver)

	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{NodeID: 4, Seeds: cluster.SeedAddrs(), JoinAddr: cluster.NodeAddr(4)})
	defer node4.Stop(t)
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	plan := manager.MustPlanOnboarding(t, 4, 1)
	if len(plan.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want one", plan.Candidates)
	}
	manager.MustStartOnboarding(t, 4, plan.StateRevision, 1)

	for i := 0; i < 100; i++ {
		suite.MustSendAndReceive(t, sender, receiver, channel, fmt.Sprintf("move-%03d", i))
	}
	manager.EventuallyOnboardingSafe(t, 4, 30*time.Second)
}
```

- [ ] **Step 2: Run e2ev2 and verify GREEN**

Run:

```bash
go test ./test/e2ev2/cluster/dynamic_node_join -run TestSlotReplicaMoveKeepsSendAvailable -count=1
```

Expected: PASS.

- [ ] **Step 3: Update FLOW docs**

Document:

```text
manager onboarding route
  -> management onboarding planner
  -> SlotReplicaMoveWriter
  -> ControllerV2 slot_replica_move task
  -> clusterv2 task executor
  -> Slot Raft ChangeConfig
  -> final ControllerV2 assignment commit
```

State that target learners are not in `DesiredPeers` before promotion.

- [ ] **Step 4: Commit e2ev2 and docs**

```bash
git add test/e2ev2 internalv2/infra/cluster/FLOW.md internalv2/usecase/management/FLOW.md internalv2/access/manager/FLOW.md
git commit -m "test: verify slot onboarding send continuity"
```

## Exit Gate

- [ ] Run full Stage 3 verification:

```bash
go test ./pkg/slot/multiraft ./pkg/clusterv2/slots ./pkg/controllerv2 ./pkg/clusterv2/control ./pkg/clusterv2/tasks ./pkg/clusterv2
go test ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/app
go test ./test/e2ev2/cluster/dynamic_node_join -run 'TestDynamicJoinFourthDataNode|TestSlotReplicaMoveKeepsSendAvailable' -count=1
git diff --check
```

Expected: all commands pass.

- [ ] Confirm Stage 4 prerequisites:

```bash
rg -n "slot_replica_move|RequestSlotReplicaMove|TaskKindSlotReplicaMove|ChangeConfig|CurrentLearners|ConfigAppliedIndex" pkg internalv2 test/e2ev2
```

Expected: Slot replica movement is task-backed, staged, bounded, and observable.
