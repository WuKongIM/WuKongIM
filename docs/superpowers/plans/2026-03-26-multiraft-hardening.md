# Multi-Raft Hardening Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the `multiraft` runtime so proposal/config futures are correlated by committed log index instead of FIFO, state machine failures are treated as fatal, and the runtime no longer has known concurrency and lifecycle correctness bugs.

**Architecture:** Keep the public `Runtime` API shape, but tighten semantics: `Propose` and `ChangeConfig` become leader-only admission paths, each accepted request is tracked through a local submission queue until a concrete raft log index is assigned, and committed entries resolve futures by index. Introduce an explicit group failure state so fatal state-machine errors stop further processing, fail pending work, and surface consistently through all entrypoints. While touching the runtime loop, fix status synchronization, close semantics, and scheduler backpressure hotspots.

**Tech Stack:** Go, `go.etcd.io/raft/v3`, package-local in-memory test fakes, race detector, async e2e tests.

---

### Task 1: Lock In Failure And Admission Semantics

**Files:**
- Modify: `multiraft/errors.go`
- Modify: `multiraft/api.go`
- Modify: `multiraft/group.go`
- Modify: `multiraft/runtime.go`
- Test: `multiraft/runtime_test.go`
- Test: `multiraft/proposal_test.go`

- [ ] **Step 1: Write failing tests for leader-only admission and fatal group behavior**

Add tests that prove:
- `Propose` on a follower returns `ErrNotLeader`
- `ChangeConfig` on a follower returns `ErrNotLeader`
- once a group hits a fatal state-machine error, later `Step`, `Propose`, `ChangeConfig`, `TransferLeadership`, and `Status` return that fatal error

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./multiraft -run 'Test(ProposeRejectsFollower|ChangeConfigRejectsFollower|FatalGroupRejectsFutureOperations)' -v`

Expected: FAIL because current code still accepts follower proposals/config changes and has no persisted fatal group state.

- [ ] **Step 3: Add explicit group failure state and shared admission checks**

Implement in `multiraft/group.go` and `multiraft/api.go`:
- a stored fatal error on `group`
- a helper that returns `ErrGroupClosed`, fatal error, or nil
- shared checks used by all public entrypoints and worker processing before mutating raft state

- [ ] **Step 4: Re-run the focused tests**

Run: `go test ./multiraft -run 'Test(ProposeRejectsFollower|ChangeConfigRejectsFollower|FatalGroupRejectsFutureOperations)' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add multiraft/errors.go multiraft/api.go multiraft/group.go multiraft/runtime.go multiraft/runtime_test.go multiraft/proposal_test.go
git commit -m "fix: define multiraft fatal group semantics"
```

### Task 2: Replace FIFO Future Resolution With Log-Index Correlation

**Files:**
- Modify: `multiraft/group.go`
- Modify: `multiraft/api.go`
- Modify: `multiraft/future.go`
- Test: `multiraft/proposal_test.go`
- Test: `multiraft/control_test.go`
- Test: `multiraft/e2e_test.go`

- [ ] **Step 1: Write failing tests that expose FIFO mis-correlation**

Add tests that prove:
- a locally proposed command is not resolved by another node's committed normal entry
- a locally proposed config change is not resolved by an unrelated committed conf change
- the resolved future carries the exact committed index assigned to the local proposal

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./multiraft -run 'Test(ProposeCorrelatesFutureByCommittedIndex|ChangeConfigCorrelatesFutureByCommittedIndex|RemoteCommitDoesNotResolveLocalFuture)' -v`

Expected: FAIL because current code still resolves futures by local FIFO order.

- [ ] **Step 3: Introduce two-stage tracking for accepted local work**

Implement in `multiraft/group.go`:
- `submittedProposals` and `submittedConfigs` queues for work accepted by `RawNode`
- `pendingProposalsByIndex` and `pendingConfigsByIndex` maps keyed by raft log index
- logic in `processReady` to bind newly appended local entries to indices from `ready.Entries`
- resolution logic in committed-entry handling that looks up the exact `entry.Index`

Keep `Propose` and `ChangeConfig` leader-only so only local leader-originated entries enter the correlation tables.

- [ ] **Step 4: Re-run the focused tests and the async e2e suite**

Run: `go test ./multiraft -run 'Test(ProposeCorrelatesFutureByCommittedIndex|ChangeConfigCorrelatesFutureByCommittedIndex|RemoteCommitDoesNotResolveLocalFuture)' -v`

Run: `go test ./multiraft -run 'TestThreeNodeClusterElectsLeaderAndReplicatesOverAsyncNetwork' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add multiraft/group.go multiraft/api.go multiraft/future.go multiraft/proposal_test.go multiraft/control_test.go multiraft/e2e_test.go
git commit -m "fix: correlate multiraft futures by log index"
```

### Task 3: Make Ready Processing Durable And Fatal-Safe

**Files:**
- Modify: `multiraft/group.go`
- Modify: `multiraft/ready.go`
- Modify: `multiraft/testenv_test.go`
- Test: `multiraft/proposal_test.go`
- Test: `multiraft/recovery_test.go`

- [ ] **Step 1: Write failing tests for ready error handling**

Add tests that prove:
- `persistReady` failure does not call `Advance`
- `StateMachine.Restore` fatal error transitions the group into failed state and stops further progress
- `StateMachine.Apply` fatal error transitions the group into failed state and fails pending futures

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./multiraft -run 'Test(ReadyPersistenceFailureDoesNotAdvance|RestoreFatalStopsGroup|ApplyFatalStopsGroup)' -v`

Expected: FAIL because current code advances after persistence failure and ignores restore/apply fatal errors.

- [ ] **Step 3: Change ready handling so state is never advanced past failed work**

Implement in `multiraft/group.go`:
- no `Advance` when `persistReady` fails
- immediate transition to fatal state on `Restore` or `Apply` error
- stop processing later committed entries once a fatal error occurs
- only call `MarkApplied` after all applies/restores in the batch succeed

Preserve the existing ordering guarantee: persist first, send messages after persistence, then restore/apply, then mark applied, then advance.

- [ ] **Step 4: Re-run the focused tests**

Run: `go test ./multiraft -run 'Test(ReadyPersistenceFailureDoesNotAdvance|RestoreFatalStopsGroup|ApplyFatalStopsGroup|ReadyPipelinePersistsBeforeApply)' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add multiraft/group.go multiraft/ready.go multiraft/testenv_test.go multiraft/proposal_test.go multiraft/recovery_test.go
git commit -m "fix: make multiraft ready processing fatal-safe"
```

### Task 4: Fix Status Synchronization And Group Close Semantics

**Files:**
- Modify: `multiraft/api.go`
- Modify: `multiraft/group.go`
- Modify: `multiraft/runtime.go`
- Test: `multiraft/runtime_test.go`
- Test: `multiraft/step_test.go`

- [ ] **Step 1: Write failing tests for status safety and close barriers**

Add tests that prove:
- concurrent `Status` polling does not trip the race detector
- `CloseGroup` returns only after the group is no longer allowed to process queued work
- in-flight `Step` or `Propose` racing with `CloseGroup` cannot enqueue new work after close wins

- [ ] **Step 2: Run the focused tests with the race detector**

Run: `go test -race ./multiraft -run 'Test(StatusIsRaceFree|CloseGroupStopsFurtherProcessing|CloseGroupBlocksNewAdmissions)' -v`

Expected: FAIL because current code reads `g.status` without synchronization and has no close barrier.

- [ ] **Step 3: Implement synchronized status snapshots and worker-side close checks**

Implement:
- a safe status snapshot path under `g.mu` or an equivalent atomic snapshot
- worker checks that stop processing closed or failed groups before requests, controls, ticks, and ready handling
- close logic that drains or rejects queued work deterministically before returning

- [ ] **Step 4: Re-run the focused tests and the package race suite**

Run: `go test -race ./multiraft -run 'Test(StatusIsRaceFree|CloseGroupStopsFurtherProcessing|CloseGroupBlocksNewAdmissions)' -v`

Run: `go test -race ./multiraft/...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add multiraft/api.go multiraft/group.go multiraft/runtime.go multiraft/runtime_test.go multiraft/step_test.go
git commit -m "fix: harden multiraft close and status paths"
```

### Task 5: Remove API Drift And Scheduler Backpressure Traps

**Files:**
- Modify: `multiraft/api.go`
- Modify: `multiraft/group.go`
- Modify: `multiraft/runtime.go`
- Modify: `multiraft/scheduler.go`
- Modify: `multiraft/types.go`
- Test: `multiraft/runtime_test.go`
- Test: `multiraft/scheduler_test.go`

- [ ] **Step 1: Write failing tests for scheduler pressure and API consistency**

Add tests that prove:
- the ticker path does not enqueue while holding `Runtime.mu`
- high group counts do not block runtime control paths on scheduler channel pressure
- exported learner-related API either works end-to-end or is removed from the public contract

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./multiraft -run 'Test(RuntimeTickLoopDoesNotHoldLockAcrossEnqueue|SchedulerBackpressureDoesNotBlockRuntime|PublicAPIReflectsLearnerSupport)' -v`

Expected: FAIL because current runtime enqueues under `RLock` and learner-facing API is partially implemented.

- [ ] **Step 3: Tighten runtime and API behavior**

Implement:
- collect group IDs under `RLock`, then enqueue after unlocking
- avoid scheduler operations that can block while runtime/global locks are held
- either fully implement `BootstrapGroupRequest.Learners` and `RoleLearner`, or remove those exported fields/constants from the public API and update docs/tests accordingly

- [ ] **Step 4: Re-run the focused tests and the full package suite**

Run: `go test ./multiraft -run 'Test(RuntimeTickLoopDoesNotHoldLockAcrossEnqueue|SchedulerBackpressureDoesNotBlockRuntime|PublicAPIReflectsLearnerSupport)' -v`

Run: `go test ./multiraft/...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add multiraft/api.go multiraft/group.go multiraft/runtime.go multiraft/scheduler.go multiraft/types.go multiraft/runtime_test.go multiraft/scheduler_test.go
git commit -m "fix: align multiraft api and scheduler behavior"
```

### Final Verification

- [ ] Run: `go test ./multiraft/...`
- [ ] Run: `go test -race ./multiraft/...`
- [ ] Run: `go test ./...`
- [ ] Confirm the review findings around race, FIFO correlation, fatal apply/restore handling, close semantics, and scheduler backpressure are all covered by tests.
