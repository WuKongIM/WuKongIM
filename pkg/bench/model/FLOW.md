---
scope: package
summary: Defines shared wkbench configuration, deterministic plans, reports, rates, scenario digests, and bench target API DTOs.
---

# Benchmark Model Flow

## Responsibility

This package owns the lightweight schemas shared by `cmd/wkbench` and the
benchmark-only target surface in `cmd/wukongim`.
It does not execute benchmarks, manage workers, or depend on server runtimes.

## Boundaries

- Keep only data models and lightweight parsing or digest helpers here; do not
  import internal, cluster, or server runtime packages.
- Exported fields are YAML, JSON, configuration, or HTTP contracts and require
  documentation.
- Worker assignments contain selected capacity profiles but never worker
  control credentials.

## Main Flows

1. Load and validate the effective scenario, then hash canonical JSON so plans,
   lifecycle tags, and Analysis MCP share one digest.
2. Produce deterministic worker identity, online-identity, source-address,
   churn, rate, and Hash Slot spread plans.
3. Exchange closed `bench/v1` target DTOs for assignments, progress, reports,
   and bounded Channel runtime probes.

## Invariants and Failure Semantics

- Optional client and TCP source profiles are complete when present; capacities
  are positive and cover the worker's final identity range.
- TCP source IPv4 addresses are unique and non-unspecified; ports remain in
  `1024..65535`. Omission delegates source selection to the OS.
- Long stability churn shares sum to one, reserve an offline swap lane, and
  disable history sync.
- Hash Slot spread maps channel index `n` to physical Hash Slot `n` for the
  declared profile count.
- Runtime probes use either the generated half-open range selector or at most
  1,200 explicit channels, never both; private errors map to the closed safe
  reason vocabulary.

## Read First

- [Scenario config](config.go)
- [Deterministic plan](plan.go)
- [Scenario digest](scenario_digest.go)
- [TCP source pool](tcp_source.go)
- [Bench target API](bench_api.go)

## Update Triggers

Update this file when schemas, scenario digest, capacity profiles, churn,
identity planning, Hash Slot spread, reports, or target API contracts change.
