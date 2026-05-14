package app

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internal/access/gateway"
	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	applifecycle "github.com/WuKongIM/WuKongIM/internal/app/lifecycle"
	"github.com/WuKongIM/WuKongIM/internal/gateway"
	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	channelmigrationruntime "github.com/WuKongIM/WuKongIM/internal/runtime/channelmigration"
	appretention "github.com/WuKongIM/WuKongIM/internal/runtime/channelretention"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type App struct {
	cfg       Config
	logger    wklog.Logger
	createdAt time.Time

	db                        *metadb.DB
	raftDB                    *raftstorage.DB
	channelLogDB              *channelstore.Engine
	cluster                   raftcluster.API
	isrRuntime                channelruntime.Runtime
	channelLog                *appChannelCluster
	channelMetaSync           *channelMetaSync
	store                     *metastore.Store
	presenceApp               *presence.App
	channelApp                *channelusecase.App
	userApp                   *userusecase.App
	deliveryApp               *deliveryusecase.App
	conversationApp           *conversationusecase.App
	deliveryRuntime           *deliveryruntime.Manager
	deliveryRuntimeLifecycle  *deliveryRuntimeLifecycle
	deliveryAcks              *deliveryruntime.AckIndex
	cmdSyncApp                *cmdsync.App
	cmdConversationUpdater    *cmdsync.ConversationUpdater
	cmdConversationIntents    cmdConversationIntentRouter
	committedDispatcher       *asyncCommittedDispatcher
	committedReplayer         *committedReplayer
	channelMigrationExecutor  *channelmigrationruntime.Executor
	channelMigrationLifecycle *channelMigrationLifecycle
	channelRetentionWorker    *appretention.Worker
	messageApp                *message.App
	managementApp             *managementusecase.App
	conversationActiveHints   *conversationusecase.ActiveHintCache
	conversationProjector     conversationusecase.Projector
	api                       *accessapi.Server
	manager                   *accessmanager.Server
	nodeClient                *accessnode.Client
	nodeAccess                *accessnode.Adapter
	presenceWorker            *presenceWorker
	gatewayHandler            *accessgateway.Handler
	gateway                   *gateway.Gateway
	gatewayBootID             uint64
	onlineRegistry            online.Registry

	isrTransport         *channeltransport.Transport
	dataPlanePool        *transport.Pool
	dataPlaneClient      *transport.Client
	metrics              *obsmetrics.Registry
	diagnostics          *obsdiagnostics.Store
	diagnosticsRestore   func()
	networkObservability *networkObservability
	observedClusterCache observedClusterStateCache
	nodeDrainState       *nodeDrainState

	stopOnce                 sync.Once
	lifecycle                sync.Mutex
	lifecycleMgr             *applifecycle.Manager
	started                  atomic.Bool
	stopped                  atomic.Bool
	clusterOn                atomic.Bool
	channelMetaOn            atomic.Bool
	presenceOn               atomic.Bool
	conversationHintsOn      atomic.Bool
	conversationOn           atomic.Bool
	cmdConversationUpdaterOn atomic.Bool
	deliveryRuntimeOn        atomic.Bool
	committedDispatcherOn    atomic.Bool
	committedReplayOn        atomic.Bool
	channelMigrationOn       atomic.Bool
	channelRetentionOn       atomic.Bool
	apiOn                    atomic.Bool
	managerOn                atomic.Bool
	gatewayOn                atomic.Bool

	startClusterFn                 func() error
	startChannelMetaSyncFn         func() error
	startPresenceFn                func() error
	startConversationActiveHintsFn func() error
	startConversationProjectorFn   func() error
	startCMDConversationUpdaterFn  func() error
	startDeliveryRuntimeFn         func() error
	startCommittedDispatcherFn     func() error
	startCommittedReplayFn         func(context.Context) error
	startChannelMigrationFn        func(context.Context) error
	startChannelRetentionFn        func(context.Context) error
	startAPIFn                     func() error
	startManagerFn                 func() error
	startGatewayFn                 func() error
	stopAPIWithContextFn           func(context.Context) error
	stopManagerWithContextFn       func(context.Context) error
	stopAPIFn                      func() error
	stopManagerFn                  func() error
	stopGatewayFn                  func() error
	stopConversationProjectorFn    func() error
	stopCMDConversationUpdaterFn   func(context.Context) error
	stopConversationActiveHintsFn  func(context.Context) error
	stopDeliveryRuntimeFn          func() error
	stopCommittedDispatcherFn      func(context.Context) error
	stopCommittedReplayFn          func(context.Context) error
	stopChannelMigrationFn         func(context.Context) error
	stopChannelRetentionFn         func(context.Context) error
	stopPresenceFn                 func() error
	stopChannelMetaSyncFn          func() error
	stopClusterFn                  func()
	closeChannelLogDBFn            func() error
	closeRaftDBFn                  func() error
	closeWKDBFn                    func() error
}

func New(cfg Config) (*App, error) {
	return build(cfg)
}

func (a *App) DB() *metadb.DB {
	if a == nil {
		return nil
	}
	return a.db
}

func (a *App) RaftDB() *raftstorage.DB {
	if a == nil {
		return nil
	}
	return a.raftDB
}

func (a *App) Cluster() raftcluster.API {
	if a == nil {
		return nil
	}
	return a.cluster
}

func (a *App) ChannelLogDB() *channelstore.Engine {
	if a == nil {
		return nil
	}
	return a.channelLogDB
}

func (a *App) ISRRuntime() channelruntime.Runtime {
	if a == nil {
		return nil
	}
	return a.isrRuntime
}

func (a *App) ChannelLog() channel.Cluster {
	if a == nil {
		return nil
	}
	return a.channelLog
}

func (a *App) Store() *metastore.Store {
	if a == nil {
		return nil
	}
	return a.store
}

func (a *App) Message() *message.App {
	if a == nil {
		return nil
	}
	return a.messageApp
}

func (a *App) GatewayHandler() *accessgateway.Handler {
	if a == nil {
		return nil
	}
	return a.gatewayHandler
}

func (a *App) Conversation() *conversationusecase.App {
	if a == nil {
		return nil
	}
	return a.conversationApp
}

func (a *App) ConversationProjector() conversationusecase.Projector {
	if a == nil {
		return nil
	}
	return a.conversationProjector
}

func (a *App) API() *accessapi.Server {
	if a == nil {
		return nil
	}
	return a.api
}

func (a *App) Manager() *accessmanager.Server {
	if a == nil {
		return nil
	}
	return a.manager
}

func (a *App) Gateway() *gateway.Gateway {
	if a == nil {
		return nil
	}
	return a.gateway
}
