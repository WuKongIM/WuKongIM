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
   mutations invalidate the affected cache entry after commit. Channel rows
   store status flags, the large-group marker, subscriber mutation version, and
   the ordinary subscriber count.
9. Subscriber mutations sort and de-duplicate UIDs, keep channel-owned
   subscriber rows through the table runtime, update the channel subscriber
   count and mutation version in the same commit, and invalidate the channel
   cache.
10. User channel membership rows are UID-owned reverse membership records keyed
    by `(uid, channel_id, channel_type)`, providing stable per-user channel
    paging without touching rows on ordinary group message commits.
11. Channel latest rows are channel-owned newest-message projections keyed by
    `(channel_id, channel_type)`. Upserts only advance when the incoming
    `last_message_seq` is newer, making committed-message retries and
    out-of-order projection delivery idempotent.
12. Message event projections are hash-slot scoped meta rows replicated by Slot
   Raft and snapshotted with other meta rows. State rows are keyed by
   `(channel_id, channel_type, client_msg_no, event_key)`, cursor rows are keyed
   by `(channel_id, channel_type, client_msg_no)`, and applied-event rows are
   keyed by `(channel_id, channel_type, client_msg_no, event_id)` so retries of
   older events cannot advance the cursor or reapply reducer payloads. Storage
   keeps reduced projection state and lean event idempotency records without raw
   replay rows. `stream.open` starts the default open lane,
   `stream.delta`/`stream.snapshot` update compact payload state, and terminal
   event types leave an idempotent terminal projection.
13. Channel runtime metadata stores routing, leadership, retention, and write
   fence state with a runtime-backed primary row and key-aware rowcodec value;
   typed methods keep monotonic upserts, guards, and retention semantics, while
   page scans use runtime primary-key order and cursor bounds.
14. Conversation state uses one kind-aware table for ordinary and CMD logical
   views. Rows are keyed by `(uid, kind, channel_id, channel_type)`, active scans
   use `(uid, kind, active_at desc, channel_id, channel_type)`, and `kind` stays
   out of the encoded row value. Storage never infers ordinary versus CMD
   semantics from channel-name suffixes; callers must pass the requested
   `ConversationKind`. Typed methods keep merge, hide, clear, and read-advance
   semantics isolated per kind. `SparseActive` marks rows whose `ActiveAt` is a
   low-frequency ordering anchor, and active patches can carry monotonic
   read/delete floors so activity advancement, sparse-active changes,
   delete-barrier checks, and floor merges happen in one shard-locked mutation.
15. Channel migration tasks use the table runtime for primary rows and terminal
   indexes while keeping the active-task index custom because its legacy value
   stores the active `task_id`; slot-scoped active-task reads page through that
   active index instead of scanning the primary table. Guarded
   task/runtime-meta mutations keep read-your-writes overlays before committing
   both records atomically, and task owner claims are fenced so only the same
   owner, an unowned task, or a task whose previous owner lease has expired at
   the claim request's `now_ms` can take ownership.
16. Hash-slot migration state uses the table runtime with a legacy primary key
   that omits the family suffix; applied-delta dedup rows and outbox rows stay
   as custom records under the same hash-slot partition, and typed values repeat
   the hash slot only for self-description.
17. `Batch` stages typed operations, locks all touched hash slots in sorted
   order, uses table overlays for ordinary runtime tables, validates guards
   against read-your-writes overlays for runtime metadata and channel migration
   tasks, commits once, then publishes or invalidates channel cache entries.
18. Hash-slot snapshots export row, index, and system spans for selected hash
    slots into a checksummed payload; imports validate the payload, lock slots
    in sorted order, replace existing spans, write entries in one sync commit,
    and clear the channel cache.
    Backup callers can open the same portable format from a pinned Pebble read
    view; counting and encoding scan the stable view without accumulating the
    full payload or blocking later metadata writes.
19. Preserving snapshot imports keep local hash-slot migration rows when they
    already exist, while still importing incoming migration rows that are not
    present locally.
20. `DeleteHashSlotData` removes all row, index, and system spans for one hash
    slot and clears the channel cache.
21. Read-only inspect APIs expose stable diagnostic rows for known metadata
    tables, supporting explicit hash-slot scans and bounded local scans across
    hash slots without mutating storage.
22. Slot FSM, proxy, cluster, runtime, access, and usecase callers use this
    package through the compatibility `DB`, `ShardStore`, and `WriteBatch`
    surface while the typed `MetaDB`/`Shard` APIs remain the new storage core.
    Legacy `UserConversation*` compatibility methods map to
    `ConversationKindNormal`, and legacy `CMDConversation*` compatibility
    methods map to `ConversationKindCMD`; neither path registers or writes a
    second conversation table.

Storage code in this package must not import Pebble directly.
