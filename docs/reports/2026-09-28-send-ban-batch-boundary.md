# Batch-cap intervention does not remove the WAL-stall failure

Both fresh same-binary diagnostic arms failed under the same bounded message-WAL
completion hold. Raising the active gateway batch cap from32 to128 did not remove
EOF. No production change follows from this pair, and neither arm qualifies the
original32-cap R2 or the separate R6 comparisons.

| Batch cap | Measured SEND time at EOF | SEND calls | Pending | Permission busy |
| --- | ---: | ---: | ---: | ---: |
|32|26.428919s|118930|6573|0|
|128|27.142041s|122138|9426|0|

## Frozen intervention and checks

The plan preceded execution: cap32, then cap128; each requests5000 Channels,
4500 SEND/s and60 seconds, with real message-WAL completion padded to380ms
only during measured seconds25..30. The validated sync-window product, harness
and observer hashes matched before and after both runs. All real sync calls still
occur exactly once and retain their errors. Only batch cap and isolated artifact
paths differ. No build, parsing or cleanup overlapped either measured workload.

All six node snapshots confirm512 shards,128 SEND workers,131072 aggregate queued
records and256 queued records per shard. The batch caps are32/128 respectively;
there is no recorder overflow or invalid record. Increasing the active batch cap
explicitly increases possible active records even though queued capacity stays
fixed. This intervention must not be described as preserving total outstanding
capacity. At the128-arm rejected shards,128 active +256 queued =384 unfinished
records. No suggestion to increase queue limits is made.

The32 arm captured three completed held syncs and one active sync per node;
completed totals were380.023–380.588ms, with actual syncs1.695–32.736ms.
The128 arm captured five completed and one active per node; completed totals
were380.014–397.875ms, with actual syncs2.983–28.332ms. The artificial sleep is
separate from the real syscall; its overshoot is retained. This is a completion-
delay experiment, not evidence that physical storage took380ms.

Both observers exited0 and all eight flight captures were complete. Scheduler
extraction, full native parsing and gzip roundtrip checks passed for all eight.
Ten reused offline parser contracts passed. Host free space minima were
49,729,294,336 and48,926,199,808 bytes. Neither run had OOM or a guard termination;
only the task container's idle sleep remained after each runner ended.

## Exact shard accumulation

The32 arm rejected node1/shard8 and node2/shard7. Each had one session and an
empty queue when its active batch started. The active batches contained only
15 and18 records, with ages1.422507s and1.425966s at rejection. Exactly256 new
admissions arrived while each batch remained occupied, filling the256-item
queue. The first rejection was1.428330s after the hold window began. These
batches were not capped at32, so a simple saturated32-record throughput estimate
does not explain their individual failure.

The128 arm rejected five shards. Every rejected shard had128 active records.
Two began with12 queued records and accepted244 more over1.355–1.356s; three
began with81 queued records and accepted175 more over0.976–0.977s. Each sum is
exactly256 with zero observational balance residual. The first rejection was
2.140527s after hold start. Each affected shard had one session and observed
arrival rates near180 records/s. Maximum retained between-batch gaps were below
4.8ms in both arms, not comparable to the occupied-batch durations.

Failure clocks and queue observations are not atomic across processes. Exact
local arithmetic does not uniquely associate a native goroutine with a shard.
The later rejection and larger pending value in one sequential128 trial do not
establish a reproducible performance benefit or regression magnitude.

## Native waits and interpretation

Completed pre-rejection gateway append waits reached roughly775–781ms across
nodes in the32 arm; in the128 arm they reached1137ms,783ms and1139ms (node2 also
had an unfinished observed interval of932ms). These are individual wait spans,
not full handler durations: result arrivals can wake and re-block a handler
multiple times before its whole batch completes. Top-N summaries also censor
unfinished waits and cannot supply a per-message critical path.

The final retained pre-rejection kernel windows had I/O-full fractions0.064% and
0.184%, with zero CPU-throttle and memory-pressure deltas. Artificial holds
reproduce backlog without long physical I/O stalls in these windows; they still
do not identify the cause of earlier natural stalls. Scalar pressure and the
largest individual wait must not be substituted for dependency evidence.

Source inspection found two boundaries: gateway dispatch joins the whole batch;
the commit coordinator completes synchronous commit/publication before collecting
the next batch. Pebble's WAL flusher also snapshots waiters before its synchronous
write/sync round. Therefore moving the coordinator's wait to another goroutine
is not yet a demonstrated remedy. Raising batch cap alone has now failed its
proposed diagnostic outcome. Inspect append/quorum phase dependencies next,
keeping fresh authorization, same-Channel order, ordered ACKs, bounded ownership
and durable-before-success semantics. Do not rerun unchanged qualification just
to obtain a favorable sample, or promote this intervention to production.

Artifacts: `assets/send-ban-batch-boundary-20260928/manifest.json`.
`verify_window.py`, parameterized `verify_shards.py`, and `join_evidence.py`
retain the hold checks, exact queue arithmetic and trace cutoff limitations.
All prior natural failures and the clean R2 failure remain authoritative.
R2 and three complete fresh R6 pairs remain incomplete.
