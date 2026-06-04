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

const (
	defaultReactorDrain = 128

	// defaultFollowerRecoveryProbeInterval keeps lost PullHint recovery within the gateway send timeout budget.
	defaultFollowerRecoveryProbeInterval = 2 * time.Second
	// defaultFollowerRecoveryProbeJitter spreads recovery probes without exceeding the send timeout budget.
	defaultFollowerRecoveryProbeJitter = time.Second
)

// ReactorConfig wires one reactor.
type ReactorConfig struct {
	ID        int
	LocalNode ch.NodeID
	Store     store.Factory
	Pools     *worker.Pools
	// MailboxSize bounds each priority queue in this reactor.
	MailboxSize int
	// MaxChannels bounds loaded runtimes owned by this reactor when MaxChannelsEnabled is true.
	MaxChannels int
	// MaxChannelsEnabled distinguishes an explicit zero-capacity partition from unlimited operation.
	MaxChannelsEnabled bool
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
	// ReplicationIdlePollInterval delays the next follower poll when a leader has no new records; defaults to 250ms.
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
	// FollowerRecoveryProbeInterval is the base delay for parked follower recovery probes. Zero uses the runtime default.
	FollowerRecoveryProbeInterval time.Duration
	// FollowerRecoveryProbeJitter spreads parked follower recovery probes across this bounded window.
	FollowerRecoveryProbeJitter time.Duration
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
	// submitGate serializes event admission with the final shutdown drain.
	submitGate sync.RWMutex
	// submitMu orders final leader eviction against concurrent Append submissions.
	submitMu sync.Mutex
	// appendSubmitSeqs increments per channel before every Append reservation or mailbox submission.
	appendSubmitSeqs map[ch.ChannelKey]uint64
	// appendReservations counts appends between loaded-state verification and mailbox submission.
	appendReservations map[ch.ChannelKey]int
	// activationRejectedTotal counts local runtime activation rejections for benchmark snapshots.
	activationRejectedTotal uint64
	// pendingMetaCount tracks follower bootstrap shells without scanning all channel slots.
	pendingMetaCount int
}

type runtimeChannel struct {
	state   *machine.ChannelState
	store   store.ChannelStore
	pending *pendingMetaState
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
	// lifecycle owns runtime stop, checkpoint, hint, and eviction state.
	lifecycle channelRuntimeLifecycle
	// pullWaiters maps leader-side async pull op ids to request futures.
	pullWaiters map[ch.OpID]*pullWaiter
	// due versions fence stale scheduler entries after channel state changes.
	appendFlushDueVersion uint64
	replicationDueVersion uint64
	lifecycleDueVersion   uint64
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
	// needMeta asks the leader completion path to include a cloned runtime metadata snapshot.
	needMeta bool
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
	r.submitGate.RLock()
	defer r.submitGate.RUnlock()
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
	r.submitGate.RLock()
	defer r.submitGate.RUnlock()
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
		r.submitGate.Lock()
		r.failPendingWaiters(ch.ErrClosed)
		r.failQueuedEvents(ch.ErrClosed)
		r.submitGate.Unlock()
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
		r.processDue(time.Now())
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
	case EventRuntimeSnapshot:
		r.handleRuntimeSnapshot(event)
	case EventRuntimeProbe:
		r.handleRuntimeProbe(event)
	case EventRuntimeEvict:
		r.handleRuntimeEvict(event)
	case EventAppend:
		r.handleAppend(event)
	case EventCancelWaiter:
		r.handleCancelWaiter(event)
	case EventPull:
		r.handleLeaderPull(event)
	case EventAck:
		r.handleLeaderAck(event)
	case EventNotify:
		r.handleLegacyFollowerNotify(event)
	case EventPullHint:
		r.handleFollowerPullHint(event)
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

func (r *Reactor) handleTick(event Event) {
	now := event.TickNow
	if now.IsZero() {
		now = time.Now()
	}
	r.processDue(now)
	if event.Key != "" {
		if rc := r.channels[event.Key]; rc != nil {
			r.tryFlushAppend(rc, now)
			r.tickFollowerReplication(rc, now)
			r.tickLifecycleController(rc, now)
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
	for key, rc := range r.channels {
		if rc != nil && rc.pending != nil && rc.state == nil {
			r.releasePendingMeta(key, rc, err)
			continue
		}
		r.failWaiters(rc, err)
		r.clearAppendCancelContexts(rc)
		r.clearPullCancelChannel(rc)
	}
}

func (r *Reactor) failQueuedEvents(err error) {
	if r == nil || r.mailbox == nil {
		return
	}
	for {
		events := r.mailbox.Drain(defaultReactorDrain)
		if len(events) == 0 {
			return
		}
		for _, event := range events {
			if event.Future != nil {
				event.Future.Complete(Result{Err: err})
			}
		}
	}
}

func (r *Reactor) handleApplyMeta(event Event) {
	key := event.Meta.Key
	if key == "" {
		key = ch.ChannelKeyForID(event.Meta.ID)
	}
	if existing := r.channels[key]; existing != nil && existing.pending != nil && existing.state == nil {
		r.handleApplyMetaToPending(event, existing)
		return
	}
	existing := r.channels[key]
	rc, err := r.ensureChannel(event.Meta)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	wasParked := rc.replication.parked
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
			rc.lifecycle.cancelFollowerStop()
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
			if rc.state.LEO > rc.lifecycle.version {
				rc.lifecycle.version = rc.state.LEO
			}
			r.syncLeaderFollowers(rc)
			if fencePendingState {
				resetFollowerStopLifecycle(rc)
			}
			r.scheduleLifecycleFromState(rc, time.Now())
		} else {
			rc.recentRecords.reset()
			rc.lifecycle.followers = nil
			rc.lifecycle.pullHintInflight = nil
			r.scheduleReplicationFromState(rc, time.Now())
		}
	}
	if decision.Err == nil {
		r.observeFollowerParkedCountIfChanged(wasParked, rc)
		r.observeRuntimeCounts()
	}
	event.Future.Complete(Result{Err: decision.Err})
}

// resetFollowerStopLifecycle clears stopped-ACK state that is scoped to the current metadata fence.
func resetFollowerStopLifecycle(rc *runtimeChannel) {
	if rc == nil {
		return
	}
	for _, follower := range rc.lifecycle.followers {
		if follower == nil {
			continue
		}
		follower.resetStop()
	}
}

func (r *Reactor) handleCheckState(event Event) {
	_, err := r.lookup(event.Key)
	event.Future.Complete(Result{Err: err})
}

func (r *Reactor) handleApplyMetaToPending(event Event, rc *runtimeChannel) {
	if event.Future == nil {
		event.Future = NewFuture()
	}
	meta := event.Meta
	pending := rc.pending
	if pending == nil {
		event.Future.Complete(Result{Err: ch.ErrChannelNotFound})
		return
	}
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	if meta.Key != pending.key {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if meta.ID != pending.id {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	cmp := comparePendingMetaFence(pending, meta)
	if cmp < 0 {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if cmp == 0 && meta.Leader != pending.leader {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if err := validatePendingMetaShape(meta); err != nil {
		r.releasePendingMeta(pending.key, rc, err)
		event.Future.Complete(Result{Err: err})
		return
	}
	if !metaHasReplica(meta, r.cfg.LocalNode) {
		r.releasePendingMeta(pending.key, rc, ch.ErrNotReplica)
		event.Future.Complete(Result{Err: ch.ErrNotReplica})
		return
	}
	if err := r.convertPendingMeta(rc, meta); err != nil {
		r.releasePendingMeta(pending.key, rc, err)
		event.Future.Complete(Result{Err: err})
		return
	}
	event.Future.Complete(Result{})
}

func (r *Reactor) ensureChannel(meta ch.Meta) (*runtimeChannel, error) {
	key := meta.Key
	if key == "" {
		key = ch.ChannelKeyForID(meta.ID)
	}
	if rc := r.channels[key]; rc != nil {
		return rc, nil
	}
	if r.cfg.MaxChannelsEnabled && len(r.channels) >= r.cfg.MaxChannels {
		r.observeActivationRejected("max_channels")
		return nil, ch.ErrTooManyChannels
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
		state:         state,
		store:         cs,
		recentRecords: newRecentRecordCache(r.cfg.LeaderRecentRecordCacheSize, r.cfg.LeaderRecentRecordCacheBytes),
		lifecycle:     newChannelRuntimeLifecycle(time.Now(), initial.LEO),
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

func (r *Reactor) ensurePendingMeta(req transport.PullHintRequest) (*runtimeChannel, error) {
	if err := validatePendingPullHint(req, r.cfg.LocalNode); err != nil {
		return nil, err
	}
	if rc := r.channels[req.ChannelKey]; rc != nil {
		if rc.pending == nil || rc.state != nil {
			return nil, ch.ErrStaleMeta
		}
		switch rc.pending.compareFence(req) {
		case -1:
			return nil, ch.ErrStaleMeta
		case 0:
			if req.LeaderLEO > rc.pending.leaderLEO {
				rc.pending.leaderLEO = req.LeaderLEO
			}
			if req.ActivityVersion > rc.pending.activityVersion {
				rc.pending.activityVersion = req.ActivityVersion
			}
			return rc, nil
		default:
			r.releasePendingMeta(req.ChannelKey, rc, ch.ErrStaleMeta)
		}
	}
	if r.cfg.MaxChannelsEnabled && len(r.channels) >= r.cfg.MaxChannels {
		r.observeActivationRejected("max_channels")
		return nil, ch.ErrTooManyChannels
	}
	cs, err := r.cfg.Store.ChannelStore(req.ChannelKey, req.ChannelID)
	if err != nil {
		return nil, err
	}
	initial, err := cs.Load(context.Background())
	if err != nil {
		_ = cs.Close()
		return nil, err
	}
	rc := &runtimeChannel{
		store: cs,
		pending: &pendingMetaState{
			key:             req.ChannelKey,
			id:              req.ChannelID,
			generation:      uint64(r.nextOpID()),
			epoch:           req.Epoch,
			leaderEpoch:     req.LeaderEpoch,
			leader:          req.Leader,
			leaderLEO:       req.LeaderLEO,
			activityVersion: req.ActivityVersion,
			deadline:        r.pendingMetaDeadline(time.Now()),
			initial: storeInitialState{
				LEO:          initial.LEO,
				HW:           initial.HW,
				CheckpointHW: initial.CheckpointHW,
			},
		},
	}
	r.channels[req.ChannelKey] = rc
	r.pendingMetaCount++
	r.observePendingMeta("created", nil)
	r.observePendingMetaCount()
	r.schedulePendingMetaDeadline(rc)
	return rc, nil
}

func validatePendingPullHint(req transport.PullHintRequest, local ch.NodeID) error {
	if req.ChannelKey == "" || req.ChannelID == (ch.ChannelID{}) || req.Epoch == 0 || req.LeaderEpoch == 0 || req.Leader == 0 || req.Leader == local {
		return ch.ErrInvalidConfig
	}
	return nil
}

func validatePendingMetaShape(meta ch.Meta) error {
	if meta.Key == "" || meta.ID == (ch.ChannelID{}) || meta.Epoch == 0 || meta.LeaderEpoch == 0 || meta.Leader == 0 {
		return ch.ErrInvalidConfig
	}
	if meta.Status != ch.StatusActive {
		return ch.ErrNotReady
	}
	if meta.MinISR <= 0 || meta.MinISR > len(meta.ISR) {
		return ch.ErrInvalidConfig
	}
	return nil
}

func comparePendingMetaFence(pending *pendingMetaState, meta ch.Meta) int {
	if pending == nil {
		return 1
	}
	if meta.Epoch < pending.epoch || (meta.Epoch == pending.epoch && meta.LeaderEpoch < pending.leaderEpoch) {
		return -1
	}
	if meta.Epoch == pending.epoch && meta.LeaderEpoch == pending.leaderEpoch {
		if meta.Key != pending.key {
			return -1
		}
		return 0
	}
	return 1
}

func metaHasReplica(meta ch.Meta, node ch.NodeID) bool {
	for _, replica := range meta.Replicas {
		if replica == node {
			return true
		}
	}
	return false
}

func (r *Reactor) convertPendingMeta(rc *runtimeChannel, meta ch.Meta) error {
	if r == nil || rc == nil || rc.pending == nil {
		return ch.ErrChannelNotFound
	}
	pending := rc.pending
	state := machine.NewChannelState(pending.key, r.cfg.LocalNode, pending.generation)
	state.ID = meta.ID
	state.LEO = pending.initial.LEO
	state.HW = pending.initial.HW
	state.CheckpointHW = pending.initial.CheckpointHW
	decision := state.ApplyMeta(meta)
	if decision.Err != nil {
		return decision.Err
	}
	rc.state = state
	rc.pending = nil
	if r.pendingMetaCount > 0 {
		r.pendingMetaCount--
	}
	rc.recentRecords = newRecentRecordCache(r.cfg.LeaderRecentRecordCacheSize, r.cfg.LeaderRecentRecordCacheBytes)
	rc.lifecycle = newChannelRuntimeLifecycle(time.Now(), state.LEO)
	rc.appendQ = newAppendQueue(appendQueueConfig{
		MaxRecords:      r.cfg.AppendBatchMaxRecords,
		MaxBytes:        r.cfg.AppendBatchMaxBytes,
		MaxWait:         r.cfg.AppendBatchMaxWait,
		MaxPending:      r.cfg.AppendQueueMaxRequests,
		MaxPendingBytes: r.cfg.AppendQueueMaxBytes,
	})
	rc.replication.reset()
	now := time.Now()
	if state.Role == ch.RoleFollower && state.Status == ch.StatusActive {
		rc.replication.lastActivityVersion = pending.activityVersion
		if pending.leaderLEO > state.LEO {
			rc.replication.hintedLeaderLEO = pending.leaderLEO
			rc.replication.hintedAt = now
		}
		rc.replication.markDirty(now)
		r.scheduleReplicationFromState(rc, now)
	} else if state.Role == ch.RoleLeader && state.Status == ch.StatusActive {
		if state.LEO > rc.lifecycle.version {
			rc.lifecycle.version = state.LEO
		}
		r.syncLeaderFollowers(rc)
		r.scheduleLifecycleFromState(rc, now)
	}
	r.observePendingMeta("converted", nil)
	r.observePendingMetaCount()
	r.observeChannelRuntimeLoaded(state.Key)
	r.observeRuntimeCounts()
	return nil
}

func (r *Reactor) releasePendingMeta(key ch.ChannelKey, rc *runtimeChannel, err error) {
	if r == nil || rc == nil || rc.pending == nil || r.channels[key] != rc {
		return
	}
	_ = rc.store.Close()
	delete(r.channels, key)
	if r.pendingMetaCount > 0 {
		r.pendingMetaCount--
	}
	r.observePendingMeta("released", err)
	r.observePendingMetaCount()
	r.observeRuntimeCounts()
}

func (r *Reactor) releaseExpiredPendingMeta(key ch.ChannelKey, rc *runtimeChannel, now time.Time) {
	if r == nil || rc == nil || rc.pending == nil || rc.state != nil || r.channels[key] != rc {
		return
	}
	if rc.pending.deadline.IsZero() || now.Before(rc.pending.deadline) {
		return
	}
	r.releasePendingMeta(key, rc, context.DeadlineExceeded)
}

func (r *Reactor) pendingMetaDeadline(now time.Time) time.Time {
	timeout := r.cfg.PullHintRetryInterval
	if timeout <= 0 {
		timeout = time.Second
	}
	return now.Add(timeout)
}

func (r *Reactor) forceReleaseRuntimeForPending(key ch.ChannelKey, rc *runtimeChannel) {
	if r == nil || rc == nil || r.channels[key] != rc {
		return
	}
	var role ch.Role
	wasParkedFollower := false
	if rc.state != nil {
		role = rc.state.Role
		wasParkedFollower = role == ch.RoleFollower && rc.replication.parked
	}
	r.failPendingAppendWaiters(rc, ch.ErrStaleMeta)
	rc.failPendingPullWaiters(ch.ErrStaleMeta)
	r.clearPullCancelChannel(rc)
	r.clearAppendCancelContexts(rc)
	if rc.store != nil {
		_ = rc.store.Close()
	}
	delete(r.channels, key)
	r.clearAppendSubmitState(key)
	if role != 0 {
		r.observeChannelRuntimeEvicted(key, role)
	}
	if wasParkedFollower {
		r.observeFollowerParkedCount(r.countParkedFollowers())
	}
	r.observeRuntimeCounts()
}

func (r *Reactor) observeRuntimeCounts() {
	if r == nil {
		return
	}
	leaders := 0
	followers := 0
	for _, rc := range r.channels {
		if rc == nil || rc.state == nil {
			continue
		}
		switch rc.state.Role {
		case ch.RoleLeader:
			leaders++
		case ch.RoleFollower:
			followers++
		}
	}
	r.observeRuntimeCount(ch.RoleLeader, leaders)
	r.observeRuntimeCount(ch.RoleFollower, followers)
}

func (r *Reactor) lookup(key ch.ChannelKey) (*runtimeChannel, error) {
	if rc := r.channels[key]; rc != nil && rc.state != nil && rc.pending == nil {
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
	if rc.appendTimings == nil {
		rc.appendTimings = make(map[ch.OpID]appendTiming)
	}
	rc.appendTimings[event.OpID] = appendTiming{mode: mode, enqueuedAt: req.enqueuedAt}
	r.registerAppendCancelContext(rc, event.OpID, event.Context)
	r.tryFlushAppend(rc, admittedAt)
}

func (r *Reactor) handleStoreAppendResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	batch := rc.appendInflight
	current := batch != nil && batch.batchOpID == result.Fence.OpID
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
	oldHW := rc.state.HW
	now := time.Now()
	if current {
		r.observeAppendStoreCompleted(rc, *batch, now, stored.BaseOffset, stored.Err)
	}
	decision := rc.state.ApplyAppendStored(stored)
	if stored.Err == nil {
		r.markAppendHWAdvanced(rc, oldHW, rc.state.HW, now)
	}
	if stored.Err == nil && rc.state.Role == ch.RoleLeader && rc.state.LEO > oldLEO {
		rc.recentRecords.append(rc.appendInflightRecords(result.Fence.OpID))
		r.markAppendActivity(rc, now)
		rc.lifecycle.version = rc.state.LEO
		r.syncFollowerMatches(rc)
		for _, follower := range rc.lifecycle.followers {
			if follower != nil && follower.match < rc.state.LEO {
				if follower.stoppedVersion != 0 || follower.stopOfferedVersion != 0 || follower.stopOfferedZero {
					follower.lastPullAt = time.Time{}
				}
				follower.resetStop()
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
