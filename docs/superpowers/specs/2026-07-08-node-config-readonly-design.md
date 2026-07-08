# Node Config Readonly Design

Date: 2026-07-08
Status: Approved for implementation planning
Scope: manager node operations, node-local effective config snapshots, web node detail panel

## Context

Cluster operations can list nodes, inspect node health, view Controller and Slot
logs, run node lifecycle actions, inspect DB state, and view node-local plugin
state. Operators still cannot inspect the effective startup configuration for
each node from the web console. That makes it hard to compare important runtime
settings such as hash-slot count, replica counts, gateway workers, delivery
limits, plugin runtime mode, webhook queues, logging, and observability flags.

This feature adds a read-only, per-node configuration view. It treats a
single-node deployment as a single-node cluster and uses the same manager
node-scoped routing model for local and remote nodes. It does not introduce a
local-only or cluster-bypass deployment branch.

## Goals

- Let an operator view one selected node's effective startup configuration from
  the cluster node operations surface.
- Keep the response safe for browser rendering by using an allowlist and
  redacting sensitive values.
- Avoid adding configuration reads to hot paths or node-list polling.
- Preserve current layering: manager HTTP owns routing and permissions,
  management usecase owns DTOs and orchestration, infra routes local or remote
  node reads, and app owns the current process config snapshot.
- Leave room for a later cross-node config comparison view without building it
  in this slice.

## Non-Goals

- No runtime config mutation, hot reload, or persistent config write API.
- No raw dump of `app.Config`, environment variables, or `wukongim.conf`.
- No exposure of manager passwords, JWT secrets, join tokens, user tokens, or
  other secret material.
- No automatic cluster-wide fan-out from the node list.
- No config diff page in this slice.
- No new deployment mode for non-cluster local behavior.

## Operator UX

Keep the feature anchored in `/cluster/nodes`.

The node list stays focused on inventory and actions. The existing Inspect
action opens the node detail sheet. The detail sheet adds a compact
configuration section below the existing node summary:

- Header: node ID, snapshot source, generated time, and restart requirement.
- Grouped table sections: Node, Cluster, Gateway, Message/Channel, Delivery,
  Webhook, Plugin, Log, and Observability.
- Each row shows the `WK_*` key, a short label, the effective value, and a
  redacted marker when applicable.
- Loading and error states are scoped to the config section, so basic node
  details still render when config loading fails.

The UI should use the existing dense manager style: plain tables, thin borders,
small status badges, and no explanatory body text. There should be no save
buttons, toggles, destructive controls, or "test config" actions.

## Manager API

Add a node-scoped read-only route:

```text
GET /manager/nodes/:node_id/config
```

Permission:

```text
cluster.node:r
```

Response shape:

```json
{
  "generated_at": "2026-07-08T10:00:00Z",
  "node_id": 1,
  "source": "effective_startup_config",
  "requires_restart": true,
  "groups": [
    {
      "id": "cluster",
      "title": "Cluster",
      "items": [
        {
          "key": "WK_CLUSTER_HASH_SLOT_COUNT",
          "label": "Hash slot count",
          "value": "256",
          "sensitive": false,
          "redacted": false
        },
        {
          "key": "WK_CLUSTER_JOIN_TOKEN",
          "label": "Join token",
          "value": "******",
          "sensitive": true,
          "redacted": true
        }
      ]
    }
  ]
}
```

HTTP behavior:

- `200 OK`: returns the selected node's config snapshot.
- `400 Bad Request`: node ID is not a positive integer.
- `404 Not Found`: selected node is not present in the control snapshot.
- `403 Forbidden`: authenticated user lacks `cluster.node:r`.
- `503 Service Unavailable`: management usecase, local config provider, node
  RPC, or remote node config provider is unavailable.

The route must not return the discovered `wukongim.conf` path, raw environment
source values, or unbounded maps. It may return configured operational path
values, such as data or log directories, only when the key is explicitly
allowlisted. Sensitive keys are either omitted or represented with a fixed
redacted value.

## Config Snapshot Policy

The initial allowlist should cover runtime-affecting `WK_*` keys that operators
commonly compare across nodes:

- Node and cluster identity/routing: node ID, cluster ID, listen/advertise
  addresses, static seed/node counts, hash slot count, initial slot count, Slot
  replica count, Channel replica count, Slot tick settings, health report
  interval, and health TTL.
- Gateway: external TCP/WS/WSS addresses, listener count, gnet settings,
  async worker and queue sizes, send timeout, session async-send batch limits.
- Message, channel, and conversation: large-group threshold, append sharding and
  pool sizes, permission cache TTL, conversation authority limits, flush
  intervals, handoff timeout, active cooldown, and admission concurrency.
- Delivery: enable flag, fanout page size, push batch size, pending ack TTL and
  caps, and delivery event queue size.
- Webhook: enable flag, endpoint presence, event filter summary, queue size,
  workers, batch sizes, request timeout, and retry attempts.
- Plugin: enable flag, hot reload, fail-open, timeout, queue and worker sizes,
  and configured directory/socket/state path fields only when explicitly
  allowed.
- API, manager, log, and observability: API/manager listen addresses, manager
  auth flag, JWT issuer and expiry, log level/rotation settings, metrics,
  Prometheus, top, debug, and diagnostics settings.

Sensitive keys are redacted by default when their names or DTO fields contain
`SECRET`, `TOKEN`, `PASSWORD`, `PASS`, `CREDENTIAL`, `PRIVATE`, or raw manager
user payloads such as `WK_MANAGER_USERS`. The implementation should prefer an
explicit denylist for known sensitive keys in addition to the name-based guard.
Examples:

- `WK_CLUSTER_JOIN_TOKEN`: redacted.
- `WK_MANAGER_JWT_SECRET`: redacted.
- `WK_MANAGER_USERS`: summarized or omitted; never return password hashes or
  plaintext password fields.
- User/device tokens from business data are out of scope and must never appear.

Values should be formatted as strings in the manager DTO. Lists can use compact
JSON-style strings or bounded summaries such as `3 configured listeners`.
Formatting in app/usecase keeps the browser from needing to understand every
Go config type.

## Backend Design

`internal/app`:

- Owns the current process's normalized startup config.
- Implements a narrow local provider that returns the allowlisted,
  already-redacted config snapshot for this node.
- Registers the node-config RPC handler when node RPC is available.

`internal/usecase/management`:

- Defines `NodeConfigSnapshot`, `NodeConfigGroup`, and `NodeConfigItem`.
- Adds a `NodeConfigReader` port and `NodeConfigSnapshot(ctx, nodeID)` usecase.
- Validates positive node IDs and verifies the selected node exists in the
  local control snapshot before delegating the read.
- Maps missing node and unavailable provider errors to stable usecase errors.

`internal/infra/cluster`:

- Implements the `NodeConfigReader` port.
- Routes local node reads to the app-owned local provider.
- Routes remote node reads through an `internal/access/node` manager RPC client.

`internal/access/node`:

- Adds a compact manager node-config RPC service and codec.
- The RPC receiver calls only the local provider. It must not recursively
  forward node-config reads.
- RPC status values should mirror existing manager RPC patterns:
  `ok`, `invalid_request`, `unavailable`, `context_canceled`,
  `context_deadline_exceeded`, and `rejected`.

`internal/access/manager`:

- Adds the HTTP route under the existing node read permission group.
- Parses `:node_id`, delegates to the management usecase, and serializes the
  DTO without reshaping secrets.
- Updates `FLOW.md` with the new read-only route.

`pkg/cluster/net`:

- Adds one RPC service ID for manager node-config reads if no suitable existing
  ID exists.

## Performance And Safety

The config snapshot is built only on explicit node detail inspection. It is not
added to `GET /manager/nodes` and is not polled by the cluster monitor.

The snapshot is derived from in-memory normalized config, so the read is cheap
and does not parse files, scan env vars, or hit storage. Remote reads are one
bounded node RPC request to the selected node. The response is allowlisted and
bounded by a fixed set of groups and keys.

The feature does not touch foreground SEND, append, route, delivery, presence,
or plugin hook hot paths.

## Frontend Design

Add types and client method:

```ts
getNodeConfig(nodeId: number): Promise<ManagerNodeConfigResponse>
```

The node detail sheet should:

- Load node config after a node is selected, independently from `getNode`.
- Render config groups in stable order.
- Show redacted values as `******` with a muted redacted badge.
- Show `forbidden`, `unavailable`, and generic errors through existing
  resource-state patterns.
- Preserve existing node detail tests and visual contract hooks.

The web implementation should use existing components such as `DetailSheet`,
`SectionCard`, `StatusBadge`, and compact table styling. It should not add
another sidebar item or standalone route for this slice.

## Testing

Backend unit tests:

- Manager HTTP route returns a config snapshot for `GET
  /manager/nodes/:node_id/config`.
- Invalid node ID returns `400`.
- Missing node maps to `404`.
- Missing provider maps to `503`.
- Auth requires `cluster.node:r`.
- Sensitive values are redacted and never appear in JSON.
- Usecase validates node existence from the control snapshot before reading.
- Infra routes local reads to the local provider and remote reads through node
  RPC.
- Node RPC encodes/decodes snapshots and maps unavailable status.
- App snapshot formats representative config groups and redacts known secrets.

Frontend tests:

- `manager-api` calls `/manager/nodes/:node_id/config`.
- Node detail sheet renders config groups after Inspect.
- Redacted values are shown as `******` and raw secret values are absent.
- Config-section errors do not hide basic node detail.

Focused verification:

```bash
GOWORK=off go test ./internal/app ./internal/access/manager ./internal/access/node ./internal/usecase/management ./internal/infra/cluster -run 'NodeConfig|ManagerNodeConfig|ConfigSnapshot' -count=1
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
git diff --check
```

## Open Decisions

None for this read-only slice. Cross-node config diff, config export, runtime
mutation, and per-key source tracking between file and environment should be
designed separately. The first implementation reports the normalized effective
startup config rather than exact per-key origin, because the current config
loader does not retain origin metadata after normalization.
