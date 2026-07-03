package runtime

import (
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

type commitNotifyState struct {
	timer        *time.Timer
	timerVersion uint64
}

// scheduleChannelCommit defers commit-only HW wakeups so a near-term append can
// piggyback the HW on a data response instead of sending a separate HW-only item.
func (r *runtime) scheduleChannelCommit(key core.ChannelKey) {
	if r.isClosed() {
		return
	}
	if r.cfg.LongPollHWOnlyNotifyDelay <= 0 {
		go r.onChannelCommit(key)
		return
	}

	r.commitNotifyMu.Lock()
	if _, exists := r.pendingCommitNotify[key]; exists {
		r.commitNotifyMu.Unlock()
		return
	}
	state := &commitNotifyState{timerVersion: 1}
	version := state.timerVersion
	state.timer = time.AfterFunc(r.cfg.LongPollHWOnlyNotifyDelay, func() {
		r.firePendingChannelCommit(key, version)
	})
	r.pendingCommitNotify[key] = state
	r.commitNotifyMu.Unlock()
}

// firePendingChannelCommit publishes the coalesced HW-only wake if it was not
// canceled by a newer data-ready event or channel removal.
func (r *runtime) firePendingChannelCommit(key core.ChannelKey, version uint64) {
	if r.isClosed() {
		return
	}

	r.commitNotifyMu.Lock()
	state, ok := r.pendingCommitNotify[key]
	if !ok || state.timerVersion != version {
		r.commitNotifyMu.Unlock()
		return
	}
	delete(r.pendingCommitNotify, key)
	r.commitNotifyMu.Unlock()

	r.onChannelCommit(key)
}

// cancelPendingChannelCommit drops a pending HW-only wake when data can carry
// the same committed frontier to followers.
func (r *runtime) cancelPendingChannelCommit(key core.ChannelKey) {
	stopTimers(r.clearPendingChannelCommit(key))
}

// clearPendingChannelCommit removes one channel's pending commit timer and
// returns it so the caller can stop timers outside the debounce lock.
func (r *runtime) clearPendingChannelCommit(key core.ChannelKey) []*time.Timer {
	r.commitNotifyMu.Lock()
	defer r.commitNotifyMu.Unlock()

	state, ok := r.pendingCommitNotify[key]
	if !ok {
		return nil
	}
	state.timerVersion++
	var timers []*time.Timer
	if state.timer != nil {
		timers = append(timers, state.timer)
		state.timer = nil
	}
	delete(r.pendingCommitNotify, key)
	return timers
}

// clearAllPendingChannelCommits removes all commit debounce timers during
// runtime shutdown and returns them for lock-free stopping.
func (r *runtime) clearAllPendingChannelCommits() []*time.Timer {
	r.commitNotifyMu.Lock()
	defer r.commitNotifyMu.Unlock()

	if len(r.pendingCommitNotify) == 0 {
		return nil
	}
	timers := make([]*time.Timer, 0, len(r.pendingCommitNotify))
	for key, state := range r.pendingCommitNotify {
		state.timerVersion++
		if state.timer != nil {
			timers = append(timers, state.timer)
			state.timer = nil
		}
		delete(r.pendingCommitNotify, key)
	}
	return timers
}
