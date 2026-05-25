# pkg/db/message Flow

`pkg/db/message` owns channel-scoped message log storage on top of the shared
`pkg/db/internal` primitives.

Current flow:

1. `MessageDB` wraps the message Pebble engine.
2. `Channel` returns a cached typed `ChannelLog` for one channel partition.
3. `ChannelLog.Append` serializes appends per channel, assigns contiguous
   sequences, and writes header/payload row families atomically.
4. `ChannelLog.LEO` lazily recovers the last durable sequence by scanning the
   primary row keyspace after reopen.
5. `ChannelLog.Read` and `ReadReverse` scan primary rows by sequence and
   materialize messages from header/payload families.
6. Schema and key helpers define the durable message table layout.
7. Later tasks add secondary indexes, system state, retention, and catalog
   mutation.

Storage code in this package must not import Pebble directly.
