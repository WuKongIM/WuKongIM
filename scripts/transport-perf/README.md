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
The same tool's `main.go` can build against another compatible, committed source
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
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/transport-perf -p test_analyze.py
```

The integration tests use separate real processes, TCP echo, graceful signal
shutdown and an intentionally exhausted sample cap. Non-Linux hosts may run
functional checks but their reports must fail native qualification. For a short
smoke only, the client permits `-warmup=100ms -duration=100ms -workers=2`.

Always stop the exact server process/session after gathering reports. Server
process exit does **not** release a paid host: use the exact lease selector and
retain authenticated zero-inventory proof before reporting cloud cleanup.
