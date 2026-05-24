package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

type lifecyclePhase uint8

const (
	lifecycleHot lifecyclePhase = iota + 1
	lifecycleCooling
	lifecycleStoppingFollowers
	lifecycleEvictingLeader
)

// channelLifecycle tracks leader-owned activity and idle eviction state for one runtime channel.
type channelLifecycle struct {
	// LoadedAt records when runtime state was created without counting as Append activity.
	LoadedAt        time.Time
	LastAppendAt    time.Time
	ActivityVersion uint64
	Phase           lifecyclePhase
	// CheckpointInflight records a leader eviction checkpoint that must complete before runtime deletion.
	CheckpointInflight bool
	// CheckpointOpID fences the leader eviction checkpoint worker result.
	CheckpointOpID ch.OpID
	// CheckpointActivityVersion is the activity version that requested the leader checkpoint.
	CheckpointActivityVersion uint64
	// CheckpointReady records a completed leader checkpoint awaiting a normal-priority eviction recheck.
	CheckpointReady bool
	// CheckpointReadyActivityVersion fences the completed checkpoint to the activity that produced it.
	CheckpointReadyActivityVersion uint64
	// CheckpointRetryAt is the next time to retry a failed or backpressured leader checkpoint.
	CheckpointRetryAt time.Time
}

// followerLifecycle tracks leader-visible follower runtime state that is not part of the pure machine progress.
type followerLifecycle struct {
	Match              uint64
	LastPullAt         time.Time
	NextExpectedPullAt time.Time
	LastHintVersion    uint64
	PendingHintVersion uint64
	HintInflight       bool
	HintRetryAt        time.Time
	Parked             bool
	Stopped            bool
	StopAckVersion     uint64
	// StopOfferedVersion records the activity version last returned with PullControlStop.
	StopOfferedVersion uint64
}

func (r *Reactor) markAppendActivity(rc *runtimeChannel, now time.Time) {
	if rc == nil {
		return
	}
	rc.lifecycle.LastAppendAt = now
	rc.lifecycle.Phase = lifecycleHot
	r.scheduleLifecycleFromState(rc, now)
}

func (r *Reactor) cancelLeaderEvictionForAppend(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	rc.lifecycle.LastAppendAt = now
	rc.lifecycle.Phase = lifecycleHot
	resetLeaderCheckpointLifecycle(rc)
	r.scheduleLifecycleFromState(rc, now)
}

// resetLeaderCheckpointLifecycle clears only leader eviction checkpoint bookkeeping.
func resetLeaderCheckpointLifecycle(rc *runtimeChannel) {
	if rc == nil {
		return
	}
	rc.lifecycle.CheckpointInflight = false
	rc.lifecycle.CheckpointOpID = 0
	rc.lifecycle.CheckpointActivityVersion = 0
	rc.lifecycle.CheckpointReady = false
	rc.lifecycle.CheckpointReadyActivityVersion = 0
	rc.lifecycle.CheckpointRetryAt = time.Time{}
}

func (r *Reactor) syncLeaderFollowers(rc *runtimeChannel) {
	if rc == nil {
		return
	}
	if rc.state == nil || rc.state.Role != ch.RoleLeader {
		rc.followers = nil
		rc.pullHintInflight = nil
		return
	}
	if rc.followers == nil {
		rc.followers = make(map[ch.NodeID]*followerLifecycle)
	}
	current := make(map[ch.NodeID]struct{}, len(rc.state.Replicas))
	for _, replica := range rc.state.Replicas {
		if replica == r.cfg.LocalNode {
			continue
		}
		current[replica] = struct{}{}
		progress := rc.state.Progress[replica]
		follower := rc.followers[replica]
		if follower == nil {
			follower = &followerLifecycle{Match: progress.Match}
			rc.followers[replica] = follower
			continue
		}
		if progress.Match > follower.Match {
			follower.Match = progress.Match
		}
	}
	for node := range rc.followers {
		if _, ok := current[node]; !ok {
			delete(rc.followers, node)
		}
	}
}

func (r *Reactor) syncFollowerMatches(rc *runtimeChannel) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	r.syncLeaderFollowers(rc)
	for node, follower := range rc.followers {
		progress := rc.state.Progress[node]
		if progress.Match > follower.Match {
			follower.Match = progress.Match
		}
	}
}

func (r *Reactor) sendPullHintsForAppend(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	r.syncFollowerMatches(rc)
	for node, follower := range rc.followers {
		if !r.followerNeedsImmediateProgress(rc, follower) {
			continue
		}
		r.trySubmitPullHint(rc, node, follower, transport.PullHintReasonAppend, now)
	}
}

func (r *Reactor) tickLifecycle(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	defer r.scheduleLifecycleFromState(rc, now)
	r.syncFollowerMatches(rc)
	for node, follower := range rc.followers {
		if follower == nil || follower.HintInflight || follower.HintRetryAt.IsZero() || now.Before(follower.HintRetryAt) {
			continue
		}
		if !r.followerNeedsImmediateProgress(rc, follower) {
			follower.HintRetryAt = time.Time{}
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
	rc.pullHintInflight = nil
	for _, follower := range rc.followers {
		if follower == nil {
			continue
		}
		follower.LastHintVersion = 0
		follower.PendingHintVersion = 0
		follower.HintInflight = false
		follower.HintRetryAt = time.Time{}
	}
}

func (r *Reactor) followerNeedsImmediateProgress(rc *runtimeChannel, follower *followerLifecycle) bool {
	if rc == nil || rc.state == nil || follower == nil || follower.Match >= rc.state.LEO {
		return false
	}
	return follower.Parked || follower.Stopped || follower.StopOfferedVersion != 0 || follower.LastPullAt.IsZero()
}

func (r *Reactor) leaderCanOfferStop(rc *runtimeChannel, now time.Time) bool {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader || rc.state.Status != ch.StatusActive {
		return false
	}
	if !r.leaderIdleExpired(rc, now) || r.hasPendingRuntimeWork(rc) {
		return false
	}
	return r.allFollowersCaughtUp(rc)
}

func (r *Reactor) leaderIdleExpired(rc *runtimeChannel, now time.Time) bool {
	idleSince := leaderIdleSince(rc)
	if idleSince.IsZero() {
		return false
	}
	return !now.Before(idleSince.Add(r.cfg.IdleEvictAfter))
}

func leaderIdleSince(rc *runtimeChannel) time.Time {
	if rc == nil {
		return time.Time{}
	}
	if !rc.lifecycle.LastAppendAt.IsZero() {
		return rc.lifecycle.LastAppendAt
	}
	return rc.lifecycle.LoadedAt
}

func (r *Reactor) allFollowersCaughtUp(rc *runtimeChannel) bool {
	if rc == nil || rc.state == nil {
		return false
	}
	r.syncLeaderFollowers(rc)
	for _, replica := range rc.state.Replicas {
		if replica == r.cfg.LocalNode {
			continue
		}
		follower := rc.followers[replica]
		if follower == nil || follower.Match < rc.state.LEO {
			return false
		}
	}
	return true
}

func (r *Reactor) allFollowersStopped(rc *runtimeChannel) bool {
	if rc == nil || rc.state == nil {
		return false
	}
	r.syncLeaderFollowers(rc)
	for _, replica := range rc.state.Replicas {
		if replica == r.cfg.LocalNode {
			continue
		}
		follower := rc.followers[replica]
		if follower == nil || !follower.Stopped || follower.StopAckVersion != rc.lifecycle.ActivityVersion || follower.Match < rc.state.LEO {
			return false
		}
	}
	return true
}

func (r *Reactor) hasPendingRuntimeWork(rc *runtimeChannel) bool {
	if rc == nil || rc.state == nil {
		return true
	}
	if len(rc.waiters) != 0 || len(rc.fetchWaiters) != 0 || len(rc.pullWaiters) != 0 {
		return true
	}
	if len(rc.appendQ.pending) != 0 || rc.appendQ.storeBlocked || rc.appendInflight != nil || rc.appendStoreBlocked || !rc.appendRetryAt.IsZero() {
		return true
	}
	if len(rc.appendCancelContexts) != 0 || len(rc.appendTimings) != 0 || len(rc.pullHintInflight) != 0 {
		return true
	}
	if rc.state.InflightAppend != nil || len(rc.state.PendingAppends) != 0 {
		return true
	}
	replication := rc.replication
	return replication.pullInflight ||
		replication.ackInflight ||
		replication.pendingAck ||
		replication.pendingPull != nil ||
		replication.applyBlocked ||
		replication.applyOpID != 0 ||
		replication.checkpointInflight ||
		replication.checkpointOpID != 0 ||
		!replication.nextCheckpointAt.IsZero() ||
		!replication.nextAckAt.IsZero()
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
	if rc.lifecycle.CheckpointReady {
		if rc.lifecycle.CheckpointReadyActivityVersion != rc.lifecycle.ActivityVersion {
			rc.lifecycle.CheckpointReady = false
			rc.lifecycle.CheckpointReadyActivityVersion = 0
			return
		}
		if !r.evictRuntimeChannel(rc.state.Key, rc, "leader idle checkpoint") {
			rc.lifecycle.CheckpointReady = false
			rc.lifecycle.CheckpointReadyActivityVersion = 0
			rc.lifecycle.CheckpointRetryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		}
		return
	}
	if rc.lifecycle.CheckpointInflight {
		return
	}
	if !rc.lifecycle.CheckpointRetryAt.IsZero() && now.Before(rc.lifecycle.CheckpointRetryAt) {
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreCheckpoint(context.Background(), rc.state.ID, fence, ch.Checkpoint{HW: rc.state.LEO}); err != nil {
		rc.lifecycle.CheckpointRetryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.lifecycle.Phase = lifecycleEvictingLeader
	rc.lifecycle.CheckpointInflight = true
	rc.lifecycle.CheckpointOpID = opID
	rc.lifecycle.CheckpointActivityVersion = rc.lifecycle.ActivityVersion
	rc.lifecycle.CheckpointRetryAt = time.Time{}
}

func (r *Reactor) trySubmitPullHint(rc *runtimeChannel, node ch.NodeID, follower *followerLifecycle, reason transport.PullHintReason, now time.Time) {
	if rc == nil || rc.state == nil || follower == nil {
		return
	}
	version := rc.lifecycle.ActivityVersion
	if version == 0 {
		version = rc.state.LEO
	}
	if follower.HintInflight {
		if follower.LastHintVersion < version {
			follower.PendingHintVersion = version
		}
		return
	}
	if follower.LastHintVersion == version && !follower.HintRetryAt.IsZero() && now.Before(follower.HintRetryAt) {
		return
	}
	if follower.LastHintVersion == version && follower.HintRetryAt.IsZero() {
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
		follower.HintInflight = false
		follower.HintRetryAt = now.Add(r.cfg.PullHintRetryInterval)
		r.observePullHintDropped(rc.state.Key, node, err)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	follower.HintInflight = true
	follower.HintRetryAt = time.Time{}
	follower.LastHintVersion = version
	follower.PendingHintVersion = 0
	if rc.pullHintInflight == nil {
		rc.pullHintInflight = make(map[ch.OpID]pullHintInflight)
	}
	rc.pullHintInflight[opID] = pullHintInflight{follower: node, activityVersion: version, reason: reason}
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
	inflight, ok := rc.pullHintInflight[result.Fence.OpID]
	if !ok {
		return
	}
	delete(rc.pullHintInflight, result.Fence.OpID)
	follower := rc.followers[inflight.follower]
	if follower == nil {
		return
	}
	follower.HintInflight = false
	now := time.Now()
	if result.Err == nil {
		r.sendCurrentPullHintIfNeeded(rc, inflight.follower, follower, now)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	r.observePullHintDropped(rc.state.Key, inflight.follower, result.Err)
	if r.followerNeedsImmediateProgress(rc, follower) {
		follower.PendingHintVersion = rc.lifecycle.ActivityVersion
		follower.HintRetryAt = now.Add(r.cfg.PullHintRetryInterval)
	}
	r.scheduleLifecycleFromState(rc, now)
}

func (r *Reactor) sendCurrentPullHintIfNeeded(rc *runtimeChannel, node ch.NodeID, follower *followerLifecycle, now time.Time) {
	if rc == nil || rc.state == nil || follower == nil || follower.LastHintVersion >= rc.lifecycle.ActivityVersion {
		return
	}
	if !r.followerNeedsImmediateProgress(rc, follower) {
		follower.PendingHintVersion = 0
		return
	}
	follower.PendingHintVersion = rc.lifecycle.ActivityVersion
	r.trySubmitPullHint(rc, node, follower, transport.PullHintReasonAppend, now)
}
