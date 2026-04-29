package app

import (
	"context"
	"errors"
	"time"

	applifecycle "github.com/WuKongIM/WuKongIM/internal/app/lifecycle"
)

const (
	apiStopTimeout              = 5 * time.Second
	defaultDataPlaneDialTimeout = 5 * time.Second
	managedSlotsReadyTimeout    = 10 * time.Second
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
	if err := manager.Start(context.Background()); err != nil {
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
		stopCtx, cancel := context.WithTimeout(context.Background(), apiStopTimeout)
		defer cancel()
		err = errors.Join(
			a.stopLifecycleManager(stopCtx),
			a.closeChannelLogDB(),
			a.closeRaftDB(),
			a.closeWKDB(),
			a.syncLogger(),
		)
	})
	return err
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

func (a *App) stopCommittedDispatcher() error {
	if !a.committedDispatcherOn.Swap(false) {
		return nil
	}
	if a.stopCommittedDispatcherFn != nil {
		return a.stopCommittedDispatcherFn()
	}
	if a.committedDispatcher == nil {
		return nil
	}
	return a.committedDispatcher.Stop()
}

func (a *App) stopCommittedReplay() error {
	if !a.committedReplayOn.Swap(false) {
		return nil
	}
	if a.stopCommittedReplayFn != nil {
		return a.stopCommittedReplayFn()
	}
	if a.committedReplayer == nil {
		return nil
	}
	return a.committedReplayer.Stop()
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
	readyCtx, cancel := context.WithTimeout(context.Background(), managedSlotsReadyTimeout)
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
