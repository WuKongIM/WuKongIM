# clusterv2 Architecture Design

Date: 2026-05-25
Status: Draft for review
Owner: Codex

## 1. Purpose

`pkg/cluster` has grown into a broad orchestration package that mixes transport, Controller hosting, Slot Multi-Raft runtime, reconciliation, observation, hash-slot migration, operator APIs, onboarding, diagnostics, and forwarding logic. This makes it difficult to read, test, and extend safely.

`pkg/clusterv2` will be a clean parallel implementation focused on the minimum production-shaped core for a three-node cluster. It will not attempt to be API-compatible or storage-compatible with `pkg/cluster`.

The first version must provide:

- Three-node cluster startup and shutdown.
- Controller abstraction with the first adapter backed by `pkg/controllerv2`.
- Control snapshot driven hash-slot routing.
- Slot Multi-Raft bootstrap and stable runtime convergence.
- Metadata `Propose` through local leader or typed forward RPC.
- `pkg/channelv2` service integration with quorum append and follower fetch.
- Clear package boundaries that prevent recreating a large all-in-one cluster package.

## 2. Non-Goals

The first version explicitly excludes:

- Compatibility with old `pkg/cluster.API`.
- Compatibility with old cluster data or old cluster config.
- Hash-slot migration and physical slot add/remove.
- Node onboarding, drain, resume, scale-in, and full repair/rebalance automation.
- Full management/operator APIs.
- Raft log entry inspection, manual compaction APIs, and advanced diagnostics.
- Automatic channel append forwarding to the channel leader.
- A full production channel metadata planner. The design exposes `ChannelMetaSource` first, with test/static and later Slot-backed implementations.

These features can be added later as independent modules. They must not be folded into `node.go` or a generic service file.

## 3. Design Principles

- **Thin composition root**: `clusterv2.Node` wires dependencies and owns lifecycle only.
- **Hot path isolation**: `Propose` and channel append must not synchronously call Controller APIs.
- **Atomic read snapshots**: routing and discovery are copy-on-write snapshots read without locks on foreground paths.
- **Small modules**: each package has one responsibility and a narrow interface.
- **Reuse proven runtimes**: reuse `pkg/slot/multiraft`, `pkg/controllerv2`, `pkg/channelv2`, and `pkg/transport` instead of rewriting them.
- **Three-node first**: design and tests target a three-node cluster from the beginning. A single-node deployment remains a single-node cluster, not a bypass mode.
- **No cluster bypass branches**: deployment semantics remain cluster semantics even for smaller topologies.

## 4. Proposed Package Layout

```text
pkg/clusterv2/
  api.go                 // Public API: Start, Stop, Route, Propose, Append, Fetch, Snapshot.
  node.go                // Lifecycle composition root.
  config.go              // Independent v2 config and data paths.
  errors.go
  FLOW.md

  control/               // Controller abstraction and adapters.
    controller.go
    snapshot.go
    controllerv2.go

  routing/               // HashSlot -> SlotID -> Leader read model.
    table.go
    cache.go
    router.go

  net/                   // Typed node-to-node RPC and discovery glue. Go package name: clusternet.
    server.go
    client.go
    codec.go
    discovery.go

  slots/                 // Slot Multi-Raft lifecycle and reconcile.
    runtime.go
    reconciler.go
    manager.go
    observer.go

  propose/               // Slot metadata write path and forwarding.
    service.go
    forward.go
    codec.go

  channels/              // channelv2 integration layer.
    service.go
    transport.go
    resolver.go
    lifecycle.go

  observe/               // Background loops and low-frequency reporting.
    reporter.go
    loops.go
    snapshot.go

  internal/
    lifecycle/
    retry/
    clock/
```

Package boundaries:

- `node.go` may depend on all subpackages because it is the only composition root.
- `control` does not import `multiraft` or `channelv2` runtime internals.
- `routing` owns foreground route snapshots only.
- `slots` owns Slot Raft runtime operations only.
- `propose` owns Slot metadata command submission and forwarding only.
- `channels` owns `pkg/channelv2` construction, channel transport, metadata resolution, and tick/close lifecycle.
- `observe` owns background synchronization and reporting only.
- `net` is the only node-to-node communication boundary. Its Go package name should be `clusternet` to avoid collisions with the standard library `net` package in implementation files.

## 5. Public API

`pkg/clusterv2` exposes a new compact API. It does not preserve the old `cluster.API` shape.

```go
type Node struct {
    // unexported fields
}

func New(cfg Config) (*Node, error)

func (n *Node) Start(ctx context.Context) error
func (n *Node) Stop(ctx context.Context) error

func (n *Node) NodeID() uint64
func (n *Node) Snapshot() Snapshot

func (n *Node) RouteKey(key string) (Route, error)
func (n *Node) RouteHashSlot(hashSlot uint16) (Route, error)

func (n *Node) Propose(ctx context.Context, req ProposeRequest) error

func (n *Node) AppendChannel(ctx context.Context, req channelv2.AppendRequest) (channelv2.AppendResult, error)
func (n *Node) AppendChannelBatch(ctx context.Context, req channelv2.AppendBatchRequest) (channelv2.AppendBatchResult, error)
func (n *Node) FetchChannel(ctx context.Context, req channelv2.FetchRequest) (channelv2.FetchResult, error)
```

Core DTOs:

```go
type ProposeRequest struct {
    Key     string
    Command []byte
    Target  ProposeTarget
}

type ProposeTarget struct {
    HashSlot    uint16
    HasHashSlot bool
    SlotID      uint32
    HasSlotID   bool
}

type Route struct {
    HashSlot uint16
    SlotID   uint32
    Leader   uint64
    Peers    []uint64
    Revision uint64
}

type Snapshot struct {
    NodeID         uint64
    ControllerLead uint64
    StateRevision  uint64
    RoutesReady    bool
    SlotsReady     bool
    ChannelsReady  bool
    SlotCount      uint32
    HashSlotCount  uint16
}
```

`ProposeRequest` supports three input levels:

- `Key`: common path. `clusterv2` computes hash slot and physical slot.
- `Target.HashSlot` with `Target.HasHashSlot=true`: caller already knows the hash slot and avoids key hashing.
- `Target.SlotID + Target.HashSlot` with both explicit flags set: most explicit path for internal or batch callers.

The explicit `HasHashSlot` flag is required because hash slot `0` is valid and cannot be distinguished from an omitted `uint16` zero value.

Validation rules:

- `Command` must be non-empty.
- If `Target.HasSlotID` is true, `Target.HasHashSlot` must also be true.
- If `Target.HasHashSlot` is false, `Key` must be non-empty.
- If only `Key` is set, `routing.Router` computes `HashSlot -> SlotID`.
- Foreground `Propose` returns `ErrRouteNotReady` instead of calling Controller when no valid route snapshot exists.

Channel APIs reuse `pkg/channelv2` DTOs directly to avoid duplicate message models.

## 6. Control Plane

`pkg/clusterv2/control` defines a stable abstraction and hides `pkg/controllerv2` from other modules.

```go
type Controller interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error

    LocalSnapshot(ctx context.Context) (Snapshot, error)
    LeaderID() uint64

    ReportNode(ctx context.Context, report NodeReport) error
    ReportSlots(ctx context.Context, report SlotRuntimeReport) error

    Watch() <-chan SnapshotEvent
}
```

Control snapshot model:

```go
type Snapshot struct {
    Revision     uint64
    ControllerID uint64
    Nodes        []Node
    Slots        []SlotAssignment
    HashSlots    HashSlotTable
    Tasks        []ReconcileTask
}

type SlotAssignment struct {
    SlotID          uint32
    DesiredPeers    []uint64
    ConfigEpoch     uint64
    PreferredLeader uint64
}
```

The first adapter uses `pkg/controllerv2`:

- Controller voter nodes start ControllerV2 Raft, FSM, statefile, and server facade.
- Non-voter/data-only nodes mirror the leader `cluster-state.json` through ControllerV2 sync.
- The adapter converts `controllerv2/state.ClusterState` into `control.Snapshot`.
- The adapter publishes `SnapshotEvent` after a valid statefile/FSM update.
- `LeaderID()` exposes the known Controller leader for status only.

State flow:

```text
Controller voter:
Controller Raft commit
  -> controllerv2 FSM saves cluster-state.json
  -> control adapter publishes SnapshotEvent
  -> routing.Update(snapshot)
  -> net.Discovery.Update(snapshot.Nodes)
  -> slots.Reconciler.Wake(snapshot)

Non-voter/data node:
state sync pulls leader cluster-state.json
  -> local statefile save
  -> control adapter publishes SnapshotEvent
  -> same downstream flow
```

Control constraints:

- `cluster-state.json` stores low-frequency control state only.
- Runtime observations are not stored as high-frequency entries in `cluster-state.json`.
- Foreground `Propose` and `AppendChannel` never wait on Controller RPC.
- Invalid snapshots are rejected and do not poison existing routing or discovery snapshots.
- During temporary Controller unavailability, the node keeps serving from the last valid snapshot when possible.
- `ReportNode` and `ReportSlots` are best-effort adapter methods. The ControllerV2 v1 adapter may implement unsupported report fields as no-op until matching ControllerV2 commands exist; this must be explicit in code and tests rather than hidden behind fake success semantics.

## 7. Routing and Slot Propose Path

`routing` stores immutable foreground snapshots:

```go
type Table struct {
    Revision      uint64
    HashToSlot    []uint32
    SlotLeaders   map[uint32]uint64
    SlotPeers     map[uint32][]uint64
    HashSlotCount uint16
}

type Router struct {
    current atomic.Pointer[Table]
}
```

Routing APIs:

```go
func (r *Router) RouteKey(key string) (Route, error)
func (r *Router) RouteHashSlot(hashSlot uint16) (Route, error)
func (r *Router) RouteSlot(slotID uint32, hashSlot uint16) (Route, error)
func (r *Router) UpdateControlSnapshot(snapshot control.Snapshot)
func (r *Router) UpdateSlotLeaders(status []slots.Status)
```

Lookup rules:

- `Key -> crc32 % HashSlotCount -> HashToSlot[hashSlot]`.
- `HashToSlot` comes from `control.HashSlotTable`; it is not derived from `slotID % count`.
- Slot leader data comes from local `slots.Observer` status cache.
- Missing route returns `ErrRouteNotReady`.
- Known slot with no leader returns `ErrNoSlotLeader`.
- Table replacement is atomic; lookup path has no mutex.

`propose.Service` owns the Slot metadata write path:

```go
type Service struct {
    localNode uint64
    router    *routing.Router
    slots     slots.Runtime
    forward   ForwardClient
}

func (s *Service) Propose(ctx context.Context, req ProposeRequest) error
```

Payload envelope:

```text
[version:1][hashSlot:2][command bytes...]
```

Execution path:

```text
Propose(ctx, req)
  -> normalize route input
  -> routing cache lookup
  -> if local leader: slots.Propose(ctx, slotID, payload)
  -> if remote leader: ForwardPropose RPC
```

Forwarding rules:

- The remote handler checks local Slot status again and must not trust the caller route.
- `ErrNotLeader` and `ErrNoSlotLeader` are typed errors.
- A caller may retry once within a small forward budget after refreshing local leader cache.
- No Controller calls happen in the propose path.

## 8. Slot Runtime and Reconciliation

`slots` is a thin wrapper around `pkg/slot/multiraft`.

```go
type Runtime interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error

    Ensure(ctx context.Context, assignment Assignment) error
    CloseUnassigned(ctx context.Context, keep map[uint32]struct{}) error

    Step(ctx context.Context, env Envelope) error
    Propose(ctx context.Context, slotID uint32, payload []byte) error
    StatusSnapshot() []Status
}
```

First-version reconciliation supports bootstrap and stable runtime convergence:

- If the local node is in `DesiredPeers`:
  - open existing local Slot state when Raft hard state exists;
  - bootstrap only when no durable state exists and the local node is the bootstrap owner;
  - when no durable state exists and the local node is not the bootstrap owner, wait until the control/observation layer has evidence that the Slot was bootstrapped, then open as a follower. This mirrors the current managed-slot safety pattern and avoids creating empty follower runtimes before a real group exists.
- If the local node is not in `DesiredPeers`:
  - do not delete local data in v1;
  - mark as unassigned and leave destructive cleanup for a later operator module.
- Only assignments relevant to the local node are actively ensured.

Bootstrap owner selection:

```text
if assignment.PreferredLeader != 0:
    owner = PreferredLeader
else:
    owner = min(DesiredPeers)
```

This prevents repeated multi-node bootstrap. If local durable Raft state exists, `OpenSlot` is always used and `BootstrapSlot` is never retried. Non-owner peers must not bootstrap the same empty Slot; they open only after bootstrap evidence exists or after they already have durable Raft state.

## 9. channelv2 Integration

`pkg/clusterv2/channels` hosts `pkg/channelv2` as a first-class runtime.

Public calls:

```text
Node.AppendChannel / AppendChannelBatch / FetchChannel
  -> channels.Service
  -> channelv2.Cluster
  -> channelv2/service + reactor group
```

`channels.Service` wires:

- `channelv2/service.New`.
- Store factory.
- `channelv2/transport.Client` implementation over `clusterv2/net`.
- `channelv2.MetaResolver` implementation backed by `ChannelMetaSource`.
- Tick and close lifecycle.

`channelv2/service.New` currently returns the root `channelv2.Cluster` interface, while replication handlers live on the concrete service value and satisfy `channelv2/transport.Server`. `channels.Service` must therefore retain an internal interface that combines both surfaces instead of storing only `channelv2.Cluster`:

```go
type channelRuntime interface {
    channelv2.Cluster
    channeltransport.Server
}
```

If `channelv2` later exports this combined interface, `clusterv2/channels` should use the exported type.

Channel replication RPCs:

```text
Pull      -> RPCChannelPull
Ack       -> RPCChannelAck
PullHint  -> RPCChannelPullHint
Notify    -> RPCChannelNotify
```

Handlers dispatch to the local `channelRuntime` without taking a global `Node` lock:

```text
RPCChannelPull     -> channelv2 transport server HandlePull
RPCChannelAck      -> HandleAck
RPCChannelPullHint -> HandlePullHint
RPCChannelNotify   -> HandleNotify
```

Metadata source interface:

```go
type ChannelMetaSource interface {
    ResolveChannelMeta(ctx context.Context, id channelv2.ChannelID) (channelv2.Meta, error)
}
```

First-version implementations:

- `StaticMetaSource`: test and smoke use. It maps channels to explicit leader, replicas, ISR, and epochs.
- `SlotMetaSource`: production direction. It reads authoritative channel runtime metadata through Slot-backed storage when that model is ready.

Important semantics:

- Channel leader is not the same concept as Slot leader.
- Channel replicas/ISR are not the same concept as Slot assignment peers.
- `ExpectedChannelEpoch` and `ExpectedLeaderEpoch` remain channelv2 fences.
- v1 does not automatically forward `AppendChannel` to the channel leader. Non-leader calls return the channelv2 typed not-leader error.

## 10. Network and RPC

`pkg/clusterv2/net` owns node-to-node communication and typed RPC service registration.

RPC groups:

```text
Raft data:
  MsgSlotRaft
  MsgSlotRaftBatch

Slot metadata:
  RPCSlotForwardPropose

Channelv2 replication:
  RPCChannelPull
  RPCChannelAck
  RPCChannelPullHint
  RPCChannelNotify

Control:
  RPCControlStateSync
  RPCControlReportNode
  RPCControlReportSlots
```

Logical pools:

```text
raftPool       // Slot Raft messages.
proposePool    // Slot forward propose.
channelPool    // channelv2 Pull/Ack/PullHint.
controlPool    // Control sync and reports.
```

The first implementation may share a physical `pkg/transport.Pool`, but `net` APIs must preserve logical pool boundaries so they can be split later without changing callers.

Handler rules:

- Decode, call the target service, encode.
- Never hold a global `Node` lock.
- Enforce max payload sizes.
- Enforce request deadlines.
- Return typed error codes rather than relying on error string matching.

Codec strategy:

- Slot Raft uses `raftpb.Message` encoding.
- Slot forward propose uses a small versioned binary envelope.
- Channel RPCs should use versioned binary codec. A temporary JSON codec is acceptable only if marked as a short-lived implementation step and covered by roundtrip tests.
- Control state sync can use ControllerV2 state JSON.

## 11. Background Observation Loops

`observe` owns low-frequency loops:

```text
stateSyncLoop        // Sync control snapshots on non-voter/data nodes.
reconcileLoop        // Wake Slot reconciliation after control snapshots.
slotStatusLoop       // Poll multiraft status and refresh route leader cache.
nodeReportLoop       // Report node liveness and addresses.
slotReportLoop       // Report local Slot runtime views.
channelTickLoop      // Call channelv2.Cluster.Tick(ctx).
```

Suggested intervals:

```go
ControlSyncInterval  = 500 * time.Millisecond to 2 * time.Second
ReconcileDebounce    = 50 * time.Millisecond to 200 * time.Millisecond
SlotStatusInterval   = 100 * time.Millisecond to 300 * time.Millisecond
NodeReportInterval   = 1 * time.Second to 2 * time.Second
SlotReportInterval   = 1 * time.Second
ChannelTickInterval  = 10 * time.Millisecond to 50 * time.Millisecond
```

Loop constraints:

- Foreground writes never wait on observe loops.
- `slotStatusLoop` updates routing by copy-on-write table replacement.
- `channelTickLoop` is independent of Slot reconcile.
- Control report failures are retried and do not invalidate the last good route.

## 12. Lifecycle

Start order:

```text
1. Validate config and create v2 data directories.
2. Start net server and register handlers.
3. Start control adapter.
4. Wait for an initial valid control snapshot.
5. Build routing and discovery snapshots from control state.
6. Start Slot runtime.
7. Run initial Slot reconcile and wait for local required slots to open/bootstrap.
8. Start channelv2 service.
9. Start observe loops.
10. Wait for readiness: routes ready + local assigned slots ready + channel service ready.
```

Stop order:

```text
1. Mark node stopping and reject new external requests.
2. Stop observe loops.
3. Close channelv2 service.
4. Stop Slot runtime.
5. Stop control adapter.
6. Stop net server and pools.
```

Readiness model:

```go
type Readiness struct {
    ControlReady  bool
    RoutesReady   bool
    SlotsReady    bool
    ChannelsReady bool
}
```

Default `Start` waits for local assigned slots to open. A strict test mode may also wait until relevant Slot leaders are known.

## 13. Testing Strategy

Unit tests:

```text
control adapter:
  - ControllerV2 state maps to control.Snapshot.
  - Invalid snapshots are rejected.

routing:
  - Hash-slot ranges build an O(1) table.
  - Atomic updates do not mutate old snapshots.
  - Missing route and missing leader produce typed errors.

slots:
  - Bootstrap owner selection is deterministic.
  - Existing hard state opens instead of bootstrapping.
  - Only local assigned slots are ensured.

propose:
  - Local leader path.
  - Remote forward path.
  - Not-leader typed retry behavior.
  - Hash-slot validation.

channels:
  - Channel transport codec roundtrip.
  - StaticMetaSource resolution.
  - Pull/Ack/PullHint/Notify handler dispatch.
```

Integration tests:

```text
TestClusterV2ThreeNodeSlotPropose:
  - start 3 nodes;
  - ControllerV2 snapshot creates slots;
  - wait Slot leaders;
  - Propose metadata command through a non-leader node;
  - verify command applied.

TestClusterV2ThreeNodeChannelAppendQuorum:
  - start 3 nodes;
  - configure channel meta leader=1 replicas=[1,2,3] ISR=[1,2,3];
  - AppendChannel on leader with CommitModeQuorum;
  - wait followers catch up;
  - FetchChannel on follower sees committed message.
```

Benchmarks:

```text
BenchmarkRouteKey
BenchmarkLocalPropose
BenchmarkForwardPropose
BenchmarkChannelAppendLocal
BenchmarkChannelAppendQuorumThreeNode
```

Default unit tests should use in-process or loopback harnesses. Docker or multi-process scenarios should be added later under integration tags.

## 14. Acceptance Criteria

The first version is complete when:

1. Three `clusterv2.Node` instances can start in one process test.
2. ControllerV2 produces and syncs a valid control snapshot.
3. Hash slots route to physical slots through an atomic routing table.
4. Slot Multi-Raft forms leaders.
5. Any node can call `Propose`; local or forwarded submission reaches the Slot leader.
6. `channelv2` starts inside `clusterv2`.
7. Node 1 can append a channel message with `CommitModeQuorum`.
8. Node 2 and Node 3 replicate via Pull/Ack/PullHint.
9. A follower can `FetchChannel` and read the committed message.

## 15. Main Risks

- **Repeated bootstrap**: use deterministic bootstrap owner and never bootstrap over existing hard state.
- **Stale routing**: remote forward handlers must re-check local leadership and return typed not-leader errors.
- **Control/hot path coupling**: prohibit Controller calls in foreground write paths.
- **Channel metadata ambiguity**: keep `ChannelMetaSource` explicit; do not infer channel replicas from Slot replicas.
- **RPC interference**: preserve logical pool separation for raft, propose, channel, and control traffic.
- **Shutdown races**: reject new external requests before closing channel and Slot runtimes.
- **Package growth**: new features must enter focused subpackages or adapters, not the composition root.
