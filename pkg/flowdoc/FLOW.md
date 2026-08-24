---
scope: package
summary: Parses the closed metadata and structure of Agent FLOW navigation files.
---

# pkg/flowdoc Flow

## Responsibility

`pkg/flowdoc` owns the reusable parser for FLOW front matter, navigation-card
structure, and local Markdown references. It does not discover repository
files, decide policy enforcement, or load Agent context.

## Boundaries

Review Agent context discovery consumes scope metadata. The repository
`flowcheck` command consumes the complete structural parser and resolves local
references against a concrete repository root.

## Main Flows

```text
frozen FLOW bytes
  -> strict scope/summary metadata
  -> package or subtree context selection

repository FLOW bytes
  -> required navigation headings
  -> bounded Read First links
  -> flowcheck diagnostics and generated index
```

## Invariants and Failure Semantics

Explicit front matter is a closed two-field format. Unknown, duplicate, or
malformed fields fail, and missing front matter fails. The parser never reads
files or follows links itself.

## Read First

- [metadata.go](metadata.go)
- [body.go](body.go)
- [references.go](references.go)

## Update Triggers

- FLOW metadata, scope, heading, or Read First contracts change.
- Parser failure or validation semantics change.
- Markdown reference extraction changes.
