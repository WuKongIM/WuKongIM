---
scope: package
summary: Implements bounded GitHub reads and fenced writes for the serverless Issue Agent.
---

# Issue Agent GitHub Flow

## Responsibility

This package implements the Issue Agent's narrow GitHub ports for fresh task
context, repository instructions, signed ancestry, candidate publication, and
bounded scheduled inventory.
It does not decide lifecycle policy, verify candidates, or invoke models.

## Boundaries

- It uses protected GitHub App credentials and exposes domain projections, not
  a generic REST, GraphQL, or Git transport.
- `AGENTS.md` from the exact source revision is mandatory context; `FLOW.md` is
  advisory context with frozen blob identity.
- Model execution and clean-checkout verification live outside this package.

## Main Flows

1. Read fresh Issue, comment, permission, review-thread, instruction, and exact
   source-revision facts into a bounded context projection.
2. Verify signed linear ancestry and all freshness fences, append the expected
   candidate head, create a draft PR, and publish the exact successor state.
3. For a bounded mechanical base sync, prove unchanged candidate paths and
   App-signed linear history before moving the staging ref.

## Invariants and Failure Semantics

- Default-branch drift, tags, protected paths, non-agent or unsigned commits,
  stale heads, external ancestry, truncation, and ambiguity fail closed.
- Every write is followed by a bounded fresh read and succeeds only when the
  exact expected successor is visible.
- Scheduled inventory is complete, sorted, and limited to 40 items; pagination
  beyond that bound is an error, not a partial result.
- Credentials and GitHub response details never enter candidate evidence.

## Read First

- [Context builder](context_builder.go)
- [Repository reader](reader.go)
- [Git database](git_database.go)
- [Domain projections](projections.go)

## Update Triggers

Update this file when GitHub permissions, context inputs, signing or ancestry
fences, publication transitions, mechanical sync, or inventory bounds change.
