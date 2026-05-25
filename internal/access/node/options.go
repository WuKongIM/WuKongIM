package node

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	channelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type Cluster interface {
	RPCMux() *transport.RPCMux
	LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error)
	IsLocal(nodeID multiraft.NodeID) bool
	SlotForKey(key string) multiraft.SlotID
	RPCService(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error)
	PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID
}

type Presence interface {
	presence.Authoritative
	ApplyRouteAction(ctx context.Context, action presence.RouteAction) error
}

type DeliverySubmit interface {
	SubmitCommitted(ctx context.Context, env deliveryruntime.CommittedEnvelope) error
}

type ChannelLog interface {
	Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error)
	Fetch(ctx context.Context, req channel.FetchRequest) (channel.FetchResult, error)
	Append(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error)
	AppendBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error)
}

type ChannelMetaRefresher interface {
	RefreshChannelMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error)
}

// ChannelLeaderRepairer repairs a channel leader on the authoritative slot leader.
type ChannelLeaderRepairer interface {
	RepairChannelLeaderAuthoritative(ctx context.Context, req channelmeta.LeaderRepairRequest) (channelmeta.LeaderRepairResult, error)
}

// ChannelLeaderTransferer transfers a channel leader on the authoritative slot leader.
type ChannelLeaderTransferer interface {
	// TransferChannelLeaderAuthoritative safely transfers channel leadership from the authoritative slot leader.
	TransferChannelLeaderAuthoritative(ctx context.Context, req channelmeta.LeaderTransferRequest) (channelmeta.LeaderTransferResult, error)
}

// ChannelLeaderEvaluator evaluates whether the local replica can safely lead.
type ChannelLeaderEvaluator interface {
	EvaluateChannelLeaderCandidate(ctx context.Context, req channelmeta.LeaderEvaluateRequest) (channelmeta.LeaderPromotionReport, error)
}

type DeliveryAck interface {
	AckRoute(ctx context.Context, cmd deliveryevents.RouteAck) error
}

type DeliveryOffline interface {
	SessionClosed(ctx context.Context, cmd deliveryevents.SessionClosed) error
}

// DeliveryTagAuthority serves leader-authoritative delivery tag reads and updates.
type DeliveryTagAuthority interface {
	GetDeliveryTag(ctx context.Context, req DeliveryTagRequest) (DeliveryTagResponse, error)
	UpdateDeliveryTag(ctx context.Context, req DeliveryTagRequest) (DeliveryTagResponse, error)
}

// RuntimeSummary contains local node runtime counters exposed over node RPC.
type RuntimeSummary struct {
	// NodeID identifies the cluster node described by this summary.
	NodeID uint64
	// ActiveOnline counts active authenticated online connections.
	ActiveOnline int
	// ClosingOnline counts authenticated online connections that are closing.
	ClosingOnline int
	// TotalOnline counts all authenticated online connections tracked locally.
	TotalOnline int
	// GatewaySessions counts all gateway sessions, including unauthenticated sessions.
	GatewaySessions int
	// SessionsByListener groups gateway sessions by listener.
	SessionsByListener map[string]int
	// AcceptingNewSessions reports whether gateway admission currently accepts new sessions.
	AcceptingNewSessions bool
	// Draining reports whether the local node is currently draining.
	Draining bool
	// Unknown means runtime counters could not be read and must fail closed.
	Unknown bool
}

// Connection is a node-local connection DTO exposed over node RPC.
type Connection struct {
	// NodeID identifies the cluster node that owns this local connection.
	NodeID uint64
	// SessionID is the gateway session identifier on the owning node.
	SessionID uint64
	// UID is the authenticated user identifier.
	UID string
	// DeviceID is the client device identifier.
	DeviceID string
	// DeviceFlag is the stable manager-facing device flag string.
	DeviceFlag string
	// DeviceLevel is the stable manager-facing device level string.
	DeviceLevel string
	// SlotID is the local authoritative slot identifier for the user.
	SlotID uint64
	// State is the local runtime connection state string.
	State string
	// Listener is the listener name that accepted the connection.
	Listener string
	// ConnectedAt is the initial local connection time.
	ConnectedAt time.Time
	// RemoteAddr is the client remote address observed by the listener.
	RemoteAddr string
	// LocalAddr is the local listener address observed by the listener.
	LocalAddr string
}

// RuntimeSummaryProvider provides the local node runtime summary for RPC callers.
type RuntimeSummaryProvider interface {
	LocalRuntimeSummary(ctx context.Context) (RuntimeSummary, error)
}

// MonitorMetricsProvider provides the local node dashboard collector for RPC callers.
type MonitorMetricsProvider interface {
	LocalMonitorMetrics(ctx context.Context, window, step time.Duration) (metrics.QueryResult, error)
}

// DiagnosticsProvider queries retained node-local diagnostics events.
type DiagnosticsProvider interface {
	QueryDiagnostics(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult
}

// DiagnosticsTrackingProvider mutates node-local diagnostics tracking rules.
type DiagnosticsTrackingProvider interface {
	AddDiagnosticsTrackingRule(ctx context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
	ListDiagnosticsTrackingRules(ctx context.Context) ([]diagnostics.TrackingRule, error)
	DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) error
}

// ChannelRetentionProvider advances local channel retention boundaries for leader-authoritative RPCs.
type ChannelRetentionProvider interface {
	// AdvanceChannelRetention advances one channel's history retention boundary on the channel leader.
	AdvanceChannelRetention(ctx context.Context, req ChannelRetentionAdvanceRequest) (ChannelRetentionAdvanceResult, error)
}

// SystemUIDCache mutates the node-local system account UID cache.
type SystemUIDCache interface {
	AddSystemUIDsToCache(uids []string) error
	RemoveSystemUIDsFromCache(uids []string) error
}

// CMDSyncUsecase serves UID-owned durable command-message sync requests.
type CMDSyncUsecase interface {
	Sync(ctx context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error)
	SyncAck(ctx context.Context, cmd cmdsync.SyncAckCommand) error
}

// PluginHTTPRouteProvider handles node-local plugin HTTP routes for node RPC callers.
type PluginHTTPRouteProvider interface {
	// Route invokes one local plugin's HTTP-compatible Route hook.
	Route(ctx context.Context, pluginNo string, req *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error)
}

// PluginManagementProvider exposes node-local plugin management operations to node RPC callers.
type PluginManagementProvider interface {
	// ListLocalPlugins returns the current node-local plugin inventory.
	ListLocalPlugins(ctx context.Context) (pluginusecase.LocalPluginList, error)
	// GetLocalPlugin returns one node-local plugin detail.
	GetLocalPlugin(ctx context.Context, pluginNo string) (pluginusecase.LocalPluginDetail, error)
	// UpdateLocalConfig persists one plugin's desired config on this node.
	UpdateLocalConfig(ctx context.Context, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error)
	// RestartLocalPlugin restarts one plugin process on this node.
	RestartLocalPlugin(ctx context.Context, pluginNo string) (pluginusecase.LocalPluginDetail, error)
	// UninstallLocalPlugin disables and removes one plugin process on this node.
	UninstallLocalPlugin(ctx context.Context, pluginNo string) error
}

// PluginCommittedProvider handles owner-routed committed-message plugin side effects on the target node.
type PluginCommittedProvider interface {
	// PersistAfterCommitted invokes node-local PersistAfter plugins for one committed message.
	PersistAfterCommitted(ctx context.Context, event messageevents.MessageCommitted) error
}

// CMDConversationIntentSink accepts UID-owner CMD conversation update intents.
type CMDConversationIntentSink interface {
	PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) error
}

type Options struct {
	Cluster                Cluster
	Presence               Presence
	Online                 online.Registry
	GatewayBootID          uint64
	LocalNodeID            uint64
	ChannelLog             ChannelLog
	ChannelLogDB           *channelstore.Engine
	DeliverySubmit         DeliverySubmit
	DeliveryAck            DeliveryAck
	DeliveryOffline        DeliveryOffline
	DeliveryTag            DeliveryTagAuthority
	ChannelMeta            ChannelMetaRefresher
	ChannelLeaderRepair    ChannelLeaderRepairer
	ChannelLeaderTransfer  ChannelLeaderTransferer
	ChannelLeaderEvaluate  ChannelLeaderEvaluator
	DeliveryAckIndex       *deliveryruntime.AckIndex
	RuntimeSummary         RuntimeSummaryProvider
	MonitorMetrics         MonitorMetricsProvider
	Diagnostics            DiagnosticsProvider
	DiagnosticsTracking    DiagnosticsTrackingProvider
	ChannelRetention       ChannelRetentionProvider
	SystemUIDCache         SystemUIDCache
	CMDSync                CMDSyncUsecase
	CMDConversationIntents CMDConversationIntentSink
	PluginHTTPRoutes       PluginHTTPRouteProvider
	PluginManagement       PluginManagementProvider
	PluginCommitted        PluginCommittedProvider
	Codec                  codec.Protocol
	Logger                 wklog.Logger
}

type Adapter struct {
	cluster                Cluster
	presence               Presence
	online                 online.Registry
	gatewayBootID          uint64
	localNodeID            uint64
	channelLog             ChannelLog
	channelLogDB           *channelstore.Engine
	deliverySubmit         DeliverySubmit
	deliveryAck            DeliveryAck
	deliveryOffline        DeliveryOffline
	deliveryTag            DeliveryTagAuthority
	channelMeta            ChannelMetaRefresher
	channelLeaderRepair    ChannelLeaderRepairer
	channelLeaderTransfer  ChannelLeaderTransferer
	channelLeaderEvaluate  ChannelLeaderEvaluator
	deliveryAckIndex       *deliveryruntime.AckIndex
	runtimeSummary         RuntimeSummaryProvider
	monitorMetrics         MonitorMetricsProvider
	diagnostics            DiagnosticsProvider
	diagnosticsTracking    DiagnosticsTrackingProvider
	channelRetention       ChannelRetentionProvider
	systemUIDCache         SystemUIDCache
	cmdSync                CMDSyncUsecase
	cmdConversationIntents CMDConversationIntentSink
	pluginHTTPRoutes       PluginHTTPRouteProvider
	pluginManagement       PluginManagementProvider
	pluginCommitted        PluginCommittedProvider
	codec                  codec.Protocol
	logger                 wklog.Logger
}

func New(opts Options) *Adapter {
	if opts.Codec == nil {
		opts.Codec = codec.New()
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	adapter := &Adapter{
		cluster:                opts.Cluster,
		presence:               opts.Presence,
		online:                 opts.Online,
		gatewayBootID:          opts.GatewayBootID,
		localNodeID:            opts.LocalNodeID,
		channelLog:             opts.ChannelLog,
		channelLogDB:           opts.ChannelLogDB,
		deliverySubmit:         opts.DeliverySubmit,
		deliveryAck:            opts.DeliveryAck,
		deliveryOffline:        opts.DeliveryOffline,
		deliveryTag:            opts.DeliveryTag,
		channelMeta:            opts.ChannelMeta,
		channelLeaderRepair:    opts.ChannelLeaderRepair,
		channelLeaderTransfer:  opts.ChannelLeaderTransfer,
		channelLeaderEvaluate:  opts.ChannelLeaderEvaluate,
		deliveryAckIndex:       opts.DeliveryAckIndex,
		runtimeSummary:         opts.RuntimeSummary,
		monitorMetrics:         opts.MonitorMetrics,
		diagnostics:            opts.Diagnostics,
		diagnosticsTracking:    opts.DiagnosticsTracking,
		channelRetention:       opts.ChannelRetention,
		systemUIDCache:         opts.SystemUIDCache,
		cmdSync:                opts.CMDSync,
		cmdConversationIntents: opts.CMDConversationIntents,
		pluginHTTPRoutes:       opts.PluginHTTPRoutes,
		pluginManagement:       opts.PluginManagement,
		pluginCommitted:        opts.PluginCommitted,
		codec:                  opts.Codec,
		logger:                 opts.Logger,
	}
	if opts.Cluster != nil && opts.Cluster.RPCMux() != nil {
		opts.Cluster.RPCMux().Handle(presenceRPCServiceID, adapter.handlePresenceRPC)
		opts.Cluster.RPCMux().Handle(deliverySubmitRPCServiceID, adapter.handleDeliverySubmitRPC)
		opts.Cluster.RPCMux().Handle(deliveryPushRPCServiceID, adapter.handleDeliveryPushRPC)
		opts.Cluster.RPCMux().Handle(deliveryAckRPCServiceID, adapter.handleDeliveryAckRPC)
		opts.Cluster.RPCMux().Handle(deliveryOfflineRPCServiceID, adapter.handleDeliveryOfflineRPC)
		opts.Cluster.RPCMux().Handle(deliveryTagRPCServiceID, adapter.handleDeliveryTagRPC)
		opts.Cluster.RPCMux().Handle(conversationFactsRPCServiceID, adapter.handleConversationFactsRPC)
		opts.Cluster.RPCMux().Handle(channelAppendRPCServiceID, adapter.handleChannelAppendRPC)
		opts.Cluster.RPCMux().Handle(channelPlaneAppendRPCServiceID, adapter.handleChannelPlaneAppendBatchesRPC)
		opts.Cluster.RPCMux().Handle(channelMessagesRPCServiceID, adapter.handleChannelMessagesRPC)
		opts.Cluster.RPCMux().Handle(channelLeaderRepairRPCServiceID, adapter.handleChannelLeaderRepairRPC)
		opts.Cluster.RPCMux().Handle(channelLeaderTransferRPCServiceID, adapter.handleChannelLeaderTransferRPC)
		opts.Cluster.RPCMux().Handle(channelLeaderEvaluateRPCServiceID, adapter.handleChannelLeaderEvaluateRPC)
		opts.Cluster.RPCMux().Handle(runtimeSummaryRPCServiceID, adapter.handleRuntimeSummaryRPC)
		opts.Cluster.RPCMux().Handle(monitorMetricsRPCServiceID, adapter.handleMonitorMetricsRPC)
		opts.Cluster.RPCMux().Handle(connectionsRPCServiceID, adapter.handleConnectionsRPC)
		opts.Cluster.RPCMux().Handle(connectionRPCServiceID, adapter.handleConnectionRPC)
		opts.Cluster.RPCMux().Handle(diagnosticsRPCServiceID, adapter.handleDiagnosticsRPC)
		opts.Cluster.RPCMux().Handle(diagnosticsTrackingRPCServiceID, adapter.handleDiagnosticsTrackingRPC)
		opts.Cluster.RPCMux().Handle(channelRetentionRPCServiceID, adapter.handleChannelRetentionRPC)
		opts.Cluster.RPCMux().Handle(systemUIDCacheRPCServiceID, adapter.handleSystemUIDCacheRPC)
		opts.Cluster.RPCMux().Handle(cmdSyncRPCServiceID, adapter.handleCMDSyncRPC)
		opts.Cluster.RPCMux().Handle(pluginHTTPForwardRPCServiceID, adapter.handlePluginHTTPForwardRPC)
		opts.Cluster.RPCMux().Handle(pluginManagementRPCServiceID, adapter.handlePluginManagementRPC)
		opts.Cluster.RPCMux().Handle(pluginCommittedRPCServiceID, adapter.handlePluginCommittedRPC)
	}
	return adapter
}

type Client struct {
	cluster Cluster
	codec   codec.Protocol

	mu                                 sync.RWMutex
	messageScopedDeliverySubmitSupport map[uint64]bool
	deliverySubmitV3Support            map[uint64]bool
}

func NewClient(cluster Cluster) *Client {
	return &Client{
		cluster: cluster,
		codec:   codec.New(),
	}
}

var _ Cluster = (*raftcluster.Cluster)(nil)
