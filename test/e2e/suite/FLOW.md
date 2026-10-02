---
scope: package
summary: Provides reusable black-box process, workspace, configuration, protocol, HTTP, diagnostics, and convergence helpers for E2E tests.
---

# E2E Suite Flow

## Responsibility

This package is reusable harness code for real `cmd/wukongim` processes. It
contains no scenario-specific business assertions and follows `test/e2e/AGENTS.md`.

## Boundaries

- Helpers observe public HTTP, WKProto, MQTT 5, metrics, process state, and bounded
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
2. Migration helpers observe bounded CLI success/refusal and start caller-configured product
   nodes on already imported data, without opening storage inside the harness.
3. Wait for readiness, stable Slot authority, or active Channel runtime metadata
   through public evidence; restart or reconfigure only after previous
   process-group cleanup completes.
4. Cleanup stops static nodes concurrently, joins repeated stops, escalates
   TERM to KILL for remaining descendants, and waits for complete group cleanup.
5. Optional static-cluster TCP relays publish membership endpoints separately
   from product listeners. Bounded socket/PID evidence identifies exact owned
   senders; partition cuts both directed links, retains node-local/public TCP,
   refuses reconnects and joins relay workers after product cleanup.

## Invariants and Failure Semantics

- One `NodeProcess` owns the only leader `Wait`; readiness fails immediately on
  child exit. Restart never reuses ports/data before prior group cleanup.
- Binary publication is atomic. Plugin runtime is disabled by default and
  enabled only by plugin scenarios.
- Managed-process WKProto readiness registers a dedicated device token through
  Product HTTP, then proves a real authenticated handshake. Registration errors
  remain bounded and never echo credentials; readiness does not disable auth.
- MQTT fixtures use independent Eclipse Paho clients with bounded receive queues
  and deadlines. Queue overflow fails the observation instead of dropping a
  message silently. Explicit WK fixture credentials preserve token-auth behavior;
  no helper provisions credentials or makes application retry decisions. Optional
  Will fields, manual PUBACK, Receive Maximum and joined TCP abort exercise Paho behavior.
- BackupClient uses authenticated public Manager HTTP with bounded response reads;
  only explicit 401 refreshes login and definite plan-revision conflicts reread.
  Archive selection and convergence assertions belong to the scenario.
- WebSocket gateway opt-in publishes only the allocated loopback listener;
  TCP WKProto remains the readiness authority for the started node.
- Diagnostics expose bounded paths and tails. TOML is re-encoded only after
  schema validation; invalid structure is fully omitted, and sensitive leaves
  plus nested secret-like keys are redacted.
- Optional enabled debug API stack capture writes at most 1 MiB to a private file.
- Message-send recovery retries only exact public
  `503 {"error":"retry required"}` with one stable body and idempotency key.
- `WaitClusterReady` proves availability only. `WaitSlotLeadersStable` proves
  closed cross-node inventories, voters, quorum, actual Raft leader agreement,
  and a stable fingerprint; PreferredLeader is not authority.
- Channel runtime convergence uses the exact public Manager lookup and requires
  both active status and an observed Channel Leader within a bounded deadline.

## Read First

- [Suite runtime](runtime.go)
- [Node process](process.go)
- [Configuration rendering](config.go)
- [Port allocation](ports.go)
- [Slot convergence](slot_convergence.go)

## Update Triggers

Update this file when workspace isolation, binary caching, process ownership,
environment filtering, cleanup, diagnostics, HTTP retry, convergence or TCP partition changes.
