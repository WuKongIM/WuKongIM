# pkg/db/message Flow

`pkg/db/message` owns channel-scoped message log storage on top of the shared
`pkg/db/internal` primitives.

Current flow:

1. `MessageDB` wraps the message Pebble engine and owns one channel registry.
   The message engine uses a 64 MiB memtable, while other engines retain the
   shared 32 MiB default. This lowers sustained message-write L0 sublevel
   creation without multiplying memory for lower-write databases. It retains
   a 128 MiB compaction-debt concurrency step so the larger memtable does not
   delay bounded recovery slots.
   Database operation guards reject new work during close; close drains admitted
   operations and background commit pins before detaching entries and closing
   the physical engine exactly once. Compatibility storage metrics use the same
   global operation guard so snapshots cannot overlap physical close.
2. `Channel` returns a distinct, idempotently closable `ChannelLog` lease. All
   leases for the same active key and identity share one canonical
   `channelEntry`, including its immutable append-key cache, append mutex,
   checkpoint mutex, and LEO state. Lease close waits for its admitted
   operations; the last lease or pin compare-deletes and reclaims the entry.
   `MessageDB.ChannelEntryMetricsSnapshot` and the compatibility `Engine`
   surface expose aggregate active-entry, lease, background-pin, acquisition,
   release, and reclamation counts. These metrics are database-wide and never
   carry channel keys or identities as labels.
3. `ChannelLog.Append` acquires one operation guard, serializes appends on the
   canonical entry, assigns contiguous
   sequences, validates strict duplicate constraints, and writes one complete
   primary row plus canonical secondary indexes atomically. Ordinary messages
   write four rows: primary message, global message ID, combined
   `(client_msg_no, from_uid)`, and sender sequence. Sender-less records retain
   a sequence-suffixed client-message index because they cannot participate in
   idempotency. Leader callers may present
   an all-records proof that message IDs came from the node-scoped globally
   unique allocator. That mode skips only existing message-ID index reads;
   in-batch duplicate IDs and durable sender/client-message idempotency keys
   remain validated. A bounded per-active-Channel membership filter skips
   durable idempotency point reads only for definite negatives; possible hits
   still read and verify the durable index and message row. The filter is
   rebuilt by a bounded-prefix scan after reopen or Channel entry reclamation,
   follows trusted follower applies while already loaded, and is capped at
   approximately 1.5 KiB per indexed active Channel. Saturation can only add
   false positives and durable reads; it cannot admit a duplicate. Caller-
   supplied or mixed-ID batches use full strict mode.
   The immutable channel catalog row is written with the first message only;
   later appends and follower applies use their base sequence to omit that
   redundant write without retaining a database-wide channel cache.
4. `ChannelLog.LEO` lazily recovers the last durable sequence by scanning the
   primary row keyspace after reopen or after a canonical entry is reclaimed
   and reacquired.
5. `ChannelLog.Read` and `ReadReverse` scan complete primary rows by sequence;
   point reads need one primary lookup rather than separate header and payload
   lookups.
   `ChannelLog.GetLastVisibleMessage` uses reverse iteration over the channel
   row keyspace to fetch the newest message above a visibility boundary without
   scanning the full channel or recovering LEO.
6. `GetByMessageID`, `ListByClientMsgNo`, `LookupIdempotency`, and
   `GetLastSenderMessageSeq` use typed secondary indexes and verify indexed
   rows before returning. The node-global message-ID index is the canonical
   location map for both point lookup and newest-message scans; no duplicate
   channel-local message-ID entry is written. The combined
   `(channel, client_msg_no, from_uid)` index provides exact idempotency lookup
   and prefix-based client-message listing without a second ordinary-message
   index write. The shared
   sender/sequence index is ordered by `(channel, from_uid, message_seq)` and
   lets callers find the latest message sent by one user through an explicit
   committed high-water mark without consulting the mutable tail. The shared
   message engine maintains the global `message_id` index so node-local
   newest-message pages are bounded by page size instead of channel count;
   truncation and retention remove that index entry atomically with the row.
   A version marker validates index startup, with no legacy-data backfill because
   the project has no released storage format. Reads bound raw index scans and
   delete dangling entries whose message rows have already been physically
   removed. Logical retention never deletes this canonical lookup state.
7. Checkpoint, epoch history, and snapshot payload APIs store channel system
   state under the message system keyspace; snapshot install persists payload,
   checkpoint, and epoch point in one batch.
8. `ApplyFetch` applies fetched records plus optional checkpoint/history in one
   batch with an explicit base sequence conflict check.
   Leader append batches may also select exact-base mode. Every exact request
   carries one versioned Channel epoch, leader term, fence, command, range,
   previous-entry identity, and digest manifest. The first call writes the
   manifest's range-end and command indexes plus each entry's authority,
   predecessor, command, and content-derived digest in the same synchronous
   physical batch as all primary rows and secondary indexes. The manifest tail
   equals the final entry digest, so a caller cannot reuse one manifest with
   different message semantics. Exact retry uses those indexes and returns the
   closed `AlreadyDurable` outcome without another commit, including after
   retention removed the materialized rows and after reopen. All append results
   are one of `Durable`, `AlreadyDurable`, `DefinitelyNotWritten`, `Conflict`,
   or `OutcomeUnknown`. Preparation rejection is definitely not written; once
   a commit request is admitted, caller cancellation or a physical commit error
   is conservatively unknown. Reopen plus exact replay resolves both the
   pre-commit-crash and post-commit-response-loss boundaries. Missing paired indexes, gaps,
   partial overlaps, or different identity/content fail as log conflicts.
   Exact and ordinary requests share the same cross-channel commit coordinator
   and add no second collection window. Exact replica mutations may carry a
   monotonic committed HW no higher than their proposal tail; MessageDB locks
   append state before checkpoint state and persists that HW in the same
   physical batch as the proposal. Adjacent exact proposals for the same
   Channel are validated against one in-batch immutable predecessor overlay and
   retain separate outcomes while sharing that commit; a gap returns the
   follower's exact next offset. `LoadDurableFrontier` takes the same lock
   order and returns one append/checkpoint-consistent LEO, committed HW, paired
   tail manifest, and tail entry identity. `LoadDurableRecovery` takes those
   same locks and adds a position-aligned set of requested entry identities;
   an identity missing at or below LEO is corruption, while an index above LEO
   is an explicit absent result. A non-empty log without the complete exact
   tail proof, or any checkpoint above LEO, is corrupt rather than a legacy
   recovery mode.
9. Truncate and retention deletes remove primary rows and secondary indexes
   together. Suffix truncation atomically deletes every proposal manifest at
   or above the new LEO and rejects a cut through the middle of one proposal;
   prefix retention deliberately preserves manifests for exact replay.
   Bounded retention trims can advance physical deletion in multiple batches
   while retention state preserves LEO across reopen after prefix trim.
10. Catalog entries are created by the first durable append or a system
    mutation and can be
   listed through `MessageDB.ListChannels`, paged with
   `MessageDB.ListChannelsPage`, or paged through the compatibility
   `Engine.ListChannelsPage` surface for Node-owned cleanup loops.
11. Read-only inspect APIs page catalog channels directly by catalog key and
    scan channel messages through raw `(MessageDB, ChannelKey)` readers. They
    own a database operation guard but never acquire or populate a channel
    registry entry.
12. The compatibility `Engine` / `ChannelStore` surface adapts legacy
    `pkg/channel` record, checkpoint, history, retention, committed-cursor, and
    query callers onto the typed `ChannelLog` core while keeping seq/offset
    conversion at the channel boundary. Sequence message scans preserve the
    caller context through forward and reverse row iteration, so canceled HTTP
    or RPC reads do not continue as background storage work. Every `ForChannel`
    call returns a distinct lease; closing one store cannot close another lease
    or the shared engine, and closed stores return `channelcompat.ErrClosed`.
13. Compatibility append/apply commits transfer canonical append locks,
    every checkpoint lock acquired for the request, and one background pin per
    entry to a terminal commit owner. The checkpoint lock transfer is preserved
    even when a requested HW update is already a durable no-op; row-only writes
    must not orphan that lock. Caller cancellation stops waiting but cannot
    release those resources before build, physical commit, publish, or
    coordinator shutdown reaches a terminal state. Finalization unlocks
    checkpoint then append locks before releasing pins.
14. The commit coordinator observer emits
    low-cardinality queue depth/capacity, batch, and logical request wait
    measurements, splitting leader append and follower apply lanes, without
    changing durable commit semantics. The coordinator can optionally route
    requests across partition-hashed shards; the default is one shard, and
    each shard still uses synchronous physical commits. Absolute queue-depth
    publication is linearized both within a shard and across the aggregate;
    delayed observer callbacks refresh the latest total and cannot overwrite a
    terminal zero with stale queued work. The coordinator also republishes
    after grouped collection drains sibling requests directly from its channel,
    so the final batch cannot retain a pre-collection depth. Batch helpers reject
    duplicate canonical entries before writes, group work by `Engine` in
    request order, and never hold channel locks from different physical engines
    simultaneously. If caller cancellation leaves an admitted group running to
    terminal completion, all remaining Engine groups fail before taking locks.
    Multi-entry lock acquisition releases the partial set and retries when any
    later entry is busy, so one batch never waits while holding an earlier
    channel lock. Checkpoint-only batches take checkpoint locks without append
    locks and retain their entries through the same terminal commit ownership.
    Each Engine group publishes all channel frontiers only after its shared
    physical commit succeeds.
15. Canonical checkpoint locking serializes all checkpoint stores and
    apply/snapshot staging. `StoreCheckpointHWMonotonic` performs a locked
    read-modify-write that initializes a missing checkpoint (including an
    explicit first HW of zero), never regresses HW, and preserves epoch and
    log-start fields.
16. Compatibility durable payloads continue to use FNV-64a payload hashes so
    handler idempotency checks compare the same value that was encoded into the
    `channel.Record` payload.
17. Message rows persist `ServerTimestampMS`, `FromUID`, `ClientMsgNo`, and
    `Payload` so conversation list display can read durable fields from the
    message log instead of transient committed events. `ServerTimestampMS` is a
    separate durable header column from the legacy `Timestamp` field; old rows
    without the new column decode `ServerTimestampMS` as zero, and new leader
    appends default it at the DB boundary when callers omit it.
18. Full-backup readers pin one engine view and stream a portable, checksummed
    hash-slot payload containing every committed message through the selected
    HW, plus checkpoint, epoch history, retention state, committed proposal and
    entry identities, and idempotency fields. Proposal identities above the
    selected HW are excluded with their uncommitted message suffix; paired
    indexes, the entry chain, and retained message content are revalidated.
    The count pass derives the exact message-row count and maximum message ID
    from the same pinned view; restore parsing independently recomputes both.
19. Restore imports one complete snapshot into a fresh isolated database in
    bounded batches. An exact retry is idempotent, any different pre-existing
    Channel checkpoint is a conflict, and final verification checks live
    checkpoint/LEO state plus deterministic snapshot content before a replica
    acknowledges staging.
20. Restore failure cleanup removes every Channel row and secondary
    index, checkpoint/history/retention record, and catalog entry before retry.
    Message and index deletion is paged in batches of at most 1024 rows and
    approximately 8 MiB of payload.
21. Schema and key helpers define the durable message table layout.

Storage code in this package must not import Pebble directly.
