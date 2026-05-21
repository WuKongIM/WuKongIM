# wkbench Compose Performance Regression Design

## Context

WuKongIM already has a black-box benchmark runtime under `internal/bench` and a
Docker Compose three-node development cluster in `docker-compose.yml`. The
existing tools serve two different purposes:

- `wkbench run` performs a bounded benchmark from explicit target, worker, and
  scenario YAML files.
- `wkbench dev-sim` runs a long-lived simulator for local development and
  triage.

The new need is different: after each code change, a developer wants a one-command
check that detects performance regressions in the current Compose three-node
cluster. This check must be reproducible on the same machine and compare the
current run with a known-good local baseline. It is not, by default, a machine
capacity search.

## Goals

- Provide a one-command local regression gate for the current `docker-compose.yml`
  three-node cluster.
- Report ingress QPS plus send acknowledgement latency p50, p95, and p99.
- Compare the current result with a local baseline and fail on meaningful
  regressions.
- Keep the benchmark black-box: use public HTTP readiness, benchmark HTTP APIs,
  and WKProto gateway connections only.
- Reuse `internal/bench` coordinator, planner, worker runner, metrics, and report
  primitives instead of creating a shell-only benchmark path.
- Preserve the project deployment semantics: the local Compose deployment is a
  three-node cluster, and a single-node deployment remains a single-node cluster.

## Non-Goals

- The regression command does not find the current machine's maximum QPS.
- The regression command does not replace evidence-first performance triage for
  failures, timeouts, or suspicious regressions.
- The first version does not require remote workers or CI-specific orchestration.
- The first version does not automatically tune server configuration.

## User-Facing Commands

### Regression Gate

```bash
go run ./cmd/wkbench regress compose-send
```

This command runs a fixed workload against the Compose cluster and compares the
measured result with a local baseline.

Useful flags:

```bash
--duration 60s
--warmup 15s
--cooldown 3s
--report-dir docs/development/perf-runs/<auto>
--baseline tmp/wkbench-baselines/compose-send.local.json
--record-baseline
--fail-on-soft
--compose-up
--build
```

Default behavior should not run `docker compose up --build` automatically. The
recommended daily flow is explicit:

```bash
docker compose up -d --build wk-node1 wk-node2 wk-node3
go run ./cmd/wkbench regress compose-send
```

`--compose-up --build` can be added after the core command is stable for a more
integrated one-command flow.

### Capacity Search

```bash
go run ./cmd/wkbench capacity compose-send
```

This command is separate from the regression gate. It performs a step or binary
search over offered ingress QPS and reports the highest stable QPS for the current
machine and scenario.

The capacity command is slower and more sensitive to Docker Desktop, host CPU,
background load, and accumulated data. It is intended for occasional measurement,
not every code change.

## Regression Workload

The default regression workload should be fixed and laptop-safe enough to run
regularly while still exercising the send hot path:

- Target: current Compose three-node cluster.
- Traffic: send plus sendack verification.
- Receive verification: disabled by default for send-path regression.
- Warmup: 15 seconds.
- Measured run: 60 seconds.
- Cooldown: 3 seconds.
- Payload: deterministic 128-byte payload.
- Users: deterministic generated UID, device, and client message prefixes.
- Person traffic: fixed number of one-to-one channels and per-channel rate.
- Group traffic: fixed number of group channels, member count, and per-channel
  rate.

The generated offered ingress QPS should be explicit in the report:

```text
offered_qps = sum(channel_count * rate_per_channel for measured traffic)
```

The measured QPS should be derived only from successful sendack samples during
`phase=run`:

```text
actual_qps = run_phase_send_success_total / measured_run_seconds
```

## Metrics Model

The current metrics registry already supports counters and latency histograms.
Regression reporting needs phase-aware metrics so warmup does not pollute the
measured run.

Required metric label additions:

- `phase`: `warmup` or `run`.
- `channel_type`: `person` or `group`.
- `traffic`: scenario traffic name.

Examples:

```text
person_send_success_total{phase=run,channel_type=person,traffic=person-send}
group_send_success_total{phase=run,channel_type=group,traffic=group-send}
person_send_latency_seconds{phase=run,channel_type=person,traffic=person-send}
group_send_latency_seconds{phase=run,channel_type=group,traffic=group-send}
```

The registry already restricts label keys to low-cardinality names, and these
labels fit the existing policy. UID, channel ID, message ID, and run ID must stay
forbidden as metric labels.

The first implementation can keep the existing aggregate percentile behavior:
merged p50, p95, and p99 are the maximum worker-local percentiles. This is
conservative for regression gates and avoids adding histogram bucket merging in
the first iteration. The report should state that these are max worker-local
percentiles, not globally recomputed percentiles.

## Report Summary

Extend `internal/bench/report.Summary` with send-throughput fields used by the
regression and capacity commands:

```go
type Summary struct {
    IngressQPS float64
    PersonQPS float64
    GroupQPS float64
    SendackP50 time.Duration
    SendackP95 time.Duration
    SendackP99 time.Duration
    // existing error-rate and worker-failure fields remain
}
```

The summary must be computed from `phase=run` metrics only. Existing hard and soft
limit evaluation continues to use error rates and p99 latency, but the regression
command adds baseline comparisons.

The human summary should include:

```text
status: passed|failed
run_duration: 60s
offered_qps: 250.0
actual_qps: 246.8
person_qps: 123.4
group_qps: 123.4
sendack_p50: 12.3ms
sendack_p95: 41.8ms
sendack_p99: 88.5ms
sendack_error_rate: 0
connect_error_rate: 0
report: docs/development/perf-runs/<timestamp>-compose-send/
```

## Baseline Model

Store local baselines outside tracked source by default:

```text
tmp/wkbench-baselines/compose-send.local.json
```

A baseline contains:

```json
{
  "version": "wkbench/regression-baseline/v1",
  "scenario": "compose-send",
  "created_at": "2026-05-22T10:00:00+08:00",
  "git_commit": "...",
  "compose_config_hash": "...",
  "go_version": "...",
  "host_hint": "darwin-arm64",
  "offered_qps": 250.0,
  "actual_qps": 246.8,
  "sendack_p50_ms": 12.3,
  "sendack_p95_ms": 41.8,
  "sendack_p99_ms": 88.5,
  "sendack_error_rate": 0,
  "connect_error_rate": 0
}
```

The baseline should include enough environment metadata to help detect invalid
comparisons, but the comparison should not require exact commit or clean git
state. A dirty worktree warning is useful, not a hard failure.

## Regression Thresholds

Default failure thresholds:

- `sendack_error_rate > 0`: fail.
- `connect_error_rate > 0`: fail.
- `worker_failed > 0`: fail.
- `actual_qps` lower than baseline by more than 10%: fail.
- `sendack_p95` higher than baseline by more than 20%: fail.
- `sendack_p99` higher than baseline by more than 30%: fail.

Default warnings:

- No baseline exists.
- Git worktree is dirty.
- Compose config hash differs from the baseline.
- Sample count is too low for stable p99 interpretation.
- Docker stats show the local machine or containers are saturated.

Thresholds should be configurable with flags after the first stable version.

## Capacity Search Design

The capacity command should use the same target discovery and scenario generator
as regression, but vary the offered QPS. It should report the highest stable QPS
under explicit stability criteria.

Default stability criteria:

- `sendack_error_rate == 0`.
- `connect_error_rate == 0`.
- `actual_qps >= 95% of offered_qps`.
- `sendack_p99 <= configured max`, for example 200ms.

Search strategy:

1. Run a smoke workload.
2. Increase offered QPS by steps until a failure criterion triggers.
3. Binary search between last stable and first unstable rate.
4. Confirm the selected stable rate with one final run.

Output:

```text
max_stable_qps: 360.0
first_unstable_qps: 375.0
criterion: sendack_error_rate=0 actual_qps>=95% offered p99<=200ms
report_dir: docs/development/perf-runs/<timestamp>-capacity-compose-send/
```

## Internal Architecture

Add a small regression/capacity layer without changing benchmark boundaries:

```text
cmd/wkbench
  regress compose-send
  capacity compose-send
      -> internal/bench/regression
           -> Compose endpoint defaults
           -> temporary local worker lifecycle
           -> deterministic scenario generation
           -> coordinator.Run
           -> report summary extraction
           -> baseline read/write/compare
           -> capacity search loop
```

Proposed package:

```text
internal/bench/regression/
  compose.go        Compose endpoint defaults and optional compose-up helper
  scenario.go       Deterministic compose-send scenario generation
  worker.go         Temporary local worker lifecycle
  baseline.go       Baseline model, read/write, comparison
  runner.go         Regression and capacity command orchestration
  summary.go        Human and JSON output helpers
```

The temporary worker can run in-process if it can reuse `worker.NewServer` with a
local HTTP listener, or as a child `wkbench worker` process if that keeps lifecycle
simpler. In-process is preferred for unit testing and to avoid requiring a second
manual command.

## Data Flow

Regression flow:

```text
wkbench regress compose-send
  -> discover Compose endpoints from defaults or flags
  -> optionally run docker compose up
  -> start temporary worker control server
  -> generate target, workers, scenario
  -> coordinator.Run
  -> write normal wkbench report directory
  -> extract run-phase QPS and latency summary
  -> load local baseline if present
  -> compare current result to baseline thresholds
  -> print human summary and return stable exit code
```

Capacity flow:

```text
wkbench capacity compose-send
  -> run smoke workload
  -> iterate offered QPS values
  -> for each rate, run bounded scenario and evaluate stability
  -> binary search final interval
  -> write capacity report summary
  -> return success if at least one stable rate exists
```

## Error Handling

- If Compose target health or readiness fails, return preflight failure and print
  the first failed endpoint.
- If the benchmark API is disabled, return config/preflight failure and explain
  that `WK_BENCH_API_ENABLE=true` is required in Compose node configs.
- If no baseline exists, the regression command should complete the run and print
  instructions for `--record-baseline`; it should not claim a regression verdict.
- If baseline comparison fails, return a hard-limit style non-zero exit code.
- If `smoke-default` style health fails before a high-rate run, stop and ask the
  user to diagnose baseline health first.

## Testing Strategy

Unit tests:

- Scenario generation produces deterministic target QPS and run IDs.
- Baseline comparison fails and passes around configured thresholds.
- Summary extraction uses only `phase=run` metrics.
- Capacity search chooses the highest stable rate from fake runner results.
- CLI parsing rejects invalid subcommands and flag combinations.

Integration-style tests, opt-in only:

- Compose regression smoke using the real three-node cluster, guarded by an e2e or
  integration build tag.
- Capacity search smoke with tiny rates and short windows.

Keep normal unit tests fast. Real Compose and long benchmark runs must remain
behind integration or e2e tags.

## Rollout Plan

1. Add phase-aware metric recording for person and group send success and latency.
2. Extend report summary with run-phase QPS and p50/p95/p99 fields.
3. Add `internal/bench/regression` baseline comparison and scenario generation.
4. Add `wkbench regress compose-send` using a temporary local worker.
5. Add `wkbench capacity compose-send` using the same runner and a fake-runner
   test harness for the search logic.
6. Document daily usage in `cmd/wkbench/README.md` and `docker/sim/README.md`.
7. Add optional Compose evidence collection hooks only after the core regression
   gate is stable.

## Open Questions

- What default offered QPS should the Compose regression use on the main developer
  machine: 250, 500, or another value?
- Should the first version include group traffic, or start with person send-path
  only and add group fanout as a second named scenario?
- Should local baselines live under `tmp/` only, or should developers be able to
  opt into `docs/development/perf-baselines/` for shared machine profiles?
