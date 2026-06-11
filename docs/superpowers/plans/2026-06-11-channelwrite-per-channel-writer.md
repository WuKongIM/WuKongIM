# channelwrite Per-Channel Writer Refactor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `internalv2/runtime/channelwrite` from a per-reactor serial model to a per-channel writer model that lets different channels run fully in parallel, while reducing SEND hot-path allocations and improving readability.

**Architecture:** Move the serial boundary from "reactor" (one goroutine + one `r.mu` serving many hashed channels) down to "channel". Each active channel becomes a self-contained `channelWriter` state machine (pending → appending → committing) advanced by a shared worker pool via an atomic `scheduled` flag (single-writer invariant), so no cross-channel lock exists. The public `Group` API (`SubmitLocal/Start/Stop/ApplySubscriberMutation`) is unchanged.

**Tech Stack:** Go, `github.com/panjf2000/ants/v2` (shared worker pool), `sync/atomic`, `sync.Pool`, existing `pkg/clusterv2` append port.

**Spec:** `docs/superpowers/specs/2026-06-11-channelwrite-per-channel-writer-design.md`

---

## File Structure

Target layout after the refactor (all under `internalv2/runtime/channelwrite/`):

| File | Responsibility | Status |
|------|----------------|--------|
| `group.go` | `Group`, shard array, writer lookup/create/recycle, public API | Modified |
| `writer.go` | `channelWriter` state machine + `advance()` (the core) | New |
| `shard.go` | `shard` struct: writer map + striped lock + atomic pressure counters | New |
| `pool.go` | shared worker pool wrapper + `sync.Pool` for `Future` and batch backing arrays | New |
| `prepare.go` | pure prepare-stage functions (mostly unchanged) | Modified |
| `append.go` | pure append-stage functions (mostly unchanged) | Modified |
| `commit.go` | pure post-commit functions (mostly unchanged) | Modified |
| `state.go` | `subscriberCache`, commit queue helpers (kept as-is) | Unchanged |
| `future.go` | `Future` + pool hooks | Modified |
| `options.go` | option structs + observer interfaces | Modified (observer mapping) |
| `router.go` `delivery.go` `recipient.go` `observer.go` | unchanged behavior | Unchanged |
| `reactor.go` `scheduler.go` `effects.go` | DELETED after Phase 2/3 | Removed |

---

## Phase 0 — Fix benchmarks and establish baseline

No production code changes. Goal: make the channelwrite benchmarks pass and capture before-numbers.

### Task 0.1: Diagnose the failing benchmarks

**Files:**
- Inspect: `internalv2/runtime/channelwrite/benchmark_test.go`

- [ ] **Step 1: Run the failing benchmarks with verbose output**

Run: `go test -run '^$' -bench 'BenchmarkSubmitLocal' -benchmem ./internalv2/runtime/channelwrite/ -v 2>&1 | head -40`

Expected: FAIL with `results = []channelwrite.SendBatchItemResult{... Err:(*errors.errorString)...}, want one successful result`

- [ ] **Step 2: Identify the rejecting error**

Temporarily change the `b.Fatalf` in `BenchmarkSubmitLocalHotChannel` (around line 39) to also print the error:

```go
if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
    b.Fatalf("results = %#v, err = %v, want one successful result", results, results[0].Err)
}
```

Run: `go test -run '^$' -bench 'BenchmarkSubmitLocalHotChannel' ./internalv2/runtime/channelwrite/ -v 2>&1 | head -20`
Expected: the printed `err` reveals the prepare/append rejection cause (e.g. a stale-route mismatch or a missing port). Note the exact error string before proceeding.

- [ ] **Step 3: Revert the temporary `Fatalf` change**

Restore the original assertion line. Do not commit the diagnostic edit.

### Task 0.2: Fix the benchmark fixture

**Files:**
- Modify: `internalv2/runtime/channelwrite/benchmark_test.go`

The fix depends on the cause found in Task 0.1. The most likely cause is that `benchmarkAuthorityTarget` (line 158) and `benchmarkSendItem` (line 170) disagree on channel identity — `benchmarkSendItem("bench-hot")` sets `ChannelType: 2`, and `benchmarkAuthorityTarget` uses `Type: 2`, but `preparedCommandMatchesTarget` (effects.go:277) compares `target.ChannelID.ID == cmd.ChannelID`. Confirm the IDs and types line up. If the cause is a missing required port (e.g. the prepared item is dropped before append), wire the minimal fixture port.

- [ ] **Step 1: Apply the minimal fixture fix**

Based on Task 0.1's error, make `benchmarkSendItem` produce a command that `prepareCanonicalSend` accepts and whose channel identity matches `benchmarkAuthorityTarget`. Example if the mismatch is channel id/type alignment:

```go
func benchmarkSendItem(channelID string) SendBatchItem {
	return SendBatchItem{
		Context: context.Background(),
		Command: SendCommand{
			FromUID:     "bench-u",
			ChannelID:   channelID,
			ChannelType: 2,
			ClientMsgNo: "bench-client-msg",
			Payload:     benchmarkPayload,
		},
	}
}
```

Ensure `benchmarkAuthorityTarget(channelID)` builds `ChannelID{ID: channelID, Type: 2}` so `preparedCommandMatchesTarget` returns true.

- [ ] **Step 2: Run the benchmarks to verify they pass**

Run: `go test -run '^$' -bench 'BenchmarkSubmitLocal' -benchmem ./internalv2/runtime/channelwrite/ 2>&1 | tail -20`
Expected: PASS, with `ns/op`, `B/op`, `allocs/op` reported for both `BenchmarkSubmitLocalHotChannel` and `BenchmarkSubmitLocalManyChannelsParallel`.

- [ ] **Step 3: Commit**

```bash
git add internalv2/runtime/channelwrite/benchmark_test.go
git commit -m "test(channelwrite): fix failing SubmitLocal benchmarks"
```

### Task 0.3: Capture the baseline

**Files:**
- Create: `docs/development/perf-runs/2026-06-11-channelwrite-baseline/benchmarks.txt`

- [ ] **Step 1: Run all channelwrite benchmarks and save the baseline**

```bash
mkdir -p docs/development/perf-runs/2026-06-11-channelwrite-baseline
go test -run '^$' -bench '.' -benchmem -count 6 ./internalv2/runtime/channelwrite/ \
  | tee docs/development/perf-runs/2026-06-11-channelwrite-baseline/benchmarks.txt
```

Expected: six samples per benchmark, no FAIL lines.

- [ ] **Step 2: Verify the full package test passes with the race detector**

Run: `go test -race ./internalv2/runtime/channelwrite/ 2>&1 | tail -5`
Expected: `ok` with no `DATA RACE` reports. Record any pre-existing race in `docs/development/CODE_QUALITY.md` (do not fix unrelated races here).

- [ ] **Step 3: Commit the baseline**

```bash
git add docs/development/perf-runs/2026-06-11-channelwrite-baseline/benchmarks.txt
git commit -m "perf(channelwrite): capture pre-refactor benchmark baseline"
```

> **Three-node baseline (optional, run once before Phase 2):** `scripts/bench-wukongimv2-three-nodes-10kch.sh` writes evidence under `docs/development/perf-runs/`. Save the path as the "before" reference for Phase 4. This is slow; skip during fast iteration and run before the final comparison.

---

## Phase 1 — Allocation reductions (no concurrency-model change)

Keep the reactor model intact. Remove per-SEND allocations so the benefit is isolated and measurable before the bigger structural change.

### Task 1.1: Allocation-free channel-key hashing

**Files:**
- Modify: `internalv2/runtime/channelwrite/group.go:330-334`
- Test: `internalv2/runtime/channelwrite/group_hash_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internalv2/runtime/channelwrite/group_hash_test.go`:

```go
package channelwrite

import (
	"hash/fnv"
	"testing"
)

func TestHashString64MatchesFNV(t *testing.T) {
	cases := []string{"", "a", "2:bench-hot", "2:" + string(make([]byte, 256))}
	for _, c := range cases {
		want := fnv.New64a()
		_, _ = want.Write([]byte(c))
		if got := hashString64(c); got != want.Sum64() {
			t.Fatalf("hashString64(%q) = %d, want %d", c, got, want.Sum64())
		}
	}
}

func TestHashString64NoAllocs(t *testing.T) {
	key := "2:bench-hot"
	allocs := testing.AllocsPerRun(100, func() {
		_ = hashString64(key)
	})
	if allocs != 0 {
		t.Fatalf("hashString64 allocs = %v, want 0", allocs)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestHashString64' ./internalv2/runtime/channelwrite/ -v`
Expected: `TestHashString64NoAllocs` FAILS (current impl allocates a hasher + `[]byte(value)`).

- [ ] **Step 3: Replace `hashString64` with an allocation-free FNV-1a**

In `group.go`, replace the existing `hashString64` (lines 330-334):

```go
func hashString64(value string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	hash := uint64(offset64)
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= prime64
	}
	return hash
}
```

Remove the now-unused `"hash/fnv"` import from `group.go` if nothing else uses it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -run 'TestHashString64' ./internalv2/runtime/channelwrite/ -v`
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/runtime/channelwrite/group.go internalv2/runtime/channelwrite/group_hash_test.go
git commit -m "perf(channelwrite): allocation-free channel-key hashing"
```

### Task 1.2: Pool the Future

**Files:**
- Create: `internalv2/runtime/channelwrite/pool.go`
- Modify: `internalv2/runtime/channelwrite/future.go`
- Test: `internalv2/runtime/channelwrite/pool_test.go` (new)

The `Future` holds a `done` channel plus two slices. Pool the struct and reset it on release. The release point is `SubmitLocal`'s caller after `Wait` returns a snapshot copy — but to stay safe in Phase 1, only pool the backing slices, not the lifecycle, until Phase 3 owns the call path. This task pools the `results`/`doneSet` backing arrays via a sized free-list keyed on small item counts (the common case is 1).

- [ ] **Step 1: Write the failing test**

Create `internalv2/runtime/channelwrite/pool_test.go`:

```go
package channelwrite

import "testing"

func TestFutureBuffersReused(t *testing.T) {
	f := acquireFutureBuffers(1)
	f.results[0] = SendBatchItemResult{}
	releaseFutureBuffers(f)
	g := acquireFutureBuffers(1)
	if &g.results[0] != &f.results[0] {
		t.Fatalf("expected pooled buffer reuse for itemCount=1")
	}
	releaseFutureBuffers(g)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestFutureBuffersReused' ./internalv2/runtime/channelwrite/ -v`
Expected: FAIL — `acquireFutureBuffers`/`releaseFutureBuffers` undefined.

- [ ] **Step 3: Implement the buffer pool**

Create `internalv2/runtime/channelwrite/pool.go`:

```go
package channelwrite

import "sync"

// futureBuffers holds the reusable backing slices for a single-batch Future.
type futureBuffers struct {
	results []SendBatchItemResult
	doneSet []bool
}

var futureBufferPool = sync.Pool{
	New: func() any { return &futureBuffers{} },
}

// acquireFutureBuffers returns buffers sized for itemCount, reusing pooled capacity.
func acquireFutureBuffers(itemCount int) *futureBuffers {
	if itemCount < 0 {
		itemCount = 0
	}
	fb := futureBufferPool.Get().(*futureBuffers)
	if cap(fb.results) < itemCount {
		fb.results = make([]SendBatchItemResult, itemCount)
		fb.doneSet = make([]bool, itemCount)
	} else {
		fb.results = fb.results[:itemCount]
		fb.doneSet = fb.doneSet[:itemCount]
		clear(fb.results)
		clear(fb.doneSet)
	}
	return fb
}

// releaseFutureBuffers returns buffers to the pool for reuse.
func releaseFutureBuffers(fb *futureBuffers) {
	if fb == nil {
		return
	}
	futureBufferPool.Put(fb)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -run 'TestFutureBuffersReused' ./internalv2/runtime/channelwrite/ -v`
Expected: PASS. (Note: `sync.Pool` reuse is best-effort; if the test is flaky under GC, assert on capacity reuse rather than pointer identity — change the assertion to `if cap(g.results) == 0 { t.Fatal(...) }`.)

- [ ] **Step 5: Wire the pool into `newFuture`**

In `future.go`, change `newFuture` (lines 21-38) to draw buffers from the pool:

```go
func newFuture(itemCount int) *Future {
	if itemCount < 0 {
		itemCount = 0
	}
	fb := acquireFutureBuffers(itemCount)
	future := &Future{
		done:    make(chan struct{}),
		results: fb.results,
		doneSet: fb.doneSet,
		remain:  itemCount,
	}
	if itemCount == 0 {
		future.once.Do(func() {
			future.closed = true
			close(future.done)
		})
	}
	return future
}
```

Note: the `done` channel is NOT pooled in Phase 1 (lifecycle ownership moves in Phase 3). Buffers are not released back in Phase 1 either — `acquireFutureBuffers` only removes the `make` on the hot path when the pool is warm. Full release lands in Phase 3 Task 3.4.

- [ ] **Step 6: Run the full package tests**

Run: `go test ./internalv2/runtime/channelwrite/ 2>&1 | tail -5`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add internalv2/runtime/channelwrite/pool.go internalv2/runtime/channelwrite/pool_test.go internalv2/runtime/channelwrite/future.go
git commit -m "perf(channelwrite): pool Future backing buffers"
```

### Task 1.3: Measure Phase 1 against baseline

- [ ] **Step 1: Re-run benchmarks and compare**

```bash
go test -run '^$' -bench '.' -benchmem -count 6 ./internalv2/runtime/channelwrite/ \
  | tee docs/development/perf-runs/2026-06-11-channelwrite-baseline/benchmarks-phase1.txt
```

- [ ] **Step 2: Verify allocations dropped**

Compare `allocs/op` and `B/op` for `BenchmarkSubmitLocalHotChannel` against `benchmarks.txt`. Expected: lower `allocs/op`. If not lower, stop and investigate before continuing — Phase 1 must show measurable improvement on its own.

- [ ] **Step 3: Commit the comparison**

```bash
git add docs/development/perf-runs/2026-06-11-channelwrite-baseline/benchmarks-phase1.txt
git commit -m "perf(channelwrite): record phase 1 benchmark comparison"
```

---

## Phase 2 — Per-channel writer state machine

Build the new model alongside the reactor, then switch `Group` over. The reactor files are deleted only after all tests pass on the new model.

**Invariants the new model must preserve (verified by existing tests + new tests):**
1. Same-channel appends are submitted to the `Appender` in submission order.
2. At most `AppendInflightLimit` append batches per channel are in flight (default 1).
3. `canAdmit` backpressure: a channel rejects with `ErrChannelBusy` past `PendingItemHighWatermark`.
4. `ErrBackpressured` when admission is closed or the bounded submit queue is full.
5. Post-commit effects run after a successful append, best-effort, drop-on-failure with the same `PostCommitFailureObservation`.
6. `ApplySubscriberMutation` updates the cached non-large subscriber snapshot for the target channel.

### Task 2.1: Define `writerPhase` and the `channelWriter` struct

**Files:**
- Create: `internalv2/runtime/channelwrite/writer.go`
- Test: `internalv2/runtime/channelwrite/writer_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internalv2/runtime/channelwrite/writer_test.go`:

```go
package channelwrite

import "testing"

func TestNewChannelWriterStartsIdle(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "c1", Type: 2}, LeaderNodeID: 1}
	target.ChannelKey = channelKey(target.ChannelID)
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	if w.scheduled.Load() {
		t.Fatal("new writer must not be scheduled")
	}
	if w.state == nil {
		t.Fatal("new writer must own a channelState")
	}
	if w.key != target.ChannelKey {
		t.Fatalf("writer key = %q, want %q", w.key, target.ChannelKey)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestNewChannelWriter' ./internalv2/runtime/channelwrite/ -v`
Expected: FAIL — `newChannelWriter` undefined.

- [ ] **Step 3: Implement the struct**

Create `internalv2/runtime/channelwrite/writer.go`:

```go
package channelwrite

import (
	"sync"
	"sync/atomic"
)

// channelWriter is the single-writer state machine for one locally authoritative channel.
// Invariant: at most one goroutine advances a writer at a time (guarded by scheduled).
type channelWriter struct {
	key string

	// scheduled reports whether a worker is already queued to advance this writer.
	scheduled atomic.Bool

	// mu guards state, inbox, and the phase transitions inside advance.
	mu    sync.Mutex
	state *channelState
	// inbox holds submitted batches not yet drained into the channelState pending queue.
	inbox []submittedBatch
}

// submittedBatch is one admitted SubmitLocal call awaiting prepare+append.
type submittedBatch struct {
	target AuthorityTarget
	items  []SendBatchItem
	future *Future
}

func newChannelWriter(target AuthorityTarget, limits channelStateLimits) *channelWriter {
	return &channelWriter{
		key:   targetKey(target),
		state: newChannelState(target, limits),
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -run 'TestNewChannelWriter' ./internalv2/runtime/channelwrite/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/runtime/channelwrite/writer.go internalv2/runtime/channelwrite/writer_test.go
git commit -m "feat(channelwrite): add channelWriter struct"
```

### Task 2.2: Implement `enqueue` + `tryActivate`

**Files:**
- Modify: `internalv2/runtime/channelwrite/writer.go`
- Test: `internalv2/runtime/channelwrite/writer_test.go`

- [ ] **Step 1: Write the failing test**

Append to `writer_test.go`:

```go
func TestWriterEnqueueActivatesOnce(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "c1", Type: 2}, LeaderNodeID: 1}
	target.ChannelKey = channelKey(target.ChannelID)
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})

	first := w.enqueue(submittedBatch{target: target, future: newFuture(0)})
	if !first {
		t.Fatal("first enqueue must request activation")
	}
	second := w.enqueue(submittedBatch{target: target, future: newFuture(0)})
	if second {
		t.Fatal("second enqueue while scheduled must not request activation")
	}
	if got := len(w.inbox); got != 2 {
		t.Fatalf("inbox len = %d, want 2", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestWriterEnqueueActivatesOnce' ./internalv2/runtime/channelwrite/ -v`
Expected: FAIL — `enqueue` undefined.

- [ ] **Step 3: Implement `enqueue` and `tryActivate`**

Append to `writer.go`:

```go
// enqueue appends a batch to the inbox and reports whether the caller should
// schedule this writer onto a worker (true only on the scheduled false->true edge).
func (w *channelWriter) enqueue(batch submittedBatch) bool {
	w.mu.Lock()
	w.inbox = append(w.inbox, batch)
	w.mu.Unlock()
	return w.tryActivate()
}

// tryActivate marks the writer scheduled. It returns true only for the
// goroutine that won the false->true transition, which then owns advancing it.
func (w *channelWriter) tryActivate() bool {
	return w.scheduled.CompareAndSwap(false, true)
}

// deactivate clears the scheduled flag and reports whether more work arrived
// after the caller stopped advancing (caller must re-activate if true).
func (w *channelWriter) deactivate() bool {
	w.scheduled.Store(false)
	w.mu.Lock()
	more := len(w.inbox) > 0 || w.state.hasPendingWork()
	w.mu.Unlock()
	return more
}
```

- [ ] **Step 4: Add `hasPendingWork` to `channelState`**

In `state.go`, add:

```go
// hasPendingWork reports whether the channel has prepared items, in-flight
// appends, or committed envelopes still needing post-commit dispatch.
func (s *channelState) hasPendingWork() bool {
	return len(s.pendingItems) > 0 || s.appendInflight > 0 || s.commitBacklog() > 0 || s.hasReadyAppendCompletion || len(s.completedAppends) > 0
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -run 'TestWriterEnqueue' ./internalv2/runtime/channelwrite/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/runtime/channelwrite/writer.go internalv2/runtime/channelwrite/state.go internalv2/runtime/channelwrite/writer_test.go
git commit -m "feat(channelwrite): writer enqueue and CAS activation"
```

### Task 2.3: Shared worker pool wrapper

**Files:**
- Modify: `internalv2/runtime/channelwrite/pool.go`
- Test: `internalv2/runtime/channelwrite/pool_test.go`

The new model needs one shared pool to run blocking append/post-commit work off the advance call stack. Prepare stays inline (pure CPU). Use a blocking ants pool sized to total append+postcommit worker budget.

- [ ] **Step 1: Write the failing test**

Append to `pool_test.go`:

```go
import (
	"sync"
	"testing"
	"time"
)

func TestWorkerPoolRunsTasks(t *testing.T) {
	p := newWorkerPool(4)
	defer p.stop(context.Background())
	var wg sync.WaitGroup
	wg.Add(8)
	var count atomic.Int64
	for i := 0; i < 8; i++ {
		if err := p.submit(func() { count.Add(1); wg.Done() }); err != nil {
			t.Fatalf("submit error = %v", err)
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tasks did not complete")
	}
	if count.Load() != 8 {
		t.Fatalf("count = %d, want 8", count.Load())
	}
}
```

Add the needed imports (`context`, `sync/atomic`) to `pool_test.go`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestWorkerPoolRunsTasks' ./internalv2/runtime/channelwrite/ -v`
Expected: FAIL — `newWorkerPool` undefined.

- [ ] **Step 3: Implement the pool wrapper**

Append to `pool.go` (add `context`, `time`, `github.com/panjf2000/ants/v2` imports):

```go
// workerPool runs blocking channel-write effects off the advance call stack.
type workerPool struct {
	pool *ants.Pool
}

func newWorkerPool(size int) *workerPool {
	if size <= 0 {
		size = 1
	}
	pool, err := ants.NewPool(size, ants.WithNonblocking(false), ants.WithDisablePurge(true))
	if err != nil {
		panic("channelwrite: create worker pool: " + err.Error())
	}
	return &workerPool{pool: pool}
}

// submit runs fn on a pooled goroutine. It blocks briefly if all workers are busy.
func (p *workerPool) submit(fn func()) error {
	return p.pool.Submit(fn)
}

func (p *workerPool) stop(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		p.pool.Release()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -run 'TestWorkerPoolRunsTasks' ./internalv2/runtime/channelwrite/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/runtime/channelwrite/pool.go internalv2/runtime/channelwrite/pool_test.go
git commit -m "feat(channelwrite): shared worker pool wrapper"
```

### Task 2.4: Implement `advance()` — prepare + append

**Files:**
- Modify: `internalv2/runtime/channelwrite/writer.go`
- Test: `internalv2/runtime/channelwrite/writer_advance_test.go` (new)

`advance()` is the single state-machine entry. It drains the inbox, prepares inline, admits to the channelState, then submits append to the pool. The append completion calls back into the writer to advance again. This task wires prepare→append; post-commit lands in Task 2.5.

- [ ] **Step 1: Write the failing test**

Create `internalv2/runtime/channelwrite/writer_advance_test.go`:

```go
package channelwrite

import (
	"context"
	"testing"
	"time"
)

func TestWriterAdvanceAppendsInOrder(t *testing.T) {
	appender := &orderedAppender{}
	rt := newWriterRuntime(t, writerRuntimeConfig{appender: appender})
	defer rt.stop()

	target := benchmarkAuthorityTarget("order-1")
	const batches = 50
	futures := make([]*Future, batches)
	for i := 0; i < batches; i++ {
		f, err := rt.submit(target, []SendBatchItem{benchmarkSendItem("order-1")})
		if err != nil {
			t.Fatalf("submit %d error = %v", i, err)
		}
		futures[i] = f
	}
	for i, f := range futures {
		res, err := f.Wait(context.Background())
		if err != nil {
			t.Fatalf("wait %d error = %v", i, err)
		}
		if len(res) != 1 || res[0].Err != nil || res[0].Result.Reason != ReasonSuccess {
			t.Fatalf("batch %d result = %#v, want success", i, res)
		}
	}
	if !appender.seqsMonotonic() {
		t.Fatalf("append message seqs not monotonic: %v", appender.seqs())
	}
}

func TestWriterAdvanceCompletesWithinDeadline(t *testing.T) {
	rt := newWriterRuntime(t, writerRuntimeConfig{appender: &orderedAppender{}})
	defer rt.stop()
	target := benchmarkAuthorityTarget("deadline-1")
	f, err := rt.submit(target, []SendBatchItem{benchmarkSendItem("deadline-1")})
	if err != nil {
		t.Fatalf("submit error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := f.Wait(ctx); err != nil {
		t.Fatalf("wait error = %v", err)
	}
}
```

Create the test harness in a new file `internalv2/runtime/channelwrite/writer_testhelpers_test.go`:

```go
package channelwrite

import (
	"context"
	"sort"
	"sync"
	"testing"
)

// orderedAppender records the message seqs it appends, in call order.
type orderedAppender struct {
	mu    sync.Mutex
	calls []uint64
	next  uint64
}

func (a *orderedAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := AppendBatchResult{Items: make([]AppendBatchItemResult, len(req.Messages))}
	for i, msg := range req.Messages {
		a.next++
		a.calls = append(a.calls, a.next)
		out.Items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: a.next, Message: msg}
	}
	return out, nil
}

func (a *orderedAppender) seqs() []uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]uint64(nil), a.calls...)
}

func (a *orderedAppender) seqsMonotonic() bool {
	s := a.seqs()
	return sort.SliceIsSorted(s, func(i, j int) bool { return s[i] < s[j] })
}

type writerRuntimeConfig struct {
	appender Appender
}

// writerRuntime wraps a single writer + pool for advance-level tests, bypassing Group.
type writerRuntime struct {
	t      *testing.T
	pool   *workerPool
	writer *channelWriter
}

func newWriterRuntime(t *testing.T, cfg writerRuntimeConfig) *writerRuntime {
	t.Helper()
	limits := channelStateLimits{pendingItemHighWatermark: 4096, appendInflightLimit: 1}
	target := benchmarkAuthorityTarget("rt")
	w := newChannelWriter(target, limits)
	rt := &writerRuntime{t: t, pool: newWorkerPool(4), writer: w}
	w.ports = writerPorts{
		prepare:  preparePorts{messageID: newBenchmarkMessageIDs(1)},
		append:   appendPorts{appender: cfg.appender},
		commit:   commitPorts{},
		pool:     rt.pool,
		schedule: rt.schedule,
	}
	return rt
}

func (rt *writerRuntime) schedule(w *channelWriter) {
	_ = rt.pool.submit(func() { w.advance() })
}

func (rt *writerRuntime) submit(target AuthorityTarget, items []SendBatchItem) (*Future, error) {
	future := newFuture(len(items))
	batch := submittedBatch{target: target, items: cloneSendBatchItems(items), future: future}
	if rt.writer.enqueue(batch) {
		rt.schedule(rt.writer)
	}
	return future, nil
}

func (rt *writerRuntime) stop() {
	_ = rt.pool.stop(context.Background())
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestWriterAdvance' ./internalv2/runtime/channelwrite/ -v`
Expected: FAIL — `writerPorts`, `w.ports`, `advance` undefined.

- [ ] **Step 3: Add `writerPorts` and implement `advance` (prepare + append)**

Add to `writer.go`:

```go
// writerPorts are the dependencies a writer needs to advance its state machine.
type writerPorts struct {
	prepare  preparePorts
	append   appendPorts
	commit   commitPorts
	pool     *workerPool
	schedule func(*channelWriter)
}
```

Add the `ports writerPorts` field to the `channelWriter` struct.

Implement `advance` in `writer.go`:

```go
// advance pushes the writer's state machine forward as far as it can without
// blocking, submitting blocking append/commit effects to the shared pool.
// Exactly one goroutine runs advance for a given writer at a time.
func (w *channelWriter) advance() {
	for {
		w.mu.Lock()
		w.drainInboxLocked()
		appendEffect, hasAppend := w.nextAppendLocked()
		commitEffect, hasCommit := w.nextCommitLocked()
		w.mu.Unlock()

		if hasAppend {
			w.runAppend(appendEffect)
		}
		if hasCommit {
			w.runCommit(commitEffect)
		}
		if !hasAppend && !hasCommit {
			if w.deactivate() && w.tryActivate() {
				continue // work arrived during the deactivate window; keep going
			}
			return
		}
	}
}

// drainInboxLocked prepares inbox batches inline and admits prepared items to state.
func (w *channelWriter) drainInboxLocked() {
	if len(w.inbox) == 0 {
		return
	}
	inbox := w.inbox
	w.inbox = nil
	for _, batch := range inbox {
		outcome := prepareBatch(context.Background(), batch.items, w.ports.prepare)
		w.admitPreparedLocked(batch, outcome)
	}
}
```

> **NOTE for the implementer:** `admitPreparedLocked`, `nextAppendLocked`, `nextCommitLocked`, `runAppend`, `runCommit` are defined in Task 2.5. This task compiles only after 2.5 lands; if running tests between tasks, mark `TestWriterAdvance*` with `t.Skip("pending 2.5")` and remove the skip in 2.5. Add `"context"` to `writer.go` imports.

- [ ] **Step 4: Commit (compiles after 2.5)**

```bash
git add internalv2/runtime/channelwrite/writer.go internalv2/runtime/channelwrite/writer_advance_test.go internalv2/runtime/channelwrite/writer_testhelpers_test.go
git commit -m "feat(channelwrite): advance loop skeleton (prepare+append)"
```

### Task 2.5: Implement append/commit transitions inside the writer

**Files:**
- Modify: `internalv2/runtime/channelwrite/writer.go`
- Test: existing `writer_advance_test.go` (un-skip)

This task ports the reactor's prepare-completion admission, in-order append, and post-commit scheduling into writer methods that reuse the existing `channelState` helpers (`enqueuePrepared`, `canAdmit`, `nextAppendBatch`, `recordAppendCompletion`, `popNextAppendCompletion`, `enqueueCommitted`, `nextCommitEffect`, `finishAppend`, `finishCommitSuccess`, `finishCommitFailure`) and the existing pure effect runners (`appendEffect.run`, `commitEffect.run`).

- [ ] **Step 1: Implement the locked-state helpers and effect runners**

Add to `writer.go`:

```go
// admitPreparedLocked applies prepare results: completes terminal items on the
// future immediately and enqueues append-bound items, honoring canAdmit backpressure.
func (w *channelWriter) admitPreparedLocked(batch submittedBatch, outcome prepareOutcome) {
	matching := make([]preparedSend, 0, len(outcome.prepared))
	matchingIndex := make(map[int]struct{}, len(outcome.prepared))
	for _, item := range outcome.prepared {
		if !preparedCommandMatchesTarget(batch.target, item.Command) {
			outcome.results[item.Index] = SendBatchItemResult{Err: ErrStaleRoute}
			continue
		}
		item.future = batch.future
		matching = append(matching, item)
		matchingIndex[item.Index] = struct{}{}
	}
	if len(matching) > 0 {
		w.state.refreshRecipientMetadata(batch.target)
		if w.state.canAdmit(len(matching)) {
			w.state.enqueuePrepared(matching)
		} else {
			for _, item := range matching {
				delete(matchingIndex, item.Index)
				outcome.results[item.Index] = SendBatchItemResult{Err: ErrChannelBusy}
			}
		}
	}
	batch.future.completeItems(outcome.results, func(index int) bool {
		_, pendingAppend := matchingIndex[index]
		return !pendingAppend
	})
}

func (w *channelWriter) nextAppendLocked() (appendEffect, bool) {
	seq, items, ok := w.state.nextAppendBatch()
	if !ok {
		return appendEffect{}, false
	}
	return appendEffect{target: w.state.target, key: w.key, seq: seq, items: items}, true
}

func (w *channelWriter) runAppend(effect appendEffect) {
	w.ports.pool.submit(func() {
		completion := effect.run(context.Background(), w.ports.append)
		w.applyAppendCompletion(completion)
		w.reschedule()
	})
}

func (w *channelWriter) applyAppendCompletion(event appendCompletedEvent) {
	w.mu.Lock()
	w.state.recordAppendCompletion(event)
	for {
		next, ok := w.state.popNextAppendCompletion()
		if !ok {
			break
		}
		w.state.finishAppend(len(next.items))
		for _, item := range next.items {
			completeAppendItem(item, next)
		}
	}
	w.mu.Unlock()
}
```

> **Implementer note:** `completeAppendItem` ports the per-item completion the reactor did in its append-completion path. The exact logic lives in `effects.go` (the reactor's append-completed handler around `effects.go:184`, which calls `state.enqueueCommitted(committedEnvelopeForAppend(completion.item, completion.appended))` and sets the item future result). Reproduce it as a free function operating on the writer's `state` under the held lock: set `item.future` result from `appendItemCompletion`, and on a successful append call `w.state.enqueueCommitted(committedEnvelopeForAppend(item, completion))`. Relocate `committedEnvelopeForAppend` out of `effects.go` (it is deleted in Task 2.8) into `append.go`. The committed-envelope enqueue MUST stay inside the lock so commit ordering matches append order.

- [ ] **Step 2: Implement commit transition**

Add to `writer.go`:

```go
func (w *channelWriter) nextCommitLocked() (commitEffect, bool) {
	return w.state.nextCommitEffect(w.key)
}

func (w *channelWriter) runCommit(effect commitEffect) {
	w.ports.pool.submit(func() {
		completion := effect.run(context.Background(), w.ports.commit)
		w.applyCommitCompletion(completion)
		w.reschedule()
	})
}

func (w *channelWriter) applyCommitCompletion(event commitCompletedEvent) {
	w.mu.Lock()
	if event.err == nil {
		w.state.recordSubscriberCache(event.subscriberCache)
		w.state.finishCommitSuccess(event.checkpointSeq)
	} else {
		w.state.finishCommitFailure()
		w.state.dropCurrentCommit()
	}
	w.mu.Unlock()
	if event.err != nil {
		observePostCommitFailure(w.ports.append.observer, postCommitFailureFromEvent(event))
	}
}

// reschedule re-activates the writer so a worker picks up newly available work.
func (w *channelWriter) reschedule() {
	if w.tryActivate() {
		w.ports.schedule(w)
	}
}
```

> **Implementer note:** `postCommitFailureFromEvent` builds a `PostCommitFailureObservation` from `commitCompletedEvent` (the reactor did this inline in `recordCommitCompletion`, reactor.go:101-123). Extract that mapping into a free function so both old and new code can call it; reuse the exact field assignments.

- [ ] **Step 3: Un-skip the advance tests and run them with the race detector**

Remove any `t.Skip` added in Task 2.4.

Run: `go test -race -run 'TestWriterAdvance' ./internalv2/runtime/channelwrite/ -v`
Expected: PASS, no `DATA RACE`.

- [ ] **Step 4: Run the order-invariant test repeatedly**

Run: `go test -race -run 'TestWriterAdvanceAppendsInOrder' -count 20 ./internalv2/runtime/channelwrite/`
Expected: PASS all 20 runs (the single-writer invariant must hold under repetition).

- [ ] **Step 5: Commit**

```bash
git add internalv2/runtime/channelwrite/writer.go internalv2/runtime/channelwrite/writer_advance_test.go
git commit -m "feat(channelwrite): writer append and commit transitions"
```

### Task 2.6: Shard — writer lookup/create with striped locking

**Files:**
- Create: `internalv2/runtime/channelwrite/shard.go`
- Test: `internalv2/runtime/channelwrite/shard_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internalv2/runtime/channelwrite/shard_test.go`:

```go
package channelwrite

import (
	"sync"
	"testing"
)

func TestShardGetOrCreateStable(t *testing.T) {
	s := newShard(channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	target := benchmarkAuthorityTarget("s1")
	w1 := s.getOrCreate(target)
	w2 := s.getOrCreate(target)
	if w1 != w2 {
		t.Fatal("getOrCreate must return the same writer for the same channel")
	}
}

func TestShardGetOrCreateConcurrent(t *testing.T) {
	s := newShard(channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	target := benchmarkAuthorityTarget("s2")
	var wg sync.WaitGroup
	writers := make([]*channelWriter, 32)
	for i := range writers {
		wg.Add(1)
		go func(i int) { defer wg.Done(); writers[i] = s.getOrCreate(target) }(i)
	}
	wg.Wait()
	for i := 1; i < len(writers); i++ {
		if writers[i] != writers[0] {
			t.Fatal("concurrent getOrCreate must converge to one writer")
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestShard' ./internalv2/runtime/channelwrite/ -v`
Expected: FAIL — `newShard` undefined.

- [ ] **Step 3: Implement the shard**

Create `internalv2/runtime/channelwrite/shard.go`:

```go
package channelwrite

import "sync"

// shard owns a subset of channel writers selected by channel-key hash.
// Its lock guards only writer lookup/creation, never advance execution.
type shard struct {
	limits  channelStateLimits
	mu      sync.RWMutex
	writers map[string]*channelWriter
}

func newShard(limits channelStateLimits) *shard {
	return &shard{limits: limits, writers: make(map[string]*channelWriter)}
}

// getOrCreate returns the writer for target's channel, creating it once.
func (s *shard) getOrCreate(target AuthorityTarget) *channelWriter {
	key := targetKey(target)
	s.mu.RLock()
	w := s.writers[key]
	s.mu.RUnlock()
	if w != nil {
		return w
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w = s.writers[key]; w != nil {
		return w
	}
	w = newChannelWriter(target, s.limits)
	s.writers[key] = w
	return w
}

// lookup returns the writer for key if present.
func (s *shard) lookup(key string) *channelWriter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writers[key]
}
```

- [ ] **Step 4: Run the test with the race detector**

Run: `go test -race -run 'TestShard' ./internalv2/runtime/channelwrite/ -v`
Expected: PASS, no `DATA RACE`.

- [ ] **Step 5: Commit**

```bash
git add internalv2/runtime/channelwrite/shard.go internalv2/runtime/channelwrite/shard_test.go
git commit -m "feat(channelwrite): shard with striped writer creation"
```

### Task 2.7: Switch `Group` to shards + writers

**Files:**
- Modify: `internalv2/runtime/channelwrite/group.go`
- Test: existing `internalv2/runtime/channelwrite/*_test.go` (the regression net)

This is the switchover. `Group` stops holding `[]*reactor` and the `effectScheduler`; it holds `[]*shard` and one `workerPool`. The public methods keep their signatures.

- [ ] **Step 1: Rewrite the `Group` struct and `New`**

In `group.go`, replace the `Group` struct (lines 150-159) and `New` (lines 162-179):

```go
// Group owns a set of hash-sharded local authority channel writers.
type Group struct {
	opts   Options
	shards []*shard
	pool   *workerPool
	ports  writerPorts

	mu       sync.RWMutex
	started  bool
	stopping bool
	stopped  bool
}

// New creates a channel write group with conservative defaults.
func New(opts Options) *Group {
	opts = applyDefaults(opts)
	limits := stateLimitsFromOptions(opts)
	poolSize := opts.AppendWorkers*opts.ReactorCount + opts.PostCommitWorkers*opts.ReactorCount
	pool := newWorkerPool(poolSize)
	group := &Group{
		opts:   opts,
		pool:   pool,
		shards: make([]*shard, opts.ReactorCount),
	}
	group.ports = writerPorts{
		prepare:  preparePortsFromOptions(opts),
		append:   appendPortsFromOptions(opts),
		commit:   commitPortsFromOptions(opts),
		pool:     pool,
		schedule: group.schedule,
	}
	for i := range group.shards {
		group.shards[i] = newShard(limits)
	}
	return group
}

func (g *Group) schedule(w *channelWriter) {
	_ = g.pool.submit(func() { w.advance() })
}

func (g *Group) shardForTarget(target AuthorityTarget) *shard {
	idx := int(hashString64(targetKey(target)) % uint64(len(g.shards)))
	return g.shards[idx]
}
```

> **Implementer note:** `newChannelWriter` must set `w.ports = g.ports` when the shard creates a writer. Add an `installPorts(writerPorts)` step: change `shard.getOrCreate` to take the group ports, or have `Group` set `w.ports` right after `getOrCreate`. Simplest: add a `ports writerPorts` parameter to `newShard`/`getOrCreate` and assign in `newChannelWriter`. Pick one and keep it consistent.

- [ ] **Step 2: Rewrite `SubmitLocal`**

Replace `SubmitLocal` (lines 279-305):

```go
// SubmitLocal admits a batch to the local channel-authority writer.
func (g *Group) SubmitLocal(ctx context.Context, target AuthorityTarget, items []SendBatchItem) (*Future, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if target.LeaderNodeID != g.opts.LocalNodeID {
		return nil, ErrNotChannelAuthority
	}
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	g.mu.RLock()
	if !g.started || g.stopping || g.stopped {
		g.mu.RUnlock()
		return nil, ErrBackpressured
	}
	g.mu.RUnlock()

	copiedItems := cloneSendBatchItems(items)
	future := newFuture(len(copiedItems))
	writer := g.shardForTarget(target).getOrCreate(target)
	if writer.enqueue(submittedBatch{target: target, items: copiedItems, future: future}) {
		g.schedule(writer)
	}
	observeLocalAdmission(g.opts.Observer, LocalAdmissionObservation{Result: "accepted", Items: len(items)})
	return future, nil
}
```

> **Implementer note:** the old `enqueue` returned `ErrBackpressured` when the bounded mailbox/effectSlots were full. Per-channel admission backpressure now lives in `canAdmit` (returns `ErrChannelBusy` to the item future). If a global outstanding-work bound is still required, add an atomic counter on `Group` checked before `getOrCreate`; otherwise rely on `canAdmit`. Confirm against the existing backpressure tests in Step 5 and adjust which error they expect ONLY if the test asserts an internal mechanism rather than client-visible behavior.

- [ ] **Step 3: Rewrite `Start`, `Stop`, `ApplySubscriberMutation`**

`Start` (lines 183-202): drop the reactor loop; just flip `started`. Writers are created lazily.

```go
func (g *Group) Start(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping || g.stopped {
		return ErrBackpressured
	}
	if g.started {
		return nil
	}
	g.started = true
	return nil
}
```

`Stop` (lines 205-236): close admission, drain via the pool, release it.

```go
func (g *Group) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	if !g.started || g.stopped {
		g.mu.Unlock()
		return nil
	}
	g.stopping = true
	g.mu.Unlock()

	if err := g.drainWriters(ctx); err != nil {
		return err
	}
	if err := g.pool.stop(ctx); err != nil {
		return err
	}
	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()
	return nil
}
```

> **Implementer note:** `drainWriters` waits until every shard's writers report `!hasPendingWork()` and not `scheduled`, or ctx expires. Poll with a short backoff (e.g. 1ms) since writers complete asynchronously through the pool. The previous `Stop` released committed effects on stop-context cancellation; preserve "stop after admission close still drains accepted appends and post-commit effects" semantics — accepted work must finish, new `SubmitLocal` returns `ErrBackpressured` (already handled by the `stopping` check).

`ApplySubscriberMutation` (lines 239-276): replace the reactor mailbox send with a direct, locked state update on the target writer.

```go
func (g *Group) ApplySubscriberMutation(ctx context.Context, update SubscriberMutationUpdate) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if g == nil {
		return nil
	}
	g.mu.RLock()
	if !g.started || g.stopping || g.stopped || len(g.shards) == 0 {
		g.mu.RUnlock()
		return nil
	}
	g.mu.RUnlock()
	target := AuthorityTarget{
		ChannelID:                 update.ChannelID,
		ChannelKey:                channelKey(update.ChannelID),
		Large:                     update.Large,
		SubscriberMutationVersion: update.SubscriberMutationVersion,
	}
	writer := g.shardForTarget(target).lookup(targetKey(target))
	if writer == nil {
		return nil // no cached state for an unseen channel; nothing to update
	}
	writer.mu.Lock()
	writer.state.applySubscriberMutation(update.clone())
	writer.mu.Unlock()
	return nil
}
```

- [ ] **Step 4: Delete `reactorForTarget`**

Remove the now-unused `reactorForTarget` method (lines 313-317).

- [ ] **Step 5: Run the full package test suite (the regression net)**

Run: `go test ./internalv2/runtime/channelwrite/ 2>&1 | tail -30`
Expected: compile errors from tests still referencing `reactor`/`effectScheduler` internals. These are addressed in Task 2.8. For now, expect failures ONLY in reactor-internal tests; the behavior tests (`router_test.go`, `commit_test.go` public paths, `delivery_test.go`) must pass once 2.8 removes the dead reactor tests.

- [ ] **Step 6: Commit**

```bash
git add internalv2/runtime/channelwrite/group.go
git commit -m "feat(channelwrite): switch Group to shards and writers"
```

### Task 2.8: Delete reactor/scheduler/effects and reconcile tests

**Files:**
- Delete: `internalv2/runtime/channelwrite/reactor.go`, `scheduler.go`, `effects.go`
- Delete/rewrite: `reactor_test.go`, `prepare_test.go` (reactor-internal portions), `commit_test.go` (reactor-internal portions), `append_test.go` (reactor-internal portions)
- Modify: `internalv2/runtime/channelwrite/effects.go` — move `preparedCommandMatchesTarget` to `writer.go` or `prepare.go` before deleting `effects.go`

- [ ] **Step 1: Relocate still-needed free functions**

Before deleting `effects.go`, move these (still used by the writer) into `prepare.go`: `preparedCommandMatchesTarget`. Move any other free functions the writer references (e.g. `effectPanicError` if commit/append runners still use it) into the appropriate stage file. Grep first:

Run: `grep -n "func " internalv2/runtime/channelwrite/effects.go`
For each function, `grep -rn "<name>" internalv2/runtime/channelwrite/*.go` to see if non-reactor code uses it; relocate the used ones, delete the rest with the file.

- [ ] **Step 2: Delete the reactor files**

```bash
git rm internalv2/runtime/channelwrite/reactor.go internalv2/runtime/channelwrite/scheduler.go internalv2/runtime/channelwrite/effects.go
```

- [ ] **Step 3: Delete reactor-internal tests, keep behavior tests**

`git rm internalv2/runtime/channelwrite/reactor_test.go`. For `prepare_test.go`, `append_test.go`, `commit_test.go`: keep tests that exercise pure stage functions (`prepareBatch`, `appendEffect.run`, `commitEffect.run`) and delete tests that construct `newReactor` or assert reactor private fields. The benchmark `newBenchmarkPressureReactor` and its tests are removed (the pressure observation surface is re-tested in Phase 4).

- [ ] **Step 4: Build and run the full suite with the race detector**

Run: `go test -race ./internalv2/runtime/channelwrite/ 2>&1 | tail -30`
Expected: `ok`, no `DATA RACE`. Fix any remaining references to deleted symbols.

- [ ] **Step 5: Run the broader internalv2 build to confirm the public API held**

Run: `go build ./internalv2/... && go test ./internalv2/app/ 2>&1 | tail -10`
Expected: build succeeds; `internalv2/app` tests pass (proves `wiring.go` did not need changes).

- [ ] **Step 6: Commit**

```bash
git add -A internalv2/runtime/channelwrite/
git commit -m "refactor(channelwrite): remove reactor model, keep behavior tests"
```

### Task 2.9: Concurrent multi-channel order-invariant stress test

**Files:**
- Test: `internalv2/runtime/channelwrite/group_stress_test.go` (new)

- [ ] **Step 1: Write the stress test**

Create `internalv2/runtime/channelwrite/group_stress_test.go`:

```go
package channelwrite

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestGroupConcurrentChannelsPreserveOrder(t *testing.T) {
	const channels = 64
	const perChannel = 100
	appenders := make(map[string]*orderedAppender)
	for i := 0; i < channels; i++ {
		appenders[fmt.Sprintf("ch-%d", i)] = &orderedAppender{}
	}
	routing := &routingAppender{byChannel: appenders}

	group := New(Options{
		LocalNodeID:       1,
		ReactorCount:      8,
		AppendWorkers:     4,
		PostCommitWorkers: 2,
		MessageID:         newBenchmarkMessageIDs(1),
		Appender:          routing,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = group.Stop(ctx)
	})

	var wg sync.WaitGroup
	for i := 0; i < channels; i++ {
		ch := fmt.Sprintf("ch-%d", i)
		target := benchmarkAuthorityTarget(ch)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perChannel; j++ {
				f, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem(ch)})
				if err != nil {
					t.Errorf("submit error = %v", err)
					return
				}
				if _, err := f.Wait(context.Background()); err != nil {
					t.Errorf("wait error = %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	for ch, a := range appenders {
		if !a.seqsMonotonic() {
			t.Fatalf("channel %s appends not monotonic: %v", ch, a.seqs())
		}
	}
}
```

Add the routing appender to `writer_testhelpers_test.go`:

```go
// routingAppender dispatches AppendBatch to a per-channel orderedAppender.
type routingAppender struct {
	byChannel map[string]*orderedAppender
}

func (r *routingAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a := r.byChannel[req.ChannelID.ID]
	if a == nil {
		return AppendBatchResult{}, fmt.Errorf("no appender for channel %q", req.ChannelID.ID)
	}
	return a.AppendBatch(ctx, req)
}
```

Add `"fmt"` to the helper file imports.

- [ ] **Step 2: Run the stress test with the race detector, repeated**

Run: `go test -race -run 'TestGroupConcurrentChannelsPreserveOrder' -count 10 ./internalv2/runtime/channelwrite/`
Expected: PASS all 10 runs, no `DATA RACE`. This is the central correctness proof of the new model.

- [ ] **Step 3: Commit**

```bash
git add internalv2/runtime/channelwrite/group_stress_test.go internalv2/runtime/channelwrite/writer_testhelpers_test.go
git commit -m "test(channelwrite): concurrent multi-channel order invariant"
```

---

## Phase 3 — Inline prepare + remove dead complexity + full Future pooling

### Task 3.1: Confirm prepare is inline and no `effectWake` remains

**Files:**
- Inspect: `internalv2/runtime/channelwrite/`

- [ ] **Step 1: Verify the dead constructs are gone**

Run: `grep -rn "effectWake\|completedPrepare\|nextDrainSeq\|nextPrepareSeq\|pendingPrepare\|effectScheduler" internalv2/runtime/channelwrite/*.go`
Expected: no matches in non-test files (these belonged to the reactor model deleted in 2.8). If any remain, they are dead code — remove them.

- [ ] **Step 2: Verify prepare runs inline in `advance`**

Confirm `drainInboxLocked` calls `prepareBatch` directly (not via the pool). This is already the case from Task 2.4. No change needed; this step is a checkpoint.

- [ ] **Step 3: Commit only if cleanup was needed**

```bash
git add -A internalv2/runtime/channelwrite/
git commit -m "refactor(channelwrite): remove residual reactor constructs" || echo "nothing to clean"
```

### Task 3.2: Full Future pooling with release on Wait

**Files:**
- Modify: `internalv2/runtime/channelwrite/future.go`, `pool.go`
- Test: `internalv2/runtime/channelwrite/future_test.go` (new or existing)

Now that `Group` owns the call path, release Future buffers after `Wait` returns a snapshot copy.

- [ ] **Step 1: Write the test**

Create/append `internalv2/runtime/channelwrite/future_test.go`:

```go
package channelwrite

import (
	"context"
	"testing"
)

func TestFutureWaitReturnsSnapshotCopy(t *testing.T) {
	f := newFuture(1)
	f.complete([]SendBatchItemResult{{Result: SendResult{Reason: ReasonSuccess}}})
	got, err := f.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait error = %v", err)
	}
	if len(got) != 1 || got[0].Result.Reason != ReasonSuccess {
		t.Fatalf("Wait result = %#v", got)
	}
	// Mutating the returned copy must not corrupt pooled buffers.
	got[0].Result.Reason = ReasonSystemError
	again, _ := f.Wait(context.Background())
	if again[0].Result.Reason != ReasonSuccess {
		t.Fatal("returned slice aliases internal buffer")
	}
}
```

- [ ] **Step 2: Run to verify current behavior**

Run: `go test -run 'TestFutureWaitReturnsSnapshotCopy' ./internalv2/runtime/channelwrite/ -v`
Expected: PASS (current `snapshot()` already copies). This test guards the invariant before adding release.

- [ ] **Step 3: Add buffer release after terminal Wait**

In `future.go`, after a Future is fully done and the caller has taken a snapshot, release its buffers back to the pool. Safest implementation: release in `Group.SubmitLocal`'s caller is out of scope (the gateway calls `Wait`), so release inside `snapshot()` is unsafe (could be called twice). Instead, add an explicit `Future.recycle()` the `Group` does NOT expose externally, and have the gateway path keep using `Wait` only. Given the public boundary, the conservative correct choice is: keep buffer pooling at acquire-time only (Phase 1) and do NOT auto-release, because the Future may outlive a single Wait. Document this decision:

Add a comment in `future.go` above `newFuture`:

```go
// newFuture draws backing buffers from a pool to avoid per-SEND slice allocation.
// Buffers are intentionally not auto-released: a Future may be awaited more than
// once and the gateway boundary does not signal "last reader". The pool still
// removes the hot-path make() once warm. Revisit only if profiling shows the
// retained buffers dominate.
```

- [ ] **Step 4: Run the package tests**

Run: `go test ./internalv2/runtime/channelwrite/ 2>&1 | tail -5`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internalv2/runtime/channelwrite/future.go internalv2/runtime/channelwrite/future_test.go
git commit -m "test(channelwrite): document Future buffer pooling boundary"
```

### Task 3.3: Remove type-alias forwarding that adds no value

**Files:**
- Modify: `internalv2/runtime/channelwrite/group.go:12-147` (the alias block)

- [ ] **Step 1: Identify package-internal-only aliases**

For each alias in `group.go` lines 12-147 (e.g. `type ChannelID = contract.ChannelID`), check whether it is referenced by code OUTSIDE the channelwrite package:

Run: `grep -rn "channelwrite\.ChannelID\|channelwrite\.SendCommand\|channelwrite\.AuthorityTarget" internalv2/ --include='*.go' | grep -v internalv2/runtime/channelwrite/`

- [ ] **Step 2: Keep externally-referenced aliases, inline the rest**

Aliases referenced by `internalv2/app`, `access`, or `infra` MUST stay (they are the package's exported surface). For aliases used only inside channelwrite, this is a judgment call: leaving them is low-risk. Only remove aliases that are unused entirely (grep returns zero matches anywhere).

Run: `grep -rn "<AliasName>" internalv2/runtime/channelwrite/*.go` for each; delete any with zero references.

- [ ] **Step 3: Build and test**

Run: `go build ./internalv2/... && go test ./internalv2/runtime/channelwrite/ 2>&1 | tail -5`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
git add internalv2/runtime/channelwrite/group.go
git commit -m "refactor(channelwrite): drop unused type aliases"
```

### Task 3.4: Re-measure against baseline

- [ ] **Step 1: Run benchmarks**

```bash
go test -run '^$' -bench '.' -benchmem -count 6 ./internalv2/runtime/channelwrite/ \
  | tee docs/development/perf-runs/2026-06-11-channelwrite-baseline/benchmarks-phase3.txt
```

- [ ] **Step 2: Compare with benchstat if available**

Run: `benchstat docs/development/perf-runs/2026-06-11-channelwrite-baseline/benchmarks.txt docs/development/perf-runs/2026-06-11-channelwrite-baseline/benchmarks-phase3.txt 2>/dev/null || echo "install benchstat: go install golang.org/x/perf/cmd/benchstat@latest"`
Expected: `BenchmarkSubmitLocalManyChannelsParallel` shows higher throughput (lower ns/op) — this is the core throughput win from per-channel parallelism. `allocs/op` lower on the hot path.

- [ ] **Step 3: Commit**

```bash
git add docs/development/perf-runs/2026-06-11-channelwrite-baseline/benchmarks-phase3.txt
git commit -m "perf(channelwrite): record phase 3 benchmark comparison"
```

---

## Phase 4 — Observability mapping + docs

The reactor produced `ReactorPressureObservation` and `EffectWorkerPressureObservation` consumed by `internalv2/app/observability.go`. The new model must keep emitting equivalent signals using atomic counters, never by scanning the writer maps.

### Task 4.1: Atomic pressure counters on Group

**Files:**
- Modify: `internalv2/runtime/channelwrite/group.go`, `writer.go`, `shard.go`
- Test: `internalv2/runtime/channelwrite/pressure_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internalv2/runtime/channelwrite/pressure_test.go`:

```go
package channelwrite

import (
	"context"
	"testing"
	"time"
)

type capturePressure struct{ last ReactorPressureObservation }

func (c *capturePressure) AppendFinished(string, error, time.Duration) {}
func (c *capturePressure) SetChannelWriteReactorPressure(o ReactorPressureObservation) {
	c.last = o
}

func TestGroupEmitsAggregatePressure(t *testing.T) {
	obs := &capturePressure{}
	group := New(Options{
		LocalNodeID:   1,
		ReactorCount:  4,
		AppendWorkers: 4,
		MessageID:     newBenchmarkMessageIDs(1),
		Appender:      &blockingAppender{release: make(chan struct{})},
		Observer:      obs,
	})
	_ = group.Start(context.Background())
	t.Cleanup(func() { _ = group.Stop(context.Background()) })

	target := benchmarkAuthorityTarget("p1")
	for i := 0; i < 5; i++ {
		if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem("p1")}); err != nil {
			t.Fatalf("submit error = %v", err)
		}
	}
	// At least one observation should report pending or in-flight work > 0.
	deadline := time.After(2 * time.Second)
	for {
		if obs.last.PendingAppendItems > 0 || obs.last.AppendInflightItems > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("no pressure observation with pending/in-flight work")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}
```

Add a `blockingAppender` to `writer_testhelpers_test.go`:

```go
// blockingAppender blocks AppendBatch until release is closed, to hold work in-flight.
type blockingAppender struct {
	release chan struct{}
	seq     atomic.Uint64
}

func (a *blockingAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	select {
	case <-a.release:
	case <-ctx.Done():
		return AppendBatchResult{}, ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}
	out := AppendBatchResult{Items: make([]AppendBatchItemResult, len(req.Messages))}
	for i, msg := range req.Messages {
		out.Items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: a.seq.Add(1), Message: msg}
	}
	return out, nil
}
```

Add `"sync/atomic"` and `"time"` to the helper imports if not present.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestGroupEmitsAggregatePressure' ./internalv2/runtime/channelwrite/ -v`
Expected: FAIL — no pressure emitted yet.

- [ ] **Step 3: Add atomic counters and emit on transitions**

Add to the `Group` struct: `pendingAppendItems`, `appendInflightItems`, `postCommitBacklog atomic.Int64`. Pass a pointer to these (or a small `*groupMetrics` struct) into `writerPorts` so writers increment/decrement them at the same points the reactor did:
- `+pending` when `enqueuePrepared` admits items; `-pending +inflight` when `nextAppendBatch` pulls a batch; `-inflight` in `finishAppend`.
- `+postCommitBacklog` when `enqueueCommitted`; `-postCommitBacklog` when a commit is dropped/succeeds.

After each transition, call `observePressure()` which builds `ReactorPressureObservation` from the atomics (set `ReactorID: -1` to denote the aggregate; `MailboxDepth`/`EffectSlots*` map to 0 or the pool's inflight/capacity). Reuse the existing `reactorPressureObserver(observer)` helper and `SetChannelWriteReactorPressure`.

> **Implementer note:** keep the `ReactorPressureObservation` struct shape unchanged so `internalv2/app/observability.go` needs no edit. Only the source of the numbers changes. Map `EffectSlotsUsed/Capacity` to the worker pool's `Running()`/`Cap()` (ants exposes these).

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -race -run 'TestGroupEmitsAggregatePressure' ./internalv2/runtime/channelwrite/ -v`
Expected: PASS, no `DATA RACE`.

- [ ] **Step 5: Confirm the app-side observer still compiles and runs**

Run: `go test ./internalv2/app/ -run 'Observab' 2>&1 | tail -10`
Expected: PASS — `deliveryMessageObserver` and metrics mapping unchanged.

- [ ] **Step 6: Commit**

```bash
git add internalv2/runtime/channelwrite/group.go internalv2/runtime/channelwrite/writer.go internalv2/runtime/channelwrite/pressure_test.go internalv2/runtime/channelwrite/writer_testhelpers_test.go
git commit -m "feat(channelwrite): aggregate pressure metrics for writer model"
```

### Task 4.2: Update FLOW.md documents

**Files:**
- Modify: `internalv2/FLOW.md:28` (the `runtime/channelwrite` row)
- Modify: `internalv2/app/FLOW.md` (the "multiple channel-hashed authority reactors" passages, around lines 77-119)

- [ ] **Step 1: Update `internalv2/FLOW.md`**

Change the `runtime/channelwrite` table row (line 28) to describe the per-channel writer model: replace "Channel-authority write reactors" with language matching the new model — each locally authoritative channel is served by an independent single-writer state machine, hash-sharded for lookup, advanced by a shared worker pool.

- [ ] **Step 2: Update `internalv2/app/FLOW.md`**

Find passages mentioning "multiple channel-hashed authority reactors", "reactor", "ReactorCount controls channel-state sharding", and the prepare/append/post-commit "ants stage pools" (around lines 77-119). Rewrite to: `ReactorCount` now controls the number of lookup shards; append/post-commit run on one shared worker pool; prepare runs inline on the advance path; per-channel append ordering is guaranteed by the single-writer invariant rather than per-reactor sequencing.

- [ ] **Step 3: Verify no stale "reactor" references remain in the flow docs**

Run: `grep -n "reactor" internalv2/FLOW.md internalv2/app/FLOW.md`
Expected: only historical/contextual mentions remain, if any; the channelwrite description matches the code.

- [ ] **Step 4: Commit**

```bash
git add internalv2/FLOW.md internalv2/app/FLOW.md
git commit -m "docs(channelwrite): update FLOW for per-channel writer model"
```

### Task 4.3: Final verification

- [ ] **Step 1: Full targeted test with race detector**

Run: `go test -race ./internalv2/... 2>&1 | tail -20`
Expected: all `ok`, no `DATA RACE`.

- [ ] **Step 2: Broader build + package tests**

Run: `go test ./internalv2/... ./pkg/clusterv2/... 2>&1 | tail -20`
Expected: all `ok`.

- [ ] **Step 3: Remove the resolved CODE_QUALITY note**

The Phase 0 benchmark-failure note in `docs/development/CODE_QUALITY.md` is now resolved. Delete that bullet.

- [ ] **Step 4: Update CODE_QUALITY note removal + commit**

```bash
git add docs/development/CODE_QUALITY.md
git commit -m "docs: clear resolved channelwrite benchmark note"
```

- [ ] **Step 5: (Optional) Three-node throughput comparison**

If the Phase 0 three-node baseline was captured, run `scripts/bench-wukongimv2-three-nodes-10kch.sh` again and compare the new evidence directory's send/recv throughput and SEND p99 against the before snapshot. Record the comparison summary in `docs/development/CODE_QUALITY.md` or the perf-run `summary.md`. If throughput regressed versus baseline, STOP and investigate before considering the refactor complete.

---

## Notes for the implementer

- **Single-writer invariant is everything.** Every state mutation on `channelState` must happen either inside `writer.mu` or inside the pool task that the `scheduled` flag guarantees is unique per writer. When in doubt, hold `writer.mu`.
- **Don't scan writer maps for metrics.** All pressure numbers come from atomics. Reintroducing an O(N) scan defeats a core goal.
- **The existing `*_test.go` behavior tests are the contract.** If one fails after the switchover, the new model is wrong — fix the model, not the test, unless the test asserts a deleted internal mechanism.
- **`AppendInflightLimit` default is 1.** Same-channel appends are strictly serial by default; do not parallelize them within a writer.
