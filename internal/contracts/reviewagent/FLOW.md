---
scope: package
summary: Defines strict generation-bound Review Agent context, advisory result, trusted evidence, explanation, and canonical state JSON.
---

# Review Agent Contracts Flow

## Responsibility

`internal/contracts/reviewagent` owns the bounded canonical JSON and immutable
generation identity exchanged between Review Agent roles.
It does not reconcile PRs, publish reviews, verify code, or invoke a model.

## Boundaries

- GitHub, lifecycle, filesystem, process, environment, network, model execution,
  named-check execution, and publication stay outside.
- Model results are advisory; named-check evidence is trusted only through its
  sealed runner and exact generation.
- Context documents preserve source meaning and scope rather than becoming one
  undifferentiated instruction blob.

## Main Flows

1. Fresh PR facts and protected policy form generation-bound Review context for
   one advisory model result.
2. The trusted named-check runner produces evidence for that exact generation.
3. Fresh authority, validated result, trusted evidence, and bounded explanation
   form canonical Review state.

## Invariants and Failure Semantics

- `GenerationIdentity` binds repository, PR, head/base, test merge, intent,
  generation, and parent across every document.
- Strict decoders reject unknown fields, trailing data, oversized input,
  malformed identity, and unbounded collections.
- Model prose may wrap exactly one unambiguous strict JSON object; competing
  objects/containers fail closed and never grant publication authority.

## Read First

- [Generation identity](identity.go)
- [Review context](context.go)
- [Advisory result](result.go)
- [Trusted evidence](evidence.go)
- [Canonical state](state.go)

## Update Triggers

Update this file when document inventory, generation identity, JSON bounds,
model extraction, evidence trust, explanation, or state authority changes.
