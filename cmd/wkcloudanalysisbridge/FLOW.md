---
scope: package
summary: Runs a local-only HTTP bridge to one authenticated Analysis MCP endpoint with exact certificate and destination pinning.
---

# Cloud Analysis Bridge Flow

## Responsibility

`cmd/wkcloudanalysisbridge` exposes one ephemeral IPv4-loopback HTTP endpoint
for a bounded local Analysis Session and forwards MCP requests to its validated
HTTPS Analysis endpoint.
It does not own Run inspection, Analysis policy, or cloud resource lifecycle.

## Boundaries

- The command accepts no remote bind address, arbitrary hostname, redirect, or
  credential argument.
- Session authentication remains in the forwarded authorization header; the
  bridge owns transport validation, not Analysis policy or token storage.
- The operator script owns startup, termination, and session-directory cleanup.

## Main Flows

1. Bind an ephemeral `127.0.0.1` listener and publish its local address.
2. Forward each request only to the fixed Analysis port and public IP while
   verifying the exact pinned certificate fingerprint and IP SAN.
3. Stop forwarding before the owning session removes its local handoff state.

## Invariants and Failure Semantics

- Listening and client access remain loopback-only.
- TLS identity and destination are exact; system trust, redirects, DNS, and
  hostname substitution cannot widen the endpoint.
- Validation or forwarding failure is terminal for that request and never
  falls back to an unpinned connection.

## Read First

- [Bridge entrypoint](main.go)
- [Bridge tests](main_test.go)

## Update Triggers

Update this file when listener exposure, destination allowlisting, TLS pinning,
header forwarding, or session lifecycle ownership changes.
