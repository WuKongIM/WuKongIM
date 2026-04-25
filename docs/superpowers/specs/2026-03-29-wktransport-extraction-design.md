# wktransport: Standalone Network Transport Package

## Overview

Extract the network layer from `wkcluster/` into an independent `wktransport/` package, enabling the business layer to reuse the same TCP transport infrastructure for its own messaging needs (RPC and push) without any raft dependencies.

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Package name | `wktransport` | Consistent with `wkdb`/`wkfsm` naming; clearly conveys "transport layer" |
| Connection sharing | Shared listener, separate outbound pools | Single port simplifies deployment; independent pools isolate raft and business traffic |
| Message routing | `msgType` byte dispatch | Already in the wire format; natural extension point for new message types |
| RPC mechanism | Built-in request/response via 0xFE/0xFF | Generalized from existing Forwarder; avoids each consumer reimplementing RPC |
| Push mechanism | `Client.Send()` — fire and forget | Direct frame write, no response expected |
| Dependencies | Zero external deps (stdlib only) | Maximum reusability; no raft/multiraft coupling |

## Package Structure

```
wktransport/
├── types.go       // NodeID, MessageHandler, RPCHandler, Discovery interface
├── codec.go       // Frame encoding/decoding: [type:1][len:4][body:N], buffer pool
├── pool.go        // Outbound connection pool, sharded by (nodeID, shardKey)
├── server.go      // TCP Server: accept loop + msgType handler registration
├── client.go      // Client: Send() (one-way) + RPC() (request/response)
├── errors.go      // Network-layer errors and reserved constants
```

## Types (types.go)

```go
package wktransport

type NodeID = uint64

// MessageHandler processes an inbound message of a specific type.
// conn is provided so the handler can write back on the same connection if needed.
type MessageHandler func(conn net.Conn, body []byte)

// RPCHandler processes an inbound RPC request and returns a response body.
// The ctx passed by the Server is context.Background(). The handler is responsible
// for applying its own timeout (e.g., wkcluster wraps with forwardTimeout).
type RPCHandler func(ctx context.Context, body []byte) ([]byte, error)

// Discovery resolves a NodeID to a network address.
type Discovery interface {
    Resolve(nodeID NodeID) (addr string, err error)
}
```

- `NodeID` is a plain `uint64` type alias. Note: `multiraft.NodeID` is a named type (`type NodeID uint64`), not an alias. This means `StaticDiscovery.Resolve` must change its parameter type from `multiraft.NodeID` to `uint64` to satisfy `wktransport.Discovery`. The conversion is safe (same underlying type) but the signature change is required.
- `Discovery` only includes `Resolve`; lifecycle (`Stop`) and enumeration (`GetNodes`) are not transport concerns

## Wire Protocol (codec.go)

### Frame Format

```
[msgType:1][bodyLen:4][body:N]
```

- `msgType`: 1 byte, meaning defined by consumers
- `bodyLen`: 4 bytes big-endian, length of body
- `body`: variable-length payload

### Public API

```go
// WriteMessage encodes and writes a framed message. Uses buffer pool to avoid allocation.
func WriteMessage(w io.Writer, msgType uint8, body []byte) error

// ReadMessage reads a framed message from r.
func ReadMessage(r io.Reader) (msgType uint8, body []byte, err error)
```

### Message Type Allocation

| Range | Owner | Usage |
|-------|-------|-------|
| 0 | Reserved | Invalid. Server drops frames with `msgType=0` (likely corruption or uninitialized memory). `ReadMessage` returns `ErrInvalidMsgType`. |
| 1-253 | Consumer-defined | wkcluster uses 1 (raft), business layer starts from 10 |
| 0xFE | wktransport internal | RPC request |
| 0xFF | wktransport internal | RPC response |

### Buffer Pool

Moved from `wkcluster/codec.go`. `sync.Pool` of byte slices, capped at 64KB to prevent retaining huge buffers.

```go
const MaxMessageSize = 64 << 20 // 64 MB upper bound
```

## Connection Pool (pool.go)

### Design

Each `Pool` instance maintains a completely independent set of TCP connections. Raft and business each create their own Pool — physically separate sockets, no data mixing between raft and business traffic. (Note: `fwdClient` shares the raft Pool because leader forwarding is raft-layer traffic, not business traffic.)

```
Node A → Node B:

  raft Pool  (size=4):   conn0 ──┐
                          conn1 ──┤
                          conn2 ──┤     Node B
                          conn3 ──┤   ┌──────────┐
                                  ├──→│ Server   │
  biz Pool   (size=2):   conn0 ──┤   │ (single  │
                          conn1 ──┘   │  port)   │
                                      └──────────┘
```

### Structure

```go
type Pool struct {
    discovery   Discovery
    size        int            // connections per node
    dialTimeout time.Duration
    nodes       map[NodeID]*nodeConns
    mu          sync.RWMutex
}

type nodeConns struct {
    addr  string
    conns []net.Conn
    mu    []sync.Mutex  // one lock per connection
}
```

### Public API

```go
func NewPool(discovery Discovery, size int, dialTimeout time.Duration) *Pool

// Get selects a connection by shardKey % size.
// On success, caller MUST call Release(nodeID, idx) after use.
// On error (err != nil), caller MUST NOT call Release — the lock is not held.
// Note: this differs from the existing connPool.getByGroup which unlocks internally on error.
// The new contract is simpler: error means no lock held, success means lock held.
func (p *Pool) Get(nodeID NodeID, shardKey uint64) (conn net.Conn, idx int, err error)

// Release unlocks the connection slot.
func (p *Pool) Release(nodeID NodeID, idx int)

// Reset closes and clears a connection (call on I/O error).
// Caller MUST hold the connection lock (i.e., call Reset between Get and Release).
// Reset does not release the lock — caller must still call Release after Reset.
func (p *Pool) Reset(nodeID NodeID, idx int)

// Close closes all connections.
func (p *Pool) Close()
```

- TCP keepalive enabled automatically on new connections
- Lazy connection establishment (dial on first Get)
- Broken connections rebuilt in the same slot on next Get

### Locking Contract

```
conn, idx, err := pool.Get(nodeID, shardKey)
if err != nil {
    // Lock NOT held — do NOT call Release
    return err
}
// Lock IS held for this slot
err = WriteMessage(conn, ...)
if err != nil {
    pool.Reset(nodeID, idx)  // close broken conn, lock still held
}
pool.Release(nodeID, idx)    // always release after successful Get
```

## Server (server.go)

### Structure

```go
type Server struct {
    listener   net.Listener
    handlers   map[uint8]MessageHandler  // msgType → handler
    rpcHandler RPCHandler                // handles 0xFE requests
    mu         sync.RWMutex
    accepted   map[net.Conn]struct{}     // track inbound connections for cleanup
    acceptedMu sync.Mutex
    stopCh     chan struct{}
    wg         sync.WaitGroup
}
```

### Public API

```go
func NewServer() *Server

// Handle registers a handler for a message type. 0xFE/0xFF are reserved.
func (s *Server) Handle(msgType uint8, h MessageHandler)

// HandleRPC registers the RPC request handler (0xFE/0xFF). Only one allowed.
// The single handler must demux by inspecting the payload if needed.
// Business layer should prefer custom msgType handlers with Send() for simple cases,
// and only use RPC (0xFE/0xFF) when request/response semantics are truly needed.
func (s *Server) HandleRPC(h RPCHandler)

// Start begins listening on addr.
func (s *Server) Start(addr string) error

// Stop closes listener + all inbound connections, waits for goroutines.
func (s *Server) Stop()

// Listener returns the underlying net.Listener (useful for tests to get actual port).
func (s *Server) Listener() net.Listener
```

### Internal Flow

```
acceptLoop:
    for {
        conn := listener.Accept()
        go handleConn(conn)
    }

handleConn(conn):
    for {
        msgType, body := ReadMessage(conn)

        switch {
        case msgType == 0xFE:
            // RPC request: decode requestID, call rpcHandler, write 0xFF response.
            // writeMu serializes response writes from concurrent RPC goroutines
            // on the same connection (prevents interleaved frame bytes).
            go handleRPCRequest(conn, &writeMu, body)
        case msgType == 0xFF:
            // RPC response: should not arrive on server-accepted connections
            // (responses go to client-initiated connections)
        default:
            handler := handlers[msgType]
            if handler != nil {
                handler(conn, body)
            }
        }
    }
```

- Evolved from current `Transport.acceptLoop` + `Transport.handleConn`
- Hard-coded `switch` replaced with handler map lookup
- RPC requests handled in separate goroutine to avoid blocking the read loop (same pattern as current `handleForwardAsync`)
- Each `handleConn` owns a `var writeMu sync.Mutex` to serialize RPC response writes from concurrent handler goroutines on the same connection (same pattern as current transport.go:237)
- Stop closes all accepted connections to unblock goroutines stuck in ReadMessage

## Client (client.go)

### Structure

```go
type Client struct {
    pool      *Pool
    nextReqID atomic.Uint64
    pending   sync.Map       // requestID → chan rpcResponse
    readLoops sync.Map       // tracks which connections have a reader goroutine
    stopCh    chan struct{}
    wg        sync.WaitGroup
}

type rpcResponse struct {
    body []byte
    err  error
}
```

### Public API

```go
func NewClient(pool *Pool) *Client

// Send writes a one-way message. No response expected.
func (c *Client) Send(nodeID NodeID, shardKey uint64, msgType uint8, body []byte) error

// RPC sends a request and waits for a response. Uses 0xFE/0xFF internally.
func (c *Client) RPC(ctx context.Context, nodeID NodeID, shardKey uint64, body []byte) ([]byte, error)

// Stop cancels all pending RPCs and waits for goroutines.
func (c *Client) Stop()
```

### Send Flow

```
Send(nodeID, shardKey, msgType, body):
    conn, idx := pool.Get(nodeID, shardKey)
    err := WriteMessage(conn, msgType, body)
    if err != nil { pool.Reset(nodeID, idx) }
    pool.Release(nodeID, idx)
```

### RPC Flow

```
RPC(ctx, nodeID, shardKey, body):
    reqID := nextReqID.Add(1)
    respCh := make(chan rpcResponse, 1)
    pending.Store(reqID, respCh)
    defer pending.Delete(reqID)

    conn, idx := pool.Get(nodeID, shardKey)
    ensureReadLoop(conn)
    WriteMessage(conn, 0xFE, encodeRPCRequest(reqID, body))
    pool.Release(nodeID, idx)

    select {
    case resp := <-respCh:  return resp.body, resp.err
    case <-ctx.Done():      return nil, ctx.Err()
    case <-stopCh:          return nil, ErrStopped
    }
```

### RPC Wire Format

```
0xFE (request):  [msgType:1][bodyLen:4][requestID:8][payload:N]
0xFF (response): [msgType:1][bodyLen:4][requestID:8][errCode:1][payload:N]
```

The standard frame `[msgType:1][bodyLen:4][body:N]` applies: `bodyLen` is the total length of everything after the header. For 0xFE: `bodyLen = 8 + len(payload)`. For 0xFF: `bodyLen = 8 + 1 + len(payload)`. `ReadMessage` returns the raw body; the RPC layer then parses `requestID` (and `errCode` for responses) from the body prefix.

`errCode` at the transport level:
- `0` = success
- `1` = handler error (error message in payload)

Application-level error codes (e.g., notLeader, groupNotFound) are encoded within the payload by the consumer (wkcluster).

### readLoop

Evolved from current `Forwarder.readLoop`. Per-connection goroutine that reads 0xFF responses and dispatches to pending channels by requestID.

**ensureReadLoop** uses `sync.Map` keyed by `(nodeID<<32 | idx)` + `CompareAndSwap` for safe concurrent startup (same pattern as existing `forward.go:125-147`):

1. `LoadOrStore(key, conn)` — if new entry, start readLoop
2. If existing entry has the same `net.Conn` pointer, readLoop already running, return
3. If existing entry has a different `net.Conn` (connection was reset and replaced), `CompareAndSwap` the old entry with the new conn and start a new readLoop. The old readLoop will exit on its next read error (the old conn was closed by `Pool.Reset`)
4. If CAS fails (concurrent update), retry from step 1

**readLoop cleanup**: When readLoop exits (read error or stopCh), it deletes its `readLoops` map entry. The next `ensureReadLoop` call for that slot will start a fresh reader.

### Client.Stop

```
Stop():
    close(stopCh)
    // Close all connections tracked in readLoops to unblock ReadMessage
    readLoops.Range(func(key, value) {
        value.(net.Conn).Close()
    })
    // Cancel all pending RPCs (select/default guard prevents blocking
    // if a readLoop already sent a response to the buffered channel)
    pending.Range(func(key, value) {
        ch := value.(chan rpcResponse)
        select {
        case ch <- rpcResponse{err: ErrStopped}:
        default:
        }
    })
    wg.Wait()
```

`Client.Stop()` explicitly closes connections tracked in `readLoops` (same pattern as current `Forwarder.Stop`). This is necessary to unblock readLoop goroutines stuck in `ReadMessage`. Note: these connections belong to the Pool but closing them here is safe — the Pool will lazily reconnect on the next `Get`.

## Errors (errors.go)

### wktransport errors

```go
var (
    ErrStopped        = errors.New("wktransport: stopped")
    ErrTimeout        = errors.New("wktransport: request timeout")
    ErrNodeNotFound   = errors.New("wktransport: node not found")
    ErrMsgTooLarge    = errors.New("wktransport: message too large")
    ErrInvalidMsgType = errors.New("wktransport: invalid message type 0")
)
```

### wkcluster errors (retained)

```go
var (
    ErrNoLeader        = errors.New("wkcluster: no leader for group")
    ErrNotLeader       = errors.New("wkcluster: not leader")
    ErrLeaderNotStable = errors.New("wkcluster: leader not stable after retries")
    ErrGroupNotFound   = errors.New("wkcluster: group not found")
    ErrInvalidConfig   = errors.New("wkcluster: invalid config")
)
```

`ErrTimeout`, `ErrNodeNotFound`, `ErrStopped` removed from wkcluster; use `wktransport` versions via `errors.Is()`.

### Error Boundary

| Layer | Responsible For |
|-------|----------------|
| wktransport | Network, connection, timeout, message format errors |
| wkcluster | Raft semantics (leader, group, config) |

## wkcluster Refactoring

### After Refactoring

```
wkcluster/
├── cluster.go          // Composes wktransport.Server/Client, lifecycle
├── transport.go        // ~30 lines: thin adapter implementing multiraft.Transport
├── forward.go          // ~40 lines: groupID+cmd encode/decode, delegates to Client.RPC
├── codec.go            // ~60 lines: raft body / forward body encode/decode only
├── router.go           // Unchanged
├── api.go              // Unchanged
├── config.go           // Adjusted: TransportConfig replaces raw pool/dial settings
├── discovery.go        // Wraps wktransport.Discovery
├── static_discovery.go // Implements wktransport.Discovery
├── errors.go           // Raft-semantic errors only
```

### Cluster struct

```go
type Cluster struct {
    cfg        Config
    server     *wktransport.Server
    raftPool   *wktransport.Pool
    raftClient *wktransport.Client
    fwdClient  *wktransport.Client    // reuses raftPool (forwarding is raft traffic, not business)
    runtime    *multiraft.Runtime
    router     *Router
    db         *wkdb.DB
    raftDB     *raftstore.DB
    stopped    atomic.Bool
}
```

### transport.go (adapter)

```go
type raftTransport struct {
    client *wktransport.Client
}

func (t *raftTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
    for _, env := range batch {
        if ctx.Err() != nil {
            return ctx.Err()
        }
        data, err := env.Message.Marshal()
        if err != nil {
            return err
        }
        body := encodeRaftBody(uint64(env.GroupID), data)
        // Individual send failures are silently skipped — the raft layer
        // handles retransmission. Only context cancellation is propagated.
        // encodeRaftBody is a new function in wkcluster/codec.go that encodes
        // only the body portion (groupID + data); Client.Send handles framing.
        _ = t.client.Send(uint64(env.Message.To), uint64(env.GroupID), msgTypeRaft, body)
    }
    return nil
}
```

### forward.go (simplified)

```go
func (c *Cluster) forwardToLeader(ctx context.Context, leaderID multiraft.NodeID, groupID multiraft.GroupID, cmd []byte) ([]byte, error) {
    payload := encodeForwardPayload(uint64(groupID), cmd)
    resp, err := c.fwdClient.RPC(ctx, uint64(leaderID), uint64(groupID), payload)
    if err != nil {
        return nil, err
    }
    return decodeForwardResp(resp) // parse errCode → ErrNotLeader etc.
}
```

The `Forwarder` struct is eliminated entirely.

### Code Size Impact

| Module | Before | After |
|--------|--------|-------|
| wktransport/ (new) | 0 | ~400 lines |
| wkcluster/transport.go | ~300 lines | ~30 lines |
| wkcluster/forward.go | ~180 lines | ~40 lines |
| wkcluster/codec.go | ~150 lines | ~60 lines |
| wkcluster/cluster.go | ~170 lines | ~150 lines |

Net: +400 new, -500 removed from wkcluster. Overall code slightly decreases.

### Discovery Adapter

`StaticDiscovery` currently has `Resolve(multiraft.NodeID) (string, error)`. Since `multiraft.NodeID` is a named type (`type NodeID uint64`), not an alias, the signature must change to `Resolve(uint64) (string, error)` to satisfy `wktransport.Discovery`. This is a source-level change (callers that pass `multiraft.NodeID` must cast to `uint64`), but the underlying types are identical.

The `wkcluster.Discovery` interface (with `GetNodes()` and `Stop()`) is retained as a superset for cluster-level concerns. `StaticDiscovery` implements both:

```go
// StaticDiscovery implements both wktransport.Discovery and wkcluster.Discovery
type StaticDiscovery struct { ... }

func (d *StaticDiscovery) Resolve(nodeID uint64) (string, error)  // satisfies wktransport.Discovery
func (d *StaticDiscovery) GetNodes() []NodeInfo                    // wkcluster-only
func (d *StaticDiscovery) Stop()                                   // wkcluster-only
```

The `wkcluster.Discovery` interface in `discovery.go` must also update `Resolve` to accept `uint64` (or embed `wktransport.Discovery`). The `discovery.go` file retains the broader interface definition with the additional methods.

### Shutdown Ordering

Components must be stopped in this order:

**Cluster.Stop() sequence** (managed by wkcluster):

```
1. fwdClient.Stop()    — cancel pending forward RPCs, close readLoop connections
2. raftClient.Stop()   — cancel pending sends, close readLoop connections
3. runtime.Close()     — stop raft processing
4. server.Stop()       — close listener + all inbound connections, wait for handlers
5. raftPool.Close()    — close all outbound raft connections
6. databases close     — raftDB, wkdb
```

**Business layer** is responsible for stopping its own `bizClient` and closing its `bizPool` before or concurrently with `Cluster.Stop()`. These are not managed by `Cluster`.

Rationale:
- Clients stop first to cancel in-flight requests and stop spawning new readLoops
- Runtime stops next so no new proposals are submitted
- Server stops after runtime so in-progress RPC handlers can finish
- Pools close last since Clients may still be draining (wg.Wait)

This matches the existing pattern (forwarder → runtime → transport → databases) with Clients replacing the forwarder and Server/Pools replacing the monolithic transport.

## Dependency Graph

```
             wktransport/          ← zero external deps (stdlib only)
            /           \
     wkcluster/        business layer
    /    |    \
multiraft  wkfsm  wkdb
     |
  raftstore
```

## Testing Strategy

### wktransport tests (migrated from wkcluster)

| Test File | Coverage | Source |
|-----------|----------|--------|
| `codec_test.go` | Frame encode/decode, large message rejection, buffer pool | From wkcluster/codec_test.go |
| `pool_test.go` | Get/Release/Reset, concurrency safety, reconnection | Extracted from wkcluster/transport_test.go |
| `server_test.go` | Accept loop, handler dispatch, Stop cleanup | Extracted from wkcluster/transport_test.go |
| `client_test.go` | Send, RPC request/response, timeout, Stop cancels pending | From wkcluster/forward_test.go |

### wkcluster tests (retained)

| Test File | Coverage |
|-----------|----------|
| `transport_test.go` | raftTransport adapter encoding correctness |
| `forward_test.go` | forwardToLeader payload encode/decode, errCode conversion |
| `cluster_test.go` | Integration tests (Start/Stop/proposeOrForward) — unchanged |
| `stress_test.go` | Unchanged |
| `router_test.go` | Unchanged |
| `config_test.go` | Unchanged |

### Test Helper

```go
// In wktransport test files (not exported)
func testPair(t *testing.T) (server *Server, client *Client, cleanup func())
```

## Business Layer Usage Example

```go
// Create business-specific pool and client
bizPool := wktransport.NewPool(discovery, 2, 3*time.Second)
bizClient := wktransport.NewClient(bizPool)

// Register business handler on the shared server
server.Handle(10, func(conn net.Conn, body []byte) {
    // handle business event push
})

// Register business RPC handler
// (or use separate msgType handlers for different business RPCs)

// One-way push
bizClient.Send(targetNode, shardKey, 10, eventPayload)

// Request/response RPC
resp, err := bizClient.RPC(ctx, targetNode, shardKey, requestPayload)
```
