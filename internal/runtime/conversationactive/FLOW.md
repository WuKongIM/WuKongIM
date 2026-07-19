# internal/runtime/conversationactive Flow

`conversationactive` owns the in-memory UID-owned active conversation cache for
all conversation kinds. Callers provide a `metadb.ConversationKind`; this package
does not infer kind from channel IDs.

Current flow:

1. Channel append or another authority-owned caller submits an `ActiveBatch`.
2. The manager merges rows by `(uid, kind, channel_id, channel_type)`.
3. Dirty flushes are serialized from cache snapshot through durable completion,
   then write through `ActiveStore.TouchConversationActiveAt`.
4. Cache-pressure admission first evicts already-clean rows while holding the
   cache lock. Only an insufficient clean working set enters the serialized
   flush lane, where the larger of required admission capacity and configured
   headroom is persisted and evicted. Spill size follows bounded admission work
   instead of scaling to the whole dirty cache.
5. Active view reads merge cache rows with durable `(uid, kind)` active-index pages.
6. Hash-slot handoff drains dirty rows scoped by UID hash slot before authority moves.

Serialization covers scheduled, cache-pressure, and hash-slot-scoped flushes. It
prevents concurrent admissions at the row cap from duplicating a pressure
snapshot while preserving version fencing for updates that arrive during
durable I/O.

Cache observations report aggregate row/dirty counts plus fixed kind-aware
normal/CMD counts maintained incrementally on cache mutation. Observation does
not scan all UID rows, so large active-cache snapshots stay bounded by the
number of fixed conversation kinds rather than total cached conversations.
