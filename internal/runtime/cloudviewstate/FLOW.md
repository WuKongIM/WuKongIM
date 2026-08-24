---
scope: package
summary: Persists monotonic run-local evidence that public interaction or operator mutation affected a cloud simulation.
---

# Cloud View State Flow

## Responsibility

This package records monotonic simulator-local state that distinguishes a pure
benchmark from a run affected by public interaction or operator modification.
It does not interpret HTTP routes, Manager permissions, or cloud lifecycle.

## Boundaries

- It owns run-local state persistence, node-exporter projection, retry, and
  final report annotation.
- HTTP route interpretation, Manager permissions, and cloud resource lifecycle
  live outside this package.

## Main Flows

1. An entry adapter marks the run interactive or operator-modified before its
   irreversible effect.
2. The recorder atomically replaces the run-owned JSON state and node-exporter
   textfile projection, retrying degraded projections in the background.
3. Final report annotation reads live state before service shutdown completes.

## Invariants and Failure Semantics

- State is restored only for the exact Run Identity.
- Transitions are monotonic and remain conservatively set in memory when a
  persistence attempt fails.
- A final report fails closed when state is unavailable or persistence remains
  degraded.
- Persistence must precede the external interaction or mutation it records.

## Read First

- [State recorder](state.go)

## Update Triggers

Update this file when state fields, Run Identity fencing, atomic persistence,
retry, metrics projection, or report annotation changes.
