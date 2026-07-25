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
dual-repository garbage collection while forwarding the one Controller-pending
erasure reference into its protected mark set, and performs a daily remote
audit. A durable pending/running verification task is resumed before retention
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
  -> load durable SlotFrontier
  -> observe metadata and message source high watermarks
  -> page committed source logs through the pinned positions
  -> roll non-empty SegmentBatch values by size or open duration
  -> ReplicatedSegmentStore.Commit
  -> one SlotFrontier compare-and-swap for both stream heads
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
plaintext size; full cursor-checkpoint load/decode, merged map, sorted output,
and encoding copies are reserved before allocation and remain charged through
sidecar commit. Slot wakeups are coalesced, and budget
pressure never blocks a worker: the Slot is reported degraded and retried,
while an expired pending accumulator is sealed before another page is admitted.
Per-Slot reconciliation is serialized inside the process, and scheduler,
rolling, cursor-checkpoint, and validation logic remain in separate files.
`CaptureEngine.Status` returns sorted per-Slot frontier, watermark, lag, state,
and bounded failure evidence for later Manager/metrics adaptation. A Slot stays
`reconciling` while the bounded message discovery sweep has more pages, even
when the currently known numeric lag is zero; it becomes `idle` only after a
complete rotated sweep.
