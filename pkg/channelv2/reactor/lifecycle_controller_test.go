package reactor

import (
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
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

func TestLifecycleFollowerVersionPredicates(t *testing.T) {
	follower := lifecycleFollower{match: 7, stopOfferedVersion: 9, stoppedVersion: 9}

	require.True(t, follower.stopOffered(9))
	require.True(t, follower.stopped(9))
	require.False(t, follower.stopOffered(8))
	require.False(t, follower.stopped(8))
	require.True(t, follower.caughtUp(7))
	require.False(t, follower.caughtUp(8))
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
