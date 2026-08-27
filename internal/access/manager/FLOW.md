---
scope: package
summary: Adapts authenticated Manager HTTP and UI operations to bounded management use cases.
---

# internal/access/manager Flow

## Responsibility

This package owns the dedicated Manager HTTP listener, embedded SPA, login and
JWT handling, permission middleware, request validation, cursor and response
mapping, and Manager-specific error envelopes. It presents cluster operations
but does not own cluster state, safety policy, durable tasks, storage, or
node-local runtime behavior.

## Boundaries

Handlers call `internal/usecase/management` and narrow backup, channel,
conversation, message, user, diagnostics, plugin, log, DB-inspect, runtime, and
Operations MCP ports supplied by `internal/app`. Local-versus-remote selection
and node RPC live below this package. The web UI consumes the same bounded HTTP
contracts; it does not grant additional authority.

## Main Flows

```text
Manager request
  -> authentication and resource permission
  -> bounded path/query/body validation and opaque cursor decoding
  -> entry-independent use case or read-model port
  -> stable status, pagination metadata, and redacted response

irreversible operator request
  -> exact permission plus operation-specific confirmation/fences
  -> use-case safety plan and fresh state checks
  -> Controller-backed intent or explicit conflict/unavailable result

remote node read or action
  -> management orchestration selects target
  -> infra/cluster node RPC adapter
  -> partial/unknown evidence remains explicit
```

## Invariants and Failure Semantics

- When Manager authentication is enabled, every route uses its declared
  resource permission. Backup writes, restore, and MCP administration fail
  closed when authentication is disabled.
- Restore requires exact `cluster.restore:w`; wildcard permission is
  insufficient. It also requires reauthentication and exact archive
  confirmation, and a successful restore invalidates prior Manager sessions.
- HTTP handlers never create Raft tasks, decide lifecycle safety, infer actual
  leaders from desired placement, or mutate node runtimes directly.
- Planning endpoints are read-only. Fenced writes return `202` only for newly
  accepted work and preserve idempotent/no-op results as `200`.
- Missing, stale, partial, or unavailable distributed evidence remains unknown
  or unsafe; it is never projected as zero, healthy, or safe to remove.
- Latest-message scan saturation returns the stable
  `latest_messages_backpressured` error with retry guidance; general
  unavailability remains `service_unavailable`.
- Backup, config, diagnostics, log, plugin, and DB-inspect responses remain
  bounded and redacted. Credentials, filesystem paths, raw provider errors,
  complete UID sets, and secret material are never returned.
- Unsigned 64-bit message IDs cross JSON as decimal strings.
- `/mcp` uses its opaque MCP bearer token rather than a Manager JWT, rejects
  browser origins, and forwards only to the Controller-selected execution
  owner. `/manager/mcp*` remains the separately authenticated admin surface.

## Read First

- [server.go](server.go)
- [auth.go](auth.go)
- [backups.go](backups.go)
- [scale_in.go](scale_in.go)
- [opsmcp.go](opsmcp.go)

## Update Triggers

- Authentication, permissions, redaction, or stable error mapping changes.
- A handler begins planning, fencing, or executing distributed mutations.
- Restore, node lifecycle, scale-in, migration, or MCP authority changes.
- Remote-node failure, unknown-state, pagination, or response bounds change.
