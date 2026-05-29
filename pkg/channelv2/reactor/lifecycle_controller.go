package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// lifecycleStage describes stop and eviction progress for the local runtime role.
type lifecycleStage uint8

const (
	lifecycleLive lifecycleStage = iota + 1
	lifecycleLeaderStoppingFollowers
	lifecycleLeaderCheckpointing
	lifecycleLeaderReadyToEvict
	lifecycleFollowerCheckpointing
	lifecycleFollowerStoppedAcking
	lifecycleFollowerReadyToEvict
)

// lifecycleEffect fences one asynchronous lifecycle side effect.
type lifecycleEffect struct {
	inflight bool
	opID     ch.OpID
	version  uint64
	retryAt  time.Time
	queued   bool
}

func (e lifecycleEffect) active() bool {
	return e.inflight || e.opID != 0 || e.queued || !e.retryAt.IsZero()
}

func (e *lifecycleEffect) reset() {
	*e = lifecycleEffect{}
}

// lifecycleFollower is leader-visible lifecycle state for a remote replica.
type lifecycleFollower struct {
	match              uint64
	lastPullAt         time.Time
	nextExpectedPullAt time.Time
	lastHintVersion    uint64
	pendingHintVersion uint64
	hint               lifecycleEffect
	parked             bool
	// stopOfferedZero preserves stop offers made before a non-zero activity version exists.
	stopOfferedZero    bool
	stopOfferedVersion uint64
	// stoppedZero preserves stopped ACKs accepted before a non-zero activity version exists.
	stoppedZero    bool
	stoppedVersion uint64
}

func (f lifecycleFollower) caughtUp(leo uint64) bool {
	return f.match >= leo
}

func (f lifecycleFollower) stopOffered(version uint64) bool {
	if version == 0 {
		return f.stopOfferedZero
	}
	return f.stopOfferedVersion == version
}

func (f lifecycleFollower) stopped(version uint64) bool {
	if version == 0 {
		return f.stoppedZero
	}
	return f.stoppedVersion == version
}

func (f *lifecycleFollower) resetStop() {
	f.stopOfferedZero = false
	f.stopOfferedVersion = 0
	f.stoppedZero = false
	f.stoppedVersion = 0
}

func (f *lifecycleFollower) resetHint() {
	f.lastHintVersion = 0
	f.pendingHintVersion = 0
	f.hint.reset()
}

// followerStopState is follower-local state for an accepted PullControlStop.
type followerStopState struct {
	accepted  bool
	version   uint64
	leaderLEO uint64
	leaderHW  uint64
}

// lifecyclePullHintInflight records enough information to finish a PullHint worker result.
type lifecyclePullHintInflight struct {
	follower ch.NodeID
	version  uint64
	reason   transport.PullHintReason
}

// channelRuntimeLifecycle owns stop, checkpoint, stopped ACK, hint, and eviction state.
type channelRuntimeLifecycle struct {
	stage lifecycleStage

	loadedAt     time.Time
	lastAppendAt time.Time
	version      uint64

	checkpoint lifecycleEffect
	stoppedAck lifecycleEffect
	finalCheck lifecycleEffect

	followerStop followerStopState

	followers        map[ch.NodeID]*lifecycleFollower
	pullHintInflight map[ch.OpID]lifecyclePullHintInflight
	nextDue          time.Time
}

func newChannelRuntimeLifecycle(now time.Time, version uint64) channelRuntimeLifecycle {
	return channelRuntimeLifecycle{
		stage:     lifecycleLive,
		loadedAt:  now,
		version:   version,
		followers: make(map[ch.NodeID]*lifecycleFollower),
	}
}

func (lc *channelRuntimeLifecycle) idleSince() time.Time {
	if lc == nil {
		return time.Time{}
	}
	if !lc.lastAppendAt.IsZero() {
		return lc.lastAppendAt
	}
	return lc.loadedAt
}

func (lc *channelRuntimeLifecycle) markAppend(now time.Time, version uint64) {
	if lc == nil {
		return
	}
	lc.stage = lifecycleLive
	lc.lastAppendAt = now
	if version > lc.version {
		lc.version = version
	}
	lc.checkpoint.reset()
	lc.finalCheck.reset()
	for _, follower := range lc.followers {
		if follower != nil && follower.match < version {
			follower.resetStop()
		}
	}
}

func (lc *channelRuntimeLifecycle) resetForMeta(now time.Time, version uint64) {
	if lc == nil {
		return
	}
	lc.stage = lifecycleLive
	lc.loadedAt = now
	lc.lastAppendAt = time.Time{}
	lc.version = version
	lc.checkpoint.reset()
	lc.stoppedAck.reset()
	lc.finalCheck.reset()
	lc.followerStop = followerStopState{}
	lc.pullHintInflight = nil
	lc.nextDue = time.Time{}
	for _, follower := range lc.followers {
		if follower == nil {
			continue
		}
		follower.resetStop()
		follower.resetHint()
		follower.parked = false
		follower.nextExpectedPullAt = time.Time{}
	}
}
