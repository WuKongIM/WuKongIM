---
scope: package
summary: Provides the temporary entry-agnostic gateway feedback facade and explicit legacy committed-event rejection.
---

# Delivery Compatibility Use Case Flow

## Responsibility

This package is the temporary gateway feedback facade for receive ACK and
session-close commands. Canonical recipient plans now go directly from Channel
append through `internal/contracts/onlinedelivery` to the delivery runtime.
It does not select recipients, construct plans, or execute online delivery.

## Boundaries

- `SubmitCommitted` remains only for source compatibility and production
  composition returns the canonical-plan-required error without fanout work.
- Runtime adapters translate use-case DTOs to runtime DTOs.
- This package must not import gateway frames, protocol frames, cluster,
  Channel, access, app, or the concrete delivery runtime.

## Main Flows

1. A legacy committed-event call is explicitly rejected as requiring a
   canonical recipient plan.
2. `RecvackCommand` delegates the exact feedback identity to the runtime port.
3. `SessionClosedCommand` delegates exact session cleanup to the runtime port.

## Invariants and Failure Semantics

- The compatibility surface never reconstructs subscriber fanout or a delivery
  plan from a committed event.
- Feedback DTOs remain entry-independent and contain no gateway frame types.
- Import-boundary tests are part of the package contract.

## Read First

- [Application facade](app.go)
- [Ports](ports.go)
- [Compatibility submission](submit.go)
- [Use-case types](types.go)

## Update Triggers

Update this file when the compatibility API, feedback commands, DTO mapping, or
import boundary changes. Remove it when the compatibility surface is retired.
