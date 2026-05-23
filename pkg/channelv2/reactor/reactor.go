package reactor

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

const defaultReactorDrain = 128

// ReactorConfig wires one reactor.
type ReactorConfig struct {
	ID        int
	LocalNode ch.NodeID
	Store     store.Factory
	Pools     *worker.Pools
	// MailboxSize bounds each priority queue in this reactor.
	MailboxSize int
	// AppendBatchMaxRecords is the queued record count that triggers a store append flush.
	AppendBatchMaxRecords int
	// AppendBatchMaxBytes is the queued payload byte budget that triggers a store append flush.
	AppendBatchMaxBytes int
	// AppendBatchMaxWait is the maximum age of the oldest queued append before flushing.
	AppendBatchMaxWait time.Duration
	// AppendQueueMaxRequests bounds accepted append requests waiting per channel.
	AppendQueueMaxRequests int
	// AppendQueueMaxBytes bounds accepted append payload bytes waiting per channel.
	AppendQueueMaxBytes int
	// AppendStoreRetryBackoff delays retry after the store append worker pool rejects a batch.
	AppendStoreRetryBackoff time.Duration
	// ReplicationIdlePollInterval delays the next follower poll when a leader has no new records; defaults to 10ms.
	ReplicationIdlePollInterval time.Duration
	// ReplicationMinBackoff is the first retry delay after pull, apply, or ack failures; defaults to 1ms.
	ReplicationMinBackoff time.Duration
	// ReplicationMaxBackoff caps follower replication retry delays after repeated failures; defaults to 100ms.
	ReplicationMaxBackoff time.Duration
	// PullMaxBytes bounds one follower pull response requested from the leader; defaults to 64 KiB.
	PullMaxBytes int
	// NextOpID allocates reactor-owned batch operation IDs distinct from client operation IDs.
	NextOpID func() ch.OpID
}

// Reactor owns channel states for one hash partition.
type Reactor struct {
	cfg      ReactorConfig
	mailbox  *Mailbox
	channels map[ch.ChannelKey]*runtimeChannel
	// appendCancelChannels indexes channels that have admitted append contexts to sweep.
	appendCancelChannels map[ch.ChannelKey]*runtimeChannel
	stop                 chan struct{}
	done                 chan struct{}
	once                 sync.Once
	nextOp               atomic.Uint64
}

type runtimeChannel struct {
	state   *machine.ChannelState
	store   store.ChannelStore
	waiters map[ch.OpID]*Future
	// fetchWaiters marks waiter entries that must be fenced by metadata changes.
	fetchWaiters map[ch.OpID]struct{}
	// appendQ holds accepted append requests before they are flushed as durable batches.
	appendQ appendQueue
	// appendInflight is the currently submitted durable append batch.
	appendInflight *appendBatch
	// appendStoreBlocked records store worker-pool backpressure that delays retry.
	appendStoreBlocked bool
	// appendRetryAt is the earliest time to retry after store worker-pool backpressure.
	appendRetryAt time.Time
	// appendCancelContexts tracks admitted append caller contexts across queued, inflight, and post-store states.
	appendCancelContexts map[ch.OpID]context.Context
	// replication owns follower pull, apply, and ack scheduling state.
	replication replicationState
	// pullWaiters maps leader-side async pull op ids to request futures.
	pullWaiters map[ch.OpID]*Future
}

// NewReactor constructs a reactor.
func NewReactor(cfg ReactorConfig) *Reactor {
	cfg = defaultReactorConfig(cfg)
	return &Reactor{cfg: cfg, mailbox: NewMailbox(MailboxConfig{HighSize: cfg.MailboxSize, NormalSize: cfg.MailboxSize, LowSize: cfg.MailboxSize}), channels: make(map[ch.ChannelKey]*runtimeChannel), stop: make(chan struct{}), done: make(chan struct{})}
}

func (r *Reactor) start() { go r.loop() }

// Submit enqueues an event.
func (r *Reactor) Submit(priority Priority, event Event) error {
	select {
	case <-r.stop:
		return ch.ErrClosed
	default:
	}
	return r.mailbox.Submit(priority, event)
}

// SubmitCompletion blocks until a high-priority worker completion is enqueued or closed.
func (r *Reactor) SubmitCompletion(event Event) error {
	if r == nil || r.mailbox == nil {
		return ch.ErrClosed
	}
	select {
	case <-r.stop:
		return ch.ErrClosed
	default:
	}
	select {
	case r.mailbox.high <- event:
		return nil
	case <-r.stop:
		return ch.ErrClosed
	}
}

// Close stops the reactor and fails future work by closing the loop.
func (r *Reactor) Close() error {
	r.once.Do(func() { close(r.stop) })
	<-r.done
	return nil
}

func (r *Reactor) loop() {
	defer func() {
		r.failPendingWaiters(ch.ErrClosed)
		close(r.done)
	}()
	for {
		select {
		case <-r.stop:
			return
		default:
		}
		events := r.mailbox.Drain(defaultReactorDrain)
		if len(events) == 0 {
			r.flushDueAppends(time.Now())
			select {
			case <-r.stop:
				return
			case <-time.After(time.Millisecond):
			}
			continue
		}
		for _, event := range events {
			r.handle(event)
		}
	}
}

func (r *Reactor) handle(event Event) {
	r.sweepAppendCancellations()
	switch event.Kind {
	case EventApplyMeta:
		r.handleApplyMeta(event)
	case EventAppend:
		r.handleAppend(event)
	case EventCancelWaiter:
		r.handleCancelWaiter(event)
	case EventFetch:
		r.handleFetch(event)
	case EventPull:
		r.handlePull(event)
	case EventAck:
		r.handleAck(event)
	case EventWorkerResult:
		r.handleWorkerResult(event)
	case EventTick:
		r.handleTick(event)
	case EventClose:
		r.handleClose(event)
	}
	r.sweepAppendCancellations()
}

func (r *Reactor) handleWorkerResult(event Event) {
	switch event.Worker.Kind {
	case worker.TaskStoreAppend:
		r.handleStoreAppendResult(event.Worker)
	case worker.TaskStoreReadCommitted:
		r.handleStoreReadCommittedResult(event.Worker)
	case worker.TaskStoreReadLog:
		r.handleStoreReadLogResult(event.Worker)
	case worker.TaskRPCPull:
		r.handleRPCPullResult(event.Worker)
	case worker.TaskStoreApply:
		r.handleStoreApplyResult(event.Worker)
	case worker.TaskRPCAck:
		r.handleRPCAckResult(event.Worker)
	}
}

func (r *Reactor) handleTick(event Event) {
	now := event.TickNow
	if now.IsZero() {
		now = time.Now()
	}
	r.flushDueAppends(now)
	for _, rc := range r.channels {
		r.tickReplication(rc, now)
	}
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

func (r *Reactor) handleClose(event Event) {
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
	r.once.Do(func() { close(r.stop) })
}

func (r *Reactor) failPendingWaiters(err error) {
	if r == nil {
		return
	}
	for _, rc := range r.channels {
		rc.failWaiters(err)
		r.clearAppendCancelContexts(rc)
	}
}

func (r *Reactor) handleApplyMeta(event Event) {
	key := event.Meta.Key
	if key == "" {
		key = ch.ChannelKeyForID(event.Meta.ID)
	}
	existing := r.channels[key]
	rc, err := r.ensureChannel(event.Meta)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	fencePendingState := existing != nil && metadataWouldFenceState(rc.state, event.Meta)
	if fencePendingState {
		if err := rc.state.ValidateMeta(event.Meta); err != nil {
			event.Future.Complete(Result{Err: err})
			return
		}
		rc.failPendingFetchWaiters(ch.ErrStaleMeta)
		rc.failPendingAppendWaiters(ch.ErrStaleMeta)
		rc.failPendingPullWaiters(ch.ErrStaleMeta)
		r.clearAppendCancelContexts(rc)
	}
	decision := rc.state.ApplyMeta(event.Meta)
	if decision.Err == nil {
		if rc.state.Role == ch.RoleFollower && rc.state.Status == ch.StatusActive {
			rc.replication.markDirty(time.Now())
		} else {
			rc.replication.reset()
		}
	}
	event.Future.Complete(Result{Err: decision.Err})
}

func (r *Reactor) ensureChannel(meta ch.Meta) (*runtimeChannel, error) {
	key := meta.Key
	if key == "" {
		key = ch.ChannelKeyForID(meta.ID)
	}
	if rc := r.channels[key]; rc != nil {
		return rc, nil
	}
	cs, err := r.cfg.Store.ChannelStore(key, meta.ID)
	if err != nil {
		return nil, err
	}
	initial, err := cs.Load(context.Background())
	if err != nil {
		return nil, err
	}
	state := machine.NewChannelState(key, r.cfg.LocalNode, 1)
	state.ID = meta.ID
	state.LEO = initial.LEO
	state.HW = initial.HW
	state.CheckpointHW = initial.CheckpointHW
	rc := &runtimeChannel{
		state:        state,
		store:        cs,
		waiters:      make(map[ch.OpID]*Future),
		fetchWaiters: make(map[ch.OpID]struct{}),
		pullWaiters:  make(map[ch.OpID]*Future),
		appendQ: newAppendQueue(appendQueueConfig{
			MaxRecords:      r.cfg.AppendBatchMaxRecords,
			MaxBytes:        r.cfg.AppendBatchMaxBytes,
			MaxWait:         r.cfg.AppendBatchMaxWait,
			MaxPending:      r.cfg.AppendQueueMaxRequests,
			MaxPendingBytes: r.cfg.AppendQueueMaxBytes,
		}),
	}
	r.channels[key] = rc
	return rc, nil
}

func (r *Reactor) lookup(key ch.ChannelKey) (*runtimeChannel, error) {
	if rc := r.channels[key]; rc != nil {
		return rc, nil
	}
	return nil, ch.ErrChannelNotFound
}

func (r *Reactor) handleAppend(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if event.Context != nil {
		if err := event.Context.Err(); err != nil {
			event.Future.Complete(Result{Err: err})
			return
		}
	}
	if _, ok := rc.waiters[event.OpID]; ok {
		event.Future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	if rc.state.Status == ch.StatusDeleted || rc.state.Status == ch.StatusDeleting {
		event.Future.Complete(Result{Err: ch.ErrChannelNotFound})
		return
	}
	if rc.state.Role != ch.RoleLeader {
		event.Future.Complete(Result{Err: ch.ErrNotLeader})
		return
	}
	if !rc.state.CommitReady {
		event.Future.Complete(Result{Err: ch.ErrNotReady})
		return
	}
	records := make([]ch.Record, len(event.Append.Messages))
	for i, msg := range event.Append.Messages {
		records[i] = ch.Record{ID: msg.MessageID, Payload: append([]byte(nil), msg.Payload...), SizeBytes: len(msg.Payload)}
	}
	mode := event.Append.CommitMode
	if mode == 0 {
		mode = ch.CommitModeQuorum
	}
	req := appendRequest{opID: event.OpID, req: event.Append, future: event.Future, ctx: event.Context, enqueuedAt: time.Now(), records: records, commitMode: mode}
	if err := rc.appendQ.push(req); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if err := rc.addWaiter(event.OpID, event.Future); err != nil {
		rc.appendQ.remove(event.OpID)
		event.Future.Complete(Result{Err: err})
		return
	}
	r.registerAppendCancelContext(rc, event.OpID, event.Context)
	r.tryFlushAppend(rc, time.Now())
}

func (r *Reactor) handleFetch(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	decision := rc.state.BuildFetch(machine.FetchCommand{OpID: event.OpID, FromSeq: event.Fetch.FromSeq, Limit: event.Fetch.Limit, MaxBytes: event.Fetch.MaxBytes})
	if decision.Err != nil {
		event.Future.Complete(Result{Err: decision.Err})
		return
	}
	if r.completeReplies(rc, decision.Replies, event.Future) {
		return
	}
	if len(decision.Tasks) == 0 {
		event.Future.Complete(Result{})
		return
	}
	if err := rc.addFetchWaiter(event.OpID, event.Future); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if err := r.submitStoreReadCommitted(context.Background(), event.Fetch.ChannelID, decision.Tasks[0]); err != nil {
		rc.removeFetchWaiter(event.OpID)
		event.Future.Complete(Result{Err: err})
	}
}

func (r *Reactor) handleStoreReadCommittedResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	readErr := result.Err
	readResult := machine.ReadCommittedResult{Fence: result.Fence, Err: readErr}
	if result.StoreReadCommitted == nil {
		if readErr == nil {
			readResult.Err = ch.ErrInvalidConfig
		}
	} else {
		readResult.Messages = result.StoreReadCommitted.Messages
		readResult.NextSeq = result.StoreReadCommitted.NextSeq
	}
	decision := rc.state.ApplyReadCommitted(readResult)
	if r.completeReplies(rc, decision.Replies, nil) {
		return
	}
	rc.completeStaleFetchIfWaiting(result.Fence.OpID)
}

func (r *Reactor) handleStoreAppendResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	current := rc.appendInflight != nil && rc.appendInflight.batchOpID == result.Fence.OpID
	appendErr := result.Err
	stored := machine.AppendStoredResult{Fence: result.Fence, Err: appendErr}
	if result.StoreAppend == nil {
		if appendErr == nil {
			stored.Err = ch.ErrInvalidConfig
		}
	} else {
		stored.BaseOffset = result.StoreAppend.BaseOffset
		stored.LastOffset = result.StoreAppend.LastOffset
	}
	decision := rc.state.ApplyAppendStored(stored)
	r.completeReplies(rc, decision.Replies, nil)
	if current {
		if stored.Err != nil {
			for _, req := range rc.appendInflight.requests {
				if future := rc.waiters[req.opID]; future != nil {
					delete(rc.waiters, req.opID)
					r.unregisterAppendCancelContext(rc, req.opID)
					future.Complete(Result{Err: stored.Err})
				}
			}
		}
		rc.appendInflight = nil
		rc.appendQ.storeBlocked = false
		r.tryFlushAppend(rc, time.Now())
	}
}

func (r *Reactor) handleCancelWaiter(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	cancelErr := event.CancelErr
	if cancelErr == nil {
		cancelErr = context.Canceled
	}
	r.cancelAppendWaiter(rc, event.CancelOp, cancelErr)
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

func (r *Reactor) handlePull(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleLeader {
		event.Future.Complete(Result{Err: ch.ErrNotLeader})
		return
	}
	if event.Pull.Epoch != rc.state.Epoch || event.Pull.LeaderEpoch != rc.state.LeaderEpoch || !rc.state.IsReplica(event.Pull.Follower) {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if event.OpID == 0 {
		event.Future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	if rc.pullWaiters == nil {
		rc.pullWaiters = make(map[ch.OpID]*Future)
	}
	if _, ok := rc.pullWaiters[event.OpID]; ok {
		event.Future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	rc.pullWaiters[event.OpID] = event.Future
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: event.OpID}
	err = r.submitStoreReadLog(context.Background(), event.Pull.ChannelID, fence, event.Pull.NextOffset, rc.state.LEO, event.Pull.MaxBytes)
	if err != nil {
		delete(rc.pullWaiters, event.OpID)
		event.Future.Complete(Result{Err: err})
	}
}

func (r *Reactor) handleAck(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleLeader || event.Ack.Epoch != rc.state.Epoch || event.Ack.LeaderEpoch != rc.state.LeaderEpoch || !rc.state.IsReplica(event.Ack.Follower) {
		event.Future.Complete(Result{})
		return
	}
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: event.Ack.Follower, MatchOffset: event.Ack.MatchOffset})
	r.completeReplies(rc, decision.Replies, nil)
	event.Future.Complete(Result{})
}
