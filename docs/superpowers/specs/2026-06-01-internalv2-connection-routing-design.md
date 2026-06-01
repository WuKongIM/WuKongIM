# internalv2 Connection Routing Design

## Goal

Add connection addressing to `internalv2` so clients can connect to any node
while each UID is addressed through its authoritative Slot leader.

The design must support many concurrent connections without periodic full
connection scans. It must preserve the project rule that a single-node
deployment is a single-node cluster and must not add a business branch that
bypasses cluster semantics.

## Non-Goals

- Implement realtime message delivery or node-to-node push in this phase.
- Add durable or replicated presence storage.
- Add owner lease heartbeats, route digests, or periodic mismatch replay.
- Move business rules into `internalv2/access/gateway`.
- Wire `internalv2` into the production `cmd/wukongim` entry.

## Key Terms

**Owner node**: the node that owns the real client TCP/WebSocket session.

**Authority node**: the current Slot leader for `uid`. It owns the authoritative
in-memory route directory for that UID.

**Route**: a virtual connection address. It points to an owner node and a local
session on that owner, but it is not the real TCP connection.

**Owner boot ID**: a process-incarnation ID generated when the owner runtime
starts. It fences stale routes from previous owner process incarnations.

## Decision

Use event-driven authoritative connection routing:

```text
client connects to any owner node
  -> owner registers local real session
  -> owner synchronously registers a Route with uid Slot leader
  -> authority stores Route in memory
  -> successful CONNACK means the route is authoritative
```

Steady state has no owner lease heartbeat and no route digest replay.
Correctness is maintained through:

- exact connect-time registration
- best-effort disconnect-time unregistration
- owner boot ID fencing
- lazy stale-route pruning when a future write finds a missing session
- bounded rehydrate only when a Slot leader changes

This keeps normal cost proportional to route churn, not total online
connection count.

## Package Layout

```text
internalv2/usecase/presence
  Entry-agnostic connection routing usecase.
  Owns activate/deactivate orchestration, authoritative directory,
  route lookup, device conflict policy, and leader-change rehydrate input.

internalv2/runtime/online
  Node-local real session registry.
  Owns session lookup by session ID, UID, and UID hash slot.
  Stores only sessions physically connected to this node.

internalv2/access/gateway
  Gateway adapter.
  Converts authenticated gateway sessions into presence Activate/Deactivate
  commands using the existing pre-CONNACK activation hook.

internalv2/access/node
  Node RPC adapter.
  Handles authority RPCs and future owner-write RPCs.
  It owns codecs and wire error mapping, not routing policy.

internalv2/infra/cluster
  clusterv2 adapter.
  Implements presence ports with RouteKey, current Slot leader lookup,
  node RPC calls, and local-vs-remote dispatch.

internalv2/app
  Single composition root.
  Wires online registry, presence app, cluster adapter, node RPC handlers,
  gateway handler, and leader-change rehydrate worker.
```

`internalv2/usecase/presence` must not import `pkg/gateway`,
`pkg/protocol/frame`, `pkg/clusterv2`, `internalv2/access`, or
`internalv2/app`.

## Data Model

```go
// Route identifies a virtual client connection known by the UID authority.
type Route struct {
    UID          string
    OwnerNodeID  uint64
    OwnerBootID  uint64
    SessionID    uint64
    DeviceID     string
    DeviceFlag   uint8
    DeviceLevel  uint8
    Listener     string
    ConnectedUnix int64
}
```

The route identity is:

```text
(OwnerNodeID, OwnerBootID, SessionID)
```

The authority indexes routes by:

- `uid -> routeKey -> Route`
- `hashSlot -> ownerNodeID -> ownerBootID -> routeKey -> Route`

The owner-local registry indexes real sessions by:

- `sessionID -> OnlineConn`
- `uid -> sessionID -> OnlineConn`
- `hashSlot -> sessionID -> OnlineConn`

The hash-slot index is required for bounded leader-change rehydrate without
scanning every local connection.

## Activation Flow

Gateway authentication already writes UID and device metadata into session
values before the activation hook runs. `internalv2/access/gateway` should use
that hook to call presence before CONNACK is written.

```text
pkg/gateway CONNECT
  -> authentication succeeds
  -> access/gateway.OnSessionActivate
  -> presence.Activate(ctx, ActivateCommand)
  -> runtime/online.Register(real session)
  -> infra/cluster.RegisterAuthoritative(route)
       -> RouteKey(uid)
       -> current Slot leader
       -> local directory or node RPC
  -> authority directory upserts route and returns RouteAction values
  -> route actions are applied synchronously
  -> gateway writes success CONNACK
```

Invariant:

```text
successful CONNACK means the current UID Slot leader has an active Route
```

If authority registration fails, the owner removes the local route and rejects
the connect. This is fail-closed because a client must not become connected
without an authoritative address.

## Deactivation Flow

```text
gateway session close
  -> access/gateway.OnSessionClose
  -> presence.Deactivate(ctx, DeactivateCommand)
  -> runtime/online.Unregister(sessionID)
  -> best-effort authority UnregisterRoute
```

Unregister deletes only the exact route identity. A stale unregister must not
remove a newer route for the same UID.

Unregister failure is logged and ignored because the real connection is already
closed. Stale authority routes are cleaned later by lazy prune or owner down
handling.

## Authority Lookup Flow

This phase exposes lookup APIs but does not implement delivery.

```text
presence.EndpointsByUID(ctx, uid)
  -> RouteKey(uid)
  -> current Slot leader
  -> local directory lookup or remote node RPC
  -> []Route
```

`EndpointsByUIDs` groups UIDs by hash slot and calls each authority once.
This keeps future delivery expansion from issuing one RPC per UID when many
recipients belong to the same authority.

## Stale Route Handling

Because there is no owner lease, stale routes are removed when they are proven
stale.

Future owner-write RPCs must include:

```text
UID, OwnerNodeID, OwnerBootID, SessionID
```

The owner accepts the write only when:

- `OwnerNodeID` is this node
- `OwnerBootID` matches the current process boot ID
- `SessionID` exists in the local registry
- the local connection UID matches the route UID
- the local connection is active, not closing

If the owner returns `ErrSessionNotFound`, `ErrOwnerBootMismatch`, or
`ErrRouteFenced`, the authority removes that exact route identity. This is
lazy prune.

Lazy prune is preferred over digest replay because the owner gives a precise
answer about one route. It does not require scanning or replaying unrelated
connections.

## Owner Restart Handling

Each owner process generates a new `OwnerBootID` at startup.

All routes from an old owner boot become fenced automatically because future
owner-write RPCs carrying the old boot ID are rejected by the new process.

When the cluster runtime exposes node-down or node-restart information,
`internalv2/app` can ask the relevant authorities to drop routes for:

```text
(ownerNodeID, oldOwnerBootID)
```

This is an optimization, not the primary correctness mechanism.

## Slot Leader Change Handling

The authority directory is in memory, so a new Slot leader may start without
the previous leader's routes. The repair mechanism is bounded rehydrate,
triggered only by leader change.

```text
clusterv2 route snapshot changes
  -> app detects hash slots whose leader changed
  -> owner registry selects active local routes for those hash slots
  -> rehydrate worker sends routes to the new authority in bounded batches
  -> authority idempotently upserts exact route identities
```

Properties:

- no periodic full scan
- no digest mismatch protocol
- work is proportional to active local routes in affected hash slots
- batches are rate-limited and retryable
- routes in closing state are excluded

Suggested defaults:

- batch size: 512 routes
- max in-flight batches per target authority: 1
- retry backoff: bounded exponential backoff with jitter
- coalescing: merge repeated leader-change notifications for the same hash slot

If rehydrate is still in progress, queries for that UID may temporarily return
offline. This is acceptable for this phase because presence is intentionally
memory-only.

## Device Conflict Policy

The authority node remains the single conflict arbiter for a UID.

Conflict scope:

```text
same UID and same DeviceFlag
```

Rules:

- new master connection replaces all existing routes in the same scope
- new slave connection replaces only existing routes with the same DeviceID
- replaced routes are removed from authority before the new connect succeeds
- replacement actions are applied on owner nodes before CONNACK succeeds

`RouteAction` targets the exact route identity:

```go
type RouteAction struct {
    UID         string
    OwnerNodeID uint64
    OwnerBootID uint64
    SessionID   uint64
    Kind        string
    Reason      string
    DelayMS     int64
}
```

If an action cannot be acknowledged, activation rolls back the new local route
and unregisters the new authoritative route.

## RPC Surface

Presence authority RPCs:

- `RegisterRoute(slot, route) -> RegisterResult`
- `UnregisterRoute(slot, routeIdentity) -> ok`
- `EndpointsByUID(uid) -> []Route`
- `EndpointsByUIDs(uids) -> map[string][]Route`
- `RehydrateRoutes(slot, ownerNodeID, ownerBootID, []Route) -> ok`

Owner action RPCs:

- `ApplyRouteAction(action) -> ok`

Future owner-write RPC:

- `WriteRoute(routeIdentity, frameBytes) -> route write result`

The first implementation can use a compact binary codec similar to existing
node RPC codecs, but the usecase must only see typed ports and DTOs.

## High-Connection-Count Behavior

Normal steady-state costs:

- connect: one local insert and one authority register
- disconnect: one local delete and one best-effort unregister
- lookup: one authority lookup per UID group
- no per-connection timer
- no periodic full registry scan
- no digest computation

Large repair costs:

- owner restart: fenced lazily by `OwnerBootID`
- stale route: deleted only after an exact failed write
- Slot leader change: bounded rehydrate only for affected hash slots

This keeps the hot path predictable when a node holds many connections.

## Error Handling

Activation errors:

- route not ready or no Slot leader: reject connect with retryable reason
- authority register failure: rollback local route and reject connect
- route action dispatch failure: rollback new route and reject connect

Deactivation errors:

- local unregister always wins
- authority unregister is best effort and logged on failure

Lookup errors:

- route not ready or no Slot leader returns a typed retryable error
- missing UID returns an empty route list, not an error

Rehydrate errors:

- failed batches are retried with bounded backoff
- repeated leader changes cancel obsolete batches for old leaders
- oversized batches are split before RPC

## Testing Strategy

Focused unit tests:

- presence directory upsert, unregister, lookup, and exact route identity
- stale unregister does not remove a newer route
- device conflict rules produce correct `RouteAction` values
- owner registry indexes routes by hash slot without full scans
- lazy prune removes only the failed route identity

Access adapter tests:

- gateway activation calls presence before successful CONNACK
- gateway close calls presence deactivation
- node RPC codecs round-trip routes, route actions, and rehydrate batches

App wiring tests:

- `internalv2/app` wires presence, gateway activation, and node RPC handlers
- single-node cluster still routes through authority flow
- leader-change rehydrate selects only affected hash slots

The first implementation should run focused tests for changed packages, then
`go test ./internalv2/...`.

## Documentation Updates

When implemented, update:

- `internalv2/FLOW.md`
- `internalv2/app/FLOW.md`
- `internalv2/access/gateway/FLOW.md`
- new `internalv2/usecase/presence/FLOW.md`
- new `internalv2/runtime/online/FLOW.md`
- `AGENTS.md` directory structure if new directories are added
