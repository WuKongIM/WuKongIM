# ChannelV2 Worker Ants Executor Design

Date: 2026-06-12

## Context

`pkg/channelv2/worker` owns the blocking effect boundary used by ChannelV2
reactors. Reactors submit typed tasks for store append, store reads, follower
apply, checkpoints, store close, and replication RPCs. Worker completions are
routed back to reactors as fenced `worker.Result` values.

The current `Pool` implementation starts one long-lived goroutine per configured
worker. Each goroutine reads directly from the pool queue, opportunistically
coalesces eligible queued items into task groups, runs blocking store or
transport calls, observes queue/task/batch metrics, and sends one completion per
original task.

The repository already depends on `github.com/panjf2000/ants/v2 v2.11.3`.
Other packages use ants successfully, but `pkg/channelv2/worker` has stronger
business semantics than a plain goroutine pool:

- `QueueSize` is an explicit ChannelV2 admission limit.
- `QueueDepth` is used by runtime snapshots and bench triage.
- RPC pull and pull-hint tasks batch by target node.
- Store append and apply tasks batch across different channel keys.
- Accepted tasks must produce ordinary fenced completions.
- Pool shutdown must cancel cooperative blocking work.

The important design constraint is to introduce ants without layering a
compatibility shell around the old worker implementation. The new model should
make ants the execution primitive inside `Pool`, while keeping admission,
batching, observation, and completion semantics owned by this package.

## Goal

Refactor `pkg/channelv2/worker.Pool` into a clean internal architecture:

```text
Submit(ctx, Task)
  -> pool-owned bounded admission queue
  -> dispatcher collects batchable queued tasks
  -> ants executor runs one task group
  -> runQueuedGroup
  -> CompletionSink.Complete per original Task
```

This should reduce long-lived idle worker goroutines, keep the worker package's
bounded backpressure behavior explicit, and preserve existing reactor-facing
semantics.

## Non-Goals

- Do not change `worker.NewPool`, `worker.NewPools`, `Submit`, `Close`,
  `QueueDepth`, or task/result public contracts.
- Do not introduce `LegacyPool`, `AntsPool`, or another adapter layer that keeps
  the old worker loop alive beside the new executor.
- Do not rely on ants' internal waiting queue as the ChannelV2 business queue.
- Do not remove RPC or store batching.
- Do not add public configuration fields in this slice.
- Do not change single-node cluster or multi-node cluster deployment semantics.

## Considered Approaches

### Approach A: Submit Each Task Directly To Ants

Each `Pool.Submit` call would call `ants.Pool.Submit` directly.

This is simple, but it loses the current worker-owned admission queue and makes
`QueueSize`, `QueueDepth`, batching windows, and accepted-task completion
behavior depend on ants internals. It also makes RPC/store batch formation
awkward because each task may run independently as soon as ants accepts it.

### Approach B: Explicit Admission Queue Plus Ants Executor

`Pool` keeps a bounded admission queue and a dispatcher. The dispatcher forms
the same task groups as today, then submits each group to ants for execution.
Ants bounds and recycles execution goroutines; the worker package still owns
backpressure, batching, observation, shutdown, and completions.

This is the recommended approach.

### Approach C: One Ants Pool Per Task Class With Ants Waiting

Each worker category would use ants as both queue and executor. This reduces
local code but hides pressure in ants' internal waiting state, weakens current
runtime snapshots, and makes batching fragile.

This is not recommended for ChannelV2 worker semantics.

## Recommended Architecture

Use Approach B.

`PoolConfig.Workers` becomes ants executor capacity. `PoolConfig.QueueSize`
continues to be the capacity of the worker-owned admission queue. `QueueDepth`
continues to report only pending items in that explicit queue.

`Pool` should own:

- Configuration, dependencies, completion sink, and root context.
- A bounded `chan queuedTask` admission queue.
- One dispatcher goroutine that drains the queue, forms task groups, and submits
  groups to ants.
- An ants-backed executor with capacity equal to `Workers`.
- Wait groups for dispatcher shutdown and submitted group completion.
- Observer state that is safe to read while `SetQueueObserver` races with
  dispatcher and executor callbacks.

The public shape remains:

```go
type Pool struct {
    cfg   PoolConfig
    deps  Deps
    sink  CompletionSink
    queue chan queuedTask

    ctx    context.Context
    cancel context.CancelFunc
    stop   chan struct{}

    exec       *executor
    dispatchWG sync.WaitGroup
    taskWG     sync.WaitGroup
}
```

Implementation details can vary, but the core boundary should stay this clear:
dispatcher schedules groups, ants executes groups, and task/result semantics
remain in `worker`.

## Package Layout

Keep the package API small and split internals by responsibility:

- `pool.go`: exported configuration, `Pool`, construction, submission, close,
  queue depth, and naming.
- `dispatcher.go`: queue draining, batch window collection, group submission,
  and close-drain behavior.
- `executor.go`: thin ants wrapper with `submit`, `running`, `capacity`,
  `waiting`, and `close`.
- `batch.go`: group formation and batch execution helpers currently living in
  `pool.go`.
- `observe.go`: optional. Move observer methods only if it improves readability
  without creating excessive jumping.

Avoid a compatibility layer. The old worker-loop responsibilities move into
these files; they are not wrapped by a second abstraction.

## Executor

The executor should use the current ants version already in `go.mod`.

Pool options:

- `ants.WithNonblocking(true)` so dispatcher never parks inside ants' waiting
  queue.
- `ants.WithDisablePurge(true)` for stable hot-path latency.
- `ants.WithPanicHandler(...)` only as a final guard; correctness should rely on
  task-level recovery.

`ErrPoolOverload` means ants has no worker immediately available. Because the
task group has already passed ChannelV2 admission, the dispatcher should retry
with a short backoff until submission succeeds or the pool closes. This keeps
accepted-task completion semantics intact and avoids exposing transient executor
saturation as a new external error.

`ErrPoolClosed` maps to `ch.ErrClosed` completions for the affected group.

## Dispatch And Batching

The dispatcher preserves existing batching rules:

- RPC pull and pull-hint tasks batch by `TaskKind` and target `NodeID`.
- Store append and apply tasks batch across different `ChannelKey` values.
- Non-batchable tasks run as single-item groups.
- Batch collection keeps the existing max item and max wait constants unless a
  later performance pass proves different values.

Task-group execution should keep the current result behavior:

- Observe queue wait per original task.
- Observe execution duration per original task.
- Observe batch calls once per actual batch.
- Return one `Result` per original task.
- Complete each result through the configured `CompletionSink`.

The `InflightObserver` should continue to represent running task groups, not
raw ants workers and not original task count. This preserves the current metric
meaning and keeps group batching visible.

## Submission Semantics

`Submit(ctx, task)` stays non-blocking except for ordinary channel send cost:

- Closed pool: observe `closed`, return `ch.ErrClosed`.
- Canceled or expired caller context before admission: observe
  `canceled`/`timeout`, return the caller context error.
- Queue has capacity: enqueue a `queuedTask` with `enqueuedAt`, observe `ok`.
- Queue full: observe `full`, return `ch.ErrBackpressured`.

Ants availability must not affect `Submit` directly. The explicit queue is the
admission boundary.

## Close Semantics

`Close` should be idempotent.

Recommended order:

1. Cancel the pool root context.
2. Close `stop` so new `Submit` calls return `ch.ErrClosed`.
3. Let the dispatcher stop accepting new queue work.
4. Convert remaining queued tasks that have not reached ants into fenced
   `ch.ErrClosed` completions.
5. Wait for submitted task groups to finish.
6. Release the ants executor with a bounded timeout.

Completing leftover queued tasks is preferable to dropping them. Reactors
already have fenced completion routing, while silent drops force unrelated paths
to recover by timeout.

Tasks already running in ants should receive the canceled pool context through
the existing `Task.Run` path and exit cooperatively when their dependency honors
context cancellation.

## Error Handling

Keep existing external errors:

- Queue full remains `ch.ErrBackpressured`.
- Pool closed remains `ch.ErrClosed`.
- Invalid task shape remains `ch.ErrInvalidConfig`.
- Caller context cancellation and deadline errors are preserved.

For panics inside task-group execution, recover at the task-group boundary and
return a fenced result for each original task. Prefer a private formatted error
such as `fmt.Errorf("channelv2 worker panic: %v", recovered)` over pretending it
is `ch.ErrInvalidConfig`. The ants `PanicHandler` remains a last-resort guard
only.

## Observability

Preserve existing observer interfaces:

- `QueueObserver`
- `QueueCapacityObserver`
- `AdmissionObserver`
- `WaitObserver`
- `TaskObserver`
- `BatchObserver`
- `InflightObserver`

`SetWorkerWorkers(pool, workers)` should report ants capacity, since `Workers`
now configures executor capacity.

`SetWorkerQueueDepth(pool, depth)` continues to report the explicit admission
queue length.

Do not add a new public observer in the first slice. If triage later needs raw
ants occupancy, add a small optional interface for `Running`, `Cap`, and
`Waiting` after the migration is stable.

## Tests

Keep existing worker tests green. Add or strengthen tests for the new internal
semantics:

- Accepted task groups are not lost when ants returns transient overload.
- Closing the pool cancels running task contexts.
- Closing the pool completes not-yet-executed queued tasks with `ch.ErrClosed`.
- `QueueDepth` reflects only the admission queue.
- RPC pull and pull-hint batching still call batch transport once.
- Store append and apply batching still call store batch APIs once.
- Observer replacement does not race with dispatcher/executor observations.

Focused commands:

```text
go test ./pkg/channelv2/worker
go test -race ./pkg/channelv2/worker
go test ./pkg/channelv2/reactor ./pkg/channelv2/service ./pkg/channelv2
```

Full unit verification remains:

```text
go test ./...
```

## Documentation Updates

Create `pkg/channelv2/worker/FLOW.md` because the package will have a
non-trivial internal flow after the ants executor migration.

Update:

- `pkg/channelv2/FLOW.md`
- `pkg/channelv2/reactor/FLOW.md`

The docs should state that ChannelV2 workers use ants as an execution primitive,
while worker-owned admission queues remain the source of backpressure,
bench-runtime queue depth, and batch formation.

No configuration file changes are expected because this slice reuses existing
`Workers` and `QueueSize` fields.

## Acceptance Criteria

- `Pool` no longer starts one long-lived goroutine per configured worker.
- `Workers` controls ants executor capacity.
- `QueueSize` controls the worker-owned admission queue.
- Existing public worker APIs and reactor call sites remain unchanged.
- Existing RPC/store batching behavior remains intact.
- Accepted tasks produce completions, including closed completions for queued
  tasks that never reach ants during shutdown.
- Worker pressure metrics keep their current low-cardinality labels and
  meanings.
- The focused worker, reactor, service, and package tests pass.
