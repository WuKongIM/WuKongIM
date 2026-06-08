package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	applog "github.com/WuKongIM/WuKongIM/internalv2/log"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
	userusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ClusterRuntime is the cluster lifecycle surface used by the app root.
type ClusterRuntime interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// GatewayRuntime is the gateway lifecycle surface used by the app root.
type GatewayRuntime interface {
	Start() error
	Stop() error
}

// APIRuntime is the HTTP API lifecycle surface used by the app root.
type APIRuntime interface {
	Start() error
	Stop(context.Context) error
}

// WorkerRuntime is a background app worker managed inside the lifecycle.
type WorkerRuntime interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// Option customizes App construction.
type Option func(*App)

// App is the internalv2 composition root for cluster, message, and gateway runtimes.
type App struct {
	cfg                         Config
	cluster                     ClusterRuntime
	api                         APIRuntime
	gateway                     GatewayRuntime
	handler                     *accessgateway.Handler
	messages                    *message.App
	senderMessages              accessgateway.MessageUsecase
	apiMessages                 accessapi.MessageUsecase
	channels                    *channelusecase.App
	conversations               *conversationusecase.App
	users                       *userusecase.App
	delivery                    *deliveryusecase.App
	deliveryManager             *runtimedelivery.Manager
	deliveryRetry               *runtimedelivery.RetryScheduler
	deliveryWorker              WorkerRuntime
	recipientWorker             *recipientCommittedWorker
	conversationRouteLifecycle  WorkerRuntime
	conversationAuthority       *conversationAuthority
	conversationAuthorityClient *clusterinfra.ConversationAuthorityClient
	recipientAuthorityClient    *clusterinfra.RecipientAuthorityClient
	// deliverySubscribers scans durable non-person channel subscribers when provided.
	deliverySubscribers runtimedelivery.ChannelSubscriberSource
	deliveryMeta        *deliveryMetaStore
	presence            *presence.App
	online              *online.Registry
	presenceDirectory   *authoritypresence.Directory
	presenceWorker      WorkerRuntime
	metrics             *obsmetrics.Registry
	// diagnostics stores sampled send-path trace events for app-local queries.
	diagnostics *obsdiagnostics.Store
	// diagnosticsTracking stores dynamic diagnostics sampling rules.
	diagnosticsTracking *obsdiagnostics.TrackingRules
	// diagnosticsRestore restores the process-wide sendtrace sink installed by this app.
	diagnosticsRestore func()
	logger             wklog.Logger

	lifecycleMu              sync.Mutex
	started                  bool
	stopped                  bool
	clusterStarted           bool
	presenceStarted          bool
	conversationRouteStarted bool
	recipientStarted         bool
	deliveryStarted          bool
	apiStarted               bool
	gatewayStarted           bool
	deliveryErrors           atomic.Uint64
}

// New creates an internalv2 App.
func New(cfg Config, opts ...Option) (*App, error) {
	app := &App{cfg: cfg}
	constructionOK := false
	defer func() {
		if !constructionOK {
			app.restoreDiagnosticsSink()
		}
	}()
	app.cfg.Presence = defaultPresenceConfig(app.cfg.Presence)
	if err := validatePresenceConfig(app.cfg.Presence); err != nil {
		return nil, err
	}
	app.cfg.Conversation = defaultConversationConfig(app.cfg.Conversation)
	if err := validateConversationConfig(app.cfg.Conversation); err != nil {
		return nil, err
	}
	app.cfg.Delivery = defaultDeliveryConfig(app.cfg.Delivery)
	if err := validateDeliveryConfig(app.cfg.Delivery); err != nil {
		return nil, err
	}
	app.cfg.Observability = defaultObservabilityConfig(app.cfg.Observability)
	if err := validateObservabilityConfig(app.cfg.Observability); err != nil {
		return nil, err
	}
	app.cfg.Log = defaultLogConfig(app.cfg.Log)
	for _, opt := range opts {
		if opt != nil {
			opt(app)
		}
	}
	cfg = app.cfg
	if app.logger == nil {
		logger, err := applog.NewLogger(applog.Config{
			Level:      cfg.Log.Level,
			Dir:        cfg.Log.Dir,
			MaxSize:    cfg.Log.MaxSize,
			MaxAge:     cfg.Log.MaxAge,
			MaxBackups: cfg.Log.MaxBackups,
			Compress:   cfg.Log.Compress,
			Console:    cfg.Log.Console,
			Format:     cfg.Log.Format,
		})
		if err != nil {
			return nil, fmt.Errorf("internalv2/app: create logger: %w", err)
		}
		app.logger = logger
	}
	clusterCfg := defaultClusterConfig(cfg)
	if cfg.Observability.MetricsEnabled {
		app.metrics = obsmetrics.New(clusterCfg.NodeID, fmt.Sprintf("node-%d", clusterCfg.NodeID))
		clusterCfg.Channel.Observer = combineChannelV2Observers(clusterCfg.Channel.Observer, channelV2MetricsObserver{metrics: app.metrics})
		clusterCfg.Slots.Observer = combineSlotObservers(clusterCfg.Slots.Observer, slotMetricsObserver{metrics: app.metrics})
		clusterCfg.Control.RaftObserver = combineControllerRaftObservers(clusterCfg.Control.RaftObserver, controllerRaftMetricsObserver{metrics: app.metrics})
		clusterCfg.Storage.CommitObserver = combineCommitCoordinatorObservers(clusterCfg.Storage.CommitObserver, storageCommitMetricsObserver{
			metrics: app.metrics,
			workers: commitCoordinatorWorkerCount(clusterCfg.Storage.CommitShards),
		})
		clusterCfg.Transport.Observer = combineTransportV2Observers(clusterCfg.Transport.Observer, &transportV2MetricsObserver{metrics: app.metrics})
	}
	if cfg.Observability.Diagnostics.Enabled {
		app.diagnostics = obsdiagnostics.NewStore(diagnosticsStoreOptions(cfg))
		app.diagnosticsTracking = obsdiagnostics.NewTrackingRules(obsdiagnostics.TrackingRulesOptions{})
		samplerOptions := diagnosticsSamplerOptions(cfg)
		samplerOptions.TrackingRules = app.diagnosticsTracking
		sink := obsdiagnostics.NewSendTraceSink(app.diagnostics, obsdiagnostics.NewSampler(samplerOptions))
		if app.metrics != nil {
			sink = sink.WithMetrics(app.metrics.Diagnostics)
		}
		app.diagnosticsRestore = sendtrace.SetSink(sink)
	}
	if app.cluster == nil {
		node, err := clusterv2.New(clusterCfg)
		if err != nil {
			return nil, err
		}
		app.cluster = node
	}
	if app.online == nil {
		app.online = online.NewRegistry(online.RegistryOptions{})
	}
	if app.deliveryMeta == nil {
		if node, ok := app.cluster.(deliveryMetaNode); ok {
			app.deliveryMeta = newDeliveryMetaStore(node)
		}
	}
	if app.cfg.Delivery.Enabled && app.deliverySubscribers == nil {
		app.deliverySubscribers = app.deliveryMeta
	}
	if app.channels == nil {
		if node, ok := app.cluster.(clusterinfra.ChannelMetadataNode); ok {
			store := clusterinfra.NewChannelMetadataStore(node)
			channelOptions := channelusecase.Options{
				Store: store,
			}
			if _, ok := node.(clusterinfra.ChannelMembershipNode); ok {
				channelOptions.MembershipIndex = store
			}
			app.channels = channelusecase.New(channelOptions)
		}
	}
	var conversationReadStore *clusterinfra.ConversationStore
	if node, ok := app.cluster.(clusterinfra.ConversationNode); ok {
		conversationReadStore = clusterinfra.NewConversationStore(node, clusterinfra.ConversationStoreOptions{
			MaxLastMessageConcurrency: app.cfg.Conversation.MaxLastMessageConcurrency,
		})
	}
	if app.conversationAuthorityClient == nil {
		authorityNode, hasAuthorityNode := app.cluster.(clusterinfra.ConversationAuthorityNode)
		authorityStore, hasAuthorityStore := app.cluster.(conversationAuthorityStore)
		if hasAuthorityNode && hasAuthorityStore {
			authority := newConversationAuthority(conversationAuthorityOptions{
				LocalNodeID:          authorityNode.NodeID(),
				Store:                authorityStore,
				MaxRowsPerUID:        app.cfg.Conversation.AuthorityCacheMaxRowsPerUID,
				MaxRows:              app.cfg.Conversation.AuthorityCacheMaxRows,
				ListDBWindowMax:      app.cfg.Conversation.AuthorityListDBWindowMax,
				AdmissionBatchRows:   app.cfg.Conversation.ProjectionAdmitBatchRows,
				AdmissionConcurrency: app.cfg.Conversation.ProjectionAdmitConcurrency,
				HandoffTimeout:       app.cfg.Conversation.AuthorityHandoffTimeout,
				Observer:             app.conversationAuthorityObserver(),
			})
			client := clusterinfra.NewConversationAuthorityClient(authorityNode, authority)
			app.conversationAuthority = authority
			app.conversationAuthorityClient = client
			if app.conversationRouteLifecycle == nil {
				routeLifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
					LocalAuthority: authority,
					LocalNodeID:    authorityNode.NodeID(),
					Initial:        app.currentPresenceAuthorities,
					Watch:          authorityNode.WatchRouteAuthorities,
					HandoffTimeout: app.cfg.Conversation.AuthorityHandoffTimeout,
				})
				routeLifecycle.applyRouteAuthorities(context.Background(), app.currentPresenceAuthorities())
				app.conversationRouteLifecycle = routeLifecycle
			}
			adapter := accessnode.New(accessnode.Options{ConversationAuthority: authority, Logger: app.logger.Named("node")})
			authorityNode.RegisterRPC(accessnode.ConversationAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandleConversationAuthorityRPC))
		}
	}
	if app.conversations == nil {
		if conversationReadStore != nil {
			var store conversationusecase.Store = conversationReadStore
			if app.conversationAuthorityClient != nil {
				store = app.conversationAuthorityClient
			}
			app.conversations = conversationusecase.New(conversationusecase.Options{
				Store:    store,
				Messages: conversationReadStore,
			})
		}
	}
	if presenceNode, ok := app.cluster.(clusterinfra.PresenceNode); ok {
		directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{LocalNodeID: presenceNode.NodeID()})
		app.presenceDirectory = directory
		authority := presenceDirectoryAuthority{directory: directory}
		ownerActions := presenceOwnerActions{local: app.online}
		client := clusterinfra.NewPresenceAuthorityClient(presenceNode, authority)
		client.SetLocalOwner(ownerActions)
		adapter := accessnode.New(accessnode.Options{Authority: authority, Owner: ownerActions, Logger: app.logger.Named("node")})
		presenceNode.RegisterRPC(accessnode.PresenceAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandlePresenceAuthorityRPC))
		presenceNode.RegisterRPC(accessnode.PresenceOwnerRPCServiceID, nodeRPCHandlerFunc(adapter.HandlePresenceOwnerRPC))
		if app.presence == nil {
			ownerBootID := newOwnerBootID()
			app.presence = presence.New(presence.Options{
				Local:        app.online,
				Authority:    client,
				OwnerActions: client,
				OwnerNodeID:  presenceNode.NodeID(),
				OwnerBootID:  ownerBootID,
				HashSlot: func(uid string) (uint16, error) {
					target, err := client.ResolveRouteTarget(uid)
					if err != nil {
						return 0, err
					}
					return target.HashSlot, nil
				},
			})
		}
		if app.presenceWorker == nil {
			app.presenceWorker = newPresenceTouchWorker(presenceTouchWorkerOptions{
				NodeID:        presenceNode.NodeID(),
				Watch:         presenceNode.WatchRouteAuthorities,
				Initial:       app.currentPresenceAuthorities,
				Local:         app.online,
				Authority:     client,
				Directory:     directory,
				FlushInterval: app.cfg.Presence.TouchFlushInterval,
				BatchSize:     app.cfg.Presence.TouchBatchSize,
				RouteTTL:      app.cfg.Presence.RouteTTL,
				Logger:        app.logger.Named("presence_touch"),
			})
		}
	}
	if app.users == nil {
		if node, ok := app.cluster.(clusterinfra.UserMetadataNode); ok {
			userStore := clusterinfra.NewUserMetadataStore(node)
			var systemUIDs userusecase.SystemUIDStore
			if channelNode, ok := app.cluster.(clusterinfra.ChannelMetadataNode); ok {
				systemUIDs = clusterinfra.NewChannelMetadataStore(channelNode)
			}
			app.users = userusecase.New(userusecase.Options{
				Users:        userStore,
				Devices:      userStore,
				DeviceReader: userStore,
				Online:       app.online,
				Presence:     app.presence,
				SystemUIDs:   systemUIDs,
				Logger:       app.logger.Named("usecase.user"),
			})
		}
	}
	if app.cfg.Delivery.Enabled && app.delivery == nil {
		localPusher := &localOwnerPusher{online: app.online, pendingAckTTL: app.cfg.Delivery.PendingAckTTL, logger: app.logger.Named("delivery.owner")}
		deliveryObserver := app.deliveryObserver()
		var push runtimedelivery.Pusher = localPusher
		var fanoutRemote runtimedelivery.FanoutTaskForwarder
		var localNodeID uint64
		if presenceNode, ok := app.cluster.(clusterinfra.PresenceNode); ok {
			localNodeID = presenceNode.NodeID()
			nodeClient := accessnode.NewClient(presenceNode)
			push = clusterinfra.NewDeliveryPusher(localNodeID, localPusher, nodeClient)
			fanoutRemote = nodeClient
		}
		var partitioner runtimedelivery.Partitioner
		if routes, ok := app.cluster.(clusterWriteReadyRuntime); ok {
			partitioner = clusterinfra.NewDeliveryPartitioner(routes)
		}
		fanoutWorker := runtimedelivery.NewFanoutWorker(runtimedelivery.FanoutWorkerOptions{
			Subscribers: appSubscriberPlanner{
				channel: runtimedelivery.NewChannelSubscriberPlanner(runtimedelivery.ChannelSubscriberPlannerOptions{
					Source: app.deliverySubscribers,
				}),
			},
			Presence:      presenceResolverAdapter{presence: app.presence},
			Push:          push,
			PageSize:      app.cfg.Delivery.FanoutPageSize,
			PushBatchSize: app.cfg.Delivery.PushBatchSize,
			Observer:      deliveryObserver,
		})
		var fanoutRunner runtimedelivery.FanoutTaskRunner = fanoutWorker
		if localNodeID != 0 {
			fanoutRunner = runtimedelivery.NewFanoutTaskRouter(runtimedelivery.FanoutTaskRouterOptions{
				LocalNodeID: localNodeID,
				Local:       fanoutWorker,
				Remote:      fanoutRemote,
				Observer:    deliveryObserver,
			})
		}
		var retryObserver runtimedelivery.RetryObserver
		if observer, ok := deliveryObserver.(runtimedelivery.RetryObserver); ok {
			retryObserver = observer
		}
		retryScheduler := runtimedelivery.NewRetryScheduler(runtimedelivery.RetrySchedulerOptions{
			Runner:      fanoutRunner,
			Capacity:    app.cfg.Delivery.EventQueueSize,
			MaxAttempts: defaultDeliveryRetryMaxAttempts,
			Backoff:     defaultDeliveryRetryBackoff,
			Observer:    retryObserver,
		})
		var managerObserver runtimedelivery.ManagerObserver
		if observer, ok := deliveryObserver.(runtimedelivery.ManagerObserver); ok {
			managerObserver = observer
		}
		manager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{
			Planner:         runtimedelivery.NewPlanner(runtimedelivery.PlannerOptions{Partitioner: partitioner}),
			Runner:          retryScheduler,
			AsyncQueueSize:  app.cfg.Delivery.EventQueueSize,
			AsyncWorkers:    1,
			ManagerObserver: managerObserver,
			Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
				MaxPendingPerSession: app.cfg.Delivery.PendingAckMaxPerSession,
			}),
		})
		localPusher.delivery = manager
		app.deliveryManager = manager
		app.deliveryRetry = retryScheduler
		app.delivery = deliveryusecase.New(deliveryusecase.Options{Runtime: deliveryRuntimeAdapter{manager: manager}})
		if app.deliveryWorker == nil {
			app.deliveryWorker = deliveryWorkerGroup{retryScheduler, manager}
		}
		if presenceNode, ok := app.cluster.(clusterinfra.PresenceNode); ok {
			adapter := accessnode.New(accessnode.Options{Delivery: localPusher, DeliveryFanout: fanoutWorker, Logger: app.logger.Named("node")})
			presenceNode.RegisterRPC(accessnode.DeliveryPushRPCServiceID, nodeRPCHandlerFunc(adapter.HandleDeliveryPushRPC))
			presenceNode.RegisterRPC(accessnode.DeliveryFanoutRPCServiceID, nodeRPCHandlerFunc(adapter.HandleDeliveryFanoutRPC))
		}
	}
	if app.recipientWorker == nil {
		if authorityNode, ok := app.cluster.(clusterinfra.RecipientAuthorityNode); ok && app.conversationAuthorityClient != nil {
			recipientAuthority := clusterinfra.NewRecipientAuthorityClient(authorityNode, nil)
			app.recipientAuthorityClient = recipientAuthority
			var deliverySubmitter recipientusecase.DeliverySubmitter
			if app.delivery != nil {
				deliverySubmitter = recipientDeliverySubmitter{delivery: app.delivery}
			}
			processor := recipientusecase.NewProcessor(recipientusecase.ProcessorOptions{
				LocalNodeID:  authorityNode.NodeID(),
				Authority:    recipientAuthority,
				Conversation: app.conversationAuthorityClient,
				Delivery:     deliverySubmitter,
			})
			var recipientSource recipientusecase.RecipientSource
			if app.deliveryMeta != nil {
				recipientSource = app.deliveryMeta
			} else if app.deliverySubscribers != nil {
				recipientSource = deliverySubscriberRecipientSource{source: app.deliverySubscribers}
			} else if subscriberNode, ok := app.cluster.(recipientSubscriberNode); ok {
				recipientSource = recipientSubscriberStore{node: subscriberNode}
			}
			dispatcher := recipientusecase.NewDispatcher(recipientusecase.DispatcherOptions{
				LocalNodeID:     authorityNode.NodeID(),
				Recipients:      recipientSource,
				Resolver:        recipientAuthority,
				Local:           processor,
				Remote:          recipientAuthority,
				PageSize:        app.cfg.Delivery.FanoutPageSize,
				TargetBatchSize: app.cfg.Delivery.PushBatchSize,
			})
			app.recipientWorker = newRecipientCommittedWorker(dispatcher, app.cfg.Delivery.EventQueueSize, app.delivery != nil, app.logger.Named("recipient"))
			if registrar, ok := app.cluster.(nodeRPCRegistrar); ok {
				adapter := accessnode.New(accessnode.Options{RecipientAuthority: processor, Logger: app.logger.Named("node")})
				registerNodeRPC(registrar, accessnode.RecipientAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandleRecipientAuthorityRPC))
			}
		}
	}
	if app.messages == nil {
		messageOpts := message.Options{MessageID: newNodeMessageIDs(clusterCfg.NodeID)}
		if appendNode, ok := app.cluster.(clusterinfra.ChannelAppendNode); ok {
			messageOpts.Appender = clusterinfra.NewChannelAppender(appendNode)
		}
		if readNode, ok := app.cluster.(clusterinfra.ChannelMessageReadNode); ok {
			messageOpts.MessageReader = clusterinfra.NewChannelMessageReader(readNode)
		}
		messageOpts.Committed = app.recipientWorker
		if app.cfg.Delivery.Enabled || app.metrics != nil {
			messageOpts.Observer = deliveryMessageObserver{app: app}
		}
		app.messages = message.New(messageOpts)
	}
	if app.senderMessages == nil {
		if senderNode, ok := app.cluster.(clusterinfra.SenderAuthorityNode); ok && app.messages != nil {
			senderAuthority := clusterinfra.NewSenderAuthorityClient(senderNode, nil)
			app.senderMessages = message.NewSenderAuthorityRouter(message.SenderAuthorityRouterOptions{
				LocalNodeID: senderNode.NodeID(),
				Resolver:    senderAuthority,
				Local:       app.messages,
				Remote:      senderAuthority,
			})
			var routes clusterWriteReadyRuntime
			if routeRuntime, ok := app.cluster.(clusterWriteReadyRuntime); ok {
				routes = routeRuntime
			}
			local := senderAuthorityLocal{
				localNodeID: senderNode.NodeID(),
				resolver:    senderAuthority,
				routes:      routes,
				submitter:   app.messages,
			}
			adapter := accessnode.New(accessnode.Options{SenderAuthority: local, Logger: app.logger.Named("node")})
			senderNode.RegisterRPC(accessnode.SenderAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandleSenderAuthorityRPC))
		}
	}
	if app.apiMessages == nil && app.messages != nil {
		sendUsecase := interface {
			Send(context.Context, message.SendCommand) (message.SendResult, error)
		}(app.messages)
		if app.senderMessages != nil {
			sendUsecase = app.senderMessages
		}
		app.apiMessages = authorityMessageUsecase{sender: sendUsecase, sync: app.messages}
	}
	if app.handler == nil {
		handlerMessages := accessgateway.MessageUsecase(app.messages)
		if app.senderMessages != nil {
			handlerMessages = app.senderMessages
		}
		app.handler = accessgateway.New(accessgateway.Options{
			Messages:        handlerMessages,
			Presence:        app.gatewayPresenceUsecase(),
			Delivery:        app.delivery,
			OwnerNodeID:     clusterCfg.NodeID,
			SendTimeout:     cfg.Gateway.SendTimeout,
			SendackObserver: app.sendackObserver(),
			Logger:          app.logger.Named("access.gateway"),
		})
	}
	if app.api == nil && strings.TrimSpace(cfg.API.ListenAddr) != "" {
		app.api = accessapi.New(accessapi.Options{
			ListenAddr:               cfg.API.ListenAddr,
			Readyz:                   app.readyzReport,
			BenchEnabled:             cfg.Bench.APIEnabled,
			BenchMaxBatchSize:        cfg.Bench.APIMaxBatchSize,
			BenchMaxPayloadBytes:     cfg.Bench.APIMaxPayloadBytes,
			Gateway:                  apiGatewayAddresses(cfg.API, cfg.Gateway.Listeners),
			BenchRuntime:             app.benchRuntimeController(),
			BenchPresence:            app.benchPresenceController(),
			BenchData:                app.deliveryMeta,
			Channels:                 app.channels,
			Users:                    app.users,
			Messages:                 app.apiMessages,
			Conversations:            app.conversations,
			ConversationListObserver: app.conversationListObserver(),
			MetricsHandler:           app.metricsHandler(),
			PProfEnabled:             cfg.Observability.PProfEnabled,
			Logger:                   app.logger.Named("access.api"),
		})
	}
	if app.gateway == nil && len(cfg.Gateway.Listeners) > 0 {
		gw, err := gateway.New(gateway.Options{
			Handler:        app.handler,
			Authenticator:  gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{NodeID: clusterCfg.NodeID}),
			Listeners:      cfg.Gateway.Listeners,
			DefaultSession: cfg.Gateway.Session,
			Transport:      cfg.Gateway.Transport,
			Observer:       app.gatewayObserver(),
			Logger:         app.logger.Named("gateway"),
		})
		if err != nil {
			return nil, err
		}
		app.gateway = gw
	}
	constructionOK = true
	return app, nil
}

func commitCoordinatorWorkerCount(shards int) int {
	if shards <= 1 {
		return dbMessageCommitWorkerCap
	}
	return shards
}

// WithCluster overrides the cluster runtime.
func WithCluster(cluster ClusterRuntime) Option {
	return func(a *App) { a.cluster = cluster }
}

// WithAPI overrides the HTTP API runtime.
func WithAPI(api APIRuntime) Option {
	return func(a *App) { a.api = api }
}

// WithGateway overrides the gateway runtime.
func WithGateway(gateway GatewayRuntime) Option {
	return func(a *App) { a.gateway = gateway }
}

// WithMessages overrides the message usecase app.
func WithMessages(messages *message.App) Option {
	return func(a *App) { a.messages = messages }
}

// WithChannels overrides the channel usecase app.
func WithChannels(channels *channelusecase.App) Option {
	return func(a *App) { a.channels = channels }
}

// WithConversations overrides the conversation usecase app.
func WithConversations(conversations *conversationusecase.App) Option {
	return func(a *App) { a.conversations = conversations }
}

// WithPresence overrides the presence usecase app.
func WithPresence(presence *presence.App) Option {
	return func(a *App) { a.presence = presence }
}

// WithOnlineRegistry overrides the owner-local online registry.
func WithOnlineRegistry(reg *online.Registry) Option {
	return func(a *App) { a.online = reg }
}

// WithDeliverySubscriberSource overrides the durable subscriber source used by delivery fanout.
func WithDeliverySubscriberSource(source runtimedelivery.ChannelSubscriberSource) Option {
	return func(a *App) { a.deliverySubscribers = source }
}

// WithLogger overrides the root logger used by the app.
func WithLogger(logger wklog.Logger) Option {
	return func(a *App) { a.logger = logger }
}

// Handler returns the gateway access handler.
func (a *App) Handler() *accessgateway.Handler {
	if a == nil {
		return nil
	}
	return a.handler
}

// Messages returns the message usecase app.
func (a *App) Messages() *message.App {
	if a == nil {
		return nil
	}
	return a.messages
}

// Conversations returns the conversation list usecase app.
func (a *App) Conversations() *conversationusecase.App {
	if a == nil {
		return nil
	}
	return a.conversations
}

// Delivery returns the delivery usecase app.
func (a *App) Delivery() *deliveryusecase.App {
	if a == nil {
		return nil
	}
	return a.delivery
}

func (a *App) metricsHandler() http.Handler {
	if a == nil || a.metrics == nil {
		return nil
	}
	return a.metrics.Handler()
}

func (a *App) conversationListObserver() accessapi.ConversationListObserver {
	if a == nil || a.metrics == nil {
		return nil
	}
	return conversationListMetricsObserver{metrics: a.metrics}
}

func (a *App) conversationAuthorityObserver() conversationAuthorityObserver {
	if a == nil || a.metrics == nil {
		return nil
	}
	return conversationAuthorityMetricsObserver{metrics: a.metrics}
}

func (a *App) conversationProjectionObserver() conversationProjectionObserver {
	if a == nil || a.metrics == nil {
		return nil
	}
	return conversationProjectionMetricsObserver{metrics: a.metrics}
}

func (a *App) gatewayObserver() gateway.Observer {
	if a == nil || a.metrics == nil {
		return nil
	}
	return gatewayMetricsObserver{metrics: a.metrics}
}

func (a *App) sendackObserver() accessgateway.SendackObserver {
	if a == nil || a.metrics == nil {
		return nil
	}
	return gatewayMetricsObserver{metrics: a.metrics}
}

func (a *App) benchRuntimeController() accessapi.ChannelRuntimeBenchController {
	if a == nil {
		return nil
	}
	node, ok := a.cluster.(clusterinfra.ChannelRuntimeBenchNode)
	if !ok {
		return nil
	}
	return clusterinfra.NewChannelRuntimeBenchController(node)
}

func (a *App) gatewayPresenceUsecase() accessgateway.PresenceUsecase {
	if a == nil || a.presence == nil {
		return nil
	}
	if a.cfg.Presence.ActivationTimeout <= 0 {
		return a.presence
	}
	return activationTimeoutPresence{
		next:    a.presence,
		timeout: a.cfg.Presence.ActivationTimeout,
	}
}

func (a *App) currentPresenceAuthorities() []clusterv2.RouteAuthority {
	routes, ok := a.cluster.(clusterWriteReadyRuntime)
	if !ok {
		return nil
	}
	snapshot := routes.Snapshot()
	if snapshot.HashSlotCount == 0 {
		return nil
	}
	authorities := make([]clusterv2.RouteAuthority, 0, snapshot.HashSlotCount)
	for hashSlot := uint16(0); hashSlot < snapshot.HashSlotCount; hashSlot++ {
		route, err := routes.RouteHashSlot(hashSlot)
		if err != nil {
			continue
		}
		authorities = append(authorities, clusterv2.RouteAuthority{
			HashSlot:       route.HashSlot,
			SlotID:         route.SlotID,
			LeaderNodeID:   route.Leader,
			RouteRevision:  route.Revision,
			AuthorityEpoch: route.AuthorityEpoch,
		})
	}
	return authorities
}

func defaultClusterConfig(cfg Config) clusterv2.Config {
	cluster := cfg.Cluster
	if cluster.NodeID == 0 {
		cluster.NodeID = cfg.NodeID
	}
	if cluster.DataDir == "" {
		cluster.DataDir = cfg.DataDir
	}
	return cluster
}

func apiGatewayAddresses(cfg APIConfig, listeners []gateway.ListenerOptions) accessapi.GatewayAddresses {
	addrs := gatewayAddressesFromListeners(listeners)
	if trimmed := strings.TrimSpace(cfg.ExternalTCPAddr); trimmed != "" {
		addrs.TCPAddr = trimmed
	}
	if trimmed := strings.TrimSpace(cfg.ExternalWSAddr); trimmed != "" {
		addrs.WSAddr = trimmed
	}
	if trimmed := strings.TrimSpace(cfg.ExternalWSSAddr); trimmed != "" {
		addrs.WSSAddr = trimmed
	}
	return addrs
}

func gatewayAddressesFromListeners(listeners []gateway.ListenerOptions) accessapi.GatewayAddresses {
	var out accessapi.GatewayAddresses
	for _, listener := range listeners {
		network := strings.ToLower(strings.TrimSpace(listener.Network))
		switch network {
		case "websocket":
			addr := normalizeWebsocketAddress(listener.Address)
			if strings.HasPrefix(strings.ToLower(addr), "wss://") {
				if out.WSSAddr == "" {
					out.WSSAddr = addr
				}
			} else if out.WSAddr == "" {
				out.WSAddr = addr
			}
		default:
			if out.TCPAddr == "" {
				out.TCPAddr = normalizeTCPAddress(listener.Address)
			}
		}
	}
	return out
}

func normalizeTCPAddress(addr string) string {
	trimmed := strings.TrimSpace(addr)
	return strings.TrimPrefix(trimmed, "tcp://")
}

func normalizeWebsocketAddress(addr string) string {
	trimmed := strings.TrimSpace(addr)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") || trimmed == "" {
		return trimmed
	}
	return "ws://" + trimmed
}

type nodeMessageIDs struct {
	next atomic.Uint64
}

func newNodeMessageIDs(nodeID uint64) *nodeMessageIDs {
	g := &nodeMessageIDs{}
	g.next.Store(nodeID << 48)
	return g
}

func (g *nodeMessageIDs) Next() uint64 {
	return g.next.Add(1)
}

type nodeRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f nodeRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

type presenceDirectoryAuthority struct {
	// directory stores authoritative virtual routes for locally led hash slots.
	directory *authoritypresence.Directory
}

type presenceOwnerActions struct {
	// local stores real sessions owned by this node.
	local presenceOwnerLocalRegistry
}

type activationTimeoutPresence struct {
	// next is the underlying entry-agnostic presence usecase.
	next accessgateway.PresenceUsecase
	// timeout bounds authority registration during gateway activation.
	timeout time.Duration
}

func (p activationTimeoutPresence) Activate(ctx context.Context, cmd presence.ActivateCommand) error {
	if p.next == nil {
		return nil
	}
	if p.timeout <= 0 {
		return p.next.Activate(ctx, cmd)
	}
	activateCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.next.Activate(activateCtx, cmd)
}

func (p activationTimeoutPresence) Deactivate(ctx context.Context, cmd presence.DeactivateCommand) error {
	if p.next == nil {
		return nil
	}
	return p.next.Deactivate(ctx, cmd)
}

func (p activationTimeoutPresence) Touch(ctx context.Context, cmd presence.TouchCommand) error {
	if p.next == nil {
		return nil
	}
	return p.next.Touch(ctx, cmd)
}

func (a presenceDirectoryAuthority) RegisterRoute(ctx context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	if err := ctx.Err(); err != nil {
		return presence.RegisterResult{}, err
	}
	return a.directory.RegisterRoute(target, route)
}

func (a presenceDirectoryAuthority) CommitRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.directory.CommitRoute(target, presence.PendingRouteToken(token))
}

func (a presenceDirectoryAuthority) AbortRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.directory.AbortRoute(target, presence.PendingRouteToken(token))
}

func (a presenceDirectoryAuthority) UnregisterRoute(ctx context.Context, target presence.RouteTarget, identity presence.RouteIdentity, ownerSeq uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.directory.UnregisterRoute(target, identity, ownerSeq)
}

func (a presenceDirectoryAuthority) EndpointsByUID(ctx context.Context, target presence.RouteTarget, uid string) ([]presence.Route, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return a.directory.EndpointsByUID(target, uid)
}

func (a presenceDirectoryAuthority) TouchRoutes(ctx context.Context, target presence.RouteTarget, routes []presence.Route) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.directory.TouchRoutes(target, routes)
}

func (a presenceOwnerActions) ApplyRouteAction(ctx context.Context, action presence.RouteAction) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.local == nil {
		return authoritypresence.ErrRouteNotReady
	}
	session, ok := a.local.LocalSession(action.SessionID)
	route := session.Route
	if !ok || route.UID != action.UID || route.OwnerNodeID != action.OwnerNodeID || route.OwnerBootID != action.OwnerBootID {
		return nil
	}
	if session.Session != nil {
		if err := session.Session.CloseSession(action.Reason); err != nil {
			return err
		}
	}
	a.local.MarkClosingAndUnregister(action.SessionID)
	return nil
}

func newOwnerBootID() uint64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		if id := binary.LittleEndian.Uint64(buf[:]); id != 0 {
			return id
		}
	}
	return uint64(time.Now().UnixNano())
}
