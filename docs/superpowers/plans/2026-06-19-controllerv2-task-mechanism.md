# ControllerV2 Task Mechanism Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first durable ControllerV2 task lifecycle for bootstrap tasks, including participant progress, fenced task result commands, control snapshot visibility, and manager read models.

**Architecture:** Extend the ControllerV2 durable state first, then make Raft commands apply fenced task progress/results, then expose those fields through the root `pkg/controllerv2` facade and `pkg/clusterv2/control`. Keep manager as read-only. Bootstrap execution uses the existing Slot reconciler/runtime observation path and reports participant progress before completing the active task.

**Tech Stack:** Go, ControllerV2 Raft commands, `pkg/clusterv2/control`, Slot Multi-Raft status, `gin` manager HTTP DTOs, package-level Go unit tests.

---

## Scope Boundaries

This plan implements the generic task mechanism only for bootstrap tasks. It must not add Slot leader transfer routes or call `pkg/slot/multiraft.Runtime.TransferLeadership`.

`cluster-state.json` remains the active control-plane state. Completed tasks are removed from `Tasks`; failed and pending tasks remain visible.

The plan avoids new configuration. No `wukongim.conf.example` update is expected.

## File Structure

- `pkg/controllerv2/state/types.go`: task completion policy and participant progress model.
- `pkg/controllerv2/state/normalize.go`: deterministic task defaults, progress sorting, deep clone.
- `pkg/controllerv2/state/validate.go`: durable task invariants.
- `pkg/controllerv2/command/command.go`: new task progress command payload and fenced result fields.
- `pkg/controllerv2/command/codec_test.go`: round-trip coverage for the new payloads.
- `pkg/controllerv2/fsm/mutations.go`: dispatch `report_task_progress`.
- `pkg/controllerv2/fsm/mutation_handlers.go`: apply progress, complete, and fail with fences.
- `pkg/controllerv2/fsm/mutation_guards.go`: stale result/progress idempotence.
- `pkg/controllerv2/fsm/fsm_test.go`: durable behavior tests.
- `pkg/controllerv2/planner/bootstrap.go`: bootstrap task initializes barrier progress.
- `pkg/controllerv2/runtime_tasks.go`: root facade task result APIs.
- `pkg/controllerv2/types.go`: public aliases for task policy/progress/result payloads.
- `pkg/controllerv2/FLOW.md`: document durable task command and lifecycle flow.
- `pkg/clusterv2/control/snapshot.go`: full task read model.
- `pkg/clusterv2/control/snapshot_clone.go`: deep clone progress fields.
- `pkg/clusterv2/control/snapshot_validate.go`: control snapshot task invariants.
- `pkg/clusterv2/control/controllerv2.go`: map ControllerV2 tasks to clusterv2 tasks.
- `pkg/clusterv2/control/controller.go`: task writer interface methods.
- `pkg/clusterv2/control/runtime.go`: delegate task result/progress writes.
- `pkg/clusterv2/control/static.go`: test controller records task writes.
- `pkg/clusterv2/control/codec.go`: task result forwarding codec.
- `pkg/clusterv2/control/transport.go`: task result RPC client and handler.
- `pkg/clusterv2/net/ids.go`: task result RPC service ID and alias.
- `pkg/clusterv2/node_defaults.go`: register control task result handler when default transport exists.
- `pkg/clusterv2/FLOW.md`: document task result forwarding and bootstrap executor flow.
- `pkg/clusterv2/tasks/bootstrap.go`: bootstrap participant executor.
- `pkg/clusterv2/tasks/bootstrap_test.go`: executor unit tests.
- `pkg/clusterv2/node.go`: task executor field and option for tests.
- `pkg/clusterv2/node_snapshot.go`: run executor on relevant task/snapshot changes.
- `internalv2/usecase/management/slots.go`: expose active task summaries on Slot rows.
- `internalv2/access/manager/slots.go`: include task summaries in JSON.
- `internalv2/usecase/management/FLOW.md`: document Slot task read model.
- `internalv2/access/manager/FLOW.md`: document read-only task visibility.

---

### Task 1: Durable Task State Model

**Files:**
- Modify: `pkg/controllerv2/state/types.go`
- Modify: `pkg/controllerv2/state/normalize.go`
- Modify: `pkg/controllerv2/state/validate.go`
- Modify: `pkg/controllerv2/state/state_test.go`
- Modify: `pkg/controllerv2/planner/bootstrap.go`
- Modify: `pkg/controllerv2/planner/bootstrap_test.go`
- Modify: `pkg/controllerv2/statefile/store_test.go`
- Modify: `pkg/controllerv2/sync/sync_test.go`

- [ ] **Step 1: Add failing state tests**

Add these tests to `pkg/controllerv2/state/state_test.go`:

```go
func TestValidateBootstrapTaskRequiresAllTargetPeerProgress(t *testing.T) {
	st := validState()
	st.Slots = []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}}
	st.Tasks = []ReconcileTask{{
		TaskID:           "slot-1-bootstrap-1",
		SlotID:           1,
		Kind:             TaskKindBootstrap,
		Step:             TaskStepCreateSlot,
		TargetNode:       1,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: TaskCompletionPolicyAllTargetPeers,
		ParticipantProgress: []TaskParticipantProgress{
			{NodeID: 1, Status: TaskParticipantStatusPending},
			{NodeID: 2, Status: TaskParticipantStatusPending},
		},
		ConfigEpoch: 1,
		Status:      TaskStatusPending,
	}}

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestNormalizeBootstrapTaskDefaultsParticipantProgress(t *testing.T) {
	st := validState()
	st.Slots = []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{3, 1, 2}, ConfigEpoch: 1, PreferredLeader: 1}}
	st.Tasks = []ReconcileTask{{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		Kind:        TaskKindBootstrap,
		Step:        TaskStepCreateSlot,
		TargetNode:  1,
		TargetPeers: []uint64{3, 1, 2},
		ConfigEpoch: 1,
		Status:      TaskStatusPending,
	}}

	st.Normalize()

	require.Equal(t, TaskCompletionPolicyAllTargetPeers, st.Tasks[0].CompletionPolicy)
	require.Equal(t, []TaskParticipantProgress{
		{NodeID: 1, Status: TaskParticipantStatusPending},
		{NodeID: 2, Status: TaskParticipantStatusPending},
		{NodeID: 3, Status: TaskParticipantStatusPending},
	}, st.Tasks[0].ParticipantProgress)
}

func TestClusterStateCloneCopiesParticipantProgress(t *testing.T) {
	st := validState()
	st.Slots = []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}}
	st.Tasks = []ReconcileTask{{
		TaskID:           "slot-1-bootstrap-1",
		SlotID:           1,
		Kind:             TaskKindBootstrap,
		Step:             TaskStepCreateSlot,
		TargetNode:       1,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: TaskCompletionPolicyAllTargetPeers,
		ParticipantProgress: []TaskParticipantProgress{
			{NodeID: 1, Status: TaskParticipantStatusPending},
		},
		ConfigEpoch: 1,
		Status:      TaskStatusPending,
	}}

	clone := st.Clone()
	clone.Tasks[0].ParticipantProgress[0].Status = TaskParticipantStatusDone

	require.Equal(t, TaskParticipantStatusPending, st.Tasks[0].ParticipantProgress[0].Status)
}
```

- [ ] **Step 2: Run state tests and verify failure**

Run:

```bash
go test ./pkg/controllerv2/state
```

Expected: FAIL because `TaskCompletionPolicy`, `TaskParticipantProgress`, and participant statuses are not defined.

- [ ] **Step 3: Add task policy and participant model**

In `pkg/controllerv2/state/types.go`, add these declarations after `TaskStatus`:

```go
// TaskCompletionPolicy describes how participant progress becomes task completion.
type TaskCompletionPolicy string

const (
	// TaskCompletionPolicySingleObserver means one eligible executor can complete the task.
	TaskCompletionPolicySingleObserver TaskCompletionPolicy = "single_observer"
	// TaskCompletionPolicyAllTargetPeers requires every target peer to report done.
	TaskCompletionPolicyAllTargetPeers TaskCompletionPolicy = "all_target_peers"
)

// TaskParticipantStatus describes one node's local task progress.
type TaskParticipantStatus string

const (
	// TaskParticipantStatusPending means the participant has not completed its local work.
	TaskParticipantStatusPending TaskParticipantStatus = "pending"
	// TaskParticipantStatusDone means the participant completed its local work.
	TaskParticipantStatusDone TaskParticipantStatus = "done"
	// TaskParticipantStatusFailed means the participant's latest local attempt failed.
	TaskParticipantStatusFailed TaskParticipantStatus = "failed"
)

// TaskParticipantProgress stores one participant's progress for the current global task attempt.
type TaskParticipantProgress struct {
	// NodeID is the participant node identity.
	NodeID uint64 `json:"node_id"`
	// Attempt counts this participant's local attempts within the current global task attempt.
	Attempt uint32 `json:"attempt"`
	// Status is the participant's current local progress.
	Status TaskParticipantStatus `json:"status"`
	// LastError stores the bounded error from the latest failed local attempt.
	LastError string `json:"last_error,omitempty"`
}
```

Extend `ReconcileTask`:

```go
	// CompletionPolicy controls how participant progress gates global completion.
	CompletionPolicy TaskCompletionPolicy `json:"completion_policy,omitempty"`
	// ParticipantProgress records per-node local progress for barrier-style tasks.
	ParticipantProgress []TaskParticipantProgress `json:"participant_progress,omitempty"`
```

Place the new fields after `TargetPeers`.

- [ ] **Step 4: Normalize task defaults and deep clone progress**

In `pkg/controllerv2/state/normalize.go`, update the task loop:

```go
	for i := range s.Tasks {
		sort.Slice(s.Tasks[i].TargetPeers, func(a, b int) bool { return s.Tasks[i].TargetPeers[a] < s.Tasks[i].TargetPeers[b] })
		normalizeTaskProgress(&s.Tasks[i])
	}
```

Add helpers:

```go
func normalizeTaskProgress(task *ReconcileTask) {
	if task == nil {
		return
	}
	if task.Kind == TaskKindBootstrap && task.CompletionPolicy == "" {
		task.CompletionPolicy = TaskCompletionPolicyAllTargetPeers
	}
	if task.CompletionPolicy == TaskCompletionPolicyAllTargetPeers && len(task.ParticipantProgress) == 0 {
		task.ParticipantProgress = make([]TaskParticipantProgress, 0, len(task.TargetPeers))
		for _, peerID := range task.TargetPeers {
			task.ParticipantProgress = append(task.ParticipantProgress, TaskParticipantProgress{NodeID: peerID, Status: TaskParticipantStatusPending})
		}
	}
	sort.Slice(task.ParticipantProgress, func(i, j int) bool {
		return task.ParticipantProgress[i].NodeID < task.ParticipantProgress[j].NodeID
	})
}
```

In `ClusterState.Clone`, after cloning `TargetPeers`, add:

```go
		out.Tasks[i].ParticipantProgress = cloneSlice(s.Tasks[i].ParticipantProgress)
```

- [ ] **Step 5: Validate task policy and participant progress**

In `pkg/controllerv2/state/validate.go`, inside `validateTasks`, after the common task status validation, add:

```go
		if task.CompletionPolicy != TaskCompletionPolicySingleObserver && task.CompletionPolicy != TaskCompletionPolicyAllTargetPeers {
			return invalid("unknown task completion_policy")
		}
		if err := validateParticipantProgress(task); err != nil {
			return err
		}
```

Add helper:

```go
func validateParticipantProgress(task ReconcileTask) error {
	if task.CompletionPolicy == TaskCompletionPolicySingleObserver {
		if len(task.ParticipantProgress) != 0 {
			return invalid("single_observer task must not have participant progress")
		}
		return nil
	}
	if task.CompletionPolicy != TaskCompletionPolicyAllTargetPeers {
		return invalid("unknown task completion_policy")
	}
	if len(task.ParticipantProgress) != len(task.TargetPeers) {
		return invalid("all_target_peers task progress must match target peers")
	}
	targets := make(map[uint64]struct{}, len(task.TargetPeers))
	for _, peerID := range task.TargetPeers {
		targets[peerID] = struct{}{}
	}
	seen := make(map[uint64]struct{}, len(task.ParticipantProgress))
	for _, progress := range task.ParticipantProgress {
		if progress.NodeID == 0 {
			return invalid("task participant node_id must be non-zero")
		}
		if _, ok := targets[progress.NodeID]; !ok {
			return invalid("task participant must be a target peer")
		}
		if _, exists := seen[progress.NodeID]; exists {
			return invalid("duplicate task participant")
		}
		seen[progress.NodeID] = struct{}{}
		switch progress.Status {
		case TaskParticipantStatusPending, TaskParticipantStatusDone, TaskParticipantStatusFailed:
		default:
			return invalid("unknown task participant status")
		}
	}
	return nil
}
```

- [ ] **Step 6: Initialize bootstrap participant progress in planner**

In `pkg/controllerv2/planner/bootstrap.go`, extend the created task:

```go
		CompletionPolicy:    state.TaskCompletionPolicyAllTargetPeers,
		ParticipantProgress: bootstrapParticipantProgress(peers),
```

Add helper:

```go
func bootstrapParticipantProgress(peers []uint64) []state.TaskParticipantProgress {
	out := make([]state.TaskParticipantProgress, 0, len(peers))
	for _, peerID := range peers {
		out = append(out, state.TaskParticipantProgress{NodeID: peerID, Status: state.TaskParticipantStatusPending})
	}
	return out
}
```

Update `TestBootstrapPlannerCommandContainsExpectedRevisionAssignmentAndTask` and `testBootstrapTask` expected tasks with the new fields.

- [ ] **Step 7: Update statefile and sync fixtures**

In `pkg/controllerv2/statefile/store_test.go` and `pkg/controllerv2/sync/sync_test.go`, update literal bootstrap tasks with:

```go
CompletionPolicy: state.TaskCompletionPolicyAllTargetPeers,
ParticipantProgress: []state.TaskParticipantProgress{
	{NodeID: 1, Status: state.TaskParticipantStatusPending},
	{NodeID: 2, Status: state.TaskParticipantStatusPending},
	{NodeID: 3, Status: state.TaskParticipantStatusPending},
},
```

- [ ] **Step 8: Run state and planner tests**

Run:

```bash
go test ./pkg/controllerv2/state ./pkg/controllerv2/planner ./pkg/controllerv2/statefile ./pkg/controllerv2/sync
```

Expected: PASS.

- [ ] **Step 9: Commit durable state model**

```bash
git add pkg/controllerv2/state pkg/controllerv2/planner pkg/controllerv2/statefile pkg/controllerv2/sync
git commit -m "feat: add controllerv2 task participant state"
```

---

### Task 2: Fenced Task Commands And FSM Apply

**Files:**
- Modify: `pkg/controllerv2/command/command.go`
- Modify: `pkg/controllerv2/command/codec_test.go`
- Modify: `pkg/controllerv2/fsm/mutations.go`
- Modify: `pkg/controllerv2/fsm/mutation_handlers.go`
- Modify: `pkg/controllerv2/fsm/mutation_guards.go`
- Modify: `pkg/controllerv2/fsm/fsm_test.go`
- Modify: `pkg/controllerv2/FLOW.md`

- [ ] **Step 1: Add failing FSM tests**

Add these tests to `pkg/controllerv2/fsm/fsm_test.go`:

```go
func TestApplyReportTaskProgressUpdatesOnlyParticipant(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))

	result, err := sm.Apply(ctx, 3, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           state.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 0,
			Status:             state.TaskParticipantStatusDone,
			FinishedAt:         time.Now().UTC(),
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 3, AppliedRaftIndex: 3}, result)
	task := sm.Snapshot(ctx).Tasks[0]
	require.Equal(t, state.TaskParticipantStatusDone, participantStatus(task, 2))
	require.Equal(t, state.TaskParticipantStatusPending, participantStatus(task, 1))
	require.Equal(t, state.TaskParticipantStatusPending, participantStatus(task, 3))
}

func TestApplyReportTaskProgressRejectsUnexpectedParticipant(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))

	result, err := sm.Apply(ctx, 3, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:            "slot-1-bootstrap-1",
			SlotID:            1,
			TaskKind:          state.TaskKindBootstrap,
			ConfigEpoch:       1,
			TaskAttempt:       0,
			ParticipantNodeID: 9,
			Status:            state.TaskParticipantStatusDone,
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonTaskParticipantUnexpected, Revision: 2, AppliedRaftIndex: 3}, result)
}

func TestApplyReportTaskProgressStaleParticipantAttemptNoops(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	applyOK(t, sm, 3, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           state.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 0,
			Status:             state.TaskParticipantStatusFailed,
			Err:                "first failure",
		},
	})

	result, err := sm.Apply(ctx, 4, command.Command{
		Kind: command.KindReportTaskProgress,
		TaskProgress: &command.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           state.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 0,
			Status:             state.TaskParticipantStatusDone,
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Noop: true, Reason: ReasonTaskParticipantAttemptStale, Revision: 3, AppliedRaftIndex: 4}, result)
	require.Equal(t, state.TaskParticipantStatusFailed, participantStatus(sm.Snapshot(ctx).Tasks[0], 2))
}

func TestApplyCompleteTaskStaleAttemptNoops(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	applyOK(t, sm, 3, command.Command{Kind: command.KindFailTask, TaskResult: &command.TaskResult{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		TaskKind:    state.TaskKindBootstrap,
		ConfigEpoch: 1,
		Attempt:     0,
		Err:         "global failure",
	}})

	result, err := sm.Apply(ctx, 4, command.Command{Kind: command.KindCompleteTask, TaskResult: &command.TaskResult{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		TaskKind:    state.TaskKindBootstrap,
		ConfigEpoch: 1,
		Attempt:     0,
	}})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Noop: true, Reason: ReasonTaskAttemptMismatch, Revision: 3, AppliedRaftIndex: 4}, result)
	require.Len(t, sm.Snapshot(ctx).Tasks, 1)
}
```

Add helper near test helpers:

```go
func participantStatus(task state.ReconcileTask, nodeID uint64) state.TaskParticipantStatus {
	for _, progress := range task.ParticipantProgress {
		if progress.NodeID == nodeID {
			return progress.Status
		}
	}
	return ""
}
```

- [ ] **Step 2: Run FSM tests and verify failure**

Run:

```bash
go test ./pkg/controllerv2/fsm
```

Expected: FAIL because `KindReportTaskProgress`, `TaskProgress`, and new reason constants do not exist.

- [ ] **Step 3: Add command payloads**

In `pkg/controllerv2/command/command.go`, add command kind:

```go
	// KindReportTaskProgress records one participant's local progress.
	KindReportTaskProgress Kind = "report_task_progress"
```

Extend `Command`:

```go
	// TaskProgress records one participant's local progress for barrier tasks.
	TaskProgress *TaskProgress `json:"task_progress,omitempty"`
```

Extend `TaskResult`:

```go
	// TaskKind fences the result to the active task kind.
	TaskKind state.TaskKind `json:"task_kind,omitempty"`
	// ConfigEpoch fences the result to the active assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch,omitempty"`
	// Attempt fences the result to the active global task attempt.
	Attempt uint32 `json:"attempt"`
```

Add:

```go
// TaskProgress describes one participant's local progress for a barrier task.
type TaskProgress struct {
	// TaskID identifies the active task.
	TaskID string `json:"task_id"`
	// SlotID confirms the slot affected by the progress report.
	SlotID uint32 `json:"slot_id"`
	// TaskKind fences the report to the active task kind.
	TaskKind state.TaskKind `json:"task_kind"`
	// ConfigEpoch fences the report to the active assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// TaskAttempt fences the report to the active global task attempt.
	TaskAttempt uint32 `json:"task_attempt"`
	// ParticipantNodeID is the reporting participant.
	ParticipantNodeID uint64 `json:"participant_node_id"`
	// ParticipantAttempt fences the report to the participant's local attempt.
	ParticipantAttempt uint32 `json:"participant_attempt"`
	// Status is the participant's new progress status.
	Status state.TaskParticipantStatus `json:"status"`
	// Err contains the participant failure reason when Status is failed.
	Err string `json:"err,omitempty"`
	// FinishedAt records when the participant observed this progress state.
	FinishedAt time.Time `json:"finished_at,omitempty"`
}
```

- [ ] **Step 4: Update command codec round-trip test**

In `pkg/controllerv2/command/codec_test.go`, add a command fixture:

```go
{
	Kind:     KindReportTaskProgress,
	IssuedAt: now.Add(2 * time.Minute),
	TaskProgress: &TaskProgress{
		TaskID:             "slot-2-bootstrap-3",
		SlotID:             2,
		TaskKind:           state.TaskKindBootstrap,
		ConfigEpoch:        3,
		TaskAttempt:        1,
		ParticipantNodeID:  2,
		ParticipantAttempt: 0,
		Status:             state.TaskParticipantStatusDone,
		FinishedAt:         now.Add(2 * time.Minute),
	},
}
```

Also update the existing `TaskResult` fixture to include `TaskKind`, `ConfigEpoch`, and `Attempt`.

- [ ] **Step 5: Add FSM reason constants and dispatch**

In `pkg/controllerv2/fsm/mutations.go`, add reason constants:

```go
	// ReasonTaskKindMismatch marks a task result for the wrong active task kind.
	ReasonTaskKindMismatch = "task_kind_mismatch"
	// ReasonTaskEpochMismatch marks a task result for the wrong config epoch.
	ReasonTaskEpochMismatch = "task_epoch_mismatch"
	// ReasonTaskAttemptMismatch marks an obsolete task result for an older global attempt.
	ReasonTaskAttemptMismatch = "task_attempt_mismatch"
	// ReasonTaskParticipantUnexpected marks a progress report from a non-participant.
	ReasonTaskParticipantUnexpected = "task_participant_unexpected"
	// ReasonTaskParticipantAttemptStale marks an obsolete participant progress report.
	ReasonTaskParticipantAttemptStale = "task_participant_attempt_stale"
```

Add dispatch:

```go
	case command.KindReportTaskProgress:
		return sm.applyReportTaskProgress(next, cmd)
```

- [ ] **Step 6: Implement task result guards and progress apply**

In `pkg/controllerv2/fsm/mutation_handlers.go`, add helpers:

```go
func taskResultGuard(task state.ReconcileTask, result *command.TaskResult) ApplyResult {
	if result == nil || result.TaskID == "" || result.SlotID == 0 || result.TaskKind == "" || result.ConfigEpoch == 0 {
		return reject(ReasonInvalidTaskResult)
	}
	if result.SlotID != task.SlotID {
		return reject(ReasonTaskSlotMismatch)
	}
	if result.TaskKind != task.Kind {
		return noop(ReasonTaskKindMismatch)
	}
	if result.ConfigEpoch != task.ConfigEpoch {
		return noop(ReasonTaskEpochMismatch)
	}
	if result.Attempt != task.Attempt {
		return noop(ReasonTaskAttemptMismatch)
	}
	return ApplyResult{}
}

func taskProgressGuard(task state.ReconcileTask, progress *command.TaskProgress) ApplyResult {
	if progress == nil || progress.TaskID == "" || progress.SlotID == 0 || progress.TaskKind == "" || progress.ConfigEpoch == 0 || progress.ParticipantNodeID == 0 {
		return reject(ReasonInvalidTaskResult)
	}
	if progress.SlotID != task.SlotID {
		return reject(ReasonTaskSlotMismatch)
	}
	if progress.TaskKind != task.Kind {
		return noop(ReasonTaskKindMismatch)
	}
	if progress.ConfigEpoch != task.ConfigEpoch {
		return noop(ReasonTaskEpochMismatch)
	}
	if progress.TaskAttempt != task.Attempt {
		return noop(ReasonTaskAttemptMismatch)
	}
	switch progress.Status {
	case state.TaskParticipantStatusPending, state.TaskParticipantStatusDone, state.TaskParticipantStatusFailed:
	default:
		return reject(ReasonInvalidTaskResult)
	}
	return ApplyResult{}
}
```

Update `applyCompleteTask` and `applyFailTask` to call `taskResultGuard` after locating the task. Treat guard noops and rejects as final results.

Add `applyReportTaskProgress`:

```go
func (sm *StateMachine) applyReportTaskProgress(next *state.ClusterState, cmd command.Command) ApplyResult {
	if next.Revision == 0 || cmd.TaskProgress == nil || cmd.TaskProgress.TaskID == "" {
		return reject(ReasonInvalidTaskResult)
	}
	idx := findTaskByID(next.Tasks, cmd.TaskProgress.TaskID)
	if idx < 0 {
		return noop(ReasonTaskMissing)
	}
	if guard := taskProgressGuard(next.Tasks[idx], cmd.TaskProgress); guard != (ApplyResult{}) {
		return guard
	}
	participantIdx := findParticipant(next.Tasks[idx].ParticipantProgress, cmd.TaskProgress.ParticipantNodeID)
	if participantIdx < 0 {
		return reject(ReasonTaskParticipantUnexpected)
	}
	current := next.Tasks[idx].ParticipantProgress[participantIdx]
	if cmd.TaskProgress.ParticipantAttempt < current.Attempt {
		return noop(ReasonTaskParticipantAttemptStale)
	}
	if cmd.TaskProgress.ParticipantAttempt == current.Attempt && current.Status == state.TaskParticipantStatusFailed && cmd.TaskProgress.Status == state.TaskParticipantStatusDone {
		return noop(ReasonTaskParticipantAttemptStale)
	}
	before := next.Clone()
	next.Tasks[idx].ParticipantProgress[participantIdx].Status = cmd.TaskProgress.Status
	next.Tasks[idx].ParticipantProgress[participantIdx].Attempt = cmd.TaskProgress.ParticipantAttempt
	next.Tasks[idx].ParticipantProgress[participantIdx].LastError = ""
	if cmd.TaskProgress.Status == state.TaskParticipantStatusFailed {
		next.Tasks[idx].Status = state.TaskStatusFailed
		next.Tasks[idx].ParticipantProgress[participantIdx].Attempt++
		next.Tasks[idx].ParticipantProgress[participantIdx].LastError = truncateUTF8(cmd.TaskProgress.Err, MaxTaskLastErrorBytes)
	}
	next.Normalize()
	if reflect.DeepEqual(before.Tasks, next.Tasks) {
		return noop(ReasonNoChange)
	}
	return validateChanged(next, before, cmd)
}
```

Add helper:

```go
func findParticipant(items []state.TaskParticipantProgress, nodeID uint64) int {
	for i, item := range items {
		if item.NodeID == nodeID {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 7: Reset participant progress on global fail**

In `applyFailTask`, after incrementing `Attempt`, reset barrier progress:

```go
	next.Tasks[idx].Status = state.TaskStatusFailed
	next.Tasks[idx].Attempt++
	next.Tasks[idx].LastError = truncateUTF8(cmd.TaskResult.Err, MaxTaskLastErrorBytes)
	if next.Tasks[idx].CompletionPolicy == state.TaskCompletionPolicyAllTargetPeers {
		next.Tasks[idx].ParticipantProgress = nil
		for _, peerID := range next.Tasks[idx].TargetPeers {
			next.Tasks[idx].ParticipantProgress = append(next.Tasks[idx].ParticipantProgress, state.TaskParticipantProgress{NodeID: peerID, Status: state.TaskParticipantStatusPending})
		}
	}
```

- [ ] **Step 8: Update stale revision handling**

In `pkg/controllerv2/fsm/mutations.go`, handle `KindReportTaskProgress` like task results in the first revision-mismatch switch:

```go
	case command.KindReportTaskProgress:
		if stale, handled := handleTaskProgressRevisionMismatch(next, cmd); handled {
			return stale
		}
```

In `pkg/controllerv2/fsm/mutation_guards.go`, add:

```go
func handleTaskProgressRevisionMismatch(current *state.ClusterState, cmd command.Command) (ApplyResult, bool) {
	if cmd.ExpectedRevision == nil || *cmd.ExpectedRevision == current.Revision {
		return ApplyResult{}, false
	}
	if cmd.TaskProgress == nil || cmd.TaskProgress.TaskID == "" {
		return reject(ReasonInvalidTaskResult), true
	}
	if findTaskByID(current.Tasks, cmd.TaskProgress.TaskID) < 0 {
		return noop(ReasonTaskMissing), true
	}
	return reject(ReasonExpectedRevisionMismatch), true
}
```

- [ ] **Step 9: Update ControllerV2 flow doc**

In `pkg/controllerv2/FLOW.md`, update the command/FSM section with:

```text
Task progress and task result writes enter ControllerV2 as Raft commands. Results are fenced by task_id, slot_id, task kind, config_epoch, and global attempt. Barrier-style tasks also accept participant progress fenced by participant node and participant attempt. Completed tasks are removed from active cluster-state tasks; failed tasks remain active with bounded errors until a subsequent successful attempt or operator action.
```

- [ ] **Step 10: Run command and FSM tests**

Run:

```bash
go test ./pkg/controllerv2/command ./pkg/controllerv2/fsm
```

Expected: PASS.

- [ ] **Step 11: Commit command and FSM behavior**

```bash
git add pkg/controllerv2/command pkg/controllerv2/fsm pkg/controllerv2/FLOW.md
git commit -m "feat: apply fenced controllerv2 task results"
```

---

### Task 3: Root ControllerV2 Task Facade

**Files:**
- Create: `pkg/controllerv2/runtime_tasks.go`
- Modify: `pkg/controllerv2/types.go`
- Modify: `pkg/controllerv2/runtime_test.go`

- [ ] **Step 1: Add failing runtime facade tests**

Add to `pkg/controllerv2/runtime_test.go`:

```go
func TestRuntimeReportTaskProgressProposesCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runtime := newStartedSingleNodeRuntime(t, 1)
	waitForRuntimeSlots(t, runtime, 1)

	err := runtime.ReportTaskProgress(ctx, TaskProgress{
		TaskID:             "slot-1-bootstrap-1",
		SlotID:             1,
		TaskKind:           TaskKindBootstrap,
		ConfigEpoch:        1,
		TaskAttempt:        0,
		ParticipantNodeID:  1,
		ParticipantAttempt: 0,
		Status:             TaskParticipantStatusDone,
		FinishedAt:         time.Now().UTC(),
	})
	require.NoError(t, err)

	st, err := runtime.LocalState(ctx)
	require.NoError(t, err)
	require.Equal(t, TaskParticipantStatusDone, st.Tasks[0].ParticipantProgress[0].Status)
}

func TestRuntimeCompleteTaskProposesCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runtime := newStartedSingleNodeRuntime(t, 1)
	waitForRuntimeSlots(t, runtime, 1)

	err := runtime.CompleteTask(ctx, TaskResult{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		TaskKind:    TaskKindBootstrap,
		ConfigEpoch: 1,
		Attempt:     0,
		FinishedAt:  time.Now().UTC(),
	})
	require.NoError(t, err)

	st, err := runtime.LocalState(ctx)
	require.NoError(t, err)
	require.Empty(t, st.Tasks)
}
```

- [ ] **Step 2: Run runtime tests and verify failure**

Run:

```bash
go test ./pkg/controllerv2 -run 'TestRuntime(ReportTaskProgress|CompleteTask)ProposesCommand'
```

Expected: FAIL because runtime task facade methods are not defined.

- [ ] **Step 3: Expose public task aliases**

In `pkg/controllerv2/types.go`, add `command` import and aliases:

```go
	// TaskCompletionPolicy describes how participant progress gates completion.
	TaskCompletionPolicy = state.TaskCompletionPolicy
	// TaskParticipantStatus describes one node's local task progress.
	TaskParticipantStatus = state.TaskParticipantStatus
	// TaskParticipantProgress stores one participant's progress for the active task attempt.
	TaskParticipantProgress = state.TaskParticipantProgress
	// TaskResult describes a global task completion or failure result.
	TaskResult = command.TaskResult
	// TaskProgress describes one participant progress report.
	TaskProgress = command.TaskProgress
```

Add exported constants:

```go
	TaskCompletionPolicySingleObserver   = state.TaskCompletionPolicySingleObserver
	TaskCompletionPolicyAllTargetPeers   = state.TaskCompletionPolicyAllTargetPeers
	TaskParticipantStatusPending         = state.TaskParticipantStatusPending
	TaskParticipantStatusDone            = state.TaskParticipantStatusDone
	TaskParticipantStatusFailed          = state.TaskParticipantStatusFailed
```

- [ ] **Step 4: Implement runtime task facade**

Create `pkg/controllerv2/runtime_tasks.go`:

```go
package controllerv2

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
)

// CompleteTask proposes a fenced task completion command and waits for local apply.
func (r *Runtime) CompleteTask(ctx context.Context, result TaskResult) error {
	return r.proposeTaskCommand(ctx, command.Command{Kind: command.KindCompleteTask, TaskResult: &result})
}

// FailTask proposes a fenced global task failure command and waits for local apply.
func (r *Runtime) FailTask(ctx context.Context, result TaskResult) error {
	return r.proposeTaskCommand(ctx, command.Command{Kind: command.KindFailTask, TaskResult: &result})
}

// ReportTaskProgress proposes one participant's fenced progress report and waits for local apply.
func (r *Runtime) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	return r.proposeTaskCommand(ctx, command.Command{Kind: command.KindReportTaskProgress, TaskProgress: &progress})
}

func (r *Runtime) proposeTaskCommand(ctx context.Context, cmd command.Command) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.raft == nil {
		return ErrNotStarted
	}
	if !cmd.IssuedAt.IsZero() {
		cmd.IssuedAt = cmd.IssuedAt.UTC()
	} else if r.cfg.Now != nil {
		cmd.IssuedAt = r.cfg.Now().UTC()
	}
	return r.raft.Propose(ctx, cmd)
}
```

- [ ] **Step 5: Run runtime tests**

Run:

```bash
go test ./pkg/controllerv2 -run 'TestRuntime(ReportTaskProgress|CompleteTask)ProposesCommand'
```

Expected: PASS.

- [ ] **Step 6: Commit runtime facade**

```bash
git add pkg/controllerv2/runtime_tasks.go pkg/controllerv2/types.go pkg/controllerv2/runtime_test.go
git commit -m "feat: expose controllerv2 task result facade"
```

---

### Task 4: clusterv2 Control Snapshot And Task Writer

**Files:**
- Modify: `pkg/clusterv2/control/snapshot.go`
- Modify: `pkg/clusterv2/control/snapshot_clone.go`
- Modify: `pkg/clusterv2/control/snapshot_validate.go`
- Modify: `pkg/clusterv2/control/controllerv2.go`
- Modify: `pkg/clusterv2/control/controllerv2_test.go`
- Modify: `pkg/clusterv2/control/controller.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/static.go`
- Modify: `pkg/clusterv2/control/control_test.go`
- Modify: `pkg/clusterv2/control/runtime_test.go`

- [ ] **Step 1: Add failing control snapshot mapping test**

In `pkg/clusterv2/control/controllerv2_test.go`, update `controllerV2State()` so its task contains:

```go
Step:             cv2.TaskStepCreateSlot,
SourceNode:       4,
TargetNode:       1,
TargetPeers:      []uint64{1, 2, 3},
CompletionPolicy: cv2.TaskCompletionPolicyAllTargetPeers,
ParticipantProgress: []cv2.TaskParticipantProgress{
	{NodeID: 1, Status: cv2.TaskParticipantStatusDone},
	{NodeID: 2, Status: cv2.TaskParticipantStatusPending},
	{NodeID: 3, Status: cv2.TaskParticipantStatusFailed, Attempt: 1, LastError: "open failed"},
},
ConfigEpoch: 2,
Attempt:     1,
Status:      cv2.TaskStatusFailed,
LastError:   "quorum missing",
```

Assert the mapped task:

```go
task := snap.Tasks[0]
if task.Step != TaskStepCreateSlot || task.SourceNode != 4 || task.Status != TaskStatusFailed || task.Attempt != 1 || task.LastError != "quorum missing" {
	t.Fatalf("mapped task = %#v, want full task read model", task)
}
if task.CompletionPolicy != TaskCompletionPolicyAllTargetPeers || len(task.ParticipantProgress) != 3 || task.ParticipantProgress[2].LastError != "open failed" {
	t.Fatalf("mapped participant progress = %#v", task.ParticipantProgress)
}
```

- [ ] **Step 2: Run control tests and verify failure**

Run:

```bash
go test ./pkg/clusterv2/control
```

Expected: FAIL because control task fields are missing.

- [ ] **Step 3: Extend control task types**

In `pkg/clusterv2/control/snapshot.go`, add task step/status/policy/progress types mirroring the public ControllerV2 values:

```go
// TaskStep identifies the current step inside a task workflow.
type TaskStep string

const (
	// TaskStepCreateSlot creates or verifies a physical Slot replica group.
	TaskStepCreateSlot TaskStep = "create_slot"
)

// TaskStatus describes whether a durable reconcile task is actionable.
type TaskStatus string

const (
	// TaskStatusPending means the task is waiting for a worker.
	TaskStatusPending TaskStatus = "pending"
	// TaskStatusRunning means the task is actively being attempted.
	TaskStatusRunning TaskStatus = "running"
	// TaskStatusFailed means the task remains active after a failed attempt.
	TaskStatusFailed TaskStatus = "failed"
)

// TaskCompletionPolicy describes how participant progress gates completion.
type TaskCompletionPolicy string

const (
	// TaskCompletionPolicySingleObserver means one eligible observer may complete the task.
	TaskCompletionPolicySingleObserver TaskCompletionPolicy = "single_observer"
	// TaskCompletionPolicyAllTargetPeers means every target peer must report done.
	TaskCompletionPolicyAllTargetPeers TaskCompletionPolicy = "all_target_peers"
)

// TaskParticipantStatus describes one node's local task progress.
type TaskParticipantStatus string

const (
	// TaskParticipantStatusPending means the participant is not complete.
	TaskParticipantStatusPending TaskParticipantStatus = "pending"
	// TaskParticipantStatusDone means the participant completed local work.
	TaskParticipantStatusDone TaskParticipantStatus = "done"
	// TaskParticipantStatusFailed means the participant's latest local attempt failed.
	TaskParticipantStatusFailed TaskParticipantStatus = "failed"
)

// TaskParticipantProgress describes one node's local progress.
type TaskParticipantProgress struct {
	// NodeID is the participant node identity.
	NodeID uint64
	// Attempt is the participant-local attempt fence.
	Attempt uint32
	// Status is the participant-local progress state.
	Status TaskParticipantStatus
	// LastError is the bounded participant error.
	LastError string
}
```

Extend `ReconcileTask` with `Step`, `SourceNode`, `CompletionPolicy`, `ParticipantProgress`, `Attempt`, `Status`, and `LastError`.

- [ ] **Step 4: Clone, validate, and map full task fields**

Update `Snapshot.Clone` to deep-copy `ParticipantProgress`.

Update `Snapshot.Validate` to require:

```go
if task.Kind == "" || task.Step == "" || task.Status == "" {
	return fmt.Errorf("control snapshot: invalid task")
}
if task.CompletionPolicy == TaskCompletionPolicyAllTargetPeers && len(task.ParticipantProgress) != len(task.TargetPeers) {
	return fmt.Errorf("control snapshot: task %q progress does not match target peers", task.TaskID)
}
```

Update `SnapshotFromControllerV2` task mapping:

```go
snap.Tasks = append(snap.Tasks, ReconcileTask{
	TaskID:              task.TaskID,
	SlotID:              task.SlotID,
	Kind:                TaskKind(task.Kind),
	Step:                TaskStep(task.Step),
	SourceNode:          task.SourceNode,
	TargetNode:          task.TargetNode,
	TargetPeers:         append([]uint64(nil), task.TargetPeers...),
	CompletionPolicy:    TaskCompletionPolicy(task.CompletionPolicy),
	ParticipantProgress: mapControllerV2ParticipantProgress(task.ParticipantProgress),
	ConfigEpoch:         task.ConfigEpoch,
	Attempt:             task.Attempt,
	Status:              TaskStatus(task.Status),
	LastError:           task.LastError,
})
```

Add `mapControllerV2ParticipantProgress`.

- [ ] **Step 5: Add task writer methods to control interface**

In `pkg/clusterv2/control/controller.go`, extend `Controller`:

```go
	// CompleteTask submits a fenced global task completion result.
	CompleteTask(context.Context, TaskResult) error
	// FailTask submits a fenced global task failure result.
	FailTask(context.Context, TaskResult) error
	// ReportTaskProgress submits one participant's fenced progress report.
	ReportTaskProgress(context.Context, TaskProgress) error
```

Add control-level aliases:

```go
type TaskResult = cv2.TaskResult
type TaskProgress = cv2.TaskProgress
```

Import the root `pkg/controllerv2` as `cv2`.

- [ ] **Step 6: Implement control Runtime and StaticController task writers**

In `pkg/clusterv2/control/runtime.go`:

```go
// CompleteTask submits a fenced global task completion result.
func (r *Runtime) CompleteTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	return r.backend.CompleteTask(ctx, result)
}

// FailTask submits a fenced global task failure result.
func (r *Runtime) FailTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	return r.backend.FailTask(ctx, result)
}

// ReportTaskProgress submits one participant's fenced progress report.
func (r *Runtime) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return cv2.ErrNotStarted
	}
	return r.backend.ReportTaskProgress(ctx, progress)
}
```

In `pkg/clusterv2/control/static.go`, add fields `CompletedTasks`, `FailedTasks`, and `ProgressReports`, and append to them from the new methods after checking `ctxErr`.

- [ ] **Step 7: Run control tests**

Run:

```bash
go test ./pkg/clusterv2/control
```

Expected: PASS.

- [ ] **Step 8: Commit control read/write model**

```bash
git add pkg/clusterv2/control
git commit -m "feat: expose clusterv2 task read and write model"
```

---

### Task 5: Control Task Result Forwarding

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Modify: `pkg/clusterv2/net/ids_test.go`
- Modify: `pkg/clusterv2/control/codec.go`
- Modify: `pkg/clusterv2/control/codec_test.go`
- Modify: `pkg/clusterv2/control/transport.go`
- Modify: `pkg/clusterv2/control/transport_test.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/node_defaults.go`

- [ ] **Step 1: Add failing task result transport tests**

In `pkg/clusterv2/control/codec_test.go`, add:

```go
func TestControlTaskRequestCodecRoundTrip(t *testing.T) {
	req := TaskRequest{
		Action: TaskActionProgress,
		Progress: cv2.TaskProgress{
			TaskID:             "slot-1-bootstrap-1",
			SlotID:             1,
			TaskKind:           cv2.TaskKindBootstrap,
			ConfigEpoch:        1,
			TaskAttempt:        0,
			ParticipantNodeID:  2,
			ParticipantAttempt: 0,
			Status:             cv2.TaskParticipantStatusDone,
		},
	}
	payload, err := EncodeTaskRequest(req)
	if err != nil {
		t.Fatalf("EncodeTaskRequest() error = %v", err)
	}
	got, err := DecodeTaskRequest(payload)
	if err != nil {
		t.Fatalf("DecodeTaskRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("DecodeTaskRequest() = %#v, want %#v", got, req)
	}
}
```

In `pkg/clusterv2/control/transport_test.go`, add:

```go
func TestTaskClientCallsRemoteHandler(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	applier := &recordingTaskApplier{}
	network.Register(1, clusternet.RPCControlTaskResult, NewTaskHandler(applier))

	client := NewTaskClient(network)
	err := client.SubmitTask(context.Background(), 1, TaskRequest{
		Action: TaskActionComplete,
		Result: cv2.TaskResult{
			TaskID:      "slot-1-bootstrap-1",
			SlotID:      1,
			TaskKind:    cv2.TaskKindBootstrap,
			ConfigEpoch: 1,
			Attempt:     0,
		},
	})

	if err != nil {
		t.Fatalf("SubmitTask() error = %v", err)
	}
	if len(applier.completed) != 1 || applier.completed[0].TaskID != "slot-1-bootstrap-1" {
		t.Fatalf("completed = %#v", applier.completed)
	}
}
```

Add this helper in the same test file:

```go
type recordingTaskApplier struct {
	completed []TaskResult
	failed    []TaskResult
	progress  []TaskProgress
}

func (a *recordingTaskApplier) CompleteTask(ctx context.Context, result TaskResult) error {
	a.completed = append(a.completed, result)
	return nil
}

func (a *recordingTaskApplier) FailTask(ctx context.Context, result TaskResult) error {
	a.failed = append(a.failed, result)
	return nil
}

func (a *recordingTaskApplier) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	a.progress = append(a.progress, progress)
	return nil
}
```

- [ ] **Step 2: Run transport tests and verify failure**

Run:

```bash
go test ./pkg/clusterv2/control ./pkg/clusterv2/net
```

Expected: FAIL because `RPCControlTaskResult`, task codec, and task client do not exist.

- [ ] **Step 3: Add RPC ID and alias**

In `pkg/clusterv2/net/ids.go`, add `RPCControlTaskResult` after `RPCControlRaft`.

Update `transportServiceAlias`:

```go
case RPCControlTaskResult:
	return "controller task result"
```

Update `servicePriority` to treat it as control priority:

```go
RPCSlotForwardPropose, RPCControlStateSync, RPCControlReportNode, RPCControlReportSlots, RPCControlTaskResult:
```

Update `pkg/clusterv2/net/ids_test.go` expected names with `"control_task_result": RPCControlTaskResult`.

- [ ] **Step 4: Add task request codec**

In `pkg/clusterv2/control/codec.go`, add a new kind:

```go
	controlKindTaskRequest
```

Add:

```go
type TaskAction string

const (
	TaskActionComplete TaskAction = "complete"
	TaskActionFail     TaskAction = "fail"
	TaskActionProgress TaskAction = "progress"
)

type TaskRequest struct {
	Action   TaskAction      `json:"action"`
	Result   cv2.TaskResult  `json:"result,omitempty"`
	Progress cv2.TaskProgress `json:"progress,omitempty"`
}

func EncodeTaskRequest(req TaskRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindTaskRequest)
	return append(out, payload...), nil
}

func DecodeTaskRequest(data []byte) (TaskRequest, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindTaskRequest)
	if err != nil {
		return TaskRequest{}, err
	}
	var req TaskRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return TaskRequest{}, err
	}
	return req, nil
}
```

- [ ] **Step 5: Add task client and handler**

In `pkg/clusterv2/control/transport.go`, add:

```go
type TaskApplier interface {
	CompleteTask(context.Context, TaskResult) error
	FailTask(context.Context, TaskResult) error
	ReportTaskProgress(context.Context, TaskProgress) error
}

type TaskClient struct {
	caller clusternet.Caller
}

func NewTaskClient(caller clusternet.Caller) *TaskClient {
	return &TaskClient{caller: caller}
}

func (c *TaskClient) SubmitTask(ctx context.Context, nodeID uint64, req TaskRequest) error {
	payload, err := EncodeTaskRequest(req)
	if err != nil {
		return err
	}
	_, err = clusternet.CallOwnedPayload(ctx, c.caller, nodeID, clusternet.RPCControlTaskResult, payload)
	return err
}

func NewTaskHandler(applier TaskApplier) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodeTaskRequest(payload)
		if err != nil {
			return nil, err
		}
		switch req.Action {
		case TaskActionComplete:
			return nil, applier.CompleteTask(ctx, req.Result)
		case TaskActionFail:
			return nil, applier.FailTask(ctx, req.Result)
		case TaskActionProgress:
			return nil, applier.ReportTaskProgress(ctx, req.Progress)
		default:
			return nil, fmt.Errorf("control task: unknown action %q", req.Action)
		}
	})
}
```

- [ ] **Step 6: Route Runtime task writes to leader when local propose says not leader**

Extend `RuntimeConfig` in `pkg/clusterv2/control/runtime.go`:

```go
	// TaskClient forwards task result commands to the current Controller leader.
	TaskClient *TaskClient
```

Add `errors` to the file imports.

Store it on `Runtime`:

```go
	taskClient *TaskClient
```

Initialize in `NewRuntime`.

Update `CompleteTask`, `FailTask`, and `ReportTaskProgress` to call `forwardTaskRequest` when the backend returns `cv2.ErrNotLeader`, or returns `cv2.ErrNotStarted` from a mirror runtime that cannot propose locally:

```go
func shouldForwardTaskWrite(err error) bool {
	return errors.Is(err, cv2.ErrNotLeader) || errors.Is(err, cv2.ErrNotStarted)
}

func (r *Runtime) forwardTaskRequest(ctx context.Context, req TaskRequest) error {
	if r == nil || r.taskClient == nil {
		return cv2.ErrNotLeader
	}
	leaderID := r.LeaderID()
	if leaderID == 0 || leaderID == r.cfg.NodeID {
		return cv2.ErrNotLeader
	}
	return r.taskClient.SubmitTask(ctx, leaderID, req)
}
```

For a mirror runtime where local backend cannot propose, the same forwarding path is used.

- [ ] **Step 7: Register handler from Node default control wiring**

In `pkg/clusterv2/node_defaults.go`, pass `TaskClient: control.NewTaskClient(n.transportClient)` when constructing the default control runtime.

Register:

```go
n.transportServer.Register(clusternet.RPCControlTaskResult, control.NewTaskHandler(runtime))
```

- [ ] **Step 8: Run forwarding tests**

Run:

```bash
go test ./pkg/clusterv2/control ./pkg/clusterv2/net ./pkg/clusterv2
```

Expected: PASS.

- [ ] **Step 9: Commit task result forwarding**

```bash
git add pkg/clusterv2/control pkg/clusterv2/net pkg/clusterv2/node_defaults.go
git commit -m "feat: forward controller task results"
```

---

### Task 6: Bootstrap Task Executor

**Files:**
- Create: `pkg/clusterv2/tasks/bootstrap.go`
- Create: `pkg/clusterv2/tasks/bootstrap_test.go`
- Modify: `pkg/clusterv2/node.go`
- Modify: `pkg/clusterv2/node_snapshot.go`
- Modify: `pkg/clusterv2/default_slots.go`
- Modify: `pkg/clusterv2/FLOW.md`

- [ ] **Step 1: Add failing bootstrap executor tests**

Create `pkg/clusterv2/tasks/bootstrap_test.go`:

```go
package tasks

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/slots"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestBootstrapExecutorReportsParticipantDoneAfterEnsure(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 2, Slots: manager, Status: status, Writer: writer})

	err := executor.Reconcile(context.Background(), bootstrapSnapshot())

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if manager.ensureCalls != 1 {
		t.Fatalf("ensure calls = %d, want 1", manager.ensureCalls)
	}
	if len(writer.progress) != 1 || writer.progress[0].ParticipantNodeID != 2 || writer.progress[0].Status != controllerv2.TaskParticipantStatusDone {
		t.Fatalf("progress = %#v, want node 2 done", writer.progress)
	}
}

func TestBootstrapExecutorCompletesAfterAllParticipantsDoneAndQuorumObserved(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 1, Slots: manager, Status: status, Writer: writer})

	err := executor.Reconcile(context.Background(), snapshot)

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(writer.completed) != 1 || writer.completed[0].TaskID != "slot-1-bootstrap-1" {
		t.Fatalf("completed = %#v", writer.completed)
	}
}

func TestBootstrapExecutorDoesNotCompleteWithoutQuorumObservation(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{err: multiraft.ErrSlotNotFound}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 1, Slots: manager, Status: status, Writer: writer})

	err := executor.Reconcile(context.Background(), snapshot)

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(writer.completed) != 0 {
		t.Fatalf("completed = %#v, want none", writer.completed)
	}
}
```

Add these helpers in the same test file:

```go
func bootstrapSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "n1", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "n2", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "n3", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 1, Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 1}}},
		Tasks: []control.ReconcileTask{{
			TaskID:           "slot-1-bootstrap-1",
			SlotID:           1,
			Kind:             control.TaskKindBootstrap,
			Step:             control.TaskStepCreateSlot,
			TargetNode:       1,
			TargetPeers:      []uint64{1, 2, 3},
			CompletionPolicy: control.TaskCompletionPolicyAllTargetPeers,
			ParticipantProgress: []control.TaskParticipantProgress{
				{NodeID: 1, Status: control.TaskParticipantStatusPending},
				{NodeID: 2, Status: control.TaskParticipantStatusPending},
				{NodeID: 3, Status: control.TaskParticipantStatusPending},
			},
			ConfigEpoch: 1,
			Status:      control.TaskStatusPending,
		}},
	}
}

type fakeSlotManager struct {
	ensureCalls int
	last        slots.Assignment
	err         error
}

func (f *fakeSlotManager) Ensure(ctx context.Context, assignment slots.Assignment) error {
	f.ensureCalls++
	f.last = assignment
	return f.err
}

type fakeStatusReader struct {
	status multiraft.Status
	err    error
}

func (f *fakeStatusReader) Status(slotID multiraft.SlotID) (multiraft.Status, error) {
	return f.status, f.err
}

type recordingWriter struct {
	completed []controllerv2.TaskResult
	failed    []controllerv2.TaskResult
	progress  []controllerv2.TaskProgress
}

func (w *recordingWriter) CompleteTask(ctx context.Context, result controllerv2.TaskResult) error {
	w.completed = append(w.completed, result)
	return nil
}

func (w *recordingWriter) FailTask(ctx context.Context, result controllerv2.TaskResult) error {
	w.failed = append(w.failed, result)
	return nil
}

func (w *recordingWriter) ReportTaskProgress(ctx context.Context, progress controllerv2.TaskProgress) error {
	w.progress = append(w.progress, progress)
	return nil
}
```

- [ ] **Step 2: Run executor tests and verify failure**

Run:

```bash
go test ./pkg/clusterv2/tasks
```

Expected: FAIL because the `tasks` package does not exist.

- [ ] **Step 3: Implement bootstrap executor**

Create `pkg/clusterv2/tasks/bootstrap.go`:

```go
package tasks

import (
	"context"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/slots"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type SlotManager interface {
	Ensure(context.Context, slots.Assignment) error
}

type StatusReader interface {
	Status(multiraft.SlotID) (multiraft.Status, error)
}

type Writer interface {
	CompleteTask(context.Context, cv2.TaskResult) error
	FailTask(context.Context, cv2.TaskResult) error
	ReportTaskProgress(context.Context, cv2.TaskProgress) error
}

type BootstrapExecutorConfig struct {
	LocalNode uint64
	Slots     SlotManager
	Status    StatusReader
	Writer    Writer
}

type BootstrapExecutor struct {
	cfg BootstrapExecutorConfig
}

func NewBootstrapExecutor(cfg BootstrapExecutorConfig) *BootstrapExecutor {
	return &BootstrapExecutor{cfg: cfg}
}

func (e *BootstrapExecutor) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if e == nil || e.cfg.LocalNode == 0 || e.cfg.Writer == nil {
		return nil
	}
	for _, task := range snapshot.Tasks {
		if task.Kind != control.TaskKindBootstrap || task.CompletionPolicy != control.TaskCompletionPolicyAllTargetPeers {
			continue
		}
		assignment, ok := findSlot(snapshot.Slots, task.SlotID)
		if !ok {
			continue
		}
		if containsNode(task.TargetPeers, e.cfg.LocalNode) && !participantDone(task, e.cfg.LocalNode) {
			if err := e.ensureLocal(ctx, snapshot.HashSlots, assignment); err != nil {
				return e.cfg.Writer.ReportTaskProgress(ctx, failedProgress(task, e.cfg.LocalNode, err.Error()))
			}
			if err := e.cfg.Writer.ReportTaskProgress(ctx, doneProgress(task, e.cfg.LocalNode)); err != nil {
				return err
			}
		}
		if allParticipantsDone(task) && e.observedConverged(task) {
			if err := e.cfg.Writer.CompleteTask(ctx, cv2.TaskResult{
				TaskID:      task.TaskID,
				SlotID:      task.SlotID,
				TaskKind:    cv2.TaskKind(task.Kind),
				ConfigEpoch: task.ConfigEpoch,
				Attempt:     task.Attempt,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
```

Implement helper behavior exactly as follows:

- `ensureLocal(ctx, table, assignment)` builds `slots.Assignment{SlotID, DesiredPeers, PreferredLeader, HashSlots}` from the control assignment and hash-slot table, then calls `e.cfg.Slots.Ensure`.
- `findSlot` returns the matching `control.SlotAssignment` by `SlotID`.
- `participantDone` returns true only when this node's participant status is `control.TaskParticipantStatusDone`.
- `allParticipantsDone` returns true only when the task has at least one participant and all participant statuses are done.
- `doneProgress` copies `TaskID`, `SlotID`, `TaskKind`, `ConfigEpoch`, `Attempt`, local `ParticipantNodeID`, current participant `Attempt`, and status done into `cv2.TaskProgress`.
- `failedProgress` copies the same fences, status failed, and the error string into `cv2.TaskProgress.Err`.
- `observedConverged` returns false when `e.cfg.Status` is nil or `Status` returns an error. Otherwise it requires sorted `status.CurrentVoters` to equal sorted `task.TargetPeers` and `status.LeaderID != 0`. It should compute quorum size as `len(voters)/2 + 1` and return false when fewer than quorum voters are present.

- [ ] **Step 4: Wire executor into Node snapshot application**

In `pkg/clusterv2/node.go`, add:

```go
type taskExecutor interface {
	Reconcile(context.Context, control.Snapshot) error
}
```

Add field:

```go
	tasks taskExecutor
```

Add test option:

```go
func withTaskExecutor(executor taskExecutor) Option {
	return func(n *Node) { n.tasks = executor }
}
```

In `pkg/clusterv2/node_snapshot.go`, after Slot reconciliation:

```go
	if n.tasks != nil && (firstSnapshot || changes.tasks || changes.slots) {
		if err := n.tasks.Reconcile(ctx, snapshot); err != nil {
			return err
		}
	}
```

If `snapshotChanges` does not yet expose `tasks`, extend it to compare `previous.Tasks` and `next.Tasks`.

- [ ] **Step 5: Construct default executor when default slots and control are present**

In `pkg/clusterv2/default_slots.go`, after `n.slots = slots.NewReconciler(n.cfg.NodeID, manager)`, assign:

```go
	if n.tasks == nil && n.control != nil {
		n.tasks = tasks.NewBootstrapExecutor(tasks.BootstrapExecutorConfig{
			LocalNode: n.cfg.NodeID,
			Slots:     manager,
			Status:    runtime,
			Writer:    n.control,
		})
	}
```

Import `github.com/WuKongIM/WuKongIM/pkg/clusterv2/tasks` in `default_slots.go`.

- [ ] **Step 6: Update clusterv2 flow doc**

In `pkg/clusterv2/FLOW.md`, update the control snapshot application flow with:

```text
When a control snapshot contains active bootstrap tasks, the Node runs the bootstrap task executor after Slot reconciliation. The executor only reports participant progress or fenced completion through the control task writer facade; it does not mutate ControllerV2 state directly. Task writes from non-leader Controller runtimes are forwarded to the current Controller leader.
```

- [ ] **Step 7: Run task and node tests**

Run:

```bash
go test ./pkg/clusterv2/tasks ./pkg/clusterv2
```

Expected: PASS.

- [ ] **Step 8: Commit bootstrap executor**

```bash
git add pkg/clusterv2/tasks pkg/clusterv2/node.go pkg/clusterv2/node_snapshot.go pkg/clusterv2/default_slots.go pkg/clusterv2/FLOW.md
git commit -m "feat: execute bootstrap task progress"
```

---

### Task 7: Manager Task Read Model

**Files:**
- Modify: `internalv2/usecase/management/slots.go`
- Modify: `internalv2/usecase/management/slots_test.go`
- Modify: `internalv2/access/manager/slots.go`
- Modify: `internalv2/access/manager/server_test.go`
- Modify: `internalv2/usecase/management/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Add failing management slot task tests**

In `internalv2/usecase/management/slots_test.go`, add a snapshot with an active bootstrap task and assert the Slot row includes task summary:

```go
func TestListSlotsIncludesActiveTaskProgress(t *testing.T) {
	app := New(AppConfig{
		Cluster: fakeClusterSnapshot(control.Snapshot{
			Revision: 1,
			Nodes: []control.Node{{NodeID: 1, Addr: "n1", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive}},
			Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1}},
			HashSlots: control.HashSlotTable{Revision: 1, Count: 1, Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 1}}},
			Tasks: []control.ReconcileTask{{
				TaskID:           "slot-1-bootstrap-1",
				SlotID:           1,
				Kind:             control.TaskKindBootstrap,
				Step:             control.TaskStepCreateSlot,
				TargetNode:       1,
				TargetPeers:      []uint64{1},
				CompletionPolicy: control.TaskCompletionPolicyAllTargetPeers,
				ParticipantProgress: []control.TaskParticipantProgress{
					{NodeID: 1, Status: control.TaskParticipantStatusPending},
				},
				ConfigEpoch: 1,
				Status:      control.TaskStatusPending,
			}},
		}),
	})

	items, err := app.ListSlots(context.Background(), ListSlotsOptions{})

	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].Task)
	require.Equal(t, "slot-1-bootstrap-1", items[0].Task.TaskID)
	require.Equal(t, "all_target_peers", items[0].Task.CompletionPolicy)
	require.Len(t, items[0].Task.Participants, 1)
}
```

- [ ] **Step 2: Add failing access DTO test**

In `internalv2/access/manager/server_test.go` or `slots` tests, assert `/manager/slots` returns `task.participants`.

Use JSON assertion:

```go
require.Equal(t, "slot-1-bootstrap-1", body.Items[0].Task.TaskID)
require.Equal(t, uint64(1), body.Items[0].Task.Participants[0].NodeID)
```

- [ ] **Step 3: Run manager tests and verify failure**

Run:

```bash
go test ./internalv2/usecase/management -run TestListSlotsIncludesActiveTaskProgress
go test ./internalv2/access/manager -run 'Test.*Slots'
```

Expected: FAIL because task DTOs are not present.

- [ ] **Step 4: Add management task DTOs**

In `internalv2/usecase/management/slots.go`, add:

```go
	// Task contains the active task summary for this Slot, when any.
	Task *SlotTask
```

Add:

```go
type SlotTask struct {
	TaskID           string
	Kind             string
	Step             string
	Status           string
	SourceNode       uint64
	TargetNode       uint64
	TargetPeers      []uint64
	CompletionPolicy string
	ConfigEpoch      uint64
	Attempt          uint32
	LastError        string
	Participants     []SlotTaskParticipant
}

type SlotTaskParticipant struct {
	NodeID    uint64
	Attempt   uint32
	Status    string
	LastError string
}
```

Add this `tasksBySlot` map in `ListSlots`, and pass the task into `slotFromControlAssignment`:

```go
tasksBySlot := make(map[uint32]*SlotTask, len(snapshot.Tasks))
for _, task := range snapshot.Tasks {
	if task.SlotID == 0 {
		continue
	}
	if _, exists := tasksBySlot[task.SlotID]; exists {
		continue
	}
	tasksBySlot[task.SlotID] = slotTaskFromControl(task)
}

for _, assignment := range snapshot.Slots {
	slot := slotFromControlAssignment(assignment, snapshot.HashSlots, generatedAt, tasksBySlot[assignment.SlotID])
	// existing filters and NodeLog enrichment stay unchanged
}
```

Change the mapper signature and set `Task`:

```go
func slotFromControlAssignment(assignment control.SlotAssignment, hashSlots control.HashSlotTable, generatedAt time.Time, task *SlotTask) Slot {
	runtime, hasRuntime := runtimeFromControlAssignment(assignment, generatedAt)
	return Slot{
		SlotID:    assignment.SlotID,
		HashSlots: slotHashSlotsFromControlTable(hashSlots, assignment.SlotID),
		Task:      task,
		State: SlotState{
			Quorum:      managerSlotQuorumState(hasRuntime, runtime.HasQuorum),
			Sync:        managerSlotSyncState(hasRuntime),
			LeaderMatch: assignment.PreferredLeader != 0 && assignment.PreferredLeader == runtime.LeaderID,
		},
		Assignment: SlotAssignment{
			DesiredPeers:    append([]uint64(nil), assignment.DesiredPeers...),
			PreferredLeader: assignment.PreferredLeader,
			ConfigEpoch:     assignment.ConfigEpoch,
		},
		Runtime: runtime,
	}
}
```

Add mapper:

```go
func slotTaskFromControl(task control.ReconcileTask) *SlotTask {
	participants := make([]SlotTaskParticipant, 0, len(task.ParticipantProgress))
	for _, item := range task.ParticipantProgress {
		participants = append(participants, SlotTaskParticipant{NodeID: item.NodeID, Attempt: item.Attempt, Status: string(item.Status), LastError: item.LastError})
	}
	return &SlotTask{
		TaskID:           task.TaskID,
		Kind:             string(task.Kind),
		Step:             string(task.Step),
		Status:           string(task.Status),
		SourceNode:       task.SourceNode,
		TargetNode:       task.TargetNode,
		TargetPeers:      append([]uint64(nil), task.TargetPeers...),
		CompletionPolicy: string(task.CompletionPolicy),
		ConfigEpoch:      task.ConfigEpoch,
		Attempt:          task.Attempt,
		LastError:        task.LastError,
		Participants:     participants,
	}
}
```

- [ ] **Step 5: Add access DTOs**

In `internalv2/access/manager/slots.go`, add `Task *SlotTaskDTO` to `SlotDTO`.

Add DTO structs:

```go
type SlotTaskDTO struct {
	TaskID           string                   `json:"task_id"`
	Kind             string                   `json:"kind"`
	Step             string                   `json:"step"`
	Status           string                   `json:"status"`
	SourceNode       uint64                   `json:"source_node,omitempty"`
	TargetNode       uint64                   `json:"target_node,omitempty"`
	TargetPeers      []uint64                 `json:"target_peers,omitempty"`
	CompletionPolicy string                   `json:"completion_policy"`
	ConfigEpoch      uint64                   `json:"config_epoch,omitempty"`
	Attempt          uint32                   `json:"attempt"`
	LastError        string                   `json:"last_error,omitempty"`
	Participants     []SlotTaskParticipantDTO `json:"participants,omitempty"`
}

type SlotTaskParticipantDTO struct {
	NodeID    uint64 `json:"node_id"`
	Attempt   uint32 `json:"attempt"`
	Status    string `json:"status"`
	LastError string `json:"last_error,omitempty"`
}
```

Add mapper:

```go
func slotTaskDTO(item *managementusecase.SlotTask) *SlotTaskDTO {
	if item == nil {
		return nil
	}
	participants := make([]SlotTaskParticipantDTO, 0, len(item.Participants))
	for _, participant := range item.Participants {
		participants = append(participants, SlotTaskParticipantDTO{
			NodeID:    participant.NodeID,
			Attempt:   participant.Attempt,
			Status:    participant.Status,
			LastError: participant.LastError,
		})
	}
	return &SlotTaskDTO{
		TaskID:           item.TaskID,
		Kind:             item.Kind,
		Step:             item.Step,
		Status:           item.Status,
		SourceNode:       item.SourceNode,
		TargetNode:       item.TargetNode,
		TargetPeers:      append([]uint64(nil), item.TargetPeers...),
		CompletionPolicy: item.CompletionPolicy,
		ConfigEpoch:      item.ConfigEpoch,
		Attempt:          item.Attempt,
		LastError:        item.LastError,
		Participants:     participants,
	}
}
```

Set `Task: slotTaskDTO(item.Task)` inside `slotDTO`.

- [ ] **Step 6: Update FLOW docs**

In `internalv2/usecase/management/FLOW.md`, update Slot List Flow with:

```text
Active task summaries are derived from the same clusterv2 control snapshot and attached to the matching Slot row. The usecase does not read ControllerV2 state directly and does not expose task mutation routes.
```

In `internalv2/access/manager/FLOW.md`, update `/manager/slots` route description:

```text
`/manager/slots` may include active task summaries and participant progress for display only. Slot task mutation routes remain outside this phase.
```

- [ ] **Step 7: Run manager tests**

Run:

```bash
go test ./internalv2/usecase/management ./internalv2/access/manager
```

Expected: PASS.

- [ ] **Step 8: Commit manager task read model**

```bash
git add internalv2/usecase/management/slots.go internalv2/usecase/management/slots_test.go internalv2/usecase/management/FLOW.md internalv2/access/manager/slots.go internalv2/access/manager/server_test.go internalv2/access/manager/FLOW.md
git commit -m "feat: expose manager slot task progress"
```

---

### Task 8: Focused End-To-End Verification

**Files:**
- Modify: tests only if package tests reveal a missing black-box hook.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./pkg/controllerv2/... ./pkg/clusterv2/control ./pkg/clusterv2/tasks ./pkg/clusterv2 ./internalv2/usecase/management ./internalv2/access/manager
```

Expected: PASS.

- [ ] **Step 2: Run wukongimv2 app smoke tests**

Run:

```bash
go test ./internalv2/app -run 'Test.*Manager|Test.*Sendack|Test.*Smoke'
```

Expected: PASS. If the regex matches no tests, run:

```bash
go test ./internalv2/app
```

Expected: PASS.

- [ ] **Step 3: Run full unit test sweep if focused tests pass**

Run:

```bash
go test ./pkg/controllerv2/... ./pkg/clusterv2/... ./internalv2/...
```

Expected: PASS.

- [ ] **Step 4: Verify no Slot leader transfer route was added**

Run:

```bash
rg -n "leader/transfer|TransferLeadership|leader_transfer" internalv2 pkg/clusterv2 pkg/controllerv2
```

Expected: no new `internalv2` manager mutation route for Slot leader transfer. Existing design text or legacy references outside this implementation scope are acceptable only if they are documentation or tests that assert the route is absent.

- [ ] **Step 5: Final commit for verification adjustments**

If Task 8 required test-only adjustments, commit them:

```bash
git add <changed-test-files>
git commit -m "test: verify controllerv2 task lifecycle"
```

If Task 8 changed no files, do not create an empty commit.

---

## Self-Review Checklist

- Spec coverage: Tasks 1-2 cover durable task state, participant progress, fenced result commands, completion/removal, and failure retention. Tasks 3-5 cover public facade and routing. Task 6 covers bootstrap execution and runtime observation. Task 7 covers manager read visibility. Task 8 covers verification and the no-leader-transfer boundary.
- Placeholder scan: this plan intentionally avoids placeholder markers and requires exact tests, methods, and commands for each step.
- Type consistency: `TaskCompletionPolicy`, `TaskParticipantStatus`, `TaskParticipantProgress`, `TaskResult`, and `TaskProgress` are introduced in ControllerV2 first, then mapped through `pkg/clusterv2/control`, then exposed to manager DTOs.
