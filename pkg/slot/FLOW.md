---
scope: subtree
summary: Implements Multi-Raft Slot metadata, atomic FSM commands, authoritative leader reads, snapshots, and distributed metadata proxies.
---

# Slot Metadata Flow

## Responsibility

`pkg/slot` is the distributed metadata layer. Physical Slots are independent
Raft groups that own logical hash-Slot partitions containing user, Channel,
subscriber, runtime, membership, plugin-binding, migration, and message-event
projection state.

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
  transport or routing table.
- Local reads are valid only for explicitly local contracts. A proxy must not
  answer a cluster-authoritative query from a convenient local replica.
- FSM command and RPC catalogs live in code and tests, not in this overview.

## Main Flows

1. The proxy derives logical and physical ownership, proposes versioned writes
   locally or through forwarding, and performs authoritative reads by following
   the reported leader and revalidating ownership at the handler.
2. A Multi-Raft worker persists Ready state, sends messages, batches normal
   entries, flushes before configuration changes, and atomically applies an
   ownership-validated FSM batch before persisting apply and completing futures.
3. Maintenance and migration controls use the same fenced worker/FSM path:
   snapshots and backup prove an applied boundary, while Channel migration
   advances task and runtime metadata together through guarded phases.

## Invariants and Failure Semantics

- Every command belongs to its physical Slot and an owned logical hash Slot.
  Multi-hash-Slot batches are allowed only by explicit command contracts and
  validate every embedded row.
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
- Recovery restores the persisted snapshot boundary then replays its committed
  suffix; a later applied marker must never skip replay.
- Ordinary and CMD membership progress is monotonic and UID-owned. Removed
  conversation table IDs stay reserved and must not be reused.

## Read First

- [Subtree boundary](BOUNDARY.md)
- [Multi-Raft API](multiraft/api.go)
- [Raft Slot worker](multiraft/slot.go)
- [FSM state machine](fsm/statemachine.go)
- [Distributed proxy](proxy/store.go)

## Update Triggers

Update this file when Slot/hash-Slot ownership changes, Raft Ready/apply order
changes, a command crosses ownership domains, authoritative read routing
changes, migration fence semantics change, or snapshot/recovery guarantees
change.
