package channelappend

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
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

// Route describes one online recipient endpoint resolved by presence.
type Route = contract.Route

// SubscriberMutationUpdate describes a committed subscriber-list change for one channel.
type SubscriberMutationUpdate struct {
	// ChannelID identifies the channel whose cached subscriber snapshot changed.
	ChannelID ChannelID
	// Large reports whether the channel should use paged subscriber fanout after the mutation.
	Large bool
	// SubscriberMutationVersion is the durable subscriber-list version after the mutation.
	SubscriberMutationVersion uint64
	// Reset reports that AddedUIDs replaces the cached snapshot instead of patching it.
	Reset bool
	// AddedUIDs are subscribers appended by this mutation.
	AddedUIDs []string
	// RemovedUIDs are subscribers removed by this mutation.
	RemovedUIDs []string
}

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

// Group owns a set of hash-sharded local authority channel appendrs.
type Group struct {
	opts   Options
	shards []*shard
	// advancePool runs non-blocking writer state-machine activation.
	advancePool *workerPool
	// pool runs blocking append and post-commit effects.
	pool *workerPool

	admissionCapacity int64
	admissionUsed     atomic.Int64
	runtimeCtx        context.Context
	runtimeCancel     context.CancelFunc
	metrics           groupMetrics

	mu       sync.RWMutex
	started  bool
	stopping bool
	stopped  bool
}

// New creates a channel append group with conservative defaults.
func New(opts Options) *Group {
	opts = applyDefaults(opts)
	limits := stateLimitsFromOptions(opts)
	advancePool := newWorkerPool(opts.AdvancePoolSize)
	pool := newWorkerPool(opts.EffectPoolSize)
	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	group := &Group{
		opts:              opts,
		advancePool:       advancePool,
		pool:              pool,
		shards:            make([]*shard, opts.AuthorityShardCount),
		admissionCapacity: int64(opts.AdmissionCapacityPerShard * opts.AuthorityShardCount),
		runtimeCtx:        runtimeCtx,
		runtimeCancel:     runtimeCancel,
	}
	var metrics *groupMetrics
	if observer := writerPressureObserver(opts.Observer); observer != nil {
		group.metrics = groupMetrics{
			observer:          observer,
			admissionUsed:     &group.admissionUsed,
			admissionCapacity: group.admissionCapacity,
			pool:              pool,
			advancePool:       advancePool,
		}
		metrics = &group.metrics
	}
	ports := writerPorts{
		prepare:    preparePortsFromOptions(opts),
		append:     appendPortsFromOptions(opts),
		commit:     commitPortsFromOptions(opts),
		pool:       pool,
		schedule:   group.schedule,
		runtimeCtx: runtimeCtx,
		metrics:    metrics,
	}
	for i := range group.shards {
		s := newShard(limits)
		s.ports = ports
		group.shards[i] = s
	}
	return group
}

func (g *Group) schedule(w *channelAppendr) {
	_ = g.advancePool.submit(func() { w.advance() })
}

func (g *Group) tryAcquireAdmission() bool {
	for {
		used := g.admissionUsed.Load()
		if used >= g.admissionCapacity {
			return false
		}
		if g.admissionUsed.CompareAndSwap(used, used+1) {
			return true
		}
	}
}

func (g *Group) releaseAdmission() {
	g.admissionUsed.Add(-1)
}

func (g *Group) shardForTarget(target AuthorityTarget) *shard {
	idx := int(hashString64(targetKey(target)) % uint64(len(g.shards)))
	return g.shards[idx]
}

// Start opens local admission. A group that has already stopped is not restarted.
func (g *Group) Start(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping || g.stopped {
		return ErrBackpressured
	}
	if g.started {
		return nil
	}
	g.started = true
	return nil
}

// Stop closes admission, drains accepted writer work, and releases the pool.
func (g *Group) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	if !g.started || g.stopped {
		g.mu.Unlock()
		return nil
	}
	g.stopping = true
	g.runtimeCancel()
	g.mu.Unlock()

	if err := g.drainWriters(ctx); err != nil {
		return err
	}
	if err := g.advancePool.stop(ctx); err != nil {
		return err
	}
	if err := g.pool.stop(ctx); err != nil {
		return err
	}

	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()
	return nil
}

// drainWriters waits until every writer has no pending work and is not scheduled.
func (g *Group) drainWriters(ctx context.Context) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if g.writersIdle() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (g *Group) writersIdle() bool {
	for _, s := range g.shards {
		s.mu.RLock()
		for _, w := range s.writers {
			if w.scheduled.Load() {
				s.mu.RUnlock()
				return false
			}
			w.mu.Lock()
			pending := len(w.inbox) > 0 || w.state.hasPendingWork()
			w.mu.Unlock()
			if pending {
				s.mu.RUnlock()
				return false
			}
		}
		s.mu.RUnlock()
	}
	return true
}

// ApplySubscriberMutation updates cached non-large subscriber snapshots after external metadata mutations.
func (g *Group) ApplySubscriberMutation(ctx context.Context, update SubscriberMutationUpdate) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if g == nil {
		return nil
	}
	g.mu.RLock()
	if !g.started || g.stopping || g.stopped || len(g.shards) == 0 {
		g.mu.RUnlock()
		return nil
	}
	g.mu.RUnlock()
	target := AuthorityTarget{
		ChannelID:                 update.ChannelID,
		ChannelKey:                channelKey(update.ChannelID),
		Large:                     update.Large,
		SubscriberMutationVersion: update.SubscriberMutationVersion,
	}
	writer := g.shardForTarget(target).lookup(targetKey(target))
	if writer == nil {
		return nil // no cached state for an unseen channel; nothing to update
	}
	writer.mu.Lock()
	writer.state.applySubscriberMutation(update.clone())
	writer.mu.Unlock()
	return nil
}

// SubmitLocal admits a batch to the local channel-authority writer.
func (g *Group) SubmitLocal(ctx context.Context, target AuthorityTarget, items []SendBatchItem) (*Future, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if target.LeaderNodeID != g.opts.LocalNodeID {
		return nil, ErrNotChannelAuthority
	}
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	g.mu.RLock()
	if !g.started || g.stopping || g.stopped {
		g.mu.RUnlock()
		return nil, ErrBackpressured
	}
	if !g.tryAcquireAdmission() {
		g.mu.RUnlock()
		observeLocalAdmission(g.opts.Observer, LocalAdmissionObservation{Result: channelAppendResultBackpressured, Items: len(items)})
		return nil, ErrBackpressured
	}
	g.mu.RUnlock()

	copiedItems := cloneSendBatchItems(items)
	future := newFuture(len(copiedItems))
	future.setOnDone(g.releaseAdmission)
	writer := g.shardForTarget(target).getOrCreate(target)
	if writer.enqueue(submittedBatch{target: target, items: copiedItems, future: future}) {
		g.schedule(writer)
	}
	observeLocalAdmission(g.opts.Observer, LocalAdmissionObservation{Result: "accepted", Items: len(items)})
	return future, nil
}

func (u SubscriberMutationUpdate) clone() SubscriberMutationUpdate {
	u.AddedUIDs = append([]string(nil), u.AddedUIDs...)
	u.RemovedUIDs = append([]string(nil), u.RemovedUIDs...)
	return u
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
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	hash := uint64(offset64)
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= prime64
	}
	return hash
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
