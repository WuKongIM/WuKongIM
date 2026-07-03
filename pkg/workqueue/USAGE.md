# pkg/workqueue Usage Guide

## Purpose

`pkg/workqueue` provides low-level bounded queue primitives for runtime code
that needs explicit admission control:

- `BoundedPool[T]` runs generic tasks through a bounded queue and worker pool.
- `ShardedMailbox[T]` routes items into hash shards and guarantees at most one
  active drain per shard.

The package does not own business retries, fencing, idempotency, protocol
session handling, durable ordering, or runtime-specific metric names. Keep those
rules in typed adapters above this package.

## When To Use

Use `BoundedPool[T]` when the work item can run independently once admitted:

- blocking storage calls;
- blocking RPC calls;
- best-effort side effects with caller-owned retry policy;
- task classes that already have a typed wrapper and result surface.

Use `ShardedMailbox[T]` when the runtime needs stable shard ownership:

- gateway-style per-session or per-connection shard mailboxes;
- channel-keyed scheduling where each shard is drained serially;
- batched foreground dispatch where adjacent shard-local items can coalesce.

Do not use `ShardedMailbox` as a per-business-key state machine. It guarantees
one drain per shard, not one drain per key. If a runtime needs per-channel or
per-UID single-flight semantics, keep that state machine above the mailbox.

## BoundedPool

### Basic Example

```go
type storeTask struct {
    ChannelKey string
    Records    []Record
}

pool, err := workqueue.NewBoundedPool[storeTask](workqueue.BoundedPoolConfig{
    Name:      "store-append",
    Workers:   16,
    QueueSize: 8192,
    Observer:  observer,
}, func(ctx context.Context, task storeTask) error {
    return store.Append(ctx, task.ChannelKey, task.Records)
})
if err != nil {
    return err
}
defer pool.Close(context.Background())

if err := pool.Submit(ctx, task); err != nil {
    return mapWorkqueueError(err)
}
```

### Admission

`Submit` is non-blocking:

- returns `nil` after the item enters the bounded queue;
- returns `ErrFull` when no queue slot is available;
- returns `ErrClosed` after `Close` begins;
- returns the caller context error if the context is already canceled.

`SubmitWait` waits for a queue slot until the caller context is canceled. Use it
only when bounded waiting is part of the caller contract. Hot foreground paths
usually want `Submit` so backpressure is immediate and predictable.

### Handler Context

The handler receives the pool runtime context, not the per-submit caller
context. Once an item has been admitted, caller cancellation no longer removes
that accepted item from the queue. Runtime-specific cancellation or waiter
cleanup should be handled by the typed adapter around the pool.

### Shutdown

`Close(ctx)` closes admission first, then waits for accepted work to drain until
`ctx` expires. If the close context expires, the pool runtime context is
canceled and the ants pool is released with the configured `ReleaseTimeout`.

Callers should make blocking dependencies honor their context promptly so close
does not depend on the caller timeout.

## ShardedMailbox

### Basic Example

```go
type sendTask struct {
    SessionID uint64
    Frame     frame.Frame
}

mailbox, err := workqueue.NewShardedMailbox[sendTask](workqueue.ShardedMailboxConfig{
    Name:              "gateway-send",
    Shards:            128,
    Workers:           128,
    QueueSizePerShard: 1024,
    BatchMaxItems:     512,
    BatchMaxWait:      time.Millisecond,
    Observer:          observer,
}, func(ctx context.Context, batch workqueue.MailboxBatch[sendTask]) error {
    for _, item := range batch.Items {
        dispatch(item)
    }
    return nil
})
if err != nil {
    return err
}
defer mailbox.Close(context.Background())

if err := mailbox.SubmitHash(ctx, sessionID, task); err != nil {
    return mapWorkqueueError(err)
}
```

### Shard Selection

Use `SubmitHash` when the caller already has a stable numeric hash or id. This
avoids hashing inside the package and makes shard selection explicit.

Use `Submit` when the caller has only a string key. It hashes the key with FNV-1a
and then routes to a shard.

### Batch Lifetime

`MailboxBatch.Items` is reused by the shard after the handler returns. A handler
must copy the slice or its items before retaining them beyond the handler call.

This keeps the normal mailbox hot path allocation-free in common cases. Treat
the batch as read-only unless the submitted item type is explicitly owned by the
handler.

### Ordering

Items are delivered in shard admission order. The mailbox does not provide
ordering across shards, and it does not isolate different keys that hash to the
same shard. Runtime code that needs stronger ordering should keep a typed
per-key state machine above the mailbox.

### Overload Behavior

When a shard drain cannot enter the ants executor because all workers are busy,
the mailbox retries scheduling after a short bounded delay. This path is for
bursty overload, not the normal steady-state path. Size `Workers` close to the
expected number of concurrently active shards when low latency matters.

## Error Mapping

`pkg/workqueue` returns generic infrastructure errors:

```go
func mapWorkqueueError(err error) error {
    switch {
    case errors.Is(err, workqueue.ErrFull):
        return myruntime.ErrBackpressured
    case errors.Is(err, workqueue.ErrClosed):
        return myruntime.ErrClosed
    default:
        return err
    }
}
```

Do not expose `workqueue.ErrFull` directly from business use cases unless that
package already uses `pkg/workqueue` errors as part of its public contract.

## Observability

Observers are optional and are called synchronously from hot paths. Keep them
concurrency-safe and non-blocking.

Use the low-level observation fields as input to package-specific metrics:

- `Kind`: `capacity`, `depth`, `admission`, `wait`, `task`, `worker`, `batch`;
- `Result`: `ok`, `full`, `closed`, `canceled`, `timeout`, `error`, `panic`;
- queue and worker gauges: depth, capacity, running, worker count, waiting;
- latency fields: queue wait and handler duration.

Runtime packages should translate these observations to their existing metric
names and stable labels. Avoid adding UID, channel id, session id, or other
high-cardinality values in observers.

## Migration Pattern

Wrap the generic primitive behind the existing typed runtime API:

```text
existing public API
  -> typed adapter validates request and maps errors
  -> workqueue primitive handles bounded queue / worker mechanics
  -> typed adapter completes futures, applies fences, or emits package metrics
```

Recommended migration order:

1. Replace internal pool mechanics while keeping the package public API stable.
2. Keep existing tests and benchmarks unchanged.
3. Add a benchmark comparison before and after migration.
4. Only then remove the old local queue/pool implementation.

For `pkg/channel/worker`, keep `Task`, `Result`, `Fence`, batching decisions,
and completion sink behavior in `pkg/channel/worker`; use `BoundedPool` only
for generic admission and execution mechanics.

For gateway SEND, keep frame cloning, session-close policy, and
`SendBatchHandler` fallback in gateway; use `ShardedMailbox` only for sharded
mailbox scheduling if the benchmark comparison stays neutral or better.

For `internalv2/runtime/channelappend`, keep `channelWriter` and `channelState`
ownership unchanged. At most replace shared worker-pool mechanics after the
typed writer tests and benchmarks have a stable baseline.

## Benchmark Commands

Run the package tests:

```sh
go test ./pkg/workqueue
go test -race -count=1 ./pkg/workqueue
```

Run the workqueue microbenchmarks:

```sh
go test -run '^$' -bench 'Benchmark(BoundedPool|ShardedMailbox)' -benchmem -benchtime=500ms -count=5 ./pkg/workqueue
```

Useful signals:

- full-reject benchmarks should stay allocation-free;
- hot shard mailbox benchmarks should stay allocation-free;
- overloaded scheduler benchmarks may allocate because they exercise retry
  scheduling under worker saturation;
- observer benchmarks should not add allocations on the bounded pool hot path.

