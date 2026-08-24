---
scope: package
summary: Implements bounded private-origin sources and strict parsers for live cloud simulation analysis.
---

# Cloud Analysis Infrastructure Flow

## Responsibility

This package implements the live analysis ports for Manager state, Prometheus
range queries, node profiles, run inspection, workload evidence, and strict
diagnostic parsing.
It does not own diagnosis policy, MCP transport, or cloud resource lifecycle.

## Boundaries

- Sources contact only configured private origins and use run-scoped Manager
  authentication with bounded response bodies.
- Prometheus receives resolved, fixed-purpose PromQL. Callers cannot use this
  package as an arbitrary query proxy.
- CPU, heap, and goroutine profiles come only from allowlisted nodes. Raw
  profiles remain bounded in memory; consumers receive metadata and parsed
  top rows.
- Run inspection validates exact provider identity. The package does not
  provision, mutate, or release cloud resources.

## Main Flows

1. Source adapters authenticate and fetch bounded Manager, Prometheus, or
   workload evidence. Workload inspection prefers the final summary and
   otherwise parses the strict three-worker atomic running status.
2. Profile collection captures an allowed bounded profile and converts it to
   safe summary rows without exposing raw profile bytes.
3. Run inspection combines static identity, provider inventory, diagnostics,
   and one-second cgroup evidence into strict analysis inputs.

## Invariants and Failure Semantics

- Missing, malformed, truncated, or identity-mismatched evidence fails closed.
  Running status never reads the command journal or exposes worker logs.
- Diagnostic summaries accept only the documented operations, statuses, exit
  codes, and verdict combinations.
- Heap analysis uses the fixed sample type; profile type and duration are not
  caller-defined escape hatches.
- Bootstrap requires readable cgroup v1 or v2 evidence rather than inferring
  resource health.

## Read First

- [HTTP sources](http_sources.go)
- [Prometheus adapter](prometheus.go)
- [Profile handling](profiles.go)
- [Run inspector](run_inspector.go)
- [Workload source](workload_source.go)

## Update Triggers

Update this file when source origins, authentication, evidence bounds, profile
formats, identity validation, or diagnostic parsing change.
