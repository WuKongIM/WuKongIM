package runtime

import (
	"errors"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const runtimeShardCount = 64

type shard struct {
	mu       sync.RWMutex
	channels map[core.ChannelKey]*channel
}

type runtime struct {
	cfg                         Config
	shards                      [runtimeShardCount]shard
	tombstones                  *tombstoneManager
	replicaFactory              ReplicaFactory
	generationStore             GenerationStore
	scheduler                   *scheduler
	schedulerPopHook            func(core.ChannelKey)
	beforePeerSessionHook       func(Envelope)
	afterOutboundValidationHook func(Envelope)
	sessions                    peerSessionCache
	laneMu                      sync.Mutex
	lanes                       map[core.NodeID]*PeerLaneManager
	// laneDispatcher holds queued peer/lane work for the lane dispatcher worker.
	laneDispatcher *laneDispatchQueue
	// laneDispatcherWorker marks whether the background lane dispatcher is running.
	laneDispatcherWorker   atomic.Bool
	leaderLanes            *laneDirectory
	peerRequests           peerRequestState
	snapshots              snapshotState
	snapshotThrottle       snapshotThrottle
	requestID              atomic.Uint64
	sendCoordActive        atomic.Int32
	laneRetryMu            sync.Mutex
	laneRetry              map[PeerLaneKey]*laneRetryState
	replicationRetryMu     sync.Mutex
	replicationRetry       map[core.ChannelKey]map[core.NodeID]*replicationRetryState
	backpressureRetry      map[core.NodeID]*backpressureRetryState
	backpressureMu         sync.Mutex
	sendCoordMu            sync.Mutex
	syncDeliveryMu         sync.Mutex
	syncDeliveryDepth      int
	syncDeferredSends      []deferredEnvelope
	syncDeferredPeerDrains map[core.NodeID]struct{}
	schedulerDrainMu       sync.Mutex
	schedulerWorker        atomic.Bool
	closed                 atomic.Bool
	countMu                sync.Mutex
	channelCount           int
	cleanupStop            chan struct{}
	cleanupDone            chan struct{}
	cleanupOnce            sync.Once
}

func New(cfg Config) (Runtime, error) {
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}
	if cfg.LocalNode == 0 {
		return nil, ErrInvalidConfig
	}
	if cfg.ReplicaFactory == nil {
		return nil, ErrInvalidConfig
	}
	if cfg.GenerationStore == nil {
		return nil, ErrInvalidConfig
	}
	if cfg.Limits.MaxChannels < 0 || cfg.Limits.MaxFetchInflightPeer < 0 || cfg.Limits.MaxSnapshotInflight < 0 {
		return nil, ErrInvalidConfig
	}
	if cfg.Tombstones.TombstoneTTL <= 0 {
		return nil, ErrInvalidConfig
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.FollowerReplicationRetryInterval <= 0 {
		cfg.FollowerReplicationRetryInterval = defaultFollowerReplicationRetryDelay
	}
	if cfg.Transport == nil {
		cfg.Transport = &nopTransport{}
	}
	if cfg.PeerSessions == nil {
		cfg.PeerSessions = nopPeerSessionManager{}
	}

	r := &runtime{
		cfg:                    cfg,
		tombstones:             newTombstoneManager(),
		replicaFactory:         cfg.ReplicaFactory,
		generationStore:        cfg.GenerationStore,
		scheduler:              newScheduler(),
		sessions:               newPeerSessionCache(),
		lanes:                  make(map[core.NodeID]*PeerLaneManager),
		laneDispatcher:         newLaneDispatchQueue(),
		leaderLanes:            newLaneDirectory(),
		peerRequests:           newPeerRequestState(),
		snapshotThrottle:       newSnapshotThrottle(cfg.Limits.MaxRecoveryBytesPerSecond, time.Sleep),
		laneRetry:              make(map[PeerLaneKey]*laneRetryState),
		replicationRetry:       make(map[core.ChannelKey]map[core.NodeID]*replicationRetryState),
		backpressureRetry:      make(map[core.NodeID]*backpressureRetryState),
		syncDeferredPeerDrains: make(map[core.NodeID]struct{}),
		cleanupStop:            make(chan struct{}),
		cleanupDone:            make(chan struct{}),
	}
	for i := range r.shards {
		r.shards[i].channels = make(map[core.ChannelKey]*channel)
	}
	cfg.Transport.RegisterHandler(r.handleEnvelope)
	r.startTombstoneCleanup()
	return r, nil
}

func (r *runtime) EnsureChannel(meta core.Meta) error {
	shard := r.shardFor(meta.Key)
	shard.mu.Lock()

	if _, ok := shard.channels[meta.Key]; ok {
		shard.mu.Unlock()
		return ErrChannelExists
	}

	reserved := false
	if r.cfg.Limits.MaxChannels > 0 {
		if !r.tryReserveChannelSlot() {
			shard.mu.Unlock()
			return ErrTooManyChannels
		}
		reserved = true
	}

	generation, err := r.allocateGeneration(meta.Key)
	if err != nil {
		shard.mu.Unlock()
		if reserved {
			r.releaseChannelSlot()
		}
		return err
	}
	changes := newReplicaChangeNotifier()
	rep, err := r.replicaFactory.New(ChannelConfig{
		ChannelKey:           meta.Key,
		Generation:           generation,
		Meta:                 meta,
		OnReplicaStateChange: changes.notify,
	})
	if err != nil {
		shard.mu.Unlock()
		if reserved {
			r.releaseChannelSlot()
		}
		return err
	}
	if notifier, ok := rep.(interface{ SetLeaderLocalAppendNotifier(func()) }); ok {
		notifier.SetLeaderLocalAppendNotifier(func() {
			r.onChannelAppend(meta.Key)
		})
	}
	if notifier, ok := rep.(interface{ SetLeaderHWAdvanceNotifier(func()) }); ok {
		notifier.SetLeaderHWAdvanceNotifier(func() {
			go r.onChannelCommit(meta.Key)
		})
	}
	if err := applyReplicaMeta(rep, r.cfg.LocalNode, meta); err != nil {
		shard.mu.Unlock()
		if reserved {
			r.releaseChannelSlot()
		}
		closeErr := rep.Close()
		if closeErr != nil {
			return errors.Join(err, closeErr)
		}
		return err
	}

	ch := newChannel(meta.Key, generation, rep, meta, r.cfg.Now, r, r.onChannelAppend, changes)
	shard.channels[meta.Key] = ch
	shard.mu.Unlock()
	r.syncFollowerLaneMembership(nil, meta)
	r.syncLeaderLaneTargets(ch, meta)

	if meta.Leader != r.cfg.LocalNode {
		r.retryReplication(meta.Key, meta.Leader, true)
	} else if status := ch.Status(); !status.CommitReady {
		for _, peer := range r.activeReplicationPeers(meta) {
			r.retryReplication(meta.Key, peer, true)
		}
	}
	return nil
}

func (r *runtime) RemoveChannel(key core.ChannelKey) error {
	shard := r.shardFor(key)
	r.sendCoordMu.Lock()
	shard.mu.Lock()
	ch, ok := shard.channels[key]
	if !ok {
		shard.mu.Unlock()
		r.sendCoordMu.Unlock()
		return ErrChannelNotFound
	}
	if err := ch.replica.Tombstone(); err != nil {
		shard.mu.Unlock()
		r.sendCoordMu.Unlock()
		return err
	}
	previousMeta := ch.metaSnapshot()
	r.tombstones.add(key, ch.gen, r.cfg.Now().Add(r.cfg.Tombstones.TombstoneTTL))
	delete(shard.channels, key)
	shard.mu.Unlock()
	r.syncFollowerLaneMembership(&previousMeta, core.Meta{})
	r.syncLeaderLaneTargets(ch, core.Meta{})
	r.snapshots.removeWaiter(key)
	r.evictInvalidPeerSessions(r.activeReplicationPeers(previousMeta))
	r.sendCoordMu.Unlock()

	stopTimers(r.clearReplicationRetries(key, 0, false))
	for _, peer := range r.peerRequests.clearChannel(key) {
		r.drainPeerQueue(peer)
	}

	if r.cfg.Limits.MaxChannels > 0 {
		r.releaseChannelSlot()
	}
	return ch.replica.Close()
}

func (r *runtime) ApplyMeta(meta core.Meta) error {
	ch, ok := r.lookupChannel(meta.Key)
	if !ok {
		return ErrChannelNotFound
	}
	previousMeta := ch.metaSnapshot()
	queueLeaderProbe := false
	if shouldSkipReplicaApplyMeta(ch, r.cfg.LocalNode, meta) {
		r.sendCoordMu.Lock()
		ch.setMeta(meta)
		r.sendCoordMu.Unlock()
		return nil
	}
	if err := applyReplicaMeta(ch.replica, r.cfg.LocalNode, meta); err != nil {
		return err
	}
	invalidatedPeers := r.invalidatedReplicationPeers(previousMeta, meta)
	r.sendCoordMu.Lock()
	ch.setMeta(meta)
	if snapshotWorkInvalidated(previousMeta, meta) {
		ch.clearSnapshotWork()
		r.snapshots.removeWaiter(meta.Key)
	}
	r.syncFollowerLaneMembership(&previousMeta, meta)
	r.syncLeaderLaneTargets(ch, meta)
	r.evictInvalidPeerSessions(invalidatedPeers)
	r.sendCoordMu.Unlock()
	r.clearInvalidPeerWork(ch, meta)
	stopTimers(r.clearStaleReplicationRetries(meta.Key, meta))
	if r.longPollEnabled() && meta.Leader == r.cfg.LocalNode {
		state := ch.Status()
		for _, peer := range r.activeReplicationPeers(meta) {
			if !r.shouldSendLeaderProbe(ch, meta, state, peer) {
				continue
			}
			ch.enqueueReplication(peer)
			queueLeaderProbe = true
		}
		if queueLeaderProbe {
			ch.markReplication()
		}
	}
	if meta.Leader != r.cfg.LocalNode {
		r.retryReplication(meta.Key, meta.Leader, true)
	} else if status := ch.Status(); !status.CommitReady {
		for _, peer := range r.activeReplicationPeers(meta) {
			r.retryReplication(meta.Key, peer, true)
		}
	}
	if queueLeaderProbe {
		r.enqueueScheduler(meta.Key, PriorityNormal)
	}
	return nil
}

func (r *runtime) clearInvalidPeerWork(ch *channel, meta core.Meta) {
	if ch == nil {
		return
	}
	allow := func(peer core.NodeID) bool {
		return r.isReplicationPeerValid(meta, peer)
	}
	ch.clearInvalidReplicationPeers(allow)
	for _, peer := range r.peerRequests.clearChannelInvalidPeers(meta.Key, allow) {
		r.drainPeerQueue(peer)
	}
}

func (r *runtime) invalidatedReplicationPeers(previous, next core.Meta) []core.NodeID {
	candidates := make(map[core.NodeID]struct{}, len(previous.Replicas)+1)
	if previous.Leader != 0 {
		candidates[previous.Leader] = struct{}{}
	}
	for _, peer := range previous.Replicas {
		if peer == 0 {
			continue
		}
		candidates[peer] = struct{}{}
	}
	invalid := make([]core.NodeID, 0, len(candidates))
	for peer := range candidates {
		if !r.isReplicationPeerValid(previous, peer) {
			continue
		}
		if r.isReplicationPeerValid(next, peer) {
			continue
		}
		invalid = append(invalid, peer)
	}
	return invalid
}

func (r *runtime) activeReplicationPeers(meta core.Meta) []core.NodeID {
	candidates := make(map[core.NodeID]struct{}, len(meta.Replicas)+1)
	if meta.Leader != 0 {
		candidates[meta.Leader] = struct{}{}
	}
	for _, peer := range meta.Replicas {
		if peer == 0 {
			continue
		}
		candidates[peer] = struct{}{}
	}
	active := make([]core.NodeID, 0, len(candidates))
	for peer := range candidates {
		if !r.isReplicationPeerValid(meta, peer) {
			continue
		}
		active = append(active, peer)
	}
	return active
}

func (r *runtime) evictInvalidPeerSessions(peers []core.NodeID) {
	if len(peers) == 0 {
		return
	}
	seen := make(map[core.NodeID]struct{}, len(peers))
	for _, peer := range peers {
		if peer == 0 || peer == r.cfg.LocalNode {
			continue
		}
		if _, exists := seen[peer]; exists {
			continue
		}
		seen[peer] = struct{}{}
		if r.peerReferencedByAnyChannel(peer) {
			continue
		}
		r.cfg.Logger.Warn("follower lane manager evicted after peer invalidation",
			wklog.Event("repl.diag.lane_manager_evict"),
			wklog.Uint64("peer", uint64(peer)),
		)
		r.deleteLaneManager(peer)
		session, ok := r.sessions.evict(peer)
		if !ok {
			continue
		}
		_ = session.Close()
	}
}

func (r *runtime) peerReferencedByAnyChannel(peer core.NodeID) bool {
	if peer == 0 || peer == r.cfg.LocalNode {
		return false
	}
	for i := range r.shards {
		shard := &r.shards[i]
		shard.mu.RLock()
		for _, ch := range shard.channels {
			meta := ch.metaSnapshot()
			if r.isReplicationPeerValid(meta, peer) {
				shard.mu.RUnlock()
				return true
			}
		}
		shard.mu.RUnlock()
	}
	return false
}

func (r *runtime) Channel(key core.ChannelKey) (ChannelHandle, bool) {
	ch, ok := r.lookupChannel(key)
	if !ok {
		return nil, false
	}
	return ch, true
}

func (r *runtime) lookupChannel(key core.ChannelKey) (*channel, bool) {
	shard := r.shardFor(key)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	ch, ok := shard.channels[key]
	return ch, ok
}

func (r *runtime) allocateGeneration(key core.ChannelKey) (uint64, error) {
	current, err := r.generationStore.Load(key)
	if err != nil {
		return 0, err
	}
	next := current + 1
	if err := r.generationStore.Store(key, next); err != nil {
		return 0, err
	}
	return next, nil
}

func (r *runtime) shardFor(key core.ChannelKey) *shard {
	idx := shardIndex(key)
	return &r.shards[idx]
}

func shardIndex(key core.ChannelKey) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32() % runtimeShardCount
}

func (r *runtime) totalChannels() int {
	total := 0
	for i := range r.shards {
		r.shards[i].mu.RLock()
		total += len(r.shards[i].channels)
		r.shards[i].mu.RUnlock()
	}
	return total
}

func (r *runtime) tryReserveChannelSlot() bool {
	r.countMu.Lock()
	defer r.countMu.Unlock()

	if r.channelCount >= r.cfg.Limits.MaxChannels {
		return false
	}
	r.channelCount++
	return true
}

func (r *runtime) releaseChannelSlot() {
	r.countMu.Lock()
	defer r.countMu.Unlock()

	if r.channelCount > 0 {
		r.channelCount--
	}
}

func applyReplicaMeta(rep replica.Replica, localNode core.NodeID, meta core.Meta) error {
	state := rep.Status()

	var err error
	switch {
	case meta.Leader == localNode && state.Role != core.ReplicaRoleLeader:
		err = rep.BecomeLeader(meta)
	case meta.Leader != localNode && state.Role != core.ReplicaRoleFollower:
		err = rep.BecomeFollower(meta)
	default:
		err = rep.ApplyMeta(meta)
	}
	if err == core.ErrLeaseExpired {
		return nil
	}
	return err
}

func shouldSkipReplicaApplyMeta(ch *channel, localNode core.NodeID, next core.Meta) bool {
	if ch == nil {
		return false
	}
	current := ch.metaSnapshot()
	if !metaEqual(current, next) {
		return false
	}
	state := ch.Status()
	expectedRole := core.ReplicaRoleFollower
	if next.Leader == localNode {
		expectedRole = core.ReplicaRoleLeader
	}
	if state.Role != expectedRole {
		return false
	}
	return state.ChannelKey == next.Key && state.Epoch == next.Epoch && state.Leader == next.Leader
}

func metaEqual(a, b core.Meta) bool {
	if a.Key != b.Key ||
		a.Epoch != b.Epoch ||
		a.LeaderEpoch != b.LeaderEpoch ||
		a.Leader != b.Leader ||
		a.MinISR != b.MinISR ||
		a.Status != b.Status ||
		a.Features != b.Features ||
		!a.LeaseUntil.Equal(b.LeaseUntil) {
		return false
	}
	if len(a.Replicas) != len(b.Replicas) || len(a.ISR) != len(b.ISR) {
		return false
	}
	for i := range a.Replicas {
		if a.Replicas[i] != b.Replicas[i] {
			return false
		}
	}
	for i := range a.ISR {
		if a.ISR[i] != b.ISR[i] {
			return false
		}
	}
	return true
}

func snapshotWorkInvalidated(previous, next core.Meta) bool {
	if previous.Epoch != next.Epoch || previous.Leader != next.Leader {
		return true
	}
	if len(previous.Replicas) != len(next.Replicas) || len(previous.ISR) != len(next.ISR) {
		return true
	}
	for i := range previous.Replicas {
		if previous.Replicas[i] != next.Replicas[i] {
			return true
		}
	}
	for i := range previous.ISR {
		if previous.ISR[i] != next.ISR[i] {
			return true
		}
	}
	return false
}

func (r *runtime) startTombstoneCleanup() {
	interval := r.cfg.Tombstones.CleanupInterval
	if interval <= 0 {
		interval = r.cfg.Tombstones.TombstoneTTL
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer close(r.cleanupDone)
		for {
			select {
			case <-ticker.C:
				r.tombstones.dropExpired(r.cfg.Now())
			case <-r.cleanupStop:
				return
			}
		}
	}()
}

func (r *runtime) stopTombstoneCleanup() {
	r.cleanupOnce.Do(func() {
		close(r.cleanupStop)
		<-r.cleanupDone
	})
}

func (r *runtime) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}

	r.stopTombstoneCleanup()
	stopTimers(r.clearAllReplicationRetries())
	stopTimers(r.clearAllLaneRetries())
	stopTimers(r.clearAllBackpressureRetries())
	r.peerRequests.clearAll()
	r.snapshots.clear()
	r.scheduler.clear()

	reps := make([]replica.Replica, 0)
	for i := range r.shards {
		shard := &r.shards[i]
		shard.mu.Lock()
		for key, ch := range shard.channels {
			reps = append(reps, ch.replica)
			delete(shard.channels, key)
		}
		shard.mu.Unlock()
	}

	r.countMu.Lock()
	r.channelCount = 0
	r.countMu.Unlock()

	sessions := make([]PeerSession, 0)
	r.sessions.mu.Lock()
	for peer, session := range r.sessions.sessions {
		sessions = append(sessions, session)
		delete(r.sessions.sessions, peer)
	}
	r.sessions.mu.Unlock()
	r.laneMu.Lock()
	r.lanes = make(map[core.NodeID]*PeerLaneManager)
	r.laneDispatcher = newLaneDispatchQueue()
	r.laneMu.Unlock()
	r.laneDispatcherWorker.Store(false)

	var err error
	for _, rep := range reps {
		err = errors.Join(err, rep.Close())
	}
	for _, session := range sessions {
		err = errors.Join(err, session.Close())
	}
	return err
}

func (r *runtime) isClosed() bool {
	return r.closed.Load()
}
