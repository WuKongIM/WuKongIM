# pkg/client Flow

`pkg/client` is a tooling-grade WKProto TCP client for wkbench, e2e tests, and server-side Go tools.

It owns protocol connection behavior only: CONNECT/CONNACK, optional session encryption, SEND/SENDACK, RECV/RECVACK, PING/PONG, one writer pump, one reader loop, and optional pooling. It does not prepare users, channels, subscribers, or tokens.

SEND batching writes multiple normal WKProto SEND frames contiguously on one TCP stream. No SENDBATCH frame is introduced.

## Connection Lifecycle

```text
New(Config)
  -> normalize defaults
  -> generate client keypair
  -> Connect(ConnectOptions)
  -> dial TCP
  -> write CONNECT
  -> read CONNACK
  -> derive optional session crypto
  -> publish active connection
  -> start writer loop once
  -> start reader loop for this connection
  -> Close
```

`Client` represents one authenticated WKProto TCP session. Reconnect is allowed by calling `Connect` again; the new connection gets a fresh pending tracker and reader loop, and the old connection is closed after the new session is published.

Synchronous CONNECT reads and writes use `OperationTimeout` and clear socket deadlines before the background reader takes over. `Close` is terminal for a `Client`; use a new `Client` or `Pool` entry after a terminal close.
`ReadFrame`, `ReadFrameWithLease`, and `Recv` are streaming waits: they have no implicit
`OperationTimeout` and return only for a frame, session transition, close, or
their caller context. Short control operations retain the configured timeout.
EOF with an incomplete buffered frame is a typed unexpected-EOF failure; it is
never accepted as a clean session boundary.

The legacy no-grant `SealIngress` seam remains fail-closed and returns
`ErrIngressSealUnsupported` without changing the live TCP session.
`SealIngressWithFence` requires a target-published `TerminalFenceGrant`. Under
the same admission lock as SEND/PING it permanently enters terminal quiescing,
then waits for every previously admitted SEND to receive a decoded SENDACK. A
missing, timed-out, or locally failed SEND fails the cut before the marker is
written; a decoded non-success SENDACK remains visible to the workload's
existing correctness verdict.
It writes one strictly bounded `_wk.bench.terminal_fence.v1` EVENT containing
only version, epoch, opaque capability, and a random 128-bit nonce, and succeeds
only when the reader decodes the exact
`_wk.bench.terminal_fence_ack.v1` epoch/nonce pair. Capability and nonce are not
logged or used as metric labels. SEND, PING, and reconnect reject promptly once
quiescing begins; RECVACK remains allowed. EOF before the ACK, a malformed or
stale ACK, or any current-session frame decoded after the ACK is a fail-closed
protocol violation. A decoded ACK first enters an internal observed state; it is
published as success only after the reader consumes the current socket-read
batch with no trailing bytes. Thus a complete or partial post-ACK frame already
coalesced with that ACK cannot race a false successful return. TCP half-close,
a local write callback, or transport-buffer state is never accepted as remote
proof.

## SEND Flow

```text
Send / SendAsync / SendBatch
  -> validate and assign ClientSeq
  -> build SendPacket
  -> reserve MaxInflight slot
  -> add pending SENDACK entry
  -> enqueue writer request
  -> writer batches nearby SEND frames
  -> encrypt each SEND when session crypto is active
  -> write contiguous WKProto SEND frames
  -> reader receives SENDACK
  -> pending tracker resolves SendFuture
```

`SendBatch` returns results in input order. The writer batcher only coalesces socket writes; the wire format remains normal WKProto SEND frames. `AckTimeout` belongs to the client pending tracker and should be set high enough for callers whose own contexts own benchmark-level sendack deadlines.

`SendAsync` is the low-level API used by adapters that need to expose SENDACKs through an older frame-oriented interface. It admits the SEND and returns a `SendFuture`; callers can wait with their own context. `TrySendAsync` provides the same future only when the admission lock, writer queue, and inflight bound are immediately available. It returns `ErrSendQueueFull` without leaving a pending SEND when local capacity is busy, allowing deterministic runtimes to retry without blocking their owner loop.
The pending tracker keys each wire attempt by both `ClientSeq` and
`ClientMsgNo`. Retries may reuse the idempotent `ClientMsgNo`, but each
overlapping attempt must provide a distinct nonzero `ClientSeq`; late and
out-of-order ACKs then resolve only their exact attempt.

## RECV Flow

```text
reader loop
  -> decode buffered bytes into frames
  -> SENDACK resolves pending send
  -> RECV checks DiscardInboundRecv
       -> yes: optionally AutoRecvAck and continue reading
       -> no: decrypt payload when session crypto is active
              -> enqueue decrypted RECV in bounded queue
              -> optionally AutoRecvAck
              -> Recv / ReadFrame consumes queue
```

The inbound RECV queue is bounded and lossless. When it is full, the reader
backpressures the socket until a consumer frees capacity. Client close or
session replacement releases a blocked publisher, so bounded delivery cannot
strand an old reader loop. `InboundQueueSnapshot` exposes only bounded numeric
queue depth, capacity, and handoff ownership. `ReadFrameWithLease` keeps one
dequeued RECV observable in that handoff count until the next processing stage
accepts it and releases the idempotent lease; ordinary `ReadFrame` releases the
lease before returning for compatibility.
Send-only tools that do not consume delivered payloads may set
`DiscardInboundRecv`; RECV frames then bypass payload decryption and the queue,
so they cannot block SENDACK progress or retain fanout payloads in memory.

## Control Flow

`Ping` and `RecvAck` share the writer loop with SENDs so control frames and SEND frames are serialized on the same TCP stream. Control writes use `OperationTimeout` or the caller's shorter context deadline. The terminal marker is admitted under the same SEND/PING ordering lock; RECVACK intentionally remains an allowed independent control write while the remote ACK is pending.

## Pool Flow

```text
NewPool(PoolConfig)
  -> validate gateway addresses
  -> Connect([]Identity)
  -> create one Client per identity
  -> assign addresses round-robin
  -> connect at optional rate limit
  -> Send / SendBatch route by UID
  -> Close closes every Client
```

`Pool` is a thin orchestration layer for tools that need many online identities. It does not retry failed sends or rebalance identities after connection; callers own benchmark or e2e policy decisions above the pool.

## Adapter Notes

`internal/bench/wkproto` wraps `pkg/client` to preserve the historical
`ReadFrame` API. It converts `SendFuture` results back into local
`SendackPacket` frames and forwards decrypted RECV packets. Separate bounded
RECV, SENDACK, and error queues isolate acknowledgement progress without
discarding receive evidence. Error and SENDACK results share a fixed priority
burst quota so neither errors nor an already queued RECV can starve.

`test/e2e/suite` uses the same package for CONNECT, crypto, SENDACK matching, and RECV decryption while keeping black-box helper methods outside the client package.
