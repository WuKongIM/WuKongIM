---
scope: package
summary: Implements exact-head GitHub reads, state refs, review publication, checks, and merge for the Review Agent.
---

# Review Agent GitHub Flow

## Responsibility

This package implements the Review Agent's narrow GitHub boundary: fresh PR
facts, exact content, signed state refs, review publication, check conclusions,
and exact-head merge.
It does not adjudicate findings, schedule reviews, verify code, or invoke models.

## Boundaries

- Protected App credentials are scoped to named domain operations; callers do
  not receive a generic GitHub writer.
- State paths and refs are fixed by the adapter rather than caller-selected.
- Finding adjudication and terminal-state policy belong to the review use case.

## Main Flows

1. Read fresh PR, repository, content, permission, review, and exact-head facts
   into bounded projections.
2. Read or append signed rolling checkpoints under the fixed state ref using
   an expected-parent fence.
3. After fresh authorization and head checks, publish the status comment or
   formal review, conclude the named check, and merge only the exact head.

## Invariants and Failure Semantics

- Read-your-write polling is bounded and remains valid only while the expected
  parent is unchanged.
- Stale head, unexpected state parent, incomplete facts, failed authorization,
  or ambiguous GitHub results fail closed.
- Terminal records contain no locally adjudicated findings, and retained
  findings are not republished as new ones.
- Token permissions do not expand the adapter beyond its exact merge and state
  operations.

## Read First

- [GitHub client](client.go)
- [Repository reader](reader.go)
- [Projection client](projection_client.go)
- [Scheduler state store](scheduler_store.go)

## Update Triggers

Update this file when GitHub facts, App permissions, state refs, publication,
checks, merge fencing, or visibility polling changes.
