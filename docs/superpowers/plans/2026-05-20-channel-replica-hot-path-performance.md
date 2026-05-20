# Channel Replica Hot Path Performance Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve `pkg/channel/replica` hot-path throughput and tail latency without weakening channel ISR, epoch, lease, checkpoint, or single-node cluster semantics.

**Architecture:** Keep each channel replica single-writer and correctness-first. Optimize the surrounding hot paths: add replica-level benchmarks, replace pooled mailbox O(n) dequeue with an O(1) ring buffer, reduce normal-path diagnostic allocation, replace durable-lane TryLock polling with a cancellable semaphore, and validate that existing owned append paths remain zero-extra-clone for runtime callers. Larger semantic changes such as checkpoint commit-readiness relaxation and virtual channel sharding are intentionally deferred to separate plans.

**Tech Stack:** Go, `testing` benchmarks, `go test`, `sync/atomic`, bounded queues, channel replica pooled execution, existing `sendtrace` instrumentation.

---

## Scope

This plan covers a first performance pass that is independently testable and low-risk:

1. Replica microbenchmarks for append, pooled mailbox, quorum candidate, and durable-lane contention.
2. Pooled mailbox ring buffer.
3. Normal-path diagnostics allocation reduction.
4. Durable lane semaphore replacing 1ms TryLock polling.
5. Validation of existing `AppendOwned` runtime path and allocation baseline.

Out of scope for this plan:

- Changing quorum semantics or `CommitReady` behavior.
- ACK coalescing in `pkg/channel/runtime`.
- Fetch long-poll changes.
- Virtual channel sharding for super-hot groups.
- Store commit coordinator profile changes.

## File Map

- Modify: `pkg/channel/replica/pooled_loop_driver.go` — replace slice-shift mailbox with bounded ring buffer while preserving single-writer scheduling.
- Modify: `pkg/channel/replica/replica.go` — add durable lane field initialization and close-safe helpers if needed.
- Modify: `pkg/channel/replica/checkpoint_writer.go` — replace `lockDurableMu` implementation and related unlock path with semaphore-backed durable lane helpers.
- Modify: `pkg/channel/replica/append_pipeline.go` — update durable lane acquisition/release calls and avoid unnecessary progress snapshot work in normal debug paths.
- Modify: `pkg/channel/replica/follower_apply.go` — update durable lane acquisition/release calls.
- Modify: `pkg/channel/replica/reconcile_coordinator.go` — update durable lane acquisition/release calls and add benchmark coverage for quorum candidate.
- Modify: `pkg/channel/replica/retention.go` — update durable lane acquisition/release calls.
- Modify: `pkg/channel/replica/snapshot_pipeline.go` — update durable lane acquisition/release calls if it currently uses `durableMu` directly.
- Create: `pkg/channel/replica/performance_benchmark_test.go` — add focused microbenchmarks.
- Create: `pkg/channel/replica/pooled_mailbox_test.go` or extend `pkg/channel/replica/pooled_loop_test.go` — ring buffer unit tests and pressure behavior.
- Create: `pkg/channel/replica/durable_lane_test.go` — cancellation, serialization, and no-1ms-poll tests.
- Modify: `pkg/channel/replica/FLOW.md` — reflect durable lane and mailbox implementation details if changed.
- Optional modify: `docs/development/PROJECT_KNOWLEDGE.md` — record any newly discovered stable performance rule, only if concise and reusable.

---

### Task 1: Add Performance Baseline Benchmarks

**Files:**
- Create: `pkg/channel/replica/performance_benchmark_test.go`
- Read: `pkg/channel/replica/testenv_test.go`
- Read: `pkg/channel/replica/execution_pressure_test.go`
- Read: `pkg/channel/append_benchmark_test.go`

- [ ] **Step 1: Write replica append benchmarks**

Add a new benchmark file with package `replica` so tests can reuse internal test helpers. Include this skeleton and adapt helper names to the existing test environment:

```go
package replica

import (
    "context"
    "fmt"
    "testing"
    "time"

    "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func BenchmarkReplicaAppendHotPath(b *testing.B) {
    for _, mode := range []channel.CommitMode{channel.CommitModeLocal, channel.CommitModeQuorum} {
        b.Run(commitModeBenchmarkName(mode), func(b *testing.B) {
            for _, batchSize := range []int{1, 16, 64} {
                b.Run(fmt.Sprintf("batch=%d", batchSize), func(b *testing.B) {
                    env := newTestEnv(b)
                    env.becomeLeader(b, channel.Meta{
                        Key:         "bench-channel",
                        Replicas:    []channel.NodeID{1},
                        ISR:         []channel.NodeID{1},
                        Leader:      1,
                        MinISR:      1,
                        Epoch:       1,
                        LeaderEpoch: 1,
                        LeaseUntil:  time.Now().Add(time.Hour),
                    })
                    records := benchmarkRecords(batchSize, 128)
                    ctx := channel.ContextWithCommitMode(context.Background(), mode)
                    b.ReportAllocs()
                    b.ResetTimer()
                    for i := 0; i < b.N; i++ {
                        if _, err := env.replica.AppendOwned(ctx, records); err != nil {
                            b.Fatalf("AppendOwned() error = %v", err)
                        }
                    }
                })
            }
        })
    }
}

func commitModeBenchmarkName(mode channel.CommitMode) string {
    switch mode {
    case channel.CommitModeLocal:
        return "local"
    case channel.CommitModeQuorum:
        return "quorum"
    default:
        return "unknown"
    }
}

func benchmarkRecords(n int, payloadBytes int) []channel.Record {
    records := make([]channel.Record, n)
    payload := make([]byte, payloadBytes)
    for i := range payload {
        payload[i] = byte('a' + i%26)
    }
    for i := range records {
        records[i] = channel.Record{Payload: append([]byte(nil), payload...), SizeBytes: payloadBytes}
    }
    return records
}
```

If `newTestEnv`/`becomeLeader` names differ, use the existing helpers in `pkg/channel/replica/testenv_test.go`; do not create a parallel fake environment unless the existing helper cannot support benchmarks.

- [ ] **Step 2: Add pooled mailbox benchmark**

In the same file, add a benchmark that isolates enqueue/dequeue overhead. Use a lightweight fake `pooledLoopDriver` when possible; if direct construction is simpler, use `NewExecutionPool` with one worker and submit no-op result events.

```go
func BenchmarkPooledMailboxSubmitResult(b *testing.B) {
    pool, err := NewExecutionPool(ExecutionPoolConfig{Workers: 1, AppendWorkers: 1, CheckpointWorkers: 1, MailboxSize: 4096})
    if err != nil {
        b.Fatalf("NewExecutionPool() error = %v", err)
    }
    defer func() { _ = pool.Close() }()

    env := newTestEnv(b)
    env.replica.executionPool = pool
    env.replica.loop = newPooledLoopDriver(env.replica, ExecutionConfig{Mode: ExecutionModePooled, Pool: pool, MailboxSize: 4096, TurnBudget: 64})
    env.replica.loop.start()

    b.ReportAllocs()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        if err := env.replica.loop.submitResult(context.Background(), machineAdvanceHWEvent{}); err != nil {
            b.Fatalf("submitResult() error = %v", err)
        }
    }
}
```

If this conflicts with `newTestEnv` already starting a loop, create a small helper that constructs a replica using `env.config()` before loop start, or benchmark a new queue type after Task 2 introduces it.

- [ ] **Step 3: Add quorum candidate benchmark**

```go
func BenchmarkQuorumProgressCandidate(b *testing.B) {
    for _, replicas := range []int{3, 5, 9, 16, 32, 64} {
        b.Run(fmt.Sprintf("isr=%d", replicas), func(b *testing.B) {
            isr := make([]channel.NodeID, replicas)
            progress := make(map[channel.NodeID]uint64, replicas)
            for i := range isr {
                id := channel.NodeID(i + 1)
                isr[i] = id
                progress[id] = uint64(1000 + i)
            }
            minISR := replicas/2 + 1
            b.ReportAllocs()
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                if _, ok, err := quorumProgressCandidate(isr, progress, minISR, 1000, 2000); err != nil || !ok {
                    b.Fatalf("candidate ok=%v err=%v", ok, err)
                }
            }
        })
    }
}
```

- [ ] **Step 4: Run the new benchmarks once as baseline**

Run:

```bash
go test ./pkg/channel/replica -run '^$' -bench 'BenchmarkReplicaAppendHotPath|BenchmarkPooledMailboxSubmitResult|BenchmarkQuorumProgressCandidate' -benchmem -count=1
```

Expected: benchmarks compile and report `ns/op`, `B/op`, `allocs/op`. Save notable numbers in your working notes, not in source unless the team requests it.

- [ ] **Step 5: Commit benchmark baseline**

```bash
git add pkg/channel/replica/performance_benchmark_test.go
git commit -m "test: add channel replica hot path benchmarks"
```

---

### Task 2: Replace Pooled Mailbox Slice Shift With Ring Buffer

**Files:**
- Modify: `pkg/channel/replica/pooled_loop_driver.go`
- Test: `pkg/channel/replica/pooled_loop_test.go`
- Optional test: `pkg/channel/replica/pooled_mailbox_test.go`

- [ ] **Step 1: Write failing ring-buffer pressure test**

Add a test that submits more messages than `TurnBudget`, forces requeue, and verifies all commands complete without queue corruption.

```go
func TestPooledLoopMailboxDrainsWithoutDroppingAfterTurnYield(t *testing.T) {
    pool, err := NewExecutionPool(ExecutionPoolConfig{Workers: 1, AppendWorkers: 1, CheckpointWorkers: 1, MailboxSize: 64, TurnBudget: 4})
    if err != nil {
        t.Fatalf("NewExecutionPool() error = %v", err)
    }
    defer func() { _ = pool.Close() }()

    env := newTestEnv(t)
    env.closeReplica(t)
    cfg := env.config()
    cfg.Execution = ExecutionConfig{Mode: ExecutionModePooled, Pool: pool, MailboxSize: 64, TurnBudget: 4}
    rep, err := NewReplica(cfg)
    if err != nil {
        t.Fatalf("NewReplica() error = %v", err)
    }
    defer func() { _ = rep.Close() }()

    r := rep.(*replica)
    for i := 0; i < 32; i++ {
        if err := r.loop.submitResult(context.Background(), machineAdvanceHWEvent{}); err != nil {
            t.Fatalf("submitResult(%d) error = %v", i, err)
        }
    }
}
```

Adjust helper calls to match the actual `testEnv` API. The test should pass before and after, but it locks in behavior while refactoring.

- [ ] **Step 2: Add ring buffer fields**

Replace the queue fields in `pooledLoopDriver`:

```go
type pooledLoopDriver struct {
    replica *replica
    pool    *ExecutionPool

    mu          sync.Mutex
    queue       []pooledLoopMessage
    head        int
    tail        int
    size        int
    queued      bool
    closed      bool
    doneCh      chan struct{}
    doneOnce    sync.Once
    mailboxSize int
    turnBudget  int
}
```

- [ ] **Step 3: Initialize bounded buffer**

In `newPooledLoopDriver`, allocate the queue with fixed mailbox capacity:

```go
mailboxSize := pool.mailboxSize(cfg.MailboxSize)
return &pooledLoopDriver{
    replica:     r,
    pool:        pool,
    doneCh:      r.loopDone,
    mailboxSize: mailboxSize,
    turnBudget:  pool.turnBudget(cfg.TurnBudget),
    queue:       make([]pooledLoopMessage, mailboxSize),
}
```

If `mailboxSize == 0` cannot happen after pool defaults, keep a defensive minimum of 1.

- [ ] **Step 4: Add enqueue/dequeue helpers**

Add private methods in `pooled_loop_driver.go`:

```go
func (d *pooledLoopDriver) queueLenLocked() int {
    return d.size
}

func (d *pooledLoopDriver) pushLocked(msg pooledLoopMessage) bool {
    if d.size >= d.mailboxSize {
        return false
    }
    d.queue[d.tail] = msg
    d.tail = (d.tail + 1) % d.mailboxSize
    d.size++
    return true
}

func (d *pooledLoopDriver) popLocked() (pooledLoopMessage, bool) {
    if d.size == 0 {
        return pooledLoopMessage{}, false
    }
    msg := d.queue[d.head]
    d.queue[d.head] = pooledLoopMessage{}
    d.head = (d.head + 1) % d.mailboxSize
    d.size--
    return msg, true
}

func (d *pooledLoopDriver) undoLastPushLocked() {
    if d.size == 0 {
        return
    }
    d.tail--
    if d.tail < 0 {
        d.tail = d.mailboxSize - 1
    }
    d.queue[d.tail] = pooledLoopMessage{}
    d.size--
}
```

- [ ] **Step 5: Update `enqueue`**

Replace `len(d.queue)` and `append` logic with ring helpers:

```go
if d.size >= d.mailboxSize {
    d.pool.observeEnqueue("queue_full")
    return channel.ErrNotReady
}
msg.queuedAt = d.pool.cfg.Now()
if !d.pushLocked(msg) {
    d.pool.observeEnqueue("queue_full")
    return channel.ErrNotReady
}
```

When ready queue submit fails after pushing, call `undoLastPushLocked()` instead of slicing `d.queue`.

- [ ] **Step 6: Update `drain`**

Replace this pattern:

```go
msg := d.queue[0]
copy(d.queue, d.queue[1:])
d.queue = d.queue[:len(d.queue)-1]
```

with:

```go
msg, ok := d.popLocked()
if !ok {
    d.queued = false
    closed := d.closed
    d.mu.Unlock()
    if closed {
        d.closeDone()
    }
    return
}
```

Use `d.size == 0` or `d.queueLenLocked() == 0` for empty checks.

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./pkg/channel/replica -run 'TestPooledLoop|TestPooledLoopMailbox|TestExecutionPool' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run mailbox benchmark**

Run:

```bash
go test ./pkg/channel/replica -run '^$' -bench 'BenchmarkPooledMailboxSubmitResult' -benchmem -count=3
```

Expected: lower `ns/op` under deep mailbox pressure, no increase in `allocs/op`.

- [ ] **Step 9: Commit mailbox optimization**

```bash
git add pkg/channel/replica/pooled_loop_driver.go pkg/channel/replica/pooled_loop_test.go pkg/channel/replica/pooled_mailbox_test.go
git commit -m "perf: use ring buffer for channel replica pooled mailbox"
```

---

### Task 3: Reduce Normal-Path Diagnostic Allocations

**Files:**
- Modify: `pkg/channel/replica/progress_pipeline.go`
- Modify: `pkg/channel/replica/append.go`
- Optional modify: `pkg/wklog` if an enabled-level method already exists or can be introduced safely.
- Test: `pkg/channel/replica/progress_test.go`
- Benchmark: `pkg/channel/replica/performance_benchmark_test.go`

- [ ] **Step 1: Check logger capabilities**

Search:

```bash
rg -n "DebugEnabled|Enabled\(|Level" pkg/wklog internal/log pkg -g'*.go'
```

Expected: determine whether the logger already exposes debug-level checks.

- [ ] **Step 2: If no enabled check exists, add local no-op guard abstraction**

Do not change global logging APIs unless necessary. Add a private helper that can later use an enabled method if present:

```go
type debugEnabledLogger interface {
    DebugEnabled() bool
}

func replicaDebugEnabled(logger wklog.Logger) bool {
    if checker, ok := logger.(debugEnabledLogger); ok {
        return checker.DebugEnabled()
    }
    return true
}
```

If this would always return true because current logger lacks the method, skip the helper and only remove avoidable pre-log allocations where possible. Do not make unsafe assumptions.

- [ ] **Step 3: Delay progress map snapshots**

In `applyCursorCommand`, `applyLeaderAppendCommittedEvent`, and `finishHWAdvanceOutcome`, avoid building progress maps unless the log call will use them. Prefer either:

```go
var progress map[uint64]uint64
if replicaDebugEnabled(r.appendLogger()) {
    progress = r.snapshotProgressLocked()
}
```

or remove `wklog.Any("progress", progress)` from normal Debug logs and keep detailed maps for timeout/error logs only.

Recommended low-risk change: keep detailed progress maps in timeout/error logs, remove from normal Debug logs:

- `follower cursor applied`
- `leader local append flushed`
- `HW advanced`

Do not remove progress from `advance HW failed` or append timeout diagnostics.

- [ ] **Step 4: Add allocation benchmark for progress ACK**

Add:

```go
func BenchmarkReplicaApplyFollowerCursor(b *testing.B) {
    env := newTestEnv(b)
    meta := channel.Meta{
        Key:         "bench-progress",
        Replicas:    []channel.NodeID{1, 2, 3},
        ISR:         []channel.NodeID{1, 2, 3},
        Leader:      1,
        MinISR:      2,
        Epoch:       1,
        LeaderEpoch: 1,
        LeaseUntil:  time.Now().Add(time.Hour),
    }
    env.becomeLeader(b, meta)
    b.ReportAllocs()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        req := channel.ReplicaFollowerCursorUpdate{
            ChannelKey:  meta.Key,
            Epoch:       meta.Epoch,
            ReplicaID:   2,
            MatchOffset: uint64(i % 1024),
            OffsetEpoch: meta.Epoch,
        }
        _ = env.replica.ApplyFollowerCursor(context.Background(), req)
    }
}
```

Adjust monotonic offsets if existing lineage rejects decreasing offset; use `uint64(i+1)` if needed.

- [ ] **Step 5: Run tests and benchmark**

Run:

```bash
go test ./pkg/channel/replica -run 'TestProgress|TestProgressAck|TestSendTrace' -count=1
go test ./pkg/channel/replica -run '^$' -bench 'BenchmarkReplicaApplyFollowerCursor' -benchmem -count=3
```

Expected: tests PASS; allocation count on cursor path does not increase and ideally drops.

- [ ] **Step 6: Commit diagnostic allocation cleanup**

```bash
git add pkg/channel/replica/progress_pipeline.go pkg/channel/replica/append.go pkg/channel/replica/performance_benchmark_test.go
git commit -m "perf: reduce channel replica normal-path diagnostic allocations"
```

---

### Task 4: Replace Durable Mutex Polling With Cancellable Durable Lane

**Files:**
- Modify: `pkg/channel/replica/replica.go`
- Modify: `pkg/channel/replica/checkpoint_writer.go`
- Modify: `pkg/channel/replica/append_pipeline.go`
- Modify: `pkg/channel/replica/follower_apply.go`
- Modify: `pkg/channel/replica/reconcile_coordinator.go`
- Modify: `pkg/channel/replica/retention.go`
- Modify: `pkg/channel/replica/snapshot_pipeline.go` if needed
- Create: `pkg/channel/replica/durable_lane_test.go`
- Modify: `pkg/channel/replica/FLOW.md`

- [ ] **Step 1: Write cancellation test for waiting durable lane**

Create `durable_lane_test.go`:

```go
package replica

import (
    "context"
    "errors"
    "testing"
    "time"
)

func TestDurableLaneAcquireHonorsContextCancellation(t *testing.T) {
    r := &replica{}
    r.initDurableLane()
    release, err := r.acquireDurableLane(context.Background())
    if err != nil {
        t.Fatalf("first acquireDurableLane() error = %v", err)
    }
    defer release()

    ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
    defer cancel()
    _, err = r.acquireDurableLane(ctx)
    if !errors.Is(err, context.DeadlineExceeded) {
        t.Fatalf("second acquireDurableLane() error = %v, want DeadlineExceeded", err)
    }
}
```

- [ ] **Step 2: Add serialization test**

```go
func TestDurableLaneSerializesMutations(t *testing.T) {
    r := &replica{}
    r.initDurableLane()

    release, err := r.acquireDurableLane(context.Background())
    if err != nil {
        t.Fatalf("first acquireDurableLane() error = %v", err)
    }

    acquired := make(chan struct{})
    done := make(chan error, 1)
    go func() {
        secondRelease, err := r.acquireDurableLane(context.Background())
        if err != nil {
            done <- err
            return
        }
        close(acquired)
        secondRelease()
        done <- nil
    }()

    select {
    case <-acquired:
        t.Fatal("second acquire completed before first release")
    case <-time.After(2 * time.Millisecond):
    }

    release()
    select {
    case <-acquired:
    case <-time.After(time.Second):
        t.Fatal("second acquire did not complete after release")
    }
    if err := <-done; err != nil {
        t.Fatalf("second acquire error = %v", err)
    }
}
```

- [ ] **Step 3: Add durable lane field and helpers**

In `replica.go`, replace or supplement `durableMu sync.Mutex` with a channel-backed lane. To minimize change size, keep `durableMu` temporarily unused only if needed, but prefer removing it after all call sites change.

```go
type replica struct {
    mu sync.RWMutex

    durableLane chan struct{}

    // ... existing fields ...
}
```

Add helpers:

```go
func (r *replica) initDurableLane() {
    if r.durableLane != nil {
        return
    }
    r.durableLane = make(chan struct{}, 1)
    r.durableLane <- struct{}{}
}

func (r *replica) acquireDurableLane(ctx context.Context) (func(), error) {
    if ctx == nil {
        ctx = context.Background()
    }
    r.initDurableLane()
    select {
    case <-r.durableLane:
        released := false
        return func() {
            if released {
                return
            }
            released = true
            r.durableLane <- struct{}{}
        }, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}
```

Call `r.initDurableLane()` in `NewReplica` before workers start.

- [ ] **Step 4: Replace `lockDurableMu` function**

In `checkpoint_writer.go`, remove the old TryLock loop or rewrite it as compatibility wrapper:

```go
func (r *replica) lockDurableMu(ctx context.Context) error {
    release, err := r.acquireDurableLane(ctx)
    if err != nil {
        return err
    }
    // This wrapper exists only during migration. Prefer direct acquireDurableLane.
    r.pendingDurableRelease = release // Do not add this global state; migrate call sites directly instead.
    return nil
}
```

Do not use global pending release in final code. Instead, update every call site directly as described below, then delete `lockDurableMu` entirely.

- [ ] **Step 5: Update append durable effect**

Replace:

```go
base, newLEO, err := uint64(0), uint64(0), r.lockDurableMu(ctx)
...
r.durableMu.Unlock()
```

with:

```go
base, newLEO := uint64(0), uint64(0)
release, err := r.acquireDurableLane(ctx)
lockDoneAt := r.now()
...
if err == nil {
    defer release()
    r.mu.Lock()
    err = r.validateAppendEffectFenceLocked(effect)
    r.mu.Unlock()
    if err == nil {
        appendStartedAt := r.now()
        base, newLEO, err = r.durable.AppendLeaderBatch(ctx, effect.Records)
        // existing sendtrace stays here
    }
}
```

Do not hold the lane while submitting the loop result.

- [ ] **Step 6: Update checkpoint writer**

Replace:

```go
if err := r.lockDurableMu(ctx); err != nil { return err }
defer r.durableMu.Unlock()
```

with:

```go
release, err := r.acquireDurableLane(ctx)
if err != nil {
    return err
}
defer release()
```

- [ ] **Step 7: Update follower apply, reconcile, retention, snapshot**

For each durable mutation site, use the same pattern:

```go
release, err := r.acquireDurableLane(ctx)
if err == nil {
    r.mu.Lock()
    err = r.validate...Locked(effect)
    r.mu.Unlock()
    if err == nil {
        // durable mutation
    }
    release()
}
```

Preserve the existing second fence validation after lane acquisition. This is required because role/meta can change while waiting.

- [ ] **Step 8: Run focused durable tests**

Run:

```bash
go test ./pkg/channel/replica -run 'TestDurableLane|TestDurableEffect|TestFollowerApply|TestReconcile|TestSnapshot|TestRetention|TestCheckpoint' -count=1
```

Expected: PASS.

- [ ] **Step 9: Run contention benchmark**

Add or run a benchmark that blocks the durable lane and measures cancellation latency. Then run:

```bash
go test ./pkg/channel/replica -run '^$' -bench 'Benchmark.*Durable.*|BenchmarkReplicaAppendHotPath' -benchmem -count=3
```

Expected: no 1ms polling plateau in cancellation benchmark; append benchmark does not regress.

- [ ] **Step 10: Update FLOW.md**

In `pkg/channel/replica/FLOW.md`, update the state ownership/event loop section from `durableMu` to `durableLane` or equivalent wording:

```markdown
All log/history/checkpoint/snapshot mutations enter the per-replica durable lane, a cancellable single-token lane that preserves one durable mutation at a time without timer polling.
```

- [ ] **Step 11: Commit durable lane optimization**

```bash
git add pkg/channel/replica/replica.go pkg/channel/replica/checkpoint_writer.go pkg/channel/replica/append_pipeline.go pkg/channel/replica/follower_apply.go pkg/channel/replica/reconcile_coordinator.go pkg/channel/replica/retention.go pkg/channel/replica/snapshot_pipeline.go pkg/channel/replica/durable_lane_test.go pkg/channel/replica/FLOW.md
git commit -m "perf: use cancellable durable lane for channel replica mutations"
```

---

### Task 5: Validate Owned Append Allocation Path

**Files:**
- Modify: `pkg/channel/runtime/channel_append_test.go`
- Modify: `pkg/channel/replica/performance_benchmark_test.go`
- Read: `pkg/channel/runtime/channel.go`
- Read: `pkg/channel/replica/append.go`

- [ ] **Step 1: Confirm runtime already uses `AppendOwned`**

Run:

```bash
rg -n "AppendOwned|ownedAppendReplica" pkg/channel/runtime pkg/channel/replica
```

Expected: `pkg/channel/runtime/channel.go` uses `AppendOwned` when the replica supports it.

- [ ] **Step 2: Add allocation comparison benchmark**

Add to `performance_benchmark_test.go`:

```go
func BenchmarkReplicaAppendCloneVsOwned(b *testing.B) {
    for _, owned := range []bool{false, true} {
        name := "clone"
        if owned {
            name = "owned"
        }
        b.Run(name, func(b *testing.B) {
            env := newTestEnv(b)
            env.becomeLeader(b, channel.Meta{
                Key:         "bench-owned",
                Replicas:    []channel.NodeID{1},
                ISR:         []channel.NodeID{1},
                Leader:      1,
                MinISR:      1,
                Epoch:       1,
                LeaderEpoch: 1,
                LeaseUntil:  time.Now().Add(time.Hour),
            })
            ctx := channel.ContextWithCommitMode(context.Background(), channel.CommitModeLocal)
            b.ReportAllocs()
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                records := benchmarkRecords(1, 1024)
                var err error
                if owned {
                    _, err = env.replica.AppendOwned(ctx, records)
                } else {
                    _, err = env.replica.Append(ctx, records)
                }
                if err != nil {
                    b.Fatalf("append error = %v", err)
                }
            }
        })
    }
}
```

If per-iteration `benchmarkRecords` allocation hides the difference, move payload creation outside the loop for the clone case and create a small owned record per iteration only where necessary. The goal is to observe replica boundary clone cost, not benchmark record construction.

- [ ] **Step 3: Strengthen runtime test**

Extend `TestChannelAppendUsesOwnedBatchWhenReplicaSupportsIt` to verify payload ownership transfer assumptions. Example:

```go
func TestChannelAppendUsesOwnedBatchPayloadWithoutFallbackClone(t *testing.T) {
    // Existing fakeReplica should capture the slice received by AppendOwned.
    // Verify Append was not called and AppendOwned received exactly the caller batch length.
}
```

Do not assert pointer equality unless the fake can safely capture it; Go slice/payload alias assertions are brittle.

- [ ] **Step 4: Run focused tests and benchmark**

Run:

```bash
go test ./pkg/channel/runtime ./pkg/channel/replica -run 'TestChannelAppendUsesOwnedBatch|TestReplicaAppend' -count=1
go test ./pkg/channel/replica -run '^$' -bench 'BenchmarkReplicaAppendCloneVsOwned' -benchmem -count=3
```

Expected: tests PASS; owned path has fewer bytes/op than clone path.

- [ ] **Step 5: Commit owned append validation**

```bash
git add pkg/channel/runtime/channel_append_test.go pkg/channel/replica/performance_benchmark_test.go
git commit -m "test: cover channel replica owned append allocation path"
```

---

### Task 6: Final Verification And Regression Report

**Files:**
- Modify: `docs/superpowers/reports/2026-05-20-channel-replica-hot-path-performance.md`

- [ ] **Step 1: Run package tests**

Run:

```bash
go test ./pkg/channel/replica ./pkg/channel/runtime ./pkg/channel/store
```

Expected: PASS.

- [ ] **Step 2: Run focused race tests**

Run:

```bash
go test -race ./pkg/channel/replica ./pkg/channel/runtime
```

Expected: PASS. If too slow locally, run at least:

```bash
go test -race ./pkg/channel/replica -run 'TestPooledLoop|TestDurableLane|TestFollowerApply|TestReconcile|TestRetention' -count=1
```

and record the limitation.

- [ ] **Step 3: Run benchmarks before final summary**

Run:

```bash
go test ./pkg/channel/replica -run '^$' -bench 'BenchmarkReplicaAppendHotPath|BenchmarkPooledMailboxSubmitResult|BenchmarkQuorumProgressCandidate|BenchmarkReplicaApplyFollowerCursor|BenchmarkReplicaAppendCloneVsOwned' -benchmem -count=5
```

Expected: benchmark output collected. Use `benchstat` if available:

```bash
benchstat /tmp/channel-replica-before.txt /tmp/channel-replica-after.txt
```

- [ ] **Step 4: Write report**

Create `docs/superpowers/reports/2026-05-20-channel-replica-hot-path-performance.md`:

```markdown
# Channel Replica Hot Path Performance Report

## Summary

- Pooled mailbox dequeue changed from slice shift to ring buffer.
- Durable lane changed from 1ms TryLock polling to cancellable single-token lane.
- Normal debug path allocation reduced.

## Verification

- `go test ./pkg/channel/replica ./pkg/channel/runtime ./pkg/channel/store`: PASS
- `go test -race ./pkg/channel/replica ./pkg/channel/runtime`: PASS or documented limitation

## Benchmarks

| Benchmark | Before | After | Delta |
| --- | ---: | ---: | ---: |
| BenchmarkPooledMailboxSubmitResult | TBD | TBD | TBD |
| BenchmarkReplicaAppendHotPath/local | TBD | TBD | TBD |
| BenchmarkReplicaApplyFollowerCursor | TBD | TBD | TBD |

## Follow-ups

- ACK coalescing in runtime.
- Checkpoint priority scheduling.
- Fetch catch-up batch sizing.
- Virtual channel sharding for super-hot groups.
```

- [ ] **Step 5: Commit final report**

```bash
git add docs/superpowers/reports/2026-05-20-channel-replica-hot-path-performance.md
git commit -m "docs: report channel replica hot path performance results"
```

---

## Rollback Plan

- Ring buffer rollback: revert only `pooled_loop_driver.go` and pooled mailbox tests; pooled execution still uses old slice queue.
- Durable lane rollback: restore `durableMu sync.Mutex` and `lockDurableMu`; revert call-site acquire/release changes.
- Diagnostic allocation rollback: restore removed debug fields if operational diagnostics need them.
- Benchmarks can remain; they are non-production code and useful for future regressions.

## Follow-up Plans

Create separate plans for these after this phase lands:

1. `channel-replica-checkpoint-priority-performance` — checkpoint low-priority scheduling and checkpoint lag metrics.
2. `channel-runtime-ack-coalescing` — follower cursor coalescing and fetch window adaptation.
3. `channel-hot-group-sharding` — virtual channel shards and timeline merge for super-hot groups.
