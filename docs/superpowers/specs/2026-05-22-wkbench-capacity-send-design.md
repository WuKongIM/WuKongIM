# wkbench Capacity Send Design

## Overview

Add a `wkbench capacity send` subcommand that measures the maximum stable
message ingress QPS of an already-running WuKongIM deployment. The command does
not start, stop, rebuild, or clean Docker Compose services. The user provides
target HTTP API addresses, and wkbench discovers the target gateway addresses
through a new benchmark-only endpoint.

The goal is capacity discovery, not daily regression gating. A capacity run
searches for the highest offered send rate that remains stable under explicit
success criteria such as zero sendack errors, sufficient delivered offered load,
and bounded sendack tail latency.

## Goals

- Provide a single wkbench subcommand for maximum stable send QPS discovery.
- Require only already-running service API addresses for the common path.
- Reuse the existing black-box wkbench coordinator, worker, target, workload,
  metrics, and report packages.
- Keep the benchmark boundary external to server internals: preparation uses
  `/bench/v1/*`, traffic uses WKProto gateway connections.
- Report ingress QPS and sendack latency p50, p95, and p99 for each attempted
  rate and for the final stable rate.
- Preserve cluster semantics. A single-node deployment remains a single-node
  cluster, and no local-only business path is introduced.

## Non-Goals

- Do not start or stop `docker compose` services.
- Do not build images or clean data directories.
- Do not infer gateway addresses from Compose files.
- Do not add a bypass path that sends messages through HTTP or server internals.
- Do not make the first version a CI regression gate; that can be layered on top
  of the capacity result later.
- Do not measure group fanout delivery QPS as the primary capacity metric. The
  primary metric is successful ingress sendack QPS.

## User Interface

Minimal local Compose usage:

```bash
wkbench capacity send \
  --api http://127.0.0.1:15001,http://127.0.0.1:15002,http://127.0.0.1:15003
```

Useful optional flags:

```bash
wkbench capacity send \
  --api http://127.0.0.1:15001,http://127.0.0.1:15002,http://127.0.0.1:15003 \
  --profile mixed \
  --start-qps 100 \
  --max-qps 5000 \
  --step-factor 1.5 \
  --duration 30s \
  --warmup 10s \
  --cooldown 3s \
  --stable-p99 200ms \
  --min-actual-ratio 0.95 \
  --report-dir ./tmp/wkbench-capacity
```

Fallback for deployments that cannot publish gateway addresses through the
benchmark endpoint:

```bash
wkbench capacity send \
  --api http://127.0.0.1:15001,http://127.0.0.1:15002,http://127.0.0.1:15003 \
  --gateway 127.0.0.1:15100,127.0.0.1:15101,127.0.0.1:15102
```

Default flag values:

```text
profile: mixed
start-qps: 100
max-qps: 5000
step-factor: 1.5
duration: 30s
warmup: 10s
cooldown: 3s
stable-p99: 200ms
min-actual-ratio: 0.95
max-sendack-error-rate: 0
max-connect-error-rate: 0
binary-search: true
binary-search-min-delta-ratio: 0.05
```

## Bench Capacity Target API

Add a benchmark-only endpoint registered only when `WK_BENCH_API_ENABLE=true`:

```http
GET /bench/v1/capacity-target
```

Response:

```json
{
  "version": "bench/v1",
  "gateway": {
    "tcp_addr": "127.0.0.1:15100",
    "ws_addr": "ws://127.0.0.1:15200",
    "wss_addr": ""
  }
}
```

The endpoint uses the same published gateway address source as the legacy
`/route` endpoint:

- `tcp_addr` uses `WK_EXTERNAL_TCPADDR` when configured.
- `ws_addr` uses `WK_EXTERNAL_WSADDR` when configured.
- `wss_addr` uses `WK_EXTERNAL_WSSADDR` when configured.
- When those values are not configured, it uses the existing listener-derived
  fallback already held by the API server as legacy route addresses.

The endpoint returns only the local node's advertised gateway addresses. The
capacity command calls it once per user-provided API address and deduplicates the
returned TCP addresses.

This shape avoids asking one node to infer every other node's externally
reachable host port. That matters for Docker Compose because the three nodes
publish different host ports:

```text
node1 API 15001 -> gateway 15100
node2 API 15002 -> gateway 15101
node3 API 15003 -> gateway 15102
```

## Discovery Flow

`wkbench capacity send --api ...` performs:

```text
parse --api
for each API address:
  GET /healthz
  GET /readyz
  GET /bench/v1/capabilities
  GET /bench/v1/capacity-target
collect non-empty gateway.tcp_addr values
deduplicate gateway.tcp_addr values
validate at least one gateway TCP address
build model.Target with API, BenchAPI, and Gateway.TCP addresses
start one temporary local worker control server
run capacity search attempts through coordinator.Run
write capacity result and per-attempt wkbench reports
```

If `--gateway` is supplied, it overrides discovered gateway TCP addresses. The
command still checks API health, readiness, and bench capabilities.

Failure cases:

- Empty `--api` fails config validation.
- Bench API disabled or missing `/bench/v1/capacity-target` fails preflight.
- Empty `gateway.tcp_addr` fails with a message that asks the user to configure
  `WK_EXTERNAL_TCPADDR` or pass `--gateway`.
- Unreachable gateway addresses fail before search starts.

## Stable QPS Definition

An attempted offered QPS is stable when all conditions pass:

```text
worker_failed == 0
target_available == true
connect_error_rate <= max-connect-error-rate
sendack_error_rate <= max-sendack-error-rate
actual_qps >= offered_qps * min-actual-ratio
sendack_p99 <= stable-p99
```

The primary QPS value is ingress send QPS:

```text
actual_qps = successful sendack count during measured run / measured run seconds
```

For mixed and group profiles, group fanout creates additional delivery work, but
capacity is still reported as ingress sendack QPS.

## Search Algorithm

The search has two phases.

### Ramp Phase

Start at `--start-qps`. Each passing attempt increases offered QPS by
`--step-factor` until an attempt fails or `--max-qps` is reached.

Example:

```text
100 -> 150 -> 225 -> 337 -> 505 -> 757
```

The first failing attempt creates a bracket:

```text
last_pass_qps <= maximum stable qps < first_fail_qps
```

If all attempts pass up to `--max-qps`, the result is capped by `--max-qps` and
the report must say the true maximum was not found.

### Binary Search Phase

When `--binary-search=true`, search inside the bracket until:

```text
(first_fail_qps - last_pass_qps) / last_pass_qps <= binary-search-min-delta-ratio
```

The final result is the highest passing offered QPS.

## Profiles

The first version supports three built-in profiles.

### person

Measures one-to-one send path capacity.

```text
person channels: derived from offered QPS
group channels: 0
verify_recv: none
```

### group

Measures group send path capacity with fixed fanout pressure.

```text
person channels: 0
group channels: derived from offered QPS
group members: default 10
verify_recv: none
```

### mixed

Default profile. Splits offered ingress load between person and group messages.

```text
person traffic share: 50%
group traffic share: 50%
group members: default 10
verify_recv: none
```

The scenario generator should choose enough channels so per-channel rates remain
moderate and deterministic. A simple first version can keep per-channel rate at
`1/s` and set channel counts from offered QPS:

```text
person_channel_count = ceil(person_qps / 1)
group_channel_count = ceil(group_qps / 1)
```

Later versions can expose `--rate-per-channel` for advanced users.

## Internal Structure

Add package:

```text
internal/bench/capacity/
  config.go       // CLI-facing config, defaults, validation
  discover.go     // health/readiness/capabilities/capacity-target discovery
  search.go       // ramp and binary search
  scenario.go     // generated wkbench scenario per attempted QPS
  runner.go       // temporary worker plus coordinator orchestration
  result.go       // result model and markdown/json output
```

Extend existing packages:

```text
cmd/wkbench/main.go
  add "capacity" command and "send" subcommand parsing

internal/access/api/bench.go
  register GET /bench/v1/capacity-target
  return API server's legacy route external addresses

internal/usecase/benchdata/types.go
  add DTOs only if shared DTOs are preferable; no business usecase is required

internal/bench/model/bench_api.go
  add CapacityTarget DTOs used by wkbench target client

internal/bench/target/client.go
  add CapacityTarget(ctx) for one API address or deterministic address list

internal/bench/workload
  add low-cardinality phase/channel/traffic labels to send counters and latency

internal/bench/report
  add helpers to derive ingress QPS and sendack p50/p95/p99 from phase=run metrics
```

No new broad service package or global aggregate object is introduced.

## Metrics Requirements

Capacity results must use measured run metrics only. Warmup must not affect
reported p50, p95, p99, or QPS.

Send metrics should include low-cardinality labels:

```text
person_send_success_total{phase=run,channel_type=person,traffic=person-send}
group_send_success_total{phase=run,channel_type=group,traffic=group-send}
person_send_latency_seconds{phase=run,channel_type=person,traffic=person-send}
group_send_latency_seconds{phase=run,channel_type=group,traffic=group-send}
```

The existing metrics label allowlist already includes `phase`, `channel_type`,
`profile`, and `traffic`, so this stays within the low-cardinality boundary.

Aggregated percentile fields remain max worker-local percentiles unless raw
histogram bucket merging is added later. The capacity summary should state this
when multiple workers are used.

## Report Layout

Default report root:

```text
tmp/wkbench-capacity/<timestamp>-send/
  result.json
  summary.md
  discovered-target.json
  attempts/
    000100-qps/
      scenario.yaml
      target.yaml
      workers.yaml
      report.json
      summary.md
    000150-qps/
      report.json
      summary.md
```

`result.json`:

```json
{
  "status": "passed",
  "profile": "mixed",
  "max_stable_qps": 1120,
  "first_failed_qps": 1260,
  "criteria": {
    "min_actual_ratio": 0.95,
    "stable_p99_ms": 200,
    "max_sendack_error_rate": 0,
    "max_connect_error_rate": 0
  },
  "stable_attempt": {
    "offered_qps": 1120,
    "actual_qps": 1098.4,
    "sendack_p50_ms": 18.2,
    "sendack_p95_ms": 73.5,
    "sendack_p99_ms": 166.8,
    "sendack_error_rate": 0,
    "connect_error_rate": 0
  },
  "failed_attempt": {
    "offered_qps": 1260,
    "actual_qps": 1090.1,
    "sendack_p99_ms": 281.4,
    "reason": "sendack_p99_exceeded"
  }
}
```

Console output stays concise:

```text
wkbench capacity send

target:
  api: 3 nodes
  gateway: 3 nodes
profile: mixed
criteria:
  actual_qps >= 95% offered_qps
  sendack_p99 <= 200ms
  sendack_error_rate <= 0

attempts:
  100 qps   pass   actual=99.4    p50=8ms   p95=22ms   p99=38ms
  150 qps   pass   actual=149.1   p50=9ms   p95=25ms   p99=44ms
  225 qps   pass   actual=223.6   p50=11ms  p95=31ms   p99=58ms
  337 qps   pass   actual=333.8   p50=14ms  p95=45ms   p99=80ms
  505 qps   pass   actual=498.2   p50=18ms  p95=70ms   p99=130ms
  757 qps   fail   actual=681.0   p50=45ms  p95=180ms  p99=340ms

result:
  max_stable_qps: 505
  first_failed_qps: 757
  report: tmp/wkbench-capacity/20260522-153000-send
```

## Error Handling

- Config validation errors exit with `1`.
- Health, readiness, capabilities, capacity-target, or gateway preflight errors
  exit with `2`.
- A completed search with no stable attempt exits with `3` and writes a failed
  capacity result.
- Worker execution failures exit with `4`.
- Target unavailable during an attempt exits with `5` unless at least one stable
  bracket already exists and the failure is used as the first failed attempt.
- Unexpected internal errors exit with `6`.

The command should still write partial reports for completed attempts when a
later attempt fails.

## Testing Plan

Unit tests:

- `internal/access/api`: capacity-target route is disabled when bench API is
  disabled, and returns configured external route addresses when enabled.
- `internal/bench/target`: client decodes `/bench/v1/capacity-target` and reports
  clear errors for empty or invalid responses.
- `internal/bench/capacity`: config defaults, validation, gateway discovery
  deduplication, ramp search, binary search, and failure classification.
- `internal/bench/report`: QPS and latency summary helpers select `phase=run`
  metrics only.
- `cmd/wkbench`: CLI parsing for `capacity send`.

Targeted integration-style unit tests:

- Fake target API servers returning different capacity-target TCP addresses.
- Fake coordinator attempt runner that returns deterministic pass/fail results
  for search algorithm tests.

E2E tests:

- Optional e2e scenario under `test/e2e/bench` can run against a real three-node
  single-machine cluster with low `--max-qps` and short duration. Keep it behind
  an e2e tag to avoid slow unit tests.

## Documentation Updates

- Update `cmd/wkbench/README.md` with the new `capacity send` command.
- Update `internal/bench/FLOW.md` with the capacity package and flow.
- If API route descriptions are maintained elsewhere, document
  `/bench/v1/capacity-target` as benchmark-only and gated by
  `WK_BENCH_API_ENABLE=true`.
- No `wukongim.conf.example` change is required unless new configuration fields
  are added. This design reuses existing `WK_EXTERNAL_TCPADDR`,
  `WK_EXTERNAL_WSADDR`, and `WK_EXTERNAL_WSSADDR`.

## Open Decisions

- Whether first version should start exactly one temporary local worker or allow
  `--workers` to use pre-existing remote workers. Recommended: one local worker
  first, `--workers` later.
- Whether `stable-p99` default should be `200ms` or a higher laptop-friendly
  value such as `500ms`. Recommended: `200ms` for a strict sendack capacity
  definition, with the flag documented.
- Whether grouped traffic should default to `group_members=10` or expose
  `--group-members` immediately. Recommended: expose `--group-members` because
  fanout size strongly changes capacity.
