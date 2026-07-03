package reactor

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

const (
	defaultReactorDrain              = 128
	defaultAppendCancelSweepInterval = 5 * time.Millisecond

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
	// AppendBatchAdaptiveFlush enables a shorter cold-channel flush delay before the normal batch window.
	AppendBatchAdaptiveFlush bool
	// AppendBatchColdMaxWait is the cold-channel flush delay used when AppendBatchAdaptiveFlush is enabled.
	AppendBatchColdMaxWait time.Duration
	// AppendQueueMaxRequests bounds accepted append requests waiting per channel.
	AppendQueueMaxRequests int
	// AppendQueueMaxBytes bounds accepted append payload bytes waiting per channel.
	AppendQueueMaxBytes int
	// AppendStoreRetryBackoff delays retry after the store append worker pool rejects a batch.
	AppendStoreRetryBackoff time.Duration
	// ReplicationIdlePollInterval delays the next follower poll when a leader has no new records; defaults to 100ms.
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
	// AppendAdmissionGuard can reject local leader appends before reactor admission.
	AppendAdmissionGuard ch.AppendAdmissionGuard
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
	// SlowEventThreshold reports reactor event handling that takes at least this long. Zero disables slow event reports.
	SlowEventThreshold time.Duration
	// SlowDueThreshold reports one due maintenance item that takes at least this long. Zero disables slow due reports.
	SlowDueThreshold time.Duration
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
	// appendCancelSweepNextAt rate-limits global append cancellation scans during hot mailboxes.
	appendCancelSweepNextAt time.Time
	// pullCancelChannels indexes leader pull waiters with cancellable caller contexts.
	pullCancelChannels map[ch.ChannelKey]*runtimeChannel
	// lookupCancelChannels indexes message lookup waiters with cancellable caller contexts.
	lookupCancelChannels map[ch.ChannelKey]*runtimeChannel
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
	// appendQueuePressure keeps aggregate append queue pressure updated by per-channel deltas.
	appendQueuePressure appendQueuePressureState
	// asyncEffects is set after start so direct handler unit tests keep their synchronous fixtures.
	asyncEffects atomic.Bool
	// appendAdmissionGuard can reject local leader appends before they enter the append queue.
	appendAdmissionGuard ch.AppendAdmissionGuard
}

type runtimeChannel struct {
	state   *machine.ChannelState
	store   store.ChannelStore
	pending *pendingMetaState
	loading *storeLoadState
	waiters map[ch.OpID]*Future
	// appendQ holds accepted append requests before they are flushed as durable batches.
	appendQ appendQueue
	// appendQueuePressure is the last pressure snapshot included in the reactor aggregate.
	appendQueuePressure appendQueuePressureState
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
	// appendCancelNudgeNextAt rate-limits cancellation-sweep follower wakeups.
	appendCancelNudgeNextAt time.Time
	// appendTimings tracks accepted append requests until their futures complete.
	appendTimings map[ch.OpID]appendTiming
	// replication owns follower pull, apply, and ack scheduling state.
	replication replicationState
	// lifecycle owns runtime stop, checkpoint, hint, and eviction state.
	lifecycle channelRuntimeLifecycle
	// pullWaiters maps leader-side async pull op ids to request futures.
	pullWaiters map[ch.OpID]*pullWaiter
	// lookupWaiters maps async committed-message lookup op ids to request futures.
	lookupWaiters map[ch.OpID]*lookupWaiter
	// retentionWaiters maps async retention apply op ids to request futures.
	retentionWaiters map[ch.OpID]*retentionWaiter
	// retentionCheckpointOp tracks the retention-owned checkpoint that unblocks future physical trim attempts.
	retentionCheckpointOp ch.OpID
	// due versions fence stale scheduler entries after channel state changes.
	appendFlushDueVersion uint64
	replicationDueVersion uint64
	lifecycleDueVersion   uint64
}

type storeLoadKind uint8

const (
	storeLoadApplyMeta storeLoadKind = iota + 1
	storeLoadPendingMeta
)

type storeLoadState struct {
	// kind records which activation path should consume the loaded store result.
	kind storeLoadKind
	// key is the stable channel runtime key reserved while the load is in flight.
	key ch.ChannelKey
	// id is the channel store identity used by the worker load.
	id ch.ChannelID
	// generation fences stale worker load results for this runtime shell.
	generation uint64
	// opID fences stale worker load results for this runtime shell.
	opID ch.OpID
	// meta is the latest metadata accepted while the store load is in flight.
	meta ch.Meta
	// pullHint is the latest pending-meta bootstrap hint accepted while the store load is in flight.
	pullHint transport.PullHintRequest
	// futures are ApplyMeta callers waiting for this load to finish.
	futures []*Future
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
	r := &Reactor{cfg: cfg, mailbox: NewMailbox(MailboxConfig{HighSize: cfg.MailboxSize, NormalSize: cfg.MailboxSize, LowSize: cfg.MailboxSize}), drainBuf: make([]Event, 0, defaultReactorDrain), channels: make(map[ch.ChannelKey]*runtimeChannel), stop: make(chan struct{}), done: make(chan struct{}), appendAdmissionGuard: cfg.AppendAdmissionGuard}
	r.observeAllMailboxCapacities()
	return r
}

func (r *Reactor) start() {
	r.asyncEffects.Store(true)
	go r.loop()
}

// Submit enqueues an event.
func (r *Reactor) Submit(priority Priority, event Event) error {
	r.submitGate.RLock()
	defer r.submitGate.RUnlock()
	select {
	case <-r.stop:
		r.observeMailboxAdmission(priority, mailboxSubmitClosed)
		r.observeMailboxDepth(priority)
		return ch.ErrClosed
	default:
	}
	if event.Kind == EventAppend {
		r.submitMu.Lock()
		r.bumpAppendSubmitSeqLocked(event.Key)
		err := r.submitMailboxWithResult(priority, event)
		r.submitMu.Unlock()
		return err
	}
	return r.submitMailboxWithResult(priority, event)
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
		r.observeMailboxAdmission(PriorityHigh, mailboxSubmitClosed)
		r.observeMailboxDepth(PriorityHigh)
		return ch.ErrClosed
	default:
	}
	return r.submitMailboxBlockingWithResult(PriorityHigh, event)
}

func (r *Reactor) submitMailboxWithResult(priority Priority, event Event) error {
	result, err := r.mailbox.submitWithResult(priority, event)
	r.observeMailboxAdmission(priority, result)
	r.observeMailboxDepth(priority)
	return err
}

func (r *Reactor) submitMailboxBlockingWithResult(priority Priority, event Event) error {
	result, err := r.mailbox.submitBlockingWithResult(priority, event, r.stop)
	r.observeMailboxAdmission(priority, result)
	r.observeMailboxDepth(priority)
	return err
}

// Close stops the reactor and fails future work by closing the loop.
func (r *Reactor) Close() error {
	r.once.Do(func() { close(r.stop) })
	<-r.done
	return nil
}
