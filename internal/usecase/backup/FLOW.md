# internal/usecase/backup Flow

`internal/usecase/backup` owns entry-independent cluster backup coordination.
It does not read storage, call Controller directly, encode artifacts, or know a
concrete object-store/KMS SDK.

Cross-layer coordination DTOs live in `internal/contracts/backup`; the usecase
re-exports those types while retaining all transition and scheduling policy.

Current flow:

1. `Trigger` creates one fenced active job through a compare-and-swap state
   port. A second active job or active verification task is rejected.
2. Workers report bounded logical hash-slot summaries through
   `ReportPartition`. Reports are fenced by job ID and backup epoch; identical
   retries are idempotent and conflicting retries fail closed.
3. `Publish` requires every configured hash slot exactly once before invoking
   the injected restore-point publisher. Missing partitions never reach object
   publication.
4. State mutations use bounded compare-and-swap retries so the usecase remains
   independent of the Controller command implementation that persists them.
5. `Status` derives RPO and later-audit health from durable state while
   preserving missing evidence as `unknown` with no numeric age. It also
   reports the Controller coordinator observation, effective non-secret policy,
   individual dependency readiness, and bounded reference capacity.
6. `ListRestorePointsPage` provides opaque newest-first keyset pagination with
   bounded ID/held filters. `StartVerification` durably admits one cluster-wide
   task; `RunVerification` persists running and terminal dual-repository
   evidence under a bounded execution timeout. Backup and verification tasks
   are mutually exclusive.
7. `ApplyRetention` deterministically selects UTC five-minute, hourly, daily,
   optional monthly, held, newest, and active-base references, then moves
   expired references into a durable pending-garbage queue before deletion.
   The target of a pending/running verification is protected too.
8. The restore state machine admits exactly one immutable plan, requires an
   empty target and distinct source/target generations, and pins the catalog
   proof, checkpoint identity, selected repository, and erasure snapshot.
   Admission requires the caller's exact immutable catalog-head reference; it
   never trusts a mutable "latest" value copied from the unavailable source
   Controller.
   Before repository work it durably records the current physical Slot, Leader
   term, configuration epoch, and install attempt. Progress reports are fenced
   and monotonic; a Leader change advances the attempt without accepting stale
   completion. Status exposes bounded per-Slot install/convergence state,
   throughput, and ETA. Final semantic verification requires every current
   desired replica to revalidate its live installed state before activation.
   Normal activation authenticates a Controller/KMS-signed source-fence receipt
   bound to this exact plan, checkpoint, source generation, and successor.
   Break-glass activation instead requires an authenticated operator, explicit
   reason, and immutable audit identity. Both paths persist `activating` first,
   run idempotent target-wide plaintext staging cleanup, and publish
   `activated` only after cleanup succeeds. A retry must present the same
   evidence and resumes cleanup without replacing the audit.
9. Permanent-erasure publication reserves one contiguous Controller sequence
   per Hash Slot. Each bounded stream state keeps its authenticated head, one
   pending record reference, and the latest committed reference so immediate
   retries can repair either repository without blocking unrelated Slots.
   Deterministic signed repository receipts retain idempotency for older
   committed events without an unbounded Controller map. Checkpoint publication
   freezes the sorted committed Slot heads. A restore plan immutably pins the
   authenticated current heads independently of the selected restore point.
   Admission counts both committed heads and every per-Slot pending reservation
   against the portable snapshot limit, so live retention is rejected before a
   deletion could make restore or garbage collection unreadable.
10. `CheckpointCoordinator.Publish` requires exactly one healthy durable
    frontier from each Slot's public capture status, builds a nonblocking vector cut,
    authenticates only its current segment and cursor commit proofs,
    dual-commits the new checkpoint and hash-linked catalog page, then advances
    only the Controller catalog head while preserving concurrent frontier
    updates. The first publication also initializes the monotonic retained
    audit root; later retention advances it before Generation GC.
    `ListCheckpointsPage` and `CheckpointByID` read immutable history
    through an injected rebuildable catalog browser instead of Controller
    arrays. Each list page also returns the exact immutable catalog head used
    to build it so an operator can carry that reference unchanged into restore
    admission.
    A Slot with a durable pending rebase blocks cluster-complete publication
    even though its old Generation remains restorable and other Slot frontiers
    may keep advancing. After promotion, the checkpoint includes the
    materialized partition reference and complete baseline cursor proof.
    A durable integrity-audit `degraded`, `rebase_required`, or `failed` Slot
    also blocks publication directly from Controller state, so a Controller
    Leader switch cannot publish before node-local capture status refreshes.
11. `DecideCheckpointRetention` applies the UTC five-minute, hourly, daily,
    and optional monthly tiers to immutable catalog references. The newest
    checkpoint, explicit operator holds, and the active restore checkpoint are
    always retained. Its output is a Generation protection decision; it does
    not place checkpoint history or object identities in Controller state.
    Daily integrity audit consumes the same sparse decision and persists only
    a content digest, so catalog pages between retained checkpoints remain
    navigable without treating their collected Generations as audit targets.
12. `FenceSource` durably installs one irreversible generation fence through
    Controller CAS, waits for every active data node to report the exact fence
    revision with `runtime_ready=false`, then signs the converged record. An
    identical retry returns the same semantic receipt; a different successor
    binding fails closed.

Large channel/object manifests stay in repositories. Coordination state stores
only one bounded summary per logical hash slot.
