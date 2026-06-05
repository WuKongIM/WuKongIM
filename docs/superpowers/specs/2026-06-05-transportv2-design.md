# transportv2 Engine Design

## Context

`pkg/transport` is the current node-to-node TCP transport used by cluster,
controller, channel, and clusterv2 paths. It has useful foundations, including
framed TCP messages, a connection pool, priority queues, an RPC mux, observer
hooks, and targeted tests. Recent review found structural issues that are hard
to remove cleanly inside the existing package:

- Connection startup can expose partially initialized state to reader dispatch.
- `Pool.Close` is not a strict lifecycle boundary and can allow later re-dial.
- Outbound payload ownership is unclear because async writers hold caller-owned
  byte slices.
- Server-side RPC dispatch can create unbounded goroutines.
- Backpressure is queue-count based rather than byte based.
- Observer hooks run synchronously on I/O hot paths.
- Race and stress verification are not strong enough to guard future transport
  work by default.

The project is still in development, so `pkg/transportv2` can be a clean new
package without wire or API compatibility with `pkg/transport`.

## Goals

- Build a high-performance, readable, and testable node transport engine in
  `pkg/transportv2`.
- Keep the implementation Go-native first: TCP connections, bounded goroutines,
  explicit queues, and `net.Buffers` batching.
- Provide clear lifecycle semantics: after `Stop` or `Close`, no new dial,
  enqueue, or RPC may start.
- Make payload ownership explicit and safe by default.
- Make backpressure byte-aware, per-peer visible, and observable.
- Bound server-side RPC concurrency and queueing per service.
- Support priority traffic for Raft/control/data-plane flows without starving
  lower-priority lanes.
- Treat testkit, race tests, benchmarks, and opt-in stress tests as part of the
  package design, not afterthoughts.

## Non-Goals

- No compatibility with the current `pkg/transport` public API or 5-byte wire
  header.
- No QUIC or custom netpoll engine in the first implementation. Node-to-node
  connection counts are bounded by cluster size and pool size, so goroutine per
  connection remains simpler and easier to verify.
- No gateway/client connection handling. This package is only for trusted or
  authenticated node-to-node cluster transport.
- No durable retry queue. Failed sends and RPCs are surfaced to callers; higher
  layers own semantic retry.
- No broad cluster integration in the first slice. Integration begins with
  one narrow consumer after the package has its own correctness and stress
  coverage.

## Package Layout

```text
pkg/transportv2/
  client.go          Public Client API for Send, SendOwned, Call, Notify.
  server.go          Public Server API for listen, accept, register handlers.
  config.go          Config, limits, defaults, validation.
  errors.go          Typed sentinel errors and RemoteError.
  types.go           NodeID, Priority, MessageKind, stats, observer types.

  wire/
    frame.go         Fixed header encode/decode and frame validation.
    reader.go        Frame reader with body limits and buffer leasing.
    writer.go        Frame assembly for batched writes.

  internal/
    buffer/          Slab allocator and private release helpers.
    conn/            Connection actor: reader, writer, pending RPCs.
    engine/          Lifecycle root and shared config/observer ownership.
    observe/         Non-blocking counters, ring events, snapshots.
    peer/            NodeID peer state, dial, reconnect, ClosePeer.
    rpc/             Service registry, worker queues, pending table.
    sched/           Priority lanes, byte budgets, weighted scheduling.

  testkit/
    fault_conn.go    Slow read/write, partial write, close, corruption.
    harness.go       Server/client harness and peer restart helpers.
    stress.go        Shared mixed workload runner for tests/benchmarks.
```

Only `pkg/transportv2` and `pkg/transportv2/wire` are public. Everything under
`internal` can change without breaking callers.

## Public API Shape

The package exposes a small API centered on clients, servers, services, and
owned payloads.

```go
type NodeID uint64

type Priority uint8

const (
	PriorityRaft Priority = iota + 1
	PriorityControl
	PriorityRPC
	PriorityBulk
)

type Discovery interface {
	Resolve(nodeID NodeID) (addr string, err error)
}

type Client struct { /* owns peers and outbound conns */ }

type OwnedBuffer struct { /* payload plus release ownership */ }

func NewClient(cfg ClientConfig) (*Client, error)
func (c *Client) Send(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) error
func (c *Client) SendOwned(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload OwnedBuffer) error
func (c *Client) Notify(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) error
func (c *Client) Call(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) ([]byte, error)
func (c *Client) ClosePeer(nodeID NodeID)
func (c *Client) Stop()
func (c *Client) Stats() Stats

type Server struct { /* owns listener, accepted conns, service registry */ }

func NewServer(cfg ServerConfig) (*Server, error)
func (s *Server) Handle(serviceID uint16, handler Handler, opts ServiceOptions) error
func (s *Server) ListenAndServe(addr string) error
func (s *Server) Stop()
func (s *Server) Addr() string
func (s *Server) Stats() Stats
```

`Send` and `Call` copy caller-owned payloads before enqueueing. Hot paths that
already own a buffer use `SendOwned` to transfer ownership to the transport
without an additional copy.

## Wire Format

`transportv2` uses a fixed-size 24-byte big-endian header:

```text
magic      uint16  constant used to reject unrelated traffic
version    uint8   wire version, starts at 1
flags      uint8   reserved for compression/checksum/future controls
kind       uint8   data, notify, rpc_req, rpc_resp, control
priority   uint8   writer scheduling lane
serviceID  uint16  service or message route
requestID  uint64  non-zero for RPC request/response
bodyLen    uint32  payload length
reserved   uint32  reserved, must be zero on send
```

Both inbound and outbound paths enforce `MaxFrameBodyBytes`. A frame with an
unknown version, invalid kind, invalid priority, oversized body, or non-zero
reserved field is rejected and closes the connection with a typed error. The
reader never allocates a body before validating the header.

The initial version does not add compression, checksums, multiplexed streams, or
authentication fields. Those can be added later through `flags` and `version`
without changing the actor model.

## Lifecycle Model

Every main object has an explicit state machine:

```text
New -> Running -> Draining -> Closed
```

Rules:

- `Client` and `Server` start in `Running` after construction succeeds.
- `Stop` is idempotent.
- Once an object enters `Draining`, no new dials, sends, notifications, or RPCs
  may start.
- `ClosePeer` evicts one peer, closes its connections, fails pending RPCs, and
  requires a later send to resolve and dial a fresh peer state.
- `Stop` closes all listeners/connections, fails all pending RPCs, stops service
  workers, and waits for owned goroutines.
- All lifecycle transitions are observable through counters/events but do not
  call user hooks on I/O goroutines.

Connection construction is two-phase:

```go
conn := newConn(...)
conn.InstallDispatch(...)
conn.Start()
```

Reader and writer goroutines start only after dispatch and peer references are
fully installed. This avoids exposing partially initialized connection state.

## Concurrency Model

The first implementation uses bounded goroutines:

- One accept goroutine per server listener.
- Two goroutines per TCP connection: `readLoop` and `writeLoop`.
- One dial goroutine only for the caller that wins a peer slot dial race.
- Bounded service workers per registered service.
- No per-request cancellation watcher goroutine.

Inbound frames are handled as follows:

- `data` and `notify` frames are routed to the service queue.
- `rpc_req` frames are routed to the service queue with response metadata.
- `rpc_resp` frames complete a pending client call in the connection pending
  table.
- `control` frames are handled by the connection actor.

Server handlers do not run on the reader goroutine. The reader validates and
enqueues work, then returns to reading. If the target service queue is full, RPC
requests receive `ErrBusy`; one-way frames are rejected and counted.

## Payload Ownership

Payload ownership is explicit:

- `Send`, `Notify`, and `Call` copy `[]byte` payloads before enqueueing.
- `SendOwned` accepts an `OwnedBuffer` and transfers ownership to the transport
  when enqueue succeeds.
- If enqueue fails, the caller retains ownership and must release or reuse the
  buffer.
- Once an owned payload is accepted, callers must not read or mutate it.
- Writer releases accepted owned buffers after write completion or connection
  close.
- Reader-owned buffers are valid only for the handler call unless the handler
  explicitly retains a copied payload.

`OwnedBuffer` is a small root-package type around `[]byte` plus a release
callback. It does not leak the slab pool implementation into public API beyond the
ownership contract.

## Peer and Connection Selection

`Client` owns a `peer.Manager` keyed by `NodeID`. Each peer owns a fixed number
of connection slots:

```text
peer(NodeID)
  slot[0] -> conn
  slot[1] -> conn
  ...
```

`shardKey % poolSize` selects the slot. A closed or absent slot triggers a
single-flight dial. Other callers wait for dial completion or their context
deadline. Dial failures are cached for a short configurable cooldown to avoid
hot retry loops. `ClosePeer` evicts the peer and clears cached failures so the
next call resolves discovery again.

The manager never creates a peer after `Client.Stop` begins.

## Scheduler and Backpressure

Each connection writer owns priority lanes with byte and item limits:

```text
Raft       high weight, strict byte cap
Control    high-medium weight
RPC        medium weight
Bulk       low weight
```

Backpressure is enforced by both item count and queued bytes. This avoids a
small item queue accidentally admitting many large frames, and avoids a byte
queue admitting too many tiny frames.

The scheduler uses weighted deficit round-robin over queued bytes. Recommended
initial weights:

```text
Raft:    8
Control: 6
RPC:     4
Bulk:    1
```

Each write batch is bounded by:

- `MaxBatchFrames`
- `MaxBatchBytes`
- available frames in scheduled lanes

The writer uses `net.Buffers` for header/body writev. Partial write errors close
the connection and fail pending RPCs. Successful write metrics include frames,
bytes, batch size, and lane mix.

## RPC Model

RPC has two independent parts:

- Client pending table per connection.
- Server service queues and bounded workers.

Client calls:

1. Acquire peer connection by shard key.
2. Allocate request ID from the connection or client monotonic counter.
3. Store pending entry in a sharded table.
4. Enqueue an `rpc_req` frame.
5. Wait for response, context cancellation, or connection close.

Server registration:

```go
type ServiceOptions struct {
	Concurrency int
	QueueSize   int
	MaxQueueBytes int64
	Timeout     time.Duration
	MaxPayload  int
}
```

Rules:

- `Concurrency <= 0` is rejected by config validation.
- Queue full returns `ErrBusy` for RPC and drops/counts one-way work.
- Handler timeout returns `ErrTimeout`.
- Handler errors are encoded as structured remote errors.
- Oversized service payloads return `ErrMsgTooLarge` or close the connection
  when the frame itself violates global limits.

Remote errors preserve a stable code:

```go
type RemoteError struct {
	Code    string
	Message string
}
```

Callers can use `errors.Is` for local typed errors and inspect
`RemoteError` for remote service failures.

## Observability

Observation must not block I/O or service workers. `transportv2` exposes:

- Atomic counters for hot metrics.
- Snapshot APIs for management/UI.
- Optional bounded event ring for recent lifecycle and error events.

Minimum metrics:

- Dial attempts, success, failure, duration.
- Active peers and active connections.
- Per-peer queue items and queued bytes.
- Enqueue success, queue full, stopped, oversized.
- Write frames, bytes, batches, write errors.
- Read frames, bytes, decode errors.
- RPC pending, started, ok, timeout, canceled, busy, remote error.
- Service queue depth and worker concurrency.

Public hooks, if added, must receive events from an observer goroutine or ring
drain path, never from connection reader/writer goroutines.

## Error Semantics

Use typed sentinel errors for local failures:

- `ErrStopped`
- `ErrTimeout`
- `ErrCanceled`
- `ErrNodeNotFound`
- `ErrQueueFull`
- `ErrMsgTooLarge`
- `ErrInvalidFrame`
- `ErrInvalidPriority`
- `ErrDialFailed`
- `ErrBusy`

Outbound API methods return these errors wrapped with context, while
preserving `errors.Is`. Connection-level protocol errors close only the affected
connection. Peer discovery errors do not poison unrelated peers.

## Configuration

`ClientConfig` includes:

- `NodeID`
- `Discovery`
- `PoolSize`
- `DialTimeout`
- `Dialer`
- `Limits`
- `Observer`

`ServerConfig` includes:

- `NodeID`
- `Limits`
- `Observer`
- `Logger`

`Limits` includes:

- `MaxFrameBodyBytes`
- `MaxQueuedBytesPerConn`
- `MaxQueuedItemsPerConn`
- `MaxBatchBytes`
- `MaxBatchFrames`
- `DialFailureCooldown`
- `WriteTimeout`
- `ReadIdleTimeout`

Defaults are conservative and documented. Invalid priorities, missing
discovery, zero service concurrency, impossible batch limits, and oversized
queue settings fail at construction rather than panic later.

## Testing and Pressure Strategy

Testing is part of the package contract.

Default unit tests:

- Wire encode/decode validates every header field and size limit.
- Reader rejects invalid magic, version, kind, priority, reserved field, and
  oversized bodies before allocation.
- Writer rejects outbound oversized frames.
- `Client.Stop` prevents new dial and fails pending RPC.
- `ClosePeer` evicts old connections and refreshes discovery.
- Payload copy tests prove `Send` is safe when callers mutate buffers after
  return.
- `SendOwned` tests prove ownership transfer and release behavior.
- Service queue tests cover `ErrBusy`, timeout, handler error, and graceful stop.
- Scheduler tests cover lane weights, byte caps, and starvation prevention.
- Race tests cover rapid inbound frames during connection startup.

Required verification:

```sh
go test ./pkg/transportv2/...
go test -race ./pkg/transportv2/...
```

Benchmarks:

- Send small frame.
- Send medium frame.
- RPC round trip.
- Parallel RPC.
- Mixed Raft/RPC/Bulk write workload.
- Allocation benchmarks for `Send` and `SendOwned`.

Opt-in stress tests:

- Mixed Raft send + RPC + Bulk for a configurable duration.
- Slow reader backpressure.
- Slow handler service queue saturation.
- Dial failure storm with cooldown.
- Peer restart and address change.
- Connection close while RPCs are pending.
- Large payload pressure near configured limits.

Stress output includes p50/p95/p99 latency, throughput, queue depth,
batch size, backpressure counts, and allocations where practical.

## Implementation Slices

1. Create package skeleton, config validation, errors, and wire codec.
2. Add buffer ownership primitives and wire reader/writer tests.
3. Implement connection actor with reader, writer, pending table, and lifecycle.
4. Implement scheduler with byte-aware weighted deficit round-robin.
5. Implement peer manager, discovery, dial single-flight, and `ClosePeer`.
6. Implement server service registry, bounded workers, and RPC response path.
7. Add testkit fault connections and mixed workload harness.
8. Add benchmarks and opt-in stress tests.
9. Integrate one narrow clusterv2 caller behind an adapter after package tests
   and race tests are stable.

Each slice leaves `go test ./pkg/transportv2/...` passing.

## Open Decisions Resolved For First Version

- Use TCP, not QUIC.
- Use goroutine-per-connection, not custom netpoll.
- Use fixed 24-byte header, not the old 5-byte header.
- Copy by default; zero-copy requires explicit owned payload API.
- Use byte-aware weighted deficit round-robin for fairness.
- Use per-service bounded worker queues for RPC.
- Keep observer hot paths non-blocking.

## Success Criteria

The first production-ready version of `pkg/transportv2` is acceptable when:

- `go test ./pkg/transportv2/...` and `go test -race ./pkg/transportv2/...`
  pass consistently.
- Benchmarks report allocations for `SendOwned` near the minimum needed for
  framing and queue metadata.
- Mixed stress tests show Raft/control p99 staying within the configured budget
  while RPC and Bulk traffic are active.
- Queue saturation returns typed backpressure errors instead of unbounded memory
  growth or goroutine growth.
- `Stop` and `ClosePeer` fail pending work deterministically and never allow
  stale connections to be reused.
- One clusterv2 integration path can use the package without adding adapter-side
  lifecycle or buffer ownership workarounds.
