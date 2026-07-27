# internal/usecase/backup Flow

`internal/usecase/backup` owns entry-independent cluster backup coordination.
It does not read storage, call Controller directly, encode artifacts, or know a
concrete object-store or key-authority implementation.

Cross-layer coordination DTOs live in `internal/contracts/backup`; the usecase
re-exports those types while retaining all transition and scheduling policy.

Current flow:

1. `Status` exposes the continuous coordinator health, checkpoint age, latest
   checkpoint, bounded Slot capture leases/statuses, permanent-erasure
   progress, and effective non-secret policy. Each lease includes only the
   immediate durable promotion predecessor, bounded reason, and timestamp, so
   operators can distinguish audit recovery from policy or remap rebases
   without exposing object references. Missing checkpoint evidence stays
   `unknown`; it is never projected as age zero.
2. Slot capture workers update one durable frontier per logical Hash Slot
   through lease- and revision-fenced compare-and-swap ports. The usecase keeps
   only bounded coordination summaries; Channel cursors and encrypted payloads
   remain repository artifacts.
3. `CheckpointCoordinator.Publish` requires exactly one healthy durable
   frontier from every configured Hash Slot. It builds a nonblocking vector
   cut, authenticates the current segment and cursor commit proofs, publishes
   the checkpoint and hash-linked catalog page, then advances only the
   Controller catalog head while preserving concurrent frontier updates.
4. `ListCheckpointsPage` and `CheckpointByID` read immutable history through an
   injected rebuildable catalog browser. Every page returns a versioned opaque
   catalog-head token that pins the exact immutable discovery window without
   exposing repository object coordinates.
5. `DecideCheckpointRetention` applies UTC five-minute, hourly, daily, and
   optional monthly tiers. The newest checkpoint, operator holds, and the
   active restore checkpoint are protected. The result is a sparse Generation
   protection decision; checkpoint history and object identities never enter
   Controller state. `SetCheckpointHold` first appends the immutable
   hold/release page in both repositories, then advances the Controller head
   and `CatalogRetentionRevision` through one CAS. An active Generation-GC
   guard blocks the transition, while every external delete compares the same
   revision, so a newly held checkpoint cannot race deletion.
6. The restore state machine admits exactly one immutable plan, requires an
   empty target and distinct source/target generations, and pins the catalog
   proof, checkpoint identity, selected repository, and erasure snapshot.
   Admission decodes the caller's exact opaque catalog-head token internally
   and always selects the primary copy itself; repository selection and raw
   object references are not entry-layer inputs. It never trusts a mutable
   "latest" value copied from the unavailable source Controller.
   Before repository work it durably records the current physical Slot, Leader
   term, configuration epoch, and install attempt. Progress reports are fenced
   and monotonic; a Leader change advances the attempt without accepting stale
   completion. Status exposes bounded per-Slot install/convergence state,
   throughput, and ETA. Final semantic verification requires every current
   desired replica to revalidate its live installed state before activation.
   Normal activation authenticates a Controller/key-authority-signed
   source-fence receipt
   bound to this exact plan, checkpoint, source generation, and successor.
   Break-glass activation instead requires an authenticated operator, explicit
   reason, and immutable audit identity. Both paths persist `activating` first,
   run idempotent target-wide plaintext staging cleanup, and publish
   `activated` only after cleanup succeeds. A retry must present the same
   evidence and resumes cleanup without replacing the audit.
7. Permanent-erasure publication reserves one contiguous Controller sequence
   per Hash Slot. Each bounded stream state keeps its authenticated head, one
   pending record reference, and the latest committed reference so immediate
   retries can repair either repository without blocking unrelated Slots.
   Deterministic signed repository receipts retain idempotency for older
   committed events without an unbounded Controller map. Checkpoint publication
   freezes the sorted committed Slot heads. A restore plan immutably pins the
   authenticated current heads independently of the selected checkpoint.
   Admission counts both committed heads and every per-Slot pending reservation
   against the portable snapshot limit, so live retention is rejected before a
   deletion could make restore or garbage collection unreadable.
8. `FenceSource` durably installs one irreversible generation fence through
   Controller CAS, waits for every active data node to report the exact fence
    revision with `runtime_ready=false`, then signs the converged record. An
    identical retry returns the same semantic receipt; a different successor
    binding fails closed.

A pending Slot rebase or durable integrity state of `degraded`,
`rebase_required`, or `failed` blocks checkpoint publication. The old
Generation remains restorable until the replacement materialized baseline and
complete cursor proof are promoted. Large manifests and object identities stay
in repositories; Controller coordination stores only one bounded summary per
logical Hash Slot.
