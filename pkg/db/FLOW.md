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
   message operation guard, including bounded idempotency-filter counters.
3. Close rejects and drains message acquisitions, closes its physical engine
   once, then closes metadata; concurrent callers join the same terminal close.

## Invariants and Failure Semantics

- Engine snapshots provide a pinned bounded-iterator view and must be closed;
  later writes and compactions may proceed while the view streams.
- Metrics must not race root shutdown or a direct message-domain close.
- Darwin uses 16 MiB `BytesPerSync` to avoid compaction range-sync full-file
  fsync behavior.
- The durable commit coordinator defaults to one shard and a 500-microsecond
  collection window; synchronous durability is unchanged.
- Each writable engine keeps one baseline compaction slot and may open three
  more only as L0 depth or compaction debt crosses configured pressure steps.

## Read First

- [Root store](db.go)
- [Store options](options.go)
- [Metrics](metrics.go)
- [Schema compatibility](SCHEMA_COMPATIBILITY.md)
- [Metadata domain](meta/FLOW.md)

## Update Triggers

Update this file when root composition, lifecycle locking, message registry,
metrics, snapshots, engine isolation, sync, or compaction tuning changes.
