package channelwrite

import (
	"context"
	"hash/fnv"
	"strconv"
	"sync"

	contract "github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
)

// ChannelID identifies a message channel.
type ChannelID = contract.ChannelID

// AuthorityTarget identifies the fenced channel authority for write admission.
type AuthorityTarget = contract.AuthorityTarget

// SendCommand is an entry-agnostic SEND request.
type SendCommand = contract.SendCommand

// Reason is the entry-agnostic result code for SEND.
type Reason = contract.Reason

const (
	// ReasonSuccess means the send was durably accepted.
	ReasonSuccess = contract.ReasonSuccess
	// ReasonInvalidRequest means the command is malformed.
	ReasonInvalidRequest = contract.ReasonInvalidRequest
	// ReasonAuthFail means the sender is not authenticated.
	ReasonAuthFail = contract.ReasonAuthFail
	// ReasonChannelNotExist means the channel cannot accept this send.
	ReasonChannelNotExist = contract.ReasonChannelNotExist
	// ReasonNodeNotMatch means the client should retry through a fresher route.
	ReasonNodeNotMatch = contract.ReasonNodeNotMatch
	// ReasonSystemError means the send failed due to infrastructure pressure or error.
	ReasonSystemError = contract.ReasonSystemError
	// ReasonUnsupported means the phase-1 stack does not implement this send mode.
	ReasonUnsupported = contract.ReasonUnsupported
)

// SendResult is the client-facing SEND outcome.
type SendResult = contract.SendResult

// SendBatchItem carries one send command with its cancellation context.
type SendBatchItem = contract.SendBatchItem

// SendBatchItemResult aligns with one SendBatch item.
type SendBatchItemResult = contract.SendBatchItemResult

// Decision is the result of send authorization.
type Decision = contract.Decision

// CommitMode controls when durable append completes.
type CommitMode = contract.CommitMode

const (
	// CommitModeQuorum waits for quorum commit.
	CommitModeQuorum = contract.CommitModeQuorum
	// CommitModeLocal completes after local durable append.
	CommitModeLocal = contract.CommitModeLocal
)

// IdempotencyQuery identifies one canonical sender/client message key.
type IdempotencyQuery = contract.IdempotencyQuery

// Message is the durable append payload used by the channel appender port.
type Message = contract.Message

// AppendBatchRequest appends messages to one canonical channel.
type AppendBatchRequest = contract.AppendBatchRequest

// AppendBatchResult returns item-aligned append outcomes.
type AppendBatchResult = contract.AppendBatchResult

// AppendBatchItemResult is one append result inside a batch.
type AppendBatchItemResult = contract.AppendBatchItemResult

// CommittedEnvelope carries one committed message into post-commit effects.
type CommittedEnvelope = contract.CommittedEnvelope

// Recipient identifies one UID selected for committed-message effects.
type Recipient = contract.Recipient

// RecipientBatch carries one committed envelope and the recipients to process together.
type RecipientBatch = contract.RecipientBatch

// SubscriberPageRequest describes one channel subscriber page scan.
type SubscriberPageRequest = contract.SubscriberPageRequest

// SubscriberPage is one bounded subscriber scan page.
type SubscriberPage = contract.SubscriberPage

// ConversationPatch is a recipient-scoped conversation activity update.
type ConversationPatch = contract.ConversationPatch

// Route describes one online recipient endpoint resolved by presence.
type Route = contract.Route

// PushCommand groups recipient routes owned by the same node for one envelope.
type PushCommand = contract.PushCommand

// PushResult reports how an owner node classified pushed recipient routes.
type PushResult = contract.PushResult

var (
	// ErrNotChannelAuthority reports that the local node is not the channel authority.
	ErrNotChannelAuthority = contract.ErrNotChannelAuthority
	// ErrBackpressured reports bounded runtime pressure or closed admission.
	ErrBackpressured = contract.ErrBackpressured
	// ErrChannelBusy reports that channel-level write flow control is saturated.
	ErrChannelBusy = contract.ErrChannelBusy
	// ErrAppenderRequired reports that durable append is not configured.
	ErrAppenderRequired = contract.ErrAppenderRequired
	// ErrStaleRoute reports that append used stale channel metadata.
	ErrStaleRoute = contract.ErrStaleRoute
	// ErrRouteNotReady reports that cluster routing is not ready for foreground writes.
	ErrRouteNotReady = contract.ErrRouteNotReady
	// ErrNotLeader reports that the append target is no longer the leader.
	ErrNotLeader = contract.ErrNotLeader
	// ErrChannelNotFound reports that the target channel is not available.
	ErrChannelNotFound = contract.ErrChannelNotFound
	// ErrAppendFailed wraps unexpected append failures.
	ErrAppendFailed = contract.ErrAppendFailed
	// ErrAppendResultMissing reports a successful batch append response without a matching item result.
	ErrAppendResultMissing = contract.ErrAppendResultMissing
	// ErrRequestSubscribersRequireSyncOnce reports that request-scoped sends must be sync_once.
	ErrRequestSubscribersRequireSyncOnce = contract.ErrRequestSubscribersRequireSyncOnce
	// ErrRequestSubscribersConflictChannel reports that request-scoped sends cannot specify a channel.
	ErrRequestSubscribersConflictChannel = contract.ErrRequestSubscribersConflictChannel
	// ErrRequestSubscribersRequired reports that request-scoped sends need at least one usable subscriber.
	ErrRequestSubscribersRequired = contract.ErrRequestSubscribersRequired
	// ErrMessageIDAllocatorRequired reports that message id allocation is not configured.
	ErrMessageIDAllocatorRequired = contract.ErrMessageIDAllocatorRequired
)

// Group owns a set of channel-hashed local authority write reactors.
type Group struct {
	opts     Options
	reactors []*reactor

	mu       sync.RWMutex
	started  bool
	stopping bool
	stopped  bool
}

// New creates a channel write reactor group with conservative defaults.
func New(opts Options) *Group {
	opts = applyDefaults(opts)
	group := &Group{opts: opts}
	limits := stateLimitsFromOptions(opts)
	ports := preparePortsFromOptions(opts)
	appendPorts := appendPortsFromOptions(opts)
	commitPorts := commitPortsFromOptions(opts)
	for i := 0; i < opts.ReactorCount; i++ {
		group.reactors = append(group.reactors, newReactor(i, opts.MailboxSize, limits, opts.EffectWorkerCount, ports, appendPorts, commitPorts))
	}
	return group
}

// Start starts every reactor and opens local admission.
// A group that has already stopped is not restarted.
func (g *Group) Start(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping || g.stopped {
		return ErrBackpressured
	}
	if g.started && !g.stopped {
		return nil
	}
	for _, reactor := range g.reactors {
		reactor.start()
	}
	g.started = true
	g.stopping = false
	g.stopped = false
	return nil
}

// Stop closes admission and waits for accepted reactor events to drain.
func (g *Group) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	if !g.started || g.stopped {
		g.mu.Unlock()
		return nil
	}
	reactors := append([]*reactor(nil), g.reactors...)
	if !g.stopping {
		g.stopping = true
		for _, reactor := range reactors {
			reactor.close()
		}
	}
	g.mu.Unlock()

	for _, reactor := range reactors {
		if err := reactor.wait(ctx); err != nil {
			return err
		}
	}

	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()
	return nil
}

// SubmitLocal admits a batch to the local channel-authority reactor.
func (g *Group) SubmitLocal(ctx context.Context, target AuthorityTarget, items []SendBatchItem) (*Future, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if target.LeaderNodeID != g.opts.LocalNodeID {
		return nil, ErrNotChannelAuthority
	}

	copiedItems := cloneSendBatchItems(items)
	future := newFuture(len(copiedItems))

	g.mu.RLock()
	if !g.started || g.stopping || g.stopped {
		g.mu.RUnlock()
		return nil, ErrBackpressured
	}
	reactor := g.reactorForTarget(target)
	ack, err := reactor.enqueue(ctx, target, copiedItems, future)
	g.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if err := <-ack; err != nil {
		return nil, err
	}
	return future, nil
}

func (g *Group) reactorForTarget(target AuthorityTarget) *reactor {
	key := targetKey(target)
	idx := int(hashString64(key) % uint64(len(g.reactors)))
	return g.reactors[idx]
}

func targetKey(target AuthorityTarget) string {
	if target.ChannelKey != "" {
		return target.ChannelKey
	}
	return channelKey(target.ChannelID)
}

func channelKey(channelID ChannelID) string {
	return strconv.Itoa(int(channelID.Type)) + ":" + channelID.ID
}

func hashString64(value string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum64()
}

func cloneSendBatchItems(items []SendBatchItem) []SendBatchItem {
	if len(items) == 0 {
		return nil
	}
	copied := make([]SendBatchItem, len(items))
	for i := range items {
		copied[i] = items[i].Clone()
	}
	return copied
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
