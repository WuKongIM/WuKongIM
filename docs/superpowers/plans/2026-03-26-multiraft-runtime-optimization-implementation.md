# MultiRaft Runtime Optimization Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce `multiraft` runtime hot-path allocations and avoidable slice copying without changing any public API or weakening the current correctness semantics.

**Architecture:** The optimization stays inside the existing `multiraft` package. Most code changes are localized to `multiraft/group.go`, where queue draining, ready-batch bookkeeping, pending-future tracking, and transport wrapping already live; tests stay in the existing behavior-focused files so the optimization is validated against the hardened runtime semantics rather than in isolation only.

**Tech Stack:** Go 1.23, `go.etcd.io/raft/v3`, standard library `context/sync/testing/time`

---

## File Structure

### Production files

- Modify: `multiraft/group.go`
  Responsibility: add reusable request/control/resolution scratch buffers, pre-grow pending-future maps, and reuse the outer transport envelope slice while preserving deep-cloned message payloads.

### Test files

- Modify: `multiraft/step_test.go`
  Responsibility: queue-drain regression coverage and `wrapMessages`/envelope reuse safety checks.
- Modify: `multiraft/proposal_test.go`
  Responsibility: multi-batch proposal resolution regression tests and pending-proposal correlation checks under bursty traffic.
- Modify: `multiraft/control_test.go`
  Responsibility: multi-batch config-change resolution regression tests and pending-config correlation checks.
- Modify: `multiraft/benchmark_test.go`
  Responsibility: verify the existing realistic multi-node benchmarks still compile and are used for post-change validation.

### Existing spec and supporting docs

- Reference: `docs/superpowers/specs/2026-03-26-multiraft-runtime-optimization-design.md`
  Responsibility: approved optimization scope, non-goals, validation targets, and ordering constraints.

## Implementation Notes

- Follow `@superpowers:test-driven-development` task-by-task. Write the test first, run it red, then implement the minimum code to turn it green.
- Do not change public types or method signatures in `multiraft/api.go`, `multiraft/types.go`, or `multiraft/future.go`.
- Do not change semantics already hardened in the branch:
  - proposal/config correlation must remain `(index, term)` based
  - `Apply()`, `Restore()`, and `MarkApplied()` failures must remain fatal
  - future success must still happen only after the full ready batch completes
  - transport payload cloning must remain safe for async transports
- Keep reuse buffers group-local. Do not introduce global pools.
- Prefer helper methods in `multiraft/group.go` when they make queue or buffer ownership easier to test directly.

## Task 1: Implement reusable request/control drains and reusable resolution buffers

**Files:**
- Modify: `multiraft/group.go`
- Modify: `multiraft/step_test.go`
- Modify: `multiraft/proposal_test.go`
- Modify: `multiraft/control_test.go`

- [ ] **Step 1: Write the failing drain-helper and resolution-buffer tests**

```go
func TestRequestDrainHelperMovesQueuedMessagesIntoReusableWorkSlice(t *testing.T) {
    g := newTestGroupForDrain(t)
    if err := g.enqueueRequest(raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1}); err != nil {
        t.Fatalf("enqueueRequest() error = %v", err)
    }

    batch := g.takeRequestBatch()
    if len(batch) != 1 {
        t.Fatalf("len(batch) = %d, want 1", len(batch))
    }
    if len(g.requests) != 0 {
        t.Fatalf("len(g.requests) = %d, want 0", len(g.requests))
    }
}

func TestControlDrainHelperMovesQueuedActionsIntoReusableWorkSlice(t *testing.T) {
    g := newLeaderTestGroupForDrain(t)
    if err := g.enqueueControl(controlAction{kind: controlTransferLeader, target: 2}); err != nil {
        t.Fatalf("enqueueControl() error = %v", err)
    }

    batch := g.takeControlBatch()
    if len(batch) != 1 {
        t.Fatalf("len(batch) = %d, want 1", len(batch))
    }
    if len(g.controls) != 0 {
        t.Fatalf("len(g.controls) = %d, want 0", len(g.controls))
    }
}
func TestResolutionBufferHelpersReuseBackingSlice(t *testing.T) {
    g := &group{}

    buf := g.takeResolutionBuffer()
    buf = append(buf, futureResolution{index: 1})
    g.releaseResolutionBuffer(buf)

    reused := g.takeResolutionBuffer()
    if len(reused) != 0 {
        t.Fatalf("len(reused) = %d, want 0", len(reused))
    }
    if cap(reused) == 0 {
        t.Fatal("expected reused capacity to be retained")
    }
}
```

Notes:
- use package-private helpers or tiny test helpers added in the test file
- do not assert implementation details beyond queue ownership and reusable-buffer behavior

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./multiraft -run 'Test(RequestDrainHelperMovesQueuedMessagesIntoReusableWorkSlice|ControlDrainHelperMovesQueuedActionsIntoReusableWorkSlice|ResolutionBufferHelpersReuseBackingSlice)' -v`

Expected: FAIL because the new queue-drain and resolution-buffer helpers do not exist yet.

- [ ] **Step 3: Add reusable scratch-buffer fields to `group`**

```go
type group struct {
    requests       []raftpb.Message
    requestWorkBuf []raftpb.Message
    controls       []controlAction
    controlWorkBuf []controlAction
    resolutionBuf  []futureResolution
}
```

Requirements:
- keep buffers owned by a single group instance
- do not expose these buffers through the public API

- [ ] **Step 4: Implement drain helpers that swap queue ownership instead of copying**

```go
func (g *group) takeRequestBatch() []raftpb.Message {
    g.mu.Lock()
    defer g.mu.Unlock()
    batch := g.requests
    g.requests = g.requestWorkBuf[:0]
    g.requestWorkBuf = batch[:0]
    return batch
}

func (g *group) takeControlBatch() []controlAction { /* same pattern */ }
```

Implementation details:
- preserve `requestCount` / other accounting behavior already used by tests
- update `processRequests()` and `processControls()` to consume the drained batch and return the reused slice to the group
- keep all locking and admission checks equivalent to the current behavior

- [ ] **Step 5: Rework `processReady()` to reuse a group-owned resolution buffer**

```go
resolutions := g.takeResolutionBuffer()
defer g.releaseResolutionBuffer(resolutions[:0])
```

Implementation details:
- collect `futureResolution` entries in the reused buffer
- continue resolving futures only after persist/send/apply/mark-applied/advance/status-refresh succeeds
- do not change fatal behavior on apply/restore/mark-applied errors

- [ ] **Step 6: Re-run the targeted tests plus existing ready-path tests**

Run: `go test ./multiraft -run 'Test(RequestDrainHelperMovesQueuedMessagesIntoReusableWorkSlice|ControlDrainHelperMovesQueuedActionsIntoReusableWorkSlice|ResolutionBufferHelpersReuseBackingSlice|ProposeWaitBlocksUntilReadyBatchFullyCompletes|ChangeConfigWaitBlocksUntilReadyBatchFullyCompletes)' -v`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add multiraft/group.go multiraft/step_test.go multiraft/proposal_test.go multiraft/control_test.go
git commit -m "perf: reuse multiraft drain and resolution buffers"
```

## Task 2: Add pending-future capacity helpers and pre-grow pending maps

**Files:**
- Modify: `multiraft/group.go`
- Modify: `multiraft/proposal_test.go`
- Modify: `multiraft/control_test.go`

- [ ] **Step 1: Write failing helper tests and burst-safety tests**

```go
func TestCountTrackedReadyEntriesIgnoresEmptyNormalEntries(t *testing.T) {
    proposals, configs := countTrackedReadyEntries([]raftpb.Entry{
        {Type: raftpb.EntryNormal},
        {Type: raftpb.EntryNormal, Data: []byte("p")},
        {Type: raftpb.EntryConfChange, Data: []byte("c")},
    })

    if proposals != 1 || configs != 1 {
        t.Fatalf("counts = (%d, %d), want (1, 1)", proposals, configs)
    }
}

func TestEnsurePendingProposalCapacityPreservesTrackedFuture(t *testing.T) {
    fut := newFuture()
    g := &group{
        pendingProposals: map[uint64]trackedFuture{
            7: {future: fut, term: 3},
        },
    }

    g.ensurePendingProposalCapacity(4)

    tracked, ok := g.pendingProposals[7]
    if !ok || tracked.future != fut || tracked.term != 3 {
        t.Fatalf("tracked future was lost: %+v ok=%v", tracked, ok)
    }
}

func TestEnsurePendingConfigCapacityPreservesTrackedFuture(t *testing.T) {
    fut := newFuture()
    g := &group{
        pendingConfigs: map[uint64]trackedFuture{
            9: {future: fut, term: 4},
        },
    }

    g.ensurePendingConfigCapacity(3)

    tracked, ok := g.pendingConfigs[9]
    if !ok || tracked.future != fut || tracked.term != 4 {
        t.Fatalf("tracked future was lost: %+v ok=%v", tracked, ok)
    }
}
```

Notes:
- add one proposal-side and one config-side burst regression test as safety, but use the helper tests above for the guaranteed red signal
- helper tests should fail initially because the counting and capacity helpers do not exist yet

- [ ] **Step 2: Run the helper tests and confirm they fail**

Run: `go test ./multiraft -run 'Test(CountTrackedReadyEntriesIgnoresEmptyNormalEntries|EnsurePendingProposalCapacityPreservesTrackedFuture|EnsurePendingConfigCapacityPreservesTrackedFuture)' -v`

Expected: FAIL because the new counting/capacity helpers do not exist yet.

- [ ] **Step 3: Add ready-entry counting and pending-map pre-growth in `group.go`**

```go
func countTrackedReadyEntries(entries []raftpb.Entry) (proposalCount, configCount int) {
    for _, entry := range entries {
        switch entry.Type {
        case raftpb.EntryNormal:
            if len(entry.Data) > 0 {
                proposalCount++
            }
        case raftpb.EntryConfChange:
            configCount++
        }
    }
    return proposalCount, configCount
}
```

Implementation details:
- call the counting helper before tracking ready entries
- if pending maps already exist, rebuild them only when necessary to reserve enough headroom
- preserve the current `(index, term)` tracking and resolution logic exactly

- [ ] **Step 4: Add or update burst proposal/config regression tests**

```go
func TestProposeCorrelatesBurstOfInflightFutures(t *testing.T) { /* 3-5 in-flight proposals */ }
func TestChangeConfigCorrelatesBurstOfInflightFutures(t *testing.T) { /* 2-3 in-flight config changes */ }
```

Requirements:
- assert unique or strictly increasing applied indexes
- assert the returned result data still matches the originating proposal where applicable
- avoid relying on map iteration order or internal counters

- [ ] **Step 5: Re-run the helper tests plus burst regression tests**

Run: `go test ./multiraft -run 'Test(CountTrackedReadyEntriesIgnoresEmptyNormalEntries|EnsurePendingProposalCapacityPreservesTrackedFuture|EnsurePendingConfigCapacityPreservesTrackedFuture|ProposeCorrelatesBurstOfInflightFutures|ChangeConfigCorrelatesBurstOfInflightFutures)' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add multiraft/group.go multiraft/proposal_test.go multiraft/control_test.go
git commit -m "perf: preallocate multiraft pending future maps"
```

## Task 3: Reuse outer transport envelope slices without weakening clone safety

**Files:**
- Modify: `multiraft/group.go`
- Modify: `multiraft/step_test.go`

- [ ] **Step 1: Write the failing envelope-reuse tests**

```go
func TestWrapMessagesIntoReusesDestinationSlice(t *testing.T) {
    dst := make([]Envelope, 0, 4)
    msgs := []raftpb.Message{{Type: raftpb.MsgHeartbeat, From: 1, To: 2}}

    batch := wrapMessagesInto(dst, 42, msgs)
    if len(batch) != 1 {
        t.Fatalf("len(batch) = %d, want 1", len(batch))
    }
    if cap(batch) != cap(dst) {
        t.Fatalf("cap(batch) = %d, want %d", cap(batch), cap(dst))
    }
}

func TestWrapMessagesIntoStillClonesRaftMessagePayloads(t *testing.T) {
    dst := make([]Envelope, 0, 2)
    msgs := []raftpb.Message{{ /* context, entries, snapshot payloads */ }}

    batch := wrapMessagesInto(dst, 42, msgs)
    msgs[0].Context[0] = 'x'
    msgs[0].Entries[0].Data[0] = 'X'

    if string(batch[0].Message.Context) != "ctx" {
        t.Fatalf("Context = %q", batch[0].Message.Context)
    }
}
```

Notes:
- migrate the current `TestWrapMessagesClonesRaftMessagePayloads` to the new helper or add a companion test
- keep the payload-clone assertions comprehensive for context, entries, snapshot, and conf state

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./multiraft -run 'TestWrapMessages(IntoReusesDestinationSlice|IntoStillClonesRaftMessagePayloads)' -v`

Expected: FAIL because `wrapMessagesInto` does not exist yet.

- [ ] **Step 3: Implement reusable outer-envelope wrapping**

```go
func wrapMessagesInto(dst []Envelope, groupID GroupID, messages []raftpb.Message) []Envelope {
    dst = dst[:0]
    for _, msg := range messages {
        dst = append(dst, Envelope{
            GroupID: groupID,
            Message: cloneMessage(msg),
        })
    }
    return dst
}
```

Implementation details:
- keep `cloneMessage`, `cloneEntry`, and `cloneSnapshot` behavior intact
- update the ready path to pass and retain a reusable destination slice
- do not leak reused envelope storage outside the current batch lifetime

- [ ] **Step 4: Re-run the targeted tests**

Run: `go test ./multiraft -run 'TestWrapMessages(IntoReusesDestinationSlice|IntoStillClonesRaftMessagePayloads)' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add multiraft/group.go multiraft/step_test.go
git commit -m "perf: reuse multiraft transport envelope slices"
```

## Task 4: Run full verification and capture benchmark deltas

**Files:**
- Modify: `multiraft/benchmark_test.go`
  Only if benchmark names, helper usage, or comments need adjustment during implementation. Otherwise leave the file unchanged and use it for verification only.

- [ ] **Step 1: Run the package test suite**

Run: `go test ./multiraft/...`

Expected: PASS

- [ ] **Step 2: Run the race check**

Run: `go test -race ./multiraft/...`

Expected: PASS

- [ ] **Step 3: Run the realistic benchmark set**

Run: `go test ./multiraft -run '^$' -bench 'Benchmark(ThreeNodeMultiGroupProposalRoundTrip|ThreeNodeMultiGroupProposalRoundTripNotified|ThreeNodeMultiGroupConcurrentProposalThroughput)$' -benchmem -benchtime=10x`

Expected:
- all benchmarks complete successfully
- `allocs/op` is lower than the pre-optimization numbers from the approved spec
- `B/op` is lower or flat
- `ns/op` is improved or at least not regressed materially

- [ ] **Step 4: Summarize the before/after numbers in the implementation notes or final handoff**

Record:
- baseline numbers from `docs/superpowers/specs/2026-03-26-multiraft-runtime-optimization-design.md`
- new numbers from this run
- any metric that did not improve and the likely reason

- [ ] **Step 5: Commit the verified optimization set**

```bash
git add multiraft/group.go multiraft/step_test.go multiraft/proposal_test.go multiraft/control_test.go multiraft/benchmark_test.go
git commit -m "perf: optimize multiraft runtime hot path"
```
