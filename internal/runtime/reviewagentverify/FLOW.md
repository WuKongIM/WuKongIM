---
scope: package
summary: Freezes Review Agent context and validates trusted check evidence.
---

# internal/runtime/reviewagentverify Flow

## Responsibility

This package freezes complete Review Agent inputs, selects applicable trusted
context, executes protected named checks, and validates their evidence. It
does not call GitHub, publish a review, or grant model output authority.

## Boundaries

GitHub adapters supply exact base/control blobs and complete changed paths.
`pkg/flowdoc` parses FLOW scope metadata. Contracts under
`internal/contracts/reviewagent` carry the resulting bounded context and
evidence between jobs.

## Main Flows

```text
exact changed paths + frozen AGENTS.md / FLOW.md candidates
  -> preserve recursive AGENTS.md scope
  -> resolve package/subtree FLOW scope with legacy-subtree compatibility
  -> stable applicable context blobs

protected named check + disposable checkout
  -> credential-free bounded execution
  -> append-only external ledger
  -> sealed mandatory evidence
```

## Invariants and Failure Semantics

Explicit malformed FLOW metadata fails context construction. A FLOW without
front matter temporarily keeps the historical subtree scope. The first result
for every mandatory check remains the sealed baseline; a model-requested rerun
cannot replace it. Callers cannot provide commands, working directories,
environment overrides, URLs, refs, or test patterns.

## Read First

- [instructions.go](instructions.go)
- [context.go](context.go)
- [runner.go](runner.go)
- [validate.go](validate.go)

## Update Triggers

- Applicable AGENTS/FLOW selection or precedence changes.
- Review context budgets or frozen identity rules change.
- Named-check execution, sealing, or final evidence validation changes.
