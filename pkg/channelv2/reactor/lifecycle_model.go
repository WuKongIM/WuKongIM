package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// LeaderLifecyclePhase identifies the leader runtime eviction phase.
type LeaderLifecyclePhase uint8

const (
	LeaderLifecycleServing LeaderLifecyclePhase = iota + 1
	LeaderLifecycleStoppingFollowers
	LeaderLifecycleCheckpointing
	LeaderLifecycleFinalRecheck
)

// FollowerLifecyclePhase identifies the follower stop-and-evict phase.
type FollowerLifecyclePhase uint8

const (
	FollowerLifecycleReplicating FollowerLifecyclePhase = iota + 1
	FollowerLifecycleStopCheckpointing
	FollowerLifecycleStopAcking
)

// runtimeLifecycle owns runtime eviction state that is separate from log progress.
type runtimeLifecycle struct {
	LeaderPhase   LeaderLifecyclePhase
	FollowerPhase FollowerLifecyclePhase
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
	PendingAck           bool
	PendingPull          bool
	ApplyBlocked         bool
	ApplyInflight        bool
	CheckpointInflight   bool
	CheckpointRetry      bool
	AckRetry             bool
	LifecycleCheckpoint  bool
	LifecycleRetry       bool
}

// AppendFenceView summarizes append submission state used by final recheck.
type AppendFenceView struct {
	Reservations uint64
	SubmitSeq    uint64
}

func (v RuntimeView) AllFollowersCaughtUp() bool {
	for _, follower := range v.Followers {
		if follower.Match < v.LEO {
			return false
		}
	}
	return true
}

func (v RuntimeView) AllFollowersStopped() bool {
	for _, follower := range v.Followers {
		if !follower.Stopped || follower.StopAckVersion != v.ActivityVersion || follower.Match < v.LEO {
			return false
		}
	}
	return true
}

func (v RuntimeView) IdleExpired(now time.Time, idleAfter time.Duration) bool {
	if v.IdleSince.IsZero() || idleAfter <= 0 {
		return false
	}
	return !now.Before(v.IdleSince.Add(idleAfter))
}

func (v RuntimeView) CanOfferFollowerStop(now time.Time, idleAfter time.Duration) bool {
	if v.Role != ch.RoleLeader || v.Status != ch.StatusActive {
		return false
	}
	if v.HW < v.LEO || !v.IdleExpired(now, idleAfter) || v.HasPendingWork() {
		return false
	}
	return v.AllFollowersCaughtUp()
}

func (v RuntimeView) HasPendingWork() bool {
	p := v.PendingWork
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
		p.PendingAck ||
		p.PendingPull ||
		p.ApplyBlocked ||
		p.ApplyInflight ||
		p.CheckpointInflight ||
		p.CheckpointRetry ||
		p.AckRetry ||
		p.LifecycleCheckpoint ||
		p.LifecycleRetry
}

func (v RuntimeView) SafeToEvict() bool {
	return !v.HasPendingWork()
}

// LifecycleConfig carries timing values needed by pure lifecycle decisions.
type LifecycleConfig struct {
	IdleEvictAfter         time.Duration
	IdleEvictCheckInterval time.Duration
	PullHintRetryInterval  time.Duration
}

// LifecycleActionKind identifies one side effect requested by the lifecycle model.
type LifecycleActionKind uint8

const (
	LifecycleActionScheduleLifecycle LifecycleActionKind = iota + 1
	LifecycleActionScheduleReplication
	LifecycleActionSendPullHint
	LifecycleActionStartFollowerStopCheckpoint
	LifecycleActionSendStoppedAck
	LifecycleActionStartLeaderCheckpoint
	LifecycleActionQueueLeaderFinalRecheck
	LifecycleActionEvictRuntime
	LifecycleActionResetEviction
	LifecycleActionFailStaleWaiters
)

// LifecycleAction is a side effect for the owning reactor to execute.
type LifecycleAction struct {
	Kind LifecycleActionKind
	Due  time.Time
}

// LifecycleDecision describes the next lifecycle state and reactor actions.
type LifecycleDecision struct {
	LeaderPhase   LeaderLifecyclePhase
	FollowerPhase FollowerLifecyclePhase
	Actions       []LifecycleAction
	NextDue       time.Time
}

func (d LifecycleDecision) ActionKinds() []LifecycleActionKind {
	out := make([]LifecycleActionKind, 0, len(d.Actions))
	for _, action := range d.Actions {
		out = append(out, action.Kind)
	}
	return out
}

func decisionHasAction(decision LifecycleDecision, kind LifecycleActionKind) bool {
	for _, action := range decision.Actions {
		if action.Kind == kind {
			return true
		}
	}
	return false
}
