# clusterv2 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `pkg/clusterv2` as a clean, parallel, three-node-first cluster runtime that integrates ControllerV2, Slot Multi-Raft metadata propose, and ChannelV2 append/fetch without inheriting `pkg/cluster` complexity.

**Architecture:** `clusterv2.Node` is the only composition root. Focused subpackages own control snapshots, atomic routing, typed node RPC, Slot runtime convergence, metadata propose forwarding, ChannelV2 integration, and background observation loops. Foreground `Propose` and channel append paths read atomic snapshots and never synchronously call Controller APIs.

**Tech Stack:** Go 1.23, existing `pkg/controllerv2`, `pkg/slot/multiraft`, `pkg/channelv2`, `pkg/transport`, `go.etcd.io/raft/v3/raftpb`, standard library `context`/`sync`/`atomic`/`hash/crc32`, existing `testing` + `testify/require` style.

---

## Reference Material

- Source spec: `docs/superpowers/specs/2026-05-25-clusterv2-design.md`
- Project rules: `AGENTS.md`
- Existing cluster flow: `pkg/cluster/FLOW.md`
- ControllerV2 flow: `pkg/controllerv2/FLOW.md`
- Slot flow: `pkg/slot/FLOW.md`
- ChannelV2 flow: `pkg/channelv2/FLOW.md`
- Existing transport API: `pkg/transport/server.go`, `pkg/transport/client.go`, `pkg/transport/rpcmux.go`, `pkg/transport/types.go`
- Existing Multi-Raft API: `pkg/slot/multiraft/api.go`, `pkg/slot/multiraft/types.go`
- Existing ChannelV2 facade: `pkg/channelv2/channel.go`, `pkg/channelv2/service/service.go`, `pkg/channelv2/service/replication.go`, `pkg/channelv2/transport/types.go`

Use @superpowers:test-driven-development for each code task. Use @superpowers:verification-before-completion before claiming implementation complete. If executing with subagents and the harness/user permits them, use @superpowers:subagent-driven-development; otherwise use @superpowers:executing-plans.

## Scope Notes

- Build only `pkg/clusterv2` and its tests, plus required documentation updates.
- Do not wire `pkg/clusterv2` into `internal/app`, `cmd/wukongim`, or existing `pkg/cluster` in this plan.
- Do not modify old `pkg/cluster` implementation.
- Treat the existing `pkg/channelv2/FLOW.md` dirty worktree change as unrelated unless the user explicitly asks to include it.
- Keep single-node behavior as single-node cluster semantics. Do not add bypass branches.
- First implementation can use fakes/static adapters for early unit tests, then replace wiring with ControllerV2/transport/multiraft adapters in later tasks.

## File Structure

Create these files:

- `pkg/clusterv2/FLOW.md` - concise package responsibility and runtime flow.
- `pkg/clusterv2/api.go` - public `Node` API DTOs and `ProposeRequest`/`Route`/`Snapshot` types.
- `pkg/clusterv2/config.go` - v2-only configuration, defaults, validation, and data path helpers.
- `pkg/clusterv2/errors.go` - typed public errors.
- `pkg/clusterv2/node.go` - thin lifecycle composition root.
- `pkg/clusterv2/api_test.go` - compile/API validation tests.
- `pkg/clusterv2/node_test.go` - lifecycle tests with fake modules.

- `pkg/clusterv2/control/controller.go` - Controller interface, reports, events.
- `pkg/clusterv2/control/snapshot.go` - control snapshot DTOs and validation helpers.
- `pkg/clusterv2/control/static.go` - deterministic test/static Controller implementation.
- `pkg/clusterv2/control/controllerv2.go` - ControllerV2 adapter and state mapping.
- `pkg/clusterv2/control/control_test.go` - snapshot validation/static controller tests.
- `pkg/clusterv2/control/controllerv2_test.go` - ControllerV2 mapping tests.

- `pkg/clusterv2/routing/table.go` - immutable route table builder.
- `pkg/clusterv2/routing/router.go` - atomic router and lookup APIs.
- `pkg/clusterv2/routing/router_test.go` - routing tests.

- `pkg/clusterv2/net/ids.go` - message/RPC service IDs. Go package name must be `clusternet`.
- `pkg/clusterv2/net/discovery.go` - atomic node discovery map.
- `pkg/clusterv2/net/codec.go` - common versioned envelope helpers.
- `pkg/clusterv2/net/client.go` - typed client interfaces and adapters.
- `pkg/clusterv2/net/server.go` - typed handler registration.
- `pkg/clusterv2/net/local.go` - in-memory test network.
- `pkg/clusterv2/net/transport.go` - `pkg/transport` backed server/client/pools.
- `pkg/clusterv2/net/net_test.go` - discovery, local RPC, codec, loopback transport tests.

- `pkg/clusterv2/propose/types.go` - propose service dependencies.
- `pkg/clusterv2/propose/codec.go` - Slot propose payload and forward RPC codec.
- `pkg/clusterv2/propose/service.go` - local/remote propose decision path.
- `pkg/clusterv2/propose/forward.go` - forward client/server handlers.
- `pkg/clusterv2/propose/propose_test.go` - local, remote, retry, validation tests.

- `pkg/clusterv2/slots/types.go` - Slot assignment/status/runtime interfaces.
- `pkg/clusterv2/slots/manager.go` - open/bootstrap decision logic.
- `pkg/clusterv2/slots/runtime.go` - Multi-Raft runtime adapter.
- `pkg/clusterv2/slots/reconciler.go` - local assignment convergence.
- `pkg/clusterv2/slots/observer.go` - status snapshot helpers.
- `pkg/clusterv2/slots/slots_test.go` - fake runtime/storage tests.

- `pkg/clusterv2/channels/service.go` - ChannelV2 service wrapper and public API bridge.
- `pkg/clusterv2/channels/resolver.go` - `ChannelMetaSource`, static source, resolver adapter.
- `pkg/clusterv2/channels/codec.go` - Channel Pull/Ack/PullHint/Notify codecs.
- `pkg/clusterv2/channels/transport.go` - ChannelV2 transport client/server over clusternet.
- `pkg/clusterv2/channels/lifecycle.go` - tick/close wrapper.
- `pkg/clusterv2/channels/channels_test.go` - static metadata, handler dispatch, transport tests.

- `pkg/clusterv2/observe/loops.go` - background loop runner.
- `pkg/clusterv2/observe/reporter.go` - node/slot report helpers.
- `pkg/clusterv2/observe/snapshot.go` - readiness aggregation.
- `pkg/clusterv2/observe/observe_test.go` - fake clock/loop tests.

- `pkg/clusterv2/internal/lifecycle/group.go` - ordered start/stop helper.
- `pkg/clusterv2/internal/retry/retry.go` - tiny bounded retry helper.
- `pkg/clusterv2/internal/clock/clock.go` - clock abstraction for loop tests.

- `pkg/clusterv2/integration_test.go` - in-process three-node smoke tests.
- `pkg/clusterv2/bench_test.go` - focused route/propose/channel benchmarks.

Modify these files:

- `AGENTS.md` - add `pkg/clusterv2` to the package directory structure once the package exists.

---

### Task 1: Scaffold Public API, Config, Errors, Flow Doc

**Files:**
- Create: `pkg/clusterv2/FLOW.md`
- Create: `pkg/clusterv2/api.go`
- Create: `pkg/clusterv2/config.go`
- Create: `pkg/clusterv2/errors.go`
- Create: `pkg/clusterv2/api_test.go`
- Modify: `AGENTS.md`

- [ ] **Step 1: Create directories**

Run:

```bash
mkdir -p pkg/clusterv2/{control,routing,net,slots,propose,channels,observe,internal/{lifecycle,retry,clock}}
```

Expected: directories exist.

- [ ] **Step 2: Write failing public API compile test**

Create `pkg/clusterv2/api_test.go`:

```go
package clusterv2_test

import (
    "context"
    "testing"

    "github.com/WuKongIM/WuKongIM/pkg/channelv2"
    "github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestPublicAPICompile(t *testing.T) {
    cfg := clusterv2.Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
    node, err := clusterv2.New(cfg)
    if err != nil {
        t.Fatalf("New() error = %v", err)
    }
    _ = node.NodeID()
    _ = node.Snapshot()
    _, _ = node.RouteKey("u1")
    _, _ = node.RouteHashSlot(0)
    _ = node.Propose(context.Background(), clusterv2.ProposeRequest{Key: "u1", Command: []byte("cmd")})
    _, _ = node.AppendChannel(context.Background(), channelv2.AppendRequest{})
    _, _ = node.AppendChannelBatch(context.Background(), channelv2.AppendBatchRequest{})
    _, _ = node.FetchChannel(context.Background(), channelv2.FetchRequest{})
    _ = node.Stop(context.Background())
}

func TestProposeTargetAllowsHashSlotZero(t *testing.T) {
    req := clusterv2.ProposeRequest{Command: []byte("cmd"), Target: clusterv2.ProposeTarget{HashSlot: 0, HasHashSlot: true}}
    if !req.Target.HasHashSlot || req.Target.HashSlot != 0 {
        t.Fatal("hash slot zero must be explicitly representable")
    }
}
```

- [ ] **Step 3: Run compile test and verify failure**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestPublicAPICompile|TestProposeTargetAllowsHashSlotZero' -count=1
```

Expected: FAIL because `pkg/clusterv2` files do not exist yet.

- [ ] **Step 4: Implement API and typed errors**

All exported types and important exported struct fields must have concise English comments, following `AGENTS.md`.

Create `pkg/clusterv2/errors.go`:

```go
package clusterv2

import "errors"

var (
    ErrInvalidConfig = errors.New("clusterv2: invalid config")
    ErrNotStarted    = errors.New("clusterv2: not started")
    ErrStopping      = errors.New("clusterv2: stopping")
    ErrRouteNotReady = errors.New("clusterv2: route not ready")
    ErrNoSlotLeader  = errors.New("clusterv2: no slot leader")
    ErrNotLeader     = errors.New("clusterv2: not leader")
    ErrSlotNotFound  = errors.New("clusterv2: slot not found")
)
```

Create `pkg/clusterv2/api.go` with the public DTOs from the spec. Include English comments for exported types and fields.

- [ ] **Step 5: Implement minimal config and node shell**

Create `pkg/clusterv2/config.go`:

```go
package clusterv2

import "time"

// Config contains v2-only cluster runtime configuration.
type Config struct {
    NodeID     uint64
    ListenAddr string
    DataDir    string

    Control ControlConfig
    Slots   SlotConfig
    Channel ChannelConfig
    Timeouts TimeoutConfig
}

type ControlConfig struct { StateDir string }
type SlotConfig struct { InitialSlotCount uint32; HashSlotCount uint16; ReplicaCount uint16 }
type ChannelConfig struct { ReactorCount int; MailboxSize int; TickInterval time.Duration }
type TimeoutConfig struct { Start time.Duration; Stop time.Duration }

func (c *Config) applyDefaults() {
    if c.Timeouts.Start == 0 { c.Timeouts.Start = 10 * time.Second }
    if c.Timeouts.Stop == 0 { c.Timeouts.Stop = 5 * time.Second }
    if c.Channel.TickInterval == 0 { c.Channel.TickInterval = 20 * time.Millisecond }
}

func (c Config) validate() error {
    if c.NodeID == 0 || c.ListenAddr == "" || c.DataDir == "" { return ErrInvalidConfig }
    return nil
}
```

Create `pkg/clusterv2/node.go` as a shell that compiles and returns typed not-ready errors until later tasks wire modules.

- [ ] **Step 6: Write `pkg/clusterv2/FLOW.md`**

Include package responsibility, subpackage table, hot paths, and explicit non-goals copied from the spec in concise form.

- [ ] **Step 7: Update `AGENTS.md` directory structure**

Add under `pkg/`:

```text
  clusterv2/             新版集群组合根：control/routing/net/slots/propose/channels/observe 分层，集成 controllerv2、slot/multiraft、channelv2
```

- [ ] **Step 8: Run tests and formatting**

Run:

```bash
gofmt -w pkg/clusterv2
go test ./pkg/clusterv2 -run 'TestPublicAPICompile|TestProposeTargetAllowsHashSlotZero' -count=1
git diff --check -- pkg/clusterv2 AGENTS.md
```

Expected: tests PASS; diff check has no output.

- [ ] **Step 9: Commit**

```bash
git add pkg/clusterv2 AGENTS.md
git commit -m "feat(clusterv2): scaffold public API"
```

---

### Task 2: Add Control Snapshot Abstraction And Static Controller

**Files:**
- Create: `pkg/clusterv2/control/controller.go`
- Create: `pkg/clusterv2/control/snapshot.go`
- Create: `pkg/clusterv2/control/static.go`
- Create: `pkg/clusterv2/control/control_test.go`

- [ ] **Step 1: Write failing control tests**

Create `pkg/clusterv2/control/control_test.go` with tests:

```go
package control

import (
    "context"
    "testing"
)

func TestSnapshotValidateRejectsInvalidHashSlotCoverage(t *testing.T) {
    snap := Snapshot{Revision: 1, HashSlots: HashSlotTable{Count: 2, Ranges: []HashSlotRange{{From: 0, To: 0, SlotID: 1}}}}
    if err := snap.Validate(); err == nil {
        t.Fatal("Validate() error = nil, want invalid coverage")
    }
}

func TestStaticControllerPublishesSnapshot(t *testing.T) {
    initial := validSnapshot()
    c := NewStaticController(initial)
    if err := c.Start(context.Background()); err != nil { t.Fatalf("Start() error = %v", err) }
    got, err := c.LocalSnapshot(context.Background())
    if err != nil { t.Fatalf("LocalSnapshot() error = %v", err) }
    if got.Revision != initial.Revision { t.Fatalf("revision = %d, want %d", got.Revision, initial.Revision) }

    next := initial
    next.Revision = 2
    if err := c.Publish(next); err != nil { t.Fatalf("Publish() error = %v", err) }
    select {
    case ev := <-c.Watch():
        if ev.Snapshot.Revision != 2 { t.Fatalf("event revision = %d, want 2", ev.Snapshot.Revision) }
    default:
        t.Fatal("missing snapshot event")
    }
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2/control -run 'TestSnapshot|TestStaticController' -count=1`

Expected: FAIL because package is empty.

- [ ] **Step 3: Implement snapshot DTOs and validation**

Create `snapshot.go` with exported structs and English comments:

```go
package control

type Role string
const (RoleController Role = "controller"; RoleData Role = "data")

type NodeStatus string
const (NodeAlive NodeStatus = "alive"; NodeSuspect NodeStatus = "suspect"; NodeDown NodeStatus = "down")

type Snapshot struct { Revision uint64; ControllerID uint64; Nodes []Node; Slots []SlotAssignment; HashSlots HashSlotTable; Tasks []ReconcileTask }
type Node struct { NodeID uint64; Addr string; Roles []Role; Status NodeStatus }
type SlotAssignment struct { SlotID uint32; DesiredPeers []uint64; ConfigEpoch uint64; PreferredLeader uint64 }
type HashSlotTable struct { Revision uint64; Count uint16; Ranges []HashSlotRange }
type HashSlotRange struct { From uint16; To uint16; SlotID uint32 }
type ReconcileTask struct { TaskID string; SlotID uint32; Kind string; TargetNode uint64; TargetPeers []uint64; ConfigEpoch uint64 }
```

Implement `Validate()` to reject zero revision when desired, zero hash-slot count, empty ranges, gaps, overlaps, decreasing ranges, zero slot IDs, duplicate nodes, duplicate slots, and desired peers that are not known data nodes.

- [ ] **Step 4: Implement Controller interface and static controller**

Create `controller.go` with `Controller`, `NodeReport`, `SlotRuntimeReport`, `SnapshotEvent`.

Create `static.go` with a mutex-protected `StaticController` for tests. `ReportNode` and `ReportSlots` should record last reports but not mutate snapshots.

- [ ] **Step 5: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/control
go test ./pkg/clusterv2/control -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/control
git commit -m "feat(clusterv2): add control snapshot abstraction"
```

---

### Task 3: Implement Atomic Routing Table

**Files:**
- Create: `pkg/clusterv2/routing/table.go`
- Create: `pkg/clusterv2/routing/router.go`
- Create: `pkg/clusterv2/routing/router_test.go`

- [ ] **Step 1: Write failing routing tests**

Tests must cover hash slot `0`, range expansion, route by key, route by explicit slot/hash-slot, no leader, and immutable old snapshots.

Use this shape in `routing/router_test.go`:

```go
func TestRouterRoutesHashSlotZero(t *testing.T) {
    r := NewRouter()
    r.UpdateControlSnapshot(testSnapshot())
    r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 2}})
    route, err := r.RouteHashSlot(0)
    require.NoError(t, err)
    require.Equal(t, uint16(0), route.HashSlot)
    require.Equal(t, uint32(1), route.SlotID)
    require.Equal(t, uint64(2), route.Leader)
}

func TestRouterOldSnapshotIsImmutable(t *testing.T) {
    r := NewRouter()
    r.UpdateControlSnapshot(testSnapshot())
    before := r.Table()
    next := testSnapshot()
    next.Revision++
    next.Slots[0].DesiredPeers[0] = 9
    r.UpdateControlSnapshot(next)
    require.NotEqual(t, before, r.Table())
    require.Equal(t, []uint64{1, 2, 3}, before.SlotPeers[1])
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2/routing -count=1`

Expected: FAIL because routing package is empty.

- [ ] **Step 3: Implement table builder**

Create `table.go` with immutable `Table`, `Route`, `SlotStatus`, and `BuildTable(snapshot control.Snapshot) (*Table, error)`. Expand ranges into `HashToSlot []uint32` with `len == HashSlotCount`.

- [ ] **Step 4: Implement atomic router**

Create `router.go` with:

```go
type Router struct { current atomic.Pointer[Table] }
func NewRouter() *Router
func (r *Router) Table() *Table
func (r *Router) RouteKey(key string) (Route, error)
func (r *Router) RouteHashSlot(hashSlot uint16) (Route, error)
func (r *Router) RouteSlot(slotID uint32, hashSlot uint16) (Route, error)
func (r *Router) UpdateControlSnapshot(snapshot control.Snapshot) error
func (r *Router) UpdateSlotLeaders(status []SlotStatus)
```

Use `crc32.ChecksumIEEE([]byte(key)) % uint32(table.HashSlotCount)` for key routing.

- [ ] **Step 5: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/routing
go test ./pkg/clusterv2/routing -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/routing
git commit -m "feat(clusterv2): add atomic routing table"
```

---

### Task 4: Add Lifecycle Helpers And Node Wiring With Fakes

**Files:**
- Create: `pkg/clusterv2/internal/lifecycle/group.go`
- Create: `pkg/clusterv2/internal/clock/clock.go`
- Modify: `pkg/clusterv2/node.go`
- Create/Modify: `pkg/clusterv2/node_test.go`

- [ ] **Step 1: Write failing lifecycle tests**

Create `pkg/clusterv2/node_test.go` with fake control/router/channels modules and tests that `Start` starts resources in order, `Stop` stops in reverse order, `Start` rejects invalid config, and a stopped node rejects foreground API calls with `ErrStopping`.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2 -run 'TestNodeStart|TestNodeStop|TestInvalidConfig' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement lifecycle group**

Create `internal/lifecycle/group.go`:

```go
package lifecycle

import "context"

type Resource interface { Start(context.Context) error; Stop(context.Context) error }
type NamedResource struct { Name string; Resource Resource }
type Group struct { started []NamedResource }
func (g *Group) Start(ctx context.Context, resources ...NamedResource) error { /* start in order; stop already-started on error */ return nil }
func (g *Group) Stop(ctx context.Context) error { /* stop reverse order; return joined error */ return nil }
```

- [ ] **Step 4: Wire minimal Node state**

Update `node.go` so `New` validates config, initializes atomic stopping/started flags, stores router/control placeholders where available, and API methods return typed errors until later modules are installed.

- [ ] **Step 5: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/internal/lifecycle pkg/clusterv2/internal/clock pkg/clusterv2/node.go pkg/clusterv2/node_test.go
go test ./pkg/clusterv2 -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/internal pkg/clusterv2/node.go pkg/clusterv2/node_test.go
git commit -m "feat(clusterv2): add node lifecycle shell"
```

---

### Task 5: Add clusternet Discovery, Local RPC, And Codec Base

**Files:**
- Create: `pkg/clusterv2/net/ids.go`
- Create: `pkg/clusterv2/net/discovery.go`
- Create: `pkg/clusterv2/net/codec.go`
- Create: `pkg/clusterv2/net/client.go`
- Create: `pkg/clusterv2/net/server.go`
- Create: `pkg/clusterv2/net/local.go`
- Create: `pkg/clusterv2/net/net_test.go`

- [ ] **Step 1: Write failing clusternet tests**

Tests:

- discovery atomically maps `nodeID -> addr` from control nodes;
- unknown node returns typed error;
- local RPC dispatches service IDs;
- codec rejects unknown version/truncated payload.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2/net -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement service IDs**

Create `ids.go` with package name `clusternet`:

```go
package clusternet

const (
    MsgSlotRaft uint8 = 32 + iota
    MsgSlotRaftBatch
)

const (
    RPCSlotForwardPropose uint8 = 1 + iota
    RPCChannelPull
    RPCChannelAck
    RPCChannelPullHint
    RPCChannelNotify
    RPCControlStateSync
    RPCControlReportNode
    RPCControlReportSlots
)
```

- [ ] **Step 4: Implement discovery and local RPC**

`Discovery` should hold an `atomic.Value` or `atomic.Pointer` to an immutable `map[uint64]string`. `LocalNetwork` should be a test-only in-memory network keyed by node ID with `Call(ctx, nodeID, serviceID, payload)`.

- [ ] **Step 5: Implement codec base**

Use small helpers:

```go
func PutHeader(buf []byte, version uint8, kind uint8) []byte
func CheckHeader(data []byte, wantVersion uint8, wantKind uint8) ([]byte, error)
```

- [ ] **Step 6: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/net
go test ./pkg/clusterv2/net -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/clusterv2/net
git commit -m "feat(clusterv2): add typed local network"
```

---

### Task 6: Implement Propose Codec And Service With Fake Slots

**Files:**
- Create: `pkg/clusterv2/propose/types.go`
- Create: `pkg/clusterv2/propose/codec.go`
- Create: `pkg/clusterv2/propose/service.go`
- Create: `pkg/clusterv2/propose/forward.go`
- Create: `pkg/clusterv2/propose/propose_test.go`
- Modify: `pkg/clusterv2/node.go`

- [ ] **Step 1: Write failing propose tests**

Tests:

- encodes `[version:1][hashSlot:2][command...]` and round-trips hash slot 0;
- local leader calls `SlotRuntime.Propose` once;
- remote leader calls forward client;
- remote not-leader returns typed error;
- missing key/hash-slot returns validation error;
- `Node.Propose` delegates to service.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2/propose ./pkg/clusterv2 -run 'Test.*Propose' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement propose dependencies and codec**

`types.go` should define small local interfaces:

```go
type Router interface { RouteKey(string) (Route, error); RouteHashSlot(uint16) (Route, error); RouteSlot(uint32, uint16) (Route, error) }
type SlotRuntime interface { Propose(context.Context, uint32, []byte) error; IsLocalLeader(uint32) bool }
type ForwardClient interface { ForwardPropose(context.Context, uint64, ForwardRequest) error }
```

Use DTOs internal to `propose` to avoid importing root `clusterv2` if it creates cycles. Root `Node` can adapt public DTOs.

- [ ] **Step 4: Implement service and forward handler**

`Service.Propose` normalizes target, reads route, encodes payload once, then chooses local or remote path. `ForwardHandler` decodes request and checks local leadership before calling slot runtime.

- [ ] **Step 5: Wire Node.Propose**

Update `node.go` to hold a `proposeService` interface and call it. Return `ErrNotStarted` if absent.

- [ ] **Step 6: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/propose pkg/clusterv2/node.go
go test ./pkg/clusterv2/propose ./pkg/clusterv2 -run 'Test.*Propose|TestPublicAPICompile' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/clusterv2/propose pkg/clusterv2/node.go pkg/clusterv2/api_test.go
git commit -m "feat(clusterv2): add propose service"
```

---

### Task 7: Implement Slot Reconciler With Fake Runtime, Then Multi-Raft Adapter

**Files:**
- Create: `pkg/clusterv2/slots/types.go`
- Create: `pkg/clusterv2/slots/manager.go`
- Create: `pkg/clusterv2/slots/reconciler.go`
- Create: `pkg/clusterv2/slots/observer.go`
- Create: `pkg/clusterv2/slots/runtime.go`
- Create: `pkg/clusterv2/slots/slots_test.go`

- [ ] **Step 1: Write failing slot manager/reconciler tests**

Tests:

- preferred leader is bootstrap owner;
- min desired peer is owner when no preferred leader;
- owner with empty hard state calls `BootstrapSlot`;
- non-owner with empty hard state does not open until bootstrap evidence exists;
- existing hard state always calls `OpenSlot`, never bootstrap;
- local node not in desired peers is marked unassigned and not deleted;
- status snapshot maps Multi-Raft status to routing slot leaders.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2/slots -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement slot types and fake-friendly manager**

Define interfaces around storage and runtime so tests do not require real disks:

```go
type StorageFactory func(slotID uint32) (multiraft.Storage, error)
type StateMachineFactory func(slotID uint32, hashSlots []uint16) (multiraft.StateMachine, error)
type Runtime interface { OpenSlot(context.Context, multiraft.SlotOptions) error; BootstrapSlot(context.Context, multiraft.BootstrapSlotRequest) error; Propose(context.Context, multiraft.SlotID, []byte) (multiraft.Future, error); Status(multiraft.SlotID) (multiraft.Status, error); Step(context.Context, multiraft.Envelope) error; CloseSlot(context.Context, multiraft.SlotID) error }
```

Use `storage.InitialState(ctx)` and `raft.IsEmptyHardState` just like `pkg/cluster/slot_manager.go`.

- [ ] **Step 4: Implement Reconciler**

`Reconciler.Reconcile(ctx, snapshot control.Snapshot)` should filter assignments to local node and call manager ensure. Keep destructive cleanup disabled in v1.

- [ ] **Step 5: Implement Multi-Raft adapter**

`runtime.go` should construct and own `multiraft.Runtime` with injected transport, storage factory, state machine factory, and log compaction config. Keep construction small and test with fakes where possible.

- [ ] **Step 6: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/slots
go test ./pkg/clusterv2/slots -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/clusterv2/slots
git commit -m "feat(clusterv2): add slot reconciliation runtime"
```

---

### Task 8: Add ControllerV2 Snapshot Adapter

**Files:**
- Create: `pkg/clusterv2/control/controllerv2.go`
- Create: `pkg/clusterv2/control/controllerv2_test.go`

- [ ] **Step 1: Write failing mapping tests**

Create tests that build `controllerv2/state.ClusterState` with nodes, controllers, slots, hash-slot ranges, and tasks, then verify conversion to `control.Snapshot`.

Also test:

- invalid ControllerV2 state is rejected;
- `ReportNode`/`ReportSlots` unsupported fields are explicit no-op/best-effort;
- `Watch` publishes after a state source update.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2/control -run 'TestControllerV2' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement pure mapper first**

Add:

```go
func SnapshotFromControllerV2(st state.ClusterState) (Snapshot, error)
```

Do not start Raft in this step. Keep mapping deterministic and validated.

- [ ] **Step 4: Implement adapter shell**

Define `ControllerV2Config`, `ControllerV2Adapter`, and a `StateSource` interface that can be backed by `controllerv2/server.Server` or a fake in tests. Start/Stop can be thin until integration tests require full Raft wiring.

- [ ] **Step 5: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/control
go test ./pkg/clusterv2/control -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/control/controllerv2.go pkg/clusterv2/control/controllerv2_test.go
git commit -m "feat(clusterv2): adapt controllerv2 snapshots"
```

---

### Task 9: Wire Node Control, Routing, Discovery, Slots, And Propose

**Files:**
- Modify: `pkg/clusterv2/node.go`
- Modify: `pkg/clusterv2/config.go`
- Modify: `pkg/clusterv2/net/discovery.go`
- Modify: `pkg/clusterv2/slots/reconciler.go`
- Modify: `pkg/clusterv2/propose/service.go`
- Create/Modify: `pkg/clusterv2/node_test.go`

- [ ] **Step 1: Write failing node wiring tests**

Tests with `control.StaticController`, fake slots runtime, fake forward client:

- `Start` waits initial snapshot, updates router, updates discovery, reconciles slots;
- `RouteKey` uses snapshot after start;
- control watch event updates route revision;
- `Propose` through Node reaches fake local slot runtime;
- `Snapshot()` reports readiness flags.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2 -run 'TestNode.*Route|TestNode.*Propose|TestNode.*Snapshot' -count=1`

Expected: FAIL.

- [ ] **Step 3: Add dependency injection to Config**

Add unexported or test-only options carefully. Prefer functional options if needed:

```go
type Option func(*Node)
func WithControllerForTest(c control.Controller) Option
func WithSlotRuntimeForTest(r slots.Runtime) Option
func WithForwardClientForTest(f propose.ForwardClient) Option
```

Keep production `Config` clean and documented.

- [ ] **Step 4: Wire control snapshot consumers**

On `Start`, call `control.LocalSnapshot`, validate, then update routing and discovery before starting slots. Start a watch goroutine later in observe task; for this task, a synchronous `applySnapshot` method is enough.

- [ ] **Step 5: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2
go test ./pkg/clusterv2 ./pkg/clusterv2/routing ./pkg/clusterv2/control ./pkg/clusterv2/propose ./pkg/clusterv2/slots -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2
git commit -m "feat(clusterv2): wire core node runtime"
```

---

### Task 10: Add Transport-Backed clusternet Server And Client

**Files:**
- Modify: `pkg/clusterv2/net/client.go`
- Modify: `pkg/clusterv2/net/server.go`
- Create: `pkg/clusterv2/net/transport.go`
- Modify: `pkg/clusterv2/net/net_test.go`

- [ ] **Step 1: Write failing loopback transport tests**

Test two loopback nodes using `pkg/transport.Server`, `RPCMux`, and `Pool`. Register an echo handler for `RPCSlotForwardPropose` and assert client receives response.

- [ ] **Step 2: Run test and verify failure**

Run: `go test ./pkg/clusterv2/net -run TestTransportLoopbackRPC -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement transport server/client**

Use existing `pkg/transport.NewServerWithConfig`, `HandleRPCMux`, `RPCMux.Register` style from `pkg/transport/rpcmux.go`. Keep logical pools in config even if first implementation shares one physical pool.

- [ ] **Step 4: Add max payload/deadline guards**

Handlers should reject oversized payloads and respect context cancellation.

- [ ] **Step 5: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/net
go test ./pkg/clusterv2/net -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/net
git commit -m "feat(clusterv2): add transport backed RPC"
```

---

### Task 11: Integrate ChannelV2 Service, Static Meta, And Replication RPC

**Files:**
- Create: `pkg/clusterv2/channels/service.go`
- Create: `pkg/clusterv2/channels/resolver.go`
- Create: `pkg/clusterv2/channels/codec.go`
- Create: `pkg/clusterv2/channels/transport.go`
- Create: `pkg/clusterv2/channels/lifecycle.go`
- Create: `pkg/clusterv2/channels/channels_test.go`
- Modify: `pkg/clusterv2/node.go`

- [ ] **Step 1: Write failing channel integration tests**

Tests:

- `StaticMetaSource` resolves active metadata;
- resolver derives key when absent;
- service stores a combined interface with both `channelv2.Cluster` and `channelv2/transport.Server` behavior;
- Pull/Ack/PullHint/Notify codecs round-trip;
- transport client dispatches to local test network service IDs;
- `Node.AppendChannel` delegates to channels service.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2/channels ./pkg/clusterv2 -run 'Test.*Channel|Test.*Meta' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement metadata source and resolver**

`resolver.go` should define:

```go
type ChannelMetaSource interface { ResolveChannelMeta(context.Context, channelv2.ChannelID) (channelv2.Meta, error) }
type StaticMetaSource struct { /* immutable map keyed by channelv2.ChannelID */ }
```

- [ ] **Step 4: Implement combined runtime interface**

Because `channelv2.Cluster` does not expose replication handlers, store:

```go
type channelRuntime interface {
    channelv2.Cluster
    channeltransport.Server
}
```

If `service.New` returns only `channelv2.Cluster`, use a constructor adapter in `channels` that type-asserts to `channeltransport.Server` and returns a clear error if not satisfied.

- [ ] **Step 5: Implement channel codecs and transport**

Use versioned binary where practical. If using JSON temporarily, prefix with a version byte and add a comment that this is a v1 implementation step to be optimized.

- [ ] **Step 6: Wire Node Channel API**

Update `Node.AppendChannel`, `AppendChannelBatch`, and `FetchChannel` to call `channels.Service`. Return `ErrNotStarted` if missing and `ErrStopping` after stop begins.

- [ ] **Step 7: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/channels pkg/clusterv2/node.go
go test ./pkg/clusterv2/channels ./pkg/clusterv2 -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/clusterv2/channels pkg/clusterv2/node.go
git commit -m "feat(clusterv2): integrate channelv2 service"
```

---

### Task 12: Add Observe Loops And Readiness Aggregation

**Files:**
- Create: `pkg/clusterv2/observe/loops.go`
- Create: `pkg/clusterv2/observe/reporter.go`
- Create: `pkg/clusterv2/observe/snapshot.go`
- Create: `pkg/clusterv2/observe/observe_test.go`
- Modify: `pkg/clusterv2/node.go`

- [ ] **Step 1: Write failing observe tests**

Tests with fake clock/ticker:

- loop calls function until stopped;
- snapshot event triggers `applySnapshot` once after debounce;
- slot status loop updates routing leaders;
- channel tick loop calls channel runtime;
- report errors are retried and do not clear readiness.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test ./pkg/clusterv2/observe ./pkg/clusterv2 -run 'Test.*Loop|Test.*Readiness' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement loop runner**

Use a small `Loop` with `Start(ctx)` and `Stop()`; do not create a generic event bus.

- [ ] **Step 4: Implement reporters and readiness snapshot**

Reporters call `control.ReportNode` and `control.ReportSlots` best-effort. Readiness is aggregated from control snapshot present, route table present, local slot reconcile done, and channel service constructed.

- [ ] **Step 5: Wire loops into Node**

Start observe loops after channels service starts. Stop loops before closing channels/slots/control/net.

- [ ] **Step 6: Run tests**

Run:

```bash
gofmt -w pkg/clusterv2/observe pkg/clusterv2/node.go
go test ./pkg/clusterv2/observe ./pkg/clusterv2 -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/clusterv2/observe pkg/clusterv2/node.go
git commit -m "feat(clusterv2): add observation loops"
```

---

### Task 13: Add In-Process Three-Node Slot Propose Smoke Test

**Files:**
- Create: `pkg/clusterv2/integration_test.go`
- Modify as needed: `pkg/clusterv2/node.go`, `pkg/clusterv2/net/local.go`, `pkg/clusterv2/slots/runtime.go`, `pkg/clusterv2/propose/forward.go`

- [ ] **Step 1: Write failing three-node propose test**

Create `TestClusterV2ThreeNodeSlotPropose` using in-process local network and static control snapshot:

```go
func TestClusterV2ThreeNodeSlotPropose(t *testing.T) {
    h := newThreeNodeHarness(t)
    h.Start(t)
    t.Cleanup(func() { h.Stop(t) })

    h.WaitSlotLeader(t, 1)
    nonLeader := h.NonLeaderForSlot(t, 1)
    err := nonLeader.Propose(context.Background(), clusterv2.ProposeRequest{Key: "user-a", Command: []byte("cmd")})
    require.NoError(t, err)
    h.RequireCommandApplied(t, 1, []byte("cmd"))
}
```

Use a fake state machine first if real Slot FSM command encoding is too much for this task. The test's purpose is clusterv2 route/forward/runtime integration.

- [ ] **Step 2: Run test and verify failure**

Run: `go test ./pkg/clusterv2 -run TestClusterV2ThreeNodeSlotPropose -count=1`

Expected: FAIL until harness/runtime wiring is complete.

- [ ] **Step 3: Implement test harness**

Keep harness in `_test.go` unless reused by many packages. Use temp dirs per node and loopback/in-memory network.

- [ ] **Step 4: Fix runtime/forward wiring until test passes**

Make the smallest production changes needed. Do not introduce migration, repair, or admin APIs.

- [ ] **Step 5: Run focused integration test**

Run:

```bash
go test ./pkg/clusterv2 -run TestClusterV2ThreeNodeSlotPropose -count=1
```

Expected: PASS within a few seconds.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2
git commit -m "test(clusterv2): cover three-node slot propose"
```

---

### Task 14: Add Three-Node ChannelV2 Quorum Append Smoke Test

**Files:**
- Modify: `pkg/clusterv2/integration_test.go`
- Modify as needed: `pkg/clusterv2/channels/*`, `pkg/clusterv2/net/*`, `pkg/clusterv2/node.go`

- [ ] **Step 1: Write failing channel quorum test**

Add `TestClusterV2ThreeNodeChannelAppendQuorum`:

```go
func TestClusterV2ThreeNodeChannelAppendQuorum(t *testing.T) {
    h := newThreeNodeHarness(t)
    h.WithStaticChannelMeta(channelv2.Meta{
        Key:         channelv2.ChannelKey("1:room-a"),
        ID:          channelv2.ChannelID{ID: "room-a", Type: 1},
        Epoch:       1,
        LeaderEpoch: 1,
        Leader:      1,
        Replicas:    []channelv2.NodeID{1, 2, 3},
        ISR:         []channelv2.NodeID{1, 2, 3},
        MinISR:      2,
        Status:      channelv2.StatusActive,
    })
    h.Start(t)
    t.Cleanup(func() { h.Stop(t) })

    res, err := h.Node(1).AppendChannel(context.Background(), channelv2.AppendRequest{
        ChannelID: channelv2.ChannelID{ID: "room-a", Type: 1},
        CommitMode: channelv2.CommitModeQuorum,
        Message: channelv2.Message{MessageID: 1001, Payload: []byte("hello")},
        ExpectedChannelEpoch: 1,
        ExpectedLeaderEpoch: 1,
    })
    require.NoError(t, err)
    require.NotZero(t, res.MessageSeq)

    h.WaitChannelCommitted(t, 2, channelv2.ChannelID{ID: "room-a", Type: 1}, res.MessageSeq)
    fetched, err := h.Node(2).FetchChannel(context.Background(), channelv2.FetchRequest{ChannelID: channelv2.ChannelID{ID: "room-a", Type: 1}, FromSeq: 1, Limit: 10})
    require.NoError(t, err)
    require.Len(t, fetched.Messages, 1)
}
```

- [ ] **Step 2: Run test and verify failure**

Run: `go test ./pkg/clusterv2 -run TestClusterV2ThreeNodeChannelAppendQuorum -count=1`

Expected: FAIL until channel service and transport are fully wired.

- [ ] **Step 3: Complete ChannelV2 wiring**

Ensure each node starts its channel service with per-node store factory and clusternet transport. Ensure PullHint can lazy-load follower metadata through `StaticMetaSource`.

- [ ] **Step 4: Run focused test**

Run:

```bash
go test ./pkg/clusterv2 -run TestClusterV2ThreeNodeChannelAppendQuorum -count=1
```

Expected: PASS within a few seconds.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2
git commit -m "test(clusterv2): cover channelv2 quorum append"
```

---

### Task 15: Add Benchmarks, FLOW Updates, And Final Verification

**Files:**
- Create: `pkg/clusterv2/bench_test.go`
- Modify: `pkg/clusterv2/FLOW.md`
- Modify as needed: `docs/superpowers/plans/2026-05-25-clusterv2-implementation.md` only if implementation discoveries require plan corrections.

- [ ] **Step 1: Add focused benchmarks**

Create benchmarks:

```go
func BenchmarkRouteKey(b *testing.B) { /* prebuilt table, call RouteKey */ }
func BenchmarkLocalPropose(b *testing.B) { /* fake local runtime */ }
func BenchmarkForwardPropose(b *testing.B) { /* local network forward */ }
func BenchmarkChannelAppendLocal(b *testing.B) { /* channelv2 local commit mode */ }
```

Do not add long-running benchmarks to normal tests.

- [ ] **Step 2: Update `pkg/clusterv2/FLOW.md`**

Ensure it documents:

- responsibilities and non-goals;
- subpackage boundaries;
- start/stop order;
- Propose flow;
- ChannelV2 append/replication flow;
- known v1 limitations.

- [ ] **Step 3: Run package tests**

Run:

```bash
go test ./pkg/clusterv2/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Run related package tests**

Run:

```bash
go test ./pkg/controllerv2/... ./pkg/slot/... ./pkg/channelv2/... ./pkg/transport/... -count=1
```

Expected: PASS. If this is too slow locally, at minimum run the packages touched by imports and document skipped packages in final handoff.

- [ ] **Step 5: Run targeted benchmarks once**

Run:

```bash
go test ./pkg/clusterv2 -bench 'Benchmark(RouteKey|LocalPropose|ForwardPropose|ChannelAppendLocal)' -benchtime=100ms -run '^$'
```

Expected: benchmarks execute without panics.

- [ ] **Step 6: Diff and status checks**

Run:

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; only intentional files are modified. Do not include unrelated `pkg/channelv2/FLOW.md` changes unless the user explicitly asks.

- [ ] **Step 7: Commit final polish**

```bash
git add pkg/clusterv2 docs/superpowers/plans/2026-05-25-clusterv2-implementation.md
git commit -m "docs(clusterv2): document runtime flow"
```

If there are code changes in this task, use a `feat` or `test` commit message instead of `docs`.

---

## Execution Order Summary

1. Scaffold API/config/errors/docs.
2. Add control snapshot and static controller.
3. Add routing.
4. Add lifecycle and Node shell.
5. Add clusternet local RPC/codec.
6. Add propose service.
7. Add slots manager/reconciler/runtime adapter.
8. Add ControllerV2 snapshot adapter.
9. Wire Node core runtime.
10. Add transport-backed clusternet.
11. Add ChannelV2 integration.
12. Add observe loops.
13. Add three-node Slot propose smoke.
14. Add three-node ChannelV2 quorum append smoke.
15. Add benchmarks and final docs.

## Final Acceptance Checklist

- [x] `GOWORK=off go test ./pkg/clusterv2/... -count=1` passes.
- [x] `GOWORK=off go test ./pkg/controllerv2/... ./pkg/slot/... ./pkg/channelv2/... ./pkg/transport/... -count=1` passes.
- [x] `TestClusterV2ThreeNodeSlotPropose` passes.
- [x] `TestClusterV2ThreeNodeChannelAppendQuorum` passes.
- [x] `BenchmarkRouteKey` and focused benchmarks run without panic.
- [x] `pkg/clusterv2/FLOW.md` matches implemented behavior.
- [x] `AGENTS.md` includes `pkg/clusterv2` directory structure.
- [x] No old `pkg/cluster` code is modified.
- [x] No unrelated dirty file, especially `pkg/channelv2/FLOW.md`, is committed by accident.
