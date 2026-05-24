package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

func (r *Reactor) lifecycleConfig() LifecycleConfig {
	return LifecycleConfig{
		IdleEvictAfter:         r.cfg.IdleEvictAfter,
		IdleEvictCheckInterval: r.cfg.IdleEvictCheckInterval,
		PullHintRetryInterval:  r.cfg.PullHintRetryInterval,
	}
}

func (r *Reactor) applyLifecycleDecision(rc *runtimeChannel, decision LifecycleDecision, now time.Time) {
	if r == nil || rc == nil {
		return
	}
	if decision.LeaderPhase != 0 {
		rc.runtimeLifecycle.LeaderPhase = decision.LeaderPhase
	}
	if decision.FollowerPhase != 0 {
		rc.runtimeLifecycle.FollowerPhase = decision.FollowerPhase
	}
	for _, action := range decision.Actions {
		r.applyLifecycleAction(rc, action, now)
	}
	if !decision.NextDue.IsZero() {
		r.scheduleLifecycleDue(rc, decision.NextDue)
	}
}

func (r *Reactor) applyLifecycleAction(rc *runtimeChannel, action LifecycleAction, now time.Time) {
	switch action.Kind {
	case LifecycleActionStartLeaderCheckpoint:
		r.startLeaderCheckpoint(rc, now)
	case LifecycleActionQueueLeaderFinalRecheck:
		if rc != nil && rc.state != nil {
			r.submitLeaderEvictReady(rc, now, r.currentAppendSubmitSeq(rc.state.Key))
		}
	case LifecycleActionStartFollowerStopCheckpoint:
		r.trySubmitStopCheckpoint(rc, now)
	case LifecycleActionSendStoppedAck:
		if rc != nil && rc.state != nil {
			r.submitAckPayload(rc, rc.state.LEO, true, rc.replication.stopActivityVersion, now)
		}
	case LifecycleActionScheduleReplication:
		r.scheduleReplicationFromState(rc, now)
	case LifecycleActionEvictRuntime:
		if rc != nil && rc.state != nil {
			key := rc.state.Key
			if r.evictRuntimeChannel(key, rc, "lifecycle decision") {
				r.clearAppendSubmitState(key)
			}
		}
	case LifecycleActionResetEviction:
		resetLeaderCheckpointLifecycle(rc)
	case LifecycleActionScheduleLifecycle:
		due := action.Due
		if due.IsZero() {
			due = now
		}
		r.scheduleLifecycleDue(rc, due)
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
		rc.lifecycle.CheckpointRetryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
		return false
	}
	rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleCheckpointing
	rc.lifecycle.CheckpointInflight = true
	rc.lifecycle.CheckpointOpID = opID
	rc.lifecycle.CheckpointActivityVersion = rc.lifecycle.ActivityVersion
	rc.lifecycle.CheckpointRetryAt = time.Time{}
	return true
}
