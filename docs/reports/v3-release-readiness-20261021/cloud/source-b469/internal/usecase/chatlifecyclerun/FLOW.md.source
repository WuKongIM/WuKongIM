---
scope: package
summary: Materializes bounded repair, rehearsal, and formal chat-lifecycle run plans from reviewed templates and trusted context.
---

# Chat Lifecycle Run Flow

## Responsibility

This package binds four operator inputs and trusted workflow context to the
versioned repository Run Plan, generic Cloud Lease input, and public bootstrap
identities. It never calls providers, deploys hosts, runs workers, or retains
private credentials.

## Boundaries

- Infrastructure quantities and workload thresholds come from a reviewed
  template. The direct repair controller may derive its bounded workload and
  Lease durations from the operator's qualification window and bind an
  explicitly authorized whole-CNY repair budget independently of the template.
- Each stage has exactly one procurement attempt. Deployment readiness repairs
  reuse the exact Lease, bundle, sealed identity, and request-bound control fix.
- Runtime YAML owns workload details; this use case owns cross-stage identity,
  budget, duration, and transition policy.

## Main Flows

1. Validate source, operator, request, bundle, clock, attempt, and trusted
   context; materialize either the bounded direct-repair Lease or the fixed
   12-hour rehearsal Lease and budget ledger.
2. Require a typed released rehearsal transition with exact zero inventory,
   matching identities, public diagnostic owner, and carried commitment.
3. Materialize a fresh 96-hour formal lease bound to the same source, bundle,
   request, diagnostic identity, and aggregate ledger.

## Invariants and Failure Semantics

- The shared CNY 1,350 operational stop and CNY 1,500 hard limit cover both
  stages.
- Topology is fixed at four Ubuntu x86 hosts, one EIP, and reviewed disk and
  port settings; rehearsal runs two hours and formal runs 72 hours after their
  two-hour readiness windows.
- Direct repair accepts a 16-minute through 72-hour-15-minute workload ceiling
  and derives its Lease as the larger of six hours or workload plus four hours
  45 minutes. Its default hard/stop limits are CNY 300/250; an exact explicit
  whole-CNY authorization may raise the hard limit through CNY 1,500, with a
  CNY 20 operational reserve, while every other quantity remains fixed.
- No fresh Lease is created for deployment retry.
- The exact release selector is derived before paid Acquire so ambiguous or
  artifact-losing acquisition remains cleanup-capable.

## Read First

- [Run plan materialization](run.go)
- [Run plan contracts](run_test.go)

## Update Triggers

Update this file when operator inputs, stage templates, topology, budgets,
durations, transition proof, retry policy, or cleanup selector derivation changes.
