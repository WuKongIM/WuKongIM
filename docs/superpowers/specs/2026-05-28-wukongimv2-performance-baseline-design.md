# wukongimv2 Performance Baseline Design

Date: 2026-05-28
Status: Draft for review
Owner: Codex

## 1. Purpose

`cmd/wukongimv2` needs a stable performance baseline before more `internal`
features are migrated into `internalv2`. The comparison target is not
`cmd/wukongim`; it is the previous accepted `cmd/wukongimv2` milestone.

The goal is to make performance regressions attributable during migration:

```text
wukongimv2 M0 baseline
  -> migrate one bounded capability
  -> run the matching v2 scenario
  -> compare current v2 with the accepted v2 baseline
  -> promote only after function and performance are both accepted
```

This keeps the migration path evidence-driven. If a later feature causes a
throughput drop or sendack latency increase, the team can tie the regression to
the specific v2 milestone that introduced it.

Single-node deployment remains a single-node cluster. The performance baseline
must not introduce local-only send, routing, storage, or delivery shortcuts.

## 2. Selected Approach

Use a milestone baseline system for `cmd/wukongimv2` only.

Core rules:

- Every scenario compares the current `cmd/wukongimv2` result with the latest
  accepted baseline for the same scenario.
- Baselines are promoted manually after a milestone is functionally correct and
  performance is accepted.
- Historical milestone results are retained for trend analysis, but only the
  accepted baseline is used as the migration gate.
- A failed performance gate stops further migration until the regression is
  classified with evidence.

Rejected alternatives:

- Comparing `cmd/wukongimv2` with `cmd/wukongim`: rejected because the user wants
  v2 self-comparison. The old binary may be useful for product-level reference,
  but it must not be the migration gate.
- Automatically updating baselines after every run: rejected because it can
  normalize a regression and erase the signal needed for attribution.
- Starting with mixed high-rate workloads: rejected because phase 1 only proves
  `SEND -> SENDACK`; broad mixed traffic would make early regressions harder to
  classify.

## 3. Scope

In scope for the first baseline milestone:

- Define v2-only scenario names and success criteria.
- Store accepted and historical baseline summaries.
- Store run evidence under the existing performance evidence layout.
- Compare current runs with accepted v2 baselines.
- Support a manual baseline promotion workflow.
- Gate future `internalv2` migration steps by scenario.

Out of scope for the first baseline milestone:

- Full group fanout, recv correctness, conversation, CMD, plugin, or management
  performance gates.
- Automatic server tuning.
- CI enforcement by default.
- Capacity search for maximum QPS.
- Replacing the evidence-first triage workflow in
  `docs/development/PERF_TRIAGE.md`.

## 4. Baseline Model

Each scenario has one accepted baseline and an append-only historical log:

```text
docs/development/perf-baselines/wukongimv2/
  v2-sendack-hotpath.accepted.json
  v2-sendack-hotpath.history.jsonl
  v2-sendack-sustained.accepted.json
  v2-sendack-sustained.history.jsonl
```

The accepted file is the gate. The history file records promoted milestones.

Baseline fields:

```json
{
  "version": "wukongimv2/perf-baseline/v1",
  "scenario": "v2-sendack-hotpath",
  "milestone": "M0-sendack-skeleton",
  "created_at": "2026-05-28T10:00:00+08:00",
  "git_commit": "...",
  "binary": "cmd/wukongimv2",
  "environment_class": "local-compose-three-node",
  "config_hash": "...",
  "workload_hash": "...",
  "trials": 3,
  "actual_qps": 1000.0,
  "sendack_p50_ms": 5.0,
  "sendack_p95_ms": 20.0,
  "sendack_p99_ms": 50.0,
  "sendack_error_rate": 0.0,
  "timeout_count": 0,
  "max_goroutines": 1000,
  "max_heap_bytes": 268435456,
  "gc_pause_p99_ms": 5.0,
  "evidence_dir": "docs/development/perf-runs/20260528-v2-sendack-hotpath"
}
```

The first implementation may omit fields that are not yet collected, but the
schema should keep their names stable once introduced.

## 5. Scenario Matrix

Phase 1 starts with only the `SEND -> SENDACK` path.

```text
v2-smoke-default
  Purpose: prove cmd/wukongimv2 startup, gateway connect, send, and sendack.
  Gate: no send errors, no sendack timeout, service exits cleanly.

v2-sendack-hotpath
  Purpose: measure steady SEND -> SENDACK throughput and latency.
  Gate: actual_qps, sendack p50/p95/p99, error rate, timeout count.

v2-sendack-sustained
  Purpose: catch drift under longer running sendack traffic.
  Gate: p99 stability, heap growth, goroutine growth, GC pause, error rate.
```

Later scenarios should be added only when the migrated feature requires them:

```text
v2-person-hotpath      after personal-channel routing and append are migrated
v2-group-fanout        after group subscriber expansion or fanout is migrated
v2-sampled-correctness after recv/delivery correctness is in scope
v2-accumulated-data    after history-sensitive storage paths are in scope
```

The initial migration gate should not require scenarios whose underlying feature
does not exist in `internalv2` yet.

## 6. Comparison Policy

The comparison uses the current run summary and the accepted baseline for the
same scenario.

Default first-version thresholds:

```text
sendack_error_rate must not increase above baseline
timeout_count must not increase above baseline
actual_qps must be at least 90% of baseline
sendack_p99 must be at most 120% of baseline
sendack_p95 must be at most 115% of baseline
heap and goroutine growth are warnings unless they exceed explicit limits
```

To reduce local-machine noise, the hotpath scenario should run three trials and
compare median values. A single failed trial should be kept in the evidence
directory, not hidden.

Failures should be classified before code or config changes. If
`v2-smoke-default` fails, stop higher-rate scenarios and diagnose baseline
health first.

## 7. Evidence Layout

Every run writes an evidence capsule under the existing performance run root:

```text
docs/development/perf-runs/20260528-v2-sendack-hotpath/
  meta.json
  report.json
  compare.json
  status.jsonl
  metrics/
  pprof/
  logs/
```

`meta.json` records the comparison context:

```json
{
  "scenario": "v2-sendack-hotpath",
  "binary": "cmd/wukongimv2",
  "git_commit": "...",
  "baseline_id": "M0-sendack-skeleton",
  "environment_class": "local-compose-three-node",
  "config_hash": "...",
  "workload_hash": "...",
  "started_at": "2026-05-28T10:00:00+08:00"
}
```

`compare.json` records the gate decision and deltas:

```json
{
  "scenario": "v2-sendack-hotpath",
  "baseline_milestone": "M0-sendack-skeleton",
  "status": "passed",
  "actual_qps_ratio": 0.98,
  "sendack_p99_ratio": 1.04,
  "sendack_error_rate_delta": 0.0,
  "timeout_count_delta": 0,
  "warnings": []
}
```

Large raw artifacts should remain in `perf-runs` or local storage. The baseline
files should store compact summaries only.

## 8. Migration Gate

Each migration step must declare the smallest scenario set that can catch a
regression in that feature.

```text
gateway/protocol adapter migration:
  v2-smoke-default
  v2-sendack-hotpath

message usecase migration:
  v2-sendack-hotpath
  v2-sendack-sustained

clusterv2/channel append integration:
  v2-sendack-hotpath
  append/propose metrics in the evidence capsule

delivery/recv migration:
  add v2-sampled-correctness before making it a gate

group fanout migration:
  add v2-group-fanout before making it a gate
```

The gate should fail closed:

- If the run is unhealthy, do not promote a baseline.
- If a scenario regresses, do not continue migrating unrelated features.
- If a scenario is too noisy to judge, fix the benchmark or environment before
  using it as a gate.

## 9. Promotion Workflow

Promotion is an explicit human-approved action.

Recommended flow:

```text
1. Run v2-smoke-default.
2. Run the scenario for the migration milestone.
3. Inspect report.json and compare.json.
4. If function and performance are accepted, promote current summary.
5. Replace <scenario>.accepted.json.
6. Append the promoted summary to <scenario>.history.jsonl.
```

The promoted milestone name should describe the architectural change, such as:

```text
M0-sendack-skeleton
M1-gateway-adapter
M2-message-usecase
M3-clusterv2-append
```

## 10. Tooling Direction

The first implementation should reuse existing WuKongIM benchmark and evidence
tools where possible:

- Use `cmd/wkbench` or `wkbench dev-sim` for workload generation.
- Reuse `docs/development/PERF_TRIAGE.md` evidence expectations.
- Extend scripts or reports only where the v2 self-baseline workflow needs
  stable scenario names, compact summaries, comparison, or promotion.

The tooling should not add a config switch that makes `cmd/wukongim` execute
`internalv2`. `cmd/wukongimv2` remains the performance target for these gates.

## 11. Testing Strategy

Unit-level coverage should focus on deterministic pieces:

- Baseline file decode/encode.
- History append behavior.
- Threshold comparison logic.
- Promotion refusing unhealthy or mismatched scenario results.
- Scenario name and binary validation.

Process or e2e coverage should stay narrow:

- A tiny `v2-smoke-default` run against `cmd/wukongimv2`.
- A fake or reduced report comparison that proves pass/fail output.

Longer sustained performance runs should remain developer-invoked or scheduled,
not part of normal `go test ./...`.

## 12. Acceptance Criteria

- The migration performance gate compares `cmd/wukongimv2` only with previous
  accepted `cmd/wukongimv2` baselines.
- The first required scenarios are limited to v2 smoke and sendack hotpath.
- Baseline promotion is manual and explicit.
- Evidence for every run includes enough metadata to reproduce what was
  compared.
- Regressions stop further migration until classified with evidence.
- No local-only single-node shortcut is introduced.
- The design remains compatible with existing `wkbench`, `dev-sim`, and
  performance triage practices.
