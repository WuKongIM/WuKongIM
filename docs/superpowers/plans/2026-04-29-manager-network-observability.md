# Manager Network Observability Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the manager `Observability / Network` page and a stable local-node network summary API backed by WuKongIM node-to-node transport observations.

**Architecture:** Add a manager usecase DTO around a local app-level network collector instead of exposing Prometheus text or internal transport structs. The first release is explicitly `local_node` scoped: peer pool/RPC/error state can be shown per outbound peer, traffic bytes are only local totals by message type, and Channel replication shows data-plane pool/RPC service/config information that is already observable. The manager HTTP layer stays thin, and the web page consumes `GET /manager/network/summary` only.

**Tech Stack:** Go manager/access/usecase/app packages, `pkg/transport.ObserverHooks`, `pkg/cluster.ObserverHooks`, React + TypeScript + React Intl + Vitest/Bun tests.

---

## Source Documents And Guardrails

- Approved spec: `docs/superpowers/specs/2026-04-29-manager-network-observability-design.md`
- Existing placeholder page: `web/src/pages/network/page.tsx`
- Do not edit the currently dirty delivery runtime files unless a later user explicitly asks; keep commits scoped to network observability files.
- Use “单节点集群” / `single-node cluster`; do not add standalone/single-machine branches.
- Before reading/editing package code, check `FLOW.md` files as required by `AGENTS.md`:
  - `internal/FLOW.md` has already been read for the manager/app/usecase work.
  - If implementation touches deeper `pkg/cluster` or `pkg/channel` files beyond constants/tests, read `pkg/cluster/FLOW.md` or `pkg/channel/FLOW.md` first.
- Do not promise unsupported data:
  - No complete peer-to-peer matrix in Phase 1.
  - No per-peer traffic bytes; current `OnSend`/`OnReceive` hooks have no peer identity.
  - No Channel lane/reset/backpressure details until a runtime snapshot exists.

## File Responsibility Map

### Backend Usecase

- Create `internal/usecase/management/network.go`
  - Own manager-facing network DTOs and `(*App).ListNetworkSummary` aggregation.
  - Define `NetworkSnapshotReader` for local collector input.
  - Keep JSON tags out of this package; access DTOs own JSON tags.
- Create `internal/usecase/management/network_test.go`
  - Test single-node cluster empty state, multi-peer aggregation, partial controller read failures, and bounded events.
- Modify `internal/usecase/management/app.go`
  - Add `Network NetworkSnapshotReader` to `Options` and `network NetworkSnapshotReader` to `App`.
  - Add `TransportPoolStats() []transport.PoolPeerStats` to `ClusterReader`.
- Modify `internal/usecase/management/nodes_test.go`
  - Extend `fakeClusterReader` with `transportStats []transport.PoolPeerStats` and a `TransportPoolStats` method.

### App Collector / Wiring

- Create `internal/app/network_observability.go`
  - Own the bounded in-memory rolling window collector for transport/RPC/network events.
  - Implement `managementusecase.NetworkSnapshotReader`.
  - Expose `TransportHooks()` and `ClusterHooks()` so existing metrics hooks and network collector hooks can run in parallel.
- Create `internal/app/network_observability_test.go`
  - Test hook recording, 1m rolling-window pruning, service aggregation, event capping, discovery/config snapshot, and data-plane pool inclusion.
- Modify `internal/app/app.go`
  - Add `networkObservability *networkObservability` field.
- Modify `internal/app/build.go`
  - Instantiate network collector before cluster creation.
  - Merge transport hooks even when Prometheus metrics are disabled.
  - Pass the collector to `managementusecase.New`.
- Modify `internal/app/observability.go`
  - Add/keep helper `mergeTransportObserverHooks` if placed there instead of `build.go`.
  - Extend `transportRPCServiceName` for services `30`, `34`, `35`, `36`, `37`, `38`, `39`.
- Modify `internal/app/observability_test.go`
  - Extend service name mapping tests and hook merge tests.

### Manager HTTP API

- Create `internal/access/manager/network.go`
  - Own JSON response structs and conversion from `managementusecase.NetworkSummary`.
  - Add `handleNetworkSummary`.
- Create or extend `internal/access/manager/network_test.go`
  - Prefer a new focused file if the existing `server_test.go` is too large.
  - Test permission, JSON shape, and error mapping.
- Modify `internal/access/manager/server.go`
  - Add `ListNetworkSummary(ctx context.Context) (managementusecase.NetworkSummary, error)` to `Management`.
- Modify `internal/access/manager/routes.go`
  - Add `GET /manager/network/summary` under `cluster.network:r`.
- Modify `internal/access/manager/server_test.go`
  - Extend `managementStub` with network fields/method.

### Web API / Types

- Modify `web/src/lib/manager-api.types.ts`
  - Add network summary response types.
- Modify `web/src/lib/manager-api.ts`
  - Add `getNetworkSummary()`.
- Modify `web/src/lib/manager-api.test.ts`
  - Test the new client method uses `/manager/network/summary`.

### Web Page

- Modify `web/src/pages/network/page.tsx`
  - Replace placeholder with live network summary UI.
  - Use local-node scope language and single-node cluster empty state.
- Create `web/src/pages/network/page.test.tsx`
  - Test loading/error/single-node/peer/RPC/service/refresh states.
- Modify `web/src/pages/page-shells.test.tsx`
  - Mock `getNetworkSummary` and update `/network` shell expectations away from placeholder text.
- Modify `web/src/i18n/messages/en.ts`
  - Add network page message ids.
- Modify `web/src/i18n/messages/zh-CN.ts`
  - Add matching Chinese translations.

---

## DTO Contract To Implement

Use these names as the stable contract unless implementation discovers an existing naming convention that conflicts.

### Usecase DTO Shape

```go
// NetworkSummary is the manager-facing local node network observation snapshot.
type NetworkSummary struct {
    GeneratedAt        time.Time
    Scope              NetworkScope
    SourceStatus       NetworkSourceStatus
    Headline           NetworkHeadline
    Traffic            NetworkTraffic
    Peers              []NetworkPeer
    Services           []NetworkRPCService
    ChannelReplication NetworkChannelReplication
    Discovery          NetworkDiscovery
    Events             []NetworkEvent
}

type NetworkScope struct {
    View               string // first release: "local_node"
    LocalNodeID        uint64
    ControllerLeaderID uint64
}

type NetworkSourceStatus struct {
    LocalCollector    string            // ok | unavailable
    ControllerContext string            // ok | unavailable
    RuntimeViews      string            // ok | unavailable
    Errors            map[string]string // nodes, runtime_views, local_collector
}

type NetworkHeadline struct {
    RemotePeers       int
    AliveNodes        int
    SuspectNodes      int
    DeadNodes         int
    DrainingNodes     int
    PoolActive        int
    PoolIdle          int
    RPCInflight       int
    DialErrors1m      int
    QueueFull1m       int
    Timeouts1m        int
    StaleObservations int
}

type NetworkTraffic struct {
    Scope                  string // first release: "local_total_by_msg_type"
    TXBytes1m              int64
    RXBytes1m              int64
    TXBps                  float64
    RXBps                  float64
    PeerBreakdownAvailable bool
    ByMessageType          []NetworkTrafficMessageType
}

type NetworkTrafficMessageType struct {
    Direction string // tx | rx
    MessageType string
    Bytes1m int64
    Bps float64
}

type NetworkPeer struct {
    NodeID          uint64
    Name            string
    Addr            string
    Health          string
    LastHeartbeatAt time.Time
    Pools           NetworkPeerPools
    RPC             NetworkPeerRPC
    Errors          NetworkPeerErrors
}

type NetworkPoolStats struct {
    Active int
    Idle   int
}

type NetworkPeerPools struct {
    Cluster   NetworkPoolStats
    DataPlane NetworkPoolStats
}

type NetworkPeerRPC struct {
    Inflight    int
    Calls1m     int
    P95Ms       float64
    SuccessRate *float64 // nil means unknown / insufficient samples
}

type NetworkPeerErrors struct {
    DialError1m  int
    QueueFull1m  int
    // Timeout1m excludes expected channel_long_poll_fetch wait timeouts.
    Timeout1m    int
    RemoteError1m int
}

type NetworkRPCService struct {
    ServiceID   uint8
    Service     string
    Group       string
    TargetNode  uint64
    Inflight    int
    Calls1m     int
    Success1m   int
    // ExpectedTimeout1m counts normal service-level timeouts, such as channel_long_poll_fetch wait expiry.
    ExpectedTimeout1m int
    // Timeout1m counts abnormal RPC timeouts that should affect peer/headline error state.
    Timeout1m   int
    QueueFull1m int
    RemoteError1m int
    OtherError1m  int
    P50Ms       float64
    P95Ms       float64
    P99Ms       float64
    LastSeenAt  time.Time
}

type NetworkChannelReplication struct {
    Pool              NetworkPoolStats
    Services          []NetworkRPCService
    LongPollConfig    NetworkLongPollConfig
    LongPollTimeouts1m int
    DataPlaneRPCTimeout time.Duration
}

type NetworkLongPollConfig struct {
    LaneCount int
    MaxWait time.Duration
    MaxBytes int
    MaxChannels int
}

type NetworkDiscovery struct {
    ListenAddr        string
    AdvertiseAddr     string
    Seeds             []string
    StaticNodes       []NetworkDiscoveryNode
    PoolSize          int
    DataPlanePoolSize int
    DialTimeout       time.Duration
    ControllerObservationInterval time.Duration
}

type NetworkDiscoveryNode struct {
    NodeID uint64
    Addr string
}

type NetworkEvent struct {
    At         time.Time
    Severity   string // info | warn | error
    Kind       string // dial_error | queue_full | rpc_timeout | node_status_change | ...
    TargetNode uint64
    Service    string
    Message    string
}
```

### Collector Snapshot Shape

Keep collector snapshots in the management package so the app collector can satisfy the usecase interface without the usecase importing `internal/app`.

```go
// NetworkSnapshotReader reads local node transport observations for manager aggregation.
type NetworkSnapshotReader interface {
    NetworkSnapshot(now time.Time) NetworkObservationSnapshot
}

// NetworkObservationSnapshot is a copy-safe local collector snapshot.
type NetworkObservationSnapshot struct {
    LocalCollectorAvailable bool
    Traffic NetworkTraffic
    DataPlanePools []NetworkPoolPeerStats
    PeerErrors map[uint64]NetworkPeerErrors
    Services []NetworkRPCService
    ChannelReplication NetworkChannelReplication
    Discovery NetworkDiscovery
    Events []NetworkEvent
}

type NetworkPoolPeerStats struct {
    NodeID uint64
    Active int
    Idle int
}
```

### JSON Response Notes

- Access DTOs in `internal/access/manager/network.go` must use snake_case JSON fields.
- Durations should be encoded in milliseconds for page-friendly display, e.g. `dial_timeout_ms`, `data_plane_rpc_timeout_ms`, `controller_observation_interval_ms`, `long_poll.max_wait_ms`.
- `success_rate` is `null` if no completed calls in the window.
- `traffic.peer_breakdown_available` must be `false` until transport hooks carry peer identity.
- `events` must be capped at 50 by default.
- `channel_long_poll_fetch` (`service_id=35`) result `timeout` is a normal long-poll wait expiry. Count it in `expected_timeout_1m` and `channel_replication.long_poll_timeouts_1m`; do not count it in headline `timeouts_1m`, peer `errors.timeout_1m`, or warning/error events.

---

### Task 1: Management Network Summary Usecase

**Files:**
- Create: `internal/usecase/management/network.go`
- Create: `internal/usecase/management/network_test.go`
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/nodes_test.go`

- [ ] **Step 1: Write failing usecase tests**

Add `internal/usecase/management/network_test.go` with tests covering these behaviors:

```go
func TestListNetworkSummaryReturnsSingleNodeClusterEmptyState(t *testing.T) {
    now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
    app := New(Options{
        LocalNodeID: 1,
        Cluster: fakeClusterReader{
            controllerLeaderID: 1,
            nodes: []controllermeta.ClusterNode{{
                NodeID: 1,
                Name: "node-1",
                Addr: "127.0.0.1:7001",
                Status: controllermeta.NodeStatusAlive,
                LastHeartbeatAt: now.Add(-time.Second),
            }},
        },
        Network: fakeNetworkSnapshotReader{snapshot: NetworkObservationSnapshot{
            LocalCollectorAvailable: true,
            Traffic: NetworkTraffic{Scope: "local_total_by_msg_type"},
        }},
        Now: func() time.Time { return now },
    })

    got, err := app.ListNetworkSummary(context.Background())
    require.NoError(t, err)
    require.Equal(t, "local_node", got.Scope.View)
    require.Equal(t, uint64(1), got.Scope.LocalNodeID)
    require.Equal(t, 0, got.Headline.RemotePeers)
    require.Empty(t, got.Peers)
    require.Equal(t, "ok", got.SourceStatus.LocalCollector)
    require.Equal(t, "ok", got.SourceStatus.ControllerContext)
    require.False(t, got.Traffic.PeerBreakdownAvailable)
}

func TestListNetworkSummaryAggregatesPeersPoolsRPCAndTraffic(t *testing.T) {
    // Arrange three nodes, cluster pool stats for node 2, data-plane pool stats for node 2,
    // one channel_long_poll_fetch expected timeout, one delivery_push timeout, one delivery_push success,
    // traffic bytes, and events.
    // Assert remote_peers=2, pool totals include cluster+data-plane, service names are stable,
    // delivery_push timeout_1m is counted as an error, channel_long_poll_fetch timeout is counted as
    // expected_timeout_1m only, and events are capped/sorted newest first.
}

func TestListNetworkSummaryPreservesLocalCollectorWhenControllerReadsFail(t *testing.T) {
    // Arrange ListNodesStrict and ListObservedRuntimeViewsStrict errors with a non-empty local snapshot.
    // Assert no returned error, local_collector=ok, controller_context=unavailable,
    // runtime_views=unavailable, source_status.errors contains both failing reads,
    // and collector traffic/services/events remain in the response.
}
```

Add a local fake reader in the same test file:

```go
type fakeNetworkSnapshotReader struct {
    snapshot NetworkObservationSnapshot
}

func (f fakeNetworkSnapshotReader) NetworkSnapshot(time.Time) NetworkObservationSnapshot {
    return f.snapshot
}
```

- [ ] **Step 2: Run tests and confirm they fail to compile**

Run:

```bash
go test ./internal/usecase/management -run 'TestListNetworkSummary' -count=1
```

Expected: FAIL/compile errors for missing `NetworkSnapshotReader`, `NetworkObservationSnapshot`, `ListNetworkSummary`, and `TransportPoolStats`.

- [ ] **Step 3: Extend management app interfaces**

In `internal/usecase/management/app.go`:

```go
import "github.com/WuKongIM/WuKongIM/pkg/transport"

// ClusterReader exposes the cluster reads needed by manager queries.
type ClusterReader interface {
    // existing methods...
    // TransportPoolStats returns local outbound cluster transport pool counters by peer.
    TransportPoolStats() []transport.PoolPeerStats
}

// Options configures the management usecase app.
type Options struct {
    // existing fields...
    // Network provides local node network observations for manager network pages.
    Network NetworkSnapshotReader
}

type App struct {
    // existing fields...
    network NetworkSnapshotReader
}
```

Set `network: opts.Network` in `New`.

Extend `fakeClusterReader` in `internal/usecase/management/nodes_test.go`:

```go
transportStats []transport.PoolPeerStats

func (f fakeClusterReader) TransportPoolStats() []transport.PoolPeerStats {
    return append([]transport.PoolPeerStats(nil), f.transportStats...)
}
```

- [ ] **Step 4: Implement `network.go` minimally**

Implement DTOs and aggregation in `internal/usecase/management/network.go`.

Required aggregation rules:

- `GeneratedAt = a.now().UTC()`.
- `Scope.View = "local_node"`; `Scope.LocalNodeID = a.localNodeID`; `Scope.ControllerLeaderID = a.cluster.ControllerLeaderID()` when cluster is non-nil.
- Call `a.network.NetworkSnapshot(now)` if configured; if nil, mark `source_status.local_collector = "unavailable"` and continue with cluster context.
- Call `ListNodesStrict` and `ListObservedRuntimeViewsStrict` independently; do not return these errors. Instead set:
  - `source_status.controller_context = "unavailable"` for node read error.
  - `source_status.runtime_views = "unavailable"` for runtime view error.
  - `source_status.errors["nodes"]` / `source_status.errors["runtime_views"]`.
- Build `Peers` from all remote node ids seen in node list, cluster pool stats, data-plane pool stats, RPC service stats, and network events.
- `Traffic.Scope` defaults to `"local_total_by_msg_type"`; `PeerBreakdownAvailable` remains `false`.
- `Headline.PoolActive` / `PoolIdle` are totals across cluster and data-plane pools.
- `Headline.RPCInflight`, errors, and service summaries come from collector snapshot RPC/service counters.
- Exclude `channel_long_poll_fetch` `timeout` results from `Headline.Timeouts1m` and `NetworkPeer.Errors.Timeout1m`; include them in `NetworkRPCService.ExpectedTimeout1m` and `NetworkChannelReplication.LongPollTimeouts1m`.
- `Headline.StaleObservations` counts runtime views where `now.Sub(view.LastReportAt) > a.scaleInRuntimeViewMaxAge` only when runtime views are available.
- Sort peers by `node_id`, services by `group/service/target_node`, events newest-first.

- [ ] **Step 5: Run usecase tests**

Run:

```bash
go test ./internal/usecase/management -run 'TestListNetworkSummary' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run full management usecase package**

Run:

```bash
go test ./internal/usecase/management -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit usecase slice**

Run:

```bash
git diff --check
git add internal/usecase/management/app.go internal/usecase/management/network.go internal/usecase/management/network_test.go internal/usecase/management/nodes_test.go
git commit -m "feat: add manager network summary usecase"
```

Expected: commit contains only the usecase/test changes from this task.

---

### Task 2: App-Level Network Collector And Hook Wiring

**Files:**
- Create: `internal/app/network_observability.go`
- Create: `internal/app/network_observability_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/observability.go`
- Modify: `internal/app/observability_test.go`

- [ ] **Step 1: Write failing collector tests**

Create `internal/app/network_observability_test.go` with these tests:

```go
func TestNetworkObservabilityRecordsTransportAndRPCWindow(t *testing.T) {
    now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
    collector := newNetworkObservability(networkObservabilityConfig{
        LocalNodeID: 1,
        LocalNodeName: "node-1",
        Window: time.Minute,
        MaxEvents: 50,
        Now: func() time.Time { return now },
    })
    hooks := collector.TransportHooks()

    hooks.OnSend(1, 1024)
    hooks.OnReceive(2, 512)
    hooks.OnDial(transport.DialEvent{TargetNode: 2, Result: "dial_error", Duration: 5 * time.Millisecond})
    hooks.OnEnqueue(transport.EnqueueEvent{TargetNode: 2, Kind: "rpc", Result: "queue_full"})
    hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 2, ServiceID: 35, Inflight: 1})
    hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 2, ServiceID: 35, Result: "timeout", Duration: 200 * time.Millisecond, Inflight: 0})

    snap := collector.NetworkSnapshot(now)
    require.True(t, snap.LocalCollectorAvailable)
    require.Equal(t, int64(1024), snap.Traffic.TXBytes1m)
    require.Equal(t, int64(512), snap.Traffic.RXBytes1m)
    require.False(t, snap.Traffic.PeerBreakdownAvailable)
    require.Equal(t, 1, snap.PeerErrors[uint64(2)].DialError1m)
    require.Equal(t, 1, snap.PeerErrors[uint64(2)].QueueFull1m)
    require.Equal(t, 0, snap.PeerErrors[uint64(2)].Timeout1m)
    require.Equal(t, "channel_long_poll_fetch", snap.Services[0].Service)
    require.Equal(t, 1, snap.Services[0].ExpectedTimeout1m)
    require.Equal(t, 0, snap.Services[0].Timeout1m)
    require.Equal(t, 1, snap.ChannelReplication.LongPollTimeouts1m)
    require.False(t, hasNetworkEvent(snap.Events, "rpc_timeout", "channel_long_poll_fetch"))
    require.NotEmpty(t, snap.Events)
}

func TestNetworkObservabilityPrunesOldEvents(t *testing.T) {
    // Record one dial_error at T0, advance Now by 61s, record one ok RPC.
    // Assert DialError1m is 0 and old event is pruned from the snapshot.
}

func TestNetworkObservabilitySnapshotIncludesConfigAndDataPlanePools(t *testing.T) {
    // Configure listen/advertise/seeds/static nodes/pool sizes/long-poll settings and DataPlanePoolStats.
    // Assert snapshot discovery fields and data-plane pool stats are copied.
}
```

Add a small `hasNetworkEvent(events []managementusecase.NetworkEvent, kind, service string) bool` helper in the test file for the long-poll timeout assertion.

- [ ] **Step 2: Extend service mapping test before implementation**

Modify `TestTransportMetricsObserverMapsServiceIDToName` or add a table test in `internal/app/observability_test.go`:

```go
func TestTransportRPCServiceNameCoversKnownServices(t *testing.T) {
    cases := map[uint8]string{
        1: "forward",
        5: "presence",
        6: "delivery_submit",
        7: "delivery_push",
        8: "delivery_ack",
        9: "delivery_offline",
        13: "conversation_facts",
        14: "controller",
        20: "managed_slot",
        30: "channel_fetch",
        33: "channel_append",
        34: "channel_reconcile_probe",
        35: "channel_long_poll_fetch",
        36: "channel_messages",
        37: "channel_leader_repair",
        38: "channel_leader_evaluate",
        39: "runtime_summary",
        99: "service_99",
    }
    for serviceID, want := range cases {
        require.Equal(t, want, transportRPCServiceName(serviceID), "service id %d", serviceID)
    }
}
```

- [ ] **Step 3: Run tests and confirm failure**

Run:

```bash
go test ./internal/app -run 'TestNetworkObservability|TestTransportRPCServiceNameCoversKnownServices|TestTransportMetricsObserverMapsServiceIDToName' -count=1
```

Expected: FAIL/compile errors for collector symbols and missing service mappings.

- [ ] **Step 4: Implement collector types**

In `internal/app/network_observability.go`, implement a small collector with a mutex and append-only samples pruned at snapshot time.

Required details:

```go
type networkObservabilityConfig struct {
    LocalNodeID uint64
    LocalNodeName string
    Window time.Duration
    MaxEvents int
    ListenAddr string
    AdvertiseAddr string
    Seeds []SeedConfig // or normalized []string if easier
    StaticNodes []NodeConfigRef
    PoolSize int
    DataPlanePoolSize int
    DialTimeout time.Duration
    ControllerObservationInterval time.Duration
    DataPlaneRPCTimeout time.Duration
    LongPollLaneCount int
    LongPollMaxWait time.Duration
    LongPollMaxBytes int
    LongPollMaxChannels int
    DataPlanePoolStats func() []transport.PoolPeerStats
    Now func() time.Time
}
```

Implementation requirements:

- Default `Window` to `time.Minute` and `MaxEvents` to `50`.
- Store event samples with `at`, `kind`, `targetNode`, `serviceID`, `result`, `duration`, `bytes`, and `direction` as needed.
- Store current RPC inflight by `(target_node, service_id)` on every `RPCClientEvent`, including start events with empty `Result`.
- Count completed RPC results only when `Result != ""`.
- Classify `channel_long_poll_fetch` (`service_id=35`) `timeout` as an expected timeout: it updates `ExpectedTimeout1m` / `LongPollTimeouts1m` but must not create `rpc_timeout` warning events or peer/headline timeout errors.
- Convert service id to name using `transportRPCServiceName` and group using a helper:
  - `controller`: `controller`
  - `managed_slot`: `slot`
  - `channel_fetch`, `channel_reconcile_probe`, `channel_long_poll_fetch`: `channel_data_plane`
  - `forward`: `cluster`
  - all delivery/presence/conversation/channel app services: `usecase`
- Keep event messages short and stable, e.g. `dial to node 2 failed`, `rpc queue full for delivery_push on node 2`.
- Compute latency percentiles from in-window completed RPC durations; small sample percentiles can use nearest-rank.
- Return copies of slices/maps so callers cannot mutate collector state.

- [ ] **Step 5: Add transport hook merge helper**

Add a helper near the existing `mergeClusterObserverHooks` or in `observability.go`:

```go
func mergeTransportObserverHooks(left, right transport.ObserverHooks) transport.ObserverHooks {
    return transport.ObserverHooks{
        OnSend: func(msgType uint8, bytes int) { /* call left then right when non-nil */ },
        OnReceive: func(msgType uint8, bytes int) { /* call left then right when non-nil */ },
        OnDial: func(event transport.DialEvent) { /* call both */ },
        OnEnqueue: func(event transport.EnqueueEvent) { /* call both */ },
        OnRPCClient: func(event transport.RPCClientEvent) { /* call both */ },
    }
}
```

Add a focused unit test that both sides receive at least `OnRPCClient` and `OnDial`.

- [ ] **Step 6: Extend service id mapping**

Update `transportRPCServiceName` in `internal/app/observability.go`:

```go
case 30:
    return "channel_fetch"
case 34:
    return "channel_reconcile_probe"
case 35:
    return "channel_long_poll_fetch"
case 36:
    return "channel_messages"
case 37:
    return "channel_leader_repair"
case 38:
    return "channel_leader_evaluate"
case 39:
    return "runtime_summary"
```

Prefer importing channel transport constants for `30/34/35` if it keeps the file readable; literals are acceptable for private node service ids `36-39`.

- [ ] **Step 7: Wire collector in `build.go`**

In `internal/app/build.go`, before `raftcluster.NewCluster(clusterCfg)`:

```go
app.networkObservability = newNetworkObservability(networkObservabilityConfig{
    LocalNodeID: cfg.Node.ID,
    LocalNodeName: cfg.Node.Name,
    ListenAddr: cfg.Cluster.ListenAddr,
    AdvertiseAddr: cfg.Cluster.AdvertiseAddr,
    StaticNodes: cfg.Cluster.Nodes,
    Seeds: cfg.Cluster.Seeds,
    PoolSize: cfg.Cluster.PoolSize,
    DataPlanePoolSize: cfg.Cluster.DataPlanePoolSize,
    DialTimeout: cfg.Cluster.DialTimeout,
    ControllerObservationInterval: cfg.Cluster.Timeouts.ControllerObservation,
    DataPlaneRPCTimeout: cfg.Cluster.DataPlaneRPCTimeout,
    LongPollLaneCount: cfg.Cluster.LongPollLaneCount,
    LongPollMaxWait: cfg.Cluster.LongPollMaxWait,
    LongPollMaxBytes: cfg.Cluster.LongPollMaxBytes,
    LongPollMaxChannels: cfg.Cluster.LongPollMaxChannels,
    DataPlanePoolStats: func() []transport.PoolPeerStats {
        if app.dataPlanePool == nil {
            return nil
        }
        return app.dataPlanePool.Stats()
    },
})
transportObserver = mergeTransportObserverHooks(transportObserver, app.networkObservability.TransportHooks())
clusterObserver = mergeClusterObserverHooks(clusterObserver, app.networkObservability.ClusterHooks())
```

Then ensure `clusterCfg.TransportObserver = transportObserver` is set whenever `transportObserver` has hooks, not only when `app.metrics != nil`.

When constructing `managementusecase.New`, pass:

```go
Network: app.networkObservability,
```

- [ ] **Step 8: Run app tests**

Run:

```bash
go test ./internal/app -run 'TestNetworkObservability|TestTransportRPCServiceNameCoversKnownServices|TestTransportMetricsObserverMapsServiceIDToName|TestMetricsHandlerRefreshesTransportPoolMetrics' -count=1
```

Expected: PASS.

- [ ] **Step 9: Run full app package if focused tests are stable**

Run:

```bash
go test ./internal/app -count=1
```

Expected: PASS. If this is too slow, at minimum run the focused command above and record why full package was skipped.

- [ ] **Step 10: Commit collector slice**

Run:

```bash
git diff --check
git add internal/app/app.go internal/app/build.go internal/app/observability.go internal/app/observability_test.go internal/app/network_observability.go internal/app/network_observability_test.go
git commit -m "feat: collect local network observations"
```

Expected: commit contains only app collector/wiring changes.

---

### Task 3: Manager Network Summary HTTP Endpoint

**Files:**
- Create: `internal/access/manager/network.go`
- Create: `internal/access/manager/network_test.go`
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing access tests**

Create `internal/access/manager/network_test.go` with tests:

```go
func TestManagerNetworkSummaryRejectsInsufficientPermission(t *testing.T) {
    srv := New(Options{
        Auth: testAuthConfig([]UserConfig{{
            Username: "viewer",
            Password: "secret",
            Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}},
        }}),
        Management: managementStub{},
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/network/summary", nil)
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestManagerNetworkSummaryReturnsLocalNodeSnapshot(t *testing.T) {
    generatedAt := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
    rate := 0.5
    srv := New(Options{
        Auth: testAuthConfig([]UserConfig{{
            Username: "admin",
            Password: "secret",
            Permissions: []PermissionConfig{{Resource: "cluster.network", Actions: []string{"r"}}},
        }}),
        Management: managementStub{networkSummary: managementusecase.NetworkSummary{
            GeneratedAt: generatedAt,
            Scope: managementusecase.NetworkScope{View: "local_node", LocalNodeID: 1, ControllerLeaderID: 1},
            SourceStatus: managementusecase.NetworkSourceStatus{LocalCollector: "ok", ControllerContext: "ok", RuntimeViews: "ok", Errors: map[string]string{}},
            Headline: managementusecase.NetworkHeadline{RemotePeers: 1, PoolActive: 3, RPCInflight: 1, Timeouts1m: 1},
            Traffic: managementusecase.NetworkTraffic{Scope: "local_total_by_msg_type", TXBytes1m: 1024, PeerBreakdownAvailable: false},
            Peers: []managementusecase.NetworkPeer{{NodeID: 2, Name: "node-2", Addr: "127.0.0.1:7002", Health: "alive", RPC: managementusecase.NetworkPeerRPC{SuccessRate: &rate}}},
            Services: []managementusecase.NetworkRPCService{{ServiceID: 7, Service: "delivery_push", Group: "usecase", TargetNode: 2, Timeout1m: 1}},
            Events: []managementusecase.NetworkEvent{{At: generatedAt, Severity: "warn", Kind: "rpc_timeout", TargetNode: 2, Service: "delivery_push", Message: "rpc timeout"}},
        }},
    })

    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/manager/network/summary", nil)
    req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
    srv.Engine().ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    require.JSONEq(t, `{
      "generated_at":"2026-04-29T12:00:00Z",
      "scope":{"view":"local_node","local_node_id":1,"controller_leader_id":1},
      "source_status":{"local_collector":"ok","controller_context":"ok","runtime_views":"ok","errors":{}},
      "headline":{"remote_peers":1,"alive_nodes":0,"suspect_nodes":0,"dead_nodes":0,"draining_nodes":0,"pool_active":3,"pool_idle":0,"rpc_inflight":1,"dial_errors_1m":0,"queue_full_1m":0,"timeouts_1m":1,"stale_observations":0},
      "traffic":{"scope":"local_total_by_msg_type","tx_bytes_1m":1024,"rx_bytes_1m":0,"tx_bps":0,"rx_bps":0,"peer_breakdown_available":false,"by_message_type":[]},
      "peers":[{"node_id":2,"name":"node-2","addr":"127.0.0.1:7002","health":"alive","last_heartbeat_at":"0001-01-01T00:00:00Z","pools":{"cluster":{"active":0,"idle":0},"data_plane":{"active":0,"idle":0}},"rpc":{"inflight":0,"calls_1m":0,"p95_ms":0,"success_rate":0.5},"errors":{"dial_error_1m":0,"queue_full_1m":0,"timeout_1m":0,"remote_error_1m":0}}],
      "services":[{"service_id":7,"service":"delivery_push","group":"usecase","target_node":2,"inflight":0,"calls_1m":0,"success_1m":0,"expected_timeout_1m":0,"timeout_1m":1,"queue_full_1m":0,"remote_error_1m":0,"other_error_1m":0,"p50_ms":0,"p95_ms":0,"p99_ms":0,"last_seen_at":"0001-01-01T00:00:00Z"}],
      "channel_replication":{"pool":{"active":0,"idle":0},"services":[],"long_poll":{"lane_count":0,"max_wait_ms":0,"max_bytes":0,"max_channels":0},"long_poll_timeouts_1m":0,"data_plane_rpc_timeout_ms":0},
      "discovery":{"listen_addr":"","advertise_addr":"","seeds":[],"static_nodes":[],"pool_size":0,"data_plane_pool_size":0,"dial_timeout_ms":0,"controller_observation_interval_ms":0},
      "events":[{"at":"2026-04-29T12:00:00Z","severity":"warn","kind":"rpc_timeout","target_node":2,"service":"delivery_push","message":"rpc timeout"}]
    }`, rec.Body.String())
}
```

Add a third test if `ListNetworkSummary` returns an unexpected error:

```go
func TestManagerNetworkSummaryReturnsInternalError(t *testing.T) { /* expect 500 */ }
```

- [ ] **Step 2: Run tests and confirm failure**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerNetworkSummary' -count=1
```

Expected: FAIL/compile errors for missing route and interface method.

- [ ] **Step 3: Extend manager interface and stub**

In `internal/access/manager/server.go` add to `Management`:

```go
// ListNetworkSummary returns the local-node network observation summary.
ListNetworkSummary(ctx context.Context) (managementusecase.NetworkSummary, error)
```

In `internal/access/manager/server_test.go` add fields and method:

```go
networkSummary managementusecase.NetworkSummary
networkSummaryErr error

func (s managementStub) ListNetworkSummary(context.Context) (managementusecase.NetworkSummary, error) {
    return s.networkSummary, s.networkSummaryErr
}
```

- [ ] **Step 4: Add route with dedicated permission**

In `internal/access/manager/routes.go`:

```go
network := s.engine.Group("/manager")
if s.auth.enabled() {
    network.Use(s.requirePermission("cluster.network", "r"))
}
network.GET("/network/summary", s.handleNetworkSummary)
```

- [ ] **Step 5: Implement access DTO conversion**

Create `internal/access/manager/network.go`.

Requirements:

- `handleNetworkSummary` mirrors existing handler patterns: 503 only when `s.management == nil`; otherwise call usecase and return 500 for unexpected usecase error.
- Define response structs with English comments for exported types/fields.
- Convert durations to milliseconds as numbers.
- Ensure nil slices/maps become `[]` / `{}` where the UI expects arrays/maps.

- [ ] **Step 6: Run access tests**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerNetworkSummary|TestManagerNodesRejectsMissingToken|TestManagerNodesRejectsInsufficientPermission' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run full access manager package**

Run:

```bash
go test ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit access slice**

Run:

```bash
git diff --check
git add internal/access/manager/network.go internal/access/manager/network_test.go internal/access/manager/routes.go internal/access/manager/server.go internal/access/manager/server_test.go
git commit -m "feat: expose manager network summary API"
```

Expected: commit contains only manager access API changes.

---

### Task 4: Web Manager API Types And Client

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client test**

In `web/src/lib/manager-api.test.ts`, import `getNetworkSummary` and add:

```ts
it("fetches network summary from the manager network endpoint", async () => {
  const summary = {
    generated_at: "2026-04-29T12:00:00Z",
    scope: { view: "local_node", local_node_id: 1, controller_leader_id: 1 },
    source_status: { local_collector: "ok", controller_context: "ok", runtime_views: "ok", errors: {} },
    headline: {
      remote_peers: 1,
      alive_nodes: 2,
      suspect_nodes: 0,
      dead_nodes: 0,
      draining_nodes: 0,
      pool_active: 3,
      pool_idle: 4,
      rpc_inflight: 1,
      dial_errors_1m: 0,
      queue_full_1m: 0,
      timeouts_1m: 1,
      stale_observations: 0,
    },
    traffic: {
      scope: "local_total_by_msg_type",
      tx_bytes_1m: 1024,
      rx_bytes_1m: 512,
      tx_bps: 17.06,
      rx_bps: 8.53,
      peer_breakdown_available: false,
      by_message_type: [],
    },
    peers: [],
    services: [],
    channel_replication: {
      pool: { active: 0, idle: 0 },
      services: [],
      long_poll: { lane_count: 8, max_wait_ms: 200, max_bytes: 65536, max_channels: 64 },
      long_poll_timeouts_1m: 0,
      data_plane_rpc_timeout_ms: 1000,
    },
    discovery: {
      listen_addr: "0.0.0.0:7000",
      advertise_addr: "127.0.0.1:7000",
      seeds: [],
      static_nodes: [{ node_id: 1, addr: "127.0.0.1:7000" }],
      pool_size: 4,
      data_plane_pool_size: 4,
      dial_timeout_ms: 5000,
      controller_observation_interval_ms: 200,
    },
    events: [],
  }
  fetchMock.mockResolvedValue(new Response(JSON.stringify(summary), { status: 200 }))

  await expect(getNetworkSummary()).resolves.toEqual(summary)
  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/network/summary",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )
})
```

- [ ] **Step 2: Run test and confirm failure**

Run:

```bash
cd web && bun run test -- manager-api
```

Expected: FAIL because `getNetworkSummary` and network types do not exist.

- [ ] **Step 3: Add TypeScript response types**

In `web/src/lib/manager-api.types.ts`, add exported types mirroring the access JSON contract:

```ts
export type ManagerNetworkSummaryResponse = {
  generated_at: string
  scope: ManagerNetworkScope
  source_status: ManagerNetworkSourceStatus
  headline: ManagerNetworkHeadline
  traffic: ManagerNetworkTraffic
  peers: ManagerNetworkPeer[]
  services: ManagerNetworkRPCService[]
  channel_replication: ManagerNetworkChannelReplication
  discovery: ManagerNetworkDiscovery
  events: ManagerNetworkEvent[]
}
```

Also define `ManagerNetworkScope`, `ManagerNetworkSourceStatus`, `ManagerNetworkHeadline`, `ManagerNetworkTraffic`, `ManagerNetworkPeer`, `ManagerNetworkPoolStats`, `ManagerNetworkPeerPools`, `ManagerNetworkPeerRPC`, `ManagerNetworkPeerErrors`, `ManagerNetworkRPCService`, `ManagerNetworkChannelReplication`, `ManagerNetworkLongPollConfig`, `ManagerNetworkDiscovery`, `ManagerNetworkDiscoveryNode`, and `ManagerNetworkEvent`.

Use `number | null` for `success_rate`.
Include `expected_timeout_1m` on `ManagerNetworkRPCService` and `long_poll_timeouts_1m` on `ManagerNetworkChannelReplication` so normal long-poll wait expiry is visible without being rendered as an error.

- [ ] **Step 4: Add client method**

In `web/src/lib/manager-api.ts`:

```ts
import type { ManagerNetworkSummaryResponse } from "@/lib/manager-api.types"

export function getNetworkSummary() {
  return jsonManagerFetch<ManagerNetworkSummaryResponse>("/manager/network/summary")
}
```

Keep imports alphabetized enough to satisfy existing lint/formatter conventions.

- [ ] **Step 5: Run API client test**

Run:

```bash
cd web && bun run test -- manager-api
```

Expected: PASS.

- [ ] **Step 6: Commit web API slice**

Run:

```bash
git diff --check
git add web/src/lib/manager-api.ts web/src/lib/manager-api.types.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add web network summary client"
```

Expected: commit contains only web API/type changes.

---

### Task 5: Rebuild The Web Network Page

**Files:**
- Modify: `web/src/pages/network/page.tsx`
- Create: `web/src/pages/network/page.test.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Create `web/src/pages/network/page.test.tsx`. Mock `getNetworkSummary` like other page tests.

Minimum tests:

```ts
it("renders loading then local-node network summary", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture({ remotePeers: 1 }))
  renderNetworkPage()

  expect(screen.getByText(/Network/i)).toBeInTheDocument()
  expect(await screen.findByText(/Local-node view/i)).toBeInTheDocument()
  expect(screen.getByText(/local total by message type/i)).toBeInTheDocument()
  expect(screen.queryByText(/complete matrix/i)).not.toBeInTheDocument()
  expect(screen.getByText(/Remote Peers/i)).toBeInTheDocument()
  expect(screen.getByText(/Outbound Peers/i)).toBeInTheDocument()
  expect(screen.getByText(/node-2/i)).toBeInTheDocument()
  expect(screen.getByText(/channel_long_poll_fetch/i)).toBeInTheDocument()
})

it("renders single-node cluster empty state as healthy", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture({ remotePeers: 0, peers: [] }))
  renderNetworkPage()

  expect(await screen.findByText(/Single-node cluster/i)).toBeInTheDocument()
  expect(screen.getByText(/no remote node-to-node transport links/i)).toBeInTheDocument()
})

it("renders source status unavailable without hiding local collector data", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture({
    source_status: { local_collector: "ok", controller_context: "unavailable", runtime_views: "unavailable", errors: { nodes: "no leader" } },
  }))
  renderNetworkPage()

  expect(await screen.findByText(/Controller context unavailable/i)).toBeInTheDocument()
  expect(screen.getByText(/Local collector ok/i)).toBeInTheDocument()
})

it("refreshes network summary", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture({ remotePeers: 1 }))
  renderNetworkPage()
  await screen.findByText(/Remote Peers/i)

  await userEvent.click(screen.getByRole("button", { name: /refresh/i }))
  expect(getNetworkSummaryMock).toHaveBeenCalledTimes(2)
})

it("maps forbidden errors to resource state", async () => {
  getNetworkSummaryMock.mockRejectedValue(new ManagerApiError(403, "forbidden", "forbidden"))
  renderNetworkPage()
  expect(await screen.findByText(/Forbidden/i)).toBeInTheDocument()
})
```

Add helpers in the test file:

```ts
type NetworkSummaryFixtureOptions = Partial<ManagerNetworkSummaryResponse> & {
  remotePeers?: number
  peers?: ManagerNetworkSummaryResponse["peers"]
}

function networkSummaryFixture(options: NetworkSummaryFixtureOptions = {}): ManagerNetworkSummaryResponse {
  /* return complete fixture; map options.remotePeers to headline.remote_peers and
     preserve traffic.peer_breakdown_available=false by default */
}
function renderNetworkPage() { /* render with AppProviders or IntlProvider matching existing tests */ }
```

- [ ] **Step 2: Update page shell test mock and expected text**

In `web/src/pages/page-shells.test.tsx`:

- Add `const getNetworkSummaryMock = vi.fn()`.
- Add it to `vi.mock("@/lib/manager-api", ...)`.
- Reset it in `beforeEach`.
- Default it to a single-node network summary fixture.
- Replace `/network` unavailable expectation with a normal shell expectation, e.g. heading `Network` and section `Network Summary` or `Outbound Peers`.

- [ ] **Step 3: Run page tests and confirm failure**

Run:

```bash
cd web && bun run test -- network page-shells
```

Expected: FAIL because page still renders placeholder and i18n keys are missing.

- [ ] **Step 4: Implement page state and loading/error handling**

In `web/src/pages/network/page.tsx`, follow the `DashboardPage` pattern:

- `useState` for `{ summary, loading, refreshing, error }`.
- `useCallback` `loadNetworkSummary(refreshing: boolean)` calling `getNetworkSummary()`.
- `useEffect` initial load.
- `mapErrorKind` matching dashboard/nodes pages: `403 -> forbidden`, `503 -> unavailable`, default `error`.
- Header actions: refresh button only for now; do not implement auto-refresh unless tests/spec are expanded.

- [ ] **Step 5: Implement header and summary cards**

Header badges:

- `Local-node view`
- `Local Node: <id>`
- `Controller Leader: <id>`
- `Generated: <formatted time>`
- `Single-node cluster` only when `headline.remote_peers === 0`

Summary cards:

- `Remote Peers`: `headline.remote_peers`
- `Node Health`: alive / suspect / dead / draining
- `Pool Connections`: active / idle
- `RPC Inflight`: `headline.rpc_inflight`
- `Network Errors`: dial / queue full / abnormal RPC timeout in 1m; explicitly exclude normal `channel_long_poll_fetch` wait timeouts.
- `Observation Freshness`: stale runtime observations

- [ ] **Step 6: Implement main sections**

Use existing `SectionCard`, `ResourceState`, and `StatusBadge` components.

Sections:

1. `Outbound Peers`
   - If `summary.peers.length === 0`, render single-node empty message:
     - English: `This is a single-node cluster, so there are no remote node-to-node transport links. Gateway client connections are shown on the Connections page.`
     - Chinese equivalent in `zh-CN.ts`.
   - Else render peer cards or table with node, health, cluster/data-plane pools, RPC inflight, errors.
   - Do not call it a complete matrix.
2. `RPC Services`
   - Table columns: service, group, target, inflight, calls 1m, p95, errors, last seen.
   - For no samples, render `Insufficient samples` for latency/success rate.
3. `Channel Replication`
   - Show data-plane pool active/idle.
   - Show long-poll config: lane count, max wait, max bytes, max channels.
   - Show `long_poll_timeouts_1m` as neutral `Long-poll wait expiries`, not as an error.
   - Show filtered services where `group === "channel_data_plane"`.
   - Do not show lane reset/backpressure fields.
4. `Discovery & Config`
   - Show listen addr, advertise addr, seeds, static nodes, pool sizes, dial timeout, controller observation interval, data-plane RPC timeout.
   - Add a neutral warning text if advertise addr starts with `0.0.0.0` or `[::]`.
5. `Network Events`
   - Show recent capped events; empty state if none.

- [ ] **Step 7: Add i18n keys**

Add matching keys to both `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts`.

Recommended key groups:

```ts
"network.title"
"network.description"
"network.scope.localNode"
"network.scope.singleNodeCluster"
"network.generatedAtValue"
"network.localNodeValue"
"network.controllerLeaderValue"
"network.summary.title"
"network.summary.remotePeers"
"network.summary.nodeHealth"
"network.summary.poolConnections"
"network.summary.rpcInflight"
"network.summary.networkErrors"
"network.summary.observationFreshness"
"network.outboundPeers.title"
"network.outboundPeers.description"
"network.outboundPeers.singleNodeEmpty"
"network.rpcServices.title"
"network.channelReplication.title"
"network.discovery.title"
"network.events.title"
"network.source.localCollectorOk"
"network.source.controllerUnavailable"
"network.source.runtimeViewsUnavailable"
"network.latency.insufficientSamples"
```

Keep existing `nav.network.*` keys if navigation already uses them; update obsolete placeholder keys only if no other page uses them.

- [ ] **Step 8: Run focused web tests**

Run:

```bash
cd web && bun run test -- network page-shells
```

Expected: PASS.

- [ ] **Step 9: Run related web tests**

Run:

```bash
cd web && bun run test -- manager-api dashboard nodes
```

Expected: PASS.

- [ ] **Step 10: Commit network page slice**

Run:

```bash
git diff --check
git add web/src/pages/network/page.tsx web/src/pages/network/page.test.tsx web/src/pages/page-shells.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: rebuild manager network page"
```

Expected: commit contains only web page/i18n/test changes.

---

### Task 6: End-To-End Verification And Cleanup

**Files:**
- No new files expected.
- Only touch previous task files if verification exposes bugs.

- [ ] **Step 1: Check working tree before verification**

Run:

```bash
git status --short
```

Expected: only known user delivery runtime files may remain dirty. If any uncommitted network files remain, either commit them or explain why before final handoff.

- [ ] **Step 2: Run backend focused verification**

Run:

```bash
go test ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Run transport/cluster compatibility tests if hooks or interfaces changed unexpectedly**

Run if Task 2 touched `pkg/transport`, `pkg/cluster`, or behavior beyond app hook wiring:

```bash
go test ./pkg/transport ./pkg/cluster -count=1
```

Expected: PASS. If these packages were not touched, this step may be skipped with a note.

- [ ] **Step 4: Run web verification**

Run:

```bash
cd web && bun run test -- manager-api network page-shells
```

Expected: PASS.

If time allows, also run:

```bash
cd web && bun run test
```

Expected: PASS.

- [ ] **Step 5: Inspect final diff for scope and unsupported claims**

Run:

```bash
git log --oneline -5
git diff --check
git status --short
rg -n "matrix|per-peer traffic|lane reset|backpressure|standalone|single-machine|单机" web/src internal/access/manager internal/usecase/management internal/app
```

Expected:

- `git diff --check` PASS.
- No new unsupported “complete matrix” or per-peer traffic claims.
- No standalone/single-machine wording.
- `git status --short` does not include uncommitted network files.

- [ ] **Step 6: Final handoff**

Summarize:

- Backend API path: `GET /manager/network/summary`.
- Scope: local-node network view.
- Tests run and pass/fail status.
- Any skipped verification and reason.
- Reminder if pre-existing delivery runtime dirty files still exist and were not touched.
