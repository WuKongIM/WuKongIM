---
scope: package
summary: Proxies one Simulation Run's public HTTP and WebSocket traffic while preserving health routing and benchmark-purity evidence.
---

# Cloud View Flow

## Responsibility

`internal/access/cloudview` is the HTTP/WebSocket entry adapter for one Cloud
Simulation Run. It rate-limits public traffic, selects healthy service nodes,
proxies Manager, Demo, product, gateway, and Prometheus paths, and records
conservative interactive/operator purity state.
It does not own Manager business rules, benchmark policy, or cloud resources.

## Boundaries

- Cloud View never joins the cluster, decodes WKProto, or holds cloud
  credentials.
- `internal/runtime/cloudviewstate` owns durable monotonic purity state; app
  composes the handler and `cmd/wkcloudview` owns process/listener lifecycle.
- Status observation is only a rebind hint; pinned-TLS MCP health remains the
  Analysis authority.

## Main Flows

1. Apply source limits, use cached readiness to select a healthy node, rewrite
   `/route`, and proxy the bounded public path to its correct upstream.
2. Persist the conservative interactive/operator marker before forwarding any
   qualifying request, then execute without replaying irreversible Manager
   writes.
3. Serve passive no-store peer status from the direct transport address without
   trusting forwarded headers or changing purity.

## Invariants and Failure Semantics

- Safe reads and handshakes may retry another healthy node after transport
  failure; Manager writes are never replayed.
- Purity persistence failure returns `503` before external effects. A later
  upstream failure may create a conservative false positive, never an unmarked
  successful modification.
- Missing or degraded final state is impure and fails closed.
- Status ignores forwarded-address headers and cannot replace the pinned HTTPS
  endpoint acceptance check.

## Read First

- [Proxy server](server.go)
- [Admission limits](limit.go)
- [Server tests](server_test.go)

## Update Triggers

Update this file when routing, retries, write replay, purity admission, status
observation, public upstreams, or process ownership changes.
