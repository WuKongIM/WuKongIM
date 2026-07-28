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

Large manifests, encrypted chunks, plaintext data keys, and repository credentials
must never be stored in Controller state. A Controller revision mismatch maps to
`backup.ErrStateConflict` so the use case can reload and retry.

`ControllerSlotFrontierStore` updates one sorted per-Hash-Slot record inside
the same bounded state. It first requires a complete local Slot Leader identity
proved by fresh Multi-Raft status matching the route term and config epoch.
Unchanged authority reuses the durable lease without a write;
leader term, holder, Slot ID, or config epoch changes atomically advance its
lease sequence while preserving both stream heads. Final commits revalidate
current authority plus exact lease and frontier revision after repository
uploads. The store retries
unrelated global Controller revision conflicts from a fresh snapshot, and
therefore never overwrites concurrent checkpoint, integrity-audit, Generation
GC, or erasure coordination.

Generation promotion is a separate lease-fenced CAS. It accepts only the
durably recorded pending target, keeps Slot authority and lease sequence
unchanged, installs the new materialized partition plus baseline cursor, and
resets the pin-age timestamp atomically with the Generation. A stale leader
cannot promote repository objects uploaded after losing authority.
Because a full metadata snapshot is cut at a physical Slot applied index that
may be later than this Hash Slot's last mutation, `ContinuousSource` preserves
that cut as the materialized Generation's resume floor until a later matching
logical command appears.

`ControllerIntegrityAuditStateStore` persists its own audit revision while
preserving unrelated Controller fields. It also maintains a narrow sorted
audit projection. Production composition publishes locally applied Controller
snapshots on every node once per interval into the same store instance consumed by capture
and GC; the per-Slot capture hot path then performs only an
in-memory binary search instead of
rebuilding all backup coordination state. Frozen entries refresh on access,
and an atomic frontier CAS that observes a newer remote freeze forces a cache
refresh. Generation GC first appends a durable per-Slot delete guard to the
same audit revision; a concurrent auditor cannot newly freeze that Slot until
the exact external delete finishes and removes the guard. This Controller
record, rather than a process mutex, preserves ordering across Leader changes.
Generation GC also rebuilds the exact sparse selection from any unfinished
audit cursor and unions those checkpoint vectors into its mark set. Each delete
guard acquisition presents the audit cycle whose selection was marked and
the fixed `CatalogRetentionRevision`, and atomically compares both with current
durable state. Hold/release refuses to advance while a live guard exists; a
hold that wins first changes the revision and prevents the stale delete from
starting. The guard itself stores
only Slot, token, and safety lease; while it exists, audit CAS refuses to start
a different cycle. Retention bucket changes, hold release, active-restore
completion, and Controller Leader changes therefore cannot delete a Generation
still owned by the fixed audit selection, without globally pausing collection
of unrelated expired Generations.
Before listing any candidate, GC also advances the Controller's monotonic
`CatalogAuditRootSequence`. That transition waits for an older in-progress
audit cycle to finish, so legally expired Generations cannot disappear beneath
a cursor that still includes them.

`CatalogSegmentIntegrityAuditPlan` fixes the current catalog head and walks
newest-to-oldest page transitions only down to the previously completed
catalog sequence during an ordinary epoch. A durable daily scrub epoch
periodically resets that lower bound to the retained catalog root. A
content-digested sparse selection is rebuilt from the authenticated checkpoint
index with the same UTC retention tiers, holds, and fixed active-restore ID
used by Generation GC. Catalog pages remain navigable, but data checkpoints
outside that selection are skipped and a retained transition joins the nearest
older retained checkpoint rather than an expired page in between. This avoids
reporting legally collected Generation holes as corruption while still
detecting latent damage without a new checkpoint. For each transition it
follows authenticated metadata, message, cursor, baseline-cursor,
materialized partition manifest/object, and permanent-erasure
commit/receipt/record/event links until the prior checkpoint boundary. Erasure
streams advance independently per Hash Slot and authenticate the predecessor
digest where a retained checkpoint delta joins its prior head. Its opaque
cursor contains the exact immutable page, checkpoint references, sparse
selection digest, navigation phase, artifact, stop reference, and conservative
debt. It never contains an erasure event's data-key envelope; the
event step reloads its authenticated signed record from each repository.
Hold/release pages are explicitly state-only and are skipped one durable page
per step. A four-entry page/checkpoint cache removes repeated GETs within a
cycle. Materialized payload steps share a separate 64 MiB byte-bounded cache of
fully authenticated manifests, keyed by repository and complete partition
reference and reset when the durable cycle changes, including on the first
Inspect or Repair after Controller Leader takeover. Cache insertion copies the
manifest once; later object steps use an immutable internal view and expose
only a constant-size navigation summary. The cursor allows process or
Controller Leader restart without rescanning already completed catalog
history.

`ReplicatedCheckpointCatalog` signs and verifies the newly published
checkpoint, its content-addressed complete Generation vector, and the catalog
page. It reuses an identical signed vector, repairs a missing vector copy,
writes the checkpoint to both repositories, then writes the catalog page
secondary-first and primary-last. A head is returned only after every required
immutable copy has matching provider metadata. Hold/release appends reload the
checkpoint and vector and reject a caller-supplied mismatch before signing the
new state-only page. Audit navigation loads page, checkpoint, and Generation
vector copies independently; one damaged copy is repaired through the explicit
repair capability and fully revalidated before traversal continues.
`CheckpointCatalogIndex` stores only the compact vector
reference, so 256 Slot strings are not duplicated in every historical row. It
is a node-local derived
index used by Manager pagination and exact-ID lookup. Its checksummed atomic
file is not authority: every process cold start walks and authenticates the
signed dual-repository hash chain through the repair-capable audit reader,
replacing missing, malformed, injected, or head-inconsistent data. While the
process remains live, a new head normally extends the authenticated index by
reading only newly linked pages. The integrity selector consumes a detached
latest-state reference snapshot from this index; only its digest and fixed
active-restore ID are persisted in Controller Raft.

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
Channel identities or the cache. Materialized baseline cursor load decodes the
authenticated full-checkpoint `MessageCursorBatch` envelope, fences its Hash
Slot and Generation, and holds a shared capture-memory reservation for its
complete working-set lifetime; the default budget admits one maximum legal
baseline through decode and immutable output construction, and admission fails
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

A materialized rebase reuses `DistributedWorker` with a Generation- and
rebase-epoch-fenced request. It commits the complete
`message_baseline_cursor` Channel index before the immutable single-layer
partition manifest. Retry loads the same replicated immutable
manifest instead of recapturing or changing the pending Generation.
A cut hook installs the replacement source floor immediately after the stable
physical cut is opened, preventing compaction from overtaking repository
upload. A published target whose source cut becomes unreadable is rotated by a
durable frontier CAS before retry, rather than loading the same stale immutable
manifest forever.

The runtime owns the pure pending-work and first-sequence rules. This package
only converts cluster routing/boundary DTOs and performs repository adaptation.

`Doctor.Check` evaluates repository, signing/encryption, staging, and UTC-clock
readiness internally and returns only aggregate health plus one bounded
first-failure category. Manager status never exposes individual repository
copies, regions, endpoint, bucket, prefix, role ARN, key ID, or fingerprints.

Alibaba production uses `OSSRepository` plus a local
`DeploymentKeyAuthority`. Doctor requires
OSS versioning plus a default COMPLIANCE ObjectWorm policy at least as long as
the configured retention. Because versioned OSS does not support atomic
create-only `PutObject`, ordinary writes perform an existence check and a
write-after-read metadata verification under the existing Controller Leader
and partition single-writer fences. Complete reads verify the stored SHA-256.
`Open` first reads authoritative object metadata, pins the returned version ID
on `GetObject`, and verifies the streamed body against that HEAD size and
SHA-256; it does not depend on a proxy preserving GET `Content-Length`.
Repair publishes a new protected version, and GC deletes exact version IDs.
Loading a garbage adapter first clears the node's stable probe slot, then
creates, lists, and removes one delete marker. This proves list,
`DeleteObject`, and exact-version deletion through the garbage role without
creating or deleting any data-object version. A failed delete leaves at most
one marker in that node's slot; the next startup must clear it before creating
another, and startup fails closed if any operation is denied.
The reusable `pkg/backup/keypackage` authority discovers exactly one protected
credential named `wukongim-backup-key-package`, authenticates all package
fields with an independent HMAC-SHA256 package key, validates its repository
binding, and performs AES-256-GCM data-key wrapping plus Ed25519 signing
locally. Its repository-pinned wrapper establishes an immutable Package ID
root and a signed odd-revision activation chain in both repositories. A staged
rotation adds pending keys for rolling distribution without advancing that
chain; activation retains old wrapping material and old signing public keys
for historical reads while removing the old signing seed and advancing the
chain. Repository qualification precedes pin creation, and the configured
lowest-ID Controller voter is the only pin publisher because versioned OSS
does not provide atomic create-if-absent. That publisher stays stable across
Raft terms. The implicit single-node cluster normalizes to its local voter.
Seed-join mirrors have no admitted voter identity, never publish pins, and
remain read-only verifiers until admission is persisted. Ordinary
repository access, repair, and garbage each use a distinct
assumed RAM STS role per repository; no cloud KMS role is required.

Production composition constructs Alibaba repair and garbage clients,
wires replicated segment/catalog repair,
durable catalog audit, all-node projection, and Leader-only Generation
collection. An unfinished per-repository cursor keeps the same deterministic
cycle across time windows and Controller Leader changes; a hold/release
revision starts a new cycle from the beginning. Automatic backup and explicit
restore mode are mutually exclusive, so an operator must hold the selected
checkpoint before stopping the source for a restore drill and may release it
only after the drill no longer needs that cut.

## File Repository

`FileRepository` is the development and unit-test adapter. It streams into a
temporary sibling file, verifies the declared size and SHA-256, fsyncs it, and
uses a hard link for create-if-absent publication. Existing keys are never
replaced. Reads reject symlinks and paths outside the configured root.

`ChunkReplicator` bounds plaintext memory, compresses before encryption, gives
every chunk a fresh envelope data key, and verifies each immutable object in
both repositories before returning its manifest reference. Every materialized
attempt gets an immutable namespace below `objects/<generation>/`; retries
authenticate the same fixed single-layer manifest and repair a missing copy
without recapturing source rows. Failed attempts may leave unreachable
immutable chunks, but they cannot enter a checkpoint and are later eligible
for Generation GC.

`PartitionPlanner` first obtains a Slot snapshot whose commit and durable apply
indexes match, then pages Channel runtime metadata directly into compact
source-node fences without retaining a duplicate full metadata slice. A second
snapshot must preserve the same Slot/term/index fence or the attempt is
discarded. The compact plan has a hard per-hash-slot Channel limit; metadata
and message payload bytes remain streaming.

## Restore And Retention

The checkpoint restore importer is the only installation path. Admission
proves target emptiness, walks both repository copies of the signed hash-linked
catalog from the operator-supplied immutable head to the exact checkpoint,
authenticates every reachable baseline/segment/cursor envelope and payload
metadata in both failure domains without payload download or data-key opening, and freezes
the latest dual-committed erasure snapshot. The runtime then routes
each Hash Slot only to its current target
Leader under the durable `(Slot ID, Leader term, config epoch, attempt)` fence.
Checkpoint proof validation, replica transfer, final verification, and
activation cleanup use bounded worker cohorts with fixed `pkg/goroutine` task
identities. Dynamic Slot, node, and checkpoint values remain ordinary work
items rather than supervisor labels.
That Leader revalidates the selected catalog copy, downloads and decrypts each
segment once into a shared-budget `0600` staging file, replays baseline and
ordered continuous segment records chronologically, and computes content, typed-count,
Channel-boundary, and message-Merkle evidence during the same pass. Finalize is
the only operation that makes the disposable install durable: it first applies
the pinned erasure boundaries, exports one authenticated plaintext target
snapshot, installs it locally, and streams bounded resumable chunks to the
current desired replicas. Each receiver rechecks current Slot authority,
persists offsets and a completion receipt, fully parses every file before live
writes, installs metadata/messages/erasures, and verifies canonical metadata,
the reconstructed Channel boundary index, and deterministic live message
snapshot bytes/counts without repository or key-authority access. A failed live install
deletes its partial metadata, message, erasure, and runtime state; if its
boundary index is unreadable, cleanup falls back to streaming and deleting the
whole restore-only Hash Slot catalog. Live bytes are re-exported before the
first receipt is written, and every long verification is followed by one final
Slot-fence check. One app-owned staging quota covers the common node root for
source downloads, target scratch, replica transfers, exports, and receipts.
Source writes use path claims instead of walking the retained tree per object.
Startup and explicit diagnostics scan the complete root; normal claim,
settlement, and deletion update cached usage while scanning only the affected
path. Target and receiver share an attempt-scoped lock, while a target-only
singleflight spans the complete replay session. The shared lock is released
before Leader-local replica distribution and reacquired for receipt mutation.
Receiver Begin durably describes and claims the complete attempt capacity
(rehydrated after restart), while each target Slot claims a bound derived from
its signed source objects so independent Slots can install concurrently when
the node budget permits. A promoted Leader can replace the same attempt-path
claim left by a partial follower transfer. Startup removes only crash-orphaned
transient source `.stage` files; semantic attempts and receipts remain available
for failover. Completed boundary evidence opens read-only during status and
invalidation, so verification cannot grow retained staging outside a claim.
Follower transfers retry from durable offsets and surface
persistent convergence failures while preserving and publishing partial
convergence evidence.
Completion identity
excludes volatile Leader/term/configuration fields, so a promoted follower can
adopt the same semantic receipt, rebind the current fence, and converge missing
replicas without sequence regression. Final verification queries every current
desired replica; each status call revalidates live target state, not only the
receipt. Permanent-erasure events replay one commit at a time into the
session's disk-backed evidence index, avoiding million-entry key lists or
in-memory Channel maps.

Activation cleanup is a separate target-wide barrier after immutable activation
evidence is durable. The cleaner resolves current placement for every Hash Slot
and sends an exact cleanup request to every current desired replica with bounded
parallelism. Each receiver revalidates its local Controller mirror, plan,
checkpoint, manifest, partition, Slot, Leader term, config epoch, attempt, and
convergence evidence before settling the quota claim and deleting only that
attempt subtree. A crash leaves the plan in `activating`; the same request can
repeat cleanup idempotently, while a different plan or stale fence is rejected.

Source-fence convergence reads only Controller node-health evidence. Every
active or leaving data node must report the fence revision and
`runtime_ready=false`; removed or non-data nodes do not block the barrier.

Before ordinary retention metadata advances, `PermanentErasureLedger` encrypts
the Channel identity and boundary, publishes identical immutable ciphertext and
signed record bytes to both repositories, reserves the next sequence for the
event's Hash Slot, and publishes a predecessor-linked signed commit marker plus
deterministic per-event receipt to both repositories before advancing the
Controller head. The still-pending reservation prevents a later same-Slot event
from overtaking that crash window. A retry that sees a valid receipt ahead of
its local Controller mirror authenticates the exact sequence commit and record,
then reconciles that original head through the authoritative Controller commit
operation instead of misclassifying replication lag as corruption or allocating
a duplicate sequence. The first immutable
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

The continuous path collects at Generation granularity. Its protection set is
built from authenticated retained checkpoints, operator-held checkpoints, the
active restore checkpoint, every current Slot frontier, and any auditor-frozen
Slot. A pending replacement target is protected until promotion or durable
rotation makes it unreachable. A Generation is eligible only when that Slot
has a different current successor; retained and frozen Generations are never
swept. Signed segment commits authenticate payload ownership independently in
each repository copy. A payload with no commit in that same copy is an ordinary
safety-window-protected orphan for that repository.

Each repository has one independent CAS-fenced `GenerationGCCursor` in
Controller state. The cursor stores only repository, cycle, fixed cutoff,
lexicographic position, completion, and revision—never pending object
identities. Before sweep, vector cache misses consume the same repository
request budget and populate a rebuildable, content-addressed node-local cache.
The cache is durable across process restart; if a small budget cannot load all
unique protected vectors in one call, later calls skip authenticated cached
copies and finish the phase instead of restarting from the first checkpoint.
Each repository has a separate cache namespace and failure domain. Before a
copy is marked complete, its cache prunes content IDs absent from the fixed
protection decision; old local vectors therefore remain bounded and are always
rebuildable. File and OSS adapters then return one cursor-based bounded
page, and the collector enforces
the remaining request budget plus the configured deleted-byte budget before
durably advancing that copy. A completed copy is not rescanned while its peer
retries. Object Lock rejection stops only the affected copy at the exact key,
and a later run resumes there after retention expires. The fixed cutoff combines
Object Lock with an additional safety window for in-flight immutable
publication.
After every collection attempt, GC debt is refreshed by reloading the
Controller cursor state, so a persisted incomplete cursor is visible while a
per-repository result before cursor creation cannot invent debt. If that
durable reload fails, the prior observation is preserved.

Continuous integrity audit uses `ControllerIntegrityAuditStateStore` to update
only the independently revisioned audit cursor, sorted per-Slot health, and
short-lived durable GC exclusion guards while
retrying unrelated Controller state conflicts. `SegmentIntegrityAuditBackend`
binds an immutable catalog plan's opaque position to one exact portable graph
node, then delegates full signature/ciphertext/decrypt/plaintext validation and
exact-copy repair to the segment, partition, or permanent-erasure adapter. File repair
atomically replaces the development copy. OSS repair publishes a new
Object-Locked/ObjectWorm-protected current version without weakening ordinary uploads;
Generation GC lists and deletes a bounded number of repair versions under the
same provider-request budget.
