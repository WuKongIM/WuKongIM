---
scope: package
summary: Owns deterministic Issue Agent admission, authorization, lifecycle reconciliation, commands, status, and publication planning.
---

# Issue Agent Use Case Flow

## Responsibility

This package owns deterministic Bug-form admission, permission and risk
classification, task identity, lifecycle state, commands, status rendering,
and candidate publication plans.
It does not perform HTTP, Git, filesystem, verifier, or model operations.

## Boundaries

- It consumes fresh facts, signed state, protected policy, candidate snapshots,
  and clean verifier evidence.
- It performs no HTTP, Git, shell, filesystem, environment, or model calls.
- A human is the only merge authority; adapters alone execute planned effects.

## Main Flows

1. Reconcile fresh Issue, PR, permission, verifier, and signed-state facts into
   one decision and successor state, including bounded stale-base sync policy.
2. Parse only exact first-line `/agent fix`, `retry`, `cancel`, and `take-over`
   commands from a freshly authorized actor.
3. Plan exact branch, commit, draft PR, and state effects only from the signed
   task, exact context, candidate snapshot, and clean evidence.

## Invariants and Failure Semantics

- Events are wake-up hints; duplicate, reordered, or missing events converge
  from fresh facts and the signed state ref.
- Advisory model output cannot replace clean verifier evidence.
- Unauthorized or non-exact commands have no lifecycle authority.
- Publication plans bind exact identities and do not grant merge authority.

## Read First

- [Reconciliation](reconcile.go)
- [Commands](commands.go)
- [State](state.go)
- [Publication planning](publication.go)
- [Intake](intake.go)

## Update Triggers

Update this file when admission, authorization, commands, lifecycle state,
stale-base handling, evidence requirements, status, or publication plans change.
