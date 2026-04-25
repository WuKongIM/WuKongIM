# Internal Service Design

## Overview

Add `internal/service` as the first business-layer module above `internal/gateway`.

`internal/gateway` already owns listener lifecycle, protocol decoding, session creation, and optional `wkproto` authentication. The missing piece is the module that consumes `gateway.Handler`, turns authenticated sessions and decoded frames into business actions, and coordinates existing lower-level packages such as `wkstore` and `wkcluster`.

The first version of `internal/service` should be a thin orchestration layer, not a feature-complete IM domain. Its job is to establish the stable runtime boundary above the gateway so later domain modules such as `channel`, `message`, `user`, and `pusher` have a single integration point.

## Goals

- Provide the first production `gateway.Handler` implementation
- Own the node-local online session registry
- Translate gateway lifecycle callbacks into business-layer events
- Route decoded application frames to focused service handlers
- Encapsulate access to `wkstore` and cluster-facing coordination behind service-owned interfaces
- Keep the module small enough that later domain extraction does not require reworking gateway integration

## Non-Goals

- Building the program entrypoint or top-level process bootstrap
- Implementing complete message persistence, fan-out, retry, or offline delivery
- Implementing full channel membership or subscription management
- Recreating the old project's large `service` global registry pattern
- Introducing cross-node RPC routes before a concrete business use case requires them

## Existing Constraints

The current repository state imposes several constraints that shape this design:

- `internal/gateway` already consumes `wkproto CONNECT` and authentication internally. `internal/service` receives a session only after authentication succeeds.
- `gateway.Context.Session` already carries auth-derived values such as UID, device flag, and device level through session values.
- `pkg/wkstore` currently exposes user and channel metadata operations, but does not yet expose durable message persistence or subscriber management APIs.
- `pkg/wkcluster` already provides slot routing and proposal mechanics, but business-layer cross-node request patterns are still undefined.

Because of these constraints, the first version of `internal/service` must prioritize session ownership, frame routing, and dependency boundaries over full IM semantics.

## Approaches Considered

### 1. Build `internal/service` first

Create a small business runtime that implements `gateway.Handler`, owns session indexes, and routes frames into focused use-case handlers.

Pros:

- closes the biggest current gap: there is no production consumer of `gateway.Handler`
- gives later domain modules one stable ingress point
- keeps `gateway` transport and protocol code business-agnostic
- allows incremental feature work without revisiting session ownership

Cons:

- some frame handlers will initially be thin or return "not implemented" style results until lower layers mature

### 2. Build `internal/server` first

Create a process bootstrap/orchestration package before the business runtime exists.

Pros:

- gives the repository a conventional entrypoint module

Cons:

- mostly wires together empty business pieces
- does not solve the missing `gateway.Handler` implementation
- risks hard-coding assembly logic before business boundaries are proven

### 3. Build `internal/channel` first

Start directly with channel semantics and let it consume gateway events.

Pros:

- targets a visible domain concept early

Cons:

- forces a domain package to own generic session lifecycle and frame dispatch
- couples channel logic to gateway details too early
- leaves no neutral place for shared concerns such as connection registry and delivery fan-out

## Recommended Approach

Choose approach 1: build `internal/service` first.

This module should become the runtime seam between transport/protocol concerns and domain concerns:

- below it: `gateway`, `wkpacket`, `wkstore`, `wkcluster`
- above or beside it later: `channel`, `message`, `user`, `pusher`, `api`

## Architecture

`internal/service` should be composed of four parts.

### 1. Gateway Adapter

Implements `gateway.Handler`.

Responsibilities:

- react to `OnSessionOpen`, `OnFrame`, `OnSessionClose`, `OnSessionError`
- extract business identity from session values written by gateway auth
- delegate real work to service-owned collaborators

This layer should stay thin. It is an adapter, not the home of business logic.

### 2. Session Registry

Owns all node-local online session state.

Responsibilities:

- map `sessionID -> session metadata`
- map `uid -> set of live sessions`
- support lookup for local fan-out
- support idempotent register/unregister on duplicate close paths

This registry is the first business runtime primitive the repository is missing today.

### 3. Frame Router

Routes decoded frames to focused handlers based on concrete `wkpacket.Frame` type.

Initial handled frame families:

- `SendPacket`
- `RecvackPacket`
- optionally `PingPacket` and other control frames if business handling is needed

Deferred frame families:

- `SubPacket`

Anything unsupported should fail in one place with a clear error path rather than being silently ignored.

### 4. Business Ports

Hide lower-level dependencies behind service-owned interfaces so later domain extraction is cheap.

Initial ports:

- `IdentityStore`: read user metadata needed by session open validation
- `ChannelStore`: read channel metadata needed by send/sub decisions
- `DeliveryPort`: fan out frames to local sessions
- `ClusterPort`: reserved for future remote forwarding and ownership checks

The first implementation can back `IdentityStore` and `ChannelStore` with `wkstore.Store`. `ClusterPort` can start as a narrow wrapper or remain unused until remote delivery is introduced.

## Proposed Package Layout

The initial layout should stay flat and explicit:

- `internal/service/service.go`
- `internal/service/options.go`
- `internal/service/handler.go`
- `internal/service/registry.go`
- `internal/service/session_state.go`
- `internal/service/frame_router.go`
- `internal/service/send.go`
- `internal/service/recvack.go`
- `internal/service/errors.go`
- `internal/service/testkit/...` for fakes if test pressure grows

Rationale:

- the package is still small enough that subpackages would add ceremony
- frame-specific files keep behavior by domain action instead of by utility
- later extraction to `internal/channel` or `internal/message` can move whole files with minimal churn

## Main Types

### `Service`

Owns dependencies and implements `gateway.Handler`.

Suggested shape:

```go
type Service struct {
    registry *Registry
    users    IdentityStore
    channels ChannelStore
    delivery DeliveryPort
    cluster  ClusterPort
    opts     Options
}
```

### `Registry`

Owns node-local online session indexes.

Suggested responsibilities:

- `Register(meta SessionMeta) error`
- `Unregister(sessionID uint64)`
- `Session(sessionID uint64) (SessionMeta, bool)`
- `SessionsByUID(uid string) []SessionMeta`

`SessionMeta` should contain only business-relevant fields:

- session id
- uid
- device flag
- device level
- listener name
- connected time
- `gateway.Context.Session`

### `FrameRouter`

A simple dispatch helper rather than a separate package abstraction.

Suggested behavior:

- type-switch on `wkpacket.Frame`
- call `handleSend`, `handleRecvack`
- return `ErrUnsupportedFrame` for everything else

Keeping the switch centralized avoids frame handling scattering across the adapter.

## Lifecycle

### Session Open

1. `gateway` authenticates `CONNECT` and populates session values
2. `gateway` invokes `OnSessionOpen`
3. `internal/service` extracts UID and device fields from the session
4. `Registry.Register(...)` stores the online session
5. optional user existence checks run through `IdentityStore`

Important rule:

`internal/service` should treat `OnSessionOpen` as "session is authenticated and ready", not as "raw socket accepted".

### Frame Handling

1. `gateway` decodes a business frame
2. `internal/service.OnFrame(...)` routes by packet type
3. frame handler validates minimal invariants
4. frame handler calls stores, registry, or delivery ports
5. frame handler writes response frames through `ctx.WriteFrame(...)` when required

### Session Close

1. `gateway` invokes `OnSessionClose`
2. `Registry.Unregister(...)` removes the session
3. any best-effort cleanup hooks run locally

Close must be idempotent because transport, auth failure, and handler failure paths may converge on the same session.

## Send Path Scope

The first `SendPacket` path must stay deliberately narrow because the repository does not yet have a durable message model.

Version 1 send responsibilities:

- validate sender identity is present in session state
- validate target channel metadata can be resolved if required
- define one business seam where later persistence and fan-out will plug in
- allow local fan-out to same-node sessions when the target semantics are already knowable

Version 1 send non-responsibilities:

- durable message IDs
- cluster-wide delivery guarantees
- retry queues
- offline storage
- conversation state updates

This path is valuable even before full message persistence exists because it proves the runtime boundary and keeps later message work localized.

## Dependency Rules

To keep the architecture from collapsing back into a large shared-state package:

- `internal/service` may depend on `internal/gateway` and `pkg/*`
- `internal/gateway` must not depend on `internal/service`
- later domain modules may depend on `internal/service` ports, or be injected into `Service`
- avoid package-level globals in the new module
- prefer constructor-injected dependencies over singleton registries

## Testing Strategy

The first implementation should have focused unit tests, not only end-to-end tests.

Required coverage:

- register authenticated session on `OnSessionOpen`
- ignore or reject open callbacks missing required auth-derived values
- unregister session on `OnSessionClose`
- route `SendPacket` and `RecvackPacket` to the correct handler
- return a clear error for unsupported frame types
- support multiple concurrent sessions for the same UID
- local delivery fan-out writes to every matching live session exactly once

Test style:

- use fake `gateway.Context` and fake `session.Session`
- keep store and delivery dependencies behind small interfaces so fakes are easy to write
- reserve full `gateway` integration tests for one or two happy-path flows

## Incremental Delivery Plan

Build `internal/service` in three small steps:

1. Skeleton
   Add `Service`, `Options`, `Registry`, constructor, and `gateway.Handler` implementation for open/close/error.

2. Frame Routing
   Add centralized type-switch routing and explicit unsupported-frame behavior.

3. First Business Path
   Add the narrow `SendPacket` path and delivery abstraction, with `Recvack` as a lightweight stub if deeper lower-layer support is not ready.

`SubPacket` should remain explicitly out of scope for the first implementation pass. When channel membership and subscription semantics become concrete, add a dedicated follow-up design or extend this module then.

This order gets a real business runtime into the tree quickly without pretending the message domain is already finished.

## Follow-On Modules

Once `internal/service` exists, the next modules should follow the business seams proven by real traffic:

- `internal/message` or message-oriented storage extensions if send persistence is next
- `internal/channel` when membership/subscription semantics are ready
- `internal/pusher` when cross-session or cross-node delivery fan-out becomes non-trivial
- `internal/server` only after enough runtime pieces exist to justify a dedicated bootstrap package

## Summary

`internal/service` should be the next module because it closes the only critical gap left after `internal/gateway`: there is still no business runtime consuming authenticated sessions and decoded frames.

The first version should stay small, constructor-driven, and explicit. Its job is to own session state, route frames, and create clean seams for later domain modules, not to finish the entire IM feature set in one step.
