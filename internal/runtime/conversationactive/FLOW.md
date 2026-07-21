# internal/runtime/conversationactive Flow

`conversationactive` owns the in-memory UID-owned active conversation cache for
all conversation kinds. Callers provide a `metadb.ConversationKind`; this package
does not infer kind from channel IDs.

Current flow:

1. Channel append or another authority-owned caller submits an `ActiveBatch`.
2. Before taking the cache lock, the manager coalesces rows by
   `(uid, kind, channel_id, channel_type)` using maximum `ActiveAtMS` and
   `ReadSeq`. Small authority batches use an inline path without a temporary
   map, so repeated addresses cause only one version and dirty-index mutation.
3. Admission mutates cache state only. Bounded managers maintain an exact index
   of clean cache addresses. When space is required, admission protects every
   address present in the incoming batch and first selects the complete clean
   victim set from that index. It mutates the cache only after all victims have
   been found. When too few evictable clean rows exist it rejects immediately
   without scanning dirty rows, partially evicting the batch, or evicting a row
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
   wakeup while dirty rows remain above the 70% low watermark and the attempt
   cleared at least one dirty marker. A zero-progress attempt keeps the drain
   active but waits for the periodic worker tick instead of forming a tight
   wakeup loop. Retained clean rows form an immediately evictable reserve for
   later admissions.
6. Active view reads merge cache rows with durable `(uid, kind)` active-index pages.
7. Hash-slot handoff drains dirty rows scoped by UID hash slot before authority moves.

Only periodic, pressure-woken, and hash-slot-scoped durable flush attempts enter
the serialized flush lane. Version fencing preserves updates that arrive during
durable I/O; admission remains independent from that lane.

Dirty rows enter one removable FIFO and bounded flush selection rotates each
selected address to the tail. A bounded attempt therefore visits every live
dirty address once before repeating one, including after version conflicts.
Authority-scoped handoff keeps the independent hash-slot dirty index. Oldest
dirty activity uses a typed deletable counted min-heap containing only live
positive `ActiveAtMS` buckets, so its size is bounded by live dirty rows rather
than cumulative updates. Cooldown classification uses whether the current
dirty version advanced `ReadSeq`; a historical cached sender `ReadSeq` does not
turn later receiver-only activity into a sender write. Skipped and persisted
results are cleared together under one cache-lock acquisition.

Cache observations report aggregate row/dirty counts plus fixed kind-aware
normal/CMD counts maintained incrementally on cache mutation. Observation does
not scan all UID rows, so large active-cache snapshots stay bounded by the
number of fixed conversation kinds rather than total cached conversations.
Each snapshot carries a manager-local monotonic revision; the metrics adapter
rejects delayed older snapshots so concurrent admissions cannot regress gauges.
Production wiring coalesces admission-path aggregate snapshots to one per 100ms;
flush completion and pressure transitions force a fresh snapshot. Mutation
counts remain per admission and count unique coalesced row transitions. Cache
observations also expose dirty FIFO rows and
live dirty-age bucket counts, while mutation and flush observations split cache
lock wait from in-lock work. Admission lock histograms retain bounded
`ok`/`cache_pressure` results so rejected attempts do not disappear from the
pressure tail.

Successful flush observations use an explicit conservation model. `Selected`
is divided into acknowledged `Persisted` and cooldown `Skipped` rows; after
durable completion those same rows are divided into actually `Cleared` rows,
version-conflicted rows retained for retry, and `Superseded` stale snapshots
that are no longer present or dirty. Persisted
rows are never reported as cleared merely because the store call succeeded. A
store error keeps every selected dirty marker for idempotent retry, but its
durable row count is unknown because the adapter may have completed an earlier
Slot proposal. The observer also records the serialized lane wait plus select,
filter, persist, and clear stage durations.
Pressure signals carry their enqueue time so the app-owned worker can report
signal-to-receive wait without adding UID, channel, hash-slot, or Slot labels.
