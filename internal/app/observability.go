package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	accessgateway "github.com/WuKongIM/WuKongIM/internal/gateway"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type gatewayMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type clusterMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type transportMetricsObserver struct {
	metrics *obsmetrics.Registry
}

const observabilityQueryTimeout = 100 * time.Millisecond

type observedClusterState struct {
	nodes          []controllermeta.ClusterNode
	assignments    []controllermeta.SlotAssignment
	views          []controllermeta.SlotRuntimeView
	tasks          []controllermeta.ReconcileTask
	migrations     []raftcluster.HashSlotMigration
	nodesErr       error
	assignmentsErr error
	viewsErr       error
	tasksErr       error
}

func (o gatewayMetricsObserver) OnConnectionOpen(event accessgateway.ConnectionEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.ConnectionOpened(event.Protocol)
}

func (o gatewayMetricsObserver) OnConnectionClose(event accessgateway.ConnectionEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.ConnectionClosed(event.Protocol)
}

func (o gatewayMetricsObserver) OnAuth(event accessgateway.AuthEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.Auth(event.Status, event.Duration)
}

func (o gatewayMetricsObserver) OnFrameIn(event accessgateway.FrameEvent) {
	if o.metrics == nil || event.FrameType != "SEND" {
		return
	}
	o.metrics.Gateway.MessageReceived(event.Protocol, event.Bytes)
}

func (o gatewayMetricsObserver) OnFrameOut(event accessgateway.FrameEvent) {
	if o.metrics == nil || event.FrameType != "RECV" {
		return
	}
	o.metrics.Gateway.MessageDelivered(event.Protocol, event.Bytes)
}

func (o gatewayMetricsObserver) OnFrameHandled(event accessgateway.FrameHandleEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.FrameHandled(event.FrameType, event.Duration)
}

func (o clusterMetricsObserver) Hooks() raftcluster.ObserverHooks {
	return raftcluster.ObserverHooks{
		OnControllerCall: func(kind string, dur time.Duration, err error) {
			if o.metrics == nil {
				return
			}
			result := "ok"
			if err != nil {
				result = "err"
			}
			o.metrics.Transport.ObserveRPC(kind, result, dur)
		},
		OnControllerDecision: func(_ uint32, kind string, dur time.Duration) {
			if o.metrics == nil {
				return
			}
			o.metrics.Controller.ObserveDecision(kind, dur)
		},
		OnHashSlotMigration: func(_ uint16, _, _ multiraft.SlotID, result string) {
			if o.metrics == nil {
				return
			}
			o.metrics.Controller.ObserveMigrationCompleted(result)
		},
		OnForwardPropose: func(slotID uint32, _ int, dur time.Duration, _ error) {
			if o.metrics == nil {
				return
			}
			o.metrics.Slot.ObserveProposal(slotID, dur)
		},
		OnTaskResult: func(_ uint32, kind string, result string) {
			if o.metrics == nil {
				return
			}
			o.metrics.Controller.ObserveTaskCompleted(kind, result)
		},
		OnLeaderChange: func(slotID uint32, _, _ multiraft.NodeID) {
			if o.metrics == nil {
				return
			}
			o.metrics.Slot.ObserveLeaderChange(slotID)
		},
	}
}

func (o transportMetricsObserver) Hooks() transport.ObserverHooks {
	return transport.ObserverHooks{
		OnSend: func(msgType uint8, bytes int) {
			if o.metrics == nil {
				return
			}
			o.metrics.Transport.ObserveSentBytes(transportMsgType(msgType), bytes)
		},
		OnReceive: func(msgType uint8, bytes int) {
			if o.metrics == nil {
				return
			}
			o.metrics.Transport.ObserveReceivedBytes(transportMsgType(msgType), bytes)
		},
		OnDial: func(event transport.DialEvent) {
			if o.metrics == nil {
				return
			}
			o.metrics.Transport.ObserveDial(strconv.FormatUint(uint64(event.TargetNode), 10), event.Result, event.Duration)
		},
		OnEnqueue: func(event transport.EnqueueEvent) {
			if o.metrics == nil {
				return
			}
			o.metrics.Transport.ObserveEnqueue(strconv.FormatUint(uint64(event.TargetNode), 10), event.Kind, event.Result)
		},
		OnRPCClient: func(event transport.RPCClientEvent) {
			if o.metrics == nil {
				return
			}
			targetNode := strconv.FormatUint(uint64(event.TargetNode), 10)
			service := transportRPCServiceName(event.ServiceID)
			o.metrics.Transport.SetRPCInflight(targetNode, service, event.Inflight)
			if event.Result == "" {
				return
			}
			o.metrics.Transport.ObserveRPCClient(targetNode, service, event.Result, event.Duration)
		},
	}
}

func transportRPCServiceName(serviceID uint8) string {
	switch serviceID {
	case 1:
		return "forward"
	case 5:
		return "presence"
	case 6:
		return "delivery_submit"
	case 7:
		return "delivery_push"
	case 8:
		return "delivery_ack"
	case 9:
		return "delivery_offline"
	case 13:
		return "conversation_facts"
	case 14:
		return "controller"
	case 20:
		return "managed_slot"
	case 33:
		return "channel_append"
	default:
		return "service_" + strconv.FormatUint(uint64(serviceID), 10)
	}
}

func (a *App) metricsHandler() http.Handler {
	if a == nil || a.metrics == nil {
		return nil
	}
	handler := a.metrics.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.refreshControllerMetrics()
		a.refreshTransportMetrics()
		a.refreshStorageMetrics()
		handler.ServeHTTP(w, r)
	})
}

func (a *App) healthDetailsSnapshot() any {
	if a == nil {
		return map[string]any{"status": "unknown"}
	}

	gatewaySnapshot := obsmetrics.GatewaySnapshot{}
	channelSnapshot := obsmetrics.ChannelSnapshot{}
	if a.metrics != nil {
		gatewaySnapshot = a.metrics.Gateway.Snapshot()
		channelSnapshot = a.metrics.Channel.Snapshot()
	}

	activeConnections := int64(0)
	protocols := make(map[string]any, len(gatewaySnapshot.ActiveConnections))
	for protocol, count := range gatewaySnapshot.ActiveConnections {
		activeConnections += count
		protocols[protocol] = map[string]any{
			"status":      "healthy",
			"connections": count,
		}
	}

	clusterState := a.collectObservedClusterState()
	aliveNodes, suspectNodes, deadNodes := clusterNodeCounts(clusterState.nodes)
	activeTasks := controllerActiveTaskCounts(clusterState.tasks)
	activeTaskTotal := 0
	for _, count := range activeTasks {
		activeTaskTotal += count
	}

	controllerStatus := "healthy"
	if clusterState.nodesErr != nil || clusterState.tasksErr != nil || suspectNodes > 0 || deadNodes > 0 {
		controllerStatus = "degraded"
	}

	slotStatus := "healthy"
	if clusterState.assignmentsErr != nil || clusterState.viewsErr != nil {
		slotStatus = "degraded"
	}
	slotDetails := make([]map[string]any, 0, len(clusterState.views))
	for _, view := range clusterState.views {
		if !view.HasQuorum {
			slotStatus = "degraded"
		}
		slotDetails = append(slotDetails, map[string]any{
			"id":             view.SlotID,
			"role":           localSlotRole(a.cfg.Node.ID, view),
			"leader_id":      view.LeaderID,
			"healthy_voters": view.HealthyVoters,
			"has_quorum":     view.HasQuorum,
			"current_peers":  view.CurrentPeers,
			"applied_epoch":  view.ObservedConfigEpoch,
			"last_report_at": view.LastReportAt,
		})
	}

	storage := a.storageHealthSnapshot()
	overallStatus := "healthy"
	if controllerStatus != "healthy" || slotStatus != "healthy" {
		overallStatus = "degraded"
	}
	if storageStatus, _ := storage["status"].(string); storageStatus != "" && storageStatus != "healthy" {
		overallStatus = "degraded"
	}

	controller := map[string]any{
		"status":                  controllerStatus,
		"alive_nodes":             aliveNodes,
		"suspect_nodes":           suspectNodes,
		"dead_nodes":              deadNodes,
		"active_tasks":            activeTaskTotal,
		"active_tasks_by_type":    activeTasks,
		"active_migrations":       len(clusterState.migrations),
		"hash_slot_table_version": a.clusterHashSlotTableVersion(),
	}
	if clusterState.nodesErr != nil {
		controller["error"] = clusterState.nodesErr.Error()
	}
	if clusterState.tasksErr != nil {
		controller["tasks_error"] = clusterState.tasksErr.Error()
	}

	slotLayer := map[string]any{
		"status":           slotStatus,
		"slots":            slotDetails,
		"assignment_count": len(clusterState.assignments),
		"runtime_views":    len(clusterState.views),
	}
	if clusterState.assignmentsErr != nil || clusterState.viewsErr != nil {
		errors := make(map[string]string, 2)
		if clusterState.assignmentsErr != nil {
			errors["assignments"] = clusterState.assignmentsErr.Error()
		}
		if clusterState.viewsErr != nil {
			errors["runtime_views"] = clusterState.viewsErr.Error()
		}
		slotLayer["errors"] = errors
	}

	return map[string]any{
		"status":         overallStatus,
		"node_id":        a.cfg.Node.ID,
		"node_name":      a.cfg.Node.Name,
		"uptime_seconds": int64(time.Since(a.createdAt).Seconds()),
		"components": map[string]any{
			"gateway": map[string]any{
				"status":             "healthy",
				"active_connections": activeConnections,
				"protocols":          protocols,
			},
			"channel_layer": map[string]any{
				"status":          "healthy",
				"active_channels": channelSnapshot.ActiveChannels,
			},
			"controller": controller,
			"slot_layer": slotLayer,
			"storage":    storage,
			"cluster": map[string]any{
				"status":                  controllerStatus,
				"hash_slot_table_version": a.clusterHashSlotTableVersion(),
			},
		},
	}
}

func (a *App) readyzReport(ctx context.Context) (bool, any) {
	if a == nil {
		return false, map[string]any{"status": "not_ready"}
	}

	gatewayReady := false
	if a.gateway != nil {
		for _, listener := range a.cfg.Gateway.Listeners {
			if a.gateway.ListenerAddr(listener.Name) != "" {
				gatewayReady = true
				break
			}
		}
	}

	clusterReady := false
	if a.cluster != nil {
		readyCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		clusterReady = a.cluster.WaitForManagedSlotsReady(readyCtx) == nil
		cancel()
	}

	hashSlotReady := a.clusterHashSlotTableVersion() > 0
	ready := gatewayReady && clusterReady && hashSlotReady
	status := "not_ready"
	if ready {
		status = "ready"
	}

	return ready, map[string]any{
		"status": status,
		"checks": map[string]any{
			"gateway":         gatewayReady,
			"managed_slots":   clusterReady,
			"hash_slot_table": hashSlotReady,
		},
	}
}

func (a *App) debugConfigSnapshot() any {
	if a == nil {
		return map[string]any{}
	}
	return map[string]any{
		"node_id":           a.cfg.Node.ID,
		"node_name":         a.cfg.Node.Name,
		"node_data_dir":     a.cfg.Node.DataDir,
		"cluster_listen":    a.cfg.Cluster.ListenAddr,
		"api_listen":        a.cfg.API.ListenAddr,
		"gateway_listeners": len(a.cfg.Gateway.Listeners),
		"metrics_enable":    a.cfg.Observability.MetricsEnabled,
		"health_detail":     a.cfg.Observability.HealthDetailEnabled,
		"health_debug":      a.cfg.Observability.HealthDebugEnabled,
	}
}

func (a *App) debugClusterSnapshot() any {
	if a == nil {
		return map[string]any{}
	}

	clusterState := a.collectObservedClusterState()
	leaderDistribution := make(map[string]int, len(clusterState.views))
	for _, view := range clusterState.views {
		leaderDistribution[strconv.FormatUint(view.LeaderID, 10)]++
	}

	snapshot := map[string]any{
		"node_id":                 a.cfg.Node.ID,
		"node_name":               a.cfg.Node.Name,
		"hash_slot_table_version": a.clusterHashSlotTableVersion(),
		"nodes":                   clusterState.nodes,
		"assignments":             clusterState.assignments,
		"runtime_views":           clusterState.views,
		"tasks":                   clusterState.tasks,
		"migrations":              clusterState.migrations,
		"leader_distribution":     leaderDistribution,
	}

	if clusterState.nodesErr != nil || clusterState.assignmentsErr != nil || clusterState.viewsErr != nil {
		errors := make(map[string]string, 3)
		if clusterState.nodesErr != nil {
			errors["nodes"] = clusterState.nodesErr.Error()
		}
		if clusterState.assignmentsErr != nil {
			errors["assignments"] = clusterState.assignmentsErr.Error()
		}
		if clusterState.viewsErr != nil {
			errors["runtime_views"] = clusterState.viewsErr.Error()
		}
		if clusterState.tasksErr != nil {
			errors["tasks"] = clusterState.tasksErr.Error()
		}
		snapshot["errors"] = errors
	}

	return snapshot
}

func (a *App) clusterHashSlotTableVersion() uint64 {
	if a == nil || a.cluster == nil {
		return 0
	}
	return a.cluster.HashSlotTableVersion()
}

func (a *App) collectObservedClusterState() observedClusterState {
	if a == nil || a.cluster == nil {
		return observedClusterState{}
	}

	state := observedClusterState{}

	nodesCtx, cancel := context.WithTimeout(context.Background(), observabilityQueryTimeout)
	state.nodes, state.nodesErr = a.cluster.ListNodes(nodesCtx)
	cancel()

	assignmentsCtx, cancel := context.WithTimeout(context.Background(), observabilityQueryTimeout)
	state.assignments, state.assignmentsErr = a.cluster.ListSlotAssignments(assignmentsCtx)
	cancel()

	viewsCtx, cancel := context.WithTimeout(context.Background(), observabilityQueryTimeout)
	state.views, state.viewsErr = a.cluster.ListObservedRuntimeViews(viewsCtx)
	cancel()

	tasksCtx, cancel := context.WithTimeout(context.Background(), observabilityQueryTimeout)
	state.tasks, state.tasksErr = a.cluster.ListTasks(tasksCtx)
	cancel()
	state.migrations = a.cluster.GetMigrationStatus()

	sort.Slice(state.nodes, func(i, j int) bool {
		return state.nodes[i].NodeID < state.nodes[j].NodeID
	})
	sort.Slice(state.assignments, func(i, j int) bool {
		return state.assignments[i].SlotID < state.assignments[j].SlotID
	})
	sort.Slice(state.views, func(i, j int) bool {
		return state.views[i].SlotID < state.views[j].SlotID
	})
	sort.Slice(state.tasks, func(i, j int) bool {
		if state.tasks[i].SlotID == state.tasks[j].SlotID {
			return state.tasks[i].Attempt < state.tasks[j].Attempt
		}
		return state.tasks[i].SlotID < state.tasks[j].SlotID
	})
	sort.Slice(state.migrations, func(i, j int) bool {
		return state.migrations[i].HashSlot < state.migrations[j].HashSlot
	})

	return state
}

func (a *App) refreshControllerMetrics() {
	if a == nil || a.metrics == nil || a.metrics.Controller == nil || a.cluster == nil {
		return
	}

	clusterState := a.collectObservedClusterState()
	alive, suspect, dead := clusterNodeCounts(clusterState.nodes)
	a.metrics.Controller.SetNodeCounts(alive, suspect, dead)
	a.metrics.Controller.SetTaskActive(controllerActiveTaskCounts(clusterState.tasks))
	a.metrics.Controller.SetMigrationsActive(len(clusterState.migrations))
}

func (a *App) refreshStorageMetrics() {
	if a == nil || a.metrics == nil || a.metrics.Storage == nil {
		return
	}

	usageByStore, _, _ := a.collectStorageUsage()
	a.metrics.Storage.SetDiskUsage(usageByStore)
}

func (a *App) refreshTransportMetrics() {
	if a == nil || a.metrics == nil || a.metrics.Transport == nil {
		return
	}

	activeByPeer := make(map[string]int)
	idleByPeer := make(map[string]int)
	merge := func(stats []transport.PoolPeerStats) {
		for _, stat := range stats {
			peer := strconv.FormatUint(uint64(stat.NodeID), 10)
			activeByPeer[peer] += stat.Active
			idleByPeer[peer] += stat.Idle
		}
	}

	if a.dataPlanePool != nil {
		merge(a.dataPlanePool.Stats())
	}
	if a.cluster != nil {
		merge(a.cluster.TransportPoolStats())
	}

	a.metrics.Transport.SetPoolConnections(activeByPeer, idleByPeer)
}

func (a *App) storageHealthSnapshot() map[string]any {
	usageByStore, totalUsage, storeErrors := a.collectStorageUsage()

	diskFreePath := a.storageStatPath()
	diskFreeBytes, freeErr := filesystemFreeBytes(diskFreePath)

	status := "healthy"
	if len(storeErrors) > 0 || freeErr != nil {
		status = "degraded"
	}

	snapshot := map[string]any{
		"status":           status,
		"disk_usage_bytes": totalUsage,
		"stores":           usageByStore,
	}
	if freeErr == nil {
		snapshot["disk_free_bytes"] = int64(diskFreeBytes)
	} else {
		snapshot["disk_free_error"] = freeErr.Error()
	}
	if len(storeErrors) > 0 {
		snapshot["errors"] = storeErrors
	}
	return snapshot
}

func (a *App) collectStorageUsage() (map[string]int64, int64, map[string]string) {
	usageByStore := make(map[string]int64, 5)
	storeErrors := make(map[string]string)

	var totalUsage int64
	for _, store := range a.storageStorePaths() {
		size, err := dirUsageBytes(store.path)
		if err != nil {
			storeErrors[store.name] = err.Error()
			continue
		}
		usageByStore[store.name] = size
		totalUsage += size
	}
	return usageByStore, totalUsage, storeErrors
}

func (a *App) storageStorePaths() []struct {
	name string
	path string
} {
	return []struct {
		name string
		path string
	}{
		{name: "meta", path: a.cfg.Storage.DBPath},
		{name: "raft", path: a.cfg.Storage.RaftPath},
		{name: "channel_log", path: a.cfg.Storage.ChannelLogPath},
		{name: "controller_meta", path: a.cfg.Storage.ControllerMetaPath},
		{name: "controller_raft", path: a.cfg.Storage.ControllerRaftPath},
	}
}

func (a *App) storageStatPath() string {
	for _, path := range []string{
		a.cfg.Node.DataDir,
		a.cfg.Storage.DBPath,
		a.cfg.Storage.RaftPath,
		a.cfg.Storage.ChannelLogPath,
		a.cfg.Storage.ControllerMetaPath,
		a.cfg.Storage.ControllerRaftPath,
	} {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return a.cfg.Node.DataDir
}

func clusterNodeCounts(nodes []controllermeta.ClusterNode) (alive, suspect, dead int) {
	for _, node := range nodes {
		switch node.Status {
		case controllermeta.NodeStatusAlive, controllermeta.NodeStatusDraining:
			alive++
		case controllermeta.NodeStatusSuspect:
			suspect++
		case controllermeta.NodeStatusDead:
			dead++
		}
	}
	return alive, suspect, dead
}

func localSlotRole(localNodeID uint64, view controllermeta.SlotRuntimeView) string {
	if view.LeaderID == localNodeID {
		return "leader"
	}
	for _, peer := range view.CurrentPeers {
		if peer == localNodeID {
			return "follower"
		}
	}
	return "remote"
}

func controllerActiveTaskCounts(tasks []controllermeta.ReconcileTask) map[string]int {
	counts := make(map[string]int, 3)
	for _, task := range tasks {
		if task.Status == controllermeta.TaskStatusFailed {
			continue
		}
		counts[controllerTaskKind(task.Kind)]++
	}
	return counts
}

func controllerTaskKind(kind controllermeta.TaskKind) string {
	switch kind {
	case controllermeta.TaskKindBootstrap:
		return "bootstrap"
	case controllermeta.TaskKindRepair:
		return "repair"
	case controllermeta.TaskKindRebalance:
		return "rebalance"
	default:
		return "unknown"
	}
}

func transportMsgType(msgType uint8) string {
	switch msgType {
	case transport.MsgTypeRPCRequest:
		return "rpc_request"
	case transport.MsgTypeRPCResponse:
		return "rpc_response"
	default:
		return strconv.FormatUint(uint64(msgType), 10)
	}
}

func dirUsageBytes(root string) (int64, error) {
	if root == "" {
		return 0, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func filesystemFreeBytes(path string) (uint64, error) {
	if path == "" {
		return 0, os.ErrNotExist
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
