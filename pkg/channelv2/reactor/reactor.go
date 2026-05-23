package reactor

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
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
	// NextOpID allocates reactor-owned batch operation IDs distinct from client operation IDs.
	NextOpID func() ch.OpID
}

// Reactor owns channel states for one hash partition.
type Reactor struct {
	cfg      ReactorConfig
	mailbox  *Mailbox
	channels map[ch.ChannelKey]*runtimeChannel
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	nextOp   atomic.Uint64
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
	case EventApplyRecords:
		r.handleApplyRecords(event)
	case EventWorkerResult:
		r.handleWorkerResult(event)
	case EventTick:
		r.handleTick(event)
	case EventClose:
		r.handleClose(event)
	}
}

func (r *Reactor) handleWorkerResult(event Event) {
	switch event.Worker.Kind {
	case worker.TaskStoreAppend:
		r.handleStoreAppendResult(event.Worker)
	case worker.TaskStoreReadCommitted:
		r.handleStoreReadCommittedResult(event.Worker)
	}
}

func (r *Reactor) handleTick(event Event) {
	now := event.TickNow
	if now.IsZero() {
		now = time.Now()
	}
	r.flushDueAppends(now)
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
	}
	decision := rc.state.ApplyMeta(event.Meta)
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
	if completeReplies(rc, decision.Replies, event.Future) {
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
	if completeReplies(rc, decision.Replies, nil) {
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
	completeReplies(rc, decision.Replies, nil)
	if current {
		if stored.Err != nil {
			for _, req := range rc.appendInflight.requests {
				if future := rc.waiters[req.opID]; future != nil {
					delete(rc.waiters, req.opID)
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
	if req, ok := rc.appendQ.remove(event.CancelOp); ok && req.future != nil {
		delete(rc.waiters, event.CancelOp)
		rc.state.CancelAppendWaiter(event.CancelOp)
		req.future.Complete(Result{Err: cancelErr})
	} else if future := rc.waiters[event.CancelOp]; future != nil {
		delete(rc.waiters, event.CancelOp)
		future.Complete(Result{Err: cancelErr})
		if _, isFetch := rc.fetchWaiters[event.CancelOp]; !isFetch {
			rc.state.CancelAppendWaiter(event.CancelOp)
		}
	}
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
	read, err := rc.store.ReadLog(context.Background(), store.ReadLogRequest{FromOffset: event.Pull.NextOffset, MaxOffset: rc.state.LEO, MaxBytes: event.Pull.MaxBytes})
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	event.Future.Complete(Result{Pull: transport.PullResponse{ChannelKey: rc.state.Key, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, LeaderHW: rc.state.HW, LeaderLEO: rc.state.LEO, Records: read.Records}})
}

func (r *Reactor) handleAck(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: event.Ack.Follower, MatchOffset: event.Ack.MatchOffset})
	for _, reply := range decision.Replies {
		future := rc.waiters[reply.OpID]
		if future == nil {
			continue
		}
		delete(rc.waiters, reply.OpID)
		future.Complete(Result{AppendBatch: ch.AppendBatchResult{Items: reply.AppendItems}, Err: reply.Err})
	}
	event.Future.Complete(Result{})
}

func (r *Reactor) handleApplyRecords(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleFollower {
		event.Future.Complete(Result{Err: ch.ErrNotReady})
		return
	}
	apply, err := rc.store.ApplyFollower(context.Background(), store.ApplyFollowerRequest{Records: event.PullResponse.Records, LeaderHW: event.PullResponse.LeaderHW})
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	rc.state.LEO = apply.LEO
	if event.PullResponse.LeaderHW < rc.state.LEO {
		rc.state.HW = event.PullResponse.LeaderHW
	} else {
		rc.state.HW = rc.state.LEO
	}
	event.Future.Complete(Result{ApplyLEO: apply.LEO})
}
