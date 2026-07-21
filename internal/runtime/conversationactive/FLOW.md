# internal/runtime/conversationactive Flow

`conversationactive` owns the in-memory UID-owned active conversation cache for
all conversation kinds. Callers provide a `metadb.ConversationKind`; this package
does not infer kind from channel IDs.

Current flow:

1. Channel append or another authority-owned caller submits an `ActiveBatch`.
2. Before taking the cache lock, the manager coalesces rows by
   `(uid, kind, channel_id, channel_type)` using maximum `ActiveAtMS`,
   `ReadSeq`, and `MessageSeq`. Small authority batches use an inline path
   without a temporary map, so repeated addresses cause only one version and
   dirty-index mutation.
3. Admission mutates cache state only. Bounded managers maintain an exact index
   of clean cache addresses. When space is required, admission protects every
   address present in the incoming batch and first selects the complete clean
   victim set from that index. It mutates the cache only after all victims have
   been found. When too few evictable clean rows exist it rejects immediately
   without scanning dirty rows, partially evicting the batch, or evicting a row
   that the same batch will update. Admission never reads or writes
   `ActiveStore` and never waits for the serialized durable flush lane. If a new
   row still exceeds the hard bound, the whole admission is rejected with
   `ErrCachePressure`. A clean row keeps its latest observed `ActiveAtMS` and
   `MessageSeq` for immediate list reads separately from its confirmed durable
   `ActiveAt` baseline. Receiver-only activity that remains strictly inside
   `ActiveCooldown` advances the cached view, emits a low-cardinality
   `CooldownSuppressed` mutation, and leaves the clean/dirty indexes unchanged.
   Sender `ReadSeq` advances, cooldown boundaries, and authority hash-slot
   changes still mark the row dirty.
4. At 80% total cache occupancy with more than 70% of configured capacity still
   dirty, the manager starts a pressure-drain cycle by sending one nonblocking,
   coalesced wakeup to the app flush worker. A full dirty cache uses the same
   wakeup and rejection path.
5. Each pressure wake persists at most the configured flush batch through
   `ActiveStore.TouchConversationActiveAt`. Flush filtering checks durable
   delete barriers for sender and receiver rows before write: a stale or unknown
   `MessageSeq` is reconciled out of cache instead of treating the store's
   idempotent no-op as a confirmed active baseline. Successful attempts requeue one
   wakeup while dirty rows remain above the 70% low watermark and the attempt
   cleared at least one dirty marker. The manager does not immediately
   self-signal after a zero-progress attempt. Instead, the app-owned worker
   keeps the drain active with a cancellation-safe delayed retry and bounded
   exponential backoff from 25ms to 250ms; progress, no work, or an error ends
   that retry chain. This avoids both a tight wakeup loop and a one-second
   periodic-tick stall under continuous version conflicts. Retained clean rows
   form an immediately evictable reserve for later admissions.
6. Active view reads carry each cache row's `MessageSeq` while merging the
   latest cached view with durable `(uid, kind)` active-index pages and primary
   row hydration. A durable `DeletedToSeq` hides an unknown or older cached
   activity even when a stale touch returned nil and left that cache row clean.
   Hydration reconciles the discovered barrier back into the bounded cache:
   stale rows are removed, while a newer cache view that is not yet reflected
   durably is forced dirty. A cooldown skip confirms only the durable baseline;
   it never rolls the newer cached `ActiveAtMS` or `MessageSeq` backward.
7. A committed conversation delete is reconciled by exact cache address under
   the manager lock. A cached row with unknown `MessageSeq`, or a sequence at or
   below `DeletedToSeq`, is removed from every cache index. A newer row keeps its
   latest view, receives a new durable-baseline generation, and is forced dirty
   so the durable delete barrier can fence its retry. The generation prevents an
   in-flight pre-delete flush from confirming the invalidated baseline. A lower
   already-observed barrier is ignored. Reapplying an equal barrier keeps an
   already-dirty newer row idempotent, but re-invalidates a clean newer row so a
   stale durable read followed by equal-barrier hydration cannot leave the
   conversation clean on an obsolete baseline. A separate unknown-prefix error
   path never removes rows or records an attempted barrier; it invalidates the
   requested durable baselines and forces present rows dirty until durable
   hydration distinguishes the committed prefix from the uncommitted tail.
8. Hash-slot handoff drains dirty rows scoped by UID hash slot before authority
   moves, then purges clean rows through an exact per-slot clean index. The
   purge is bounded by rows owned by that slot and does not scan or allocate for
   the full cache. Dirty rows are never purged by the clean-slot purge.

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
turn later receiver-only activity into a sender write. No-write and persisted
snapshots are reconciled together under one cache-lock acquisition. Both successful
writes and durable cooldown reads advance the confirmed baseline when their
baseline generation still matches, including across a cache-version conflict;
the cached list view remains the maximum observation.

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
is divided into acknowledged `Persisted`, cooldown `Skipped`, and durable
delete-barrier `DeleteFenced` rows; after
durable completion those same rows are divided into actually `Cleared` rows,
version-conflicted rows retained for retry, and `Superseded` stale snapshots
that are no longer present or dirty. Persisted
rows are never reported as cleared merely because the store call succeeded. A
store error reports `Requeued` from the selected addresses that are still dirty
at error observation time. This includes a newer version recreated after a
delete-fenced snapshot was removed, so first-stage `DeleteFenced` and
second-stage `Requeued` may overlap. The durable row count remains unknown
because the adapter may have completed an earlier Slot proposal. The observer
also records the serialized lane wait plus select, filter, persist, and clear
stage durations.
Pressure signals carry their enqueue time so the app-owned worker can report
signal-to-receive wait without adding UID, channel, hash-slot, or Slot labels.
