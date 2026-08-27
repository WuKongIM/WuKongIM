---
scope: package
summary: Provides reusable black-box process, workspace, configuration, protocol, HTTP, diagnostics, and convergence helpers for E2E tests.
---

# E2E Suite Flow

## Responsibility

This package is reusable harness code for real `cmd/wukongim` processes. It
contains no scenario-specific business assertions and follows `test/e2e/AGENTS.md`.

## Boundaries

- Helpers observe public HTTP, WKProto, metrics, process state, and bounded
  artifacts; they do not import app, use cases, or storage internals.
- `WK_E2E_*` is harness-only and is removed from spawned nodes. Real product
  variables must be passed explicitly through `NodeSpec.Env`.
- Unix socket placement uses a short independent workspace path.

## Main Flows

1. Allocate isolated workspace and non-overlapping loopback port block, render
   node TOML, obtain the repository/OS/architecture-scoped cached E2E binary,
   and start each product as an independently owned process group.
   `WithWebSocketGateway` adds a browser-addressable `/ws` wsmux listener and
   published route while retaining the default TCP WKProto listener.
2. Wait for readiness or stable Slot authority through public evidence; restart
   or reconfigure only after previous process-group cleanup completes.
3. Cleanup stops static nodes concurrently, joins repeated stops, escalates
   TERM to KILL for remaining descendants, and waits for complete group cleanup.

## Invariants and Failure Semantics

- One `NodeProcess` owns the only leader `Wait`; readiness fails immediately on
  child exit. Restart never reuses ports/data before prior group cleanup.
- Binary publication is atomic. Plugin runtime is disabled by default and
  enabled only by plugin scenarios.
- WebSocket gateway opt-in publishes only the allocated loopback listener;
  TCP WKProto remains the readiness authority for the started node.
- Diagnostics expose bounded paths and tails. TOML is re-encoded only after
  schema validation; invalid structure is fully omitted, and sensitive leaves
  plus nested secret-like keys are redacted.
- Message-send recovery retries only exact public
  `503 {"error":"retry required"}` with one stable body and idempotency key.
- `WaitClusterReady` proves availability only. `WaitSlotLeadersStable` proves
  closed cross-node inventories, voters, quorum, actual Raft leader agreement,
  and a stable fingerprint; PreferredLeader is not authority.

## Read First

- [Suite runtime](runtime.go)
- [Node process](process.go)
- [Configuration rendering](config.go)
- [Port allocation](ports.go)
- [Slot convergence](slot_convergence.go)

## Update Triggers

Update this file when workspace isolation, binary caching, process ownership,
environment filtering, cleanup, diagnostics, HTTP retry, or convergence changes.
