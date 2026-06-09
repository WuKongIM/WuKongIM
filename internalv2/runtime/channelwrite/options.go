package channelwrite

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
)

const (
	defaultReactorCount                = 1
	defaultMailboxSize                 = 1024
	defaultPendingItemHighWatermark    = 1024
	defaultAppendInflightLimit         = 1
	defaultEffectWorkerCount           = 1
	defaultSubscriberPageSize          = 256
	defaultRecipientBatchSize          = 256
	defaultReplayPageSize              = 256
	defaultDeliveryRetryMaxAttempts    = 3
	defaultDeliveryRetryInitialBackoff = 5 * time.Millisecond
	defaultDeliveryRetryMaxBackoff     = 100 * time.Millisecond
	defaultCommitRetryMaxAttempts      = 3
)

// Clock provides the time source used by channel write runtime state.
type Clock interface {
	// Now returns the current wall-clock time.
	Now() time.Time
}

// Authorizer decides whether a send may enter the pre-append pending queue.
type Authorizer interface {
	// AuthorizeSend returns a send decision for the canonical command shape known so far.
	AuthorizeSend(context.Context, SendCommand) (Decision, error)
}

// MessageIDAllocator allocates durable message ids before append.
type MessageIDAllocator interface {
	// Next returns the next globally unique message id.
	Next() uint64
}

// IdempotencyStore recovers previously committed sends before allocating a new message id.
type IdempotencyStore interface {
	// LookupSend returns a prior successful result for a canonical sender/client/channel key.
	LookupSend(context.Context, IdempotencyQuery) (SendResult, bool, error)
}

// SenderFenceValidator validates sender-scoped fencing before a send is prepared.
type SenderFenceValidator interface {
	// ValidateSender returns an error when the sender fence rejects the command.
	ValidateSender(context.Context, SendCommand) error
}

// Appender owns blocking durable append for one channel batch.
type Appender interface {
	// AppendBatch appends one channel-aligned message batch.
	AppendBatch(context.Context, AppendBatchRequest) (AppendBatchResult, error)
}

// AppendObserver receives per-message durable append observations.
type AppendObserver interface {
	// AppendFinished observes one append item outcome.
	AppendFinished(path string, err error, dur time.Duration)
}

// SubscriberSource pages channel subscribers for post-commit recipient selection.
type SubscriberSource interface {
	// NextSubscriberPage returns one bounded subscriber page for the requested channel.
	NextSubscriberPage(context.Context, SubscriberPageRequest) (SubscriberPage, error)
}

// RecipientAuthorityTarget identifies the fenced authority target for recipient-scoped effects.
type RecipientAuthorityTarget = authority.Target

// RecipientAuthorityResolver resolves the authority target for a recipient UID.
type RecipientAuthorityResolver interface {
	// ResolveRecipientAuthority returns the recipient authority target for uid.
	ResolveRecipientAuthority(context.Context, string) (RecipientAuthorityTarget, error)
}

// RecipientAuthorityRouter dispatches recipient batches to their authority target.
type RecipientAuthorityRouter interface {
	// DispatchRecipientBatch sends a batch to the recipient authority target.
	DispatchRecipientBatch(context.Context, RecipientAuthorityTarget, RecipientBatch) error
}

// ConversationProjector admits recipient-scoped conversation activity patches.
type ConversationProjector interface {
	// AdmitRecipientPatches applies conversation updates before delivery is resolved.
	AdmitRecipientPatches(context.Context, []ConversationPatch) error
}

// PresenceResolver resolves online endpoints for recipient UIDs.
type PresenceResolver interface {
	// EndpointsByUIDs returns currently known online endpoints for uids.
	EndpointsByUIDs(context.Context, []string) ([]Route, error)
}

// OwnerPusher pushes committed messages to owner-node gateway sessions.
type OwnerPusher interface {
	// Push sends one owner-node grouped push command.
	Push(context.Context, PushCommand) (PushResult, error)
}

// CursorStore persists the last post-commit sequence fully accepted by recipient dispatch.
type CursorStore interface {
	// LoadPostCommitCursor returns the last completed post-commit channel sequence.
	LoadPostCommitCursor(context.Context, ChannelID) (uint64, error)
	// StorePostCommitCursor stores a monotonic post-commit channel sequence.
	StorePostCommitCursor(context.Context, ChannelID, uint64) error
}

// CommittedReader reads durable committed messages for post-commit replay.
type CommittedReader interface {
	// ReadCommittedFrom returns committed messages starting at fromSeq in ascending order.
	ReadCommittedFrom(context.Context, ChannelID, uint64, int) ([]CommittedMessage, error)
}

// Options configures the channel write reactor group.
type Options struct {
	// LocalNodeID is the node id allowed to own local channel authority state.
	LocalNodeID uint64
	// Appender owns blocking durable append for prepared channel messages.
	Appender Appender
	// MessageID allocates message ids for non-idempotent gateway-origin sends.
	MessageID MessageIDAllocator
	// Authorizer decides whether a send may enter the pending append queue.
	Authorizer Authorizer
	// Idempotency recovers successful sends before allocating a new message id.
	Idempotency IdempotencyStore
	// SenderFence validates sender-scoped fencing before authorization.
	SenderFence SenderFenceValidator
	// ReactorCount is the number of channel-hashed reactors. Values <= 0 use one reactor.
	ReactorCount int
	// MailboxSize bounds each reactor mailbox. Values <= 0 use a conservative bounded default.
	MailboxSize int
	// PendingItemHighWatermark is reserved for per-channel admission pressure. Values <= 0 use a bounded default.
	PendingItemHighWatermark int
	// AppendInflightLimit is reserved for future per-channel append concurrency; same-channel append is currently hard-capped at one in flight.
	AppendInflightLimit int
	// EffectWorkerCount is the number of bounded prepare and append workers. Values <= 0 use one worker of each kind.
	EffectWorkerCount int
	// Observer receives non-fatal append observations.
	Observer AppendObserver
	// Subscribers pages channel subscribers for group-channel post-commit effects.
	Subscribers SubscriberSource
	// RecipientAuthorityResolver resolves each recipient UID to its recipient authority node.
	RecipientAuthorityResolver RecipientAuthorityResolver
	// RecipientRouter dispatches selected recipients to their recipient authority node.
	RecipientRouter RecipientAuthorityRouter
	// ConversationProjector updates recipient conversations before delivery is resolved.
	ConversationProjector ConversationProjector
	// PresenceResolver resolves online recipient endpoints for delivery pushes.
	PresenceResolver PresenceResolver
	// OwnerPusher pushes online delivery commands to owner nodes.
	OwnerPusher OwnerPusher
	// SubscriberPageSize bounds each group-channel subscriber scan page. Values <= 0 use a bounded default.
	SubscriberPageSize int
	// RecipientBatchSize bounds one dispatched recipient batch. Values <= 0 use a bounded default.
	RecipientBatchSize int
	// DeliveryRetryMaxAttempts bounds retryable owner push attempts. Values <= 0 use a bounded default.
	DeliveryRetryMaxAttempts int
	// DeliveryRetryInitialBackoff is the first retry sleep for retryable owner pushes. Values <= 0 use a bounded default.
	DeliveryRetryInitialBackoff time.Duration
	// DeliveryRetryMaxBackoff caps retry sleeps for retryable owner pushes. Values <= 0 use a bounded default.
	DeliveryRetryMaxBackoff time.Duration
	// CommitRetryMaxAttempts bounds recipient dispatch retries before terminal in-memory drop. Values <= 0 use a bounded default.
	CommitRetryMaxAttempts int
	// CursorStore persists post-commit progress. Nil keeps post-commit replay in memory only.
	CursorStore CursorStore
	// CommittedReader reads durable channel messages for authority restart replay.
	CommittedReader CommittedReader
	// ReplayPageSize bounds each durable post-commit replay read page. Values <= 0 use a bounded default.
	ReplayPageSize int
	// Clock supplies runtime timestamps. Nil uses the system clock.
	Clock Clock
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func applyDefaults(opts Options) Options {
	if opts.ReactorCount <= 0 {
		opts.ReactorCount = defaultReactorCount
	}
	if opts.MailboxSize <= 0 {
		opts.MailboxSize = defaultMailboxSize
	}
	if opts.PendingItemHighWatermark <= 0 {
		opts.PendingItemHighWatermark = defaultPendingItemHighWatermark
	}
	if opts.AppendInflightLimit <= 0 {
		opts.AppendInflightLimit = defaultAppendInflightLimit
	}
	if opts.EffectWorkerCount <= 0 {
		opts.EffectWorkerCount = defaultEffectWorkerCount
	}
	if opts.SubscriberPageSize <= 0 {
		opts.SubscriberPageSize = defaultSubscriberPageSize
	}
	if opts.RecipientBatchSize <= 0 {
		opts.RecipientBatchSize = defaultRecipientBatchSize
	}
	if opts.ReplayPageSize <= 0 {
		opts.ReplayPageSize = defaultReplayPageSize
	}
	if opts.DeliveryRetryMaxAttempts <= 0 {
		opts.DeliveryRetryMaxAttempts = defaultDeliveryRetryMaxAttempts
	}
	if opts.DeliveryRetryInitialBackoff <= 0 {
		opts.DeliveryRetryInitialBackoff = defaultDeliveryRetryInitialBackoff
	}
	if opts.DeliveryRetryMaxBackoff <= 0 {
		opts.DeliveryRetryMaxBackoff = defaultDeliveryRetryMaxBackoff
	}
	if opts.CommitRetryMaxAttempts <= 0 {
		opts.CommitRetryMaxAttempts = defaultCommitRetryMaxAttempts
	}
	if opts.Authorizer == nil {
		opts.Authorizer = allowAllAuthorizer{}
	}
	if opts.Clock == nil {
		opts.Clock = systemClock{}
	}
	return opts
}

func preparePortsFromOptions(opts Options) preparePorts {
	return preparePorts{
		messageID:   opts.MessageID,
		authorizer:  opts.Authorizer,
		idempotency: opts.Idempotency,
		senderFence: opts.SenderFence,
		clock:       opts.Clock,
	}
}

func appendPortsFromOptions(opts Options) appendPorts {
	return appendPorts{
		appender: opts.Appender,
		observer: opts.Observer,
	}
}

func commitPortsFromOptions(opts Options) commitPorts {
	return commitPorts{
		subscribers:                opts.Subscribers,
		recipientAuthorityResolver: opts.RecipientAuthorityResolver,
		recipientRouter:            opts.RecipientRouter,
		cursorStore:                opts.CursorStore,
		subscriberPageSize:         opts.SubscriberPageSize,
		recipientBatchSize:         opts.RecipientBatchSize,
		retryMaxAttempts:           opts.CommitRetryMaxAttempts,
	}
}

func cursorPortsFromOptions(opts Options) cursorPorts {
	return cursorPorts{
		store:          opts.CursorStore,
		reader:         opts.CommittedReader,
		replayPageSize: opts.ReplayPageSize,
	}
}

func stateLimitsFromOptions(opts Options) channelStateLimits {
	return channelStateLimits{
		pendingItemHighWatermark: opts.PendingItemHighWatermark,
		appendInflightLimit:      opts.AppendInflightLimit,
	}
}
