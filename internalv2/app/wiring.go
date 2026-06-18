package app

import (
	"context"
	"fmt"
	"strings"

	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	accessmanager "github.com/WuKongIM/WuKongIM/internalv2/access/manager"
	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	applog "github.com/WuKongIM/WuKongIM/internalv2/log"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/delivery"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	userusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

func (a *App) applyConfigDefaults() error {
	a.cfg.Manager = defaultManagerConfig(a.cfg.Manager)
	if err := validateManagerConfig(a.cfg.Manager); err != nil {
		return err
	}
	a.cfg.Message = defaultMessageConfig(a.cfg.Message)
	if err := validateMessageConfig(a.cfg.Message); err != nil {
		return err
	}
	a.cfg.Presence = defaultPresenceConfig(a.cfg.Presence)
	if err := validatePresenceConfig(a.cfg.Presence); err != nil {
		return err
	}
	a.cfg.Channel = defaultChannelConfig(a.cfg.Channel)
	if err := validateChannelConfig(a.cfg.Channel); err != nil {
		return err
	}
	a.cfg.ChannelAppend = defaultChannelAppendConfig(a.cfg.ChannelAppend)
	if err := validateChannelAppendConfig(a.cfg.ChannelAppend); err != nil {
		return err
	}
	a.cfg.Conversation = defaultConversationConfig(a.cfg.Conversation)
	if err := validateConversationConfig(a.cfg.Conversation); err != nil {
		return err
	}
	a.cfg.Delivery = defaultDeliveryConfig(a.cfg.Delivery)
	if err := validateDeliveryConfig(a.cfg.Delivery); err != nil {
		return err
	}
	a.cfg.Observability = defaultObservabilityConfig(a.cfg.Observability)
	a.cfg.Observability.Prometheus = defaultPrometheusConfigForApp(a.cfg)
	if err := validateObservabilityConfig(a.cfg.Observability); err != nil {
		return err
	}
	if err := validatePrometheusConfig(a.cfg); err != nil {
		return err
	}
	var err error
	a.cfg.Top, err = NormalizeTopConfig(a.cfg.Top)
	if err != nil {
		return err
	}
	a.cfg.Log = defaultLogConfig(a.cfg.Log)
	return nil
}

func (a *App) applyOptions(opts []Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
}

func (a *App) ensureLogger() error {
	if a.logger == nil {
		logger, err := applog.NewLogger(applog.Config{
			Level:      a.cfg.Log.Level,
			Dir:        a.cfg.Log.Dir,
			MaxSize:    a.cfg.Log.MaxSize,
			MaxAge:     a.cfg.Log.MaxAge,
			MaxBackups: a.cfg.Log.MaxBackups,
			Compress:   a.cfg.Log.Compress,
			Console:    a.cfg.Log.Console,
			Format:     a.cfg.Log.Format,
		})
		if err != nil {
			return fmt.Errorf("internalv2/app: create logger: %w", err)
		}
		a.logger = logger
	}
	return nil
}

func (a *App) configureObservability(clusterCfg *clusterv2.Config) {
	var top *topCollector
	if a.cfg.Top.APIEnabled {
		top = a.ensureTopCollector(clusterCfg.NodeID)
	}
	if a.cfg.Observability.MetricsEnabled {
		a.metrics = obsmetrics.New(clusterCfg.NodeID, fmt.Sprintf("node-%d", clusterCfg.NodeID))
		clusterCfg.Channel.Observer = combineChannelV2Observers(clusterCfg.Channel.Observer, channelV2MetricsObserver{metrics: a.metrics})
		clusterCfg.Slots.Observer = combineSlotObservers(clusterCfg.Slots.Observer, slotMetricsObserver{metrics: a.metrics})
		clusterCfg.Control.RaftObserver = combineControllerRaftObservers(clusterCfg.Control.RaftObserver, controllerRaftMetricsObserver{metrics: a.metrics})
		clusterCfg.Storage.CommitObserver = combineCommitCoordinatorObservers(clusterCfg.Storage.CommitObserver, storageCommitMetricsObserver{
			metrics: a.metrics,
			workers: commitCoordinatorWorkerCount(clusterCfg.Storage.CommitShards),
		})
		clusterCfg.Transport.Observer = combineTransportV2Observers(clusterCfg.Transport.Observer, &transportV2MetricsObserver{metrics: a.metrics})
	}
	if top != nil {
		clusterCfg.Channel.Observer = combineChannelV2Observers(clusterCfg.Channel.Observer, topChannelV2Observer{top: top})
		clusterCfg.Slots.Observer = combineSlotObservers(clusterCfg.Slots.Observer, topSlotObserver{top: top})
		clusterCfg.Control.RaftObserver = combineControllerRaftObservers(clusterCfg.Control.RaftObserver, topControllerRaftObserver{top: top})
		clusterCfg.Storage.CommitObserver = combineCommitCoordinatorObservers(clusterCfg.Storage.CommitObserver, topStorageObserver{top: top})
		clusterCfg.Transport.Observer = combineTransportV2Observers(clusterCfg.Transport.Observer, &topTransportV2Observer{top: top})
	}
	if a.cfg.Observability.Diagnostics.Enabled {
		a.diagnostics = obsdiagnostics.NewStore(diagnosticsStoreOptions(a.cfg))
		a.diagnosticsTracking = obsdiagnostics.NewTrackingRules(obsdiagnostics.TrackingRulesOptions{})
		samplerOptions := diagnosticsSamplerOptions(a.cfg)
		samplerOptions.TrackingRules = a.diagnosticsTracking
		sink := obsdiagnostics.NewSendTraceSink(a.diagnostics, obsdiagnostics.NewSampler(samplerOptions))
		if a.metrics != nil {
			sink = sink.WithMetrics(a.metrics.Diagnostics)
		}
		a.diagnosticsRestore = sendtrace.SetSink(sink)
	}
}

func (a *App) ensureTopCollector(nodeID uint64) *topCollector {
	if collector, ok := a.topProvider.(*topCollector); ok {
		return collector
	}
	collector := newTopCollector(topCollectorOptions{
		NodeID:          nodeID,
		NodeName:        fmt.Sprintf("node-%d", nodeID),
		CollectInterval: a.cfg.Top.CollectInterval,
		HistoryWindow:   a.cfg.Top.HistoryWindow,
		ClusterSnapshot: func() clusterv2.Snapshot {
			if runtime, ok := a.cluster.(interface{ Snapshot() clusterv2.Snapshot }); ok {
				return runtime.Snapshot()
			}
			return clusterv2.Snapshot{NodeID: nodeID}
		},
		MetricsEnabled: a.cfg.Observability.MetricsEnabled,
	})
	a.top = collector
	a.topProvider = collector
	return collector
}

func (a *App) ensureCluster(clusterCfg clusterv2.Config) error {
	if a.cluster == nil {
		node, err := clusterv2.New(clusterCfg)
		if err != nil {
			return err
		}
		a.cluster = node
	}
	return nil
}

func (a *App) ensureOnlineRegistry() {
	if a.online == nil {
		a.online = online.NewRegistry(online.RegistryOptions{})
	}
}

func (a *App) wireDeliveryMetadata() {
	if a.deliveryMeta == nil {
		if node, ok := a.cluster.(deliveryMetaNode); ok {
			a.deliveryMeta = newDeliveryMetaStore(node)
		}
	}
	if a.cfg.Delivery.Enabled && a.deliverySubscribers == nil {
		a.deliverySubscribers = a.deliveryMeta
	}
}

func (a *App) wireChannels() {
	if a.channels == nil {
		if node, ok := a.cluster.(clusterinfra.ChannelMetadataNode); ok {
			metadata := a.ensureChannelAppendMetadataCache()
			store := clusterinfra.NewChannelMetadataStore(node, metadata)
			channelOptions := channelusecase.Options{
				Store:                         store,
				LargeGroupSubscriberThreshold: a.cfg.Channel.LargeGroupSubscriberThreshold,
				SubscriberMutationObserver:    channelAppendSubscriberMutationObserver{app: a},
			}
			if _, ok := node.(clusterinfra.ChannelMembershipNode); ok {
				channelOptions.MembershipIndex = store
			}
			a.channels = channelusecase.New(channelOptions)
		}
	}
}

func (a *App) newConversationReadStore() *clusterinfra.ConversationStore {
	if node, ok := a.cluster.(clusterinfra.ConversationNode); ok {
		return clusterinfra.NewConversationStore(node, clusterinfra.ConversationStoreOptions{
			MaxLastMessageConcurrency: a.cfg.Conversation.MaxLastMessageConcurrency,
		})
	}
	return nil
}

func (a *App) wireConversationAuthority() {
	if a.conversationAuthorityClient == nil {
		authorityNode, hasAuthorityNode := a.cluster.(clusterinfra.ConversationAuthorityNode)
		authorityStore, hasAuthorityStore := a.cluster.(conversationAuthorityStore)
		if hasAuthorityNode && hasAuthorityStore {
			authority := newConversationAuthority(conversationAuthorityOptions{
				LocalNodeID:          authorityNode.NodeID(),
				Store:                authorityStore,
				MaxRowsPerUID:        a.cfg.Conversation.AuthorityCacheMaxRowsPerUID,
				MaxRows:              a.cfg.Conversation.AuthorityCacheMaxRows,
				ListDBWindowMax:      a.cfg.Conversation.AuthorityListDBWindowMax,
				AdmissionBatchRows:   a.cfg.Conversation.AuthorityAdmitBatchRows,
				AdmissionConcurrency: a.cfg.Conversation.AuthorityAdmitConcurrency,
				Observer:             a.conversationAuthorityObserver(),
			})
			client := clusterinfra.NewConversationAuthorityClient(authorityNode, authority)
			a.conversationAuthority = authority
			a.conversationAuthorityClient = client
			if a.conversationActiveWorker == nil {
				a.conversationActiveWorker = newConversationActiveFlushWorker(conversationActiveFlushWorkerOptions{
					Authority:     authority,
					FlushInterval: a.cfg.Conversation.AuthorityFlushInterval,
					BatchRows:     a.cfg.Conversation.AuthorityFlushBatchRows,
					Logger:        a.logger.Named("conversation_active_flush"),
				})
			}
			if a.conversationRouteLifecycle == nil {
				routeLifecycle := newConversationAuthorityRouteLifecycle(conversationAuthorityRouteLifecycleOptions{
					LocalAuthority: authority,
					LocalNodeID:    authorityNode.NodeID(),
					Initial:        a.currentPresenceAuthorities,
					Watch:          authorityNode.WatchRouteAuthorities,
					HandoffTimeout: a.cfg.Conversation.AuthorityHandoffTimeout,
				})
				routeLifecycle.applyRouteAuthorities(context.Background(), a.currentPresenceAuthorities())
				a.conversationRouteLifecycle = routeLifecycle
			}
			adapter := accessnode.New(accessnode.Options{ConversationAuthority: authority, Logger: a.logger.Named("node")})
			authorityNode.RegisterRPC(accessnode.ConversationAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandleConversationAuthorityRPC))
		}
	}
}

func (a *App) wireConversations(conversationReadStore *clusterinfra.ConversationStore) {
	if a.conversations == nil {
		if conversationReadStore != nil {
			var store conversationusecase.Store = conversationReadStore
			if a.conversationAuthorityClient != nil {
				store = a.conversationAuthorityClient
			}
			a.conversations = conversationusecase.New(conversationusecase.Options{
				Store:              store,
				StateStore:         conversationReadStore,
				StateMutationStore: conversationReadStore,
				DeleteStore:        conversationReadStore,
				Messages:           conversationReadStore,
			})
		}
	}
}

func (a *App) wirePresence() {
	if presenceNode, ok := a.cluster.(clusterinfra.PresenceNode); ok {
		directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{LocalNodeID: presenceNode.NodeID()})
		a.presenceDirectory = directory
		authority := presenceDirectoryAuthority{directory: directory}
		ownerActions := presenceOwnerActions{local: a.online}
		client := clusterinfra.NewPresenceAuthorityClient(presenceNode, authority)
		client.SetLocalOwner(ownerActions)
		a.presenceAuthorityClient = client
		adapter := accessnode.New(accessnode.Options{Authority: authority, Owner: ownerActions, Logger: a.logger.Named("node")})
		presenceNode.RegisterRPC(accessnode.PresenceAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandlePresenceAuthorityRPC))
		presenceNode.RegisterRPC(accessnode.PresenceOwnerRPCServiceID, nodeRPCHandlerFunc(adapter.HandlePresenceOwnerRPC))
		if a.presence == nil {
			ownerBootID := newOwnerBootID()
			a.presence = presence.New(presence.Options{
				Local:        a.online,
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
		if a.presenceWorker == nil {
			a.presenceWorker = newPresenceTouchWorker(presenceTouchWorkerOptions{
				NodeID:        presenceNode.NodeID(),
				Watch:         presenceNode.WatchRouteAuthorities,
				Initial:       a.currentPresenceAuthorities,
				Local:         a.online,
				Authority:     client,
				Directory:     directory,
				FlushInterval: a.cfg.Presence.TouchFlushInterval,
				BatchSize:     a.cfg.Presence.TouchBatchSize,
				RouteTTL:      a.cfg.Presence.RouteTTL,
				Logger:        a.logger.Named("presence_touch"),
			})
		}
	}
}

func (a *App) wireManagerConnectionRPC() {
	node, hasNode := a.cluster.(clusterinfra.ManagementNode)
	registrar, hasRegistrar := a.cluster.(nodeRPCRegistrar)
	if !hasNode || !hasRegistrar || a.online == nil {
		return
	}
	service := managementusecase.New(managementusecase.Options{
		Cluster:     clusterinfra.NewManagementSnapshotReader(node),
		Connections: a.online,
	})
	adapter := accessnode.New(accessnode.Options{ManagerConnections: service, Logger: a.logger.Named("node")})
	registrar.RegisterRPC(accessnode.ManagerConnectionRPCServiceID, nodeRPCHandlerFunc(adapter.HandleManagerConnectionRPC))
}

func (a *App) wireManagerLogRPC() {
	node, hasNode := a.cluster.(clusterinfra.ManagementLogNode)
	registrar, hasRegistrar := a.cluster.(nodeRPCRegistrar)
	if !hasNode || !hasRegistrar {
		return
	}
	logs := clusterinfra.NewManagementLogReader(node)
	adapter := accessnode.New(accessnode.Options{ManagerLogs: logs, Logger: a.logger.Named("node")})
	registrar.RegisterRPC(accessnode.ManagerLogRPCServiceID, nodeRPCHandlerFunc(adapter.HandleManagerLogRPC))
}

func (a *App) wireManagerAppLogRPC() {
	registrar, hasRegistrar := a.cluster.(nodeRPCRegistrar)
	if !hasRegistrar {
		return
	}
	reader := a.newManagementApplicationLogReader()
	adapter := accessnode.New(accessnode.Options{ManagerAppLogs: reader, Logger: a.logger.Named("node")})
	registrar.RegisterRPC(accessnode.ManagerAppLogRPCServiceID, nodeRPCHandlerFunc(adapter.HandleManagerAppLogRPC))
}

func (a *App) wireManagerChannelRPC() {
	node, hasNode := a.cluster.(clusterinfra.ManagementNode)
	channelNode, hasChannelNode := a.cluster.(clusterinfra.ChannelBusinessScanNode)
	registrar, hasRegistrar := a.cluster.(nodeRPCRegistrar)
	if !hasNode || !hasChannelNode || !hasRegistrar {
		return
	}
	service := managementusecase.New(managementusecase.Options{
		Cluster:               clusterinfra.NewManagementSnapshotReader(node),
		ChannelBusinessReader: clusterinfra.NewChannelBusinessReader(channelNode),
	})
	adapter := accessnode.New(accessnode.Options{ManagerChannels: service, Logger: a.logger.Named("node")})
	registrar.RegisterRPC(accessnode.ManagerChannelRPCServiceID, nodeRPCHandlerFunc(adapter.HandleManagerChannelRPC))
}

func (a *App) wireManagerDBInspectRPC() {
	registrar, hasRegistrar := a.cluster.(nodeRPCRegistrar)
	if !hasRegistrar {
		return
	}
	reader := a.newDBInspectReader()
	adapter := accessnode.New(accessnode.Options{ManagerDBInspect: reader, Logger: a.logger.Named("node")})
	registrar.RegisterRPC(accessnode.ManagerDBInspectRPCServiceID, nodeRPCHandlerFunc(adapter.HandleManagerDBInspectRPC))
}

func (a *App) wireUsers() {
	if a.users == nil {
		if node, ok := a.cluster.(clusterinfra.UserMetadataNode); ok {
			userStore := clusterinfra.NewUserMetadataStore(node)
			var systemUIDs userusecase.SystemUIDStore
			if channelNode, ok := a.cluster.(clusterinfra.ChannelMetadataNode); ok {
				systemUIDs = clusterinfra.NewChannelMetadataStore(channelNode, nil)
			}
			a.users = userusecase.New(userusecase.Options{
				Users:        userStore,
				Devices:      userStore,
				DeviceReader: userStore,
				Online:       a.online,
				Presence:     a.presence,
				SystemUIDs:   systemUIDs,
				Logger:       a.logger.Named("usecase.user"),
			})
		}
	}
}

func (a *App) wireDelivery() {
	if a.cfg.Delivery.Enabled && a.delivery == nil {
		var top *topCollector
		if collector, ok := a.topProvider.(*topCollector); ok {
			top = collector
		}
		localPusher := &localOwnerPusher{online: a.online, top: top, pendingAckTTL: a.cfg.Delivery.PendingAckTTL, logger: a.logger.Named("delivery.owner")}
		a.localOwnerPusher = localPusher
		deliveryObserver := a.deliveryObserver()
		var push runtimedelivery.Pusher = localPusher
		var fanoutRemote runtimedelivery.FanoutTaskForwarder
		var localNodeID uint64
		if presenceNode, ok := a.cluster.(clusterinfra.PresenceNode); ok {
			localNodeID = presenceNode.NodeID()
			nodeClient := accessnode.NewClient(presenceNode)
			push = clusterinfra.NewDeliveryPusher(localNodeID, localPusher, nodeClient)
			fanoutRemote = nodeClient
		}
		var partitioner runtimedelivery.Partitioner
		if routes, ok := a.cluster.(clusterWriteReadyRuntime); ok {
			partitioner = clusterinfra.NewDeliveryPartitioner(routes)
		}
		fanoutWorker := runtimedelivery.NewFanoutWorker(runtimedelivery.FanoutWorkerOptions{
			Subscribers: appSubscriberPlanner{
				channel: runtimedelivery.NewChannelSubscriberPlanner(runtimedelivery.ChannelSubscriberPlannerOptions{
					Source: a.deliverySubscribers,
				}),
			},
			Presence:      presenceResolverAdapter{presence: a.presence},
			Push:          push,
			PageSize:      a.cfg.Delivery.FanoutPageSize,
			PushBatchSize: a.cfg.Delivery.PushBatchSize,
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
			Capacity:    a.cfg.Delivery.EventQueueSize,
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
			AsyncQueueSize:  a.cfg.Delivery.EventQueueSize,
			AsyncWorkers:    1,
			ManagerObserver: managerObserver,
			Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
				MaxPendingPerSession: a.cfg.Delivery.PendingAckMaxPerSession,
			}),
		})
		localPusher.delivery = manager
		a.deliveryManager = manager
		a.deliveryRetry = retryScheduler
		a.delivery = deliveryusecase.New(deliveryusecase.Options{Runtime: deliveryRuntimeAdapter{manager: manager}})
		if a.deliveryWorker == nil {
			a.deliveryWorker = deliveryWorkerGroup{retryScheduler, manager}
		}
		if presenceNode, ok := a.cluster.(clusterinfra.PresenceNode); ok {
			adapter := accessnode.New(accessnode.Options{Delivery: localPusher, DeliveryFanout: fanoutWorker, Logger: a.logger.Named("node")})
			presenceNode.RegisterRPC(accessnode.DeliveryPushRPCServiceID, nodeRPCHandlerFunc(adapter.HandleDeliveryPushRPC))
			presenceNode.RegisterRPC(accessnode.DeliveryFanoutRPCServiceID, nodeRPCHandlerFunc(adapter.HandleDeliveryFanoutRPC))
		}
	}
}

func (a *App) wireChannelAppend(nodeID uint64) error {
	if a.channelAppends == nil {
		appendNode, hasAppendNode := a.cluster.(clusterinfra.ChannelAppendNode)
		authorityNode, hasAuthorityNode := a.cluster.(clusterinfra.ChannelAppendAuthorityNode)
		if hasAppendNode && hasAuthorityNode {
			metadata := a.ensureChannelAppendMetadataCache()
			messageIDs, err := newNodeMessageIDs(nodeID)
			if err != nil {
				return fmt.Errorf("internalv2/app: create message id generator: %w", err)
			}
			opts := channelappend.Options{
				LocalNodeID:                           nodeID,
				Appender:                              clusterinfra.NewChannelAppender(appendNode, a.logger.Named("cluster.append")),
				MessageID:                             messageIDs,
				AuthorityShardCount:                   a.cfg.ChannelAppend.AuthorityShardCount,
				AdvancePoolSize:                       a.cfg.ChannelAppend.AdvancePoolSize,
				EffectPoolSize:                        a.cfg.ChannelAppend.EffectPoolSize,
				RecipientAuthorityDispatchConcurrency: a.cfg.ChannelAppend.RecipientAuthorityDispatchConcurrency,
				RecipientBatchSize:                    a.cfg.Delivery.PushBatchSize,
				SubscriberScanPageSize:                a.cfg.Delivery.FanoutPageSize,
			}
			if a.deliveryMeta != nil {
				opts.Subscribers = channelAppendDeliverySubscriberSource{source: a.deliveryMeta}
			} else if a.deliverySubscribers != nil {
				opts.Subscribers = channelAppendDeliverySubscriberSource{source: a.deliverySubscribers}
			} else if subscriberNode, ok := a.cluster.(recipientSubscriberNode); ok {
				opts.Subscribers = channelAppendSubscriberSource{node: subscriberNode}
			}
			if recipientNode, ok := a.cluster.(recipientAuthorityRouteNode); ok {
				opts.RecipientAuthorityResolver = channelAppendRecipientResolver{node: recipientNode}
			}
			if a.conversationAuthorityClient != nil {
				opts.ConversationActiveAdmitter = a.conversationAuthorityClient
			}
			var observer deliveryMessageObserver
			if _, topEnabled := a.topProvider.(*topCollector); a.cfg.Delivery.Enabled || a.metrics != nil || topEnabled {
				observer = deliveryMessageObserver{app: a}
				opts.Observer = observer
			}
			if a.cfg.Delivery.Enabled {
				processor := channelappend.NewRecipientProcessor(channelappend.RecipientProcessorOptions{
					PresenceResolver:            channelAppendPresenceResolver{presence: a.presence},
					OwnerPusher:                 a.channelAppendOwnerPusher(nodeID),
					DeliveryRetryMaxAttempts:    defaultDeliveryRetryMaxAttempts,
					DeliveryRetryInitialBackoff: defaultDeliveryRetryBackoff,
					DeliveryRetryMaxBackoff:     defaultDeliveryRetryBackoff,
				})
				if a.channelAppendDeliveryWorker == nil {
					a.channelAppendDeliveryWorker = channelappend.NewRecipientDeliveryWorker(channelappend.RecipientDeliveryWorkerOptions{
						Processor: processor,
						QueueSize: a.cfg.Delivery.EventQueueSize,
						Workers:   a.cfg.ChannelAppend.RecipientAuthorityDispatchConcurrency,
						Observer:  observer,
					})
				}
				opts.RecipientDeliveryEnqueuer = a.channelAppendDeliveryWorker
				a.deliveryWorker = appendDeliveryWorker(a.deliveryWorker, a.channelAppendDeliveryWorker)
			}
			group := channelappend.New(opts)
			var remote clusterinfra.ChannelAppendRemoteForwarder
			if rpcNode, ok := a.cluster.(accessnode.PresenceRPCNode); ok {
				remote = accessnode.NewClient(rpcNode)
			}
			client := clusterinfra.NewChannelAppendClient(authorityNode, remote, metadata)
			router := channelappend.NewRouter(channelappend.RouterOptions{
				LocalNodeID:        nodeID,
				Resolver:           client,
				Local:              group,
				Remote:             client,
				MaxOutboundPerNode: a.cfg.Delivery.EventQueueSize,
				MaxRouteAttempts:   defaultDeliveryRetryMaxAttempts,
				Observer:           observer,
			})
			a.channelAppends = group
			a.channelAppendRouter = router
			if registrar, ok := a.cluster.(nodeRPCRegistrar); ok {
				adapter := accessnode.NewChannelAppendAdapter(accessnode.ChannelAppendOptions{
					ChannelAppend: channelAppendAuthorityLocal{group: group},
					Logger:        a.logger.Named("node"),
				})
				registrar.RegisterRPC(accessnode.ChannelAppendRPCServiceID, nodeRPCHandlerFunc(adapter.HandleChannelAppendRPC))
			}
		}
	}
	return nil
}

func (a *App) ensureChannelAppendMetadataCache() *clusterinfra.ChannelAppendMetadataCache {
	if a.channelAppendMetadata == nil {
		a.channelAppendMetadata = clusterinfra.NewChannelAppendMetadataCache()
	}
	return a.channelAppendMetadata
}

func (a *App) channelAppendOwnerPusher(nodeID uint64) channelappend.OwnerPusher {
	if a.localOwnerPusher == nil {
		return nil
	}
	var pusher runtimedelivery.Pusher = a.localOwnerPusher
	if rpcNode, ok := a.cluster.(accessnode.PresenceRPCNode); ok {
		pusher = clusterinfra.NewDeliveryPusher(nodeID, a.localOwnerPusher, accessnode.NewClient(rpcNode))
	}
	return channelAppendOwnerPusher{next: pusher}
}

func (a *App) wireMessages() {
	if a.messages == nil {
		messageOpts := message.Options{
			Submitter:              a.channelAppendRouter,
			SystemUIDs:             a.users,
			PersonWhitelistEnabled: a.cfg.Message.PersonWhitelistEnabled,
			SystemDeviceID:         a.cfg.Message.SystemDeviceID,
			PermissionCacheTTL:     a.cfg.Message.PermissionCacheTTL,
		}
		if channelNode, ok := a.cluster.(clusterinfra.ChannelMetadataNode); ok {
			messageOpts.PermissionStore = clusterinfra.NewChannelMetadataStore(channelNode, nil)
		}
		if readNode, ok := a.cluster.(clusterinfra.ChannelMessageReadNode); ok {
			messageOpts.Reader = clusterinfra.NewChannelMessageReader(readNode)
		}
		a.messages = message.New(messageOpts)
	}
}

func (a *App) wireAPIMessageFacade() {
	if a.apiMessages == nil && a.messages != nil {
		a.apiMessages = a.messages
	}
}

func (a *App) wireGatewayHandler(ownerNodeID uint64) {
	if a.handler == nil {
		handlerMessages := accessgateway.MessageUsecase(a.messages)
		a.handler = accessgateway.New(accessgateway.Options{
			Messages:        handlerMessages,
			Presence:        a.gatewayPresenceUsecase(),
			Delivery:        a.delivery,
			OwnerNodeID:     ownerNodeID,
			SendTimeout:     a.cfg.Gateway.SendTimeout,
			SendackObserver: a.sendackObserver(),
			Logger:          a.logger.Named("access.gateway"),
		})
	}
}

func (a *App) wireAPI() {
	if a.api == nil && strings.TrimSpace(a.cfg.API.ListenAddr) != "" {
		legacyRouteExternal, legacyRouteIntranet := legacyRouteAddresses(a.cfg.API, a.cfg.Gateway.Listeners)
		legacyRouteNodes := legacyRouteNodeAddresses(a.cfg.NodeID, a.cfg.Cluster.Control.Voters, legacyRouteExternal, legacyRouteIntranet)
		a.api = accessapi.New(accessapi.Options{
			ListenAddr:               a.cfg.API.ListenAddr,
			Readyz:                   a.readyzReport,
			BenchEnabled:             a.cfg.Bench.APIEnabled,
			BenchMaxBatchSize:        a.cfg.Bench.APIMaxBatchSize,
			BenchMaxPayloadBytes:     a.cfg.Bench.APIMaxPayloadBytes,
			Gateway:                  apiGatewayAddresses(a.cfg.API, a.cfg.Gateway.Listeners),
			BenchRuntime:             a.benchRuntimeController(),
			BenchPresence:            a.benchPresenceController(),
			BenchData:                a.deliveryMeta,
			Channels:                 a.channels,
			Users:                    a.users,
			Messages:                 a.apiMessages,
			Conversations:            a.conversations,
			ConversationListObserver: a.conversationListObserver(),
			ConversationSyncObserver: a.conversationSyncObserver(),
			LegacyRouteExternal:      legacyRouteExternal,
			LegacyRouteIntranet:      legacyRouteIntranet,
			LegacyRouteNodes:         legacyRouteNodes,
			MetricsHandler:           a.metricsHandler(),
			DebugAPIEnabled:          a.cfg.Observability.DebugAPIEnabled,
			DebugConfig:              a.debugConfigSnapshot,
			DebugCluster:             a.debugClusterSnapshot,
			Diagnostics:              a,
			Top:                      a.topProvider,
			Logger:                   a.logger.Named("access.api"),
		})
	}
}

func (a *App) wireManager() {
	if a.manager == nil && strings.TrimSpace(a.cfg.Manager.ListenAddr) != "" {
		management := a.newManagerManagement()
		a.manager = accessmanager.New(accessmanager.Options{
			ListenAddr: a.cfg.Manager.ListenAddr,
			Auth: accessmanager.AuthConfig{
				On:        a.cfg.Manager.AuthOn,
				JWTSecret: a.cfg.Manager.JWTSecret,
				JWTIssuer: a.cfg.Manager.JWTIssuer,
				JWTExpire: a.cfg.Manager.JWTExpire,
				Users:     managerUserConfigs(a.cfg.Manager.Users),
			},
			Management:     management,
			Monitor:        a.newManagerMonitorProvider(),
			ClusterMonitor: a.newManagerClusterMonitorProvider(management),
			Top:            a.topProvider,
			Logger:         a.logger.Named("access.manager"),
		})
	}
}

func (a *App) newManagerMonitorProvider() accessmanager.RealtimeMonitorProvider {
	prometheusBaseURL := ""
	if listenAddr := strings.TrimSpace(a.cfg.Observability.Prometheus.ListenAddr); listenAddr != "" {
		prometheusBaseURL = "http://" + listenAddr
	}
	nodeID := a.cfg.NodeID
	if nodeID == 0 {
		nodeID = a.cfg.Cluster.NodeID
	}
	return newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled:  a.cfg.Observability.MetricsEnabled && a.cfg.Observability.Prometheus.Enabled,
		BaseURL:  prometheusBaseURL,
		NodeID:   nodeID,
		NodeName: fmt.Sprintf("node-%d", nodeID),
	})
}

func (a *App) newManagerClusterMonitorProvider(control managerClusterControlReader) accessmanager.ClusterRealtimeMonitorProvider {
	prometheusBaseURL := ""
	if listenAddr := strings.TrimSpace(a.cfg.Observability.Prometheus.ListenAddr); listenAddr != "" {
		prometheusBaseURL = "http://" + listenAddr
	}
	return newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled: a.cfg.Observability.MetricsEnabled && a.cfg.Observability.Prometheus.Enabled,
		BaseURL: prometheusBaseURL,
		Control: control,
	})
}

func (a *App) newManagerManagement() accessmanager.Management {
	if node, ok := a.cluster.(clusterinfra.ManagementNode); ok {
		opts := managementusecase.Options{
			Cluster:       clusterinfra.NewManagementSnapshotReader(node),
			Conversations: a.conversations,
		}
		localApplicationLogs := a.newManagementApplicationLogReader()
		opts.ApplicationLogs = localApplicationLogs
		if rpcNode, ok := a.cluster.(clusterinfra.ManagementApplicationLogRPCNode); ok {
			opts.ApplicationLogs = clusterinfra.NewManagementApplicationLogReader(rpcNode, localApplicationLogs)
		}
		opts.DBInspect = a.newDBInspectReader()
		if rpcNode, ok := a.cluster.(clusterinfra.ManagementDBInspectNode); ok {
			opts.RemoteDBInspect = clusterinfra.NewManagementDBInspectReader(rpcNode)
		}
		if runtimeNode, ok := a.cluster.(clusterinfra.ChannelRuntimeMetaScanNode); ok {
			opts.ChannelRuntimeMeta = clusterinfra.NewChannelRuntimeMetaReader(runtimeNode)
		}
		if channelNode, ok := a.cluster.(clusterinfra.ChannelBusinessScanNode); ok {
			opts.ChannelBusinessReader = clusterinfra.NewChannelBusinessReader(channelNode)
		}
		if channelRPCNode, ok := a.cluster.(clusterinfra.ManagementChannelNode); ok {
			opts.RemoteBusinessChannels = clusterinfra.NewManagementChannelReader(channelRPCNode)
		}
		if userNode, ok := a.cluster.(clusterinfra.UserMetadataNode); ok {
			opts.Users = clusterinfra.NewUserMetadataStore(userNode)
		}
		if a.users != nil {
			opts.UserOperator = a.users
			opts.SystemUsers = a.users
		}
		if a.presence != nil {
			opts.UserPresence = a.presence
		}
		if a.presenceAuthorityClient != nil {
			opts.UserActions = a.presenceAuthorityClient
		}
		if readNode, ok := a.cluster.(clusterinfra.ChannelMessageReadNode); ok {
			opts.Messages = clusterinfra.NewManagementMessageReader(readNode)
		}
		if a.online != nil {
			opts.Connections = a.online
		}
		if connNode, ok := a.cluster.(clusterinfra.ManagementConnectionNode); ok {
			opts.RemoteConnections = clusterinfra.NewManagementConnectionReader(connNode)
		}
		if logNode, ok := a.cluster.(clusterinfra.ManagementLogNode); ok {
			opts.Logs = clusterinfra.NewManagementLogReader(logNode)
		}
		return managementusecase.New(opts)
	}
	return nil
}

func (a *App) newManagementApplicationLogReader() *applicationLogReader {
	if a == nil {
		return nil
	}
	return newApplicationLogReader(a.cfg.NodeID, a.cfg.Log.Dir)
}

func (a *App) newDBInspectReader() *dbInspectReader {
	if a == nil || strings.TrimSpace(a.cfg.DataDir) == "" {
		return nil
	}
	hashSlotCount := a.cfg.Cluster.Slots.HashSlotCount
	if hashSlotCount == 0 {
		hashSlotCount = 16
	}
	return newDBInspectReader(dbInspectReaderOptions{
		NodeID:        a.cfg.NodeID,
		DataDir:       a.cfg.DataDir,
		HashSlotCount: hashSlotCount,
	})
}

func managerUserConfigs(users []ManagerUserConfig) []accessmanager.UserConfig {
	out := make([]accessmanager.UserConfig, 0, len(users))
	for _, user := range users {
		out = append(out, accessmanager.UserConfig{
			Username:    user.Username,
			Password:    user.Password,
			Permissions: managerPermissionConfigs(user.Permissions),
		})
	}
	return out
}

func managerPermissionConfigs(permissions []ManagerPermissionConfig) []accessmanager.PermissionConfig {
	out := make([]accessmanager.PermissionConfig, 0, len(permissions))
	for _, permission := range permissions {
		out = append(out, accessmanager.PermissionConfig{
			Resource: permission.Resource,
			Actions:  append([]string(nil), permission.Actions...),
		})
	}
	return out
}

func (a *App) wireGateway(nodeID uint64) error {
	if a.gateway == nil && len(a.cfg.Gateway.Listeners) > 0 {
		gw, err := gateway.New(gateway.Options{
			Handler:        a.handler,
			Authenticator:  gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{NodeID: nodeID}),
			Listeners:      a.cfg.Gateway.Listeners,
			DefaultSession: a.cfg.Gateway.Session,
			Runtime:        a.cfg.Gateway.Runtime,
			Transport:      a.cfg.Gateway.Transport,
			Observer:       a.gatewayObserver(),
			Logger:         a.logger.Named("gateway"),
		})
		if err != nil {
			return err
		}
		a.gateway = gw
	}
	return nil
}
