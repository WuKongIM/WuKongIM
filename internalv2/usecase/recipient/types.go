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

// RecipientPage is one bounded recipient scan page.
type RecipientPage struct {
	// Recipients are the durable recipients found in this page.
	Recipients []Recipient
	// Cursor is the opaque continuation token for the next page.
	Cursor string
	// Done reports whether the scan is complete after this page.
	Done bool
}

// RecipientSource pages durable channel recipients for unscoped committed events.
type RecipientSource interface {
	NextPage(context.Context, messageevents.MessageCommitted, string, int) (RecipientPage, error)
}

// RecipientAuthorityResolver resolves a recipient UID to its fenced authority target.
type RecipientAuthorityResolver interface {
	ResolveRecipientAuthority(context.Context, string) (authority.Target, error)
}

// RecipientAuthorityValidator verifies this node still owns a fenced recipient authority target.
type RecipientAuthorityValidator interface {
	ValidateRecipientAuthority(context.Context, authority.Target) error
}

// RecipientRemote forwards recipient-authority work to remote nodes.
type RecipientRemote interface {
	ProcessRemote(context.Context, ProcessRequest) error
}

// LocalProcessor processes recipient-authority work on the local node.
type LocalProcessor interface {
	Process(context.Context, ProcessRequest) error
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
	// Authority validates the exact fenced target before side effects.
	Authority RecipientAuthorityValidator
	// Conversation updates recent conversation state before delivery.
	Conversation ConversationUpdater
	// Delivery submits delivery after conversation state is updated.
	Delivery DeliverySubmitter
}

// DispatcherOptions configures committed-message recipient dispatch.
type DispatcherOptions struct {
	// LocalNodeID is this node's clusterv2 identity for target locality checks.
	LocalNodeID uint64
	// Recipients pages durable recipients when the committed event is not scoped.
	Recipients RecipientSource
	// Resolver maps each recipient UID to its current fenced authority target.
	Resolver RecipientAuthorityResolver
	// Local processes recipient-authority batches owned by this node.
	Local LocalProcessor
	// Remote forwards recipient-authority batches owned by other nodes.
	Remote RecipientRemote
	// PageSize bounds each durable recipient scan page.
	PageSize int
	// TargetBatchSize bounds each processor request for one authority target.
	TargetBatchSize int
}
