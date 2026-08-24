# Chat Lifecycle Soak Operations Runbook

## Purpose and evidence levels

This run proves that one continuously growing real chat catalog remains correct
while Channel runtimes repeatedly become hot, cool naturally, and reheat. It
uses real WKProto CONNECT/SEND/SENDACK/RECV/RECVACK traffic and the product
`/conversation/sync` route. It does not preserve a login cursor: every login
constructs a new request with `version=0`, empty `last_msg_seqs`,
`msg_count=20`, `only_unread=0`, and `limit=500`.

Run the levels in order:

1. Local 30-minute shakeout with the committed native-process helper.
2. Reviewed two-hour rehearsal on the four intended hosts. Its immutable
   rehearsal stage ends itself at exactly two measured hours and may emit
   `rehearsal_pass`; it is still not formal qualification evidence.
3. A fresh uninterrupted formal run. The 24-hour cut is qualification evidence
   and continues in the same generation; the 72-hour cut is the final result.
4. Capacity search only on the same still-live dataset after a passing 72-hour
   final report.

No run resumes. A process crash, worker loss, config change, Slot migration,
filesystem expansion, service restart, or incomplete finalization invalidates
the run. Preserve its artifacts for diagnosis, then use a new run ID and fresh
service data directories. If the initial 500 GiB filesystems later prove too
small, resize only between runs and start the next run from fresh data.

## Fixed topology

Use four hosts on the Lease-private network; only the load host also has the
temporary public EIP:

| Hosts | Count | Responsibility |
| --- | ---: | --- |
| Service/data | 3 | WuKongIM API, Gateway, Manager/debug, metrics, and one filesystem metrics endpoint per host. |
| Load/coordinator/monitor | 1 | Three isolated `wkbench worker --mode chat-lifecycle` processes, one coordinator, Prometheus, Analysis, proxy, and the durable report directory. |

The product topology is fixed for the complete run:

- 12 logical Slot Raft Groups;
- 256 physical hash slots;
- Slot replicas 3 and Channel replicas 3;
- no Slot migration, node replacement, or topology change;
- at least 500,000,000,000 usable bytes on each selected service data filesystem;
- coordinated safe stop when any selected filesystem has less than 5 percent
  available.

Host metrics must identify exactly one `device` and `mountpoint` pair on each
service host. The formal YAML selectors must match the exported labels exactly.
Do not point all three declarations at one shared exporter or filesystem.

## Network and credentials

Bind worker control, benchmark APIs, debug/pprof, host metrics, and the
coordinator report path to private administration networks. Gateway endpoints
may use a separate private traffic network. Firewalls should permit only these
edges:

- coordinator to all service API, host-metrics, worker-control, and Gateway
  endpoints;
- each worker to the declared service API pool and Gateway pool;
- service-to-service cluster and Channel/Slot transport;
- operator access to the coordinator artifact host.

Create separate high-entropy Bench and worker-control tokens. Supply each
credential through exactly one environment value or one owner-only file. Token
files must be mode `0600`; containing directories should be `0700`.

```bash
install -d -m 0700 /secure/wukongim-chat-lifecycle
chmod 0600 /secure/wukongim-chat-lifecycle/bench.token
chmod 0600 /secure/wukongim-chat-lifecycle/worker.token
export WK_CHAT_LIFECYCLE_BENCH_TOKEN_FILE=/secure/wukongim-chat-lifecycle/bench.token
export WK_CHAT_LIFECYCLE_WORKER_TOKEN_FILE=/secure/wukongim-chat-lifecycle/worker.token
```

Never place credentials in YAML, process arguments, logs, reports, or worker
assignments. The service Bench bearer must protect `/bench/v1/*` and the enabled
debug subtree. Every lifecycle worker endpoint, including health and info,
requires the worker bearer.

## Build and configuration freeze

Build both binaries from one reviewed commit and record their SHA-256 digests:

```bash
GOWORK=off go build -o /opt/wukongim/bin/wukongim ./cmd/wukongim
GOWORK=off go build -o /opt/wukongim/bin/wkbench ./cmd/wkbench
sha256sum /opt/wukongim/bin/wukongim /opt/wukongim/bin/wkbench
```

Copy `configs/wkbench/chat-lifecycle/formal.yaml` to an owner-only operations
directory. Replace its run ID and every `.invalid` address. Do not change the
reviewed formal workload, thresholds, topology, 24-hour checkpoint, or 72-hour
final duration. Freeze and retain:

- source commit and binary digests;
- all three effective `wukongim.toml` files and redacted environment snapshots;
- lifecycle YAML digest;
- host inventory, interface addresses, filesystem device/mountpoint pairs, and
  filesystem size;
- run ID and UTC start time.

Service configuration must enable the Bench API, bearer protection, metrics,
debug API, and one real WKProto TCP Gateway per node. It must set initial Slot
count 12, hash-slot count 256, Slot replica count 3, Channel replica count 3,
and per-node Channel bound 50,000.

## Local shakeout

Use a fresh run directory. The helper builds binaries and owns three service
processes, three workers, three host-metrics processes, the coordinator, PID
files, logs, report output, and graceful cleanup.

```bash
export WK_BENCH_API_TOKEN='replace-local-bench-token'
export WK_BENCH_WORKER_TOKEN='replace-local-worker-token'
scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh \
  --run-dir "$PWD/tmp/chat-lifecycle-shakeout" \
  --stop-after 1800
```

Expected outcomes are either a clean coordinated operator stop with
`report/final.json`, or an explicit preflight failure. On a host whose selected
filesystem is already below the 5-percent reserve, `preflight_code=disk_free`
is correct and must not be relabeled as a successful shakeout.

Before continuing, verify that all recorded child PIDs exited, no configured
ports remain open, and logs contain no panic, fatal Slot error, or leaked raw
credential.

## Reviewed two-hour rehearsal

Use the four intended hosts, fresh rehearsal data, and the sealed
`configs/wkbench/chat-lifecycle/rehearsal.yaml`. Deployment leaves the
coordinator dormant; the top-level rehearsal workflow starts its remote
systemd unit only after cluster and worker readiness:

```bash
wkbench soak chat-lifecycle \
  --config /etc/wukongim-cloud/chat-lifecycle-rehearsal.yaml \
  --output-dir /secure/reports/chat-lifecycle-rehearsal
```

The measured clock begins only after all 10,000 users complete full version-zero
sync and the first complete 2,000 SEND/s grant is accepted. The coordinator
then stops and finalizes itself at exactly two measured hours. A clean result
is `rehearsal_pass`, never formal `pass`. A manual `TERM` remains an
`operator_stop` and does not count as completion of this reviewed stage.

On a fresh dataset, expect the three workers to admit one global 50-login/second
bootstrap stream, split into fixed 17/17/16 per-worker shares. Every login still performs
the real protocol handshake and stateless full sync. The deterministic scheduler
reaches 10,000 simultaneous online users in 209 seconds under configured churn
and is guarded by a 15-minute model bound; remote handshake or sync latency can
extend wall time within the separate two-hour pre-clock safety ceiling. After
the barrier, the first global grant clears the unequal bootstrap attempt phase
and switches all three workers to the unchanged 250,000-new-user/day 80/20
steady stream without resetting assigned UIDs. Delayed ticks and temporarily full
starting pools discard unused bootstrap credit; they must not produce catch-up
login bursts above 50/second globally.

Require the rehearsal to prove:

- all three workers became traffic-ready;
- 12/256/3/3 topology preflight passed on every service node;
- every service filesystem passed the 500,000,000,000-byte and 5-percent gates;
- continuous health/readiness/Slot progress observations had no 30-second gap;
- all logins performed valid full version-zero sync;
- lifecycle samples, metadata-create counters, latency, queues, heap, and
  goroutine evidence were present;
- coordinated stop returned stable final worker snapshots and left no process
  or listening port behind.

Discard the rehearsal data before the formal run. Do not copy or seed it into
the formal data directories.

## Formal 24-hour qualification and 72-hour final

Provision fresh data directories on all three service hosts and a fresh report
directory on the coordinator. Start service nodes first, workers second, and
the coordinator last. Record all PIDs and UTC timestamps in the operations
journal.

```bash
wkbench soak chat-lifecycle \
  --config /secure/config/chat-lifecycle-formal.yaml \
  --output-dir /secure/reports/chat-lifecycle-formal
```

Do not signal a healthy formal run at 24 hours. The coordinator atomically
writes the qualification JSON and Markdown, keeps the same worker fence and
process generations, and continues traffic. Qualification proves continuity to
that point but is not a final pass.

At 72 hours the coordinator freezes the terminal decision, stops and joins all
three workers, joins lifecycle observation, refreshes the live dataset digest,
performs the exact per-Slot metadata-create reconciliation against stable final
worker counters, and atomically writes `final.json` and `final.md`. Exact
metadata equality is intentionally a post-stop check; during live qualification
ordinary person-channel creation can race a metrics scrape.

Do not call the run passed unless `final.json` is schema-valid, terminal,
`profile=formal`, `mode=soak`, at least 72 hours, and has verdict `pass`. The
mere presence of a qualification file, coordinator process exit, or clean
application logs is not a passing result.

## Monitoring and stop rules

Monitor without mutating the run:

- service `/healthz` and `/readyz` every five seconds;
- all 12 live Slot groups, one leader per group, voter progress, replica lag,
  and leader distribution;
- SENDACK error rate and hot/cold/sync threshold histograms;
- Channel runtime loaded roles, pending metadata, PullHint/Pull, queue and
  inflight pressure;
- metadata-create `created`, `already_existing`, and `error` counters for all 12
  logical groups;
- process RSS, forced-GC live heap, goroutines, CPU, open files, and filesystem
  size/available bytes;
- worker status fence, snapshot sequence, uptime, queue depths, correctness,
  sync, and lifecycle counters.

The coordinator owns normal failure and disk-safe-stop decisions. For an
operator stop, send one `TERM` and wait. Use a second signal only when the
coordinator cannot complete bounded cleanup; classify that run invalid.

Never delete or overwrite a failed run directory. Capture process trees,
listeners, service/worker/coordinator logs, metrics, pprof requested by the
incident procedure, effective config, final or unavailable-report summary, and
filesystem evidence before stopping remaining processes.

## Capacity on the aged dataset

Capacity is eligible only after the passing 72-hour final report. Keep the same
three service processes and data directories running. Do not restart, clean,
resize, migrate, or substitute a copied dataset. Create a capacity YAML with
`mode: capacity` and the exact final report reference, then run:

```bash
wkbench capacity chat-lifecycle \
  --config /secure/config/chat-lifecycle-capacity.yaml \
  --checkpoint /secure/reports/chat-lifecycle-formal/final.json \
  --output-dir /secure/reports/chat-lifecycle-capacity
```

Admission performs a fresh direct probe of all three distinct live service
nodes and requires the checkpoint dataset/process-generation digest. The
staircase begins at 2,000 SEND/s, uses 25-percent coarse increases and
10-percent refinement, then requires one uninterrupted 30-minute recovery at
2,000 SEND/s. Capacity failures still join observation, stop workers, and write
final evidence; they do not authorize reuse of a partially stopped generation.

## Exit-code triage

| Code | Meaning | Operator action |
| ---: | --- | --- |
| 0 | Passing terminal report | Preserve and review the full evidence set. |
| 1 | Configuration failure | Correct config offline; start a new run. |
| 2 | Preflight failure | Inspect `coordinator_code` and `preflight_code`; no traffic was assigned. |
| 6 | Internal command failure | Preserve logs and treat the run as invalid. |
| 7 | Product failure | Preserve full evidence and diagnose the product path. |
| 8 | Harness invalid | Fix the harness or observation gap; start a new run. |
| 9 | Infrastructure failure | Inspect filesystem/service-host evidence; start a new run after remediation. |
| 130 | Coordinated operator stop | Expected only for rehearsal/manual termination; not a pass. |

For preflight `disk_size`, provision a 500 GiB data disk whose formatted
filesystem provides at least 500,000,000,000 usable bytes. For
`disk_free`, free or provision capacity before creating fresh data; never lower
the 5-percent threshold to force admission. For Channel cold-activation stalls,
check runtime-meta read results, PullHint receive stages, follower runtime
presence, and leader LEO/HW before changing timeouts.
