# wkcluster: Distributed Cluster Coordination Package

## Overview

`wkcluster/` is the top-level coordination package that assembles `multiraft`, `raftstore`, `wkfsm`, and `wkdb` into a functioning distributed cluster. It provides multi-node channel operations with automatic request routing, leader forwarding, and hash-based sharding.

First version scope: Channel CRUD only.

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Transport | Raw TCP | Lightweight, no external deps, max performance |
| Node discovery | Static config + Discovery interface | Simple first version, extensible to gossip later |
| Non-leader handling | Server-side forwarding | Client-transparent, simpler client logic |
| Sharding | hash(channelID) % groupCount + 1 | groupID == slot, consistent with wkdb/wkfsm |
| Hash function | crc32.ChecksumIEEE | Hardware-accelerated, already used in wkdb |
| Connection model | Connection pool, grouped by groupID | Avoids head-of-line blocking while preserving per-group ordering |

## Package Structure

```
wkcluster/
├── cluster.go          // Cluster main struct, lifecycle (Start/Stop)
├── transport.go        // TCP Transport, implements multiraft.Transport
├── codec.go            // Wire format encoding/decoding
├── router.go           // channelID → groupID → leaderNodeID routing
├── forward.go          // Non-leader request forwarding
├── discovery.go        // Discovery interface definition
├── static_discovery.go // Static config implementation
├── config.go           // Config types
└── api.go              // Channel CRUD public API
```

## Configuration

```go
type Config struct {
    NodeID         multiraft.NodeID   // This node's ID
    ListenAddr     string             // TCP listen address, e.g. ":9001"
    GroupCount     uint32             // Total group count, hash maps to groupID
    DataDir        string             // Base data directory
    RaftDataDir    string             // Raft log directory (default: DataDir/raft)
    Nodes          []NodeConfig       // Static node list
    Groups         []GroupConfig      // Raft group configuration
    ForwardTimeout time.Duration      // Timeout for leader forwarding (default: 5s)
    PoolSize       int                // Connections per node pair (default: 4)
}

type NodeConfig struct {
    NodeID multiraft.NodeID
    Addr   string               // e.g. "10.0.0.1:9001"
}

type GroupConfig struct {
    GroupID multiraft.GroupID
    Peers   []multiraft.NodeID   // Member nodes for this group
}
```

### Config Validation Rules

- `GroupCount > 0`
- `len(Groups) == GroupCount`
- Every `NodeID` in `Groups[].Peers` must exist in `Nodes`
- This node's `NodeID` must appear as a peer in at least one group
- `DataDir` is the base directory; raft logs go to `RaftDataDir` (or `DataDir/raft`), business data goes to `DataDir/data`. Two separate Pebble instances.

Example: 3 nodes, 3 groups:

```
GroupCount: 3
Groups:
  - GroupID: 1, Peers: [1, 2, 3]
  - GroupID: 2, Peers: [1, 2, 3]
  - GroupID: 3, Peers: [1, 2, 3]

hash("channel_abc") % 3 + 1 = 2 → GroupID 2
```

## Discovery Interface

```go
type NodeInfo struct {
    NodeID multiraft.NodeID
    Addr   string
}

type NodeEvent struct {
    Type string    // "join" | "leave"
    Node NodeInfo
}

type Discovery interface {
    GetNodes() []NodeInfo
    Resolve(nodeID multiraft.NodeID) (string, error)
    Watch() <-chan NodeEvent   // Static returns nil
    Stop()
}
```

First version: `StaticDiscovery` backed by `[]NodeConfig`. All components (`Cluster`, `Transport`, `Router`) depend on `Discovery` interface, not static config directly. Future gossip implementation only requires a new `Discovery` implementation.

## Transport (TCP)

### Connection Model

Per node-pair connection pool with group-based connection selection:

```go
type Transport struct {
    nodeID    multiraft.NodeID
    discovery Discovery
    pools     map[multiraft.NodeID]*connPool
    mu        sync.RWMutex
    runtime   *multiraft.Runtime
    listener  net.Listener
}

type connPool struct {
    addr  string
    conns []net.Conn     // Fixed size, select by groupID % len(conns)
    size  int            // Default 4
    mu    []sync.Mutex   // One lock per connection
}

func (p *connPool) GetByGroup(groupID multiraft.GroupID) (net.Conn, *sync.Mutex)
```

Connection selection: `connIndex = groupID % poolSize`. Same group always uses the same connection, guaranteeing in-order delivery within a group while allowing cross-group parallelism.

Connections are established on first use and persist. Each connection has its own mutex to avoid write contention. Broken connections are rebuilt in the same slot.

### Wire Format

```
[msgType:1][bodyLen:4][body:N]
```

Message types:

| msgType | Value | Description |
|---------|-------|-------------|
| msgTypeRaft | 1 | Raft consensus messages |
| msgTypeForward | 2 | Client request forwarded to leader |
| msgTypeResp | 3 | Response to forwarded request |

### Implements multiraft.Transport

`Send(ctx context.Context, batch []Envelope) error` routes each envelope through `envelope.GroupID % poolSize` to select connection, encodes as `msgTypeRaft`, and writes to the connection.

### Receive-Side Demultiplexer

Each connection has a dedicated read goroutine that decodes incoming messages by `msgType` and dispatches:

- `msgTypeRaft` → `runtime.Step(ctx, Envelope{GroupID: groupID, Message: msg})` for raft consensus processing
- `msgTypeForward` → local request handler: decode groupID + cmd, call `runtime.Propose()`, wait on Future, write `msgTypeResp` back
- `msgTypeResp` → matched to waiting goroutine via `requestID` (see forward wire format below)

### Connection Failure Handling

- A broken connection is detected by read/write errors (io.EOF, TCP RST, write timeout)
- On failure: close the connection, rebuild in the same slot on next use
- In-flight forward requests on a broken connection receive an error; caller retries via `proposeOrForward`
- TCP keepalive enabled on all connections to detect dead peers

## Router

```go
type Router struct {
    groupCount uint32
    runtime    *multiraft.Runtime
    discovery  Discovery
    localNode  multiraft.NodeID
}

func (r *Router) SlotForChannel(channelID string) multiraft.GroupID {
    return multiraft.GroupID(crc32.ChecksumIEEE([]byte(channelID))%r.groupCount + 1)
}

func (r *Router) LeaderOf(groupID multiraft.GroupID) (multiraft.NodeID, error)
func (r *Router) IsLocal(nodeID multiraft.NodeID) bool
```

Routing flow: `channelID → SlotForChannel() → groupID → LeaderOf() → leaderNodeID → IsLocal() → local propose or forward`

## Forwarder

```go
type Forwarder struct {
    nodeID    multiraft.NodeID
    transport *Transport
    router    *Router
    timeout   time.Duration       // From Config.ForwardTimeout
    nextReqID atomic.Uint64       // Monotonic request ID generator
    pending   sync.Map            // requestID → chan forwardResp
}

func (f *Forwarder) Forward(ctx context.Context, groupID multiraft.GroupID, cmdBytes []byte) ([]byte, error)
```

### Forward Wire Format

```
msgTypeForward: [msgType:1][bodyLen:4][requestID:8][groupID:8][cmd:N]
msgTypeResp:    [msgType:1][bodyLen:4][requestID:8][errCode:1][data:N]
```

`requestID` is a monotonically increasing uint64 used to correlate responses with waiting goroutines. The demultiplexer matches `msgTypeResp` to the pending request channel by `requestID`.

Error codes:

| errCode | Meaning |
|---------|---------|
| 0 | Success |
| 1 | Not leader (leader migrated, caller should retry) |
| 2 | Timeout |
| 3 | Group not found |

Forwarding is synchronous: the sender goroutine blocks until response arrives. Leader receives the forwarded command, calls `runtime.Propose()`, waits on the Future, and returns the result.

## Cluster (Main Struct)

```go
type Cluster struct {
    cfg       Config
    nodeID    multiraft.NodeID
    runtime   *multiraft.Runtime
    transport *Transport
    discovery Discovery
    router    *Router
    forwarder *Forwarder
    db        *wkdb.DB
}

func NewCluster(cfg Config) (*Cluster, error)
func (c *Cluster) Start() error
func (c *Cluster) Stop()
```

### Startup Sequence

1. Open two pebble DBs: `raftstore.Open(RaftDataDir)` for raft logs, `wkdb.Open(DataDir/data)` for business data
2. Initialize StaticDiscovery from `Config.Nodes`
3. Start Transport (TCP listener)
4. Create `multiraft.Runtime` (inject transport)
5. For each GroupConfig:
   - Create storage: `raftDB.ForGroup(uint64(groupID))` → `multiraft.Storage`
   - Create state machine: `sm, err := wkfsm.NewStateMachine(wkDB, uint64(groupID))` (slot == groupID)
   - Build group options: `opts := multiraft.GroupOptions{ID: groupID, Storage: storage, StateMachine: sm}`
   - If storage has existing state (`!raft.IsEmptyHardState(InitialState().HardState)`): `runtime.OpenGroup(ctx, opts)`
   - Otherwise (first boot): `runtime.BootstrapGroup(ctx, multiraft.BootstrapGroupRequest{Group: opts, Voters: peers})`

### Shutdown Sequence

1. Stop accepting new API requests
2. Drain in-flight forward requests (wait up to `ForwardTimeout`)
3. Close `multiraft.Runtime` (stops raft processing)
4. Stop Transport (close listener, close all connections)
5. Close pebble DBs

## Public API

```go
// Writes: route to leader. All write methods accept context for timeout/cancellation.
func (c *Cluster) CreateChannel(ctx context.Context, channelID string, channelType int64) error
func (c *Cluster) UpdateChannel(ctx context.Context, channelID string, channelType int64, ban int64) error
func (c *Cluster) DeleteChannel(ctx context.Context, channelID string, channelType int64) error

// Reads: local wkdb, eventual consistency.
// Note: reads from a follower behind on replication may return stale data.
// Routes channelID → groupID via SlotForChannel, then reads from db.ForSlot(uint64(groupID)).
func (c *Cluster) GetChannel(ctx context.Context, channelID string, channelType int64) (*wkdb.Channel, error)
```

### Core Write Path

```go
func (c *Cluster) proposeOrForward(ctx context.Context, groupID multiraft.GroupID, cmd []byte) error {
    // Retry up to 3 times on leader migration (errCode=1)
    for attempt := 0; attempt < 3; attempt++ {
        leaderID, err := c.router.LeaderOf(groupID)
        if err != nil {
            return err
        }
        if c.router.IsLocal(leaderID) {
            future, err := c.runtime.Propose(ctx, groupID, cmd)
            if err != nil {
                return err
            }
            _, err = future.Wait(ctx)
            return err
        }
        _, err = c.forwarder.Forward(ctx, groupID, cmd)
        if err == ErrNotLeader {
            continue // leader migrated, retry with fresh lookup
        }
        return err
    }
    return ErrLeaderNotStable
}
```

### Read Path

Reads go directly to local wkdb via `ShardStore`, accepting eventual consistency. No raft involved.

### New Commands Required

- Add `cmdTypeDeleteChannel` to `wkfsm/command.go` with encoder/decoder
- Add `deleteChannelCmd` to `wkfsm/state_machine.go` ApplyBatch handling
- Add `WriteBatch.DeleteChannel(channelID, channelType)` to `wkdb/batch.go`

### CreateChannel Semantics

`CreateChannel` uses `cmdTypeUpsertChannel` (idempotent). In a distributed system, checking existence before proposing introduces TOCTOU races. Upsert semantics avoids this — creating an already-existing channel is a no-op.

## Dependencies

```
         wkcluster/
        /    |    \
  multiraft  wkfsm  wkdb
       |
   raftstore
```

No circular dependencies. wkcluster is the top-level assembly layer.
