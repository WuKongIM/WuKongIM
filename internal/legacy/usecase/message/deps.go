package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/sequence"
	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
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

// SendHook can inspect, mutate, or reject a send before persistence or realtime dispatch.
type SendHook interface {
	// BeforeSend returns a possibly mutated command and optional rejection reason.
	BeforeSend(ctx context.Context, cmd SendCommand) (SendCommand, frame.ReasonCode, error)
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

// ChannelAppender owns durable channel append routing for message sends.
type ChannelAppender interface {
	AppendBatch(ctx context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error)
}

type ChannelMessageReader interface {
	// SyncMessages returns one authoritative legacy-compatible channel message page.
	SyncMessages(ctx context.Context, query ChannelMessageQuery) (ChannelMessagePage, error)
}
