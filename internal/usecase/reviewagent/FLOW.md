---
scope: package
summary: Owns deterministic Review Agent commands, PR lifecycle reconciliation, scheduling, projection planning, and publication policy.
---

# Review Agent Use Case Flow

## Responsibility

This package owns deterministic pull-request lifecycle, command authorization,
scheduling, signed state, projection repair, publication, and merge-eligibility
planning.
It does not perform GitHub, Git, filesystem, verification, or model operations.

## Boundaries

- It accepts fresh facts, protected policy, validated signed state, and current
  UTC time; it performs no GitHub, Git, filesystem, shell, network, or model I/O.
- Only adapters execute plans. Model results never choose check conclusions.
- Human review state and admin authority remain external facts.

## Main Flows

1. Reconcile fresh PR, signal, per-PR state, and scheduler state into one bounded
   plan; lifecycle signals without an admin command may cancel stale work or
   repair projections but cannot start a model generation.
2. Parse the exact `@review-agent` commands `review`, `status`, `explain`,
   `reconsider`, `retry`, and `cancel`; only fresh admin authority may mutate work.
3. Plan status, formal review, verdict check, and exact-head auto-merge eligibility
   from validated durable decisions and fresh publication facts.

## Invariants and Failure Semantics

- Non-command comments are observed no-ops.
- Auto-merge requires a clean approved admin/member PR and no human
  `REQUEST_CHANGES` review.
- Each infrastructure attempt receives a signed 90-minute deadline. The single
  retry stays in the generation but starts a fresh deadline on lease reacquire.
- The signed generation start imposes a 180-minute cap; if it cannot fit a
  complete fresh attempt, reconciliation fails closed.

## Read First

- [Reconciliation](reconcile.go)
- [Scheduler](scheduler.go)
- [Commands](commands.go)
- [State](state.go)
- [Publication planning](publication.go)

## Update Triggers

Update this file when commands, permissions, scheduling, deadlines, signed
state, projection repair, publication, verdict, or merge policy changes.
