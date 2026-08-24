---
scope: package
summary: Captures credential-free candidates and verifies them in a clean exact-base checkout using trusted tests.
---

# Issue Agent Verification Flow

## Responsibility

This package captures candidate filesystem changes and verifies the canonical
change set in a fresh exact-base checkout. It contains no GitHub API, model,
Issue lifecycle, or publisher logic.

## Boundaries

- Candidate capture compares an immutable baseline with a disposable Codex
  workspace while ignoring Codex Git metadata.
- Verification trusts only the recomputed complete diff, protected policy, and
  fixed or cataloged focused test plan.
- Docker-dependent scenarios run in a separate trusted job; the engineer never
  receives the host Docker socket.

## Main Flows

1. Capture regular-file upserts and deletions into a bounded canonical snapshot.
2. Apply the snapshot to a fresh exact-base checkout and recompute the entire
   diff rather than trusting candidate claims.
3. Enforce protected paths, file modes, dependency, size, symlink, and risk
   checks, then run trusted tests and emit candidate evidence.

## Invariants and Failure Semantics

- Git refs, index state, attributes, ignore rules, command claims, and claimed
  results are never evidence.
- Existing safe symlinks must remain byte-for-byte unchanged; new, removed,
  retargeted, escaping, chained, or type-changing symlinks fail closed.
- Every `AGENTS.md` and `FLOW.md` is protected by its exact source blob identity.
- Only regular-file upserts and deletions may enter a candidate.

## Read First

- [Candidate capture](capture.go)
- [Verification](verify.go)
- [Trusted runner](runner.go)

## Update Triggers

Update this file when candidate shape, filesystem comparison, protected policy,
symlink handling, test selection, or clean-checkout evidence changes.
