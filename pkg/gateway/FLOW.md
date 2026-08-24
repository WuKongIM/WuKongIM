---
scope: subtree
summary: Provides reusable client listeners, protocol adapters, sessions, authentication, bounded dispatch, transport writes, and connection lifecycle.
---

# Gateway Flow

## Responsibility

`pkg/gateway` is reusable client-entry infrastructure. It binds TCP and
WebSocket listeners, adapts WKProto, JSON-RPC, and multiplexed frames, owns
sessions and authentication handshakes, dispatches decoded frames to an
injected handler, serializes outbound protocol writes, and closes idle or
overloaded connections.

`core` owns runtime state; `protocol` and `transport` define extension seams;
`session` owns connection-facing session values and writes; `binding` provides
built-in listener presets.

## Boundaries

- Message, ACK, ping, presence, Channel, Slot, and Controller business behavior
  stays in `internal/access/gateway` and downstream usecases/runtimes.
- This subtree must not import `internal`. Protocol adapters understand wire
  formats and protocol-local state only; transports understand bytes and
  connection lifecycle only.
- A single-node deployment still follows cluster semantics. Gateway never adds
  a local business-write shortcut.
- Handler contexts and session value keys are narrow public contracts; changes
  require matching access-adapter and lifecycle tests.

## Main Flows

1. Startup validates and builds listeners, protocols, transports, bounded
   auth/SEND runtimes, and idle tracking; connection open applies drain
   admission, creates Session state, and decodes bounded inbound protocol data.
2. WKProto CONNECT authenticates and activates off the transport loop, writes
   CONNACK, then opens the callback gate; authenticated SEND uses bounded
   session-sharded batching, other frames dispatch synchronously, and all
   outbound frames serialize through protocol-aware Session writes.
   A terminal session sealer shares that write lock, permanently closes ordinary
   outbound admission, and enqueues the unique marker ACK before later inbound
   frames can reach the handler.
3. Close cancels request work, removes indexes, releases protocol and transport
   state, and orders error/close callbacks after open completion; drain rejects
   only new sessions and reports existing session state for safety checks.

## Invariants and Failure Semantics

- During auth pending, any additional frame is a protocol violation. CONNECT
  must be the sole first decoded frame, and successful activation is rolled back
  if CONNACK cannot be written before close.
- `OnSessionOpen` happens at most once and before `OnFrame`; `OnSessionClose`
  and relevant errors happen only after an in-progress open callback returns.
- Inbound bytes, outbound bytes, auth queue, SEND backlog, per-shard mailboxes,
  batch records/bytes/wait, idle work, actor work, and shutdown waits are
  bounded. Saturation closes only the affected session with a typed reason.
- `DrainSends` is a one-shot SEND-admission fence that waits for accepted
  mailbox work without canceling or resetting it when a caller times out.
  It does not prove append, delivery, transport flush, or client receipt.
- Async SEND owns retained payload bytes unless the protocol explicitly proves
  decoded-frame ownership. Result order within a session is preserved.
- Only inbound activity refreshes the idle deadline. Drain rejects new sessions
  and does not silently terminate existing ones.
- Session writes serialize encode and close interaction; business code must not
  write directly to the transport connection.
  Sealed ACK enqueue failure never reopens ordinary writes, and remote proof
  still requires the client's exact decoded ACK.
- Observations remain low-cardinality and never add per-connection identities.
  Absolute pressure snapshots carry monotonic source/publication revisions so
  delayed callbacks cannot overwrite terminal zero or resurrect a cleared
  connection source.

## Read First

- [Gateway facade](gateway.go)
- [Public options](types/options.go)
- [Core server](core/server.go)
- [Async SEND](core/async_send.go)
- [Transport contract](transport/transport.go)

## Update Triggers

Update this file when listener/protocol ownership changes, authentication or
activation ordering changes, session lifecycle changes, dispatch/backpressure
bounds change, payload ownership changes, or transport write/close semantics
change.
