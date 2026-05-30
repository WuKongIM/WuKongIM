package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
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
	if err := r.submitRPCPull(context.Background(), rc.state.Leader, fence, req); err != nil {
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
	rc, err := r.lookup(event.Key)
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	req := event.PullHint
	if req.ChannelKey != "" && req.ChannelKey != rc.state.Key {
		err = ch.ErrStaleMeta
	} else if req.ChannelID != (ch.ChannelID{}) && req.ChannelID != rc.state.ID {
		err = ch.ErrStaleMeta
	} else if rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive ||
		req.Epoch != rc.state.Epoch || req.LeaderEpoch != rc.state.LeaderEpoch ||
		req.Leader != rc.state.Leader || req.Leader == r.cfg.LocalNode ||
		!rc.state.IsReplica(r.cfg.LocalNode) {
		err = ch.ErrStaleMeta
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

// handleLegacyFollowerNotify accepts the legacy transport compatibility nudge
// and maps it to the current PullHint-driven follower resume path.
func (r *Reactor) handleLegacyFollowerNotify(event Event) {
	rc, err := r.lookup(event.Key)
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
	rc, err := r.lookup(result.Fence.ChannelKey)
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
	if resp.ChannelKey != rc.state.Key || resp.Epoch != rc.state.Epoch || resp.LeaderEpoch != rc.state.LeaderEpoch {
		r.observeFollowerPullRPCWait(pullSubmittedAt, "err", now)
		r.observePull("err", false)
		if recoveryProbe {
			r.observeRecoveryProbe("err")
		}
		r.backoffPull(rc, ch.ErrStaleMeta, now)
		return
	}
	r.observeFollowerPullRPCWait(pullSubmittedAt, "ok", now)
	r.observeFollowerAckReturnWait(rc, pullSubmittedAckOffset, ackReturnOffset, ackReturnStartedAt, now)
	r.observePull("ok", len(resp.Records) == 0)
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

func (r *Reactor) observeFollowerPullRPCWait(submittedAt time.Time, result string, completedAt time.Time) {
	if submittedAt.IsZero() {
		return
	}
	r.observeReplicationStage(replicationStagePullRPC, result, completedAt.Sub(submittedAt))
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
	rc, err := r.lookup(result.Fence.ChannelKey)
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
}

func (r *Reactor) handleRPCAckResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
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
