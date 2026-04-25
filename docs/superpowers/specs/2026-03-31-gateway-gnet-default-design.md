# Gateway gnet Default Transport Design

## Overview

Extend `internal/gateway` with a `gnet`-based transport implementation for both TCP and WebSocket listeners, keep `stdnet` available as a non-default transport, and switch the built-in listener presets to use `gnet` by default.

The existing gateway core, session layer, and protocol adapters remain transport-agnostic. The new work is confined to transport construction, transport lifecycle orchestration, and a new `transport/gnet` implementation.

## Goals

- Add a `gnet` transport implementation for `tcp` listeners
- Add a `gnet` transport implementation for `websocket` listeners
- Allow multiple logical `gnet` listeners to share a single underlying `gnet` engine group
- Keep `stdnet` supported and selectable through explicit listener configuration
- Change built-in listener presets to default to `gnet`
- Preserve the existing `core`, `session`, and `protocol` APIs used by the rest of the gateway

## Non-Goals

- Removing `stdnet`
- Introducing same-port protocol auto-detection between raw TCP and WebSocket
- Rewriting gateway business callbacks or session semantics
- Adding TLS, middleware systems, or transport-specific behavior to the gateway core
- Generalizing transport orchestration beyond what is needed to support grouped `gnet` listeners

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `gnet` sharing unit | Share a `gnet` engine group across multiple logical listeners | Matches `gnet`'s ability to bind multiple addresses while preserving listener-level semantics in the gateway |
| Core integration boundary | Keep `core`, `session`, and `protocol` unaware of grouped engines | Prevents transport-specific concerns from leaking into the rest of the gateway |
| Transport execution model | Event loop only drains, frames, routes, and enqueues; per-connection workers call gateway handlers | Follows `gnet` best practices by avoiding blocking work in event callbacks |
| WebSocket support | Implement HTTP upgrade and frame parsing inside `transport/gnet` | Keeps WebSocket protocol details out of the core and allows `gnet` to own the full connection path |
| Default listener presets | Switch built-in presets from `stdnet` to `gnet` | Satisfies the requested default behavior while preserving explicit opt-in to `stdnet` |
| `stdnet` compatibility | Retain `stdnet` factory and adapt it to the new factory API | Minimizes migration risk and keeps existing tests meaningful |

## Existing Constraints

The current gateway startup flow assumes a transport factory creates one listener at a time:

- `core.Server.Start()` loops listeners and calls `factory.New(...)`
- `transport.Listener` exposes `Start()`, `Stop()`, and `Addr()`
- `binding.TCPWKProto` and `binding.WSJSONRPC` currently hard-code `stdnet`

That model is sufficient for `stdnet`, but it is awkward for shared `gnet` engines because one real engine may back multiple logical listeners. The transport factory contract must therefore change to support grouped construction while preserving listener-level control in the core.

The current option validation also rejects duplicate listener names but does not reject duplicate addresses. Shared `gnet` routing requires one logical listener per bound address, so validation must be tightened in this work.

## Listener Address Policy

In the first version, no two gateway listeners may share the same bound address.

Validation rule:

- reject configurations where two listeners have the same normalized `Address`, regardless of `Network`, `Transport`, `Protocol`, or `Path`
- report this condition as a dedicated validation error, `ErrListenerAddressDuplicate`
- perform this check in `Options.Validate()` before any transport factory build or bind attempt

Rationale:

- the gateway transport model routes one accepted connection to exactly one logical listener
- the shared `gnet` routing table is keyed by bound local address
- same-address multiplexing between raw TCP and WebSocket is explicitly out of scope
- same-address multiple-WebSocket-listener routing by path would require a different transport model and is not needed here

This rule should be enforced in gateway option validation before any transport factory is called.

## Architecture

The transport layer is split into three levels:

1. Logical listener
   A gateway listener remains the unit of configuration and identity. It owns:
   - `Name`
   - `Network`
   - `Address`
   - `Path`
   - listener-scoped error reporting
   - one `transport.ConnHandler`

2. Engine group
   A `gnet` engine group is the real I/O runtime. It owns:
   - one underlying `gnet` engine lifecycle
   - one set of bound addresses
   - routing from bound local addresses to logical listeners
   - connection state tables
   - shared start/stop reference counting

3. Connection state
   Each accepted connection owns:
   - transport-level mode (`tcp`, `ws_handshake`, `ws_frames`)
   - inbound transport buffers
   - WebSocket protocol state when applicable
   - one serialized worker queue for gateway handler calls

The gateway core continues to see a set of logical `transport.Listener` handles. It does not know whether those handles are backed by one or many underlying engines.

## Transport API Changes

The transport factory contract changes from one-at-a-time construction to grouped construction:

```go
type Factory interface {
    Name() string
    Build(specs []ListenerSpec) ([]Listener, error)
}

type ListenerSpec struct {
    Options ListenerOptions
    Handler ConnHandler
}
```

`transport.Listener` remains:

```go
type Listener interface {
    Start() error
    Stop() error
    Addr() string
}
```

Semantics:

- `stdnet`: one logical listener handle maps to one real listener
- `gnet`: multiple logical listener handles may reference one engine group
- `Start()` on grouped `gnet` listeners is idempotent and only the first start actually boots the shared engine
- `Stop()` decrements the group reference count; the last stop shuts down the shared engine

## Core Startup and Shutdown Flow

`core.Server.Start()` changes to:

1. Build `listenerRuntime` values as it does today
2. Group runtimes by `ListenerOptions.Transport`
3. For each transport:
   - build `[]transport.ListenerSpec`
   - call `factory.Build(specs)`
   - assign returned logical listeners back to the matching runtimes
4. Start all returned listener handles
5. Roll back already-started listeners on any failure

`core.Server.Stop()` remains listener-oriented:

- stop every logical listener handle
- close active sessions
- wait for worker goroutines to drain

This keeps the core lifecycle model stable while allowing transport internals to share resources.

## `transport/gnet` Internal Design

### Files

New files:

- `internal/gateway/transport/gnet/factory.go`
- `internal/gateway/transport/gnet/group.go`
- `internal/gateway/transport/gnet/listener.go`
- `internal/gateway/transport/gnet/conn.go`
- `internal/gateway/transport/gnet/ws_handshake.go`
- `internal/gateway/transport/gnet/ws_frame.go`
- `internal/gateway/transport/gnet/factory_test.go`
- `internal/gateway/transport/gnet/tcp_listener_test.go`
- `internal/gateway/transport/gnet/ws_listener_test.go`

Modified files:

- `internal/gateway/transport/transport.go`
- `internal/gateway/core/server.go`
- `internal/gateway/types/options.go`
- `internal/gateway/types/errors.go`
- `internal/gateway/transport/stdnet/factory.go`
- `internal/gateway/testkit/fake_transport.go`
- `internal/gateway/gateway.go`
- `internal/gateway/binding/builtin.go`
- `internal/gateway/options_test.go`
- `internal/gateway/gateway_test.go`
- `go.mod`
- `go.sum`

### Main Types

`Factory`

- implements grouped construction
- groups listener specs into one or more engine groups
- returns logical listener handles in the same order as input specs

`engineGroup`

- owns one underlying `gnet` runtime
- owns `start`/`stop` synchronization and reference counting
- keeps `localAddr -> listenerRuntime` routing
- stores connection state in `gnet.Conn.Context()`

`listenerRuntime`

- stores one logical listener's normalized options
- stores the listener's `ConnHandler`
- stores the resolved bound address used for `Addr()`

`listenerHandle`

- implements `transport.Listener`
- delegates start/stop to its engine group
- exposes its own listener address

`connState`

- stores one connection's mode and buffers
- stores the logical listener assignment
- stores a serialized worker queue for handler callbacks
- stores WebSocket state for handshake, fragmentation, and close sequencing

`stateConn`

- implements `transport.Conn`
- exposes connection identity and addresses
- routes writes through the `gnet` async write path
- encodes outbound WebSocket frames when the connection is in WebSocket mode

## `gnet` Event-Loop Rules

The `gnet` transport must follow the `gnet` best-practices guidance:

- do not run blocking or unbounded application work in event-loop callbacks
- do not pass `gnet`-owned read buffers across goroutines
- drain available inbound data in `OnTraffic()` until no complete transport unit remains
- prefer connection-local state via `Conn.Context()`

Therefore, event-loop callbacks are limited to:

- listener routing
- connection state lookup or creation
- inbound buffer draining
- HTTP upgrade parsing
- WebSocket frame parsing and control-frame handling
- copying fully decoded application payloads
- enqueuing connection events to a serialized worker
- scheduling async writes or connection closes

Per-connection workers are responsible for:

- `ConnHandler.OnOpen`
- `ConnHandler.OnData`
- `ConnHandler.OnClose`

This preserves per-connection ordering without blocking the event loop.

## TCP Handling

For TCP listeners:

- accepted connections start in `tcp` mode
- `OnTraffic()` drains available bytes and copies complete payload chunks into worker events
- `stateConn.Write()` writes raw bytes through the `gnet` async write path
- closing the connection emits exactly one close event to the worker

The gateway core and protocol adapters continue to decide how inbound byte streams are decoded into application frames.

## WebSocket Handling

For WebSocket listeners, transport logic is split into two phases.

### Phase 1: HTTP Upgrade

Connections start in `ws_handshake` mode. `OnTraffic()` accumulates bytes until a complete HTTP request header is available, then:

- verifies method and required upgrade headers
- verifies `Sec-WebSocket-Key` and version
- verifies the request path against `ListenerOptions.Path`
- emits a `101 Switching Protocols` response on success
- switches the connection to `ws_frames` mode

On handshake failure:

- emit an HTTP error response when practical
- report the error through the listener's error callback
- close the connection

### Phase 2: WebSocket Frames

In `ws_frames` mode, `OnTraffic()` continues draining and parsing frames:

- accept masked client frames only
- support `text` and `binary` application messages
- support fragmented messages
- handle `ping`, `pong`, and `close`
- treat protocol violations as transport errors that trigger a close

Only complete application payloads are copied and sent to the connection worker. The worker never sees partial handshake data or partial frames.

Outbound writes use `stateConn.Write()`:

- TCP listeners write raw bytes
- WebSocket listeners encode outbound bytes into WebSocket frames before async write

## Engine Grouping Rules

`gnet` listeners are grouped by a transport-internal engine key.

The first implementation exposes no `gnet` runtime tuning knobs through gateway options, so the grouping rule is intentionally explicit:

| Field | Participates in engine compatibility? | Reason |
|-------|--------------------------------------|--------|
| `Transport` | Yes | Only `gnet` listeners are considered for grouped construction |
| `Network` | No | TCP and WebSocket differ only in connection state, not engine construction |
| `Address` | No | One `gnet` engine group may bind multiple addresses |
| `Path` | No | WebSocket path is listener-local handshake policy, not engine configuration |
| `Protocol` | No | Protocol adapters run above the transport layer |
| Listener name | No | Identity only; does not affect engine construction |

Therefore, in the first version, all `gnet` listeners in one gateway instance belong to one shared engine group.

This grouping rule is applied only after option validation has already rejected duplicate addresses via `ErrListenerAddressDuplicate`. Engine-group construction never attempts to merge, prioritize, or disambiguate two logical listeners that claim the same address.

If future gateway options introduce `gnet` runtime knobs that materially affect engine construction, those new transport-internal fields must be added to the engine key before grouped construction is expanded.

The first version explicitly supports:

- multiple TCP listeners sharing one engine group
- multiple WebSocket listeners sharing one engine group
- mixed TCP and WebSocket listeners sharing one engine group when their engine configuration is compatible

The first version does not support:

- raw TCP and WebSocket multiplexing on the same address
- transport-level auto-detection on one port

## Error Handling

Listener-scoped failures continue to report through `Handler.OnListenerError`, including:

- `gnet` engine startup failure
- address binding failure
- accept-time routing failure
- WebSocket handshake failure before a session is opened

For shared `gnet` engine groups, callback fanout is defined explicitly:

- failures attributable to one logical listener or one bound address are reported once for that listener name
- failures that prevent the entire engine group from starting or remaining operational are reported once per logical listener in that group

This preserves the existing listener-scoped callback contract without inventing a new engine-scoped callback API.

Connection-scoped failures continue to flow through the existing session lifecycle:

- handler errors
- protocol decode errors
- transport read or write errors after open
- WebSocket protocol violations after the connection is associated with a session

The `gnet` transport must guarantee `OnClose` is delivered at most once per connection.

## Default Behavior Change

Built-in presets change from:

- `binding.TCPWKProto(...) -> Transport: "stdnet"`
- `binding.WSJSONRPC(...) -> Transport: "stdnet"`

to:

- `binding.TCPWKProto(...) -> Transport: "gnet"`
- `binding.WSJSONRPC(...) -> Transport: "gnet"`

`stdnet` remains fully supported when explicitly configured in `ListenerOptions.Transport`.

## Testing Strategy

### Transport API and Core

- add tests for grouped `Factory.Build(...)` behavior
- update core server tests to cover grouped transport creation and startup rollback
- add option-validation tests for duplicate listener addresses and `ErrListenerAddressDuplicate`
- adapt `internal/gateway/testkit/fake_transport.go` to the grouped factory API used by core tests
- ensure current `stdnet` tests still pass through the adapted factory API

### `gnet` TCP

- listener starts and accepts traffic
- multiple logical TCP listeners can share one engine group
- stopping one logical listener does not prematurely stop a shared engine group
- the last logical listener stop shuts the engine group down

### `gnet` WebSocket

- successful HTTP upgrade
- path mismatch rejection
- text and binary message delivery
- ping/pong handling
- close-frame handling
- fragmented inbound messages
- multiple logical WebSocket listeners route by bound address correctly

### End-to-End Gateway

- built-in TCP preset works without explicitly naming a transport and now uses `gnet`
- built-in WebSocket preset works without explicitly naming a transport and now uses `gnet`
- explicitly configured `stdnet` listeners still work

## Risks and Mitigations

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Blocking gateway callbacks stall `gnet` | Would erase the benefit of `gnet` and can freeze event loops | Use per-connection workers and keep event-loop work minimal |
| Read-buffer lifetime bugs | `gnet` buffers are unsafe to share asynchronously | Copy fully decoded payloads before enqueueing worker events |
| WebSocket framing complexity | Incorrect frame parsing will break interoperability | Isolate handshake and frame logic into dedicated files with focused tests |
| Shared-engine lifecycle bugs | One logical listener could accidentally stop the group for others | Use explicit group reference counting and start/stop tests |
| Transport API change regresses `stdnet` | Existing gateway functionality depends on it | Adapt `stdnet` to the new `Build` API and keep its tests green |

## Implementation Outline

1. Change transport factory interfaces to support grouped builds
2. Update `core.Server` startup to build listeners by transport group
3. Adapt `stdnet` to the new transport factory API without changing behavior
4. Implement `gnet` TCP transport with shared engine groups
5. Implement `gnet` WebSocket handshake and frame handling
6. Register both `stdnet` and `gnet` factories in `gateway.New`
7. Switch built-in presets to default to `gnet`
8. Add transport, gateway, and regression tests

## Success Criteria

- `gateway.New` registers both `stdnet` and `gnet`
- `binding.TCPWKProto` and `binding.WSJSONRPC` default to `gnet`
- multiple logical `gnet` listeners can share one underlying engine group
- `gnet` event-loop callbacks do not directly execute gateway handler logic
- TCP and WebSocket traffic both work through `gnet`
- explicit `stdnet` listeners still pass existing transport and end-to-end tests
