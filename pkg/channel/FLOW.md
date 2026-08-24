---
scope: subtree
summary: Implements the reusable multi-reactor Channel log runtime, replication, persistence ports, transport, services, and bounded workers.
---

# Channel Runtime Flow

## Responsibility

`pkg/channel` is the reusable replicated Channel log runtime. It owns Channel
metadata fences, per-Channel ordering, leader append, durable-quorum commits, follower pull replication,
committed progress, retention adoption, runtime lifecycle, and synchronous
facades over reactor futures.

The subtree is split by role: `machine` holds pure transitions, `reactor` owns
Channel state and scheduling, `replication` holds protocol decisions, `service`
is the public synchronous facade, `store` defines persistence, `transport`
defines RPC DTOs, and `worker` bounds blocking I/O.

## Boundaries

- Product permission, authority selection, subscriber fanout, and SENDACK
  orchestration stay above this package.
- `store/channel_adapter.go` is the only Channel file allowed to import message
  DB compatibility DTOs; other packages use Channel contracts.
  Storage-neutral proposal and entry identities live in the leaf `pkg/quorumlog`
  contract rather than depending on Channel or MessageDB implementations.
- Reactor goroutines decide state transitions but never perform blocking store
  or transport I/O. Typed workers execute that work and return fenced results.
- Recent-record caches, PullHint, batching, and benchmark controls are
  performance or observation mechanisms, never sources of durable truth.

## Main Flows

1. The service reserves a Channel key and submits an append; the reactor fences
   role, epochs, write admission, and capacity, while store workers durably
   append in order and local or quorum progress completes aligned futures.
   With `DurableQuorumLog`, leader activation first installs a recovered
   authority frontier and current-term barrier, then each caller append is one
   immutable exact quorum proposal rather than a transient worker batch.
2. Followers pull continuous records, apply and return ACK progress, and use a
   bounded checkpoint path; idle leaders and caught-up followers coordinate
   checkpointed stop before either runtime can be evicted.
3. Committed reads expose only HW-covered records above the logical retention
   floor; optional physical trim runs later when all local and replica safety
   watermarks cover that boundary.

## Invariants and Failure Semantics

- Channel epoch, leader epoch, leader ID, write fence, generation, and worker op
  identity fence every relevant transition and completion.
- Durable quorum success requires local durability plus a distinct-voter quorum.
  Exact manifests and closed durable/already-durable/absent/conflict/unknown
  outcomes make ambiguous commits safely retryable after cancellation or
  restart; caller cancellation cannot revoke admitted durability.
- The node-owned replication runtime bounds local mutation batches, per-target
  exchange, recovery probes, and follower repair without per-Channel goroutines.
  Install selects a quorum-identical hash-chain prefix, repairs bounded pages,
  and makes writes ready only after the deterministic current-term barrier.
- LEO and HW are monotonic, HW never exceeds LEO, and committed reads expose
  only positive sequences covered by local HW and the logical retention floor.
- Same-Channel append ordering survives batching and worker concurrency.
  Quorum success requires replicated progress; desired replicas never imply it.
- Unloaded state is absence from the reactor map. Cold PullHint activation must
  resolve authoritative metadata and prove local replica membership before
  opening storage.
- Mailboxes, Channel count, append queues, worker queues, batching windows,
  recovery probes, maintenance turns, and result payloads are bounded.
- Write fencing rejects new append admission without discarding already
  accepted work. Lifecycle eviction requires no pending work and current
  fenced checkpoint/replica evidence.

## Read First

- [Public contracts](channel.go)
- [Core types](types.go)
- [Service facade](service/service.go)
- [Pure Channel state](machine/channel.go)
- [Reactor ownership](reactor/FLOW.md)

## Update Triggers

Update this file when subtree ownership changes, append or quorum semantics
change, a new blocking-I/O path is added, metadata fencing changes, lifecycle
states change, or committed-read and retention guarantees change.
