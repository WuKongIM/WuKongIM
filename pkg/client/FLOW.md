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

`SendAsync` is the low-level API used by adapters that need to expose SENDACKs through an older frame-oriented interface. It admits the SEND and returns a `SendFuture`; callers can wait with their own context.

## RECV Flow

```text
reader loop
  -> decode buffered bytes into frames
  -> SENDACK resolves pending send
  -> RECV decrypts payload when session crypto is active
  -> optional AutoRecvAck writes RECVACK
  -> enqueue decrypted RECV in bounded queue
  -> Recv / ReadFrame consumes queue
```

The inbound RECV queue is bounded. When full, the oldest queued RECV is dropped and the newest RECV is retained, matching benchmark tooling needs where current delivery state is more useful than unbounded backlog.

## Control Flow

`Ping` and `RecvAck` share the writer loop with SENDs so control frames and SEND frames are serialized on the same TCP stream. Control writes use `OperationTimeout` or the caller's shorter context deadline.

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

`internal/bench/wkproto` wraps `pkg/client` to preserve the historical `ReadFrame` API. It converts `SendFuture` results back into local `SendackPacket` frames and forwards decrypted RECV packets. Its bounded adapter queue keeps SENDACK/error results ahead of RECV bursts so successful sends are not hidden by receive backlog.

`test/e2e/suite` and `test/legacy/e2e` use the same package for CONNECT, crypto, SENDACK matching, and RECV decryption while keeping their old helper methods.
