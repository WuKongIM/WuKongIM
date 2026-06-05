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
	pendingQuorum := hasPendingQuorumAppendWaiter(rc)
	for node, follower := range rc.lifecycle.followers {
		if follower == nil {
			continue
		}
		if pendingQuorum && followerResumeRetryDueForPendingQuorum(follower, rc.state.LEO, now, r.cfg.PullHintRetryInterval) && !follower.hint.inflight {
			if follower.hint.retryAt.IsZero() || now.Before(follower.hint.retryAt) {
				follower.hint.retryAt = now
			}
		}
		immediate := rc.lifecycle.followerNeedsImmediateProgress(node, rc.state.LEO)
		retryDue := rc.lifecycle.followerNeedsHintRetry(node, rc.state.LEO) && !now.Before(follower.hint.retryAt)
		if !immediate && !retryDue {
			continue
		}
		reason := transport.PullHintReasonAppend
		if !immediate && retryDue {
			reason = transport.PullHintReasonResume
		}
		r.trySubmitPullHint(rc, node, follower, reason, now)
	}
}

func (r *Reactor) scheduleLaggingFollowerResumeHints(rc *runtimeChannel, now time.Time) {
	if r == nil || rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader || !hasPendingQuorumAppendWaiter(rc) {
		return
	}
	r.syncFollowerMatches(rc)
	scheduled := false
	for _, follower := range rc.lifecycle.followers {
		if follower == nil || follower.match >= rc.state.LEO || follower.hint.inflight {
			continue
		}
		if followerResumeRetryDueForPendingQuorum(follower, rc.state.LEO, now, r.cfg.PullHintRetryInterval) {
			if follower.hint.retryAt.IsZero() || now.Before(follower.hint.retryAt) {
				follower.hint.retryAt = now
				scheduled = true
			}
			continue
		}
		if !follower.hint.retryAt.IsZero() {
			continue
		}
		follower.hint.retryAt = retryDue(now, r.cfg.PullHintRetryInterval)
		scheduled = true
	}
	if scheduled {
		r.scheduleLifecycleFromState(rc, now)
	}
}

func hasPendingQuorumAppendWaiter(rc *runtimeChannel) bool {
	if rc == nil || rc.state == nil || len(rc.state.PendingAppends) == 0 {
		return false
	}
	for _, waiter := range rc.state.PendingAppends {
		if waiter != nil && waiter.CommitMode == ch.CommitModeQuorum && rc.state.HW < waiter.Target {
			return true
		}
	}
	return false
}

// followerResumeRetryDueForPendingQuorum reports whether stale follower silence is due for another wakeup.
func followerResumeRetryDueForPendingQuorum(follower *lifecycleFollower, leaderLEO uint64, now time.Time, interval time.Duration) bool {
	if follower == nil || follower.match >= leaderLEO || follower.lastPullAt.IsZero() {
		return false
	}
	base := follower.lastPullAt
	if !follower.lastHintAt.IsZero() && !follower.lastHintAt.Before(follower.lastPullAt) {
		base = follower.lastHintAt
	}
	if interval <= 0 {
		return true
	}
	return !now.Before(base.Add(interval))
}

func (r *Reactor) nudgePendingQuorumFollowers(rc *runtimeChannel, now time.Time) {
	if r == nil || rc == nil || rc.state == nil || !hasPendingQuorumAppendWaiter(rc) {
		return
	}
	r.sendPullHintsForAppend(rc, now)
	r.scheduleLaggingFollowerResumeHints(rc, now)
}

func (r *Reactor) tickLifecycleController(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	defer r.scheduleLifecycleFromState(rc, now)
	r.syncFollowerMatches(rc)
	for node, follower := range rc.lifecycle.followers {
		if follower == nil || follower.hint.inflight || follower.hint.retryAt.IsZero() || now.Before(follower.hint.retryAt) {
			continue
		}
		if !rc.lifecycle.followerNeedsImmediateProgress(node, rc.state.LEO) && !rc.lifecycle.followerNeedsHintRetry(node, rc.state.LEO) {
			follower.hint.retryAt = time.Time{}
			continue
		}
		r.trySubmitPullHint(rc, node, follower, transport.PullHintReasonResume, now)
	}
	r.driveLifecycle(rc, lifecycleEvent{kind: lifecycleEventIdleTick, now: now})
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

func (r *Reactor) submitLeaderEvictReady(rc *runtimeChannel, now time.Time, appendSeq uint64) {
	if r == nil || rc == nil || rc.state == nil || rc.lifecycle.finalCheck.queued {
		return
	}
	event := Event{Kind: EventLeaderEvictReady, Key: rc.state.Key, LeaderEvictAppendSeq: appendSeq}
	if err := r.submitMailboxWithResult(PriorityNormal, event); err != nil {
		rc.lifecycle.checkpoint.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.lifecycle.finalCheck.queued = true
	rc.lifecycle.checkpoint.retryAt = time.Time{}
	rc.lifecycle.stage = lifecycleLeaderReadyToEvict
}

func (r *Reactor) handleLeaderEvictReady(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		return
	}
	rc.lifecycle.finalCheck.queued = false
	r.driveLifecycle(rc, lifecycleEvent{kind: lifecycleEventFinalEvictReady, now: time.Now(), appendSeqObserved: event.LeaderEvictAppendSeq})
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
		r.observePullHintResult(reason, "err", err)
		r.observePullHintDropped(rc.state.Key, node, err)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.lifecycle.beginPullHint(node, opID, version, reason, now)
	r.observePullHintResult(reason, "submitted", nil)
	r.observePullHintSent(rc.state.Key, node, reason)
}

func (r *Reactor) handleRPCPullHintResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
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
		r.observePullHintResult(inflight.reason, "ok", nil)
		r.sendCurrentPullHintIfNeeded(rc, inflight.follower, follower, now)
		if !follower.hint.inflight && follower.match < rc.state.LEO && follower.hint.retryAt.IsZero() {
			follower.hint.retryAt = retryDue(now, r.cfg.PullHintRetryInterval)
		}
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	r.observePullHintResult(inflight.reason, "err", result.Err)
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
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
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
	r.driveLifecycle(rc, lifecycleEvent{kind: lifecycleEventStoreCheckpointDone, now: now, err: err})
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
		r.driveLifecycle(rc, lifecycleEvent{kind: lifecycleEventStoreCheckpointDone, now: now, err: err, activityVersion: activityVersion})
		return
	}
	r.driveLifecycle(rc, lifecycleEvent{kind: lifecycleEventStoreCheckpointDone, now: now, activityVersion: activityVersion})
}
