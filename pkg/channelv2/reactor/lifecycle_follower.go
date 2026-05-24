package reactor

import (
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// FollowerLifecycleEventKind identifies a follower stop lifecycle input.
type FollowerLifecycleEventKind uint8

const (
	FollowerLifecycleStopOffered FollowerLifecycleEventKind = iota + 1
	FollowerLifecycleStopCheckpointDone
	FollowerLifecycleStoppedAckDone
	FollowerLifecyclePullHintReceived
	FollowerLifecycleMetaFence
)

// FollowerLifecycleEvent carries the minimal data needed by follower stop transitions.
type FollowerLifecycleEvent struct {
	Kind            FollowerLifecycleEventKind
	Now             time.Time
	LeaderLEO       uint64
	LeaderHW        uint64
	ActivityVersion uint64
	Err             error
}

func (lc runtimeLifecycle) OnFollowerLifecycleEvent(event FollowerLifecycleEvent, view RuntimeView, cfg LifecycleConfig) LifecycleDecision {
	phase := lc.FollowerPhase
	if phase == 0 {
		phase = FollowerLifecycleReplicating
	}
	decision := LifecycleDecision{LeaderPhase: lc.LeaderPhase, FollowerPhase: phase}
	now := event.Now
	if now.IsZero() {
		now = time.Now()
	}

	switch event.Kind {
	case FollowerLifecycleStopOffered:
		if view.Role == ch.RoleFollower && view.Status == ch.StatusActive &&
			!view.followerStopBlocked() && view.LEO >= event.LeaderLEO && view.HW >= event.LeaderHW {
			decision.FollowerPhase = FollowerLifecycleStopCheckpointing
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionStartFollowerStopCheckpoint})
			return decision
		}
		decision.FollowerPhase = FollowerLifecycleReplicating
		decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionScheduleReplication})
		return decision
	case FollowerLifecycleStopCheckpointDone:
		if event.Err != nil {
			decision.FollowerPhase = FollowerLifecycleStopCheckpointing
			decision.NextDue = retryDue(now, cfg.IdleEvictCheckInterval)
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionScheduleReplication, Due: decision.NextDue})
			return decision
		}
		decision.FollowerPhase = FollowerLifecycleStopAcking
		decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionSendStoppedAck})
		return decision
	case FollowerLifecycleStoppedAckDone:
		if event.Err == nil {
			decision.FollowerPhase = FollowerLifecycleStopAcking
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionEvictRuntime})
			return decision
		}
		if errors.Is(event.Err, ch.ErrStaleMeta) {
			decision.FollowerPhase = FollowerLifecycleReplicating
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionScheduleReplication})
			return decision
		}
		decision.FollowerPhase = FollowerLifecycleStopAcking
		decision.NextDue = retryDue(now, cfg.IdleEvictCheckInterval)
		decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionScheduleReplication, Due: decision.NextDue})
		return decision
	case FollowerLifecyclePullHintReceived, FollowerLifecycleMetaFence:
		decision.FollowerPhase = FollowerLifecycleReplicating
		decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionScheduleReplication})
		return decision
	default:
		return decision
	}
}

func (v RuntimeView) followerStopBlocked() bool {
	p := v.PendingWork
	return p.PullInflight ||
		p.AckInflight ||
		p.PendingAck ||
		p.PendingPull ||
		p.ApplyBlocked ||
		p.ApplyInflight ||
		p.CheckpointInflight
}
