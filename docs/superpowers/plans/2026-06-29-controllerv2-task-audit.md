# ControllerV2 Task Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build bounded ControllerV2 task audit history for internalv2 manager and Web task center.

**Architecture:** ControllerV2 FSM produces stable task transitions from committed Raft commands. The root ControllerV2 runtime exposes those transitions through a nonblocking observer surface wired by `pkg/clusterv2/control` into `internalv2/app`, where `internalv2/observability/taskaudit` stores JSONL and serves bounded in-memory query projections. `internalv2/usecase/management` owns query ports and DTOs; manager HTTP and node RPC adapt those ports without importing storage details.

**Tech Stack:** Go, ControllerV2 Raft/FSM, JSONL files, internalv2 manager HTTP, internalv2 node RPC, Bun/Vitest/TypeScript for Web.

---

## File Map

- Modify `pkg/controllerv2/fsm/fsm.go`: add `TaskTransitions []TaskTransition` to `ApplyResult`, carry Raft term through apply.
- Create `pkg/controllerv2/fsm/task_transition.go`: transition model and diff extraction helpers.
- Modify `pkg/controllerv2/fsm/mutations.go`: attach task transitions only for `Changed` task mutations.
- Modify `pkg/controllerv2/types.go`: expose root-level aliases and observer type.
- Modify `pkg/controllerv2/raft/apply_scheduler.go`: dispatch transitions after `MarkAppliedBatch`.
- Modify `pkg/controllerv2/raft/service.go`: carry task transition observer from runtime config into scheduler.
- Modify `pkg/controllerv2/runtime.go`: wire task transition observer through `RuntimeConfig`.
- Modify `pkg/clusterv2/control/runtime.go`: pass observer through `control.RuntimeConfig`.
- Create `internalv2/observability/taskaudit/*`: JSONL store, model, retention, replay, queries.
- Modify `internalv2/usecase/management/app.go`: add management-owned `TaskAuditReader` and remote reader ports.
- Create `internalv2/usecase/management/task_audit.go`: audit list/event query usecase and filters.
- Create `internalv2/access/manager/controller_task_audits.go`: HTTP DTO, parsing, routes, error mapping.
- Modify `internalv2/access/manager/server.go`: register `/manager/controller/task-audits*`.
- Modify `internalv2/access/manager/controller_tasks.go`: allow `slot_replica_move` filter for active tasks.
- Create `internalv2/access/node/task_audit_codec.go`, `task_audit_client.go`, `task_audit_handler.go`: remote audit read RPC.
- Modify `pkg/clusterv2/net/ids.go`: add `RPCManagerTaskAudit`.
- Modify `internalv2/app/FLOW.md`, `internalv2/usecase/management/FLOW.md`, `internalv2/access/manager/FLOW.md`, `internalv2/access/node/FLOW.md`, `pkg/controllerv2/FLOW.md`: document new paths.
- Modify Web files after backend is green: `web/src/lib/manager-api.types.ts`, `web/src/lib/manager-api.ts`, `web/src/pages/tasks/page.tsx`, `web/src/pages/tasks/page.test.tsx`.

---

### Task 1: ControllerV2 FSM Task Transitions

**Files:**
- Create: `pkg/controllerv2/fsm/task_transition.go`
- Modify: `pkg/controllerv2/fsm/fsm.go`
- Modify: `pkg/controllerv2/fsm/mutations.go`
- Test: `pkg/controllerv2/fsm/task_transition_test.go`

- [ ] **Step 1: Write failing tests for task transition extraction**

Add tests that exercise real FSM behavior:

```go
func TestApplyTaskCommandsReturnTaskTransitions(t *testing.T) {
    ctx := context.Background()
    store := newMemoryStore()
    sm, err := New(store)
    require.NoError(t, err)
    require.NoError(t, sm.Load(ctx))

    init := initCommand()
    _, err = sm.Apply(ctx, 1, init)
    require.NoError(t, err)

    cmd := bootstrapCommand()
    result, err := sm.ApplyBatch(ctx, []AppliedCommand{{Index: 2, Term: 9, Command: cmd}})
    require.NoError(t, err)
    require.Len(t, result.Results, 1)
    require.True(t, result.Results[0].Changed)
    require.Len(t, result.Results[0].TaskTransitions, 1)
    transition := result.Results[0].TaskTransitions[0]
    require.Equal(t, uint64(2), transition.AppliedRaftIndex)
    require.Equal(t, uint64(9), transition.AppliedRaftTerm)
    require.False(t, transition.BeforeValid)
    require.True(t, transition.AfterValid)
    require.Equal(t, "slot-1-bootstrap-1", transition.After.TaskID)
    require.Equal(t, command.KindUpsertSlotAssignmentAndTask, transition.CommandKind)
}
```

Add companion cases with concrete assertions:

- `TestApplyTaskResultTransitionsForFailAndComplete`: failing a task returns `BeforeValid=true, AfterValid=true`; completing a task returns `BeforeValid=true, AfterValid=false`.
- `TestApplyTaskProgressTransitionCarriesParticipantNode`: progress reports return the participant node from `TaskProgress`.
- `TestApplyTaskNoopAndRejectDoNotReturnTransitions`: stale or mismatched task result commands do not return transitions.
- `TestTaskTransitionsAreDeepCopied`: mutating transition task slices after apply does not mutate the FSM state.

- [ ] **Step 2: Run RED test**

Run:

```bash
go test ./pkg/controllerv2/fsm -run 'TestApplyTask.*Transition|TestTaskTransitionsAreDeepCopied' -count=1
```

Expected: fail because `ApplyResult.TaskTransitions` and `TaskTransition` do not exist.

- [ ] **Step 3: Add transition model and extraction**

Create `pkg/controllerv2/fsm/task_transition.go`:

```go
package fsm

import (
    "reflect"

    "github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
    "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// TaskTransition describes one durable ControllerV2 task state edge.
type TaskTransition struct {
    AppliedRaftIndex uint64
    AppliedRaftTerm  uint64
    CommandKind      command.Kind
    IssuedAt         time.Time
    Before           state.ReconcileTask
    BeforeValid      bool
    After            state.ReconcileTask
    AfterValid       bool
    ParticipantNode  uint64
}
```

Add helper functions in the same file:

```go
func taskTransitionsForCommand(index, term uint64, cmd command.Command, before, after []state.ReconcileTask) []TaskTransition
func transitionParticipantNode(cmd command.Command) uint64
func cloneTransitionTask(task state.ReconcileTask) state.ReconcileTask
```

The helper must:
- match tasks by `TaskID`;
- return added tasks as `BeforeValid=false, AfterValid=true`;
- return removed tasks as `BeforeValid=true, AfterValid=false`;
- return changed tasks when `!reflect.DeepEqual(beforeTask, afterTask)`;
- deep-copy `TargetPeers`, `ParticipantProgress`, `ObservedVoters`, and `ObservedLearners`;
- set `AppliedRaftIndex`, `AppliedRaftTerm`, `CommandKind`, `IssuedAt`, and `ParticipantNode`.

- [ ] **Step 4: Thread transitions through ApplyResult**

Modify `pkg/controllerv2/fsm/fsm.go`:

```go
type ApplyResult struct {
    Changed bool
    Noop bool
    Rejected bool
    Reason string
    Revision uint64
    AppliedRaftIndex uint64
    TaskTransitions []TaskTransition
}
```

Modify `ApplyBatch` to pass `entry.Term` into mutation handling.

Modify `pkg/controllerv2/fsm/mutations.go` so `applyMutation` snapshots `beforeTasks` before command handlers and attaches transitions only when `result.Changed` is true.

- [ ] **Step 5: Run GREEN test**

Run:

```bash
go test ./pkg/controllerv2/fsm -run 'TestApplyTask.*Transition|TestTaskTransitionsAreDeepCopied' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run focused package tests**

Run:

```bash
go test ./pkg/controllerv2/fsm -count=1
```

Expected: PASS.

---

### Task 2: Public Observer Wiring After Applied Metadata

**Files:**
- Modify: `pkg/controllerv2/types.go`
- Modify: `pkg/controllerv2/runtime.go`
- Modify: `pkg/controllerv2/raft/service.go`
- Modify: `pkg/controllerv2/raft/apply_scheduler.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Test: `pkg/controllerv2/raft/apply_scheduler_test.go`
- Test: `pkg/clusterv2/control/runtime_test.go`

- [ ] **Step 1: Write failing scheduler observer test**

Add a test that records call order:

```go
func TestApplySchedulerDispatchesTaskTransitionsAfterMarkApplied(t *testing.T) {
    applier := &fakeBatchApplier{results: []fsm.ApplyResult{{
        Changed: true,
        AppliedRaftIndex: 7,
        TaskTransitions: []fsm.TaskTransition{{AppliedRaftIndex: 7}},
    }}}
    marker := &recordingAppliedMarker{}
    var observed []string
    sched := newApplyScheduler(applySchedulerConfig{}, applier, marker, nil)
    sched.onTaskTransitions = func([]fsm.TaskTransition) {
        observed = append(observed, "observer")
    }
    marker.onMark = func(uint64) {
        observed = append(observed, "mark")
    }
    err := sched.applyJob(context.Background(), toApply{entries: []raftpb.Entry{normalEntry(7)}})
    require.NoError(t, err)
    require.Equal(t, []string{"mark", "observer"}, observed)
}
```

- [ ] **Step 2: Run RED test**

Run:

```bash
go test ./pkg/controllerv2/raft -run TestApplySchedulerDispatchesTaskTransitionsAfterMarkApplied -count=1
```

Expected: fail because scheduler has no transition observer hook.

- [ ] **Step 3: Add root observer API**

Expose aliases in `pkg/controllerv2/types.go`:

```go
type TaskTransition = fsm.TaskTransition

type TaskTransitionObserver interface {
    ObserveControllerTaskTransitions([]TaskTransition)
}

type TaskTransitionObserverFunc func([]TaskTransition)

func (f TaskTransitionObserverFunc) ObserveControllerTaskTransitions(items []TaskTransition) {
    f(items)
}
```

Add `TaskTransitionObserver TaskTransitionObserver` to `controllerv2.RuntimeConfig` and pass it into the Raft service.

- [ ] **Step 4: Dispatch after MarkAppliedBatch**

Modify `pkg/controllerv2/raft/apply_scheduler.go` to collect transitions from each `ApplyResult`, call `MarkAppliedBatch`, then call observer without returning observer errors. Observer must never block Raft apply indefinitely; if the root observer uses a channel, it must use a bounded nonblocking send.

- [ ] **Step 5: Pass through clusterv2/control**

Modify `pkg/clusterv2/control.RuntimeConfig`:

```go
TaskTransitionObserver cv2.TaskTransitionObserver
```

Pass it to `cv2.NewRuntime`.

- [ ] **Step 6: Run GREEN tests**

Run:

```bash
go test ./pkg/controllerv2/raft ./pkg/clusterv2/control -run 'TaskTransition|NewRuntime' -count=1
```

Expected: PASS.

---

### Task 3: internalv2 Task Audit JSONL Store

**Files:**
- Create: `internalv2/observability/taskaudit/FLOW.md`
- Create: `internalv2/observability/taskaudit/model.go`
- Create: `internalv2/observability/taskaudit/store.go`
- Create: `internalv2/observability/taskaudit/jsonl.go`
- Create: `internalv2/observability/taskaudit/retention.go`
- Test: `internalv2/observability/taskaudit/store_test.go`

- [ ] **Step 1: Write failing store tests**

Create tests with concrete assertions:

- `TestStoreAppendReplayAndQuery`: append two events, reopen the store, and list snapshots sorted by `AppliedRaftIndex` descending.
- `TestStoreRetentionKeepsLatestTasksAndEvents`: appending 201 task histories retains 200 tasks; appending 51 events for one task retains 50 events and reports truncation.
- `TestStoreSkipsCorruptJSONLLines`: one bad JSONL line is skipped during replay without blocking valid retained lines.
- `TestStoreAppendDuplicateDoesNotWriteJSONL`: appending the same `EventID` twice writes one persisted JSONL event.
- `TestStoreCompactionSerializesWithAppend`: concurrent append and compaction leave retained events replayable after reopen.

- [ ] **Step 2: Run RED tests**

Run:

```bash
go test ./internalv2/observability/taskaudit -count=1
```

Expected: fail because package does not exist.

- [ ] **Step 3: Implement store model**

`model.go` must define:

```go
type EventType string
const (
    EventCreated EventType = "created"
    EventRunning EventType = "running"
    EventParticipantProgress EventType = "participant_progress"
    EventFailed EventType = "failed"
    EventCompleted EventType = "completed"
    EventSnapshot EventType = "snapshot"
)
type ListRequest struct { Kind, Status, Keyword string; SlotID uint32; NodeID uint64; Limit int }
type ListResponse struct { Total, Limit int; Truncated bool; Items []Snapshot }
type EventsResponse struct { Task Snapshot; Events []Event; Truncated bool }
```

`Event` must include explicit JSON-tagged fields from the design spec: event id, task id, event type, task kind, status, slot id, leader id, from/to node ids, applied Raft index and term, command kind, participant node, timestamp, summary, reason, and bounded details.

`Snapshot` must include explicit JSON-tagged query projection fields: task id, kind, status, slot id, leader id, from/to node ids, first/last applied Raft index, started/completed timestamps, event count, truncated flag, summary, and last reason.

- [ ] **Step 4: Implement append/replay/retention**

`store.go` must expose:

```go
func Open(path string, opts Options) (*Store, error)
func (s *Store) Append(ctx context.Context, event Event) error
func (s *Store) List(ctx context.Context, req ListRequest) (ListResponse, error)
func (s *Store) Events(ctx context.Context, taskID string) (EventsResponse, error)
func (s *Store) Compact(ctx context.Context) error
func (s *Store) Close() error
```

`Store` must keep a mutex, JSONL file path, open file handle, event dedupe index, task snapshots, and per-task event maps.

Defaults:

```go
MaxTasks = 200
MaxEventsPerTask = 50
DetailLimitBytes = 16 << 10
```

- [ ] **Step 5: Run GREEN tests**

Run:

```bash
go test ./internalv2/observability/taskaudit -count=1
```

Expected: PASS.

---

### Task 4: Management Usecase And Manager HTTP API

**Files:**
- Modify: `internalv2/usecase/management/app.go`
- Create: `internalv2/usecase/management/task_audit.go`
- Test: `internalv2/usecase/management/task_audit_test.go`
- Create: `internalv2/access/manager/controller_task_audits.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/controller_tasks.go`
- Test: `internalv2/access/manager/controller_task_audits_test.go`
- Test: `internalv2/access/manager/controller_tasks_test.go`

- [ ] **Step 1: Write failing management and HTTP tests**

Use management-owned fake reader and add these cases:

- `TestListControllerTaskAuditsFiltersAndLimits`: verifies kind, status, slot, node, keyword, and limit behavior.
- `TestControllerTaskAuditEventsReturnsTimeline`: reads events for an exact task id and preserves timeline order.
- `TestControllerTaskAuditUnavailableMapsToHTTP503`: manager route maps unavailable audit storage to HTTP 503.
- `TestManagerControllerTaskKindAcceptsSlotReplicaMove`: active task route accepts `slot_replica_move` as a valid kind filter.

- [ ] **Step 2: Run RED tests**

Run:

```bash
go test ./internalv2/usecase/management ./internalv2/access/manager -run 'TaskAudit|ControllerTaskKind' -count=1
```

Expected: fail because usecase/API do not exist and active filter rejects `slot_replica_move`.

- [ ] **Step 3: Add usecase ports and DTOs**

In `internalv2/usecase/management/app.go`:

```go
type ControllerTaskAuditReader interface {
    ListControllerTaskAudits(context.Context, ControllerTaskAuditListRequest) (ControllerTaskAuditListResponse, error)
    ControllerTaskAuditEvents(context.Context, string) (ControllerTaskAuditEventsResponse, error)
}
```

Add `ControllerTaskAudit ControllerTaskAuditReader` to `AppOptions` and `App`.

- [ ] **Step 4: Add manager routes**

In `server.go`, under controller read group:

```go
controllerReads.GET("/controller/task-audits", s.handleControllerTaskAudits)
controllerReads.GET("/controller/task-audits/:task_id/events", s.handleControllerTaskAuditEvents)
```

Both routes require `cluster.controller:r`.

- [ ] **Step 5: Fix active task kind whitelist**

In `controller_tasks.go`, allow:

```go
case "", string(control.TaskKindBootstrap), string(control.TaskKindLeaderTransfer), string(control.TaskKindSlotReplicaMove):
```

- [ ] **Step 6: Run GREEN tests**

Run:

```bash
go test ./internalv2/usecase/management ./internalv2/access/manager -run 'TaskAudit|ControllerTaskKind' -count=1
```

Expected: PASS.

---

### Task 5: Node RPC For Remote Audit Reads

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Test: `pkg/clusterv2/net/ids_test.go`
- Create: `internalv2/access/node/task_audit_codec.go`
- Create: `internalv2/access/node/task_audit_client.go`
- Create: `internalv2/access/node/task_audit_handler.go`
- Test: `internalv2/access/node/task_audit_codec_test.go`

- [ ] **Step 1: Write failing RPC id and codec tests**

Tests must assert:

```go
require.Equal(t, "manager_task_audit", net.ServiceName(net.RPCManagerTaskAudit))
require.NoError(t, decodeTaskAuditListRequest(encodeTaskAuditListRequest(req), &got))
require.Error(t, decodeTaskAuditListRequest(append(encoded, 0x00), &got))
```

- [ ] **Step 2: Run RED tests**

Run:

```bash
go test ./pkg/clusterv2/net ./internalv2/access/node -run TaskAudit -count=1
```

Expected: fail because RPC id and codec do not exist.

- [ ] **Step 3: Implement codec, handler, client**

Follow existing manager diagnostics/app-log codec style: strict JSON decode, reject trailing bytes, preserve `service_unavailable` mapping at caller.

- [ ] **Step 4: Run GREEN tests**

Run:

```bash
go test ./pkg/clusterv2/net ./internalv2/access/node -run TaskAudit -count=1
```

Expected: PASS.

---

### Task 6: App Wiring And Backfill

**Files:**
- Modify: `internalv2/app/*`
- Test: targeted app wiring test if an existing app composition test exists
- Modify FLOW docs listed in the spec

- [ ] **Step 1: Write failing wiring test**

Add or extend an app-level test that constructs the internalv2 app with a fake ControllerV2 transition observer and asserts the management app receives a configured task audit reader.

- [ ] **Step 2: Run RED test**

Run:

```bash
go test ./internalv2/app -run TaskAudit -count=1
```

Expected: fail because wiring does not exist.

- [ ] **Step 3: Wire store and observer**

Create `taskaudit.Store` at `${WK_NODE_DATA_DIR}/task-audit/controller-v2-events.jsonl`, inject:

- observer into `controllerv2.RuntimeConfig` through `pkg/clusterv2/control.RuntimeConfig`;
- local reader into `internalv2/usecase/management`;
- remote reader into `internalv2/usecase/management`;
- startup backfill from local control snapshot.

- [ ] **Step 4: Update FLOW docs**

Update the exact route and observer flow in:

- `pkg/controllerv2/FLOW.md`
- `internalv2/app/FLOW.md`
- `internalv2/usecase/management/FLOW.md`
- `internalv2/access/manager/FLOW.md`
- `internalv2/access/node/FLOW.md`

- [ ] **Step 5: Run GREEN tests**

Run:

```bash
go test ./internalv2/app ./pkg/controllerv2/... ./pkg/clusterv2/control -run TaskAudit -count=1
```

Expected: PASS.

---

### Task 7: Web ControllerV2 Task Center

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/pages/tasks/page.tsx`
- Modify: `web/src/pages/tasks/page.test.tsx`

- [ ] **Step 1: Write failing Web tests**

Update tests to expect:

```ts
expect(getControllerTasksMock).toHaveBeenCalledWith({ kind: "slot_replica_move" })
expect(getControllerTaskAuditsMock).toHaveBeenCalled()
expect(getControllerTaskAuditEventsMock).toHaveBeenCalledWith("slot-1-replica-move-2-to-4-r9")
```

Permissions fixture uses:

```ts
permissions: [{ resource: "cluster.controller", actions: ["r"] }]
```

- [ ] **Step 2: Run RED test**

Run:

```bash
cd web && bun run test -- src/pages/tasks/page.test.tsx
```

Expected: fail because page still calls distributed task APIs.

- [ ] **Step 3: Implement API client and page migration**

Replace task center data source with:

```text
GET /manager/controller/tasks
GET /manager/controller/tasks/:task_id
GET /manager/controller/task-audits
GET /manager/controller/task-audits/:task_id/events
```

- [ ] **Step 4: Run GREEN Web tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/tasks/page.test.tsx
cd web && bunx tsc -b
```

Expected: PASS.

---

### Task 8: Final Verification And Review

**Files:**
- All changed files

- [ ] **Step 1: Run focused backend tests**

```bash
go test ./pkg/controllerv2/... ./pkg/clusterv2/control ./pkg/clusterv2/net ./internalv2/observability/taskaudit ./internalv2/usecase/management ./internalv2/access/manager ./internalv2/access/node
```

- [ ] **Step 2: Run Web verification**

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/tasks/page.test.tsx
cd web && bunx tsc -b
```

- [ ] **Step 3: Run diff check**

```bash
git diff --check
```

- [ ] **Step 4: Request code review**

Use `superpowers:requesting-code-review` with base `origin/main` and current `HEAD`.
