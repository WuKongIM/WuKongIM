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
| `ShardedMailbox[T]` | Hash work into bounded shard queues and run at most one drain per shard at a time. |

Runtime packages should keep typed adapters around these primitives. For
example, channelv2 can keep its `Task` / `Result` / `Fence` surface, gateway can
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

