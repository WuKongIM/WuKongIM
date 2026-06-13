# pkg/client Design

## Purpose

`pkg/client` is a tooling-grade WuKongIM WKProto TCP client library for
`wkbench`, e2e tests, and server-side Go tools. It should make high-throughput
SEND workloads easy to drive while keeping protocol behavior explicit and easy
to assert in tests.

This package is not a business SDK. It does not prepare users, tokens,
channels, or subscribers through HTTP APIs. It does not own automatic business
retry, reconnect compensation, or channel/person/group workload models.

## Existing Context

The current repository has two useful client implementations:

- `internal/bench/wkproto` provides a production-like benchmark TCP client with
  WKProto handshake, session encryption, partial-frame buffering, payload
  detaching, and a background reader.
- `test/e2e/suite/WKProtoClient` provides a smaller black-box test client.

The benchmark workload adds another matching layer on top of the bench client
to correlate SENDACK frames by `ClientSeq` and `ClientMsgNo`, buffer unmatched
frames, and optionally auto-ack RECV frames. That matching logic belongs in the
shared client library so callers do not compete for reads from the same TCP
connection.

On the server side, `pkg/gateway` already batches adjacent SEND frames inside
the async SEND runtime. WKProto does not need a new SENDBATCH protocol frame for
the first version. Client-side batching should encode multiple normal SEND
frames and write them contiguously to the TCP stream so gateway shards can
collect them efficiently.

## Goals

- Provide a reusable WKProto TCP client for repository tools and tests.
- Support high-throughput SEND workloads through bounded queues, one writer
  pump, batch flushing, and in-flight limiting.
- Keep SENDACK matching internal and deterministic.
- Keep RECV draining and optional RECVACK behavior compatible with SENDACK
  waiting.
- Reuse existing `pkg/protocol/codec`, `pkg/protocol/frame`, and
  `pkg/protocol/wkprotoenc`.
- Make test failures diagnosable with compact connection-local counters and
  recent-frame summaries.

## Non-Goals

- No public end-user SDK surface in the first version.
- No HTTP bench API client.
- No user/channel/subscriber preparation logic.
- No automatic reconnect with message replay.
- No new WKProto batch frame.
- No import of `internal/*` packages.

## Package Layout

```text
pkg/client/
  client.go        Client lifecycle and public methods.
  options.go       Config defaults and validation.
  message.go       Message, SendResult, Identity, and small DTOs.
  writer.go        Bounded send queue, single writer pump, batch flush.
  reader.go        Single reader loop, partial frame buffer, frame routing.
  pending.go       SENDACK future map and timeout/close fanout.
  crypto.go        CONNECT key setup and SEND/RECV payload encryption helpers.
  pool.go          Optional multi-client pool for tooling workloads.
  observer.go      Low-cardinality tooling observations.
  errors.go        Stable package errors and SendError.
  FLOW.md          Package flow and ownership notes.
```

The package root should be the public API. Internal helpers can remain
unexported files in the same package unless a subpackage becomes necessary.

## Public API

### Client

`Client` owns one authenticated WKProto TCP session.

```go
type Client struct { ... }

func New(cfg Config) (*Client, error)
func (c *Client) Connect(ctx context.Context, opts ConnectOptions) (*frame.ConnackPacket, error)
func (c *Client) Send(ctx context.Context, msg Message) (SendResult, error)
func (c *Client) SendBatch(ctx context.Context, msgs []Message) ([]SendResult, error)
func (c *Client) SendAsync(ctx context.Context, msg Message) (*SendFuture, error)
func (c *Client) ReadFrame(ctx context.Context) (frame.Frame, error)
func (c *Client) Recv(ctx context.Context) (*frame.RecvPacket, error)
func (c *Client) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error
func (c *Client) Ping(ctx context.Context) error
func (c *Client) Close() error
```

`Send` is a convenience wrapper over `SendBatch` with one item. `SendAsync`
enqueues one SEND and returns a future that resolves when the matching SENDACK
arrives. `SendBatch` returns a result slice aligned with the input slice even
when SENDACK frames arrive out of order.

`ReadFrame` is a tooling compatibility method for existing wkbench and e2e
helpers. It returns non-SENDACK inbound frames from the reader-owned queue;
SENDACK frames remain owned by the pending tracker.

### Config

```go
type Config struct {
    Addr string
    Token string
    Dialer Dialer

    OperationTimeout time.Duration
    AckTimeout time.Duration

    SendQueueCapacity int
    MaxInflight int

    BatchMaxRecords int
    BatchMaxBytes int
    BatchMaxWait time.Duration

    ReadBufferSize int
    InboundFrameBufferSize int
    AutoRecvAck bool
    GenerateClientMsgNo bool

    Observer Observer
}
```

Default batch values should mirror gateway defaults unless the caller
overrides them:

```text
BatchMaxRecords = 512
BatchMaxBytes   = 512 * 1024
BatchMaxWait    = 1ms
```

`OperationTimeout` bounds connect, non-SEND writes, and reads when the caller
does not provide a deadline. `AckTimeout` bounds SENDACK wait after a SEND has
been admitted. `MaxInflight` applies per connection and should default to a
bounded but high value suitable for tooling workloads.

### Message

```go
type Message struct {
    Setting frame.Setting
    Expire uint32
    ClientSeq uint64
    ClientMsgNo string
    ChannelID string
    ChannelType uint8
    Topic string
    Payload []byte
}
```

When `ClientSeq` is zero, the client assigns the next connection-local
sequence. When `ClientMsgNo` is empty, the client only generates one when
`Config.GenerateClientMsgNo` is true. The default is false because wkbench
usually supplies deterministic message numbers for diagnosis.

The client must reject payloads larger than `codec.PayloadMaxSize`.

### SendResult

```go
type SendResult struct {
    ClientSeq uint64
    ClientMsgNo string
    MessageID int64
    MessageSeq uint64
    ReasonCode frame.ReasonCode
}
```

`ReasonCode == frame.ReasonSuccess` means the server accepted the send. A
non-success reason is returned as an item-level `SendError` while still
preserving the result fields.

## Connection Flow

```text
New
  -> validate Config
  -> create codec and X25519 client keypair

Connect
  -> dial tcp
  -> write CONNECT with UID, device id, token, protocol version, client key
  -> read CONNACK synchronously
  -> fail if reason is not success
  -> derive session crypto when CONNACK has server key and salt
  -> start reader loop
  -> start writer loop
```

The reader loop starts only after a successful handshake, so connect failures
stay simple and deterministic.

## High-Throughput SEND Flow

```text
SendBatch / SendAsync
  -> validate each message
  -> assign ClientSeq when needed
  -> create pending future before queue admission
  -> enqueue bounded send request
  -> writer pump collects adjacent queued requests
  -> writer encodes encrypted SEND frames into one contiguous byte buffer
  -> writer performs one full socket write for the collected batch
  -> reader loop receives SENDACK frames
  -> pending tracker resolves matching futures by ClientSeq + ClientMsgNo
  -> SendBatch returns results in original input order
```

Only the writer pump writes SEND frames to the socket. This avoids per-caller
write lock contention and makes batching predictable. `Ping` and `RecvAck`
also use the writer pump as control requests so no frame write can interleave
with another frame.

The batch collector uses three flush triggers:

- `BatchMaxRecords` caps frame count.
- `BatchMaxBytes` caps encoded or payload bytes used by the write buffer.
- `BatchMaxWait` bounds the time spent waiting for nearby queued requests.

If the send queue is full, the item fails with `ErrSendQueueFull`. If
`MaxInflight` is full, the enqueue path waits for room until the caller context
expires, then returns the context error.

## SENDACK Matching

The client owns a `pending` map keyed by `(ClientSeq, ClientMsgNo)`.

Rules:

- A SENDACK with both fields set must match both fields.
- If the original message has an empty `ClientMsgNo`, matching by `ClientSeq`
  is allowed.
- Unmatched SENDACK frames are counted and retained only in a bounded debug
  ring, not exposed as normal inbound messages.
- Closing the client resolves all pending futures with `ErrClosed`.
- Ack timeout resolves only the affected future.

`ClientSeq` is encoded by the protocol codec as `uint32`. The client must not
silently wrap. When the next generated sequence would exceed `math.MaxUint32`,
new sends fail with `ErrClientSeqExhausted`; tooling can reconnect and continue
with a new session.

## RECV Handling

The reader loop also handles server RECV frames. It decrypts encrypted payloads,
detaches payload bytes from the shared read buffer, and then either:

- sends `RecvAck` automatically when `AutoRecvAck` is enabled, and
- buffers the RECV in a bounded inbound queue for `Recv(ctx)`.

When the inbound queue is full, the default should favor SEND stability:
increment a dropped-RECV counter and drop the oldest buffered RECV. This
behavior fits benchmark traffic where SENDACK latency is often
the primary measurement. Tests that need exact receive verification can use a
larger queue or disable drop-prone scenarios.

## Pool

`Pool` is an optional helper for tooling workloads that manage many online
sessions.

```go
type Identity struct {
    UID string
    DeviceID string
    Token string
}

type PoolConfig struct {
    Addrs []string
    Balance string
    Client Config
    ConnectRatePerSecond int
}
```

The first version should support round-robin gateway address assignment and
UID lookup. Hash-based routing can be added later if a workload needs stable
distribution across gateway addresses.

`Pool.SendBatch` accepts routed messages that include the sender UID, groups
items by owning client, calls each client's `SendBatch`, and reassembles
results in the original order.

## Observer

Observer events should be low-cardinality and optional:

```go
type Observer interface {
    OnConnect(event ConnectEvent)
    OnSendQueue(event SendQueueEvent)
    OnSendBatch(event SendBatchEvent)
    OnSendAck(event SendAckEvent)
    OnRecv(event RecvEvent)
    OnError(event ErrorEvent)
}
```

Events should avoid per-channel labels by default. They can include counts,
durations, queue depth, batch records, batch bytes, reason code, and broad
error class. This makes the client useful for wkbench without importing
`internal/bench/metrics`.

## Error Model

Batch-level errors indicate the whole call could not be completed:

- nil or closed client
- not connected
- context canceled before admission
- connection write failure affecting the batch

Item-level errors indicate one SEND failed while preserving input alignment:

- `ErrPayloadTooLarge`
- `ErrSendQueueFull`
- `ErrClientSeqExhausted`
- `ErrAckTimeout`
- `SendError{ReasonCode: ...}` for non-success SENDACK

The implementation should keep stable sentinel errors and wrap them with enough
connection-local debug context to diagnose benchmark failures.

## Testing Strategy

Unit tests should use `net.Pipe` or a small fake dialer/server. They should be
fast and deterministic.

Required tests:

- successful and failed CONNECT/CONNACK handshake
- encrypted SEND payload and decrypted RECV payload round trip
- partial frame and concatenated frame decoding
- `SendBatch` preserves input result order under out-of-order SENDACKs
- queue full and in-flight full behavior
- ack timeout resolves only the affected future
- close resolves all pending futures
- writer flushes by max records, max bytes, and max wait
- unmatched SENDACK frames do not block normal sends
- AutoRecvAck does not steal SENDACK matching
- generated `ClientSeq` refuses `uint32` overflow

Integration or e2e migration tests can later replace the private e2e client
and selected wkbench client paths with `pkg/client`.

## Migration Plan

1. Add `pkg/client` with the single-connection client, writer pump, reader
   loop, pending tracker, crypto helpers, and unit tests.
2. Add `Pool` after the single-connection path is stable.
3. Switch `internal/bench/wkproto` to wrap or re-export `pkg/client`, keeping
   wkbench behavior unchanged.
4. Switch e2e WKProto helpers to `pkg/client`.
5. Remove duplicated matcher and auto-ack logic from benchmark workload code
   only after equivalent behavior is covered by tests.
