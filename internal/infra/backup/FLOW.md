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

## File Repository

`FileRepository` is the development and unit-test adapter. It streams into a
temporary sibling file, verifies the declared size and SHA-256, fsyncs it, and
uses a hard link for create-if-absent publication. Existing keys are never
replaced. Reads reject symlinks and paths outside the configured root.

`ChunkReplicator` bounds plaintext memory, compresses before encryption, gives
every chunk a fresh envelope data key, and verifies each immutable object in
both repositories before returning its manifest reference. Every stream attempt
also gets a fresh immutable key namespace, so a retry never collides with
different randomized ciphertext from a partial attempt. A failed stream may
leave unreachable immutable chunks, but it cannot expose a restore point.

Partition and top-level manifest publication are retryable without overwrites.
If only one repository accepted an immutable manifest, the retry authenticates
the existing exact bytes, verifies their references, repairs the missing copy,
and reuses the original signature/report instead of generating a conflicting
object for the fixed manifest key.

The Controller-side `RestorePointPublisher` reloads every partition manifest
from both repositories, compares the exact bytes and job/cut summaries, stats
all referenced objects, copies the authenticated cumulative record counts and
message-ID fence into the signed top-level partition reference, then publishes
the manifest. It never trusts a node report as proof that repository data
exists.

`PartitionPlanner` first obtains a Slot snapshot whose commit and durable apply
indexes match, then pages Channel runtime metadata directly into compact
source-node fences without retaining a duplicate full metadata slice. A second
snapshot must preserve the same Slot/term/index fence or the attempt is
discarded. The compact plan has a hard per-hash-slot Channel limit; metadata
and message payload bytes remain streaming.

## Restore And Retention

Restore inspection authenticates both repository copies, requires matching
manifest bytes and identities, and asks every current target node for semantic
storage emptiness before persisting a plan. Installation streams encrypted
objects through a bounded staging file into restore-only metadata/message
imports. The latest authenticated Channel index also rebuilds
`ChannelRuntimeMeta` on every target node in batches of at most 4096: Channel
epoch and retention floor come from the durable cut, while leader, replicas,
ISR, and MinISR are derived from the successor topology. Source runtime
placement is never restored. Installation independently recomputes metadata
records, message rows, and maximum message ID. Final verification compares
them with the signed partition evidence, then checks every authenticated
Channel sequence cut, rebuilt target runtime metadata, and the post-transform
canonical metadata SHA-256 on every current node. The configured staging-byte
ceiling is shared by all concurrent partition streams on one node, not
multiplied per stream.

Retention first moves expired Controller references into `PendingGarbage`.
The garbage collector authenticates every retained graph in both repositories,
marks reachable keys, protects active job prefixes, and deletes only exact old
unreachable versions through the separate garbage-collector credentials.
