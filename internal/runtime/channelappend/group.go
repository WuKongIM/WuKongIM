package channelappend

import (
	"context"
	"strconv"
	"sync"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// Group owns a set of hash-sharded local authority channel writers.
type Group struct {
	opts   Options
	shards []*shard
	// advancePool runs non-blocking writer state-machine activation.
	advancePool *workerPool
	// advanceScheduler is the only blocking submitter to advancePool. It keeps
	// callers and effect completions out of cross-pool dependency cycles.
	advanceScheduler *writerAdvanceScheduler
	// appendPool runs foreground blocking durable append effects.
	appendPool *workerPool
	// postCommitPool runs best-effort post-commit and realtime recipient effects.
	postCommitPool *workerPool
	// handoff bounds append-bound messages across append and post-commit work.
	handoff *postCommitHandoff
	// postCommitRetries fairly retries writers rejected by the non-blocking effect pool.
	postCommitRetries *postCommitRetryScheduler

	runtimeCtx    context.Context
	runtimeCancel context.CancelFunc
	metrics       groupMetrics
	stopOnce      sync.Once
	stopDone      chan struct{}

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
	advanceScheduler := newWriterAdvanceScheduler(advancePool)
	appendPool := newWorkerPool(opts.EffectPoolSize)
	postCommitPool := newNonblockingWorkerPool(opts.EffectPoolSize)
	handoff := newPostCommitHandoff(opts.PostCommitHandoffCapacity)
	postCommitRetries := newPostCommitRetryScheduler()
	postCommitRetries.capacityAvailable = func() bool {
		return postCommitPool.running() < postCommitPool.capacity()
	}
	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	group := &Group{
		opts:              opts,
		advancePool:       advancePool,
		advanceScheduler:  advanceScheduler,
		appendPool:        appendPool,
		postCommitPool:    postCommitPool,
		handoff:           handoff,
		postCommitRetries: postCommitRetries,
		shards:            make([]*shard, opts.AuthorityShardCount),
		runtimeCtx:        runtimeCtx,
		runtimeCancel:     runtimeCancel,
		stopDone:          make(chan struct{}),
	}
	for i := range group.shards {
		group.shards[i] = newShard(limits, int64(opts.AdmissionCapacityPerShard), opts.WriterIdleRetention)
	}
	var metrics *groupMetrics
	if observer := writerPressureObserver(opts.Observer); observer != nil {
		group.metrics = groupMetrics{
			observer:          observer,
			admissionShards:   group.shards,
			admissionCapacity: int64(opts.AdmissionCapacityPerShard * opts.AuthorityShardCount),
			appendPool:        appendPool,
			postCommitPool:    postCommitPool,
			advancePool:       advancePool,
			advanceScheduler:  advanceScheduler,
			handoff:           handoff,
			postCommitRetries: postCommitRetries,
		}
		metrics = &group.metrics
	}
	advanceScheduler.onStateChange = group.metrics.observePressure
	ports := writerPorts{
		prepare:        preparePortsFromOptions(opts),
		append:         appendPortsFromOptions(opts),
		commit:         commitPortsFromOptions(opts),
		appendPool:     appendPool,
		postCommitPool: postCommitPool,
		handoff:        handoff,
		commitRetries:  postCommitRetries,
		schedule:       group.schedule,
		runtimeCtx:     runtimeCtx,
		metrics:        metrics,

		inboxCoalesceWindow:   opts.InboxCoalesceWindow,
		inboxCoalesceMaxItems: opts.InboxCoalesceMaxItems,
	}
	for i := range group.shards {
		group.shards[i].ports = ports
	}
	return group
}

func (g *Group) schedule(w *channelWriter) {
	g.advanceScheduler.schedule(w)
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
	if g.stopping || g.stopped {
		g.mu.Unlock()
		return ErrBackpressured
	}
	if g.started {
		g.mu.Unlock()
		return nil
	}
	g.advanceScheduler.start()
	g.metrics.startPressurePublisher()
	g.postCommitRetries.start()
	g.started = true
	g.mu.Unlock()
	g.metrics.observePressure()
	return nil
}

// Stop closes admission, drains accepted work, and only then cancels the
// runtime and releases its pools. A deadline preserves all unfinished state so
// a later Stop call can continue the same drain.
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
	g.mu.Unlock()
	g.stopOnce.Do(func() {
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskChannelAppendStopDrain, g.finishStop)
	})
	select {
	case <-g.stopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// finishStop owns the one-way background drain. Caller deadlines only stop
// waiting; they never cancel accepted work or discard retry ownership.
func (g *Group) finishStop() {
	background := context.Background()
	_ = g.drainWriters(background)
	_ = g.postCommitRetries.stopAndWait(background)
	g.advanceScheduler.stop()
	_ = g.metrics.stopPressurePublisher(background)
	_ = g.advancePool.stop(background)
	_ = g.appendPool.stop(background)
	_ = g.postCommitPool.stop(background)

	g.runtimeCancel()
	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()
	close(g.stopDone)
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
	if g.postCommitRetries.pending() > 0 || g.handoff.depth() > 0 {
		return false
	}
	for _, s := range g.shards {
		if s.admissionUsed.Load() > 0 {
			return false
		}
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
		return nil, ErrRouteNotReady
	}
	shard := g.shardForTarget(target)
	if !shard.tryAcquireAdmission() {
		g.mu.RUnlock()
		observeLocalAdmission(g.opts.Observer, LocalAdmissionObservation{Result: channelAppendResultBackpressured, Items: len(items)})
		return nil, ErrBackpressured
	}
	g.mu.RUnlock()

	copiedItems := copySendBatchItems(items)
	future := newFuture(len(copiedItems))
	future.setOnDone(shard.releaseAdmission)
	writer := shard.getOrCreate(target)
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

func copySendBatchItems(items []SendBatchItem) []SendBatchItem {
	if len(items) == 0 {
		return nil
	}
	return append([]SendBatchItem(nil), items...)
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
