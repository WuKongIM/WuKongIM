package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

func (r *Reactor) handleApplyMeta(event Event) {
	key := event.Meta.Key
	if key == "" {
		key = ch.ChannelKeyForID(event.Meta.ID)
	}
	if existing := r.channels[key]; existing != nil && existing.loading != nil && existing.state == nil && existing.pending == nil {
		r.handleApplyMetaToLoading(event, existing)
		return
	}
	if existing := r.channels[key]; existing != nil && existing.pending != nil && existing.state == nil {
		r.handleApplyMetaToPending(event, existing)
		return
	}
	existing := r.channels[key]
	if existing == nil && r.shouldAsyncStoreLoad() {
		r.startApplyMetaLoad(event)
		return
	}
	rc, err := r.ensureChannel(event.Meta)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	fencePendingState := existing != nil && metadataWouldFenceState(rc.state, event.Meta)
	event.Future.Complete(Result{Err: r.applyLoadedRuntimeMeta(rc, event.Meta, fencePendingState)})
}

func (r *Reactor) applyLoadedRuntimeMeta(rc *runtimeChannel, meta ch.Meta, fencePendingState bool) error {
	if r == nil || rc == nil || rc.state == nil {
		return ch.ErrChannelNotFound
	}
	if err := rc.state.ValidateMeta(meta); err != nil {
		return err
	}
	wasParked := rc.replication.parked
	if fencePendingState {
		r.clearFencedRuntimeWork(rc, ch.ErrStaleMeta)
	}
	decision := rc.state.ApplyMeta(meta)
	if decision.Err != nil {
		return decision.Err
	}
	r.applyLoadedMetaDecision(rc, fencePendingState)
	r.observeFollowerParkedCountIfChanged(wasParked, rc)
	r.observeRuntimeCounts()
	return nil
}

func (r *Reactor) shouldAsyncStoreLoad() bool {
	return r != nil && r.cfg.Pools != nil && r.asyncEffects.Load()
}

func (r *Reactor) startApplyMetaLoad(event Event) {
	if event.Future == nil {
		event.Future = NewFuture()
	}
	meta := event.Meta
	key := meta.Key
	if key == "" {
		key = ch.ChannelKeyForID(meta.ID)
		meta.Key = key
	}
	if r.cfg.MaxChannelsEnabled && len(r.channels) >= r.cfg.MaxChannels {
		r.observeActivationRejected("max_channels")
		event.Future.Complete(Result{Err: ch.ErrTooManyChannels})
		return
	}
	opID := r.nextOpID()
	generation := uint64(opID)
	fence := ch.Fence{ChannelKey: key, Generation: generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: opID}
	rc := &runtimeChannel{loading: &storeLoadState{
		kind:       storeLoadApplyMeta,
		key:        key,
		id:         meta.ID,
		generation: generation,
		opID:       opID,
		meta:       meta,
		futures:    []*Future{event.Future},
	}}
	r.channels[key] = rc
	if err := r.submitStoreLoad(context.Background(), meta.ID, fence); err != nil {
		delete(r.channels, key)
		r.completeStoreLoadFutures(rc.loading, Result{Err: err})
	}
}

func (r *Reactor) handleApplyMetaToLoading(event Event, rc *runtimeChannel) {
	if event.Future == nil {
		event.Future = NewFuture()
	}
	meta := event.Meta
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	loading := rc.loading
	if loading == nil {
		event.Future.Complete(Result{Err: ch.ErrChannelNotFound})
		return
	}
	if meta.Key != loading.key || meta.ID != loading.id {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	cmp := compareLoadingMetaFence(loading.meta, meta)
	if cmp > 0 {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if cmp < 0 {
		r.completeStoreLoadFutures(loading, Result{Err: ch.ErrStaleMeta})
		loading.futures = nil
		loading.meta = meta
	}
	loading.futures = append(loading.futures, event.Future)
}

func compareLoadingMetaFence(current ch.Meta, next ch.Meta) int {
	if next.Epoch < current.Epoch || (next.Epoch == current.Epoch && next.LeaderEpoch < current.LeaderEpoch) {
		return 1
	}
	if next.Epoch == current.Epoch && next.LeaderEpoch == current.LeaderEpoch {
		return 0
	}
	return -1
}

func (r *Reactor) handleStoreLoadResult(result worker.Result) {
	rc := r.channels[result.Fence.ChannelKey]
	if rc == nil || rc.loading == nil || rc.state != nil || rc.pending != nil {
		r.closeRejectedStoreLoad(result)
		return
	}
	loading := rc.loading
	if result.Fence.Generation != loading.generation || result.Fence.OpID != loading.opID {
		r.closeRejectedStoreLoad(result)
		return
	}
	if result.Err != nil {
		delete(r.channels, loading.key)
		r.completeStoreLoadFutures(loading, Result{Err: result.Err})
		return
	}
	if result.StoreLoad == nil || result.StoreLoad.Store == nil {
		delete(r.channels, loading.key)
		r.completeStoreLoadFutures(loading, Result{Err: ch.ErrInvalidConfig})
		return
	}
	switch loading.kind {
	case storeLoadApplyMeta:
		r.completeApplyMetaStoreLoad(rc, loading, result.StoreLoad)
	case storeLoadPendingMeta:
		r.completePendingMetaStoreLoad(rc, loading, result.StoreLoad)
	default:
		delete(r.channels, loading.key)
		r.closeStoreAsync(loading.key, loading.generation, result.StoreLoad.Store)
		r.completeStoreLoadFutures(loading, Result{Err: ch.ErrInvalidConfig})
	}
}

func (r *Reactor) completeApplyMetaStoreLoad(rc *runtimeChannel, loading *storeLoadState, loaded *worker.StoreLoadResult) {
	state := machine.NewChannelState(loading.key, r.cfg.LocalNode, loading.generation)
	state.ID = loading.meta.ID
	state.LEO = loaded.Initial.LEO
	state.HW = loaded.Initial.HW
	state.CheckpointHW = loaded.Initial.CheckpointHW
	applyLoadedRetentionState(state, loaded.Retention)
	rc.state = state
	rc.store = loaded.Store
	rc.loading = nil
	r.resetLoadedRuntimeStructures(rc, time.Now(), loaded.Initial.LEO)
	decision := rc.state.ApplyMeta(loading.meta)
	if decision.Err == nil {
		r.applyLoadedMetaDecision(rc, false)
		r.observeRuntimeCounts()
		r.observeChannelRuntimeLoaded(loading.key)
	}
	r.completeStoreLoadFutures(loading, Result{Err: decision.Err})
}

func (r *Reactor) completeStoreLoadFutures(loading *storeLoadState, result Result) {
	if loading == nil {
		return
	}
	for _, future := range loading.futures {
		if future != nil {
			future.Complete(result)
		}
	}
	loading.futures = nil
}

func (r *Reactor) closeRejectedStoreLoad(result worker.Result) {
	if result.StoreLoad != nil && result.StoreLoad.Store != nil {
		r.closeStoreAsync(result.Fence.ChannelKey, result.Fence.Generation, result.StoreLoad.Store)
	}
}

func (r *Reactor) startPendingMetaLoad(req transport.PullHintRequest) error {
	if err := validatePendingPullHint(req, r.cfg.LocalNode); err != nil {
		return err
	}
	if rc := r.channels[req.ChannelKey]; rc != nil {
		if rc.loading != nil && rc.state == nil && rc.pending == nil {
			return r.coalescePendingMetaLoad(rc, req)
		}
		if rc.pending != nil && rc.state == nil {
			ensured, err := r.ensurePendingMeta(req)
			if err != nil {
				return err
			}
			return r.submitPendingMetaPull(ensured, time.Now())
		}
		return ch.ErrStaleMeta
	}
	if r.cfg.MaxChannelsEnabled && len(r.channels) >= r.cfg.MaxChannels {
		r.observeActivationRejected("max_channels")
		return ch.ErrTooManyChannels
	}
	opID := r.nextOpID()
	generation := uint64(opID)
	fence := ch.Fence{ChannelKey: req.ChannelKey, Generation: generation, Epoch: req.Epoch, LeaderEpoch: req.LeaderEpoch, OpID: opID}
	rc := &runtimeChannel{loading: &storeLoadState{
		kind:       storeLoadPendingMeta,
		key:        req.ChannelKey,
		id:         req.ChannelID,
		generation: generation,
		opID:       opID,
		pullHint:   req,
	}}
	r.channels[req.ChannelKey] = rc
	if err := r.submitStoreLoad(context.Background(), req.ChannelID, fence); err != nil {
		delete(r.channels, req.ChannelKey)
		return err
	}
	return nil
}

func (r *Reactor) coalescePendingMetaLoad(rc *runtimeChannel, req transport.PullHintRequest) error {
	loading := rc.loading
	if loading == nil || loading.kind != storeLoadPendingMeta {
		return ch.ErrStaleMeta
	}
	if req.ChannelKey != loading.key || req.ChannelID != loading.id {
		return ch.ErrStaleMeta
	}
	cmp := comparePendingLoadFence(loading.pullHint, req)
	if cmp > 0 {
		return ch.ErrStaleMeta
	}
	if cmp < 0 {
		loading.pullHint = req
		return nil
	}
	if req.LeaderLEO > loading.pullHint.LeaderLEO {
		loading.pullHint.LeaderLEO = req.LeaderLEO
	}
	if req.ActivityVersion > loading.pullHint.ActivityVersion {
		loading.pullHint.ActivityVersion = req.ActivityVersion
	}
	return nil
}

func comparePendingLoadFence(current transport.PullHintRequest, next transport.PullHintRequest) int {
	if next.Epoch < current.Epoch || (next.Epoch == current.Epoch && next.LeaderEpoch < current.LeaderEpoch) {
		return 1
	}
	if next.Epoch == current.Epoch && next.LeaderEpoch == current.LeaderEpoch {
		return 0
	}
	return -1
}

func (r *Reactor) completePendingMetaStoreLoad(rc *runtimeChannel, loading *storeLoadState, loaded *worker.StoreLoadResult) {
	req := loading.pullHint
	now := time.Now()
	rc.store = loaded.Store
	rc.loading = nil
	rc.pending = &pendingMetaState{
		key:             req.ChannelKey,
		id:              req.ChannelID,
		generation:      loading.generation,
		epoch:           req.Epoch,
		leaderEpoch:     req.LeaderEpoch,
		leader:          req.Leader,
		leaderLEO:       req.LeaderLEO,
		activityVersion: req.ActivityVersion,
		deadline:        r.pendingMetaDeadline(now),
		initial: storeInitialState{
			LEO:                         loaded.Initial.LEO,
			HW:                          loaded.Initial.HW,
			CheckpointHW:                loaded.Initial.CheckpointHW,
			LocalRetentionThroughSeq:    loaded.Retention.LocalRetentionThroughSeq,
			PhysicalRetentionThroughSeq: loaded.Retention.PhysicalRetentionThroughSeq,
		},
	}
	r.pendingMetaCount++
	r.observePendingMeta("created", nil)
	r.observePendingMetaCount()
	r.schedulePendingMetaDeadline(rc)
	if err := r.submitPendingMetaPull(rc, now); err != nil {
		r.releasePendingMeta(req.ChannelKey, rc, err)
	}
}

func (r *Reactor) closeStoreAsync(key ch.ChannelKey, generation uint64, cs store.ChannelStore) {
	if cs == nil {
		return
	}
	if r != nil && r.cfg.Pools != nil && r.asyncEffects.Load() {
		opID := r.nextOpID()
		fence := ch.Fence{ChannelKey: key, Generation: generation, OpID: opID}
		if err := r.submitStoreClose(context.Background(), fence, cs); err == nil {
			return
		}
	}
	go func() { _ = cs.Close() }()
}

func (r *Reactor) clearFencedRuntimeWork(rc *runtimeChannel, err error) {
	if rc != nil && rc.state != nil {
		r.clearLoadedMetaRefresh(rc.state.Key)
	}
	r.failPendingAppendWaiters(rc, err)
	rc.failPendingPullWaiters(err)
	rc.failPendingLookupWaiters(err)
	rc.failPendingRetentionWaiters(err)
	r.clearPullCancelChannel(rc)
	r.clearLookupCancelChannel(rc)
	r.clearAppendCancelContexts(rc)
}

// metadataWouldFenceState reports whether accepted metadata invalidates pending state.
func metadataWouldFenceState(state *machine.ChannelState, meta ch.Meta) bool {
	if state == nil {
		return false
	}
	role := ch.RoleFollower
	if meta.Leader == state.LocalNode {
		role = ch.RoleLeader
	}
	return state.Epoch != meta.Epoch ||
		state.LeaderEpoch != meta.LeaderEpoch ||
		state.Leader != meta.Leader ||
		state.Role != role ||
		state.Status != meta.Status
}

func (r *Reactor) applyLoadedMetaDecision(rc *runtimeChannel, fencePendingState bool) {
	if fencePendingState {
		resetLeaderCheckpointLifecycle(rc)
		rc.lifecycle.cancelFollowerStop()
		rc.replication.reset()
		rc.recentRecords.reset()
		r.resetPullHintLifecycle(rc)
	}
	now := time.Now()
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
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.recentRecords.reset()
	rc.lifecycle.followers = nil
	rc.lifecycle.pullHintInflight = nil
	r.scheduleReplicationFromState(rc, now)
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
	_, err := r.lookupLoadedChannel(event.Key)
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
	cs, initial, err := r.loadChannelStore(key, meta.ID)
	if err != nil {
		return nil, err
	}
	state := machine.NewChannelState(key, r.cfg.LocalNode, 1)
	state.ID = meta.ID
	state.LEO = initial.LEO
	state.HW = initial.HW
	state.CheckpointHW = initial.CheckpointHW
	applyLoadedRetentionState(state, initial.Retention)
	rc := &runtimeChannel{state: state, store: cs}
	r.resetLoadedRuntimeStructures(rc, time.Now(), initial.LEO)
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
	cs, initial, err := r.loadChannelStore(req.ChannelKey, req.ChannelID)
	if err != nil {
		if cs != nil {
			_ = cs.Close()
		}
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
				LEO:                         initial.LEO,
				HW:                          initial.HW,
				CheckpointHW:                initial.CheckpointHW,
				LocalRetentionThroughSeq:    initial.Retention.LocalRetentionThroughSeq,
				PhysicalRetentionThroughSeq: initial.Retention.PhysicalRetentionThroughSeq,
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

type loadedChannelStoreState struct {
	store.InitialState
	Retention store.RetentionState
}

func (r *Reactor) loadChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, loadedChannelStoreState, error) {
	cs, err := r.cfg.Store.ChannelStore(key, id)
	if err != nil {
		return nil, loadedChannelStoreState{}, err
	}
	initial, err := cs.Load(context.Background())
	if err != nil {
		return cs, loadedChannelStoreState{}, err
	}
	retention, err := cs.LoadRetentionState(context.Background())
	if err != nil {
		return cs, loadedChannelStoreState{}, err
	}
	return cs, loadedChannelStoreState{InitialState: initial, Retention: retention}, nil
}

func (r *Reactor) resetLoadedRuntimeStructures(rc *runtimeChannel, loadedAt time.Time, activityLEO uint64) {
	rc.recentRecords = newRecentRecordCache(r.cfg.LeaderRecentRecordCacheSize, r.cfg.LeaderRecentRecordCacheBytes)
	rc.lifecycle = newChannelRuntimeLifecycle(loadedAt, activityLEO)
	rc.appendQ = r.newAppendQueue()
	r.observeAppendQueuePressure(rc)
}

func (r *Reactor) newAppendQueue() appendQueue {
	return newAppendQueue(appendQueueConfig{
		MaxRecords:      r.cfg.AppendBatchMaxRecords,
		MaxBytes:        r.cfg.AppendBatchMaxBytes,
		MaxWait:         r.cfg.AppendBatchMaxWait,
		AdaptiveFlush:   r.cfg.AppendBatchAdaptiveFlush,
		ColdMaxWait:     r.cfg.AppendBatchColdMaxWait,
		MaxPending:      r.cfg.AppendQueueMaxRequests,
		MaxPendingBytes: r.cfg.AppendQueueMaxBytes,
	})
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
	state.LocalRetentionThroughSeq = pending.initial.LocalRetentionThroughSeq
	state.PhysicalRetentionThroughSeq = pending.initial.PhysicalRetentionThroughSeq
	decision := state.ApplyMeta(meta)
	if decision.Err != nil {
		return decision.Err
	}
	now := time.Now()
	rc.state = state
	rc.pending = nil
	if r.pendingMetaCount > 0 {
		r.pendingMetaCount--
	}
	r.resetLoadedRuntimeStructures(rc, now, state.LEO)
	rc.replication.reset()
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
	storeHandle := rc.store
	generation := rc.pending.generation
	rc.store = nil
	delete(r.channels, key)
	r.closeStoreAsync(key, generation, storeHandle)
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

func (r *Reactor) lookupLoadedChannel(key ch.ChannelKey) (*runtimeChannel, error) {
	if rc := r.channels[key]; rc != nil && rc.state != nil && rc.pending == nil {
		return rc, nil
	}
	return nil, ch.ErrChannelNotFound
}
