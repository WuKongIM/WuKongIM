# pkg/channel/worker Flow

## Responsibility

`worker` owns Channel blocking effects. Reactors submit typed store and RPC
tasks through bounded admission queues, and workers return one fenced
`Result` per accepted task. Loaded-runtime authoritative metadata refresh uses
its own optional bounded pool. Unloaded authority resolution and the subsequent
store load use a second bounded `ColdActivation` pool, so cold bursts cannot
occupy hot store, metadata-refresh, or replication RPC workers.
Durable checkpoint maintenance uses a separate low-concurrency
`StoreCheckpoint` pool, so per-channel checkpoint fsyncs cannot occupy every
foreground follower-apply worker or hold checkpoint locks across the apply
queue.

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
  -> worker runs store groups serially
  -> worker runs different-target RPC groups independently within the configured worker limit
  -> CompletionSink receives one Result per original task
```

`PoolConfig.QueueSize` is the admission queue capacity. `QueueDepth` reports
current workqueue admission occupancy, including accepted work not yet entered
by the executor. `PoolConfig.Workers` is the workqueue ants executor capacity.
`Pools.MetaResolve` is created only when `Deps.MetaResolver` is configured;
otherwise metadata-resolve submission returns `ErrInvalidConfig`, depth is zero,
and observer installation and shutdown skip the absent pool safely.
`Pools.ColdActivation` is also optional and is created only when a resolver is
present and its configured worker and queue limits are positive.
`TaskColdMetaResolve` and `TaskColdStoreLoad` route exclusively to that pool;
expired store-load tasks check their task context before acquiring a store.
The reactor derives the default cold budget from its partition count, with
strict worker and queue caps, while explicit `PoolsConfig.ColdActivation`
values remain available to package integrators.

## Batching

RPC pull and pull-hint tasks can batch when they have the same task kind and
target node. Workqueue chooses the collection policy from the first accepted
task: a Pull-led window collects at most four adjacent tasks, while a
PullHint-led window collects at most two. This is not strict queue isolation;
later tasks of another RPC kind or target can enter the same window and are
partitioned into compatible subgroups. Different-target RPC subgroups may run
independently, but a pool-wide slot budget keeps actual Pull/PullHint transport
calls at or below the configured worker count. Both policies retain the
built-in 250-microsecond collection window. Store append, store apply, and checkpoint
tasks can batch across different channel keys when the store factory exposes
the corresponding optional batch interface. The message DB checkpoint batch
path commits monotonic HW updates through one grouped commit without taking
foreground append locks, instead of issuing one synchronous physical commit
per channel.
Store-apply results also return the checkpoint HW persisted atomically with the
follower records, allowing the reactor to suppress a redundant standalone
checkpoint task.
`TaskMetaResolve`, `TaskColdMetaResolve`, and `TaskColdStoreLoad` are never
batched. The first runs only in the loaded-runtime metadata resolver pool; the
two cold stages share the separate cold-activation admission budget.
Retention tasks remain single-channel store-apply-pool work: they first adopt a
logical boundary and only call physical trim when the reactor marked it safe.
`PoolConfig.BatchMaxWait` overrides the task-class coalescing wait for that
pool; zero keeps the built-in default. This lets low-latency store-append
deployments shorten the extra worker-side wait without removing batching from
throughput-oriented configurations.

Batching changes only the blocking dependency call. Workqueue chooses the
collection window from the first accepted task. The worker then splits that
window into compatible typed groups; incompatible or non-batchable items become
single-task groups. Store groups remain serial inside one workqueue handler.
RPC groups for different targets use the pool-wide bounded slot budget so one
slow peer does not block another peer collected in the same window. Reactors
still observe one fenced completion per original task.

## Shutdown

`Close` closes admission to new submissions, completes accepted items that have
not entered the executor with `ErrClosed`, cancels the worker runtime context
for running handlers, waits for them to exit, and releases the workqueue ants
pool. Running handlers must rely on their task-owned context or the worker
runtime context when blocking in dependency calls.

Store append, apply, read, lookup, checkpoint, and retention tasks own only a
temporary channel-store lease. They register lease cleanup immediately after a
successful acquisition and release it on success, dependency failure,
cancellation, or panic. Cleanup errors never replace the primary durable/read
result, which prevents a completed write from being retried solely because its
temporary handle failed to close. Store-load tasks keep the same panic-safe
cleanup until both initial and retention state are loaded; only a complete
`StoreLoadResult` transfers the lease to the result consumer.

Store-close tasks own an already detached runtime lease rather than acquiring a
temporary one. `Submit == nil` transfers that lease to the pool; any submission
error leaves it with the caller. Execution and queued-task cancellation call the
same exactly-once finalizer, so pool shutdown cannot drop an accepted close and
cannot close it again if execution races cancellation. A canceled queued close
still completes its reactor result with `ErrClosed`; cleanup errors do not
replace that shutdown result.

## Observability

Worker queue, capacity, admission, wait, task, batch, inflight, and ants pool
usage observers retain their existing meanings. Workqueue observations are
adapted to queue/capacity/admission/ants usage metrics. Typed wait, task, batch,
and inflight observations stay in this package because they need `TaskKind`,
`Fence`, and per-result errors. Inflight is the number of running task groups,
not the number of original tasks inside those groups.
