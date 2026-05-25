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
5. Later tasks add table codecs, caches, batch mutations, snapshots, and
   migration tables.

Storage code in this package must not import Pebble directly.
