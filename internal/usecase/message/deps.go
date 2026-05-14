package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/runtime/sequence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
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
	SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error
}

// CMDConversationIntentSink receives request-scoped CMD conversation intents after durable append.
type CMDConversationIntentSink interface {
	PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) (bool, error)
}

// RealtimeDispatcher receives transient messages that should bypass durable append.
type RealtimeDispatcher interface {
	SubmitRealtime(ctx context.Context, event messageevents.MessageRealtime) error
}

// MessageIDGenerator allocates transient IDs for non-durable realtime messages.
type MessageIDGenerator interface {
	Next() uint64
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

type ChannelMessageReader interface {
	// SyncMessages returns one authoritative legacy-compatible channel message page.
	SyncMessages(ctx context.Context, query ChannelMessageQuery) (ChannelMessagePage, error)
}

type MetaRefresher interface {
	// RefreshChannelMeta loads authoritative channel metadata, applies the
	// refreshed routing/runtime view locally, and returns the applied metadata.
	RefreshChannelMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error)
}

// MetaInvalidator drops cached channel metadata before a forced refresh.
type MetaInvalidator interface {
	// InvalidateChannelMeta invalidates cached metadata for one channel.
	InvalidateChannelMeta(id channel.ChannelID)
}

type RemoteAppender interface {
	AppendToLeader(ctx context.Context, nodeID uint64, req channel.AppendRequest) (channel.AppendResult, error)
}
