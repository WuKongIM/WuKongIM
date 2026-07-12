package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

func (r *Reactor) tickFollowerReplication(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive {
		return
	}
	defer r.scheduleReplicationFromState(rc, now)
	if rc.lifecycle.stage == lifecycleFollowerReadyToEvict {
		r.tryEvictStoppedFollower(rc, now)
		return
	}
	if rc.lifecycle.followerStop.accepted {
		if rc.lifecycle.checkpoint.inflight || rc.lifecycle.stoppedAck.inflight {
			return
		}
		if rc.lifecycle.stage == lifecycleFollowerCheckpointing {
			r.trySubmitStopCheckpoint(rc, now)
			return
		}
		if rc.lifecycle.stage == lifecycleFollowerStoppedAcking {
			r.trySubmitStoppedAck(rc, now)
			return
		}
	}
	if rc.replication.pendingPull != nil {
		r.trySubmitPendingApply(rc, now)
		return
	}
	if rc.replication.pullInflight || (!rc.replication.nextPullAt.IsZero() && now.Before(rc.replication.nextPullAt)) {
		return
	}
	r.trySubmitPull(rc, now)
}

func (r *Reactor) trySubmitPull(rc *runtimeChannel, now time.Time) {
	recoveryProbe := rc.replication.recoveryProbe && !rc.replication.nextPullAt.IsZero() && !now.Before(rc.replication.nextPullAt)
	if r.cfg.Pools == nil {
		if recoveryProbe {
			r.observeRecoveryProbe("err")
		}
		// Keep recoveryProbe armed so the backoff retry is still classified as a recovery probe.
		r.backoffPull(rc, ch.ErrInvalidConfig, now)
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	req := transport.PullRequest{
		ChannelKey:  rc.state.Key,
		ChannelID:   rc.state.ID,
		Epoch:       rc.state.Epoch,
		LeaderEpoch: rc.state.LeaderEpoch,
		Follower:    r.cfg.LocalNode,
		NextOffset:  rc.state.LEO + 1,
		AckOffset:   rc.state.LEO,
		MaxBytes:    r.cfg.PullMaxBytes,
	}
	pullCtx, cancelPull := followerPullCancelContext()
	if err := r.submitRPCPull(pullCtx, rc.state.Leader, fence, req, r.followerPullRPCTimeout()); err != nil {
		cancelPull()
		if !rc.replication.hintedAt.IsZero() && req.NextOffset <= rc.replication.hintedLeaderLEO {
			r.observeReplicationStage(replicationStagePullHintToSubmit, "err", now.Sub(rc.replication.hintedAt))
		}
		if recoveryProbe {
			r.observeRecoveryProbe("err")
		}
		// Keep recoveryProbe armed so the backoff retry is still classified as a recovery probe.
		r.backoffPull(rc, err, now)
		return
	}
	rc.replication.pullInflight = true
	rc.replication.pullOpID = opID
	rc.replication.pullCancel = cancelPull
	rc.replication.pullSubmittedAt = now
	rc.replication.pullSubmittedAckOffset = req.AckOffset
	rc.replication.dirty = false
	if !rc.replication.hintedAt.IsZero() && req.NextOffset <= rc.replication.hintedLeaderLEO {
		r.observeReplicationStage(replicationStagePullHintToSubmit, "ok", now.Sub(rc.replication.hintedAt))
		rc.replication.hintedAt = time.Time{}
	}
	if recoveryProbe {
		rc.replication.recoveryProbe = false
		rc.replication.recoveryProbeInflight = true
		r.observeRecoveryProbe("submitted")
	} else {
		rc.replication.recoveryProbeInflight = false
	}
}

func followerPullCancelContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func (r *Reactor) followerPullRPCTimeout() time.Duration {
	interval := r.cfg.PullHintRetryInterval
	if interval <= 0 {
		interval = time.Second
	}
	timeout := interval * 2
	if timeout <= 0 {
		timeout = time.Second
	}
	if timeout > time.Second {
		timeout = time.Second
	}
	return timeout
}

func (r *Reactor) trySubmitPendingApply(rc *runtimeChannel, now time.Time) {
	if rc.replication.pendingPull == nil || rc.replication.applyOpID != 0 {
		return
	}
	if rc.replication.applyBlocked && !rc.replication.applyRetryAt.IsZero() && now.Before(rc.replication.applyRetryAt) {
		return
	}
	rc.replication.applyBlocked = false
	rc.replication.applyRetryAt = time.Time{}
	resp := rc.replication.pendingPull
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreApply(context.Background(), rc.state.ID, fence, resp.Records, resp.LeaderHW); err != nil {
		rc.replication.applyBlocked = true
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.applyRetryAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = err
		return
	}
	rc.replication.applyOpID = opID
	rc.replication.applySubmittedAt = now
	rc.replication.applyBlocked = false
	rc.replication.applyRetryAt = time.Time{}
}

func (r *Reactor) trySubmitStopCheckpoint(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || !rc.lifecycle.followerStop.accepted || rc.lifecycle.checkpoint.inflight {
		return
	}
	if !rc.lifecycle.checkpoint.retryAt.IsZero() && now.Before(rc.lifecycle.checkpoint.retryAt) {
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreCheckpoint(context.Background(), rc.state.ID, fence, ch.Checkpoint{HW: rc.state.HW}); err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.checkpoint.retryAt = now.Add(rc.replication.backoff)
		rc.lifecycle.checkpoint.version = rc.lifecycle.followerStop.version
		rc.replication.lastError = err
		return
	}
	rc.lifecycle.stage = lifecycleFollowerCheckpointing
	rc.lifecycle.checkpoint.inflight = true
	rc.lifecycle.checkpoint.opID = opID
	rc.lifecycle.checkpoint.version = rc.lifecycle.followerStop.version
	rc.lifecycle.checkpoint.retryAt = time.Time{}
}

func (r *Reactor) trySubmitStoppedAck(rc *runtimeChannel, now time.Time) bool {
	if rc == nil || rc.state == nil || !rc.lifecycle.followerStop.accepted || rc.lifecycle.stoppedAck.inflight {
		return false
	}
	if !rc.lifecycle.stoppedAck.retryAt.IsZero() && now.Before(rc.lifecycle.stoppedAck.retryAt) {
		return false
	}
	opID := r.nextOpID()
	version := rc.lifecycle.followerStop.version
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	req := transport.AckRequest{
		ChannelKey:      rc.state.Key,
		Epoch:           rc.state.Epoch,
		LeaderEpoch:     rc.state.LeaderEpoch,
		Follower:        r.cfg.LocalNode,
		MatchOffset:     rc.state.LEO,
		ActivityVersion: version,
		Stopped:         true,
	}
	if err := r.submitRPCAck(context.Background(), rc.state.Leader, fence, req); err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.stoppedAck.retryAt = now.Add(rc.replication.backoff)
		rc.lifecycle.stoppedAck.version = version
		rc.replication.lastError = err
		return false
	}
	rc.lifecycle.stage = lifecycleFollowerStoppedAcking
	rc.lifecycle.stoppedAck.inflight = true
	rc.lifecycle.stoppedAck.opID = opID
	rc.lifecycle.stoppedAck.version = version
	rc.lifecycle.stoppedAck.retryAt = time.Time{}
	return true
}

// tryEvictStoppedFollower retries runtime deletion after the stopped ACK has already reached the leader.
func (r *Reactor) tryEvictStoppedFollower(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.lifecycle.stage != lifecycleFollowerReadyToEvict {
		return
	}
	if !rc.lifecycle.finalCheck.retryAt.IsZero() && now.Before(rc.lifecycle.finalCheck.retryAt) {
		return
	}
	rc.lifecycle.finalCheck.retryAt = time.Time{}
	if r.evictRuntimeChannel(rc.state.Key, rc, "stopped ack retry") {
		r.clearAppendSubmitState(rc.state.Key)
		return
	}
	rc.lifecycle.finalCheck.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
}

func (r *Reactor) handleFollowerPullHint(event Event) {
	req := event.PullHint
	if err := validatePendingPullHint(req, r.cfg.LocalNode); err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		r.handlePendingMetaPullHint(event)
		return
	}
	if loadedPullHintNewer(rc, req) {
		err = r.submitLoadedMetaRefresh(rc, req, time.Now())
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	if req.ChannelKey != "" && req.ChannelKey != rc.state.Key {
		err = ch.ErrStaleMeta
	} else if req.ChannelID != (ch.ChannelID{}) && req.ChannelID != rc.state.ID {
		err = ch.ErrStaleMeta
	} else if rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive ||
		req.Epoch != rc.state.Epoch || req.LeaderEpoch != rc.state.LeaderEpoch ||
		req.Leader != rc.state.Leader {
		err = ch.ErrStaleMeta
	} else if !rc.state.IsReplica(r.cfg.LocalNode) {
		err = ch.ErrNotReplica
	}
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	if req.ActivityVersion < rc.replication.lastActivityVersion {
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	now := time.Now()
	if req.ActivityVersion > rc.replication.lastActivityVersion {
		rc.lifecycle.cancelFollowerStop()
		rc.replication.lastActivityVersion = req.ActivityVersion
	}
	if req.LeaderLEO <= rc.state.LEO {
		rc.replication.hintedLeaderLEO = 0
		rc.replication.hintedAt = time.Time{}
		if rc.replication.ackReturnOffset > 0 {
			wasParked := rc.replication.parked
			rc.replication.clearParkedForHint(now)
			r.observeFollowerParkedCountIfChanged(wasParked, rc)
			r.tickFollowerReplication(rc, now)
			if event.Future != nil {
				event.Future.Complete(Result{})
			}
			return
		}
		if rc.replication.parked && rc.replication.recoveryProbe && rc.replication.nextPullAt.IsZero() {
			rc.replication.parkWithRecovery(rc.state.Key, now, r.cfg.FollowerRecoveryProbeInterval, r.cfg.FollowerRecoveryProbeJitter)
			r.observeFollowerParkedCount(r.countParkedFollowers())
		}
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	if req.LeaderLEO > rc.replication.hintedLeaderLEO {
		rc.replication.hintedLeaderLEO = req.LeaderLEO
		rc.replication.hintedAt = now
	}
	wasParked := rc.replication.parked
	rc.replication.clearParkedForHint(now)
	r.observeFollowerParkedCountIfChanged(wasParked, rc)
	r.tickFollowerReplication(rc, now)
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

func (r *Reactor) handlePendingMetaPullHint(event Event) {
	if r.shouldAsyncStoreLoad() {
		if err := r.startColdActivation(event.PullHint); err != nil {
			if event.Future != nil {
				event.Future.Complete(Result{Err: err})
			}
			return
		}
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	rc, err := r.ensurePendingMeta(event.PullHint)
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	if err := r.submitPendingMetaPull(rc, time.Now()); err != nil {
		r.releasePendingMeta(event.PullHint.ChannelKey, rc, err)
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

func (r *Reactor) submitPendingMetaPull(rc *runtimeChannel, now time.Time) error {
	if r == nil || rc == nil || rc.pending == nil {
		return ch.ErrChannelNotFound
	}
	pending := rc.pending
	if pending.pullInflight {
		return nil
	}
	for {
		opID := r.nextOpID()
		fence := ch.Fence{ChannelKey: pending.key, Generation: pending.generation, Epoch: pending.epoch, LeaderEpoch: pending.leaderEpoch, OpID: opID}
		req := transport.PullRequest{
			ChannelKey:  pending.key,
			ChannelID:   pending.id,
			Epoch:       pending.epoch,
			LeaderEpoch: pending.leaderEpoch,
			Follower:    r.cfg.LocalNode,
			NextOffset:  pending.initial.LEO + 1,
			AckOffset:   pending.initial.LEO,
			MaxBytes:    r.cfg.PullMaxBytes,
			NeedMeta:    true,
		}
		if err := r.submitRPCPull(context.Background(), pending.leader, fence, req, 0); err != nil {
			pending.lastError = err
			if retryablePendingPullError(err) && pending.retryCount < 1 {
				pending.retryCount++
				r.observeNeedMetaPull("retry", err)
				continue
			}
			r.observeNeedMetaPull("err", err)
			return err
		}
		pending.pullInflight = true
		pending.pullOpID = opID
		pending.pullSubmittedAt = now
		pending.pullSubmittedAckOffset = req.AckOffset
		pending.lastError = nil
		r.observeNeedMetaPull("submitted", nil)
		return nil
	}
}

func (r *Reactor) retryPendingMetaPull(rc *runtimeChannel, now time.Time) error {
	if r == nil || rc == nil || rc.pending == nil {
		return ch.ErrChannelNotFound
	}
	pending := rc.pending
	if pending.retryCount < 1 {
		pending.retryCount++
		if err := r.submitPendingMetaPull(rc, now); err != nil {
			return err
		}
		if pending.pullInflight {
			return nil
		}
	}
	return pending.lastError
}

// handleLegacyFollowerNotify accepts the legacy transport compatibility nudge
// and maps it to the current PullHint-driven follower resume path.
func (r *Reactor) handleLegacyFollowerNotify(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	req := event.Notify
	if rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive ||
		req.Epoch != rc.state.Epoch || req.LeaderEpoch != rc.state.LeaderEpoch ||
		req.Leader != rc.state.Leader {
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	now := time.Now()
	wasParked := rc.replication.parked
	rc.replication.markDirty(now)
	r.observeFollowerParkedCountIfChanged(wasParked, rc)
	r.tickFollowerReplication(rc, now)
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

func (r *Reactor) handleRPCPullResult(result worker.Result) {
	if rc := r.channels[result.Fence.ChannelKey]; rc != nil && rc.pending != nil && rc.state == nil {
		r.handlePendingMetaPullResult(rc, result)
		return
	}
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	now := time.Now()
	pullSubmittedAt := rc.replication.pullSubmittedAt
	pullSubmittedAckOffset := rc.replication.pullSubmittedAckOffset
	ackReturnStartedAt := rc.replication.ackReturnStartedAt
	ackReturnOffset := rc.replication.ackReturnOffset
	current := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: rc.replication.pullOpID}
	if !rc.replication.applyPullResult(result, current, now) {
		return
	}
	rc.replication.pullSubmittedAt = time.Time{}
	rc.replication.pullSubmittedAckOffset = 0
	recoveryProbe := rc.replication.recoveryProbeInflight
	rc.replication.recoveryProbeInflight = false
	defer r.scheduleReplicationFromState(rc, now)
	if result.Err != nil {
		r.observeFollowerPullRPCWait(pullSubmittedAt, "err", now)
		r.observePull("err", false)
		if recoveryProbe {
			r.observeRecoveryProbe("err")
		}
		r.backoffPull(rc, result.Err, now)
		return
	}
	if result.RPCPull == nil {
		r.observeFollowerPullRPCWait(pullSubmittedAt, "err", now)
		r.observePull("err", false)
		if recoveryProbe {
			r.observeRecoveryProbe("err")
		}
		r.backoffPull(rc, ch.ErrInvalidConfig, now)
		return
	}
	resp := result.RPCPull.Response
	if resp.ChannelKey != rc.state.Key || resp.Epoch != rc.state.Epoch || resp.LeaderEpoch != rc.state.LeaderEpoch || resp.Meta != nil {
		r.observeFollowerPullRPCWait(pullSubmittedAt, "err", now)
		r.observePull("err", false)
		if recoveryProbe {
			r.observeRecoveryProbe("err")
		}
		if resp.Meta != nil {
			r.backoffPull(rc, ch.ErrInvalidConfig, now)
			return
		}
		r.backoffPull(rc, ch.ErrStaleMeta, now)
		return
	}
	r.acceptFollowerPullResponse(rc, resp, pullSubmittedAt, pullSubmittedAckOffset, ackReturnOffset, ackReturnStartedAt, recoveryProbe, now)
}

func (r *Reactor) handlePendingMetaPullResult(rc *runtimeChannel, result worker.Result) {
	pending := rc.pending
	if pending == nil || result.Fence.ChannelKey != pending.key || result.Fence.Generation != pending.generation || result.Fence.Epoch != pending.epoch || result.Fence.LeaderEpoch != pending.leaderEpoch || result.Fence.OpID != pending.pullOpID || !pending.pullInflight {
		return
	}
	now := time.Now()
	pullSubmittedAt := pending.pullSubmittedAt
	pullSubmittedAckOffset := pending.pullSubmittedAckOffset
	pending.pullInflight = false
	pending.pullOpID = 0
	pending.pullSubmittedAt = time.Time{}
	pending.pullSubmittedAckOffset = 0
	if result.Err != nil {
		r.observeNeedMetaPullRPCWait(pullSubmittedAt, "err", now)
		pending.lastError = result.Err
		if contextDoneError(result.Err) || nonRetryablePendingPullError(result.Err) {
			r.observeNeedMetaPull("err", result.Err)
			r.releasePendingMeta(pending.key, rc, result.Err)
			return
		}
		if retryablePendingPullError(result.Err) && pending.retryCount < 1 {
			r.observeNeedMetaPull("retry", result.Err)
			if err := r.retryPendingMetaPull(rc, now); err != nil {
				r.releasePendingMeta(pending.key, rc, err)
			}
			return
		}
		r.observeNeedMetaPull("err", result.Err)
		r.releasePendingMeta(pending.key, rc, result.Err)
		return
	}
	if result.RPCPull == nil {
		err := ch.ErrInvalidConfig
		r.observeNeedMetaPullRPCWait(pullSubmittedAt, "err", now)
		r.observeNeedMetaPull("err", err)
		r.releasePendingMeta(pending.key, rc, err)
		return
	}
	resp := result.RPCPull.Response
	if resp.Meta == nil || resp.ChannelKey != pending.key || resp.Epoch != pending.epoch || resp.LeaderEpoch != pending.leaderEpoch {
		err := ch.ErrStaleMeta
		if resp.Meta == nil {
			err = ch.ErrInvalidConfig
		}
		r.observeNeedMetaPullRPCWait(pullSubmittedAt, "err", now)
		r.observeNeedMetaPull("err", err)
		r.releasePendingMeta(pending.key, rc, err)
		return
	}
	meta := *resp.Meta
	if err := validatePendingMetaShape(meta); err != nil {
		r.observeNeedMetaPullRPCWait(pullSubmittedAt, "err", now)
		r.observeNeedMetaPull("err", err)
		r.releasePendingMeta(pending.key, rc, err)
		return
	}
	if cmp := comparePendingMetaFence(pending, meta); cmp != 0 || meta.ID != pending.id || meta.Leader != pending.leader || !metaHasReplica(meta, r.cfg.LocalNode) {
		err := ch.ErrStaleMeta
		if !metaHasReplica(meta, r.cfg.LocalNode) {
			err = ch.ErrNotReplica
		}
		r.observeNeedMetaPullRPCWait(pullSubmittedAt, "err", now)
		r.observeNeedMetaPull("err", err)
		r.releasePendingMeta(pending.key, rc, err)
		return
	}
	if err := r.convertPendingMeta(rc, meta); err != nil {
		r.observeNeedMetaPullRPCWait(pullSubmittedAt, "err", now)
		r.observeNeedMetaPull("err", err)
		r.releasePendingMeta(pending.key, rc, err)
		return
	}
	resp.Meta = nil
	r.acceptNeedMetaPullResponse(rc, resp, pullSubmittedAt, pullSubmittedAckOffset, 0, time.Time{}, false, now)
	r.scheduleReplicationFromState(rc, now)
}

// loadedPullHintNewer reports whether a validated hint strictly supersedes a loaded runtime fence.
func loadedPullHintNewer(rc *runtimeChannel, req transport.PullHintRequest) bool {
	if rc == nil || rc.state == nil {
		return false
	}
	if req.ChannelKey != rc.state.Key || req.ChannelID != rc.state.ID {
		return false
	}
	if req.Epoch > rc.state.Epoch {
		return true
	}
	return req.Epoch == rc.state.Epoch && req.LeaderEpoch > rc.state.LeaderEpoch
}

func (r *Reactor) submitLoadedMetaRefresh(rc *runtimeChannel, req transport.PullHintRequest, now time.Time) error {
	if r == nil || rc == nil || rc.state == nil || r.channels[rc.state.Key] != rc || !loadedPullHintNewer(rc, req) {
		return ch.ErrStaleMeta
	}
	if r.cfg.Pools == nil || r.cfg.Pools.MetaResolve == nil {
		return ch.ErrNotReady
	}
	if current := r.loadedMetaRefreshes[req.ChannelKey]; current != nil {
		if !current.deadline.IsZero() && !now.Before(current.deadline) {
			r.clearLoadedMetaRefresh(req.ChannelKey)
			current = nil
		}
		if current != nil {
			if current.runtime != rc || current.generation != rc.state.Generation ||
				current.baseEpoch != rc.state.Epoch || current.baseLeaderEpoch != rc.state.LeaderEpoch ||
				current.baseLeader != rc.state.Leader || current.id != req.ChannelID {
				return ch.ErrStaleMeta
			}
			// The hint is only a refresh trigger. All valid newer hints coalesce into the
			// fixed admission lease without trusting or extending the first target fence.
			return nil
		}
	}

	timeout := r.followerPullRPCTimeout()
	resolveCtx, resolveCancel := context.WithTimeout(context.Background(), timeout)
	opID := r.nextOpID()
	fence := ch.Fence{
		ChannelKey:  req.ChannelKey,
		Generation:  rc.state.Generation,
		Epoch:       rc.state.Epoch,
		LeaderEpoch: rc.state.LeaderEpoch,
		OpID:        opID,
	}
	if r.loadedMetaRefreshes == nil {
		r.loadedMetaRefreshes = make(map[ch.ChannelKey]*loadedMetaRefreshState)
	}
	refresh := &loadedMetaRefreshState{
		key:             req.ChannelKey,
		id:              req.ChannelID,
		runtime:         rc,
		generation:      rc.state.Generation,
		baseEpoch:       rc.state.Epoch,
		baseLeaderEpoch: rc.state.LeaderEpoch,
		baseLeader:      rc.state.Leader,
		opID:            opID,
		deadline:        now.Add(timeout),
		cancel:          resolveCancel,
		inflight:        true,
	}
	r.loadedMetaRefreshes[req.ChannelKey] = refresh
	r.scheduleLoadedMetaRefreshDeadline(refresh)
	if err := r.submitMetaResolve(resolveCtx, fence, req.ChannelID); err != nil {
		r.clearLoadedMetaRefresh(req.ChannelKey)
		return err
	}
	return nil
}

// scheduleLoadedMetaRefreshDeadline bounds failed refresh admission state even when no later hint arrives.
func (r *Reactor) scheduleLoadedMetaRefreshDeadline(refresh *loadedMetaRefreshState) {
	if r == nil || refresh == nil || refresh.deadline.IsZero() {
		return
	}
	r.due.push(dueItem{
		key:     refresh.key,
		kind:    dueLoadedMetaRefresh,
		due:     refresh.deadline,
		version: uint64(refresh.opID),
	})
}

// releaseExpiredLoadedMetaRefresh releases only the admission lease that scheduled this due item.
func (r *Reactor) releaseExpiredLoadedMetaRefresh(key ch.ChannelKey, version uint64, now time.Time) {
	if r == nil || r.loadedMetaRefreshes == nil {
		return
	}
	refresh := r.loadedMetaRefreshes[key]
	if refresh == nil || uint64(refresh.opID) != version || refresh.deadline.IsZero() || now.Before(refresh.deadline) {
		return
	}
	r.clearLoadedMetaRefresh(key)
}

func (r *Reactor) handleMetaResolveResult(result worker.Result) {
	if r == nil || r.loadedMetaRefreshes == nil {
		return
	}
	refresh := r.loadedMetaRefreshes[result.Fence.ChannelKey]
	if refresh == nil ||
		result.Fence.Generation != refresh.generation ||
		result.Fence.Epoch != refresh.baseEpoch ||
		result.Fence.LeaderEpoch != refresh.baseLeaderEpoch ||
		result.Fence.OpID != refresh.opID || !refresh.inflight {
		return
	}
	refresh.inflight = false
	if refresh.cancel != nil {
		refresh.cancel()
		refresh.cancel = nil
	}

	rc := r.channels[result.Fence.ChannelKey]
	if rc == nil || rc != refresh.runtime || rc.state == nil ||
		rc.state.Generation != refresh.generation ||
		rc.state.Epoch != refresh.baseEpoch ||
		rc.state.LeaderEpoch != refresh.baseLeaderEpoch || rc.state.Leader != refresh.baseLeader {
		r.clearLoadedMetaRefresh(result.Fence.ChannelKey)
		return
	}
	if result.Err != nil {
		return
	}
	if result.MetaResolve == nil {
		return
	}

	meta, err := validateLoadedMetaResolveResult(refresh, result.MetaResolve.Meta, r.cfg.LocalNode)
	if err != nil {
		return
	}
	if err := r.applyLoadedRuntimeMeta(rc, meta, true); err != nil {
		return
	}
}

func validateLoadedMetaResolveResult(refresh *loadedMetaRefreshState, meta ch.Meta, local ch.NodeID) (ch.Meta, error) {
	if refresh == nil {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	if err := validatePendingMetaShape(meta); err != nil {
		return ch.Meta{}, err
	}
	if meta.Key != refresh.key || meta.ID != refresh.id || !metaFenceStrictlyNewer(meta.Epoch, meta.LeaderEpoch, refresh.baseEpoch, refresh.baseLeaderEpoch) {
		return ch.Meta{}, ch.ErrStaleMeta
	}
	if err := validateActiveMetaTopology(meta, local); err != nil {
		return ch.Meta{}, err
	}
	return meta, nil
}

func metaFenceStrictlyNewer(epoch, leaderEpoch, baseEpoch, baseLeaderEpoch uint64) bool {
	return epoch > baseEpoch || (epoch == baseEpoch && leaderEpoch > baseLeaderEpoch)
}

func validateActiveMetaTopology(meta ch.Meta, local ch.NodeID) error {
	replicas := make(map[ch.NodeID]struct{}, len(meta.Replicas))
	for _, node := range meta.Replicas {
		if node == 0 {
			return ch.ErrInvalidConfig
		}
		if _, exists := replicas[node]; exists {
			return ch.ErrInvalidConfig
		}
		replicas[node] = struct{}{}
	}
	if _, ok := replicas[meta.Leader]; !ok {
		return ch.ErrInvalidConfig
	}
	if _, ok := replicas[local]; !ok {
		return ch.ErrNotReplica
	}
	isr := make(map[ch.NodeID]struct{}, len(meta.ISR))
	for _, node := range meta.ISR {
		if _, ok := replicas[node]; !ok {
			return ch.ErrInvalidConfig
		}
		if _, exists := isr[node]; exists {
			return ch.ErrInvalidConfig
		}
		isr[node] = struct{}{}
	}
	return nil
}

func (r *Reactor) clearLoadedMetaRefresh(key ch.ChannelKey) {
	if r == nil || r.loadedMetaRefreshes == nil {
		return
	}
	r.due.remove(dueLoadedMetaRefresh, key)
	if refresh := r.loadedMetaRefreshes[key]; refresh != nil && refresh.cancel != nil {
		refresh.cancel()
	}
	delete(r.loadedMetaRefreshes, key)
	if len(r.loadedMetaRefreshes) == 0 {
		r.loadedMetaRefreshes = nil
	}
}

func (r *Reactor) clearAllLoadedMetaRefreshes() {
	if r == nil || r.loadedMetaRefreshes == nil {
		return
	}
	for key := range r.loadedMetaRefreshes {
		r.clearLoadedMetaRefresh(key)
	}
}

func (r *Reactor) acceptFollowerPullResponse(rc *runtimeChannel, resp transport.PullResponse, pullSubmittedAt time.Time, pullSubmittedAckOffset uint64, ackReturnOffset uint64, ackReturnStartedAt time.Time, recoveryProbe bool, now time.Time) {
	r.observeFollowerPullRPCWait(pullSubmittedAt, "ok", now)
	r.observeFollowerAckReturnWait(rc, pullSubmittedAckOffset, ackReturnOffset, ackReturnStartedAt, now)
	r.observePull("ok", len(resp.Records) == 0)
	r.applyFollowerPullResponse(rc, resp, recoveryProbe, now)
}

func (r *Reactor) acceptNeedMetaPullResponse(rc *runtimeChannel, resp transport.PullResponse, pullSubmittedAt time.Time, pullSubmittedAckOffset uint64, ackReturnOffset uint64, ackReturnStartedAt time.Time, recoveryProbe bool, now time.Time) {
	r.observeNeedMetaPullRPCWait(pullSubmittedAt, "ok", now)
	r.observeFollowerAckReturnWait(rc, pullSubmittedAckOffset, ackReturnOffset, ackReturnStartedAt, now)
	r.observeNeedMetaPull("ok", nil)
	r.applyFollowerPullResponse(rc, resp, recoveryProbe, now)
}

func (r *Reactor) applyFollowerPullResponse(rc *runtimeChannel, resp transport.PullResponse, recoveryProbe bool, now time.Time) {
	if recoveryProbe {
		r.observeRecoveryProbe("ok")
	}
	rc.replication.lastLeaderHW = resp.LeaderHW
	rc.replication.backoff = 0
	if resp.Control == transport.PullControlStop && resp.ActivityVersion < rc.replication.lastActivityVersion {
		wasParked := rc.replication.parked
		rc.replication.markDirty(now)
		r.observeFollowerParkedCountIfChanged(wasParked, rc)
		r.tickFollowerReplication(rc, now)
		return
	}
	if resp.ActivityVersion > rc.replication.lastActivityVersion {
		rc.replication.lastActivityVersion = resp.ActivityVersion
	}
	if resp.Control == transport.PullControlStop {
		r.handleFollowerStopControl(rc, resp, now)
		return
	}
	if len(resp.Records) == 0 {
		rc.state.HW = minUint64(rc.state.LEO, resp.LeaderHW)
		r.trySubmitCommittedCheckpoint(rc)
		if rc.replication.hintedLeaderLEO <= rc.state.LEO {
			rc.replication.hintedLeaderLEO = 0
			rc.replication.hintedAt = time.Time{}
		}
		if resp.LeaderLEO > rc.state.LEO || rc.replication.hintedLeaderLEO > rc.state.LEO {
			r.scheduleEmptyLaggingPullRetry(rc, now)
			return
		}
		wasParked := rc.replication.parked
		rc.replication.parkWithRecovery(rc.state.Key, now, r.cfg.FollowerRecoveryProbeInterval, r.cfg.FollowerRecoveryProbeJitter)
		r.observeFollowerParkedCountIfChanged(wasParked, rc)
		return
	}
	wasParked := rc.replication.parked
	rc.replication.parked = false
	rc.replication.nextPullAfter = 0
	rc.replication.pendingPull = &resp
	rc.replication.applyBlocked = false
	rc.replication.applyRetryAt = time.Time{}
	rc.replication.applyOpID = 0
	r.observeFollowerParkedCountIfChanged(wasParked, rc)
	r.trySubmitPendingApply(rc, now)
}

func (r *Reactor) trySubmitCommittedCheckpoint(rc *runtimeChannel) {
	if rc == nil || rc.state == nil || rc.state.HW <= rc.state.CheckpointHW || rc.committedCheckpointOp != 0 {
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{
		ChannelKey:  rc.state.Key,
		Generation:  rc.state.Generation,
		Epoch:       rc.state.Epoch,
		LeaderEpoch: rc.state.LeaderEpoch,
		OpID:        opID,
	}
	if err := r.submitStoreCheckpoint(context.Background(), rc.state.ID, fence, ch.Checkpoint{HW: rc.state.HW}); err != nil {
		return
	}
	rc.committedCheckpointOp = opID
}

func (r *Reactor) observeFollowerPullRPCWait(submittedAt time.Time, result string, completedAt time.Time) {
	if submittedAt.IsZero() {
		return
	}
	r.observeReplicationStage(replicationStagePullRPC, result, completedAt.Sub(submittedAt))
}

func (r *Reactor) observeNeedMetaPullRPCWait(submittedAt time.Time, result string, completedAt time.Time) {
	if submittedAt.IsZero() {
		return
	}
	r.observeReplicationStage(replicationStageNeedMetaPullRPC, result, completedAt.Sub(submittedAt))
}

func (r *Reactor) observeFollowerStoreApplyWait(submittedAt time.Time, result string, completedAt time.Time) {
	if submittedAt.IsZero() {
		return
	}
	r.observeReplicationStage(replicationStageStoreApply, result, completedAt.Sub(submittedAt))
}

func (r *Reactor) observeFollowerAckReturnWait(rc *runtimeChannel, submittedAckOffset uint64, ackReturnOffset uint64, startedAt time.Time, completedAt time.Time) {
	if rc == nil || submittedAckOffset == 0 || ackReturnOffset == 0 || startedAt.IsZero() || submittedAckOffset < ackReturnOffset {
		return
	}
	r.observeReplicationStage(replicationStageApplyToAckReturn, "ok", completedAt.Sub(startedAt))
	rc.replication.ackReturnOffset = 0
	rc.replication.ackReturnStartedAt = time.Time{}
}

func (r *Reactor) scheduleEmptyLaggingPullRetry(rc *runtimeChannel, now time.Time) {
	delay := r.cfg.ReplicationMinBackoff
	if delay <= 0 {
		delay = time.Millisecond
	}
	wasParked := rc.replication.parked
	rc.replication.dirty = true
	rc.replication.parked = false
	rc.replication.nextPullAfter = delay
	rc.replication.nextPullAt = now.Add(delay)
	r.observeFollowerParkedCountIfChanged(wasParked, rc)
}

func (r *Reactor) countParkedFollowers() int {
	count := 0
	for _, rc := range r.channels {
		if rc != nil && rc.state != nil && rc.state.Role == ch.RoleFollower && rc.replication.parked {
			count++
		}
	}
	return count
}

func (r *Reactor) observeFollowerParkedCountIfChanged(wasParked bool, rc *runtimeChannel) {
	if rc == nil || wasParked == rc.replication.parked {
		return
	}
	r.observeFollowerParkedCount(r.countParkedFollowers())
}

func (r *Reactor) handleFollowerStopControl(rc *runtimeChannel, resp transport.PullResponse, now time.Time) {
	if rc == nil || rc.state == nil {
		return
	}
	view := runtimeViewFromChannel(rc, now, AppendFenceView{})
	canAccept := view.Role == ch.RoleFollower &&
		view.Status == ch.StatusActive &&
		!view.followerStopBlocked() &&
		rc.canAcceptFollowerStop() &&
		view.LEO >= resp.LeaderLEO &&
		view.HW >= resp.LeaderHW
	if !canAccept {
		rc.lifecycle.cancelFollowerStop()
		wasParked := rc.replication.parked
		rc.replication.markDirty(now)
		r.observeFollowerParkedCountIfChanged(wasParked, rc)
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.state.HW = minUint64(rc.state.LEO, resp.LeaderHW)
	rc.lifecycle.acceptFollowerStop(resp.ActivityVersion, resp.LeaderLEO, resp.LeaderHW)
	wasParked := rc.replication.parked
	rc.replication.parked = false
	rc.replication.dirty = false
	rc.replication.nextPullAt = time.Time{}
	rc.replication.nextPullAfter = 0
	r.observeFollowerParkedCountIfChanged(wasParked, rc)
	r.applyLifecycleActions(rc, []lifecycleAction{{kind: lifecycleActionStartFollowerStopCheckpoint}}, now)
}

func (r *Reactor) handleStoreApplyResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch || result.Fence.OpID != rc.replication.applyOpID {
		return
	}
	rc.replication.applyOpID = 0
	now := time.Now()
	applySubmittedAt := rc.replication.applySubmittedAt
	rc.replication.applySubmittedAt = time.Time{}
	defer r.scheduleReplicationFromState(rc, now)
	if result.Err != nil {
		r.observeFollowerStoreApplyWait(applySubmittedAt, "err", now)
		rc.replication.applyBlocked = true
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.applyRetryAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = result.Err
		return
	}
	if result.StoreApply == nil {
		r.observeFollowerStoreApplyWait(applySubmittedAt, "err", now)
		rc.replication.applyBlocked = true
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.applyRetryAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = ch.ErrInvalidConfig
		return
	}
	r.observeFollowerStoreApplyWait(applySubmittedAt, "ok", now)
	rc.state.LEO = result.StoreApply.LEO
	rc.state.HW = minUint64(rc.state.LEO, rc.replication.lastLeaderHW)
	if rc.replication.hintedLeaderLEO <= rc.state.LEO {
		rc.replication.hintedLeaderLEO = 0
		rc.replication.hintedAt = time.Time{}
	}
	if rc.state.LEO > 0 {
		rc.replication.ackReturnStartedAt = now
		rc.replication.ackReturnOffset = rc.state.LEO
	}
	rc.replication.pendingPull = nil
	rc.replication.applyBlocked = false
	rc.replication.applyRetryAt = time.Time{}
	rc.replication.backoff = 0
	rc.replication.lastError = nil
	wasParked := rc.replication.parked
	rc.replication.markDirty(now)
	r.observeFollowerParkedCountIfChanged(wasParked, rc)
	rc.replication.nextPullAt = now
	r.tickFollowerReplication(rc, now)
}

func (r *Reactor) handleRPCAckResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	if rc.lifecycle.stoppedAck.inflight &&
		result.Fence.ChannelKey == rc.state.Key &&
		result.Fence.Generation == rc.state.Generation &&
		result.Fence.Epoch == rc.state.Epoch &&
		result.Fence.LeaderEpoch == rc.state.LeaderEpoch &&
		result.Fence.OpID == rc.lifecycle.stoppedAck.opID {
		r.handleStoppedAckResult(rc, result)
		return
	}
}

func (r *Reactor) handleStoppedAckResult(rc *runtimeChannel, result worker.Result) {
	now := time.Now()
	r.driveLifecycle(rc, lifecycleEvent{kind: lifecycleEventStoppedAckDone, now: now, err: result.Err})
}

func (r *Reactor) backoffPull(rc *runtimeChannel, err error, now time.Time) {
	rc.replication.pullInflight = false
	rc.replication.pullOpID = 0
	rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
	rc.replication.nextPullAt = now.Add(rc.replication.backoff)
	rc.replication.lastError = err
}

func (rc *runtimeChannel) canAcceptFollowerStop() bool {
	if rc == nil {
		return false
	}
	replication := rc.replication
	return !replication.pullInflight &&
		replication.pendingPull == nil &&
		!replication.applyBlocked &&
		replication.applyOpID == 0 &&
		!rc.lifecycle.followerStop.accepted &&
		!rc.lifecycle.checkpoint.inflight &&
		!rc.lifecycle.stoppedAck.inflight
}
