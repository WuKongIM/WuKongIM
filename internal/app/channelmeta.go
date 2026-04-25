package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelreplica "github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"golang.org/x/sync/singleflight"
)

type channelMetaSource interface {
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
	ListChannelRuntimeMeta(ctx context.Context) ([]metadb.ChannelRuntimeMeta, error)
}

type hashSlotTableVersionSource interface {
	HashSlotTableVersion() uint64
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
	onStateChange               func(channel.ChannelKey)
	logger                      wklog.Logger
}

type channelMetaCluster interface {
	ApplyRoutingMeta(meta channel.Meta) error
	EnsureLocalRuntime(meta channel.Meta) error
	RemoveLocalRuntime(key channel.ChannelKey) error
}

type channelMetaSync struct {
	source          channelMetaSource
	cluster         channelMetaCluster
	bootstrap       *channelMetaBootstrapper
	localNode       uint64
	refreshInterval time.Duration

	mu           sync.Mutex
	runCtx       context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	refreshWG    sync.WaitGroup
	stateChanges chan channel.ChannelKey
	appliedLocal map[channel.ChannelKey]struct{}
	appliedSlots map[multiraft.SlotID]int
	slotLeaders  map[multiraft.SlotID]multiraft.NodeID
	cache        channelActivationCache
	pendingSlots map[multiraft.SlotID]struct{}
	dirtySlots   map[multiraft.SlotID]struct{}
	nodeLiveness map[uint64]controllermeta.NodeStatus
	localRuntime channel.HandlerRuntime
	livenessSF   singleflight.Group
	repairer     channelMetaRepairer

	lastHashSlotTableVersion uint64
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
		Logger:                      f.logger,
		OnStateChange:               onStateChange,
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
func (s channelEpochHistoryStore) TruncateTo(leo uint64) error { return s.store.TruncateHistoryTo(leo) }

type channelSnapshotApplier struct{ store *channelstore.ChannelStore }

func (s channelSnapshotApplier) InstallSnapshot(_ context.Context, snap channel.Snapshot) error {
	return s.store.StoreSnapshotPayload(snap.Payload)
}

func (s *channelMetaSync) RefreshChannelMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error) {
	return s.ActivateByID(ctx, id, channelruntime.ActivationSourceBusiness)
}

func (s *channelMetaSync) ensureChannelRuntimeMeta(ctx context.Context, id channel.ChannelID) (metadb.ChannelRuntimeMeta, error) {
	if s == nil || s.bootstrap == nil {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	if _, _, err := s.bootstrap.EnsureChannelRuntimeMeta(ctx, id); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return s.source.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
}

func (s *channelMetaSync) Start() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.runCtx = ctx
	s.cancel = cancel
	s.done = done
	s.stateChanges = make(chan channel.ChannelKey, 128)
	interval := s.refreshInterval
	s.mu.Unlock()

	go s.watchActiveSlotLeaders(ctx, interval, done)
	s.refreshWG.Add(1)
	go s.watchLocalReplicaStateChanges(ctx)
	return nil
}

func (s *channelMetaSync) Stop() error {
	return s.stop(true)
}

func (s *channelMetaSync) StopWithoutCleanup() error {
	return s.stop(false)
}

func (s *channelMetaSync) stop(cleanup bool) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.runCtx = nil
	s.cancel = nil
	s.done = nil
	s.stateChanges = nil
	s.mu.Unlock()
	if cancel == nil {
		return s.cleanupAppliedLocal()
	}
	cancel()
	<-done
	s.refreshWG.Wait()
	if !cleanup {
		return nil
	}
	return s.cleanupAppliedLocal()
}

func (s *channelMetaSync) syncOnce(ctx context.Context) error {
	s.observeHashSlotTableVersion()
	metas, err := s.source.ListChannelRuntimeMeta(ctx)
	if err != nil {
		return err
	}
	currentLocal := make(map[channel.ChannelKey]struct{})
	for _, meta := range metas {
		if !containsUint64(meta.Replicas, s.localNode) {
			continue
		}
		meta, err = s.reconcileChannelRuntimeMeta(ctx, meta)
		if err != nil {
			return err
		}
		applied, err := s.applyLocalMeta(meta)
		if err != nil {
			return err
		}
		currentLocal[applied.Key] = struct{}{}
	}
	for key := range s.snapshotAppliedLocal() {
		if _, ok := currentLocal[key]; ok {
			continue
		}
		if err := s.removeLocalRuntime(key); err != nil {
			return err
		}
	}
	s.setAppliedLocal(currentLocal)
	return nil
}

func (s *channelMetaSync) applyLocalMeta(meta metadb.ChannelRuntimeMeta) (channel.Meta, error) {
	if !containsUint64(meta.Replicas, s.localNode) {
		return channel.Meta{}, channel.ErrStaleMeta
	}
	return s.applyAuthoritativeMeta(meta)
}

func (s *channelMetaSync) reconcileChannelRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (metadb.ChannelRuntimeMeta, error) {
	if s == nil || s.bootstrap == nil {
		return s.maybeRepairChannelRuntimeMeta(ctx, meta)
	}
	reconciled, _, err := s.bootstrap.RenewChannelLeaderLease(ctx, meta, s.localNode, s.leaseRenewLeadTime())
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return s.maybeRepairChannelRuntimeMeta(ctx, reconciled)
}

func (s *channelMetaSync) leaseRenewLeadTime() time.Duration {
	if s == nil || s.refreshInterval <= 0 {
		return time.Second
	}
	return s.refreshInterval
}

func (s *channelMetaSync) maybeRepairChannelRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (metadb.ChannelRuntimeMeta, error) {
	if s == nil || s.repairer == nil {
		return meta, nil
	}
	if meta.Leader != 0 {
		s.warmNodeLiveness(ctx, meta.Leader)
	}
	need, reason := s.needsLeaderRepair(meta)
	if !need {
		return meta, nil
	}
	repaired, _, err := s.repairer.RepairIfNeeded(ctx, meta, reason)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return repaired, nil
}

func (s *channelMetaSync) observeHashSlotTableVersion() {
	if s == nil || s.source == nil {
		return
	}
	versionSource, ok := s.source.(hashSlotTableVersionSource)
	if !ok {
		return
	}
	version := versionSource.HashSlotTableVersion()
	changed := false
	s.mu.Lock()
	if s.lastHashSlotTableVersion != 0 && version != s.lastHashSlotTableVersion {
		s.resetAppliedLocalTrackingLocked()
		changed = true
	}
	s.lastHashSlotTableVersion = version
	s.mu.Unlock()
	if changed {
		s.cache.clear()
	}
}

func (s *channelMetaSync) watchActiveSlotLeaders(ctx context.Context, interval time.Duration, done chan struct{}) {
	defer close(done)
	if s == nil {
		return
	}
	if interval <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		s.pollActiveSlotLeaders()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func projectChannelMeta(meta metadb.ChannelRuntimeMeta) channel.Meta {
	id := channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}
	var leaseUntil time.Time
	if meta.LeaseUntilMS > 0 {
		leaseUntil = time.UnixMilli(meta.LeaseUntilMS).UTC()
	}
	return channel.Meta{
		Key:         channelhandler.KeyFromChannelID(id),
		ID:          id,
		Epoch:       meta.ChannelEpoch,
		LeaderEpoch: meta.LeaderEpoch,
		Replicas:    projectNodeIDs(meta.Replicas),
		ISR:         projectNodeIDs(meta.ISR),
		Leader:      channel.NodeID(meta.Leader),
		MinISR:      int(meta.MinISR),
		LeaseUntil:  leaseUntil,
		Status:      channel.Status(meta.Status),
		Features: channel.Features{
			MessageSeqFormat: channel.MessageSeqFormat(meta.Features),
		},
	}
}

func projectNodeIDs(ids []uint64) []channel.NodeID {
	out := make([]channel.NodeID, 0, len(ids))
	for _, id := range ids {
		out = append(out, channel.NodeID(id))
	}
	return out
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *channelMetaSync) removeLocalRuntime(key channel.ChannelKey) error {
	if s.cluster == nil {
		return nil
	}
	if err := s.cluster.RemoveLocalRuntime(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.untrackAppliedLocalKeyLocked(key)
	return nil
}

func (s *channelMetaSync) cleanupAppliedLocal() error {
	var err error
	for key := range s.snapshotAppliedLocal() {
		err = errors.Join(err, s.removeLocalRuntime(key))
	}
	s.mu.Lock()
	s.resetAppliedLocalTrackingLocked()
	s.mu.Unlock()
	return err
}

func (s *channelMetaSync) snapshotAppliedLocal() map[channel.ChannelKey]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAppliedLocalSet(s.appliedLocal)
}

func (s *channelMetaSync) setAppliedLocal(values map[channel.ChannelKey]struct{}) {
	s.mu.Lock()
	s.resetAppliedLocalTrackingLocked()
	for key := range values {
		s.trackAppliedLocalKeyLocked(key)
	}
	s.mu.Unlock()
}

func (s *channelMetaSync) snapshotAppliedSlots() []multiraft.SlotID {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.appliedSlots) == 0 {
		return nil
	}
	slots := make([]multiraft.SlotID, 0, len(s.appliedSlots))
	for slotID := range s.appliedSlots {
		slots = append(slots, slotID)
	}
	return slots
}

func (s *channelMetaSync) pollActiveSlotLeaders() {
	if s == nil || s.bootstrap == nil || s.bootstrap.cluster == nil {
		return
	}
	for _, slotID := range s.snapshotAppliedSlots() {
		leader, err := s.bootstrap.cluster.LeaderOf(slotID)
		if err != nil || leader == 0 {
			continue
		}
		if !s.updateObservedSlotLeader(slotID, leader) {
			continue
		}
		s.scheduleSlotLeaderRefresh(slotID)
	}
}

func (s *channelMetaSync) updateObservedSlotLeader(slotID multiraft.SlotID, leader multiraft.NodeID) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slotLeaders == nil {
		s.slotLeaders = make(map[multiraft.SlotID]multiraft.NodeID)
	}
	previous := s.slotLeaders[slotID]
	s.slotLeaders[slotID] = leader
	return previous != leader
}

func (s *channelMetaSync) trackAppliedLocalKeyLocked(key channel.ChannelKey) {
	if s == nil {
		return
	}
	if s.appliedLocal == nil {
		s.appliedLocal = make(map[channel.ChannelKey]struct{})
	}
	if _, exists := s.appliedLocal[key]; exists {
		return
	}
	s.appliedLocal[key] = struct{}{}
	slotID, ok := s.slotForChannelKey(key)
	if !ok {
		return
	}
	if s.appliedSlots == nil {
		s.appliedSlots = make(map[multiraft.SlotID]int)
	}
	s.appliedSlots[slotID]++
}

func (s *channelMetaSync) untrackAppliedLocalKeyLocked(key channel.ChannelKey) {
	if s == nil || s.appliedLocal == nil {
		return
	}
	if _, exists := s.appliedLocal[key]; !exists {
		return
	}
	delete(s.appliedLocal, key)
	if len(s.appliedLocal) == 0 {
		s.appliedLocal = nil
	}
	slotID, ok := s.slotForChannelKey(key)
	if !ok || s.appliedSlots == nil {
		return
	}
	if remaining := s.appliedSlots[slotID] - 1; remaining > 0 {
		s.appliedSlots[slotID] = remaining
		return
	}
	delete(s.appliedSlots, slotID)
	if len(s.appliedSlots) == 0 {
		s.appliedSlots = nil
	}
	if s.slotLeaders != nil {
		delete(s.slotLeaders, slotID)
		if len(s.slotLeaders) == 0 {
			s.slotLeaders = nil
		}
	}
}

func (s *channelMetaSync) resetAppliedLocalTrackingLocked() {
	if s == nil {
		return
	}
	s.appliedLocal = nil
	s.appliedSlots = nil
	s.slotLeaders = nil
}

func (s *channelMetaSync) slotForChannelKey(key channel.ChannelKey) (multiraft.SlotID, bool) {
	if s == nil || s.bootstrap == nil || s.bootstrap.cluster == nil {
		return 0, false
	}
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return 0, false
	}
	return s.bootstrap.cluster.SlotForKey(id.ID), true
}

func cloneAppliedLocalSet(values map[channel.ChannelKey]struct{}) map[channel.ChannelKey]struct{} {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[channel.ChannelKey]struct{}, len(values))
	for key := range values {
		cloned[key] = struct{}{}
	}
	return cloned
}
