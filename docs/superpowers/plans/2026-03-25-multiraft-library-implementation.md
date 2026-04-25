# Multi-Raft Library Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first working `multiraft` library that implements the approved high-level API, hides `RawNode/Ready`, and is usable with injected transport, storage, and state machine implementations.

**Architecture:** The implementation lives in a new `multiraft/` package. `Runtime` owns a registry of groups, worker-driven scheduling, and the hidden ready pipeline; each group wraps a single `etcd/raft.RawNode` plus mailbox, storage adapter, and proposal tracking. Tests use in-memory fakes for transport, storage, and state machine so the package can be validated without any external services.

**Tech Stack:** Go 1.23, `go.etcd.io/raft/v3`, standard library `context/sync/atomic/time/testing`

---

## File Structure

### Production files

- Create: `multiraft/doc.go`
  Responsibility: package overview and public contract notes.
- Create: `multiraft/types.go`
  Responsibility: public IDs, options, envelopes, results, config-change enums, role enums.
- Create: `multiraft/errors.go`
  Responsibility: exported sentinel errors and internal error helpers.
- Create: `multiraft/runtime.go`
  Responsibility: `Runtime`, constructor, shutdown, group registry, worker lifecycle.
- Create: `multiraft/group.go`
  Responsibility: internal per-group state, storage/state machine references, mailbox queues.
- Create: `multiraft/storage_adapter.go`
  Responsibility: adapt public `Storage` to the internal raft-storage view used by each `RawNode`.
- Create: `multiraft/future.go`
  Responsibility: proposal futures and completion paths.
- Create: `multiraft/scheduler.go`
  Responsibility: enqueue, de-duplicate, and worker processing order for request/tick/ready work.
- Create: `multiraft/ready.go`
  Responsibility: hidden ready loop, persist-send-apply ordering, future completion, re-enqueue.
- Create: `multiraft/api.go`
  Responsibility: public methods `OpenGroup`, `BootstrapGroup`, `CloseGroup`, `Step`, `Propose`, `ChangeConfig`, `TransferLeadership`, `Status`, `Groups`.

### Test files

- Create: `multiraft/testenv_test.go`
  Responsibility: fake storage, fake transport, fake state machine, and helper assertions.
- Create: `multiraft/runtime_test.go`
  Responsibility: constructor, lifecycle, registry, and status tests.
- Create: `multiraft/step_test.go`
  Responsibility: message routing and unknown-group behavior.
- Create: `multiraft/proposal_test.go`
  Responsibility: proposal completion, persist/apply ordering, future result tests.
- Create: `multiraft/control_test.go`
  Responsibility: config changes and leadership transfer behavior.
- Create: `multiraft/recovery_test.go`
  Responsibility: open-from-storage recovery and snapshot restore behavior.
- Create: `multiraft/example_test.go`
  Responsibility: package-level usage example that matches the approved API.

### Module files

- Modify: `go.mod`
  Responsibility: add `go.etcd.io/raft/v3` and any direct test-time dependencies only if required.

## Implementation Notes

- Follow `@superpowers:test-driven-development` task-by-task.
- Keep the public surface in `types.go` and `api.go`; keep `RawNode`, `Ready`, and scheduling internals unexported.
- Prefer small files with one responsibility; do not create a generic “utils.go”.
- Return sentinel errors for API misuse or missing state instead of string-matching errors.
- `Future.Wait()` must resolve only after local state-machine apply succeeds.
- The ready pipeline must preserve this order:
  1. persist raft state
  2. send outbound messages
  3. apply committed entries / snapshots
  4. complete futures
  5. mark applied and re-enqueue if more work exists

## Task 1: Lock the public API surface

**Files:**
- Modify: `go.mod`
- Create: `multiraft/doc.go`
- Create: `multiraft/types.go`
- Create: `multiraft/errors.go`
- Create: `multiraft/api.go`
- Create: `multiraft/runtime.go`
- Test: `multiraft/runtime_test.go`

- [ ] **Step 1: Write the failing API-constructor tests**

```go
func TestNewValidatesRequiredOptions(t *testing.T) {
    _, err := multiraft.New(multiraft.Options{})
    if !errors.Is(err, multiraft.ErrInvalidOptions) {
        t.Fatalf("expected ErrInvalidOptions, got %v", err)
    }
}

func TestPublicTypesExposeApprovedFields(t *testing.T) {
    var opts multiraft.Options
    opts.NodeID = 1
    opts.TickInterval = time.Second
    opts.Workers = 1
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./multiraft -run 'TestNewValidatesRequiredOptions|TestPublicTypesExposeApprovedFields' -v`

Expected: FAIL because `multiraft` package and exported symbols do not exist yet.

- [ ] **Step 3: Add the minimal package shell and public type definitions**

```go
package multiraft

type GroupID uint64
type NodeID uint64

var ErrInvalidOptions = errors.New("multiraft: invalid options")
```

Implementation details:
- add package comment in `multiraft/doc.go`
- define `Options`, `RaftOptions`, `Envelope`, `Result`, `Status`, `ConfigChange`, `ChangeType`, `Role`
- define `Transport`, `Storage`, `StateMachine`, `Future`
- create a minimal `Runtime` shell in `multiraft/runtime.go` so `New` and public methods compile from the first task
- add constructor signature `func New(opts Options) (*Runtime, error)`
- add method signatures in `multiraft/api.go` so later tasks can fill in behavior without moving public declarations

- [ ] **Step 4: Add constructor validation**

Validation rules for V1:
- `NodeID != 0`
- `TickInterval > 0`
- `Workers > 0`
- `Transport != nil`
- `Raft.ElectionTick > 0`
- `Raft.HeartbeatTick > 0`
- `Raft.ElectionTick > Raft.HeartbeatTick`

- [ ] **Step 5: Re-run the targeted tests**

Run: `go test ./multiraft -run 'TestNewValidatesRequiredOptions|TestPublicTypesExposeApprovedFields' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod multiraft/doc.go multiraft/types.go multiraft/errors.go multiraft/api.go multiraft/runtime.go multiraft/runtime_test.go
git commit -m "feat: add multiraft public api types"
```

## Task 2: Implement runtime lifecycle and group registry

**Files:**
- Modify: `multiraft/runtime.go`
- Create: `multiraft/group.go`
- Create: `multiraft/storage_adapter.go`
- Test: `multiraft/runtime_test.go`
- Test: `multiraft/testenv_test.go`

- [ ] **Step 1: Write failing lifecycle and registry tests**

```go
func TestOpenGroupRegistersGroup(t *testing.T) {
    rt := newTestRuntime(t)
    err := rt.OpenGroup(context.Background(), multiraft.GroupOptions{
        ID:           10,
        Storage:      newFakeStorage(),
        StateMachine: newFakeStateMachine(),
    })
    if err != nil {
        t.Fatalf("OpenGroup() error = %v", err)
    }
    if got := rt.Groups(); !reflect.DeepEqual(got, []multiraft.GroupID{10}) {
        t.Fatalf("Groups() = %v", got)
    }
}

func TestOpenGroupRejectsDuplicateID(t *testing.T) {
    rt := newTestRuntime(t)
    if err := rt.OpenGroup(context.Background(), newGroupOptions(10)); err != nil {
        t.Fatalf("first OpenGroup() error = %v", err)
    }
    err := rt.OpenGroup(context.Background(), newGroupOptions(10))
    if !errors.Is(err, multiraft.ErrGroupExists) {
        t.Fatalf("expected ErrGroupExists, got %v", err)
    }
}
```

- [ ] **Step 2: Run the lifecycle tests and confirm they fail**

Run: `go test ./multiraft -run 'TestOpenGroupRegistersGroup|TestOpenGroupRejectsDuplicateID' -v`

Expected: FAIL because `Runtime` does not hold groups yet.

- [ ] **Step 3: Build test doubles for storage, transport, and state machine**

```go
type fakeTransport struct{ sent []multiraft.Envelope }
type fakeStorage struct{ applied uint64 }
type fakeStateMachine struct{ applied [][]byte }
```

Requirements:
- fake storage implements all `Storage` methods with in-memory state
- fake state machine records applied commands and supports snapshot/restore
- fake transport records sent batches for assertions

- [ ] **Step 4: Implement runtime registry and shutdown**

Implementation details:
- `Runtime` owns a mutex, closed flag, `map[GroupID]*group`, worker wait group, and stop channel
- `OpenGroup` validates options and creates a group from `Storage.InitialState()`
- `storage_adapter.go` provides the internal `raft.Storage` bridge so later tasks can plug in `RawNode` without redesigning `group`
- `CloseGroup` removes a group and rejects future operations
- `Groups()` returns sorted IDs for deterministic tests
- `Status()` returns a snapshot from the in-memory group state

- [ ] **Step 5: Re-run lifecycle tests plus constructor tests**

Run: `go test ./multiraft -run 'Test(NewValidatesRequiredOptions|OpenGroupRegistersGroup|OpenGroupRejectsDuplicateID)' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add multiraft/runtime.go multiraft/group.go multiraft/storage_adapter.go multiraft/runtime_test.go multiraft/testenv_test.go
git commit -m "feat: add multiraft runtime registry"
```

## Task 3: Add bootstrap, close, status, and exported error model

**Files:**
- Modify: `multiraft/errors.go`
- Modify: `multiraft/api.go`
- Modify: `multiraft/runtime.go`
- Modify: `multiraft/group.go`
- Test: `multiraft/runtime_test.go`

- [ ] **Step 1: Write failing tests for bootstrap, close, and status**

```go
func TestBootstrapGroupCreatesInitialMembership(t *testing.T) {
    rt := newTestRuntime(t)
    err := rt.BootstrapGroup(context.Background(), multiraft.BootstrapGroupRequest{
        Group: newGroupOptions(20),
        Voters: []multiraft.NodeID{1, 2, 3},
    })
    if err != nil {
        t.Fatalf("BootstrapGroup() error = %v", err)
    }
    st, err := rt.Status(20)
    if err != nil {
        t.Fatalf("Status() error = %v", err)
    }
    if st.GroupID != multiraft.GroupID(20) {
        t.Fatalf("Status().GroupID = %d", st.GroupID)
    }
}

func TestCloseGroupMakesFutureOperationsFail(t *testing.T) {
    rt := newTestRuntime(t)
    if err := rt.OpenGroup(context.Background(), newGroupOptions(10)); err != nil {
        t.Fatalf("OpenGroup() error = %v", err)
    }
    if err := rt.CloseGroup(context.Background(), 10); err != nil {
        t.Fatalf("CloseGroup() error = %v", err)
    }
    _, err := rt.Status(10)
    if !errors.Is(err, multiraft.ErrGroupNotFound) {
        t.Fatalf("expected ErrGroupNotFound, got %v", err)
    }
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./multiraft -run 'TestBootstrapGroupCreatesInitialMembership|TestCloseGroupMakesFutureOperationsFail' -v`

Expected: FAIL because bootstrap and close semantics are incomplete.

- [ ] **Step 3: Implement exported sentinel errors**

Add these sentinels in `multiraft/errors.go`:

```go
var (
    ErrInvalidOptions = errors.New("multiraft: invalid options")
    ErrGroupExists = errors.New("multiraft: group already exists")
    ErrGroupNotFound = errors.New("multiraft: group not found")
    ErrGroupClosed = errors.New("multiraft: group closed")
    ErrRuntimeClosed = errors.New("multiraft: runtime closed")
    ErrNotLeader = errors.New("multiraft: not leader")
)
```

- [ ] **Step 4: Implement bootstrap and close semantics**

Implementation details:
- bootstrap writes an initial raft state through the storage adapter, then opens the group through the same creation path as `OpenGroup`
- reject bootstrap on existing group
- reject open on already-bootstrapped group ID
- `CloseGroup` stops scheduling for that group and resolves any pending futures with `ErrGroupClosed`
- `Status` returns `ErrGroupNotFound` after close

- [ ] **Step 5: Re-run runtime package tests**

Run: `go test ./multiraft -run 'Test(BootstrapGroupCreatesInitialMembership|CloseGroupMakesFutureOperationsFail|OpenGroupRegistersGroup|OpenGroupRejectsDuplicateID)' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add multiraft/errors.go multiraft/api.go multiraft/runtime.go multiraft/group.go multiraft/runtime_test.go
git commit -m "feat: add multiraft lifecycle semantics"
```

## Task 4: Add scheduler, mailboxes, and inbound message routing

**Files:**
- Create: `multiraft/scheduler.go`
- Modify: `multiraft/runtime.go`
- Modify: `multiraft/group.go`
- Modify: `multiraft/api.go`
- Test: `multiraft/step_test.go`
- Test: `multiraft/testenv_test.go`

- [ ] **Step 1: Write failing routing tests**

```go
func TestStepRoutesMessageToCorrectGroup(t *testing.T) {
    rt := newStartedRuntime(t)
    if err := rt.OpenGroup(context.Background(), newGroupOptions(100)); err != nil {
        t.Fatalf("OpenGroup() error = %v", err)
    }
    if err := rt.Step(context.Background(), multiraft.Envelope{
        GroupID: 100,
        Message: raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1},
    }); err != nil {
        t.Fatalf("Step() error = %v", err)
    }
    waitForCondition(t, func() bool { return groupRequestCount(rt, 100) == 1 })
}

func TestStepUnknownGroupReturnsErrGroupNotFound(t *testing.T) {
    rt := newStartedRuntime(t)
    err := rt.Step(context.Background(), multiraft.Envelope{GroupID: 404})
    if !errors.Is(err, multiraft.ErrGroupNotFound) {
        t.Fatalf("expected ErrGroupNotFound, got %v", err)
    }
}

func TestRuntimeTickLoopEnqueuesOpenGroups(t *testing.T) {
    rt := newStartedRuntime(t)
    if err := rt.OpenGroup(context.Background(), newGroupOptions(101)); err != nil {
        t.Fatalf("OpenGroup() error = %v", err)
    }
    waitForCondition(t, func() bool { return groupTickCount(rt, 101) > 0 })
}
```

- [ ] **Step 2: Run the routing tests and confirm they fail**

Run: `go test ./multiraft -run 'Test(StepRoutesMessageToCorrectGroup|StepUnknownGroupReturnsErrGroupNotFound|RuntimeTickLoopEnqueuesOpenGroups)' -v`

Expected: FAIL because inbound queues and workers do not exist yet.

- [ ] **Step 3: Implement per-group mailboxes and scheduler**

Implementation details:
- each group stores request queue, control queue, and pending-ready flag
- scheduler de-duplicates group IDs so workers do not hot-loop the same group
- `Runtime` starts a ticker goroutine that periodically enqueues all open groups for tick processing
- worker order mirrors the approved design:
  1. process inbound requests
  2. process tick/control actions
  3. process ready work

- [ ] **Step 4: Implement `Runtime.Step` using the scheduler**

```go
func (r *Runtime) Step(ctx context.Context, env Envelope) error {
    g, err := r.lookupGroup(env.GroupID)
    if err != nil {
        return err
    }
    g.enqueueRequest(env.Message)
    r.scheduler.enqueue(env.GroupID)
    return nil
}
```

- [ ] **Step 5: Re-run routing and lifecycle tests**

Run: `go test ./multiraft -run 'Test(StepRoutesMessageToCorrectGroup|StepUnknownGroupReturnsErrGroupNotFound|RuntimeTickLoopEnqueuesOpenGroups|OpenGroupRegistersGroup)' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add multiraft/scheduler.go multiraft/runtime.go multiraft/group.go multiraft/api.go multiraft/step_test.go multiraft/testenv_test.go
git commit -m "feat: add multiraft scheduler and step routing"
```

## Task 5: Implement proposal futures and the hidden ready pipeline

**Files:**
- Create: `multiraft/future.go`
- Create: `multiraft/ready.go`
- Modify: `multiraft/api.go`
- Modify: `multiraft/group.go`
- Modify: `multiraft/runtime.go`
- Test: `multiraft/proposal_test.go`
- Test: `multiraft/testenv_test.go`

- [ ] **Step 1: Write failing proposal and ordering tests**

```go
func TestProposeWaitReturnsAfterLocalApply(t *testing.T) {
    rt := newStartedRuntime(t)
    leaderGroup := openSingleNodeLeader(t, rt, 10)

    fut, err := rt.Propose(context.Background(), leaderGroup, []byte("set a=1"))
    if err != nil {
        t.Fatalf("Propose() error = %v", err)
    }

    res, err := fut.Wait(context.Background())
    if err != nil {
        t.Fatalf("Wait() error = %v", err)
    }
    if string(res.Data) != "ok:set a=1" {
        t.Fatalf("Wait().Data = %q", res.Data)
    }
}

func TestReadyPipelinePersistsBeforeApply(t *testing.T) {
    rt := newStartedRuntime(t)
    groupID := openSingleNodeLeader(t, rt, 11)

    _, err := rt.Propose(context.Background(), groupID, []byte("cmd"))
    if err != nil {
        t.Fatalf("Propose() error = %v", err)
    }

    waitForCondition(t, func() bool {
        return fakeStorageFor(rt, groupID).saveCount > 0 &&
            fakeStorageFor(rt, groupID).lastApplied >= fakeStorageFor(rt, groupID).lastSavedIndex
    })
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./multiraft -run 'Test(ProposeWaitReturnsAfterLocalApply|ReadyPipelinePersistsBeforeApply)' -v`

Expected: FAIL because proposal tracking and ready processing are not implemented.

- [ ] **Step 3: Implement `future` completion plumbing**

Implementation details:
- track proposals by local request ID
- resolve success with `Result{Index, Term, Data}`
- resolve failure on context cancel, group close, or runtime close
- avoid goroutine leaks by closing waiter channels exactly once

- [ ] **Step 4: Implement the hidden ready pipeline**

Implementation details:
- create an internal raw-node wrapper per group
- `ready.go` handles:
  - `HasReady`
  - durable `Storage.Save`
  - outbound `Transport.Send`
  - snapshot restore
  - committed-entry apply via `StateMachine.Apply`
  - `Storage.MarkApplied`
  - future completion
  - re-enqueue if more ready work exists

- [ ] **Step 5: Re-run proposal tests and then the full package test suite**

Run: `go test ./multiraft -run 'Test(ProposeWaitReturnsAfterLocalApply|ReadyPipelinePersistsBeforeApply)' -v`

Expected: PASS

Run: `go test ./multiraft/...`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add multiraft/future.go multiraft/ready.go multiraft/api.go multiraft/group.go multiraft/runtime.go multiraft/proposal_test.go multiraft/testenv_test.go
git commit -m "feat: add multiraft proposal and ready pipeline"
```

## Task 6: Implement config changes and leadership transfer

**Files:**
- Modify: `multiraft/api.go`
- Modify: `multiraft/group.go`
- Modify: `multiraft/ready.go`
- Test: `multiraft/control_test.go`

- [ ] **Step 1: Write failing control-plane tests**

```go
func TestChangeConfigAppliesAddLearner(t *testing.T) {
    rt := newStartedRuntime(t)
    groupID := openSingleNodeLeader(t, rt, 30)

    fut, err := rt.ChangeConfig(context.Background(), groupID, multiraft.ConfigChange{
        Type:   multiraft.AddLearner,
        NodeID: 2,
    })
    if err != nil {
        t.Fatalf("ChangeConfig() error = %v", err)
    }
    _, err = fut.Wait(context.Background())
    if err != nil {
        t.Fatalf("Wait() error = %v", err)
    }
}

func TestTransferLeadershipRejectsUnknownGroup(t *testing.T) {
    rt := newStartedRuntime(t)
    err := rt.TransferLeadership(context.Background(), 999, 2)
    if !errors.Is(err, multiraft.ErrGroupNotFound) {
        t.Fatalf("expected ErrGroupNotFound, got %v", err)
    }
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./multiraft -run 'Test(ChangeConfigAppliesAddLearner|TransferLeadershipRejectsUnknownGroup)' -v`

Expected: FAIL because control operations are not wired.

- [ ] **Step 3: Implement `ChangeConfig`**

Implementation details:
- map public `ChangeType` to raft conf-change payloads
- keep joint-consensus details internal
- complete the returned future only after the local group applies the config-change entry

- [ ] **Step 4: Implement `TransferLeadership`**

Implementation details:
- enqueue a control action instead of calling raw node directly from API goroutines
- update status after leadership changes are observed
- return `ErrGroupNotFound` for missing groups and `ErrRuntimeClosed` after shutdown

- [ ] **Step 5: Re-run control tests and full package tests**

Run: `go test ./multiraft -run 'Test(ChangeConfigAppliesAddLearner|TransferLeadershipRejectsUnknownGroup)' -v`

Expected: PASS

Run: `go test ./multiraft/...`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add multiraft/api.go multiraft/group.go multiraft/ready.go multiraft/control_test.go
git commit -m "feat: add multiraft control operations"
```

## Task 7: Implement recovery and snapshot restore

**Files:**
- Modify: `multiraft/group.go`
- Modify: `multiraft/ready.go`
- Test: `multiraft/recovery_test.go`
- Test: `multiraft/testenv_test.go`

- [ ] **Step 1: Write failing recovery tests**

```go
func TestOpenGroupRestoresAppliedIndexFromStorage(t *testing.T) {
    store := newFakeStorageWithState(multiraft.BootstrapState{
        AppliedIndex: 7,
    })
    rt := newStartedRuntime(t)
    if err := rt.OpenGroup(context.Background(), multiraft.GroupOptions{
        ID:           40,
        Storage:      store,
        StateMachine: newFakeStateMachine(),
    }); err != nil {
        t.Fatalf("OpenGroup() error = %v", err)
    }
    st, err := rt.Status(40)
    if err != nil {
        t.Fatalf("Status() error = %v", err)
    }
    if st.AppliedIndex != 7 {
        t.Fatalf("Status().AppliedIndex = %d", st.AppliedIndex)
    }
}

func TestReadyPipelineRestoresSnapshotBeforeApplyingEntries(t *testing.T) {
    // assert fake FSM restore runs before post-snapshot entries apply
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./multiraft -run 'Test(OpenGroupRestoresAppliedIndexFromStorage|ReadyPipelineRestoresSnapshotBeforeApplyingEntries)' -v`

Expected: FAIL because recovery state and snapshot restore ordering are incomplete.

- [ ] **Step 3: Implement recovery and restore ordering**

Implementation details:
- initialize in-memory group status from `BootstrapState`
- when ready contains snapshot, call `StateMachine.Restore` before applying subsequent entries
- call `Storage.Save` for snapshot metadata before restore
- keep `AppliedIndex` and `CommitIndex` consistent after recovery

- [ ] **Step 4: Re-run recovery tests and full package tests**

Run: `go test ./multiraft -run 'Test(OpenGroupRestoresAppliedIndexFromStorage|ReadyPipelineRestoresSnapshotBeforeApplyingEntries)' -v`

Expected: PASS

Run: `go test ./multiraft/...`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add multiraft/group.go multiraft/ready.go multiraft/recovery_test.go multiraft/testenv_test.go
git commit -m "feat: add multiraft recovery path"
```

## Task 8: Add example coverage and final verification

**Files:**
- Create: `multiraft/example_test.go`
- Modify: `multiraft/doc.go`

- [ ] **Step 1: Write the example test to match the approved API**

```go
func ExampleRuntime() {
    rt, _ := multiraft.New(multiraft.Options{
        NodeID:       1,
        TickInterval: 100 * time.Millisecond,
        Workers:      1,
        Transport:    newNoopTransport(),
        Raft: multiraft.RaftOptions{
            ElectionTick:  10,
            HeartbeatTick: 1,
            PreVote:       true,
            CheckQuorum:   true,
        },
    })
    defer rt.Close()
}
```

- [ ] **Step 2: Run the example and package tests**

Run: `go test ./multiraft -run ExampleRuntime -v`

Expected: PASS

Run: `go test ./multiraft/...`

Expected: PASS

- [ ] **Step 3: Run repository-wide verification**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 4: Review exported API for accidental leaks**

Checklist:
- no exported `RawNode`
- no exported `Ready`
- no exported scheduler types
- no helper types that force callers to manage ready advancement manually

- [ ] **Step 5: Commit**

```bash
git add multiraft/example_test.go multiraft/doc.go
git commit -m "docs: add multiraft usage example"
```

## Final Verification Checklist

- [ ] `go test ./multiraft/...`
- [ ] `go test ./...`
- [ ] Confirm the example uses only the approved API
- [ ] Confirm `Future.Wait()` resolves after local apply
- [ ] Confirm `Status()` never exposes mutable internal state
- [ ] Confirm package docs explain the three injected interfaces clearly

## Handoff Notes

- Implement the tasks in order; later tasks assume earlier scaffolding exists.
- Do not add `ReadIndex`, auto-lazy group creation, or default network/storage implementations during this plan.
- If `etcd/raft/v3` introduces API mismatches versus the spec, adjust internals, not the approved public API, unless the spec is explicitly revised.
