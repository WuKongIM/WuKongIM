# Natural EOF: the occupied SEND shard accounts for the full queue

The original5,000-Channel /4,500-SEND/s workload failed after1,340.3762024s
(22m20s), with6,031,693 SEND calls,6,796 pending messages and zero permission
admission busy. This run used original Pebble behavior: no compaction pacing or
injected sync delay. A private gateway recorder and the existing flight/kernel
observers were the only diagnostic additions. It is not a clean R2 qualification.

The new evidence explains the gateway closure mechanism: an occupied32-record
batch prevented its shard from taking another batch for about1.25s, while its
single session continued entering at about180/s. The residual queue plus those
arrivals exactly reached256. This distinguishes an executing/waiting batch from
a lost scheduling wakeup or a colliding sender shard. The physical origin of
storage stalls still needs separate evidence; no production repair is claimed.

## Bounded probe and validation

The copied source overlays only `pkg/gateway/core/async_send.go` bookkeeping,
a private helper, and flight-capture composition. It preserves512 logical
shards,128 workers,131,072 total queued items,256 items/shard and32 records/batch.
At most512 shards and8 executors/process are observable. Each active shard has
128 completed batch spans, admission/rollback/completion prefixes, at most32
synthetic session IDs, and its current batch. First rejection freezes that shard
before close/cancellation can overwrite the relevant history. No payload, UID or
client message identity is retained. Handler return means handler completion,
not proof of durable delivery. Timing/queue observations are not a single atomic
cut with admission; the analyzer exposes the residual instead of hiding it.

Failure contracts and tests preceded the helper. RED failed on its absent APIs;
GREEN and race passed. Tests cover ring eviction/order, snapshot aliasing,
first-rejection freeze including the active batch, concurrent admissions,
rollback, invalid overlap/time/order, shard bounds and session-ID overflow.
Existing gateway async-SEND/executor/drain tests also passed with the overlay.

`positive-01` completed67,500 SENDs at25 Channels /4,500/s /15s. Three nodes'
recorded admissions and finished records exactly matched24,300/21,600/21,600;
no active batch, rejection, overflow or invalid state remained. All four traces
passed scheduler extraction and full native decoding. This checks instrumentation,
not feature/performance acceptance.

## Original-load failure: exact rejected-shard accounting

`run-plan.json` froze the original30-minute request and binary identity before
starting. The run terminated naturally with EOF; no guard or OOM termination,
no parallel build/analysis during SEND. Minimum sampled free space was
49,407,361,024 bytes. All four native snapshots were complete and decoded in full:
1,788,323 harness events and4,948,931 /5,345,414 /5,072,213 node events. Kernel
observer source errors were empty. Only the owned container's idle sleep remained.

| Node / shard | Queue when active batch began | New admissions while it stayed active | Queue at rejection | Active elapsed (wall clock) |
| --- | ---: | ---: | ---: | ---: |
|1 /4 |31 |225 |256 |1,243.807ms |
|1 /10 |29 |227 |256 |1,257.394ms |
|2 /4 |29 |227 |256 |1,259.948ms |
|3 /6 |28 |228 |256 |1,259.351ms |

Every row has one unique session,32 active records and288 reserved-but-unfinished
records (32 active +256 queued). All four queue-balance residuals are exactly
zero. Rejection reasons are `shard_full`; aggregate queue snapshots were only
2,184 /1,955 /1,923 out of131,072. Global spare capacity did not unblock the
ordered shard. Public close counters agree: node1 two `async_dispatch_queue_full`,
node2 one, node3 one. Frozen admitted counts across nodes total6,031,689; adding
four rejected admissions accounts for all6,031,693 driver SEND calls.

All25 active shards had exactly one session. Last128 completed batches on the
rejected shards span5.75–5.93s, serving169.74–170.45 records/s (the current
unfinished1.25s batch is excluded from that completed-window rate). Maximum
between-batch gaps were3.86–5.87ms. Thus, the critical recorded gap is not a shard
waiting1.25s to be scheduled: its handler was already active. This does not assert
that CPU scheduling never contributed to any part of the workload.

Measured admissions during those active spans were180.17–181.05/s. The driver's
last100ms bins have18–19 calls per sender (14 in the final partial bin), no socket
write error and no large final write delay. The evidence does not require an
unobserved sender burst or session hash collision to explain the full queues.

## What the active work was waiting for

The last native clock snapshots have an observed wall/trace-offset change of
about4.383ms on all three nodes. The join retains that range and uses an integer
upper-median offset; completed-before-rejection checks conservatively use the
largest observed offset. This is not a certified error bound. Active-batch ages
and admission rates in the table use wall timestamps, so their millisecond-level
precision must not be confused with the monotonic completed-span durations. The
roughly1.25s occupancy conclusion and exact counter arithmetic do not depend on
sub-millisecond cross-source ordering. No particular clock-adjustment cause is
attributed.

Native gateway waits point to `Router.submitResolvedGroupsEach`, reached through
`SendBatchEach` and `OnSendBatch`: message append results, not permission planning.
Retained completed gateway append waits before the first rejection reached
932.10 /927.09 /944.31ms; node WAL `Fdatasync` maxima were385.60 /386.46 /385.60ms.
SST compaction sync maxima were671.45 /532.79 /848.19ms. These are native syscall
wall durations including OS scheduling, not physical disk measurements.

The first rejection provides the analysis cutoff, rather than post-close logs.
Five complete kernel intervals inside its preceding1.5s cover1,245.718ms:
I/O full stall65.84%, CPU throttling increment0, memory some-stall increment0.
This supports a storage-dependent service deficit and excludes those two
resource-pressure counters as explanations for that exact window. Whole-VM
block-device counters are not exclusive physical-host-device evidence.

Completed-only summaries cannot describe all active work. A test-first extension
now retains bounded unfinished Waiting/Syscall states as explicit censored lower
bounds. Four contracts passed after the expected missing-module RED. It leaves
the old completed-only output intact and produces `.open.analysis.json` files.
Open WAL/commit/gateway waits remain visible; they must not be represented as
completed durations or extended beyond the first-rejection cutoff. There is no
exact per-message or native-goroutine-to-shard identity join in this recorder;
process stack evidence and exact shard accounting have different scopes.

A configuration check also rules out a misleading shortcut: the repository's
Linux `BytesPerSync=0` is normalized by Pebble v2.1.4 `EnsureDefaults` to512KiB.
It does not disable periodic SST sync. Its `SyncTo` remains nondurable range
writeback; the final real `SyncData` remains required. Do not select a fix on the
false premise that Linux had no background syncing.

## Next falsifiable step and qualification status

Use the proven message-WAL interception seam for a bounded repeated completion
hold, with a same-binary zero-hold control. Preserve every real Fdatasync/error,
limits, ordering and input rate. A proposed380ms floor for5s, armed after25s SEND,
is within this natural WAL-duration evidence and predicts sustained service below
180 records/s per ordered sender, with rejection during the hold window. Record
actual operations and exact active-shard growth; no failure near the injected
window makes that prediction unproven. This intervention tests sufficiency of
storage completion delays, not why the original host/kernel produced them.

No queue expansion, timeout relaxation, stale policy read or weaker durability
is justified by this report. R2 and three complete fresh R6 performance pairs
remain incomplete. Artifacts, red/green/race output, overlays, binaries, commands,
raw compressed traces and derivations are in
`assets/send-ban-shard-timeline-20260927/manifest.json`.
