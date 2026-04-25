# Gateway Module Design

## Overview

Add a new `internal/gateway` module that accepts frontend connections over TCP or WebSocket, decodes protocol-specific traffic into a unified `wkpacket.Frame` model, and encodes outbound `wkpacket.Frame` values back to the client protocol.

The gateway is intentionally narrow in scope:

- Own connection lifecycle and session state
- Own transport-specific accept/read/write behavior
- Own protocol-specific encode/decode behavior
- Expose a thin callback interface to upstream logic
- Do not contain business logic, routing logic, or application state machines

The first supported bindings are:

- `TCP -> wkproto`
- `WebSocket -> jsonrpc`

The design must preserve the ability to add:

- New protocols
- New transports
- New networking frameworks such as `gnet`
- Future transport/protocol combinations without refactoring the gateway core

## Goals

- Provide a clean internal gateway boundary under `internal/gateway`
- Expose and consume a unified `wkpacket.Frame` model
- Keep transport, protocol, and lifecycle concerns isolated
- Make the core testable without real sockets
- Allow replacing the underlying network framework without rewriting gateway core logic

## Non-Goals

- Business authentication, authorization, routing, or message processing
- Command dispatch or service orchestration
- Cross-session fanout or broker semantics
- Automatic protocol detection on a single listener
- A generic middleware framework

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Unified application model | `wkpacket.Frame` | `pkg/wkproto` and `pkg/jsonrpc` already converge naturally on `wkpacket` objects, which keeps upstream logic protocol-agnostic |
| Core structure | Layered plugin-style gateway | Separates transport, protocol, session, and lifecycle orchestration for extensibility and testability |
| Initial listener binding | Explicit configuration | Avoids protocol auto-detection complexity and prevents transport/protocol coupling in the core |
| Initial transport implementation | `stdnet` first, `gnet` second | Establishes stable abstractions before adapting to a specific event-loop model |
| Upstream interaction | Thin callback interface | Fits the "gateway owns I/O, not logic" requirement while remaining easy to test |
| Backpressure policy | Fail fast when write queue is full | Keeps first version deterministic and avoids hidden data loss policies |

## Package Structure

```text
internal/gateway/
  gateway.go            // Public entrypoint: New/Start/Stop
  options.go            // Top-level gateway and listener options
  event.go              // Upstream handler, event context, and outbound metadata
  errors.go             // Shared gateway errors and close reasons

  core/
    server.go           // Listener/session orchestration
    registry.go         // Transport/protocol registration and lookup
    dispatcher.go       // Thin bridge to upstream handler callbacks

  session/
    session.go          // Session interface and implementation
    manager.go          // Session indexing and lifecycle tracking

  transport/
    transport.go        // Transport abstractions
    listener.go         // Common listener option types
    stdnet/             // Initial transport implementation
    gnet/               // Future transport implementation

  protocol/
    protocol.go         // Protocol adapter abstraction
    wkproto/
      adapter.go
    jsonrpc/
      adapter.go

  binding/
    binding.go          // Built-in listener presets and registration helpers
    builtin.go          // First-version convenience presets

  testkit/
    fake_transport.go
    fake_protocol.go
    fake_session.go
```

## Core Interfaces

### Upstream Handler

The gateway exposes a thin callback interface to the layer above it.

```go
package gateway

type Handler interface {
    OnListenerError(listener string, err error)
    OnSessionOpen(ctx *Context) error
    OnFrame(ctx *Context, frame wkpacket.Frame) error
    OnSessionClose(ctx *Context) error
    OnSessionError(ctx *Context, err error)
}
```

Design notes:

- `OnListenerError` handles failures that happen before a session exists, such as listener startup, accept, or WebSocket upgrade failures
- Upstream code sees only lifecycle events and decoded `wkpacket.Frame` values
- Returning an error from `OnFrame` or `OnSessionOpen` is treated as a handler error
- `OnSessionError` is notification-only and does not return an error

### Event Context

The gateway passes an immutable per-callback context snapshot to every upstream callback.

```go
package gateway

type Context struct {
    Session     session.Session
    Listener    string
    Network     string
    Transport   string
    Protocol    string
    CloseReason CloseReason
    ReplyToken  string
}
```

Design notes:

- `Context` is the carrier for session identity and listener metadata
- Each callback receives its own immutable context snapshot; the gateway does not mutate and reuse one shared instance across lifecycle events
- `CloseReason` is empty for open and frame callbacks and set only for close/error paths that terminate the session
- `ReplyToken` is empty for events that are not associated with a request/response protocol interaction
- For JSON-RPC request handling, `ReplyToken` is the inbound request ID
- `Context` must be safe to retain for asynchronous response flows, as long as the session remains open

### Session Abstraction

The session is the only connection handle visible to the upstream layer.

```go
package session

type Session interface {
    ID() uint64
    Listener() string
    RemoteAddr() string
    LocalAddr() string

    WriteFrame(frame wkpacket.Frame, opts ...WriteOption) error
    Close() error

    SetValue(key string, value any)
    Value(key string) any
}
```

Design notes:

- Upstream logic writes `wkpacket.Frame` values and never deals with raw bytes
- The session hides transport-specific connection types such as `net.Conn` or `gnet.Conn`
- The session key/value store is intentionally small and only meant for connection-local metadata
- `WriteOption` is the only way to attach outbound protocol metadata without leaking protocol-specific payload shapes into business logic

### Outbound Metadata

The gateway keeps `wkpacket.Frame` as the unified payload model, but outbound writes may need protocol metadata.

```go
package session

type WriteOption interface {
    apply(*OutboundMeta)
}

type OutboundMeta struct {
    ReplyToken string
}
```

Design notes:

- `ReplyToken` is an opaque protocol correlation token
- Most protocols and push-style writes do not use it
- JSON-RPC responses use it to carry the request ID that the adapter must echo in the encoded response
- The gateway should provide a helper such as `WithReplyToken(token string)` and a convenience helper on `Context` so the common reply path is explicit and easy to test

### Protocol Adapter

Protocol adapters convert raw bytes to and from `wkpacket.Frame`.

```go
package protocol

type Adapter interface {
    Name() string

    Decode(session session.Session, in []byte) (frames []wkpacket.Frame, consumed int, err error)
    Encode(session session.Session, frame wkpacket.Frame, meta session.OutboundMeta) ([]byte, error)

    OnOpen(session session.Session) error
    OnClose(session session.Session) error
}
```

Design notes:

- `Decode` may emit multiple frames from a single input buffer
- `Decode` returns `consumed` bytes so the core can maintain a persistent per-session read buffer
- `OnOpen` and `OnClose` are optional protocol lifecycle hooks for protocol-level policy
- Protocol adapters do not know or depend on the underlying network framework
- `Encode` receives outbound metadata so protocols such as JSON-RPC can safely correlate responses under concurrency

### Transport Abstraction

Transport adapters own connection acceptance and raw byte delivery.

```go
package transport

type Factory interface {
    Name() string
    New(opts ListenerOptions, handler ConnHandler) (Listener, error)
}

type Listener interface {
    Start() error
    Stop() error
    Addr() string
}

type Conn interface {
    ID() uint64
    Write([]byte) error
    Close() error
    LocalAddr() string
    RemoteAddr() string
}

type ConnHandler interface {
    OnOpen(conn Conn) error
    OnData(conn Conn, data []byte) error
    OnClose(conn Conn, err error)
}
```

Design notes:

- The gateway core depends only on these abstractions, never on `gnet`
- `transport.Conn` exposes only the minimal write/close/address surface
- `OnData` delivers raw bytes or whole transport messages, depending on the transport type

## Configuration and Built-In Presets

Listener transport and protocol selection is explicit in configuration. The core does not infer protocol from data.

```go
type ListenerOptions struct {
    Name      string
    Network   string // "tcp" or "websocket"
    Address   string
    Transport string // "stdnet", "gnet", ...
    Protocol  string // "wkproto", "jsonrpc", ...
}
```

Rules:

- `Name` is the canonical listener identity inside the gateway
- `Name` must be unique across all configured listeners
- `Network`, `Transport`, and `Protocol` in `ListenerOptions` are the single source of truth for listener behavior
- `Context.Listener` is always populated with this same `Name`

The optional `binding/` package does not introduce a second configuration model. It only provides convenience helpers for constructing `ListenerOptions` with known-good first-version combinations.

Initial built-in presets:

- `tcp-wkproto`: `tcp + wkproto`
- `ws-jsonrpc`: `websocket + jsonrpc`

This keeps the first implementation simple while preserving the ability to support future combinations such as:

- `websocket + wkproto`
- `tcp + jsonrpc`
- Multiple listeners using the same protocol

## Runtime Flow

### Connection Open

1. A transport listener accepts a new connection
2. The transport invokes `ConnHandler.OnOpen`
3. The gateway core creates a `session.Session`
4. The core resolves the protocol adapter named in that listener's `ListenerOptions`
5. The protocol adapter's `OnOpen` hook runs
6. The upstream handler's `OnSessionOpen` callback runs

If any step fails, the session is closed and the error is reported through `OnSessionError`.

### Listener-Scoped Failures

Some failures happen before a session exists:

- listener startup failure
- accept loop failure
- WebSocket upgrade failure

These errors must be reported through `Handler.OnListenerError(listener, err)`, not `OnSessionError`.

### Inbound Data

1. The transport invokes `ConnHandler.OnData(conn, data)`
2. The core appends `data` to the session's inbound buffer
3. The core calls `protocol.Decode(session, buffer)`
4. The protocol adapter returns zero or more `wkpacket.Frame` values and a `consumed` byte count
5. The core removes `consumed` bytes from the inbound buffer
6. The core invokes `Handler.OnFrame` once per decoded frame

This supports:

- sticky packets on TCP
- partial frames on TCP
- whole-message delivery on WebSocket
- future protocol adapters that emit multiple frames from one payload

### Outbound Data

1. Upstream code calls `session.WriteFrame(frame, opts...)`
2. The session asks the protocol adapter to encode the frame
3. The encoded payload is pushed to the session's write queue
4. A dedicated writer loop serializes payload writes through `transport.Conn.Write`

This ensures:

- no concurrent writes against the underlying connection
- transport-specific write behavior remains isolated from the core
- backpressure can be enforced consistently
- request/response protocols can attach correlation metadata without changing the unified `wkpacket.Frame` model

### JSON-RPC Correlation Rule

JSON-RPC requires the gateway to preserve request IDs on responses.

Rules:

- When `protocol/jsonrpc` decodes an inbound request, it must attach the request ID to the callback `Context.ReplyToken`
- When upstream code wants to reply to that request, it must write through either:
  - `ctx.Session.WriteFrame(frame, session.WithReplyToken(ctx.ReplyToken))`, or
  - an equivalent `ctx.WriteFrame(frame)` convenience helper that copies the same token into outbound metadata
- When `protocol/jsonrpc` encodes an outbound frame with a non-empty `ReplyToken`, it must treat the output as a response and echo that token as the JSON-RPC `id`
- When `ReplyToken` is empty, the adapter must encode an outbound notification form where the protocol supports it

This rule keeps response correlation explicit, works with concurrent in-flight requests, and does not require gateway core logic to understand JSON-RPC payload structure.

### Connection Close

1. The transport detects local or remote closure, or the core initiates shutdown
2. The core marks the session as closing
3. The protocol adapter's `OnClose` hook runs
4. The core invokes `Handler.OnSessionClose`
5. The session manager removes the session from indexes

`OnSessionClose` is guaranteed to run at most once per session.

## Error Model

Errors are grouped into four categories:

- `transport error`
  - listener failure
  - accept failure
  - socket write failure
  - websocket upgrade failure
- `protocol error`
  - invalid payload
  - decode failure
  - encode failure
  - oversized frame
- `policy error`
  - protocol hook validation failures such as first-frame restrictions or connect timeouts
- `handler error`
  - upstream callback returned an error

Default behavior:

- pre-session transport errors are reported through `OnListenerError`
- session-scoped `transport`, `protocol`, and `policy` errors are reported through `OnSessionError`, then the session is closed
- `handler` errors are reported through `OnSessionError`
- `handler` errors close the session by default, behind a gateway option so that the behavior is explicit and testable

## Close Reasons

The gateway reports a concrete close reason to avoid ambiguous connection teardown.

```go
type CloseReason string

const (
    CloseReasonServerStop     CloseReason = "server_stop"
    CloseReasonPeerClosed     CloseReason = "peer_closed"
    CloseReasonProtocolError  CloseReason = "protocol_error"
    CloseReasonInboundOverflow CloseReason = "inbound_overflow"
    CloseReasonPolicyViolation CloseReason = "policy_violation"
    CloseReasonPolicyTimeout  CloseReason = "policy_timeout"
    CloseReasonWriteQueueFull CloseReason = "write_queue_full"
    CloseReasonOutboundOverflow CloseReason = "outbound_overflow"
    CloseReasonIdleTimeout    CloseReason = "idle_timeout"
    CloseReasonHandlerError   CloseReason = "handler_error"
)
```

Design notes:

- `OnSessionError` reports the error
- `OnSessionClose` reports the terminal lifecycle event
- The two callbacks serve different purposes and should not be merged

## Backpressure and Write Path

Each session owns a write queue and a writer loop.

```go
type SessionOptions struct {
    ReadBufferSize       int
    WriteQueueSize       int
    MaxInboundBytes      int
    MaxOutboundBytes     int
    IdleTimeout          time.Duration
    WriteTimeout         time.Duration
    CloseOnHandlerError  bool
}
```

First-version backpressure policy:

- If the write queue is full, return `ErrWriteQueueFull`
- Report the error through `OnSessionError`
- Close the session with `CloseReasonWriteQueueFull`

`MaxOutboundBytes` is the maximum total number of encoded bytes buffered in the per-session write queue but not yet written to the transport. It is enforced when enqueueing outbound payloads, not as a per-frame encoding limit.

Overflow behavior:

- If `queuedBytes + len(encodedPayload) > MaxOutboundBytes`, return `ErrOutboundOverflow`
- Report the error through `OnSessionError`
- Close the session with `CloseReasonOutboundOverflow`

This prevents one slow client from accumulating unbounded pending output even if the write queue still has free item slots.

This keeps the behavior deterministic and avoids hidden drop policies.

Inbound buffering policy:

- `ReadBufferSize` is the initial allocation hint for each session's inbound buffer
- `MaxInboundBytes` is the maximum number of undecoded bytes allowed in that buffer at any point in time
- The limit is enforced immediately after appending newly received transport data and before protocol decode continues

Inbound overflow behavior:

- If `len(inboundBuffer) > MaxInboundBytes`, return `ErrInboundOverflow`
- Report the error through `OnSessionError`
- Close the session with `CloseReasonInboundOverflow`

This prevents a client from forcing unbounded memory growth through partial frames or intentionally malformed streams.

## Protocol Policy Hooks

The gateway may enforce transport-safe and protocol-safe constraints, but not business logic.

The gateway itself owns:

- WebSocket upgrade handling
- TCP and WebSocket read boundaries
- maximum inbound frame size
- write timeout
- idle timeout

Protocol adapters may optionally own protocol policy hooks such as:

- first frame must be `connect`
- `jsonrpc` connect must arrive within two seconds

These hooks remain protocol-level concerns because they validate stream shape, not business meaning.

Policy close mapping:

- invalid protocol sequencing closes with `CloseReasonPolicyViolation`
- protocol-level connect timeout closes with `CloseReasonPolicyTimeout`

## Transport Responsibilities

### `transport/stdnet`

The first transport implementation should use the standard library and establish the baseline behavior of the gateway abstractions.

Responsibilities:

- TCP listening
- WebSocket listening and upgrade
- read loop and write loop integration with the core
- conversion of underlying connections into `transport.Conn`

### `transport/gnet`

The `gnet` transport should be implemented only after the core abstractions and `stdnet` behavior are stable.

Responsibilities:

- adapt `gnet` callbacks to `transport.ConnHandler`
- wrap `gnet.Conn` as `transport.Conn`
- preserve the same semantics as `stdnet`

Constraint:

- only `internal/gateway/transport/gnet` imports `gnet`
- `core`, `session`, and `protocol` must remain `gnet`-agnostic

## Protocol Responsibilities

### `protocol/wkproto`

Responsibilities:

- adapt `pkg/wkproto` encode/decode behavior to the gateway `protocol.Adapter` interface
- decode from byte streams into `wkpacket.Frame`
- encode `wkpacket.Frame` into `wkproto` wire bytes

### `protocol/jsonrpc`

Responsibilities:

- adapt `pkg/jsonrpc` encode/decode behavior to the gateway `protocol.Adapter` interface
- decode JSON-RPC requests and notifications into `wkpacket.Frame`
- encode outbound `wkpacket.Frame` values into JSON-RPC responses or notifications

Constraint:

- protocol adapters own protocol translation only
- they do not dispatch business handlers or maintain cross-session state

## Testing Strategy

### Protocol Unit Tests

Test the two protocol adapters directly:

- byte payload to `wkpacket.Frame`
- `wkpacket.Frame` to byte payload
- partial packet handling
- multiple frame decode behavior where applicable
- error behavior on malformed payloads

### Core Unit Tests

Test the core with fakes:

- use `testkit/fake_transport`
- use `testkit/fake_protocol`
- use in-memory sessions only

Coverage:

- open/data/close lifecycle
- multi-frame decode from one buffer
- partial reads
- handler error behavior
- queue-full close behavior
- close reason correctness

These tests should not require real sockets.

### Transport Integration Tests

Test each transport implementation independently:

- listener start and stop
- open/data/close callbacks
- real network round-trip for TCP
- real WebSocket handshake and message delivery

These tests verify transport behavior only and should not duplicate protocol adapter coverage.

## Implementation Sequence

1. Create `internal/gateway` public entrypoints, shared errors, and options
2. Implement session abstraction and session manager
3. Implement core listener/session orchestration
4. Implement transport and protocol registries
5. Implement `transport/stdnet`
6. Implement `protocol/wkproto`
7. Implement `protocol/jsonrpc`
8. Wire built-in listener presets: `tcp -> wkproto`, `websocket -> jsonrpc`
9. Add core and protocol tests
10. Add `stdnet` integration tests
11. Implement `transport/gnet` as a second phase after the baseline passes

## Risks and Mitigations

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Transport abstraction leaks framework details | Makes `gnet` replacement expensive and pollutes core logic | Keep `transport.Conn` intentionally minimal and ban `gnet` imports outside `transport/gnet` |
| Protocol adapters absorb business logic | Blurs module responsibility and makes tests brittle | Limit protocol hooks to stream-shape and protocol policy only |
| Write path races | Concurrent writes can corrupt outbound data or create transport-specific bugs | Single session writer loop and bounded write queue |
| Over-design for future combinations | Slows delivery and complicates the first version | Use explicit listener configuration and defer auto-detection and middleware systems |
| Too much reliance on integration tests | Makes failures slow and hard to localize | Push most logic into core and protocol unit tests using fakes |

## Recommended First Version Scope

Build exactly this first:

- `internal/gateway`
- one core server implementation
- one `stdnet` transport implementation
- one `wkproto` protocol adapter
- one `jsonrpc` protocol adapter
- explicit built-in listener presets for `tcp -> wkproto` and `websocket -> jsonrpc`
- unit tests for core and protocol layers
- transport integration tests for `stdnet`

Defer:

- `gnet`
- transport/protocol auto-detection
- generic middleware chains
- cross-session routing features

This keeps the first implementation aligned with the current requirement while preserving the extension points needed for later work.
