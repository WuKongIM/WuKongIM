---
scope: subtree
summary: Implements the reusable multi-reactor Channel log runtime, replication, persistence ports, transport, services, and bounded workers.
---

# Channel Runtime Flow

## Responsibility
`pkg/channel` is the reusable replicated Channel log runtime. It owns Channel
metadata fences, per-Channel ordering, leader append, durable-quorum commits, follower pull replication,
committed progress, retention, lifecycle, and synchronous reactor facades.

`machine` holds pure transitions; `reactor` owns scheduling; `replication` owns protocol decisions.
`service` is the facade, `store` defines persistence, `transport` defines RPC, and `worker` bounds blocking I/O.

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
   Borrowed payload and publication-metadata bytes are cloned at admission; an adapter-owned append
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
  On-demand committed replica refresh replays the installed sequencer's tail
  through existing repair workers, including under an unchanged write fence. Exact
  authority is required; caller HW and scheduling supply no durability receipt.
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
   The optional persisted-frontier port reads LEO without an unused checkpoint;
   committed reads keep the full Load contract and quorum boundary.
- Nonzero lifetimes require exact proposal format 2; publication metadata
  requires format 3 and message record codec 2. Native hashes remain unchanged.
  Channel RPC 11 and quorum exchange 6 preserve bounded, validated metadata;
  all content budgets include it. Older lossy encodings fail explicitly.
  Exchange 6 requires matched replicas even before MQTT activation. Binary-only
  rollback after new-format writes is unsupported.
- Explicit MQTT source activation uses one format-4 control with a separate hash
  domain. Replica stores must advertise atomic pending protection/materialization;
  unsupported factories reject append and recovery. Quorum, restart and learner
  repair carry the exact control. The first activation survives repeated controls;
  reactor admission orders it with business appends. The optional source facade
  confirms captured HW through checkpoint workers and rechecks current fences;
  existing protection avoids another control. Subscription projection and
  shared-copy transfer remain separate; this receipt is not SUBACK authority.
  Format-5 journals retain full-content checkpoints; exact retry, recovery and learner transfer preserve control intent.
  Unsupported stores reject it; neither the payload nor its journal authorizes GC.
  Explicit format-6 retirement uses the sequencer with closed retry intent and journal-capability checks.
  It preserves whole-anchor decisions through replica recovery; product consumer admission remains separate.
  The optional retirement store port materializes verified baselines and bounded cleanup, preserving suffix repair/readiness after body removal.
  The optional retirement selector verifies historical whole anchors below a captured consumer floor in bounded reverse pages.
  Typed anchor admission shares the durable sequencer, checks exact installed
  membership and chains the latest accepted prefix. Stable source/Through commands
  reuse committed proofs after restart or original trim; pending retries keep the
  original row. Anchor-only tails stay idle. The service now reserves the append
  queue and uses typed workers; control completion advances durable progress even
  after observer cancellation, without inserting request identities into caches.
  Cluster entry adds fresh Slot routing; source-release admission remains separate.
  Read-only planning pins HW and the exact write fence through checkpoint workers;
  next ranges use accepted progress, skip idle control tails and ignore copy-ahead.
  The optional store repair port exports/imports complete bounded anchor intervals;
  each side verifies its own committed journal, with no sender-supplied expected
  digest. Store planning selects from durable coverage and at most 64 journals,
  validates continuation hints and completes only an exact target. Cluster recovery
  steps route to that target, try at most four donors with separate deadlines and
  return scan/import/retry/completion under fresh metadata; explicit retirement first applies committed baselines with bounded cleanup.
  Import keeps the pre-import plan; the next read verifies completion. Active migration
  probes bind coverage to captured HW; explicit source release independently verifies committed/local proofs through an optional store port.
  Replay preparation uses the same recovered leader/route admission and bounded
  checkpoint workers. It captures HW, verifies protection and returns owned
  local content after rechecking fences; it cannot release the original source.
- Same-Channel append ordering survives batching and worker concurrency.
  Quorum success requires replicated progress; desired replicas never imply it.
- Optional server Will lookup preserves its separate storage identity domain;
  its durable candidate still requires current committed visibility proof.
- Unloaded state is absence from the reactor map. Cold PullHint activation must
  resolve authoritative metadata and prove local replica membership before
  opening storage.
- Mailboxes, Channel count, append/worker queues, batching, recovery probes,
  maintenance turns, and result payloads are bounded.
- Write fencing rejects new append admission without discarding already
  accepted work. Lifecycle eviction requires no pending work and current
  fenced checkpoint/replica evidence.

- Message version and update time are transient read overlays only. Immutable stored/replicated message encodings do not include edit state; raw log/backup reads remain original records.

## Read First

- [Public contracts](channel.go)
- [Core types](types.go)
- [Service facade](service/service.go)
- [Pure Channel state](machine/channel.go)
- [Reactor ownership](reactor/FLOW.md)

## Update Triggers

Update when subtree ownership, append/quorum semantics, blocking-I/O paths, metadata fencing or lifecycle
states change, or committed-read and retention guarantees change.
