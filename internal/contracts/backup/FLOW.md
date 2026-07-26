# Backup Contracts Flow

`internal/contracts/backup` contains only lightweight coordination DTOs,
bounded status enums, and cross-layer sentinel errors. It lets node-local
runtime code communicate with the backup usecase through injected ports without
importing `internal/usecase`.

The package does not schedule jobs, mutate Controller state, read storage,
encode repository artifacts, or call infrastructure. Business decisions remain
in `internal/usecase/backup`; runtime receives the pure scheduling function
through composition.

The continuous-capture contract adds exactly one bounded `SlotFrontier` per
Hash Slot. A frontier atomically binds independent metadata and message stream
heads, their opaque bounded source cursors, reconciled source positions, and
the older stream watermark. The same record carries a `SlotCaptureLease`
containing the physical Slot ID, Raft leader term, configuration epoch, holder
node, Generation, monotonic lease sequence, and takeover time. `SourceSlotID`
separately identifies the physical Raft index space used by the durable
metadata cursor; a mismatch after routing remap survives restart and forces a
materialized rebase instead of comparing unrelated indices. Channel
identities never enter this record; the
message stream has a payload head plus a separate `CursorHead` pointing to
immutable cursor-only evidence in the repository. After a single-Slot rebase,
`Baseline` authenticates the independent materialized partition root and
`BaselineCursorHead` authenticates its complete Channel boundary index.
`Rebase` is a bounded durable pending record containing only target Generation,
lease-bound rebase epoch, bounded reason, and start time. The old Generation
and all of its restore references stay active until that pending replacement
is atomically promoted. Authority takeover or a compacted immutable source cut
rotates only the pending target so retries cannot loop forever on an
unreadable target.
`SourcePinStartedAtUnixMillis` is the durable age origin for the retained
source floor. It survives lease takeover and resets only when metadata capture
advances the floor or rebase promotion installs a new baseline.
`GenerationStartedAtUnixMillis`, per-stream committed plaintext byte counters,
and the materialized baseline plaintext size drive independent Slot Generation
compaction without adding object lists to coordination state. Promotion resets
the counters and Generation age with the replacement references.
`SlotCaptureStatus` is a detached public projection of that frontier plus the
latest observed source watermarks, per-stream lag, capture state, and a bounded
failure category. Its `LeaseCurrent` bit distinguishes a current owner from a
fenced stale worker.
The coordination state adds one `CatalogPageReference` head beside those Slot
frontiers plus one scalar `CatalogRetentionRevision`. The revision changes only
when a signed immutable hold/release page becomes the visible head and fences
external deletion against that policy transition. It never transports
checkpoint history or repository payloads.
Permanent erasure coordination is also partitioned by Hash Slot: each bounded
stream exposes only its authenticated head plus at most one pending and one
last-committed reference. Restore contracts carry sorted stream heads, their
aggregate event count, and the snapshot digest; public projections reduce each
stream to Slot, sequence, and pending state without Channel identity or
repository keys.

Backup coordination state also carries at most one irreversible
`SourceFenceRecord`, binding one source
cluster generation to one exact restore plan, checkpoint digest, and successor
generation. The record becomes converged only after every active data node
reports the fence revision while not ready for ordinary traffic.

Generation GC adds at most two sorted `GenerationGCCursor` records—one per
explicit repository. Each record contains only a CAS revision, cycle identity,
fixed catalog-retention revision, fixed cutoff, lexicographic key cursor,
completion bit, and update time. Object identities and pending delete queues
remain repository concerns.

Restore plans carry the exact catalog proof, checkpoint identity, selected
repository copy, target generation, and pinned erasure snapshot. Per-Slot
reports remain bounded while distinguishing pending, Leader installation,
installed, follower convergence, converged, and failed phases. Activation adds
an explicit `activating` phase between verification and service. That phase
persists immutable source-receipt or break-glass evidence before cluster-wide
plaintext staging cleanup; only `activated` carries cleanup and activation
timestamps. Reports fence the
physical Slot, Leader term, configuration epoch, and install attempt, and carry
exact typed counts, Channel-boundary count, content/message-Merkle digests,
download/replication progress, replica convergence, and timestamps. Storage
adapters compute this evidence; this package only transports it between restore
infrastructure, usecase, runtime, and Controller state. Missing evidence cannot
be installed, verified, activated, or admitted by normal startup.

Checkpoint replica DTOs carry one begin/chunk/commit/status/cleanup action, immutable
semantic plan identity, current Slot authority, exact file digests and offsets,
pre/post-erasure message counts, maximum message ID, and bounded completion
evidence. They deliberately contain no repository
locator, encrypted data key, KMS identifier, or provider credential.
