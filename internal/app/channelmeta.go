package app

import (
	"context"
	"sync"
	"time"

	runtimechannelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelreplica "github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type channelMetaSync struct {
	resolver *runtimechannelmeta.Sync
}

type channelMetaMetrics interface {
	ObserveMetaRefresh(result string, dur time.Duration)
}

type channelMetaMetricsObserver struct {
	metrics channelMetaMetrics
}

type channelReplicaExecutionMetrics interface {
	SetExecutionQueueDepth(int)
	ObserveExecutionEnqueue(string)
	SetExecutionWorkerBusyRatio(float64)
	ObserveExecutionMailboxWait(time.Duration)
}

type channelReplicaExecutionMetricsObserver struct {
	metrics channelReplicaExecutionMetrics
}

func (o channelMetaMetricsObserver) OnMetaRefresh(event runtimechannelmeta.MetaRefreshEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.ObserveMetaRefresh(string(event.Result), event.Duration)
}

func (o channelReplicaExecutionMetricsObserver) SetQueueDepth(v int) {
	metrics := o.channelMetrics()
	if metrics == nil {
		return
	}
	metrics.SetExecutionQueueDepth(v)
}

func (o channelReplicaExecutionMetricsObserver) ObserveEnqueue(result string) {
	metrics := o.channelMetrics()
	if metrics == nil {
		return
	}
	metrics.ObserveExecutionEnqueue(result)
}

func (o channelReplicaExecutionMetricsObserver) SetWorkerBusyRatio(v float64) {
	metrics := o.channelMetrics()
	if metrics == nil {
		return
	}
	metrics.SetExecutionWorkerBusyRatio(v)
}

func (o channelReplicaExecutionMetricsObserver) ObserveMailboxWait(d time.Duration) {
	metrics := o.channelMetrics()
	if metrics == nil {
		return
	}
	metrics.ObserveExecutionMailboxWait(d)
}

func (o channelReplicaExecutionMetricsObserver) channelMetrics() channelReplicaExecutionMetrics {
	return o.metrics
}

func (s *channelMetaSync) Start() error {
	if s == nil || s.resolver == nil {
		return nil
	}
	return s.resolver.Start()
}

func (s *channelMetaSync) Stop() error {
	if s == nil || s.resolver == nil {
		return nil
	}
	return s.resolver.Stop()
}

func (s *channelMetaSync) StopWithoutCleanup() error {
	if s == nil || s.resolver == nil {
		return nil
	}
	return s.resolver.StopWithoutCleanup()
}

func (s *channelMetaSync) RefreshChannelMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error) {
	if s == nil || s.resolver == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	return s.resolver.RefreshChannelMeta(ctx, id)
}

func (s *channelMetaSync) InvalidateChannelMeta(id channel.ChannelID) {
	if s == nil || s.resolver == nil {
		return
	}
	s.resolver.InvalidateChannelMeta(id)
}

func (s *channelMetaSync) ActivateByID(ctx context.Context, id channel.ChannelID, source channelruntime.ActivationSource) (channel.Meta, error) {
	if s == nil || s.resolver == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	return s.resolver.ActivateByID(ctx, id, source)
}

func (s *channelMetaSync) ActivateByKey(ctx context.Context, key channel.ChannelKey, source channelruntime.ActivationSource) (channel.Meta, error) {
	if s == nil || s.resolver == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	return s.resolver.ActivateByKey(ctx, key, source)
}

func (s *channelMetaSync) RefreshAuthoritativeByKey(ctx context.Context, key channel.ChannelKey) (channel.Meta, error) {
	if s == nil || s.resolver == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	return s.resolver.RefreshAuthoritativeByKey(ctx, key)
}

func (s *channelMetaSync) applyAuthoritativeMeta(meta metadb.ChannelRuntimeMeta) (channel.Meta, error) {
	if s == nil || s.resolver == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	return s.resolver.ApplyAuthoritativeMeta(meta)
}

type channelMetaRuntimeAdapter struct {
	routing runtimechannelmeta.RoutingRuntime
	local   interface {
		EnsureLocalRuntime(channel.Meta) error
		RemoveLocalRuntime(channel.ChannelKey) error
	}
	observer channel.HandlerRuntime
}

func (a channelMetaRuntimeAdapter) ApplyRoutingMeta(meta channel.Meta) error {
	if a.routing == nil {
		return channel.ErrInvalidConfig
	}
	return a.routing.ApplyRoutingMeta(meta)
}

func (a channelMetaRuntimeAdapter) EnsureLocalRuntime(meta channel.Meta) error {
	if a.local == nil {
		return channel.ErrInvalidConfig
	}
	return a.local.EnsureLocalRuntime(meta)
}

func (a channelMetaRuntimeAdapter) RemoveLocalRuntime(key channel.ChannelKey) error {
	if a.local == nil {
		return nil
	}
	return a.local.RemoveLocalRuntime(key)
}

func (a channelMetaRuntimeAdapter) Channel(key channel.ChannelKey) (runtimechannelmeta.ChannelObserver, bool) {
	if a.observer == nil {
		return nil, false
	}
	handle, ok := a.observer.Channel(key)
	if !ok {
		return nil, false
	}
	return handle, true
}

type memoryGenerationStore struct {
	mu     sync.RWMutex
	values map[channel.ChannelKey]uint64
}

type channelReplicaFactory struct {
	db                          *channelstore.Engine
	localNode                   channel.NodeID
	now                         func() time.Time
	appendGroupCommitMaxWait    time.Duration
	appendGroupCommitMaxRecords int
	appendGroupCommitMaxBytes   int
	executionMode               string
	executionWorkers            int
	executionQueueSize          int
	executionPool               *channelreplica.ExecutionPool
	onStateChange               func(channel.ChannelKey)
	logger                      wklog.Logger
}

func newMemoryGenerationStore() *memoryGenerationStore {
	return &memoryGenerationStore{values: make(map[channel.ChannelKey]uint64)}
}

func (s *memoryGenerationStore) Load(channelKey channel.ChannelKey) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[channelKey], nil
}

func (s *memoryGenerationStore) Store(channelKey channel.ChannelKey, generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[channelKey] = generation
	return nil
}

func newChannelReplicaFactory(db *channelstore.Engine, localNode channel.NodeID, now func() time.Time, appendGroupCommitMaxWait time.Duration, appendGroupCommitMaxRecords, appendGroupCommitMaxBytes int, logger wklog.Logger) *channelReplicaFactory {
	return &channelReplicaFactory{
		db:                          db,
		localNode:                   localNode,
		now:                         now,
		appendGroupCommitMaxWait:    appendGroupCommitMaxWait,
		appendGroupCommitMaxRecords: appendGroupCommitMaxRecords,
		appendGroupCommitMaxBytes:   appendGroupCommitMaxBytes,
		logger:                      logger,
	}
}

func (f *channelReplicaFactory) setExecutionPool(mode string, workers, queueSize int, pool *channelreplica.ExecutionPool) {
	if f == nil {
		return
	}
	f.executionMode = mode
	f.executionWorkers = workers
	f.executionQueueSize = queueSize
	f.executionPool = pool
}

func (f *channelReplicaFactory) New(cfg channelruntime.ChannelConfig) (channelreplica.Replica, error) {
	store := f.db.ForChannel(cfg.ChannelKey, cfg.Meta.ID)
	onStateChange := cfg.OnReplicaStateChange
	if f.onStateChange != nil {
		onStateChange = func() {
			if cfg.OnReplicaStateChange != nil {
				cfg.OnReplicaStateChange()
			}
			f.onStateChange(cfg.ChannelKey)
		}
	}
	return channelreplica.NewReplica(channelreplica.ReplicaConfig{
		LocalNode:                   f.localNode,
		LogStore:                    store,
		CheckpointStore:             channelCheckpointStore{store: store},
		ApplyFetchStore:             store,
		EpochHistoryStore:           channelEpochHistoryStore{store: store},
		SnapshotApplier:             channelSnapshotApplier{store: store},
		Now:                         f.now,
		AppendGroupCommitMaxWait:    f.appendGroupCommitMaxWait,
		AppendGroupCommitMaxRecords: f.appendGroupCommitMaxRecords,
		AppendGroupCommitMaxBytes:   f.appendGroupCommitMaxBytes,
		Execution: channelreplica.ExecutionConfig{
			Mode:        channelreplica.ExecutionMode(f.executionMode),
			Pool:        f.executionPool,
			MailboxSize: f.executionQueueSize,
		},
		Logger:        f.logger,
		OnStateChange: onStateChange,
	})
}

type channelCheckpointStore struct{ store *channelstore.ChannelStore }

func (s channelCheckpointStore) Load() (channel.Checkpoint, error) { return s.store.LoadCheckpoint() }
func (s channelCheckpointStore) Store(cp channel.Checkpoint) error {
	return s.store.StoreCheckpoint(cp)
}

type channelEpochHistoryStore struct{ store *channelstore.ChannelStore }

func (s channelEpochHistoryStore) Load() ([]channel.EpochPoint, error) { return s.store.LoadHistory() }
func (s channelEpochHistoryStore) Append(point channel.EpochPoint) error {
	return s.store.AppendHistory(point)
}
func (s channelEpochHistoryStore) TruncateTo(leo uint64) error {
	return s.store.TruncateHistoryTo(leo)
}

type channelSnapshotApplier struct{ store *channelstore.ChannelStore }

func (s channelSnapshotApplier) InstallSnapshot(_ context.Context, snap channel.Snapshot) error {
	return s.store.StoreSnapshotPayload(snap.Payload)
}

func (s channelSnapshotApplier) LoadSnapshotPayload(_ context.Context) ([]byte, error) {
	payload, err := s.store.LoadSnapshotPayload()
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, channel.ErrEmptyState
	}
	return payload, nil
}
func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
