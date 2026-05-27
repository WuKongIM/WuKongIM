# pkg/db/meta Flow

`pkg/db/meta` owns hash-slot-scoped metadata storage on top of shared
`pkg/db/internal` primitives.

Current flow:

1. `MetaDB` wraps the metadata engine and returns stable `Shard` handles per
   hash slot.
2. Key helpers encode rows, indexes, and system state under the meta domain and
   hash-slot partition.
3. Ordinary metadata tables register a `TableSpec` in their `table_<name>.go`
   file; the registry drives `Tables()`, row spans for snapshots, and common
   primary/index runtime behavior.
4. Schema descriptors define the durable metadata table catalog.
5. Multi-hash-slot helpers lock shards in sorted order to avoid deadlocks.
6. User, device, subscriber, channel runtime metadata, and plugin binding tables
   use the table runtime for primary rows, key-aware values when needed, scans,
   indexes, and ordinary batch staging.
7. Channel ordinary CRUD and channel-ID reads use the table runtime, while
   channel batch/cache orchestration remains custom so post-commit cache
   publishing is unchanged.
8. Channel reads populate an opportunistic in-memory cache, and channel
   mutations invalidate the affected cache entry after commit.
9. Subscriber mutations sort and de-duplicate UIDs, keep membership rows through
   the table runtime, update the channel subscriber mutation version in the same
   commit, and invalidate the channel cache.
10. Channel runtime metadata stores routing, leadership, retention, and write
   fence state with a runtime-backed primary row and key-aware rowcodec value;
   typed methods keep monotonic upserts, guards, and retention semantics, while
   page scans use runtime primary-key order and cursor bounds.
11. User and CMD conversation tables use the table runtime for primary rows,
   primary-prefix pages, active-index maintenance, and active scans; their typed
   methods keep merge, hide, clear, and read-advance business semantics.
12. Channel migration tasks use the table runtime for primary rows and terminal
   indexes while keeping the active-task index custom because its legacy value
   stores the active `task_id`; guarded task/runtime-meta mutations keep
   read-your-writes overlays before committing both records atomically.
13. Hash-slot migration state uses the table runtime with a legacy primary key
   that omits the family suffix; applied-delta dedup rows and outbox rows stay
   as custom records under the same hash-slot partition, and typed values repeat
   the hash slot only for self-description.
14. `Batch` stages typed operations, locks all touched hash slots in sorted
   order, uses table overlays for ordinary runtime tables, validates guards
   against read-your-writes overlays for runtime metadata and channel migration
   tasks, commits once, then publishes or invalidates channel cache entries.
15. Hash-slot snapshots export row, index, and system spans for selected hash
    slots into a checksummed payload; imports validate the payload, lock slots
    in sorted order, replace existing spans, write entries in one sync commit,
    and clear the channel cache.
16. Preserving snapshot imports keep local hash-slot migration rows when they
    already exist, while still importing incoming migration rows that are not
    present locally.
17. `DeleteHashSlotData` removes all row, index, and system spans for one hash
    slot and clears the channel cache.
18. Read-only inspect APIs expose stable diagnostic rows for known metadata
    tables, supporting explicit hash-slot scans and bounded local scans across
    hash slots without mutating storage.
19. Slot FSM, proxy, cluster, runtime, access, and usecase callers use this
    package through the compatibility `DB`, `ShardStore`, and `WriteBatch`
    surface while the typed `MetaDB`/`Shard` APIs remain the new storage core.

Storage code in this package must not import Pebble directly.
