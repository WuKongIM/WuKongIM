package app

import (
	"context"
	"errors"
	"sync"
	"time"

	channelmigrationruntime "github.com/WuKongIM/WuKongIM/internal/runtime/channelmigration"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/legacy/channel/runtime"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type channelMigrationTicker interface {
	Tick(context.Context) error
}

type channelMigrationLifecycleConfig struct {
	// Executor advances durable migration tasks for locally led slots.
	Executor channelMigrationTicker
	// Interval controls the steady-state task scan cadence.
	Interval time.Duration
	// After supplies timers and is overridden by tests.
	After func(time.Duration) <-chan time.Time
	// Logger receives non-terminal executor errors.
	Logger wklog.Logger
}

// channelMigrationLifecycle runs the app-neutral migration executor as an app lifecycle component.
type channelMigrationLifecycle struct {
	cfg    channelMigrationLifecycleConfig
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newChannelMigrationLifecycle(cfg channelMigrationLifecycleConfig) *channelMigrationLifecycle {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultChannelMigrationScanInterval
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}
	return &channelMigrationLifecycle{cfg: cfg}
}

func (l *channelMigrationLifecycle) Start(ctx context.Context) error {
	if l == nil || l.cfg.Executor == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	l.cancel = cancel
	l.done = done
	go l.run(runCtx, done)
	return nil
}

func (l *channelMigrationLifecycle) Stop(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	cancel := l.cancel
	done := l.done
	l.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		l.mu.Lock()
		if l.done == done {
			l.cancel = nil
			l.done = nil
		}
		l.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *channelMigrationLifecycle) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	l.tick(ctx)
	next := l.cfg.After(l.cfg.Interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-next:
			l.tick(ctx)
			next = l.cfg.After(l.cfg.Interval)
		}
	}
}

func (l *channelMigrationLifecycle) tick(ctx context.Context) {
	if err := l.cfg.Executor.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		l.cfg.Logger.Warn("channel migration executor tick failed", wklog.Error(err))
	}
}

type appChannelMigrationStore struct {
	*metastore.Store
}

func (s appChannelMigrationStore) ListRunnableTasksForLocalLeaderSlots(ctx context.Context, nowMS int64, limit int) ([]channelmigrationruntime.Task, error) {
	if s.Store == nil {
		return nil, channel.ErrInvalidConfig
	}
	return s.Store.ListRunnableChannelMigrationTasksForLocalLeaderSlots(ctx, nowMS, limit)
}

func (s appChannelMigrationStore) GarbageCollectTerminalTasks(ctx context.Context, beforeMS int64, limit int) (int, error) {
	if s.Store == nil {
		return 0, channel.ErrInvalidConfig
	}
	return s.Store.GarbageCollectTerminalChannelMigrationTasks(ctx, beforeMS, limit)
}

type appChannelMigrationSlots struct {
	cluster raftcluster.API
}

func (s appChannelMigrationSlots) IsLocalLeader(ctx context.Context, slotID uint32) (bool, error) {
	if err := contextError(ctx); err != nil {
		return false, err
	}
	if s.cluster == nil {
		return false, channel.ErrInvalidConfig
	}
	leaderID, err := s.cluster.LeaderOf(multiraft.SlotID(slotID))
	if err != nil {
		return false, err
	}
	return s.cluster.IsLocal(leaderID), nil
}

func (s appChannelMigrationSlots) SlotForChannel(channelID string) uint32 {
	if s.cluster == nil {
		return 0
	}
	return uint32(s.cluster.SlotForKey(channelID))
}

type appChannelMigrationProbeTransport interface {
	Probe(context.Context, channel.NodeID, channelruntime.ReconcileProbeRequestEnvelope) (channelruntime.ReconcileProbeResponseEnvelope, error)
}

type appChannelMigrationProbeClient struct {
	client    appChannelMigrationProbeTransport
	localNode channel.NodeID
}

func (p appChannelMigrationProbeClient) ProbeChannel(ctx context.Context, nodeID channel.NodeID, meta channel.Meta) (channelmigrationruntime.ProbeReport, error) {
	if p.client == nil {
		return channelmigrationruntime.ProbeReport{}, channel.ErrInvalidConfig
	}
	if p.localNode == 0 {
		return channelmigrationruntime.ProbeReport{}, channel.ErrInvalidConfig
	}
	resp, err := p.client.Probe(ctx, nodeID, channelruntime.ReconcileProbeRequestEnvelope{
		ChannelKey:              meta.Key,
		Epoch:                   meta.Epoch,
		LeaderEpoch:             meta.LeaderEpoch,
		ReplicaID:               p.localNode,
		RequireExtendedResponse: true,
	})
	if err != nil {
		return channelmigrationruntime.ProbeReport{}, err
	}
	return channelMigrationProbeReportFromResponse(nodeID, meta, resp), nil
}

func channelMigrationProbeReportFromResponse(nodeID channel.NodeID, meta channel.Meta, resp channelruntime.ReconcileProbeResponseEnvelope) channelmigrationruntime.ProbeReport {
	replicaID := resp.ReplicaID
	if replicaID == 0 {
		replicaID = nodeID
	}
	channelKey := resp.ChannelKey
	if channelKey == "" {
		channelKey = meta.Key
	}
	return channelmigrationruntime.ProbeReport{
		ChannelKey:       channelKey,
		ChannelEpoch:     resp.Epoch,
		LeaderEpoch:      resp.LeaderEpoch,
		ReplicaID:        replicaID,
		Leader:           resp.Leader,
		Role:             resp.Role,
		CommitReady:      resp.CommitReady,
		OffsetEpoch:      resp.OffsetEpoch,
		LogStartOffset:   resp.LogStartOffset,
		LogEndOffset:     resp.LogEndOffset,
		CheckpointHW:     resp.CheckpointHW,
		EpochHistory:     nil,
		TruncateTo:       nil,
		SnapshotRequired: false,
	}
}

func newAppChannelMigrationExecutor(
	cfg Config,
	localNodeID uint64,
	store channelmigrationruntime.Store,
	slots channelmigrationruntime.SlotLeadership,
	probe channelmigrationruntime.ProbeClient,
	control channel.MigrationControlClient,
	logger wklog.Logger,
) (*channelmigrationruntime.Executor, *channelMigrationLifecycle) {
	if logger == nil {
		logger = wklog.NewNop()
	}
	executor := channelmigrationruntime.NewExecutor(channelmigrationruntime.ExecutorOptions{
		Store:            store,
		Slots:            slots,
		ProbeClient:      probe,
		MigrationControl: control,
		LocalNode:        channel.NodeID(localNodeID),
		Now:              time.Now,
		Config:           resolveAppChannelMigrationExecutorConfig(cfg),
		Logger:           logger.Named("channel.migration"),
	})
	lifecycle := newChannelMigrationLifecycle(channelMigrationLifecycleConfig{
		Executor: executor,
		Interval: cfg.ChannelMigration.ScanInterval,
		Logger:   logger.Named("channel.migration.lifecycle"),
	})
	return executor, lifecycle
}

func resolveAppChannelMigrationExecutorConfig(cfg Config) channelmigrationruntime.Config {
	return channelmigrationruntime.Config{
		ScanLimit:            cfg.ChannelMigration.ScanLimit,
		OwnerLease:           cfg.ChannelMigration.OwnerLeaseTTL,
		RetryBackoff:         cfg.ChannelMigration.RetryBackoff,
		FenceLease:           cfg.ChannelMigration.FenceTTL,
		LeaderLease:          cfg.ChannelMigration.LeaderLeaseTTL,
		CatchUpStableWindow:  cfg.ChannelMigration.CatchUpStableWindow,
		CatchUpLagThreshold:  cfg.ChannelMigration.CatchUpLagThreshold,
		GCRetention:          cfg.ChannelMigration.CompletedRetentionTTL,
		GCLimit:              cfg.ChannelMigration.GCLimit,
		MaxConcurrent:        cfg.ChannelMigration.MaxConcurrent,
		MaxConcurrentSources: cfg.ChannelMigration.MaxConcurrentPerSource,
		MaxConcurrentTargets: cfg.ChannelMigration.MaxConcurrentPerTarget,
	}
}

var _ channelmigrationruntime.Store = appChannelMigrationStore{}
var _ channelmigrationruntime.SlotLeadership = appChannelMigrationSlots{}
var _ channelmigrationruntime.ProbeClient = appChannelMigrationProbeClient{}
