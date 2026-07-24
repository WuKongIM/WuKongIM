package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultClusterWriteReadyTimeout = 30 * time.Second
	clusterWriteReadyPollInterval   = 10 * time.Millisecond
	clusterWriteReadyProbeTimeout   = time.Second
	clusterWriteReadyProbePerSlot   = 500 * time.Millisecond
)

// clusterWriteReadyRuntime exposes the cluster route state needed before gateway sends are admitted.
type clusterWriteReadyRuntime interface {
	Snapshot() cluster.Snapshot
	RouteHashSlot(uint16) (cluster.Route, error)
}

// clusterWriteProbeRuntime optionally proves that routed Slot writes can commit.
type clusterWriteProbeRuntime interface {
	ProbeWriteReady(context.Context) error
}

// restoreActivationFenceRuntime exposes persisted recovery state before normal
// entry runtimes or their mutating readiness probes are admitted.
type restoreActivationFenceRuntime interface {
	LoadRestoreCoordinationState(context.Context) (controller.ClusterState, error)
}

type startupClusterRuntime interface {
	ClusterID() string
	ListenAddr() string
	NodeCount() int
}

type startupAddrRuntime interface {
	Addr() string
}

type startupGatewayRuntime interface {
	ListenerAddr(name string) string
}

// Start starts the cluster first, then optional entry runtimes when configured.
func (a *App) Start(ctx context.Context) error {
	if a == nil {
		return ErrInvalidConfig
	}
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()

	if a.cluster == nil {
		return ErrInvalidConfig
	}
	if a.cfg.Backup.RestoreMode && a.backupInitErr != nil {
		return a.backupInitErr
	}
	if a.stopped {
		return ErrStopped
	}
	if a.started {
		return ErrAlreadyStarted
	}
	startedAt := time.Now()
	startingSnapshot := a.startupSnapshot()
	a.startupConsole.starting(startingSnapshot)
	a.lifecycleLogger().Info("Starting WuKongIM", append(
		a.startupLogFields(startingSnapshot),
		wklog.Event("internal.app.starting"),
	)...)
	if err := a.cluster.Start(ctx); err != nil {
		a.logLifecycleError("cluster", "start", err)
		return err
	}
	a.started = true
	a.clusterStarted = true
	if err := a.backfillControllerTaskAudit(ctx); err != nil {
		a.logLifecycleWarn("controller_task_audit", "backfill", err)
	}
	if a.seedJoinLoop != nil {
		if err := a.seedJoinLoop.Start(ctx); err != nil {
			a.logLifecycleError("seed_join", "start", err)
			stopErr := a.cluster.Stop(ctx)
			if stopErr != nil {
				a.logLifecycleWarn("cluster", "rollback_stop", stopErr)
			}
			if stopErr == nil {
				a.started = false
				a.clusterStarted = false
			}
			return errors.Join(err, stopErr)
		}
		a.seedJoinStarted = true
		if err := a.seedJoinLoop.WaitForAdmission(ctx); err != nil {
			stopErr := a.rollbackStarted(ctx)
			a.logLifecycleError("seed_join_admission", "start", err)
			return errors.Join(err, stopErr)
		}
	}
	if !a.cfg.Backup.RestoreMode {
		if err := a.waitRestoreActivationFence(ctx); err != nil {
			stopErr := a.rollbackStarted(ctx)
			a.logLifecycleError("restore_activation_fence", "start", err)
			return errors.Join(err, stopErr)
		}
	}
	if !a.seedJoinPreActivationMode(ctx) {
		var err error
		if a.cfg.Backup.RestoreMode {
			err = a.waitClusterRestoreReady(ctx)
		} else {
			err = a.waitClusterWriteReady(ctx)
		}
		if err != nil {
			stopErr := a.rollbackStarted(ctx)
			readiness := "cluster_write_ready"
			if a.cfg.Backup.RestoreMode {
				readiness = "cluster_restore_ready"
			}
			a.logLifecycleError(readiness, "start", err)
			return errors.Join(err, stopErr)
		}
	}
	if a.cfg.Backup.RestoreMode {
		if a.restoreRuntime != nil {
			if err := a.restoreRuntime.Start(ctx); err != nil {
				stopErr := a.rollbackStarted(ctx)
				return errors.Join(err, stopErr)
			}
			a.restoreRuntimeStarted = true
		}
		if a.manager != nil {
			if err := a.manager.Start(); err != nil {
				stopErr := a.rollbackStarted(ctx)
				return errors.Join(err, stopErr)
			}
			a.managerStarted = true
		}
		if a.prometheus != nil {
			if err := a.prometheus.Start(ctx); err != nil {
				stopErr := a.rollbackStarted(ctx)
				return errors.Join(err, stopErr)
			}
			a.prometheusStarted = true
		}
		a.logStarted(startedAt)
		return nil
	}
	if a.backupRuntime != nil {
		if err := a.backupRuntime.Start(ctx); err != nil {
			a.logLifecycleWarn("backup_runtime", "start", err)
		} else {
			a.backupRuntimeStarted = true
		}
	}
	if a.conversationRouteLifecycle != nil {
		if err := a.conversationRouteLifecycle.Start(ctx); err != nil {
			a.logLifecycleError("conversation_route_lifecycle", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.conversationRouteStarted = true
	}
	if a.conversationActiveWorker != nil {
		if err := a.conversationActiveWorker.Start(ctx); err != nil {
			a.logLifecycleError("conversation_active_worker", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.conversationActiveStarted = true
	}
	if a.presenceWorker != nil {
		if err := a.presenceWorker.Start(ctx); err != nil {
			a.logLifecycleError("presence_worker", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.presenceStarted = true
	}
	if a.pluginRuntime != nil {
		if err := a.pluginRuntime.Start(ctx); err != nil {
			a.logLifecycleError("plugin_runtime", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.pluginRuntimeStarted = true
	}
	if a.pluginHook != nil {
		if err := a.pluginHook.Start(ctx); err != nil {
			a.logLifecycleError("plugin_hook", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.pluginHookStarted = true
	}
	if a.webhook != nil {
		if err := a.webhook.Start(ctx); err != nil {
			a.logLifecycleError("webhook", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.webhookStarted = true
	}
	if a.deliveryWorker != nil {
		if err := a.deliveryWorker.Start(ctx); err != nil {
			a.logLifecycleError("delivery_worker", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.deliveryStarted = true
	}
	if a.channelAppends != nil {
		if err := a.channelAppends.Start(ctx); err != nil {
			a.logLifecycleError("channel_append", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.channelAppendStarted = true
	}
	if a.top != nil {
		if err := a.top.Start(ctx); err != nil {
			a.logLifecycleError("top", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.topStarted = true
	}
	if a.api != nil {
		if err := a.api.Start(); err != nil {
			a.logLifecycleError("api", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.apiStarted = true
	}
	if a.manager != nil {
		if err := a.manager.Start(); err != nil {
			a.logLifecycleError("manager", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.managerStarted = true
	}
	if a.prometheus != nil {
		if err := a.prometheus.Start(ctx); err != nil {
			a.logLifecycleError("prometheus", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.prometheusStarted = true
	}
	if a.gateway != nil {
		if err := a.gateway.Start(); err != nil {
			a.logLifecycleError("gateway", "start", err)
			stopErr := a.rollbackStarted(ctx)
			return errors.Join(err, stopErr)
		}
		a.gatewayStarted = true
	}
	a.logStarted(startedAt)
	return nil
}

func (a *App) waitRestoreActivationFence(ctx context.Context) error {
	runtime, ok := a.cluster.(restoreActivationFenceRuntime)
	if !ok {
		return nil
	}
	timeout := a.cfg.Cluster.Timeouts.Start
	if timeout <= 0 {
		timeout = defaultClusterWriteReadyTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(clusterWriteReadyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		state, err := runtime.LoadRestoreCoordinationState(waitCtx)
		if err == nil {
			return a.validateRestoreActivationFence(state)
		}
		lastErr = err
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("internal/app: load restore activation fence: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func (a *App) validateRestoreActivationFence(state controller.ClusterState) error {
	if state.Restore == nil || state.Restore.Plan == nil {
		return nil
	}
	plan := state.Restore.Plan
	if plan.Status != controller.RestoreStatus("activated") {
		return fmt.Errorf("internal/app: restore plan %s is %s; activate it in restore mode before enabling ordinary traffic", plan.ID, plan.Status)
	}
	if plan.TargetClusterID != state.ClusterID || plan.HashSlotCount != state.Config.HashSlotCount {
		return fmt.Errorf("internal/app: activated restore plan target does not match the current cluster")
	}
	if a.cfg.Backup.Enabled && a.cfg.Backup.SourceGeneration != plan.TargetGeneration {
		return fmt.Errorf("internal/app: backup source generation must equal activated restore target generation %q", plan.TargetGeneration)
	}
	if len(plan.Partitions) != int(plan.HashSlotCount) {
		return fmt.Errorf("internal/app: activated restore plan has incomplete partition evidence")
	}
	var maxMessageID uint64
	for hashSlot, partition := range plan.Partitions {
		if partition.HashSlot != uint16(hashSlot) || partition.EvidenceVersion != backupartifact.PartitionEvidenceVersion ||
			(partition.MessageCount == 0) != (partition.MaxMessageID == 0) || !partition.Installed || !partition.Verified {
			return fmt.Errorf("internal/app: activated restore plan has unverified partition evidence")
		}
		if partition.MaxMessageID > maxMessageID {
			maxMessageID = partition.MaxMessageID
		}
	}
	if maxMessageID > 0 {
		if a.messageIDs == nil {
			return fmt.Errorf("internal/app: message ID allocator is unavailable for activated restore fence")
		}
		if err := a.messageIDs.SetFloor(maxMessageID); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) logStarted(startedAt time.Time) {
	readySnapshot := a.startupSnapshot()
	startupDuration := time.Since(startedAt)
	a.lifecycleLogger().Info("WuKongIM started", append(
		a.startupLogFields(readySnapshot),
		wklog.Event("internal.app.started"),
		wklog.Duration("startupDuration", startupDuration),
	)...)
	a.startupConsole.ready(readySnapshot, startupDuration)
}

func (a *App) startupSnapshot() startupConsoleSnapshot {
	clusterID := a.cfg.Cluster.Control.ClusterID
	clusterListen := a.cfg.Cluster.ListenAddr
	deployment := configuredDeployment(a.cfg.Cluster)
	if runtime, ok := a.cluster.(startupClusterRuntime); ok {
		if value := strings.TrimSpace(runtime.ClusterID()); value != "" {
			clusterID = value
		}
		if value := strings.TrimSpace(runtime.ListenAddr()); value != "" {
			clusterListen = value
		}
		if nodeCount := runtime.NodeCount(); nodeCount > 0 {
			deployment = deploymentFromNodeCount(nodeCount)
		}
	}
	return startupConsoleSnapshot{
		nodeID:           effectiveAppNodeID(a.cfg),
		clusterID:        clusterID,
		deployment:       deployment,
		configPath:       a.cfg.ConfigPath,
		dataDir:          a.cfg.DataDir,
		clusterListen:    clusterListen,
		apiListen:        boundRuntimeAddr(a.api, a.cfg.API.ListenAddr),
		managerListen:    boundRuntimeAddr(a.manager, a.cfg.Manager.ListenAddr),
		gatewayListeners: a.gatewayListenerSnapshots(),
		metricsEnabled:   a.cfg.Observability.MetricsEnabled,
	}
}

func (a *App) startupLogFields(snapshot startupConsoleSnapshot) []wklog.Field {
	return []wklog.Field{
		wklog.NodeID(snapshot.nodeID),
		wklog.String("clusterID", snapshot.clusterID),
		wklog.String("deployment", snapshot.deployment),
		wklog.String("configPath", snapshot.configPath),
		wklog.String("dataDir", snapshot.dataDir),
		wklog.String("clusterListen", snapshot.clusterListen),
		wklog.String("apiListen", snapshot.apiListen),
		wklog.String("managerListen", snapshot.managerListen),
		wklog.Any("gatewayListeners", gatewayListenerLogSummaries(snapshot.gatewayListeners)),
		wklog.Bool("metricsEnabled", snapshot.metricsEnabled),
	}
}

func effectiveAppNodeID(cfg Config) uint64 {
	if cfg.Cluster.NodeID != 0 {
		return cfg.Cluster.NodeID
	}
	return cfg.NodeID
}

func configuredDeployment(cfg cluster.Config) string {
	if len(cfg.Control.Voters) <= 1 && len(cfg.Join.Seeds) == 0 && cfg.Slots.ReplicaCount <= 1 {
		return "single-node-cluster"
	}
	return "multi-node-cluster"
}

func deploymentFromNodeCount(nodeCount int) string {
	if nodeCount == 1 {
		return "single-node-cluster"
	}
	return "multi-node-cluster"
}

func boundRuntimeAddr(runtime any, configured string) string {
	if listener, ok := runtime.(startupAddrRuntime); ok {
		if addr := strings.TrimSpace(listener.Addr()); addr != "" {
			return addr
		}
	}
	return strings.TrimSpace(configured)
}

func (a *App) gatewayListenerSnapshots() []startupGatewayListener {
	runtime, _ := a.gateway.(startupGatewayRuntime)
	out := make([]startupGatewayListener, 0, len(a.cfg.Gateway.Listeners))
	for _, listener := range a.cfg.Gateway.Listeners {
		address := listener.Address
		if runtime != nil {
			if bound := strings.TrimSpace(runtime.ListenerAddr(listener.Name)); bound != "" {
				address = bound
			}
		}
		out = append(out, startupGatewayListener{
			name:    strings.TrimSpace(listener.Name),
			address: gatewayListenerAddress(listener, address),
		})
	}
	return out
}

func gatewayListenerAddress(listener gateway.ListenerOptions, rawAddress string) string {
	network := strings.ToLower(strings.TrimSpace(listener.Network))
	address := "tcp://" + normalizeTCPAddress(rawAddress)
	if network == "websocket" {
		address = normalizeWebsocketAddress(rawAddress)
		path := strings.TrimSpace(listener.Path)
		if path != "" {
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			address += path
		}
	}
	return address
}

func gatewayListenerLogSummaries(listeners []startupGatewayListener) []string {
	out := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		if listener.name == "" {
			out = append(out, listener.address)
			continue
		}
		out = append(out, listener.name+"="+listener.address)
	}
	return out
}

// Stop stops entry runtimes first, then workers and the cluster runtime.
func (a *App) Stop(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()

	a.stopped = true
	a.restoreDiagnosticsSink()
	if !a.started {
		err := a.closeControllerTaskAudit()
		if err != nil {
			a.logLifecycleWarn("controller_task_audit", "stop", err)
		}
		return errors.Join(err, a.syncLogger())
	}
	var err error
	if a.gatewayStarted && a.gateway != nil {
		if stopErr := a.gateway.Stop(); stopErr != nil {
			a.logLifecycleWarn("gateway", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.gatewayStarted = false
		}
	}
	if a.prometheusStarted && a.prometheus != nil {
		if stopErr := a.prometheus.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("prometheus", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.prometheusStarted = false
		}
	}
	if a.managerStarted && a.manager != nil {
		if stopErr := a.manager.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("manager", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.managerStarted = false
		}
	}
	if a.apiStarted && a.api != nil {
		if stopErr := a.api.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("api", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.apiStarted = false
		}
	}
	if a.topStarted && a.top != nil {
		if stopErr := a.top.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("top", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.topStarted = false
		}
	}
	if a.backupRuntimeStarted && a.backupRuntime != nil {
		if stopErr := a.backupRuntime.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("backup_runtime", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.backupRuntimeStarted = false
		}
	}
	if a.restoreRuntimeStarted && a.restoreRuntime != nil {
		if stopErr := a.restoreRuntime.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("restore_runtime", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.restoreRuntimeStarted = false
		}
	}
	if a.channelAppendStarted && a.channelAppends != nil {
		if stopErr := a.channelAppends.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("channel_append", "stop", stopErr)
			err = errors.Join(err, stopErr)
			// Channel append keeps the same graceful drain alive after a caller
			// deadline. Its delivery, conversation, plugin, webhook, and cluster
			// dependencies must remain running until a later Stop finishes that drain.
			return errors.Join(err, a.syncLogger())
		} else {
			a.channelAppendStarted = false
		}
	}
	if a.deliveryStarted && a.deliveryWorker != nil {
		if stopErr := a.deliveryWorker.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("delivery_worker", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.deliveryStarted = false
		}
	}
	if a.webhookStarted && a.webhook != nil {
		if stopErr := a.webhook.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("webhook", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.webhookStarted = false
		}
	}
	if a.pluginHookStarted && a.pluginHook != nil {
		if stopErr := a.pluginHook.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("plugin_hook", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.pluginHookStarted = false
		}
	}
	if a.pluginRuntimeStarted && a.pluginRuntime != nil {
		if stopErr := a.pluginRuntime.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("plugin_runtime", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.pluginRuntimeStarted = false
		}
	}
	if a.conversationActiveStarted && a.conversationActiveWorker != nil {
		if stopErr := a.conversationActiveWorker.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("conversation_active_worker", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.conversationActiveStarted = false
		}
	}
	if a.conversationRouteStarted && a.conversationRouteLifecycle != nil {
		if stopErr := a.conversationRouteLifecycle.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("conversation_route_lifecycle", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.conversationRouteStarted = false
		}
	}
	if a.presenceStarted && a.presenceWorker != nil {
		if stopErr := a.presenceWorker.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("presence_worker", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.presenceStarted = false
		}
	}
	if a.seedJoinStarted && a.seedJoinLoop != nil {
		if stopErr := a.seedJoinLoop.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("seed_join", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.seedJoinStarted = false
		}
	}
	if a.clusterStarted && a.cluster != nil {
		if stopErr := a.cluster.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("cluster", "stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.clusterStarted = false
		}
	}
	if stopErr := a.closeControllerTaskAudit(); stopErr != nil {
		a.logLifecycleWarn("controller_task_audit", "stop", stopErr)
		err = errors.Join(err, stopErr)
	}
	if !a.gatewayStarted && !a.prometheusStarted && !a.managerStarted && !a.apiStarted && !a.topStarted && !a.backupRuntimeStarted && !a.restoreRuntimeStarted && !a.channelAppendStarted && !a.deliveryStarted && !a.webhookStarted && !a.pluginHookStarted && !a.pluginRuntimeStarted && !a.conversationActiveStarted && !a.conversationRouteStarted && !a.presenceStarted && !a.seedJoinStarted && !a.clusterStarted {
		a.started = false
	}
	err = errors.Join(err, a.syncLogger())
	return err
}

func (a *App) syncLogger() error {
	if a == nil || a.logger == nil {
		return nil
	}
	return a.logger.Sync()
}

func (a *App) rollbackStarted(ctx context.Context) error {
	var err error
	if a.restoreRuntimeStarted && a.restoreRuntime != nil {
		if stopErr := a.restoreRuntime.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("restore_runtime", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.restoreRuntimeStarted = false
		}
	}
	if a.backupRuntimeStarted && a.backupRuntime != nil {
		if stopErr := a.backupRuntime.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("backup_runtime", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.backupRuntimeStarted = false
		}
	}
	if a.prometheusStarted && a.prometheus != nil {
		if stopErr := a.prometheus.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("prometheus", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.prometheusStarted = false
		}
	}
	if a.managerStarted && a.manager != nil {
		if stopErr := a.manager.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("manager", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.managerStarted = false
		}
	}
	if a.apiStarted && a.api != nil {
		if stopErr := a.api.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("api", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.apiStarted = false
		}
	}
	if a.topStarted && a.top != nil {
		if stopErr := a.top.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("top", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.topStarted = false
		}
	}
	if a.channelAppendStarted && a.channelAppends != nil {
		if stopErr := a.channelAppends.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("channel_append", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
			// Keep already-started post-commit dependencies available for the
			// channel append drain. App remains retryable because started and the
			// component flags are cleared only after a complete later Stop.
			return err
		} else {
			a.channelAppendStarted = false
		}
	}
	if a.deliveryStarted && a.deliveryWorker != nil {
		if stopErr := a.deliveryWorker.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("delivery_worker", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.deliveryStarted = false
		}
	}
	if a.webhookStarted && a.webhook != nil {
		if stopErr := a.webhook.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("webhook", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.webhookStarted = false
		}
	}
	if a.pluginHookStarted && a.pluginHook != nil {
		if stopErr := a.pluginHook.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("plugin_hook", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.pluginHookStarted = false
		}
	}
	if a.pluginRuntimeStarted && a.pluginRuntime != nil {
		if stopErr := a.pluginRuntime.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("plugin_runtime", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.pluginRuntimeStarted = false
		}
	}
	if a.conversationActiveStarted && a.conversationActiveWorker != nil {
		if stopErr := a.conversationActiveWorker.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("conversation_active_worker", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.conversationActiveStarted = false
		}
	}
	if a.conversationRouteStarted && a.conversationRouteLifecycle != nil {
		if stopErr := a.conversationRouteLifecycle.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("conversation_route_lifecycle", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.conversationRouteStarted = false
		}
	}
	if a.presenceStarted && a.presenceWorker != nil {
		if stopErr := a.presenceWorker.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("presence_worker", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.presenceStarted = false
		}
	}
	if a.seedJoinStarted && a.seedJoinLoop != nil {
		if stopErr := a.seedJoinLoop.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("seed_join", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.seedJoinStarted = false
		}
	}
	if a.clusterStarted && a.cluster != nil {
		if stopErr := a.cluster.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("cluster", "rollback_stop", stopErr)
			err = errors.Join(err, stopErr)
		} else {
			a.clusterStarted = false
		}
	}
	if stopErr := a.closeControllerTaskAudit(); stopErr != nil {
		a.logLifecycleWarn("controller_task_audit", "rollback_stop", stopErr)
		err = errors.Join(err, stopErr)
	}
	if err == nil {
		a.started = false
	}
	return err
}

func (a *App) logLifecycleError(component, phase string, err error) {
	if err == nil {
		return
	}
	if phase == "start" {
		a.startupConsole.failed(component, err)
	}
	a.lifecycleLogger().Error("app lifecycle component failed",
		wklog.Event("internal.app.lifecycle_start_failed"),
		wklog.String("component", component),
		wklog.String("phase", phase),
		wklog.Error(err),
	)
}

func (a *App) logLifecycleWarn(component, phase string, err error) {
	if err == nil {
		return
	}
	event := "internal.app.lifecycle_stop_failed"
	if phase == "rollback_stop" {
		event = "internal.app.lifecycle_rollback_failed"
	}
	a.lifecycleLogger().Warn("app lifecycle component stop failed",
		wklog.Event(event),
		wklog.String("component", component),
		wklog.String("phase", phase),
		wklog.Error(err),
	)
}

func (a *App) lifecycleLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger.Named("lifecycle")
}

func (a *App) readyzReport(ctx context.Context) (bool, any) {
	if a == nil || a.cluster == nil {
		return false, map[string]any{"ready": false, "reason": "cluster not configured"}
	}
	if a.seedJoinPreActivationMode(ctx) {
		a.lifecycleMu.Lock()
		ready := a.clusterStarted && a.gatewayStarted
		a.lifecycleMu.Unlock()
		if ready {
			return true, map[string]any{"ready": true}
		}
		return false, map[string]any{"ready": false, "reason": "seed join runtime not ready"}
	}
	routes, ok := a.cluster.(clusterWriteReadyRuntime)
	if !ok {
		return true, map[string]any{"ready": true}
	}
	var lastErr error
	if clusterWriteReady(ctx, routes, &lastErr) {
		return true, map[string]any{"ready": true}
	}
	reason := "cluster write routing not ready"
	if lastErr != nil {
		reason = lastErr.Error()
	}
	return false, map[string]any{"ready": false, "reason": reason}
}

func (a *App) waitClusterWriteReady(ctx context.Context) error {
	return a.waitClusterReady(ctx, "cluster write readiness", clusterWriteReady)
}

// waitClusterRestoreReady proves that the recovery stores and routing table are
// available without issuing the ordinary mutating write-readiness probe.
func (a *App) waitClusterRestoreReady(ctx context.Context) error {
	return a.waitClusterReady(ctx, "cluster restore readiness", clusterRestoreReady)
}

func (a *App) waitClusterReady(ctx context.Context, label string, ready func(context.Context, clusterWriteReadyRuntime, *error) bool) error {
	routes, ok := a.cluster.(clusterWriteReadyRuntime)
	if !ok {
		return nil
	}
	timeout := a.cfg.Cluster.Timeouts.Start
	if timeout <= 0 {
		timeout = defaultClusterWriteReadyTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(clusterWriteReadyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		if ready(waitCtx, routes, &lastErr) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("internal/app: %s: %w", label, lastErr)
			}
			return fmt.Errorf("internal/app: %s: %w", label, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func clusterWriteReady(ctx context.Context, routes clusterWriteReadyRuntime, lastErr *error) bool {
	snapshot, ready := clusterRestoreRoutingReady(routes, lastErr)
	if !ready {
		return false
	}
	if probe, ok := routes.(clusterWriteProbeRuntime); ok {
		probeCtx, cancel := context.WithTimeout(ctx, clusterWriteReadyProbeBudget(snapshot))
		err := probe.ProbeWriteReady(probeCtx)
		cancel()
		if err != nil {
			*lastErr = fmt.Errorf("write probe: %w", err)
			return false
		}
	}
	return true
}

func clusterRestoreReady(_ context.Context, routes clusterWriteReadyRuntime, lastErr *error) bool {
	_, ready := clusterRestoreRoutingReady(routes, lastErr)
	return ready
}

func clusterRestoreRoutingReady(routes clusterWriteReadyRuntime, lastErr *error) (cluster.Snapshot, bool) {
	snapshot := routes.Snapshot()
	if !snapshot.RoutesReady || !snapshot.SlotsReady || !snapshot.ChannelsReady || snapshot.HashSlotCount == 0 {
		*lastErr = fmt.Errorf("snapshot not ready: routes=%t slots=%t channels=%t hashSlotCount=%d", snapshot.RoutesReady, snapshot.SlotsReady, snapshot.ChannelsReady, snapshot.HashSlotCount)
		return snapshot, false
	}
	for hashSlot := uint16(0); hashSlot < snapshot.HashSlotCount; hashSlot++ {
		route, err := routes.RouteHashSlot(hashSlot)
		if err != nil {
			*lastErr = fmt.Errorf("route hash slot %d: %w", hashSlot, err)
			return snapshot, false
		}
		if route.Leader == 0 {
			*lastErr = fmt.Errorf("route hash slot %d has no leader", hashSlot)
			return snapshot, false
		}
	}
	return snapshot, true
}

func clusterWriteReadyProbeBudget(snapshot cluster.Snapshot) time.Duration {
	budget := clusterWriteReadyProbeTimeout
	if snapshot.SlotCount > 0 {
		scaled := time.Duration(snapshot.SlotCount) * clusterWriteReadyProbePerSlot
		if scaled > budget {
			budget = scaled
		}
	}
	return budget
}
