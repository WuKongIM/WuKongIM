---
scope: package
summary: Runs bounded typed Channel store and RPC tasks with fenced completions, class-aware batching, lease cleanup, and observations.
---

# Channel Worker Flow

## Responsibility

This package owns Channel blocking effects. Reactors submit typed tasks through
bounded pools and receive one fence-preserving result per accepted task.
It does not own reactor state machines, business retries, or dependency policy.

## Boundaries

- `pkg/workqueue.BoundedBatchPool` owns generic admission, collection, executor
  handoff, backpressure, and close mechanics; this package owns task kinds,
  grouping, fences, dependency calls, temporary leases, and typed observations.
- Hot store/RPC, loaded metadata refresh, cold authority/load, and durable
  checkpoint work use separate bounded pools so cold or fsync bursts cannot
  occupy all foreground workers.
- Reactors own retry and pre-admission pacing policy.

## Main Flows

1. Validate admission and context, reject obvious fullness before stamping
   enqueue time, collect adjacent work by the first task's policy, partition it
   into compatible typed groups, and emit one result per original task.
2. Pull and PullHint batch by task kind and target node, defaulting to 16 items
   and 250 microseconds; optional store interfaces batch append, apply, and
   checkpoint across channels while preserving per-task proof and results.
3. Close admission, resolve queued accepted tasks as closed when configured,
   cancel the runtime for active dependency calls, wait for handlers, and
   release the executor.

## Invariants and Failure Semantics

- Queue depth includes accepted work not yet in the executor; workers are the
  hard executor concurrency. Optional pools reject submission when absent.
- Cold resolve/load and loaded metadata resolution never batch. Retention stays
  single-Channel and trims only after a safe logical boundary.
- Compatible groups run serially inside one handler; rotating first-group
  priority avoids permanent target or task-kind tailing under skew.
- Temporary store leases are registered immediately and released on success,
  error, cancellation, or panic. Cleanup error never replaces the primary result.
- A detached store-close lease transfers only after successful submission and
  has one shared exactly-once finalizer across execution and cancellation.
- Inflight counts task groups, not original tasks. Pool, task kind, and result
  dimensions are bounded; dynamic Channel, Slot, and fence values are not labels.

## Read First

- [Pool](pool.go)
- [Pool composition](pools.go)
- [Task contracts](task.go)
- [Batch grouping](batch.go)
- [Dependency ports](deps.go)

## Update Triggers

Update this file when pool separation, admission, batching, task ownership,
lease cleanup, shutdown, pacing signals, or observation semantics change.
