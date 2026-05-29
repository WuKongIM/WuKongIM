package reactor

import (
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// channelLifecycle tracks leader-owned activity and idle eviction state for one runtime channel.
type channelLifecycle struct {
	// LoadedAt records when runtime state was created without counting as Append activity.
	LoadedAt        time.Time
	LastAppendAt    time.Time
	ActivityVersion uint64
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
	// CheckpointReadyQueued records a normal-priority final eviction recheck already in the mailbox.
	CheckpointReadyQueued bool
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
	// HintInflightOpID fences the PullHint RPC currently allowed to clear HintInflight.
	HintInflightOpID ch.OpID
	HintRetryAt      time.Time
	Parked           bool
	Stopped          bool
	StopAckVersion   uint64
	// StopOffered records that PullControlStop was returned even when the activity version is zero.
	StopOffered bool
	// StopOfferedVersion records the activity version last returned with PullControlStop.
	StopOfferedVersion uint64
}

func (r *Reactor) markAppendActivity(rc *runtimeChannel, now time.Time) {
	if rc == nil {
		return
	}
	rc.lifecycle.LastAppendAt = now
	rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleServing
	r.scheduleLifecycleFromState(rc, now)
}

func (r *Reactor) cancelLeaderEvictionForAppend(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	rc.lifecycle.LastAppendAt = now
	rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleServing
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
	rc.lifecycle.CheckpointReadyQueued = false
	rc.lifecycle.CheckpointRetryAt = time.Time{}
}

func retireFollowerPullHints(rc *runtimeChannel, node ch.NodeID) {
	if rc == nil {
		return
	}
	for opID, inflight := range rc.pullHintInflight {
		if inflight.follower == node {
			delete(rc.pullHintInflight, opID)
		}
	}
	if follower := rc.followers[node]; follower != nil {
		follower.HintInflight = false
		follower.HintInflightOpID = 0
		follower.PendingHintVersion = 0
		follower.HintRetryAt = time.Time{}
	}
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

func leaderIdleSince(rc *runtimeChannel) time.Time {
	if rc == nil {
		return time.Time{}
	}
	if !rc.lifecycle.LastAppendAt.IsZero() {
		return rc.lifecycle.LastAppendAt
	}
	return rc.lifecycle.LoadedAt
}

func (r *Reactor) currentAppendSubmitSeq(key ch.ChannelKey) uint64 {
	r.submitMu.Lock()
	defer r.submitMu.Unlock()
	return r.appendSubmitSeqs[key]
}

func (r *Reactor) bumpAppendSubmitSeqLocked(key ch.ChannelKey) uint64 {
	if r.appendSubmitSeqs == nil {
		r.appendSubmitSeqs = make(map[ch.ChannelKey]uint64)
	}
	r.appendSubmitSeqs[key]++
	return r.appendSubmitSeqs[key]
}

func (r *Reactor) clearAppendSubmitStateLocked(key ch.ChannelKey) {
	if r.appendReservations[key] != 0 {
		return
	}
	delete(r.appendSubmitSeqs, key)
}

func (r *Reactor) clearAppendSubmitState(key ch.ChannelKey) {
	r.submitMu.Lock()
	r.clearAppendSubmitStateLocked(key)
	r.submitMu.Unlock()
}

func (r *Reactor) reserveAppend(key ch.ChannelKey) func() {
	if r == nil {
		return func() {}
	}
	r.submitMu.Lock()
	r.bumpAppendSubmitSeqLocked(key)
	if r.appendReservations == nil {
		r.appendReservations = make(map[ch.ChannelKey]int)
	}
	r.appendReservations[key]++
	r.submitMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.submitMu.Lock()
			if r.appendReservations[key] <= 1 {
				delete(r.appendReservations, key)
			} else {
				r.appendReservations[key]--
			}
			r.submitMu.Unlock()
		})
	}
}
