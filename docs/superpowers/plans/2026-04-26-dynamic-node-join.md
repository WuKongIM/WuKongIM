# Dynamic Node Join Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow a new data node to join an existing WuKongIM cluster with seed addresses and a join token, without restarting existing nodes.

**Architecture:** Replace static runtime discovery with a mutable discovery snapshot, add seed-based join bootstrapping, persist explicit node membership in Controller metadata, and route JoinCluster through the Controller leader. Ordinary joins create data/slot worker members only; Controller voter changes remain a separate future operation.

**Tech Stack:** Go, etcd/raft, existing `pkg/transport` RPC mux, Pebble-backed `pkg/controller/meta`, existing binary controller RPC codec, `go test`.

---

## Scope And Sequencing

This plan intentionally implements the smallest production-useful slice:

- New nodes can start with `WK_CLUSTER_SEEDS`, `WK_CLUSTER_ADVERTISE_ADDR`, and `WK_CLUSTER_JOIN_TOKEN`.
- Existing nodes update discovery from Controller node snapshots without restart.
- Joined nodes become data workers only.
- Join token generation via Manager API and Controller voter add/remove are left for later specs.
- Local join cache persistence is left out of this slice; restart recovery relies on idempotent rejoin with the same node ID, advertise address, seeds, and token.

Implementation must happen in order because later tasks depend on types and APIs introduced by earlier tasks. Do not run implementation workers for these tasks in parallel; explorer/reviewer agents may run in parallel.

All commands from the worktree at `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/dynamic-node-join` should use `GOWORK=off` because this worktree is nested under a parent `go.work` that does not include it.

## File Structure

- `pkg/cluster/dynamic_discovery.go` — new mutable node discovery implementation with seed support and address-change subscribers.
- `pkg/cluster/dynamic_discovery_test.go` — discovery unit tests.
- `pkg/transport/pool.go` — add a small connection eviction API for address changes/removals.
- `pkg/transport/pool_test.go` — eviction behavior tests.
- `internal/app/config.go` — app-level config fields and validation for seeds / advertise address / join token while preserving static `WK_CLUSTER_NODES` semantics.
- `cmd/wukongim/config.go` — parse new `WK_` keys.
- `cmd/wukongim/config_test.go`, `internal/app/config_test.go`, and `internal/app/build_test.go` — config and shared-discovery regression tests.
- `internal/app/build.go` — pass join/discovery settings into cluster config and share cluster discovery with data-plane pool.
- `pkg/controller/meta/types.go`, `pkg/controller/meta/codec.go`, `pkg/controller/meta/store.go` — membership fields, encoding, validation helpers.
- `pkg/controller/meta/store_test.go` — membership round-trip/backward compatibility tests.
- `pkg/controller/plane/commands.go`, `pkg/controller/plane/statemachine.go` — node join command and membership state transitions.
- `pkg/controller/plane/controller_test.go` — state machine join tests.
- `pkg/controller/raft/service.go` — JSON command envelope encode/decode for node join.
- `pkg/controller/raft/service_test.go` — command codec round-trip tests.
- `pkg/cluster/codec_control.go` — binary controller RPC support for join request/response.
- `pkg/cluster/controller_client.go`, `pkg/cluster/controller_handler.go` — JoinCluster client/server path.
- `pkg/cluster/controller_handler_test.go`, `pkg/cluster/controller_client_internal_test.go` — join RPC tests.
- `pkg/cluster/config.go` — cluster config fields and validation for seed join mode.
- `pkg/cluster/cluster.go`, `pkg/cluster/transport_glue.go` — startup, DynamicDiscovery, join bootstrapping, controller peer updates.
- `pkg/cluster/agent.go`, `pkg/cluster/observation_sync.go`, `pkg/cluster/controller_host.go` — apply membership snapshots/deltas to discovery and mark node-join commands dirty.
- `pkg/controller/plane/planner.go` — filter candidates by active data membership.
- `pkg/cluster/cluster_test.go`, `pkg/cluster/reconciler_test.go`, `pkg/controller/plane/controller_test.go` — sync and planning tests.
- `wukongim.conf.example` — config examples and comments; docker static three-node behavior must remain unchanged and is not edited unless a later explicit joiner example is added.
- `docs/development/PROJECT_KNOWLEDGE.md` — short project knowledge note if new operational semantics are discovered.

---

### Task 1: Dynamic Discovery And Pool Eviction

**Files:**
- Create: `pkg/cluster/dynamic_discovery.go`
- Create: `pkg/cluster/dynamic_discovery_test.go`
- Modify: `pkg/transport/pool.go`
- Modify: `pkg/transport/pool_test.go`

- [ ] **Step 1: Write failing discovery tests**

Add `pkg/cluster/dynamic_discovery_test.go` with tests:

```go
func TestDynamicDiscoveryResolveUsesSeedsThenNodes(t *testing.T) {
    d := NewDynamicDiscovery([]SeedConfig{{ID: 9001, Addr: "127.0.0.1:7001"}}, nil)

    addr, err := d.Resolve(9001)
    require.NoError(t, err)
    require.Equal(t, "127.0.0.1:7001", addr)

    d.UpdateNodes([]NodeConfig{{NodeID: 1, Addr: "wk-node1:7000"}})
    addr, err = d.Resolve(1)
    require.NoError(t, err)
    require.Equal(t, "wk-node1:7000", addr)
}

func TestDynamicDiscoveryUpdateRemovesStaleNodes(t *testing.T) {
    d := NewDynamicDiscovery(nil, []NodeConfig{{NodeID: 1, Addr: "a"}, {NodeID: 2, Addr: "b"}})
    d.UpdateNodes([]NodeConfig{{NodeID: 2, Addr: "b2"}})

    _, err := d.Resolve(1)
    require.ErrorIs(t, err, transport.ErrNodeNotFound)
    addr, err := d.Resolve(2)
    require.NoError(t, err)
    require.Equal(t, "b2", addr)
}
```

- [ ] **Step 2: Write failing pool eviction test**

Add a `pkg/transport/pool.go` test that creates a fake discovery, dials a node, changes its address, calls the new eviction API, and verifies the next dial resolves the new address. Keep the test small; use the existing `mutableDiscovery` helper in `pkg/transport/pool_test.go` if possible.

- [ ] **Step 3: Run failing tests**

Run:

```bash
GOWORK=off go test ./pkg/cluster -run 'TestDynamicDiscovery' -count=1
GOWORK=off go test ./pkg/transport -run 'TestPool.*Evict|TestPool.*ClosePeer' -count=1
```

Expected: FAIL because the new APIs do not exist.

- [ ] **Step 4: Implement `DynamicDiscovery`**

Create focused types:

```go
type SeedConfig struct {
    ID   multiraft.NodeID
    Addr string
}

type DynamicDiscovery struct {
    mu    sync.RWMutex
    seeds map[uint64]NodeInfo
    nodes map[uint64]NodeInfo
}
```

Required methods:

```go
func NewDynamicDiscovery(seeds []SeedConfig, nodes []NodeConfig) *DynamicDiscovery
func (d *DynamicDiscovery) GetNodes() []NodeInfo
func (d *DynamicDiscovery) Resolve(nodeID uint64) (string, error)
func (d *DynamicDiscovery) UpsertSeed(seed SeedConfig)
func (d *DynamicDiscovery) UpdateNodes(nodes []NodeConfig) (changed []uint64)
func (d *DynamicDiscovery) OnAddressChange(fn func(nodeID uint64, oldAddr, newAddr string)) func()
func (d *DynamicDiscovery) Stop()
```

Rules:

- `Resolve` checks current nodes first, then seeds.
- `UpsertSeed` records a temporary seed or leader hint without replacing current dynamic nodes; it is used when a follower seed returns a leader address before full membership is known.
- `UpdateNodes` replaces the dynamic node snapshot, returns node IDs whose address disappeared or changed, and notifies address-change subscribers outside the discovery lock.
- `OnAddressChange` is used by transport pools to close stale connections when node addresses change or disappear; use an empty `newAddr` for removal.
- Sort `GetNodes` by node ID for deterministic tests.
- Keep `StaticDiscovery` unchanged for compatibility.

- [ ] **Step 5: Add pool peer eviction**

Add a narrow method to `pkg/transport.Pool`:

```go
func (p *Pool) ClosePeer(nodeID NodeID) {
    if p == nil {
        return
    }
    if value, ok := p.nodes.LoadAndDelete(nodeID); ok {
        value.(*nodeConnSet).close()
    }
}
```

If `nodeConnSet` has no close helper, add one that closes all active/idle `MuxConn` instances for that peer without touching other peers.

- [ ] **Step 6: Register pool close subscribers**

When cluster transport pools and app data-plane pool are constructed in later tasks, they must register `ClosePeer` callbacks through `DynamicDiscovery.OnAddressChange`. Add unit coverage now for subscriber invocation, but wire concrete pools in Task 6.

- [ ] **Step 7: Run tests**

Run:

```bash
GOWORK=off go test ./pkg/cluster -run 'TestDynamicDiscovery' -count=1
GOWORK=off go test ./pkg/transport -run 'TestPool' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/cluster/dynamic_discovery.go pkg/cluster/dynamic_discovery_test.go pkg/transport/pool.go pkg/transport/pool_test.go
git commit -m "feat(cluster): add dynamic node discovery"
```

---

### Task 2: Config Surface For Seed Join Mode

**Files:**
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `internal/app/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write failing command config tests**

In `cmd/wukongim/config_test.go`, add tests:

```go
func TestLoadConfigParsesClusterSeedJoinKeys(t *testing.T) {
    dir := t.TempDir()
    configPath := writeConf(t, dir, "wukongim.conf",
        "WK_NODE_ID=4",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-4"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7003",
        "WK_CLUSTER_ADVERTISE_ADDR=wk-node4:7000",
        `WK_CLUSTER_SEEDS=["wk-node1:7000","wk-node2:7000"]`,
        "WK_CLUSTER_JOIN_TOKEN=join-secret",
        "WK_CLUSTER_INITIAL_SLOT_COUNT=1",
        "WK_CLUSTER_HASH_SLOT_COUNT=256",
        `WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5103","transport":"stdnet","protocol":"wkproto"}]`,
    )

    cfg, err := loadConfig(configPath)
    require.NoError(t, err)
    require.Equal(t, []string{"wk-node1:7000", "wk-node2:7000"}, cfg.Cluster.Seeds)
    require.Equal(t, "wk-node4:7000", cfg.Cluster.AdvertiseAddr)
    require.Equal(t, "join-secret", cfg.Cluster.JoinToken)
}
```

- [ ] **Step 2: Write failing app validation tests**

In `internal/app/config_test.go`, add tests that prove:

- Existing `WK_CLUSTER_NODES` configuration still works with the same local-node requirement and replica-count defaults.
- Seed join mode allows `Cluster.Nodes` to be empty only when `Seeds`, `AdvertiseAddr`, and `JoinToken` are all set.
- Seed join mode rejects empty `AdvertiseAddr`.
- Seed join mode rejects empty `JoinToken`.
- Seed join mode rejects missing/zero `ControllerReplicaN` or `SlotReplicaN` because these cannot default from an empty node list.
- If both `Nodes` and `Seeds` are set, static `Nodes` remains authoritative for first bootstrap; seeds are pass-through runtime join/discovery settings.

- [ ] **Step 3: Run failing tests**

Run:

```bash
GOWORK=off go test ./cmd/wukongim -run 'TestLoadConfigParsesClusterSeedJoinKeys' -count=1
GOWORK=off go test ./internal/app -run 'TestConfigValidate.*Seed|TestConfigValidate.*Join' -count=1
```

Expected: FAIL because fields and parsers do not exist.

- [ ] **Step 4: Add config fields and validation**

Add to `internal/app.ClusterConfig` with English comments:

```go
// Seeds lists existing cluster RPC addresses used only to bootstrap dynamic node join.
Seeds []string
// AdvertiseAddr is this node's cluster RPC address published to controller membership.
AdvertiseAddr string
// JoinToken authenticates automatic node join requests.
JoinToken string
```

Add helper:

```go
func (c ClusterConfig) JoinModeEnabled() bool {
    return len(c.Seeds) > 0 || c.AdvertiseAddr != "" || c.JoinToken != ""
}
```

Validation rules:

- Static mode is `len(Cluster.Nodes) > 0`; preserve current `Cluster.Nodes`, duplicate checks, local-node-present behavior, and replica defaults from `len(Nodes)`.
- Dynamic join mode is `len(Cluster.Nodes) == 0 && len(Cluster.Seeds) > 0`; require non-empty unique `Seeds`, `AdvertiseAddr`, and `JoinToken`.
- Dynamic join mode requires explicit positive `ControllerReplicaN` and `SlotReplicaN` because these cannot default from an empty node list.
- If both `Cluster.Nodes` and `Cluster.Seeds` are provided, treat `Nodes` as authoritative static bootstrap membership and pass seeds through only for runtime join/discovery behavior.
- Keep `ControllerReplicaN <= len(Nodes)` / `SlotReplicaN <= len(Nodes)` validation only in static mode. In seed join mode with empty `Nodes`, defer peer discovery until join response.

- [ ] **Step 5: Parse `WK_` keys**

In `cmd/wukongim/config.go`:

- parse `WK_CLUSTER_SEEDS` as JSON `[]string`, preserving the project rule that list fields use JSON strings.
- read `WK_CLUSTER_ADVERTISE_ADDR` and `WK_CLUSTER_JOIN_TOKEN`.
- populate `app.ClusterConfig` fields.

- [ ] **Step 6: Update example config and supported key list**

Add `WK_CLUSTER_SEEDS`, `WK_CLUSTER_ADVERTISE_ADDR`, and `WK_CLUSTER_JOIN_TOKEN` to `supportedConfigExampleKeys` in `internal/app/config_test.go`. Add commented English guidance to `wukongim.conf.example` near `WK_CLUSTER_NODES`:

```conf
# Optional dynamic join bootstrap. Existing nodes should keep WK_CLUSTER_NODES for first bootstrap.
# New data nodes can use seeds plus a join token instead of a full static node list.
# WK_CLUSTER_SEEDS=["wk-node1:7000"]
# WK_CLUSTER_ADVERTISE_ADDR=wk-node4:7000
# WK_CLUSTER_JOIN_TOKEN=change-me
```

- [ ] **Step 7: Run tests**

Run:

```bash
GOWORK=off go test ./cmd/wukongim -run 'TestLoadConfigParsesClusterSeedJoinKeys|TestBuildAppConfigParsesAutomaticSlotManagementKeys' -count=1
GOWORK=off go test ./internal/app -run 'TestConfigValidate|TestConfigExampleDocumentsSupportedWKKeys|TestClusterRuntimeConfigIncludesDynamicJoinSettings' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/app/config.go cmd/wukongim/config.go cmd/wukongim/config_test.go internal/app/config_test.go wukongim.conf.example
git commit -m "feat(config): add dynamic join settings"
```

---

### Task 3: Persist Explicit Node Membership

**Files:**
- Modify: `pkg/controller/meta/types.go`
- Modify: `pkg/controller/meta/codec.go`
- Modify: `pkg/controller/meta/store.go`
- Modify: `pkg/controller/meta/store_test.go`

- [ ] **Step 1: Write failing metadata tests**

In `pkg/controller/meta/store_test.go`, add:

```go
func TestStoreClusterNodeMembershipFieldsRoundTrip(t *testing.T) {
    store := openTestStore(t)
    ctx := context.Background()
    joinedAt := time.Unix(10, 0)
    heartbeatAt := time.Unix(20, 0)

    err := store.UpsertNode(ctx, ClusterNode{
        NodeID: 4, Name: "wk-node4", Addr: "wk-node4:7000",
        Role: NodeRoleData, JoinState: NodeJoinStateActive,
        Status: NodeStatusAlive, JoinedAt: joinedAt,
        LastHeartbeatAt: heartbeatAt, CapacityWeight: 2,
    })
    require.NoError(t, err)

    got, err := store.GetNode(ctx, 4)
    require.NoError(t, err)
    require.Equal(t, "wk-node4", got.Name)
    require.Equal(t, NodeRoleData, got.Role)
    require.Equal(t, NodeJoinStateActive, got.JoinState)
    require.Equal(t, joinedAt, got.JoinedAt)
}
```

Also add a backward-compatibility test that decodes a version-1 node value and expects defaults `Role=NodeRoleData`, `JoinState=NodeJoinStateActive`, `Name=""`, `JoinedAt` equal to zero or first heartbeat.

- [ ] **Step 2: Run failing tests**

Run:

```bash
GOWORK=off go test ./pkg/controller/meta -run 'TestStoreClusterNodeMembershipFieldsRoundTrip|TestStoreClusterNodeVersion1DefaultsMembership' -count=1
```

Expected: FAIL because fields/enums do not exist.

- [ ] **Step 3: Add membership types**

In `types.go`, add English comments and enums:

```go
type NodeRole uint8
const (
    NodeRoleUnknown NodeRole = iota
    NodeRoleData
    NodeRoleControllerVoter
)

type NodeJoinState uint8
const (
    NodeJoinStateUnknown NodeJoinState = iota
    NodeJoinStateJoining
    NodeJoinStateActive
    NodeJoinStateRejected
)
```

Extend `ClusterNode` with `Name`, `Role`, `JoinState`, `JoinedAt`.

- [ ] **Step 4: Update store validation and normalization**

Update `normalizeClusterNode`, `validNodeStatus`, and new helpers in `codec.go`/`store.go`:

- Default empty role to `NodeRoleData` for valid nodes.
- Default empty join state to `NodeJoinStateActive` for existing nodes.
- Require `NodeID > 0`, `Addr != ""`, valid role, valid join state, valid status, non-negative capacity.
- Preserve `NodeStatusDraining` behavior.

- [ ] **Step 5: Version node codec**

Encode cluster node data using a node-specific codec version while keeping existing persisted records readable. Do not append fields to the current v1 layout without a decode branch, because current v1 decode rejects trailing bytes. Required behavior:

- New encoded nodes include all fields.
- Existing version-1 data from current code decodes successfully with `Role=NodeRoleData`, `JoinState=NodeJoinStateActive`, and `JoinedAt=LastHeartbeatAt`.
- Snapshot validation still accepts both forms.

- [ ] **Step 6: Run metadata tests**

Run:

```bash
GOWORK=off go test ./pkg/controller/meta -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/controller/meta/types.go pkg/controller/meta/codec.go pkg/controller/meta/store.go pkg/controller/meta/store_test.go
git commit -m "feat(controller): persist node membership fields"
```

---

### Task 4: Controller State Machine Node Join Command

**Files:**
- Modify: `pkg/controller/plane/commands.go`
- Modify: `pkg/controller/plane/statemachine.go`
- Modify: `pkg/controller/plane/controller_test.go`
- Modify: `pkg/controller/raft/service.go`
- Modify: `pkg/controller/raft/service_test.go`

- [ ] **Step 1: Write failing state machine tests**

In `pkg/controller/plane/controller_test.go`, add tests for:

- `CommandKindNodeJoin` creates a `Joining` data node with supplied name, addr, capacity, joined time.
- Repeating join with same `NodeID + Addr` is idempotent.
- Same `NodeID` with different addr is rejected by Join RPC precheck; stale replicated state-machine apply is a no-op to keep Controller Raft alive.
- Different `NodeID` with same addr is rejected by Join RPC precheck; stale replicated state-machine apply is a no-op to keep Controller Raft alive.
- `applyNodeHeartbeat` updates status/heartbeat for a `Joining` node but does not make it schedulable yet.
- `CommandKindNodeJoinActivate` transitions `JoinStateJoining` to `JoinStateActive` only after the node has completed runtime full sync.
- Legacy static bootstrap compatibility: a direct heartbeat for an unknown node still creates a default data/active member in the state machine; dynamic join mode blocks this earlier in the Controller RPC handler.

- [ ] **Step 2: Write failing raft codec test**

In `pkg/controller/raft/service_test.go`, add a command encode/decode round-trip test for `CommandKindNodeJoin`.

- [ ] **Step 3: Run failing tests**

Run:

```bash
GOWORK=off go test ./pkg/controller/plane -run 'TestStateMachineApplyNodeJoin|TestStateMachineNodeHeartbeatKeepsJoiningUntilFullSync|TestStateMachineNodeJoinActivate' -count=1
GOWORK=off go test ./pkg/controller/raft -run 'TestEncodeDecodeCommand.*NodeJoin' -count=1
```

Expected: FAIL because command/request types do not exist.

- [ ] **Step 4: Add command types**

In `commands.go`, add:

```go
const CommandKindNodeJoin CommandKind = 17
const CommandKindNodeJoinActivate CommandKind = 18

type NodeJoinRequest struct {
    NodeID         uint64
    Name           string
    Addr           string
    CapacityWeight int
    JoinedAt       time.Time
}

type NodeJoinActivateRequest struct {
    NodeID      uint64
    ActivatedAt time.Time
}
```

Add `NodeJoin *NodeJoinRequest` and `NodeJoinActivate *NodeJoinActivateRequest` to `Command`.

- [ ] **Step 5: Implement state machine join**

In `statemachine.go`:

- Route `CommandKindNodeJoin` to `applyNodeJoin`.
- Validate node ID and addr.
- Check existing node by ID.
- Check duplicate addr by listing nodes.
- Same ID + same addr is idempotent: update name/capacity if supplied but do not reset draining to alive.
- New node writes `Role=NodeRoleData`, `JoinState=NodeJoinStateJoining`, `Status=NodeStatusAlive`, `JoinedAt=req.JoinedAt`, `LastHeartbeatAt=req.JoinedAt`, normalized capacity.
- Modify `applyNodeHeartbeat` so `Joining` nodes remain `Joining` while heartbeat/status are updated.
- Add `applyNodeJoinActivate` so a node moves from `Joining` to `Active` only after the Controller leader observes that node's `runtimeObservationReport.FullSync`.
- Preserve legacy state-machine behavior where an unknown heartbeat can create a default `Role=NodeRoleData`, `JoinState=NodeJoinStateActive` member. This keeps existing static first bootstrap working; dynamic join mode must reject unauthorized heartbeat/runtime reports in the Controller RPC handler before they become heartbeat commands.

- [ ] **Step 6: Encode raft command**

In `pkg/controller/raft/service.go`:

- Extend `commandEnvelope` with `NodeJoin`.
- Extend `commandEnvelope` with `NodeJoinActivate`.
- Clone encode/decode fields.
- Add helper clone if needed.

- [ ] **Step 7: Run tests**

Run:

```bash
GOWORK=off go test ./pkg/controller/plane -run 'TestStateMachine' -count=1
GOWORK=off go test ./pkg/controller/raft -run 'TestEncodeDecodeCommand|TestService' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/controller/plane/commands.go pkg/controller/plane/statemachine.go pkg/controller/plane/controller_test.go pkg/controller/raft/service.go pkg/controller/raft/service_test.go
git commit -m "feat(controller): add node join command"
```

---

### Task 5: JoinCluster Controller RPC

**Files:**
- Modify: `pkg/cluster/codec_control.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/controller_handler.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/controller_client_internal_test.go`
- Modify: `pkg/cluster/controller_handler_test.go`

- [ ] **Step 1: Write failing codec tests**

In the appropriate existing `pkg/cluster` codec test file, add round-trip coverage for:

```go
type joinRequest struct {
    NodeID uint64
    Name string
    Addr string
    CapacityWeight int
    Token string
    Version string
}
```

and responses containing:

- success: `Nodes`, `HashSlotTableVersion`, and `HashSlotTable`.
- rejection: `JoinErrorCode` and sanitized `JoinErrorMessage`.
- redirect: `NotLeader`, `LeaderID`, and `LeaderAddr`.

If there is no focused codec test file, add the tests to `pkg/cluster/controller_client_internal_test.go` near other controller RPC codec tests.

Also add a cluster-node codec round-trip assertion that a node with non-default `Name`, `Role`, `JoinState`, and `JoinedAt` survives `encodeClusterNodes` / `consumeClusterNodes`. This covers `JoinCluster`, `ListNodes`, and `FetchObservationDelta` responses.

- [ ] **Step 2: Write failing handler tests**

In `pkg/cluster/controller_handler_test.go`, add tests:

- Join on Controller leader proposes `CommandKindNodeJoin` and returns nodes/hash slot table.
- Join with wrong token returns an error and does not propose.
- Join on a follower forwards to the current leader when possible.
- If forwarding is not possible, follower returns `NotLeader` with both `LeaderID` and `LeaderAddr` when the address is resolvable; the join client records that hint as a temporary seed and retries.
- A new node can join when its only configured seed is a follower.
- Join rejection travels in the encoded JoinCluster response with an explicit error code; invalid token, NodeID conflict, address conflict, unsupported version, and invalid request are non-retryable, while commit timeout / temporary unavailable are retryable.
- Dynamic-join mode rejects heartbeat/runtime reports from nodes that are not already configured static members or persisted joined members.

- [ ] **Step 3: Run failing tests**

Run:

```bash
GOWORK=off go test ./pkg/cluster -run 'TestController.*Join|Test.*JoinCluster.*Codec' -count=1
```

Expected: FAIL.

- [ ] **Step 4: Add RPC codec**

In `codec_control.go`:

- Add `controllerRPCJoinCluster` string.
- Add a new controller kind byte.
- Add `LeaderAddr` to redirect responses, in addition to existing `LeaderID`.
- Add request/response fields for `Join`.
- Update `encodeClusterNodes` / `consumeClusterNode` to carry the membership fields added in Task 3: `Name`, `Role`, `JoinState`, and `JoinedAt`. Use a backward-compatible node wire version/branch so older encoded node payloads still decode as data/active members.
- Add explicit join response error classification:

```go
type joinErrorCode string

const (
    joinErrorNone               joinErrorCode = ""
    joinErrorInvalidRequest     joinErrorCode = "invalid_request"
    joinErrorInvalidToken       joinErrorCode = "invalid_token"
    joinErrorNodeIDConflict     joinErrorCode = "node_id_conflict"
    joinErrorAddrConflict       joinErrorCode = "addr_conflict"
    joinErrorUnsupportedVersion joinErrorCode = "unsupported_version"
    joinErrorCommitTimeout      joinErrorCode = "commit_timeout"
    joinErrorTemporary          joinErrorCode = "temporary_unavailable"
)
```

- Encode `JoinErrorCode` and a sanitized `JoinErrorMessage` only in JoinCluster responses. Do not rely on generic transport/remote errors for expected join rejections.
- Implement binary encode/decode with existing helper functions.
- Keep token only in request; never log it.

- [ ] **Step 5: Extend controller API/client**

Add to `controllerAPI`:

```go
JoinCluster(ctx context.Context, req joinClusterRequest) (joinClusterResponse, error)
```

Implement in `controllerClient` using the normal retry/redirect path, extended so `NotLeader` responses with `LeaderAddr` call `DynamicDiscovery.UpsertSeed` before retrying. Map `JoinErrorCode` to typed errors so `joinClusterWithRetry` can distinguish retryable (`commit_timeout`, `temporary_unavailable`, transport/no-leader/not-leader) from non-retryable (`invalid_token`, conflicts, unsupported version, invalid request). Add a method to update controller client peers after a successful join:

```go
func (c *controllerClient) UpdatePeers(peers []multiraft.NodeID)
```

- [ ] **Step 6: Implement handler and heartbeat authorization**

In `controller_handler.go`:

- Validate local cluster has `cfg.JoinToken` set and compare token with constant-time comparison.
- Validate the request version against the current binary's supported join protocol version; unsupported versions must be non-retryable client errors.
- Require leader for mutation; on followers, first try forwarding the join request to the leader via the local controller client, and fall back to `NotLeader` with `LeaderID` and resolvable `LeaderAddr`.
- Propose `CommandKindNodeJoin` with controller timeout.
- Convert expected validation/propose outcomes into JoinCluster response codes instead of returning generic handler errors, so the caller can make correct retry decisions.
- On success, load nodes from leader metadata snapshot/store.
- Return nodes and hash slot table sync payload.
- Before accepting `controllerRPCHeartbeat` or `controllerRPCRuntimeReport`, call a helper such as `isNodeAuthorizedForObservation(nodeID)` when join mode is enabled. It should accept configured static nodes and persisted joined nodes, and reject unknown nodes so heartbeat cannot bypass JoinCluster.
- When accepting `controllerRPCRuntimeReport` on the leader and `report.FullSync` is true for a `JoinStateJoining` node, propose `CommandKindNodeJoinActivate` before marking planner dirty. This is the point where a joined node becomes schedulable.

- [ ] **Step 7: Wire controller host dirty/reload handling**

In `controller_host.go`, include both `CommandKindNodeJoin` and `CommandKindNodeJoinActivate` anywhere committed commands mark metadata snapshots dirty, enqueue reloads, update sync hints, or refresh node health mirrors. Add/extend tests proving committed node join and activation changes become visible to follower observation delta.

- [ ] **Step 8: Run tests**

Run:

```bash
GOWORK=off go test ./pkg/cluster -run 'TestController' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/cluster/codec_control.go pkg/cluster/controller_client.go pkg/cluster/controller_handler.go pkg/cluster/controller_host.go pkg/cluster/controller_client_internal_test.go pkg/cluster/controller_handler_test.go
git commit -m "feat(cluster): add join cluster rpc"
```

---

### Task 6: Cluster Startup Join Bootstrap

**Files:**
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/transport_glue.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `internal/app/build.go`
- Modify: `pkg/cluster/config_test.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `internal/app/build_test.go`
- Modify: `internal/app/lifecycle_test.go` if app build expectations need updates

- [ ] **Step 1: Write failing config/runtime tests**

Add tests proving:

- `cluster.Config` allows seed join mode without local node in `Nodes`.
- Existing static mode still requires local node in `Nodes`.
- Controller client peer/discovery state uses static nodes in static mode and seed/join-response nodes after join; Controller Raft voters remain a static/explicit operator concern.

- [ ] **Step 2: Write failing startup test**

In `pkg/cluster/cluster_test.go`, add a unit-level test using a fake controller client or local handler that proves `Start()` in join mode:

- creates DynamicDiscovery with seeds,
- calls JoinCluster before observation loops start,
- applies returned nodes to discovery,
- updates controller client peers to controller voter/data nodes returned by membership,
- applies returned HashSlotTable.

Keep this as a unit test; do not create a slow multi-process test here.

- [ ] **Step 3: Run failing tests**

Run:

```bash
GOWORK=off go test ./pkg/cluster -run 'TestConfig.*Join|TestCluster.*Join' -count=1
```

Expected: FAIL.

- [ ] **Step 4: Add cluster config fields**

In `pkg/cluster.Config`, add:

```go
Seeds []SeedConfig
AdvertiseAddr string
JoinToken string
```

Validation rules mirror app config:

- Static mode unchanged.
- Join mode requires seeds, advertise addr, join token, and explicit positive replica counts when no static nodes are present.
- Join mode may start with no static nodes. If static nodes are present, static compatibility rules still require the local node.

- [ ] **Step 5: Wire DynamicDiscovery**

Change transport setup to use `DynamicDiscovery`:

- Initialize `c.discovery` in `NewCluster`, so `Cluster.Discovery()` is non-nil before `Start()`.
- Existing static nodes: `NewDynamicDiscovery(nil, cfg.Nodes)`.
- Join mode: assign deterministic synthetic seed IDs for seed addresses if app config has only address strings; use high IDs near `math.MaxUint64` to avoid real node ID collisions.
- Store discovery as a concrete dynamic type on `Cluster` while returning it through the existing `Discovery` interface.
- Update `startTransportLayer` and `transportLayer.discovery` to use the cluster-owned dynamic discovery/interface instead of constructing a fresh `StaticDiscovery`.

Add helper:

```go
func (c *Cluster) applyClusterNodes(nodes []controllermeta.ClusterNode)
```

It should convert active/non-rejected nodes to `NodeConfig` and call `DynamicDiscovery.UpdateNodes`. Pool eviction should happen through `DynamicDiscovery.OnAddressChange` subscribers registered by cluster raft/rpc pools and the app data-plane pool.

- [ ] **Step 6: Implement `joinClusterIfConfigured`**

In `Cluster.Start()` after transport/runtime/controller client exist but before observation loops:

```go
if err := c.joinClusterIfConfigured(context.Background()); err != nil {
    c.Stop()
    return err
}
```

Rules:

- Static existing nodes with no seeds do nothing.
- Static nodes with seeds may accept joiners but should not auto-join themselves.
- Join mode with no local static membership calls `controllerClient.JoinCluster` with `NodeID`, `Node.Name`, `AdvertiseAddr`, token, and version.
- Use `joinClusterWithRetry(ctx)` rather than one best-effort call: retry transient network/not-leader/no-leader errors and retryable JoinCluster response codes with bounded exponential backoff, but return immediately for non-retryable response codes such as invalid token, NodeID conflict, address conflict, unsupported version, or invalid config.
- While retrying, keep the node in `joining` / not-ready state and do not start observation loops or managed Slot write capability. Current implementation blocks inside `Start()` until join succeeds or the caller stops the cluster, so app HTTP/gateway services and `/readyz` are not available during this retry; non-blocking joining readiness is deferred.
- On success, require the returned nodes to contain the joining `NodeID + AdvertiseAddr` before applying the HashSlotTable and nodes.
- Update controller client peers to known controller voters. For first implementation, if role data/controller distinction is not yet fully propagated in returned nodes, keep using seed targets plus all returned nodes so leader discovery still works.

- [ ] **Step 7: Wire app build**

In `internal/app/build.go`, pass app config fields into `raftcluster.Config` and make the data-plane discovery use the same mutable discovery instance as `app.cluster.Discovery()` instead of a separate static discovery from config. Register `app.dataPlanePool.ClosePeer` with the discovery address-change subscription. This is required so existing nodes can send channel RPCs to newly joined or readdressed nodes after discovery updates. Add or update `internal/app/build_test.go` to prove the data-plane pool is not built from a one-time static `cfg.Cluster.runtimeNodes()` snapshot.

- [ ] **Step 8: Run tests**

Run:

```bash
GOWORK=off go test ./pkg/cluster -run 'TestConfig|TestCluster' -count=1
GOWORK=off go test ./internal/app -run 'TestBuild|TestConfig|TestLifecycle|TestBuildWiresDataPlanePoolToClusterDiscovery' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/cluster/config.go pkg/cluster/transport_glue.go pkg/cluster/cluster.go pkg/cluster/config_test.go pkg/cluster/cluster_test.go internal/app/build.go internal/app/build_test.go internal/app/lifecycle_test.go
git commit -m "feat(cluster): bootstrap nodes through join seeds"
```

---

### Task 7: Membership Sync, Planner Filtering, And Existing Node Discovery Updates

**Files:**
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/agent.go`
- Modify: `pkg/cluster/observation_sync.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/controller/plane/planner.go`
- Modify: `pkg/controller/plane/controller_test.go`
- Modify: `pkg/cluster/cluster_test.go`
- Modify: `pkg/cluster/reconciler_test.go`

- [ ] **Step 1: Write failing sync tests**

Add tests proving:

- `controllerClient.ListNodes` or `FetchObservationDelta` updates DynamicDiscovery with returned nodes.
- A follower applying observation delta containing a new active node can resolve that node without restart.
- Removed/readdressed nodes trigger discovery subscribers and close stale raft/rpc/data-plane pool peers.

- [ ] **Step 2: Write failing planner tests**

In `pkg/controller/plane/controller_test.go`, extend planner tests so:

- `NodeJoinStateJoining` nodes are not selected as bootstrap/repair/rebalance targets.
- v1-decoded nodes with default active/data membership remain schedulable so existing clusters do not lose placement candidates.
- `NodeJoinStateActive + NodeStatusAlive + NodeRoleData` nodes are selected.
- `NodeRoleControllerVoter` without data role is not selected if such role is represented as controller-only.

- [ ] **Step 3: Run failing tests**

Run:

```bash
GOWORK=off go test ./pkg/controller/plane -run 'TestPlanner.*Join|TestPlanner.*Membership' -count=1
GOWORK=off go test ./pkg/cluster -run 'Test.*Discovery.*Node|TestSyncObservationDelta.*Node' -count=1
```

Expected: FAIL.

- [ ] **Step 4: Apply node snapshots to discovery**

Whenever nodes are received from Controller leader:

- `controllerClient.ListNodes`
- `controllerClient.RefreshAssignments` if response includes nodes later
- `agent.SyncObservationDelta`
- heartbeat response if membership is added there

call `cluster.applyClusterNodes(nodes)`.

For incremental observation deltas, do not pass only `delta.Nodes` to `UpdateNodes`, because `UpdateNodes` replaces the full discovery snapshot. First call `applyObservationDelta`, then pass the merged `appliedObservationNodes()` / `observationState.Nodes` snapshot to `cluster.applyClusterNodes`. Add coverage proving an incremental delta for node 4 does not remove unchanged nodes 1-3 from discovery.

If observation delta currently already includes `Nodes`, reuse that path first. Avoid adding a second independent node sync protocol unless necessary. Also update `controllerHost.hintPeers` or derive hint targets from dynamic discovery/metadata snapshots so newly joined nodes are not forced to rely only on slow sync.

- [ ] **Step 5: Update planner filters**

In `planner.go`, add helper:

```go
func nodeSchedulableForData(node controllermeta.ClusterNode) bool {
    return node.Status == controllermeta.NodeStatusAlive &&
        node.JoinState == controllermeta.NodeJoinStateActive &&
        node.Role == controllermeta.NodeRoleData
}
```

Use it in bootstrap, repair target, and load extremes. Preserve old decoded nodes by making metadata defaults active/data.

- [ ] **Step 6: Run focused tests**

Run:

```bash
GOWORK=off go test ./pkg/controller/plane -run 'TestPlanner' -count=1
GOWORK=off go test ./pkg/cluster -run 'TestSyncObservationDelta|TestControllerClient|TestDynamicDiscovery' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/cluster/controller_client.go pkg/cluster/agent.go pkg/cluster/observation_sync.go pkg/cluster/controller_host.go pkg/controller/plane/planner.go pkg/controller/plane/controller_test.go pkg/cluster/cluster_test.go pkg/cluster/reconciler_test.go
git commit -m "feat(cluster): sync dynamic membership to discovery"
```

---

### Task 8: Integration Coverage And Documentation

**Files:**
- Modify: `pkg/cluster/cluster_integration_test.go` or create focused `pkg/cluster/join_integration_test.go`
- Modify: `test/e2e/suite/config.go` only if e2e config rendering needs seed mode helper
- Modify: `wukongim.conf.example`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `pkg/controller/FLOW.md`

- [ ] **Step 1: Write integration test**

Add a non-e2e Go integration-style unit test under `pkg/cluster` that starts a three-node static cluster, starts a fourth node with seed join mode, waits for membership active, then verifies:

- existing node discovery resolves node4,
- node4 discovery resolves nodes 1-3,
- `ListNodesStrict` shows node4 active/alive,
- no Controller voter set change occurred.

Keep runtime bounded. If this test is too slow for normal unit tests, guard it with `//go:build integration` and document command below.

- [ ] **Step 2: Add optional e2e suite helper**

Only if needed, add a helper in `test/e2e/suite/config.go` to render seed-join node config. Do not add a full process e2e unless the unit/integration coverage above is insufficient.

- [ ] **Step 3: Run focused integration test**

Run one of:

```bash
GOWORK=off go test ./pkg/cluster -run 'Test.*DynamicJoin' -count=1
```

or if build-tagged:

```bash
GOWORK=off go test -tags=integration ./pkg/cluster -run 'Test.*DynamicJoin' -count=1
```

Expected: PASS.

- [ ] **Step 4: Update flow docs**

Because `pkg/cluster` and `pkg/controller` behavior changes, update:

- `pkg/cluster/FLOW.md` startup and observation sections.
- `pkg/controller/FLOW.md` node command/state sections.

Use “单节点集群” wording where deployment shape is discussed.

- [ ] **Step 5: Record project knowledge**

If not already present, add a short note to `docs/development/PROJECT_KNOWLEDGE.md`:

```md
- Dynamic node join adds ordinary data nodes only; Controller Raft voter changes remain explicit future operator work.
```

Keep the note brief.

- [ ] **Step 6: Run package tests**

Run:

```bash
GOWORK=off go test ./pkg/cluster ./pkg/controller/... ./internal/app ./cmd/wukongim -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/cluster pkg/controller test/e2e/suite wukongim.conf.example docs/development/PROJECT_KNOWLEDGE.md
git commit -m "test(cluster): cover dynamic node join"
```

---

### Final Verification

- [ ] **Step 1: Run full unit suite**

```bash
GOWORK=off go test ./...
```

Expected: PASS.

- [ ] **Step 2: Inspect git history**

```bash
git log --oneline --decorate -8
```

Expected: one commit per completed task, on `feature/dynamic-node-join`.

- [ ] **Step 3: Check worktree cleanliness**

```bash
git status --short
```

Expected: clean.

- [ ] **Step 4: Request final code review**

Use `superpowers:requesting-code-review` for the full branch before reporting completion.
