---
scope: subtree
summary: Guides node-local storage ownership, root lifecycle, message and metadata domains, snapshots, metrics, and engine isolation.
---

# Node Storage Flow

## Responsibility

`pkg/db` is the root of node-local durable storage. It owns shared errors and
options, the root `NodeStore`, message and metadata domain composition,
Pebble-neutral metrics, lifecycle fencing, and pinned engine snapshots.
It does not own product policy or expose Pebble-specific APIs to callers.

## Boundaries

- Pebble-specific implementation stays under `pkg/db/internal` and must not
  leak into callers.
- Durable schema changes follow `SCHEMA_COMPATIBILITY.md`.
- Child package details belong in their nearest `FLOW.md`; this subtree guide
  records only root ownership and cross-domain lifecycle.

## Main Flows

1. Build options and open one `NodeStore`; repeated `Messages` calls return the
   same canonical message registry while metadata has its independent domain.
2. Read physical message/meta metrics under the root lifecycle fence and the
   message operation guard, including bounded idempotency-filter counters and
   O(1) Pebble write-stall aggregates (count by reason, total, max, active),
   a WAL fsync latency summary, and slow-disk reports split by WAL and other
   files; a positive engine DiskSlowThreshold replaces Pebble's 5s default.
3. Close rejects and drains message acquisitions, closes its physical engine
   once, then closes metadata; concurrent callers join the same terminal close.

## Invariants and Failure Semantics

- Startup metadata snapshot streams use bounded pre-sized batches; the engine
  owns encoded allocation and durability while metadata owns the restart fence.
- Optional recovery seals bind certificates to the physical commit sequence;
  uncertified batches invalidate them, including mixed group-commit requests.
  A live epoch checked per certificate under the commit lock fences stale proofs;
  known disjoint Slot writes invalidate only their own certificate.
- Engine snapshots provide a pinned bounded-iterator view and must be closed;
  later writes and compactions may proceed while the view streams.
- Metrics must not race root shutdown or a direct message-domain close.
- Darwin uses 16 MiB `BytesPerSync` to avoid compaction range-sync full-file
  fsync behavior.
- The durable commit coordinator defaults to one shard and a 500-microsecond
  collection window; synchronous durability is unchanged. Metadata can opt into
  one isolated rebuild after a neighboring request rejects a group before any
  physical commit. Ambiguous physical commit errors are never retried.
- Each writable engine keeps one baseline compaction slot and may open three
  more only as L0 depth or compaction debt crosses configured pressure steps.

- Offline subscriber transfer preserves join incarnations and per-Slot System-1 sequence witnesses,
  including empty Slots. Imports reject conflicting identities; empty-target checks include sequences.
- Offline inspect/export/import/verify preserve all four message-edit tables. New
  JSONL edit datasets are omitted when empty; populated bundles require a
  matching CLI and import heads before their dependent projections.
- Message JSONL preserves optional publication metadata and validates its bounds
  and source timestamp before import. Native rows/digests remain unchanged;
  import budgets and both comparison modes include the complete metadata.
- Shared replay stays in the message domain, separate from Slot metadata. Binary
  backups use version 2 for full copies, 3 for retired baselines/suffixes and 4 for retained Will receipts.
  JSONL export rejects durable MQTT metadata/replay/Will/funding evidence before
  creating or overwriting output; complete MQTT JSONL state transfer is unsupported.

## Read First

- [Root store](db.go)
- [Store options](options.go)
- [Metrics](metrics.go)
- [Schema compatibility](SCHEMA_COMPATIBILITY.md)
- [Metadata domain](meta/FLOW.md)

## Update Triggers

Update this file when root composition, lifecycle locking, message registry,
metrics, snapshots, engine isolation, sync, or compaction tuning changes.
