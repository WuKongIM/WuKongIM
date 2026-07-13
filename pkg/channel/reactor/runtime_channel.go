package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

const (
	minColdActivationTimeout = 100 * time.Millisecond
	maxColdActivationTimeout = 5 * time.Second
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
	previousRole := rc.state.Role
	decision := rc.state.ApplyMeta(meta)
	if decision.Err != nil {
		return decision.Err
	}
	r.applyLoadedMetaDecision(rc, fencePendingState)
	r.observeFollowerParkedCountIfChanged(wasParked, rc)
	r.updateActiveRuntimeRole(previousRole, rc.state.Role)
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
	if loading.kind == storeLoadColdActivation {
		r.handleApplyMetaToColdActivation(event, rc, loading, meta)
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

// handleApplyMetaToColdActivation lets explicit authoritative metadata bypass an in-flight cold resolve without extending its deadline.
func (r *Reactor) handleApplyMetaToColdActivation(event Event, rc *runtimeChannel, loading *storeLoadState, meta ch.Meta) {
	if meta.Key != loading.key || meta.ID != loading.id {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if loading.coldPhase == coldActivationStoreLoad {
		cmp := compareLoadingMetaFence(loading.meta, meta)
		if cmp > 0 {
			event.Future.Complete(Result{Err: ch.ErrStaleMeta})
			return
		}
		if cmp < 0 {
			r.completeStoreLoadFutures(loading, Result{Err: ch.ErrStaleMeta})
			loading.meta = meta
		}
		loading.futures = append(loading.futures, event.Future)
		return
	}
	if loading.cancel != nil {
		loading.cancel()
	}
	ctx, cancel := context.WithDeadline(context.Background(), loading.deadline)
	loading.context = ctx
	loading.cancel = cancel
	loading.meta = meta
	loading.coldPhase = coldActivationStoreLoad
	loading.futures = append(loading.futures, event.Future)
	loading.opID = r.nextOpID()
	fence := ch.Fence{ChannelKey: loading.key, Generation: loading.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: loading.opID}
	if err := r.submitColdStoreLoad(ctx, loading.id, fence); err != nil {
		r.releaseColdActivation(loading.key, rc, err)
	}
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
		if loading.kind == storeLoadColdActivation {
			r.releaseColdActivation(loading.key, rc, result.Err)
			return
		}
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
	case storeLoadColdActivation:
		r.completeColdActivationStoreLoad(rc, loading, result.StoreLoad)
	default:
		delete(r.channels, loading.key)
		r.closeStoreAsync(loading.key, loading.generation, result.StoreLoad.Store)
		r.completeStoreLoadFutures(loading, Result{Err: ch.ErrInvalidConfig})
	}
}

// completeColdActivationStoreLoad transfers the authority-proven store handle into the ordinary ApplyMeta activation path.
func (r *Reactor) completeColdActivationStoreLoad(rc *runtimeChannel, loading *storeLoadState, loaded *worker.StoreLoadResult) {
	r.due.remove(dueColdActivation, loading.key)
	if loading.cancel != nil {
		loading.cancel()
		loading.cancel = nil
	}
	loading.context = nil
	loading.kind = storeLoadApplyMeta
	r.completeApplyMetaStoreLoad(rc, loading, loaded)
}

func (r *Reactor) completeApplyMetaStoreLoad(rc *runtimeChannel, loading *storeLoadState, loaded *worker.StoreLoadResult) {
	state := machine.NewChannelState(loading.key, r.cfg.LocalNode, loading.generation)
	state.ID = loading.meta.ID
	state.LEO = loaded.Initial.LEO
	state.HW = loaded.Initial.HW
	state.CheckpointHW = loaded.Initial.CheckpointHW
	applyLoadedRetentionState(state, loaded.Retention)
	decision := state.ApplyMeta(loading.meta)
	if decision.Err != nil {
		if r.channels[loading.key] == rc {
			rc.loading = nil
			delete(r.channels, loading.key)
		}
		r.closeStoreAsync(loading.key, loading.generation, loaded.Store)
		r.completeStoreLoadFutures(loading, Result{Err: decision.Err})
		return
	}
	rc.state = state
	rc.store = loaded.Store
	rc.loading = nil
	r.resetLoadedRuntimeStructures(rc, time.Now(), loaded.Initial.LEO)
	r.applyLoadedMetaDecision(rc, false)
	r.updateActiveRuntimeRole(0, state.Role)
	r.observeChannelRuntimeLoaded(loading.key)
	r.completeStoreLoadFutures(loading, Result{})
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

// startColdActivation admits an unloaded PullHint into the authority-first cold activation lifecycle.
func (r *Reactor) startColdActivation(req transport.PullHintRequest) error {
	if err := validatePendingPullHint(req, r.cfg.LocalNode); err != nil {
		return err
	}
	if rc := r.channels[req.ChannelKey]; rc != nil {
		if rc.loading != nil && rc.state == nil && rc.pending == nil {
			return r.coalesceColdActivationLoad(rc, req)
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
	if r.cfg.Pools == nil || r.cfg.Pools.ColdActivation == nil {
		return ch.ErrNotReady
	}
	opID := r.nextOpID()
	generation := uint64(opID)
	fence := ch.Fence{ChannelKey: req.ChannelKey, Generation: generation, Epoch: req.Epoch, LeaderEpoch: req.LeaderEpoch, OpID: opID}
	timeout := r.coldActivationTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	deadline, _ := ctx.Deadline()
	rc := &runtimeChannel{loading: &storeLoadState{
		kind:       storeLoadColdActivation,
		key:        req.ChannelKey,
		id:         req.ChannelID,
		generation: generation,
		opID:       opID,
		pullHint:   req,
		coldPhase:  coldActivationResolve,
		deadline:   deadline,
		context:    ctx,
		cancel:     cancel,
	}}
	r.channels[req.ChannelKey] = rc
	r.scheduleColdActivationDeadline(rc.loading)
	if err := r.submitColdMetaResolve(ctx, fence, req.ChannelID); err != nil {
		r.releaseColdActivation(req.ChannelKey, rc, err)
		return err
	}
	return nil
}

// coldActivationTimeout gives queued authority resolution and store load several hint intervals while bounding retained shells.
func (r *Reactor) coldActivationTimeout() time.Duration {
	interval := r.cfg.PullHintRetryInterval
	if interval <= 0 {
		interval = time.Second
	}
	timeout := maxColdActivationTimeout
	if interval <= maxColdActivationTimeout/5 {
		timeout = interval * 5
	}
	if timeout < minColdActivationTimeout {
		return minColdActivationTimeout
	}
	if timeout > maxColdActivationTimeout {
		return maxColdActivationTimeout
	}
	return timeout
}

// coalesceColdActivationLoad merges repeated wakeups without extending the original activation deadline.
func (r *Reactor) coalesceColdActivationLoad(rc *runtimeChannel, req transport.PullHintRequest) error {
	loading := rc.loading
	if loading == nil || loading.kind != storeLoadColdActivation {
		return ch.ErrStaleMeta
	}
	if req.ChannelKey != loading.key || req.ChannelID != loading.id {
		return ch.ErrStaleMeta
	}
	cmp := compareColdActivationHintFence(loading.pullHint, req)
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

// handleColdMetaResolveResult validates authority and opens storage only after local replica membership is proven.
func (r *Reactor) handleColdMetaResolveResult(result worker.Result) {
	rc := r.channels[result.Fence.ChannelKey]
	if rc == nil || rc.loading == nil || rc.state != nil || rc.pending != nil {
		return
	}
	loading := rc.loading
	if loading.kind != storeLoadColdActivation || loading.coldPhase != coldActivationResolve ||
		result.Fence.Generation != loading.generation || result.Fence.OpID != loading.opID {
		return
	}
	if result.Err != nil {
		r.releaseColdActivation(loading.key, rc, result.Err)
		return
	}
	if result.MetaResolve == nil {
		r.releaseColdActivation(loading.key, rc, ch.ErrInvalidConfig)
		return
	}
	meta, err := validateColdActivationMeta(loading, result.MetaResolve.Meta, r.cfg.LocalNode)
	if err != nil {
		r.releaseColdActivation(loading.key, rc, err)
		return
	}
	loading.meta = meta
	loading.coldPhase = coldActivationStoreLoad
	loading.opID = r.nextOpID()
	fence := ch.Fence{ChannelKey: loading.key, Generation: loading.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: loading.opID}
	if err := r.submitColdStoreLoad(loading.context, loading.id, fence); err != nil {
		r.releaseColdActivation(loading.key, rc, err)
	}
}

// validateColdActivationMeta proves the resolved identity, active topology, and local replica membership.
func validateColdActivationMeta(loading *storeLoadState, meta ch.Meta, local ch.NodeID) (ch.Meta, error) {
	if loading == nil {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	if err := validatePendingMetaShape(meta); err != nil {
		return ch.Meta{}, err
	}
	if meta.Key != loading.key || meta.ID != loading.id {
		return ch.Meta{}, ch.ErrStaleMeta
	}
	if err := validateActiveMetaTopology(meta, local); err != nil {
		return ch.Meta{}, err
	}
	return meta, nil
}

func (r *Reactor) scheduleColdActivationDeadline(loading *storeLoadState) {
	if r == nil || loading == nil || loading.deadline.IsZero() {
		return
	}
	r.due.push(dueItem{key: loading.key, kind: dueColdActivation, due: loading.deadline, version: loading.generation})
}

func (r *Reactor) releaseExpiredColdActivation(key ch.ChannelKey, version uint64, now time.Time) {
	rc := r.channels[key]
	if rc == nil || rc.loading == nil || rc.loading.kind != storeLoadColdActivation ||
		rc.loading.generation != version || rc.loading.deadline.IsZero() || now.Before(rc.loading.deadline) {
		return
	}
	r.releaseColdActivation(key, rc, context.DeadlineExceeded)
}

// releaseColdActivation cancels outstanding work and returns the shell's channel-capacity lease.
func (r *Reactor) releaseColdActivation(key ch.ChannelKey, rc *runtimeChannel, err error) {
	if r == nil || rc == nil || rc.loading == nil || rc.loading.kind != storeLoadColdActivation || r.channels[key] != rc {
		return
	}
	loading := rc.loading
	r.due.remove(dueColdActivation, key)
	if loading.cancel != nil {
		loading.cancel()
		loading.cancel = nil
	}
	loading.context = nil
	r.completeStoreLoadFutures(loading, Result{Err: err})
	delete(r.channels, key)
	r.observeActivationRejected(coldActivationRejectionReason(err))
}

func coldActivationRejectionReason(err error) string {
	switch {
	case errors.Is(err, ch.ErrNotReplica):
		return "cold_not_replica"
	case errors.Is(err, ch.ErrStaleMeta):
		return "cold_stale_meta"
	case errors.Is(err, ch.ErrInvalidConfig):
		return "cold_invalid_config"
	case errors.Is(err, ch.ErrNotReady):
		return "cold_not_ready"
	case errors.Is(err, context.DeadlineExceeded):
		return "cold_deadline"
	case errors.Is(err, context.Canceled):
		return "cold_canceled"
	default:
		return "cold_dependency"
	}
}

func compareColdActivationHintFence(current transport.PullHintRequest, next transport.PullHintRequest) int {
	if next.Epoch < current.Epoch || (next.Epoch == current.Epoch && next.LeaderEpoch < current.LeaderEpoch) {
		return 1
	}
	if next.Epoch == current.Epoch && next.LeaderEpoch == current.LeaderEpoch {
		return 0
	}
	return -1
}

// closeStoreAsync transfers one detached handle either to an accepted worker task
// or, when admission fails, to the shutdown-tracked fallback runtime.
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
	if r != nil && r.storeCloses != nil && r.storeCloses.start(func() { _ = cs.Close() }) {
		return
	}
	_ = cs.Close()
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
	if r == nil || r.cfg.Store == nil {
		return nil, loadedChannelStoreState{}, ch.ErrInvalidConfig
	}
	cs, err := r.cfg.Store.ChannelStore(key, id)
	if err != nil {
		return nil, loadedChannelStoreState{}, err
	}
	if cs == nil {
		return nil, loadedChannelStoreState{}, ch.ErrInvalidConfig
	}
	owned := true
	defer func() {
		if owned {
			_ = cs.Close()
		}
	}()
	initial, err := cs.Load(context.Background())
	if err != nil {
		return nil, loadedChannelStoreState{}, err
	}
	retention, err := cs.LoadRetentionState(context.Background())
	if err != nil {
		return nil, loadedChannelStoreState{}, err
	}
	owned = false
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
	r.updateActiveRuntimeRole(0, state.Role)
	return nil
}

func (r *Reactor) releasePendingMeta(key ch.ChannelKey, rc *runtimeChannel, err error) {
	detached, ok := r.detachPendingMeta(key, rc, err)
	if !ok {
		return
	}
	r.closeStoreAsync(detached.key, detached.generation, detached.store)
}

func (r *Reactor) detachPendingMeta(key ch.ChannelKey, rc *runtimeChannel, err error) (detachedStoreHandle, bool) {
	if r == nil || rc == nil || rc.pending == nil || r.channels[key] != rc {
		return detachedStoreHandle{}, false
	}
	detached := detachedStoreHandle{key: key, generation: rc.pending.generation, store: rc.store}
	rc.store = nil
	delete(r.channels, key)
	if r.pendingMetaCount > 0 {
		r.pendingMetaCount--
	}
	r.observePendingMeta("released", err)
	r.observePendingMetaCount()
	return detached, true
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
	r.observeRuntimeCount(ch.RoleLeader, r.activeLeaderRuntimeCount)
	r.observeRuntimeCount(ch.RoleFollower, r.activeFollowerRuntimeCount)
}

// updateActiveRuntimeRole updates the reactor-local runtime counters after a
// loaded Channel runtime is added, removed, or changes role.
func (r *Reactor) updateActiveRuntimeRole(previous ch.Role, current ch.Role) {
	if r == nil || previous == current {
		return
	}
	switch previous {
	case ch.RoleLeader:
		if r.activeLeaderRuntimeCount > 0 {
			r.activeLeaderRuntimeCount--
		}
	case ch.RoleFollower:
		if r.activeFollowerRuntimeCount > 0 {
			r.activeFollowerRuntimeCount--
		}
	}
	switch current {
	case ch.RoleLeader:
		r.activeLeaderRuntimeCount++
	case ch.RoleFollower:
		r.activeFollowerRuntimeCount++
	}
	r.observeRuntimeCounts()
}

func (r *Reactor) lookupLoadedChannel(key ch.ChannelKey) (*runtimeChannel, error) {
	if rc := r.channels[key]; rc != nil && rc.state != nil && rc.pending == nil {
		return rc, nil
	}
	return nil, ch.ErrChannelNotFound
}
