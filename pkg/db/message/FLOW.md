# pkg/db/message Flow

`pkg/db/message` owns channel-scoped message log storage on top of the shared
`pkg/db/internal` primitives.

Current flow:

1. `MessageDB` wraps the message Pebble engine.
2. `Channel` returns a cached typed `ChannelLog` for one channel partition.
3. `ChannelLog.Append` serializes appends per channel, assigns contiguous
   sequences, validates strict duplicate constraints, and writes header/payload
   row families plus secondary indexes atomically.
4. `ChannelLog.LEO` lazily recovers the last durable sequence by scanning the
   primary row keyspace after reopen.
5. `ChannelLog.Read` and `ReadReverse` scan primary rows by sequence and
   materialize messages from header/payload families.
6. `GetByMessageID`, `ListByClientMsgNo`, and `LookupIdempotency` use typed
   secondary indexes and verify indexed rows before returning.
7. Checkpoint, epoch history, and snapshot payload APIs store channel system
   state under the message system keyspace; snapshot install persists payload,
   checkpoint, and epoch point in one batch.
8. `ApplyFetch` applies fetched records plus optional checkpoint/history in one
   batch with an explicit base sequence conflict check.
9. Truncate and retention deletes remove primary rows and secondary indexes
   together; retention state preserves LEO across reopen after prefix trim.
10. Catalog entries are updated by durable append/system mutations and can be
   listed through `MessageDB.ListChannels`.
11. Read-only inspect APIs page catalog channels directly by catalog key and
    scan channel messages for diagnostics without mutating storage or
    populating channel caches.
12. The compatibility `Engine` / `ChannelStore` surface adapts legacy
    `pkg/channel` record, checkpoint, history, retention, committed-cursor, and
    query callers onto the typed `ChannelLog` core while keeping seq/offset
    conversion at the channel boundary.
13. Compatibility durable payloads continue to use FNV-64a payload hashes so
    handler idempotency checks compare the same value that was encoded into the
    `channel.Record` payload.
14. Schema and key helpers define the durable message table layout.

Storage code in this package must not import Pebble directly.
