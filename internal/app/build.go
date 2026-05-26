package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	accessgateway "github.com/WuKongIM/WuKongIM/internal/access/gateway"
	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	applifecycle "github.com/WuKongIM/WuKongIM/internal/app/lifecycle"
	applog "github.com/WuKongIM/WuKongIM/internal/log"
	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	runtimechannelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	runtimechannelplane "github.com/WuKongIM/WuKongIM/internal/runtime/channelplane"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	deliverytagruntime "github.com/WuKongIM/WuKongIM/internal/runtime/deliverytag"
	"github.com/WuKongIM/WuKongIM/internal/runtime/messageid"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/runtime/userlimit"
	"github.com/WuKongIM/WuKongIM/internal/usecase/benchdata"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	testdatausecase "github.com/WuKongIM/WuKongIM/internal/usecase/testdata"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelreplica "github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// buildAfterChannelRuntimeHook is a narrow test hook for late build failures.
var buildAfterChannelRuntimeHook func(*App) error

var openRaftLogDB = raftstorage.Open

const defaultChannelLongPollHWOnlyNotifyDelay = 5 * time.Millisecond

func build(cfg Config) (_ *App, err error) {
	if err := cfg.ApplyDefaultsAndValidate(); err != nil {
		return nil, err
	}

	app := &App{cfg: cfg, createdAt: time.Now()}
	app.nodeDrainState = newNodeDrainState(cfg.Node.ID, time.Now)
	cleanup := &applifecycle.ResourceStack{}
	defer func() {
		if err != nil {
			err = errors.Join(err, cleanup.Close())
		}
	}()

	app.logger, err = applog.NewLogger(applog.Config{
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
		return nil, fmt.Errorf("app: create logger: %w", err)
	}
	cleanup.Push("logger", func() error { return app.syncLogger() })
	if cfg.Observability.MetricsEnabled {
		app.metrics = obsmetrics.New(cfg.Node.ID, cfg.Node.Name)
		app.metrics.Channel.SetMaxChannels(cfg.Cluster.MaxChannels)
		app.dashboardCollector = obsmetrics.NewDashboardCollector(app.metrics)
		app.dashboardCollector.Start()
		cleanup.Push("dashboard collector", func() error {
			app.stopDashboardCollector()
			return nil
		})
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
		cleanup.Push("diagnostics sendtrace sink", func() error {
			app.restoreDiagnosticsSink()
			return nil
		})
	}

	app.db, err = metadb.Open(cfg.Storage.DBPath)
	if err != nil {
		return nil, fmt.Errorf("app: open metadb: %w", err)
	}
	cleanup.Push("metadb", func() error {
		if app.db == nil {
			return nil
		}
		return app.db.Close()
	})
	app.raftDB, err = openRaftLogDB(cfg.Storage.RaftPath, raftstorage.Options{
		SnapshotPath:      cfg.Storage.RaftSnapshotPath,
		SnapshotChunkSize: cfg.Storage.RaftSnapshotChunkSize,
		SnapshotGCGrace:   cfg.Storage.RaftSnapshotGCGrace,
	})
	if err != nil {
		return nil, fmt.Errorf("app: open raftstorage: %w", err)
	}
	cleanup.Push("raft log db", func() error {
		if app.raftDB == nil {
			return nil
		}
		return app.raftDB.Close()
	})

	clusterCfg := cfg.Cluster.runtimeConfig(cfg.Storage, app.db, app.raftDB, cfg.Node.ID, cfg.Node.Name, app.logger.Named("cluster"))
	var transportObserver transport.ObserverHooks
	clusterObserver := raftcluster.ObserverHooks{
		OnLeaderChange: func(slotID uint32, _, _ multiraft.NodeID) {
			if app.channelMetaSync == nil {
				return
			}
			app.channelMetaSync.scheduleSlotLeaderRefresh(multiraft.SlotID(slotID))
		},
		OnNodeStatusChange: func(nodeID uint64, _ controllermeta.NodeStatus, to controllermeta.NodeStatus) {
			app.observeNodeStatusChange(nodeID, to)
			if app.channelMetaSync == nil {
				return
			}
			app.channelMetaSync.UpdateNodeLiveness(nodeID, to)
		},
	}
	if cfg.Observability.NetworkEnabled {
		app.networkObservability = newNetworkObservability(networkObservabilityConfig{
			LocalNodeID:                   cfg.Node.ID,
			LocalNodeName:                 cfg.Node.Name,
			ListenAddr:                    cfg.Cluster.ListenAddr,
			AdvertiseAddr:                 cfg.Cluster.AdvertiseAddr,
			StaticNodes:                   cfg.Cluster.Nodes,
			Seeds:                         cfg.Cluster.Seeds,
			PoolSize:                      cfg.Cluster.PoolSize,
			DataPlanePoolSize:             cfg.Cluster.DataPlanePoolSize,
			DialTimeout:                   cfg.Cluster.DialTimeout,
			ControllerObservationInterval: cfg.Cluster.Timeouts.ControllerObservation,
			DataPlaneRPCTimeout:           cfg.Cluster.DataPlaneRPCTimeout,
			LongPollLaneCount:             cfg.Cluster.LongPollLaneCount,
			LongPollMaxWait:               cfg.Cluster.LongPollMaxWait,
			LongPollMaxBytes:              cfg.Cluster.LongPollMaxBytes,
			LongPollMaxChannels:           cfg.Cluster.LongPollMaxChannels,
			DataPlanePoolStats: func() []transport.PoolPeerStats {
				if app.dataPlanePool == nil {
					return nil
				}
				return app.dataPlanePool.Stats()
			},
		})
	}
	if app.metrics != nil {
		clusterObserver = mergeClusterObserverHooks(clusterObserver, clusterMetricsObserver{metrics: app.metrics}.Hooks())
		transportObserver = transportMetricsObserver{metrics: app.metrics}.Hooks()
	}
	if app.networkObservability != nil {
		transportObserver = mergeTransportObserverHooks(transportObserver, app.networkObservability.TransportHooks())
		clusterObserver = mergeClusterObserverHooks(clusterObserver, app.networkObservability.ClusterHooks())
	}
	if hasTransportObserverHooks(transportObserver) {
		clusterCfg.TransportObserver = transportObserver
	}
	clusterCfg.Observer = clusterObserver
	app.cluster, err = raftcluster.NewCluster(clusterCfg)
	if err != nil {
		return nil, fmt.Errorf("app: create cluster: %w", err)
	}

	app.channelLogDB, err = channelstore.Open(cfg.Storage.ChannelLogPath)
	if err != nil {
		return nil, fmt.Errorf("app: open channel store: %w", err)
	}
	app.channelLogDB.ConfigureCommitCoordinator(channelstore.CommitCoordinatorConfig{
		FlushWindow: cfg.Cluster.CommitCoordinatorFlushWindow,
		MaxRequests: cfg.Cluster.CommitCoordinatorMaxRequests,
		MaxRecords:  cfg.Cluster.CommitCoordinatorMaxRecords,
		MaxBytes:    cfg.Cluster.CommitCoordinatorMaxBytes,
	})
	cleanup.Push("channel log db", func() error {
		if app.channelLogDB == nil {
			return nil
		}
		return app.channelLogDB.Close()
	})

	messageIDs, err := messageid.NewSnowflakeGenerator(cfg.Node.ID)
	if err != nil {
		return nil, fmt.Errorf("app: create message id generator: %w", err)
	}
	app.gatewayBootID, err = newGatewayBootID()
	if err != nil {
		return nil, fmt.Errorf("app: create gateway boot id: %w", err)
	}

	discovery := app.cluster.Discovery()
	if discovery == nil {
		return nil, fmt.Errorf("app: cluster discovery not initialized")
	}
	poolSize := effectiveDataPlanePoolSize(cfg.Cluster.PoolSize, cfg.Cluster.DataPlanePoolSize)
	dialTimeout := cfg.Cluster.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultDataPlaneDialTimeout
	}
	dataPlanePoolOwnedByClient := false
	replicationCfg := cfg.Cluster.replicationConfig()
	app.dataPlanePool = transport.NewPool(transport.PoolConfig{
		Discovery:   discovery,
		Size:        poolSize,
		DialTimeout: dialTimeout,
		Observer:    transportObserver,
	})
	cleanup.Push("data-plane pool", func() error {
		if dataPlanePoolOwnedByClient || app.dataPlanePool == nil {
			return nil
		}
		app.dataPlanePool.Close()
		return nil
	})
	if dynamicDiscovery, ok := discovery.(interface {
		OnAddressChange(func(nodeID uint64, oldAddr, newAddr string)) func()
	}); ok {
		cancelDiscoveryWatch := dynamicDiscovery.OnAddressChange(func(nodeID uint64, _, _ string) {
			app.dataPlanePool.ClosePeer(nodeID)
		})
		cleanup.Push("data-plane discovery watch", func() error {
			cancelDiscoveryWatch()
			return nil
		})
	}
	app.dataPlaneClient = transport.NewClient(app.dataPlanePool)
	dataPlanePoolOwnedByClient = true
	cleanup.Push("data-plane client", func() error {
		if app.dataPlaneClient == nil {
			return nil
		}
		app.dataPlaneClient.Stop()
		return nil
	})
	app.isrTransport, err = channeltransport.New(channeltransport.Options{
		LocalNode:           channel.NodeID(cfg.Node.ID),
		Client:              app.dataPlaneClient,
		RPCMux:              app.cluster.RPCMux(),
		RPCTimeout:          cfg.Cluster.DataPlaneRPCTimeout,
		MaxPendingFetchRPC:  effectiveDataPlaneMaxPendingFetch(cfg.Cluster.PoolSize, cfg.Cluster.DataPlaneMaxPendingFetch),
		LongPollLaneCount:   replicationCfg.LongPollLaneCount,
		LongPollMaxWait:     replicationCfg.LongPollMaxWait,
		LongPollMaxBytes:    replicationCfg.LongPollMaxBytes,
		LongPollMaxChannels: replicationCfg.LongPollMaxChannels,
	})
	if err != nil {
		return nil, fmt.Errorf("app: create channel transport: %w", err)
	}
	channelTransportOwnedByChannelLog := false
	cleanup.Push("channel transport", func() error {
		if channelTransportOwnedByChannelLog || app.isrTransport == nil {
			return nil
		}
		return app.isrTransport.Close()
	})
	app.channelMetaSync = &channelMetaSync{}
	if cfg.Cluster.ChannelExecutionMode == "pooled" {
		var executionObserver channelreplica.ExecutionObserver
		if app.metrics != nil {
			executionObserver = channelReplicaExecutionMetricsObserver{metrics: app.metrics.Channel}
		}
		app.replicaExecutionPool, err = channelreplica.NewExecutionPool(channelreplica.ExecutionPoolConfig{
			Workers:         cfg.Cluster.ChannelExecutionWorkers,
			MailboxSize:     cfg.Cluster.ChannelExecutionQueueSize,
			EffectQueueSize: cfg.Cluster.ChannelExecutionQueueSize,
			Observer:        executionObserver,
			Logger:          app.logger.Named("channel.replica.execution"),
		})
		if err != nil {
			return nil, fmt.Errorf("app: create channel replica execution pool: %w", err)
		}
		cleanup.Push("channel replica execution pool", app.replicaExecutionPool.Close)
	}
	replicaFactory := newChannelReplicaFactory(app.channelLogDB, channel.NodeID(cfg.Node.ID), nil, cfg.Cluster.AppendGroupCommitMaxWait, cfg.Cluster.AppendGroupCommitMaxRecords, cfg.Cluster.AppendGroupCommitMaxBytes, app.logger.Named("channel"))
	replicaFactory.setExecutionPool(cfg.Cluster.ChannelExecutionMode, cfg.Cluster.ChannelExecutionWorkers, cfg.Cluster.ChannelExecutionQueueSize, app.replicaExecutionPool)
	replicaFactory.onStateChange = app.channelMetaSync.enqueueLocalReplicaStateChange
	app.isrRuntime, err = channelruntime.New(channelruntime.Config{
		LocalNode:                        channel.NodeID(cfg.Node.ID),
		ReplicaFactory:                   replicaFactory,
		GenerationStore:                  newMemoryGenerationStore(),
		Activator:                        app.channelMetaSync,
		Transport:                        app.isrTransport,
		PeerSessions:                     app.isrTransport,
		AutoRunScheduler:                 true,
		FollowerReplicationRetryInterval: cfg.Cluster.FollowerReplicationRetryInterval,
		LongPollLaneCount:                replicationCfg.LongPollLaneCount,
		LongPollMaxWait:                  replicationCfg.LongPollMaxWait,
		LongPollMaxBytes:                 replicationCfg.LongPollMaxBytes,
		LongPollMaxChannels:              replicationCfg.LongPollMaxChannels,
		LongPollHWOnlyNotifyDelay:        defaultChannelLongPollHWOnlyNotifyDelay,
		LongPollDataNotifyDelay:          cfg.Cluster.LongPollDataNotifyDelay,
		IdleEviction: channelruntime.IdleEvictionPolicy{
			IdleTimeout:  cfg.Cluster.ChannelIdleTimeout,
			ScanInterval: cfg.Cluster.ChannelIdleScanInterval,
		},
		OnIdleEvict: func(key channel.ChannelKey) {
			if app.channelLog != nil {
				app.channelLog.setChannelActive(key, false)
			}
			if app.metrics != nil && app.metrics.Channel != nil {
				app.metrics.Channel.ObserveIdleEvict()
			}
		},
		OnActivationReject: func(_ channel.ChannelKey, err error) {
			if app.metrics == nil || app.metrics.Channel == nil {
				return
			}
			reason := "err"
			if errors.Is(err, channelruntime.ErrTooManyChannels) {
				reason = "too_many_channels"
			}
			app.metrics.Channel.ObserveActivationRejected(reason)
		},
		Limits: channelruntime.Limits{
			MaxChannels:               cfg.Cluster.MaxChannels,
			MaxFetchInflightPeer:      effectiveDataPlaneMaxFetchInflight(cfg.Cluster.PoolSize, cfg.Cluster.DataPlaneMaxFetchInflight),
			MaxSnapshotInflight:       1,
			MaxRecoveryBytesPerSecond: 0,
		},
		Tombstones: channelruntime.TombstonePolicy{
			TombstoneTTL:    time.Minute,
			CleanupInterval: time.Minute,
		},
		Now:    time.Now,
		Logger: app.logger.Named("channel.runtime"),
	})
	if err != nil {
		return nil, fmt.Errorf("app: create channel runtime: %w", err)
	}
	isrRuntimeOwnedByChannelLog := false
	cleanup.Push("isr runtime", func() error {
		if isrRuntimeOwnedByChannelLog || app.isrRuntime == nil {
			return nil
		}
		return app.isrRuntime.Close()
	})
	app.channelLog, err = newAppChannelCluster(app.channelLogDB, app.isrRuntime, app.isrTransport, messageIDs, cfg.Node.ID, app.logger)
	if err != nil {
		return nil, fmt.Errorf("app: create channel cluster: %w", err)
	}
	channelTransportOwnedByChannelLog = true
	isrRuntimeOwnedByChannelLog = true
	cleanup.Push("app channel cluster", func() error {
		if app.channelLog == nil {
			return nil
		}
		return app.channelLog.Close()
	})
	app.channelLog.metrics = app.metrics
	if buildAfterChannelRuntimeHook != nil {
		if err := buildAfterChannelRuntimeHook(app); err != nil {
			return nil, fmt.Errorf("app: after channel runtime build: %w", err)
		}
	}

	app.store = metastore.New(app.cluster, app.db)
	app.channelApp = channelusecase.New(channelusecase.Options{Store: app.store})
	app.channelRetentionWorker = newAppChannelRetentionWorker(
		resolveAppChannelRetentionConfig(cfg),
		cfg.Node.ID,
		app.channelLogDB,
		app.isrRuntime,
		app.store,
		app.logger.Named("channel.retention"),
	)
	app.nodeClient = accessnode.NewClient(app.cluster)
	app.channelLog.remoteAppender = app.nodeClient
	var pluginSystemUIDs *pluginSystemUIDCheckerRef
	if cfg.Plugin.Enable {
		pluginSystemUIDs = &pluginSystemUIDCheckerRef{}
		if err := app.buildPluginSubsystem(cfg, pluginSystemUIDs); err != nil {
			return nil, fmt.Errorf("app: create plugin subsystem: %w", err)
		}
	}
	app.cmdConversationUpdater = cmdsync.NewConversationUpdater(cmdsync.ConversationUpdaterOptions{
		Store:          app.store,
		DataDir:        cfg.Node.DataDir,
		FlushInterval:  cfg.Conversation.ActiveHintFlushInterval,
		FlushBatchSize: cfg.Conversation.ActiveHintFlushBatchSize,
		Logger:         app.logger.Named("cmdsync.pending"),
	})
	cmdIntentRouter := cmdConversationIntentRouter{
		local:       app.cmdConversationUpdater,
		remote:      app.nodeClient,
		cluster:     app.cluster,
		localNodeID: cfg.Node.ID,
		logger:      app.logger.Named("cmdsync.intent"),
	}
	app.cmdConversationIntents = cmdIntentRouter
	app.cmdSyncApp = cmdsync.New(cmdsync.Options{
		States: app.store,
		Messages: cmdsyncMessageStore{
			localNodeID: cfg.Node.ID,
			channelLog:  app.channelLogDB,
			metas:       app.store,
			remote:      app.nodeClient,
		},
		Pending: app.cmdConversationUpdater,
		Logger:  app.logger.Named("cmdsync"),
	})
	repairProbeClient, err := channeltransport.NewProbeClient(channeltransport.ProbeClientOptions{
		Client:     app.dataPlaneClient,
		RPCTimeout: cfg.Cluster.DataPlaneRPCTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("app: create channel repair probe client: %w", err)
	}
	app.channelMigrationExecutor, app.channelMigrationLifecycle = newAppChannelMigrationExecutor(
		cfg,
		cfg.Node.ID,
		appChannelMigrationStore{Store: app.store},
		appChannelMigrationSlots{cluster: app.cluster},
		appChannelMigrationProbeClient{client: repairProbeClient, localNode: channel.NodeID(cfg.Node.ID)},
		app.isrTransport,
		app.logger,
	)
	channelLeaderEvaluator := runtimechannelmeta.NewLeaderPromotionEvaluator(runtimechannelmeta.LeaderPromotionEvaluatorOptions{
		DB:        app.channelLogDB,
		LocalNode: cfg.Node.ID,
		Probe:     repairProbeClient,
	})
	channelLeaderRepairer := runtimechannelmeta.NewLeaderRepairer(runtimechannelmeta.LeaderRepairerOptions{
		Store:     app.store,
		Cluster:   app.cluster,
		Remote:    app.nodeClient,
		Evaluator: channelLeaderEvaluator,
		LocalNode: cfg.Node.ID,
		Now:       time.Now,
		ApplyAuthoritative: func(meta metadb.ChannelRuntimeMeta) error {
			_, err := app.channelMetaSync.applyAuthoritativeMeta(meta)
			return err
		},
		RepairPolicy: app.channelMetaSync.needsLeaderRepair,
	})
	var channelMetaObserver runtimechannelmeta.MetaRefreshObserver
	if app.metrics != nil {
		channelMetaObserver = channelMetaMetricsObserver{metrics: app.metrics.Message}
	}
	app.channelMetaSync.resolver = runtimechannelmeta.NewSync(runtimechannelmeta.SyncOptions{
		Source: app.store,
		Runtime: channelMetaRuntimeAdapter{
			routing:  app.channelLog,
			local:    app.channelLog,
			observer: app.isrRuntime,
		},
		Bootstrapper: runtimechannelmeta.NewBootstrapper(runtimechannelmeta.BootstrapOptions{
			Cluster:       app.cluster,
			Store:         app.store,
			DefaultMinISR: cfg.Cluster.ChannelBootstrapDefaultMinISR,
			Now:           time.Now,
			Logger:        app.logger,
		}),
		Cluster:             app.cluster,
		Repairer:            channelLeaderRepairer,
		RepairPolicy:        app.channelMetaSync.needsLeaderRepair,
		LivenessSource:      app.cluster,
		LocalNode:           cfg.Node.ID,
		RefreshInterval:     time.Second,
		Now:                 time.Now,
		AfterLocalApply:     app.channelMetaSync.scheduleLeaderRepairForMeta,
		MetaRefreshObserver: channelMetaObserver,
	})
	app.channelPlane, err = runtimechannelplane.New(runtimechannelplane.Options{
		LocalNode:        channel.NodeID(cfg.Node.ID),
		ReactorCount:     cfg.ChannelPlane.ReactorCount,
		PeerLaneCount:    cfg.ChannelPlane.PeerLaneCount,
		PeerBatchMaxWait: cfg.ChannelPlane.PeerBatchMaxWait,
		PeerRPCTimeout:   cfg.Cluster.DataPlaneRPCTimeout,
		Resolver:         appChannelPlaneRouteResolver{meta: app.channelMetaSync},
		LocalOwner:       app.channelLog,
		PeerClient:       app.nodeClient,
	})
	if err != nil {
		return nil, fmt.Errorf("app: create channel plane: %w", err)
	}
	app.conversationActiveHints = conversationusecase.NewActiveHintCache(conversationusecase.ActiveHintCacheOptions{
		Store:          app.store,
		FlushInterval:  cfg.Conversation.ActiveHintFlushInterval,
		HintTTL:        cfg.Conversation.ActiveHintTTL,
		BarrierTTL:     cfg.Conversation.ActiveHintBarrierTTL,
		MaxHints:       cfg.Conversation.ActiveHintMaxHints,
		MaxHintsPerUID: cfg.Conversation.ActiveHintMaxHintsPerUID,
		FlushBatchSize: cfg.Conversation.ActiveHintFlushBatchSize,
		Logger:         app.logger.Named("conversation.active_hint"),
	})
	app.store.RegisterUserConversationActiveOverlay(app.conversationActiveHints)
	app.conversationProjector = conversationusecase.NewProjector(conversationusecase.ProjectorOptions{
		Store:                           app.store,
		FlushInterval:                   cfg.Conversation.FlushInterval,
		DirtyLimit:                      cfg.Conversation.FlushDirtyLimit,
		ColdThreshold:                   cfg.Conversation.ColdThreshold,
		SubscriberPageSize:              cfg.Conversation.SubscriberPageSize,
		GroupActiveFanoutInterval:       cfg.Conversation.GroupActiveFanoutInterval,
		GroupActiveFanoutMaxSubscribers: cfg.Conversation.GroupActiveFanoutMaxSubscribers,
		Logger:                          app.logger.Named("conversation.projector"),
	})
	app.conversationApp = conversationusecase.New(conversationusecase.Options{
		States: app.store,
		Facts: channelLogConversationFacts{
			cluster: app.channelLog,
			metas:   app.store,
			remote:  app.nodeClient,
		},
		Now:                   time.Now,
		ColdThreshold:         cfg.Conversation.ColdThreshold,
		ActiveScanLimit:       cfg.Conversation.ActiveScanLimit,
		ChannelProbeBatchSize: cfg.Conversation.ChannelProbeBatchSize,
		Logger:                app.logger.Named("conversation"),
	})
	onlineRegistry := online.NewRegistry()
	app.onlineRegistry = onlineRegistry
	runtimeSummaries := runtimeSummaryCollector{app: app}
	authorityClient := &presenceAuthorityClient{
		cluster:     app.cluster,
		remote:      app.nodeClient,
		localNodeID: cfg.Node.ID,
	}
	app.presenceApp = presence.New(presence.Options{
		LocalNodeID:      cfg.Node.ID,
		GatewayBootID:    app.gatewayBootID,
		Online:           onlineRegistry,
		Router:           presenceRouter{cluster: app.cluster},
		AuthorityClient:  authorityClient,
		ActionDispatcher: authorityClient,
		Logger:           app.logger.Named("presence"),
	})
	authorityClient.local = app.presenceApp
	app.presenceWorker = newPresenceWorker(app.presenceApp, 0)
	app.presenceWorker.activeSlotIDs = func() []uint64 {
		slots := onlineRegistry.ActiveSlots()
		out := make([]uint64, 0, len(slots))
		for _, slot := range slots {
			out = append(out, slot.SlotID)
		}
		return out
	}
	app.presenceWorker.leaderOf = func(slotID uint64) (uint64, error) {
		leaderID, err := app.cluster.LeaderOf(multiraft.SlotID(slotID))
		return uint64(leaderID), err
	}
	deliveryAuthority := presence.Authoritative(authorityClient)
	if cfg.Delivery.PresenceCacheTTL > 0 {
		deliveryAuthority = newDeliveryPresenceCache(authorityClient, cfg.Delivery.PresenceCacheTTL, defaultDeliveryPresenceCacheMaxEntries, time.Now)
	}
	app.deliveryAcks = deliveryruntime.NewAckIndex()
	deliveryTagManager := deliverytagruntime.NewManager(deliverytagruntime.Options{
		LocalNodeID: cfg.Node.ID,
		TTL:         time.Minute,
	})
	subscriberResolver := deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{
		Store: app.store,
	})
	cmdUIDObserver := cmdConversationResolvedUIDObserver{
		sink:   cmdIntentRouter,
		now:    time.Now,
		logger: app.logger.Named("cmdsync.intent"),
	}
	var deliveryObserver deliveryruntime.Observer
	var deliveryMetrics *obsmetrics.DeliveryMetrics
	if app.metrics != nil {
		deliveryMetrics = app.metrics.Delivery
		deliveryObserver = deliveryRuntimeMetricsObserver{metrics: deliveryMetrics}
	}
	var offlineResolvedObserver deliveryruntime.OfflineResolvedObserver
	if app.pluginApp != nil {
		app.pluginReceiveObserver = newPluginReceiveObserver(app.pluginApp, cfg.Plugin.Timeout, app.logger.Named("plugin.receive"))
		offlineResolvedObserver = app.pluginReceiveObserver
	}
	app.deliveryRuntime = deliveryruntime.NewManager(deliveryruntime.Config{
		ShardCount: deliveryShardCountForParallelism(runtime.GOMAXPROCS(0)),
		Resolver: tagDeliveryResolver{
			localNodeID:        cfg.Node.ID,
			tags:               deliveryTagManager,
			subscribers:        subscriberResolver,
			authority:          deliveryAuthority,
			topology:           deliveryTagTopologyReaderAdapter{cluster: app.cluster},
			uidObserver:        cmdUIDObserver,
			collectOfflineUIDs: offlineResolvedObserver != nil,
			metrics:            deliveryMetrics,
			logger:             app.logger.Named("delivery.resolve"),
		},
		Push: distributedDeliveryPush{
			localNodeID: cfg.Node.ID,
			local: localDeliveryPush{
				online:        onlineRegistry,
				localNodeID:   cfg.Node.ID,
				gatewayBootID: app.gatewayBootID,
				logger:        app.logger.Named("delivery.push.local"),
			},
			client:  app.nodeClient,
			metrics: deliveryMetrics,
			logger:  app.logger.Named("delivery.push.remote"),
		},
		Observer:                deliveryObserver,
		OfflineResolvedObserver: offlineResolvedObserver,
	})
	app.deliveryRuntimeLifecycle = newDeliveryRuntimeLifecycle(deliveryRuntimeLifecycleConfig{
		Runtime:  app.deliveryRuntime,
		Observer: deliveryObserver,
	})
	app.deliveryApp = deliveryusecase.New(deliveryusecase.Options{
		Runtime: app.deliveryRuntime,
		Logger:  app.logger.Named("delivery"),
	})
	var messageMetrics *obsmetrics.MessageMetrics
	if app.metrics != nil {
		messageMetrics = app.metrics.Message
	}
	app.committedDispatcher = newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID:  cfg.Node.ID,
		Logger:       app.logger.Named("delivery.route"),
		ChannelLog:   app.channelLog,
		Delivery:     app.deliveryApp,
		Conversation: app.conversationProjector,
		NodeClient:   app.nodeClient,
		Metrics:      messageMetrics,
	})
	app.committedReplayer = newCommittedReplayer(committedReplayerConfig{
		Log: channelStoreCommittedReplayLog{
			engine:      app.channelLogDB,
			status:      app.channelLog,
			localNodeID: cfg.Node.ID,
		},
		Delivery:     app.deliveryApp,
		Conversation: app.conversationProjector,
		Logger:       app.logger.Named("committed.replay"),
		Metrics:      messageMetrics,
	})
	deliverySubmitDispatcher := committedFanout{subscribers: []committedSubscriber{app.committedDispatcher, app.committedReplayer}}
	committedSubscribers := []committedSubscriber{app.committedDispatcher, app.committedReplayer}
	if app.pluginApp != nil {
		committedSubscribers = append(committedSubscribers, pluginCommittedRouter{
			localNodeID:   cfg.Node.ID,
			channelLog:    app.channelLog,
			nodeClient:    app.nodeClient,
			pluginUsecase: app.pluginApp,
			logger:        app.logger.Named("plugin.committed"),
		})
	}
	committedDispatcher := committedFanout{subscribers: committedSubscribers}
	managerRetention := &managerMessageRetentionOperator{
		localNodeID: cfg.Node.ID,
		metas:       app.store,
		runtime:     app.isrRuntime,
		stores:      managerMessageRetentionStores{engine: app.channelLogDB},
		metadata:    app.store,
		remote:      app.nodeClient,
		now:         time.Now,
	}
	userApp := userusecase.New(userusecase.Options{
		Users:        app.store,
		Devices:      app.store,
		DeviceReader: app.store,
		Presence:     authorityClient,
		SystemUIDs:   app.store,
		Online:       onlineRegistry,
		Logger:       app.logger.Named("user"),
	})
	if pluginSystemUIDs != nil {
		pluginSystemUIDs.target = userApp
	}
	app.userApp = userApp
	var pluginHTTPRoutes accessnode.PluginHTTPRouteProvider
	var pluginManagement accessnode.PluginManagementProvider
	var pluginCommitted accessnode.PluginCommittedProvider
	var pluginRoutes accessapi.PluginRouteUsecase
	var sendHook message.SendHook
	if app.pluginApp != nil {
		pluginHTTPRoutes = app.pluginApp
		pluginManagement = app.pluginApp
		pluginCommitted = app.pluginApp
		pluginRoutes = app.pluginApp
		sendHook = app.pluginApp
	}
	clusterUsers := &clusterUserUsecase{
		local:       userApp,
		remote:      app.nodeClient,
		localNodeID: cfg.Node.ID,
		peerNodeIDs: controllerPeerIDs(cfg.Cluster.DerivedControllerNodes(), cfg.Cluster.runtimeSeeds()),
	}
	app.nodeAccess = accessnode.New(accessnode.Options{
		Cluster:               app.cluster,
		Presence:              app.presenceApp,
		Online:                onlineRegistry,
		GatewayBootID:         app.gatewayBootID,
		LocalNodeID:           cfg.Node.ID,
		ChannelLog:            app.channelLog,
		ChannelLogDB:          app.channelLogDB,
		ChannelMeta:           app.channelMetaSync,
		DeliverySubmit:        deliveryRuntimeCommittedSubmitter{target: deliverySubmitDispatcher},
		DeliveryAck:           app.deliveryApp,
		DeliveryOffline:       app.deliveryApp,
		DeliveryTag:           deliveryTagAuthority{tags: deliveryTagManager},
		DeliveryAckIndex:      app.deliveryAcks,
		ChannelLeaderRepair:   channelLeaderRepairer,
		ChannelLeaderTransfer: channelLeaderRepairer,
		ChannelLeaderEvaluate: channelLeaderEvaluator,
		RuntimeSummary:        nodeRuntimeSummaryProvider{collector: runtimeSummaries},
		MonitorMetrics:        nodeMonitorMetricsProvider{collector: app.dashboardCollector},
		Diagnostics:           app,
		DiagnosticsTracking:   app,
		ChannelRetention:      managerMessageRetentionNodeProvider{target: managerRetention},
		CMDSync:               app.cmdSyncApp,
		CMDConversationIntents: ownerValidatingCMDIntentSink{
			local:       app.cmdConversationUpdater,
			cluster:     app.cluster,
			localNodeID: cfg.Node.ID,
		},
		SystemUIDCache:   userApp,
		PluginHTTPRoutes: pluginHTTPRoutes,
		PluginManagement: pluginManagement,
		PluginCommitted:  pluginCommitted,
		Logger:           app.logger.Named("access.node"),
	})
	deliveryNotifier := deliveryOwnerNotifier(app.nodeClient)
	if cfg.Delivery.AckBatchMaxWait > 0 && cfg.Delivery.AckBatchMaxSize != 1 {
		ackBatcher := newDeliveryAckBatchNotifier(app.nodeClient, deliveryAckBatchNotifierOptions{
			FlushDelay: cfg.Delivery.AckBatchMaxWait,
			MaxBatch:   cfg.Delivery.AckBatchMaxSize,
		})
		cleanup.Push("delivery ack batcher", func() error {
			ackBatcher.Close()
			return nil
		})
		deliveryNotifier = ackBatcher
	}

	var userSendLimiter message.UserSendLimiter
	if cfg.Message.UserRateLimitEnabled {
		limiter := userlimit.New(userlimit.Config{
			Enabled:         true,
			RatePerSecond:   cfg.Message.UserRateLimitRate,
			Burst:           cfg.Message.UserRateLimitBurst,
			BucketShards:    cfg.Message.UserRateLimitBucketShards,
			IdleTTL:         cfg.Message.UserRateLimitIdleTTL,
			MaxBuckets:      cfg.Message.UserRateLimitMaxBuckets,
			SystemUIDBypass: cfg.Message.UserRateLimitSystemUIDBypass,
			PluginBypass:    cfg.Message.UserRateLimitPluginBypass,
		})
		stopLimiter := limiter.StartJanitor(userRateLimitJanitorInterval(cfg.Message.UserRateLimitIdleTTL))
		cleanup.Push("user send rate limiter", func() error {
			stopLimiter()
			return nil
		})
		userSendLimiter = limiter
	}

	app.messageApp = message.New(message.Options{
		IdentityStore:          app.store,
		ChannelStore:           app.store,
		PermissionStore:        app.store,
		SystemUIDs:             userApp,
		UserSendLimiter:        userSendLimiter,
		PersonWhitelistEnabled: cfg.Message.PersonWhitelistEnabled,
		SystemDeviceID:         cfg.Message.SystemDeviceID,
		PermissionCacheTTL:     cfg.Message.PermissionCacheTTL,
		ChannelAppender:        app.channelPlane,
		MessageReader: managerMessageReader{
			localNodeID: cfg.Node.ID,
			channelLog:  app.channelLogDB,
			metas:       app.store,
			remote:      app.nodeClient,
		},
		AppendMetrics:          messageMetrics,
		Online:                 onlineRegistry,
		CommittedDispatcher:    committedDispatcher,
		CMDConversationIntents: cmdIntentRouter,
		RealtimeDispatcher:     app.deliveryApp,
		SendHook:               sendHook,
		MessageIDs:             messageIDs,
		DeliveryAck: ackRouting{
			localNodeID: cfg.Node.ID,
			local:       app.deliveryApp,
			remoteAcks:  app.deliveryAcks,
			notifier:    deliveryNotifier,
		},
		DeliveryOffline: offlineRouting{
			localNodeID: cfg.Node.ID,
			local:       app.deliveryApp,
			remoteAcks:  app.deliveryAcks,
			notifier:    app.nodeClient,
		},
		LocalNodeID: cfg.Node.ID,
		Logger:      app.logger.Named("message"),
	})
	if cfg.Manager.ListenAddr != "" {
		var pluginNodes managementusecase.PluginNodeClient
		var pluginBindings managementusecase.PluginBindingUsecase
		if app.pluginApp != nil {
			pluginNodes = pluginManagementNodeClient{
				localNodeID: cfg.Node.ID,
				local:       app.pluginApp,
				remote:      app.nodeClient,
			}
			pluginBindings = app.pluginApp
		}
		app.managementApp = managementusecase.New(managementusecase.Options{
			LocalNodeID:       cfg.Node.ID,
			ControllerPeerIDs: controllerPeerIDs(cfg.Cluster.DerivedControllerNodes(), cfg.Cluster.runtimeSeeds()),
			SlotReplicaN:      cfg.Cluster.SlotReplicaN,
			Cluster:           app.cluster,
			Online:            onlineRegistry,
			RuntimeSummary:    managementRuntimeSummaryReader{collector: runtimeSummaries, nodeClient: app.nodeClient},
			MonitorMetrics: managementMonitorMetricsReader{
				localNodeID: cfg.Node.ID,
				collector:   app.dashboardCollector,
				remote:      app.nodeClient,
			},
			Connections: managementConnectionReader{nodeClient: app.nodeClient},
			Diagnostics: managementDiagnosticsReader{
				localNodeID: cfg.Node.ID,
				local:       app,
				remote:      app.nodeClient,
			},
			DiagnosticsTracking: managementDiagnosticsTrackingReader{
				localNodeID: cfg.Node.ID,
				local:       app,
				remote:      app.nodeClient,
			},
			ChannelRuntimeMeta:      app.store,
			Users:                   app.store,
			ChannelBusinessReader:   app.store,
			ChannelBusinessOperator: app.channelApp,
			UserOperator:            userApp,
			SystemUsers:             clusterUsers,
			UserPresence:            authorityClient,
			UserActions:             authorityClient,
			Conversations:           app.conversationApp,
			ChannelReplicaStatus: managerChannelReplicaStatusReader{
				channelLog: app.channelLog,
			},
			ChannelLeaderRepair: managerChannelLeaderRepairOperator{
				metas:    app.store,
				repairer: channelLeaderRepairer,
			},
			ChannelLeaderTransfer: managerChannelLeaderTransferOperator{
				metas:      app.store,
				transferer: channelLeaderRepairer,
			},
			ChannelMigration:   app.store,
			Network:            app.networkObservability,
			MessageRetention:   managerRetention,
			DashboardCollector: app.dashboardCollector,
			Messages: managerMessageReader{
				localNodeID: cfg.Node.ID,
				channelLog:  app.channelLogDB,
				metas:       app.store,
				remote:      app.nodeClient,
			},
			Plugins:        pluginNodes,
			PluginBindings: pluginBindings,
		})
		app.manager = accessmanager.New(accessmanager.Options{
			ListenAddr: cfg.Manager.ListenAddr,
			Auth: accessmanager.AuthConfig{
				On:        cfg.Manager.AuthOn,
				JWTSecret: cfg.Manager.JWTSecret,
				JWTIssuer: cfg.Manager.JWTIssuer,
				JWTExpire: cfg.Manager.JWTExpire,
				Users:     managerUserConfigs(cfg.Manager.Users),
			},
			Management: app.managementApp,
			Logger:     app.logger.Named("access.manager"),
		})
	}
	if cfg.API.ListenAddr != "" {
		legacyRouteExternal, legacyRouteIntranet := legacyRouteAddresses(cfg.API, cfg.Gateway.Listeners)
		legacyRouteNodes := legacyRouteNodeAddresses(cfg.Node.ID, cfg.Cluster.Nodes, legacyRouteExternal, legacyRouteIntranet)
		testDataApp := testdatausecase.New(testdatausecase.AppOptions{
			SlotSnapshotUsers:      app.store,
			ControllerSnapshotJobs: app.cluster,
		})
		cmdSyncAPI := clusterCMDSyncUsecase{
			local:       app.cmdSyncApp,
			remote:      app.nodeClient,
			cluster:     app.cluster,
			localNodeID: cfg.Node.ID,
		}
		var benchData accessapi.BenchDataUsecase
		if cfg.Bench.APIEnabled {
			benchData = benchdata.New(benchdata.Config{
				Users:           benchUserWriter{users: clusterUsers},
				Channels:        benchChannelWriter{channels: app.channelApp},
				MaxBatchSize:    cfg.Bench.APIMaxBatchSize,
				MaxPayloadBytes: cfg.Bench.APIMaxPayloadBytes,
			})
		}
		app.api = accessapi.New(accessapi.Options{
			ListenAddr:               cfg.API.ListenAddr,
			Messages:                 app.messageApp,
			CMDSync:                  cmdSyncAPI,
			Users:                    clusterUsers,
			Channels:                 app.channelApp,
			TestMode:                 cfg.TestMode,
			TestData:                 testDataApp,
			Conversations:            app.conversationApp,
			ConversationDefaultLimit: cfg.Conversation.SyncDefaultLimit,
			ConversationMaxLimit:     cfg.Conversation.SyncMaxLimit,
			MetricsHandler:           app.metricsHandler(),
			HealthDetailEnabled:      cfg.Observability.HealthDetailEnabled,
			HealthDetails:            app.healthDetailsSnapshot,
			Readyz:                   app.readyzReport,
			DebugEnabled:             cfg.Observability.HealthDebugEnabled,
			BenchEnabled:             cfg.Bench.APIEnabled,
			BenchData:                benchData,
			BenchMaxBatchSize:        cfg.Bench.APIMaxBatchSize,
			BenchMaxPayloadBytes:     cfg.Bench.APIMaxPayloadBytes,
			DebugConfig:              app.debugConfigSnapshot,
			DebugCluster:             app.debugClusterSnapshot,
			DiagnosticsDebugEnabled:  cfg.Observability.Diagnostics.Enabled && cfg.Observability.Diagnostics.DebugAPIEnabled,
			Diagnostics:              app,
			PluginRoutes:             pluginRoutes,
			LegacyRouteExternal:      legacyRouteExternal,
			LegacyRouteIntranet:      legacyRouteIntranet,
			LegacyRouteNodes:         legacyRouteNodes,
			Logger:                   app.logger.Named("access.api"),
		})
	}
	app.gatewayHandler = accessgateway.New(accessgateway.Options{
		LocalNodeID: cfg.Node.ID,
		Online:      onlineRegistry,
		Messages:    app.messageApp,
		Presence:    app.presenceApp,
		SendTimeout: cfg.Gateway.SendTimeout,
		Logger:      app.logger.Named("access.gateway"),
	})

	var gatewayObserver gateway.Observer
	if app.metrics != nil {
		gatewayObserver = gatewayMetricsObserver{metrics: app.metrics}
	}
	app.gateway, err = gateway.New(gateway.Options{
		Handler:        app.gatewayHandler,
		Authenticator:  gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{TokenAuthOn: cfg.Gateway.TokenAuthOn, NodeID: cfg.Node.ID}),
		Observer:       gatewayObserver,
		DefaultSession: cfg.Gateway.DefaultSession,
		Transport:      cfg.Gateway.Transport,
		Listeners:      cfg.Gateway.Listeners,
		Logger:         app.logger.Named("gateway"),
	})
	if err != nil {
		return nil, fmt.Errorf("app: create gateway: %w", err)
	}
	app.updateGatewayAdmissionFromDrainState()

	cleanup.Release()
	return app, nil
}

func deliveryShardCountForParallelism(parallelism int) int {
	if parallelism <= 0 {
		parallelism = 1
	}
	return min(16, max(4, parallelism))
}

func dataPlaneMaxFetchInflightPeer(poolSize int) int {
	if poolSize > 2 {
		return poolSize
	}
	return 2
}

func effectiveDataPlanePoolSize(clusterPoolSize, configured int) int {
	if configured > 0 {
		return configured
	}
	if clusterPoolSize > 0 {
		return clusterPoolSize
	}
	return 1
}

func effectiveDataPlaneMaxFetchInflight(clusterPoolSize, configured int) int {
	if configured > 0 {
		return configured
	}
	return dataPlaneMaxFetchInflightPeer(clusterPoolSize)
}

func controllerPeerIDs(nodes []NodeConfigRef, seeds []raftcluster.SeedConfig) []uint64 {
	seen := make(map[uint64]struct{}, len(nodes)+len(seeds))
	peerIDs := make([]uint64, 0, len(nodes)+len(seeds))
	for _, node := range nodes {
		if node.ID == 0 {
			continue
		}
		if _, ok := seen[node.ID]; ok {
			continue
		}
		seen[node.ID] = struct{}{}
		peerIDs = append(peerIDs, node.ID)
	}
	for _, seed := range seeds {
		nodeID := uint64(seed.ID)
		if nodeID == 0 {
			continue
		}
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		peerIDs = append(peerIDs, nodeID)
	}
	sort.Slice(peerIDs, func(i, j int) bool { return peerIDs[i] < peerIDs[j] })
	return peerIDs
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

func legacyRouteAddresses(apiCfg APIConfig, listeners []gateway.ListenerOptions) (accessapi.LegacyRouteAddresses, accessapi.LegacyRouteAddresses) {
	external, intranet := legacyRouteAddressesFromListeners(listeners)
	if trimmed := strings.TrimSpace(apiCfg.ExternalTCPAddr); trimmed != "" {
		external.TCPAddr = trimmed
	}
	if trimmed := strings.TrimSpace(apiCfg.ExternalWSAddr); trimmed != "" {
		external.WSAddr = trimmed
	}
	if trimmed := strings.TrimSpace(apiCfg.ExternalWSSAddr); trimmed != "" {
		external.WSSAddr = trimmed
	}
	return external, intranet
}

func legacyRouteNodeAddresses(localNodeID uint64, nodes []NodeConfigRef, external, intranet accessapi.LegacyRouteAddresses) map[uint64]accessapi.LegacyRouteNodeAddresses {
	out := make(map[uint64]accessapi.LegacyRouteNodeAddresses, len(nodes)+1)
	if localNodeID != 0 {
		out[localNodeID] = accessapi.LegacyRouteNodeAddresses{External: external, Intranet: intranet}
	}
	for _, node := range nodes {
		if node.ID == 0 || node.ID == localNodeID {
			continue
		}
		host := legacyRouteNodeHost(node.Addr)
		if host == "" {
			continue
		}
		out[node.ID] = accessapi.LegacyRouteNodeAddresses{
			External: legacyRouteAddressesForHost(external, host),
			Intranet: legacyRouteAddressesForHost(intranet, host),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func legacyRouteAddressesForHost(addrs accessapi.LegacyRouteAddresses, host string) accessapi.LegacyRouteAddresses {
	return accessapi.LegacyRouteAddresses{
		TCPAddr: legacyRouteHostPort(addrs.TCPAddr, host),
		WSAddr:  legacyRouteURLHost(addrs.WSAddr, host),
		WSSAddr: legacyRouteURLHost(addrs.WSSAddr, host),
	}
}

func legacyRouteAddressesFromListeners(listeners []gateway.ListenerOptions) (accessapi.LegacyRouteAddresses, accessapi.LegacyRouteAddresses) {
	var external accessapi.LegacyRouteAddresses
	var intranet accessapi.LegacyRouteAddresses
	for _, listener := range listeners {
		network := strings.ToLower(strings.TrimSpace(listener.Network))
		switch network {
		case "websocket":
			addr := normalizeLegacyWebsocketAddress(listener.Address)
			switch {
			case strings.HasPrefix(strings.ToLower(addr), "wss://"):
				if external.WSSAddr == "" {
					external.WSSAddr = addr
				}
			case external.WSAddr == "":
				external.WSAddr = addr
			}
		default:
			if external.TCPAddr == "" {
				external.TCPAddr = normalizeLegacyTCPAddress(listener.Address)
			}
			if intranet.TCPAddr == "" {
				intranet.TCPAddr = normalizeLegacyTCPAddress(listener.Address)
			}
		}
	}
	return external, intranet
}

func legacyRouteNodeHost(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		return legacyRouteHostOnly(parsed.Host)
	}
	trimmed = strings.TrimPrefix(trimmed, "tcp://")
	return legacyRouteHostOnly(trimmed)
}

func legacyRouteHostOnly(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	if strings.Contains(err.Error(), "missing port in address") {
		return strings.Trim(addr, "[]")
	}
	return ""
}

func legacyRouteHostPort(addr string, host string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" || strings.TrimSpace(host) == "" {
		return trimmed
	}
	_, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		return trimmed
	}
	return net.JoinHostPort(host, port)
}

func legacyRouteURLHost(addr string, host string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" || strings.TrimSpace(host) == "" {
		return trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err == nil {
		parsed.Host = net.JoinHostPort(host, port)
		return parsed.String()
	}
	if strings.Contains(err.Error(), "missing port in address") {
		parsed.Host = host
		return parsed.String()
	}
	return trimmed
}

func normalizeLegacyTCPAddress(addr string) string {
	trimmed := strings.TrimSpace(addr)
	trimmed = strings.TrimPrefix(trimmed, "tcp://")
	return trimmed
}

func normalizeLegacyWebsocketAddress(addr string) string {
	trimmed := strings.TrimSpace(addr)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") || trimmed == "" {
		return trimmed
	}
	return "ws://" + trimmed
}

func effectiveDataPlaneMaxPendingFetch(clusterPoolSize, configured int) int {
	if configured > 0 {
		return configured
	}
	return dataPlaneMaxFetchInflightPeer(clusterPoolSize)
}

func (c ClusterConfig) runtimeConfig(storage StorageConfig, db *metadb.DB, raftDB *raftstorage.DB, nodeID uint64, nodeName string, logger wklog.Logger) raftcluster.Config {
	cfg := raftcluster.Config{
		NodeID:                       multiraft.NodeID(nodeID),
		Name:                         nodeName,
		ListenAddr:                   c.ListenAddr,
		SlotCount:                    c.SlotCount,
		HashSlotCount:                c.HashSlotCount,
		EnableHashSlotMigration:      c.EnableHashSlotMigration,
		InitialSlotCount:             c.InitialSlotCount,
		ControllerMetaPath:           storage.ControllerMetaPath,
		ControllerRaftPath:           storage.ControllerRaftPath,
		ControllerRaftSnapshotPath:   storage.ControllerRaftSnapshotPath,
		RaftSnapshotChunkSize:        storage.RaftSnapshotChunkSize,
		RaftSnapshotGCGrace:          storage.RaftSnapshotGCGrace,
		ControllerReplicaN:           c.ControllerReplicaN,
		SlotReplicaN:                 c.SlotReplicaN,
		NewStorage:                   newStorageFactory(raftDB),
		NewStateMachine:              metafsm.NewStateMachineFactory(db),
		NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(db),
		Nodes:                        c.runtimeNodes(),
		Seeds:                        c.runtimeSeeds(),
		AdvertiseAddr:                c.AdvertiseAddr,
		JoinToken:                    c.JoinToken,
		ForwardTimeout:               c.ForwardTimeout,
		PoolSize:                     c.PoolSize,
		TickInterval:                 c.TickInterval,
		RaftWorkers:                  c.RaftWorkers,
		ElectionTick:                 c.ElectionTick,
		HeartbeatTick:                c.HeartbeatTick,
		DialTimeout:                  c.DialTimeout,
		ControllerLogCompaction: controllerraft.LogCompactionConfig{
			Enabled:        c.ControllerLogCompaction.Enabled,
			EnabledSet:     true,
			TriggerEntries: c.ControllerLogCompaction.TriggerEntries,
			CheckInterval:  c.ControllerLogCompaction.CheckInterval,
		},
		SlotLogCompaction: multiraft.LogCompactionConfig{
			Enabled:        c.SlotLogCompaction.Enabled,
			EnabledSet:     true,
			TriggerEntries: c.SlotLogCompaction.TriggerEntries,
			CheckInterval:  c.SlotLogCompaction.CheckInterval,
		},
		Timeouts: c.Timeouts,
		Logger:   logger,
	}
	cfg.SetRaftSnapshotExplicitFlags(storage.raftSnapshotChunkSizeSet, storage.raftSnapshotGCGraceSet)
	return cfg
}

func (c ClusterConfig) replicationConfig() channel.Config {
	return channel.Config{
		LongPollLaneCount:   c.LongPollLaneCount,
		LongPollMaxWait:     c.LongPollMaxWait,
		LongPollMaxBytes:    c.LongPollMaxBytes,
		LongPollMaxChannels: c.LongPollMaxChannels,
	}
}

func (c ClusterConfig) runtimeNodes() []raftcluster.NodeConfig {
	nodes := make([]raftcluster.NodeConfig, 0, len(c.Nodes))
	for _, node := range c.Nodes {
		nodes = append(nodes, raftcluster.NodeConfig{
			NodeID: multiraft.NodeID(node.ID),
			Addr:   node.Addr,
		})
	}
	return nodes
}

func (c ClusterConfig) runtimeSeeds() []raftcluster.SeedConfig {
	seeds := make([]raftcluster.SeedConfig, 0, len(c.Seeds))
	for i, addr := range c.Seeds {
		seeds = append(seeds, raftcluster.SeedConfig{
			ID:   multiraft.NodeID(^uint64(0) - uint64(i)),
			Addr: addr,
		})
	}
	return seeds
}

const conversationFetchMaxBytes = 1 << 20

type channelLogConversationFacts struct {
	cluster interface {
		Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error)
		Fetch(ctx context.Context, req channel.FetchRequest) (channel.FetchResult, error)
	}
	metas interface {
		GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
	}
	remote interface {
		LoadLatestConversationMessage(ctx context.Context, nodeID uint64, key channel.ChannelID, maxBytes int) (channel.Message, bool, error)
		LoadRecentConversationMessages(ctx context.Context, nodeID uint64, key channel.ChannelID, limit, maxBytes int) ([]channel.Message, error)
	}
}

type batchConversationFactsRemote interface {
	LoadLatestConversationMessages(ctx context.Context, nodeID uint64, keys []channel.ChannelID, maxBytes int) (map[channel.ChannelID]channel.Message, error)
	LoadRecentConversationMessagesBatch(ctx context.Context, nodeID uint64, keys []channel.ChannelID, limit, maxBytes int) (map[channel.ChannelID][]channel.Message, error)
}

type batchConversationFactsMetas interface {
	BatchGetChannelRuntimeMetas(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelRuntimeMeta, error)
}

func (f channelLogConversationFacts) LoadLatestMessages(ctx context.Context, keys []conversationusecase.ConversationKey) (map[conversationusecase.ConversationKey]channel.Message, error) {
	out := make(map[conversationusecase.ConversationKey]channel.Message, len(keys))
	remoteKeys := make([]conversationusecase.ConversationKey, 0, len(keys))
	for _, key := range keys {
		channelID := channel.ChannelID{ID: key.ChannelID, Type: key.ChannelType}
		msg, ok, err := loadLatestConversationMessage(ctx, f.cluster, channelID, conversationFetchMaxBytes)
		switch {
		case err == nil || errors.Is(err, channel.ErrChannelNotFound):
			if ok {
				out[key] = msg
			}
		case errors.Is(err, channel.ErrStaleMeta):
			remoteKeys = append(remoteKeys, key)
		default:
			return nil, err
		}
	}
	if len(remoteKeys) == 0 {
		return out, nil
	}
	if remote, ok := f.remote.(batchConversationFactsRemote); ok {
		remoteMessages, err := f.loadRemoteLatestMessagesBatch(ctx, remote, remoteKeys)
		if err != nil {
			return nil, err
		}
		for key, msg := range remoteMessages {
			out[key] = msg
		}
		return out, nil
	}
	for _, key := range remoteKeys {
		msg, ok, err := f.loadRemoteLatestMessage(ctx, channel.ChannelID{ID: key.ChannelID, Type: key.ChannelType})
		if err != nil {
			return nil, err
		}
		if ok {
			out[key] = msg
		}
	}
	return out, nil
}

func (f channelLogConversationFacts) LoadRecentMessages(ctx context.Context, key conversationusecase.ConversationKey, limit int) ([]channel.Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	messagesByKey, err := f.LoadRecentMessagesBatch(ctx, []conversationusecase.ConversationKey{key}, limit)
	if err != nil {
		return nil, err
	}
	return messagesByKey[key], nil
}

func (f channelLogConversationFacts) LoadRecentMessagesBatch(ctx context.Context, keys []conversationusecase.ConversationKey, limit int) (map[conversationusecase.ConversationKey][]channel.Message, error) {
	if limit <= 0 {
		return map[conversationusecase.ConversationKey][]channel.Message{}, nil
	}
	out := make(map[conversationusecase.ConversationKey][]channel.Message, len(keys))
	remoteKeys := make([]conversationusecase.ConversationKey, 0, len(keys))
	for _, key := range keys {
		channelID := channel.ChannelID{ID: key.ChannelID, Type: key.ChannelType}
		messages, err := loadRecentConversationMessages(ctx, f.cluster, channelID, limit, conversationFetchMaxBytes)
		switch {
		case err == nil || errors.Is(err, channel.ErrChannelNotFound):
			out[key] = messages
		case errors.Is(err, channel.ErrStaleMeta):
			remoteKeys = append(remoteKeys, key)
		default:
			return nil, err
		}
	}
	if len(remoteKeys) == 0 {
		return out, nil
	}
	if remote, ok := f.remote.(batchConversationFactsRemote); ok {
		remoteMessages, err := f.loadRemoteRecentMessagesBatch(ctx, remote, remoteKeys, limit)
		if err != nil {
			return nil, err
		}
		for key, messages := range remoteMessages {
			out[key] = messages
		}
		return out, nil
	}
	for _, key := range remoteKeys {
		messages, err := f.loadRemoteRecentMessages(ctx, channel.ChannelID{ID: key.ChannelID, Type: key.ChannelType}, limit)
		if err != nil {
			return nil, err
		}
		out[key] = messages
	}
	return out, nil
}

func (f channelLogConversationFacts) loadRemoteLatestMessage(ctx context.Context, key channel.ChannelID) (channel.Message, bool, error) {
	ownerNodeID, err := f.ownerNodeID(ctx, key)
	if err != nil {
		return channel.Message{}, false, err
	}
	if ownerNodeID == 0 || f.remote == nil {
		return channel.Message{}, false, channel.ErrStaleMeta
	}
	return f.remote.LoadLatestConversationMessage(ctx, ownerNodeID, key, conversationFetchMaxBytes)
}

func (f channelLogConversationFacts) loadRemoteRecentMessages(ctx context.Context, key channel.ChannelID, limit int) ([]channel.Message, error) {
	ownerNodeID, err := f.ownerNodeID(ctx, key)
	if err != nil {
		return nil, err
	}
	if ownerNodeID == 0 || f.remote == nil {
		return nil, channel.ErrStaleMeta
	}
	return f.remote.LoadRecentConversationMessages(ctx, ownerNodeID, key, limit, conversationFetchMaxBytes)
}

func (f channelLogConversationFacts) loadRemoteLatestMessagesBatch(ctx context.Context, remote batchConversationFactsRemote, keys []conversationusecase.ConversationKey) (map[conversationusecase.ConversationKey]channel.Message, error) {
	grouped, err := f.groupConversationKeysByOwner(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[conversationusecase.ConversationKey]channel.Message, len(keys))
	for ownerNodeID, groupKeys := range grouped {
		channelKeys := make([]channel.ChannelID, 0, len(groupKeys))
		keyByChannel := make(map[channel.ChannelID]conversationusecase.ConversationKey, len(groupKeys))
		for _, key := range groupKeys {
			channelID := channel.ChannelID{ID: key.ChannelID, Type: key.ChannelType}
			channelKeys = append(channelKeys, channelID)
			keyByChannel[channelID] = key
		}
		messages, err := remote.LoadLatestConversationMessages(ctx, ownerNodeID, channelKeys, conversationFetchMaxBytes)
		if err != nil {
			return nil, err
		}
		for channelKey, msg := range messages {
			if key, ok := keyByChannel[channelKey]; ok {
				out[key] = msg
			}
		}
	}
	return out, nil
}

func (f channelLogConversationFacts) loadRemoteRecentMessagesBatch(ctx context.Context, remote batchConversationFactsRemote, keys []conversationusecase.ConversationKey, limit int) (map[conversationusecase.ConversationKey][]channel.Message, error) {
	grouped, err := f.groupConversationKeysByOwner(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[conversationusecase.ConversationKey][]channel.Message, len(keys))
	for ownerNodeID, groupKeys := range grouped {
		channelKeys := make([]channel.ChannelID, 0, len(groupKeys))
		keyByChannel := make(map[channel.ChannelID]conversationusecase.ConversationKey, len(groupKeys))
		for _, key := range groupKeys {
			channelID := channel.ChannelID{ID: key.ChannelID, Type: key.ChannelType}
			channelKeys = append(channelKeys, channelID)
			keyByChannel[channelID] = key
		}
		messagesByChannel, err := remote.LoadRecentConversationMessagesBatch(ctx, ownerNodeID, channelKeys, limit, conversationFetchMaxBytes)
		if err != nil {
			return nil, err
		}
		for channelKey, messages := range messagesByChannel {
			if key, ok := keyByChannel[channelKey]; ok {
				out[key] = append([]channel.Message(nil), messages...)
			}
		}
	}
	return out, nil
}

func (f channelLogConversationFacts) groupConversationKeysByOwner(ctx context.Context, keys []conversationusecase.ConversationKey) (map[uint64][]conversationusecase.ConversationKey, error) {
	grouped := make(map[uint64][]conversationusecase.ConversationKey, len(keys))
	if metas, ok := f.metas.(batchConversationFactsMetas); ok {
		metaKeys := make([]metadb.ConversationKey, 0, len(keys))
		convKeysByMeta := make(map[metadb.ConversationKey]conversationusecase.ConversationKey, len(keys))
		for _, key := range keys {
			metaKey := metadb.ConversationKey{ChannelID: key.ChannelID, ChannelType: int64(key.ChannelType)}
			metaKeys = append(metaKeys, metaKey)
			convKeysByMeta[metaKey] = key
		}
		metasByKey, err := metas.BatchGetChannelRuntimeMetas(ctx, metaKeys)
		if err != nil {
			return nil, err
		}
		for _, metaKey := range metaKeys {
			meta, ok := metasByKey[metaKey]
			if !ok || meta.Leader == 0 {
				return nil, channel.ErrStaleMeta
			}
			grouped[meta.Leader] = append(grouped[meta.Leader], convKeysByMeta[metaKey])
		}
		return grouped, nil
	}
	for _, key := range keys {
		ownerNodeID, err := f.ownerNodeID(ctx, channel.ChannelID{ID: key.ChannelID, Type: key.ChannelType})
		if err != nil {
			return nil, err
		}
		if ownerNodeID == 0 {
			return nil, channel.ErrStaleMeta
		}
		grouped[ownerNodeID] = append(grouped[ownerNodeID], key)
	}
	return grouped, nil
}

func (f channelLogConversationFacts) ownerNodeID(ctx context.Context, key channel.ChannelID) (uint64, error) {
	if f.metas == nil {
		return 0, channel.ErrStaleMeta
	}
	meta, err := f.metas.GetChannelRuntimeMeta(ctx, key.ID, int64(key.Type))
	if err != nil {
		return 0, err
	}
	return meta.Leader, nil
}

func loadLatestConversationMessage(ctx context.Context, cluster interface {
	Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error)
	Fetch(ctx context.Context, req channel.FetchRequest) (channel.FetchResult, error)
}, key channel.ChannelID, maxBytes int) (channel.Message, bool, error) {
	if cluster == nil {
		return channel.Message{}, false, channel.ErrStaleMeta
	}
	status, err := cluster.Status(key)
	if errors.Is(err, channel.ErrNotReady) {
		return channel.Message{}, false, nil
	}
	if err != nil {
		return channel.Message{}, false, err
	}
	if status.CommittedSeq == 0 {
		return channel.Message{}, false, nil
	}

	fetch, err := cluster.Fetch(ctx, channel.FetchRequest{
		ChannelID: key,
		FromSeq:   status.CommittedSeq,
		Limit:     1,
		MaxBytes:  maxBytes,
	})
	if errors.Is(err, channel.ErrNotReady) {
		return channel.Message{}, false, nil
	}
	if err != nil {
		return channel.Message{}, false, err
	}
	if len(fetch.Messages) == 0 {
		return channel.Message{}, false, nil
	}
	return fetch.Messages[0], true, nil
}

func loadRecentConversationMessages(ctx context.Context, cluster interface {
	Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error)
	Fetch(ctx context.Context, req channel.FetchRequest) (channel.FetchResult, error)
}, key channel.ChannelID, limit, maxBytes int) ([]channel.Message, error) {
	if cluster == nil || limit <= 0 {
		return nil, nil
	}
	status, err := cluster.Status(key)
	if errors.Is(err, channel.ErrNotReady) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if status.CommittedSeq == 0 {
		return nil, nil
	}

	fromSeq := uint64(1)
	if status.CommittedSeq >= uint64(limit) {
		fromSeq = status.CommittedSeq - uint64(limit) + 1
	}
	fetch, err := cluster.Fetch(ctx, channel.FetchRequest{
		ChannelID: key,
		FromSeq:   fromSeq,
		Limit:     limit,
		MaxBytes:  maxBytes,
	})
	if errors.Is(err, channel.ErrNotReady) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return append([]channel.Message(nil), fetch.Messages...), nil
}

func newStorageFactory(raftDB *raftstorage.DB) func(slotID multiraft.SlotID) (multiraft.Storage, error) {
	return func(slotID multiraft.SlotID) (multiraft.Storage, error) {
		return raftDB.ForSlot(uint64(slotID)), nil
	}
}

type presenceRouter struct {
	cluster raftcluster.API
}

func (r presenceRouter) SlotForKey(key string) uint64 {
	if r.cluster == nil {
		return 0
	}
	return uint64(r.cluster.SlotForKey(key))
}

func mergeClusterObserverHooks(left, right raftcluster.ObserverHooks) raftcluster.ObserverHooks {
	return raftcluster.ObserverHooks{
		OnControllerCall: func(kind string, dur time.Duration, err error) {
			if left.OnControllerCall != nil {
				left.OnControllerCall(kind, dur, err)
			}
			if right.OnControllerCall != nil {
				right.OnControllerCall(kind, dur, err)
			}
		},
		OnControllerDecision: func(slotID uint32, kind string, dur time.Duration) {
			if left.OnControllerDecision != nil {
				left.OnControllerDecision(slotID, kind, dur)
			}
			if right.OnControllerDecision != nil {
				right.OnControllerDecision(slotID, kind, dur)
			}
		},
		OnReconcileStep: func(slotID uint32, step string, dur time.Duration, err error) {
			if left.OnReconcileStep != nil {
				left.OnReconcileStep(slotID, step, dur, err)
			}
			if right.OnReconcileStep != nil {
				right.OnReconcileStep(slotID, step, dur, err)
			}
		},
		OnForwardPropose: func(slotID uint32, attempts int, dur time.Duration, err error) {
			if left.OnForwardPropose != nil {
				left.OnForwardPropose(slotID, attempts, dur, err)
			}
			if right.OnForwardPropose != nil {
				right.OnForwardPropose(slotID, attempts, dur, err)
			}
		},
		OnSlotEnsure: func(slotID uint32, action string, err error) {
			if left.OnSlotEnsure != nil {
				left.OnSlotEnsure(slotID, action, err)
			}
			if right.OnSlotEnsure != nil {
				right.OnSlotEnsure(slotID, action, err)
			}
		},
		OnTaskResult: func(slotID uint32, kind string, result string) {
			if left.OnTaskResult != nil {
				left.OnTaskResult(slotID, kind, result)
			}
			if right.OnTaskResult != nil {
				right.OnTaskResult(slotID, kind, result)
			}
		},
		OnHashSlotMigration: func(hashSlot uint16, source, target multiraft.SlotID, result string) {
			if left.OnHashSlotMigration != nil {
				left.OnHashSlotMigration(hashSlot, source, target, result)
			}
			if right.OnHashSlotMigration != nil {
				right.OnHashSlotMigration(hashSlot, source, target, result)
			}
		},
		OnLeaderChange: func(slotID uint32, from, to multiraft.NodeID) {
			if left.OnLeaderChange != nil {
				left.OnLeaderChange(slotID, from, to)
			}
			if right.OnLeaderChange != nil {
				right.OnLeaderChange(slotID, from, to)
			}
		},
		OnNodeStatusChange: func(nodeID uint64, from, to controllermeta.NodeStatus) {
			if left.OnNodeStatusChange != nil {
				left.OnNodeStatusChange(nodeID, from, to)
			}
			if right.OnNodeStatusChange != nil {
				right.OnNodeStatusChange(nodeID, from, to)
			}
		},
	}
}

func userRateLimitJanitorInterval(idleTTL time.Duration) time.Duration {
	if idleTTL <= 0 {
		return 0
	}
	interval := idleTTL / 2
	if interval < time.Second {
		return time.Second
	}
	return interval
}
