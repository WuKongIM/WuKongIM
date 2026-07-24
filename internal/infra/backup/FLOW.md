# Backup Infrastructure Flow

## Responsibility

`internal/infra/backup` adapts entry-independent backup use cases to concrete
Controller and repository runtimes. It does not decide scheduling, retention,
restore eligibility, or backup health.

## Controller State

```text
backup usecase StateStore
  -> ControllerStateStore.Load
  -> Controller Runtime.LocalState
  -> detach bounded BackupCoordinationState

backup usecase CompareAndSwap
  -> convert bounded usecase state
  -> Runtime.ReplaceBackupCoordinationState(expected cluster revision)
  -> Controller Raft command
  -> cluster-state.json
```

Large manifests, encrypted chunks, KMS data keys, and repository credentials
must never be stored in Controller state. A Controller revision mismatch maps to
`backup.ErrStateConflict` so the use case can reload and retry.
The bounded mapping includes one verification task and each retained restore
point's latest audit evidence, allowing a new Controller Leader to resume the
task without treating node-local metrics as authority.

`Doctor.Check` reports primary repository, secondary repository, KMS, staging,
and UTC-clock readiness individually with a bounded first-failure category.
Manager status may expose those health buckets and configured regions, but
never endpoint, bucket, prefix, role ARN, key ID, or fingerprint values.

## File Repository

`FileRepository` is the development and unit-test adapter. It streams into a
temporary sibling file, verifies the declared size and SHA-256, fsyncs it, and
uses a hard link for create-if-absent publication. Existing keys are never
replaced. Reads reject symlinks and paths outside the configured root.

`ChunkReplicator` bounds plaintext memory, compresses before encryption, gives
every chunk a fresh envelope data key, and verifies each immutable object in
both repositories before returning its manifest reference. Every stream attempt
also gets a fresh immutable key namespace nested below `objects/<jobID>/`, so a
retry never collides with different randomized ciphertext from a partial
attempt while active-job garbage-collection protection still matches every
attempt. A failed stream may leave unreachable immutable chunks, but it cannot
expose a restore point.

Partition and top-level manifest publication are retryable without overwrites.
The top-level manifest is first staged and verified in both repositories, then
a separately signed publication marker makes it discoverable. If only one
repository accepted an immutable manifest or marker, the retry authenticates
the existing exact bytes, repairs the missing copy, and reuses the original
signature/report instead of generating a conflicting object for the fixed key.

The Controller-side `RestorePointPublisher` reloads every partition manifest
from both repositories, compares the exact bytes and job/cut summaries, stats
the complete recursive base-to-tip object graph in both repositories, copies
the authenticated cumulative record counts and message-ID fence into the
signed top-level partition reference, then publishes the manifest. It never
trusts a node report as proof that repository data exists.

`PartitionPlanner` first obtains a Slot snapshot whose commit and durable apply
indexes match, then pages Channel runtime metadata directly into compact
source-node fences without retaining a duplicate full metadata slice. A second
snapshot must preserve the same Slot/term/index fence or the attempt is
discarded. The compact plan has a hard per-hash-slot Channel limit; metadata
and message payload bytes remain streaming.

## Restore And Retention

Restore inspection authenticates both repository copies, requires matching
manifest bytes and identities, authenticates the current contiguous permanent-
erasure ledger in both repositories, pins its version, boundary, and SHA-256,
and asks every current target node for semantic storage emptiness before
persisting a plan. The ledger pin is current even when the operator deliberately
selects an older restore point, so deletion cannot be undone through rollback.
Installation resolves each hash
slot through the successor `HashSlotTable` and installs its encrypted objects
only on that physical Slot's `DesiredPeers`, with at most eight node installs
in flight per partition. Unrelated cluster members never receive a full copy
of the partition. The latest authenticated Channel index also rebuilds
`ChannelRuntimeMeta` on those target Slot replicas in batches of at most 4096:
Channel epoch and retention floor come from the durable cut, while leader,
replicas, ISR, and MinISR are derived from the successor Slot replica
candidates. Source runtime placement is never restored. Installation
independently recomputes metadata records, message rows, and maximum message
ID. Final verification compares them with the signed partition evidence, then
checks every authenticated Channel sequence cut, rebuilt target runtime
metadata, and the post-transform canonical metadata SHA-256 on the same target
Slot replicas. The configured staging-byte ceiling is shared by all concurrent
partition streams on one node, not multiplied per stream.

Before ordinary retention metadata advances, `PermanentErasureLedger` encrypts
the Channel identity and boundary, publishes identical immutable ciphertext and
signed record bytes to both repositories, reserves the next Controller sequence,
publishes the identical signed commit marker and deterministic per-event receipt
to both repositories, and commits the Controller boundary. One partially
published sequence is repaired before a new sequence is admitted. The receipt
lets any older committed event retry return its original sequence in constant
time. If any ledger step fails, live retention fails closed
without advancing the deletion boundary. Restore decrypts the plan-pinned
prefix, collapses repeated events to the greatest per-Channel boundary, applies
bounded physical prefix deletion plus checkpoint/LEO fences on every successor
Slot replica, and only then installs reconstructed runtime metadata. Channels
present only in the ledger still receive a sequence fence so erased sequence
numbers cannot be reused.

Retention first moves expired Controller references into `PendingGarbage`.
The garbage collector authenticates every retained graph in both repositories,
authenticates and marks every committed ledger event/record/commit/receipt
object, and also authenticates and marks the one Controller-referenced pending
event so failover can resume after the grace period. It protects active job
prefixes and deletes only exact old unreachable versions through separate
garbage-collector credentials that retain the signed logical repository names.
Unreferenced uncommitted ledger orphans remain eligible for age-gated
collection; no broad prefix is permanently exempted.
