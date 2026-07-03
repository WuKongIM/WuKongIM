package reactor

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
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

func TestRuntimeProbeReportsMigrationProofFields(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})

	meta := testMeta("runtime-probe-migration-proof", 1, 1)
	meta.Epoch = 3
	meta.LeaderEpoch = 9
	meta.WriteFence = ch.WriteFence{
		Token:   "migration-7",
		Version: 7,
		Reason:  ch.WriteFenceReasonLeaderTransfer,
	}
	require.NoError(t, applyMetaDirect(t, r, meta))

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	rc.state.LEO = 11
	rc.state.HW = 10
	rc.state.CheckpointHW = 8
	rc.state.PendingAppends[1] = &machine.AppendWaiter{OpID: 1}
	rc.state.InflightAppend = &machine.AppendOp{OpID: 2}

	future := NewFuture()
	r.handleRuntimeProbe(Event{Kind: EventRuntimeProbe, RuntimeChannelIDs: []ch.ChannelID{meta.ID}, Future: future})
	result := awaitFutureResult(t, future).RuntimeProbe

	require.Len(t, result.Channels, 1)
	proof := result.Channels[0]
	require.Equal(t, meta.ID, proof.ChannelID)
	require.Equal(t, meta.LeaderEpoch, proof.LeaderEpoch)
	require.Equal(t, meta.Epoch, proof.ChannelEpoch)
	require.Equal(t, ch.RoleLeader, proof.Role)
	require.Equal(t, ch.StatusActive, proof.Status)
	require.Equal(t, uint64(11), proof.LEO)
	require.Equal(t, uint64(10), proof.HW)
	require.Equal(t, uint64(8), proof.CheckpointHW)
	require.Equal(t, meta.WriteFence, proof.WriteFence)
	require.True(t, proof.InflightAppend)
	require.Equal(t, 1, proof.PendingAppendCount)
}

func TestDrainChannelReturnsDrainedLeaderWhenFenceMatches(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("runtime-drain-ready", 1, 1)
	meta.WriteFence = ch.WriteFence{Token: "migration-8", Version: 8, Reason: ch.WriteFenceReasonLeaderTransfer}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	rc := g.reactors[0].channels[meta.Key]
	require.NotNil(t, rc)
	rc.state.LEO = 4
	rc.state.HW = 4

	result, err := g.DrainChannel(context.Background(), ch.DrainChannelRequest{
		ChannelID:    meta.ID,
		LeaderEpoch:  meta.LeaderEpoch,
		FenceVersion: meta.WriteFence.Version,
		Timeout:      time.Millisecond,
	})

	require.NoError(t, err)
	require.True(t, result.Drained)
	require.Equal(t, uint64(4), result.LEO)
	require.Equal(t, uint64(4), result.HW)
}

func TestDrainChannelReturnsStaleMetaWhenFenceVersionChanges(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("runtime-drain-stale-fence", 1, 1)
	meta.WriteFence = ch.WriteFence{Token: "migration-9", Version: 9, Reason: ch.WriteFenceReasonLeaderTransfer}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	_, err = g.DrainChannel(context.Background(), ch.DrainChannelRequest{
		ChannelID:    meta.ID,
		LeaderEpoch:  meta.LeaderEpoch,
		FenceVersion: 8,
		Timeout:      time.Millisecond,
	})

	require.ErrorIs(t, err, ch.ErrStaleMeta)
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

func TestSubmitCompletionBlockedByFullQueueReturnsClosedOnClose(t *testing.T) {
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 1})
	entered := make(chan struct{})
	release := make(chan struct{})
	blockingFuture := NewFuture()
	blockingFuture.beforeComplete = func(Result) {
		close(entered)
		<-release
	}
	require.NoError(t, r.mailbox.Submit(PriorityHigh, Event{Kind: EventTick, Future: blockingFuture}))
	r.start()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("reactor did not enter blocking handler")
	}
	queuedFuture := NewFuture()
	require.NoError(t, r.mailbox.Submit(PriorityHigh, Event{Kind: EventRuntimeProbe, Future: queuedFuture}))

	submitDone := make(chan error, 1)
	go func() {
		submitDone <- r.SubmitCompletion(Event{Kind: EventWorkerResult})
	}()
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- r.Close()
	}()

	select {
	case err := <-submitDone:
		require.ErrorIs(t, err, ch.ErrClosed)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("SubmitCompletion did not unblock after close")
	}
	close(release)
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
}
