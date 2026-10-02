---
scope: subtree
summary: Provides reusable client listeners, protocol adapters, sessions, authentication, bounded dispatch, transport writes, and connection lifecycle.
---

# Gateway Flow

## Responsibility

`pkg/gateway` binds TCP/WebSocket listeners, adapts WKProto, JSON-RPC, multiplexed
frames and independent MQTT packets, owns sessions/authentication, dispatches to
injected handlers, serializes writes, and closes idle or overloaded connections.

`core` owns runtime state; `protocol` and `transport` define extension seams;
`session` owns session values/writes; `binding` provides listener presets.
Message, presence, Channel, and Controller business policy stays outside gateway.

## Boundaries

- Message, ACK, ping, presence, Channel, Slot, and Controller business behavior
  stays in `internal/access/gateway` and downstream usecases/runtimes.
- This subtree must not import `internal`. Protocol adapters own wire formats
  and protocol-local state; transports own bytes and connection lifecycle.
- A single-node cluster still follows cluster semantics. Gateway never adds
  a local business-write shortcut.
- Handler contexts and session value keys are narrow public contracts; changes
  require matching access-adapter and lifecycle tests.

## Main Flows

1. Startup validates and builds listeners, protocols, transports, bounded
   auth/SEND runtimes, and idle tracking; connection open applies drain
   admission, creates Session state, and decodes bounded inbound protocol data.
   All listeners detect direct traffic or PROXY v1/v2 by default within a
   five-second deadline and 4096-byte v2/107-byte v1 header caps. Nonempty trusted
   CIDRs restrict header sources; an empty list accepts unverified peer assertions.
   Detection precedes TCP Session creation and WebSocket HTTP Upgrade; accepted
   source addresses are immutable before callbacks; `gateway.peer_addr` retains physical peers.
2. WKProto and JSON-RPC CONNECT authenticate and activate off the transport
   loop, write a protocol-correlated CONNACK, then open the callback gate;
   authenticated SEND uses bounded session-sharded batching; other frames dispatch
   synchronously. Protocol-aware Session writes serialize output. WSMux delegates
   CONNECT requirements to its selected nested protocol.
   The terminal sealer shares that write lock, closes ordinary outbound admission,
   and enqueues its unique marker ACK before later inbound frames reach the handler.
   Independent packet adapters reuse the auth pool and ordered SEND mailbox,
   including control packets, with a shared queued/executing byte budget. When WK
   deferred SEND is enabled, the same shard worker separates packets from WK
   preparation and joins packet callbacks before releasing bytes/admission. Their
   accepted activation transfers cleanup to open/close callbacks only after the
   reply is enqueued; earlier failure invokes the returned rollback once.
   Optional CheckReply revalidates accepted activation immediately before enqueue;
   rejection or panic skips the reply and rolls back without opening the session.
3. Close cancels request work, removes indexes, releases protocol and transport
   state, and orders error/close callbacks after open completion; drain rejects
   only new sessions and reports existing session state for safety checks.

## Invariants and Failure Semantics

- During auth pending, any additional frame is a protocol violation. CONNECT
  must be the sole first decoded frame, and successful activation is rolled back
  if CONNACK cannot be written before close.
- `OnSessionOpen` happens at most once and before `OnFrame` or `OnPacket`; `OnSessionClose`
  and relevant errors happen only after an in-progress open callback returns.
- Inbound bytes, outbound bytes, auth queue, SEND backlog, per-shard mailboxes,
  batch records/bytes/wait, idle work, actor work, and shutdown waits are
  bounded. Each SEND shard admits at least one record batch, unless the global
  queue is smaller. Increasing workers cannot reduce that burst capacity.
  Saturation closes only the affected session with a typed reason.
- `DrainSends` is a one-shot SEND-admission fence that waits for accepted
  mailbox work without canceling or resetting it when a caller times out.
  It does not prove append, delivery, transport flush, or client receipt.
- Async SEND owns retained payload bytes unless the protocol explicitly proves
  decoded-frame ownership. Result order within a session is preserved.
- Optional deferred batch handlers join preparation, then core retains the
  original global/shard reservations through handler completion and ordered
  publication across batches. Completion errors join before drain can finish.
  Session chains keep only admitted records and never hold a shard mutex while
  writing. Existing joined handlers retain their dispatch behavior.
- Only inbound activity refreshes idle deadlines. Drain preserves existing sessions.
  MQTT refreshes only on complete packets, negotiates 1.5 times Keep Alive, and
  disables that deadline when Keep Alive is zero. One shared heap/monitor owns
  these deadlines, including when the default idle timeout is disabled.
- MQTT decoding owns payload bytes, caps coalesced batches at 128 packets and
  grows fragmented input amortized; independent inbound/outbound codec limits preserve the peer packet cap.
  Independent packet callbacks recover panics with fixed, redacted diagnostics.
  One constant-size receipt preserves decoded DISCONNECT reason/expiry/time across
  EOF before dispatch. Entry validates it; no strings or payloads are retained.
- Session writes serialize encode and close interaction; business code must not
  write directly to the transport connection.
  Sealed ACK enqueue failure never reopens ordinary writes, and remote proof
  still requires the client's exact decoded ACK.
- Physical isolation uses optional CloseTransportAndWait, fencing new I/O and
  joining gnet CloseWithCallback; OnClose/enqueue are not completion proof.
  Cancellation bounds waiting; one receipt persists independently of cleanup.
- Observations remain low-cardinality and never add per-connection identities.
  Pressure snapshots carry monotonic revisions so delayed callbacks cannot
  overwrite terminal zero or resurrect a cleared connection source.

## Read First

- [Options](types/options.go), [Core](core/server.go), [Async SEND](core/async_send.go)
- [Packet path](core/packet_protocol.go), [Transport](transport/transport.go)

## Update Triggers

Update when ownership, auth/activation, lifecycle, dispatch bounds or transport semantics change.
