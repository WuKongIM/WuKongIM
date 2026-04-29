package app

import (
	"context"

	applifecycle "github.com/WuKongIM/WuKongIM/internal/app/lifecycle"
)

const (
	appLifecycleCluster               = "cluster"
	appLifecycleManagedSlotsReady     = "managed_slots_ready"
	appLifecycleChannelMeta           = "channelmeta"
	appLifecyclePresence              = "presence"
	appLifecycleConversationProjector = "conversation_projector"
	appLifecycleCommittedReplay       = "committed_replay"
	appLifecycleGateway               = "gateway"
	appLifecycleAPI                   = "api"
	appLifecycleManager               = "manager"
)

type appLifecycleComponent struct {
	name  string
	start func(context.Context) error
	stop  func(context.Context) error
}

func (c appLifecycleComponent) Name() string { return c.name }

func (c appLifecycleComponent) Start(ctx context.Context) error {
	if c.start == nil {
		return nil
	}
	return c.start(ctx)
}

func (c appLifecycleComponent) Stop(ctx context.Context) error {
	if c.stop == nil {
		return nil
	}
	return c.stop(ctx)
}

func (a *App) startLifecycleComponents() []applifecycle.Component {
	return a.lifecycleComponents(false)
}

func (a *App) stopLifecycleComponents() []applifecycle.Component {
	return a.lifecycleComponents(true)
}

func (a *App) lifecycleComponents(includeStopOnly bool) []applifecycle.Component {
	components := []applifecycle.Component{
		a.clusterLifecycleComponent(),
		a.managedSlotsReadyLifecycleComponent(),
	}

	if a.hasChannelMetaLifecycle(includeStopOnly) {
		components = append(components, a.channelMetaLifecycleComponent())
	}
	if a.hasPresenceLifecycle(includeStopOnly) {
		components = append(components, a.presenceLifecycleComponent())
	}
	if a.hasConversationProjectorLifecycle(includeStopOnly) {
		components = append(components, a.conversationProjectorLifecycleComponent())
	}
	if a.hasCommittedReplayLifecycle(includeStopOnly) {
		components = append(components, a.committedReplayLifecycleComponent())
	}

	components = append(components, a.gatewayLifecycleComponent())

	if a.hasAPILifecycle(includeStopOnly) {
		components = append(components, a.apiLifecycleComponent())
	}
	if a.hasManagerLifecycle(includeStopOnly) {
		components = append(components, a.managerLifecycleComponent())
	}
	return components
}

func (a *App) clusterLifecycleComponent() applifecycle.Component {
	return appLifecycleComponent{
		name: appLifecycleCluster,
		start: func(context.Context) error {
			if err := a.startCluster(); err != nil {
				return err
			}
			a.clusterOn.Store(true)
			return nil
		},
		stop: func(context.Context) error {
			return a.stopClusterWithError()
		},
	}
}

func (a *App) managedSlotsReadyLifecycleComponent() applifecycle.Component {
	return appLifecycleComponent{
		name: appLifecycleManagedSlotsReady,
		start: func(context.Context) error {
			return a.waitForManagedSlotsReady()
		},
	}
}

func (a *App) channelMetaLifecycleComponent() applifecycle.Component {
	return appLifecycleComponent{
		name: appLifecycleChannelMeta,
		start: func(context.Context) error {
			if err := a.startChannelMetaSync(); err != nil {
				return err
			}
			a.channelMetaOn.Store(true)
			return nil
		},
		stop: func(context.Context) error {
			return a.stopChannelMetaSync()
		},
	}
}

func (a *App) presenceLifecycleComponent() applifecycle.Component {
	return appLifecycleComponent{
		name: appLifecyclePresence,
		start: func(context.Context) error {
			if err := a.startPresence(); err != nil {
				return err
			}
			a.presenceOn.Store(true)
			return nil
		},
		stop: func(context.Context) error {
			return a.stopPresence()
		},
	}
}

func (a *App) conversationProjectorLifecycleComponent() applifecycle.Component {
	return appLifecycleComponent{
		name: appLifecycleConversationProjector,
		start: func(context.Context) error {
			if err := a.startConversationProjector(); err != nil {
				return err
			}
			a.conversationOn.Store(true)
			return nil
		},
		stop: func(context.Context) error {
			return a.stopConversationProjector()
		},
	}
}

func (a *App) committedReplayLifecycleComponent() applifecycle.Component {
	return appLifecycleComponent{
		name: appLifecycleCommittedReplay,
		start: func(ctx context.Context) error {
			if err := a.startCommittedReplay(ctx); err != nil {
				return err
			}
			a.committedReplayOn.Store(true)
			return nil
		},
		stop: func(context.Context) error {
			return a.stopCommittedReplay()
		},
	}
}

func (a *App) gatewayLifecycleComponent() applifecycle.Component {
	return appLifecycleComponent{
		name: appLifecycleGateway,
		start: func(context.Context) error {
			if err := a.startGateway(); err != nil {
				return err
			}
			a.gatewayOn.Store(true)
			return nil
		},
		stop: func(context.Context) error {
			return a.stopGateway()
		},
	}
}

func (a *App) apiLifecycleComponent() applifecycle.Component {
	return appLifecycleComponent{
		name: appLifecycleAPI,
		start: func(context.Context) error {
			if err := a.startAPI(); err != nil {
				return err
			}
			a.apiOn.Store(true)
			return nil
		},
		stop: func(ctx context.Context) error {
			return a.stopAPI(ctx)
		},
	}
}

func (a *App) managerLifecycleComponent() applifecycle.Component {
	return appLifecycleComponent{
		name: appLifecycleManager,
		start: func(context.Context) error {
			if err := a.startManager(); err != nil {
				return err
			}
			a.managerOn.Store(true)
			return nil
		},
		stop: func(ctx context.Context) error {
			return a.stopManager(ctx)
		},
	}
}

func (a *App) hasChannelMetaLifecycle(includeStopOnly bool) bool {
	return a.channelMetaSync != nil ||
		a.startChannelMetaSyncFn != nil ||
		(includeStopOnly && (a.stopChannelMetaSyncFn != nil || a.channelMetaOn.Load()))
}

func (a *App) hasPresenceLifecycle(includeStopOnly bool) bool {
	return a.presenceWorker != nil ||
		a.startPresenceFn != nil ||
		(includeStopOnly && (a.stopPresenceFn != nil || a.presenceOn.Load()))
}

func (a *App) hasConversationProjectorLifecycle(includeStopOnly bool) bool {
	return a.conversationProjector != nil ||
		a.startConversationProjectorFn != nil ||
		(includeStopOnly && (a.stopConversationProjectorFn != nil || a.conversationOn.Load()))
}

func (a *App) hasCommittedReplayLifecycle(includeStopOnly bool) bool {
	return a.committedReplayer != nil ||
		(includeStopOnly && a.committedReplayOn.Load())
}

func (a *App) hasAPILifecycle(includeStopOnly bool) bool {
	return a.api != nil ||
		a.startAPIFn != nil ||
		(includeStopOnly && (a.stopAPIFn != nil || a.stopAPIWithContextFn != nil || a.apiOn.Load()))
}

func (a *App) hasManagerLifecycle(includeStopOnly bool) bool {
	return a.manager != nil ||
		a.startManagerFn != nil ||
		(includeStopOnly && (a.stopManagerFn != nil || a.stopManagerWithContextFn != nil || a.managerOn.Load()))
}
