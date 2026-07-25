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

`ControllerSlotFrontierStore` updates one sorted per-Hash-Slot record inside
the same bounded state. It first requires a complete local Slot Leader identity
proved by fresh Multi-Raft status matching the route term and config epoch.
Unchanged authority reuses the durable lease without a write;
leader term, holder, Slot ID, or config epoch changes atomically advance its
lease sequence while preserving both stream heads. Final commits revalidate
current authority plus exact lease and frontier revision after repository
uploads. The store retries
unrelated global Controller revision conflicts from a fresh snapshot, and
therefore never overwrites concurrent checkpoint, verification, retention, or
erasure coordination.

Generation promotion is a separate lease-fenced CAS. It accepts only the
durably recorded pending target, keeps Slot authority and lease sequence
unchanged, installs the new materialized partition plus baseline cursor, and
resets the pin-age timestamp atomically with the Generation. A stale leader
cannot promote repository objects uploaded after losing authority.
Because a full metadata snapshot is cut at a physical Slot applied index that
may be later than this Hash Slot's last mutation, `ContinuousSource` preserves
that cut as the materialized Generation's resume floor until a later matching
logical command appears.

`ReplicatedCheckpointCatalog` signs and verifies only the newly published
checkpoint and catalog page. It writes the checkpoint to both repositories,
then the catalog page secondary-first and primary-last, and returns a head only
after all four immutable objects have matching provider metadata. Publication
never opens earlier pages. `CheckpointCatalogIndex` is a node-local derived
index used by Manager pagination and exact-ID lookup. Its checksummed atomic
file is not authority: every process cold start walks and authenticates the
signed dual-repository hash chain, replacing missing, malformed, injected, or
head-inconsistent data. While the process remains live, a new head normally
extends the authenticated index by reading only newly linked pages.

## Continuous Sources

`MetadataLogSource` maps the routed Slot Leader's last applied Raft index that
affects the requested Hash Slot, plus the forward
`ReadBackupMetadataLogPage` cursor, into continuous runtime pages. The
cluster adapter builds a rebuildable sparse `(physical Slot, Hash Slot) -> Raft
index` read index, so a physical Raft log is scanned once instead of once per
logical Hash Slot. Unrelated commands therefore do not advance the logical
watermark or trigger an empty frontier CAS. The index is populated only by
backup reads and is discarded on node restart; retained RaftDB remains the
correctness source.

`MessageLogSource` pages authoritative Channel runtime metadata on the Slot
Leader, routes fence-checked observations and committed-row reads to local or
remote Channel Leaders, and compares those cuts with the cursor-only artifact
referenced by the durable frontier. A bounded in-memory Channel hint selects
hot work directly; hints may be dropped because each no-hint call examines at
most one 256-entry metadata page and advances a disposable sweep from the
durable rotation cursor. Restart begins that paged sweep again, so correctness
does not depend on the hint queue or scan memory and a poll never opens every
Channel store in a large Hash Slot. Each page groups boundary observations by
Channel leader, producing at most one RPC and one runtime probe per leader
group.
An unfinished metadata sweep marks immediate discovery continuation, allowing
the runtime to tail-requeue the Slot instead of waiting for the periodic poll.
After a Slot frontier CAS failure, `ContinuousSource` drops the disposable
message scan so the next attempt retries every admitted cut from the durable
cursor.

One observation pins one exact Channel epoch, retention start, HW, and time in
the opaque cut cursor. `ReadPage` refreshes placement only, reads no later than
that HW, and leaves post-observation commits for another cut. A durable partial
segment carries the same target and consumed position, so restart completes it
without substituting another Channel. The transient sweep advances past a
completed target only after runtime validation has admitted the page to its
rolling accumulator; a read or validation failure keeps the target pinned.
Large remote rows are transferred in
deterministic frame-bounded chunks. A node retains at most one encoded
oversized page, and materialization is globally serialized per node; cache loss
only causes an authoritative reread and is never correctness state.
`ContinuousSource` combines this message adapter with the metadata adapter
without merging their cursors or watermarks.

`MessageCursorResolver` starts from `CursorHead`, opens cursor-only sidecars
through `ReplicatedSegmentStore.Load`, validates Hash Slot, Generation,
sequence, cursor continuity, and non-regressing source watermarks, then rebuilds
the latest sorted Channel boundaries. Every 1024 sidecars a full cursor
checkpoint terminates the delta chain. The resolver caches only the latest
complete index per Hash Slot, capped globally by 256 entries and 64 MiB with
LRU eviction, so normal advancement loads the new sidecar while restart work
and resident cache memory remain bounded. Controller state never contains
Channel identities or the cache. Materialized baseline cursor load and decode
hold a shared capture-memory reservation for their complete working-set
lifetime; the default budget admits one maximum legal baseline through decode
and immutable output construction, and admission fails
before repository loading when the node budget is unavailable. The pinned
message cut carries the compact previous boundary needed by each page, so a
page read never reloads the full prior baseline while its outer page
reservation is held. Decoded immutable baselines are keyed by the complete
authenticated Segment reference and retained in a shared-budget-charged LRU;
multiple Channel cuts reuse the same sorted index. Baseline and cursor-delta
views remain separate and resolve each Channel with allocation-free binary
search, avoiding a full Hash-Slot merge or map per cut. Reference changes and
memory pressure select or evict bounded entries without weakening correctness.

`ClusterSourcePinManager` converts each healthy capture lease into an
idempotent local Multi-Raft compaction floor. It measures retained Raft-log
bytes after the metadata frontier, deduplicates node accounting by physical
Slot at the minimum floor, and serializes records that share that physical
Slot. After a member changes, it remeasures the exact current minimum floor
before aggregate accounting, then selects the largest physical interval's
oldest floor as the deterministic victim when the node budget is exceeded.
The age origin is durable across lease takeover, so failover cannot renew an
already-old pin and deterministic victim ties use that same durable origin.
Release addresses the recorded physical Slot directly, remeasures the new
physical minimum before returning aggregate bytes, and
lease replacement first removes the old exact record, so route movement and
Leader transfer cannot strand a floor.

A materialized rebase reuses `DistributedWorker` with an independent-full
request. It commits the complete `message_baseline_cursor` Channel index before
the immutable partition manifest. The manifest binds that cursor only when it
has no incremental base. Retry loads the same dual-repository immutable
manifest instead of recapturing or changing the pending Generation.
A cut hook installs the replacement source floor immediately after the stable
physical cut is opened, preventing compaction from overtaking repository
upload. A published target whose source cut becomes unreadable is rotated by a
durable frontier CAS before retry, rather than loading the same stale immutable
manifest forever.

The runtime owns the pure pending-work and first-sequence rules. This package
only converts cluster routing/boundary DTOs and performs repository adaptation.

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
The first persisted signed manifest freezes the restore point's erasure heads;
a retry that observes newer heads adopts the frozen values while requiring all
other job, cut, and partition fields to match before repairing missing copies.

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
manifest bytes and identities, authenticates every current per-Hash-Slot
permanent-erasure stream in both repositories, and pins its version, sorted
heads, total event count, and SHA-256. Current heads must dominate the heads
frozen by the selected manifest. Inspection then asks every current target node
for semantic storage emptiness before
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
signed record bytes to both repositories, reserves the next sequence for the
event's Hash Slot, and publishes a predecessor-linked signed commit marker plus
deterministic per-event receipt to both repositories. The first immutable
primary commit serializes concurrent same-Slot finalizers; a contender adopts
that authenticated commit and repairs the secondary copy. Each lineage uses a
repository/source-cluster/source-generation digest namespace, so successor
generations reuse physical repositories without commit-key collisions or mixed
listings. One partially
published sequence is repaired before a new event is admitted to the same Slot,
while unrelated Slots continue independently. The receipt lets any older
committed event retry return its original sequence in constant time. If any
ledger step fails, live retention fails closed
without advancing the deletion boundary. Restore decrypts the plan-pinned
Slot prefixes, collapses repeated events to the greatest per-Channel boundary, applies
bounded physical prefix deletion plus checkpoint/LEO fences on every successor
Slot replica, and only then installs reconstructed runtime metadata. Channels
present only in the ledger still receive a sequence fence so erased sequence
numbers cannot be reused.

Retention first moves expired Controller references into `PendingGarbage`.
The garbage collector authenticates every retained graph in both repositories,
authenticates and marks every committed ledger event/record/commit/receipt
object, and also authenticates and marks every Controller-referenced pending
Slot event so failover can resume after the grace period. It protects active job
prefixes and deletes only exact old unreachable versions through separate
garbage-collector credentials that retain the signed logical repository names.
For every retained restore point from another source generation, collection
loads that manifest's lineage-specific ledger, verifies the frozen heads are
exact commits in its current prefix, and marks the whole lineage before sweep.
Unreferenced uncommitted ledger orphans remain eligible for age-gated
collection; no broad prefix is permanently exempted.
