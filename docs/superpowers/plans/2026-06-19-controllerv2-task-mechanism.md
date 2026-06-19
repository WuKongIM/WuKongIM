# ControllerV2 Task Mechanism Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close ControllerV2 task mechanism v0 by hardening the existing bootstrap task lifecycle, fixing participant retry fencing, and proving active bootstrap tasks clear through real `wukongimv2` processes.

**Architecture:** Keep the existing ControllerV2 durable task substrate: active tasks live in `cluster-state.json`, completed tasks are removed by `complete_task`, failed tasks remain active, and multi-node bootstrap work uses `all_target_peers` participant progress. The remaining implementation work is focused on correctness boundaries around task-result fencing and the `pkg/clusterv2/tasks` bootstrap executor; no Slot leader transfer task kind, manager write route, task history, cancellation, pause, or batch transfer scheduling is added.

**Tech Stack:** Go, ControllerV2 FSM/Raft commands, `pkg/clusterv2/tasks`, Slot Multi-Raft status, `internalv2` manager read model, process-level e2ev2 tests for `cmd/wukongimv2`.

---

## Scope Boundaries

This plan starts from the current branch state, where the durable model, progress/result command payloads, `pkg/clusterv2/control` snapshot mapping, manager read model, bootstrap executor skeleton, and bootstrap-task e2e scenario already exist.

Implement only the remaining v0 hardening:

- fix participant retry completion after a failed participant attempt
- add regression tests for executor-produced task results not relying on planner `ExpectedRevision`
- add bootstrap executor tests for retry, non-target nodes, and runtime observation mismatch
- run targeted unit tests and the bootstrap-task e2e scenario

Do not add a Slot leader transfer route or call `pkg/slot/multiraft.Runtime.TransferLeadership`. Do not add configuration; `wukongim.conf.example` should remain unchanged.

## File Structure

- `pkg/controllerv2/fsm/fsm_test.go`: add regression tests for progress CAS rejection and advanced participant retry success.
- `pkg/controllerv2/fsm/mutation_handlers.go`: allow a participant that previously failed to report `done` using the advanced participant attempt.
- `pkg/clusterv2/tasks/bootstrap_test.go`: add bootstrap executor edge-case tests for retry, non-target nodes, and mismatched runtime voters.
- `pkg/clusterv2/tasks/bootstrap.go`: change only if the new executor tests expose a mismatch with the v0 design.
- `test/e2ev2/control/bootstrap_task/bootstrap_task_test.go`: keep the existing black-box scenario as the v0 process-level acceptance test.
- `pkg/controllerv2/FLOW.md` and `pkg/clusterv2/FLOW.md`: update only if implementation behavior changes in a way not already described there.

---

### Task 1: Fix Participant Retry Fencing

**Files:**
- Modify: `pkg/controllerv2/fsm/fsm_test.go`
- Modify: `pkg/controllerv2/fsm/mutation_handlers.go`

- [ ] **Step 1: Add failing FSM regression tests**

Add these tests after `TestApplyReportTaskProgressStaleParticipantAttemptNoops` in `pkg/controllerv2/fsm/fsm_test.go`:

```go
func TestApplyReportTaskProgressAcceptsAdvancedParticipantAttemptAfterFailure(t *testing.T) {
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
			ParticipantAttempt: 1,
			Status:             state.TaskParticipantStatusDone,
		},
	})

	require.NoError(t, err)
	require.Equal(t, ApplyResult{Changed: true, Revision: 4, AppliedRaftIndex: 4}, result)
	task := sm.Snapshot(ctx).Tasks[0]
	var got state.TaskParticipantProgress
	for _, item := range task.ParticipantProgress {
		if item.NodeID == 2 {
			got = item
			break
		}
	}
	require.Equal(t, state.TaskParticipantStatusDone, got.Status)
	require.Equal(t, uint32(1), got.Attempt)
	require.Empty(t, got.LastError)
}

func TestApplyReportTaskProgressRejectsExpectedRevisionMismatchWhenTaskExists(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	applyOK(t, sm, 2, bootstrapCommand(1, 1, []uint64{1, 2, 3}))
	expected := uint64(1)

	result, err := sm.Apply(ctx, 3, command.Command{
		Kind:             command.KindReportTaskProgress,
		ExpectedRevision: &expected,
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
	require.Equal(t, ApplyResult{Rejected: true, Reason: ReasonExpectedRevisionMismatch, Revision: 2, AppliedRaftIndex: 3}, result)
	require.Equal(t, state.TaskParticipantStatusPending, participantStatus(sm.Snapshot(ctx).Tasks[0], 2))
}
```

- [ ] **Step 2: Run the new FSM tests and verify the retry regression fails**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/fsm -run 'TestApplyReportTaskProgress(AcceptsAdvancedParticipantAttemptAfterFailure|RejectsExpectedRevisionMismatchWhenTaskExists)' -count=1
```

Expected before the implementation change: `TestApplyReportTaskProgressAcceptsAdvancedParticipantAttemptAfterFailure` fails because a `done` report with participant attempt `1` is treated as stale after the previous failure advanced the participant attempt.

- [ ] **Step 3: Allow advanced participant retry success**

In `pkg/controllerv2/fsm/mutation_handlers.go`, remove this stale-attempt block from `applyReportTaskProgress`:

```go
	if cmd.TaskProgress.ParticipantAttempt == current.Attempt && current.Status == state.TaskParticipantStatusFailed && cmd.TaskProgress.Status == state.TaskParticipantStatusDone {
		return noop(ReasonTaskParticipantAttemptStale)
	}
```

Keep the preceding lower-attempt guard unchanged:

```go
	if cmd.TaskProgress.ParticipantAttempt < current.Attempt {
		return noop(ReasonTaskParticipantAttemptStale)
	}
```

This preserves stale old-attempt protection because a failed report increments the stored participant attempt before the next retry.

- [ ] **Step 4: Run FSM tests and verify the regression passes**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/fsm -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

Run:

```bash
git add pkg/controllerv2/fsm/fsm_test.go pkg/controllerv2/fsm/mutation_handlers.go
git commit -m "fix: allow controller task participant retry completion"
```

---

### Task 2: Harden Bootstrap Executor Boundaries

**Files:**
- Modify: `pkg/clusterv2/tasks/bootstrap_test.go`
- Modify: `pkg/clusterv2/tasks/bootstrap.go` only if required by the new tests

- [ ] **Step 1: Add executor boundary tests**

Add these tests after `TestBootstrapExecutorDoesNotCompleteWithoutQuorumObservation` in `pkg/clusterv2/tasks/bootstrap_test.go`:

```go
func TestBootstrapExecutorRetriesFailedParticipantWithAdvancedAttempt(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	snapshot := bootstrapSnapshot()
	for i := range snapshot.Tasks[0].ParticipantProgress {
		if snapshot.Tasks[0].ParticipantProgress[i].NodeID == 2 {
			snapshot.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusFailed
			snapshot.Tasks[0].ParticipantProgress[i].Attempt = 1
			snapshot.Tasks[0].ParticipantProgress[i].LastError = "open failed"
		}
	}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 2, Slots: manager, Status: status, Writer: writer})

	err := executor.Reconcile(context.Background(), snapshot)

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if manager.ensureCalls != 1 {
		t.Fatalf("ensure calls = %d, want 1", manager.ensureCalls)
	}
	if len(writer.progress) != 1 {
		t.Fatalf("progress = %#v, want one retry report", writer.progress)
	}
	got := writer.progress[0]
	if got.ParticipantNodeID != 2 || got.ParticipantAttempt != 1 || got.Status != controllerv2.TaskParticipantStatusDone {
		t.Fatalf("progress[0] = %#v, want node 2 done at participant attempt 1", got)
	}
}

func TestBootstrapExecutorSkipsLocalProgressForNonTargetNode(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
	executor := NewBootstrapExecutor(BootstrapExecutorConfig{LocalNode: 9, Slots: manager, Status: status, Writer: writer})

	err := executor.Reconcile(context.Background(), bootstrapSnapshot())

	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if manager.ensureCalls != 0 {
		t.Fatalf("ensure calls = %d, want 0 for non-target node", manager.ensureCalls)
	}
	if len(writer.progress) != 0 || len(writer.completed) != 0 {
		t.Fatalf("writer progress=%#v completed=%#v, want no task writes", writer.progress, writer.completed)
	}
}

func TestBootstrapExecutorDoesNotCompleteWhenObservedVotersDifferFromTargetPeers(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 1, CurrentVoters: []multiraft.NodeID{1, 2, 4}}}
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
		t.Fatalf("completed = %#v, want none for mismatched voters", writer.completed)
	}
}

func TestBootstrapExecutorDoesNotCompleteWithoutObservedLeader(t *testing.T) {
	manager := &fakeSlotManager{}
	writer := &recordingWriter{}
	status := &fakeStatusReader{status: multiraft.Status{SlotID: 1, LeaderID: 0, CurrentVoters: []multiraft.NodeID{1, 2, 3}}}
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
		t.Fatalf("completed = %#v, want none without observed leader", writer.completed)
	}
}
```

- [ ] **Step 2: Run bootstrap executor tests**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/tasks -count=1
```

Expected: PASS after Task 1. If this fails, fix only `pkg/clusterv2/tasks/bootstrap.go` to preserve these semantics:

```go
// Reconcile reports local bootstrap progress and completes converged tasks.
func (e *BootstrapExecutor) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
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
		if e.cfg.Slots != nil && containsNode(task.TargetPeers, e.cfg.LocalNode) && !participantDone(task, e.cfg.LocalNode) {
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

- [ ] **Step 3: Run Node snapshot task-trigger tests**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2 -run 'TestNodeControlWatchTaskChangeRunsTaskExecutor|TestControlSnapshotChangesDetectTasks' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit Task 2**

Run:

```bash
git add pkg/clusterv2/tasks/bootstrap_test.go pkg/clusterv2/tasks/bootstrap.go
git commit -m "test: harden bootstrap task executor boundaries"
```

If `pkg/clusterv2/tasks/bootstrap.go` is unchanged, leave it out of `git add`.

---

### Task 3: Verify V0 Acceptance

**Files:**
- Read: `pkg/controllerv2/FLOW.md`
- Read: `pkg/clusterv2/FLOW.md`
- Read: `test/e2ev2/control/bootstrap_task/bootstrap_task_test.go`

- [ ] **Step 1: Confirm FLOW docs still match implementation**

Run:

```bash
rg -n "Task progress|task result|participant progress|bootstrap task executor|Completed tasks|failed tasks|ExpectedRevision|ReportSlots" pkg/controllerv2/FLOW.md pkg/clusterv2/FLOW.md
```

Expected: output includes Controller task writes entering as Raft commands, completed tasks removed, failed tasks retained, and Node bootstrap executor running after Slot reconciliation. If implementation changed wording beyond these statements, update the relevant `FLOW.md` before committing.

- [ ] **Step 2: Run targeted unit test set**

Run:

```bash
GOWORK=off go test ./pkg/controllerv2/... ./pkg/clusterv2/control/... ./pkg/clusterv2/tasks ./internalv2/usecase/management/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run bootstrap task e2e**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/control/bootstrap_task -count=1 -timeout 2m
```

Expected: PASS. This proves a real three-node `cmd/wukongimv2` cluster creates bootstrap tasks, converges Slot Raft, clears completed active tasks from manager Slot rows, and exposes node-local Slot Raft evidence.

- [ ] **Step 4: Run full e2ev2 regression**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/... -count=1 -timeout 4m
```

Expected: PASS.

- [ ] **Step 5: Check final diff and commit any documentation updates**

Run:

```bash
git diff --check
git status --short
```

Expected: `git diff --check` exits 0. If `FLOW.md` files changed in Step 1, commit them:

```bash
git add pkg/controllerv2/FLOW.md pkg/clusterv2/FLOW.md
git commit -m "docs: align controller task flow"
```

If no docs changed, do not create an empty commit.

---

## Final Review Checklist

- [ ] `bootstrap` remains the only executable task kind.
- [ ] Manager routes remain read-only for Slot tasks.
- [ ] Executor-produced progress/results do not use `ExpectedRevision`.
- [ ] Participant retry after local failure uses the advanced participant attempt and can become `done`.
- [ ] Completed bootstrap tasks are removed from active `cluster-state.json` task state.
- [ ] Failed active tasks remain visible with bounded errors.
- [ ] Bootstrap completion requires real Slot runtime observation: target voters, non-zero leader, and quorum.
- [ ] No new config keys were added.
- [ ] `GOWORK=off go test -tags=e2e ./test/e2ev2/... -count=1 -timeout 4m` passes before finish.
