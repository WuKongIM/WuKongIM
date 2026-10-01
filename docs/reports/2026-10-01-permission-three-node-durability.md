# Ordinary sequential SEND: three-node durability waits

**Comparative acceptance remains FAILED: 6/12 original sequential rows.** This
follow-up diagnoses one preselected request; it changes no candidate Go code,
performance threshold, original run, or release gate. The frozen candidate is
`ec21c1a586941ffd74cfb497f0da4522941bc1a4`, old runtime is
`424d03eb298b972ec572261d617972eecb5523c5`, and driver is
`c0e95b5a45babbd1907c590a9dcdab83e74634a9`.

## Fixed experiment and results

Both runs preserve the full four-placement prefix, 32 connections, 64 sequential
and 64 burst SENDs per placement, 32 ban and 32 unban controls, exact 161-message
histories, 256 hash slots, 12 physical Slots, three message replicas, one metadata
voter, and GOMAXPROCS=4. All eight placement cases pass. The one-voter metadata
fixture does not qualify HA or metadata quorum cost.

The target is `two-remote-leaders-c1-030`, selected before execution, rather than
the observed maximum. Public runtime placement proves Channel Leader node 1 and
ingress node 3. Each owned node receives one four-second runtime trace request
with a six-second/16-MiB bound. After the target phase, each node receives one
two-second/64-KiB/32-event diagnostics query. All six traces and six queries
complete within bounds; the two owned temporary directories are empty and
removed. The exact-target debug rule is 100% for 120 seconds; ordinary sampling
and all fixture controls remain unchanged.

| Diagnostic observation | Old | Candidate |
| --- | ---: | ---: |
| Target SENDACK | 8.037 ms | 20.019 ms |
| Leader quorum-task interval (`local_durable`) | 4.825000 ms | 16.950084 ms |
| Leader message coordinator completion wait | 4.656640 ms | 16.787520 ms |
| Leader Pebble commit-completion wait | 4.097984 ms | 16.226688 ms |
| Early Leader quorum exchange wait, temporal candidate | 4.790144 ms | 9.180480 ms |
| Node 2 exchange handler's message coordinator wait | 4.704128 ms | 9.064768 ms |
| Node 2 Pebble commit-completion wait | 4.105216 ms | 8.463424 ms |
| Node 3 overlapping exchange handler's coordinator wait | 7.756032 ms | 19.838080 ms |
| Node 3 overlapping Pebble commit-completion wait | 7.163776 ms | 19.242624 ms |

These are nested or concurrent intervals and must not be added. Each observed
Pebble waiter is awakened by its WAL log writer's `flushPending` /
`pendingSyncs.pop` path. Exact Pebble v2.1.4 `commitPipeline.publish` waits on
`b.commit`, which gates ordered publication and WAL sync; its entire waiting
duration is not a measurement of one `fsync` system call. The observed log-writer
wakeup identifies the final dependency without claiming exclusive syscall time.

In this candidate capture, the early exchange returns about 9.22 ms after the
Leader interval begins; the local coordinator result arrives about 16.93 ms
after it begins. Local completion therefore arrives later. In the old capture,
local completion precedes the early exchange result. The preceding single-node
capture of this same ordinary request instead had peer completion arrive last.
The required local-plus-voter join can change which durability path controls
completion; `local_durable` is the whole quorum task interval, and the following
`quorum_wait` event is not the complete peer replication cost.

Node 3's overlapping exchange remains pending beyond the selected request's
quorum completion. It must not be attributed to that request's required quorum
wait merely because the wall-clock intervals overlap. Runtime traces contain
function stacks and goroutine identities, but no RPC arguments or proposal
identity. Pairing the early Leader exchange with node 2's handler is a temporal
inference. Neither follower's runtime stack certifies exact request identity,
batch priority, or the complete sender queue residence time.

## Hypotheses and limits

The ranked hypotheses were recorded before execution:

1. Follower synchronous storage dominates the foreground wait. The temporally
   paired follower handler spends most of its interval awaiting commit, but
   this candidate request finishes after the longer local storage path. A
   universal follower-dominance explanation is contradicted by this capture.
2. Follower receive/scheduling delays storage entry. The observed node 2 commit
   wait begins near the Leader interval's start. Its relevant runnable spans
   are short; no multi-millisecond delay is established here. Exact transport
   receive-to-admission time remains unobserved.
3. Same-Channel background work blocks the foreground sender. The early
   Leader exchange wait begins near the interval's start. No multi-millisecond
   foreground queue delay is established in that temporal candidate. Queue
   identity and other requests remain unresolved.
4. GC or scheduling delays the critical path. A full-stream range scan finds
   no GC or stop-the-world range overlapping the anchored interval on any of
   the six captures. The maximum observed relevant runnable overlap is
   2.816–12.288 microseconds. This does not explain other requests, earlier
   ingress dispatch, or original whole-node CPU regressions.

No selected transition contains `rotateWAL`; all selected transition lists and
active-span lists are below their declared caps. Ordinary commit waits therefore
also occur outside the WAL-rotation boundary diagnosed in the companion report.
The runtime clock snapshots map each node to the exact Leader diagnostic span.
Measured offset spreads are 45.688–49.752 microseconds, with microsecond event
timestamp rounding; these are observations, not a certified clock-error bound.
Do not infer sub-millisecond cross-node ordering from the mapping.

New-trace decoding overlapped old fixture setup and completed before the old
target-phase capture. The summary retains that timing evidence. Profiling,
diagnostics, periodic ownership sampling, shared-host activity and all other
overheads belong to these diagnostic timings. These two runs cannot establish
a performance improvement, explain the original CPU regression, or replace
the original unprofiled comparison. Replay of the original immutable archive in
a new extraction still exits 1 with exactly six sequential failures.

## Evidence and replay

The full local archive contains all six raw runtime traces, bounded full
analyses, raw E2E reports and timelines, plans, collector/parser/verifier sources,
source instruction digests, failed preparation receipts and final checks. The
bounded public archive contains the raw functional reports, safe timelines,
plans, checks, scripts and complete selected wait begin/end events; it omits
raw runtime traces and full parsed analyses. Its verifier checks retained facts
and hashes, but those excerpts alone cannot independently certify entire-capture
coverage or absence of GC/rotation. Both scopes and hashes are explicit in the
archive receipt. Earlier archives remain unchanged.

Use an explicit Go 1.25 trace reader. The system Go predates the trace format.
For a full archive extraction, decode each `{old,new}.node-{1,2,3}.trace` with:

```sh
/absolute/go1.25/bin/go tool trace -d=parsed old.node-1.trace > old.node-1.trace.parsed.txt
/absolute/go1.25/bin/go tool trace -d=wire old.node-1.trace > old.node-1.trace.wire.txt
python3 analyze-three-node-trace.py old 1
```

Repeat for all six traces, then run `summarize-three-node-trace.py` and
`verify-three-node-evidence.py`. The summary reuses the recorded host-decode timing receipt, so extraction
file mtimes do not affect replay. `verify-three-node-evidence.py --bounded` verifies a bounded archive
without pretending that missing raw traces were checked. The collector is
host-specific and requires the frozen binaries/driver; the analyzer and verifier
use files in their own directory. Restored tooling came from the original cached
Go 1.25.0 archive after verifying its module `h1` checksum. No E2E observation
started during failed preparation; no observed run was replaced or retried.

The next diagnostic needs exact proposal/priority correlation and a split of WAL
write, sync completion and request cadence. Any instrumentation must be bounded
and tested against failure cases before implementation. A product repair still
needs the original unprofiled p99/whole-node CPU comparison and unchanged Linux
500 SEND/s gates; the observations here justify no durability relaxation.
