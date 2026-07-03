package app

import (
	"context"
	"errors"
	"time"

	applifecycle "github.com/WuKongIM/WuKongIM/internal/legacy/app/lifecycle"
)

const (
	apiStopTimeout                  = 5 * time.Second
	activeHintStopTimeout           = time.Second
	defaultDataPlaneDialTimeout     = 5 * time.Second
	defaultManagedSlotsReadyTimeout = 30 * time.Second
)

func (a *App) Start() error {
	if a == nil || a.cluster == nil || a.gateway == nil {
		return ErrNotBuilt
	}
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	if a.stopped.Load() {
		return ErrStopped
	}
	if !a.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}

	manager := applifecycle.NewManager(a.startLifecycleComponents()...)
	if err := manager.StartWithRollbackTimeout(context.Background(), apiStopTimeout); err != nil {
		a.lifecycleMgr = nil
		a.started.Store(false)
		return err
	}
	a.lifecycleMgr = manager
	return nil
}

func (a *App) Stop() error {
	if a == nil {
		return nil
	}
	a.lifecycle.Lock()
	defer a.lifecycle.Unlock()
	a.stopped.Store(true)

	var err error
	a.stopOnce.Do(func() {
		a.started.Store(false)
		a.restoreDiagnosticsSink()
		stopCtx, cancel := context.WithTimeout(context.Background(), apiStopTimeout)
		defer cancel()
		err = errors.Join(
			a.stopLifecycleManager(stopCtx),
			a.stopDashboardCollectorWithError(),
			a.closeChannelLogDB(),
			a.closeRaftDB(),
			a.closeWKDB(),
			a.syncLogger(),
		)
	})
	return err
}

func (a *App) stopDashboardCollectorWithError() error {
	a.stopDashboardCollector()
	return nil
}

func (a *App) stopDashboardCollector() {
	if a == nil || a.dashboardCollector == nil {
		return
	}
	a.dashboardCollectorStop.Do(func() {
		a.dashboardCollector.Stop()
	})
}

func (a *App) stopLifecycleManager(ctx context.Context) error {
	if a.lifecycleMgr != nil {
		err := a.lifecycleMgr.Stop(ctx)
		a.lifecycleMgr = nil
		return err
	}
	return stopComponentsReverse(ctx, a.stopLifecycleComponents())
}

func stopComponentsReverse(ctx context.Context, components []applifecycle.Component) error {
	var errs []error
	for i := len(components) - 1; i >= 0; i-- {
		if err := components[i].Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (a *App) startCluster() error {
	if a.startClusterFn != nil {
		return a.startClusterFn()
	}
	if a.cluster == nil {
		return ErrNotBuilt
	}
	return a.cluster.Start()
}

func (a *App) startGateway() error {
	if a.startGatewayFn != nil {
		return a.startGatewayFn()
	}
	if a.gateway == nil {
		return ErrNotBuilt
	}
	return a.gateway.Start()
}

func (a *App) startChannelMetaSync() error {
	if a.startChannelMetaSyncFn != nil {
		return a.startChannelMetaSyncFn()
	}
	if a.channelMetaSync == nil {
		return nil
	}
	if a.cluster == nil || a.isrRuntime == nil || a.channelLog == nil {
		return ErrNotBuilt
	}
	return a.channelMetaSync.Start()
}

func (a *App) startChannelPlane() error {
	if a.startChannelPlaneFn != nil {
		return a.startChannelPlaneFn()
	}
	if a.channelPlane == nil {
		return nil
	}
	return a.channelPlane.Start()
}

func (a *App) startAPI() error {
	if a.startAPIFn != nil {
		return a.startAPIFn()
	}
	if a.api == nil {
		return nil
	}
	return a.api.Start()
}

func (a *App) startManager() error {
	if a.startManagerFn != nil {
		return a.startManagerFn()
	}
	if a.manager == nil {
		return nil
	}
	return a.manager.Start()
}

func (a *App) startPresence() error {
	if a.startPresenceFn != nil {
		return a.startPresenceFn()
	}
	if a.presenceWorker == nil {
		return nil
	}
	return a.presenceWorker.Start()
}

func (a *App) startConversationProjector() error {
	if a.startConversationProjectorFn != nil {
		return a.startConversationProjectorFn()
	}
	if a.conversationProjector == nil {
		return nil
	}
	return a.conversationProjector.Start()
}

func (a *App) startCMDConversationUpdater() error {
	if a.startCMDConversationUpdaterFn != nil {
		return a.startCMDConversationUpdaterFn()
	}
	if a.cmdConversationUpdater == nil {
		return nil
	}
	return a.cmdConversationUpdater.Start()
}

func (a *App) startConversationActiveHints() error {
	if a.startConversationActiveHintsFn != nil {
		return a.startConversationActiveHintsFn()
	}
	if a.conversationActiveHints == nil {
		return nil
	}
	return a.conversationActiveHints.Start()
}

func (a *App) startDeliveryRuntime(ctx context.Context) error {
	if a.startDeliveryRuntimeFn != nil {
		return a.startDeliveryRuntimeFn()
	}
	if a.deliveryRuntimeLifecycle == nil {
		return nil
	}
	return a.deliveryRuntimeLifecycle.Start(ctx)
}

func (a *App) startCommittedDispatcher(ctx context.Context) error {
	if a.startCommittedDispatcherFn != nil {
		return a.startCommittedDispatcherFn()
	}
	if a.committedDispatcher == nil {
		return nil
	}
	return a.committedDispatcher.Start(ctx)
}

func (a *App) startCommittedReplay(ctx context.Context) error {
	if a.startCommittedReplayFn != nil {
		return a.startCommittedReplayFn(ctx)
	}
	if a.committedReplayer == nil {
		return nil
	}
	return a.committedReplayer.Start(ctx)
}

func (a *App) startChannelMigration(ctx context.Context) error {
	if a.startChannelMigrationFn != nil {
		return a.startChannelMigrationFn(ctx)
	}
	if a.channelMigrationLifecycle == nil {
		return nil
	}
	return a.channelMigrationLifecycle.Start(ctx)
}

func (a *App) startChannelRetention(ctx context.Context) error {
	if a.startChannelRetentionFn != nil {
		return a.startChannelRetentionFn(ctx)
	}
	if a.channelRetentionWorker == nil {
		return nil
	}
	return a.channelRetentionWorker.Start(ctx)
}

func (a *App) startPlugin(ctx context.Context) error {
	if a.startPluginFn != nil {
		return a.startPluginFn(ctx)
	}
	if a.pluginRuntime != nil {
		if err := a.pluginRuntime.Start(ctx); err != nil {
			return err
		}
	}
	if a.pluginReceiveObserver == nil {
		return nil
	}
	return a.pluginReceiveObserver.Start(ctx)
}

func (a *App) stopGateway() error {
	if !a.gatewayOn.Swap(false) {
		return nil
	}
	if a.stopGatewayFn != nil {
		return a.stopGatewayFn()
	}
	if a.gateway == nil {
		return nil
	}
	return a.gateway.Stop()
}

func (a *App) stopPresence() error {
	if !a.presenceOn.Swap(false) {
		return nil
	}
	if a.stopPresenceFn != nil {
		return a.stopPresenceFn()
	}
	if a.presenceWorker == nil {
		return nil
	}
	return a.presenceWorker.Stop()
}

func (a *App) stopConversationProjector() error {
	if !a.conversationOn.Swap(false) {
		return nil
	}
	if a.stopConversationProjectorFn != nil {
		return a.stopConversationProjectorFn()
	}
	if a.conversationProjector == nil {
		return nil
	}
	return a.conversationProjector.Stop()
}

func (a *App) stopCMDConversationUpdater(ctx context.Context) error {
	if !a.cmdConversationUpdaterOn.Swap(false) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if a.stopCMDConversationUpdaterFn != nil {
		return a.stopCMDConversationUpdaterFn(ctx)
	}
	if a.cmdConversationUpdater == nil {
		return nil
	}
	return a.cmdConversationUpdater.StopContext(ctx)
}

func (a *App) stopConversationActiveHints(ctx context.Context) error {
	if !a.conversationHintsOn.Swap(false) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, activeHintStopTimeout)
	defer cancel()
	// Active hints are best-effort; a slow flush should not block app shutdown.
	if a.stopConversationActiveHintsFn != nil {
		if err := a.stopConversationActiveHintsFn(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
	if a.conversationActiveHints == nil {
		return nil
	}
	if err := a.conversationActiveHints.StopContext(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func (a *App) stopDeliveryRuntime(ctx context.Context) error {
	if !a.deliveryRuntimeOn.Swap(false) {
		return nil
	}
	if a.stopDeliveryRuntimeFn != nil {
		return a.stopDeliveryRuntimeFn()
	}
	if a.deliveryRuntimeLifecycle == nil {
		return nil
	}
	return a.deliveryRuntimeLifecycle.StopContext(ctx)
}

func (a *App) stopCommittedDispatcher(ctx context.Context) error {
	if !a.committedDispatcherOn.Swap(false) {
		return nil
	}
	if a.stopCommittedDispatcherFn != nil {
		return a.stopCommittedDispatcherFn(ctx)
	}
	if a.committedDispatcher == nil {
		return nil
	}
	return a.committedDispatcher.StopContext(ctx)
}

func (a *App) stopCommittedReplay(ctx context.Context) error {
	if !a.committedReplayOn.Swap(false) {
		return nil
	}
	if a.stopCommittedReplayFn != nil {
		return a.stopCommittedReplayFn(ctx)
	}
	if a.committedReplayer == nil {
		return nil
	}
	return a.committedReplayer.StopContext(ctx)
}

func (a *App) stopChannelMigration(ctx context.Context) error {
	if !a.channelMigrationOn.Swap(false) {
		return nil
	}
	if a.stopChannelMigrationFn != nil {
		return a.stopChannelMigrationFn(ctx)
	}
	if a.channelMigrationLifecycle == nil {
		return nil
	}
	return a.channelMigrationLifecycle.Stop(ctx)
}

func (a *App) stopChannelRetention(ctx context.Context) error {
	if !a.channelRetentionOn.Swap(false) {
		return nil
	}
	if a.stopChannelRetentionFn != nil {
		return a.stopChannelRetentionFn(ctx)
	}
	if a.channelRetentionWorker == nil {
		return nil
	}
	return a.channelRetentionWorker.Stop(ctx)
}

func (a *App) stopPlugin(ctx context.Context) error {
	if !a.pluginOn.Swap(false) {
		return nil
	}
	if a.stopPluginFn != nil {
		return a.stopPluginFn(ctx)
	}
	var err error
	if a.pluginReceiveObserver != nil {
		err = errors.Join(err, a.pluginReceiveObserver.Stop(ctx))
	}
	if a.pluginRuntime != nil {
		err = errors.Join(err, a.pluginRuntime.Stop(ctx))
	}
	return err
}

func (a *App) stopChannelMetaSync() error {
	if !a.channelMetaOn.Swap(false) {
		return nil
	}
	if a.stopChannelMetaSyncFn != nil {
		return a.stopChannelMetaSyncFn()
	}

	var err error
	if a.channelMetaSync != nil {
		err = errors.Join(err, a.channelMetaSync.StopWithoutCleanup())
	}
	return err
}

func (a *App) stopChannelPlane(ctx context.Context) error {
	if !a.channelPlaneOn.Swap(false) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if a.stopChannelPlaneFn != nil {
		return a.stopChannelPlaneFn(ctx)
	}
	if a.channelPlane == nil {
		return nil
	}
	return a.channelPlane.Stop(ctx)
}

func (a *App) stopAPI(ctx context.Context) error {
	if !a.apiOn.Swap(false) {
		return nil
	}
	if a.stopAPIWithContextFn != nil {
		return a.stopAPIWithContextFn(ctx)
	}
	if a.stopAPIFn != nil {
		return a.stopAPIFn()
	}
	if a.api == nil {
		return nil
	}
	return a.api.Stop(ctx)
}

func (a *App) stopManager(ctx context.Context) error {
	if !a.managerOn.Swap(false) {
		return nil
	}
	if a.managementApp != nil {
		a.managementApp.Stop()
	}
	if a.stopManagerWithContextFn != nil {
		return a.stopManagerWithContextFn(ctx)
	}
	if a.stopManagerFn != nil {
		return a.stopManagerFn()
	}
	if a.manager == nil {
		return nil
	}
	return a.manager.Stop(ctx)
}

func (a *App) stopCluster() {
	if !a.clusterOn.Swap(false) {
		return
	}
	if a.stopClusterFn != nil {
		a.stopClusterFn()
		return
	}
	if a.cluster == nil {
		return
	}
	a.cluster.Stop()
}

func (a *App) stopClusterWithError() error {
	a.stopCluster()
	return nil
}

func (a *App) syncLogger() error {
	if a == nil || a.logger == nil {
		return nil
	}
	return a.logger.Sync()
}

func (a *App) waitForManagedSlotsReady() error {
	if a == nil || a.cluster == nil {
		return nil
	}
	timeout := a.cfg.Cluster.ManagedSlotsReadyTimeout
	if timeout <= 0 {
		timeout = defaultManagedSlotsReadyTimeout
	}
	readyCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return a.cluster.WaitForManagedSlotsReady(readyCtx)
}

func (a *App) closeRaftDB() error {
	if a.closeRaftDBFn != nil {
		return a.closeRaftDBFn()
	}
	if a.raftDB == nil {
		return nil
	}
	return a.raftDB.Close()
}

func (a *App) closeChannelLogDB() error {
	if a.closeChannelLogDBFn != nil {
		return a.closeChannelLogDBFn()
	}
	var err error
	if a.channelLog != nil {
		err = errors.Join(err, a.channelLog.Close())
	}
	if a.replicaExecutionPool != nil {
		err = errors.Join(err, a.replicaExecutionPool.Close())
		a.replicaExecutionPool = nil
	}
	if a.dataPlaneClient != nil {
		a.dataPlaneClient.Stop()
		a.dataPlaneClient = nil
	}
	if a.dataPlanePool != nil {
		a.dataPlanePool.Close()
		a.dataPlanePool = nil
	}
	if a.channelLogDB != nil {
		err = errors.Join(err, a.channelLogDB.Close())
	}
	return err
}

func (a *App) closeWKDB() error {
	if a.closeWKDBFn != nil {
		return a.closeWKDBFn()
	}
	if a.db == nil {
		return nil
	}
	return a.db.Close()
}
