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
- Rendered TOML is also mirrored into product environment variables by default.
  `WithConfigFileOnly` omits that generated mirror across startup, seed join and
  stopped-node reconfiguration; explicit `WithNodeEnv` controls remain caller-owned.
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

## Invariants and Failure Semantics

- One `NodeProcess` owns the only leader `Wait`; readiness fails immediately on
  child exit. Restart never reuses ports/data before prior group cleanup.
- Binary publication is atomic. Plugin runtime is disabled by default and
  enabled only by plugin scenarios.
- Managed-process WKProto readiness registers a dedicated device token through
  Product HTTP, then proves a real authenticated handshake. Registration errors
  remain bounded and never echo credentials; readiness does not disable auth.
- WKProto clients accept explicit registered device Tokens without changing
  server authentication; tokenless fixtures remain explicit.
- WKProto clients may inject a Dialer to observe public socket bytes. Their
  synthetic future ACK bridge is not wire-order evidence; ordering probes must
  capture decoded ACKs directly from the TCP read stream.
- WebSocket gateway opt-in publishes only the allocated loopback listener;
  TCP WKProto remains the readiness authority for the started node.
- Diagnostics expose bounded paths and tails. TOML is re-encoded only after
  schema validation; invalid structure is fully omitted, and sensitive leaves
  plus nested secret-like keys are redacted.
- Linux recovery sampling separates process RSS/I/O/CPU from enclosing cgroup
  limits/OOM counters and joins its sampler; public profiles are size/time bounded.
- Full public metrics observations explicitly request identity encoding, reuse
  the existing HTTP transport, and reject redirects, non-200 responses, empty
  snapshots, and invalid sample lines. They do not cache, filter, or retry.
- Optional metrics receipts bind status, encoding, logical-body byte count and
  SHA-256, UTC bounds, and monotonic duration to that same request. Receipts keep
  no body or metric labels. Failed reads retain safe partial metadata and return
  no samples; a partial-body hash does not prove a complete snapshot.
- Message-send recovery retries only exact public
  `503 {"error":"retry required"}` with one stable body and idempotency key.
- A bounded HTTP fault proxy may forward one real POST and withhold its response
  until the caller deadline. It records only the upstream status, joins its
  canceled handler, and leaves commit verification to subsequent public reads.
- `WaitClusterReady` proves availability only. `WaitSlotLeadersStable` proves
  closed cross-node inventories, voters, quorum, actual Raft leader agreement,
  and a stable fingerprint; PreferredLeader is not authority.
- Channel runtime convergence uses the exact public Manager lookup and requires
  both active status and an observed Channel Leader within a bounded deadline.

## Read First

- [Suite runtime](runtime.go)
- [Node process](process.go)
- [Configuration rendering](config.go)
- [Slot convergence](slot_convergence.go)
- [Public metrics observation](metrics.go)

## Update Triggers

Update this file when workspace isolation, binary caching, process ownership,
environment filtering, cleanup, diagnostics, HTTP metrics observation, HTTP
retry, or convergence changes.
