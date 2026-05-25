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
9. Later tasks add batch mutations, snapshots, conversation tables, and
   migration tables.

Storage code in this package must not import Pebble directly.
