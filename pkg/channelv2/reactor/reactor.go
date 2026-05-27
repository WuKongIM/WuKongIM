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
	// ReplicationIdlePollInterval delays the next follower poll when a leader has no new records; defaults to 10ms.
	ReplicationIdlePollInterval time.Duration
	// ReplicationMinBackoff is the first retry delay after pull, apply, or ack failures; defaults to 1ms.
	ReplicationMinBackoff time.Duration
	// ReplicationMaxBackoff caps follower replication retry delays after repeated failures; defaults to 100ms.
	ReplicationMaxBackoff time.Duration
	// PullMaxBytes bounds one follower pull response requested from the leader; defaults to 64 KiB.
	PullMaxBytes int
	// LeaderRecentRecordCacheSize bounds recently appended leader log records kept for follower pulls.
	LeaderRecentRecordCacheSize int
	// LeaderRecentRecordCacheBytes is a retained payload-byte soft cap for the per-channel leader log cache; the newest oversized record may exceed it.
	LeaderRecentRecordCacheBytes int
	// IdleSlowdownAfter is the idle duration after the last Append before follower pull intervals begin increasing.
	IdleSlowdownAfter time.Duration
	// IdleEvictAfter is the idle duration after the last Append before a leader may ask caught-up followers to stop.
	IdleEvictAfter time.Duration
	// IdlePullMinInterval is the shortest no-record follower pull delay returned by a leader; defaults to ReplicationIdlePollInterval.
	IdlePullMinInterval time.Duration
	// IdlePullMaxInterval is the longest parked follower pull delay returned by a leader.
	IdlePullMaxInterval time.Duration
	// IdleEvictCheckInterval is the retry interval for lifecycle checks while eviction is blocked.
	IdleEvictCheckInterval time.Duration
	// PullHintRetryInterval is the retry interval for best-effort PullHint while a follower still needs progress.
	PullHintRetryInterval time.Duration
	// Observer receives lightweight reactor metrics; nil uses a no-op observer.
	Observer Observer
	// NextOpID allocates reactor-owned batch operation IDs distinct from client operation IDs.
	NextOpID func() ch.OpID
}

// Reactor owns channel states for one hash partition.
type Reactor struct {
	cfg      ReactorConfig
	mailbox  *Mailbox
	drainBuf []Event
	channels map[ch.ChannelKey]*runtimeChannel
	// appendCancelChannels indexes channels that have admitted append contexts to sweep.
	appendCancelChannels map[ch.ChannelKey]*runtimeChannel
	// pullCancelChannels indexes leader pull waiters with cancellable caller contexts.
	pullCancelChannels map[ch.ChannelKey]*runtimeChannel
	// due schedules channel maintenance without scanning every loaded channel.
	due    dueScheduler
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	nextOp atomic.Uint64
	// submitMu orders final leader eviction against concurrent Append submissions.
	submitMu sync.Mutex
	// appendSubmitSeqs increments per channel before every Append reservation or mailbox submission.
	appendSubmitSeqs map[ch.ChannelKey]uint64
	// appendReservations counts appends between loaded-state verification and mailbox submission.
	appendReservations map[ch.ChannelKey]int
}

type runtimeChannel struct {
	state   *machine.ChannelState
	store   store.ChannelStore
	waiters map[ch.OpID]*Future
	// appendQ holds accepted append requests before they are flushed as durable batches.
	appendQ appendQueue
	// appendInflight is the currently submitted durable append batch.
	appendInflight *appendBatch
	// recentRecords keeps a leader-owned suffix of durable log records for follower pulls.
	recentRecords recentRecordCache
	// appendStoreBlocked records store worker-pool backpressure that delays retry.
	appendStoreBlocked bool
	// appendRetryAt is the earliest time to retry after store worker-pool backpressure.
	appendRetryAt time.Time
	// appendCancelContexts tracks admitted append caller contexts across queued, inflight, and post-store states.
	appendCancelContexts map[ch.OpID]context.Context
	// appendTimings tracks accepted append requests until their futures complete.
	appendTimings map[ch.OpID]appendTiming
	// replication owns follower pull, apply, and ack scheduling state.
	replication replicationState
	// lifecycle tracks leader-owned activity and idle eviction state.
	lifecycle channelLifecycle
	// runtimeLifecycle tracks explicit leader/follower runtime eviction phases.
	runtimeLifecycle runtimeLifecycle
	// followers stores leader-owned runtime state for remote replicas.
	followers map[ch.NodeID]*followerLifecycle
	// pullHintInflight maps worker op ids back to followers waiting for completion.
	pullHintInflight map[ch.OpID]pullHintInflight
	// pullWaiters maps leader-side async pull op ids to request futures.
	pullWaiters map[ch.OpID]*pullWaiter
	// due versions fence stale scheduler entries after channel state changes.
	appendFlushDueVersion uint64
	replicationDueVersion uint64
	lifecycleDueVersion   uint64
}

type pullHintInflight struct {
	follower        ch.NodeID
	activityVersion uint64
	reason          transport.PullHintReason
}

type pullWaiter struct {
	// future completes the leader-side pull request once the store read is fenced back.
	future *Future
	// ctx is the caller context used to cancel the waiter before a blocked store read returns.
	ctx context.Context
	// follower is the replica waiting for this pull response.
	follower ch.NodeID
	// nextOffset is the requested offset used to prove caught-up stop eligibility.
	nextOffset uint64
	// maxBytes is the follower's response byte budget.
	maxBytes int
	// mergeCacheSuffix asks the store-read completion path to append the cached suffix after the read range.
	mergeCacheSuffix bool
}

// NewReactor constructs a reactor.
func NewReactor(cfg ReactorConfig) *Reactor {
	cfg = defaultReactorConfig(cfg)
	return &Reactor{cfg: cfg, mailbox: NewMailbox(MailboxConfig{HighSize: cfg.MailboxSize, NormalSize: cfg.MailboxSize, LowSize: cfg.MailboxSize}), drainBuf: make([]Event, 0, defaultReactorDrain), channels: make(map[ch.ChannelKey]*runtimeChannel), stop: make(chan struct{}), done: make(chan struct{})}
}

func (r *Reactor) start() { go r.loop() }

// Submit enqueues an event.
func (r *Reactor) Submit(priority Priority, event Event) error {
	select {
	case <-r.stop:
		return ch.ErrClosed
	default:
	}
	if event.Kind == EventAppend {
		r.submitMu.Lock()
		r.bumpAppendSubmitSeqLocked(event.Key)
		err := r.mailbox.Submit(priority, event)
		r.submitMu.Unlock()
		r.observeMailboxDepth(priority)
		return err
	}
	err := r.mailbox.Submit(priority, event)
	r.observeMailboxDepth(priority)
	return err
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
		r.observeMailboxDepth(PriorityHigh)
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
	idleTimer := time.NewTimer(time.Hour)
	stopTimer(idleTimer)
	defer func() {
		stopTimer(idleTimer)
		r.failPendingWaiters(ch.ErrClosed)
		close(r.done)
	}()
	for {
		select {
		case <-r.stop:
			return
		default:
		}
		events := r.mailbox.DrainInto(r.drainBuf, defaultReactorDrain)
		r.observeAllMailboxDepths()
		if len(events) == 0 {
			r.sweepAppendCancellations()
			r.sweepPullCancellations()
			now := time.Now()
			r.processDue(now)
			resetTimer(idleTimer, r.idleWait(now))
			event, ok := r.mailbox.WaitOne(r.stop, idleTimer.C)
			stopTimer(idleTimer)
			if !ok {
				select {
				case <-r.stop:
					return
				default:
				}
				continue
			}
			r.handle(event)
			continue
		}
		for i := range events {
			r.handle(events[i])
			events[i] = Event{}
		}
		r.drainBuf = events[:0]
	}
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if d < 0 {
		d = 0
	}
	stopTimer(timer)
	timer.Reset(d)
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (r *Reactor) idleWait(now time.Time) time.Duration {
	wait := r.due.nextWait(now)
	if wait > time.Millisecond && (len(r.appendCancelChannels) > 0 || len(r.pullCancelChannels) > 0) {
		return time.Millisecond
	}
	return wait
}

func (r *Reactor) handle(event Event) {
	r.sweepAppendCancellations()
	r.sweepPullCancellations()
	switch event.Kind {
	case EventApplyMeta:
		r.handleApplyMeta(event)
	case EventCheckState:
		r.handleCheckState(event)
	case EventAppend:
		r.handleAppend(event)
	case EventCancelWaiter:
		r.handleCancelWaiter(event)
	case EventPull:
		r.handlePull(event)
	case EventAck:
		r.handleAck(event)
	case EventNotify:
		r.handleNotify(event)
	case EventPullHint:
		r.handlePullHint(event)
	case EventLeaderEvictReady:
		r.handleLeaderEvictReady(event)
	case EventWorkerResult:
		r.handleWorkerResult(event)
	case EventTick:
		r.handleTick(event)
	case EventClose:
		r.handleClose(event)
	}
	r.sweepAppendCancellations()
	r.sweepPullCancellations()
}

func (r *Reactor) handlePullHint(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	req := event.PullHint
	if req.ChannelKey != "" && req.ChannelKey != rc.state.Key {
		err = ch.ErrStaleMeta
	} else if req.ChannelID != (ch.ChannelID{}) && req.ChannelID != rc.state.ID {
		err = ch.ErrStaleMeta
	} else if rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive ||
		req.Epoch != rc.state.Epoch || req.LeaderEpoch != rc.state.LeaderEpoch ||
		req.Leader != rc.state.Leader || req.Leader == r.cfg.LocalNode ||
		!rc.state.IsReplica(r.cfg.LocalNode) {
		err = ch.ErrStaleMeta
	}
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	if req.ActivityVersion < rc.replication.lastActivityVersion {
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	now := time.Now()
	if req.ActivityVersion > rc.replication.lastActivityVersion {
		rc.replication.cancelStopping()
		rc.runtimeLifecycle.FollowerPhase = FollowerLifecycleReplicating
		rc.replication.lastActivityVersion = req.ActivityVersion
	}
	rc.replication.parked = false
	rc.replication.nextPullAfter = 0
	rc.replication.markDirty(now)
	r.tickReplication(rc, now)
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

func (r *Reactor) handleWorkerResult(event Event) {
	switch event.Worker.Kind {
	case worker.TaskStoreAppend:
		r.handleStoreAppendResult(event.Worker)
	case worker.TaskStoreReadLog:
		r.handleStoreReadLogResult(event.Worker)
	case worker.TaskStoreCheckpoint:
		r.handleStoreCheckpointResult(event.Worker)
	case worker.TaskRPCPull:
		r.handleRPCPullResult(event.Worker)
	case worker.TaskStoreApply:
		r.handleStoreApplyResult(event.Worker)
	case worker.TaskRPCAck:
		r.handleRPCAckResult(event.Worker)
	case worker.TaskRPCPullHint:
		r.handleRPCPullHintResult(event.Worker)
	}
}

func (r *Reactor) handleTick(event Event) {
	now := event.TickNow
	if now.IsZero() {
		now = time.Now()
	}
	r.processDue(now)
	if event.Key != "" {
		if rc := r.channels[event.Key]; rc != nil {
			r.tryFlushAppend(rc, now)
			r.tickReplication(rc, now)
			r.tickLifecycle(rc, now)
		}
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
		r.failWaiters(rc, err)
		r.clearAppendCancelContexts(rc)
		r.clearPullCancelChannel(rc)
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
		r.failPendingAppendWaiters(rc, ch.ErrStaleMeta)
		rc.failPendingPullWaiters(ch.ErrStaleMeta)
		r.clearPullCancelChannel(rc)
		r.clearAppendCancelContexts(rc)
	}
	decision := rc.state.ApplyMeta(event.Meta)
	if decision.Err == nil {
		if fencePendingState {
			resetLeaderCheckpointLifecycle(rc)
			rc.replication.reset()
			rc.recentRecords.reset()
			r.resetPullHintLifecycle(rc)
		}
		if rc.state.Role == ch.RoleFollower && rc.state.Status == ch.StatusActive {
			rc.replication.markDirty(time.Time{})
		} else {
			rc.replication.reset()
		}
		if rc.state.Role == ch.RoleLeader {
			if rc.state.LEO > rc.lifecycle.ActivityVersion {
				rc.lifecycle.ActivityVersion = rc.state.LEO
			}
			r.syncLeaderFollowers(rc)
			if fencePendingState {
				resetFollowerStopLifecycle(rc)
			}
			r.scheduleLifecycleFromState(rc, time.Now())
		} else {
			rc.recentRecords.reset()
			rc.followers = nil
			rc.pullHintInflight = nil
			r.scheduleReplicationFromState(rc, time.Now())
		}
	}
	event.Future.Complete(Result{Err: decision.Err})
}

// resetFollowerStopLifecycle clears stopped-ACK state that is scoped to the current metadata fence.
func resetFollowerStopLifecycle(rc *runtimeChannel) {
	if rc == nil {
		return
	}
	for _, follower := range rc.followers {
		if follower == nil {
			continue
		}
		follower.Stopped = false
		follower.StopAckVersion = 0
		follower.StopOffered = false
		follower.StopOfferedVersion = 0
	}
}

func (r *Reactor) handleCheckState(event Event) {
	_, err := r.lookup(event.Key)
	event.Future.Complete(Result{Err: err})
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
		state:            state,
		store:            cs,
		waiters:          make(map[ch.OpID]*Future),
		pullWaiters:      make(map[ch.OpID]*pullWaiter),
		appendTimings:    make(map[ch.OpID]appendTiming),
		recentRecords:    newRecentRecordCache(r.cfg.LeaderRecentRecordCacheSize, r.cfg.LeaderRecentRecordCacheBytes),
		lifecycle:        channelLifecycle{LoadedAt: time.Now(), ActivityVersion: initial.LEO},
		runtimeLifecycle: runtimeLifecycle{LeaderPhase: LeaderLifecycleServing, FollowerPhase: FollowerLifecycleReplicating},
		appendQ: newAppendQueue(appendQueueConfig{
			MaxRecords:      r.cfg.AppendBatchMaxRecords,
			MaxBytes:        r.cfg.AppendBatchMaxBytes,
			MaxWait:         r.cfg.AppendBatchMaxWait,
			MaxPending:      r.cfg.AppendQueueMaxRequests,
			MaxPendingBytes: r.cfg.AppendQueueMaxBytes,
		}),
	}
	r.channels[key] = rc
	r.observeChannelRuntimeLoaded(key)
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
	if event.Append.ExpectedChannelEpoch != 0 && event.Append.ExpectedChannelEpoch != rc.state.Epoch {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if event.Append.ExpectedLeaderEpoch != 0 && event.Append.ExpectedLeaderEpoch != rc.state.LeaderEpoch {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	admittedAt := time.Now()
	records := make([]ch.Record, len(event.Append.Messages))
	for i, msg := range event.Append.Messages {
		records[i] = ch.Record{ID: msg.MessageID, Payload: append([]byte(nil), msg.Payload...), SizeBytes: len(msg.Payload)}
	}
	mode := event.Append.CommitMode
	if mode == 0 {
		mode = ch.CommitModeQuorum
	}
	req := appendRequest{opID: event.OpID, req: event.Append, future: event.Future, ctx: event.Context, enqueuedAt: admittedAt, records: records, commitMode: mode}
	if err := rc.appendQ.push(req); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if err := rc.addWaiter(event.OpID, event.Future); err != nil {
		rc.appendQ.remove(event.OpID)
		event.Future.Complete(Result{Err: err})
		return
	}
	r.cancelLeaderEvictionForAppend(rc, admittedAt)
	rc.appendTimings[event.OpID] = appendTiming{mode: mode, enqueuedAt: req.enqueuedAt}
	r.registerAppendCancelContext(rc, event.OpID, event.Context)
	r.tryFlushAppend(rc, admittedAt)
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
	oldLEO := rc.state.LEO
	decision := rc.state.ApplyAppendStored(stored)
	now := time.Now()
	if stored.Err == nil && rc.state.Role == ch.RoleLeader && rc.state.LEO > oldLEO {
		rc.recentRecords.append(rc.appendInflightRecords(result.Fence.OpID))
		r.markAppendActivity(rc, now)
		rc.lifecycle.ActivityVersion = rc.state.LEO
		r.syncFollowerMatches(rc)
		for _, follower := range rc.followers {
			if follower != nil && follower.Match < rc.state.LEO {
				if follower.Stopped || follower.StopOffered {
					follower.LastPullAt = time.Time{}
				}
				follower.Stopped = false
				follower.StopAckVersion = 0
				follower.StopOffered = false
				follower.StopOfferedVersion = 0
			}
		}
		r.sendPullHintsForAppend(rc, now)
	}
	r.completeReplies(rc, decision.Replies, nil)
	if current {
		if stored.Err != nil {
			for _, req := range rc.appendInflight.requests {
				if future := rc.waiters[req.opID]; future != nil {
					delete(rc.waiters, req.opID)
					r.unregisterAppendCancelContext(rc, req.opID)
					r.completeAppendFuture(rc, req.opID, future, Result{Err: stored.Err})
				}
			}
		}
		rc.appendInflight = nil
		rc.appendQ.storeBlocked = false
		r.tryFlushAppend(rc, now)
	}
}

func (rc *runtimeChannel) appendInflightRecords(batchOpID ch.OpID) []ch.Record {
	if rc == nil || rc.appendInflight == nil || rc.appendInflight.batchOpID != batchOpID {
		return nil
	}
	return rc.appendInflight.records
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
	r.cancelPullWaiter(rc, event.CancelOp, cancelErr)
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
	ctx := event.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleLeader {
		event.Future.Complete(Result{Err: ch.ErrNotLeader})
		return
	}
	if event.Pull.ChannelKey != rc.state.Key || event.Pull.ChannelID != rc.state.ID || event.Pull.Epoch != rc.state.Epoch || event.Pull.LeaderEpoch != rc.state.LeaderEpoch || !rc.state.IsReplica(event.Pull.Follower) {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if event.OpID == 0 || event.Pull.NextOffset == 0 || event.Pull.MaxBytes <= 0 {
		event.Future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	if rc.pullWaiters == nil {
		rc.pullWaiters = make(map[ch.OpID]*pullWaiter)
	}
	if _, ok := rc.pullWaiters[event.OpID]; ok {
		event.Future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	now := time.Now()
	r.syncLeaderFollowers(rc)
	if follower := rc.followers[event.Pull.Follower]; follower != nil {
		follower.LastPullAt = now
		follower.Parked = false
		follower.Stopped = false
		follower.NextExpectedPullAt = time.Time{}
		retireFollowerPullHints(rc, event.Pull.Follower)
		match := event.Pull.NextOffset - 1
		if event.Pull.NextOffset <= rc.state.LEO+1 && match > follower.Match {
			follower.Match = match
		}
	}
	waiter := &pullWaiter{future: event.Future, ctx: ctx, follower: event.Pull.Follower, nextOffset: event.Pull.NextOffset, maxBytes: event.Pull.MaxBytes}
	if r.tryCompletePullFromLeaderCache(rc, event, waiter, now) {
		return
	}
	maxOffset, mergeCacheSuffix := leaderPullReadRange(rc, event.Pull.NextOffset)
	waiter.mergeCacheSuffix = mergeCacheSuffix
	rc.pullWaiters[event.OpID] = waiter
	r.registerPullCancelContext(rc, ctx)
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: event.OpID}
	err = r.submitStoreReadLog(ctx, event.Pull.ChannelID, fence, event.Pull.NextOffset, maxOffset, event.Pull.MaxBytes)
	if err != nil {
		delete(rc.pullWaiters, event.OpID)
		r.unregisterPullCancelContext(rc)
		event.Future.Complete(Result{Err: err})
	}
}

func (r *Reactor) tryCompletePullFromLeaderCache(rc *runtimeChannel, event Event, waiter *pullWaiter, now time.Time) bool {
	if rc == nil || rc.state == nil || !rc.recentRecords.enabled() {
		return false
	}
	nextOffset := event.Pull.NextOffset
	if nextOffset == rc.state.LEO+1 {
		r.completeLeaderPull(rc, waiter, event.Future, nil, now)
		return true
	}
	if nextOffset > rc.state.LEO+1 {
		return false
	}
	records, ok := rc.recentRecords.slice(nextOffset, rc.state.LEO, waiter.maxBytes)
	if !ok {
		return false
	}
	r.completeLeaderPull(rc, waiter, event.Future, records, now)
	return true
}

func leaderPullReadRange(rc *runtimeChannel, nextOffset uint64) (maxOffset uint64, mergeCacheSuffix bool) {
	if rc == nil || rc.state == nil {
		return 0, false
	}
	if rc.recentRecords.enabled() && rc.recentRecords.hasSuffixAfter(nextOffset) {
		base := rc.recentRecords.base()
		if base > 0 {
			return base - 1, true
		}
	}
	return rc.state.LEO, false
}

func (r *Reactor) handleAck(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleLeader || event.Ack.ChannelKey != rc.state.Key || event.Ack.Epoch != rc.state.Epoch || event.Ack.LeaderEpoch != rc.state.LeaderEpoch || !rc.state.IsReplica(event.Ack.Follower) {
		if event.Ack.Stopped {
			event.Future.Complete(Result{Err: ch.ErrStaleMeta})
			return
		}
		event.Future.Complete(Result{})
		return
	}
	r.syncLeaderFollowers(rc)
	if event.Ack.Stopped {
		if event.Ack.ActivityVersion != rc.lifecycle.ActivityVersion || event.Ack.MatchOffset != rc.state.LEO {
			event.Future.Complete(Result{Err: ch.ErrStaleMeta})
			return
		}
		if follower := rc.followers[event.Ack.Follower]; follower != nil {
			if follower.StopOffered && follower.StopOfferedVersion != event.Ack.ActivityVersion {
				event.Future.Complete(Result{Err: ch.ErrStaleMeta})
				return
			}
			wasStopped := follower.Stopped && follower.StopAckVersion == event.Ack.ActivityVersion
			follower.Stopped = true
			follower.StopAckVersion = event.Ack.ActivityVersion
			follower.Parked = false
			retireFollowerPullHints(rc, event.Ack.Follower)
			if event.Ack.MatchOffset > follower.Match {
				follower.Match = event.Ack.MatchOffset
			}
			if !wasStopped {
				r.observeFollowerStopped(rc.state.Key, event.Ack.Follower, event.Ack.ActivityVersion)
			}
		}
		decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: event.Ack.Follower, MatchOffset: event.Ack.MatchOffset})
		r.completeReplies(rc, decision.Replies, nil)
		now := time.Now()
		r.tryEvictLeader(rc, now)
		r.scheduleLifecycleFromState(rc, now)
		event.Future.Complete(Result{})
		return
	}
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: event.Ack.Follower, MatchOffset: event.Ack.MatchOffset})
	if follower := rc.followers[event.Ack.Follower]; follower != nil && event.Ack.MatchOffset > follower.Match {
		follower.Match = event.Ack.MatchOffset
	}
	if follower := rc.followers[event.Ack.Follower]; follower != nil && follower.Match >= rc.state.LEO {
		retireFollowerPullHints(rc, event.Ack.Follower)
	}
	r.completeReplies(rc, decision.Replies, nil)
	r.scheduleLifecycleFromState(rc, time.Now())
	event.Future.Complete(Result{})
}
