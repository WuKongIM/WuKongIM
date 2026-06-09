package app

import (
	"context"
	"fmt"
	"strings"

	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	applog "github.com/WuKongIM/WuKongIM/internalv2/log"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
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
)

func (a *App) applyConfigDefaults() error {
	a.cfg.Presence = defaultPresenceConfig(a.cfg.Presence)
	if err := validatePresenceConfig(a.cfg.Presence); err != nil {
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
	if err := validateObservabilityConfig(a.cfg.Observability); err != nil {
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
			store := clusterinfra.NewChannelMetadataStore(node)
			channelOptions := channelusecase.Options{
				Store: store,
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
				HandoffTimeout:       a.cfg.Conversation.AuthorityHandoffTimeout,
				Observer:             a.conversationAuthorityObserver(),
			})
			client := clusterinfra.NewConversationAuthorityClient(authorityNode, authority)
			a.conversationAuthority = authority
			a.conversationAuthorityClient = client
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
				Store:    store,
				Messages: conversationReadStore,
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

func (a *App) wireUsers() {
	if a.users == nil {
		if node, ok := a.cluster.(clusterinfra.UserMetadataNode); ok {
			userStore := clusterinfra.NewUserMetadataStore(node)
			var systemUIDs userusecase.SystemUIDStore
			if channelNode, ok := a.cluster.(clusterinfra.ChannelMetadataNode); ok {
				systemUIDs = clusterinfra.NewChannelMetadataStore(channelNode)
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
		localPusher := &localOwnerPusher{online: a.online, pendingAckTTL: a.cfg.Delivery.PendingAckTTL, logger: a.logger.Named("delivery.owner")}
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

func (a *App) wireRecipientAuthority() {
	if a.recipientWorker == nil {
		if authorityNode, ok := a.cluster.(clusterinfra.RecipientAuthorityNode); ok && a.conversationAuthorityClient != nil {
			recipientAuthority := clusterinfra.NewRecipientAuthorityClient(authorityNode, nil)
			a.recipientAuthorityClient = recipientAuthority
			authorityObserver := a.authorityObserver()
			var deliverySubmitter recipientusecase.DeliverySubmitter
			if a.delivery != nil {
				deliverySubmitter = recipientDeliverySubmitter{delivery: a.delivery, observer: authorityObserver}
			}
			var conversationUpdater recipientusecase.ConversationUpdater = a.conversationAuthorityClient
			if authorityObserver != nil {
				conversationUpdater = observedRecipientConversationUpdater{next: conversationUpdater, observer: authorityObserver}
			}
			processor := recipientusecase.NewProcessor(recipientusecase.ProcessorOptions{
				LocalNodeID:  authorityNode.NodeID(),
				Authority:    recipientAuthority,
				Conversation: conversationUpdater,
				Delivery:     deliverySubmitter,
			})
			dispatcher := recipientusecase.NewDispatcher(recipientusecase.DispatcherOptions{
				LocalNodeID:     authorityNode.NodeID(),
				Recipients:      a.recipientSource(),
				Resolver:        recipientAuthority,
				Local:           processor,
				Remote:          recipientAuthority,
				PageSize:        a.cfg.Delivery.FanoutPageSize,
				TargetBatchSize: a.cfg.Delivery.PushBatchSize,
			})
			a.recipientWorker = newRecipientCommittedWorker(dispatcher, a.cfg.Delivery.EventQueueSize, a.delivery != nil, a.logger.Named("recipient"), authorityObserver)
			if registrar, ok := a.cluster.(nodeRPCRegistrar); ok {
				adapter := accessnode.New(accessnode.Options{RecipientAuthority: processor, Logger: a.logger.Named("node")})
				registerNodeRPC(registrar, accessnode.RecipientAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandleRecipientAuthorityRPC))
			}
		}
	}
}

func (a *App) wireChannelWrite(nodeID uint64) {
	if a.channelWrites == nil {
		appendNode, hasAppendNode := a.cluster.(clusterinfra.ChannelAppendNode)
		writeNode, hasWriteNode := a.cluster.(clusterinfra.ChannelWriteNode)
		if hasAppendNode && hasWriteNode {
			opts := channelwrite.Options{
				LocalNodeID:                 nodeID,
				Appender:                    clusterinfra.NewChannelAppender(appendNode),
				MessageID:                   newNodeMessageIDs(nodeID),
				RecipientBatchSize:          a.cfg.Delivery.PushBatchSize,
				SubscriberPageSize:          a.cfg.Delivery.FanoutPageSize,
				DeliveryRetryMaxAttempts:    defaultDeliveryRetryMaxAttempts,
				DeliveryRetryInitialBackoff: defaultDeliveryRetryBackoff,
				DeliveryRetryMaxBackoff:     defaultDeliveryRetryBackoff,
				CommitRetryMaxAttempts:      defaultDeliveryRetryMaxAttempts,
			}
			if a.deliveryMeta != nil {
				opts.Subscribers = channelWriteDeliverySubscriberSource{source: a.deliveryMeta}
			} else if a.deliverySubscribers != nil {
				opts.Subscribers = channelWriteDeliverySubscriberSource{source: a.deliverySubscribers}
			} else if subscriberNode, ok := a.cluster.(recipientSubscriberNode); ok {
				opts.Subscribers = channelWriteSubscriberSource{node: subscriberNode}
			}
			if recipientNode, ok := a.cluster.(recipientAuthorityRouteNode); ok {
				opts.RecipientAuthorityResolver = channelWriteRecipientResolver{node: recipientNode}
			}
			if a.conversationAuthorityClient != nil {
				opts.ConversationProjector = channelWriteConversationProjector{client: a.conversationAuthorityClient}
			}
			if a.cfg.Delivery.Enabled {
				opts.PresenceResolver = channelWritePresenceResolver{presence: a.presence}
				opts.OwnerPusher = a.channelWriteOwnerPusher(nodeID)
			}
			if a.cfg.Delivery.Enabled || a.metrics != nil {
				opts.Observer = deliveryMessageObserver{app: a}
			}
			if cursorNode, ok := a.cluster.(clusterinfra.ChannelWriteCursorMetadataNode); ok {
				opts.CursorStore = clusterinfra.NewChannelWriteCursorStore(cursorNode)
			}
			if readNode, ok := a.cluster.(clusterinfra.ChannelMessageReadNode); ok {
				opts.CommittedReader = clusterinfra.NewChannelWriteCommittedReader(readNode)
			}
			processor := channelwrite.NewRecipientProcessor(channelwrite.RecipientProcessorOptions{
				ConversationProjector:       opts.ConversationProjector,
				PresenceResolver:            opts.PresenceResolver,
				OwnerPusher:                 opts.OwnerPusher,
				DeliveryRetryMaxAttempts:    opts.DeliveryRetryMaxAttempts,
				DeliveryRetryInitialBackoff: opts.DeliveryRetryInitialBackoff,
				DeliveryRetryMaxBackoff:     opts.DeliveryRetryMaxBackoff,
			})
			opts.RecipientRouter = channelWriteRecipientRouter{processor: processor}
			group := channelwrite.New(opts)
			var remote clusterinfra.ChannelWriteRemoteForwarder
			if rpcNode, ok := a.cluster.(accessnode.PresenceRPCNode); ok {
				remote = accessnode.NewClient(rpcNode)
			}
			client := clusterinfra.NewChannelWriteClient(writeNode, remote)
			router := channelwrite.NewRouter(channelwrite.RouterOptions{
				LocalNodeID:        nodeID,
				Resolver:           client,
				Local:              group,
				Remote:             client,
				MaxOutboundPerNode: a.cfg.Delivery.EventQueueSize,
				MaxRouteAttempts:   defaultDeliveryRetryMaxAttempts,
			})
			a.channelWrites = group
			a.channelWriteRouter = router
			if registrar, ok := a.cluster.(nodeRPCRegistrar); ok {
				adapter := accessnode.NewChannelWriteAdapter(accessnode.ChannelWriteOptions{
					ChannelWrite: channelWriteAuthorityLocal{group: group},
					Logger:       a.logger.Named("node"),
				})
				registerNodeRPC(registrar, accessnode.ChannelWriteRPCServiceID, nodeRPCHandlerFunc(adapter.HandleChannelWriteRPC))
			}
		}
	}
}

func (a *App) channelWriteOwnerPusher(nodeID uint64) channelwrite.OwnerPusher {
	if a.localOwnerPusher == nil {
		return nil
	}
	var pusher runtimedelivery.Pusher = a.localOwnerPusher
	if rpcNode, ok := a.cluster.(accessnode.PresenceRPCNode); ok {
		pusher = clusterinfra.NewDeliveryPusher(nodeID, a.localOwnerPusher, accessnode.NewClient(rpcNode))
	}
	return channelWriteOwnerPusher{next: pusher}
}

func (a *App) recipientSource() recipientusecase.RecipientSource {
	if a.deliveryMeta != nil {
		return a.deliveryMeta
	}
	if a.deliverySubscribers != nil {
		return deliverySubscriberRecipientSource{source: a.deliverySubscribers}
	}
	if subscriberNode, ok := a.cluster.(recipientSubscriberNode); ok {
		return recipientSubscriberStore{node: subscriberNode}
	}
	return nil
}

func (a *App) wireMessages() {
	if a.messages == nil {
		messageOpts := message.Options{Submitter: a.channelWriteRouter}
		if readNode, ok := a.cluster.(clusterinfra.ChannelMessageReadNode); ok {
			messageOpts.Reader = clusterinfra.NewChannelMessageReader(readNode)
		}
		a.messages = message.New(messageOpts)
	}
}

func (a *App) wireSenderAuthority() {
	if a.senderMessages == nil {
		if senderNode, ok := a.cluster.(clusterinfra.SenderAuthorityNode); ok && a.messages != nil {
			senderAuthority := clusterinfra.NewSenderAuthorityClient(senderNode, nil)
			var senderResolver message.UIDAuthorityResolver = senderAuthority
			if authorityObserver := a.authorityObserver(); authorityObserver != nil {
				senderResolver = observedSenderAuthorityResolver{
					localNodeID: senderNode.NodeID(),
					next:        senderAuthority,
					observer:    authorityObserver,
				}
			}
			a.senderMessages = message.NewSenderAuthorityRouter(message.SenderAuthorityRouterOptions{
				LocalNodeID: senderNode.NodeID(),
				Resolver:    senderResolver,
				Local:       a.messages,
				Remote:      senderAuthority,
			})
			var routes clusterWriteReadyRuntime
			if routeRuntime, ok := a.cluster.(clusterWriteReadyRuntime); ok {
				routes = routeRuntime
			}
			local := senderAuthorityLocal{
				localNodeID: senderNode.NodeID(),
				resolver:    senderAuthority,
				routes:      routes,
				submitter:   a.messages,
			}
			adapter := accessnode.New(accessnode.Options{SenderAuthority: local, Logger: a.logger.Named("node")})
			senderNode.RegisterRPC(accessnode.SenderAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandleSenderAuthorityRPC))
		}
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
			MetricsHandler:           a.metricsHandler(),
			PProfEnabled:             a.cfg.Observability.PProfEnabled,
			Logger:                   a.logger.Named("access.api"),
		})
	}
}

func (a *App) wireGateway(nodeID uint64) error {
	if a.gateway == nil && len(a.cfg.Gateway.Listeners) > 0 {
		gw, err := gateway.New(gateway.Options{
			Handler:        a.handler,
			Authenticator:  gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{NodeID: nodeID}),
			Listeners:      a.cfg.Gateway.Listeners,
			DefaultSession: a.cfg.Gateway.Session,
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
