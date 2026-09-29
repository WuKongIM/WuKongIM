# External I/O pressure window for the sustained EOF

The previous failed trace shows repeated WAL sync waits and simultaneous SST
compaction/flush sync waits. It does not prove compaction caused the WAL latency:
a shared filesystem/VM/device event could delay both. Before choosing a product
change, distinguish I/O pressure from CPU or memory pressure and correlate the
runtime intervals with actual kernel counters.

## Fixed experiment

Retain the exact product and harness binaries from the preceding flight-recorder
run. SHA-256 comparison confirms both are unchanged. Add only a separate Linux
counter observer. It waits for the four task-owned PID readiness manifests and
then reads 21 fixed sources every 250ms: the owned lab's cgroup pressure/CPU/
memory/I/O counters, the Linux VM's global pressure/diskstats/vmstat counters,
and io/stat for the four exact manifest PIDs. Global VM sources are not claimed
to be private to this task. Reads are sequential, with individual errors and
sample start/end timestamps; no sample is an atomic whole-system snapshot.

The process retains 160 samples (recent 40 seconds) and at most ten peak pairs
ranked by nonnegative cgroup I/O full-stall microsecond deltas. Reset/missing/
malformed pressure is explicit and cannot become zero. The ring starts only
when all four processes are ready, after cold prime. It writes no periodic
sample files. A flight-capture manifest, exact runner stop marker, or 40-minute
bound terminates collection. Each input is capped at 32KiB and the exclusive
final output at 8MiB. The runner joins the observer, preserves its exit code,
and verifies process cleanup. These bounds do not eliminate observer overhead.

## Test-first and real-process positive control

The failure list and fast contracts preceded implementation. RED failed on
missing ring/pressure/read helpers. Ring wrap/eviction, invalid/reset pressure,
bounded source reads and peak retention passed after implementation; race also
passed. The 25-Channel / 500 SEND/s / 10-second positive run completed 5,000
messages. All four flight captures parsed successfully. The observer retained
41 samples with zero source errors or invalid pressure pairs; sample reads took
0.293ms median and 0.837ms maximum. Final counter evidence is 647,335 bytes.
The full runner finished in 23.059 seconds and left only the lab's idle sleep.
These measurements are a positive control, not a sustained capacity result.

## Offline clock precision

The installed Go trace formatter used `time.RFC3339`, dropping fractions from
clock-snapshot wall timestamps. An isolated offline `cmd/trace` decoder changes
only that constant to `time.RFC3339Nano`; the Go installation is unchanged.
On the complete positive harness trace, all 342,702 event headers match the
original decoder after removing only the wall fractions. Exact source hashes,
replacement/build command and fractional clock snapshots are retained. This
preserves existing precision; it does not manufacture it. Sequential /proc
read intervals must still be respected when correlating 250ms samples.

## Long-run status and interpretation

The prepared command is:

`python3 docs/reports/assets/send-ban-io-window-20260927/run.py sustained-01`

It preserves 5000 Channels, 4500 SEND/s, 30 minutes, 256 hash Slots, 128 append
workers, gateway batch cap 32, and existing rejection/latency gates. The task's
rebuildable Go cache was cleared after all build/parse activity completed,
before this workload. No compilation, cleanup or parsing may overlap SEND.
The first long run started at 2026-09-27T11:26:31Z and was aborted by the
host disk guard after 748.538 seconds total, including 168.757 seconds of cold
prime. At minute 8 it reported 2,160,001 ACK/RECV and pending=0; minute 9 reported
2,427,944 ACK/RECV and pending=2,057. Free space fell from 9.338GiB at outer
+723.3s to 4.863GiB at +738.4s. The next action was container stop; both workload
and observer exited 137, OOMKilled=false. There is no normal capture manifest or
I/O window. This is an aborted observation, not a reproduced EOF or an R2 pass.
The shared-host free-space change remains unexplained; it must not be attributed
to swap, compaction, or the product from these readings alone.

The original guard stopped the container before requesting the observer's ring,
losing its in-memory evidence. A controlled process-level RED reproduced this:
only the runner's free-space reading changed to 7GiB after 15s (no disk filler),
with frozen binaries and 25 Channels / 500 SEND/s. Both processes were killed,
and both I/O and trace artifacts were absent. The repair first records the guard
time, signals and joins the observer within 3s, requests the four existing
one-shot recorders concurrently with bounded deadlines, then stops the exact
owned container. Below 1GiB free it explicitly skips trace writes. A separate
`guard-capture.json` records resource-guard snapshots; normal `capture.json`
and normal-pass semantics remain separate.

The matching GREEN was intentionally aborted (workload 137, observer 1,
`runner_stop`). It preserved 16 samples, zero source errors, and all four complete
snapshots; total runner 25.591s, capture 10.878ms. Scheduler-profile and full
RFC3339Nano decoding both succeeded for all four files. Full event counts:
harness 137,831; nodes 708,328 / 767,521 / 641,593. This validates evidence
preservation on the tested shutdown path only. It does not establish capacity.
Snapshot writes occur after the trigger and are excluded from pre-trigger claims.
A build setup failure caused by mixing the isolated decoder's package with the
observer was retained and fixed by explicitly selecting observer source files.
Fast observer contracts and race checks pass after that correction.

Strict offline counter/clock contracts also went RED (missing implementation),
then passed all six checks: missing/read-error preservation, reset handling,
pressure totals, disk field units/layout/device selection, exact nanosecond clock
conversion, and exclusion of capture-overlapping samples. These are analysis
primitives; there is still no I/O window for the aborted full-size run to analyze.

All three stopped fixtures' logs, configs and file inventories were saved before
removing only their exact task-owned test/plugin directories. Original long-run
observer identity is preserved separately from the repaired guard observer.
A second full-size diagnostic may now use the repaired guard; its result must
remain distinct from this failed observation and from clean R2 qualification.

Predictions: durable waits caused by shared I/O pressure should align with
I/O-stall and block-queue changes; CPU throttling or memory reclaim alternatives
should have their own counter changes. Compaction competition still requires a
single-variable intervention after the observational evidence. A successful
instrumented run never substitutes for clean R2 or the three complete R6 pairs.
Missing/truncated observation remains insufficient evidence. Do not expand the
queue, relax durability or attribute a kernel cause from syscall wall time alone.

Sources, binaries, scripts, test outputs and positive artifacts are retained in
`assets/send-ban-io-window-20260927/manifest.json`.


## Second run: EOF with complete kernel and runtime evidence

`sustained-02` used the frozen product/harness and the repaired external observer
from `d48c6f8ed`. It failed with sender-read EOF at 342.726 seconds of SEND,
1,542,268 completed calls and 6,634 pending messages. Permission admission busy
remained zero. Failure runtime metrics recorded async-dispatch queue closure on
node 1 (one) and node 3 (four). Total runner time was 534.670s; neither disk guard
nor OOM fired. The harness removed its own fixture; a read-only terminal check
confirmed the exact fixture path absent and only the container's idle sleep live.
This is a diagnostic failure, not clean R2 qualification or an R6 pair.

All four snapshots are complete, gzip round-trips match their recorded raw
SHA-256 values, and both scheduler extraction and full native decoding succeed.
Full event counts are 1,986,034 / 6,756,754 / 6,642,913 / 6,297,381 for harness /
node 1 / node 2 / node 3. Nanosecond clock-snapshot offset spreads are respectively
74 / 83 / 53 / 89ns; correlation uses an actual upper-median integer offset,
retains the spread and does not interpret it as a certified clock-error bound.

The observer sampled 1,372 times and retained the final 160 samples plus ten
whole-run peak pairs, with zero source errors or invalid pressure pairs. Final
output is 1,910,389 bytes; retained reads took 0.325ms median and 1.943ms maximum.
One pair overlaps capture and is excluded from pre-failure claims. All trace-file
writes began after the cutoff used below. Counter reads are sequential and the
250ms deltas cannot identify an exact syscall or physical host disk operation.

Retrospective windows, relative to capture start, show:

| Window | Owned-cgroup writes | Cgroup I/O full stall | VM block-flush mean |
| --- | ---: | ---: | ---: |
| −6.044s to −3.545s | 27.88 MB/s | 5.30% | 0.127ms (4,883 completions) |
| −3.047s to −2.296s | 298.39 MB/s | 10.01% | 0.379ms (747 completions) |
| −2.296s to −0.044s | 111.32 MB/s | 49.12% | 17.28ms (125 completions) |

These are explanatory windows chosen after observation, not predeclared
statistical comparisons. Device 254:0 is selected from the owned cgroup's
`io.stat`; its block counters cover the whole Linux VM. The block-flush mean is
accumulated completed-flush time divided by completed flushes, not a WAL fsync
measurement or a physical-disk saturation claim. The VM `vda` in-flight gauge
reads zero in these samples despite nonzero activity; it is not used to infer
queue occupancy. Per-PID counters show the write burst came from the three node
processes, while the harness had zero accounted writes before capture. They do
not distinguish WAL bytes from SST bytes.

Every selected window has zero increments in CPU throttled time and memory PSI.
Completed runnable delays in the retained runtime windows reach 23.38 / 17.01 /
14.67ms on the nodes. Completed pre-capture WAL Fdatasync intervals reach
173.79 / 193.76 / 222.11ms; SST **compaction** Fdatasync intervals reach
303.94 / 298.85 / 348.23ms. The retained compaction stacks include
`DB.compact1 -> compactAndWrite -> Runner.WriteTable -> fileBufferedWritable.Finish`;
WAL stacks include `record.LogWriter.flushLoop -> syncWithLatency`. Gateway
`OnSendBatch` waits reach about 380–381ms before capture. Separate completed
intervals after capture are retained but excluded from these maxima. The top-160
interval summaries are bounded selections, not exhaustive syscall counts.

This adds direct kernel evidence that the failure window contains a sharp I/O
stall after a node-write burst, with overlapping slow WAL and compaction syncs.
It narrows the next causal experiment to competition from SST compaction writes;
it still does not prove those writes caused the WAL latency, identify a host
firmware/filesystem cause, or establish a product fix. A single-variable
intervention must preserve true sync, original input load, admission/queue bounds
and completed work, and must watch compaction debt rather than merely deferring
it beyond the test. No product setting or queue has been changed by this probe.

Repeatable offline commands (only after the workload is terminal):

- `python3 docs/reports/assets/send-ban-io-window-20260927/check_capture.py sustained-02`
- `python3 docs/reports/assets/send-ban-io-window-20260927/analyze_trace.py sustained-02`
- `python3 docs/reports/assets/send-ban-io-window-20260927/analyze_io.py sustained-02`
- `python3 docs/reports/assets/send-ban-io-window-20260927/correlate.py sustained-02`

The trace analyzer reuses the preceding recorder's interval algorithm unchanged,
with only its asset root and precision-preserving offline decoder selected here.
`io-intervals.json`, `io-stage-summary.json`, `correlation.json`, original raw
counter text, selected raw stacks and complete compressed traces preserve the
basis and limits of these conclusions. R2 and R6 remain incomplete.
