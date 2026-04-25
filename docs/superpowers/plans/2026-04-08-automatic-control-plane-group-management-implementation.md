# Automatic Control-Plane Group Management Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build dedicated controller-raft-driven automatic management for control-plane `multiraft` groups, replacing static business-group peer lists with controller-assigned peers while keeping `SlotForKey -> GroupID` stable.

**Architecture:** Add a dedicated controller plane in three layers: `pkg/storage/controllermeta` persists controller state, `pkg/cluster/groupcontroller` owns placement/reconcile decisions, and `pkg/cluster/controllerraft` replicates that state on an independent Raft quorum derived from `WK_CLUSTER_NODES` plus `WK_CLUSTER_CONTROLLER_REPLICA_N`. Refactor `pkg/cluster/raftcluster` into a dynamic managed-group runtime with a node-local `GroupAgent`, while `internal/app` and `cmd/wukongim` move from static `WK_CLUSTER_GROUPS` startup to controller-driven bootstrap, repair, and rebalance.

**Tech Stack:** Go, direct `go.etcd.io/raft/v3` for controller Raft, existing `pkg/replication/multiraft`, Pebble-backed storage, `pkg/transport/nodetransport`, `testify/require`

---

## Implementation Sequence

Follow the spec’s rollout order in code:

1. bootstrap config + controller persistence foundation
2. controller observation and recommendation logic
3. repair-only execution
4. rebalance + draining
5. remove legacy static business-group peer configuration

Do not change `SlotForKey`, business key routing, or `pkg/replication/multiraft` semantics unless a failing test proves a concrete API gap.

## File Map

- Modify: `internal/app/config.go`
  - add controller/group replica configuration and controller storage defaults
  - keep `Groups` only as a temporary migration field until the final removal task
- Modify: `internal/app/config_test.go`
  - cover automatic-group validation, controller peer derivation, and storage-path defaults
- Modify: `internal/app/build.go`
  - wire controller storage, derived controller peers, and new `raftcluster.Config`
- Modify: `internal/app/lifecycle.go`
  - replace static-leader wait with managed-group readiness from `raftcluster`
- Modify: `internal/app/app.go`
  - add controller storage handles and close ordering
- Modify: `internal/app/multinode_integration_test.go`
  - switch the app harness from static `Cluster.Groups` to controller-driven startup
- Modify: `internal/app/conversation_sync_integration_test.go`
  - update fixtures to the new cluster config surface
- Modify: `cmd/wukongim/config.go`
  - parse `WK_CLUSTER_CONTROLLER_REPLICA_N`, `WK_CLUSTER_GROUP_REPLICA_N`, and optional controller storage overrides
  - stop requiring `WK_CLUSTER_GROUPS` in final state
- Modify: `cmd/wukongim/config_test.go`
  - cover the new config keys and the no-`WK_CLUSTER_GROUPS` path
- Modify: `wukongim.conf.example`
  - replace static group peers with automatic-group configuration

- Create: `pkg/storage/controllermeta/types.go`
  - `ClusterNode`, `GroupAssignment`, `GroupRuntimeView`, `ReconcileTask`, node/task enums
- Create: `pkg/storage/controllermeta/codec.go`
  - deterministic encoding/decoding for persisted controller records and snapshots
- Create: `pkg/storage/controllermeta/store.go`
  - Pebble-backed CRUD for nodes, assignments, runtime views, controller tasks, controller membership metadata
- Create: `pkg/storage/controllermeta/snapshot.go`
  - export/import helpers for controller state-machine snapshots
- Create: `pkg/storage/controllermeta/store_test.go`
  - CRUD and snapshot round-trip coverage

- Create: `pkg/cluster/groupcontroller/types.go`
  - planner inputs, outputs, status enums, reconcile-step constants
- Create: `pkg/cluster/groupcontroller/commands.go`
  - controller command envelope for reports, operator requests, and assignment/task updates
- Create: `pkg/cluster/groupcontroller/planner.go`
  - placement math, repair/rebalance prioritization, degraded-group handling
- Create: `pkg/cluster/groupcontroller/statemachine.go`
  - apply controller commands onto `controllermeta.Store`
- Create: `pkg/cluster/groupcontroller/controller.go`
  - leader-only reconcile loop on top of persisted controller state
- Create: `pkg/cluster/groupcontroller/controller_test.go`
  - node-state transitions, bootstrap-task creation, repair priority, rebalance gating, draining behavior

- Create: `pkg/cluster/controllerraft/config.go`
  - controller-raft config, peer derivation helpers, bootstrap gating inputs
- Create: `pkg/cluster/controllerraft/service.go`
  - dedicated controller Raft node lifecycle using shared `nodetransport`
- Create: `pkg/cluster/controllerraft/transport.go`
  - controller-raft message encoding and transport glue
- Create: `pkg/cluster/controllerraft/service_test.go`
  - smallest-node bootstrap, join-wait, restart-from-persisted-state

- Modify: `pkg/cluster/raftcluster/config.go`
  - remove hard dependency on static business `Groups`
  - add controller/group replica config, controller storage paths, optional legacy groups for migration
- Modify: `pkg/cluster/raftcluster/config_test.go`
  - validate automatic-group config and migration rules
- Modify: `pkg/cluster/raftcluster/cluster.go`
  - split startup into server, controller-raft, controller client, `multiraft.Runtime`, and `GroupAgent`
- Create: `pkg/cluster/raftcluster/controller_client.go`
  - RPC client for heartbeats, runtime reports, assignments, and operator requests
- Create: `pkg/cluster/raftcluster/assignment_cache.go`
  - thread-safe local cache of controller assignments and epochs
- Create: `pkg/cluster/raftcluster/managed_groups.go`
  - open/bootstrap/close business groups dynamically from controller assignments
- Create: `pkg/cluster/raftcluster/agent.go`
  - node-local `GroupAgent` heartbeat/report/apply loop
- Create: `pkg/cluster/raftcluster/readiness.go`
  - startup readiness for “all managed groups assigned and leader-known”
- Create: `pkg/cluster/raftcluster/operator.go`
  - `MarkNodeDraining`, `ResumeNode`, `ListGroupAssignments`, `ForceReconcile`, `TransferGroupLeader`, `RecoverGroup(groupID, strategy)`
- Modify: `pkg/cluster/raftcluster/errors.go`
  - add controller/assignment/operator sentinel errors as needed
- Modify: `pkg/cluster/raftcluster/cluster_test.go`
  - multi-node controller bootstrap, repair, rebalance, draining, leader failover

- Modify: `pkg/storage/metastore/testutil_test.go`
  - update `raftcluster.Config` fixtures to the new fields
- Modify: `pkg/storage/metastore/integration_test.go`
  - keep metastore integration green with controller-driven cluster startup

## Task 1: Introduce Automatic-Group Config Surface And Derived Controller Peers

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `internal/app/build.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`
- Modify: `pkg/cluster/raftcluster/config.go`
- Modify: `pkg/cluster/raftcluster/config_test.go`
- Modify: `pkg/storage/metastore/testutil_test.go`
- Modify: `pkg/storage/metastore/integration_test.go`

- [ ] **Step 1: Add failing app-config tests for automatic-group validation**

```go
func TestConfigValidateAllowsAutomaticGroupManagementWithoutStaticGroups(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.Groups = nil
	cfg.Cluster.GroupCount = 8
	cfg.Cluster.ControllerReplicaN = 3
	cfg.Cluster.GroupReplicaN = 3

	require.NoError(t, cfg.ApplyDefaultsAndValidate())
	require.Equal(t, []NodeConfigRef{
		{ID: 1, Addr: "127.0.0.1:7000"},
		{ID: 2, Addr: "127.0.0.1:7001"},
		{ID: 3, Addr: "127.0.0.1:7002"},
	}, cfg.Cluster.DerivedControllerNodes())
}

func TestConfigValidateRejectsInvalidControllerReplicaN(t *testing.T) {
	cfg := validConfig()
	cfg.Cluster.ControllerReplicaN = 4
	cfg.Cluster.Nodes = []NodeConfigRef{
		{ID: 3, Addr: "127.0.0.1:7002"},
		{ID: 1, Addr: "127.0.0.1:7000"},
		{ID: 2, Addr: "127.0.0.1:7001"},
	}

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}

func TestConfigValidateRejectsLocalNodeMissingFromClusterNodes(t *testing.T) {
	cfg := validConfig()
	cfg.Node.ID = 9
	cfg.Cluster.Groups = nil
	cfg.Cluster.GroupCount = 8
	cfg.Cluster.ControllerReplicaN = 3
	cfg.Cluster.GroupReplicaN = 3

	require.Error(t, cfg.ApplyDefaultsAndValidate())
}
```

- [ ] **Step 2: Run the focused app-config tests and verify RED**

Run: `go test ./internal/app -run 'TestConfigValidate(AllowsAutomaticGroupManagementWithoutStaticGroups|RejectsInvalidControllerReplicaN|RejectsLocalNodeMissingFromClusterNodes)'`

Expected: FAIL because `ClusterConfig` does not yet expose `ControllerReplicaN`, `GroupReplicaN`, or `DerivedControllerNodes`, and validation still requires `Cluster.Groups`.

- [ ] **Step 3: Add failing loader and raftcluster-config tests for the new keys**

```go
func TestBuildAppConfigParsesAutomaticGroupManagementKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_STORAGE_CONTROLLER_META_PATH="+filepath.Join(dir, "controller-meta"),
		"WK_STORAGE_CONTROLLER_RAFT_PATH="+filepath.Join(dir, "controller-raft"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_GROUP_COUNT=8",
		"WK_CLUSTER_CONTROLLER_REPLICA_N=3",
		"WK_CLUSTER_GROUP_REPLICA_N=3",
		`WK_CLUSTER_NODES=[{"id":3,"addr":"127.0.0.1:7002"},{"id":1,"addr":"127.0.0.1:7000"},{"id":2,"addr":"127.0.0.1:7001"}]`,
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, 3, cfg.Cluster.ControllerReplicaN)
	require.Equal(t, 3, cfg.Cluster.GroupReplicaN)
	require.Equal(t, filepath.Join(dir, "controller-meta"), cfg.Storage.ControllerMetaPath)
	require.Equal(t, filepath.Join(dir, "controller-raft"), cfg.Storage.ControllerRaftPath)
	require.Nil(t, cfg.Cluster.Groups)
}

func TestConfigValidateAllowsDynamicManagedGroups(t *testing.T) {
	cfg := validTestConfig()
	cfg.Groups = nil
	cfg.GroupCount = 8
	cfg.ControllerReplicaN = 3
	cfg.GroupReplicaN = 3

	require.NoError(t, cfg.validate())
}

func TestConfigValidateRejectsDynamicManagedGroupsWhenLocalNodeMissing(t *testing.T) {
	cfg := validTestConfig()
	cfg.Groups = nil
	cfg.GroupCount = 8
	cfg.ControllerReplicaN = 3
	cfg.GroupReplicaN = 3
	cfg.NodeID = 99

	require.Error(t, cfg.validate())
}
```

- [ ] **Step 4: Run loader and raftcluster-config tests and verify RED**

Run: `go test ./cmd/wukongim ./pkg/cluster/raftcluster -run 'Test(BuildAppConfigParsesAutomaticGroupManagementKeys|ConfigValidateAllowsDynamicManagedGroups|ConfigValidateRejectsDynamicManagedGroupsWhenLocalNodeMissing)'`

Expected: FAIL because the config loader only knows static `WK_CLUSTER_GROUPS`, and `raftcluster.Config` still requires `Groups`.

- [ ] **Step 5: Implement the new config surface and migration-safe defaults**

Use this shape as the target:

```go
type StorageConfig struct {
	DBPath             string
	RaftPath           string
	ChannelLogPath     string
	ControllerMetaPath string
	ControllerRaftPath string
}

type ClusterConfig struct {
	ListenAddr          string
	GroupCount          uint32
	Nodes               []NodeConfigRef
	Groups              []GroupConfig // legacy-only until Task 7 removes it
	ControllerReplicaN  int
	GroupReplicaN       int
	ForwardTimeout      time.Duration
	PoolSize            int
	TickInterval        time.Duration
	RaftWorkers         int
	ElectionTick        int
	HeartbeatTick       int
	DialTimeout         time.Duration
	DataPlaneRPCTimeout time.Duration
}

func (c ClusterConfig) DerivedControllerNodes() []NodeConfigRef {
	nodes := append([]NodeConfigRef(nil), c.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	if c.ControllerReplicaN < len(nodes) {
		nodes = nodes[:c.ControllerReplicaN]
	}
	return nodes
}
```

Implementation notes:

- default `ControllerMetaPath` to `filepath.Join(Node.DataDir, "controller-meta")`
- default `ControllerRaftPath` to `filepath.Join(Node.DataDir, "controller-raft")`
- keep validating that `Cluster.Nodes` contains the local node even after static business peers are removed
- validate controller replica count against `len(Cluster.Nodes)`
- validate group replica count is positive and satisfiable by the static node set
- validate controller storage paths do not alias `DBPath`, `RaftPath`, or `ChannelLogPath`
- validate `ControllerMetaPath` and `ControllerRaftPath` do not alias each other
- keep `Cluster.Groups` temporarily accepted for migration, but stop requiring it in validation
- parse optional `WK_STORAGE_CONTROLLER_META_PATH` / `WK_STORAGE_CONTROLLER_RAFT_PATH`
- update `build.go` and `raftcluster.Config` to pass controller fields through without opening business groups yet
- update all direct `raftcluster.Config` fixtures in `pkg/storage/metastore/*_test.go`

- [ ] **Step 6: Run the focused config suites and verify GREEN**

Run: `go test ./internal/app ./cmd/wukongim ./pkg/cluster/raftcluster ./pkg/storage/metastore -run 'Test(ConfigValidate|BuildAppConfig|LoadConfig|ConfigValidateAllowsDynamicManagedGroups)'`

Expected: PASS for the newly added config cases and existing metastore fixtures.

- [ ] **Step 7: Commit the config foundation**

```bash
git add internal/app/config.go internal/app/config_test.go internal/app/build.go \
  cmd/wukongim/config.go cmd/wukongim/config_test.go wukongim.conf.example \
  pkg/cluster/raftcluster/config.go pkg/cluster/raftcluster/config_test.go \
  pkg/storage/metastore/testutil_test.go pkg/storage/metastore/integration_test.go
git commit -m "feat(cluster): add automatic control-plane group config surface"
```

## Task 2: Build Dedicated Controller Metadata Storage

**Files:**
- Create: `pkg/storage/controllermeta/types.go`
- Create: `pkg/storage/controllermeta/codec.go`
- Create: `pkg/storage/controllermeta/store.go`
- Create: `pkg/storage/controllermeta/snapshot.go`
- Create: `pkg/storage/controllermeta/store_test.go`

- [ ] **Step 1: Write failing CRUD and snapshot tests for controller metadata**

```go
func TestStoreAssignmentAndTaskRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertAssignment(ctx, GroupAssignment{
		GroupID:        7,
		DesiredPeers:   []uint64{1, 2, 3},
		ConfigEpoch:    11,
		BalanceVersion: 3,
	}))
	require.NoError(t, store.UpsertTask(ctx, ReconcileTask{
		GroupID:    7,
		Kind:       TaskKindRepair,
		Step:       TaskStepAddLearner,
		SourceNode: 4,
		TargetNode: 2,
		Attempt:    1,
	}))

	assignment, err := store.GetAssignment(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, assignment.DesiredPeers)

	task, err := store.GetTask(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, TaskKindRepair, task.Kind)
}

func TestStoreSnapshotRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertNode(ctx, ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7000",
		Status:          NodeStatusAlive,
		LastHeartbeatAt: time.Unix(11, 0),
		CapacityWeight:  1,
	}))
	require.NoError(t, store.UpsertAssignment(ctx, GroupAssignment{GroupID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1}))
	require.NoError(t, store.UpsertRuntimeView(ctx, GroupRuntimeView{
		GroupID:             1,
		CurrentPeers:        []uint64{1, 2, 3},
		LeaderID:            2,
		HasQuorum:           true,
		ObservedConfigEpoch: 1,
		LastReportAt:        time.Unix(12, 0),
	}))

	snap, err := store.ExportSnapshot(ctx)
	require.NoError(t, err)

	restored := openTestStore(t)
	require.NoError(t, restored.ImportSnapshot(ctx, snap))
	assignment, err := restored.GetAssignment(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, assignment.DesiredPeers)

	node, err := restored.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, time.Unix(11, 0), node.LastHeartbeatAt)
	require.Equal(t, 1, node.CapacityWeight)

	view, err := restored.GetRuntimeView(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(2), view.LeaderID)
	require.Equal(t, uint64(1), view.ObservedConfigEpoch)
	require.Equal(t, time.Unix(12, 0), view.LastReportAt)
}

func TestStoreListsControllerStateForPlannerQueries(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertNode(ctx, ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7000",
		Status:          NodeStatusAlive,
		LastHeartbeatAt: time.Unix(20, 0),
		CapacityWeight:  1,
	}))
	require.NoError(t, store.UpsertAssignment(ctx, GroupAssignment{GroupID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 2}))
	require.NoError(t, store.UpsertRuntimeView(ctx, GroupRuntimeView{
		GroupID:             1,
		CurrentPeers:        []uint64{1, 2, 3},
		HasQuorum:           true,
		ObservedConfigEpoch: 2,
		LastReportAt:        time.Unix(21, 0),
	}))
	require.NoError(t, store.UpsertTask(ctx, ReconcileTask{GroupID: 1, Kind: TaskKindRepair, Step: TaskStepAddLearner}))

	nodes, err := store.ListNodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Equal(t, time.Unix(20, 0), nodes[0].LastHeartbeatAt)
	require.Equal(t, 1, nodes[0].CapacityWeight)

	assignments, err := store.ListAssignments(ctx)
	require.NoError(t, err)
	require.Len(t, assignments, 1)

	views, err := store.ListRuntimeViews(ctx)
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, uint64(2), views[0].ObservedConfigEpoch)
	require.Equal(t, time.Unix(21, 0), views[0].LastReportAt)

	tasks, err := store.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
}
```

- [ ] **Step 2: Run controller-metadata tests and verify RED**

Run: `go test ./pkg/storage/controllermeta -run 'TestStore(AssignmentAndTaskRoundTrip|SnapshotRoundTrip|ListsControllerStateForPlannerQueries)'`

Expected: FAIL because the package does not exist yet.

- [ ] **Step 3: Implement deterministic controller metadata persistence**

Use a small, focused API:

```go
type Store struct {
	db *pebble.DB
}

func (s *Store) GetNode(ctx context.Context, nodeID uint64) (ClusterNode, error)
func (s *Store) ListNodes(ctx context.Context) ([]ClusterNode, error)
func (s *Store) UpsertNode(ctx context.Context, node ClusterNode) error
func (s *Store) GetAssignment(ctx context.Context, groupID uint32) (GroupAssignment, error)
func (s *Store) ListAssignments(ctx context.Context) ([]GroupAssignment, error)
func (s *Store) UpsertAssignment(ctx context.Context, assignment GroupAssignment) error
func (s *Store) GetRuntimeView(ctx context.Context, groupID uint32) (GroupRuntimeView, error)
func (s *Store) ListRuntimeViews(ctx context.Context) ([]GroupRuntimeView, error)
func (s *Store) UpsertRuntimeView(ctx context.Context, view GroupRuntimeView) error
func (s *Store) GetTask(ctx context.Context, groupID uint32) (ReconcileTask, error)
func (s *Store) ListTasks(ctx context.Context) ([]ReconcileTask, error)
func (s *Store) UpsertTask(ctx context.Context, task ReconcileTask) error
func (s *Store) ExportSnapshot(ctx context.Context) ([]byte, error)
func (s *Store) ImportSnapshot(ctx context.Context, data []byte) error
```

Implementation notes:

- use stable key prefixes per record type
- encode slices in sorted order where ordering matters (`DesiredPeers`, `CurrentPeers`)
- keep snapshots versioned and self-describing
- persist controller membership metadata separately from per-group assignment/task data
- keep the persisted model aligned with the spec:
  - `ClusterNode` includes `LastHeartbeatAt` and `CapacityWeight`
  - `GroupRuntimeView` includes `ObservedConfigEpoch` and `LastReportAt`
- ensure every `List*` method returns deterministic order so planner decisions and tests are stable

- [ ] **Step 4: Run controller-metadata tests and verify GREEN**

Run: `go test ./pkg/storage/controllermeta`

Expected: PASS.

- [ ] **Step 5: Commit the storage layer**

```bash
git add pkg/storage/controllermeta
git commit -m "feat(cluster): add controller metadata storage"
```

## Task 3: Implement GroupController Planning And Controller State Machine

**Files:**
- Create: `pkg/cluster/groupcontroller/types.go`
- Create: `pkg/cluster/groupcontroller/commands.go`
- Create: `pkg/cluster/groupcontroller/planner.go`
- Create: `pkg/cluster/groupcontroller/statemachine.go`
- Create: `pkg/cluster/groupcontroller/controller.go`
- Create: `pkg/cluster/groupcontroller/controller_test.go`

- [ ] **Step 1: Write failing planner tests for bootstrap, repair, rebalance, and degraded groups**

```go
func TestPlannerCreatesBootstrapTaskForBrandNewGroup(t *testing.T) {
	planner := NewPlanner(PlannerConfig{GroupCount: 4, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
	)

	decision, err := planner.ReconcileGroup(context.Background(), state, 1)
	require.NoError(t, err)
	require.Equal(t, TaskKindBootstrap, decision.Task.Kind)
	require.Len(t, decision.Assignment.DesiredPeers, 3)
}

func TestPlannerPrefersRepairBeforeRebalance(t *testing.T) {
	planner := NewPlanner(PlannerConfig{GroupCount: 2, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 2, 4),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(2, []uint64{1, 2, 4}, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, uint32(1), decision.GroupID)
	require.Equal(t, TaskKindRepair, decision.Task.Kind)
}

func TestPlannerDoesNotMigrateOnSuspectNode(t *testing.T) {
	planner := NewPlanner(PlannerConfig{GroupCount: 1, ReplicaN: 3, RebalanceSkewThreshold: 2})
	state := testState(
		aliveNode(1), aliveNode(2), suspectNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
	)

	decision, err := planner.ReconcileGroup(context.Background(), state, 1)
	require.NoError(t, err)
	require.Nil(t, decision.Task)
	require.False(t, decision.Degraded)
}

func TestPlannerRebalancesOnlyWhenSkewExceedsThreshold(t *testing.T) {
	planner := NewPlanner(PlannerConfig{GroupCount: 4, ReplicaN: 3, RebalanceSkewThreshold: 2})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 2, 3),
		withAssignment(3, 1, 2, 3),
		withAssignment(4, 1, 2, 4),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(2, []uint64{1, 2, 3}, true),
		withRuntimeView(3, []uint64{1, 2, 3}, true),
		withRuntimeView(4, []uint64{1, 2, 4}, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, TaskKindRebalance, decision.Task.Kind)
	require.Equal(t, uint64(4), decision.Task.TargetNode)
}

func TestPlannerStopsAutomaticChangesAfterQuorumLoss(t *testing.T) {
	planner := NewPlanner(PlannerConfig{GroupCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), deadNode(2), deadNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withRuntimeView(1, []uint64{1}, false),
	)

	decision, err := planner.ReconcileGroup(context.Background(), state, 1)
	require.NoError(t, err)
	require.Nil(t, decision.Task)
	require.True(t, decision.Degraded)
}
```

- [ ] **Step 2: Run the planner tests and verify RED**

Run: `go test ./pkg/cluster/groupcontroller -run 'TestPlanner(CreatesBootstrapTaskForBrandNewGroup|PrefersRepairBeforeRebalance|DoesNotMigrateOnSuspectNode|RebalancesOnlyWhenSkewExceedsThreshold|StopsAutomaticChangesAfterQuorumLoss)'`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement planner types and controller-command apply path**

Target the following boundaries:

```go
type PlannerConfig struct {
	GroupCount             uint32
	ReplicaN               int
	RebalanceSkewThreshold int
	MaxTaskAttempts        int
	RetryBackoffBase       time.Duration
}

type Command struct {
	Kind    CommandKind
	Report  *AgentReport
	Op      *OperatorRequest
	Advance *TaskAdvance
}

type StateMachine struct {
	store *controllermeta.Store
}

func (sm *StateMachine) Apply(ctx context.Context, cmd Command) error
func (c *Controller) Tick(ctx context.Context) error
```

Implementation notes:

- keep desired state and observed state separate in the store
- model `Alive`, `Suspect`, `Dead`, `Draining` exactly once in `types.go`
- build planner snapshots from `ListNodes`, `ListAssignments`, `ListRuntimeViews`, and `ListTasks`
- treat `Suspect` nodes as migration-ineligible: do not repair away from them yet, and do not place new replicas onto them
- bootstrap tasks only when there is no assignment history and no runtime view
- keep each reconcile task to a single source/target pair
- only trigger rebalance when `maxAssignedGroups-minAssignedGroups >= RebalanceSkewThreshold`; stop generating further rebalance tasks once skew falls below that threshold, and use `BalanceVersion` to avoid oscillating on the same group
- retry failed reconcile steps with exponential backoff derived from `RetryBackoffBase`, stop after `MaxTaskAttempts`, and persist an operator-visible terminal failure state (`TaskStatusFailed` + `LastError`) instead of retrying forever
- mark quorum-lost groups degraded and do not generate auto-repair tasks

- [ ] **Step 4: Add failing state-machine tests for `Suspect -> Dead -> Alive` and `Draining -> Alive`**

```go
func TestStateMachineTransitionsNodeStatusFromSuspectToDeadToAlive(t *testing.T) {
	store := openTestStore(t)
	sm := NewStateMachine(store, StateMachineConfig{
		SuspectTimeout: time.Second,
		DeadTimeout:    2 * time.Second,
	})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeHeartbeat,
		Report: &AgentReport{
			NodeID:     1,
			Addr:       "127.0.0.1:7000",
			ObservedAt: time.Unix(0, 0),
		},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind:    CommandKindEvaluateTimeouts,
		Advance: &TaskAdvance{Now: time.Unix(2, 0)},
	}))

	node, err := store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusSuspect, node.Status)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind:    CommandKindEvaluateTimeouts,
		Advance: &TaskAdvance{Now: time.Unix(3, 0)},
	}))
	node, err = store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusDead, node.Status)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeHeartbeat,
		Report: &AgentReport{
			NodeID:     1,
			Addr:       "127.0.0.1:7000",
			ObservedAt: time.Unix(4, 0),
		},
	}))
	node, err = store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusAlive, node.Status)
}

func TestStateMachineRequiresResumeToLeaveDraining(t *testing.T) {
	store := openTestStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorMarkNodeDraining, NodeID: 2},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeHeartbeat,
		Report: &AgentReport{
			NodeID:     2,
			Addr:       "127.0.0.1:7001",
			ObservedAt: time.Unix(10, 0),
		},
	}))

	node, err := store.GetNode(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, NodeStatusDraining, node.Status)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorResumeNode, NodeID: 2},
	}))
	node, err = store.GetNode(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, NodeStatusAlive, node.Status)
}

func TestStateMachineMarksTaskFailedAfterRetryExhaustion(t *testing.T) {
	store := openTestStore(t)
	sm := NewStateMachine(store, StateMachineConfig{
		MaxTaskAttempts:  3,
		RetryBackoffBase: time.Second,
	})
	ctx := context.Background()

	require.NoError(t, store.UpsertTask(ctx, ReconcileTask{
		GroupID:    1,
		Kind:       TaskKindRepair,
		Step:       TaskStepAddLearner,
		Attempt:    2,
		NextRunAt:  time.Unix(10, 0),
		LastError:  "previous failure",
		Status:     TaskStatusRetrying,
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindTaskResult,
		Advance: &TaskAdvance{
			GroupID: 1,
			Now:     time.Unix(11, 0),
			Err:     errors.New("learner catch-up timeout"),
		},
	}))

	task, err := store.GetTask(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, TaskStatusFailed, task.Status)
	require.Equal(t, 3, task.Attempt)
	require.Contains(t, task.LastError, "learner catch-up timeout")
}
```

- [ ] **Step 5: Run the full groupcontroller suite and verify GREEN**

Run: `go test ./pkg/cluster/groupcontroller`

Expected: PASS.

- [ ] **Step 6: Commit the controller logic**

```bash
git add pkg/cluster/groupcontroller
git commit -m "feat(cluster): add controller planning and state machine"
```

## Task 4: Implement Dedicated Controller Raft Bootstrap And Persistence

**Files:**
- Create: `pkg/cluster/controllerraft/config.go`
- Create: `pkg/cluster/controllerraft/service.go`
- Create: `pkg/cluster/controllerraft/transport.go`
- Create: `pkg/cluster/controllerraft/service_test.go`

- [ ] **Step 1: Write failing controller-raft tests for smallest-node bootstrap and restart semantics**

```go
func TestServiceBootstrapsOnlyOnSmallestDerivedPeer(t *testing.T) {
	env := newThreeNodeControllerEnv(t)
	defer env.Close()

	require.NoError(t, env.StartNode(2))
	require.NoError(t, env.StartNode(3))
	require.NoError(t, env.StartNode(1))

	require.True(t, env.Node(1).BootstrapIssued())
	require.False(t, env.Node(2).BootstrapIssued())
	require.False(t, env.Node(3).BootstrapIssued())
}

func TestServiceRestartUsesPersistedMembershipInsteadOfConfigDrift(t *testing.T) {
	env := newThreeNodeControllerEnv(t)
	defer env.Close()

	require.NoError(t, env.StartAll())
	require.NoError(t, env.RestartNodeWithConfigDrift(1, []uint64{1, 2}))

	peers := env.Node(1).PersistedPeers()
	require.Equal(t, []uint64{1, 2, 3}, peers)
}
```

- [ ] **Step 2: Run controller-raft tests and verify RED**

Run: `go test ./pkg/cluster/controllerraft -run 'TestService(BootstrapsOnlyOnSmallestDerivedPeer|RestartUsesPersistedMembershipInsteadOfConfigDrift)'`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement the independent controller Raft service**

Use the shared cluster transport rather than creating a second listener:

```go
type Config struct {
	NodeID         uint64
	Peers          []Peer
	AllowBootstrap bool
	LogDB          *raftstorage.DB
	StateMachine   *groupcontroller.StateMachine
	Server         *nodetransport.Server
	RPCMux         *nodetransport.RPCMux
	Pool           *nodetransport.Pool
}

type Service struct {
	cfg Config
}

func (s *Service) Start(ctx context.Context) error
func (s *Service) Stop() error
func (s *Service) LeaderID() uint64
func (s *Service) Propose(ctx context.Context, cmd groupcontroller.Command) error
```

Implementation notes:

- persist controller Raft log/state under `ControllerRaftPath`
- if local persisted controller state exists, open it and trust persisted membership
- otherwise derive peers from config and only allow the smallest peer to call bootstrap
- non-bootstrap peers must start in join-wait mode and never issue concurrent bootstrap
- keep the controller group namespace fully separate from business `multiraft.GroupID`

- [ ] **Step 4: Run the controller-raft suite and verify GREEN**

Run: `go test ./pkg/cluster/controllerraft`

Expected: PASS.

- [ ] **Step 5: Commit the controller Raft layer**

```bash
git add pkg/cluster/controllerraft
git commit -m "feat(cluster): add dedicated controller raft service"
```

## Task 5: Wire Observation And Recommendation Into `raftcluster`

**Files:**
- Modify: `pkg/cluster/raftcluster/config.go`
- Modify: `pkg/cluster/raftcluster/config_test.go`
- Modify: `pkg/cluster/raftcluster/cluster.go`
- Create: `pkg/cluster/raftcluster/controller_client.go`
- Create: `pkg/cluster/raftcluster/assignment_cache.go`
- Create: `pkg/cluster/raftcluster/readiness.go`
- Modify: `pkg/cluster/raftcluster/cluster_test.go`

- [ ] **Step 1: Write failing `raftcluster` tests for controller observation and assignment caching**

```go
func TestClusterReportsRuntimeViewsToController(t *testing.T) {
	nodes := startThreeNodesWithController(t, 4, 3)
	defer stopNodes(nodes)

	require.Eventually(t, func() bool {
		views, err := nodes[0].cluster.ListObservedRuntimeViews(context.Background())
		return err == nil && len(views) == 4
	}, 10*time.Second, 100*time.Millisecond)
}

func TestClusterGroupIDsNoLongerDependOnStaticGroupConfig(t *testing.T) {
	node := startSingleNodeWithController(t, 8, 1)
	defer node.stop()

	require.Equal(t, []multiraft.GroupID{1, 2, 3, 4, 5, 6, 7, 8}, node.cluster.GroupIDs())
}
```

- [ ] **Step 2: Run the focused `raftcluster` observation tests and verify RED**

Run: `go test ./pkg/cluster/raftcluster -run 'TestCluster(ReportsRuntimeViewsToController|GroupIDsNoLongerDependOnStaticGroupConfig)'`

Expected: FAIL because `Cluster.Start()` still eagerly opens `cfg.Groups`, there is no controller client, and `GroupIDs()` still reflects static configuration.

- [ ] **Step 3: Implement shared transport startup plus controller observation**

Refactor `Cluster.Start()` to this order:

```go
func (c *Cluster) Start() error {
	c.startServer()
	c.startPools()
	c.startControllerRaftIfLocalPeer()
	c.startMultiraftRuntime()
	c.startControllerClient()
	c.startObservationLoop()
	return c.seedLegacyGroupsIfConfigured()
}
```

Implementation notes:

- in this task, keep `cfg.Groups` supported as a temporary migration path so the app still boots while controller observation is being wired
- make `GroupIDs()` return `1..GroupCount`
- make `PeersForGroup()` read the assignment cache first, then fall back to legacy static peers until Task 6 removes the fallback
- make `controller_client` discover the active controller leader by probing the derived controller peer set, honoring `not leader` redirects, and refreshing the cached leader on RPC failure or leader change
- expose a read-only helper for tests to inspect observed runtime views and assignments

- [ ] **Step 4: Run the `raftcluster` observation suite and verify GREEN**

Run: `go test ./pkg/cluster/raftcluster -run 'TestCluster(ReportsRuntimeViewsToController|GroupIDsNoLongerDependOnStaticGroupConfig)'`

Expected: PASS.

- [ ] **Step 5: Commit the observation/recommendation wiring**

```bash
git add pkg/cluster/raftcluster/config.go pkg/cluster/raftcluster/config_test.go \
  pkg/cluster/raftcluster/cluster.go pkg/cluster/raftcluster/controller_client.go \
  pkg/cluster/raftcluster/assignment_cache.go pkg/cluster/raftcluster/readiness.go \
  pkg/cluster/raftcluster/cluster_test.go
git commit -m "feat(cluster): wire controller observation into raftcluster"
```

## Task 6: Switch Business Groups To Controller-Driven Bootstrap And Repair

**Files:**
- Modify: `pkg/cluster/raftcluster/cluster.go`
- Create: `pkg/cluster/raftcluster/managed_groups.go`
- Create: `pkg/cluster/raftcluster/agent.go`
- Create: `pkg/cluster/raftcluster/operator.go`
- Modify: `pkg/cluster/raftcluster/errors.go`
- Modify: `pkg/cluster/raftcluster/cluster_test.go`

- [ ] **Step 1: Write failing tests for controller-driven group bootstrap and majority-available repair**

```go
func TestClusterBootstrapsManagedGroupsFromControllerAssignments(t *testing.T) {
	nodes := startThreeNodesWithController(t, 4, 3)
	defer stopNodes(nodes)

	for groupID := 1; groupID <= 4; groupID++ {
		waitForStableLeader(t, nodes, uint64(groupID))
	}
}

func TestClusterContinuesServingWithOneReplicaNodeDown(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	leaderIdx := int(waitForStableLeader(t, nodes, 1) - 1)
	nodes[leaderIdx].stop()

	require.Eventually(t, func() bool {
		_, err := nodes[(leaderIdx+1)%3].cluster.LeaderOf(1)
		return err == nil
	}, 10*time.Second, 100*time.Millisecond)
}
```

- [ ] **Step 2: Run the bootstrap/repair tests and verify RED**

Run: `go test ./pkg/cluster/raftcluster -run 'TestCluster(BootstrapsManagedGroupsFromControllerAssignments|ContinuesServingWithOneReplicaNodeDown)'`

Expected: FAIL because business groups still come from static `cfg.Groups`, and there is no `GroupAgent` executing controller assignments.

- [ ] **Step 3: Implement `GroupAgent` and dynamic managed-group lifecycle**

Use this structure:

```go
type groupAgent struct {
	cluster *Cluster
	client  *controllerClient
	cache   *assignmentCache
}

func (a *groupAgent) HeartbeatOnce(ctx context.Context) error
func (a *groupAgent) SyncAssignments(ctx context.Context) error
func (a *groupAgent) ApplyAssignments(ctx context.Context) error
```

Implementation notes:

- business groups must no longer bootstrap from static config when controller assignments exist
- first-time formation uses `ReconcileTask{Kind: Bootstrap}` and local `BootstrapGroup(groupID, voters)` only on empty local state
- later membership changes must use `AddLearner -> CatchUp -> Promote -> TransferLeadership(if needed) -> RemoveOld`
- keep at most one peer replacement active per group
- preserve already-open groups when the controller is unavailable
- expose operator methods with explicit contracts:

```go
func (c *Cluster) ListGroupAssignments(ctx context.Context) ([]controllermeta.GroupAssignment, error)
func (c *Cluster) MarkNodeDraining(ctx context.Context, nodeID uint64) error
func (c *Cluster) ResumeNode(ctx context.Context, nodeID uint64) error
func (c *Cluster) ForceReconcile(ctx context.Context, groupID uint32) error
func (c *Cluster) TransferGroupLeader(ctx context.Context, groupID uint32, nodeID multiraft.NodeID) error
func (c *Cluster) RecoverGroup(ctx context.Context, groupID uint32, strategy RecoverStrategy) error
```

- [ ] **Step 4: Add failing tests for operator APIs and repair control**

```go
func TestClusterListGroupAssignmentsReflectsControllerState(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	assignments, err := nodes[0].cluster.ListGroupAssignments(context.Background())
	require.NoError(t, err)
	require.Len(t, assignments, 2)
	require.Len(t, assignments[0].DesiredPeers, 3)
}

func TestClusterTransferGroupLeaderDelegatesToManagedGroup(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	currentLeader := waitForStableLeader(t, nodes, 1)
	target := multiraft.NodeID(1)
	if currentLeader == target {
		target = 2
	}

	require.NoError(t, nodes[0].cluster.TransferGroupLeader(context.Background(), 1, target))
	require.Eventually(t, func() bool {
		leader, err := nodes[0].cluster.LeaderOf(1)
		return err == nil && leader == target
	}, 10*time.Second, 100*time.Millisecond)
}

func TestClusterMarkNodeDrainingMovesAssignmentsAway(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	require.NoError(t, nodes[0].cluster.MarkNodeDraining(context.Background(), 1))
	require.Eventually(t, func() bool {
		assignments, err := nodes[0].cluster.ListGroupAssignments(context.Background())
		if err != nil {
			return false
		}
		for _, assignment := range assignments {
			for _, peer := range assignment.DesiredPeers {
				if peer == 1 {
					return false
				}
			}
		}
		return true
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterForceReconcileRetriesFailedRepair(t *testing.T) {
	nodes := startFourNodesWithInjectedRepairFailure(t, 1, 3)
	defer stopNodes(nodes)

	require.NoError(t, nodes[0].cluster.ForceReconcile(context.Background(), 1))
	require.Eventually(t, func() bool {
		task, err := nodes[0].cluster.GetReconcileTask(context.Background(), 1)
		return err == nil && task.Attempt >= 2
	}, 10*time.Second, 100*time.Millisecond)
}

func TestClusterSurfacesFailedRepairAfterRetryExhaustion(t *testing.T) {
	nodes := startFourNodesWithPermanentRepairFailure(t, 1, 3)
	defer stopNodes(nodes)

	require.Eventually(t, func() bool {
		task, err := nodes[0].cluster.GetReconcileTask(context.Background(), 1)
		return err == nil && task.Status == TaskStatusFailed && task.Attempt == 3
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterRecoverGroupReturnsManualRecoveryErrorWhenQuorumLost(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	waitForStableLeader(t, nodes, 1)
	nodes[1].stop()
	nodes[2].stop()

	err := nodes[0].cluster.RecoverGroup(context.Background(), 1, RecoverStrategyLatestLiveReplica)
	require.ErrorIs(t, err, ErrManualRecoveryRequired)
}
```

- [ ] **Step 5: Run the `raftcluster` dynamic-group suite and verify GREEN**

Run: `go test ./pkg/cluster/raftcluster -run 'TestCluster(BootstrapsManagedGroupsFromControllerAssignments|ContinuesServingWithOneReplicaNodeDown|ListGroupAssignmentsReflectsControllerState|TransferGroupLeaderDelegatesToManagedGroup|MarkNodeDrainingMovesAssignmentsAway|ForceReconcileRetriesFailedRepair|SurfacesFailedRepairAfterRetryExhaustion|RecoverGroupReturnsManualRecoveryErrorWhenQuorumLost)'`

Expected: PASS.

- [ ] **Step 6: Commit repair-mode controller execution**

```bash
git add pkg/cluster/raftcluster/cluster.go pkg/cluster/raftcluster/managed_groups.go \
  pkg/cluster/raftcluster/agent.go pkg/cluster/raftcluster/operator.go \
  pkg/cluster/raftcluster/errors.go pkg/cluster/raftcluster/cluster_test.go
git commit -m "feat(cluster): execute controller-driven bootstrap and repair"
```

## Task 7: Enable Rebalance, Remove Legacy Static Group Peers, And Integrate The App

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `internal/app/multinode_integration_test.go`
- Modify: `internal/app/conversation_sync_integration_test.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`
- Modify: `pkg/cluster/raftcluster/cluster.go`
- Modify: `pkg/cluster/raftcluster/cluster_test.go`

- [ ] **Step 1: Add failing app-level integration tests for controller-driven startup with no static `Cluster.Groups`**

```go
func TestAppThreeNodeClusterStartsWithoutStaticGroupPeers(t *testing.T) {
	h := newThreeNodeAppHarness(t, harnessOptions{
		GroupCount:          4,
		GroupReplicaN:       3,
		ControllerReplicaN:  3,
		UseStaticGroupPeers: false,
	})
	defer h.Close()

	require.NoError(t, h.StartAll())
	h.WaitForManagedGroupsReady()
	h.RequireConversationWritesSucceed()
}

func TestAppMajorityAvailableAfterSingleReplicaNodeFailure(t *testing.T) {
	h := newThreeNodeAppHarness(t, harnessOptions{
		GroupCount:          1,
		GroupReplicaN:       3,
		ControllerReplicaN:  3,
		UseStaticGroupPeers: false,
	})
	defer h.Close()

	require.NoError(t, h.StartAll())
	h.StopLeaderForGroup(1)
	h.RequireConversationWritesSucceed()
}
```

- [ ] **Step 2: Run the focused app integration tests and verify RED**

Run: `go test ./internal/app -run 'TestApp(ThreeNodeClusterStartsWithoutStaticGroupPeers|MajorityAvailableAfterSingleReplicaNodeFailure)'`

Expected: FAIL because `App` still assumes static `Cluster.Groups`, does not open controller storage, and waits for leaders using the old static path.

- [ ] **Step 3: Replace static app startup with managed-group readiness and remove legacy peers**

Implementation checklist:

- open and close `controllermeta.Store` and controller `raftstorage.DB` in `build.go` / `app.go`
- extend `raftcluster.Config` construction with controller storage handles
- replace `waitForPresenceLeaders()` with `cluster.WaitForManagedGroupsReady(ctx)` so the app does not race initial controller bootstrap
- remove the final runtime dependency on `Cluster.Groups`
- remove `WK_CLUSTER_GROUPS` parsing from `cmd/wukongim/config.go`
- update `wukongim.conf.example` to only show:

```conf
WK_CLUSTER_GROUP_COUNT=8
WK_CLUSTER_CONTROLLER_REPLICA_N=3
WK_CLUSTER_GROUP_REPLICA_N=3
WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"},{"id":2,"addr":"127.0.0.1:7001"},{"id":3,"addr":"127.0.0.1:7002"}]
```

- [ ] **Step 4: Add failing rebalance and controller-leader-failover tests**

```go
func TestClusterRebalancesAfterNewWorkerNodeJoins(t *testing.T) {
	h := newFourNodeRebalanceHarness(t, 4, 3)
	defer h.Close()

	require.NoError(t, h.StartCoreNodes())
	require.NoError(t, h.StartWorkerNode(4))
	require.Eventually(t, func() bool {
		assignments, err := h.Node(1).cluster.ListGroupAssignments(context.Background())
		if err != nil {
			return false
		}
		for _, assignment := range assignments {
			for _, peer := range assignment.DesiredPeers {
				if peer == 4 {
					return true
				}
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterRebalancesAfterRecoveredNodeReturns(t *testing.T) {
	h := newFourNodeRebalanceHarness(t, 4, 3)
	defer h.Close()

	require.NoError(t, h.StartAll())
	h.StopNode(4)
	require.Eventually(t, func() bool {
		assignments, err := h.Node(1).cluster.ListGroupAssignments(context.Background())
		if err != nil {
			return false
		}
		for _, assignment := range assignments {
			for _, peer := range assignment.DesiredPeers {
				if peer == 4 {
					return false
				}
			}
		}
		return true
	}, 20*time.Second, 200*time.Millisecond)

	require.NoError(t, h.RestartNode(4))
	require.Eventually(t, func() bool {
		assignments, err := h.Node(1).cluster.ListGroupAssignments(context.Background())
		if err != nil {
			return false
		}
		for _, assignment := range assignments {
			for _, peer := range assignment.DesiredPeers {
				if peer == 4 {
					return true
				}
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterControllerLeaderFailoverResumesInFlightRepair(t *testing.T) {
	h := startFourNodesWithInjectedSlowRepair(t, 1, 3)
	defer h.Close()

	controllerLeader := waitForControllerLeader(t, h.Nodes())
	require.NoError(t, h.Node(1).cluster.MarkNodeDraining(context.Background(), 1))
	h.StopNode(uint64(controllerLeader))

	require.Eventually(t, func() bool {
		assignments, err := h.Node(2).cluster.ListGroupAssignments(context.Background())
		if err != nil {
			return false
		}
		return len(assignments) == 1 && !containsNode(assignments[0].DesiredPeers, 1)
	}, 20*time.Second, 200*time.Millisecond)
}
```

- [ ] **Step 5: Run the app and cluster integration suites and verify GREEN**

Run: `go test ./internal/app ./pkg/cluster/raftcluster -run 'Test(App|Cluster)'`

Expected: PASS for controller-driven startup, repair, rebalance, and controller leader failover.

- [ ] **Step 6: Commit the final behavior switch**

```bash
git add internal/app/app.go internal/app/build.go internal/app/lifecycle.go \
  internal/app/config.go internal/app/config_test.go \
  internal/app/multinode_integration_test.go internal/app/conversation_sync_integration_test.go \
  cmd/wukongim/config.go cmd/wukongim/config_test.go wukongim.conf.example \
  pkg/cluster/raftcluster/cluster.go pkg/cluster/raftcluster/cluster_test.go
git commit -m "feat(cluster): enable automatic control-plane group management"
```

## Task 8: Final Verification Before Declaring Completion

**Files:**
- Verify only; no new files unless a discovered gap requires a targeted follow-up patch

- [ ] **Step 1: Run the focused controller/storage/app suites**

Run: `go test ./pkg/storage/controllermeta ./pkg/cluster/groupcontroller ./pkg/cluster/controllerraft ./pkg/cluster/raftcluster ./internal/app ./pkg/storage/metastore`

Expected: PASS.

- [ ] **Step 2: Run the full repository test suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 3: If a test fails, fix the smallest responsible layer first and re-run only the affected package before re-running the full suite**

Run: `go test ./<affected-package>`

Expected: PASS before returning to `go test ./...`.

- [ ] **Step 4: Commit verification-only fixes, if any**

```bash
git add <affected files>
git commit -m "fix(cluster): address automatic group management verification gaps"
```
