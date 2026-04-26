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

type appendRequestStage uint8

const (
	appendRequestQueued appendRequestStage = iota + 1
	appendRequestDurable
	appendRequestWaitingQuorum
	appendRequestCompleted
)

type appendRequest struct {
	requestID  uint64
	ctx        context.Context
	batch      []channel.Record
	byteCount  int
	commitMode channel.CommitMode
	waiter     *appendWaiter
	enqueuedAt time.Time
	stage      appendRequestStage
	completed  bool
}

type appendWaiter struct {
	request       *appendRequest
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

	durableMu sync.Mutex

	localNode     channel.NodeID
	onStateChange func()

	log                 LogStore
	checkpoints         CheckpointStore
	history             EpochHistoryStore
	durable             durableReplicaStore
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

	roleGeneration               uint64
	nextEffectID                 uint64
	pendingCheckpointEffectID    uint64
	pendingSnapshotEffectID      uint64
	pendingFollowerApplyEffectID uint64
	pendingReconcileEffectID     uint64
	pendingLeaderEpochEffectID   uint64

	appendGroupCommit      appendGroupCommitConfig
	appendRequests         map[uint64]*appendRequest
	appendPending          []*appendRequest
	appendInFlightIDs      []uint64
	appendInFlightEffectID uint64
	appendFlushScheduled   bool
	appendEffects          chan appendLeaderBatchEffect
	checkpointEffects      chan storeCheckpointEffect
	loopCommands           chan replicaLoopCommand
	loopResults            chan machineEvent
	stopCh                 chan struct{}
	loopDone               chan struct{}
	appendWorkerDone       chan struct{}
	checkpointWorkerDone   chan struct{}
	closeOnce              sync.Once

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
		history:       cfg.EpochHistoryStore,
		durable:       newDurableReplicaStore(cfg.LogStore, cfg.CheckpointStore, cfg.ApplyFetchStore, cfg.EpochHistoryStore, cfg.SnapshotApplier),
		probeSource:   cfg.ReconcileProbeSource,
		now:           cfg.Now,
		logger:        cfg.Logger,
		appendGroupCommit: appendGroupCommitConfig{
			maxWait:    effectiveAppendGroupCommitMaxWait(cfg.AppendGroupCommitMaxWait),
			maxRecords: effectiveAppendGroupCommitMaxRecords(cfg.AppendGroupCommitMaxRecords),
			maxBytes:   effectiveAppendGroupCommitMaxBytes(cfg.AppendGroupCommitMaxBytes),
		},
		appendEffects:        make(chan appendLeaderBatchEffect, 16),
		checkpointEffects:    make(chan storeCheckpointEffect, 1),
		loopCommands:         make(chan replicaLoopCommand, 16),
		loopResults:          make(chan machineEvent, 16),
		stopCh:               make(chan struct{}),
		loopDone:             make(chan struct{}),
		appendWorkerDone:     make(chan struct{}),
		checkpointWorkerDone: make(chan struct{}),
		roleGeneration:       1,
		state: channel.ReplicaState{
			Role:        channel.ReplicaRoleFollower,
			CommitReady: true,
		},
	}
	r.publishStateLocked()
	if err := r.recoverFromStores(); err != nil {
		return nil, err
	}
	r.startLoop()
	r.startAppendEffectWorker()
	r.startCheckpointEffectWorker()
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
	result := r.submitLoopCommand(context.Background(), machineApplyMetaCommand{Meta: meta})
	if result.Err != nil {
		return result.Err
	}
	return r.executeLeaderReconcileEffects(result.Effects)
}

func (r *replica) BecomeLeader(meta channel.Meta) error {
	result := r.submitLoopCommand(context.Background(), machineBecomeLeaderCommand{Meta: meta})
	if result.Err != nil {
		return result.Err
	}
	return r.executeLeaderReconcileEffects(result.Effects)
}

func (r *replica) BecomeFollower(meta channel.Meta) error {
	return r.submitLoopCommand(context.Background(), machineBecomeFollowerCommand{Meta: meta}).Err
}

func (r *replica) Tombstone() error {
	return r.submitLoopCommand(context.Background(), machineTombstoneCommand{}).Err
}

func (r *replica) Close() error {
	if r == nil {
		return nil
	}
	var closeErr error
	r.closeOnce.Do(func() {
		closeErr = r.submitLoopCommand(context.Background(), machineCloseCommand{}).Err
		if r.stopCh != nil {
			close(r.stopCh)
		}
	})
	if r.loopDone != nil {
		<-r.loopDone
	}
	if r.appendWorkerDone != nil {
		<-r.appendWorkerDone
	}
	if r.checkpointWorkerDone != nil {
		<-r.checkpointWorkerDone
	}
	return closeErr
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
