---
scope: package
summary: Owns routed local Channel append admission, ordered durable writes, item futures, and bounded post-commit delivery handoff.
---

# Channel Append Runtime Flow

## Responsibility

This package routes SEND batches to Channel authority, validates and prepares
local items, allocates IDs, serializes durable append, completes aligned futures,
and hands committed messages to bounded best-effort delivery and side effects.

`NoPersist` sends use the same authority and recipient machinery but create no
Channel log or membership state. Ordinary sends retain the source Channel;
command-style sends retain the command Channel. Both allocate a transient
message ID and enter online delivery with sequence zero, preserving the sender's
complete setting bitset, topic, and expiration just as durable envelopes do.

## Boundaries

- `Router` owns authority resolution, bounded local/remote grouping, and stale-
  route retry; only a resolved local target may call `SubmitLocal`. Remote
  forwarding attempts have an independent deadline. Ambiguous timeouts refresh
  the exact failed route and retry only persistent sends with an unchanged
  idempotency key; caller cancellation remains terminal.
- Product permission and Channel business policy remain in usecases. Concrete
  routing, durable storage, presence, and owner push are injected ports.
- The runtime owns scheduling and handoff state, not subscriber metadata or
  session mutation.
- Post-commit delivery, plugins, webhooks, and offline observation are best-
  effort and cannot change an already durable SENDACK result.

## Main Flows

1. The router performs side-effect-safe checks, derives canonical Channels,
   resolves authority, groups by target, and submits locally or forwards once
   per bounded lane. Trusted origin-only guards run immediately before local or
   remote submission and are stripped from accepted work; failures stay aligned.
2. The local shard creates one writer per Channel key; that writer prepares and
   orders items, performs fenced append, applies completions in sequence, and
   recovers only content-proven committed retries. MQTT content comparison
   includes publication metadata and exact body; clock-only retries retain the
   first committed record and never duplicate post-commit effects.
3. Fresh commits retain bounded delivery-handoff ownership until a terminal
   enqueue result. Subscriber pages reuse only page-local authority-planning
   scratch; each enqueued delivery plan owns its grouped recipient storage.
   Non-large snapshots load in 1,024-row pages and retain only actual recipients.
   Stop closes admission and drains all futures, append, realtime, reservation,
   handoff, and retry ownership before pool release.

## Invariants and Failure Semantics

- One writer state machine advances a Channel key at a time; same-Channel
  durable ordering must hold even when configured append concurrency exceeds
  one.
- Expected Channel and leader epochs fence every durable write. A canonical
  target mismatch is stale routing and creates no state.
- Invalid metadata and unkeyed Will templates fail before routing or IDs, including
  transient sends. Valid MQTT metadata permits empty bodies; native sends still require a body. Owned envelopes preserve original metadata.
- Temporary gofail builds can delay accepted Will appends before the native budget; ordinary controls are inert.
- Accepted work survives caller cancellation; a timed-out Stop bounds its wait and never discards admitted work.
- Per-item result order and cardinality are preserved across routing, append,
  retry, and remote forwarding.
- Backlog, worker pools, router concurrency, recipient pages, owner fanout, and
  post-commit handoff are all bounded. Saturation fails before append with a
  typed busy/backpressure result; acknowledged commits are never dropped.
- A post-commit completion must match both sequence and attempt. Stale
  completions cannot release another item's reservation or advance state.
  Every admitted effect publishes exactly one closed terminal result only after
  that match; panics, mixed batches, and stale completions cannot double-count
  or first publish a false success.
- Idempotency recovery observes only batch counts for recovered, unresolved,
  and lookup-error items. Recovered items are not errors; every fresh item that
  fails its bounded retry remains unresolved with its original aligned result.
- Command IDs use the configured per-instance suffix. Person validation and
  recipient derivation strip it before parsing UIDs; event and authority IDs
  retain it. Router and local preparation must produce the same canonical ID.
- Group commands page current subscribers from their source Channel while retaining
  their command Channel in delivery and persistence. They never reuse a snapshot
  fenced only by the command Channel metadata version.
- Persistent command messages use their command Channel; transient messages
  write neither Channel logs nor directory membership.
- Observability is aggregate and low-cardinality: never label Channel, UID,
  Slot, route, or authority identities.
  Pool pressure republishes after the final running count decrement so a
  terminal zero is observable without later traffic.
The router retains submission uncertainty monotonically across route retries and removes a later non-submission capability after an ambiguous earlier call. Private uncertainty wrappers do not change terminal error identity. Both batch error recovery paths still check positive committed idempotency for a funding refusal; a miss retains the exact refusal. Sibling retries cannot erase earlier unknown submission.

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
