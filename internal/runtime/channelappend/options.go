package channelappend

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
)

const (
	defaultAuthorityShardCount         = 1
	defaultAdvancePoolSize             = 1
	defaultAdmissionCapacityPerShard   = 1024
	defaultChannelBacklogHighWatermark = 1024
	// defaultAppendInflightBatchesPerChannel only bounds how many same-channel
	// append batches this runtime keeps in flight toward the appender. The
	// downstream channel reactor serializes and coalesces per-channel appends,
	// so this value affects submit concurrency and batching efficiency, never
	// durable ordering or MessageSeq assignment.
	defaultAppendInflightBatchesPerChannel       = 10
	defaultInboxCoalesceWindow                   = 250 * time.Microsecond
	defaultInboxCoalesceMaxItems                 = 16
	defaultWriterIdleRetention                   = 10 * time.Minute
	defaultEffectPoolSize                        = 2
	defaultSubscriberScanPageSize                = 256
	defaultRecipientBatchSize                    = 256
	defaultRecipientAuthorityDispatchConcurrency = 4
	defaultDeliveryRetryMaxAttempts              = 3
	defaultDeliveryRetryInitialBackoff           = 5 * time.Millisecond
	defaultDeliveryRetryMaxBackoff               = 100 * time.Millisecond
)

// Clock provides the time source used by channel append runtime state.
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
	// ObserveChannelAppendRouter records one routed foreground SEND group.
	ObserveChannelAppendRouter(RouterObservation)
}

// LocalAdmissionObservation describes local writer-group admission for one batch.
type LocalAdmissionObservation struct {
	// Result is accepted or a low-cardinality rejection class.
	Result string
	// Items is the number of items in the admitted group.
	Items int
}

// LocalAdmissionObserver receives local authority admission observations.
type LocalAdmissionObserver interface {
	// ObserveChannelAppendLocalAdmission records one local writer-group admission attempt.
	ObserveChannelAppendLocalAdmission(LocalAdmissionObservation)
}

// WriterPressureObservation describes bounded local append authority-group state.
type WriterPressureObservation struct {
	// AdmissionDepth is the current admitted-but-incomplete item count.
	AdmissionDepth int
	// AdmissionCapacity is the configured admitted item capacity.
	AdmissionCapacity int
	// WorkerRunning is the current shared worker pool running count.
	WorkerRunning int
	// WorkerCapacity is the shared worker pool capacity.
	WorkerCapacity int
	// PendingAppendItems is the total prepared-but-not-appended item count.
	PendingAppendItems int
	// AppendInflightItems is the total appender-owned item count.
	AppendInflightItems int
	// PostCommitBacklog is the total committed post-commit backlog count.
	PostCommitBacklog int
}

// WriterPressureObserver receives local writer-group pressure gauges.
type WriterPressureObserver interface {
	// SetChannelAppendWriterPressure records current bounded writer-group pressure.
	SetChannelAppendWriterPressure(WriterPressureObservation)
}

// EffectPoolObservation describes shared ants pool admission and pressure.
type EffectPoolObservation struct {
	// Stage is prepare, append, or post_commit.
	Stage string
	// Result is submitted, full, error, or released.
	Result string
	// Inflight is the current number of pool tokens in use.
	Inflight int
	// Capacity is the shared stage pool worker capacity.
	Capacity int
	// Saturated is true when Inflight is at or above Capacity.
	Saturated bool
}

// EffectPoolObserver receives shared ants pool admission and pressure gauges.
type EffectPoolObserver interface {
	// ObserveChannelAppendEffectPool records one shared effect pool observation.
	ObserveChannelAppendEffectPool(EffectPoolObservation)
}

// AntsPoolObservation describes one direct ants/v2 pool occupancy sample.
type AntsPoolObservation struct {
	// Pool is the stable pool name within channelappend.
	Pool string
	// Running is the current number of executing workers.
	Running int
	// Capacity is the configured worker capacity.
	Capacity int
	// Waiting is the current number of blocked submitters.
	Waiting int
}

// AntsPoolObserver receives direct ants/v2 pool occupancy samples.
type AntsPoolObserver interface {
	// ObserveChannelAppendAntsPool records one direct ants/v2 pool occupancy sample.
	ObserveChannelAppendAntsPool(AntsPoolObservation)
}

// PostCommitFailureObservation describes one best-effort post-commit side-effect failure.
type PostCommitFailureObservation struct {
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
	// ObserveChannelAppendPostCommitFailure records a dropped best-effort post-commit side effect.
	ObserveChannelAppendPostCommitFailure(PostCommitFailureObservation)
}

// EffectObservation describes one asynchronous channel append effect.
type EffectObservation struct {
	// Stage is prepare, append, or post_commit.
	Stage string
	// Result is ok or a low-cardinality error class.
	Result string
	// Items is the number of logical items handled by the effect.
	Items int
	// Duration is the effect runtime.
	Duration time.Duration
}

// EffectObserver receives asynchronous prepare/append/post-commit observations.
type EffectObserver interface {
	// ObserveChannelAppendEffect records one channel append effect.
	ObserveChannelAppendEffect(EffectObservation)
}

// RecipientDeliveryQueueObservation describes the dedicated recipient delivery worker queue.
type RecipientDeliveryQueueObservation struct {
	// QueueDepth is the current queued recipient delivery batch count.
	QueueDepth int
	// QueueCapacity is the configured recipient delivery queue capacity.
	QueueCapacity int
}

// RecipientDeliveryQueueObserver receives recipient delivery queue pressure gauges.
type RecipientDeliveryQueueObserver interface {
	// SetChannelAppendRecipientDeliveryQueue records current recipient delivery queue pressure.
	SetChannelAppendRecipientDeliveryQueue(RecipientDeliveryQueueObservation)
}

// RecipientDeliveryAdmissionObservation describes one recipient delivery enqueue attempt.
type RecipientDeliveryAdmissionObservation struct {
	// Result is accepted, closed, canceled, timeout, or error.
	Result string
	// QueueDepth is the queue depth when the attempt completed.
	QueueDepth int
	// QueueCapacity is the configured recipient delivery queue capacity.
	QueueCapacity int
	// Duration is the time spent trying to enqueue.
	Duration time.Duration
}

// RecipientDeliveryAdmissionObserver receives recipient delivery enqueue attempts.
type RecipientDeliveryAdmissionObserver interface {
	// ObserveChannelAppendRecipientDeliveryAdmission records one delivery queue admission attempt.
	ObserveChannelAppendRecipientDeliveryAdmission(RecipientDeliveryAdmissionObservation)
}

// RecipientDeliveryProcessObservation describes one recipient delivery worker command.
type RecipientDeliveryProcessObservation struct {
	// Result is ok, error, or panic.
	Result string
	// Recipients is the number of recipients in the processed batch.
	Recipients int
	// Duration is the worker processing latency.
	Duration time.Duration
}

// RecipientDeliveryProcessObserver receives recipient delivery worker process observations.
type RecipientDeliveryProcessObserver interface {
	// ObserveChannelAppendRecipientDeliveryProcess records one recipient delivery worker command.
	ObserveChannelAppendRecipientDeliveryProcess(RecipientDeliveryProcessObservation)
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

// RecipientDeliveryEnqueuer accepts post-commit recipient batches for asynchronous delivery processing.
type RecipientDeliveryEnqueuer interface {
	// EnqueueRecipientBatch queues one committed recipient batch for the recipient authority target.
	EnqueueRecipientBatch(context.Context, RecipientAuthorityTarget, RecipientBatch) error
}

// PersistAfterEnqueuer accepts durable committed messages for plugin PersistAfter hooks.
type PersistAfterEnqueuer interface {
	// EnqueuePersistAfter queues one committed message for plugin side effects.
	EnqueuePersistAfter(context.Context, CommittedEnvelope)
}

// ConversationActiveAdmitter admits committed recipient activity into the conversation active worker.
type ConversationActiveAdmitter interface {
	// AdmitActiveBatch hands one committed recipient set to the conversation active worker.
	AdmitActiveBatch(context.Context, conversationactive.ActiveBatch) error
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

// Options configures the local channel append group.
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
	// AuthorityShardCount is the number of channel-key lookup shards. Values <= 0 use one shard.
	AuthorityShardCount int
	// AdvancePoolSize is the direct ants pool size used to activate writer state machines. Values <= 0 use a conservative default.
	AdvancePoolSize int
	// AdmissionCapacityPerShard bounds admitted-but-incomplete items per shard. Values <= 0 use a conservative bounded default.
	AdmissionCapacityPerShard int
	// ChannelBacklogHighWatermark is reserved for per-channel admission pressure. Values <= 0 use a bounded default.
	ChannelBacklogHighWatermark int
	// AppendInflightBatchesPerChannel bounds same-channel append batches in flight. Values <= 0 use the runtime default.
	AppendInflightBatchesPerChannel int
	// InboxCoalesceWindow is the bounded delay a scheduled writer may wait to merge near-simultaneous same-channel submissions. A negative value disables coalescing; zero uses the runtime default.
	InboxCoalesceWindow time.Duration
	// InboxCoalesceMaxItems bounds logical send items collected during one coalescing wait. A negative value disables coalescing; zero uses the runtime default.
	InboxCoalesceMaxItems int
	// WriterIdleRetention keeps drained channel writers available for reuse before shard cleanup may reclaim them. Values <= 0 use a conservative default.
	WriterIdleRetention time.Duration
	// EffectPoolSize is the direct ants pool size shared by blocking append calls and post-append recipient effects. Values <= 0 use a conservative default.
	EffectPoolSize int
	// Observer receives non-fatal append observations.
	Observer AppendObserver
	// Subscribers pages channel subscribers for group-channel post-commit effects.
	Subscribers SubscriberSource
	// RecipientAuthorityResolver resolves each recipient UID to its recipient authority node.
	RecipientAuthorityResolver RecipientAuthorityResolver
	// RecipientDeliveryEnqueuer queues selected recipients for asynchronous delivery processing.
	RecipientDeliveryEnqueuer RecipientDeliveryEnqueuer
	// PersistAfterEnqueuer queues durable committed messages for plugin PersistAfter side effects.
	PersistAfterEnqueuer PersistAfterEnqueuer
	// ConversationActiveAdmitter admits active conversation batches after recipient expansion.
	ConversationActiveAdmitter ConversationActiveAdmitter
	// SubscriberScanPageSize bounds each group-channel subscriber scan page. Values <= 0 use a bounded default.
	SubscriberScanPageSize int
	// RecipientBatchSize bounds one dispatched recipient batch. Values <= 0 use a bounded default.
	RecipientBatchSize int
	// RecipientAuthorityDispatchConcurrency bounds per-message recipient-authority dispatch fanout. Values <= 0 use a bounded default.
	RecipientAuthorityDispatchConcurrency int
	// Clock supplies runtime timestamps. Nil uses the system clock.
	Clock Clock
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func applyDefaults(opts Options) Options {
	if opts.AuthorityShardCount <= 0 {
		opts.AuthorityShardCount = defaultAuthorityShardCount
	}
	if opts.AdvancePoolSize <= 0 {
		opts.AdvancePoolSize = defaultAdvancePoolSize
	}
	if opts.AdmissionCapacityPerShard <= 0 {
		opts.AdmissionCapacityPerShard = defaultAdmissionCapacityPerShard
	}
	if opts.ChannelBacklogHighWatermark <= 0 {
		opts.ChannelBacklogHighWatermark = defaultChannelBacklogHighWatermark
	}
	if opts.AppendInflightBatchesPerChannel <= 0 {
		opts.AppendInflightBatchesPerChannel = defaultAppendInflightBatchesPerChannel
	}
	if opts.InboxCoalesceWindow < 0 || opts.InboxCoalesceMaxItems < 0 {
		opts.InboxCoalesceWindow = 0
		opts.InboxCoalesceMaxItems = 0
	} else {
		if opts.InboxCoalesceWindow == 0 {
			opts.InboxCoalesceWindow = defaultInboxCoalesceWindow
		}
		if opts.InboxCoalesceMaxItems == 0 {
			opts.InboxCoalesceMaxItems = defaultInboxCoalesceMaxItems
		}
	}
	if opts.WriterIdleRetention <= 0 {
		opts.WriterIdleRetention = defaultWriterIdleRetention
	}
	if opts.EffectPoolSize <= 0 {
		opts.EffectPoolSize = defaultEffectPoolSize
	}
	if opts.SubscriberScanPageSize <= 0 {
		opts.SubscriberScanPageSize = defaultSubscriberScanPageSize
	}
	if opts.RecipientBatchSize <= 0 {
		opts.RecipientBatchSize = defaultRecipientBatchSize
	}
	if opts.RecipientAuthorityDispatchConcurrency <= 0 {
		opts.RecipientAuthorityDispatchConcurrency = defaultRecipientAuthorityDispatchConcurrency
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
		appender:    opts.Appender,
		idempotency: opts.Idempotency,
		observer:    opts.Observer,
	}
}

func commitPortsFromOptions(opts Options) commitPorts {
	return commitPorts{
		subscribers:                  opts.Subscribers,
		activeAdmitter:               opts.ConversationActiveAdmitter,
		recipientAuthorityResolver:   opts.RecipientAuthorityResolver,
		deliveryEnqueuer:             opts.RecipientDeliveryEnqueuer,
		persistAfter:                 opts.PersistAfterEnqueuer,
		subscriberPageSize:           opts.SubscriberScanPageSize,
		recipientBatchSize:           opts.RecipientBatchSize,
		recipientDispatchConcurrency: opts.RecipientAuthorityDispatchConcurrency,
		observer:                     opts.Observer,
	}
}

func stateLimitsFromOptions(opts Options) channelStateLimits {
	return channelStateLimits{
		pendingItemHighWatermark: opts.ChannelBacklogHighWatermark,
		appendInflightLimit:      opts.AppendInflightBatchesPerChannel,
	}
}
