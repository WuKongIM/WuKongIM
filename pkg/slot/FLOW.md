---
scope: subtree
summary: Implements Multi-Raft Slot metadata, atomic FSM commands, authoritative leader reads, snapshots, and distributed metadata proxies.
---

# Slot Metadata Flow

## Responsibility

`pkg/slot` is the distributed metadata layer. Physical Slots are independent
Raft groups owning logical hash-Slot partitions for users, Channels, subscribers,
runtime, membership, plugin-binding, migration, and message-event projections.

`multiraft` owns Raft groups and futures, `fsm` decodes and atomically applies
metadata commands, and `proxy` routes writes to proposals and authoritative
reads to the current Slot leader. Durable rows live in `pkg/db/meta`.

## Boundaries

- Controller chooses Slot assignments; Channel stores message logs; usecases
  own business policy. This subtree persists and serves metadata under the
  supplied ownership and command contracts.
- Writes resolve `HashSlotForKey` then `SlotForKey`; authoritative reads execute
  on the actual Slot leader through registered typed RPC.
- Slot proxy handlers register through the promoted
  `pkg/cluster.Node.RegisterRPC` bridge; they do not construct a second cluster
  transport or routing table. Device identity reads use promoted service ID 87; the default runtime handler
  accepts only device lookups and does not expose user scans.
- Local reads are valid only for explicitly local contracts. A proxy must not
  answer a cluster-authoritative query from a convenient local replica.
- FSM command and RPC catalogs live in code and tests, not in this overview.

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
   Durable Slot storage owns snapshot payload bytes; the Raft memory view keeps
   only the matching index, term, and membership boundary and loads the payload
   from durable storage only when a lagging peer needs snapshot transfer.
   Fenced startup verifies pinned snapshots and optional FSM proofs against engine
   continuity, identity, ownership and exact Raft history. Valid proofs skip rewriting;
   otherwise bounded installation publishes its watermark before registration.
   Open reserves identity before mutation; close joins constructors. INFO
   `slot.recovery.progress` throttles same-stage counters to five seconds; suffix completion requires durable apply.
3. Maintenance and migration controls use the same fenced worker/FSM path:
   snapshots and backup prove an applied boundary, while Channel migration
   advances task and runtime metadata together through guarded phases.

## Invariants and Failure Semantics

- Every command belongs to its physical Slot and an owned logical hash Slot.
  Multi-hash-Slot batches are allowed only by explicit command contracts and
  validate every embedded row.
  Runtime-meta batches are canonical, identity-unique, and bounded to 64;
  person membership/ready batches are bounded to 128. Ordinary membership upsert batches also cap at 128 rows/256 KiB/64 KiB UID bytes, validate every owned shard atomically, preserve upsert source-version/rejoin semantics, and filter migration replay to the requested shard. The combined prepare command returns aligned create results but never publishes ready.
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
- Losing leadership fails pending proposal/configuration futures. Transport
  payload ownership, queues, apply batches, subscriber commands, scans,
  snapshots, and result payloads remain bounded.
- Recovery starts at a verified snapshot/certified FSM boundary and replays its
  committed suffix; a watermark alone cannot skip replay. Unknown or migration writes invalidate
  all proofs; disjoint FSM writes invalidate only their own. Compaction reanchors
  after durable snapshot publication. Snapshotless legacy
  recovery keeps watermark semantics without certifying unknown state.
- Ordinary and CMD membership progress is monotonic and UID-owned. Removed
  conversation table IDs stay reserved and must not be reused.
- Message edits atomically resolve CAS/idempotency and maintain latest-state indexes through the Slot FSM. Reads group at most 200 targets by physical Slot with eight managed workers and a fresh local-only safe ReadIndex plus durable-apply barrier per group, followed by one shared database snapshot for that group (noop fallback for embedding ports without ReadIndex). Replica capability activation is persisted in each channel head; later quorum writes reuse it unless the replica set changes. JSON RPC format, row counts and bytes are bounded; read DTOs omit default zero fields while preserving field names, aligned pages and legacy decoding; matched binaries remain a rollout requirement. Read assembly revalidates Slot mapping and authority with a dedicated retryable read-route cause, distinct from database/CAS conflicts. ReadIndex requires a durable current-term commit; unconfirmed/canceled reads remain counted up to 256 per Slot until confirmation or Raft reset.
- Send-permission facts use a separate node-scoped typed RPC: same-leader Slots share one envelope, but each Slot retains its own route fence, fresh required ReadIndex/apply barrier and snapshot. Maintenance admission spans the entire read. Local leaders use the same path without loopback. Requests cap at 4096 facts/1 MiB, four workers and sixty-four executing envelopes (at most 256 Slot workers). At most 1024 additional envelopes (16 MiB of undecoded bytes) may wait before decoding for up to 2 s or caller cancellation; overflow/timeout is typed busy, with no extra RPC retry. An idle Store reads one/two distinct facts synchronously under the same ownership bounds; concurrent Store callers collect for at most 1 ms before sealing; each cohort caps at 64 calls/4096 input facts, with at most 64 active cohorts, 1024 retained calls and 16 MiB conservative memory credits until joined work/result transfer. Identical facts deduplicate only inside the cohort; late arrivals require new barriers. Caller cancellation/deadlines remain independent; the last cancellation and Store close join the owned worker before dependencies close. Fixed metrics expose owned credits/calls/cohorts separately from receiver envelopes. One stale-route retry touches only failed groups. Policy/Channel-info commands preserve omitted flags and increment send-policy versions only on actual changes.

## Read First

- [Boundary](BOUNDARY.md), [API](multiraft/api.go), [Worker](multiraft/slot.go), [FSM](fsm/statemachine.go), [Proxy](proxy/store.go)

## Update Triggers

Update when Slot/hash-Slot ownership, Raft Ready/apply order, cross-domain commands,
authoritative read routing, migration fences or snapshot/recovery guarantees change.
