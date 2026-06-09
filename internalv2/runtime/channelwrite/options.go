package channelwrite

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
)

const (
	defaultReactorCount                 = 1
	defaultMailboxSize                  = 1024
	defaultPendingItemHighWatermark     = 1024
	defaultAppendInflightLimit          = 1
	defaultEffectWorkerCount            = 1
	defaultSubscriberPageSize           = 256
	defaultRecipientBatchSize           = 256
	defaultRecipientDispatchConcurrency = 4
	defaultReplayPageSize               = 256
	defaultDeliveryRetryMaxAttempts     = 3
	defaultDeliveryRetryInitialBackoff  = 5 * time.Millisecond
	defaultDeliveryRetryMaxBackoff      = 100 * time.Millisecond
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

// RouterObservation describes one routed foreground SEND group.
type RouterObservation struct {
	// Path is local, remote, or pre_route.
	Path string
	// Result is a low-cardinality result or error class.
	Result string
	// Items is the number of items in the observed group.
	Items int
	// Duration is the foreground routing and submit duration.
	Duration time.Duration
}

// RouterObserver receives foreground channel authority routing observations.
type RouterObserver interface {
	// ObserveChannelWriteRouter records one routed foreground SEND group.
	ObserveChannelWriteRouter(RouterObservation)
}

// LocalAdmissionObservation describes local reactor admission for one group.
type LocalAdmissionObservation struct {
	// ReactorID is the local channel-hashed reactor id.
	ReactorID int
	// Result is accepted or a low-cardinality rejection class.
	Result string
	// Items is the number of items in the admitted group.
	Items int
}

// LocalAdmissionObserver receives local authority admission observations.
type LocalAdmissionObserver interface {
	// ObserveChannelWriteLocalAdmission records one local reactor admission attempt.
	ObserveChannelWriteLocalAdmission(LocalAdmissionObservation)
}

// ReactorPressureObservation describes bounded local authority reactor state.
type ReactorPressureObservation struct {
	// ReactorID is the local channel-hashed reactor id.
	ReactorID int
	// MailboxDepth is the current reactor mailbox depth.
	MailboxDepth int
	// MailboxCapacity is the configured reactor mailbox capacity.
	MailboxCapacity int
	// EffectSlotsUsed is the current accepted prepare/append/commit slot count.
	EffectSlotsUsed int
	// EffectSlotsCapacity is the configured accepted effect slot capacity.
	EffectSlotsCapacity int
	// PendingAppendItems is the total prepared-but-not-appended item count.
	PendingAppendItems int
	// AppendInflightItems is the total appender-owned item count.
	AppendInflightItems int
	// PostCommitBacklog is the total committed post-commit backlog count.
	PostCommitBacklog int
}

// ReactorPressureObserver receives local reactor pressure gauges.
type ReactorPressureObserver interface {
	// SetChannelWriteReactorPressure records current bounded reactor pressure.
	SetChannelWriteReactorPressure(ReactorPressureObservation)
}

// EffectWorkerPressureObservation describes effect worker utilization and queue pressure.
type EffectWorkerPressureObservation struct {
	// ReactorID is the local channel-hashed reactor id.
	ReactorID int
	// Stage is prepare, append, post_commit, or replay.
	Stage string
	// WorkerInflight is the current number of busy workers for this stage.
	WorkerInflight int
	// WorkerCapacity is the configured worker count for this stage.
	WorkerCapacity int
	// QueueDepth is the current number of queued effects for this stage.
	QueueDepth int
	// QueueCapacity is the configured queue capacity for this stage.
	QueueCapacity int
}

// EffectWorkerPressureObserver receives effect worker and queue pressure gauges.
type EffectWorkerPressureObserver interface {
	// SetChannelWriteEffectWorkerPressure records current effect worker and queue pressure.
	SetChannelWriteEffectWorkerPressure(EffectWorkerPressureObservation)
}

// PostCommitFailureObservation describes one best-effort post-commit side-effect failure.
type PostCommitFailureObservation struct {
	// ReactorID is the local channel-hashed reactor id.
	ReactorID int
	// ChannelID is the committed message channel id.
	ChannelID string
	// ChannelType is the committed message channel type.
	ChannelType uint8
	// MessageID is the committed message id.
	MessageID uint64
	// MessageSeq is the committed channel sequence.
	MessageSeq uint64
	// Attempt is the post-commit dispatch attempt number.
	Attempt int
	// Result is a low-cardinality error class.
	Result string
	// Phase identifies the post-commit sub-step that produced the failure.
	Phase string
	// UID is one representative recipient UID involved in the failure.
	UID string
	// UIDCount is the number of unique UIDs being resolved or processed.
	UIDCount int
	// RecipientCount is the number of recipient rows in the failed batch or page.
	RecipientCount int
	// TargetHashSlot is the recipient authority hash slot when known.
	TargetHashSlot uint16
	// TargetSlotID is the physical Slot that owns TargetHashSlot when known.
	TargetSlotID uint32
	// TargetLeaderNodeID is the recipient authority leader node when known.
	TargetLeaderNodeID uint64
	// TargetRouteRevision is the route-table revision used to resolve the target when known.
	TargetRouteRevision uint64
	// TargetAuthorityEpoch is the authority epoch used to fence the target when known.
	TargetAuthorityEpoch uint64
	// DispatchTargetCount is the number of recipient authority targets in the failed dispatch fanout.
	DispatchTargetCount int
	// DispatchBatchSize is the number of recipients in the failed dispatch batch.
	DispatchBatchSize int
	// DispatchOwnerNodeID is the owner node for a failed online delivery push.
	DispatchOwnerNodeID uint64
	// DispatchOwnerRouteNum is the number of online routes in a failed owner push.
	DispatchOwnerRouteNum int
	// Err is the concrete failure for structured logs.
	Err error
}

// PostCommitFailureObserver receives best-effort post-commit failure events.
type PostCommitFailureObserver interface {
	// ObserveChannelWritePostCommitFailure records a dropped best-effort post-commit side effect.
	ObserveChannelWritePostCommitFailure(PostCommitFailureObservation)
}

// EffectObservation describes one asynchronous channel write effect.
type EffectObservation struct {
	// Stage is append, post_commit, or replay.
	Stage string
	// Result is ok or a low-cardinality error class.
	Result string
	// Items is the number of logical items handled by the effect.
	Items int
	// Duration is the effect runtime.
	Duration time.Duration
}

// EffectObserver receives asynchronous append/post-commit/replay observations.
type EffectObserver interface {
	// ObserveChannelWriteEffect records one channel write effect.
	ObserveChannelWriteEffect(EffectObservation)
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

// BatchRecipientAuthorityResolver resolves authority targets for multiple recipient UIDs.
type BatchRecipientAuthorityResolver interface {
	// ResolveRecipientAuthorities returns authority targets keyed by UID.
	ResolveRecipientAuthorities(context.Context, []string) (map[string]RecipientAuthorityTarget, error)
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
	// AppendInflightLimit bounds same-channel append batches in flight. Values <= 0 use one in-flight batch.
	AppendInflightLimit int
	// EffectWorkerCount is the bounded worker count for prepare, append, replay, and post-commit effects. Values <= 0 use one worker of each kind.
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
	// RecipientDispatchConcurrency bounds per-message recipient-authority dispatch fanout. Values <= 0 use a bounded default.
	RecipientDispatchConcurrency int
	// DeliveryRetryMaxAttempts bounds retryable owner push attempts. Values <= 0 use a bounded default.
	DeliveryRetryMaxAttempts int
	// DeliveryRetryInitialBackoff is the first retry sleep for retryable owner pushes. Values <= 0 use a bounded default.
	DeliveryRetryInitialBackoff time.Duration
	// DeliveryRetryMaxBackoff caps retry sleeps for retryable owner pushes. Values <= 0 use a bounded default.
	DeliveryRetryMaxBackoff time.Duration
	// CursorStore is ignored because post-commit side effects are best-effort and no longer checkpointed.
	CursorStore CursorStore
	// CommittedReader is ignored because authority restart replay for post-commit side effects is disabled.
	CommittedReader CommittedReader
	// ReplayPageSize is kept for compatibility with older option wiring and is ignored.
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
	if opts.RecipientDispatchConcurrency <= 0 {
		opts.RecipientDispatchConcurrency = defaultRecipientDispatchConcurrency
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
		subscribers:                  opts.Subscribers,
		recipientAuthorityResolver:   opts.RecipientAuthorityResolver,
		recipientRouter:              opts.RecipientRouter,
		subscriberPageSize:           opts.SubscriberPageSize,
		recipientBatchSize:           opts.RecipientBatchSize,
		recipientDispatchConcurrency: opts.RecipientDispatchConcurrency,
		observer:                     opts.Observer,
	}
}

func cursorPortsFromOptions(opts Options) cursorPorts {
	return cursorPorts{
		replayPageSize: opts.ReplayPageSize,
		observer:       opts.Observer,
	}
}

func stateLimitsFromOptions(opts Options) channelStateLimits {
	return channelStateLimits{
		pendingItemHighWatermark: opts.PendingItemHighWatermark,
		appendInflightLimit:      opts.AppendInflightLimit,
	}
}
