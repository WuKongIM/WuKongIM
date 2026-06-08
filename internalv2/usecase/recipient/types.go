package recipient

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
)

var (
	ErrNotLeader     = errors.New("internalv2/usecase/recipient: not leader")
	ErrStaleRoute    = errors.New("internalv2/usecase/recipient: stale route")
	ErrRouteNotReady = errors.New("internalv2/usecase/recipient: route not ready")
	// ErrConversationRequired reports delivery configured without the required conversation updater.
	ErrConversationRequired = errors.New("internalv2/usecase/recipient: conversation updater required")
)

// Recipient identifies one UID in a recipient-authority group.
type Recipient struct {
	// UID identifies the receiving user.
	UID string
	// JoinSeq is the first visible channel sequence for this recipient.
	JoinSeq uint64
}

// ProcessRequest carries one recipient-authority batch.
type ProcessRequest struct {
	// Target fences the recipient authority that should process this request.
	Target authority.Target
	// Event is the durable committed message.
	Event messageevents.MessageCommitted
	// Recipients are the UIDs owned by Target for this message.
	Recipients []Recipient
}

// ConversationUpdater accepts recipient-scoped active conversation patches.
type ConversationUpdater interface {
	AdmitPatches(context.Context, []conversationusecase.ActivePatch) error
}

// DeliverySubmitter accepts recipient-scoped delivery events.
type DeliverySubmitter interface {
	SubmitDelivery(context.Context, messageevents.MessageCommitted) error
}

// ProcessorOptions configures the recipient authority processor.
type ProcessorOptions struct {
	// LocalNodeID is this node's clusterv2 identity.
	LocalNodeID uint64
	// Conversation updates recent conversation state before delivery.
	Conversation ConversationUpdater
	// Delivery submits delivery after conversation state is updated.
	Delivery DeliverySubmitter
}
