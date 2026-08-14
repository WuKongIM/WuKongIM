---
scope: package
summary: Owns routed local Channel append admission, ordered durable writes, item futures, and bounded post-commit delivery handoff.
---

# Channel Append Runtime Flow

## Responsibility

`internal/runtime/channelappend` routes SEND batches to the current Channel
append authority. On the local authority it validates and prepares items,
allocates message IDs, serializes per-Channel durable append, completes aligned
futures, and hands committed messages to bounded best-effort delivery and
side-effect runtimes.

Command-style `NoPersist` sends use the same authority and recipient machinery
but create no Channel log or membership state. Plain non-command `NoPersist`
sends terminate successfully before routing.

## Boundaries

- `Router` owns authority resolution, bounded local/remote grouping, and stale-
  route retry; only a resolved local target may call `SubmitLocal`.
- Product permission and Channel business policy remain in usecases. Concrete
  routing, durable storage, presence, and owner push are injected ports.
- The runtime owns scheduling and handoff state, not subscriber metadata or
  session mutation.
- Post-commit delivery, plugins, webhooks, and offline observation are best-
  effort and cannot change an already durable SENDACK result.

## Main Flows

1. The router performs side-effect-safe checks, derives canonical Channels,
   resolves authority, groups by target, and submits locally or forwards once
   per bounded lane.
2. The local shard creates one writer per Channel key; that writer prepares and
   orders items, performs fenced append, applies completions in sequence, and
   recovers only payload-hash-proven committed retries.
3. Fresh commits retain bounded delivery-handoff ownership until a terminal
   enqueue result, while Stop closes admission and drains all futures, append,
   realtime, reservation, handoff, and retry ownership before pool release.

## Invariants and Failure Semantics

- One writer state machine advances a Channel key at a time; same-Channel
  durable ordering must hold even when configured append concurrency exceeds
  one.
- Expected Channel and leader epochs fence every durable write. A canonical
  target mismatch is stale routing and creates no state.
- Accepted work is not canceled by later caller cancellation. A timed-out Stop
  bounds only that caller's wait and never discards admitted work.
- Per-item result order and cardinality are preserved across routing, append,
  retry, and remote forwarding.
- Backlog, worker pools, router concurrency, recipient pages, owner fanout, and
  post-commit handoff are all bounded. Saturation fails before append with a
  typed busy/backpressure result; acknowledged commits are never dropped.
- A post-commit completion must match both sequence and attempt. Stale
  completions cannot release another item's reservation or advance state.
- Persistent command messages use their command Channel; transient messages
  write neither Channel logs nor directory membership.
- Observability is aggregate and low-cardinality: never label Channel, UID,
  Slot, route, or authority identities.

## Read First

- [Runtime contracts](contracts.go)
- [Authority router](router.go)
- [Group lifecycle](group.go)
- [Writer state machine](writer.go)
- [Append state](state.go)

## Update Triggers

Update this file when authority routing, admission bounds, writer ordering,
idempotency recovery, append fencing, `NoPersist` behavior, post-commit
ownership, recipient planning, or graceful shutdown semantics change.
