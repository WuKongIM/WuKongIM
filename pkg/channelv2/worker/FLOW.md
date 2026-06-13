# pkg/channelv2/worker Flow

## Responsibility

`worker` owns ChannelV2 blocking effects. Reactors submit typed store and RPC
tasks through bounded admission queues, and workers return one fenced
`Result` per accepted task.

The package uses `github.com/panjf2000/ants/v2` only as an execution primitive.
Backpressure, queue depth, batch formation, shutdown completion, and observer
events are owned by this package.

## Pool Flow

```text
Submit(ctx, Task)
  -> validate pool open and caller context
  -> enqueue queuedTask into pool-owned bounded queue
  -> dispatcher receives queuedTask
  -> dispatcher collects eligible batch peers
  -> dispatcher submits the collected execution window to ants
  -> executor runs each grouped blocking store or transport call serially
  -> CompletionSink receives one Result per original task
```

`PoolConfig.QueueSize` is the admission queue capacity. `QueueDepth` reports
current admission occupancy, including queued work and dispatcher-held groups
not yet accepted by the executor. `PoolConfig.Workers` is the ants executor
capacity.

## Batching

RPC pull and pull-hint tasks can batch when they have the same task kind and
target node. Store append and store apply tasks can batch across different
channel keys when the store factory exposes the optional batch interface.
`PoolConfig.BatchMaxWait` overrides the task-class coalescing wait for that
pool; zero keeps the built-in default. This lets low-latency store-append
deployments shorten the extra worker-side wait without removing batching from
throughput-oriented configurations.

Batching changes only the blocking dependency call. A collected execution
window may split into multiple incompatible groups, but those groups still run
serially inside one ants task so the window preserves dispatcher order. Reactors
still observe one fenced completion per original task.

## Shutdown

`Close` cancels the pool context, closes admission to new submissions, stops
dispatcher draining, completes queued tasks that never reached the executor with
`ErrClosed`, waits for submitted execution windows, and releases the ants pool.

Running tasks receive the canceled pool context through `Task.Run` and exit
cooperatively when their dependency honors context cancellation.

## Observability

Worker queue, capacity, admission, wait, task, batch, inflight, and ants pool
usage observers retain their existing meanings. Inflight is the number of
running task groups, not the number of original tasks inside those groups.
Ants pool usage is reported with the worker pool name so app-level metrics can
publish it through the existing generic `wukongim_ants_pool_*` gauges and bench
`ANTS POOL USAGE` summaries.
