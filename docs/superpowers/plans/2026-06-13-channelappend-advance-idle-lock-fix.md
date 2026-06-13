# channelappend advance idle-lock optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce `channelWriter.advance()` idle-loop lock contention by removing the no-work path's extra `deactivate()` lock/unlock cycle.

**Architecture:** Add a lock-held `deactivateLocked()` helper that performs the scheduled-to-idle transition while the caller already owns `channelWriter.mu`. `advance()` and `advanceAppendOnly()` use that helper on the no-work path; the existing public `deactivate()` remains for callers/tests that do not hold the lock. This plan deliberately skips the optional atomic hint from the design spec so locked state remains the only correctness source; re-profile before adding another state signal.

**Tech Stack:** Go 1.25; package `github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend`; tests with `go test`, `go test -race`, and targeted benchmarks.

---

## File Structure

- `internalv2/runtime/channelappend/writer.go` — add `deactivateLocked()` and use it inside `advance()` / `advanceAppendOnly()` no-work paths.
- `internalv2/runtime/channelappend/writer_test.go` — add unit tests for the lock-held deactivate helper's runnable and idle cases.
- `internalv2/runtime/channelappend/FLOW.md` — no expected change; current flow language still says one scheduled writer advances state, which remains true.

No new production files.

---

### Task 1: Add lock-held deactivate tests

**Files:**
- Modify: `internalv2/runtime/channelappend/writer_test.go`

These tests are intentionally package-internal because they pin a writer state-machine primitive. They should fail to compile before Task 2 because `deactivateLocked` does not exist yet.

- [ ] **Step 1: Add tests after `TestWriterEnqueueActivatesOnce`**

Append this code to `writer_test.go`:

```go
func TestWriterDeactivateLockedReportsRunnableInbox(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "c1", Type: 2}, LeaderNodeID: 1}
	target.ChannelKey = channelKey(target.ChannelID)
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	w.scheduled.Store(true)

	w.mu.Lock()
	w.inbox = append(w.inbox, submittedBatch{target: target, future: newFuture(1)})
	more := w.deactivateLocked()
	w.mu.Unlock()

	if !more {
		t.Fatal("deactivateLocked must report runnable inbox work")
	}
	if w.scheduled.Load() {
		t.Fatal("deactivateLocked must clear scheduled before reporting more work")
	}
	if idleAt := w.lastIdleUnixNano.Load(); idleAt != 0 {
		t.Fatalf("lastIdleUnixNano = %d, want 0 while work is runnable", idleAt)
	}
	if !w.tryActivate() {
		t.Fatal("current advance owner should be able to reactivate after lock-held runnable work")
	}
}

func TestWriterDeactivateLockedMarksIdleWhenNoRunnableWork(t *testing.T) {
	target := AuthorityTarget{ChannelID: ChannelID{ID: "c1", Type: 2}, LeaderNodeID: 1}
	target.ChannelKey = channelKey(target.ChannelID)
	w := newChannelWriter(target, channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1})
	w.scheduled.Store(true)

	w.mu.Lock()
	more := w.deactivateLocked()
	w.mu.Unlock()

	if more {
		t.Fatal("deactivateLocked reported runnable work for an idle writer")
	}
	if w.scheduled.Load() {
		t.Fatal("deactivateLocked must clear scheduled for idle writer")
	}
	if idleAt := w.lastIdleUnixNano.Load(); idleAt == 0 {
		t.Fatal("deactivateLocked must record idle timestamp")
	}
}
```

- [ ] **Step 2: Run the tests and confirm RED**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -run 'TestWriterDeactivateLocked' -count=1
```

Expected: FAIL to build with an error like:

```text
w.deactivateLocked undefined (type *channelWriter has no field or method deactivateLocked)
```

If the tests pass before implementation, stop and inspect whether `deactivateLocked` already exists on the branch.

---

### Task 2: Collapse the no-work deactivate path into the existing lock window

**Files:**
- Modify: `internalv2/runtime/channelappend/writer.go`
- Test: `internalv2/runtime/channelappend/writer_test.go`

- [ ] **Step 1: Add `deactivateLocked` and simplify `deactivate`**

Replace the current `deactivate` function in `writer.go` with these two functions:

```go
// deactivate clears the scheduled flag and reports whether more work arrived
// after the caller stopped advancing (caller must re-activate if true).
func (w *channelWriter) deactivate() bool {
	w.mu.Lock()
	more := w.deactivateLocked()
	w.mu.Unlock()
	return more
}

// deactivateLocked performs the scheduled-to-idle transition while w.mu is held.
func (w *channelWriter) deactivateLocked() bool {
	w.scheduled.Store(false)
	more := w.hasRunnableWorkLocked()
	if !more {
		w.lastIdleUnixNano.Store(time.Now().UnixNano())
	}
	return more
}
```

Keep the existing English comment on `deactivate`; add the `deactivateLocked` comment exactly as above because it documents the lock precondition for future maintainers.

- [ ] **Step 2: Update `advance` to use the lock-held idle transition**

Replace the whole `advance` function with:

```go
// advance pushes the writer's state machine forward as far as it can without
// blocking, submitting blocking append/commit effects to the shared pool.
// Exactly one goroutine runs advance for a given writer at a time.
func (w *channelWriter) advance() {
	if !w.ports.commit.hasPostCommitWork() {
		w.advanceAppendOnly()
		return
	}
	var appendEff appendEffect
	var commitEff commitEffect
	for {
		w.mu.Lock()
		w.drainInboxLocked()
		hasAppend := w.nextAppendLocked(&appendEff)
		hasCommit := w.nextCommitLocked(&commitEff)
		if !hasAppend && !hasCommit {
			more := w.deactivateLocked()
			w.mu.Unlock()
			if more && w.tryActivate() {
				continue // work arrived during the deactivate window; keep going
			}
			return
		}
		w.mu.Unlock()

		if hasAppend {
			w.runAppend(&appendEff)
		}
		if hasCommit {
			w.runCommit(&commitEff)
		}
	}
}
```

This preserves the existing behavior of running append/commit effects outside `w.mu` while removing the second no-work-path lock acquisition.

- [ ] **Step 3: Update `advanceAppendOnly` the same way**

Replace the whole `advanceAppendOnly` function with:

```go
func (w *channelWriter) advanceAppendOnly() {
	var appendEff appendEffect
	for {
		w.mu.Lock()
		w.drainInboxLocked()
		hasAppend := w.nextAppendLocked(&appendEff)
		if !hasAppend {
			more := w.deactivateLocked()
			w.mu.Unlock()
			if more && w.tryActivate() {
				continue // work arrived during the deactivate window; keep going
			}
			return
		}
		w.mu.Unlock()

		w.runAppend(&appendEff)
	}
}
```

- [ ] **Step 4: Format and run the new tests**

Run:

```bash
gofmt -w internalv2/runtime/channelappend/writer.go internalv2/runtime/channelappend/writer_test.go
GOWORK=off go test ./internalv2/runtime/channelappend/ -run 'TestWriterDeactivateLocked' -count=1 -v
```

Expected: both `TestWriterDeactivateLockedReportsRunnableInbox` and `TestWriterDeactivateLockedMarksIdleWhenNoRunnableWork` PASS.

- [ ] **Step 5: Run package verification for semantic regressions**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -count=1
GOWORK=off go test ./internalv2/runtime/channelappend/ -race \
  -run 'TestWriterAdvance|TestCommitEffect|TestChannelState|TestGroup|TestAdvancePostCommitReusesEffectWithoutBleed|TestWriterDeactivateLocked' -count=1
```

Expected: both commands PASS. On macOS, the race command may print a linker `LC_DYSYMTAB` warning; that warning is acceptable only if the Go test exits successfully.

- [ ] **Step 6: Commit the implementation**

Run:

```bash
git add internalv2/runtime/channelappend/writer.go internalv2/runtime/channelappend/writer_test.go
git commit -m "perf(channelappend): collapse advance idle deactivate lock"
```

---

### Task 3: Benchmark the scoped hot paths

**Files:** none

- [ ] **Step 1: Run the comparison benchmark set**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -run XXX_NONE \
  -bench 'BenchmarkSubmitLocalHotChannelPostCommit|BenchmarkSubmitLocalHotChannelBatch16|BenchmarkSubmitLocalManyChannelsParallel$' \
  -benchtime=200x -count=5 | tee /tmp/channelappend_idle_lock_bench_after.txt
```

Expected: PASS. Record the five `BenchmarkSubmitLocalHotChannelPostCommit` rows and check that `allocs/op` does not increase.

- [ ] **Step 2: Compare against the previous post-copy benchmark if available**

If `/tmp/channelappend_bench_after.txt` from the prior copy optimization still exists, run:

```bash
GOWORK=off go run golang.org/x/perf/cmd/benchstat@latest \
  /tmp/channelappend_bench_after.txt /tmp/channelappend_idle_lock_bench_after.txt
```

Expected: `BenchmarkSubmitLocalHotChannelPostCommit` should be neutral or faster. If the microbenchmark is noisy, keep the implementation only if tests pass and the code removes the measured duplicate lock path; the true gate for this change is the three-node profile in Task 4.

---

### Task 4: Re-profile gate

**Files:** none

Run this only when a realistic three-node benchmark environment is available. This is the evidence gate for the production bottleneck, not a unit-test gate.

- [ ] **Step 1: Run the same 10000 QPS scenario used to identify the lock bottleneck**

Run:

```bash
WK_BENCH_DURATION=60s WK_BENCH_WARMUP=10s WK_BENCH_COOLDOWN=5s GOWORK=off \
  ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000 \
  --out-dir docs/development/perf-runs/$(date +%Y%m%d-%H%M%S)-qps10000-idle-lock-after
```

Expected: script completes and writes `summary.txt`.

- [ ] **Step 2: Capture CPU profiles during the measurement window**

After warmup starts and while the benchmark is still measuring, run from another shell:

```bash
mkdir -p /tmp/wk_cpu_prof_idle_lock_after
for p in 5011 5012 5013; do
  curl -fsS "http://127.0.0.1:$p/debug/pprof/profile?seconds=30" \
    > /tmp/wk_cpu_prof_idle_lock_after/node-$p-cpu.pb.gz &
done
wait
```

- [ ] **Step 3: Inspect the profile**

Run:

```bash
go tool pprof -top -nodecount=20 /tmp/wk_cpu_prof_idle_lock_after/node-5012-cpu.pb.gz | head -30
```

Expected:

- `channelWriter.deactivate` cumulative share drops from the previously observed high value.
- `channelWriter.mu` lock/unlock flat CPU drops materially from about 21%.
- If `worker_task / append` remains high, capture that as input to the next sticky-worker or inbound batching design rather than expanding this patch.

---

## Self-Review

**Spec coverage:**

- Collapse no-work `advance`/`deactivate` double lock: Task 2.
- Preserve single-writer scheduling race window: Task 1 tests plus Task 2 `more && tryActivate()` flow.
- Avoid batching/prepare movement/pool replacement in this iteration: File Structure and Non-Goals enforced by limited file list.
- Verify with tests, race, benchmark, and profile gate: Tasks 2-4.

**Placeholder scan:** No placeholder markers. Optional profile commands are concrete and scoped.

**Type consistency:**

- `deactivateLocked() bool` is introduced in Task 2 and used by tests from Task 1.
- `advance` and `advanceAppendOnly` still call `runAppend`/`runCommit` outside `w.mu`.
- `time.Now()` remains available through the existing `time` import in `writer.go`.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-13-channelappend-advance-idle-lock-fix.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
