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
		applyLeaderLifecyclePhase(&rc.lifecycle, decision.LeaderPhase)
	}
	if decision.FollowerPhase != 0 {
		applyFollowerLifecyclePhase(&rc.lifecycle, decision.FollowerPhase)
	}
	for _, action := range decision.Actions {
		r.applyLifecycleAction(rc, action, now)
	}
	if !decision.NextDue.IsZero() {
		r.scheduleLifecycleDue(rc, decision.NextDue)
	}
}

func runtimeLifecycleFromController(lc channelRuntimeLifecycle) runtimeLifecycle {
	return runtimeLifecycle{
		LeaderPhase:   leaderPhaseFromLifecycleStage(lc.stage),
		FollowerPhase: followerPhaseFromLifecycleStage(lc.stage),
	}
}

func applyLeaderLifecyclePhase(lc *channelRuntimeLifecycle, phase LeaderLifecyclePhase) {
	if lc == nil {
		return
	}
	switch phase {
	case LeaderLifecycleServing:
		lc.stage = lifecycleLive
	case LeaderLifecycleStoppingFollowers:
		lc.stage = lifecycleLeaderStoppingFollowers
	case LeaderLifecycleCheckpointing:
		lc.stage = lifecycleLeaderCheckpointing
	case LeaderLifecycleFinalRecheck:
		lc.stage = lifecycleLeaderReadyToEvict
	}
}

func applyFollowerLifecyclePhase(lc *channelRuntimeLifecycle, phase FollowerLifecyclePhase) {
	if lc == nil {
		return
	}
	switch phase {
	case FollowerLifecycleReplicating:
		lc.stage = lifecycleLive
	case FollowerLifecycleStopCheckpointing:
		lc.stage = lifecycleFollowerCheckpointing
	case FollowerLifecycleStopAcking:
		lc.stage = lifecycleFollowerStoppedAcking
	}
}

func leaderPhaseFromLifecycleStage(stage lifecycleStage) LeaderLifecyclePhase {
	switch stage {
	case lifecycleLeaderStoppingFollowers:
		return LeaderLifecycleStoppingFollowers
	case lifecycleLeaderCheckpointing:
		return LeaderLifecycleCheckpointing
	case lifecycleLeaderReadyToEvict:
		return LeaderLifecycleFinalRecheck
	default:
		return LeaderLifecycleServing
	}
}

func followerPhaseFromLifecycleStage(stage lifecycleStage) FollowerLifecyclePhase {
	switch stage {
	case lifecycleFollowerCheckpointing:
		return FollowerLifecycleStopCheckpointing
	case lifecycleFollowerStoppedAcking, lifecycleFollowerReadyToEvict:
		return FollowerLifecycleStopAcking
	default:
		return FollowerLifecycleReplicating
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
