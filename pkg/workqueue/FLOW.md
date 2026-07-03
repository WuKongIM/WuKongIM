# pkg/workqueue Flow

## Responsibility

`pkg/workqueue` provides reusable low-level queue and worker primitives. It owns
bounded admission, shard-local mailbox scheduling, worker execution, shutdown
waiting, and low-cardinality observations. It does not own business retries,
fencing, message ordering rules, protocol/session handling, or runtime-specific
metrics names.

## Primitives

| Type | Responsibility |
|------|----------------|
| `BoundedPool[T]` | Admit generic work into a bounded queue and execute it on an ants-backed worker pool. |
| `BoundedBatchPool[T]` | Admit generic work into a bounded queue, collect adjacent items into policy-driven batches, and execute those batches on an ants-backed worker pool. |
| `BoundedWorkerQueue[T]` | Admit generic work into a bounded queue and execute it on direct worker goroutines for very hot, low-latency paths. |
| `ShardedMailbox[T]` | Hash work into bounded shard queues and run at most one drain per shard at a time. |

Runtime packages should keep typed adapters around these primitives. For
example, channel can keep its `Task` / `Result` / `Fence` surface, gateway can
keep SEND frame cloning and session-close policy, and channelappend can keep its
channel-writer state machine.

## Bounded Pool Flow

```text
Submit / SubmitWait
  -> check closed and caller context
  -> reserve one bounded admission slot
  -> enqueue item
  -> dispatcher submits item to ants executor
  -> handler runs with the pool runtime context
  -> slot is released when ants accepts the task
```

`Submit` is non-blocking and returns `ErrFull` when no admission slot is
available. `SubmitWait` waits for admission until the caller context expires.
`Close` closes admission and drains already accepted work until its context
expires.

## Bounded Worker Queue Flow

```text
Submit / SubmitWait
  -> check caller context
  -> reserve one free queue slot
  -> re-check closed state under admission lock
  -> enqueue item
  -> direct worker receives the item
  -> slot is released when the worker receives the item
  -> handler runs with the queue runtime context
```

`BoundedWorkerQueue` is for hot paths where the ants executor handoff is too
expensive and fixed direct workers are enough. `Submit` is non-blocking and
returns `ErrFull` when no free queue slot is available. `SubmitWait` waits for
capacity, caller context expiry, or close. `Close` closes admission, drains
accepted work, and waits for workers until its context expires.

## Bounded Batch Pool Flow

```text
Submit
  -> check closed and caller context
  -> reserve one bounded admission slot
  -> enqueue item
  -> dispatcher starts from the first accepted item
  -> policy chooses MaxItems / MaxWait
  -> dispatcher drains immediately ready adjacent items
  -> dispatcher optionally waits for one adjacent peer, then drains ready peers
  -> dispatcher extends the batch with ready adjacent items before executor retries
  -> dispatcher submits the batch to the ants executor
  -> handler runs with the pool runtime context
  -> item slots are released when ants accepts the batch
```

Default `Close` closes admission, skips outstanding batch waits, drains all
accepted items into the executor, and waits for running handlers. With
`CancelAcceptedOnClose`, `Close` instead cancels accepted items that have not
entered the executor, calls the configured cancellation hook, and still waits
for executor-accepted or already running handlers without canceling their
runtime context unless the close context expires. With `CancelRunningOnClose`,
`Close` cancels the runtime context as soon as shutdown starts so running
handlers can exit through their own context-aware dependency calls.

## Sharded Mailbox Flow

```text
Submit(key, item)
  -> hash key to shard
  -> enqueue into the shard's bounded mailbox
  -> schedule a shard drain on the false-to-true scheduled edge
  -> drain collects an ordered shard-local batch
  -> handler processes the batch
  -> shard is unscheduled only after its queue is empty
```

The mailbox guarantees one active drain per shard. It does not guarantee
per-business-key isolation beyond stable hash placement; callers that need
per-key state machines should keep that logic above this package.
