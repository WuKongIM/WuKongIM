# Raftcluster Split Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `pkg/cluster/raftcluster` with focused cluster packages and migrate all callers to the new names and narrower capability interfaces in one non-compatible change.

**Architecture:** Extract five packages with clear ownership: `nodediscovery` for endpoint lookup, `grouprouting` for slotting and leader queries, `raftnode` for node-local raft runtime and forwarding, `controllerclient` for controller RPC, and `groupreconcile` for assignment execution. Keep `internal/app` as the only composition root and update `metastore`, `internal/access/node`, and app helpers to depend on exact capabilities rather than a single broad cluster facade.

**Tech Stack:** Go 1.23, `pkg/cluster/*`, `pkg/replication/multiraft`, `pkg/transport/nodetransport`, `pkg/storage/controllermeta`, `internal/app`, `pkg/storage/metastore`, `internal/access/node`, `testing`, `testify`.

---

## File Structure

### New packages

- Create: `pkg/cluster/nodediscovery/discovery.go`
- Create: `pkg/cluster/nodediscovery/static.go`
- Create: `pkg/cluster/nodediscovery/static_test.go`
- Create: `pkg/cluster/grouprouting/router.go`
- Create: `pkg/cluster/grouprouting/runtime_peers.go`
- Create: `pkg/cluster/grouprouting/router_test.go`
- Create: `pkg/cluster/raftnode/config.go`
- Create: `pkg/cluster/raftnode/node.go`
- Create: `pkg/cluster/raftnode/transport.go`
- Create: `pkg/cluster/raftnode/forward.go`
- Create: `pkg/cluster/raftnode/forward_codec.go`
- Create: `pkg/cluster/raftnode/errors.go`
- Create: `pkg/cluster/raftnode/config_test.go`
- Create: `pkg/cluster/raftnode/transport_test.go`
- Create: `pkg/cluster/raftnode/forward_test.go`
- Create: `pkg/cluster/raftnode/codec_test.go`
- Create: `pkg/cluster/raftnode/cluster_internal_test.go`
- Create: `pkg/cluster/raftnode/cluster_test.go`
- Create: `pkg/cluster/raftnode/stress_test.go`
- Create: `pkg/cluster/raftnode/debug_retry_count_test.go`
- Create: `pkg/cluster/controllerclient/client.go`
- Create: `pkg/cluster/controllerclient/operator.go`
- Create: `pkg/cluster/controllerclient/timeouts.go`
- Create: `pkg/cluster/controllerclient/errors.go`
- Create: `pkg/cluster/controllerclient/client_internal_test.go`
- Create: `pkg/cluster/groupreconcile/agent.go`
- Create: `pkg/cluster/groupreconcile/assignment_cache.go`
- Create: `pkg/cluster/groupreconcile/managed_groups.go`
- Create: `pkg/cluster/groupreconcile/readiness.go`
- Create: `pkg/cluster/groupreconcile/agent_internal_test.go`

### App and caller migration

- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/app/multinode_integration_test.go`
- Modify: `internal/app/integration_test.go`
- Modify: `internal/app/messagerouting.go`
- Modify: `internal/app/presenceauthority.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/channelmeta_test.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/client.go`
- Modify: `internal/access/node/presence_rpc.go`
- Modify: `internal/access/node/presence_rpc_test.go`
- Modify: `pkg/storage/metastore/store.go`
- Modify: `pkg/storage/metastore/authoritative_rpc.go`
- Modify: `pkg/storage/metastore/runtime_meta_rpc.go`
- Modify: `pkg/storage/metastore/authoritative_rpc_test.go`
- Modify: `pkg/storage/metastore/integration_test.go`
- Modify: `pkg/storage/metastore/testutil_test.go`

### Remove legacy package

- Delete: `pkg/cluster/raftcluster/agent.go`
- Delete: `pkg/cluster/raftcluster/assignment_cache.go`
- Delete: `pkg/cluster/raftcluster/cluster.go`
- Delete: `pkg/cluster/raftcluster/codec.go`
- Delete: `pkg/cluster/raftcluster/config.go`
- Delete: `pkg/cluster/raftcluster/controller_client.go`
- Delete: `pkg/cluster/raftcluster/discovery.go`
- Delete: `pkg/cluster/raftcluster/errors.go`
- Delete: `pkg/cluster/raftcluster/forward.go`
- Delete: `pkg/cluster/raftcluster/managed_groups.go`
- Delete: `pkg/cluster/raftcluster/operator.go`
- Delete: `pkg/cluster/raftcluster/readiness.go`
- Delete: `pkg/cluster/raftcluster/router.go`
- Delete: `pkg/cluster/raftcluster/static_discovery.go`
- Delete: migrated tests from `pkg/cluster/raftcluster/*.go`

### Key API targets

- `raftcluster.Cluster` -> `raftnode.Node`
- `raftcluster.Config` -> `raftnode.Config`
- `raftcluster.NewCluster` -> `raftnode.New`
- `raftcluster.NodeConfig` -> `nodediscovery.NodeEndpoint`
- `raftcluster.GroupConfig` -> `raftnode.SeedGroup`
- `raftcluster.NewStaticDiscovery` -> `nodediscovery.NewStatic`
- `App.Cluster()` -> `App.RaftNode()`

### Compatibility rule

- Do **not** leave a wrapper `pkg/cluster/raftcluster`
- Do **not** re-hide `controllerclient.Client` or `groupreconcile.Agent` inside `raftnode.Node`
- Do **not** keep callers importing a broad cluster package when a local interface is enough

### Task 1: Extract `nodediscovery`

**Files:**
- Create: `pkg/cluster/nodediscovery/discovery.go`
- Create: `pkg/cluster/nodediscovery/static.go`
- Create: `pkg/cluster/nodediscovery/static_test.go`
- Modify: `internal/app/lifecycle.go`
- Test: `pkg/cluster/nodediscovery/static_test.go`

- [ ] **Step 1: Write the failing discovery tests**

```go
package nodediscovery

func TestStaticResolve(t *testing.T) {
    d := NewStatic([]NodeEndpoint{{NodeID: 1, Addr: "127.0.0.1:1111"}})
    addr, err := d.Resolve(1)
    require.NoError(t, err)
    require.Equal(t, "127.0.0.1:1111", addr)
}

func TestStaticGetNodesReturnsCopy(t *testing.T) {
    d := NewStatic([]NodeEndpoint{{NodeID: 1, Addr: "127.0.0.1:1111"}})
    nodes := d.GetNodes()
    nodes[0].Addr = "mutated"
    require.Equal(t, "127.0.0.1:1111", d.GetNodes()[0].Addr)
}
```

- [ ] **Step 2: Run the tests to verify RED**

Run: `go test ./pkg/cluster/nodediscovery -run 'TestStatic(Resolve|GetNodesReturnsCopy)'`
Expected: FAIL because `pkg/cluster/nodediscovery` and `NewStatic` do not exist yet.

- [ ] **Step 3: Implement `Discovery`, `NodeEndpoint`, and `Static`**

```go
type NodeEndpoint struct {
    NodeID multiraft.NodeID
    Addr   string
}

type Discovery interface {
    Resolve(nodeID uint64) (string, error)
    GetNodes() []NodeEndpoint
    Stop()
}
```

Use the existing `raftcluster/discovery.go` and `raftcluster/static_discovery.go` code as the starting point, rename the package, and add a constructor:

```go
func NewStatic(endpoints []NodeEndpoint) *Static
```

- [ ] **Step 4: Update the one independent caller that only needs static discovery construction**

Replace `raftcluster.NewStaticDiscovery(a.cfg.Cluster.runtimeNodes())` in `internal/app/lifecycle.go` with `nodediscovery.NewStatic(a.cfg.Cluster.runtimeNodes())`.

- [ ] **Step 5: Run the package test and app lifecycle test**

Run: `go test ./pkg/cluster/nodediscovery ./internal/app -run 'TestStatic(Resolve|GetNodesReturnsCopy)|TestStartChannelMetaSyncUsesExplicitDataPlaneSettings'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/nodediscovery internal/app/lifecycle.go
git commit -m "refactor: extract node discovery package"
```

### Task 2: Extract `grouprouting`

**Files:**
- Create: `pkg/cluster/grouprouting/router.go`
- Create: `pkg/cluster/grouprouting/runtime_peers.go`
- Create: `pkg/cluster/grouprouting/router_test.go`
- Modify: `pkg/cluster/raftcluster/router_test.go` only long enough to copy assertions, then delete it later
- Test: `pkg/cluster/grouprouting/router_test.go`

- [ ] **Step 1: Write the failing router tests**

```go
package grouprouting

func TestSlotForKeyIsStableForGroupCount(t *testing.T) {
    r := New(8, 1, nil)
    require.Equal(t, multiraft.GroupID(3), r.SlotForKey("alpha"))
}

func TestIsLocalUsesConfiguredNode(t *testing.T) {
    r := New(8, 9, nil)
    require.True(t, r.IsLocal(9))
    require.False(t, r.IsLocal(8))
}
```

Also add a table-driven leader lookup test using a small fake runtime view interface instead of `*multiraft.Runtime` directly.

- [ ] **Step 2: Run the tests to verify RED**

Run: `go test ./pkg/cluster/grouprouting -run 'Test(SlotForKeyIsStableForGroupCount|IsLocalUsesConfiguredNode|LeaderOf)'`
Expected: FAIL because the package and constructor do not exist yet.

- [ ] **Step 3: Implement the new router package around a narrow runtime interface**

```go
type RuntimeView interface {
    Status(groupID multiraft.GroupID) (multiraft.Status, error)
}

type Router struct {
    groupCount uint32
    runtime    RuntimeView
    localNode  multiraft.NodeID
}
```

Move the CRC32 slotting logic from `pkg/cluster/raftcluster/router.go`, keep the same slot semantics, and move runtime-peer tracking helpers out of `cluster.go` into `runtime_peers.go` so `raftnode.Node` can embed or delegate to them.

- [ ] **Step 4: Run the router tests**

Run: `go test ./pkg/cluster/grouprouting -run 'Test(SlotForKeyIsStableForGroupCount|IsLocalUsesConfiguredNode|LeaderOf)'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/cluster/grouprouting
git commit -m "refactor: extract group routing package"
```

### Task 3: Create `raftnode` and move node runtime behavior

**Files:**
- Create: `pkg/cluster/raftnode/config.go`
- Create: `pkg/cluster/raftnode/node.go`
- Create: `pkg/cluster/raftnode/transport.go`
- Create: `pkg/cluster/raftnode/forward.go`
- Create: `pkg/cluster/raftnode/forward_codec.go`
- Create: `pkg/cluster/raftnode/errors.go`
- Create: `pkg/cluster/raftnode/config_test.go`
- Create: `pkg/cluster/raftnode/transport_test.go`
- Create: `pkg/cluster/raftnode/forward_test.go`
- Create: `pkg/cluster/raftnode/codec_test.go`
- Create: `pkg/cluster/raftnode/cluster_internal_test.go`
- Create: `pkg/cluster/raftnode/cluster_test.go`
- Create: `pkg/cluster/raftnode/stress_test.go`
- Create: `pkg/cluster/raftnode/debug_retry_count_test.go`
- Modify: `internal/app/build.go`
- Test: `pkg/cluster/raftnode/*_test.go`

- [ ] **Step 1: Copy the existing config and runtime tests into the new package and make them fail on missing names**

Start by moving the existing `config_test.go`, `transport_test.go`, `forward_test.go`, `codec_test.go`, `cluster_internal_test.go`, `cluster_test.go`, `stress_test.go`, and `debug_retry_count_test.go` into `pkg/cluster/raftnode`, then mechanically update imports and references from `raftcluster` to `raftnode`, `nodediscovery`, and `grouprouting`.

- [ ] **Step 2: Run the new-package tests to verify RED**

Run: `go test ./pkg/cluster/raftnode -run 'Test(Config|RaftTransport|Forward|RaftBody|TestNode|ThreeNode|Stress|DebugRetry)'`
Expected: FAIL with undefined `Node`, `Config`, `New`, and renamed dependency symbols.

- [ ] **Step 3: Implement `raftnode.Config`, `SeedGroup`, and `Node` by moving runtime code out of `raftcluster`**

```go
type Config struct {
    NodeID             multiraft.NodeID
    ListenAddr         string
    GroupCount         uint32
    ControllerMetaPath string
    ControllerRaftPath string
    ControllerReplicaN int
    GroupReplicaN      int
    NewStorage         func(groupID multiraft.GroupID) (multiraft.Storage, error)
    NewStateMachine    func(groupID multiraft.GroupID) (multiraft.StateMachine, error)
    Nodes              []nodediscovery.NodeEndpoint
    Groups             []SeedGroup
    // timeouts and worker counts unchanged
}

type Node struct {
    cfg       Config
    router    *grouprouting.Router
    discovery *nodediscovery.Static
    // runtime, server, pools, controller-raft service, rpc mux, etc.
}
```

Move the current `Cluster` implementation into `Node`, keep controller-raft startup inside `raftnode`, and rename `NewCluster` to `New`.

- [ ] **Step 4: Update app construction to use `raftnode` types**

Change `internal/app/build.go` so `runtimeConfig()` returns `raftnode.Config`, `runtimeNodes()` returns `[]nodediscovery.NodeEndpoint`, and `build()` creates `app.raftNode` with `raftnode.New(...)`.

- [ ] **Step 5: Run the new raftnode tests and the app build smoke tests**

Run: `go test ./pkg/cluster/raftnode ./internal/app -run 'Test(Config|RaftTransport|Forward|RaftBody|TestNewBuilds|TestAccessorsExposeBuiltRuntime)'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/raftnode internal/app/build.go
git commit -m "refactor: move cluster node runtime into raftnode"
```

### Task 4: Extract `controllerclient`

**Files:**
- Create: `pkg/cluster/controllerclient/client.go`
- Create: `pkg/cluster/controllerclient/operator.go`
- Create: `pkg/cluster/controllerclient/timeouts.go`
- Create: `pkg/cluster/controllerclient/errors.go`
- Create: `pkg/cluster/controllerclient/client_internal_test.go`
- Modify: `pkg/cluster/raftnode/node.go`
- Test: `pkg/cluster/controllerclient/client_internal_test.go`

- [ ] **Step 1: Move the current controller client tests into the new package and update names**

Copy `pkg/cluster/raftcluster/controller_client_internal_test.go` to `pkg/cluster/controllerclient/client_internal_test.go` and update helper fakes so they target a narrow caller interface instead of `*raftcluster.Cluster`.

- [ ] **Step 2: Run the tests to verify RED**

Run: `go test ./pkg/cluster/controllerclient -run 'TestClusterGetReconcileTaskFallsThroughSlowStaleLeaderToCurrentLeader'`
Expected: FAIL because `Client` and its constructor do not exist.

- [ ] **Step 3: Implement the client around a minimal transport interface**

```go
type RPCTransport interface {
    RPCService(ctx context.Context, nodeID multiraft.NodeID, groupID multiraft.GroupID, serviceID uint8, payload []byte) ([]byte, error)
}

type LocalFallback interface {
    ControllerMeta() *controllermeta.Store
    ControllerNodeID() multiraft.NodeID
}
```

Move request/response structs, leader cache, retry logic, and operator methods out of `raftcluster`. Keep retry classification in this package rather than in `raftnode`.

- [ ] **Step 4: Replace direct controller-client ownership inside `raftnode.Node`**

Delete `controllerClient` and `agent` fields from the node type. `raftnode.Node` should expose only the pieces the app composition root needs to build `controllerclient.Client` and `groupreconcile.Agent`.

- [ ] **Step 5: Run the controllerclient tests and a focused app compile check**

Run: `go test ./pkg/cluster/controllerclient ./internal/app -run 'TestClusterGetReconcileTaskFallsThroughSlowStaleLeaderToCurrentLeader|TestNewBuildsDBClusterStoreMessageAndGatewayAdapter'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/controllerclient pkg/cluster/raftnode/node.go
git commit -m "refactor: extract controller client package"
```

### Task 5: Extract `groupreconcile`

**Files:**
- Create: `pkg/cluster/groupreconcile/agent.go`
- Create: `pkg/cluster/groupreconcile/assignment_cache.go`
- Create: `pkg/cluster/groupreconcile/managed_groups.go`
- Create: `pkg/cluster/groupreconcile/readiness.go`
- Create: `pkg/cluster/groupreconcile/agent_internal_test.go`
- Modify: `pkg/cluster/raftnode/node.go`
- Modify: `internal/app/build.go`
- Test: `pkg/cluster/groupreconcile/agent_internal_test.go`

- [ ] **Step 1: Copy the managed-group and agent tests into the new package**

Move the reconciliation-related tests out of `pkg/cluster/raftcluster/cluster_internal_test.go` into `pkg/cluster/groupreconcile/agent_internal_test.go`, keeping only node-startup tests in `pkg/cluster/raftnode/cluster_internal_test.go`.

- [ ] **Step 2: Run the new reconciliation test slice to verify RED**

Run: `go test ./pkg/cluster/groupreconcile -run 'Test(GroupAgent|ManagedGroups|WaitForManaged)'`
Expected: FAIL because `Agent`, `assignmentCache`, and managed-group helpers do not exist yet.

- [ ] **Step 3: Implement the reconcile package against narrow interfaces**

```go
type RuntimeNode interface {
    LocalNodeID() multiraft.NodeID
    Runtime() *multiraft.Runtime
    Router() *grouprouting.Router
    OpenSeedGroup(ctx context.Context, g raftnode.SeedGroup) error
    RPCService(ctx context.Context, nodeID multiraft.NodeID, groupID multiraft.GroupID, serviceID uint8, payload []byte) ([]byte, error)
    ControllerMeta() *controllermeta.Store
}

type ControllerClient interface {
    Report(ctx context.Context, report groupcontroller.AgentReport) error
    ListNodes(ctx context.Context) ([]controllermeta.ClusterNode, error)
    RefreshAssignments(ctx context.Context) ([]controllermeta.GroupAssignment, error)
    ListRuntimeViews(ctx context.Context) ([]controllermeta.GroupRuntimeView, error)
    GetTask(ctx context.Context, groupID uint32) (controllermeta.ReconcileTask, error)
    ReportTaskResult(ctx context.Context, task controllermeta.ReconcileTask, taskErr error) error
}
```

Move assignment caching, heartbeat/report loops, managed-group open/bootstrap/close logic, readiness checks, and test hooks into this package.

- [ ] **Step 4: Wire the new agent from `internal/app/build.go` and `internal/app/lifecycle.go`**

Add `app.controllerClient` and `app.groupReconciler` fields, create them in `build()`, start and stop the reconciler explicitly from lifecycle code, and replace `cluster.WaitForManagedGroupsReady` calls with `groupReconciler.WaitForManagedGroupsReady`.

- [ ] **Step 5: Run reconciliation and lifecycle tests**

Run: `go test ./pkg/cluster/groupreconcile ./internal/app -run 'Test(GroupAgent|ManagedGroups|WaitForManaged)|TestStartStartsClusterBeforeGateway|TestStartStartsAPIAfterGatewayWhenEnabled'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/groupreconcile internal/app/build.go internal/app/lifecycle.go internal/app/app.go
git commit -m "refactor: extract managed-group reconcile package"
```

### Task 6: Rewire `internal/app` around explicit composition

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/app/multinode_integration_test.go`
- Modify: `internal/app/integration_test.go`
- Modify: `internal/app/messagerouting.go`
- Modify: `internal/app/presenceauthority.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/channelmeta_test.go`

- [ ] **Step 1: Add the new app fields and accessor renames in tests first**

Update app tests to target:

```go
require.NotNil(t, app.RaftNode())
require.Same(t, app.raftNode, app.RaftNode())
```

Also add assertions for explicit composition fields when appropriate:

```go
require.NotNil(t, app.controllerClient)
require.NotNil(t, app.groupReconciler)
```

- [ ] **Step 2: Run the app test slice to verify RED**

Run: `go test ./internal/app -run 'Test(NewBuilds|AccessorsExposeBuiltRuntime|StartStartsClusterBeforeGateway|StartChannelMetaSyncUsesExplicitDataPlaneSettings)'`
Expected: FAIL because `RaftNode()` and the new fields do not exist yet.

- [ ] **Step 3: Implement explicit app composition**

Rename the app field:

```go
raftNode         *raftnode.Node
controllerClient *controllerclient.Client
groupReconciler  *groupreconcile.Agent
```

Update all call sites in:

- `internal/app/messagerouting.go`
- `internal/app/presenceauthority.go`
- `internal/app/channelmeta.go`
- `internal/app/integration_test.go`
- `internal/app/multinode_integration_test.go`

Use `raftnode.Node` only for routing/propose/RPC behavior, and move managed-group readiness to the reconciler object.

- [ ] **Step 4: Run the app package tests**

Run: `go test ./internal/app`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app
git commit -m "refactor: make app compose cluster capabilities explicitly"
```

### Task 7: Migrate `internal/access/node` and `metastore` to precise interfaces

**Files:**
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/client.go`
- Modify: `internal/access/node/presence_rpc.go`
- Modify: `internal/access/node/presence_rpc_test.go`
- Modify: `pkg/storage/metastore/store.go`
- Modify: `pkg/storage/metastore/authoritative_rpc.go`
- Modify: `pkg/storage/metastore/runtime_meta_rpc.go`
- Modify: `pkg/storage/metastore/authoritative_rpc_test.go`
- Modify: `pkg/storage/metastore/integration_test.go`
- Modify: `pkg/storage/metastore/testutil_test.go`

- [ ] **Step 1: Add minimal local interfaces in the consumers and update tests first**

In `pkg/storage/metastore/store.go` define:

```go
type ShardRuntime interface {
    SlotForKey(key string) multiraft.GroupID
    Propose(ctx context.Context, groupID multiraft.GroupID, cmd []byte) error
    RPCMux() *nodetransport.RPCMux
    RPCService(ctx context.Context, nodeID multiraft.NodeID, groupID multiraft.GroupID, serviceID uint8, payload []byte) ([]byte, error)
    GroupIDs() []multiraft.GroupID
    PeersForGroup(groupID multiraft.GroupID) []multiraft.NodeID
    IsLocal(nodeID multiraft.NodeID) bool
    LeaderOf(groupID multiraft.GroupID) (multiraft.NodeID, error)
}
```

In `internal/access/node/options.go`, keep the local `Cluster` interface and only swap the compile-time assertion target to `*raftnode.Node`.

- [ ] **Step 2: Run the consumer tests to verify RED**

Run: `go test ./pkg/storage/metastore ./internal/access/node -run 'Test(CallAuthoritativeRPC|HandleAuthoritativeRPC|Presence|Integration)'`
Expected: FAIL because constructors and tests still reference `raftcluster` names and error owners.

- [ ] **Step 3: Update imports, constructors, and error references**

Use `raftnode` in test fixtures and production code where the caller actually needs the node runtime. Update references to `ErrNoLeader` and `ErrGroupNotFound` to the package that now owns them.

Also update comments so they stop describing metastore as built on top of `raftcluster`.

- [ ] **Step 4: Run the access-node and metastore suites**

Run: `go test ./pkg/storage/metastore ./internal/access/node`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/metastore internal/access/node
git commit -m "refactor: narrow cluster dependencies in callers"
```

### Task 8: Delete `raftcluster`, finish test migration, and run repo verification

**Files:**
- Delete: `pkg/cluster/raftcluster/*`
- Modify: any remaining imports reported by `rg -n "pkg/cluster/raftcluster|raftcluster\."`
- Test: repo-wide cluster and app suites

- [ ] **Step 1: Remove the old package and search for stragglers**

Run:

```bash
rg -n "pkg/cluster/raftcluster|raftcluster\." .
```

Expected: remaining hits should be only the approved design doc and implementation plan before the delete, and none in production Go files after the delete.

- [ ] **Step 2: Delete the legacy package files**

Delete every Go source and migrated test under `pkg/cluster/raftcluster` once all references are moved.

- [ ] **Step 3: Run the focused verification suites**

Run:

```bash
go test ./pkg/cluster/nodediscovery ./pkg/cluster/grouprouting ./pkg/cluster/raftnode ./pkg/cluster/controllerclient ./pkg/cluster/groupreconcile
```

Expected: PASS.

- [ ] **Step 4: Run the dependent app and metadata suites**

Run:

```bash
go test ./internal/app ./internal/access/node ./pkg/storage/metastore
```

Expected: PASS.

- [ ] **Step 5: Run the broad regression suite**

Run:

```bash
go test ./internal/... ./pkg/...
```

Expected: PASS.

- [ ] **Step 6: Commit the cleanup**

```bash
git add -A
git commit -m "refactor: replace raftcluster with focused cluster packages"
```

## Execution Notes

- Prefer moving tests with the code they validate rather than leaving them in an integration-heavy package.
- Keep the runtime semantics unchanged: a single deployed node is still a single-node cluster, not a special non-cluster mode.
- If any package boundary forces a confusing name, stop and split again instead of preserving a mixed package under a new label.
- Do not add a compatibility `raftcluster` facade, type alias, or forwarding wrapper.
