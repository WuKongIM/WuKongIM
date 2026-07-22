# internal/usecase/backup Flow

`internal/usecase/backup` owns entry-independent cluster backup coordination.
It does not read storage, call Controller directly, encode artifacts, or know a
concrete object-store/KMS SDK.

Cross-layer coordination DTOs live in `internal/contracts/backup`; the usecase
re-exports those types while retaining all transition and scheduling policy.

Current flow:

1. `Trigger` creates one fenced active job through a compare-and-swap state
   port. A second active job is rejected.
2. Workers report bounded logical hash-slot summaries through
   `ReportPartition`. Reports are fenced by job ID and backup epoch; identical
   retries are idempotent and conflicting retries fail closed.
3. `Publish` requires every configured hash slot exactly once before invoking
   the injected restore-point publisher. Missing partitions never reach object
   publication.
4. State mutations use bounded compare-and-swap retries so the usecase remains
   independent of the Controller command implementation that persists them.
5. `Status` derives RPO health from the newest restore point while preserving a
   missing first restore point as `unknown` with no numeric age.
6. `ApplyRetention` deterministically selects UTC five-minute, hourly, daily,
   optional monthly, held, newest, and active-base references, then moves
   expired references into a durable pending-garbage queue before deletion.
7. The restore state machine admits exactly one immutable plan, requires empty
   target and generation evidence, records idempotent per-hash-slot install
   reports including record counts and the message-ID fence, requires final
   semantic verification, and accepts activation only with a lowercase SHA-256
   old-cluster fence digest.
8. Permanent-erasure publication reserves one contiguous Controller sequence
   at a time. The bounded state keeps the committed boundary, one pending
   record reference, and the latest committed reference so immediate retries
   can repair either repository. Deterministic signed repository receipts retain
   idempotency for older committed events without an unbounded Controller map. A restore plan
   immutably pins the authenticated current ledger prefix independently of the
   selected restore point.

Large channel/object manifests stay in repositories. Coordination state stores
only one bounded summary per logical hash slot.
