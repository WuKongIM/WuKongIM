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
   physical `message` and `meta` stores. A root lifecycle read lock prevents
   these physical reads from overlapping root shutdown, while the canonical
   message-domain operation guard also prevents snapshots from overlapping a
   direct `Messages().Close()`.
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
