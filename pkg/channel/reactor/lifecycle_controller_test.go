package reactor

import (
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/stretchr/testify/require"
)

func TestLifecycleControllerIdleSinceUsesAppendThenLoad(t *testing.T) {
	loadedAt := time.Unix(100, 0)
	appendAt := time.Unix(200, 0)
	lc := newChannelRuntimeLifecycle(loadedAt, 3)

	require.Equal(t, loadedAt, lc.idleSince())

	lc.markAppend(appendAt, 4)

	require.Equal(t, appendAt, lc.idleSince())
	require.Equal(t, uint64(4), lc.version)
	require.Equal(t, lifecycleLive, lc.stage)
}

func TestLifecycleControllerResetForMetaClearsEffects(t *testing.T) {
	now := time.Unix(100, 0)
	lc := newChannelRuntimeLifecycle(now, 3)
	lc.stage = lifecycleLeaderReadyToEvict
	lc.checkpoint = lifecycleEffect{inflight: true, opID: 11, version: 3}
	lc.stoppedAck = lifecycleEffect{inflight: true, opID: 12, version: 3}
	lc.finalCheck = lifecycleEffect{queued: true, version: 3}
	lc.followers = map[ch.NodeID]*lifecycleFollower{
		2: {match: 3, stopOfferedVersion: 3, stoppedVersion: 3, hint: lifecycleEffect{inflight: true, opID: 13, version: 3}},
	}

	lc.resetForMeta(now.Add(time.Second), 5)

	require.Equal(t, lifecycleLive, lc.stage)
	require.Equal(t, uint64(5), lc.version)
	require.False(t, lc.checkpoint.active())
	require.False(t, lc.stoppedAck.active())
	require.False(t, lc.finalCheck.active())
	require.Zero(t, lc.followers[2].stopOfferedVersion)
	require.Zero(t, lc.followers[2].stoppedVersion)
	require.False(t, lc.followers[2].hint.active())
}

func TestLifecycleCancelFollowerStopClearsActiveCheckpoint(t *testing.T) {
	lc := newChannelRuntimeLifecycle(time.Unix(100, 0), 3)
	lc.acceptFollowerStop(3, 3, 3)
	lc.checkpoint = lifecycleEffect{inflight: true, opID: 11, version: 3}
	lc.stoppedAck = lifecycleEffect{inflight: true, opID: 12, version: 3}

	lc.cancelFollowerStop()

	require.Equal(t, lifecycleLive, lc.stage)
	require.False(t, lc.followerStop.accepted)
	require.False(t, lc.checkpoint.active())
	require.False(t, lc.stoppedAck.active())
}

func TestLifecycleFollowerVersionPredicates(t *testing.T) {
	follower := lifecycleFollower{match: 7, stopOfferedVersion: 9, stoppedVersion: 9}

	require.True(t, follower.stopOffered(9))
	require.True(t, follower.stopped(9))
	require.False(t, follower.stopOffered(8))
	require.False(t, follower.stopped(8))
	require.True(t, follower.caughtUp(7))
	require.False(t, follower.caughtUp(8))
}

func addLifecycleFollower(lc *channelRuntimeLifecycle, node ch.NodeID, follower *lifecycleFollower) {
	if lc.followers == nil {
		lc.followers = make(map[ch.NodeID]*lifecycleFollower)
	}
	lc.followers[node] = follower
}

func TestLifecycleControllerLeaderFollowerStopVersioning(t *testing.T) {
	now := time.Unix(100, 0)
	lc := newChannelRuntimeLifecycle(now, 3)
	addLifecycleFollower(&lc, 2, &lifecycleFollower{match: 3})

	lc.offerStop(2)

	require.True(t, lc.followers[2].stopOffered(3))
	require.False(t, lc.markFollowerStopped(2, 2, 3))
	require.False(t, lc.followers[2].stopped(3))
	require.True(t, lc.markFollowerStopped(2, 3, 3))
	require.True(t, lc.followers[2].stopped(3))

	lc.markAppend(now.Add(time.Second), 4)

	require.False(t, lc.followers[2].stopOffered(4))
	require.False(t, lc.followers[2].stopped(4))
}

func TestLifecycleControllerMarkFollowerStoppedSemantics(t *testing.T) {
	lc := newChannelRuntimeLifecycle(time.Unix(100, 0), 5)
	addLifecycleFollower(&lc, 2, &lifecycleFollower{match: 5})

	require.True(t, lc.markFollowerStopped(2, 5, 5))
	require.False(t, lc.markFollowerStopped(2, 5, 5))
	require.True(t, lc.followers[2].stopped(5))

	lc.offerStop(2)
	require.False(t, lc.markFollowerStopped(2, 4, 5))
	require.False(t, lc.followers[2].stopped(4))
}

func TestLifecycleControllerZeroVersionStopCompatibility(t *testing.T) {
	lc := newChannelRuntimeLifecycle(time.Unix(100, 0), 0)
	addLifecycleFollower(&lc, 2, &lifecycleFollower{match: 0})

	lc.offerStop(2)
	require.True(t, lc.followers[2].stopOffered(0))
	require.True(t, lc.markFollowerStopped(2, 0, 0))
	require.True(t, lc.followers[2].stopped(0))
	require.True(t, lc.followers[2].stoppedZero)
}

func TestLifecycleControllerUnofferedStoppedAckCompatibility(t *testing.T) {
	lc := newChannelRuntimeLifecycle(time.Unix(100, 0), 3)
	addLifecycleFollower(&lc, 2, &lifecycleFollower{match: 3})

	require.True(t, lc.markFollowerStopped(2, 3, 3))
	require.True(t, lc.followers[2].stopped(3))
}

func TestLifecycleControllerRecordFollowerProgressRetiresHintsWhenCaughtUp(t *testing.T) {
	now := time.Unix(100, 0)
	lc := newChannelRuntimeLifecycle(time.Unix(100, 0), 7)
	addLifecycleFollower(&lc, 2, &lifecycleFollower{match: 3})
	lc.beginPullHint(2, ch.OpID(11), 7, transport.PullHintReasonAppend, now)

	lc.recordFollowerProgress(2, 7, 7)

	require.Equal(t, uint64(7), lc.followers[2].match)
	require.Empty(t, lc.pullHintInflight)
	require.False(t, lc.followers[2].hint.active())
}

func TestLifecycleControllerRecordFollowerPullKeepsHintsUntilCaughtUp(t *testing.T) {
	now := time.Unix(100, 0)
	retryAt := now.Add(time.Second)
	lc := newChannelRuntimeLifecycle(now, 7)
	addLifecycleFollower(&lc, 2, &lifecycleFollower{match: 3})
	lc.followers[2].hint.retryAt = retryAt
	lc.followers[2].lastHintVersion = 7

	lc.recordFollowerPull(2, 4, 7, now)

	require.Equal(t, uint64(3), lc.followers[2].match)
	require.Equal(t, retryAt, lc.followers[2].hint.retryAt)
	require.Equal(t, uint64(7), lc.followers[2].lastHintVersion)

	lc.recordFollowerPull(2, 8, 7, now)

	require.Equal(t, uint64(7), lc.followers[2].match)
	require.False(t, lc.followers[2].hint.active())
}

func TestLifecycleControllerPullHintLifecycle(t *testing.T) {
	now := time.Unix(100, 0)
	lc := newChannelRuntimeLifecycle(now, 7)
	addLifecycleFollower(&lc, 2, &lifecycleFollower{match: 3})

	lc.recordFollowerPull(2, 4, 7, now)
	require.False(t, lc.followerNeedsImmediateProgress(2, 7))

	lc.followers[2].parked = true
	require.True(t, lc.followerNeedsImmediateProgress(2, 7))

	opID := ch.OpID(11)
	lc.beginPullHint(2, opID, 7, transport.PullHintReasonAppend, now)
	require.True(t, lc.followers[2].hint.inflight)
	require.Equal(t, uint64(7), lc.followers[2].lastHintVersion)
	require.Equal(t, now, lc.followers[2].lastHintAt)

	inflight, ok := lc.finishPullHint(opID)
	require.True(t, ok)
	require.Equal(t, ch.NodeID(2), inflight.follower)
	require.False(t, lc.followers[2].hint.inflight)

	lc.beginPullHint(2, ch.OpID(12), 8, transport.PullHintReasonResume, now)
	lc.retirePullHints(2)
	require.Empty(t, lc.pullHintInflight)
	require.False(t, lc.followers[2].hint.active())
}

func TestLifecycleControllerStalePullHintCompletionReturnsNotCurrent(t *testing.T) {
	lc := newChannelRuntimeLifecycle(time.Unix(100, 0), 7)
	addLifecycleFollower(&lc, 2, &lifecycleFollower{match: 3})
	lc.pullHintInflight = map[ch.OpID]lifecyclePullHintInflight{
		11: {follower: 2, version: 7, reason: transport.PullHintReasonAppend},
		12: {follower: 2, version: 8, reason: transport.PullHintReasonResume},
	}

	inflight, ok := lc.finishPullHint(11)
	require.False(t, ok)
	require.Equal(t, lifecyclePullHintInflight{}, inflight)
	require.NotContains(t, lc.pullHintInflight, ch.OpID(11))
	require.Contains(t, lc.pullHintInflight, ch.OpID(12))
	require.False(t, lc.followers[2].hint.active())

	lc.pullHintInflight[11] = lifecyclePullHintInflight{follower: 2, version: 7, reason: transport.PullHintReasonAppend}
	lc.followers[2].hint.inflight = true
	lc.followers[2].hint.opID = 12
	inflight, ok = lc.finishPullHint(11)
	require.False(t, ok)
	require.Equal(t, lifecyclePullHintInflight{}, inflight)
	require.True(t, lc.followers[2].hint.inflight)
	require.Equal(t, ch.OpID(12), lc.followers[2].hint.opID)
}

func TestLifecycleZeroVersionStoppedAckVisibleInRuntimeView(t *testing.T) {
	follower := lifecycleFollower{match: 3, stoppedZero: true}
	require.True(t, follower.stopped(0))

	state := machine.NewChannelState(ch.ChannelKey("1:zero-stopped"), 1, 1)
	state.Role = ch.RoleLeader
	state.Status = ch.StatusActive
	state.LEO = 3
	state.HW = 3
	rc := &runtimeChannel{
		state: state,
		lifecycle: channelRuntimeLifecycle{
			stage:     lifecycleLive,
			version:   0,
			followers: map[ch.NodeID]*lifecycleFollower{2: &follower},
		},
	}

	view := runtimeViewFromChannel(rc, time.Now(), AppendFenceView{})

	require.Len(t, view.Followers, 1)
	require.True(t, view.Followers[0].Stopped)
	require.True(t, view.AllFollowersStopped())
}

func TestLifecycleSyncLeaderFollowersAddsUpdatesAndRemovesReplicas(t *testing.T) {
	meta := ch.Meta{
		Key:         "1:lifecycle-sync",
		ID:          ch.ChannelID{ID: "lifecycle-sync", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2, 3},
		ISR:         []ch.NodeID{1, 2, 3},
		MinISR:      2,
		Status:      ch.StatusActive,
	}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]

	require.NotNil(t, rc.lifecycle.followers[2])
	require.NotNil(t, rc.lifecycle.followers[3])

	rc.state.Progress[2] = machine.ReplicaProgress{Match: 4}
	r.syncLeaderFollowers(rc)

	require.Equal(t, uint64(4), rc.lifecycle.followers[2].match)

	rc.state.Replicas = []ch.NodeID{1, 2}
	r.syncLeaderFollowers(rc)

	require.NotNil(t, rc.lifecycle.followers[2])
	require.Nil(t, rc.lifecycle.followers[3])
}

func TestRuntimeViewFollowerStopBlockedByPendingHotPathWork(t *testing.T) {
	tests := []struct {
		name string
		work PendingWorkView
	}{
		{name: "pending pull", work: PendingWorkView{PendingPull: true}},
		{name: "apply inflight", work: PendingWorkView{ApplyInflight: true}},
		{name: "apply blocked", work: PendingWorkView{ApplyBlocked: true}},
		{name: "ack inflight", work: PendingWorkView{AckInflight: true}},
		{name: "pull inflight", work: PendingWorkView{PullInflight: true}},
		{name: "checkpoint inflight", work: PendingWorkView{CheckpointInflight: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := RuntimeView{PendingWork: tt.work}

			require.True(t, view.followerStopBlocked())
		})
	}

	require.False(t, RuntimeView{}.followerStopBlocked())
}

func TestPlanLeaderCheckpointEvictionStartsCheckpointAfterFollowersStopped(t *testing.T) {
	now := time.Unix(100, 0)
	lc := newChannelRuntimeLifecycle(now.Add(-time.Minute), 3)
	lc.stage = lifecycleLeaderStoppingFollowers
	view := RuntimeView{
		Role:            ch.RoleLeader,
		Status:          ch.StatusActive,
		LEO:             3,
		HW:              3,
		ActivityVersion: 3,
		IdleSince:       now.Add(-time.Minute),
		Followers: []FollowerView{
			{Node: 2, Match: 3, Stopped: true, StopAckVersion: 3},
		},
	}

	actions := planLeaderCheckpointEviction(lc, view, now, time.Second)

	require.Contains(t, lifecycleActionKinds(actions), lifecycleActionStartLeaderCheckpoint)
}

func TestPlanLeaderCheckpointEvictionStartsCheckpointWhenRetryDue(t *testing.T) {
	now := time.Unix(100, 0)
	lc := newChannelRuntimeLifecycle(now.Add(-time.Minute), 3)
	lc.stage = lifecycleLeaderCheckpointing
	lc.checkpoint.retryAt = now
	view := RuntimeView{
		Role:            ch.RoleLeader,
		Status:          ch.StatusActive,
		LEO:             3,
		HW:              3,
		ActivityVersion: 3,
		IdleSince:       now.Add(-time.Minute),
		PendingWork:     PendingWorkView{LifecycleRetry: true},
		Followers: []FollowerView{
			{Node: 2, Match: 3, Stopped: true, StopAckVersion: 3},
		},
	}

	actions := planLeaderCheckpointEviction(lc, view, now, time.Second)

	require.Contains(t, lifecycleActionKinds(actions), lifecycleActionStartLeaderCheckpoint)
}

func TestPlanLeaderCheckpointEvictionDoesNotStartCheckpointBeforeRetryDue(t *testing.T) {
	now := time.Unix(100, 0)
	lc := newChannelRuntimeLifecycle(now.Add(-time.Minute), 3)
	lc.stage = lifecycleLeaderCheckpointing
	lc.checkpoint.retryAt = now.Add(time.Second)
	view := RuntimeView{
		Role:            ch.RoleLeader,
		Status:          ch.StatusActive,
		LEO:             3,
		HW:              3,
		ActivityVersion: 3,
		IdleSince:       now.Add(-time.Minute),
		PendingWork:     PendingWorkView{LifecycleRetry: true},
		Followers: []FollowerView{
			{Node: 2, Match: 3, Stopped: true, StopAckVersion: 3},
		},
	}

	actions := planLeaderCheckpointEviction(lc, view, now, time.Second)

	require.NotContains(t, lifecycleActionKinds(actions), lifecycleActionStartLeaderCheckpoint)
}

func TestPlanFinalLeaderEvictionRespectsAppendFence(t *testing.T) {
	now := time.Unix(100, 0)
	view := RuntimeView{
		Role:            ch.RoleLeader,
		Status:          ch.StatusActive,
		LEO:             3,
		HW:              3,
		ActivityVersion: 3,
		Followers: []FollowerView{
			{Node: 2, Match: 3, Stopped: true, StopAckVersion: 3},
		},
		AppendFence: AppendFenceView{Reservations: 1, SubmitSeq: 7},
	}

	actions := planFinalLeaderEviction(view, now, 5*time.Second)

	require.Equal(t, []lifecycleActionKind{lifecycleActionScheduleLifecycle}, lifecycleActionKinds(actions))
	require.Equal(t, now.Add(5*time.Second), actions[0].due)

	view.AppendFence = AppendFenceView{SubmitSeq: 8, ObservedSubmitSeq: 7}
	actions = planFinalLeaderEviction(view, now, 5*time.Second)
	require.Equal(t, []lifecycleActionKind{lifecycleActionQueueLeaderFinalRecheck}, lifecycleActionKinds(actions))

	view.AppendFence = AppendFenceView{SubmitSeq: 8, ObservedSubmitSeq: 8}
	actions = planFinalLeaderEviction(view, now, 5*time.Second)
	require.Equal(t, []lifecycleActionKind{lifecycleActionEvictRuntime}, lifecycleActionKinds(actions))
}

func lifecycleActionKinds(actions []lifecycleAction) []lifecycleActionKind {
	kinds := make([]lifecycleActionKind, 0, len(actions))
	for _, action := range actions {
		kinds = append(kinds, action.kind)
	}
	return kinds
}
