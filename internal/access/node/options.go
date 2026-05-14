package node

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	channelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
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

// DiagnosticsProvider queries retained node-local diagnostics events.
type DiagnosticsProvider interface {
	QueryDiagnostics(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult
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
	Diagnostics            DiagnosticsProvider
	ChannelRetention       ChannelRetentionProvider
	SystemUIDCache         SystemUIDCache
	CMDSync                CMDSyncUsecase
	CMDConversationIntents CMDConversationIntentSink
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
	diagnostics            DiagnosticsProvider
	channelRetention       ChannelRetentionProvider
	systemUIDCache         SystemUIDCache
	cmdSync                CMDSyncUsecase
	cmdConversationIntents CMDConversationIntentSink
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
		diagnostics:            opts.Diagnostics,
		channelRetention:       opts.ChannelRetention,
		systemUIDCache:         opts.SystemUIDCache,
		cmdSync:                opts.CMDSync,
		cmdConversationIntents: opts.CMDConversationIntents,
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
		opts.Cluster.RPCMux().Handle(channelMessagesRPCServiceID, adapter.handleChannelMessagesRPC)
		opts.Cluster.RPCMux().Handle(channelLeaderRepairRPCServiceID, adapter.handleChannelLeaderRepairRPC)
		opts.Cluster.RPCMux().Handle(channelLeaderTransferRPCServiceID, adapter.handleChannelLeaderTransferRPC)
		opts.Cluster.RPCMux().Handle(channelLeaderEvaluateRPCServiceID, adapter.handleChannelLeaderEvaluateRPC)
		opts.Cluster.RPCMux().Handle(runtimeSummaryRPCServiceID, adapter.handleRuntimeSummaryRPC)
		opts.Cluster.RPCMux().Handle(connectionsRPCServiceID, adapter.handleConnectionsRPC)
		opts.Cluster.RPCMux().Handle(connectionRPCServiceID, adapter.handleConnectionRPC)
		opts.Cluster.RPCMux().Handle(diagnosticsRPCServiceID, adapter.handleDiagnosticsRPC)
		opts.Cluster.RPCMux().Handle(channelRetentionRPCServiceID, adapter.handleChannelRetentionRPC)
		opts.Cluster.RPCMux().Handle(systemUIDCacheRPCServiceID, adapter.handleSystemUIDCacheRPC)
		opts.Cluster.RPCMux().Handle(cmdSyncRPCServiceID, adapter.handleCMDSyncRPC)
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
