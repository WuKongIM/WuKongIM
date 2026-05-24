package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/stretchr/testify/require"
)

func TestLifecycleViewCanOfferFollowerStopRequiresIdleCaughtUpAndNoWork(t *testing.T) {
	now := time.Unix(100, 0)
	view := RuntimeView{
		Role:            ch.RoleLeader,
		Status:          ch.StatusActive,
		LEO:             3,
		HW:              3,
		ActivityVersion: 3,
		IdleSince:       now.Add(-time.Minute),
		Followers: []FollowerView{
			{Node: 2, Match: 3},
			{Node: 3, Match: 3},
		},
	}

	require.True(t, view.CanOfferFollowerStop(now, time.Second))

	view.PendingWork.Waiters = 1
	require.False(t, view.CanOfferFollowerStop(now, time.Second))

	view.PendingWork = PendingWorkView{}
	view.Followers[1].Match = 2
	require.False(t, view.CanOfferFollowerStop(now, time.Second))
}

func TestLifecycleViewSafeToEvictRejectsPendingWork(t *testing.T) {
	view := RuntimeView{}
	require.True(t, view.SafeToEvict())

	view.PendingWork.AppendInflight = true
	require.False(t, view.SafeToEvict())
}

func TestRuntimeViewFromChannelCapturesPendingWork(t *testing.T) {
	state := machine.NewChannelState(ch.ChannelKey("1:a"), 1, 1)
	rc := &runtimeChannel{
		state:                state,
		waiters:              map[ch.OpID]*Future{1: NewFuture()},
		fetchWaiters:         map[ch.OpID]struct{}{},
		pullWaiters:          map[ch.OpID]*pullWaiter{},
		appendCancelContexts: map[ch.OpID]context.Context{},
		appendTimings:        map[ch.OpID]appendTiming{},
		lifecycle:            channelLifecycle{CheckpointInflight: true},
	}

	view := runtimeViewFromChannel(rc, time.Now(), AppendFenceView{})

	require.Equal(t, 1, view.PendingWork.Waiters)
	require.True(t, view.PendingWork.LifecycleCheckpoint)
	require.True(t, view.HasPendingWork())
}
