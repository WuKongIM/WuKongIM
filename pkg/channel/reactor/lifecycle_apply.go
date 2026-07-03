package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// driveLifecycle translates one lifecycle event into controller actions and applies them.
func (r *Reactor) driveLifecycle(rc *runtimeChannel, event lifecycleEvent) {
	if r == nil || rc == nil || rc.state == nil {
		return
	}
	now := event.now
	if now.IsZero() {
		now = time.Now()
	}
	switch event.kind {
	case lifecycleEventIdleTick, lifecycleEventLeaderStoppedAck:
		r.syncFollowerMatches(rc)
		if rc.lifecycle.finalCheck.inflight && rc.lifecycle.finalCheck.version != rc.lifecycle.version {
			rc.lifecycle.finalCheck.reset()
		}
		view := runtimeViewFromChannel(rc, now, AppendFenceView{})
		if rc.lifecycle.stage == lifecycleLive && view.CanOfferFollowerStop(now, r.cfg.IdleEvictAfter) {
			rc.lifecycle.stage = lifecycleLeaderStoppingFollowers
		}
		r.applyLifecycleActions(rc, planLeaderCheckpointEviction(rc.lifecycle, view, now, r.cfg.IdleEvictAfter), now)
	case lifecycleEventStoreCheckpointDone:
		r.applyStoreCheckpointDone(rc, event, now)
	case lifecycleEventStoppedAckDone:
		r.applyStoppedAckDone(rc, event, now)
	case lifecycleEventFinalEvictReady:
		r.applyFinalLeaderEviction(rc, event, now)
	case lifecycleEventAppendAdmitted, lifecycleEventAppendStored, lifecycleEventMetaFence:
		resetLeaderCheckpointLifecycle(rc)
	case lifecycleEventFollowerStopControl, lifecycleEventPullHintDone:
	}
}

// applyLifecycleActions executes reactor-owned effects selected by lifecycle planning.
func (r *Reactor) applyLifecycleActions(rc *runtimeChannel, actions []lifecycleAction, now time.Time) {
	for _, action := range actions {
		switch action.kind {
		case lifecycleActionStartLeaderCheckpoint:
			r.startLeaderCheckpoint(rc, now)
		case lifecycleActionQueueLeaderFinalRecheck:
			appendSeq := action.appendSeq
			if appendSeq == 0 && rc != nil && rc.state != nil {
				appendSeq = r.currentAppendSubmitSeq(rc.state.Key)
			}
			r.submitLeaderEvictReady(rc, now, appendSeq)
		case lifecycleActionStartFollowerStopCheckpoint:
			r.trySubmitStopCheckpoint(rc, now)
		case lifecycleActionSendStoppedAck:
			r.trySubmitStoppedAck(rc, now)
		case lifecycleActionScheduleReplication:
			r.scheduleReplicationFromState(rc, now)
		case lifecycleActionScheduleLifecycle:
			if action.due.IsZero() {
				r.scheduleLifecycleFromState(rc, now)
			} else {
				r.scheduleLifecycleDue(rc, action.due)
			}
		case lifecycleActionEvictRuntime:
			if rc != nil && rc.state != nil && r.evictRuntimeChannel(rc.state.Key, rc, "lifecycle decision") {
				r.clearAppendSubmitState(rc.state.Key)
			}
		}
	}
}

func (r *Reactor) scheduleLifecycleDue(rc *runtimeChannel, due time.Time) {
	if r == nil || rc == nil || rc.state == nil {
		return
	}
	r.due.push(dueItem{key: rc.state.Key, kind: dueLifecycle, due: due, version: rc.lifecycleDueVersion + 1})
	rc.lifecycleDueVersion++
}

func (r *Reactor) startLeaderCheckpoint(rc *runtimeChannel, now time.Time) bool {
	if r == nil || rc == nil || rc.state == nil {
		return false
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreCheckpoint(context.Background(), rc.state.ID, fence, ch.Checkpoint{HW: rc.state.LEO}); err != nil {
		rc.lifecycle.checkpoint.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
		return false
	}
	rc.lifecycle.stage = lifecycleLeaderCheckpointing
	rc.lifecycle.checkpoint.inflight = true
	rc.lifecycle.checkpoint.opID = opID
	rc.lifecycle.checkpoint.version = rc.lifecycle.version
	rc.lifecycle.checkpoint.retryAt = time.Time{}
	return true
}

func (r *Reactor) applyStoreCheckpointDone(rc *runtimeChannel, event lifecycleEvent, now time.Time) {
	if rc.lifecycle.followerStop.accepted {
		if event.err != nil {
			rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
			rc.lifecycle.checkpoint.retryAt = now.Add(rc.replication.backoff)
			rc.replication.lastError = event.err
			r.applyLifecycleActions(rc, []lifecycleAction{{kind: lifecycleActionScheduleReplication}}, now)
			return
		}
		rc.replication.backoff = 0
		rc.replication.lastError = nil
		rc.lifecycle.checkpoint.retryAt = time.Time{}
		rc.lifecycle.stage = lifecycleFollowerStoppedAcking
		r.applyLifecycleActions(rc, []lifecycleAction{{kind: lifecycleActionSendStoppedAck}, {kind: lifecycleActionScheduleReplication}}, now)
		return
	}
	if event.err != nil {
		rc.lifecycle.stage = lifecycleLeaderCheckpointing
		rc.lifecycle.checkpoint.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.applyLifecycleActions(rc, []lifecycleAction{{kind: lifecycleActionScheduleLifecycle, due: rc.lifecycle.checkpoint.retryAt}}, now)
		return
	}
	rc.lifecycle.checkpoint.retryAt = time.Time{}
	if event.activityVersion != rc.lifecycle.version {
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	r.syncFollowerMatches(rc)
	view := runtimeViewFromChannel(rc, now, AppendFenceView{})
	if rc.state.Role != ch.RoleLeader || rc.state.HW < rc.state.LEO || !view.AllFollowersStopped() || view.HasPendingWork() {
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.lifecycle.stage = lifecycleLeaderReadyToEvict
	rc.lifecycle.finalCheck.inflight = true
	rc.lifecycle.finalCheck.version = event.activityVersion
	r.applyLifecycleActions(rc, []lifecycleAction{{kind: lifecycleActionQueueLeaderFinalRecheck}}, now)
}

func (r *Reactor) applyStoppedAckDone(rc *runtimeChannel, event lifecycleEvent, now time.Time) {
	version := rc.lifecycle.stoppedAck.version
	rc.lifecycle.stoppedAck.inflight = false
	rc.lifecycle.stoppedAck.opID = 0
	if event.err != nil {
		if errors.Is(event.err, ch.ErrStaleMeta) {
			rc.lifecycle.cancelFollowerStop()
			rc.replication.backoff = 0
			rc.replication.lastError = event.err
			wasParked := rc.replication.parked
			rc.replication.markDirty(now)
			r.observeFollowerParkedCountIfChanged(wasParked, rc)
			r.applyLifecycleActions(rc, []lifecycleAction{{kind: lifecycleActionScheduleReplication}}, now)
			return
		}
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.stoppedAck.retryAt = now.Add(rc.replication.backoff)
		rc.lifecycle.stoppedAck.version = version
		rc.replication.lastError = event.err
		r.applyLifecycleActions(rc, []lifecycleAction{{kind: lifecycleActionScheduleReplication}}, now)
		return
	}
	rc.replication.backoff = 0
	rc.replication.lastError = nil
	rc.lifecycle.stoppedAck.retryAt = time.Time{}
	rc.lifecycle.stage = lifecycleFollowerReadyToEvict
	if !r.evictRuntimeChannel(rc.state.Key, rc, "stopped ack") {
		rc.lifecycle.finalCheck.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.applyLifecycleActions(rc, []lifecycleAction{{kind: lifecycleActionScheduleReplication}}, now)
		return
	}
	r.clearAppendSubmitState(rc.state.Key)
}

// applyFinalLeaderEviction fences append submissions while checking and evicting an idle leader.
func (r *Reactor) applyFinalLeaderEviction(rc *runtimeChannel, event lifecycleEvent, now time.Time) {
	if r == nil || rc == nil || rc.state == nil || !rc.lifecycle.finalCheck.inflight || rc.lifecycle.finalCheck.version != rc.lifecycle.version {
		return
	}
	rc.lifecycle.stage = lifecycleLeaderReadyToEvict
	r.syncFollowerMatches(rc)

	key := rc.state.Key
	r.submitMu.Lock()
	appendSeq := r.appendSubmitSeqs[key]
	appendFence := AppendFenceView{
		Reservations:      uint64(r.appendReservations[key]),
		SubmitSeq:         appendSeq,
		ObservedSubmitSeq: event.appendSeqObserved,
	}
	view := runtimeViewFromChannel(rc, now, appendFence)
	actions := planFinalLeaderEviction(view, now, r.cfg.IdleEvictCheckInterval)
	for _, action := range actions {
		switch action.kind {
		case lifecycleActionEvictRuntime:
			evicted := r.evictRuntimeChannel(key, rc, "leader idle checkpoint")
			if evicted {
				r.clearAppendSubmitStateLocked(key)
				r.submitMu.Unlock()
				return
			}
			rc.lifecycle.finalCheck.inflight = false
			rc.lifecycle.finalCheck.version = 0
			rc.lifecycle.checkpoint.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
			r.submitMu.Unlock()
			r.scheduleLifecycleFromState(rc, now)
			return
		case lifecycleActionQueueLeaderFinalRecheck:
			if action.appendSeq != 0 {
				appendSeq = action.appendSeq
			}
			r.submitMu.Unlock()
			r.submitLeaderEvictReady(rc, now, appendSeq)
			return
		case lifecycleActionScheduleLifecycle:
			if !action.due.IsZero() {
				rc.lifecycle.checkpoint.retryAt = action.due
			}
			r.submitMu.Unlock()
			r.scheduleLifecycleFromState(rc, now)
			return
		}
	}
	r.submitMu.Unlock()
}
