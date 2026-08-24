---
scope: package
summary: Provides bounded pools, batch pools, direct worker queues, sharded mailboxes, ownership, shutdown, and observations.
---

# Work Queue Flow

## Responsibility

This package owns reusable low-level admission, queueing, worker execution,
batch collection, shard scheduling, shutdown waiting, and low-cardinality
observations.
It does not own business retries, fencing, ordering, or protocol semantics.

## Boundaries

- Callers retain business retries, fencing, ordering, protocol/session rules,
  metric names, and typed task/result adapters.
- Every primitive uses a goroutine registry and fixed pool task; omitted
  ownership is explicitly attributed to `app/detached_workqueue`.
- Worker panics follow the owning catalog task's recover-or-repanic policy.

## Main Flows

1. `BoundedPool` and `BoundedWorkerQueue` reserve one admission slot, enqueue,
   release capacity at executor or direct-worker acceptance, and run with a
   pool context. Nonblocking submit returns `ErrFull`; waiting submit obeys context.
2. `BoundedBatchPool` selects collection policy from the first item, drains
   adjacent ready peers, optionally waits for one peer, extends before executor
   retry, then releases all item slots when the batch is accepted.
3. `ShardedMailbox` hashes a key, enqueues within a bounded shard, schedules
   only the false-to-true edge, and runs at most one ordered drain per shard.

## Invariants and Failure Semantics

- Admission closes before shutdown. Default close drains accepted work and
  waits; optional policies cancel queued items and/or runtime context for handlers.
- Cancellation hooks cover accepted work that never enters the executor.
- Queue depth, capacity, busy workers, goroutines, and rejected admissions are
  distinct bounded observations.
- Mailboxes guarantee one drain per shard, not per-business-key isolation;
  keyed state machines remain above this package.
- Runtime packages should wrap these generic primitives in typed APIs.

## Read First

- [Package usage](USAGE.md)
- [Bounded pool](bounded_pool.go)
- [Bounded batch pool](bounded_batch_pool.go)
- [Direct worker queue](bounded_worker_queue.go)
- [Sharded mailbox](sharded_mailbox.go)

## Update Triggers

Update this file when admission ownership, slot release, batch collection,
shutdown policies, mailbox scheduling, panic handling, or observations change.
