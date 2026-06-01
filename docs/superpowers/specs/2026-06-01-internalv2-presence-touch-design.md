# internalv2 Presence Touch Design

## Goal

Simplify internalv2 connection addressing by replacing authority-change
rehydrate with owner-forwarded touch keepalive.

Clients can still connect to any owner node, and each UID is still addressed by
its current hash-slot leader. The owner node behaves like a reverse proxy: it
owns the real client session and forwards route liveness to the authoritative
slot leader.

## Decision

Remove route rehydrate. Do not scan owner-local sessions on authority changes.

Use this simpler model instead:

```text
client -> owner: CONNECT
owner -> slot leader: RegisterRoute

client -> owner: PING or other valid activity
owner -> slot leader: TouchRoutes batch

client disconnects
owner -> slot leader: UnregisterRoute

owner crash / missing unregister / authority restart
slot leader -> TTL expiry removes stale route
```

After an authority restart or leader change, the new authority starts with an
empty in-memory directory. Existing client sessions recover their virtual route
when the owner forwards the next touch. Before that touch arrives, authority
lookup may miss the route; this is accepted for this phase.

## Non-Goals

- No authority-change rehydrate worker.
- No per-connection lease heartbeat RPC.
- No durable or replicated presence storage.
- No full owner-local connection scan after route events.
- No forced reconnect of all sessions in an affected hash slot.
- No realtime message delivery implementation in this phase.

## Architecture

`internalv2/runtime/online`
: Stores real owner-local sessions and their last local activity time. It does
  not page sessions for rehydrate.

`internalv2/usecase/presence`
: Orchestrates activation, deactivation, lookup, and owner-side touch marking.
  It stays entry-agnostic.

`internalv2/runtime/presence`
: Stores authoritative virtual routes in memory. Each route has `LastSeenUnix`
  or equivalent monotonic timestamp state. The directory expires routes whose
  touch deadline has passed.

`internalv2/access/gateway`
: On CONNECT, calls presence activation before CONNACK. On PING or other valid
  client activity, marks the owner-local route dirty for touch. On close, calls
  presence deactivation.

`internalv2/access/node`
: Exposes authority RPCs for register, unregister, lookup, owner action, and
  batch touch.

`internalv2/infra/cluster`
: Resolves the current UID hash-slot leader and routes authority RPCs. Touch
  flushes are grouped by authority target.

`internalv2/app`
: Wires the online registry, presence usecase, authority directory, owner action
  RPC, and a bounded owner touch flusher. It does not wire a rehydrate worker.

## Touch Flow

Owner nodes should not forward every client ping as an individual RPC. They mark
routes dirty locally and flush them in batches.

```text
gateway receives PING or valid client activity
  -> presence.MarkTouched(sessionID, now)
  -> online registry records the route as dirty

owner touch flusher tick
  -> drain dirty routes up to batch size
  -> group by current hash-slot authority target
  -> send TouchRoutes(target, []RouteTouch)
  -> authority validates target
  -> authority upserts or updates route LastSeen
```

`TouchRoutes` should be allowed to recreate a missing authoritative route when
the owner still has the real session. This is what replaces rehydrate after an
authority restart or leader move.

## Data Model

`Route` remains the authoritative virtual connection identity:

```text
UID
OwnerNodeID
OwnerBootID
OwnerSeq
SessionID
DeviceID
DeviceFlag
DeviceLevel
Listener
ConnectedUnix
```

`RouteTouch` can either reuse `Route` or use a smaller DTO. In this phase prefer
reusing `Route` for touch so a missing authority route can be rebuilt without a
second lookup.

Authority state adds:

```text
LastSeenUnix
```

Owner-local state adds:

```text
LastActivityUnix
DirtyTouch flag or dirty queue membership
```

## Expiry

The authority directory expires routes by TTL:

```text
if now - LastSeenUnix > PresenceTTL:
    remove route
```

Recommended defaults:

```text
TouchFlushInterval = 1s
TouchBatchSize     = 512
PresenceTTL        = 90s
```

`PresenceTTL` should be configured independently from client ping interval, but
the default assumes a 30s client ping interval and allows three missed touches.

## Conflict Handling

Register and touch use the same owner fencing rules:

- `OwnerBootID` rejects routes from a previous owner process generation.
- `OwnerSeq` rejects stale owner updates after a newer unregister.
- Device conflict policy is handled on register.

Touch should not create new device conflict actions. It refreshes or recreates
the same owner route. If a touch conflicts with a newer active route, the
authority rejects it as stale or ignores it.

## Failure Handling

If touch RPC fails, the owner keeps or requeues dirty entries for a bounded
retry. Repeated failure does not close the client immediately. The authority will
eventually expire the route if touches cannot reach it.

If route resolution is not ready, touch is retried later. Activation remains
fail-closed: a successful CONNACK still requires initial `RegisterRoute`.

If unregister fails, TTL cleanup is the safety net.

## Trade-Offs

Advantages:

- Removes rehydrate worker and authority-change replay complexity.
- Avoids full owner-local scans after leadership changes.
- Handles owner crash and missed unregister through TTL.
- Authority restart recovers naturally on the next owner touch.

Accepted costs:

- After authority restart or leader move, an existing session may be temporarily
  invisible until its next touch.
- Presence becomes near-realtime rather than exact-realtime.
- There is steady-state cross-node touch traffic, so batching is required.

## Testing

Add focused unit tests for:

- gateway PING marks a local route dirty.
- touch flusher batches dirty routes by authority target.
- `TouchRoutes` refreshes an existing route.
- `TouchRoutes` recreates a missing route after authority reset.
- stale owner sequence touch cannot overwrite a newer unregister.
- authority TTL removes untouched routes.
- unregister failure is eventually cleaned by TTL.

Add app wiring tests to prove:

- no rehydrate worker is constructed.
- touch flusher starts after cluster readiness and stops before cluster stop.
- config defaults are applied and invalid values are rejected.

## Migration From Current Implementation

The implementation plan should:

1. Remove `presence_rehydrate_worker`.
2. Remove `RehydrateRoutes`, `RehydrateResult`, and rehydrate-specific commit or
   abort APIs.
3. Add `TouchRoutes` to the authority directory and RPC adapter.
4. Add owner-local dirty touch tracking and a bounded flusher.
5. Update FLOW docs and config examples.

This design supersedes the rehydrate portion of
`2026-06-01-internalv2-connection-routing-design.md`; the rest of the
connection addressing model remains valid.
