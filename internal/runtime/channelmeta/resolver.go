package channelmeta

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// RepairPolicy decides whether a refreshed authoritative metadata record needs leader repair.
type RepairPolicy func(meta metadb.ChannelRuntimeMeta) (bool, string)

// SyncOptions configures the channel runtime metadata resolver.
type SyncOptions struct {
	// Source reads authoritative runtime metadata from slot metadata storage.
	Source MetaSource
	// Runtime applies routing metadata and owns node-local channel runtimes.
	Runtime Runtime
	// Bootstrapper creates missing authoritative runtime metadata and renews local leader leases.
	Bootstrapper Bootstrapper
	// Cluster maps channel keys to slots and observes authoritative slot leaders.
	Cluster BootstrapCluster
	// Repairer runs a neutral channel leader repair request when RepairPolicy asks for repair.
	Repairer Repairer
	// RepairPolicy keeps repair decisions outside the resolver core while it owns refresh orchestration.
	RepairPolicy RepairPolicy
	// LivenessSource warms controller-observed node liveness when repair policy needs it.
	LivenessSource LivenessSource
	// LocalNode is this process' cluster node ID.
	LocalNode uint64
	// RefreshInterval controls active-slot leader polling and lease-renew lead time.
	RefreshInterval time.Duration
	// Now returns the current time for cache and lease decisions.
	Now func() time.Time
	// AfterLocalApply is called after metadata is applied to a local runtime and repair policy still wants repair.
	AfterLocalApply func(channel.Meta)
}

// Sync refreshes authoritative channel runtime metadata into routing and local runtime views.
type Sync struct {
	source          MetaSource
	runtime         Runtime
	bootstrap       Bootstrapper
	cluster         BootstrapCluster
	repairer        Repairer
	repairPolicy    RepairPolicy
	livenessSource  LivenessSource
	localNode       uint64
	refreshInterval time.Duration
	now             func() time.Time
	afterLocalApply func(channel.Meta)

	mu           sync.Mutex
	scheduleMu   sync.Mutex
	runCtx       context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	refreshWG    sync.WaitGroup
	stateChanges chan channel.ChannelKey
	appliedLocal map[channel.ChannelKey]struct{}
	appliedSlots map[multiraft.SlotID]int
	slotLeaders  map[multiraft.SlotID]multiraft.NodeID
	cache        ActivationCache
	liveness     LivenessCache
	slotRefresh  SlotRefreshScheduler

	lastHashSlotTableVersion uint64
}

var _ Refresher = (*Sync)(nil)
var _ Activator = (*Sync)(nil)

// NewSync constructs a channel runtime metadata resolver.
func NewSync(opts SyncOptions) *Sync {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cluster := opts.Cluster
	if cluster == nil {
		if bootstrapper, ok := opts.Bootstrapper.(*RuntimeBootstrapper); ok {
			cluster = bootstrapper.cluster
		}
	}
	return &Sync{
		source:          opts.Source,
		runtime:         opts.Runtime,
		bootstrap:       opts.Bootstrapper,
		cluster:         cluster,
		repairer:        opts.Repairer,
		repairPolicy:    opts.RepairPolicy,
		livenessSource:  opts.LivenessSource,
		localNode:       opts.LocalNode,
		refreshInterval: opts.RefreshInterval,
		now:             now,
		afterLocalApply: opts.AfterLocalApply,
	}
}

// Start launches lightweight resolver watchers without scanning all authoritative metadata.
func (s *Sync) Start() error {
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
	s.refreshWG.Add(1)
	s.mu.Unlock()

	go s.watchActiveSlotLeaders(ctx, interval, done)
	go s.watchLocalReplicaStateChanges(ctx)
	return nil
}

// Stop stops watchers and removes node-local runtimes applied by the resolver.
func (s *Sync) Stop() error {
	return s.stop(true)
}

// StopWithoutCleanup stops watchers while leaving node-local runtime cleanup to the owner.
func (s *Sync) StopWithoutCleanup() error {
	return s.stop(false)
}

func (s *Sync) stop(cleanup bool) error {
	if s == nil {
		return nil
	}
	s.scheduleMu.Lock()
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.runCtx = nil
	s.cancel = nil
	s.done = nil
	s.stateChanges = nil
	s.mu.Unlock()
	s.scheduleMu.Unlock()
	if cancel == nil {
		if cleanup {
			return s.cleanupAppliedLocal()
		}
		return nil
	}
	cancel()
	<-done
	s.refreshWG.Wait()
	if !cleanup {
		return nil
	}
	return s.cleanupAppliedLocal()
}

// RefreshChannelMeta refreshes authoritative metadata for a business append/fetch path.
func (s *Sync) RefreshChannelMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error) {
	return s.ActivateByID(ctx, id, channelruntime.ActivationSourceBusiness)
}

// ActivateByID loads authoritative metadata by channel ID and applies it to runtime views.
func (s *Sync) ActivateByID(ctx context.Context, id channel.ChannelID, source channelruntime.ActivationSource) (channel.Meta, error) {
	if s == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	return s.activate(ctx, channelhandler.KeyFromChannelID(id), source, func(ctx context.Context) (metadb.ChannelRuntimeMeta, error) {
		if s.source == nil {
			return metadb.ChannelRuntimeMeta{}, channel.ErrInvalidConfig
		}
		meta, err := s.source.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		if err != nil {
			if errors.Is(err, metadb.ErrNotFound) && source == channelruntime.ActivationSourceBusiness {
				meta, err = s.ensureChannelRuntimeMeta(ctx, id)
			}
			if err != nil {
				return metadb.ChannelRuntimeMeta{}, err
			}
		}
		return s.reconcileChannelRuntimeMeta(ctx, meta)
	})
}

// ActivateByKey loads authoritative metadata by channel key and applies it to runtime views.
func (s *Sync) ActivateByKey(ctx context.Context, key channel.ChannelKey, source channelruntime.ActivationSource) (channel.Meta, error) {
	if s == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	if source != channelruntime.ActivationSourceBusiness {
		if meta, ok := s.cache.LoadPositive(key, s.Now()); ok {
			return meta, nil
		}
		if err := s.cache.LoadNegative(key, s.Now()); err != nil {
			return channel.Meta{}, err
		}
	}
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return channel.Meta{}, err
	}
	return s.activate(ctx, key, source, func(ctx context.Context) (metadb.ChannelRuntimeMeta, error) {
		if s.source == nil {
			return metadb.ChannelRuntimeMeta{}, channel.ErrInvalidConfig
		}
		meta, err := s.source.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		if err != nil {
			return metadb.ChannelRuntimeMeta{}, err
		}
		return s.reconcileChannelRuntimeMeta(ctx, meta)
	})
}

// ApplyAuthoritativeMeta applies one authoritative metadata record to routing and local runtime views.
func (s *Sync) ApplyAuthoritativeMeta(meta metadb.ChannelRuntimeMeta) (channel.Meta, error) {
	if s == nil || s.runtime == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	rootMeta := ProjectChannelMeta(meta)
	notifyAfterLocalApply := s.shouldNotifyAfterLocalApply(meta)
	if err := s.runtime.ApplyRoutingMeta(rootMeta); err != nil {
		return channel.Meta{}, err
	}
	if containsUint64(meta.Replicas, s.localNode) {
		if err := s.runtime.EnsureLocalRuntime(rootMeta); err != nil {
			return channel.Meta{}, err
		}
		s.mu.Lock()
		s.trackAppliedLocalKeyLocked(rootMeta.Key)
		s.mu.Unlock()
		if notifyAfterLocalApply {
			s.afterLocalApply(rootMeta)
		}
		return rootMeta, nil
	}
	if err := s.removeLocalRuntime(rootMeta.Key); err != nil {
		return channel.Meta{}, err
	}
	return rootMeta, nil
}

// RefreshAuthoritativeByKey rereads and reapplies authoritative metadata for an active local key.
func (s *Sync) RefreshAuthoritativeByKey(ctx context.Context, key channel.ChannelKey) (channel.Meta, error) {
	if s == nil {
		return channel.Meta{}, channel.ErrInvalidConfig
	}
	s.observeHashSlotTableVersion()
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return channel.Meta{}, err
	}
	return s.cache.RunSingleflight(key, func() (channel.Meta, error) {
		if s.source == nil {
			return channel.Meta{}, channel.ErrInvalidConfig
		}
		meta, err := s.source.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		if err != nil {
			s.cache.StoreNegative(key, err, s.Now())
			return channel.Meta{}, err
		}
		meta, err = s.reconcileChannelRuntimeMeta(ctx, meta)
		if err != nil {
			return channel.Meta{}, err
		}
		applied, err := s.ApplyAuthoritativeMeta(meta)
		if err != nil {
			return channel.Meta{}, err
		}
		s.cache.StorePositive(key, applied, s.Now())
		return applied, nil
	})
}

// UpdateNodeLiveness stores the latest controller-observed node status.
func (s *Sync) UpdateNodeLiveness(nodeID uint64, status controllermeta.NodeStatus) {
	if s == nil {
		return
	}
	if s.liveness.Update(nodeID, status) {
		s.scheduleLeaderHealthRefresh(nodeID)
	}
}

// NodeLivenessStatus returns the cached controller-observed status for a node.
func (s *Sync) NodeLivenessStatus(nodeID uint64) (controllermeta.NodeStatus, bool) {
	if s == nil {
		return controllermeta.NodeStatusUnknown, false
	}
	return s.liveness.Status(nodeID)
}

// WarmNodeLiveness populates liveness cache from the strict source when status is absent.
func (s *Sync) WarmNodeLiveness(ctx context.Context, nodeID uint64) {
	if s == nil {
		return
	}
	s.liveness.Warm(ctx, nodeID, s.livenessSource)
}

// LocalRuntimeLeaderRepairReason reports observed local runtime leader drift for metadata.
func (s *Sync) LocalRuntimeLeaderRepairReason(meta metadb.ChannelRuntimeMeta) string {
	if s == nil || s.runtime == nil {
		return ""
	}
	key := channelhandler.KeyFromChannelID(channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)})
	handle, ok := s.runtime.Channel(key)
	if !ok {
		return ""
	}
	return ObservedLeaderRepairReason(handle.Meta(), handle.Status())
}

// Now returns the resolver clock.
func (s *Sync) Now() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// ScheduleSlotLeaderRefresh refreshes active local channel metas in a slot.
func (s *Sync) ScheduleSlotLeaderRefresh(slotID multiraft.SlotID) {
	if s == nil {
		return
	}
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	s.mu.Lock()
	runCtx := s.runCtx
	s.mu.Unlock()
	if runCtx == nil || runCtx.Err() != nil {
		return
	}
	s.slotRefresh.Schedule(runCtx, &s.refreshWG, slotID, s.snapshotAppliedLocalKeysForSlot, func(ctx context.Context, key channel.ChannelKey) {
		_, _ = s.RefreshAuthoritativeByKey(ctx, key)
	}, SlotLeaderRefreshTimeout)
}

func (s *Sync) shouldNotifyAfterLocalApply(meta metadb.ChannelRuntimeMeta) bool {
	if s == nil || s.afterLocalApply == nil || s.repairer == nil || s.repairPolicy == nil {
		return false
	}
	need, _ := s.repairPolicy(meta)
	return need
}

// EnqueueLocalReplicaStateChange enqueues a local replica state-change notification.
func (s *Sync) EnqueueLocalReplicaStateChange(key channel.ChannelKey) {
	if s == nil {
		return
	}
	s.mu.Lock()
	stateChanges := s.stateChanges
	s.mu.Unlock()
	EnqueueLocalReplicaStateChange(stateChanges, key)
}

// SlotForChannelKey maps a channel key to its authoritative slot when cluster topology is available.
func (s *Sync) SlotForChannelKey(key channel.ChannelKey) (multiraft.SlotID, bool) {
	return s.slotForChannelKey(key)
}

func (s *Sync) ensureChannelRuntimeMeta(ctx context.Context, id channel.ChannelID) (metadb.ChannelRuntimeMeta, error) {
	if s == nil || s.bootstrap == nil {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	if _, _, err := s.bootstrap.EnsureChannelRuntimeMeta(ctx, id); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if s.source == nil {
		return metadb.ChannelRuntimeMeta{}, channel.ErrInvalidConfig
	}
	return s.source.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
}

func (s *Sync) activate(ctx context.Context, key channel.ChannelKey, source channelruntime.ActivationSource, load func(context.Context) (metadb.ChannelRuntimeMeta, error)) (channel.Meta, error) {
	s.observeHashSlotTableVersion()
	if source != channelruntime.ActivationSourceBusiness {
		if meta, ok := s.cache.LoadPositive(key, s.Now()); ok {
			return meta, nil
		}
		if err := s.cache.LoadNegative(key, s.Now()); err != nil {
			return channel.Meta{}, err
		}
	}
	meta, err := s.cache.RunSingleflight(key, func() (channel.Meta, error) {
		// Re-check cache inside the coalesced call to avoid a second authority read when
		// a concurrent caller missed the outer cache check before the first call stored it.
		if source != channelruntime.ActivationSourceBusiness {
			if meta, ok := s.cache.LoadPositive(key, s.Now()); ok {
				return meta, nil
			}
			if err := s.cache.LoadNegative(key, s.Now()); err != nil {
				return channel.Meta{}, err
			}
		}
		loaded, err := load(ctx)
		if err != nil {
			s.cache.StoreNegative(key, err, s.Now())
			return channel.Meta{}, err
		}
		applied, err := s.ApplyAuthoritativeMeta(loaded)
		if err != nil {
			return channel.Meta{}, err
		}
		s.cache.StorePositive(key, applied, s.Now())
		return applied, nil
	})
	if err != nil {
		return channel.Meta{}, err
	}
	return meta, nil
}

func (s *Sync) syncOnce(ctx context.Context) error {
	s.observeHashSlotTableVersion()
	if s.source == nil {
		return channel.ErrInvalidConfig
	}
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

func (s *Sync) applyLocalMeta(meta metadb.ChannelRuntimeMeta) (channel.Meta, error) {
	if !containsUint64(meta.Replicas, s.localNode) {
		return channel.Meta{}, channel.ErrStaleMeta
	}
	return s.ApplyAuthoritativeMeta(meta)
}

func (s *Sync) reconcileChannelRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (metadb.ChannelRuntimeMeta, error) {
	if s == nil || s.bootstrap == nil {
		return s.maybeRepairChannelRuntimeMeta(ctx, meta)
	}
	if s.repairer != nil && s.repairPolicy != nil {
		need, _ := s.repairPolicy(meta)
		if need {
			return s.maybeRepairChannelRuntimeMeta(ctx, meta)
		}
	}
	reconciled, _, err := s.bootstrap.RenewChannelLeaderLease(ctx, meta, s.localNode, s.leaseRenewLeadTime())
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return s.maybeRepairChannelRuntimeMeta(ctx, reconciled)
}

func (s *Sync) leaseRenewLeadTime() time.Duration {
	if s == nil || s.refreshInterval <= 0 {
		return time.Second
	}
	return s.refreshInterval
}

func (s *Sync) maybeRepairChannelRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (metadb.ChannelRuntimeMeta, error) {
	if s == nil || s.repairer == nil || s.repairPolicy == nil {
		return meta, nil
	}
	if meta.Leader != 0 {
		s.WarmNodeLiveness(ctx, meta.Leader)
	}
	need, reason := s.repairPolicy(meta)
	if !need {
		return meta, nil
	}
	repaired, _, err := s.repairer.RepairIfNeeded(ctx, meta, reason)
	if err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	return repaired, nil
}

func (s *Sync) observeHashSlotTableVersion() {
	if s == nil || s.source == nil {
		return
	}
	versionSource, ok := s.source.(HashSlotTableVersionSource)
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
		s.cache.Clear()
	}
}

func (s *Sync) watchActiveSlotLeaders(ctx context.Context, interval time.Duration, done chan struct{}) {
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

func (s *Sync) watchLocalReplicaStateChanges(ctx context.Context) {
	if s == nil {
		return
	}
	defer s.refreshWG.Done()
	s.mu.Lock()
	stateChanges := s.stateChanges
	s.mu.Unlock()
	WatchLocalReplicaStateChanges(ctx, stateChanges, s.observeLocalReplicaStateChange)
}

func (s *Sync) observeLocalReplicaStateChange(key channel.ChannelKey) {
	if s == nil || s.runtime == nil {
		return
	}
	ObserveLocalReplicaStateChange(LocalReplicaStateChange{
		Key:     key,
		Runtime: s.runtime,
		Track: func(key channel.ChannelKey) {
			s.mu.Lock()
			s.trackAppliedLocalKeyLocked(key)
			s.mu.Unlock()
		},
		Untrack: func(key channel.ChannelKey) {
			s.mu.Lock()
			s.untrackAppliedLocalKeyLocked(key)
			s.mu.Unlock()
		},
		SlotForKey:          s.slotForChannelKey,
		ScheduleSlotRefresh: s.ScheduleSlotLeaderRefresh,
	})
}

// ProjectChannelMeta converts authoritative slot metadata into channel runtime metadata.
func ProjectChannelMeta(meta metadb.ChannelRuntimeMeta) channel.Meta {
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

func (s *Sync) removeLocalRuntime(key channel.ChannelKey) error {
	if s.runtime == nil {
		return nil
	}
	if err := s.runtime.RemoveLocalRuntime(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.untrackAppliedLocalKeyLocked(key)
	return nil
}

func (s *Sync) cleanupAppliedLocal() error {
	var err error
	for key := range s.snapshotAppliedLocal() {
		err = errors.Join(err, s.removeLocalRuntime(key))
	}
	s.mu.Lock()
	s.resetAppliedLocalTrackingLocked()
	s.mu.Unlock()
	return err
}

func (s *Sync) snapshotAppliedLocal() map[channel.ChannelKey]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAppliedLocalSet(s.appliedLocal)
}

func (s *Sync) setAppliedLocal(values map[channel.ChannelKey]struct{}) {
	s.mu.Lock()
	s.resetAppliedLocalTrackingLocked()
	for key := range values {
		s.trackAppliedLocalKeyLocked(key)
	}
	s.mu.Unlock()
}

func (s *Sync) snapshotAppliedSlots() []multiraft.SlotID {
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

func (s *Sync) pollActiveSlotLeaders() {
	if s == nil || s.cluster == nil {
		return
	}
	for _, slotID := range s.snapshotAppliedSlots() {
		leader, err := s.cluster.LeaderOf(slotID)
		if err != nil || leader == 0 {
			continue
		}
		if !s.updateObservedSlotLeader(slotID, leader) {
			continue
		}
		s.ScheduleSlotLeaderRefresh(slotID)
	}
}

func (s *Sync) updateObservedSlotLeader(slotID multiraft.SlotID, leader multiraft.NodeID) bool {
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

func (s *Sync) trackAppliedLocalKeyLocked(key channel.ChannelKey) {
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

func (s *Sync) untrackAppliedLocalKeyLocked(key channel.ChannelKey) {
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

func (s *Sync) resetAppliedLocalTrackingLocked() {
	if s == nil {
		return
	}
	s.appliedLocal = nil
	s.appliedSlots = nil
	s.slotLeaders = nil
}

func (s *Sync) slotForChannelKey(key channel.ChannelKey) (multiraft.SlotID, bool) {
	if s == nil || s.cluster == nil {
		return 0, false
	}
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return 0, false
	}
	return s.cluster.SlotForKey(id.ID), true
}

func (s *Sync) snapshotAppliedLocalKeysForSlot(slotID multiraft.SlotID) []channel.ChannelKey {
	if s == nil || s.cluster == nil {
		return nil
	}
	applied := s.snapshotAppliedLocal()
	if len(applied) == 0 {
		return nil
	}
	keys := make([]channel.ChannelKey, 0, len(applied))
	for key := range applied {
		id, err := channelhandler.ParseChannelKey(key)
		if err != nil {
			continue
		}
		if s.cluster.SlotForKey(id.ID) != slotID {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func (s *Sync) scheduleLeaderHealthRefresh(nodeID uint64) {
	if s == nil || nodeID == 0 {
		return
	}
	applied := s.snapshotAppliedLocal()
	if len(applied) == 0 {
		return
	}
	affectedSlots := make(map[multiraft.SlotID]struct{})
	for key := range applied {
		if s.runtime != nil {
			handle, ok := s.runtime.Channel(key)
			if !ok || uint64(handle.Meta().Leader) != nodeID {
				continue
			}
		}
		slotID, ok := s.slotForChannelKey(key)
		if !ok {
			continue
		}
		affectedSlots[slotID] = struct{}{}
	}
	for slotID := range affectedSlots {
		s.ScheduleSlotLeaderRefresh(slotID)
	}
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
