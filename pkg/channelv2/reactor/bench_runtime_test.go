package reactor

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/stretchr/testify/require"
)

func TestRuntimeSnapshotCountsLeaderFollowerAndParked(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})

	leaderMeta := testMeta("runtime-snapshot-leader", 2, 2)
	followerMeta := testMeta("runtime-snapshot-follower", 2, 1)
	require.NoError(t, applyMetaDirect(t, r, leaderMeta))
	require.NoError(t, applyMetaDirect(t, r, followerMeta))

	rc := r.channels[followerMeta.Key]
	require.NotNil(t, rc)
	rc.replication.parkWithRecovery(followerMeta.Key, time.Now(), time.Minute, 0)
	r.activationRejectedTotal = 3

	future := NewFuture()
	r.handleRuntimeSnapshot(Event{Kind: EventRuntimeSnapshot, Future: future})
	result := awaitFutureResult(t, future)
	require.Equal(t, 1, result.RuntimeSnapshot.Leader)
	require.Equal(t, 1, result.RuntimeSnapshot.Follower)
	require.Equal(t, 1, result.RuntimeSnapshot.Parked)
	require.Equal(t, uint64(3), result.RuntimeActivationRejectedTotal)
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
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})

	safeMeta := testMeta("runtime-evict-safe", 1, 1)
	busyMeta := testMeta("runtime-evict-busy", 1, 1)
	require.NoError(t, applyMetaDirect(t, r, safeMeta))
	require.NoError(t, applyMetaDirect(t, r, busyMeta))

	busy := r.channels[busyMeta.Key]
	require.NotNil(t, busy)
	if busy.waiters == nil {
		busy.waiters = make(map[ch.OpID]*Future)
	}
	busy.waiters[1] = NewFuture()
	missingID := ch.ChannelID{ID: "runtime-evict-missing", Type: 1}

	future := NewFuture()
	r.handleRuntimeEvict(Event{Kind: EventRuntimeEvict, RuntimeChannelIDs: []ch.ChannelID{safeMeta.ID, busyMeta.ID, missingID}, Future: future})
	result := awaitFutureResult(t, future).RuntimeEvict
	require.Equal(t, 3, result.Requested)
	require.Equal(t, 1, result.Evicted)
	require.Equal(t, 1, result.SkippedBusy)
	require.Equal(t, 1, result.Missing)
	require.Nil(t, r.channels[safeMeta.Key])
	require.NotNil(t, r.channels[busyMeta.Key])
}

func TestRuntimeProbeSpansMultipleReactorsAndPreservesMissingOrder(t *testing.T) {
	factory := store.NewMemoryFactory()
	const reactorCount = 2
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: reactorCount, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	leaderID := channelIDForReactorIndex(t, g.router, 0)
	followerID := channelIDForReactorIndex(t, g.router, 1)
	missingFirst := ch.ChannelID{ID: "runtime-probe-missing-first", Type: 1}
	missingSecond := ch.ChannelID{ID: "runtime-probe-missing-second", Type: 1}
	leaderMeta := testMeta(leaderID.ID, 1, 1)
	leaderMeta.ID = leaderID
	leaderMeta.Key = ch.ChannelKeyForID(leaderID)
	followerMeta := testMeta(followerID.ID, 1, 2)
	followerMeta.ID = followerID
	followerMeta.Key = ch.ChannelKeyForID(followerID)
	require.NoError(t, awaitSubmit(g, leaderMeta.Key, Event{Kind: EventApplyMeta, Key: leaderMeta.Key, Meta: leaderMeta}))
	require.NoError(t, awaitSubmit(g, followerMeta.Key, Event{Kind: EventApplyMeta, Key: followerMeta.Key, Meta: followerMeta}))

	result, err := g.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{missingFirst, followerID, missingSecond, leaderID}})
	require.NoError(t, err)
	require.Equal(t, 4, result.Checked)
	require.Equal(t, 1, result.LoadedLeader)
	require.Equal(t, 1, result.LoadedFollower)
	require.Equal(t, []ch.ChannelID{missingFirst, missingSecond}, result.Missing)
}

func TestRuntimeEvictReturnsPartialResultWhenLaterSubmitFails(t *testing.T) {
	const reactorCount = 2
	g := newUnstartedTestGroup(t, reactorCount, 1)
	evictID := channelIDForReactorIndex(t, g.router, 0)
	blockedID := channelIDForReactorIndex(t, g.router, 1)
	evictMeta := testMeta(evictID.ID, 1, 1)
	evictMeta.ID = evictID
	evictMeta.Key = ch.ChannelKeyForID(evictID)
	require.NoError(t, applyMetaDirect(t, g.reactors[0], evictMeta))
	require.NoError(t, g.reactors[1].Submit(PriorityNormal, Event{Kind: EventRuntimeProbe, Future: NewFuture()}))

	g.reactors[0].start()
	defer g.reactors[0].Close()

	result, err := g.RuntimeEvict(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{evictID, blockedID}})
	require.ErrorIs(t, err, ch.ErrBackpressured)
	require.Equal(t, 2, result.Requested)
	require.Equal(t, 1, result.Evicted)
}

func TestRuntimeMethodsReturnErrClosedForNilAndClosedGroup(t *testing.T) {
	var nilGroup *Group
	_, err := nilGroup.RuntimeSnapshot(context.Background())
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = nilGroup.RuntimeProbe(context.Background(), ch.RuntimeSelector{})
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = nilGroup.RuntimeEvict(context.Background(), ch.RuntimeSelector{})
	require.ErrorIs(t, err, ch.ErrClosed)

	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory()})
	require.NoError(t, err)
	require.NoError(t, g.Close())
	_, err = g.RuntimeSnapshot(context.Background())
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = g.RuntimeProbe(context.Background(), ch.RuntimeSelector{})
	require.ErrorIs(t, err, ch.ErrClosed)
	_, err = g.RuntimeEvict(context.Background(), ch.RuntimeSelector{})
	require.ErrorIs(t, err, ch.ErrClosed)
}

func TestReactorCompletesQueuedFuturesOnExit(t *testing.T) {
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	future := NewFuture()
	require.NoError(t, r.mailbox.Submit(PriorityNormal, Event{Kind: EventRuntimeProbe, Future: future}))

	r.failQueuedEvents(ch.ErrClosed)
	_, err := future.Await(context.Background())
	require.True(t, errors.Is(err, ch.ErrClosed))
}
