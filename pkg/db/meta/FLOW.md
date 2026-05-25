# pkg/db/meta Flow

`pkg/db/meta` owns hash-slot-scoped metadata storage on top of shared
`pkg/db/internal` primitives.

Current flow:

1. `MetaDB` wraps the metadata engine and returns stable `Shard` handles per
   hash slot.
2. Key helpers encode rows, indexes, and system state under the meta domain and
   hash-slot partition.
3. Schema descriptors define the durable metadata table catalog.
4. Multi-hash-slot helpers lock shards in sorted order to avoid deadlocks.
5. User, device, and channel tables use typed shard methods; channel ID indexes
   stay in the same commit as primary rows.
6. Channel reads populate an opportunistic in-memory cache, and channel
   mutations invalidate the affected cache entry after commit.
7. Subscriber mutations sort and de-duplicate UIDs, update the channel
   subscriber mutation version in the same commit, and invalidate the channel
   cache.
8. Channel runtime metadata stores routing, leadership, retention, and write
   fence state with monotonic upserts; page scans use primary-key order and
   cursor bounds.
9. User and CMD conversation tables keep primary state rows and active indexes
   in the same commit; active scans read newest-first and verify rows to ignore
   stale index entries.
10. Plugin bindings maintain UID-primary rows plus plugin-number indexes for
   paged scans.
11. Hash-slot migration state, applied-delta dedup rows, and outbox rows stay
   under the hash-slot partition; typed values repeat the hash slot only for
   self-description.
12. `Batch` stages typed operations, locks all touched hash slots in sorted
   order, validates guards against read-your-writes overlays for runtime
   metadata and channel migration tasks, commits once, then publishes or
   invalidates channel cache entries.
13. Channel migration tasks keep primary rows, one active-task index per
   channel, and terminal indexes in sync; guarded batch creates can fence on
   runtime metadata.
14. Hash-slot snapshots export row, index, and system spans for selected hash
    slots into a checksummed payload; imports validate the payload, lock slots
    in sorted order, replace existing spans, write entries in one sync commit,
    and clear the channel cache.
15. Preserving snapshot imports keep local hash-slot migration rows when they
    already exist, while still importing incoming migration rows that are not
    present locally.
16. `DeleteHashSlotData` removes all row, index, and system spans for one hash
    slot and clears the channel cache.
17. Slot FSM, proxy, cluster, runtime, access, and usecase callers use this
    package through the compatibility `DB`, `ShardStore`, and `WriteBatch`
    surface while the typed `MetaDB`/`Shard` APIs remain the new storage core.

Storage code in this package must not import Pebble directly.
