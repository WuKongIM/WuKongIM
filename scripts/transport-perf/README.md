# Native Linux RPC repeatability gate

Use this diagnostic before judging small transport performance changes. It does
not buy hosts, deploy a cluster, alter production defaults, or qualify a candidate.
The initial gate intentionally covers only 64-byte, unbudgeted mutation RPCs on
one connection, with 16 callers and the Observer disabled. It is not a business
capacity or fixed-arrival-rate SLO test.

## Build once from a committed source

Use two separate Linux amd64 hosts with the same CPU model, at least four vCPUs
and 8 GiB each. KVM guests are supported; this does not establish physical host
isolation. Containers and architecture emulation are functional-smoke environments
only. The executable reports its hash, source SHA, CPU model, CPU affinity, host-ID
hash, toolchain, process architecture, kernel architecture, and runtime overrides. Confirm the deployment boundary from the exact lease
inventory too; absence of common container markers alone does not prove host
isolation.

From a clean checkout, with an output outside the repository:

```sh
python3 scripts/transport-perf/build.py \
  --source "$PWD" --output /tmp/wkrpc-native/base
```

The builder fixes Go 1.25.11, CGO off, `-trimpath -buildvcs=false`, and a stable
temporary import path. It refuses a dirty checkout or an existing output, removes
only its own temporary source directory, and writes a SHA256 build manifest.
The same tool's three probe source files can build against another compatible, committed source
worktree via `--source`; keep the client binary fixed in version comparisons.
Verify the manifest SHA256 on both hosts after transfer.

## First run: six independent baseline windows

Start the server in its own remote session, bound to its private address. Use
existing SSH and provider lifecycle tools; this diagnostic has no cloud credentials.
The server handles SIGTERM and exits after a hard 30-minute lifetime by default.
Only allow benchmark traffic from the selected client host.

```sh
env -u GOMEMLIMIT -u GODEBUG GOMAXPROCS=4 \
  taskset -c 0-3 ./base -mode=server -addr="$SERVER_PRIVATE_IP:7001" -lifetime=30m
```

Wait for `RPC_PROBE_READY`. On the separate client host, create a new empty results
directory and run exactly six windows, serially. Do not build, profile, or execute
other workloads during these windows. Do not discard failed or slow windows.

```sh
mkdir results
for round in 1 2 3 4 5 6; do
  env -u GOMEMLIMIT -u GODEBUG GOMAXPROCS=4 \
    taskset -c 0-3 ./base -addr="$SERVER_PRIVATE_IP:7001" \
      >"results/aa-$round.json" 2>"results/aa-$round.stderr" || exit 1
done
```

Each client process preallocates bounded exact samples, warms the actual workload
for 10 seconds, forces one client GC before measurement, and measures for 20 seconds.
Client GOGC is explicitly 400; server GOGC is explicitly 100. These are diagnostic
controls, not production recommendations. Sample capacity is one million per
worker; reaching it fails the window. Whole-run quantiles preserve the historical
`floor((n-1)*p)` rank. Per-second windows use call start time and are never averaged
to obtain the whole-run P99. Copying and sorting run after resource snapshots.

The server exposes a second RPC only inside this diagnostic process for metadata
and boundary counters. Its echo handler adds one atomic counter increment per
call; use this same harness for all comparisons. Server resource deltas include
small control-RPC boundary work and are approximate per-RPC attribution. Exact
echo-count deltas must equal the client's completed measurement calls; extra
clients, a server restart, failed responses, and capped samples invalidate evidence.
Warmup calls are recorded separately. Memory and process CPU/GC counters are
reported on both ends; none are equivalent to server-only request latency.

Copy all six JSON files and stderr files back and run:

```sh
python3 scripts/transport-perf/analyze.py results/aa-*.json >repeatability.json
```

The gate requires matching identities and settings, separate hosts, no hidden
runtime overrides, no overlapping windows, valid duration/counters, zero errors,
and no sample cap. Across **all** supplied windows, `(max-min)/median` must be at
most **3% for throughput and 10% for P99**. These are declared engineering gates,
not confidence intervals or statistical significance. The exit code is nonzero
for invalid or unstable evidence. A passed result means only that this baseline
was repeatable under these conditions.

If this fails, preserve everything, stop this test round, and release its exact
temporary lease. Do not rerun until a favorable batch appears or start candidate
testing on an unstable baseline. If it passes, predeclare a separate AB/BA plan
covering both connection counts, budgets/read controls, large payloads and rejected
admission, followed by another same-version gate. Do not reuse this single-scenario
gate as a complete optimization acceptance test.

## Local validation

```sh
GOWORK=off GOTOOLCHAIN=go1.25.11 go test ./scripts/transport-perf/probe -count=1
GOWORK=off GOTOOLCHAIN=go1.25.11 go test -race -tags=integration ./scripts/transport-perf/probe -count=1 -timeout=1m
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/transport-perf -p 'test_*.py'
```

The integration tests use separate real processes, TCP echo, graceful signal
shutdown and an intentionally exhausted sample cap. Non-Linux hosts may run
functional checks but their reports must fail native qualification. For a short
smoke only, the client permits `-warmup=100ms -duration=100ms -workers=2`.

Always stop the exact server process/session after gathering reports. Server
process exit does **not** release a paid host: use the exact lease selector and
retain authenticated zero-inventory proof before reporting cloud cleanup.

## Separate diagnostic pass with host samples

A failed repeatability gate needs evidence from a **separate diagnostic pass**.
Sampling consumes CPU and reads procfs, so its results cannot qualify throughput
or latency. The client `-telemetry-tail=2s` flag retains the process after writing
its report so the sidecar can sample beyond the last measured second; the gate
rejects that flag. The gate cannot detect an arbitrary external collector: keep
all sampled runs out of qualification inputs even if that flag was omitted.
Use profiles in a separate bounded diagnostic window too.

The sidecar needs Linux, Python 3.9+, readable procfs and the exact probe PID. It
has no cloud API, credentials, deployment or purchase behavior. Use direct probe
executables as wrapped commands, not a shell or launcher which later changes its
executable. Apply affinity/environment outside the sidecar so the child inherits
them. Never change kernel scheduler settings merely to fill missing metrics.

On the server host, after the server reports ready, attach to its exact PID:

```sh
python3 host_sample.py --pid "$SERVER_PID" --duration 90 \
  --output server-host.jsonl
```

On the client host, start one bounded diagnostic window during that interval:

```sh
env -u GOMEMLIMIT -u GODEBUG GOMAXPROCS=4 \
  taskset -c 0-3 python3 host_sample.py --duration 45 \
    --output client-host.jsonl -- ./base \
      -addr="$SERVER_PRIVATE_IP:7001" -telemetry-tail=2s \
      >client-probe.json 2>client-probe.stderr
```

Copy `host_sample.py` and `correlate.py` together. Copy the client probe report
and both sample files back, then analyze each host against the same report:

```sh
python3 correlate.py --probe client-probe.json --samples client-host.jsonl \
  --role client >client-correlated.json
python3 correlate.py --probe client-probe.json --samples server-host.jsonl \
  --role server >server-correlated.json
```

Stop the attached collector with SIGTERM after the client finishes if its full
90-second bound is unnecessary. It retains an interrupted terminal record and
returns nonzero; `--pid` never signals the server. The owned-command mode stops
its own child session on timeout/interruption, with a one-second TERM grace and
then KILL. Finish the normal exact-server and exact-lease cleanup independently.
The sidecar reserves an exclusive mode-0600 output before launching a child and
refuses existing files. Disk errors or SIGKILL can leave truncated evidence;
the correlator rejects a missing terminal record.

### What is measured and how to interpret it

Each one-second snapshot reads a fixed whitelist: aggregate CPU jiffies and
context switches, CPU/memory/I/O PSI totals, TCP/TcpExt counters, interface bytes,
packets/errors/drops, the first three softnet counters per CPU, process CPU/fault
counters, and per-thread context switches and scheduler runtime/runqueue wait.
The raw records retain units in metric names where not implied by the kernel
counter name; CPU tick conversion uses the header's `clock_ticks_per_second`.
CPU, PSI and softnet are host-wide. TCP and interface counters include **all
traffic in the target network namespace**, not just this RPC connection.
Interface names do not establish persistent device identity across replacement.

The collector checks PID start ticks, executable inode and network namespace
around each snapshot; the correlator additionally matches executable SHA256,
hashed host/boot identity and PID against the probe. PID reuse, exec, server
restart and mismatched reports fail closed. Collector and target must share a
monotonic time namespace. Commands, environments, raw machine IDs and packet
payloads are not recorded.

Client alignment uses a `CLOCK_MONOTONIC` bracket around the probe's measurement
start. Server alignment uses timestamps inside the **existing** two metadata
RPCs and client send/return brackets. It never compares unrelated monotonic
origins directly or assumes synchronized UTC clocks. It encloses both offset
brackets with an explicit **1000 ppm relative clock drift assumption** between
boundaries, rejects inconsistent clocks or more than 100 ms uncertainty, and
reports that assumption with the bounds. This is a conservative diagnostic model,
not proof of clock accuracy. A detected wall-clock jump is reported without
shifting monotonic alignment. Old reports without anchors cannot be retroactively
aligned.

Counter deltas remain attached to their original sample intervals. The report
lists every RPC second that an interval might overlap; it does **not** duplicate
or prorate a retransmission into exact seconds. A second is `bracketed` only when
samples cover both ends after allowing for read/clock uncertainty. Samples over
250 ms late/long or intervals over 1.25 s mark incomplete regular coverage.
Missing metrics, disabled `sched_schedstats`, counter regressions and interface/
CPU membership changes remain explicit; old softnet layouts without CPU IDs
are unavailable rather than assigned unstable row identities; absent counters are never filled with
zero. Surviving-thread deltas are lower bounds: terminated and short-lived
threads can lose counters, and observed thread churn is listed. `aligned` means
available time alignment, not a performance pass or a root-cause finding;
`partial` preserves useful evidence plus its gaps. Invalid inputs return nonzero.

Bounds are fixed: 1 Hz, 1–900 seconds, at most 901 snapshots, 256 threads,
1024 softnet CPU rows, 64 interfaces, 256 KiB per proc read/JSON record, 32 MiB
output and a 256 MiB streaming executable-hash limit. Hitting a bound fails the
capture and retains terminal evidence when writable. Late sampling skips slots
instead of launching catch-up bursts. Read span, lateness, per-sample CPU, total
sidecar CPU, peak RSS and output size make observation cost visible. Measure that
cost again on native hosts before interpreting a diagnostic run; local container
smoke does not establish production overhead.

Linux sources: [procfs](https://docs.kernel.org/filesystems/proc.html),
[PSI](https://docs.kernel.org/accounting/psi.html),
[scheduler statistics](https://docs.kernel.org/scheduler/sched-stats.html).

Linux process/lifecycle integration tests (no public ports or cloud resources):

```sh
GOWORK=off GOTOOLCHAIN=go1.25.11 go test -tags=integration \
  ./scripts/transport-perf/... -count=1 -timeout=1m
```
