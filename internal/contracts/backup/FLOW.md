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
`SlotCaptureStatus` is a detached public projection of that frontier plus the
latest observed source watermarks, per-stream lag, capture state, and a bounded
failure category. Its `LeaseCurrent` bit distinguishes a current owner from a
fenced stale worker.
The coordination state adds one `CatalogPageReference` head beside those Slot
frontiers. It never transports checkpoint history or repository payloads.

Backup coordination state includes at most one durable verification task and
bounded per-restore-point later-audit evidence. Publication-time primary and
secondary verification flags remain separate from this later evidence so a
new audit cannot rewrite the original publication result. Pending or running
verification excludes a backup job, and the active verification target remains
retention-protected.

Restore partition reports carry only bounded verification evidence: the
canonical metadata digest, exact metadata and cumulative message record counts,
the greatest restored message ID, an explicit evidence version, and
install/verify status. Storage adapters recompute these values; this contracts
package only transports them between restore infrastructure, usecase, and
Controller state. A missing evidence version remains unknown and cannot be
installed, verified, activated, or admitted by normal startup.
