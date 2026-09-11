---
scope: subtree
summary: Implements the reusable multi-reactor Channel log runtime, replication, persistence ports, transport, services, and bounded workers.
---

# Channel Runtime Flow

## Responsibility
`pkg/channel` is the reusable replicated Channel log runtime. It owns Channel
metadata fences, per-Channel ordering, leader append, durable-quorum commits, follower pull replication,
committed progress, retention, lifecycle, and synchronous reactor facades.

`machine` holds pure transitions, `reactor` owns state and scheduling,
`replication` holds protocol decisions, `service`
is the synchronous facade, `store` defines persistence, `transport` defines RPC
DTOs, and `worker` bounds blocking I/O.

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
   Borrowed public payloads are cloned at admission; an adapter-owned append
   may explicitly transfer immutable payloads that downstream state, quorum,
   and storage submissions share while copying record metadata.
   With `DurableQuorumLog`, leader activation first installs a recovered
   authority frontier and current-term barrier, then each caller append is one
   immutable exact quorum proposal rather than a transient worker batch.
2. Followers pull continuous records, apply and return ACK progress, and use a
   bounded checkpoint path; idle leaders and caught-up followers coordinate
   checkpointed stop before either runtime can be evicted.
   In durable-log mode, authority installation seeds non-voting learner catch-up
   from the quorum-proved frontier; fixed repair workers retain one-page progress.
   Tail growth preserves that cursor; new gap evidence and authority replacement fence it.
3. Committed reads expose only HW-covered records above the logical retention
   floor. Runtime probes distinguish a loaded Leader from completed quorum
   recovery, including when a durable write fence permits only reads. Optional
   physical trim runs later when all local and replica safety
   watermarks cover that boundary.

## Invariants and Failure Semantics

- Channel epoch, leader epoch, leader ID, write fence, generation, and worker op
  identity fence every relevant transition and completion.
- Durable quorum success requires local durability plus a distinct-voter quorum.
  Exact manifests and closed durable/already-durable/absent/conflict/unknown
  outcomes make ambiguous commits safely retryable after cancellation or
  restart; caller cancellation cannot revoke admitted durability. A definitive
  local conflict reaches durable command lookup without waiting for missing
  peers or retaining an impossible local pending proposal. A valid newer durable
  authority invalidates a resumed former leader and returns stale metadata;
  same-authority, missing, or malformed evidence remains a conflict.
- The node-owned replication runtime bounds local mutation batches, per-target
  exchange, recovery probes, and follower repair without per-Channel goroutines.
  Install preserves every observed suffix, proves compatible voter tails on one
  exact hash chain, and copies at most one bounded page before yielding for a fresh proof. Probe rounds
  consume arrived evidence plus the local result, then use a quorum without waiting
  for outstanding voters; convergence rechecks require all previously observed
  tails, while ordinary identity pages retain stable quorum supporters. A fresh
  quorum-identical prefix proof precedes append-only local repair; authority
  recovery completes only after the deterministic current-term barrier. Non-ISR learners receive quorum-proven exact proposals through the
  bounded repair workers without contributing votes; page progress survives
  retry deadlines. Recovery can complete under a transfer write fence for new-leader
  verification; business Commit stays blocked until the fence is cleared.
- LEO and HW are monotonic, HW never exceeds LEO, and committed reads expose
  only positive sequences covered by local HW and the logical retention floor.
  Committed-read results own their payload bytes beyond store-handle closure,
  so upper layers may transfer them without another deep copy.
- New proposals containing a nonzero message lifetime use exact proposal format 2,
  binding Expire in the digest; legacy format-1 hashes remain unchanged.
  Channel RPC 9 and quorum exchange 5 preserve lifetimes during replication and
  recovery. Deploy matched runtimes before emitting format 2; binary-only rollback
  and lossy older message encodings are unsupported.
- Same-Channel append ordering survives batching and worker concurrency.
  Quorum success requires replicated progress; desired replicas never imply it.
- Unloaded state is absence from the reactor map. Cold PullHint activation must
  resolve authoritative metadata and prove local replica membership before
  opening storage.
- Mailboxes, Channel count, append/worker queues, batching, recovery probes,
  maintenance turns, and result payloads are bounded.
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
