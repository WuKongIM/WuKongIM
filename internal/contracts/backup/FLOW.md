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
the older stream watermark. Channel identities never enter this record; the
message stream has a payload head plus a separate `CursorHead` pointing to
immutable cursor-only evidence in the repository.
`SlotCaptureStatus` is a detached public projection of that frontier plus the
latest observed source watermarks, per-stream lag, capture state, and a bounded
failure category.
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
