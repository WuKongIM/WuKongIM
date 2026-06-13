# channelappend conservative coalescing design

## Context

The previous channelappend work removed hot-loop effect copies and collapsed the
idle deactivate double-lock path. The next bottleneck is broader writer churn:

- `append_batch_avg_records` is about `1.15`, so most appends still carry one
  message.
- `worker_task / append` is about `4.5x`, which shows frequent scheduling
  cycles around tiny units of work.
- Once batching improves, the current lock-held `prepareBatch` call would make
  `channelWriter.mu` a larger serial section.

This design addresses the remaining local writer overhead conservatively. It
does not change cluster semantics: single-node deployment remains a single-node
cluster, and all channel authority behavior stays inside the existing local
authority writer.

## Goal

Increase same-channel append batch size and reduce advance scheduling churn
without adding more than a small bounded SENDACK queueing delay.

## Non-Goals

- Do not add a long sticky worker that parks while holding an advance-pool
  worker.
- Do not change durable append ordering, future alignment, or post-commit
  ordering.
- Do not expose external config keys in this first pass. Runtime `Options`
  fields are enough; if these knobs later become config-backed, update
  `wukongim.conf.example` with detailed English comments.
- Do not change router grouping or remote forwarding behavior.

## Design

### Move prepare out of the writer lock

`drainInboxLocked` currently drains inbox and calls `prepareBatch` while holding
`channelWriter.mu`. The implementation should split this into two phases:

1. Lock briefly and take the current inbox slice.
2. Prepare those submitted batches outside `w.mu`.
3. Re-lock and admit each prepared outcome into `channelState`.

The prepared outcome still validates against the original `AuthorityTarget`
inside `admitPreparedLocked`, so stale route protection and item-aligned future
completion remain unchanged. Moving prepare outside the lock lets concurrent
`enqueue` calls append to inbox while the current advance goroutine is doing
CPU-side validation, message-id allocation, idempotency checks, and timestamp
assignment.

### Add a conservative inbound coalescing window

Add runtime-only `Options` fields:

- `InboxCoalesceWindow time.Duration` with default `250 * time.Microsecond`.
- `InboxCoalesceMaxItems int` with default `16`.

Both fields need English comments. Values `<= 0` disable or default according
to the final implementation plan; the recommended first pass is to use defaults
when unset and allow tests to set zero explicitly only if a disabled mode is
needed.

Coalescing belongs to the already scheduled writer goroutine, not to
`SubmitLocal`. `SubmitLocal` continues to copy items, create a `Future`, append
one `submittedBatch` to the writer inbox, and return immediately after
admission.

When a writer starts an advance pass and finds a small inbox, it may wait up to
the configured window before draining, or stop earlier once the inbox reaches
`InboxCoalesceMaxItems` logical send items. The wait must never hold
`channelWriter.mu`; it should poll or use a short timer outside the lock. The
lock-held state remains the source of truth for draining and runnable checks.

The wait is intentionally tiny. Its purpose is to catch near-simultaneous
same-channel submissions and turn many one-record append requests into one
channel-aligned append batch. It is not a latency-hiding queue or a durable
group-commit mechanism.

### Let scheduling cost fall naturally

The coalescing window should reduce the number of `advance` invocations and
pool submissions because one scheduled writer pass can drain more inbox work.
This design does not introduce a separate sticky-worker mode. If a later profile
still shows high `worker_task / append`, sticky writer parking can be designed
as a separate change with its own worker-capacity and shutdown analysis.

## Expected Behavior

- `SubmitLocal` remains non-blocking after admission and does not wait for the
  coalescing window.
- A newly scheduled writer may add up to about `250µs` of queueing before
  prepare/append begins.
- Multiple same-channel single-item submissions arriving within that window can
  be prepared and appended together.
- Existing caller deadlines and cancellations still apply during prepare and
  future wait. A deadline that expires during the small window should produce
  the same terminal result path it would produce if it expired just before
  prepare.
- Writer stop/drain behavior remains bounded. `Group.Stop` cancels runtime
  context and waits for accepted work; the coalescing wait must observe runtime
  cancellation promptly.
- Writer idle reclamation still depends on inbox and `channelState` being idle.

## Test Plan

- Add a writer-level test proving prepare no longer blocks concurrent enqueue:
  use a controlled `MessageID` or idempotency hook that blocks in prepare, submit
  another batch while prepare is blocked, and assert the second enqueue can
  enter the inbox without waiting for the first prepare to finish.
- Add a group-level or writer-level test proving conservative coalescing merges
  several single-item same-channel submissions into fewer append calls, ideally
  one append request with multiple messages when submissions arrive inside the
  window.
- Add a cancellation/deadline regression test showing an item canceled during
  the coalescing wait completes through the existing canceled path and does not
  stall later items.
- Keep existing ordering tests:
  `TestWriterAdvanceAppendsInOrder`,
  `TestAdvancePostCommitReusesEffectWithoutBleed`, commit batching tests, and
  router alignment tests.
- Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -count=1
GOWORK=off go test ./internalv2/runtime/channelappend/ -race \
  -run 'TestWriterAdvance|TestCommitEffect|TestChannelState|TestGroup|TestAdvancePostCommitReusesEffectWithoutBleed|TestWriterDeactivateLocked|TestInboxCoalesce|TestPrepareOutsideWriterLock' -count=1
```

- Benchmark:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend/ -run XXX_NONE \
  -bench 'BenchmarkSubmitLocalHotChannelPostCommit|BenchmarkSubmitLocalHotChannelBatch16|BenchmarkSubmitLocalManyChannelsParallel$' \
  -benchtime=200x -count=5
```

## Profile Gate

After implementation, rerun the same three-node 10000 QPS scenario and collect
CPU profiles during the measurement window. The desired evidence is:

- `append_batch_avg_records` rises from about `1.15` toward at least `5` under
  bursty same-channel load.
- `worker_task / append` drops materially from about `4.5x`.
- `channelWriter.mu` lock flat CPU does not rise after prepare moves out of the
  lock.
- p99 remains acceptable with the conservative `250µs` default window.

