---
scope: package
summary: Defines closed-world Operations MCP validation and bounded observation envelopes over narrow sources.
---

# Operations Observation Use Case Flow

## Responsibility

This package owns entry-independent Operations MCP request validation, the
frozen tool registry, source contracts, caching policy, and bounded
`wukongim/ops-observation/v1` responses.
It does not own MCP transport, authentication, or concrete observation sources.

## Boundaries

- Requests cannot provide URLs, paths, commands, PromQL, SQL, or a general
  Controller writer.
- Sources implement cluster, node, Slot, Channel, task, metrics, logs,
  diagnostics, redacted config, backup, and profile observations.
- Authentication, MCP transport, concrete cluster access, and profile capture
  runtime live outside the use case.

## Main Flows

1. Closed-world decode and validate one named tool request.
2. Invoke exactly one narrow source method and apply tool-specific bounds and
   optional short inventory or redacted-config caching.
3. Return a response capped at 1 MiB with explicit unavailable/unknown evidence.

## Invariants and Failure Semantics

- Missing or failed source evidence is `unavailable` with `unknown` verdict,
  never zero or healthy.
- Logs use opaque cursors, untrusted content, 8-KiB lines, default 100 and max
  200 lines. Metric ranges are at most 24 hours, 100 series, and 2,000 points.
- Point Channel reads never scan the catalog. Inventory cache is three seconds;
  redacted config is 30 seconds; logs, diagnostics, and profiles are uncached.
- Backup inspection never exposes repository credentials.
- Profile analysis is the only active observation and is bounded to one node,
  supported kind, at most 30 CPU seconds, and at most 100 rows.

## Read First

- [Observation service](service.go)
- [Tool and response contracts](types.go)

## Update Triggers

Update this file when registry tools, validation, source ports, response bounds,
cache policy, log or metric limits, backup projection, or profiling changes.
