# pkg/db/message Flow

`pkg/db/message` owns channel-scoped message log storage on top of the shared
`pkg/db/internal` primitives.

Current flow:

1. `MessageDB` wraps the message Pebble engine and owns one channel registry.
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
   sequences, validates strict duplicate constraints, and writes header/payload
   row families plus secondary indexes atomically.
4. `ChannelLog.LEO` lazily recovers the last durable sequence by scanning the
   primary row keyspace after reopen or after a canonical entry is reclaimed
   and reacquired.
5. `ChannelLog.Read` and `ReadReverse` scan primary rows by sequence and
   materialize messages from header/payload families.
   `ChannelLog.GetLastVisibleMessage` uses reverse iteration over the channel
   row keyspace to fetch the newest message above a visibility boundary without
   scanning the full channel or recovering LEO.
6. `GetByMessageID`, `ListByClientMsgNo`, and `LookupIdempotency` use typed
   secondary indexes and verify indexed rows before returning. The shared
   message engine also maintains a global `message_id` index so node-local
   newest-message pages are bounded by page size instead of channel count;
   truncation and retention remove that index entry atomically with the row.
   A version marker gates reads while a resumable, idempotent background
   backfill adds index entries for databases created before the index existed.
   The backfill persists its channel/message cursor with each bounded batch and
   pauses between batches; callers receive an explicit building error instead
   of a partial page. Reads also bound raw index scans and delete dangling
   projection keys left by concurrent truncate/backfill races.
7. Checkpoint, epoch history, and snapshot payload APIs store channel system
   state under the message system keyspace; snapshot install persists payload,
   checkpoint, and epoch point in one batch.
8. `ApplyFetch` applies fetched records plus optional checkpoint/history in one
   batch with an explicit base sequence conflict check.
9. Truncate and retention deletes remove primary rows and secondary indexes
   together; bounded retention trims can advance physical deletion in multiple
   batches while retention state preserves LEO across reopen after prefix trim.
10. Catalog entries are updated by durable append/system mutations and can be
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
    conversion at the channel boundary. Every `ForChannel` call returns a
    distinct lease; closing one store cannot close another lease or the shared
    engine, and closed stores return `channelcompat.ErrClosed`.
13. Compatibility append/apply commits transfer canonical append locks,
    optional checkpoint locks, and one background pin per entry to a terminal
    commit owner. Caller cancellation stops waiting but cannot release those
    resources before build, physical commit, publish, or coordinator shutdown
    reaches a terminal state. Finalization unlocks checkpoint then append locks
    before releasing pins.
14. The commit coordinator observer emits
    low-cardinality queue depth/capacity, batch, and logical request wait
    measurements, splitting leader append and follower apply lanes, without
    changing durable commit semantics. The coordinator can optionally route
    requests across partition-hashed shards; the default is one shard, and
    each shard still uses synchronous physical commits. Batch helpers reject
    duplicate canonical entries before writes, group work by `Engine` in
    request order, and never hold channel locks from different physical engines
    simultaneously. If caller cancellation leaves an admitted group running to
    terminal completion, all remaining Engine groups fail before taking locks.
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
18. Schema and key helpers define the durable message table layout.

Storage code in this package must not import Pebble directly.
