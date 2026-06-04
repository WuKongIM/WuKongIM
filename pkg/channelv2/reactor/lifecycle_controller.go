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

// lifecycleEventKind identifies one runtime lifecycle input.
type lifecycleEventKind uint8

const (
	lifecycleEventMetaFence lifecycleEventKind = iota + 1
	lifecycleEventAppendAdmitted
	lifecycleEventAppendStored
	lifecycleEventIdleTick
	lifecycleEventLeaderStoppedAck
	lifecycleEventFollowerStopControl
	lifecycleEventStoreCheckpointDone
	lifecycleEventStoppedAckDone
	lifecycleEventFinalEvictReady
	lifecycleEventPullHintDone
)

// lifecycleEvent carries the small amount of data needed by lifecycle planners.
type lifecycleEvent struct {
	kind              lifecycleEventKind
	now               time.Time
	err               error
	activityVersion   uint64
	appendSeqObserved uint64
}

// lifecycleActionKind identifies one reactor side effect requested by lifecycle planning.
type lifecycleActionKind uint8

const (
	lifecycleActionScheduleLifecycle lifecycleActionKind = iota + 1
	lifecycleActionScheduleReplication
	lifecycleActionStartFollowerStopCheckpoint
	lifecycleActionSendStoppedAck
	lifecycleActionStartLeaderCheckpoint
	lifecycleActionQueueLeaderFinalRecheck
	lifecycleActionEvictRuntime
)

// lifecycleAction is a reactor-owned side effect chosen by pure lifecycle planning.
type lifecycleAction struct {
	kind      lifecycleActionKind
	due       time.Time
	appendSeq uint64
}

// RuntimeView is an immutable snapshot used by pure lifecycle guards.
type RuntimeView struct {
	Key             ch.ChannelKey
	Role            ch.Role
	Status          ch.Status
	LEO             uint64
	HW              uint64
	ActivityVersion uint64
	IdleSince       time.Time
	PendingWork     PendingWorkView
	Followers       []FollowerView
	AppendFence     AppendFenceView
}

// FollowerView is the leader-visible lifecycle state for one follower.
type FollowerView struct {
	Node           ch.NodeID
	Match          uint64
	Stopped        bool
	StopAckVersion uint64
	StopOffered    bool
}

// PendingWorkView summarizes transient runtime work that blocks eviction.
type PendingWorkView struct {
	Waiters              int
	PullWaiters          int
	AppendQueued         int
	AppendQueueBlocked   bool
	AppendInflight       bool
	AppendStoreBlocked   bool
	AppendRetryScheduled bool
	AppendCancelContexts int
	AppendTimings        int
	MachineAppendPending bool
	PullInflight         bool
	AckInflight          bool
	PendingPull          bool
	ApplyBlocked         bool
	ApplyInflight        bool
	CheckpointInflight   bool
	CheckpointRetry      bool
	AckRetry             bool
	LifecycleCheckpoint  bool
	LifecycleRetry       bool
}

// AppendFenceView summarizes append submission state used by final leader eviction.
type AppendFenceView struct {
	Reservations      uint64
	SubmitSeq         uint64
	ObservedSubmitSeq uint64
}

// AllFollowersCaughtUp reports whether every follower has reached the leader LEO.
func (v RuntimeView) AllFollowersCaughtUp() bool {
	for _, follower := range v.Followers {
		if follower.Match < v.LEO {
			return false
		}
	}
	return true
}

// AllFollowersStopped reports whether every follower stopped for this activity version.
func (v RuntimeView) AllFollowersStopped() bool {
	for _, follower := range v.Followers {
		if !follower.Stopped || follower.StopAckVersion != v.ActivityVersion || follower.Match < v.LEO {
			return false
		}
	}
	return true
}

// IdleExpired reports whether the runtime has been idle for at least idleAfter.
func (v RuntimeView) IdleExpired(now time.Time, idleAfter time.Duration) bool {
	if v.IdleSince.IsZero() || idleAfter <= 0 {
		return false
	}
	return !now.Before(v.IdleSince.Add(idleAfter))
}

// CanOfferFollowerStop reports whether the leader may offer stop control to followers.
func (v RuntimeView) CanOfferFollowerStop(now time.Time, idleAfter time.Duration) bool {
	if v.Role != ch.RoleLeader || v.Status != ch.StatusActive {
		return false
	}
	if v.HW < v.LEO || !v.IdleExpired(now, idleAfter) || v.HasPendingWork() {
		return false
	}
	return v.AllFollowersCaughtUp()
}

// HasPendingWork reports whether transient runtime work still blocks lifecycle progress.
func (v RuntimeView) HasPendingWork() bool {
	return v.PendingWork.hasPendingWork()
}

func (p PendingWorkView) hasPendingWork() bool {
	return p.Waiters != 0 ||
		p.PullWaiters != 0 ||
		p.AppendQueued != 0 ||
		p.AppendQueueBlocked ||
		p.AppendInflight ||
		p.AppendStoreBlocked ||
		p.AppendRetryScheduled ||
		p.AppendCancelContexts != 0 ||
		p.AppendTimings != 0 ||
		p.MachineAppendPending ||
		p.PullInflight ||
		p.AckInflight ||
		p.PendingPull ||
		p.ApplyBlocked ||
		p.ApplyInflight ||
		p.CheckpointInflight ||
		p.CheckpointRetry ||
		p.AckRetry ||
		p.LifecycleCheckpoint ||
		p.LifecycleRetry
}

// SafeToEvict reports whether the runtime has no pending work left before eviction.
func (v RuntimeView) SafeToEvict() bool {
	return !v.HasPendingWork()
}

func (v RuntimeView) followerStopBlocked() bool {
	p := v.PendingWork
	return p.PullInflight ||
		p.AckInflight ||
		p.PendingPull ||
		p.ApplyBlocked ||
		p.ApplyInflight ||
		p.CheckpointInflight
}

// planLeaderCheckpointEviction chooses leader checkpoint/recheck work from an immutable runtime view.
func planLeaderCheckpointEviction(lc channelRuntimeLifecycle, view RuntimeView, now time.Time, idleAfter time.Duration) []lifecycleAction {
	if view.Role != ch.RoleLeader || view.Status != ch.StatusActive || view.HW < view.LEO || !view.IdleExpired(now, idleAfter) {
		return nil
	}
	retryDue := !lc.checkpoint.retryAt.IsZero() && !now.Before(lc.checkpoint.retryAt)
	if !lc.checkpoint.retryAt.IsZero() && !retryDue {
		return nil
	}
	pending := view.PendingWork
	if retryDue {
		pending.LifecycleRetry = false
	}
	if !view.AllFollowersStopped() || pending.hasPendingWork() {
		return nil
	}
	if lc.finalCheck.inflight {
		if lc.finalCheck.version == lc.version && !lc.finalCheck.queued {
			return []lifecycleAction{{kind: lifecycleActionQueueLeaderFinalRecheck}}
		}
		return nil
	}
	if lc.checkpoint.inflight {
		return nil
	}
	return []lifecycleAction{{kind: lifecycleActionStartLeaderCheckpoint}}
}

// planFinalLeaderEviction chooses the final leader eviction effect after append fencing.
func planFinalLeaderEviction(view RuntimeView, now time.Time, retryInterval time.Duration) []lifecycleAction {
	if view.Role != ch.RoleLeader || view.Status != ch.StatusActive {
		return nil
	}
	if view.AppendFence.Reservations > 0 {
		return []lifecycleAction{{kind: lifecycleActionScheduleLifecycle, due: retryDue(now, retryInterval)}}
	}
	if view.AppendFence.ObservedSubmitSeq != view.AppendFence.SubmitSeq {
		return []lifecycleAction{{kind: lifecycleActionQueueLeaderFinalRecheck, appendSeq: view.AppendFence.SubmitSeq}}
	}
	if view.HW >= view.LEO && view.AllFollowersStopped() && view.SafeToEvict() {
		return []lifecycleAction{{kind: lifecycleActionEvictRuntime}}
	}
	return []lifecycleAction{{kind: lifecycleActionScheduleLifecycle}}
}

func retryDue(now time.Time, interval time.Duration) time.Time {
	if interval <= 0 {
		return now
	}
	return now.Add(interval)
}

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
		stage:    lifecycleLive,
		loadedAt: now,
		version:  version,
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

// offerStop records that the leader offered PullControlStop to a follower.
func (lc *channelRuntimeLifecycle) offerStop(node ch.NodeID) {
	if lc == nil {
		return
	}
	follower := lc.followers[node]
	if follower == nil {
		return
	}
	follower.stopOfferedVersion = lc.version
	follower.stopOfferedZero = lc.version == 0
}

// markFollowerStopped accepts a stopped ACK when it matches the current offer version.
func (lc *channelRuntimeLifecycle) markFollowerStopped(node ch.NodeID, version, match uint64) bool {
	if lc == nil || version != lc.version {
		return false
	}
	follower := lc.followers[node]
	if follower == nil {
		return false
	}
	if (follower.stopOfferedZero || follower.stopOfferedVersion != 0) && !follower.stopOffered(version) {
		return false
	}
	wasStopped := follower.stopped(version)
	follower.stoppedZero = version == 0
	follower.stoppedVersion = version
	follower.parked = false
	if match > follower.match {
		follower.match = match
	}
	lc.retirePullHints(node)
	return !wasStopped
}

// recordFollowerPull updates leader-visible state after observing a follower pull.
func (lc *channelRuntimeLifecycle) recordFollowerPull(node ch.NodeID, nextOffset, leo uint64, now time.Time) {
	if lc == nil {
		return
	}
	follower := lc.followers[node]
	if follower == nil {
		return
	}
	follower.lastPullAt = now
	follower.parked = false
	follower.stoppedZero = false
	follower.stoppedVersion = 0
	follower.nextExpectedPullAt = time.Time{}
	lc.retirePullHints(node)
	if nextOffset == 0 || nextOffset > leo+1 {
		return
	}
	match := nextOffset - 1
	if match > follower.match {
		follower.match = match
	}
}

// recordFollowerProgress advances leader-visible follower progress from ACK state.
func (lc *channelRuntimeLifecycle) recordFollowerProgress(node ch.NodeID, match, leaderLEO uint64) {
	if lc == nil {
		return
	}
	follower := lc.followers[node]
	if follower == nil {
		return
	}
	if match > follower.match {
		follower.match = match
	}
	if follower.match >= leaderLEO {
		lc.retirePullHints(node)
	}
}

// followerNeedsImmediateProgress reports whether a trailing follower should be woken now.
func (lc *channelRuntimeLifecycle) followerNeedsImmediateProgress(node ch.NodeID, leaderLEO uint64) bool {
	if lc == nil {
		return false
	}
	follower := lc.followers[node]
	if follower == nil || follower.match >= leaderLEO {
		return false
	}
	return follower.parked ||
		follower.stoppedZero ||
		follower.stoppedVersion != 0 ||
		follower.stopOfferedZero ||
		follower.stopOfferedVersion != 0 ||
		follower.lastPullAt.IsZero()
}

func (lc *channelRuntimeLifecycle) followerNeedsHintRetry(node ch.NodeID, leaderLEO uint64) bool {
	if lc == nil {
		return false
	}
	follower := lc.followers[node]
	return follower != nil && follower.match < leaderLEO && !follower.hint.retryAt.IsZero()
}

// beginPullHint records an inflight PullHint worker operation.
func (lc *channelRuntimeLifecycle) beginPullHint(node ch.NodeID, opID ch.OpID, version uint64, reason transport.PullHintReason) {
	if lc == nil {
		return
	}
	follower := lc.followers[node]
	if follower == nil {
		return
	}
	follower.hint.inflight = true
	follower.hint.opID = opID
	follower.hint.retryAt = time.Time{}
	follower.lastHintVersion = version
	follower.pendingHintVersion = 0
	if lc.pullHintInflight == nil {
		lc.pullHintInflight = make(map[ch.OpID]lifecyclePullHintInflight)
	}
	lc.pullHintInflight[opID] = lifecyclePullHintInflight{follower: node, version: version, reason: reason}
}

// finishPullHint retires a PullHint worker operation and clears current inflight state.
func (lc *channelRuntimeLifecycle) finishPullHint(opID ch.OpID) (lifecyclePullHintInflight, bool) {
	if lc == nil {
		return lifecyclePullHintInflight{}, false
	}
	inflight, ok := lc.pullHintInflight[opID]
	if !ok {
		return lifecyclePullHintInflight{}, false
	}
	delete(lc.pullHintInflight, opID)
	follower := lc.followers[inflight.follower]
	if follower == nil || !follower.hint.inflight || follower.hint.opID != opID {
		return lifecyclePullHintInflight{}, false
	}
	follower.hint.inflight = false
	follower.hint.opID = 0
	return inflight, true
}

// retirePullHints clears obsolete PullHint state for a follower.
func (lc *channelRuntimeLifecycle) retirePullHints(node ch.NodeID) {
	if lc == nil {
		return
	}
	for opID, inflight := range lc.pullHintInflight {
		if inflight.follower == node {
			delete(lc.pullHintInflight, opID)
		}
	}
	if follower := lc.followers[node]; follower != nil {
		follower.hint.inflight = false
		follower.hint.opID = 0
		follower.pendingHintVersion = 0
		follower.hint.retryAt = time.Time{}
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

// acceptFollowerStop records the accepted leader stop offer and arms checkpointing.
func (lc *channelRuntimeLifecycle) acceptFollowerStop(version, leaderLEO, leaderHW uint64) {
	lc.stage = lifecycleFollowerCheckpointing
	lc.followerStop = followerStopState{
		accepted:  true,
		version:   version,
		leaderLEO: leaderLEO,
		leaderHW:  leaderHW,
	}
	lc.stoppedAck.reset()
}

// cancelFollowerStop clears follower-local stop progress after stale metadata or newer activity.
func (lc *channelRuntimeLifecycle) cancelFollowerStop() {
	if lc == nil {
		return
	}
	lc.stage = lifecycleLive
	lc.followerStop = followerStopState{}
	lc.stoppedAck.reset()
	lc.checkpoint.reset()
}

// followerStopVersion returns the accepted stop activity version, or zero when no stop is active.
func (lc *channelRuntimeLifecycle) followerStopVersion() uint64 {
	if lc == nil || !lc.followerStop.accepted {
		return 0
	}
	return lc.followerStop.version
}
