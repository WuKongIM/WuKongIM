package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/stretchr/testify/require"
)

func TestRuntimeSnapshotCountsLeaderFollowerAndParked(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	leaderMeta := testMeta("runtime-snapshot-leader", 2, 2)
	followerMeta := testMeta("runtime-snapshot-follower", 2, 1)
	require.NoError(t, awaitSubmit(g, leaderMeta.Key, Event{Kind: EventApplyMeta, Key: leaderMeta.Key, Meta: leaderMeta}))
	require.NoError(t, awaitSubmit(g, followerMeta.Key, Event{Kind: EventApplyMeta, Key: followerMeta.Key, Meta: followerMeta}))

	rc := g.reactors[0].channels[followerMeta.Key]
	require.NotNil(t, rc)
	rc.replication.parkWithRecovery(followerMeta.Key, time.Now(), time.Minute, 0)
	g.reactors[0].activationRejectedTotal = 3

	snapshot, err := g.RuntimeSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, ch.NodeID(2), snapshot.NodeID)
	require.Equal(t, 2, snapshot.ActiveTotal)
	require.Equal(t, 1, snapshot.ActiveLeader)
	require.Equal(t, 1, snapshot.ActiveFollower)
	require.Equal(t, 1, snapshot.FollowerParked)
	require.Equal(t, uint64(3), snapshot.ActivationRejectedTotal)
	require.Len(t, snapshot.Reactors, 1)
	require.Equal(t, 1, snapshot.Reactors[0].Leader)
	require.Equal(t, 1, snapshot.Reactors[0].Follower)
	require.Equal(t, 1, snapshot.Reactors[0].Parked)
	require.Len(t, snapshot.WorkerQueues, 4)
}

func TestRuntimeProbeReportsLoadedAndMissingChannels(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	leaderMeta := testMeta("runtime-probe-leader", 2, 2)
	followerMeta := testMeta("runtime-probe-follower", 2, 1)
	missingID := ch.ChannelID{ID: "runtime-probe-missing", Type: 1}
	require.NoError(t, awaitSubmit(g, leaderMeta.Key, Event{Kind: EventApplyMeta, Key: leaderMeta.Key, Meta: leaderMeta}))
	require.NoError(t, awaitSubmit(g, followerMeta.Key, Event{Kind: EventApplyMeta, Key: followerMeta.Key, Meta: followerMeta}))

	result, err := g.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{leaderMeta.ID, followerMeta.ID, missingID}})
	require.NoError(t, err)
	require.Equal(t, 3, result.Checked)
	require.Equal(t, 1, result.LoadedLeader)
	require.Equal(t, 1, result.LoadedFollower)
	require.Equal(t, []ch.ChannelID{missingID}, result.Missing)
}

func TestRuntimeEvictEvictsSafeRuntimeAndSkipsBusyRuntime(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	safeMeta := testMeta("runtime-evict-safe", 1, 1)
	busyMeta := testMeta("runtime-evict-busy", 1, 1)
	require.NoError(t, awaitSubmit(g, safeMeta.Key, Event{Kind: EventApplyMeta, Key: safeMeta.Key, Meta: safeMeta}))
	require.NoError(t, awaitSubmit(g, busyMeta.Key, Event{Kind: EventApplyMeta, Key: busyMeta.Key, Meta: busyMeta}))

	busy := g.reactors[0].channels[busyMeta.Key]
	require.NotNil(t, busy)
	if busy.waiters == nil {
		busy.waiters = make(map[ch.OpID]*Future)
	}
	busy.waiters[1] = NewFuture()
	missingID := ch.ChannelID{ID: "runtime-evict-missing", Type: 1}

	result, err := g.RuntimeEvict(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{safeMeta.ID, busyMeta.ID, missingID}})
	require.NoError(t, err)
	require.Equal(t, 3, result.Requested)
	require.Equal(t, 1, result.Evicted)
	require.Equal(t, 1, result.SkippedBusy)
	require.Equal(t, 1, result.Missing)
	require.Nil(t, g.reactors[0].channels[safeMeta.Key])
	require.NotNil(t, g.reactors[0].channels[busyMeta.Key])
}
