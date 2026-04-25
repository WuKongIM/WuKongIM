package message

import (
	"context"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/runtime/sequence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type OnlineRegistry = online.Registry
type Delivery = online.Delivery
type SequenceAllocator = sequence.Allocator

type Endpoint struct {
	NodeID     uint64
	BootID     uint64
	SessionID  uint64
	DeviceFlag uint8
}

type RecipientDirectory interface {
	EndpointsByUID(ctx context.Context, uid string) ([]Endpoint, error)
}

type RemoteDeliveryCommand struct {
	NodeID     uint64
	UID        string
	BootID     uint64
	SessionIDs []uint64
	Frame      frame.Frame
}

type RemoteDelivery interface {
	DeliverRemote(ctx context.Context, cmd RemoteDeliveryCommand) error
}

type CommittedMessageDispatcher interface {
	SubmitCommitted(ctx context.Context, env deliveryruntime.CommittedEnvelope) error
}

type DeliveryAck interface {
	AckRoute(ctx context.Context, cmd RouteAckCommand) error
}

type DeliveryOffline interface {
	SessionClosed(ctx context.Context, cmd SessionClosedCommand) error
}

type ChannelCluster interface {
	ApplyMeta(meta channel.Meta) error
	Append(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error)
}

type MetaRefresher interface {
	// RefreshChannelMeta loads authoritative channel metadata, applies the
	// refreshed routing/runtime view locally, and returns the applied metadata.
	RefreshChannelMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error)
}

type RemoteAppender interface {
	AppendToLeader(ctx context.Context, nodeID uint64, req channel.AppendRequest) (channel.AppendResult, error)
}

type onlineRegistryProvider interface {
	OnlineRegistry() online.Registry
}
