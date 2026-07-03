package reactor

import (
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *Reactor) markAppendActivity(rc *runtimeChannel, now time.Time) {
	if rc == nil {
		return
	}
	rc.lifecycle.lastAppendAt = now
	rc.lifecycle.stage = lifecycleLive
	r.scheduleLifecycleFromState(rc, now)
}

func (r *Reactor) cancelLeaderEvictionForAppend(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	rc.lifecycle.lastAppendAt = now
	rc.lifecycle.stage = lifecycleLive
	resetLeaderCheckpointLifecycle(rc)
	r.scheduleLifecycleFromState(rc, now)
}

// resetLeaderCheckpointLifecycle clears only leader eviction checkpoint bookkeeping.
func resetLeaderCheckpointLifecycle(rc *runtimeChannel) {
	if rc == nil {
		return
	}
	rc.lifecycle.checkpoint.inflight = false
	rc.lifecycle.checkpoint.opID = 0
	rc.lifecycle.checkpoint.version = 0
	rc.lifecycle.finalCheck.inflight = false
	rc.lifecycle.finalCheck.version = 0
	rc.lifecycle.finalCheck.queued = false
	rc.lifecycle.checkpoint.retryAt = time.Time{}
}

func retireFollowerPullHints(rc *runtimeChannel, node ch.NodeID) {
	if rc == nil {
		return
	}
	rc.lifecycle.retirePullHints(node)
}

func (r *Reactor) syncLeaderFollowers(rc *runtimeChannel) {
	if rc == nil {
		return
	}
	if rc.state == nil || rc.state.Role != ch.RoleLeader {
		rc.lifecycle.followers = nil
		rc.lifecycle.pullHintInflight = nil
		return
	}
	var current map[ch.NodeID]struct{}
	for _, replica := range rc.state.Replicas {
		if replica == r.cfg.LocalNode {
			continue
		}
		if current == nil {
			current = make(map[ch.NodeID]struct{}, len(rc.state.Replicas)-1)
		}
		if rc.lifecycle.followers == nil {
			rc.lifecycle.followers = make(map[ch.NodeID]*lifecycleFollower)
		}
		current[replica] = struct{}{}
		progress := rc.state.Progress[replica]
		follower := rc.lifecycle.followers[replica]
		if follower == nil {
			follower = &lifecycleFollower{match: progress.Match}
			rc.lifecycle.followers[replica] = follower
			continue
		}
		if progress.Match > follower.match {
			follower.match = progress.Match
		}
	}
	if len(current) == 0 {
		rc.lifecycle.followers = nil
		rc.lifecycle.pullHintInflight = nil
		return
	}
	for node := range rc.lifecycle.followers {
		if _, ok := current[node]; !ok {
			delete(rc.lifecycle.followers, node)
		}
	}
}

func (r *Reactor) syncFollowerMatches(rc *runtimeChannel) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader {
		return
	}
	r.syncLeaderFollowers(rc)
	for node, follower := range rc.lifecycle.followers {
		progress := rc.state.Progress[node]
		if follower != nil && progress.Match > follower.match {
			follower.match = progress.Match
		}
	}
}

func leaderIdleSince(rc *runtimeChannel) time.Time {
	if rc == nil {
		return time.Time{}
	}
	return rc.lifecycle.idleSince()
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
