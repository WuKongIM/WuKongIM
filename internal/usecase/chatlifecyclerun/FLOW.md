---
scope: package
summary: Materializes reviewed rehearsal and formal chat-lifecycle run plans from four operator inputs and trusted context.
---

# Chat Lifecycle Run Flow

## Responsibility

This package binds four operator inputs and trusted workflow context to the
versioned repository Run Plan, generic Cloud Lease input, and public bootstrap
identities. It never calls providers, deploys hosts, runs workers, or retains
private credentials.

## Boundaries

- Infrastructure quantities and workload thresholds come from the reviewed
  template, not command-line input.
- Each stage has exactly one procurement attempt. Deployment readiness repairs
  reuse the exact Lease, bundle, sealed identity, and request-bound control fix.
- Runtime YAML owns workload details; this use case owns cross-stage identity,
  budget, duration, and transition policy.

## Main Flows

1. Validate source, operator, request, bundle, clock, attempt, and protected
   workflow context; materialize the 12-hour rehearsal lease and budget ledger.
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
- No fresh Lease is created for deployment retry.
- The exact release selector is derived before paid Acquire so ambiguous or
  artifact-losing acquisition remains cleanup-capable.

## Read First

- [Run plan materialization](run.go)
- [Run plan contracts](run_test.go)

## Update Triggers

Update this file when operator inputs, stage templates, topology, budgets,
durations, transition proof, retry policy, or cleanup selector derivation changes.
