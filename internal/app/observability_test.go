package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestBuildWiresObservabilityIntoAPIAndChannelCluster(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	require.NotNil(t, app.metrics)
	require.NotNil(t, app.channelLog)
	require.Same(t, app.metrics, app.channelLog.metrics)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	app.API().Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "wukongim_channel_active_channels")
}

func TestClusterMetricsObserverHooksRecordSlotAndTransportMetrics(t *testing.T) {
	registry := obsmetrics.New(2, "node-2")
	hooks := clusterMetricsObserver{metrics: registry}.Hooks()

	hooks.OnForwardPropose(5, 2, 14*time.Millisecond, nil)
	hooks.OnLeaderChange(5, 1, 2)
	hooks.OnControllerCall("list_nodes", 11*time.Millisecond, errors.New("boom"))
	hooks.OnControllerDecision(5, "repair", 9*time.Millisecond)
	hooks.OnHashSlotMigration(7, 1, 2, "abort")
	hooks.OnTaskResult(5, "repair", "ok")

	families, err := registry.Gather()
	require.NoError(t, err)

	proposals := requireMetricFamilyByName(t, families, "wukongim_slot_proposals_total")
	require.Len(t, proposals.GetMetric(), 1)
	require.Equal(t, float64(1), proposals.GetMetric()[0].GetCounter().GetValue())

	leaderChanges := requireMetricFamilyByName(t, families, "wukongim_slot_leader_elections_total")
	require.Len(t, leaderChanges.GetMetric(), 1)
	require.Equal(t, float64(1), leaderChanges.GetMetric()[0].GetCounter().GetValue())

	rpcTotal := requireMetricFamilyByName(t, families, "wukongim_transport_rpc_total")
	require.Len(t, rpcTotal.GetMetric(), 1)
	require.Equal(t, float64(1), rpcTotal.GetMetric()[0].GetCounter().GetValue())

	controllerDecisions := requireMetricFamilyByName(t, families, "wukongim_controller_decisions_total")
	require.NotEmpty(t, controllerDecisions.GetMetric())

	controllerCompleted := requireMetricFamilyByName(t, families, "wukongim_controller_tasks_completed_total")
	require.NotEmpty(t, controllerCompleted.GetMetric())

	migrationsCompleted := requireMetricFamilyByName(t, families, "wukongim_controller_hashslot_migrations_total")
	require.NotEmpty(t, migrationsCompleted.GetMetric())
}

func TestTransportMetricsObserverRecordsSendAndReceiveBytes(t *testing.T) {
	registry := obsmetrics.New(2, "node-2")
	hooks := transportMetricsObserver{metrics: registry}.Hooks()

	hooks.OnSend(0xFE, 64)
	hooks.OnReceive(9, 96)

	families, err := registry.Gather()
	require.NoError(t, err)

	sentBytes := requireMetricFamilyByName(t, families, "wukongim_transport_sent_bytes_total")
	require.Len(t, sentBytes.GetMetric(), 1)

	receivedBytes := requireMetricFamilyByName(t, families, "wukongim_transport_received_bytes_total")
	require.Len(t, receivedBytes.GetMetric(), 1)
}

func TestTransportMetricsObserverRecordsRPCClientMetrics(t *testing.T) {
	registry := obsmetrics.New(2, "node-2")
	hooks := transportMetricsObserver{metrics: registry}.Hooks()

	hooks.OnDial(transport.DialEvent{TargetNode: 3, Result: "dial_error", Duration: 5 * time.Millisecond})
	hooks.OnEnqueue(transport.EnqueueEvent{TargetNode: 3, Kind: "rpc", Result: "queue_full"})
	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 3, ServiceID: 33, Inflight: 1})
	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 3, ServiceID: 33, Result: "timeout", Duration: 20 * time.Millisecond, Inflight: 0})

	families, err := registry.Gather()
	require.NoError(t, err)

	rpcTotal := requireMetricFamilyByName(t, families, "wukongim_transport_rpc_client_total")
	require.Len(t, rpcTotal.GetMetric(), 1)

	rpcInflight := requireMetricFamilyByName(t, families, "wukongim_transport_rpc_inflight")
	require.Len(t, rpcInflight.GetMetric(), 1)
	require.Equal(t, float64(0), rpcInflight.GetMetric()[0].GetGauge().GetValue())

	enqueueTotal := requireMetricFamilyByName(t, families, "wukongim_transport_enqueue_total")
	require.Len(t, enqueueTotal.GetMetric(), 1)

	dialTotal := requireMetricFamilyByName(t, families, "wukongim_transport_dial_total")
	require.Len(t, dialTotal.GetMetric(), 1)
}

func TestTransportMetricsObserverMapsServiceIDToName(t *testing.T) {
	registry := obsmetrics.New(2, "node-2")
	hooks := transportMetricsObserver{metrics: registry}.Hooks()

	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 3, ServiceID: 33, Result: "ok", Duration: 7 * time.Millisecond})
	hooks.OnRPCClient(transport.RPCClientEvent{TargetNode: 3, ServiceID: 99, Result: "other", Duration: 9 * time.Millisecond})

	families, err := registry.Gather()
	require.NoError(t, err)

	rpcTotal := requireMetricFamilyByName(t, families, "wukongim_transport_rpc_client_total")
	require.True(t, metricFamilyHasLabels(rpcTotal, map[string]string{
		"target_node": "3",
		"service":     "channel_append",
		"result":      "ok",
	}))
	require.True(t, metricFamilyHasLabels(rpcTotal, map[string]string{
		"target_node": "3",
		"service":     "service_99",
		"result":      "other",
	}))
}

func TestMetricsHandlerRefreshesTransportPoolMetrics(t *testing.T) {
	app := &App{
		cfg: Config{
			Node: NodeConfig{ID: 1, Name: "node-1"},
		},
		metrics: obsmetrics.New(1, "node-1"),
		cluster: fakeObservabilityCluster{
			transportStats: []transport.PoolPeerStats{
				{NodeID: 2, Active: 1, Idle: 2},
				{NodeID: 3, Active: 2, Idle: 1},
			},
		},
	}

	handler := app.metricsHandler()
	require.NotNil(t, handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	families, err := app.metrics.Gather()
	require.NoError(t, err)

	poolActive := requireMetricFamilyByName(t, families, "wukongim_transport_connections_pool_active")
	require.Len(t, poolActive.GetMetric(), 2)

	poolIdle := requireMetricFamilyByName(t, families, "wukongim_transport_connections_pool_idle")
	require.Len(t, poolIdle.GetMetric(), 2)
}

func TestHealthDetailsSnapshotIncludesControllerSlotAndStorageDetails(t *testing.T) {
	cfg := validConfig()
	cfg.Node.DataDir = t.TempDir()
	cfg.Storage = StorageConfig{
		DBPath:             filepath.Join(cfg.Node.DataDir, "data"),
		RaftPath:           filepath.Join(cfg.Node.DataDir, "raft"),
		ChannelLogPath:     filepath.Join(cfg.Node.DataDir, "channel"),
		ControllerMetaPath: filepath.Join(cfg.Node.DataDir, "controller-meta"),
		ControllerRaftPath: filepath.Join(cfg.Node.DataDir, "controller-raft"),
	}
	for _, dir := range []string{
		cfg.Storage.DBPath,
		cfg.Storage.RaftPath,
		cfg.Storage.ChannelLogPath,
		cfg.Storage.ControllerMetaPath,
		cfg.Storage.ControllerRaftPath,
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "usage.bin"), []byte("12345678"), 0o644))
	}

	app := &App{
		cfg:       cfg,
		createdAt: time.Now().Add(-10 * time.Second),
		metrics:   obsmetrics.New(cfg.Node.ID, cfg.Node.Name),
		cluster: fakeObservabilityCluster{
			hashSlotTableVersion: 9,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Status: controllermeta.NodeStatusAlive},
				{NodeID: 2, Status: controllermeta.NodeStatusSuspect},
				{NodeID: 3, Status: controllermeta.NodeStatusDead},
			},
			assignments: []controllermeta.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}},
			},
			views: []controllermeta.SlotRuntimeView{
				{
					SlotID:              1,
					CurrentPeers:        []uint64{1, 2, 3},
					LeaderID:            1,
					HealthyVoters:       2,
					HasQuorum:           true,
					ObservedConfigEpoch: 4,
				},
			},
		},
	}
	app.metrics.Channel.SetActiveChannels(3)

	snapshot := app.healthDetailsSnapshot().(map[string]any)
	components := snapshot["components"].(map[string]any)

	controller, ok := components["controller"].(map[string]any)
	require.True(t, ok, "controller health should be included")
	require.Equal(t, 1, controller["alive_nodes"])
	require.Equal(t, 1, controller["suspect_nodes"])
	require.Equal(t, 1, controller["dead_nodes"])

	slotLayer, ok := components["slot_layer"].(map[string]any)
	require.True(t, ok, "slot layer details should be included")
	_, ok = slotLayer["slots"]
	require.True(t, ok, "slot details should be included")

	storage, ok := components["storage"].(map[string]any)
	require.True(t, ok, "storage details should be included")
	usage, ok := storage["disk_usage_bytes"].(int64)
	require.True(t, ok, "storage usage should be an int64")
	require.Greater(t, usage, int64(0))
}

func TestDebugClusterSnapshotIncludesNodesAssignmentsAndViews(t *testing.T) {
	app := &App{
		cfg: Config{
			Node: NodeConfig{ID: 1, Name: "node-1"},
		},
		cluster: fakeObservabilityCluster{
			hashSlotTableVersion: 12,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			},
			assignments: []controllermeta.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1}},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 1, CurrentPeers: []uint64{1}, LeaderID: 1, HealthyVoters: 1, HasQuorum: true},
			},
		},
	}

	snapshot, ok := app.debugClusterSnapshot().(map[string]any)
	require.True(t, ok, "debug cluster snapshot should be a map")
	require.Contains(t, snapshot, "nodes")
	require.Contains(t, snapshot, "assignments")
	require.Contains(t, snapshot, "runtime_views")
}

func TestMetricsHandlerRefreshesControllerMetricsFromClusterState(t *testing.T) {
	app := &App{
		cfg: Config{
			Node: NodeConfig{ID: 1, Name: "node-1"},
		},
		metrics: obsmetrics.New(1, "node-1"),
		cluster: fakeObservabilityCluster{
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Status: controllermeta.NodeStatusAlive},
				{NodeID: 2, Status: controllermeta.NodeStatusDead},
			},
			tasks: []controllermeta.ReconcileTask{
				{SlotID: 1, Kind: controllermeta.TaskKindRepair, Status: controllermeta.TaskStatusPending},
				{SlotID: 2, Kind: controllermeta.TaskKindRepair, Status: controllermeta.TaskStatusRetrying},
				{SlotID: 3, Kind: controllermeta.TaskKindRebalance, Status: controllermeta.TaskStatusFailed},
			},
			migrations: []raftcluster.HashSlotMigration{
				{HashSlot: 1, Source: 1, Target: 2},
				{HashSlot: 2, Source: 2, Target: 1},
			},
		},
	}

	handler := app.metricsHandler()
	require.NotNil(t, handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	families, err := app.metrics.Gather()
	require.NoError(t, err)

	nodesAlive := requireMetricFamilyByName(t, families, "wukongim_controller_nodes_alive")
	require.Len(t, nodesAlive.GetMetric(), 1)
	require.Equal(t, float64(1), nodesAlive.GetMetric()[0].GetGauge().GetValue())

	tasksActive := requireMetricFamilyByName(t, families, "wukongim_controller_tasks_active")
	foundRepair := false
	for _, metric := range tasksActive.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "type" && label.GetValue() == "repair" {
				require.Equal(t, float64(2), metric.GetGauge().GetValue())
				foundRepair = true
			}
		}
	}
	require.True(t, foundRepair, "repair task gauge should be refreshed")

	migrationsActive := requireMetricFamilyByName(t, families, "wukongim_controller_hashslot_migrations_active")
	require.Len(t, migrationsActive.GetMetric(), 1)
	require.Equal(t, float64(2), migrationsActive.GetMetric()[0].GetGauge().GetValue())
}

func TestMetricsHandlerRefreshesStorageMetricsFromStorageState(t *testing.T) {
	dataDir := t.TempDir()
	cfg := Config{
		Node: NodeConfig{ID: 1, Name: "node-1", DataDir: dataDir},
		Storage: StorageConfig{
			DBPath:             filepath.Join(dataDir, "data"),
			RaftPath:           filepath.Join(dataDir, "raft"),
			ChannelLogPath:     filepath.Join(dataDir, "channel"),
			ControllerMetaPath: filepath.Join(dataDir, "controller-meta"),
			ControllerRaftPath: filepath.Join(dataDir, "controller-raft"),
		},
	}
	writeStoreFile := func(path, name string, size int) {
		require.NoError(t, os.MkdirAll(path, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(path, name), make([]byte, size), 0o644))
	}
	writeStoreFile(cfg.Storage.DBPath, "meta.bin", 11)
	writeStoreFile(cfg.Storage.RaftPath, "raft.bin", 13)
	writeStoreFile(cfg.Storage.ChannelLogPath, "channel.bin", 17)
	writeStoreFile(cfg.Storage.ControllerMetaPath, "controller-meta.bin", 19)
	writeStoreFile(cfg.Storage.ControllerRaftPath, "controller-raft.bin", 23)

	app := &App{
		cfg:     cfg,
		metrics: obsmetrics.New(1, "node-1"),
	}

	handler := app.metricsHandler()
	require.NotNil(t, handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	families, err := app.metrics.Gather()
	require.NoError(t, err)

	usage := requireMetricFamilyByName(t, families, "wukongim_storage_disk_usage_bytes")
	require.Len(t, usage.GetMetric(), 5)

	want := map[string]float64{
		"meta":            11,
		"raft":            13,
		"channel_log":     17,
		"controller_meta": 19,
		"controller_raft": 23,
	}
	for _, metric := range usage.GetMetric() {
		labels := map[string]string{}
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		store := labels["store"]
		expected, ok := want[store]
		require.True(t, ok, "unexpected store label %q", store)
		require.Equal(t, expected, metric.GetGauge().GetValue(), "store %s", store)
	}
}

type fakeObservabilityCluster struct {
	hashSlotTableVersion uint64
	nodes                []controllermeta.ClusterNode
	assignments          []controllermeta.SlotAssignment
	views                []controllermeta.SlotRuntimeView
	tasks                []controllermeta.ReconcileTask
	migrations           []raftcluster.HashSlotMigration
	transportStats       []transport.PoolPeerStats
	waitReadyErr         error
}

func (f fakeObservabilityCluster) Start() error { return nil }

func (f fakeObservabilityCluster) Stop() {}

func (f fakeObservabilityCluster) NodeID() multiraft.NodeID { return 1 }

func (f fakeObservabilityCluster) IsLocal(nodeID multiraft.NodeID) bool { return nodeID == 1 }

func (f fakeObservabilityCluster) SlotForKey(string) multiraft.SlotID { return 1 }

func (f fakeObservabilityCluster) HashSlotForKey(string) uint16 { return 1 }

func (f fakeObservabilityCluster) HashSlotsOf(multiraft.SlotID) []uint16 { return []uint16{1} }

func (f fakeObservabilityCluster) HashSlotTableVersion() uint64 { return f.hashSlotTableVersion }

func (f fakeObservabilityCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) { return 1, nil }

func (f fakeObservabilityCluster) Propose(context.Context, multiraft.SlotID, []byte) error {
	return nil
}

func (f fakeObservabilityCluster) SlotIDs() []multiraft.SlotID { return []multiraft.SlotID{1} }

func (f fakeObservabilityCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return []multiraft.NodeID{1}
}

func (f fakeObservabilityCluster) WaitForManagedSlotsReady(context.Context) error {
	return f.waitReadyErr
}

func (f fakeObservabilityCluster) ListNodes(context.Context) ([]controllermeta.ClusterNode, error) {
	return append([]controllermeta.ClusterNode(nil), f.nodes...), nil
}

func (f fakeObservabilityCluster) ListNodesStrict(context.Context) ([]controllermeta.ClusterNode, error) {
	return append([]controllermeta.ClusterNode(nil), f.nodes...), nil
}

func (f fakeObservabilityCluster) ListSlotAssignments(context.Context) ([]controllermeta.SlotAssignment, error) {
	return append([]controllermeta.SlotAssignment(nil), f.assignments...), nil
}

func (f fakeObservabilityCluster) ListSlotAssignmentsStrict(context.Context) ([]controllermeta.SlotAssignment, error) {
	return append([]controllermeta.SlotAssignment(nil), f.assignments...), nil
}

func (f fakeObservabilityCluster) ListObservedRuntimeViews(context.Context) ([]controllermeta.SlotRuntimeView, error) {
	return append([]controllermeta.SlotRuntimeView(nil), f.views...), nil
}

func (f fakeObservabilityCluster) ListObservedRuntimeViewsStrict(context.Context) ([]controllermeta.SlotRuntimeView, error) {
	return append([]controllermeta.SlotRuntimeView(nil), f.views...), nil
}

func (f fakeObservabilityCluster) ControllerLeaderID() uint64 { return 0 }

func (f fakeObservabilityCluster) ListTasks(context.Context) ([]controllermeta.ReconcileTask, error) {
	return append([]controllermeta.ReconcileTask(nil), f.tasks...), nil
}

func (f fakeObservabilityCluster) ListTasksStrict(context.Context) ([]controllermeta.ReconcileTask, error) {
	return append([]controllermeta.ReconcileTask(nil), f.tasks...), nil
}

func (f fakeObservabilityCluster) GetMigrationStatus() []raftcluster.HashSlotMigration {
	return append([]raftcluster.HashSlotMigration(nil), f.migrations...)
}

func (f fakeObservabilityCluster) TransportPoolStats() []transport.PoolPeerStats {
	return append([]transport.PoolPeerStats(nil), f.transportStats...)
}

func (f fakeObservabilityCluster) GetReconcileTask(context.Context, uint32) (controllermeta.ReconcileTask, error) {
	return controllermeta.ReconcileTask{}, nil
}

func (f fakeObservabilityCluster) GetReconcileTaskStrict(_ context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
	for _, task := range f.tasks {
		if task.SlotID == slotID {
			return task, nil
		}
	}
	return controllermeta.ReconcileTask{}, controllermeta.ErrNotFound
}

func (f fakeObservabilityCluster) ForceReconcile(context.Context, uint32) error { return nil }

func (f fakeObservabilityCluster) MarkNodeDraining(context.Context, uint64) error { return nil }

func (f fakeObservabilityCluster) ResumeNode(context.Context, uint64) error { return nil }

func (f fakeObservabilityCluster) TransferSlotLeader(context.Context, uint32, multiraft.NodeID) error {
	return nil
}

func (f fakeObservabilityCluster) RecoverSlot(context.Context, uint32, raftcluster.RecoverStrategy) error {
	return nil
}

func (f fakeObservabilityCluster) RecoverSlotStrict(context.Context, uint32, raftcluster.RecoverStrategy) error {
	return nil
}

func (f fakeObservabilityCluster) Rebalance(context.Context) ([]raftcluster.MigrationPlan, error) {
	return nil, nil
}

func (f fakeObservabilityCluster) Server() *transport.Server { return nil }

func (f fakeObservabilityCluster) RPCMux() *transport.RPCMux { return nil }

func (f fakeObservabilityCluster) Discovery() raftcluster.Discovery { return nil }

func (f fakeObservabilityCluster) RPCService(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error) {
	return nil, nil
}

func metricFamilyHasLabels(family *dto.MetricFamily, want map[string]string) bool {
	if family == nil {
		return false
	}
	for _, metric := range family.GetMetric() {
		labels := map[string]string{}
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		matched := true
		for key, value := range want {
			if labels[key] != value {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
