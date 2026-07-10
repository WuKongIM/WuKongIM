# pkg/channel/worker Flow

## Responsibility

`worker` owns Channel blocking effects. Reactors submit typed store and RPC
tasks through bounded admission queues, and workers return one fenced
`Result` per accepted task. Authoritative channel metadata resolution uses its
own optional bounded pool so a slow resolver cannot occupy store or replication
RPC workers.

The package uses `pkg/workqueue.BoundedBatchPool` as its execution primitive.
Backpressure, queue depth, adjacent batch collection, close admission, and
executor entry are owned by workqueue. This package keeps the Channel typed
semantics: task/result envelopes, fence-preserving completions, task-kind batch
grouping, and worker-level observations.

## Pool Flow

```text
Submit(ctx, Task)
  -> validate pool open and caller context
  -> reject obviously full admission before stamping enqueue time
  -> submit queuedTask to workqueue bounded admission
  -> workqueue collects adjacent peers according to task-kind policy
  -> workqueue submits the collected execution window to ants
  -> worker groups compatible tasks inside the collected window
  -> worker runs each grouped blocking store or transport call serially
  -> CompletionSink receives one Result per original task
```

`PoolConfig.QueueSize` is the admission queue capacity. `QueueDepth` reports
current workqueue admission occupancy, including accepted work not yet entered
by the executor. `PoolConfig.Workers` is the workqueue ants executor capacity.
`Pools.MetaResolve` is created only when `Deps.MetaResolver` is configured;
otherwise metadata-resolve submission returns `ErrInvalidConfig`, depth is zero,
and observer installation and shutdown skip the absent pool safely.

## Batching

RPC pull and pull-hint tasks can batch when they have the same task kind and
target node. Store append and store apply tasks can batch across different
channel keys when the store factory exposes the optional batch interface.
`TaskMetaResolve` is never batched and runs only in the dedicated metadata
resolver pool.
Retention tasks are single-channel store-apply-pool work: they first adopt a
logical boundary and only call physical trim when the reactor marked it safe.
`PoolConfig.BatchMaxWait` overrides the task-class coalescing wait for that
pool; zero keeps the built-in default. This lets low-latency store-append
deployments shorten the extra worker-side wait without removing batching from
throughput-oriented configurations.

Batching changes only the blocking dependency call. Workqueue chooses the
collection window from the first accepted task. The worker then splits that
window into compatible typed groups; incompatible or non-batchable items become
single-task groups. Groups still run serially inside one workqueue handler call,
so reactors observe one fenced completion per original task.

## Shutdown

`Close` closes admission to new submissions, completes accepted items that have
not entered the executor with `ErrClosed`, cancels the worker runtime context
for running handlers, waits for them to exit, and releases the workqueue ants
pool. Running handlers must rely on their task-owned context or the worker
runtime context when blocking in dependency calls.

## Observability

Worker queue, capacity, admission, wait, task, batch, inflight, and ants pool
usage observers retain their existing meanings. Workqueue observations are
adapted to queue/capacity/admission/ants usage metrics. Typed wait, task, batch,
and inflight observations stay in this package because they need `TaskKind`,
`Fence`, and per-result errors. Inflight is the number of running task groups,
not the number of original tasks inside those groups.
