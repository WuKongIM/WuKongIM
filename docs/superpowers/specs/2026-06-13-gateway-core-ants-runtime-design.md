# Gateway Core Ants Runtime Design

Date: 2026-06-13

## Context

`pkg/gateway/core` currently owns three background execution paths:

- A shared idle monitor goroutine for read-idle deadlines.
- A bounded CONNECT authentication queue with fixed worker goroutines.
- A sharded SEND dispatch queue with one long-lived worker goroutine per
  shard.

The SEND path has important gateway semantics beyond generic goroutine
pooling:

- Admission must be non-blocking for transport event loops.
- Queue-full must map to `ErrAsyncDispatchQueueFull` and close the session with
  `CloseReasonAsyncDispatchQueueFull`.
- SEND work is sharded by session so one session's frames keep local ordering.
- SEND frames can be collected into micro-batches before handler dispatch.
- Queue depth, capacity, wait time, and batch observations are part of
  performance triage.

The auth path has a smaller but still explicit contract:

- CONNECT authentication and activation must run off the transport event loop.
- A pending CONNECT must reject additional inbound frames as protocol
  violations.
- Queue-full must map to `ErrAsyncAuthQueueFull` and
  `CloseReasonAsyncAuthQueueFull`.
- Auth queue wait and queue pressure observations must stay available.

The repository already depends on `github.com/panjf2000/ants/v2 v2.11.3`.
Other packages use ants as an execution primitive with non-blocking submission,
explicit package-owned backpressure, and bounded release.

## Goal

Refactor `pkg/gateway/core` to use ants-backed executors for asynchronous
gateway work while removing the old hand-written worker goroutine model.

The target shape is:

```text
Server
  -> asyncRuntime
       -> authExecutor: bounded CONNECT admission + ants execution
       -> sendExecutor: sharded SEND mailboxes + ants shard drain tasks
```

Ants should own executable goroutine capacity. Gateway core should continue to
own ordering, batching, backpressure, close reasons, and observations.

## Non-Goals

- Do not wrap the old `asyncAuthQueue` and `asyncDispatchQueue` with ants
  workers.
- Do not submit every SEND frame directly to ants.
- Do not rely on ants' internal waiting queue as the gateway business queue.
- Do not put the idle monitor into ants; it is a long-lived timer loop, not a
  short task.
- Do not change protocol decode, handler, authenticator, observer, or transport
  public contracts.
- Do not introduce deployment semantics outside the existing single-node
  cluster and multi-node cluster model.

## Considered Approaches

### Approach A: Direct Per-Task Ants Submit

Each CONNECT or SEND frame calls `pool.Invoke(task)` directly.

This is too blunt for SEND. Frames from the same session may execute
concurrently on different workers, batch formation becomes incidental, and
queue-full behavior becomes coupled to ants internals. It also makes existing
queue depth and wait observations less meaningful.

### Approach B: Keep Old Queues And Run Old Workers In Ants

This replaces `go s.runAsyncDispatchWorker(...)` and
`go s.runAsyncAuthWorker(...)` with ants submissions while preserving the old
queue and worker types.

This is not a real refactor. It keeps both models alive, adds lifecycle
complexity, and leaves most goroutine-management logic in `server.go`.

### Approach C: New Async Runtime With Ants Executors

Create a new package-local runtime that uses ants for execution and package-
owned structures for admission semantics.

This is the recommended approach. It makes ants the only execution primitive
for async auth and SEND dispatch while keeping gateway-specific behavior
explicit and testable.

## Recommended Architecture

Use Approach C.

`Server` should own a single `asyncRuntime` pointer instead of separate
`asyncAuth`, `asyncDispatch`, and worker goroutine accounting fields for those
paths. The runtime starts during `Server.Start` and stops during `Server.Stop`.

Suggested file layout:

- `pkg/gateway/core/async_runtime.go`: runtime construction, lifecycle, shared
  ants options, release timeout, and error mapping helpers.
- `pkg/gateway/core/async_auth.go`: auth task type, auth executor, bounded
  admission, queue observations, and wait observations.
- `pkg/gateway/core/async_send.go`: SEND task type, sharded mailboxes, shard
  scheduling, batch collection, queue observations, and wait observations.
- `pkg/gateway/core/server.go`: keep protocol/session orchestration and call
  into `asyncRuntime` from `handleAuthFrame` and `dispatchSendFrameAsync`.

The split is meant to reduce `server.go`, not create a compatibility layer.
Old queue and worker types should be moved or deleted as part of the refactor.

## Executor Options

Use package-owned ants pools, not the global ants pool.

Recommended options:

- `ants.WithNonblocking(true)` so transport event loops never park on ants.
- `ants.WithDisablePurge(true)` for stable hot-path latency.
- `ants.WithPanicHandler(...)` as a final guard that emits a bounded
  observation or warning.
- `ReleaseTimeout` on shutdown with a small bounded timeout.

Do not use ants waiting count as the primary gateway queue metric. Gateway
queue metrics should come from the explicit auth queue and SEND mailboxes.

## Auth Executor

`authExecutor` should use `ants.PoolWithFuncGeneric[asyncAuthTask]`.

Admission flow:

```text
CONNECT frame
  -> clone ConnectPacket
  -> enqueue into bounded auth queue
  -> schedule a lightweight auth drain if needed
  -> ants worker invokes runAuthTask
```

The queue exists to preserve gateway admission semantics, not old worker
compatibility. It should track:

- `capacity`
- `queued`
- `closed`
- `enqueuedAt` per task

The auth drain is a scheduler, not a fixed worker pool. It should move queued
tasks into `PoolWithFuncGeneric[asyncAuthTask]` while there are queued tasks and
ants capacity. If ants reports overload for an accepted task, the task remains
queued and the drain retries on the next enqueue or completion signal without
spinning.

When the queue is full, `handleAuthFrame` must clear `authPending`, emit
admission/queue observations, write a system-error CONNACK when possible, and
close with `CloseReasonAsyncAuthQueueFull`.

When a task starts, `observeAsyncAuthWait` should measure the duration from
`enqueuedAt` to worker start. `runAuthTask` should keep the existing
authenticate, activate, CONNACK, rollback, open callback, and failure-class
logic.

## SEND Executor

`sendExecutor` should not submit every SEND directly to ants. Instead, it owns
sharded mailboxes and submits one drain task per active shard.

Admission flow:

```text
SEND frame
  -> clone SendPacket using ownsDecodedFrames rule
  -> choose shard by session id
  -> enqueue into shard mailbox
  -> if shard was idle, schedule one ants drain task
```

Drain flow:

```text
ants worker drains shard
  -> collect micro-batch using AsyncSendBatchMaxWait/Records/Bytes
  -> observe queue depth and batch
  -> dispatch via SendBatchHandler when available
  -> fallback to per-frame OnFrame when batch handling is unavailable
  -> keep draining until shard mailbox is empty or runtime is closed
```

This preserves the current useful semantics:

- Frames from the same session go through the same shard and are dispatched in
  queue order.
- Different shards can execute concurrently under ants capacity.
- Queue-full still means explicit gateway backpressure, not an ants detail.
- Batch formation remains controlled by gateway session options.

If ants returns overload while scheduling a shard drain, the accepted task must
not be dropped. The shard should remain marked as needing scheduling and retry
when another enqueue or completion signal occurs, with a small non-spinning
retry path. Admission pressure is owned by the shard mailbox capacity.

## Runtime Options

The current `SessionOptions.AsyncSendDispatchWorkers` is a runtime executor
setting, not a per-session setting. For a clean refactor, move async execution
tuning under a gateway runtime option instead of keeping compatibility aliases.

Suggested public shape:

```go
type RuntimeOptions struct {
	// AsyncSendWorkers sets the ants-backed SEND executor capacity.
	AsyncSendWorkers int
	// AsyncSendQueueCapacity caps queued SEND tasks across all shards.
	AsyncSendQueueCapacity int
	// AsyncAuthWorkers sets the ants-backed CONNECT auth executor capacity.
	AsyncAuthWorkers int
	// AsyncAuthQueueCapacity caps queued CONNECT auth tasks.
	AsyncAuthQueueCapacity int
	// AsyncPoolReleaseTimeout bounds graceful ants pool shutdown.
	AsyncPoolReleaseTimeout time.Duration
}
```

`Options` should gain `Runtime RuntimeOptions`. The implementation should
normalize zero values to the current adaptive defaults:

- SEND workers: existing CPU-scaled min/max logic.
- SEND queue capacity: existing `asyncDispatchMaxBufferedTasks` default.
- Auth workers: existing CPU-scaled min/max logic.
- Auth queue capacity: existing `asyncAuthMaxBufferedTasks` default.
- Release timeout: short bounded default such as `100ms`.

Because this is a breaking cleanup inside the gateway package API, update tests
and call sites directly instead of keeping a deprecated session-field alias.
If external compatibility is required for a release, handle it as a separate
explicit compatibility decision rather than hiding it in this refactor.

## Shutdown Semantics

`Server.Stop` should stop accepting new sessions, stop listeners, close active
session states, then stop `asyncRuntime`.

`asyncRuntime.Stop` should:

1. Mark auth and SEND admission closed.
2. Wake any scheduled drains.
3. Convert queued auth tasks that have not started into ordinary queue-full or
   stopped handling only when a session is still open.
4. Let running auth and SEND tasks finish within the release timeout.
5. Release ants pools with `ReleaseTimeout`.

Repeated stop calls must be idempotent.

The idle monitor should remain separate and can continue to use the server
worker wait group until a dedicated lifecycle cleanup is planned.

## Observability

Keep existing observer contracts stable:

- `AsyncSendQueueEvent`
- `AsyncSendBatchEvent`
- `AsyncSendDispatchWaitEvent`
- `AsyncSendAdmissionEvent`
- `AsyncAuthQueueEvent`
- `AsyncAuthAdmissionEvent`
- `AsyncAuthWaitEvent`

Queue depth and capacity should report gateway-owned queues, not ants waiting
state. If executor-level observations are needed, add low-cardinality internal
events only after the core behavior is stable.

## Testing

Targeted tests:

- Auth queue full still writes failure CONNACK when possible and closes with
  `CloseReasonAsyncAuthQueueFull`.
- Auth wait observation is emitted when a CONNECT waits before execution.
- Pending auth still rejects frames received before authentication completes.
- SEND queue full still closes with `CloseReasonAsyncDispatchQueueFull`.
- SEND frames from the same session dispatch in order under high concurrency.
- Different SEND shards can run concurrently when ants capacity allows.
- Batch max wait, record count, and byte count behavior remains unchanged.
- Runtime stop rejects new submissions and releases ants pools.
- Panic in async task is recovered and observed without leaking queued depth.

Run:

```sh
go test ./pkg/gateway/core ./pkg/gateway/types ./pkg/gateway
```

Benchmark comparison:

```sh
go test -run '^$' -bench 'ServerAsyncSend|Gateway|Async' -benchmem ./pkg/gateway/core
```

Also keep the existing goroutine-count guard that verifies async SEND does not
spawn per frame.

## Rollout

1. Add tests that lock current auth and SEND semantics.
2. Introduce `RuntimeOptions` and normalize defaults.
3. Add `asyncRuntime`, `authExecutor`, and `sendExecutor`.
4. Wire `Server.Start`, `handleAuthFrame`, `dispatchSendFrameAsync`, and
   `Server.Stop` to the new runtime.
5. Delete old async queue and worker loop types.
6. Update tests that referenced old queue helpers to use new executor helpers.
7. Run targeted tests and benchmark before `go test ./...`.

## Open Decisions

- Whether `RuntimeOptions` should be exposed only in `pkg/gateway/types` or
  also surfaced through application config in the same implementation slice.
  If surfaced through config, update `wukongim.conf.example` and add detailed
  English comments as required by project rules.
- Whether SEND shard count should equal worker count or use a separate
  normalized value. Start with worker count to preserve current behavior, then
  split only if benchmarks show lock or queue contention.
