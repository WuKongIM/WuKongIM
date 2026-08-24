---
scope: package
summary: Defines bounded canonical Issue Agent context, candidate, evidence, result, and state JSON without lifecycle or publication behavior.
---

# Issue Agent Contracts Flow

## Responsibility

`internal/contracts/issueagent` owns the strict canonical JSON exchanged among
Issue Agent roles and the identities/digests that bind those documents.
It does not reconcile lifecycle, call GitHub, verify candidates, or run models.

## Boundaries

- GitHub, lifecycle, filesystem, process, model execution, verification, and
  publication behavior stay outside.
- Engineer model output is advisory; only trusted evidence and fresh Publisher
  fences can authorize state/publication planning.
- Exact-source context documents remain distinct from untrusted conversation.

## Main Flows

1. Fresh GitHub facts and protected policy form a bounded context bundle for an
   advisory Engineer result.
2. An immutable baseline plus captured workspace forms a candidate, which clean
   verification binds to trusted evidence.
3. Fresh authority and exact digests form canonical state; base synchronization
   advances identity/budget and clears stale Review authority.

## Invariants and Failure Semantics

- Decoders reject unknown fields, trailing data, oversized input, malformed
  identity, and unbounded collections.
- Model prose may contain exactly one unambiguous JSON object or one JSON fence;
  competing containers and brace-bearing prose fail closed.
- Only low-risk passing evidence bound to the exact task/candidate may enter a
  Publisher plan.
- Base synchronization increments its bounded budget and invalidates prior
  review authority.

## Read First

- [Context bundle](context.go)
- [Candidate snapshot](candidate.go)
- [Verification evidence](evidence.go)
- [Canonical state](state.go)

## Update Triggers

Update this file when document inventory, identity/digest binding, JSON bounds,
model extraction, evidence authority, or base synchronization changes.
