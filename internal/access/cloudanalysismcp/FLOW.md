---
scope: package
summary: Adapts authenticated Analysis MCP and token requests to a closed, bounded observation usecase surface.
---

# Cloud Analysis MCP Flow

## Responsibility

`internal/access/cloudanalysismcp` registers the approved Analysis tools with
the MCP SDK, validates protocol input, and delegates observations to
`internal/usecase/cloudanalysis`. It also adapts the Analysis token HTTP request
through injected claim-verifier and issuer callbacks.
It does not own diagnostic policy, live evidence collection, or cloud lifecycle.

## Boundaries

- The adapter is stateless, JSON-response-only, and owns neither OIDC keys nor
  Analysis Token storage.
- Tool schemas accept fixed IDs and bounded filters, never shell, paths, URLs,
  arbitrary PromQL, restart, configuration, cloud mutation, or deletion.
- Raw profiles, worker text, reports, URLs, and filesystem details stay private.

## Main Flows

1. Authenticate the run-scoped bearer session, reject cross-origin requests,
   validate inferred JSON schemas, and dispatch an allowlisted tool.
2. Project bounded observations and parsed workload/profile summaries with
   incomplete evidence explicitly nullable or partial.
3. Parse `/analysis/token`, verify GitHub OIDC through callbacks, and serialize
   only the bounded issued-session response.

## Invariants and Failure Semantics

- The tool registry is closed world. Only trace/profile capture is active and
  non-destructive; every other tool is read-only.
- Failure descriptions come from fixed reason-code templates and never echo raw
  untrusted or secret material.
- Metrics queries select server-owned query IDs only; profile output contains
  bounded symbol summaries rather than raw profiles.

## Read First

- [MCP handler](handler.go)
- [Token adapter](token.go)
- [Handler tests](handler_test.go)

## Update Triggers

Update this file when authentication, tool inventory, active diagnostics,
schema bounds, redaction, token callbacks, or raw-data exposure changes.
