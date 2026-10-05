---
scope: subtree
summary: Implements Multi-Raft Slot metadata, atomic FSM commands, authoritative leader reads, snapshots, and distributed metadata proxies.
---

# Slot Metadata Flow

## Responsibility
`pkg/slot` owns distributed metadata. Physical Slots are independent Raft groups
owning hash-Slot partitions for user, Channel, subscriber, runtime, membership,
plugin, migration, message projections, and MQTT session state.

`multiraft` owns Raft, `fsm` applies commands, `proxy` routes authoritative access; rows live in `pkg/db/meta`.

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
   Runtime-meta `get_fresh` is a separate bounded version-3 point read: fresh local quorum/apply barrier, derived mapping/leadership recheck, exact identity, and no legacy codec fallback or metadata creation.
2. A Multi-Raft worker groups contiguous queued read barriers into one fresh
   quorum round, retaining each caller's cancellation, pending bound and durable
   apply fence. Later arrivals and intervening controls require another round.
   It persists Ready state, sends messages, batches normal
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
4. Bounded/versioned MQTT commands persist Session children, sources and Will.
   Overflow and Session/Will lifecycle update state atomically; ACK preserves gaps;
   tombstones fence stale work. Rows/indexes/applied progress retain exact receipts. Temporary-copy gofail controls select opaque proposals before RawNode, observe persisted uncommitted entries, lose matched MsgApp batches, pause first/second-generation Started before FSM mutation, distinguish originating/applied successor claims, or delay applied Will CAS replies; ordinary proposals stay unchanged.
   Command 75 routes bounded ended-Session child reclamation and its durable completion witness; command 76 resumes a 64-row historical index build, and read kind 23 rejects uncertified coverage. Isolation/scheduling remain caller work. Command 72 preserves optional Will preparation phases/frozen bodies within 320 KiB. Authentication, owner isolation and publication execution remain caller work.
   The distributed facade hashes a versioned namespace/ClientID tuple for Session
   children; source bindings retain ordinary Channel-ID/UID routing. RPC 106 uses
   a fresh local ReadIndex/apply barrier then one pinned primary/index snapshot,
   revalidating routing/authority before return. Recovery pages select a logical
   hash Slot; writes require exact committed results without result-less fallback.
   Read kind 16 discovers active Channel sources; kind 17 includes retained tombstones for replay cleanup.
   Both use bounded encoded-order cursors and unchanged older JSON; old peers reject kind 17, and discovery authorizes no GC.
   Matched nodes use command 82 operation 4 for qualified charges and read kind 18 for pinned Session/cursor/head.
   Kind 19 pins Channel flags/member/incarnation; kind 20 pages stable UID directory keys.
   Command 74 and kind 21 persist/read person admission progress with runtime-incarnation fencing. Kind 22 routes by Channel ID and pins runtime/retirement together. Command 71 preserves optional UID drain progress across apply/snapshot/replay. Matched peers are required; absent optional fields preserve older JSON.

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
  advance monotonically; physical deletion retains a floor for explicit recreation and retires person-directory work atomically. Cleared fences retain their generation marker, and no
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
- Send-permission facts use a separate node-scoped typed RPC: same-leader Slots share one envelope, but each Slot retains its own route fence, fresh required ReadIndex/apply barrier and snapshot. Maintenance admission spans the entire read. Local leaders use the same path without loopback. Requests cap at 4096 facts/1 MiB, four workers and 128 executing envelopes (at most 512 Slot workers). At most 1024 additional envelopes (16 MiB of undecoded bytes) may wait before decoding for up to 2 s or caller cancellation; overflow/timeout is typed busy, with no extra RPC retry. One stale-route retry touches only failed groups. Policy/Channel-info commands preserve omitted flags and increment send-policy versions only on actual changes.

Command 79 adjusts the exact node MQTT storage escrow using revision/bytes CAS, complete Controller roster and cluster limit. Grants remain at zero to prevent ABA. Mismatched limits, stale roster revisions, incomplete startup debt and growth over the cluster sum fail closed. This allocation does not certify Channel content.

## Read First

- [Boundary](BOUNDARY.md), [Multi-Raft API](multiraft/api.go), [Raft worker](multiraft/slot.go), [FSM](fsm/statemachine.go), [Proxy](proxy/store.go)

## Update Triggers
Update when Slot/hash-Slot ownership, Raft Ready/apply ordering, command ownership, authoritative reads, migration fences, or snapshot/recovery guarantees change.
