package node

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	channelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
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

// RuntimeSummaryProvider provides the local node runtime summary for RPC callers.
type RuntimeSummaryProvider interface {
	LocalRuntimeSummary(ctx context.Context) (RuntimeSummary, error)
}

type Options struct {
	Cluster               Cluster
	Presence              Presence
	Online                online.Registry
	GatewayBootID         uint64
	LocalNodeID           uint64
	ChannelLog            ChannelLog
	ChannelLogDB          *channelstore.Engine
	DeliverySubmit        DeliverySubmit
	DeliveryAck           DeliveryAck
	DeliveryOffline       DeliveryOffline
	ChannelMeta           ChannelMetaRefresher
	ChannelLeaderRepair   ChannelLeaderRepairer
	ChannelLeaderEvaluate ChannelLeaderEvaluator
	DeliveryAckIndex      *deliveryruntime.AckIndex
	RuntimeSummary        RuntimeSummaryProvider
	Codec                 codec.Protocol
	Logger                wklog.Logger
}

type Adapter struct {
	cluster               Cluster
	presence              Presence
	online                online.Registry
	gatewayBootID         uint64
	localNodeID           uint64
	channelLog            ChannelLog
	channelLogDB          *channelstore.Engine
	deliverySubmit        DeliverySubmit
	deliveryAck           DeliveryAck
	deliveryOffline       DeliveryOffline
	channelMeta           ChannelMetaRefresher
	channelLeaderRepair   ChannelLeaderRepairer
	channelLeaderEvaluate ChannelLeaderEvaluator
	deliveryAckIndex      *deliveryruntime.AckIndex
	runtimeSummary        RuntimeSummaryProvider
	codec                 codec.Protocol
	logger                wklog.Logger
}

func New(opts Options) *Adapter {
	if opts.Codec == nil {
		opts.Codec = codec.New()
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	adapter := &Adapter{
		cluster:               opts.Cluster,
		presence:              opts.Presence,
		online:                opts.Online,
		gatewayBootID:         opts.GatewayBootID,
		localNodeID:           opts.LocalNodeID,
		channelLog:            opts.ChannelLog,
		channelLogDB:          opts.ChannelLogDB,
		deliverySubmit:        opts.DeliverySubmit,
		deliveryAck:           opts.DeliveryAck,
		deliveryOffline:       opts.DeliveryOffline,
		channelMeta:           opts.ChannelMeta,
		channelLeaderRepair:   opts.ChannelLeaderRepair,
		channelLeaderEvaluate: opts.ChannelLeaderEvaluate,
		deliveryAckIndex:      opts.DeliveryAckIndex,
		runtimeSummary:        opts.RuntimeSummary,
		codec:                 opts.Codec,
		logger:                opts.Logger,
	}
	if opts.Cluster != nil && opts.Cluster.RPCMux() != nil {
		opts.Cluster.RPCMux().Handle(presenceRPCServiceID, adapter.handlePresenceRPC)
		opts.Cluster.RPCMux().Handle(deliverySubmitRPCServiceID, adapter.handleDeliverySubmitRPC)
		opts.Cluster.RPCMux().Handle(deliveryPushRPCServiceID, adapter.handleDeliveryPushRPC)
		opts.Cluster.RPCMux().Handle(deliveryAckRPCServiceID, adapter.handleDeliveryAckRPC)
		opts.Cluster.RPCMux().Handle(deliveryOfflineRPCServiceID, adapter.handleDeliveryOfflineRPC)
		opts.Cluster.RPCMux().Handle(conversationFactsRPCServiceID, adapter.handleConversationFactsRPC)
		opts.Cluster.RPCMux().Handle(channelAppendRPCServiceID, adapter.handleChannelAppendRPC)
		opts.Cluster.RPCMux().Handle(channelMessagesRPCServiceID, adapter.handleChannelMessagesRPC)
		opts.Cluster.RPCMux().Handle(channelLeaderRepairRPCServiceID, adapter.handleChannelLeaderRepairRPC)
		opts.Cluster.RPCMux().Handle(channelLeaderEvaluateRPCServiceID, adapter.handleChannelLeaderEvaluateRPC)
		opts.Cluster.RPCMux().Handle(runtimeSummaryRPCServiceID, adapter.handleRuntimeSummaryRPC)
	}
	return adapter
}

type Client struct {
	cluster Cluster
	codec   codec.Protocol
}

func NewClient(cluster Cluster) *Client {
	return &Client{
		cluster: cluster,
		codec:   codec.New(),
	}
}

var _ Cluster = (*raftcluster.Cluster)(nil)
