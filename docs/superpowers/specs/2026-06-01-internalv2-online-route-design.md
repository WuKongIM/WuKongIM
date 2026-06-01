# internalv2 Online Route Design

Date: 2026-06-01
Status: Draft for review
Owner: Codex

## 1. Purpose

`internalv2` needs a small online-route module before the rest of delivery is
migrated. Clients must be allowed to connect to any gateway node, while each UID
slot leader remains the authoritative owner of online visibility and online
write routing for that UID.

The first phase adds an independent `internalv2/usecase/online` module. It
tracks gateway sessions, synchronously registers online routes with the UID slot
leader during CONNECT, answers online queries, and provides a simple
UID-addressed `WriteBatch` facade. Callers must not know whether a UID's session
is local, remote, or owned by the current slot leader.

Single-node deployment remains a single-node cluster. The design keeps the
cluster semantics visible through router/client ports while allowing local
short-circuit calls when the leader or owner is the local node.

## 2. Selected Approach

The selected approach is a lightweight in-memory online route system:

- A gateway owner node holds the real client connection and session writer.
- The UID slot leader holds the authoritative `uid -> route` index.
- CONNECT succeeds only after the owner has synchronously registered the route
  with the UID slot leader.
- `OnlineBatch` and `WriteBatch` route through the UID slot leader and hide
  owner-node details from callers.
- Route references include `OwnerBootID`, `SessionID`, and `Version` fencing so
  stale routes cannot write to a restarted process or a reused session ID.

The design deliberately avoids Raft, owner-to-leader heartbeats, node startup
broadcasts, offline storage, and delivery retry queues in phase 1. Online routes
are ephemeral memory state. Stale routes are removed lazily when queried or used
for writes, with optional per-UID TTL lazy cleanup.

Two alternatives were rejected:

- Redirecting clients to their UID leader would make the online directory
  simpler, but clients must be able to connect to any node and keep that
  connection.
- Asynchronous route registration would reduce CONNECT latency, but it creates a
  window where the client has connected successfully while the UID leader still
  reports the user offline.

## 3. Package Layout

```text
internalv2/usecase/online/
  app.go              Public facade, Options, constructor.
  types.go            Commands, results, RouteRef, OnlineState.
  ports.go            Router, AuthorityPeerClient, OwnerClient, SessionWriter.
  owner_registry.go   Node-local real session registry.
  authority_dir.go    UID-leader authoritative route index.
  lifecycle.go        Activate and Deactivate flows.
  write.go            Online Write and WriteBatch routing.
  errors.go           Online-specific errors.
  FLOW.md             Package flow and boundary notes.
```

RPC adapters belong outside the usecase package. Inbound node RPC handlers
should live under `internalv2/access/node` when they are introduced.
`internalv2/infra/cluster` should stay focused on runtime/client adaptation,
including outbound peer clients that implement online ports.

The dependency direction is:

```text
access/gateway -> usecase/online
usecase/online -> usecase-defined ports and opaque outbound DTOs
infra/cluster -> cluster runtime/client adapters, implementing peer ports
access/node -> node RPC request handlers, calling usecase/online
app -> access, usecase, infra, and pkg composition dependencies
```

## 4. Public API

The module exposes a small facade:

```go
type App struct {}

func (a *App) Activate(ctx context.Context, cmd ActivateCommand) error
func (a *App) Deactivate(ctx context.Context, cmd DeactivateCommand) error

func (a *App) Online(ctx context.Context, uid string) (OnlineState, error)
func (a *App) OnlineBatch(ctx context.Context, uids []string) (map[string]OnlineState, error)

func (a *App) Write(ctx context.Context, cmd WriteCommand) (WriteResult, error)
func (a *App) WriteBatch(ctx context.Context, items []WriteCommand) []WriteResult
```

`Online` and `Write` are batch-of-one wrappers. `OnlineBatch` and `WriteBatch`
are the canonical paths so later delivery code can remain batch-oriented.

The gateway adapter only needs the lifecycle subset:

```go
type OnlineLifecycle interface {
    Activate(ctx context.Context, cmd online.ActivateCommand) error
    Deactivate(ctx context.Context, cmd online.DeactivateCommand) error
}
```

Message and delivery code use only `OnlineBatch` and `WriteBatch`; they must not
depend on `RouteRef`.

`Options` wires the module without exposing cluster implementation details:

```go
type Options struct {
    LocalNodeID         uint64
    OwnerBootID         uint64
    Router              Router
    AuthorityPeerClient AuthorityPeerClient
    OwnerClient         OwnerClient
    Now                 func() time.Time
    RouteTTL            time.Duration
    Observer            Observer
}
```

`RouteTTL` may be zero. A zero value disables route TTL and leaves stale route
cleanup to write-time fencing. With zero TTL, a stale route for an untouched UID
can remain in memory indefinitely; this is acceptable only because owner fencing
prevents false successful writes.

## 5. Types

`ActivateCommand` maps an authenticated gateway session into online state:

```go
type ActivateCommand struct {
    UID         string
    DeviceID    string
    DeviceFlag  uint8
    DeviceLevel uint8
    Listener    string
    SessionID   uint64
    Session     SessionWriter
    ConnectedAt time.Time
}
```

`DeactivateCommand` removes a local owner session and best-effort removes the
authoritative route:

```go
type DeactivateCommand struct {
    UID       string
    SessionID uint64
}
```

`WriteCommand` is UID-addressed and intentionally hides route details:

```go
type WriteCommand struct {
    UID     string
    Message OutboundMessage
}
```

`OutboundMessage` is an online-local DTO, not `pkg/protocol/frame.Frame`:

```go
type OutboundMessage struct {
    Kind    OutboundKind
    Payload []byte
}
```

`internalv2/usecase/online` treats the payload as opaque. The gateway or node RPC
adapter owns conversion between concrete protocol frames and `OutboundMessage`.
This keeps the online usecase entry-agnostic and prevents protocol imports from
leaking into the module.

`RouteRef` is internal to the online module and online RPC DTOs:

```go
type RouteRef struct {
    UID          string
    OwnerNodeID uint64
    OwnerBootID uint64
    SessionID   uint64
    Version     uint64
    DeviceID    string
    DeviceFlag  uint8
    DeviceLevel uint8
    Listener    string
    ConnectedAt time.Time
    ExpiresAt   time.Time // zero disables TTL for this route
}
```

`OwnerBootID` changes on every node process start. It should be generated once
from a collision-resistant source such as `crypto/rand` and must not be
persisted or restored. It fences stale routes left behind by fast node restarts.

`Version` is allocated by the owner registry from a strictly increasing
per-process counter before `RegisterPending`. It starts at 1 for each boot and
increments on every activation attempt. It fences stale unregisters and stale
route writes even if a session ID is later reused.

Online queries return only summary state:

```go
type OnlineState struct {
    UID    string
    Online bool
    Routes int
}
```

Write results expose business state, not internal stale-route details:

```go
type WriteStatus uint8

const (
    WriteStatusOK WriteStatus = iota
    WriteStatusOffline
    WriteStatusTemporaryUnavailable
)

type WriteResult struct {
    UID    string
    Status WriteStatus
    Err    error
}
```

Stale route conditions such as session missing, boot mismatch, and version
mismatch are handled inside the UID leader and reported to callers as
`WriteStatusOffline` after cleanup.

## 6. Ports

The router hides cluster details behind a narrow interface:

```go
type Router interface {
    RouteUID(uid string) (UIDRoute, error)
}

type UIDRoute struct {
    UID          string
    SlotID       uint64
    LeaderNodeID uint64
    Revision     uint64
}
```

`RouteUID` must resolve slot, leader, and revision from one cluster routing
snapshot. It must not compute `SlotForUID` and `LeaderOfSlot` from different
snapshots. The clusterv2 adapter should map this to the atomic key-routing API
and preserve typed routing errors.

The authority peer client sends UID-leader operations to an explicit local or
remote leader node:

```go
type RegisterCommand struct {
    Route RouteRef
}

type UnregisterCommand struct {
    Route RouteRef
}

type AuthorityPeerClient interface {
    Register(ctx context.Context, leaderNodeID uint64, cmd RegisterCommand) error
    Unregister(ctx context.Context, leaderNodeID uint64, cmd UnregisterCommand) error
    OnlineBatch(ctx context.Context, leaderNodeID uint64, uids []string) (map[string]OnlineState, error)
    WriteBatch(ctx context.Context, leaderNodeID uint64, items []WriteCommand) []WriteResult
}
```

`authorityGateway`, not `AuthorityPeerClient`, owns `RouteUID` calls, grouping,
and local short-circuit decisions. The peer client receives the already chosen
target leader. If the target replies `not_leader`, the peer client should return
a typed not-leader error carrying the observed leader; `authorityGateway` may
refresh routing and retry within a bounded policy.

The owner client sends concrete route writes to the node that holds the gateway
session:

```go
type WriteOwnerCommand struct {
    Route   RouteRef
    Message OutboundMessage
}

type WriteOwnerResult struct {
    Route  RouteRef
    Status ownerWriteStatus
    Err    error
}

type OwnerClient interface {
    WriteOwnerBatch(ctx context.Context, ownerNodeID uint64, items []WriteOwnerCommand) []WriteOwnerResult
}
```

`SessionWriter` is the only gateway session surface stored by online:

```go
type SessionWriter interface {
    ID() uint64
    WriteOnline(OutboundMessage) error
}
```

The gateway package is adapted to this interface in `internalv2/access/gateway`;
`online` does not depend on `pkg/gateway.Context` or `pkg/protocol/frame`.

## 7. Internal Components

`App` is a facade over four components:

```text
ownerRegistry      Node-local real sessions and route state.
authorityDir       UID-leader authoritative uid -> RouteRef index.
authorityGateway   Resolves UID leaders and routes Register/Online/Write with local short-circuit.
ownerGateway       Routes WriteOwnerBatch to owner nodes with local short-circuit.
```

`ownerRegistry` stores only local gateway sessions:

```text
sessionsByID: sessionID -> localRoute
routesByUID:  uid -> set(sessionID)
```

`localRoute` tracks:

```text
RouteRef
SessionWriter
state: pending | active | closing
```

Only active routes can be written. Pending routes exist while CONNECT is
synchronously registering the UID leader route.

`authorityDir` stores authoritative routes when the local node is the UID slot
leader:

```text
routesByUID: uid -> map[routeKey]RouteRef
routesByKey: routeKey -> uid
```

`routeKey` is:

```text
OwnerNodeID + OwnerBootID + SessionID
```

Unregister and stale removal require fencing by UID, owner node, boot, session,
and version so old lifecycle events cannot delete newer routes.

Register is an upsert by `routeKey`. Re-registering the same route key replaces
the previous route only when the incoming version is equal or newer. Older
versions are ignored so delayed activation messages cannot overwrite newer
session metadata.

## 8. Activate Flow

```text
access/gateway.OnSessionActivate
  -> online.Activate(ctx, cmd)
```

`Activate` runs:

```text
1. Validate UID and Session.
2. Allocate Version and build RouteRef with LocalNodeID, OwnerBootID, SessionID, and device metadata.
3. ownerRegistry.RegisterPending(route, session).
4. authorityGateway.Register(route).
5. ownerRegistry.MarkActive(sessionID, route.Version).
6. Return nil.
```

CONNECT can return CONNACK success only after `Activate` returns nil.

Gateway activation rollback is part of the phase-1 contract. The gateway core
currently calls `OnSessionActivate` before writing a successful CONNACK, while
`OnSessionClose` is dispatched only after `OnSessionOpen`. If CONNACK writing
fails after `Activate` succeeds, online state would otherwise remain registered
without a real open session.

The preferred implementation is a narrow gateway rollback hook that is invoked
when activation succeeded but CONNACK success was not completed. The
`internalv2/access/gateway` adapter maps that hook to `online.Deactivate`.
Extending close semantics to dispatch close for activated-but-not-open sessions
is also acceptable, but phase 1 must explicitly test this lifecycle gap.

Failure handling:

- If `RegisterPending` fails, `Activate` returns the error.
- If `authorityGateway.Register` fails, `Activate` unregisters the pending local
  route and returns the error.
- If `MarkActive` fails because the session closed during registration,
  `Activate` best-effort unregisters the authoritative route, unregisters the
  local route, and returns `ErrSessionClosed`.

The key invariant is:

```text
CONNACK success => owner active session exists && UID leader route exists
```

## 9. Deactivate Flow

```text
access/gateway.OnSessionClose
  -> online.Deactivate(ctx, cmd)
```

`Deactivate` runs:

```text
1. ownerRegistry.MarkClosingOrUnregister(sessionID).
2. If no local route existed, return nil.
3. authorityGateway.Unregister(route) best-effort.
4. Return nil.
```

Connection close must not be blocked by remote unregister failure. Failed remote
unregisters are logged or observed. Stale routes are later removed by owner
write fencing or TTL lazy cleanup.

`Deactivate` and authoritative unregister are idempotent.

## 10. OnlineBatch Flow

`OnlineBatch` groups input UIDs by UID slot leader:

```text
1. For each UID, call `Router.RouteUID`.
2. Group UIDs by leader node.
3. Local leader groups call authorityDir.OnlineBatch.
4. Remote leader groups call AuthorityPeerClient.OnlineBatch with the target leader.
5. Merge results by UID.
```

`OnlineBatch` is authoritative-directory based. It does not synchronously verify
owner sessions. This means old routes can briefly report online until they are
cleaned by write attempts or TTL lazy cleanup. If TTL is disabled, an untouched
stale route can report online indefinitely; the correctness boundary is that it
cannot produce a successful write.

## 11. WriteBatch Flow

`WriteBatch` also groups items by UID leader:

```text
1. Preserve each input index.
2. For each UID, call `Router.RouteUID`.
3. Group WriteCommand items by UID leader.
4. Local leader groups call writeAuthoritativeBatch.
5. Remote leader groups call AuthorityPeerClient.WriteBatch with the target leader.
6. Merge per-item results in original input order.
```

`writeAuthoritativeBatch` runs on the UID leader:

```text
1. Look up routes for each UID in authorityDir.
2. Lazily remove expired routes for touched UIDs.
3. Use RoutePolicy to select target routes. Phase 1 selects all active routes.
4. Group WriteOwnerCommand items by OwnerNodeID.
5. Local owner groups call ownerRegistry.WriteOwnerBatch.
6. Remote owner groups call OwnerClient.WriteOwnerBatch.
7. Remove routes that return stale owner results.
8. Summarize per input item.
```

Default per-UID result precedence is:

```text
OK > TemporaryUnavailable > Offline
```

Rules:

- No route means `Offline`.
- All routes stale means stale routes are removed and the item is `Offline`.
- Any successful route write means `OK`.
- Temporary owner errors without any success mean `TemporaryUnavailable`.
- Mixed stale and temporary errors without any success mean
  `TemporaryUnavailable`.

`WriteBatch` must preserve input order even after leader and owner grouping.

## 12. Owner Write Flow

`WriteOwnerBatch` is route-addressed and runs on the real session owner node.
For each item:

```text
1. Reject if Route.OwnerBootID != current OwnerBootID.
2. Look up local route by Route.SessionID.
3. If the route is pending, return TemporaryUnavailable rather than stale.
4. Reject if the route is not active.
5. Reject if the local route UID or Version does not match the item route.
6. Release registry locks.
7. Call SessionWriter.WriteOnline(message).
8. Return owner write status.
```

Owner write status is internal:

```text
OK
SessionMissing
BootMismatch
VersionMismatch
TemporaryUnavailable
```

The UID leader maps stale statuses to route removal and `WriteStatusOffline`.
It maps temporary failures to `WriteStatusTemporaryUnavailable`.

Pending local routes are temporary, not stale. This prevents a route that was
just registered with the UID leader from being deleted during the small window
before `Activate` marks the local owner route active.

## 13. Route Lifetime

Phase 1 does not add owner heartbeats or node startup broadcasts.

Stale route controls are:

- `OwnerBootID` fencing for fast node restarts.
- `SessionID` and `Version` fencing for session reuse and stale unregisters.
- Lazy stale removal when `WriteOwnerBatch` reports boot, session, or version
  mismatch.
- Optional route TTL with per-UID lazy cleanup. Zero `ExpiresAt` means no TTL.

If a node crashes and no one queries or writes an affected UID, old routes can
remain in memory until TTL cleanup when TTL is enabled. If TTL is disabled, they
can remain indefinitely. This is an accepted phase-1 tradeoff: stale routes can
cause false online, but cannot write to a restarted owner process.

## 14. Concurrency Model

Phase 1 uses sharded maps, not actors:

```text
ownerRegistry: 64 or 128 shards, keyed by SessionID.
authorityDir:  64 or 128 shards, keyed by UID.
```

Rules:

- Do not hold registry or directory locks while doing RPC.
- Do not hold locks while calling `SessionWriter.WriteOnline`.
- Do not hold ownerRegistry and authorityDir locks at the same time.
- Copy route/session references under lock, release the lock, then write or
  call remote clients.

This keeps the online module simple while supporting large online sets. Actors,
background scanners, and bounded internal write parallelism can be added later
if measurements require them.

## 15. RPC Contract

Phase 1 needs five RPC operations:

```text
RegisterAuthoritative(RouteRef)
UnregisterAuthoritative(RouteRef)
OnlineAuthoritativeBatch([]UID)
WriteAuthoritativeBatch([]WriteCommand)
WriteOwnerBatch([]WriteOwnerCommand)
```

Authority RPC direction:

```text
any node -> UID slot leader
```

Owner RPC direction:

```text
UID slot leader -> real session owner node
```

`authorityGateway` handles routing and local short-circuiting before calling the
peer client. RPC adapters handle not-leader responses from explicit target
leaders. If an authority request hits a follower, the follower returns
`not_leader` with `leader_id`; the gateway refreshes routing and may retry the
leader within a bounded policy. Public callers see a clean online result or
online error, not follower-routing details.

Owner RPC is direct by `OwnerNodeID`; it does not route through slots.

## 16. App Wiring

`internalv2/app.New` creates and injects online:

```text
OwnerBootID := new process-unique boot id

onlineApp := online.New(online.Options{
  LocalNodeID:     clusterCfg.NodeID,
  OwnerBootID:     OwnerBootID,
  Router:          onlineRouter{cluster: app.cluster},
  AuthorityPeerClient: onlineAuthorityPeerClient{...},
  OwnerClient:     onlineOwnerClient{...},
})

gatewayHandler := accessgateway.New(accessgateway.Options{
  Messages: app.messages,
  Online:   onlineApp,
})
```

The gateway handler implements `pkg/gateway.SessionActivator` by mapping
authenticated session values into `online.ActivateCommand`. On close it calls
`online.Deactivate` best-effort. It must also handle activation rollback when
`Activate` succeeded but CONNACK success was not completed.

## 17. Non-Goals

Phase 1 does not implement:

- Offline storage.
- Delivery retry queues.
- Message durability or ack state.
- Raft-backed online state.
- Owner-to-leader heartbeat.
- Node startup route cleanup broadcast.
- Multi-device kick policies.
- Master/slave device routing policies.
- External route-list APIs.
- Background TTL scanner.

These can be added later without changing the public `online.App` facade.

## 18. Observability

The online module should expose an optional observer rather than hard-wiring
metrics:

```go
type Observer interface {
    OnRouteRegistered(RouteRef)
    OnRouteUnregistered(RouteRef)
    OnStaleRoute(RouteRef, string)
    OnWriteResult(WriteResult)
}
```

Useful event names:

```text
online.activate.success
online.activate.register_failed
online.deactivate.unregister_failed
online.write.ok
online.write.offline
online.write.temporary_unavailable
online.route.stale_removed
```

Do not log every successful write at info level. Use debug logs or metrics for
high-volume write success paths.

## 19. Tests

Use focused unit tests first:

- `ownerRegistry` pending, active, closing, and duplicate lifecycle behavior.
- `authorityDir` register, unregister fencing, multi-route UID handling, and
  stale removal.
- `Activate` success and failed-register rollback.
- `Activate` session-closed-during-register cleanup.
- `Deactivate` idempotence and best-effort unregister behavior.
- `OnlineBatch` grouping by UID leader and merge behavior.
- `WriteBatch` local leader plus local owner.
- `WriteBatch` local leader plus remote owner.
- `WriteBatch` remote leader grouping while preserving result order.
- Owner boot mismatch removes stale routes and returns offline.
- Session/version mismatch removes stale routes and returns offline.

Gateway adapter tests:

- `OnSessionActivate` calls `online.Activate`.
- Activate failure rejects CONNECT.
- `OnSessionClose` calls `online.Deactivate`.
- Activation rollback calls `online.Deactivate` when CONNACK write fails before
  `OnSessionOpen`.
- Existing SEND -> SENDACK behavior remains unchanged.

Phase acceptance also requires focused integration coverage:

- A connects to node1 while A's UID leader is node2; node2 reports online.
- A connects to node1; node3 calls `online.Write(A)`; the outbound message is
  written to node1's session through node2.
- node1 restarts with a new boot ID while node2 has an old route; write cleans
  the stale route and returns offline.

## 20. Acceptance Criteria

The first phase is complete when:

- `internalv2/usecase/online` exists as an independent module with `FLOW.md`.
- Gateway CONNECT success requires successful `online.Activate`.
- Gateway activation rollback removes online state if CONNACK success is not
  completed.
- Clients can connect to any node and still become visible on their UID leader.
- `OnlineBatch` hides leader routing from callers.
- `WriteBatch` hides UID leader and owner node routing from callers.
- Stale routes cannot write to restarted owners or reused sessions.
- Deactivate does not block connection close on remote unregister failure.
- `WriteBatch` preserves input/result order.
- `internalv2/usecase/online` does not import `pkg/gateway` or
  `pkg/protocol/frame`.
- No Raft, owner heartbeat, startup broadcast, offline storage, or retry queue is
  introduced in phase 1.
- `internalv2/FLOW.md`, `internalv2/app/FLOW.md`, and
  `internalv2/access/gateway/FLOW.md` are updated when implementation begins.
