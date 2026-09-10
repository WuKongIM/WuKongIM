---
scope: package
summary: Owns Hash-Slot-scoped metadata tables, deterministic batches, membership directories, snapshots, restore, and cache invalidation.
---

# Metadata Storage Flow

## Responsibility

This package stores Channel-owned and UID-owned metadata on shared internal DB
primitives. It exposes stable `Shard` handles and must not import Pebble directly.
It does not own product business policy or expose engine-specific APIs.

## Boundaries

- Table specifications and the registry drive primary/index behavior, inspect,
  snapshot, backup, restore, and deletion.
- Multi-Hash-Slot batches lock shards in sorted order, commit once, then publish
  cache invalidations. A neighboring logical rejection may cause one isolated
  rebuild with fresh staged state while retaining the original locks.
- There is no conversation table; table IDs 6 and 7 remain reserved and must
  not be reused.

## Main Flows

1. Typed tables store Channel policy, subscribers, latest state, runtime and
   migration state, plus UID users, devices, memberships, plugins, and events.
2. Ordinary conversation directory scans UID-owned
   `user_channel_membership` by `(uid, activated_at desc, channel_id,
   channel_type)` and returns the complete cursor and `done` flag.
3. Snapshot and restore cover registered row, index, and system spans; restore
   installs isolated portable metadata, replays ordered Slot FSM commands, and
   verifies canonical digests.
4. Active migration tasks expose bounded ID/type cursor pages over the existing
   index, allowing fair executor scheduling without changing stored encodings.
5. Business Channel point reads use a fixed 8,192-entry LRU. Mutations and
   restore invalidate affected or complete cache state after durable commit.

## Invariants and Failure Semantics

- Event sequence pages scan a pinned native iterator and retain a bounded heap,
  so event-key order cannot truncate results before the sequence cursor.
- Offline event import installs one exact historical projection, its last event
  idempotency result and the full message cursor atomically. It never replays
  reducers; exact retries succeed and changed or advanced target state fails.
  Shared preflight validation rejects invalid projection/cursor combinations
  and projections that cannot fit the bounded native sequence page.
- Membership writes update obsolete/new activation index keys atomically;
  ordinary SEND never touches membership.
- Subscriber `source_version` fences stale cross-Slot writes. Rejoin resets
  visibility from one captured Channel tail; personal read/hide/activation
  preserves source version and rejects tombstones.
- Command-channel membership is a separate UID table with start/ACK sequence
  and no ordinary activation, read, or delete fields.
- Subscriber rows, count, and mutation version change atomically after UID sort
  and deduplication.
- Runtime metadata, Channel latest sequence, and event reducers stay monotonic
  and idempotent; create-only runtime batches never overwrite existing rows.
- The Channel read cache is capacity-bounded, independently locked from shard
  lookup, and exposes current entries and capacity through `MetricsSnapshot`.

- An imported `conversation_hidden_through_seq` is list-only state. Optional
  fixed-value tails preserve old rows; marked rows require matching binaries.
  Same-generation projections preserve it, while a new source generation replaces it.

## Read First

- [Metadata database](db.go)
- [Schema registry](schema.go)
- [Transaction helpers](tx_helpers.go)
- [Snapshots](snapshot.go)

## Update Triggers

Update this file when table ownership, batching, memberships, indexes, source
fences, runtime metadata, event state, snapshots, restore, or caches change.
