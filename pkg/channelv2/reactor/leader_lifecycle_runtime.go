package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

func (r *Reactor) sendPullHintsForAppend(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	r.syncFollowerMatches(rc)
	for node, follower := range rc.lifecycle.followers {
		if !rc.lifecycle.followerNeedsImmediateProgress(node, rc.state.LEO) {
			continue
		}
		r.trySubmitPullHint(rc, node, follower, transport.PullHintReasonAppend, now)
	}
}

func (r *Reactor) tickLeaderLifecycle(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	defer r.scheduleLifecycleFromState(rc, now)
	r.syncFollowerMatches(rc)
	for node, follower := range rc.lifecycle.followers {
		if follower == nil || follower.hint.inflight || follower.hint.retryAt.IsZero() || now.Before(follower.hint.retryAt) {
			continue
		}
		if !rc.lifecycle.followerNeedsImmediateProgress(node, rc.state.LEO) {
			follower.hint.retryAt = time.Time{}
			continue
		}
		r.trySubmitPullHint(rc, node, follower, transport.PullHintReasonResume, now)
	}
	r.tryEvictLeader(rc, now)
}

func (r *Reactor) resetPullHintLifecycle(rc *runtimeChannel) {
	if rc == nil {
		return
	}
	rc.lifecycle.pullHintInflight = nil
	for _, follower := range rc.lifecycle.followers {
		if follower != nil {
			follower.resetHint()
		}
	}
}

func (r *Reactor) leaderCanOfferStop(rc *runtimeChannel, now time.Time) bool {
	if rc == nil {
		return false
	}
	r.syncLeaderFollowers(rc)
	return runtimeViewFromChannel(rc, now, AppendFenceView{}).CanOfferFollowerStop(now, r.cfg.IdleEvictAfter)
}

func (r *Reactor) leaderIdleExpired(rc *runtimeChannel, now time.Time) bool {
	return runtimeViewFromChannel(rc, now, AppendFenceView{}).IdleExpired(now, r.cfg.IdleEvictAfter)
}

func (r *Reactor) allFollowersCaughtUp(rc *runtimeChannel) bool {
	if rc == nil {
		return false
	}
	r.syncLeaderFollowers(rc)
	return runtimeViewFromChannel(rc, time.Now(), AppendFenceView{}).AllFollowersCaughtUp()
}

func (r *Reactor) allFollowersStopped(rc *runtimeChannel) bool {
	if rc == nil {
		return false
	}
	r.syncLeaderFollowers(rc)
	return runtimeViewFromChannel(rc, time.Now(), AppendFenceView{}).AllFollowersStopped()
}

func (r *Reactor) hasPendingRuntimeWork(rc *runtimeChannel) bool {
	return runtimeViewFromChannel(rc, time.Now(), AppendFenceView{}).HasPendingWork()
}

func (r *Reactor) tryEvictLeader(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader || rc.state.Status != ch.StatusActive {
		return
	}
	if !r.leaderIdleExpired(rc, now) || rc.state.HW < rc.state.LEO {
		return
	}
	if !r.allFollowersStopped(rc) {
		return
	}
	if r.hasPendingRuntimeWork(rc) {
		return
	}
	if !rc.lifecycle.checkpoint.retryAt.IsZero() {
		if now.Before(rc.lifecycle.checkpoint.retryAt) {
			return
		}
		rc.lifecycle.checkpoint.retryAt = time.Time{}
	}
	if rc.lifecycle.finalCheck.inflight {
		if rc.lifecycle.finalCheck.version != rc.lifecycle.version {
			rc.lifecycle.finalCheck.inflight = false
			rc.lifecycle.finalCheck.version = 0
			rc.lifecycle.finalCheck.queued = false
			return
		}
		r.submitLeaderEvictReady(rc, now, r.currentAppendSubmitSeq(rc.state.Key))
		return
	}
	if rc.lifecycle.checkpoint.inflight {
		return
	}
	if !rc.lifecycle.checkpoint.retryAt.IsZero() && now.Before(rc.lifecycle.checkpoint.retryAt) {
		return
	}
	r.startLeaderCheckpoint(rc, now)
}

func (r *Reactor) submitLeaderEvictReady(rc *runtimeChannel, now time.Time, appendSeq uint64) {
	if r == nil || rc == nil || rc.state == nil || rc.lifecycle.finalCheck.queued {
		return
	}
	event := Event{Kind: EventLeaderEvictReady, Key: rc.state.Key, LeaderEvictAppendSeq: appendSeq}
	if err := r.mailbox.Submit(PriorityNormal, event); err != nil {
		rc.lifecycle.checkpoint.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.lifecycle.finalCheck.queued = true
	rc.lifecycle.checkpoint.retryAt = time.Time{}
	rc.lifecycle.stage = lifecycleLeaderReadyToEvict
}

func (r *Reactor) handleLeaderEvictReady(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		return
	}
	rc.lifecycle.finalCheck.queued = false
	if !rc.lifecycle.finalCheck.inflight || rc.lifecycle.finalCheck.version != rc.lifecycle.version {
		return
	}
	now := time.Now()
	rc.lifecycle.stage = lifecycleLeaderReadyToEvict
	if rc.state.HW < rc.state.LEO || !r.allFollowersStopped(rc) || r.hasPendingRuntimeWork(rc) {
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	r.submitMu.Lock()
	if r.appendReservations[event.Key] > 0 {
		r.submitMu.Unlock()
		rc.lifecycle.checkpoint.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	appendSeq := r.appendSubmitSeqs[event.Key]
	if appendSeq != event.LeaderEvictAppendSeq {
		r.submitMu.Unlock()
		r.submitLeaderEvictReady(rc, now, appendSeq)
		return
	}
	evicted := r.evictRuntimeChannel(rc.state.Key, rc, "leader idle checkpoint")
	if evicted {
		r.clearAppendSubmitStateLocked(event.Key)
	}
	r.submitMu.Unlock()
	if !evicted {
		rc.lifecycle.finalCheck.inflight = false
		rc.lifecycle.finalCheck.version = 0
		rc.lifecycle.checkpoint.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
	}
}

func (r *Reactor) trySubmitPullHint(rc *runtimeChannel, node ch.NodeID, follower *lifecycleFollower, reason transport.PullHintReason, now time.Time) {
	if rc == nil || rc.state == nil || follower == nil {
		return
	}
	version := rc.lifecycle.version
	if version == 0 {
		version = rc.state.LEO
	}
	if follower.hint.inflight {
		if follower.lastHintVersion < version {
			follower.pendingHintVersion = version
		}
		return
	}
	if follower.lastHintVersion == version && !follower.hint.retryAt.IsZero() && now.Before(follower.hint.retryAt) {
		return
	}
	if follower.lastHintVersion == version && follower.hint.retryAt.IsZero() {
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	req := transport.PullHintRequest{
		ChannelKey:      rc.state.Key,
		ChannelID:       rc.state.ID,
		Epoch:           rc.state.Epoch,
		LeaderEpoch:     rc.state.LeaderEpoch,
		Leader:          r.cfg.LocalNode,
		LeaderLEO:       rc.state.LEO,
		ActivityVersion: version,
		Reason:          reason,
	}
	if err := r.submitPullHint(context.Background(), node, fence, req); err != nil {
		follower.hint.inflight = false
		follower.hint.retryAt = now.Add(r.cfg.PullHintRetryInterval)
		r.observePullHintDropped(rc.state.Key, node, err)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.lifecycle.beginPullHint(node, opID, version, reason)
	r.observePullHintSent(rc.state.Key, node, reason)
}

func (r *Reactor) handleRPCPullHintResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	if result.Fence.ChannelKey != rc.state.Key ||
		result.Fence.Generation != rc.state.Generation ||
		result.Fence.Epoch != rc.state.Epoch ||
		result.Fence.LeaderEpoch != rc.state.LeaderEpoch {
		return
	}
	inflight, ok := rc.lifecycle.finishPullHint(result.Fence.OpID)
	if !ok {
		return
	}
	follower := rc.lifecycle.followers[inflight.follower]
	if follower == nil {
		return
	}
	if follower.hint.inflight && follower.hint.opID != result.Fence.OpID {
		return
	}
	now := time.Now()
	if result.Err == nil {
		r.sendCurrentPullHintIfNeeded(rc, inflight.follower, follower, now)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	r.observePullHintDropped(rc.state.Key, inflight.follower, result.Err)
	if rc.lifecycle.followerNeedsImmediateProgress(inflight.follower, rc.state.LEO) {
		follower.pendingHintVersion = rc.lifecycle.version
		follower.hint.retryAt = now.Add(r.cfg.PullHintRetryInterval)
	}
	r.scheduleLifecycleFromState(rc, now)
}

func (r *Reactor) sendCurrentPullHintIfNeeded(rc *runtimeChannel, node ch.NodeID, follower *lifecycleFollower, now time.Time) {
	if rc == nil || rc.state == nil || follower == nil || follower.lastHintVersion >= rc.lifecycle.version {
		return
	}
	if !rc.lifecycle.followerNeedsImmediateProgress(node, rc.state.LEO) {
		follower.pendingHintVersion = 0
		return
	}
	follower.pendingHintVersion = rc.lifecycle.version
	r.trySubmitPullHint(rc, node, follower, transport.PullHintReasonAppend, now)
}

func (r *Reactor) handleStoreCheckpointResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch {
		if rc.lifecycle.checkpoint.inflight && result.Fence.OpID == rc.lifecycle.checkpoint.opID {
			resetLeaderCheckpointLifecycle(rc)
		}
		return
	}
	if rc.lifecycle.followerStop.accepted &&
		rc.lifecycle.checkpoint.inflight &&
		result.Fence.OpID == rc.lifecycle.checkpoint.opID &&
		result.Fence.Generation == rc.state.Generation &&
		result.Fence.Epoch == rc.state.Epoch &&
		result.Fence.LeaderEpoch == rc.state.LeaderEpoch {
		r.handleFollowerStopCheckpointResult(rc, result)
		return
	}
	if rc.lifecycle.checkpoint.inflight && result.Fence.OpID == rc.lifecycle.checkpoint.opID {
		r.handleLeaderCheckpointResult(rc, result)
		return
	}
}

func (r *Reactor) handleFollowerStopCheckpointResult(rc *runtimeChannel, result worker.Result) {
	now := time.Now()
	rc.lifecycle.checkpoint.inflight = false
	rc.lifecycle.checkpoint.opID = 0
	err := result.Err
	if err == nil && result.StoreCheckpoint == nil {
		err = ch.ErrInvalidConfig
	}
	if err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.checkpoint.retryAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = err
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.replication.backoff = 0
	rc.replication.lastError = nil
	rc.lifecycle.checkpoint.retryAt = time.Time{}
	rc.lifecycle.stage = lifecycleFollowerStoppedAcking
	r.trySubmitStoppedAck(rc, now)
	r.scheduleReplicationFromState(rc, now)
}

func (r *Reactor) handleLeaderCheckpointResult(rc *runtimeChannel, result worker.Result) {
	if rc == nil || rc.state == nil || !rc.lifecycle.checkpoint.inflight || result.Fence.OpID != rc.lifecycle.checkpoint.opID {
		return
	}
	activityVersion := rc.lifecycle.checkpoint.version
	now := time.Now()
	rc.lifecycle.checkpoint.inflight = false
	rc.lifecycle.checkpoint.opID = 0
	rc.lifecycle.checkpoint.version = 0
	rc.lifecycle.finalCheck.inflight = false
	rc.lifecycle.finalCheck.version = 0
	rc.lifecycle.finalCheck.queued = false
	err := result.Err
	if err == nil && result.StoreCheckpoint == nil {
		err = ch.ErrInvalidConfig
	}
	if err != nil {
		rc.lifecycle.stage = lifecycleLeaderCheckpointing
		rc.lifecycle.checkpoint.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.lifecycle.checkpoint.retryAt = time.Time{}
	if activityVersion != rc.lifecycle.version ||
		rc.state.Role != ch.RoleLeader ||
		rc.state.HW < rc.state.LEO ||
		!r.allFollowersStopped(rc) ||
		r.hasPendingRuntimeWork(rc) {
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.lifecycle.stage = lifecycleLeaderReadyToEvict
	rc.lifecycle.finalCheck.inflight = true
	rc.lifecycle.finalCheck.version = activityVersion
	r.submitLeaderEvictReady(rc, now, r.currentAppendSubmitSeq(rc.state.Key))
}
