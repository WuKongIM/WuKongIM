# wkbench Activate Channels Design

## Problem

The current local 10k-channel script uses a normal `wkbench run` scenario and relies on warmup traffic to incidentally activate channels. That mixes four concerns: benchmark data preparation, channel runtime activation, measured send throughput, and evidence collection. It also creates a different run id for each offered-QPS attempt, so a default multi-QPS run activates multiple independent channel sets instead of proving one stable set of 10,000 simultaneously live channels.

We need a benchmark that answers a narrower and more valuable question:

> Can an already-running cluster prepare, activate, hold, probe, and cleanly release N ChannelV2 group channels through the real SEND path?

## Goals

- Add a first-class `wkbench` scenario for simultaneous channel activation.
- Keep the default activation path black-box: WKProto SEND must travel through gateway, message usecase, clusterv2/channelv2 append, and SENDACK.
- Add bench-only server runtime controls for observation and controlled cleanup, not as a replacement for the real send path.
- Produce hard evidence for 10,000-channel tests: active leaders, followers, parked followers, activation rejects, sendack latency, runtime distribution, queue pressure, pprof, and run artifacts.
- Make failures crisp: not enough active leaders, activation rejections, send errors, or lost channels during hold must be terminal failures.

## Non-Goals

- Do not add a general business channel management API.
- Do not bypass cluster semantics. A single-node deployment remains a single-node cluster; this design targets a running cluster.
- Do not make server-side runtime activation the default success path.
- Do not implement automatic maximum-channel search in the first version.
- Do not tune ChannelV2 runtime behavior as part of this feature.

## Command Shape

Add a dedicated fixed-size experiment:

```bash
wkbench capacity activate-channels \
  --api http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013 \
  --channels 10000 \
  --members 10 \
  --users 20000 \
  --activation-concurrency 2000 \
  --activation-window 10s \
  --hold 60s \
  --report-dir ./tmp/wkbench-activate-channels
```

`capacity activate-channels` fits the existing `capacity send` and `capacity hot-channel` family: it discovers an already-running target, starts a temporary local worker, builds a deterministic scenario, runs it, and writes a report. The first version is fixed-N only. Search can be added later once the fixed 10k evidence is trustworthy.

## Server Bench API

Extend `/bench/v1/*` with a restricted channel runtime control surface. These routes remain unauthenticated and must only be enabled in controlled benchmark environments.

### Capabilities

`GET /bench/v1/capabilities` adds:

```json
{
  "supports": {
    "channel_runtime_snapshot": true,
    "channel_runtime_probe": true,
    "channel_runtime_evict": true,
    "channel_runtime_faults": false,
    "channel_runtime_activate": false
  }
}
```

Targets that do not support the new routes report false or omit the fields. `wkbench activate-channels` must fail preflight when snapshot and probe are unavailable.

### Snapshot

`GET /bench/v1/channel-runtime/snapshot?run_id=...&profile=...`

Returns local node runtime state. The response is intentionally aggregate-first to avoid high-cardinality JSON for 10k channels:

```json
{
  "version": "bench/v1",
  "node_id": 1,
  "run_id": "activate-10k",
  "profile": "ten-thousand-groups",
  "active_total": 10000,
  "active_leader": 3334,
  "active_follower": 6666,
  "follower_parked": 6600,
  "activation_rejected_total": 0,
  "reactors": [
    {"reactor_id": 0, "leader": 53, "follower": 105, "parked": 102, "mailbox_depth": 0}
  ],
  "worker_queues": [
    {"pool": "store_append", "depth": 0}
  ]
}
```

### Probe

`POST /bench/v1/channel-runtime/probe`

Checks a run/profile/range without sending business traffic. It reports loaded leader/follower/parked counts and missing channels for a bounded selector. This is used during hold and final verification.

```json
{
  "run_id": "activate-10k",
  "profile": "ten-thousand-groups",
  "channel_type": 2,
  "range": {"start": 0, "end": 10000}
}
```

### Evict

`POST /bench/v1/channel-runtime/evict`

Evicts or stops selected runtime state for cleanup and cold-start repeatability. It must not delete durable channel metadata or messages.

```json
{
  "run_id": "activate-10k",
  "profile": "ten-thousand-groups",
  "channel_type": 2,
  "range": {"start": 0, "end": 10000}
}
```

### Faults

`POST /bench/v1/channel-runtime/faults`

Optional phase-two route for controlled slowdowns and pauses. Every fault must require a run id, a bounded selector, and a TTL. Baseline 10k activation does not depend on this route.

### Activate

`POST /bench/v1/channel-runtime/activate`

Optional diagnostic route for server-side runtime load without WKProto SEND. It is useful as an A/B control to isolate gateway/client overhead, but it must not be the default path or the source of success for the 10k activation benchmark.

## Runtime Boundary

Introduce a narrow internal runtime port rather than exposing clusterv2 internals to HTTP handlers:

```go
type ChannelRuntimeBenchController interface {
  Snapshot(ctx context.Context, query ChannelRuntimeQuery) (ChannelRuntimeSnapshot, error)
  Probe(ctx context.Context, query ChannelRuntimeQuery) (ChannelRuntimeProbeResult, error)
  Evict(ctx context.Context, query ChannelRuntimeQuery) (ChannelRuntimeEvictResult, error)
}
```

For `cmd/wukongimv2`, the internalv2 composition root wires this port from the clusterv2/channelv2 runtime. Legacy `internal/access/api` can expose capability fields as unsupported until the old path needs the feature.

## Activation Flow

`wkbench capacity activate-channels` performs:

1. Discover target API, gateway, metrics, and new channel-runtime capabilities.
2. Prepare deterministic group channels and subscribers through existing bench data APIs.
3. Capture cold runtime snapshots from all target nodes.
4. Connect the worker user pool.
5. Activate channels by sending exactly one WKProto group SEND per channel.
6. Capture active runtime snapshots and Prometheus metrics.
7. Hold without sends for the configured duration while polling snapshots.
8. Probe all channels or configured batches through `/channel-runtime/probe`.
9. Optionally evict selected runtime state.
10. Write `activation_report.json`, `summary.md`, raw metrics, runtime snapshots, pprof, worker report, and console summary.

## Success Criteria

The first version fails when any of these are true:

- `activation_success != channels`
- `activation_errors > 0`
- `activation_rejected_delta > 0`
- cluster-summed `active_leader < channels` after activation
- hold/probe reports missing active leaders for any selected channel
- measured activation sendack p99 exceeds the configured gate
- any target route or worker phase fails

Follower and parked follower counts are diagnostic in phase one because the expected value depends on ChannelV2 follower parking policy.

## Report

`activation_report.json` includes:

- scenario: channel count, members, users, activation concurrency, hold duration, run id
- target: API/gateway/metrics addresses and capabilities
- activation: duration, actual activation qps, success, errors, sendack p50/p95/p99/max
- runtime snapshots: cold, active, hold samples, final probe
- ChannelV2 deltas: active, rejected, pull, recovery, meta cache, append, worker task
- artifacts: raw metrics, pprof files, worker report, logs when provided by wrapper scripts

`summary.md` is a concise human-readable version with explicit PASS/FAIL reasons.

## Testing

- Unit-test CLI parsing and default config for `capacity activate-channels`.
- Unit-test scenario generation uses one stable run id and produces exactly N channels.
- Unit-test activation evaluation fails on missing active leaders, activation rejections, send errors, and p99 gate failures.
- Unit-test bench API handlers for snapshot/probe/evict request validation and capability reporting.
- Unit-test runtime controller adapters with fake clusterv2/channelv2 state.
- Keep real three-node 10k activation as a manual or integration test, not a normal unit test.

## Rollout

1. Add server capabilities and read-only snapshot/probe first.
2. Add `wkbench capacity activate-channels` using real SEND activation and hard assertions.
3. Add evict for repeatable cold-start experiments.
4. Add optional faults and server-side activate only after the baseline report is useful.
