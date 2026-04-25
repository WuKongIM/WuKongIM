package app

import (
	"sync"
	"sync/atomic"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internal/access/gateway"
	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/gateway"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
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

	db                    *metadb.DB
	raftDB                *raftstorage.DB
	channelLogDB          *channelstore.Engine
	cluster               raftcluster.API
	isrRuntime            channelruntime.Runtime
	channelLog            *appChannelCluster
	channelMetaSync       *channelMetaSync
	store                 *metastore.Store
	presenceApp           *presence.App
	deliveryApp           *deliveryusecase.App
	conversationApp       *conversationusecase.App
	deliveryRuntime       *deliveryruntime.Manager
	deliveryAcks          *deliveryruntime.AckIndex
	messageApp            *message.App
	managementApp         *managementusecase.App
	conversationProjector conversationusecase.Projector
	api                   *accessapi.Server
	manager               *accessmanager.Server
	nodeClient            *accessnode.Client
	nodeAccess            *accessnode.Adapter
	presenceWorker        *presenceWorker
	gatewayHandler        *accessgateway.Handler
	gateway               *gateway.Gateway
	gatewayBootID         uint64

	isrTransport    *channeltransport.Transport
	dataPlanePool   *transport.Pool
	dataPlaneClient *transport.Client
	metrics         *obsmetrics.Registry

	stopOnce       sync.Once
	lifecycle      sync.Mutex
	started        atomic.Bool
	stopped        atomic.Bool
	clusterOn      atomic.Bool
	channelMetaOn  atomic.Bool
	presenceOn     atomic.Bool
	conversationOn atomic.Bool
	apiOn          atomic.Bool
	managerOn      atomic.Bool
	gatewayOn      atomic.Bool

	startClusterFn               func() error
	startChannelMetaSyncFn       func() error
	startPresenceFn              func() error
	startConversationProjectorFn func() error
	startAPIFn                   func() error
	startManagerFn               func() error
	startGatewayFn               func() error
	stopAPIFn                    func() error
	stopManagerFn                func() error
	stopGatewayFn                func() error
	stopConversationProjectorFn  func() error
	stopPresenceFn               func() error
	stopChannelMetaSyncFn        func() error
	stopClusterFn                func()
	closeChannelLogDBFn          func() error
	closeRaftDBFn                func() error
	closeWKDBFn                  func() error
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
