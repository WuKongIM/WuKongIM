# Controlled compaction-write pacing: full comparison rejects it as a fix

The complete `io-window/sustained-02` failure narrowed the competing hypotheses
to a node-write burst followed by kernel I/O stalls, overlapping slow WAL and SST
compaction syncs. This experiment tests whether smoothing compaction writes
changes that chain. It is not a product fix or completed performance acceptance.

## Single variable and preserved behavior

A private copy of Pebble v2.1.4 intercepts only `diskHealthCheckingFile.Write` and
`WriteAt`. Its existing `pebble-compaction` category is the sole pacing target.
WAL, memtable flush, metadata, real Sync/SyncData, actual bytes, offsets, return
values and errors are unchanged. A process-shared virtual-time reservation uses
16MiB/s with a512KiB burst allowance, versus zero-rate control in the exact same
binary. Separate files share one budget; sleep happens outside the mutex.
Reservation is conservative on partial/failed writes and does not fabricate IO.
Actual scheduling can affect achieved rate; the reservation is not claimed to
be a physical-device bandwidth guarantee.

Every process keeps successful compaction/other-write bytes, attempted compaction
bytes, reserved/actual waiting and largest request. One250ms sampler retains only
160 entries; the snapshot is written after the existing flight capture trigger.
The `total.unix_ns` field is unused/zero: totals are cumulative counters, while
sample timestamps and the enclosing capture identify observation timing. Actual
wait is summed across calls and includes scheduler delay, not exclusively sleep.
The histogram/counter overhead is present in both arms. The public product and
installed upstream Go/Pebble source are untouched.

Module parity checked1,208 upstream files: only `vfs/disk_health.go` changes,
with the private helper and two test files added. The module source/derived hash,
exact overlays, modfile, build commands and binary hashes are archived. The
existing frozen flight harness and external I/O observer are reused unchanged.

## Test-first and real-process verification

The failure contract, virtual-clock tests and in-memory VFS seam test preceded
implementation. RED failed on missing limiter APIs. GREEN and race passed after
implementation. Covered categories/disabled mode, common concurrent budget,
bounded idle credit, large reservations, rounding, monotonic deadlines, telemetry
wrap, exact Write/WriteAt data and offsets, partial/error result propagation, and
unchanged real SyncData error propagation. These validate the diagnostic tool;
they do not reproduce the product's sustained failure.

Both real-process checks used25 Channels,4500 SEND/s,60s and the same diagnostic
binary. This smaller scope only verifies that actual compaction reaches the
control point; it cannot qualify R2 or R6.

- `positive-16`: runner73.309s, full60s SEND completed. Each node wrote about
  59.8MB of categorized compaction data. Pacing was exercised8,383–8,522 times
  per node; actual summed wait2.49–2.55s, reserved wait1.33–1.38s.
- `positive-0`: runner72.922s, full60s SEND completed. Each node wrote about
  61.5MB of categorized compaction data; wait count/time were exactly zero.

Each run retained four complete snapshots, an error-free I/O window, and only
idle sleep after completion. Scheduler extraction succeeded for all eight
traces. `verify_probe.py` rejects missing actual compaction, unexpected rates,
unbounded/unordered sample history, or ineffective/incorrectly active pacing.
These probe checks passed for both runs. Different completed compaction bytes
between these short controls are not evidence of equal background work or debt.

## Next causal comparison and limits

The fixed next order is fresh zero-rate control, then16MiB/s intervention, each
at the original5000 Channels /4500 SEND/s /30-minute request with unchanged
admission, batch, queue, durability and failure gates. Preserve failures/guard
aborts. Correlate categorized write deltas with kernel I/O and runtime sync
intervals. Inspect achieved ingress, completed delivery, compaction debt/read
amplification and pending background work; merely postponing compaction does not
count as a fix. A single successful intervention will not establish R2 or R6.

At preparation, full comparison was blocked by disk headroom. The user has now
freed disk space; both original-load arms below are complete. The8GiB guard was
unchanged.

Reproduction assets: `assets/send-ban-compaction-pace-20260927/manifest.json`.


## Verified disk reclamation and remaining prerequisite

With all jobs terminal and only owned-container idle sleep live, gzip round-trip
and raw SHA-256 checks verified the committed trace archives. Removed redundant
raw copies:874,502,276 bytes on the host and862,733,339 bytes in the owned lab.
Compressed traces and identity manifests remain. The cleanup ledger gives exact
host/remote reconstruction paths; decompress the indicated committed archive
there before rerunning older offline commands that expect raw files.

Five archived source-identity records identified351,412,604 bytes of obsolete
Darwin diagnostic binaries. Each matched its recorded hash and had no `lsof`
open handle before removal. Eleven obsolete owned-lab binaries were losslessly
gzipped and round-trip verified, saving384,701,963 bytes; their original hashes,
paths and executable modes are retained for restoration. Current experimental,
clean-candidate, R6 comparison and decoder binaries remain executable. Finally,
only the owned lab's rebuildable Go cache was cleared. No unrelated container,
volume, image, cache, worktree or application data was pruned.

Host free-space readings moved9.2GiB ->5.5GiB ->about11GiB during this work;
the transient drop/rebound is not attributed to swap (reported use zero) or to a
specific external activity. Archived counters from the earlier28-minute long run
show19.099GiB initial free and9.945GiB minimum, a9.154GiB whole-host decline.
That is a planning observation, not exclusive product storage attribution.
With the unchanged8GiB runtime guard,19GiB starting free space is the current
conservative preflight target. Current headroom is recorded in `disk-headroom.json`.

The disk prerequisite was subsequently resolved by the user. Preflight recorded
63,686,574,080 free bytes and verified all three original binary hashes. No
workload, queue, timeout, durability or guard setting was relaxed.


## Complete original-load pair after disk cleanup

The frozen order was `control-01` (zero pacing), then `paced-16-01` (16MiB/s).
Each requested5,000 Channels /4,500 SEND/s /30minutes with the exact same product,
harness and external observer binaries. No builds, trace parsing or cleanup ran
alongside measured SEND. Product source was not changed. The per-node limiter is
one common budget across its compaction files; it does not limit WAL or flush.

| Result | Zero-rate control |16MiB/s compaction pacing |
| --- | ---: | ---: |
| SEND elapsed |1,800.000s |251.208s before EOF |
| SEND calls |8,100,000 |1,130,439 |
| Achieved ingress/s |4,499.999 |4,500.004 |
| Pending at result |0 |5,304 |
| Permission admission busy |0 |0 |
| SENDACK P99 |153ms |640ms (shorter failure window) |
| Minimum sampled host free |49,429,069,824 bytes |57,601,912,832 bytes |
| Resource-guard/OOM termination |none |none |

Control completed every receiver's324,000 expected messages and drained. The
paced arm closed one node1 connection with `async_dispatch_queue_full`; other
recorded closes were peer closes. Its final full-minute sample already had443
pending messages. Both workloads were terminal with only container idle sleep
remaining. All eight trace snapshots were complete, scheduler extraction passed,
and full native decoding succeeded. I/O source error lists were empty.

The limiter was active: all three nodes recorded actual and reserved waits. In
the retained pre-capture250ms sample intervals, compaction successful-write peaks
were17.11/17.26/17.66MiB/s, consistent with the finite burst allowance. The final
roughly9.75s averages were15.37/15.63/15.37MiB/s. Actual summed waiting was
139.1–146.7s per process; concurrent waiting must not be read as elapsed wall time.
Zero control had exactly zero waits. These are VFS successful-write counters,
not physical-device bandwidth measurements.

## Failure evidence and limits

The paced failure still had synchronized slow WAL calls. In the retained completed
trace intervals, pre-capture WAL maxima were113.52/121.10/117.18ms. Several nodes'
WAL calls ended together about3.706s and0.878s before capture. SST compaction sync
maxima were150.12/150.37/152.60ms; memtable-flush sync maxima184.65/180.26/203.39ms.
Gateway batch waits reached269.00/264.30/263.54ms. Completed runnable maxima were
20.63/16.38/11.62ms. Clock-offset spreads were29/88/58/90ns across harness/nodes;
integer upper-median offsets were used, with variation retained as observation,
not a certified error bound. These top-N completed intervals are not exhaustive
syscall counts and omit waits unfinished at capture.

A fixed final-10-second selection, excluding partial/capture-overlapping pairs,
retained39 I/O intervals (about9.75s) in each arm. Paced failure:24.66% cgroup I/O
full stall,71.61MB/s cgroup writes,778 whole-VM completed block flushes averaging
10.12ms,5,998us CPU throttling and zero memory some-stall increment. Control's
completion window:7.63% I/O full stall,135.81MB/s writes,15,094 completed block
flushes averaging0.232ms; it also had CPU throttling and memory-pressure increments.
These are different lifecycle endpoints, not a matched treatment-effect estimate.
VM block-flush averages are not WAL latency or physical-host-device measurements.
The control also survived a retained whole-run interval with87.70% I/O full stall;
that scalar alone is therefore insufficient to predict connection closure.

At the comparable minute4 sample, control had1,080,001 ACKs/receives and zero
pending; pacing had1,079,558 ACKs,1,079,664 receives and443 pending. Compaction
written bytes were4.424GB versus3.841GB; cumulative maximum compaction debt277.3MB
versus307.4MB; maximum read amplification6 versus7. This is evidence that pacing
changed background progress as well as write shape. Final debt gauges after a
quiescent drain were not collected, so equal eventual work cannot be asserted.
Whole30-minute and251-second totals must not be compared as equivalent work.

**Conclusion:** this rate limit did not repair sustained EOF. Smoothing categorized
compaction writes did not eliminate the slow-WAL/queue-close chain. One sequential
pair does not prove pacing caused the failure or that compaction is irrelevant;
shared storage and timing remain uncontrolled. Do not promote the limiter into
product configuration. The next causal seam is repeated durable-completion stalls
and the session-ordered batch service/backlog relationship: distinguish a sustained
service deficit from one isolated I/O peak, preserving ordering and boundedness.

This diagnostic control pass is not the uninstrumented R2 acceptance run. R2 and
three complete fresh R6 pairs remain pending. The trace and I/O analyzers were
reused unchanged; correlation deliberately drops the old failure's retrospective
windows. `full-pair-analysis-contracts.md` preceded the new bounded summarizer.
Reproduce with `check_capture.py`, `analyze_trace.py`, `analyze_io.py`, `correlate.py`
and `analyze_pair.py` in the asset directory. Probe-only `verify_probe.py` requires
PASS and is intentionally not used to reject this captured failure.
