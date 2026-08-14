---
scope: package
summary: Validates exact-run Analysis MCP requests and returns bounded observations from narrow live sources.
---

# Cloud Analysis Use Case Flow

## Responsibility

This package is the entry-independent Analysis MCP use case. Every call binds
to one exact Run Identity, proves it has not been released, invokes one narrow
source, and caps the final JSON response.
It does not own MCP transport, live source adapters, or cloud resource lifecycle.

## Boundaries

- Metrics select server-owned query IDs; callers cannot provide PromQL, URLs,
  paths, shell commands, configuration writes, restarts, or cleanup actions.
- Logs, diagnostics, workload summaries, and profiles use fixed bounded
  contracts. Source and cloud adapters live outside this package.
- Exact identifiers may appear as bounded evidence, never as metric labels.

## Main Flows

1. Validate run, node, selector, time range, and count; `InspectRun` stops the
   call when provider inventory proves release.
2. Invoke the closed source method and wrap its data with an explicit source
   and observation window before applying the response-size gate.
3. `run_inspect` additionally validates source commit, scenario digest,
   non-zero deterministic seed, full effective scenario, and 256-slot identity.

## Invariants and Failure Semantics

- Missing, nullable, or incomplete evidence remains unknown and is never
  rewritten as zero, empty, healthy, or complete.
- Metrics aggregate away UID, Channel, authority target, Slot, session, and
  message identities; dimensions remain fixed and low-cardinality.
- Active diagnostics serialize trace rules and profile kinds. CPU is at most
  30 seconds per call and 60 seconds per Analysis Session.
- Workload inspection returns structured lifecycle, phase windows, actual QPS,
  successful sends, and bounded failures rather than raw reports or parsed prose.
- Released or inconsistent Run Identity evidence fails closed before a live
  observability source is touched.

## Read First

- [Analysis service](service.go)
- [Analysis types](types.go)
- [Diagnosis contracts](diagnosis.go)

## Update Triggers

Update this file when tool inputs, run inspection, fixed metrics, cardinality,
diagnostics, profile budgets, workload lifecycle, or response bounds change.
