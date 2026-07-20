# internal/runtime/conversationactive Flow

`conversationactive` owns the in-memory UID-owned active conversation cache for
all conversation kinds. Callers provide a `metadb.ConversationKind`; this package
does not infer kind from channel IDs.

Current flow:

1. Channel append or another authority-owned caller submits an `ActiveBatch`.
2. The manager merges rows by `(uid, kind, channel_id, channel_type)`.
3. Admission mutates cache state only. It may evict already-clean rows while
   holding the cache lock, but it protects every address present in the incoming
   batch and first compares the remaining exact clean-row count with the space
   required by the whole batch. When too few evictable clean rows exist it
   rejects immediately instead of scanning a full dirty cache or evicting a row
   that the same batch will update. Admission never reads or writes
   `ActiveStore` and never waits for the serialized durable flush lane. If a new
   row still exceeds the hard bound, the whole admission is rejected with
   `ErrCachePressure`.
4. At 80% total cache occupancy with more than 70% of configured capacity still
   dirty, the manager starts a pressure-drain cycle by sending one nonblocking,
   coalesced wakeup to the app flush worker. A full dirty cache uses the same
   wakeup and rejection path.
5. Each pressure wake persists at most the configured flush batch through
   `ActiveStore.TouchConversationActiveAt`. Successful attempts requeue one
   wakeup while dirty rows remain above the 70% low watermark. Retained clean
   rows form an immediately evictable reserve for later admissions.
6. Active view reads merge cache rows with durable `(uid, kind)` active-index pages.
7. Hash-slot handoff drains dirty rows scoped by UID hash slot before authority moves.

Only periodic, pressure-woken, and hash-slot-scoped durable flush attempts enter
the serialized flush lane. Version fencing preserves updates that arrive during
durable I/O; admission remains independent from that lane.

Cache observations report aggregate row/dirty counts plus fixed kind-aware
normal/CMD counts maintained incrementally on cache mutation. Observation does
not scan all UID rows, so large active-cache snapshots stay bounded by the
number of fixed conversation kinds rather than total cached conversations.
