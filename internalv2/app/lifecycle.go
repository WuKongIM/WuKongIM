package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultClusterWriteReadyTimeout = 10 * time.Second
	clusterWriteReadyPollInterval   = 10 * time.Millisecond
	clusterWriteReadyProbeTimeout   = time.Second
)

// clusterWriteReadyRuntime exposes the clusterv2 route state needed before gateway sends are admitted.
type clusterWriteReadyRuntime interface {
	Snapshot() clusterv2.Snapshot
	RouteHashSlot(uint16) (clusterv2.Route, error)
}

// clusterWriteProbeRuntime optionally proves that routed Slot writes can commit.
type clusterWriteProbeRuntime interface {
	ProbeWriteReady(context.Context) error
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
	if a.stopped {
		return ErrStopped
	}
	if a.started {
		return ErrAlreadyStarted
	}
	if err := a.cluster.Start(ctx); err != nil {
		a.logLifecycleError("cluster", "start", err)
		return err
	}
	a.started = true
	a.clusterStarted = true
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
	if err := a.waitClusterWriteReady(ctx); err != nil {
		stopErr := a.rollbackStarted(ctx)
		a.logLifecycleError("cluster_write_ready", "start", err)
		return errors.Join(err, stopErr)
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
	return nil
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
		return a.syncLogger()
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
	if a.channelAppendStarted && a.channelAppends != nil {
		if stopErr := a.channelAppends.Stop(ctx); stopErr != nil {
			a.logLifecycleWarn("channel_append", "stop", stopErr)
			err = errors.Join(err, stopErr)
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
	if !a.gatewayStarted && !a.prometheusStarted && !a.managerStarted && !a.apiStarted && !a.topStarted && !a.channelAppendStarted && !a.deliveryStarted && !a.webhookStarted && !a.pluginHookStarted && !a.pluginRuntimeStarted && !a.conversationActiveStarted && !a.conversationRouteStarted && !a.presenceStarted && !a.seedJoinStarted && !a.clusterStarted {
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
	if err == nil {
		a.started = false
	}
	return err
}

func (a *App) logLifecycleError(component, phase string, err error) {
	if err == nil {
		return
	}
	a.lifecycleLogger().Error("app lifecycle component failed",
		wklog.Event("internalv2.app.lifecycle_start_failed"),
		wklog.String("component", component),
		wklog.String("phase", phase),
		wklog.Error(err),
	)
}

func (a *App) logLifecycleWarn(component, phase string, err error) {
	if err == nil {
		return
	}
	event := "internalv2.app.lifecycle_stop_failed"
	if phase == "rollback_stop" {
		event = "internalv2.app.lifecycle_rollback_failed"
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
		if clusterWriteReady(waitCtx, routes, &lastErr) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("internalv2/app: cluster write readiness: %w", lastErr)
			}
			return fmt.Errorf("internalv2/app: cluster write readiness: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func clusterWriteReady(ctx context.Context, routes clusterWriteReadyRuntime, lastErr *error) bool {
	snapshot := routes.Snapshot()
	if !snapshot.RoutesReady || !snapshot.SlotsReady || !snapshot.ChannelsReady || snapshot.HashSlotCount == 0 {
		*lastErr = fmt.Errorf("snapshot not ready: routes=%t slots=%t channels=%t hashSlotCount=%d", snapshot.RoutesReady, snapshot.SlotsReady, snapshot.ChannelsReady, snapshot.HashSlotCount)
		return false
	}
	for hashSlot := uint16(0); hashSlot < snapshot.HashSlotCount; hashSlot++ {
		route, err := routes.RouteHashSlot(hashSlot)
		if err != nil {
			*lastErr = fmt.Errorf("route hash slot %d: %w", hashSlot, err)
			return false
		}
		if route.Leader == 0 {
			*lastErr = fmt.Errorf("route hash slot %d has no leader", hashSlot)
			return false
		}
	}
	if probe, ok := routes.(clusterWriteProbeRuntime); ok {
		probeCtx, cancel := context.WithTimeout(ctx, clusterWriteReadyProbeTimeout)
		err := probe.ProbeWriteReady(probeCtx)
		cancel()
		if err != nil {
			*lastErr = fmt.Errorf("write probe: %w", err)
			return false
		}
	}
	return true
}
