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
	LastAppendAt    time.Time
	ActivityVersion uint64
	Phase           lifecyclePhase
}

// followerLifecycle tracks leader-visible follower runtime state that is not part of the pure machine progress.
type followerLifecycle struct {
	Match              uint64
	LastPullAt         time.Time
	NextExpectedPullAt time.Time
	LastHintVersion    uint64
	HintInflight       bool
	HintRetryAt        time.Time
	Parked             bool
	Stopped            bool
	StopAckVersion     uint64
}

func (r *Reactor) markAppendActivity(rc *runtimeChannel, now time.Time) {
	if rc == nil {
		return
	}
	rc.lifecycle.LastAppendAt = now
	rc.lifecycle.Phase = lifecycleHot
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
}

func (r *Reactor) tickAllLifecycle(now time.Time) {
	if r == nil {
		return
	}
	for _, rc := range r.channels {
		r.tickLifecycle(rc, now)
	}
}

func (r *Reactor) followerNeedsImmediateProgress(rc *runtimeChannel, follower *followerLifecycle) bool {
	if rc == nil || rc.state == nil || follower == nil || follower.Match >= rc.state.LEO {
		return false
	}
	return follower.Parked || follower.Stopped || follower.LastPullAt.IsZero()
}

func (r *Reactor) trySubmitPullHint(rc *runtimeChannel, node ch.NodeID, follower *followerLifecycle, reason transport.PullHintReason, now time.Time) {
	if rc == nil || rc.state == nil || follower == nil || follower.HintInflight {
		return
	}
	version := rc.lifecycle.ActivityVersion
	if version == 0 {
		version = rc.state.LEO
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
		return
	}
	follower.HintInflight = true
	follower.HintRetryAt = time.Time{}
	follower.LastHintVersion = version
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
	if result.Err == nil {
		return
	}
	now := time.Now()
	r.observePullHintDropped(rc.state.Key, inflight.follower, result.Err)
	if inflight.activityVersion == rc.lifecycle.ActivityVersion && r.followerNeedsImmediateProgress(rc, follower) {
		follower.HintRetryAt = now.Add(r.cfg.PullHintRetryInterval)
	}
}
