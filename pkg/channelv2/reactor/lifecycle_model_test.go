package reactor

import (
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
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
