---
scope: package
summary: Owns node-local Operations MCP authentication, budgets, audits, metrics, and fenced profile capture.
---

# Operations MCP Runtime Flow

## Responsibility

This package owns high-frequency node-local MCP authentication, execution
budgets, audit summaries, metrics, and profile capture that must not enter
Controller Raft.
It does not own tool registration, remote ingress routing, or Controller state.

## Boundaries

- Desired credential state and MCP ownership are Controller-derived; raw
  bearer secrets, full arguments, results, and log keywords are never retained.
- Tool registration, request validation, and remote routing live in access and
  app layers.
- `Profiler` is the only active observation runtime.

## Main Flows

1. Parse credential ID and 256-bit secret, require the latest enabled desired
   state, and compare its SHA-256 digest in constant time.
2. Apply per-credential, per-node, ingress, authentication, and concurrency
   budgets before executing a stable tool name; record only bounded summaries.
3. For CPU, heap, or goroutine capture, verify current owner/revision, consume
   an exact one-time owner-held lease, capture under cluster and node fences,
   and return parsed top rows.

## Invariants and Failure Semantics

- Ordinary tools allow 60 calls per credential per minute, log tools 20;
  concurrency is four per credential and sixteen per node.
- Audits retain 200 summaries and rotate `mcp-audit.jsonl`; selectors and
  metrics remain bounded and low-cardinality.
- CPU duration is at most 30 seconds; heap and goroutine require zero duration.
- Only one profile runs cluster-wide, each node has a 60-second cooldown, and
  raw profiles remain size-capped in memory.
- A random one-time 35-second lease and Controller-derived stop fence prevent
  forged callers and overlapping owner generations.

## Read First

- [Credential verifier](verifier.go)
- [Call budgets and audits](calls.go)
- [Profile runtime](profile.go)
- [Profile analyzer](analyzer.go)

## Update Triggers

Update this file when token verification, call limits, audit fields, metrics,
profile kinds or limits, ownership leases, or stop fencing changes.
