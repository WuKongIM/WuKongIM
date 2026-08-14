---
scope: package
summary: Composes product and Agent runtimes and owns their dependency-safe lifecycle ordering.
---

# internal/app Flow

## Responsibility

This package is the only composition root under `internal`. It converts
validated configuration into product access adapters, use cases, node-local
runtimes, infrastructure adapters, cluster/gateway services, observability,
and lifecycle ownership. It also contains standalone Issue Agent, Review Agent,
Cloud Analysis, and Cloud View composition roots that do not start the product
cluster.

## Boundaries

Business policy belongs in `internal/usecase`, entry mapping in
`internal/access`, node-local capabilities in `internal/runtime`, and concrete
adapters in `internal/infra` or `pkg`. This package may adapt sibling DTOs and
wire optional capabilities, but must not become a global service object or a
second implementation of those layers.

## Main Flows

```text
validated Config
  -> construct cluster and shared runtime foundations
  -> construct use cases and infrastructure ports
  -> register node RPC and access adapters
  -> expose optional API, Manager, metrics, diagnostics, plugins, and gateway

Start
  -> cluster/control readiness
  -> internal producers and post-commit consumers
  -> API and Manager
  -> Prometheus and Gateway admission

Stop or startup rollback
  -> close entry admission
  -> drain Channel append and accepted post-commit work
  -> stop side-effect, presence, and cluster dependencies in reverse order
```

## Invariants and Failure Semantics

- Every product deployment, including one node, uses cluster semantics. Wiring
  must not introduce a local business bypass.
- Optional features are wired only when all required ports exist; unavailable
  capabilities stay explicit instead of receiving partial implementations.
- Channel append producers start after their post-commit consumers and drain
  before those dependencies stop. A drain timeout returns promptly but keeps
  dependencies alive so a later `Stop` can continue the same drain.
- Startup failure rolls back completed components in reverse order. Constructor
  failure releases constructor-owned pools, sinks, and audit resources.
- Gateway admission opens only after cluster write routing and required runtime
  readiness. Joining nodes remain fenced until observed membership permits it.
- Restore maintenance keeps Manager reachable while product traffic is fenced;
  restore-sensitive caches and side-effect runtimes are reactivated before
  Controller clears maintenance.
- Observability is bounded and low-cardinality; runtime labels must not contain
  UIDs, Channel IDs, client message IDs, addresses, or secret material.
- Issue/Review Agent composition keeps read, verification, signed-state, and
  publication credentials separated and never joins the product cluster.

## Read First

- [app.go](app.go)
- [FLOW_PRODUCT_RUNTIME.md](FLOW_PRODUCT_RUNTIME.md)
- [backup.go](backup.go)
- [issue_agent.go](issue_agent.go)
- [review_agent.go](review_agent.go)

## Update Triggers

- Dependency ownership or the sole-composition-root boundary changes.
- Product startup, readiness, rollback, drain, or shutdown ordering changes.
- Restore maintenance or side-effect lifecycle fencing changes.
- Optional capability wiring or Agent credential/authority separation changes.
