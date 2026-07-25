# Backup Runtime Flow

## Responsibility

`internal/runtime/backup` runs bounded node-local capture work and the
Controller-Leader background loop. The loop executes scheduling decisions
injected by `internal/app`, resumes cluster jobs, and dispatches logical
partitions; policy rules remain in `internal/usecase/backup` and top-level
restore-point publication remains behind the use-case port.

```text
fenced CaptureRequest
  -> PartitionSource.OpenPartition
  -> one pinned logical-partition session and committed cut
  -> metadata stream -> bounded chunk replicator -> primary and secondary
  -> committed-message stream -> bounded chunk replicator -> both repositories
  -> cumulative record counts and message-ID fence
  -> strict partition manifest -> immutable publication in both repositories
  -> bounded PartitionReport returned to the coordinator
```

The source owns consistency and retention pins. Payload streams are never
accumulated as a whole logical partition: only the injected replicator's
bounded chunk, object-reference list, and a compact Channel-fence plan capped
at `maxBackupChannelsPerHashSlot` are resident. A partition manifest is
published only after both logical streams have replicated successfully.
Before recapturing a missing Controller report, the worker loads the fixed
partition-manifest key. A verified existing copy repairs its missing repository
replica and reconstructs the same bounded report without reopening the source.

The Controller Leader coordinator resumes missing partition reports from
Controller state, runs backup doctor checks without changing message
readiness, publishes only complete jobs, applies reference retention, retries
dual-repository garbage collection while forwarding every per-Slot
Controller-pending erasure reference into its protected mark set, and performs
a daily remote audit. A durable pending/running verification task is resumed before retention
or new scheduling after Controller Leader failover; scheduled audits first
create that task, then persist per-restore-point success or bounded failure
evidence. Verification and backup capture never run concurrently. The
restore coordinator similarly resumes missing installs only on the Controller
Leader and bounds concurrent logical partitions.

## Continuous Capture Foundation

The replacement capture path is implemented beside the current job runtime but
is not composed into production entrypoints yet.

```text
in-memory Channel commit hint -> CaptureEngine.Wake (bounded, non-blocking)
periodic poll / process start  -> enqueue every configured Hash Slot
bounded Slot workers
  -> acquire/reuse durable Slot capture lease for exact local Raft authority
  -> acquire/refresh its node-local source-log compaction pin
  -> load durable SlotFrontier
  -> observe metadata and message source high watermarks
  -> page committed source logs through the pinned positions
  -> roll non-empty SegmentBatch values by size or open duration
  -> ReplicatedSegmentStore.Commit
  -> one lease-and-revision fenced SlotFrontier compare-and-swap for both stream heads
```

Wake hints never call source storage, repositories, KMS, or a backup outbox.
They may be dropped when the bounded hint queue is full because the initial and
periodic paged high-watermark reconciliation is the correctness path. The
same logical Hash Slot source contract is used for a single-node cluster and a
multi-node cluster; no deployment-mode branch bypasses cluster semantics.

Metadata and messages have independent segment sequences inside one Slot
Generation. Each message payload segment is committed first, then a separate
cursor-only sidecar is committed; the final Slot frontier CAS exposes both
references together. Cursor sidecars carry Channel deltas and every 1024th
sidecar carries a complete checkpoint, bounding repository/KMS reads after
restart. A source page with no records emits no object; an entirely idle source
also avoids a Controller rewrite. A metadata watermark advances only when the
logical Hash Slot has a matching applied command, so unrelated traffic in the
shared physical Slot does not create cursor-only Controller updates.

The default rolling policy targets 64 MiB, rejects plaintext above 256 MiB, and
seals a non-empty sparse segment after 30 seconds even when that requires
retaining the accumulator across reconciliation cycles. A dynamic deadline
timer wakes the exact Slot independently of the slower correctness poll.
Pending accumulators are fenced to the durable payload head, cursor head, and
source cursor; restart safely rereads from the durable cursor. Each message
observation fixes exactly one Channel boundary in the opaque cut cursor, so a
hot Channel cannot consume work attributed to another observed Channel.
After a sparse accumulator is sealed, an older observation cannot reopen the
stream behind the newly committed local frontier. If the final Slot frontier
CAS fails, the runtime invalidates disposable source scan state so the orphaned
cut is rediscovered from the unchanged durable frontier.
Completed exact cuts may share one rolling accumulator; their Channel deltas
remain in the immutable sidecar rather than Controller state. Each source page
reports the exact represented `NextPosition`, so a size-rolled partial segment
advances only through its own rows. The opaque message cursor retains the
original Channel target, allowing restart to finish that cut before observing
a newer commit. When cuts from different Channels share a segment, its source
time remains the earliest observation in the group; it never claims coverage
after an earlier Channel boundary that was not re-observed. Message segments
also inherit the prior stream time across rolls; a later rebase may advance
that conservative chain-wide time only after it proves a complete new cut.
These values, page records, pages per reconciliation quantum, poll interval,
and worker count are runtime options. The default quantum yields after eight
pages; a remaining hot Slot is requeued at the worker queue tail.
Paged discovery exposes a separate immediate-continuation hint, so workers
tail-requeue unfinished discovery without busy-looping sparse accumulators that
are only waiting for their rolling deadline.

Before `ReadPage` may materialize plaintext, the runtime reserves the target
page, record/cursor structure overhead, and one legal oversized record from one
node-level capture budget. After the exact page shape is known, its owned
records, framing, cursor map, and encoding copies are charged until the segment
commits or the accumulator is invalidated. The separate replicated-store codec
budget is entered only afterward. Immutable references carry their authenticated
plaintext size; full cursor-checkpoint load/decode, sorted output, and encoding
copies are reserved before allocation and remain charged through sidecar
commit. Repository-backed baseline and delta indexes stay immutable and use
binary lookup per Channel instead of rebuilding a full map for every cut. Slot
wakeups are coalesced, and budget
pressure never blocks a worker: the Slot is reported degraded and retried,
while an expired pending accumulator is sealed before another page is admitted.
Per-Slot reconciliation is serialized inside the process, and scheduler,
rolling, cursor-checkpoint, and validation logic remain in separate files.
`CaptureEngine.Status` returns sorted per-Slot frontier, watermark, lag, state,
and bounded failure evidence for later Manager/metrics adaptation. A Slot stays
`reconciling` while the bounded message discovery sweep has more pages, even
when the currently known numeric lag is zero; it becomes `idle` only after a
complete rotated sweep.

The durable lease binds `(SlotID, leader term, config epoch, holder node,
Generation, lease sequence)` to the same Controller record as the frontier.
Reacquiring unchanged Slot authority is read-only, so Controller Leader failover
does not change task identity. Slot Leader or placement changes increment the
lease sequence while preserving committed stream heads and the durable
source-pin age origin. A separate durable
`SourceSlotID` binds the metadata cursor to its physical Raft index space;
physical Slot remapping forces rebase before any old cursor can be read.
Pending accumulators
also carry the exact lease; takeover releases them and invalidates disposable
source scans before observing watermarks, forcing both layers to reread from
the durable frontier. The final frontier CAS revalidates current local Slot leadership and
the complete durable lease after uploads, leaving stale worker objects
unreachable for later Generation GC. Status exposes the lease and a fenced
state; metrics expose owned-Slot gauge plus takeover and fenced counters.

Each Hash Slot floor has a hard age and all floors on one node share a hard
physical retained-byte budget. Shared physical logs are counted once at their
minimum floor. Exceeding either boundary durably records a pending rebase for
only the selected oldest-floor Hash Slot, discards only that Slot's uncommitted
accumulators, releases its local floor, and builds an independent materialized
baseline.
Other Slot workers continue, and foreground SEND never enters this runtime.
While pending, the old Generation remains the public frontier and every retry
within one unchanged lease uses the same target Generation and epoch. Lease
takeover or an immutable cut that is already compacted durably rotates only
the disposable target, preventing endless retry of an unreadable baseline
while the old healthy Generation remains authoritative. Promotion happens
only after the partition and complete message cursor are committed in both
repositories. The new cut installs its replacement floor immediately after
snapshot creation, before repository upload, and promotion atomically installs
the references and resets the pin-age timestamp. Normal metadata advancement
also resets the age origin to the newly retained floor. Failure
keeps the pending record and last healthy frontier intact. Lost authority
fences promotion and releases the former Leader's local hold.

Generation compaction reuses this same single-Slot replacement path; it never
creates a cluster-wide synthetic full. Each durable stream frontier counts the
exact plaintext committed into its payload and cursor-sidecar segments. A Slot
starts replacement when delta plaintext reaches the smaller of its baseline
size and the configured threshold (64 GiB by default), when the Generation
reaches 1024 committed segments, or when its age reaches 24 hours. A shared
non-blocking node budget caps concurrent materializations plus estimated source
I/O and dual-repository network bytes before the old floor is released. The
injected cost planner measures a conservative upper bound from the exact source
snapshot; historical delta counters are not used as a proxy for full snapshot
cost. A replacement larger than a configured byte capacity is charged the
whole capacity and therefore runs exclusively instead of starving forever.
Budget pressure leaves the pending rebase and old public Generation unchanged
for a later retry. Materialization retries use the same immutable target and
epoch; an already committed baseline is loaded and revalidated without
recapturing source rows. The replacement becomes authoritative only after the
materialized partition and complete cursor baseline pass dual-repository
validation, an injected durable auditor attests the Generation, and the
lease-fenced promotion CAS resets the Generation counters and age.

The background integrity auditor advances one Controller-fenced transition per
call. Its backend fixes an immutable catalog/frontier cycle and returns one
opaque bounded artifact cursor at a time. `inspect` performs full independent
copy validation; a single bad copy is durably recorded as `degraded` before a
later `repair` step copies bytes, and `revalidate` must repeat the complete
check before the Slot becomes healthy. A dual-copy failure is durably frozen
as `rebase_required` before live-source availability is queried. The next step
requests a Slot-local rebase or records `failed` and advances the global cursor
so unrelated Slots still receive audit work. Only the affected Slot's capture
gate returns `ErrIntegrityAuditFrozen`; foreground SEND never calls this path.
Permanent-erasure nodes use the same Hash Slot isolation, but their synthetic
`erasure-ledger` Generation is intentionally unavailable to live-source rebase:
dual-copy loss therefore becomes `failed` while the saved continuation still
allows later Slots to be inspected.
`RunIfLeader` and the retrying `Run` loop execute work only on the current
Controller Leader. Dual-copy recovery observes the durable frontier instead of
calling a Controller-Leader-local capture engine: the owning Slot Leader's
normal worker consumes `rebase_required` and reuses `CaptureEngine`'s
materialized rebase. The audit cursor preserves the complete next phase and
remains frozen until the replacement Generation has passed validation and
promotion. Ordinary capture remains blocked until
the auditor durably records that new Generation as healthy. Every step
reprojects durable debt and last-success evidence, so a new Leader does not
start with empty gauges; an unchanged complete catalog cursor is a read-only
poll rather than a Controller Raft write.

Capture checks the projected gate before work, but correctness does not depend
on cache freshness: lease acquisition and the final frontier/promotion CAS read
the current audit state in the same Controller snapshot. A remote freeze that
wins while a long upload is running therefore leaves the uploaded object
unreachable and refreshes the local gate before retry.

Before resolving repository-backed watermarks, reconciliation first seals any
same-Slot accumulator whose open-duration deadline has elapsed and durably
commits that frontier in a separate CAS. This returns its shared-memory
reservation before a maximum materialized baseline asks for admission, so a
sparse pending segment cannot permanently prevent its own Slot from making
progress.
