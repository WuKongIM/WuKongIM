---
scope: package
summary: Executes canonical online delivery plans with bounded Channel ordering, owner routing, retries, and RECVACK state.
---

# Online Delivery Runtime Flow

## Responsibility

This package is the sole executor of canonical recipient delivery plans and
owner-local RECVACK state. Channel append is the sole plan producer.
It does not select subscribers, append messages, or build gateway packets.

## Boundaries

- The runtime depends on narrow plan, presence, owner-push, local-write,
  offline, ACK, and observation ports; it does not import app, gateway,
  concrete cluster runtimes, Prometheus, or packet builders.
- Exported version-one owner-push DTOs remain wire compatibility types;
  canonical logic uses `internal/contracts/onlinedelivery`.
- Durable append, subscriber selection, and webhook delivery live elsewhere.

## Main Flows

1. Admission validates an immutable plan and places it on a bounded stable
   Channel shard; one shard drains FIFO, preserving per-Channel message order.
2. A worker resolves aligned presence groups, emits durable-only offline
   batches, groups online routes by owner, and performs bounded owner pushes;
   retries contain only exact retryable routes.
3. Owner-local push fences node, UID, session, and owner, reserves ACK tokens,
   writes through the local adapter, and finishes or rolls back only the
   matching attempt.

## Invariants and Failure Semantics

- Queue capacity is node-wide; worker count is both maximum plan concurrency
  and the stable Channel shard count.
- A failed presence group does not discard successful sibling groups.
- Transient plans never create offline effects.
- Duplicate recipient rows intentionally produce duplicate writes and retain
  independent ACK attempt state.
- RECVACK and session-close remove only matching owner-local identities;
  activity-throttled expiry avoids full tracker scans.
- `Stop` closes admission, waits for enqueuers, and drains every accepted plan
  within context, resets transient ACK state, and leaves a successful stop
  restartable.
- `Quiesce` is the terminal-evidence variant: it drains admission, workers,
  owner pushes, and then pending ACK bindings without resetting them. A caller
  timeout stops only that wait; later calls join the same detached drain.
  Quiesce does not prove transport flush or remote acknowledgement.
- Identity samples never become metric labels; observation callbacks are
  bounded and aggregate tracker work.

## Read First

- [Runtime](runtime.go)
- [Runtime ports](runtime_ports.go)
- [Plan queue](plan_queue.go)
- [ACK tracker](ack_tracker.go)
- [Observability](observability.go)

## Update Triggers

Update this file when plan ownership, admission, Channel ordering, presence or
owner grouping, retry classification, ACK state, shutdown, or wire DTOs change.
