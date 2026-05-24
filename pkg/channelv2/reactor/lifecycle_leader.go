package reactor

import "time"

// LeaderLifecycleEventKind identifies a leader lifecycle input.
type LeaderLifecycleEventKind uint8

const (
	LeaderLifecycleTick LeaderLifecycleEventKind = iota + 1
	LeaderLifecycleAppendAdmitted
	LeaderLifecycleAppendStored
	LeaderLifecycleFollowerAcked
	LeaderLifecycleCheckpointDone
	LeaderLifecycleFinalRecheckEvent
	LeaderLifecycleMetaFence
)

// LeaderLifecycleEvent carries the minimal data needed by leader lifecycle transitions.
type LeaderLifecycleEvent struct {
	Kind              LeaderLifecycleEventKind
	Now               time.Time
	CheckpointErr     error
	ActivityVersion   uint64
	AppendSeqObserved uint64
}

func (lc runtimeLifecycle) OnLeaderLifecycleEvent(event LeaderLifecycleEvent, view RuntimeView, cfg LifecycleConfig) LifecycleDecision {
	phase := lc.LeaderPhase
	if phase == 0 {
		phase = LeaderLifecycleServing
	}
	decision := LifecycleDecision{LeaderPhase: phase, FollowerPhase: lc.FollowerPhase}
	now := event.Now
	if now.IsZero() {
		now = time.Now()
	}

	switch event.Kind {
	case LeaderLifecycleAppendAdmitted, LeaderLifecycleAppendStored, LeaderLifecycleMetaFence:
		decision.LeaderPhase = LeaderLifecycleServing
		decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionResetEviction})
		return decision
	case LeaderLifecycleTick, LeaderLifecycleFollowerAcked:
		if view.Role != 0 && view.CanOfferFollowerStop(now, cfg.IdleEvictAfter) && phase == LeaderLifecycleServing {
			decision.LeaderPhase = LeaderLifecycleStoppingFollowers
		}
		if view.Role != 0 && view.HW >= view.LEO && view.AllFollowersStopped() && !view.HasPendingWork() {
			decision.LeaderPhase = LeaderLifecycleCheckpointing
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionStartLeaderCheckpoint})
		}
		return decision
	case LeaderLifecycleCheckpointDone:
		if event.CheckpointErr != nil {
			decision.LeaderPhase = LeaderLifecycleCheckpointing
			decision.NextDue = retryDue(now, cfg.IdleEvictCheckInterval)
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionScheduleLifecycle, Due: decision.NextDue})
			return decision
		}
		if event.ActivityVersion != 0 && event.ActivityVersion != view.ActivityVersion {
			decision.LeaderPhase = phase
			return decision
		}
		if view.HW >= view.LEO && view.AllFollowersStopped() && !view.HasPendingWork() {
			decision.LeaderPhase = LeaderLifecycleFinalRecheck
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionQueueLeaderFinalRecheck})
		}
		return decision
	case LeaderLifecycleFinalRecheckEvent:
		decision.LeaderPhase = LeaderLifecycleFinalRecheck
		if view.AppendFence.Reservations > 0 {
			decision.NextDue = retryDue(now, cfg.IdleEvictCheckInterval)
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionScheduleLifecycle, Due: decision.NextDue})
			return decision
		}
		if event.AppendSeqObserved != view.AppendFence.SubmitSeq {
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionQueueLeaderFinalRecheck})
			return decision
		}
		if view.SafeToEvict() && view.HW >= view.LEO && view.AllFollowersStopped() {
			decision.Actions = append(decision.Actions, LifecycleAction{Kind: LifecycleActionEvictRuntime})
		}
		return decision
	default:
		return decision
	}
}

func retryDue(now time.Time, interval time.Duration) time.Time {
	if interval <= 0 {
		return now
	}
	return now.Add(interval)
}
