# Repeated message-WAL delay reproduces the occupied-shard EOF

A same-binary diagnostic pair establishes that delayed message-WAL completion
is sufficient to produce the observed gateway failure mechanism. The zero-hold
control completed 270,000 SENDs. Padding real message-WAL sync completion to
380 ms, starting at measured second 25, produced EOF at 26.595 seconds. The
first shard rejection occurred 1.594 seconds into the five-second intervention.
No production repair or physical host-device diagnosis is claimed.

This follows the [natural shard timeline](2026-09-27-send-ban-shard-timeline.md),
which captured slow WAL operations, an occupied ordered batch, and exact queue
accumulation. The intervention distinguishes sufficiency from correlation; it
does not explain why the original environment produced slow syncs.

## Frozen experiment and real durability

`assets/send-ban-sync-window-20260928/contracts.md` fixed the full pair before
execution: control then treatment, fresh three-node fixtures, 256 Hash Slots,
5,000 Channels, 4,500 SEND/s and a 60-second request. Both arms use the same
product and harness binaries, 128 Store append workers, 32-record SEND batches,
512 gateway shards, 256 queued records per shard, and unchanged permission,
ordering, replication and durability rules. There were no concurrent builds,
trace analyses or cleanup during SEND.

The isolated Pebble copy changes only Linux `SyncData` interception and adds
the private helper/tests. The original dependency is untouched; the recursive
module comparison records exactly these three differences. Only direct
`messages/*.log` files match. Metadata, SST, directory and `SyncTo` operations
are unchanged. Each intercepted call executes the real `Fdatasync` exactly
once, preserves its error, then pads total duration to 380 ms. Already slower
real operations are never shortened. Zero hold retains the same instrumentation.

An atomic control file arms a five-second window at measured +25 seconds. Each
process anchors that window once to its monotonic clock. The watcher is bounded
to ten minutes and 4,096 control bytes. Up to 128 completed operations and 16
active operations are recorded, with explicit eviction/overflow. There is no
per-operation file write. Active operations have no invented completion time.

Failure contracts and tests preceded the helper: archived RED failed because
its APIs were absent; GREEN and race passed. Existing Pebble syncing-file and
sync-range tests plus the new contracts passed against the copied module.
The unchanged gateway recorder was already validated by its prior tests.
Ten reused offline-analysis contracts passed. Small 25-Channel / 15-second
tool checks yielded zero-hold PASS (67,500 SENDs) and held EOF (2.603 seconds).
Those tool checks are not full-cardinality qualification.

## Full-cardinality pair

| Arm | SEND calls | Measured time | Outcome | Pending | Permission busy |
| --- | ---: | ---: | --- | ---: | ---: |
| `control-01`, 0 ms | 270,000 | 60.011 s | PASS | 0 | 0 |
| `window-380-01`, 380 ms | 119,677 | 26.595 s | EOF | 6,915 | 0 |

The control's SENDACK P99 was 65 ms. The failed arm's partial P99 (126 ms)
is not a whole-window performance comparison: most observations preceded the
intervention, and unfinished work has no completed latency sample.
Minimum sampled free space was 52,775,219,200 / 53,381,332,992 bytes. Neither
arm hit the disk guard or OOM; after each run only the container's idle sleep
remained. Four complete process traces and an error-free kernel window were
captured per arm; compressed originals, decoder results and identities are kept.

All nodes loaded the control about 24.93 seconds before its scheduled start.
The treatment captured four completed held calls plus one active call on each
node, without overflow or eviction. Completed total durations were
380.006–381.276 ms; actual sync durations were 0.650–42.688 ms. The remaining
time was deliberate completion delay, rather than an invented slow disk syscall.
The control recorded 2,391 / 2,388 / 2,397 completed eligible operations; its
bounded ring explicitly evicted older records and inserted no delay.

## Exact occupied-shard accounting

Only node 2, shard 7 rejected in the full treatment. It had one session:

| Observation at first rejection | Value |
| --- | ---: |
| Active batch | 32 records |
| Queue when that batch began | 38 records |
| New admissions during that batch | 218 records |
| Queue at rejection | 256 records |
| Balance residual, `256 - 38 - 218` | 0 |
| Active age, wall-clock observation | 1,212.241 ms |
| Admission rate over that interval | 179.832 records/s |
| Reserved but unfinished | 288 records |
| Largest gap between retained completed batches | 2.547 ms |

The current handler was occupied, not waiting for a lost scheduling wakeup.
Aggregate queue depth was only 1,979 of 131,072; that spare capacity cannot
make an ordered session's blocked batch complete. The retained completed-batch
window served 144 records/s and excludes the still-active 1.212-second batch.
No queue expansion, larger worker pool or relaxed timeout follows from this
evidence.

Frozen gateway admissions total 119,675; adding its one rejected admission
leaves one of the driver's 119,677 calls unaccounted for by these snapshots.
Do not claim whole-run exact transport-to-gateway accounting. The rejected
shard's local queue arithmetic is exact and independent of that difference.
Handler completion itself is not proof of durable delivery.

## Native and kernel evidence

Native stacks separately identify injected sleeps in `executeWindowSync` and
real `Fdatasync` calls. Completed pre-rejection injected sleeps reached about
379 ms. Gateway waits for `Router.submitResolvedGroupsEach` append results
reached 1,131 / 1,142 / 1,136 ms across the nodes. These are not permission
planning waits. The summaries retain unfinished states as censored observations;
they do not assign a native goroutine to a unique gateway shard or message.

Five complete kernel intervals in the preceding 1.5 seconds cover 1,249.647 ms:
I/O full stall was only 0.203%, CPU throttling increment zero, and memory
some-stall increment zero. The treatment therefore reproduces the queue failure
without requiring the natural run's high kernel I/O-stall percentage. It does
not show that the original natural stalls were unrelated to storage.

Observed wall/trace offset ranges span about 0.2214 ms per node. These are not
certified clock-error bounds. The helper's durations and eligibility use monotonic
time; cross-process window/rejection joins and active-age estimates use wall
timestamps. The 1.59-second window placement and integer queue balance do not
depend on sub-millisecond ordering claims.

## Conclusion and remaining work

The falsifiable prediction was met at both tool-check and full-cardinality
scales: repeated slow durable completions keep a session batch occupied while
normal arrivals fill its bounded queue, then gateway overload protection closes
the connection. This is evidence for the failure mechanism, not an unconditional
claim that queue protection is a product bug or that every historical EOF has
the same cause.

The next unresolved question is below that chain: which storage/kernel/host
condition causes the natural long syncs, and whether there is an avoidable
product-side amplification. A useful next observation must distinguish those
possibilities; repeating synthetic sleeps alone cannot do so. Do not promote
the rejected compaction pacing experiment or change durability/queue limits as
a substitute for that evidence.

This is one fixed-order short diagnostic pair with separate fresh fixtures,
not randomized repeated qualification. Clean 30-minute R2 and three complete
fresh R6 pairs remain incomplete. Production source remains unchanged since
the earlier admission-handoff fix. Reproduction commands, source overlays,
tests, hashes, actual window records, compressed traces and derived results
are indexed by `assets/send-ban-sync-window-20260928/manifest.json`;
`summarize_pair.py` regenerates the checked pair summary.
