---
scope: package
summary: Formats immutable presence-provider evidence for existing access-adapter failure logs.
---

# Presence Failure Log Flow

## Responsibility

Convert existing typed provider failure evidence into one fixed scalar field
group shared by the node RPC and gateway access adapters.

## Boundaries

No business policy, new event, retry, routing, wire field, mutable observation,
or request identity belongs here. The presence usecase owns the evidence.

## Main Flows

Existing adapter failure -> typed immutable evidence -> known-mask-selected fields.
Absent evidence leaves the supplied field slice unchanged.

## Invariants and Failure Semantics

Physical-round timings are not exclusive caller timings. Missing stages stay
absent. Fixed reasons, masks and field order remain identical across adapters.

## Read First

- [Provider formatter](provider.go)
- [Provider evidence](../../usecase/presence/recovery_provider_failure.go)

## Update Triggers

Update when evidence fields, stage masks, formatting ownership or consumers change.
