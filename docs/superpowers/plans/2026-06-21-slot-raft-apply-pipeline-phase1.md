# Slot Raft Apply Pipeline Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move normal Slot Raft committed-entry FSM apply off the Raft worker so slow business apply no longer blocks ticks, inbound Raft messages, Ready persistence, outbound Raft messages, or `RawNode.Advance`.

**Architecture:** Keep `RawNode` owned by the existing slot worker. For normal entries without snapshots or configuration changes, the worker persists Ready, sends Raft messages, enqueues cloned committed entries to a per-slot serial apply pipeline, calls `Advance`, and returns to Raft work. The apply pipeline applies entries in index order, marks the durable applied index, updates Slot status, and resolves proposal futures.

**Tech Stack:** Go, `go.etcd.io/raft/v3`, existing `pkg/slot/multiraft` tests and fake storage/state-machine helpers.

---

## Scope Boundary

This is phase 1 of `docs/superpowers/specs/2026-06-21-slot-raft-core-apply-pipeline-design.md`.

Included:

- Normal `raftpb.EntryNormal` committed-entry apply pipeline.
- Durable applied-index tracking separate from `RawNode.BasicStatus().Applied`.
- Slot close/runtime close waits for asynchronous apply completion.
- Existing synchronous behavior preserved for snapshots and configuration changes.

Deferred to later plans:

- Proposal admission classes and background QoS.
- Conversation active caller retry/backoff changes.
- Public metrics additions beyond preserving `ApplyStateObserver` semantics.
- Moving manual and automatic compaction fully out of the Raft core hot path.
- Full async snapshot and configuration-change barrier implementation.

## File Structure

- Modify `pkg/slot/multiraft/runtime.go`
  - Add `Runtime.apply *applyPipeline`.
  - Start the apply pipeline in `New`.
  - Close the apply pipeline after Raft workers stop in `Close`.

- Modify `pkg/slot/multiraft/api.go`
  - Pass the runtime apply pipeline into `newSlot`.
  - Make `CloseSlot` wait for both core processing and apply pipeline work for that slot.

- Modify `pkg/slot/multiraft/slot.go`
  - Add Slot fields for `apply *applyPipeline`, durable applied index, and apply wait state.
  - Split `processReady` into synchronous barrier and asynchronous normal-entry paths.
  - Keep all `RawNode` calls on the slot worker.
  - Update `applyBasicStatusLocked` to report durable applied index, not optimistic `RawNode` applied index.

- Create `pkg/slot/multiraft/apply_pipeline.go`
  - Implement a sharded apply pipeline with per-slot serial execution.
  - Apply normal entries, mark durable applied index, complete futures, and record fatal errors.

- Modify `pkg/slot/multiraft/proposal_test.go`
  - Add failing tests for normal-entry apply not blocking ticks and proposal futures still waiting for apply.

- Modify `pkg/slot/multiraft/step_test.go`
  - Add runtime-close regression coverage for asynchronous apply.

- Modify `pkg/slot/multiraft/control_test.go`
  - Add a regression test that documents configuration changes remain synchronous barriers in phase 1.

## Task 1: Add Liveness Red Test For Slow Normal Apply

**Files:**
- Modify: `pkg/slot/multiraft/proposal_test.go`

- [ ] **Step 1: Write the failing test**

Add this test near `TestProposeWaitBlocksUntilReadyBatchFullyCompletes`:

```go
func TestNormalEntryApplyDoesNotBlockTickProcessing(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(184)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	beforeTicks := slotTickCount(rt, slotID)
	fut, err := rt.Propose(context.Background(), slotID, proposalString("slow-apply"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	g.markTickPending()
	rt.scheduler.enqueue(slotID)

	waitForCondition(t, func() bool {
		return slotTickCount(rt, slotID) > beforeTicks
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() while Apply blocked = %v, want %v", err, context.DeadlineExceeded)
	}

	fsm.unblock()
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() after unblock error = %v", err)
	}
}
```

- [ ] **Step 2: Verify the test fails for the current reason**

Run:

```bash
go test ./pkg/slot/multiraft -run TestNormalEntryApplyDoesNotBlockTickProcessing -count=1
```

Expected result:

```text
FAIL: condition not satisfied before timeout
```

This proves the old slot worker is stuck inside `StateMachine.Apply` and cannot process the pending tick.

## Task 2: Add Red Test For Durable Applied Status

**Files:**
- Modify: `pkg/slot/multiraft/proposal_test.go`

- [ ] **Step 1: Write the failing test**

Add this test after `TestNormalEntryApplyDoesNotBlockTickProcessing`:

```go
func TestNormalEntryAsyncApplyKeepsStatusAppliedAtDurableIndex(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(185)
	store := &internalFakeStorage{}
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	store.mu.Lock()
	baselineApplied := store.lastApplied
	store.mu.Unlock()

	fut, err := rt.Propose(context.Background(), slotID, proposalString("durable-status"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	var committed uint64
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		if err != nil {
			return false
		}
		committed = st.CommitIndex
		return st.CommitIndex > baselineApplied
	})

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if st.AppliedIndex != baselineApplied {
		t.Fatalf("Status().AppliedIndex = %d while apply blocked, want durable %d; committed=%d", st.AppliedIndex, baselineApplied, committed)
	}

	fsm.unblock()
	res, err := fut.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() after unblock error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.AppliedIndex == res.Index
	})
}
```

- [ ] **Step 2: Verify the test fails for the current reason**

Run:

```bash
go test ./pkg/slot/multiraft -run TestNormalEntryAsyncApplyKeepsStatusAppliedAtDurableIndex -count=1
```

Expected result:

```text
FAIL: condition not satisfied before timeout
```

The current runtime cannot publish a newer commit index while normal apply is blocked because `processReady` has not reached `Advance` or `refreshStatus`.

## Task 3: Track Durable Applied Index Separately

**Files:**
- Modify: `pkg/slot/multiraft/slot.go`

- [ ] **Step 1: Add durable applied state to `slot`**

Add this field near `status Status`:

```go
	// durableAppliedIndex is the highest index that completed FSM apply and Storage.MarkApplied.
	durableAppliedIndex uint64
```

Initialize it in `newSlot`:

```go
		status: Status{
			SlotID:       opts.ID,
			NodeID:       nodeID,
			LeaderID:     NodeID(state.HardState.Vote),
			CommitIndex:  state.HardState.Commit,
			AppliedIndex: appliedIndex,
		},
		durableAppliedIndex: appliedIndex,
```

- [ ] **Step 2: Add helpers**

Replace the existing `appliedIndex` helper with:

```go
func (g *slot) appliedIndex() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.durableAppliedIndex
}

func (g *slot) setDurableAppliedIndex(index uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if index > g.durableAppliedIndex {
		g.durableAppliedIndex = index
	}
	g.status.AppliedIndex = g.durableAppliedIndex
}
```

- [ ] **Step 3: Keep status applied index durable**

Change `applyBasicStatusLocked` so it does not copy `st.Applied` into public status:

```go
	g.status.CommitIndex = st.Commit
	g.status.AppliedIndex = g.durableAppliedIndex
```

Keep observer reporting durable applied state:

```go
	if observer, ok := g.observer.(ApplyStateObserver); ok && observer != nil {
		observer.SetSlotApplyState(g.id, st.Commit, g.durableAppliedIndex)
	}
```

- [ ] **Step 4: Update synchronous MarkApplied path**

In the current synchronous `processReady` code, after successful `g.storage.MarkApplied(ctx, lastApplied)`, add:

```go
		g.setDurableAppliedIndex(lastApplied)
```

- [ ] **Step 5: Run focused tests**

Run:

```bash
go test ./pkg/slot/multiraft -run 'TestProposeCorrelatesFutureByCommittedIndex|TestChangeConfigCorrelatesFutureByCommittedIndex|TestRuntimeStatusIncludesCurrentVoters' -count=1
```

Expected result:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/slot/multiraft
```

## Task 4: Add Apply Pipeline Primitive

**Files:**
- Create: `pkg/slot/multiraft/apply_pipeline.go`
- Modify: `pkg/slot/multiraft/runtime.go`
- Modify: `pkg/slot/multiraft/api.go`
- Modify: `pkg/slot/multiraft/slot.go`

- [ ] **Step 1: Create `apply_pipeline.go`**

Create the file with this implementation skeleton:

```go
package multiraft

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"go.etcd.io/raft/v3/raftpb"
)

// applyPipeline runs committed normal-entry FSM apply outside the Raft core loop.
type applyPipeline struct {
	goroutines *goroutine.Registry
	workers    int

	mu      sync.Mutex
	queues  map[SlotID]*applyQueue
	readyCh chan *applyQueue
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

type applyQueue struct {
	slotID  SlotID
	tasks   []applyTask
	running bool
	closed  bool
}

type applyTask struct {
	slot          *slot
	entries       []raftpb.Entry
	appliedBefore uint64
}

func newApplyPipeline(workers int, goroutines *goroutine.Registry) *applyPipeline {
	if workers <= 0 {
		workers = 1
	}
	p := &applyPipeline{
		goroutines: goroutines,
		workers:    workers,
		queues:     make(map[SlotID]*applyQueue),
		readyCh:    make(chan *applyQueue, workers*4),
		stopCh:     make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		goroutine.SafeGo(goroutines, "slot", "raft_apply_worker", func() {
			defer p.wg.Done()
			p.runWorker()
		})
	}
	return p
}
```

- [ ] **Step 2: Add enqueue and worker methods**

Add the following methods below the constructor:

```go
func (p *applyPipeline) enqueue(task applyTask) bool {
	if p == nil || task.slot == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	q := p.queues[task.slot.id]
	if q == nil {
		q = &applyQueue{slotID: task.slot.id}
		p.queues[task.slot.id] = q
	}
	if q.closed {
		return false
	}
	q.tasks = append(q.tasks, task)
	task.slot.beginApply()
	if !q.running {
		q.running = true
		p.scheduleLocked(q)
	}
	return true
}

func (p *applyPipeline) scheduleLocked(q *applyQueue) {
	select {
	case p.readyCh <- q:
	case <-p.stopCh:
	}
}

func (p *applyPipeline) runWorker() {
	for {
		select {
		case <-p.stopCh:
			return
		case q := <-p.readyCh:
			p.runQueue(q)
		}
	}
}

func (p *applyPipeline) runQueue(q *applyQueue) {
	for {
		task, ok := p.popTask(q)
		if !ok {
			return
		}
		task.slot.runApplyTask(context.Background(), task)
		task.slot.finishApply()
	}
}

func (p *applyPipeline) popTask(q *applyQueue) (applyTask, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(q.tasks) == 0 {
		q.running = false
		return applyTask{}, false
	}
	task := q.tasks[0]
	copy(q.tasks, q.tasks[1:])
	q.tasks[len(q.tasks)-1] = applyTask{}
	q.tasks = q.tasks[:len(q.tasks)-1]
	return task, true
}
```

- [ ] **Step 3: Add close and slot-drain methods**

Add:

```go
func (p *applyPipeline) close() {
	if p == nil {
		return
	}
	close(p.stopCh)
	p.wg.Wait()
}

func (p *applyPipeline) closeSlot(slotID SlotID) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if q := p.queues[slotID]; q != nil {
		q.closed = true
	}
	p.mu.Unlock()
}
```

`closeSlot` rejects future enqueue for the slot, but already queued tasks must drain so `slot.applying` reaches zero and `CloseSlot` can return.

- [ ] **Step 4: Wire the pipeline into `Runtime`**

In `Runtime` add:

```go
	apply *applyPipeline
```

In `New`, initialize before `rt.start()`:

```go
		apply:     newApplyPipeline(opts.Workers, opts.Goroutines),
```

In `Close`, after `r.wg.Wait()` and before clearing `r.slots`, close it:

```go
	r.apply.close()
```

- [ ] **Step 5: Pass the pipeline into new slots**

Change `newSlot` signature:

```go
func newSlot(ctx context.Context, nodeID NodeID, logger wklog.Logger, raftOpts RaftOptions, opts SlotOptions, observer SchedulerObserver, apply *applyPipeline) (*slot, error)
```

Set the slot field:

```go
		apply:       apply,
```

Update all call sites in `api.go` and tests to pass either `r.apply` or `nil` for direct unit construction.

- [ ] **Step 6: Add slot apply wait state**

In `slot` add:

```go
	applying int
```

Add helpers:

```go
func (g *slot) beginApply() {
	g.mu.Lock()
	g.applying++
	g.mu.Unlock()
}

func (g *slot) finishApply() {
	g.mu.Lock()
	if g.applying > 0 {
		g.applying--
	}
	if g.cond != nil {
		g.cond.Broadcast()
	}
	g.mu.Unlock()
}

func (g *slot) waitIdleLocked() {
	for g.processing || g.applying > 0 {
		g.cond.Wait()
	}
}
```

Use `waitIdleLocked` from `CloseSlot` after marking the slot closed and deleting it from `r.slots`.

- [ ] **Step 7: Run compile check**

Run:

```bash
go test ./pkg/slot/multiraft -run TestNewValidatesRequiredOptions -count=1
```

Expected result:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/slot/multiraft
```

## Task 5: Move Normal Entries To Async Apply

**Files:**
- Modify: `pkg/slot/multiraft/slot.go`
- Modify: `pkg/slot/multiraft/apply_pipeline.go`

- [ ] **Step 1: Add barrier detection**

Add helpers near `countTrackedReadyEntries`:

```go
func readyRequiresSynchronousApply(ready raft.Ready) bool {
	if !raft.IsEmptySnap(ready.Snapshot) {
		return true
	}
	for _, entry := range ready.CommittedEntries {
		if entry.Type == raftpb.EntryConfChange {
			return true
		}
	}
	return false
}

func cloneEntries(entries []raftpb.Entry) []raftpb.Entry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]raftpb.Entry, len(entries))
	for i, entry := range entries {
		out[i] = cloneEntry(entry, true)
	}
	return out
}
```

- [ ] **Step 2: Split synchronous Ready handling**

Extract the current post-send apply body into:

```go
func (g *slot) processReadySynchronously(ctx context.Context, ready raft.Ready) (bool, bool) {
	lastApplied := g.appliedIndex()
	appliedBeforeReady := lastApplied
	resolutions := g.takeResolutionBuffer()
	defer func() {
		g.releaseResolutionBuffer(resolutions)
	}()

	if !raft.IsEmptySnap(ready.Snapshot) {
		if err := g.stateMachine.Restore(ctx, Snapshot{
			Index: ready.Snapshot.Metadata.Index,
			Term:  ready.Snapshot.Metadata.Term,
			Data:  append([]byte(nil), ready.Snapshot.Data...),
		}); err != nil {
			g.fail(err)
			return true, false
		}
		lastApplied = ready.Snapshot.Metadata.Index
	}

	batchSM, canBatch := g.stateMachine.(BatchStateMachine)
	var configChanged bool
	resolutions, configChanged = g.applyCommittedEntries(ctx, ready.CommittedEntries, &lastApplied, resolutions, batchSM, canBatch)
	if g.fatalErr != nil {
		return true, false
	}

	if lastApplied > appliedBeforeReady {
		started := time.Now()
		err := g.storage.MarkApplied(ctx, lastApplied)
		g.observeResolutionFutures(resolutions, "meta_create_slot_mark_applied", err, time.Since(started))
		if err != nil {
			g.fail(err)
			return true, false
		}
		g.setDurableAppliedIndex(lastApplied)
	}

	g.rawNode.Advance(ready)
	g.refreshStatus()
	g.completeResolutions(resolutions)
	if g.compactor.shouldCompact(lastApplied) || (configChanged && g.compactor.shouldRefreshAfterConfigChange(lastApplied)) {
		if err := g.compactLog(ctx, lastApplied); err != nil {
			g.logCompactionWarning(err, lastApplied)
		} else {
			g.compactor.recordSnapshot(lastApplied)
		}
	}
	return true, g.rawNode.HasReady()
}
```

- [ ] **Step 3: Add asynchronous normal path**

Add:

```go
func (g *slot) processReadyAsyncNormal(ctx context.Context, ready raft.Ready) (bool, bool) {
	task := applyTask{
		slot:          g,
		entries:       cloneEntries(ready.CommittedEntries),
		appliedBefore: g.appliedIndex(),
	}
	if len(task.entries) > 0 {
		if ok := g.apply.enqueue(task); !ok {
			g.fail(ErrSlotClosed)
			return true, false
		}
	}
	g.rawNode.Advance(ready)
	g.refreshStatus()
	return true, g.rawNode.HasReady()
}
```

If `g.apply` is nil in direct tests, fall back to synchronous handling:

```go
	if g.apply == nil {
		return g.processReadySynchronously(ctx, ready)
	}
```

- [ ] **Step 4: Update `processReady` dispatcher**

After persistence, tracking, and send, replace the old apply body with:

```go
	if readyRequiresSynchronousApply(ready) {
		return g.processReadySynchronously(ctx, ready)
	}
	return g.processReadyAsyncNormal(ctx, ready)
```

- [ ] **Step 5: Implement apply task execution**

Add this method in `apply_pipeline.go`:

```go
func (g *slot) runApplyTask(ctx context.Context, task applyTask) {
	lastApplied := task.appliedBefore
	resolutions := g.takeResolutionBuffer()
	defer func() {
		g.releaseResolutionBuffer(resolutions)
	}()

	batchSM, canBatch := g.stateMachine.(BatchStateMachine)
	resolutions, _ = g.applyCommittedEntries(ctx, task.entries, &lastApplied, resolutions, batchSM, canBatch)
	if g.fatalErr != nil {
		return
	}

	if lastApplied > task.appliedBefore {
		started := time.Now()
		err := g.storage.MarkApplied(ctx, lastApplied)
		g.observeResolutionFutures(resolutions, "meta_create_slot_mark_applied", err, time.Since(started))
		if err != nil {
			g.fail(err)
			return
		}
		g.setDurableAppliedIndex(lastApplied)
	}

	g.refreshStatus()
	g.completeResolutions(resolutions)
}
```

Do not call `g.rawNode` from `runApplyTask`.

- [ ] **Step 6: Run the red tests**

Run:

```bash
go test ./pkg/slot/multiraft -run 'TestNormalEntryApplyDoesNotBlockTickProcessing|TestNormalEntryAsyncApplyKeepsStatusAppliedAtDurableIndex' -count=1
```

Expected result:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/slot/multiraft
```

## Task 6: Preserve Close And Failure Semantics

**Files:**
- Modify: `pkg/slot/multiraft/api.go`
- Modify: `pkg/slot/multiraft/apply_pipeline.go`
- Modify: `pkg/slot/multiraft/step_test.go`
- Modify: `pkg/slot/multiraft/proposal_test.go`

- [ ] **Step 1: Update `CloseSlot` to wait for apply**

Replace the close wait loop in `CloseSlot`:

```go
	for g.processing {
		g.cond.Wait()
	}
```

with:

```go
	if r.apply != nil {
		r.apply.closeSlot(slotID)
	}
	g.waitIdleLocked()
```

- [ ] **Step 2: Add runtime-close regression test**

Add to `step_test.go` near `TestCloseSlotStopsFurtherProcessing`:

```go
func TestRuntimeCloseWaitsForAsyncApply(t *testing.T) {
	rt, err := New(Options{
		NodeID:       1,
		TickInterval: time.Hour,
		Workers:      1,
		Transport:    &internalFakeTransport{},
		Raft: RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	slotID := SlotID(186)
	fsm := newBlockingStateMachine()
	err = rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	fut, err := rt.Propose(context.Background(), slotID, proposalString("close-runtime"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rt.Close()
	}()

	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before async apply finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	fsm.unblock()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not return after apply completed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("Wait() error = %v, want %v", err, ErrRuntimeClosed)
	}
}
```

- [ ] **Step 3: Make runtime close wait for slot apply**

In `Runtime.Close`, after `r.wg.Wait()` and before `r.apply.close()`, wait for each slot:

```go
	for _, g := range r.slots {
		g.mu.Lock()
		g.waitIdleLocked()
		g.mu.Unlock()
	}
	r.apply.close()
```

The futures are already failed under the runtime lock before the wait, preserving current shutdown semantics.

- [ ] **Step 4: Run close/failure tests**

Run:

```bash
go test ./pkg/slot/multiraft -run 'TestCloseSlotStopsFurtherProcessing|TestRuntimeCloseWaitsForAsyncApply|TestFatalSlotRejectsFutureOperations|TestApplyFatalStopsSlot|TestMarkAppliedFailureFailsFutureAndStopsAdvance' -count=1
```

Expected result:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/slot/multiraft
```

## Task 7: Keep Barriers Synchronous In Phase 1

**Files:**
- Modify: `pkg/slot/multiraft/control_test.go`
- Modify: `pkg/slot/multiraft/recovery_test.go`
- Modify: `pkg/slot/multiraft/slot.go`

- [ ] **Step 1: Add explicit barrier regression test**

Add to `control_test.go`:

```go
func TestConfigChangeStillUsesSynchronousBarrier(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(187)
	store := newBlockingMarkAppliedStorage()

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	store.internalFakeStorage.mu.Lock()
	baselineApplied := store.internalFakeStorage.lastApplied
	store.internalFakeStorage.mu.Unlock()
	store.armAfter(baselineApplied + 1)

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 2,
	})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}

	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("MarkApplied() did not start")
	}

	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	beforeTicks := slotTickCount(rt, slotID)
	g.markTickPending()
	rt.scheduler.enqueue(slotID)

	time.Sleep(100 * time.Millisecond)
	if got := slotTickCount(rt, slotID); got != beforeTicks {
		t.Fatalf("tick count advanced across config barrier: got %d want %d", got, beforeTicks)
	}

	store.unblock()
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() after unblock error = %v", err)
	}
}
```

This test documents phase-1 behavior: configuration changes remain synchronous barriers until the dedicated barrier implementation lands.

- [ ] **Step 2: Run barrier and recovery tests**

Run:

```bash
go test ./pkg/slot/multiraft -run 'TestConfigChangeStillUsesSynchronousBarrier|TestChangeConfigWaitBlocksUntilReadyBatchFullyCompletes|TestChangeConfigRejectsWhileAnotherConfigChangeIsPending|TestRestoreFatalStopsSlot|TestOpenSlotRestoresSnapshotIntoStateMachine' -count=1
```

Expected result:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/slot/multiraft
```

## Task 8: Run Package Regression And Commit

**Files:**
- Modify: all files changed by tasks 1-7.

- [ ] **Step 1: Run full multiraft package tests**

Run:

```bash
go test ./pkg/slot/multiraft -count=1
```

Expected result:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/slot/multiraft
```

- [ ] **Step 2: Run clusterv2 Slot propose smoke tests**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestDefaultSlotProposer|TestNodeMeta|TestRouteAuthority|TestDefaultSlots' -count=1
```

Expected result:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/clusterv2
```

- [ ] **Step 3: Check formatting and whitespace**

Run:

```bash
gofmt -w pkg/slot/multiraft/apply_pipeline.go pkg/slot/multiraft/runtime.go pkg/slot/multiraft/api.go pkg/slot/multiraft/slot.go pkg/slot/multiraft/proposal_test.go pkg/slot/multiraft/step_test.go pkg/slot/multiraft/control_test.go
git diff --check
```

Expected result:

```text
```

`git diff --check` should print nothing and exit successfully.

- [ ] **Step 4: Review staged scope**

Run:

```bash
git diff --stat
git status --short
```

Expected changed implementation files:

```text
pkg/slot/multiraft/apply_pipeline.go
pkg/slot/multiraft/runtime.go
pkg/slot/multiraft/api.go
pkg/slot/multiraft/slot.go
pkg/slot/multiraft/proposal_test.go
pkg/slot/multiraft/step_test.go
pkg/slot/multiraft/control_test.go
```

Other existing dirty files in the original checkout must not be staged.

- [ ] **Step 5: Commit the phase**

Run:

```bash
git add pkg/slot/multiraft/apply_pipeline.go pkg/slot/multiraft/runtime.go pkg/slot/multiraft/api.go pkg/slot/multiraft/slot.go pkg/slot/multiraft/proposal_test.go pkg/slot/multiraft/step_test.go pkg/slot/multiraft/control_test.go
git commit -m "refactor: decouple slot raft normal apply"
```

Expected result:

```text
[branch <sha>] refactor: decouple slot raft normal apply
```

## Self-Review Notes

- Spec coverage: this plan implements the normal-entry Raft core/apply split, future resolution after apply, durable applied-index reporting, close semantics, and synchronous phase-1 barriers.
- Deferred coverage: proposal QoS, conversation active background retry, new metrics, and full async conf/snapshot barriers need separate plans because they touch clusterv2/internalv2 caller behavior and monitor surfaces.
- Etcd/raft contract: `Node.Ready` documentation permits `Advance` while applying the last Ready, but no committed entries from a later Ready may be applied before earlier entries finish. The per-slot serial apply queue enforces that ordering.
- Public API churn: this plan avoids new exported configuration and keeps phase-1 behavior behind the existing `Runtime` API.
