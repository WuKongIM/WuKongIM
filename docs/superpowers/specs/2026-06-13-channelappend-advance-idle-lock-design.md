# channelappend advance idle/lock optimization design

## Context

The previous `channelWriter.advance()` effect-copy optimization removed the
dominant `runtime.duffcopy` and `runtime.duffzero` cost from the post-commit
send path. New three-node profiles show the remaining bottleneck has shifted to
writer scheduling churn and lock contention:

- `runtime.duffcopy` dropped from about 28% flat to about 3%; `duffzero` is no
  longer material.
- `channelWriter.mu` lock/unlock paths now account for about 21% flat CPU.
- `channelWriter.deactivate` has high cumulative cost because the no-work path
  unlocks after `advance` has already inspected state, then immediately locks
  again to re-check runnable work.
- `worker_task` count is about 4.5x append count, which indicates the advance
  worker is often scheduled only to discover no runnable work.
- `append_batch_avg_records` remains about 1.15, but batching is intentionally
  out of scope for this iteration.

## Goal

Reduce `channelWriter.advance()` idle-loop lock contention and empty scheduling
work while preserving the single-writer invariant, ordering, stop behavior, and
current append/post-commit batching semantics.

## Non-Goals

- Do not add an inbound coalescing window.
- Do not change append batch sizing policy.
- Do not move `prepareBatch` outside `channelWriter.mu` in this iteration.
- Do not replace the worker pool or introduce sticky writer workers yet.

These remain candidates for later work after the lock/idle path is re-profiled.

## Design

### Collapse the no-work deactivate path into one lock window

`advance()` and `advanceAppendOnly()` already hold `w.mu` when they know the
current loop did not produce append or commit work. Instead of unlocking and
calling `deactivate()`, they should perform the idle transition while still in
the same critical section:

1. Drain inbox.
2. Try to produce append and commit effects.
3. If neither exists, store `scheduled=false` while still holding `w.mu`.
4. Re-check `hasRunnableWorkLocked()` under the same lock.
5. If no work exists, record `lastIdleUnixNano`, unlock, and return.
6. If work exists, unlock and attempt `tryActivate()` to regain ownership.

The race window remains explicit: a submitter can append to `w.inbox` after
`scheduled=false` is visible but before the advance goroutine returns. The
submitter will win `tryActivate()` and schedule another advance. If the advance
goroutine itself sees work in the same locked check, it can attempt to reactivate
and continue the loop. This preserves the existing scheduled CAS ownership
invariant.

### Keep `deactivate()` as the shared primitive

`deactivate()` is still useful for tests and any caller that is not already
holding `w.mu`. The implementation should delegate the lock-held logic to a new
helper, for example:

```go
func (w *channelWriter) deactivateLocked() bool {
	w.scheduled.Store(false)
	more := w.hasRunnableWorkLocked()
	if !more {
		w.lastIdleUnixNano.Store(time.Now().UnixNano())
	}
	return more
}
```

`deactivate()` becomes:

```go
func (w *channelWriter) deactivate() bool {
	w.mu.Lock()
	more := w.deactivateLocked()
	w.mu.Unlock()
	return more
}
```

The advance loops use `deactivateLocked()` directly so the no-work path avoids
one extra lock/unlock pair.

### Add conservative atomic fast-path signals only

An atomic fast path may be added only as a hint, not as a correctness source.
The safe first version is an atomic inbox length counter:

- Increment when `enqueue` appends to `w.inbox`.
- Reset or decrement when the advance goroutine drains the inbox.
- Use it only to avoid obviously empty work checks or to guide scheduling
  metrics/benchmarks.

This design does not require atomics to prove liveness. Lock-held state remains
the source of truth for append readiness, commit readiness, idle detection, and
writer reclamation. If implementation complexity exceeds the expected benefit,
the atomic hint can be skipped in the first patch without failing this design.

## Expected Behavior

- At most one goroutine still runs `advance` for a writer at a time.
- Accepted submissions are not dropped when they arrive during the deactivate
  race window.
- `Group.Stop` can still drain accepted work and stop scheduling post-commit
  work once the runtime context is canceled.
- Writer idle reclamation still observes `lastIdleUnixNano` only after inbox and
  state are both idle.
- Existing append ordering, commit ordering, and item-aligned futures are
  unchanged.

## Test Plan

- Unit-test the deactivate race window by arranging work to appear while the
  writer transitions `scheduled` to false and verifying it is either continued
  by the current advance loop or rescheduled by the submit path.
- Keep existing writer lifecycle tests that assert `deactivate()` reports
  pending inbox work.
- Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -count=1
GOWORK=off go test ./internalv2/runtime/channelappend/ -race \
  -run 'TestWriterAdvance|TestCommitEffect|TestChannelState|TestGroup|TestAdvancePostCommitReusesEffectWithoutBleed' -count=1
```

- Benchmark the post-commit hot path and the prior comparison set:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -run XXX_NONE \
  -bench 'BenchmarkSubmitLocalHotChannelPostCommit|BenchmarkSubmitLocalHotChannelBatch16|BenchmarkSubmitLocalManyChannelsParallel$' \
  -benchtime=200x -count=5
```

## Re-profile Gate

After implementation, re-run the three-node profile used to identify this
bottleneck. The desired outcome is:

- `channelWriter.mu` lock/unlock flat CPU drops materially from the current
  about 21%.
- `channelWriter.deactivate` cumulative share drops.
- `worker_task / append` ratio moves closer to append work, or remains as the
  next measured bottleneck for a later sticky-worker or batching design.

