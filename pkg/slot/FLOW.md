---
scope: subtree
summary: Implements Multi-Raft Slot metadata, atomic FSM commands, authoritative leader reads, snapshots, and distributed metadata proxies.
---

# Slot Metadata Flow

## Responsibility

`pkg/slot` owns distributed metadata. Physical Slots are independent Raft groups
owning hash-Slot partitions for user, Channel, subscriber, runtime, membership,
plugin, migration, message projections, and MQTT session state.

`multiraft` owns Raft groups/futures; `fsm` atomically applies metadata commands;
`proxy` routes proposals and authoritative reads. Rows live in `pkg/db/meta`.

## Boundaries

- Controller chooses Slot assignments, Channel stores logs, and usecases own
  business policy. This subtree serves metadata under supplied command contracts.
- Writes resolve `HashSlotForKey` then `SlotForKey`; authoritative reads execute
  on the actual Slot leader through registered typed RPC.
- Slot proxy handlers register through the promoted
  `pkg/cluster.Node.RegisterRPC` bridge; they do not construct a second cluster
  transport or routing table. Device identity reads use promoted service ID 87; the default runtime handler
  accepts only device lookups and does not expose user scans.
- Local reads are valid only for explicitly local contracts. A proxy must not
  answer a cluster-authoritative query from a convenient local replica.
- Code/tests own the FSM command and RPC catalogs.

## Main Flows

1. The proxy derives ownership, proposes versioned writes locally or through
   forwarding, and follows the leader for authoritative reads. Person-directory
   commands prepare bounded UID membership/runtime metadata before publishing
   ready only after every prepare group succeeds.
   Compound conversation metadata reads share lifecycle/runtime ownership checks
   and four workers; response codec 2 carries full runtime fences. Legacy permission
   reads keep codec 1. Missing compound fields and changed ownership fail closed.
   Runtime-metadata read batches accept at most 4,096 keys, group them by
   physical Slot, use at most four supervised workers, and preserve item-scoped
   missing or Slot failures. Exact ordinary-membership batches accept at most
   200 keys for one UID and use one authoritative membership RPC; found rows
   are identity-unique, missing keys remain absent, and read errors fail the batch.
2. A Multi-Raft worker persists Ready state, sends messages, batches normal
   entries, flushes before configuration changes, and atomically applies an
   ownership-validated FSM batch before persisting apply and completing futures.
   Durable storage owns snapshot bytes; Raft memory keeps the index, term and
   membership boundary, loading payload only for a lagging peer's snapshot transfer.
3. Maintenance and migration controls use the same fenced worker/FSM path:
   snapshots and backup prove an applied boundary, while Channel migration
   advances task and runtime metadata together through guarded phases.
4. Bounded/versioned MQTT commands persist Session children, sources and Will.
   Overflow and Session/Will lifecycle update state atomically; ACK preserves gaps;
   tombstones fence stale work. Rows/indexes/applied progress retain exact receipts.
   Authentication, owner isolation and publication execution remain caller work.
   The distributed facade hashes a versioned namespace/ClientID tuple for Session
   children; source bindings retain ordinary Channel-ID/UID routing. RPC 91 uses
   a fresh local ReadIndex/apply barrier then one pinned primary/index snapshot,
   revalidating routing/authority before return. Recovery pages select a logical
   hash Slot; writes require exact committed results without result-less fallback.

## Invariants and Failure Semantics

- Every command belongs to its physical Slot and an owned logical hash Slot.
  Multi-hash-Slot batches are allowed only by explicit command contracts and
  validate every embedded row.
  Runtime-meta batches are canonical, identity-unique, and bounded to 64;
  person membership/ready batches are bounded to 128. The combined prepare
  command returns aligned create results but never publishes ready.
- Entity routing keys are stable: UID-owned rows use UID; Channel-owned rows use
  Channel identity. Caller-supplied Slot IDs never override derived ownership.
- FSM batches are atomic. Expected conditional conflicts and migration races
  return deterministic results such as stale metadata; unexpected apply
  failures do not expose a partially committed batch.
- Runtime metadata epochs, route generation, retention, and write-fence version
  advance monotonically. Cleared fences retain their generation marker, and no
  task may overwrite a foreign fence.
- Migration cutover requires task, epoch, leader, fence, drain, replica, ISR,
  and phase proof from the same authoritative state. Irreversible commit or
  promotion cannot later be labeled aborted.
- Losing leadership fails pending futures. Transport ownership, queues, apply
  batches, subscriber commands, scans, snapshots and results remain bounded.
- Recovery restores the persisted snapshot boundary then replays its committed
  suffix; a later applied marker must never skip replay.
- Ordinary and CMD membership progress is monotonic and UID-owned. Removed
  conversation table IDs stay reserved and must not be reused.

- Message edits atomically resolve CAS/idempotency and maintain latest-state indexes through the Slot FSM. Reads group at most 200 targets by physical Slot with eight managed workers and a fresh local-only safe ReadIndex plus durable-apply barrier per group, followed by one shared database snapshot for that group (noop fallback for embedding ports without ReadIndex). Replica capability activation is persisted in each channel head; later quorum writes reuse it unless the replica set changes. JSON RPC format, row counts and bytes are bounded; read DTOs omit default zero fields while preserving field names, aligned pages and legacy decoding; matched binaries remain a rollout requirement. Read assembly revalidates Slot mapping and authority with a dedicated retryable read-route cause, distinct from database/CAS conflicts. ReadIndex requires a durable current-term commit; unconfirmed/canceled reads remain counted up to 256 per Slot until confirmation or Raft reset.

## Read First

- [Subtree boundary](BOUNDARY.md)
- [Multi-Raft API](multiraft/api.go), [Raft Slot worker](multiraft/slot.go)
- [FSM state machine](fsm/statemachine.go), [Distributed proxy](proxy/store.go)

## Update Triggers

Update when Slot/hash-Slot ownership, Raft Ready/apply ordering, command ownership,
authoritative reads, migration fences, or snapshot/recovery guarantees change.
