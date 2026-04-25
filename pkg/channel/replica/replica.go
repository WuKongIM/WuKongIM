package replica

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type appendGroupCommitConfig struct {
	maxWait    time.Duration
	maxRecords int
	maxBytes   int
}

type appendCompletion struct {
	result channel.CommitResult
	err    error
}

type appendRequest struct {
	ctx        context.Context
	batch      []channel.Record
	byteCount  int
	waiter     *appendWaiter
	enqueuedAt time.Time
}

type appendWaiter struct {
	target        uint64
	rangeStart    uint64
	rangeEnd      uint64
	result        channel.CommitResult
	ch            chan appendCompletion
	enqueuedAt    time.Time
	durableDoneAt time.Time
}

type replica struct {
	mu sync.RWMutex

	advanceMu sync.Mutex
	appendMu  sync.Mutex

	localNode     channel.NodeID
	onStateChange func()

	log                 LogStore
	checkpoints         CheckpointStore
	applyFetch          ApplyFetchStore
	history             EpochHistoryStore
	snapshots           SnapshotApplier
	probeSource         ReconcileProbeSource
	now                 func() time.Time
	onLeaderLocalAppend func()
	onLeaderHWAdvance   func()
	logger              wklog.Logger

	meta         channel.Meta
	state        channel.ReplicaState
	statePointer atomic.Pointer[channel.ReplicaState]
	progress     map[channel.NodeID]uint64
	waiters      []*appendWaiter
	epochHistory []channel.EpochPoint
	recovered    bool
	closed       bool

	appendGroupCommit appendGroupCommitConfig
	appendPending     []*appendRequest
	appendSignal      chan struct{}
	advanceSignal     chan struct{}
	checkpointSignal  chan struct{}
	stopCh            chan struct{}
	collectorDone     chan struct{}
	advanceDone       chan struct{}
	checkpointDone    chan struct{}
	closeOnce         sync.Once

	pendingCheckpoint  channel.Checkpoint
	checkpointQueued   bool
	checkpointInFlight bool
	reconcilePending   map[channel.NodeID]struct{}
}

func NewReplica(cfg ReplicaConfig) (Replica, error) {
	if cfg.LocalNode == 0 {
		return nil, channel.ErrInvalidConfig
	}
	if cfg.LogStore == nil {
		return nil, channel.ErrInvalidConfig
	}
	if cfg.CheckpointStore == nil {
		return nil, channel.ErrInvalidConfig
	}
	if cfg.EpochHistoryStore == nil {
		return nil, channel.ErrInvalidConfig
	}
	if cfg.SnapshotApplier == nil {
		return nil, channel.ErrInvalidConfig
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}

	r := &replica{
		localNode:     cfg.LocalNode,
		onStateChange: cfg.OnStateChange,
		log:           cfg.LogStore,
		checkpoints:   cfg.CheckpointStore,
		applyFetch:    cfg.ApplyFetchStore,
		history:       cfg.EpochHistoryStore,
		snapshots:     cfg.SnapshotApplier,
		probeSource:   cfg.ReconcileProbeSource,
		now:           cfg.Now,
		logger:        cfg.Logger,
		appendGroupCommit: appendGroupCommitConfig{
			maxWait:    effectiveAppendGroupCommitMaxWait(cfg.AppendGroupCommitMaxWait),
			maxRecords: effectiveAppendGroupCommitMaxRecords(cfg.AppendGroupCommitMaxRecords),
			maxBytes:   effectiveAppendGroupCommitMaxBytes(cfg.AppendGroupCommitMaxBytes),
		},
		appendSignal:     make(chan struct{}, 1),
		advanceSignal:    make(chan struct{}, 1),
		checkpointSignal: make(chan struct{}, 1),
		stopCh:           make(chan struct{}),
		collectorDone:    make(chan struct{}),
		advanceDone:      make(chan struct{}),
		checkpointDone:   make(chan struct{}),
		state: channel.ReplicaState{
			Role:        channel.ReplicaRoleFollower,
			CommitReady: true,
		},
	}
	r.publishStateLocked()
	if err := r.recoverFromStores(); err != nil {
		return nil, err
	}
	r.startAppendCollector()
	r.startAdvancePublisher()
	r.startCheckpointPublisher()
	return r, nil
}

func (r *replica) appendLogger() wklog.Logger {
	if r == nil || r.logger == nil {
		return wklog.NewNop()
	}
	return r.logger.Named("replica")
}

func effectiveAppendGroupCommitMaxWait(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return time.Millisecond
}

func effectiveAppendGroupCommitMaxRecords(configured int) int {
	if configured > 0 {
		return configured
	}
	return 64
}

func effectiveAppendGroupCommitMaxBytes(configured int) int {
	if configured > 0 {
		return configured
	}
	return 64 * 1024
}

func (r *replica) SetLeaderLocalAppendNotifier(fn func()) {
	r.mu.Lock()
	r.onLeaderLocalAppend = fn
	r.mu.Unlock()
}

func (r *replica) SetLeaderHWAdvanceNotifier(fn func()) {
	r.mu.Lock()
	r.onLeaderHWAdvance = fn
	r.mu.Unlock()
}

func (r *replica) ApplyMeta(meta channel.Meta) error {
	r.appendMu.Lock()
	defer r.appendMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if err := r.applyMetaLocked(meta); err != nil {
		return err
	}
	if r.state.Role == channel.ReplicaRoleLeader || r.state.Role == channel.ReplicaRoleFencedLeader {
		r.beginLeaderReconcileLocked()
	}
	r.publishStateLocked()
	return nil
}

func (r *replica) BecomeLeader(meta channel.Meta) error {
	r.appendMu.Lock()
	r.mu.Lock()
	if r.state.Role == channel.ReplicaRoleTombstoned {
		r.mu.Unlock()
		r.appendMu.Unlock()
		return channel.ErrTombstoned
	}
	if !r.recovered {
		r.mu.Unlock()
		r.appendMu.Unlock()
		return channel.ErrCorruptState
	}

	normalized, err := normalizeMeta(meta)
	if err != nil {
		r.mu.Unlock()
		r.appendMu.Unlock()
		return err
	}
	if normalized.Leader != r.localNode {
		r.mu.Unlock()
		r.appendMu.Unlock()
		return channel.ErrInvalidMeta
	}
	if err := r.validateMetaLocked(normalized); err != nil {
		r.mu.Unlock()
		r.appendMu.Unlock()
		return err
	}

	leo := r.log.LEO()
	if leo < r.state.HW {
		r.mu.Unlock()
		r.appendMu.Unlock()
		return channel.ErrCorruptState
	}

	if len(r.epochHistory) == 0 || r.epochHistory[len(r.epochHistory)-1].Epoch != normalized.Epoch {
		point := channel.EpochPoint{Epoch: normalized.Epoch, StartOffset: leo}
		if err := r.appendEpochPointLocked(point); err != nil {
			r.mu.Unlock()
			r.appendMu.Unlock()
			return err
		}
	}

	r.commitMetaLocked(normalized)
	r.state.Role = channel.ReplicaRoleLeader
	r.state.LEO = leo
	r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, leo)
	r.seedLeaderProgressLocked(normalized.ISR, leo, r.state.HW)
	needsLeaderReconcile := r.needsLeaderReconcileLocked()
	r.beginLeaderReconcileLocked()
	needsLocalReconcile := needsLeaderReconcile && len(r.reconcilePending) == 0
	if !r.now().Before(normalized.LeaseUntil) {
		r.state.Role = channel.ReplicaRoleFencedLeader
		r.publishStateLocked()
		r.mu.Unlock()
		r.appendMu.Unlock()
		return channel.ErrLeaseExpired
	}
	r.publishStateLocked()
	probeSource := r.probeSource
	needsReconcile := needsLeaderReconcile && probeSource != nil
	r.mu.Unlock()
	r.appendMu.Unlock()
	if needsLocalReconcile {
		return r.runLocalLeaderReconcile()
	}
	if !needsReconcile {
		return nil
	}
	return r.runConfiguredLeaderReconcile(context.Background(), normalized, probeSource)
}

func (r *replica) BecomeFollower(meta channel.Meta) error {
	r.appendMu.Lock()
	defer r.appendMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if meta.Leader == r.localNode {
		return channel.ErrInvalidMeta
	}
	if err := r.applyMetaLocked(meta); err != nil {
		return err
	}
	r.state.Role = channel.ReplicaRoleFollower
	r.reconcilePending = nil
	r.failOutstandingAppendWorkLocked(channel.ErrNotLeader)
	r.publishStateLocked()
	return nil
}

func (r *replica) Tombstone() error {
	r.appendMu.Lock()
	defer r.appendMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	r.state.Role = channel.ReplicaRoleTombstoned
	r.reconcilePending = nil
	r.failOutstandingAppendWorkLocked(channel.ErrTombstoned)
	r.publishStateLocked()
	return nil
}

func (r *replica) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.appendMu.Lock()
		r.mu.Lock()
		r.closed = true
		r.failOutstandingAppendWorkLocked(channel.ErrNotLeader)
		r.publishStateLocked()
		r.mu.Unlock()
		r.appendMu.Unlock()
		if r.stopCh != nil {
			close(r.stopCh)
		}
	})
	if r.collectorDone != nil {
		<-r.collectorDone
	}
	if r.advanceDone != nil {
		<-r.advanceDone
	}
	if r.checkpointDone != nil {
		<-r.checkpointDone
	}
	return nil
}

func (r *replica) Status() channel.ReplicaState {
	state := r.statePointer.Load()
	if state == nil {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.state
	}
	return *state
}

func (r *replica) publishStateLocked() {
	snapshot := r.state
	r.statePointer.Store(&snapshot)
	if r.onStateChange != nil {
		r.onStateChange()
	}
}
