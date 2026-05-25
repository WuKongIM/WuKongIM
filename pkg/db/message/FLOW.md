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
8. Schema and key helpers define the durable message table layout.
9. Later tasks add retention and catalog mutation.

Storage code in this package must not import Pebble directly.
