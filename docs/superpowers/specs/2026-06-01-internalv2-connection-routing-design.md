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
- queued exact disconnect-time unregistration
- owner boot ID fencing
- lazy stale-route pruning when a future write finds a missing session
- bounded rehydrate only when a route authority changes

This keeps normal cost proportional to route churn, not total online
connection count.

## Package Layout

```text
internalv2/usecase/presence
  Entry-agnostic connection routing usecase.
  Owns activate/deactivate orchestration, route lookup orchestration,
  device conflict policy, and usecase-owned ports. It does not own the
  mutexed in-memory directory.

internalv2/runtime/online
  Node-local real session registry.
  Owns session lookup by session ID, UID, and UID hash slot.
  Stores only sessions physically connected to this node.

internalv2/runtime/presence
  Node-local authoritative route directory for the hash slots currently led
  by this node.
  Owns sharded in-memory indexes, authority epochs, pending route state,
  and exact-route lazy prune state.

internalv2/access/gateway
  Gateway adapter.
  Converts authenticated gateway sessions into presence Activate/Deactivate
  commands using the existing pre-CONNACK activation hook and the new
  activation rollback hook described below.

internalv2/access/node
  Node RPC adapter.
  Handles authority RPCs and future owner-write RPCs.
  It owns codecs and wire error mapping, not routing policy.

internalv2/infra/cluster
  clusterv2 adapter.
  Implements presence ports with RouteKey, current Slot leader lookup,
  authority target fencing, node RPC calls, and local-vs-remote dispatch.

internalv2/app
  Single composition root.
  Wires online registry, presence app, cluster adapter, node RPC handlers,
  gateway handler, and authority-change rehydrate worker.
```

`internalv2/usecase/presence` must not import `pkg/gateway`,
`pkg/protocol/frame`, `pkg/clusterv2`, `internalv2/access`, or
`internalv2/app`. Add an import-boundary test for this package.

## Required Platform Extensions

This design requires two narrow platform surfaces before the usecase can be
implemented cleanly.

`pkg/clusterv2` must expose a node-RPC surface without leaking private
transport fields:

```go
type NodeRPCRegistrar interface {
    RegisterRPC(serviceID uint8, handler NodeRPCHandler)
}

type NodeRPCCaller interface {
    CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error)
}
```

It must also expose route-authority changes for active hash slots:

```go
type RouteAuthority struct {
    HashSlot       uint16
    SlotID         uint32
    LeaderNodeID   uint64
    RouteRevision  uint64
    AuthorityEpoch uint64
}

type RouteAuthorityWatcher interface {
    WatchRouteAuthorities() <-chan RouteAuthorityEvent
}
```

`AuthorityEpoch` changes whenever a node becomes authority for a hash slot,
including same-node process restart or leadership loss followed by regain.
The app must trigger rehydrate on `(HashSlot, LeaderNodeID, AuthorityEpoch)`
changes, not only on leader node ID changes.

`pkg/gateway` must expose an activation rollback contract. If
`OnSessionActivate` succeeds but CONNACK write fails, gateway must call a
rollback/deactivation hook or dispatch close for activated-but-not-opened
sessions. The owner registry must also hold a physical close capability for
forced route actions; a metadata-only `session.Close()` is not enough if it
does not close the underlying transport.

## Data Model

```go
// Route identifies a virtual client connection known by the UID authority.
type Route struct {
    UID           string
    OwnerNodeID   uint64
    OwnerBootID   uint64
    OwnerSeq      uint64
    SessionID     uint64
    DeviceID      string
    DeviceFlag    uint8
    DeviceLevel   uint8
    Listener      string
    ConnectedUnix int64
}

// RouteTarget fences an authority operation to one observed Slot route.
type RouteTarget struct {
    HashSlot       uint16
    SlotID         uint32
    LeaderNodeID   uint64
    RouteRevision  uint64
    AuthorityEpoch uint64
}
```

The route identity is:

```text
(OwnerNodeID, OwnerBootID, SessionID)
```

`SessionID` must not be reused within one `OwnerBootID`. `OwnerSeq` is a
monotonic sequence scoped to `(OwnerNodeID, OwnerBootID, HashSlot)`. Every
register, unregister tombstone, and rehydrate route carries an owner sequence
so the authority can reject stale upserts that arrive after a newer unregister.

The authority indexes routes by:

- `uid -> routeKey -> Route`
- `hashSlot -> ownerNodeID -> ownerBootID -> routeKey -> Route`
- `hashSlot -> ownerNodeID -> ownerBootID -> routeKey -> lastOwnerSeq`

The owner-local registry indexes real sessions by:

- `sessionID -> OnlineConn`
- `uid -> sessionID -> OnlineConn`
- `hashSlot -> sessionID -> OnlineConn`

The hash-slot index is required for bounded authority-change rehydrate without
scanning every local connection.

Both authority and owner registries must be sharded, either by hash slot or a
fixed UID hash shard. RPC calls, action dispatch, and gateway writes must happen
outside registry locks. Directory methods should return small immutable result
objects that callers process after releasing locks.

## Activation Flow

Gateway authentication already writes UID and device metadata into session
values before the activation hook runs. `internalv2/access/gateway` should use
that hook to call presence before CONNACK is written.

Activation is synchronous in the CONNECT path, so it must be bounded:

- short activation deadline
- per-target authority concurrency limit
- bounded pending activation queue
- retryable CONNACK failure when the queue is full or authority routing is not ready
- metrics for attempts, failures, queue-full, latency, and target node

```text
pkg/gateway CONNECT
  -> authentication succeeds
  -> access/gateway.OnSessionActivate
  -> presence.Activate(ctx, ActivateCommand)
  -> runtime/online.RegisterPending(real session)
  -> infra/cluster.ResolveRouteTarget(uid)
       -> RouteKey(uid)
       -> RouteTarget{HashSlot, SlotID, LeaderNodeID, RouteRevision, AuthorityEpoch}
  -> infra/cluster.RegisterRoute(target, route)
       -> authority re-checks target leadership before mutating memory
       -> route becomes active or pending-conflict
  -> route actions are applied synchronously when returned
  -> pending-conflict route is committed after action ack
  -> owner verifies the local session is still active
  -> runtime/online.MarkActive(sessionID)
  -> gateway writes success CONNACK
```

Invariant:

```text
successful CONNACK means the current UID Slot leader has an active Route
```

If authority registration fails, the owner removes the local route and rejects
the connect. This is fail-closed because a client must not become connected
without an authoritative address.

If the session closes while activation is in flight, the owner marks the local
route closing. After authority registration returns, activation must re-check
the local session state; if it is not active, activation unregisters the exact
authoritative route and fails the connect.

If activation succeeds but CONNACK write fails, the gateway rollback hook must
call `presence.Deactivate` for the activated session. This prevents a route from
remaining authoritative for a client that never observed connect success.

## Authority Fencing

Every authority mutation and lookup must carry `RouteTarget`. The receiver must
validate before reading or mutating the authority directory:

- `LeaderNodeID` matches the local node
- the local Slot runtime still leads `SlotID`
- `HashSlot` still maps to `SlotID`
- `RouteRevision` is not stale for the installed route table
- `AuthorityEpoch` matches the local authority epoch for `HashSlot`
- `HashSlot` equals the hash of `route.UID` for route mutations

Failed validation returns typed retryable errors such as `ErrNotLeader`,
`ErrStaleRoute`, or `ErrRouteNotReady`. The caller retries only after resolving
a fresh `RouteTarget`.

## Deactivation Flow

```text
gateway session close
  -> access/gateway.OnSessionClose
  -> presence.Deactivate(ctx, DeactivateCommand)
  -> runtime/online.MarkClosingAndUnregister(sessionID)
  -> enqueue exact authority UnregisterRoute tombstone
```

Unregister deletes only the exact route identity and carries a newer `OwnerSeq`
than the route activation. A stale unregister must not remove a newer route for
the same UID.

The close path removes local state synchronously. Authority unregister is sent
through a bounded retry/batch worker keyed by `(HashSlot, OwnerBootID)`. The
worker coalesces duplicate tombstones, preserves owner-sequence ordering for
each hash slot, records dropped-tombstone metrics, and retries with bounded
backoff. If retries are exhausted, the route may remain in authority memory
until lazy prune or owner-down cleanup proves it stale; lookup results are
therefore advisory until future write-time validation exists.

## Authority Lookup Flow

This phase exposes lookup APIs but does not implement delivery.

```text
presence.EndpointsByUID(ctx, uid)
  -> ResolveRouteTarget(uid)
  -> local directory lookup or remote node RPC with RouteTarget
  -> authority re-checks target leadership
  -> []Route
```

`EndpointsByUIDs` groups UIDs by hash slot and calls each authority once.
This keeps future delivery expansion from issuing one RPC per UID when many
recipients belong to the same authority.

Until owner-write validation is implemented, lookup proves only that the
authority currently has a route record. It does not prove the owner session is
still writable.

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

Future owner-write results must be classified:

- `ok`: write accepted
- `session_not_found`: prune exact route
- `boot_mismatch`: prune all routes for `(OwnerNodeID, OwnerBootID)` when fenced
- `route_fenced`: prune exact route
- `session_closing`: prune exact route
- `backpressured`: retry without pruning
- `write_failed`: retry or mark delivery failed without pruning

Owner writes must go through gateway `Session.WriteFrame` or an equivalent
gateway-owned writer so per-session serialization and outbound backpressure are
preserved.

Lazy prune is preferred over digest replay because the owner gives a precise
answer about one route. It does not require scanning or replaying unrelated
connections.

## Owner Restart Handling

Each owner process generates a new `OwnerBootID` at startup.

All routes from an old owner boot become fenced automatically because future
owner-write RPCs carrying the old boot ID are rejected by the new process.

When the cluster runtime exposes node-down or node-restart information,
`internalv2/app` should ask the relevant authorities to drop routes for:

```text
(ownerNodeID, oldOwnerBootID)
```

This cleanup is required once a reliable node-down or owner-boot-change signal
exists. Until then, write-time fencing remains the correctness mechanism and
lookup may temporarily return stale routes from old owner boots.

## Slot Leader Change Handling

The authority directory is in memory, so a new Slot leader may start without
the previous leader's routes. The repair mechanism is bounded rehydrate,
triggered only by authority changes.

```text
clusterv2 RouteAuthorityEvent
  -> app detects changed (HashSlot, LeaderNodeID, AuthorityEpoch)
  -> old authority epoch is cleared or quarantined
  -> owner registry pages active local routes for affected hash slots
  -> rehydrate worker sends routes to the new authority in bounded batches
  -> authority validates RouteTarget and owner sequence
  -> authority processes routes through the same conflict arbiter as RegisterRoute
```

Properties:

- no periodic full scan
- no digest mismatch protocol
- work is proportional to active local routes in affected hash slots
- batches are rate-limited and retryable
- routes in closing state are excluded
- stale batches for older `AuthorityEpoch` or older `OwnerSeq` are rejected
- rehydrate returns per-route accept/reject results and any required `RouteAction`

Suggested defaults:

- batch size: 512 routes
- max in-flight batches per target authority: 1
- retry backoff: bounded exponential backoff with jitter
- coalescing: merge repeated authority-change notifications for the same hash slot

The owner registry must provide a paged iterator such as:

```go
VisitActiveByHashSlot(hashSlot uint16, cursor RouteCursor, limit int, fn func(Route) bool) (RouteCursor, bool)
```

The iterator must not copy or sort the entire bucket. It should hold locks only
long enough to collect one bounded page.

Each owner serializes register, unregister tombstones, and rehydrate batches per
`(OwnerBootID, HashSlot)` or includes `OwnerSeq` tombstones that let the
authority reject stale upserts. A delayed rehydrate batch must not re-add a
route that was already unregistered with a newer sequence.

If rehydrate is still in progress, queries for that UID may temporarily return
offline. This is acceptable for this phase because presence is intentionally
memory-only.

When a node loses authority for a hash slot, it must stop serving that hash
slot immediately. When it later gains authority again, it starts a new
`AuthorityEpoch` and begins with a fresh directory for that epoch. Routes from a
previous epoch must never be returned by lookup or used for conflict decisions.

## Device Conflict Policy

The authority node remains the single conflict arbiter for a UID.

Conflict scope:

```text
same UID and same DeviceFlag
```

Rules:

- new master connection replaces all existing routes in the same scope
- new slave connection replaces only existing routes with the same DeviceID
- replacement routes are not removed from the active directory until owner
  actions are acknowledged
- replacement actions are applied on owner nodes before the new route is
  committed and before CONNACK succeeds

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
and aborts the pending authoritative route. Existing active routes remain
authoritative unless the failed action proves the existing owner/session is
stale.

Register with conflicts is a two-step authority operation:

```text
RegisterRoute
  -> validate RouteTarget
  -> compute conflicts
  -> store candidate as pending
  -> return PendingRouteToken and RouteAction values

owner applies actions

CommitRoute(PendingRouteToken)
  -> validate RouteTarget and authority epoch
  -> remove acknowledged conflicting routes
  -> promote candidate to active
```

If there are no conflicts, `RegisterRoute` may promote the route to active
immediately. Rehydrate uses the same conflict arbiter and returns per-route
results; it must not blind-upsert routes that violate device policy.

## RPC Surface

Presence authority RPCs:

- `RegisterRoute(RouteTarget, Route) -> RegisterResult`
- `CommitRoute(RouteTarget, PendingRouteToken) -> ok`
- `AbortRoute(RouteTarget, PendingRouteToken) -> ok`
- `UnregisterRoute(RouteTarget, routeIdentity, ownerSeq) -> ok`
- `EndpointsByUID(RouteTarget, uid) -> []Route`
- `EndpointsByUIDs(RouteTarget, uids) -> map[string][]Route`
- `RehydrateRoutes(RouteTarget, ownerNodeID, ownerBootID, []Route) -> []RehydrateResult`

Owner action RPCs:

- `ApplyRouteAction(action) -> ok`

Future owner-write RPC:

- `WriteRoute(routeIdentity, frameBytes) -> route write result`

The first implementation can use a compact binary codec similar to existing
node RPC codecs, but the usecase must only see typed ports and DTOs.

Presence RPC service IDs must be reserved centrally in `pkg/clusterv2/net`.
Add a uniqueness test so duplicate service registration cannot silently land in
the shared `uint8` namespace.

## High-Connection-Count Behavior

Normal steady-state costs:

- connect: one local pending insert, one bounded authority register, and
  optional conflict commit
- disconnect: one local delete and one queued exact unregister tombstone
- lookup: one authority lookup per UID group
- no per-connection timer
- no periodic full registry scan
- no digest computation
- no RPC or action dispatch while holding registry locks

Large repair costs:

- owner restart: fenced lazily by `OwnerBootID`
- stale route: deleted only after an exact failed write
- Slot leader change or authority epoch change: bounded paged rehydrate only
  for affected hash slots

This keeps the hot path predictable when a node holds many connections.

## Error Handling

Activation errors:

- route not ready or no Slot leader: reject connect with retryable reason
- activation queue full or activation deadline exceeded: reject connect with retryable reason
- stale RouteTarget or authority epoch mismatch: resolve a fresh target and retry within deadline
- authority register failure: rollback local route and reject connect
- route action dispatch failure: abort pending authority route, rollback local route, and reject connect
- CONNACK write failure after activation: rollback through gateway activation hook

Deactivation errors:

- local unregister always wins
- authority unregister tombstone is queued for bounded retry and logged/metriced on final drop

Lookup errors:

- route not ready or no Slot leader returns a typed retryable error
- missing UID returns an empty route list, not an error
- stale RouteTarget returns a typed retryable error

Rehydrate errors:

- failed batches are retried with bounded backoff
- repeated authority changes cancel obsolete batches for old epochs
- oversized batches are split before RPC
- per-route reject results are logged/metriced and do not block unrelated routes

## Testing Strategy

Focused unit tests:

- presence directory upsert, unregister, lookup, and exact route identity
- stale unregister does not remove a newer route
- stale rehydrate batch does not re-add a route removed by newer owner sequence
- authority rejects stale RouteTarget and old AuthorityEpoch
- authority clears/quarantines routes on leadership loss and starts fresh on gain
- device conflict rules produce correct `RouteAction` values
- conflict action failure leaves existing active routes authoritative
- owner registry indexes routes by hash slot without full scans
- owner registry pages rehydrate routes without copying an entire hash-slot bucket
- lazy prune removes only the failed route identity

Access adapter tests:

- gateway activation calls presence before successful CONNACK
- gateway rollback/deactivate runs when CONNACK write fails after activation
- gateway close calls presence deactivation
- node RPC codecs round-trip routes, route actions, and rehydrate batches

App wiring tests:

- `internalv2/app` wires presence, gateway activation, and node RPC handlers
- single-node cluster still routes through authority flow
- authority-epoch-change rehydrate selects only affected hash slots
- activation queue-full and deadline paths return retryable connect failures

The first implementation should run focused tests for changed packages, then
`go test ./internalv2/...`.

## Documentation Updates

When implemented, update:

- `internalv2/FLOW.md`
- `internalv2/app/FLOW.md`
- `internalv2/access/gateway/FLOW.md`
- new `internalv2/access/node/FLOW.md`
- `internalv2/infra/cluster/FLOW.md`
- new `internalv2/usecase/presence/FLOW.md`
- new `internalv2/runtime/online/FLOW.md`
- new `internalv2/runtime/presence/FLOW.md`
- `pkg/gateway/FLOW.md` for activation rollback and physical close contract
- `pkg/clusterv2/FLOW.md` for node RPC and route-authority watch surfaces
- `AGENTS.md` directory structure if new directories are added
