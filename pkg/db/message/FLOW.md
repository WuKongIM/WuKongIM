# pkg/db/message Flow

`pkg/db/message` owns channel-scoped message log storage on top of the shared
`pkg/db/internal` primitives.

Current flow:

1. `MessageDB` wraps the message Pebble engine.
2. `Channel` returns a typed `ChannelLog` for one channel partition.
3. Schema and key helpers define the durable message table layout.
4. Later tasks add row codecs, append/read, indexes, system state, and retention.

Storage code in this package must not import Pebble directly.
