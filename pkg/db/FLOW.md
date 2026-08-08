# pkg/db Flow

`pkg/db` is the root package for node-local storage. It owns shared errors,
options, and the root `NodeStore` handle.

When changing existing durable table fields, follow
[`SCHEMA_COMPATIBILITY.md`](SCHEMA_COMPATIBILITY.md).

Current flow:

1. Build `NodeStoreOptions` with `DefaultNodeStoreOptions` or explicit paths.
2. Open the root handle with `OpenNodeStore`.
3. `NodeStore` constructs one message-domain registry and one metadata domain;
   repeated `Messages` calls return the same canonical message registry.
4. Read `MetricsSnapshot` when operators need a Pebble-neutral view of the
   physical `message` and `meta` stores. The message snapshot also carries the
   cumulative definite-negative idempotency reads avoided and possible hits
   verified durably. A root lifecycle read lock prevents these physical reads
   from overlapping root shutdown, while the canonical message-domain
   operation guard also prevents snapshots from overlapping a direct
   `Messages().Close()`.
5. Close the root handle during application shutdown. Message shutdown first
   rejects acquisitions and drains its registry before the physical message
   engine is closed exactly once; metadata then closes independently. The root
   lifecycle write lock also makes concurrent Close callers wait for the same
   terminal physical shutdown.
6. Internal engine snapshots provide a Pebble-neutral pinned read view with
   bounded iterators. Callers must close the view; later writes and compactions
   may proceed while the snapshot is streamed.

Pebble-specific code must stay under `pkg/db/internal/*` and must not leak into
callers.
On Darwin, a 16 MiB `BytesPerSync` interval avoids turning range syncs during
compactions into repeated full-file fsyncs.
Every writable engine keeps one baseline compaction slot and permits Pebble to
open up to three additional slots as L0 read amplification or compaction debt
crosses successive pressure thresholds. The L0 concurrency step is 6, so the
extra slots may open at depths 6, 12, and 18; the fourth has six sublevels of
recovery headroom before the write-stop depth of 24. A separate debt step of
four memtables (128 MiB with defaults) also lets sustained debt open additional
slots. High-write callers may keep an explicit debt step when using a larger
memtable so flush cadence and recovery concurrency remain independently tuned.
The reactive upper bound avoids extra compactions while the store is
calm.
