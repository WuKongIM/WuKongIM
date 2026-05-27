package reactor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestLeaderPullDelayUsesIdleAgeWithoutCoolingPhase(t *testing.T) {
	r := NewReactor(ReactorConfig{
		LocalNode:                   1,
		Store:                       store.NewMemoryFactory(),
		IdleSlowdownAfter:           time.Second,
		IdlePullMinInterval:         time.Millisecond,
		IdlePullMaxInterval:         8 * time.Millisecond,
		ReplicationIdlePollInterval: time.Millisecond,
	})
	rc := &runtimeChannel{lifecycle: channelLifecycle{LoadedAt: time.Unix(0, 0)}}

	delay := r.leaderPullDelay(rc, time.Unix(3, 0))

	require.GreaterOrEqual(t, delay, 2*time.Millisecond)
}

func TestFollowerTickPullsFromLocalLEOPlusOne(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("a")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, func() bool { return net.LastPull().NextOffset == 1 }, time.Second, time.Millisecond)

	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Hour)})
		return net.LastAck().MatchOffset == 1
	}, time.Second, time.Millisecond)
	net.SetPullResponse(transport.PullResponse{})
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)})
		return net.LastPull().NextOffset == 2
	}, time.Second, time.Millisecond)
}

func TestKeyedTickHandlesManuallyDirtyFollowerWithoutSeparateSchedule(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	leaderMeta := testMeta("tick-scan-follower", 2, 2)
	require.NoError(t, awaitSubmit(g, leaderMeta.Key, Event{Kind: EventApplyMeta, Key: leaderMeta.Key, Meta: leaderMeta}))

	reactor := g.reactors[g.router.PickIndex(leaderMeta.Key)]
	rc := reactor.channels[leaderMeta.Key]
	require.NotNil(t, rc)
	followerMeta := leaderMeta
	followerMeta.Leader = 1
	followerMeta.Replicas = []ch.NodeID{1, 2}
	followerMeta.ISR = []ch.NodeID{1, 2}
	require.NoError(t, rc.state.ApplyMeta(followerMeta).Err)
	rc.replication.markDirty(time.Time{})

	require.NoError(t, awaitSubmit(g, leaderMeta.Key, Event{Kind: EventTick, Key: leaderMeta.Key, TickNow: time.Now()}))
	require.Eventually(t, func() bool { return net.LastPull().NextOffset == 1 }, time.Second, time.Millisecond)
}

func TestFollowerPullInflightSuppressesDuplicatePull(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{
		LocalNode:    2,
		ReactorCount: 1,
		MailboxSize:  16,
		Store:        factory,
		Transport:    net,
		WorkerPools:  worker.PoolsConfig{RPC: worker.PoolConfig{Name: "rpc", Workers: 2, QueueSize: 2}},
	})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("a")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0).Add(time.Millisecond)}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0).Add(2 * time.Millisecond)}))
	require.Equal(t, 1, net.PullCalls())
	net.UnblockPulls()
}

func TestFollowerMetaFenceResetsPullInflightAndStaleCompletionDoesNotClearNewPull(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{
		LocalNode:    2,
		ReactorCount: 1,
		MailboxSize:  16,
		Store:        factory,
		Transport:    net,
		WorkerPools:  worker.PoolsConfig{RPC: worker.PoolConfig{Name: "rpc", Workers: 2, QueueSize: 2}},
	})
	require.NoError(t, err)
	defer g.Close()
	defer net.UnblockPulls()

	meta := followerTestMeta("pull-fence-reset")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
	oldOpID := rc.replication.pullOpID
	require.True(t, rc.replication.pullInflight)
	require.NotZero(t, oldOpID)

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, awaitSubmit(g, updated.Key, Event{Kind: EventApplyMeta, Key: updated.Key, Meta: updated}))
	require.NoError(t, awaitSubmit(g, updated.Key, Event{Kind: EventTick, Key: updated.Key, TickNow: time.Unix(1, 0).Add(time.Millisecond)}))
	require.True(t, rc.replication.pullInflight)
	require.NotZero(t, rc.replication.pullOpID)
	require.NotEqual(t, oldOpID, rc.replication.pullOpID)
	require.Eventually(t, func() bool { return net.PullCalls() == 2 && net.LastPull().LeaderEpoch == updated.LeaderEpoch }, time.Second, time.Millisecond)
	newOpID := rc.replication.pullOpID

	stale := worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: oldOpID},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
		}},
	}
	g.reactors[g.router.PickIndex(meta.Key)].handleRPCPullResult(stale)
	require.True(t, rc.replication.pullInflight)
	require.Equal(t, newOpID, rc.replication.pullOpID)
}

func TestFollowerMetaFenceDropsPendingPullBeforeSchedulingNewEpoch(t *testing.T) {
	net := newCapturingTransport()
	factory := newBlockingApplyFactory()
	factory.BlockApplies()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	defer factory.UnblockApplies()
	r := NewReactor(ReactorConfig{
		ID:          0,
		LocalNode:   2,
		Store:       factory,
		Pools:       pools,
		MailboxSize: 16,
	})

	meta := followerTestMeta("pending-pull-fence-reset")
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pendingPull = &transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 11, Index: 1, Payload: []byte("old"), SizeBytes: 3}},
	}

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, updated))
	require.False(t, factory.ApplyStarted())
	require.Nil(t, rc.replication.pendingPull)
	require.Zero(t, rc.replication.applyOpID)

	r.handleTick(Event{Kind: EventTick, Key: updated.Key, TickNow: time.Unix(1, 0)})
	pullResult := sink.awaitResultKind(t, worker.TaskRPCPull)
	require.Equal(t, updated.LeaderEpoch, pullResult.Fence.LeaderEpoch)
	require.False(t, factory.ApplyStarted())
	require.Nil(t, rc.replication.pendingPull)
	require.Zero(t, rc.replication.applyOpID)
	require.True(t, rc.replication.pullInflight)
	require.Equal(t, updated.LeaderEpoch, net.LastPull().LeaderEpoch)
}

func TestFollowerMetaFenceClearsAckState(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	meta := followerTestMeta("ack-fence-reset")
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication = replicationState{
		ackInflight:     true,
		ackOpID:         3,
		ackMatch:        7,
		pendingAck:      true,
		pendingAckMatch: 8,
		nextAckAt:       time.Unix(1, 0).Add(time.Hour),
	}

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, updated))

	require.False(t, rc.replication.ackInflight)
	require.Zero(t, rc.replication.ackOpID)
	require.Zero(t, rc.replication.ackMatch)
	require.False(t, rc.replication.pendingAck)
	require.Zero(t, rc.replication.pendingAckMatch)
	require.True(t, rc.replication.dirty)
}

func TestFollowerPullErrorBacksOff(t *testing.T) {
	net := newCapturingTransport()
	net.SetPullError(ch.ErrNotReady)
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{
		LocalNode:                   2,
		ReactorCount:                1,
		MailboxSize:                 16,
		Store:                       factory,
		Transport:                   net,
		ReplicationMinBackoff:       time.Hour,
		ReplicationMaxBackoff:       time.Hour,
		ReplicationIdlePollInterval: time.Hour,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("a")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)}))
	require.Equal(t, 1, net.PullCalls())
}

func TestFollowerEmptyPullWithLeaderLEOAheadAdvancesHWAndRetriesImmediately(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("empty-pull-hw")
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16,
		ReplicationIdlePollInterval: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 1
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	result := worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:      meta.Key,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			LeaderHW:        99,
			LeaderLEO:       99,
			ActivityVersion: 99,
			NextPullAfter:   time.Hour,
			Control:         transport.PullControlContinue,
		}},
	}
	before := time.Now()
	r.handleRPCPullResult(result)
	after := time.Now()

	require.Equal(t, uint64(3), rc.state.HW)
	require.False(t, rc.replication.ackInflight)
	require.False(t, rc.replication.pendingAck)
	require.False(t, rc.replication.nextPullAt.IsZero())
	require.False(t, rc.replication.nextPullAt.Before(before))
	require.True(t, rc.replication.nextPullAt.Before(after.Add(time.Second)))
}

func TestFollowerEmptyPullWithLeaderLEOAheadAndZeroDelayRetriesImmediately(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("empty-pull-zero-delay")
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16,
		ReplicationIdlePollInterval: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 1
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	result := worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			LeaderHW:    99,
			LeaderLEO:   99,
		}},
	}
	before := time.Now()
	r.handleRPCPullResult(result)
	after := time.Now()

	require.Equal(t, uint64(3), rc.state.HW)
	require.False(t, rc.replication.nextPullAt.IsZero())
	require.False(t, rc.replication.nextPullAt.Before(before))
	require.True(t, rc.replication.nextPullAt.Before(after.Add(time.Second)))
}

func TestFollowerStopCheckpointsThenSendsStoppedAckBeforeEvicting(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("stop-checkpoint-ack")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:      meta.Key,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			LeaderHW:        3,
			LeaderLEO:       3,
			ActivityVersion: 3,
			Control:         transport.PullControlStop,
		}},
	})

	require.Equal(t, FollowerLifecycleStopCheckpointing, rc.runtimeLifecycle.FollowerPhase)
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.Contains(t, r.channels, meta.Key)

	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: checkpoint})
	ack := sink.awaitResultKind(t, worker.TaskRPCAck)
	require.Contains(t, r.channels, meta.Key)
	require.Equal(t, transport.AckRequest{
		ChannelKey:      meta.Key,
		Epoch:           meta.Epoch,
		LeaderEpoch:     meta.LeaderEpoch,
		Follower:        2,
		MatchOffset:     3,
		ActivityVersion: 3,
		Stopped:         true,
	}, net.LastAck())

	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: ack})
	require.NotContains(t, r.channels, meta.Key)
}

func TestFollowerStopEmptyChannelSendsStoppedAckAndEvicts(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("stop-empty")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:      meta.Key,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			LeaderHW:        0,
			LeaderLEO:       0,
			ActivityVersion: 1,
			Control:         transport.PullControlStop,
		}},
	})

	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.Contains(t, r.channels, meta.Key)

	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: checkpoint})
	ack := sink.awaitResultKind(t, worker.TaskRPCAck)
	require.Contains(t, r.channels, meta.Key)
	require.Equal(t, transport.AckRequest{
		ChannelKey:      meta.Key,
		Epoch:           meta.Epoch,
		LeaderEpoch:     meta.LeaderEpoch,
		Follower:        2,
		MatchOffset:     0,
		ActivityVersion: 1,
		Stopped:         true,
	}, net.LastAck())

	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: ack})
	require.NotContains(t, r.channels, meta.Key)
}

func TestFollowerStopEvictionAllowsMemoryStoreReload(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("stop-reload")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:      meta.Key,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			LeaderHW:        0,
			LeaderLEO:       0,
			ActivityVersion: 1,
			Control:         transport.PullControlStop,
		}},
	})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskStoreCheckpoint)})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskRPCAck)})
	require.NotContains(t, r.channels, meta.Key)

	require.NoError(t, applyMetaDirect(t, r, meta))
	require.Contains(t, r.channels, meta.Key)
}

func TestStoppedAckRetryKeepsStoppedPayloadAndRuntime(t *testing.T) {
	net := newCapturingTransport()
	net.SetAckError(ch.ErrNotReady)
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("stopped-ack-retry")
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16,
		ReplicationMinBackoff: time.Nanosecond, ReplicationMaxBackoff: time.Nanosecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			LeaderHW: 3, LeaderLEO: 3, ActivityVersion: 3, Control: transport.PullControlStop,
		}},
	})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskStoreCheckpoint)})
	firstAck := sink.awaitResultKind(t, worker.TaskRPCAck)
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: firstAck})
	require.Contains(t, r.channels, meta.Key)
	require.True(t, rc.replication.pendingAck)

	net.SetAckError(nil)
	r.tickReplication(rc, time.Now().Add(time.Second))
	retryAck := sink.awaitResultKind(t, worker.TaskRPCAck)
	require.Equal(t, transport.AckRequest{
		ChannelKey:      meta.Key,
		Epoch:           meta.Epoch,
		LeaderEpoch:     meta.LeaderEpoch,
		Follower:        2,
		MatchOffset:     3,
		ActivityVersion: 3,
		Stopped:         true,
	}, net.LastAck())

	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: retryAck})
	require.NotContains(t, r.channels, meta.Key)
}

func TestStoppedAckStaleMetaCancelsStopAndPullsImmediately(t *testing.T) {
	net := newCapturingTransport()
	net.SetAckError(ch.ErrStaleMeta)
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("stopped-ack-stale-pulls")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			LeaderHW: 3, LeaderLEO: 3, ActivityVersion: 3, Control: transport.PullControlStop,
		}},
	})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskStoreCheckpoint)})
	ack := sink.awaitResultKind(t, worker.TaskRPCAck)
	require.True(t, net.LastAck().Stopped)

	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: ack})
	require.Contains(t, r.channels, meta.Key)
	require.False(t, rc.replication.stopping)
	require.False(t, rc.replication.pendingAck)
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.Equal(t, 1, net.AckCalls())
}

func TestFollowerStopRejectedWhenLocalLEOBelowLeaderLEO(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("stop-reject-leo")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			LeaderHW: 3, LeaderLEO: 4, ActivityVersion: 3, Control: transport.PullControlStop,
		}},
	})

	require.Contains(t, r.channels, meta.Key)
	require.Equal(t, 0, net.AckCalls())
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint, worker.TaskRPCAck)
	require.False(t, rc.replication.parked)
	require.True(t, rc.replication.nextPullAt.IsZero() || !rc.replication.nextPullAt.After(time.Now()))
}

func TestFollowerStopRejectedWhenLocalHWBelowLeaderHW(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("stop-reject-hw")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 2
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			LeaderHW: 3, LeaderLEO: 3, ActivityVersion: 3, Control: transport.PullControlStop,
		}},
	})

	require.Contains(t, r.channels, meta.Key)
	require.Equal(t, 0, net.AckCalls())
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint, worker.TaskRPCAck)
	require.False(t, rc.replication.parked)
}

func TestStaleStopCompletionDoesNotDeleteAfterNewerPullHint(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	defer net.UnblockPulls()

	meta := followerTestMeta("stale-stop-newer-hint")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			LeaderHW: 3, LeaderLEO: 3, ActivityVersion: 3, Control: transport.PullControlStop,
		}},
	})
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)

	r.handlePullHint(Event{Kind: EventPullHint, Key: meta.Key, PullHint: transport.PullHintRequest{
		ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
		Leader: meta.Leader, LeaderLEO: 4, ActivityVersion: 4, Reason: transport.PullHintReasonAppend,
	}})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: checkpoint})

	require.Contains(t, r.channels, meta.Key)
	require.Equal(t, 0, net.AckCalls())
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
}

func TestPullHintDuringInflightPullPreventsOldEmptyResponseParking(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("inflight-pull-hint-old-empty")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handlePullHint(Event{Kind: EventPullHint, Key: meta.Key, PullHint: transport.PullHintRequest{
		ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
		Leader: meta.Leader, LeaderLEO: 1, ActivityVersion: 2, Reason: transport.PullHintReasonAppend,
	}})
	require.Equal(t, 0, net.PullCalls())
	require.True(t, rc.replication.dirty)

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			LeaderHW: 0, LeaderLEO: 0, ActivityVersion: 1, NextPullAfter: time.Hour, Control: transport.PullControlContinue,
		}},
	})

	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.False(t, rc.replication.parked)
	require.True(t, rc.replication.pullInflight)
}

func TestFollowerPullHintInterruptsParked(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("parked-hint")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:      meta.Key,
		Epoch:           meta.Epoch,
		LeaderEpoch:     meta.LeaderEpoch,
		LeaderHW:        0,
		LeaderLEO:       0,
		ActivityVersion: 7,
		NextPullAfter:   time.Hour,
		Control:         transport.PullControlContinue,
	})
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:      meta.Key,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			LeaderHW:        0,
			LeaderLEO:       0,
			ActivityVersion: 7,
			NextPullAfter:   time.Hour,
			Control:         transport.PullControlContinue,
		}},
	})
	r.tickReplication(rc, time.Now().Add(time.Minute))
	require.Equal(t, 0, net.PullCalls())

	future := NewFuture()
	r.handlePullHint(Event{
		Kind:   EventPullHint,
		Key:    meta.Key,
		Future: future,
		PullHint: transport.PullHintRequest{
			ChannelKey:      meta.Key,
			ChannelID:       meta.ID,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			Leader:          meta.Leader,
			LeaderLEO:       1,
			ActivityVersion: 8,
			Reason:          transport.PullHintReasonAppend,
		},
	})
	require.NoError(t, awaitFutureResult(t, future).Err)
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
}

func TestFollowerStoreApplyResultSendsAck(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("a")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, func() bool { return net.LastAck().MatchOffset == 1 }, time.Second, time.Millisecond)

	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	read, err := cs.ReadCommitted(context.Background(), store.ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, read.Messages, 1)
	require.Equal(t, uint64(1), read.Messages[0].MessageSeq)
}

func TestFollowerAckResultResetsBackoff(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("a")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	net.SetAckError(ch.ErrNotReady)
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16,
		ReplicationMinBackoff: time.Hour, ReplicationMaxBackoff: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]

	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskRPCPull)})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskStoreApply)})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskRPCAck)})
	require.Equal(t, 1, net.AckCalls())
	require.True(t, rc.replication.pendingAck)
	require.False(t, rc.replication.nextAckAt.IsZero())
	nextAckAt := rc.replication.nextAckAt

	net.SetAckError(nil)
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
	})
	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: nextAckAt.Add(-time.Millisecond)})
	require.Equal(t, 1, net.AckCalls())
	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: nextAckAt.Add(time.Millisecond)})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskRPCAck)})
	require.Equal(t, 2, net.AckCalls())
	require.False(t, rc.replication.pendingAck)
	require.Zero(t, rc.replication.backoff)
	require.True(t, rc.replication.nextAckAt.IsZero())
}

func TestStoreApplyPoolFullKeepsOnePendingPullAndRetries(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("a")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := newBlockingApplyFactory()
	factory.BlockApplies()
	g, err := NewGroup(Config{
		LocalNode:    2,
		ReactorCount: 1,
		MailboxSize:  16,
		Store:        factory,
		Transport:    net,
		WorkerPools: worker.PoolsConfig{
			StoreApply: worker.PoolConfig{Name: "store-apply", Workers: 1, QueueSize: 1},
		},
	})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, factory.ApplyStarted, time.Second, time.Millisecond)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Millisecond)}))
	require.Equal(t, 1, net.PullCalls())

	factory.UnblockApplies()
	require.Eventually(t, func() bool { return net.LastAck().MatchOffset == 1 }, time.Second, time.Millisecond)
}

func TestFollowerStoreApplyErrorRetriesSamePendingPull(t *testing.T) {
	meta := followerTestMeta("apply-error-retry")
	state := replicationState{pendingPull: &transport.PullResponse{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, LeaderHW: 1, Records: []ch.Record{{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1}}}, applyOpID: 7}
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16, ReplicationMinBackoff: time.Nanosecond, ReplicationMaxBackoff: time.Nanosecond})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication = state

	result := worker.Result{Kind: worker.TaskStoreApply, Fence: ch.Fence{ChannelKey: meta.Key, Generation: 1, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7}, Err: ch.ErrNotReady}
	r.handleStoreApplyResult(result)
	require.NotNil(t, rc.replication.pendingPull)
	require.Equal(t, uint64(1), rc.replication.pendingPull.Records[0].Index)
	require.Zero(t, rc.replication.applyOpID)
}

func TestStaleStoreApplyCompletionDoesNotClearNewerApplyInflight(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("stale-apply")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pendingPull = &transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    2,
		Records:     []ch.Record{{ID: 2, Index: 2, Payload: []byte("new"), SizeBytes: 3}},
	}
	rc.replication.applyOpID = 9

	stale := worker.Result{
		Kind:       worker.TaskStoreApply,
		Fence:      ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 8},
		StoreApply: &worker.StoreApplyResult{LEO: 1},
	}
	r.handleStoreApplyResult(stale)

	require.Equal(t, ch.OpID(9), rc.replication.applyOpID)
	require.NotNil(t, rc.replication.pendingPull)
	require.Equal(t, uint64(2), rc.replication.pendingPull.Records[0].Index)
	require.Zero(t, rc.state.LEO)
}

func TestAckPoolFullKeepsPendingAckAndRetriesOnTick(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("a")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	factory := newBlockingApplyFactory()
	factory.BlockApplies()
	g, err := NewGroup(Config{
		LocalNode:             2,
		ReactorCount:          1,
		MailboxSize:           32,
		Store:                 factory,
		Transport:             net,
		ReplicationMinBackoff: time.Nanosecond,
		ReplicationMaxBackoff: time.Nanosecond,
		WorkerPools:           worker.PoolsConfig{RPC: worker.PoolConfig{Name: "rpc", Workers: 1, QueueSize: 1}},
	})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	require.Eventually(t, factory.ApplyStarted, time.Second, time.Millisecond)

	net.BlockPulls()
	blockerFence1 := ch.Fence{ChannelKey: "1:blocker", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 901}
	blockerFence2 := ch.Fence{ChannelKey: "1:blocker", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 902}
	require.NoError(t, g.pools.Submit(context.Background(), worker.Task{Kind: worker.TaskRPCPull, Fence: blockerFence1, RPCPull: &worker.RPCPullTask{Node: 1, Request: transport.PullRequest{ChannelKey: "1:blocker", NextOffset: 1}}}))
	require.NoError(t, g.pools.Submit(context.Background(), worker.Task{Kind: worker.TaskRPCPull, Fence: blockerFence2, RPCPull: &worker.RPCPullTask{Node: 1, Request: transport.PullRequest{ChannelKey: "1:blocker", NextOffset: 1}}}))

	factory.UnblockApplies()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)}))
	require.Equal(t, 0, net.AckCalls())

	net.UnblockPulls()
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Millisecond)})
		return net.LastAck().MatchOffset == 1
	}, time.Second, time.Millisecond)
}

func TestStaleRPCPullCompletionDoesNotClearNewerPullInflight(t *testing.T) {
	state := replicationState{pullInflight: true, pullOpID: 2}
	stale := worker.Result{Kind: worker.TaskRPCPull, Fence: ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}, RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{ChannelKey: "1:a", Epoch: 1, LeaderEpoch: 1}}}
	applied := state.applyPullResult(stale, ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: state.pullOpID}, time.Unix(1, 0))
	require.False(t, applied)
	require.True(t, state.pullInflight)
	require.Equal(t, ch.OpID(2), state.pullOpID)
}

func TestStaleRPCAckCompletionDoesNotClearNewerAckInflight(t *testing.T) {
	state := replicationState{ackInflight: true, ackOpID: 4, ackMatch: 9}
	stale := worker.Result{Kind: worker.TaskRPCAck, Fence: ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 3}}
	applied := state.applyAckResult(stale, ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: state.ackOpID}, time.Unix(1, 0))
	require.False(t, applied)
	require.True(t, state.ackInflight)
	require.Equal(t, ch.OpID(4), state.ackOpID)
	require.Equal(t, uint64(9), state.ackMatch)
}

func TestAckErrorRetryKeepsSameMatchOffset(t *testing.T) {
	state := replicationState{ackInflight: true, ackOpID: 5, ackMatch: 9}
	result := worker.Result{Kind: worker.TaskRPCAck, Fence: ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 5}, Err: ch.ErrNotReady}
	applied := state.applyAckResult(result, result.Fence, time.Unix(1, 0))
	require.True(t, applied)
	require.False(t, state.ackInflight)
	require.True(t, state.pendingAck)
	require.Equal(t, uint64(9), state.pendingAckMatch)
}

func TestLeaderPullUsesStoreReadLogWorkerWithoutBlockingReactor(t *testing.T) {
	factory := newBlockingReadLogFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: -1})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	appendFuture, err := g.Submit(context.Background(), meta.Key, appendQuorumEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, appendFuture)

	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 77,
		Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadLogStarted, time.Second, time.Millisecond)

	metaFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	_, err = metaFuture.Await(context.Background())
	require.NoError(t, err)

	factory.UnblockReadLogs()
	_, err = pullFuture.Await(context.Background())
	require.NoError(t, err)
}

func TestLeaderPullWaiterFailsOnMetadataFence(t *testing.T) {
	factory := newBlockingReadLogFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: -1})
	require.NoError(t, err)
	defer g.Close()
	defer factory.UnblockReadLogs()

	meta := ch.Meta{Key: "1:pull-fence", ID: ch.ChannelID{ID: "pull-fence", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventPull, Key: meta.Key, OpID: 88, Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024}})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadLogStarted, time.Second, time.Millisecond)

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: updated}))
	_, err = pullFuture.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
}

func TestLeaderPullMismatchedChannelKeyFailsWithStaleMeta(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("pull-key-mismatch")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 201,
		Pull: transport.PullRequest{ChannelKey: "1:other", ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
	require.Empty(t, rc.pullWaiters)
}

func TestLeaderPullMismatchedChannelIDFailsWithStaleMeta(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("pull-id-mismatch")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 202,
		Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: ch.ChannelID{ID: "other", Type: meta.ID.Type}, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
	require.Empty(t, rc.pullWaiters)
}

func TestLeaderPullInvalidRangeFailsWithInvalidConfig(t *testing.T) {
	tests := []struct {
		name       string
		nextOffset uint64
		maxBytes   int
	}{
		{name: "zero next offset", nextOffset: 0, maxBytes: 1024},
		{name: "non positive max bytes", nextOffset: 1, maxBytes: 0},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := store.NewMemoryFactory()
			g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
			require.NoError(t, err)
			defer g.Close()

			meta := followerTestMeta("pull-invalid-range-" + tt.name)
			require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

			future, err := g.Submit(context.Background(), meta.Key, Event{
				Kind: EventPull,
				Key:  meta.Key,
				OpID: ch.OpID(210 + i),
				Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, NextOffset: tt.nextOffset, MaxBytes: tt.maxBytes},
			})
			require.NoError(t, err)
			_, err = future.Await(context.Background())
			require.ErrorIs(t, err, ch.ErrInvalidConfig)

			rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
			require.Empty(t, rc.pullWaiters)
		})
	}
}

func TestLeaderPullWaiterFailsWithErrClosedOnGroupClose(t *testing.T) {
	factory := newNonCancelingBlockingReadLogFactory()
	g, err := NewGroup(Config{
		LocalNode:                   1,
		ReactorCount:                1,
		MailboxSize:                 16,
		Store:                       factory,
		WorkerPools:                 worker.PoolsConfig{StoreRead: worker.PoolConfig{Name: "test-store-read", Workers: 1, QueueSize: 1}},
		LeaderRecentRecordCacheSize: -1,
	})
	require.NoError(t, err)
	closeStarted := false
	closeCompleted := false
	defer func() {
		factory.UnblockReadLogs()
		if !closeStarted && !closeCompleted {
			_ = g.Close()
		}
	}()

	meta := ch.Meta{Key: "1:pull-close", ID: ch.ChannelID{ID: "pull-close", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 89,
		Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	factory.waitReadLogStarted(t)

	closeDone := make(chan error, 1)
	closeStarted = true
	go func() {
		closeDone <- g.Close()
	}()

	waitForCloseAfterFailure := func() {
		factory.UnblockReadLogs()
		select {
		case <-closeDone:
			closeCompleted = true
		case <-time.After(time.Second):
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err = pullFuture.Await(ctx)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) {
		waitForCloseAfterFailure()
		t.Fatal("timed out waiting for pull future to fail with ErrClosed")
	}
	if !errors.Is(err, ch.ErrClosed) {
		waitForCloseAfterFailure()
		t.Fatalf("pull future failed with %v, want %v", err, ch.ErrClosed)
	}

	factory.UnblockReadLogs()
	select {
	case err := <-closeDone:
		closeCompleted = true
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for group close after unblocking ReadLog")
	}
	require.ErrorIs(t, err, ch.ErrClosed)
}

func TestLeaderPullReadLogPoolFullFailsFuture(t *testing.T) {
	factory := newBlockingReadLogFactory()
	g, err := NewGroup(Config{
		LocalNode:                   1,
		ReactorCount:                1,
		MailboxSize:                 16,
		Store:                       factory,
		WorkerPools:                 worker.PoolsConfig{StoreRead: worker.PoolConfig{Name: "test-store-read", Workers: 1, QueueSize: 1}},
		LeaderRecentRecordCacheSize: -1,
	})
	require.NoError(t, err)
	defer g.Close()
	defer factory.UnblockReadLogs()

	meta := ch.Meta{Key: "1:pull-backpressure", ID: ch.ChannelID{ID: "pull-backpressure", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	first, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 90,
		Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	require.Eventually(t, factory.ReadLogStarted, time.Second, time.Millisecond)

	second, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 91,
		Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	requireFuturePending(t, second)
	third, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 92,
		Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = third.Await(ctx)
	require.ErrorIs(t, err, ch.ErrBackpressured)
	rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
	require.NotContains(t, rc.pullWaiters, ch.OpID(92))

	factory.UnblockReadLogs()
	_, err = first.Await(ctx)
	require.NoError(t, err)
	_, err = second.Await(ctx)
	require.NoError(t, err)
}

func TestLeaderPullDuplicateOpIDRejectsBeforeFollowerStateUpdates(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("duplicate-pull-state", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1, 2}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 0}

	lastPullAt := time.Unix(10, 0)
	nextExpectedPullAt := time.Unix(20, 0)
	rc.followers[2] = &followerLifecycle{
		Match:              0,
		LastPullAt:         lastPullAt,
		Parked:             true,
		Stopped:            true,
		NextExpectedPullAt: nextExpectedPullAt,
	}
	rc.pullWaiters = map[ch.OpID]*pullWaiter{
		99: {future: NewFuture(), follower: 2, nextOffset: 1},
	}

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		OpID:    99,
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := future.Await(ctx)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.Equal(t, lastPullAt, rc.followers[2].LastPullAt)
	require.True(t, rc.followers[2].Parked)
	require.True(t, rc.followers[2].Stopped)
	require.Equal(t, nextExpectedPullAt, rc.followers[2].NextExpectedPullAt)
	require.Equal(t, uint64(0), rc.followers[2].Match)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
}

func TestLeaderPullResponsePacesCaughtUpIdleFollower(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pace-idle", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleSlowdownAfter: time.Millisecond, IdlePullMinInterval: 10 * time.Millisecond, IdlePullMaxInterval: time.Second, IdleEvictAfter: 2 * time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1,
			Follower: 2, NextOffset: 4, MaxBytes: 1024,
		},
		OpID: 10,
	})
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)

	result := awaitFutureResult(t, future)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.Equal(t, uint64(3), result.Pull.ActivityVersion)
	require.GreaterOrEqual(t, result.Pull.NextPullAfter, 10*time.Millisecond)
	require.NotNil(t, rc.followers[2])
	require.True(t, rc.followers[2].Parked)
}

func TestLeaderDoesNotOfferStopWhileNonISRFollowerLagging(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("stop-non-isr-lagging", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdlePullMinInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[3] = machine.ReplicaProgress{Match: 2}
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 4, MaxBytes: 1024,
		},
		OpID: 21,
	})
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)

	result := awaitFutureResult(t, future)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.Greater(t, result.Pull.NextPullAfter, time.Duration(0))
}

func TestLeaderReturnsStopWhenAllReplicasCaughtUpAndIdle(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("stop-all-caught-up", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdlePullMinInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[3] = machine.ReplicaProgress{Match: 3}
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 4, MaxBytes: 1024,
		},
		OpID: 22,
	})
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)

	result := awaitFutureResult(t, future)
	require.Equal(t, transport.PullControlStop, result.Pull.Control)
	require.Equal(t, uint64(3), result.Pull.ActivityVersion)
	require.Zero(t, result.Pull.NextPullAfter)
	require.True(t, rc.followers[2].StopOffered)
	require.Equal(t, uint64(3), rc.followers[2].StopOfferedVersion)
}

func TestLeaderDoesNotLetFuturePullOffsetFakeStopEligibility(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("stop-future-offset", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1, 2}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdlePullMinInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 2}
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 99, MaxBytes: 1024,
		},
		OpID: 23,
	})
	r.handleStoreReadLogResult(sink.awaitResultKind(t, worker.TaskStoreReadLog))

	result := awaitFutureResult(t, future)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.Less(t, rc.followers[2].Match, rc.state.LEO)
}

func TestLeaderPullResponsePacesFromReplicationIdlePollIntervalByDefault(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pace-default-idle-poll", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		ReplicationIdlePollInterval: 250 * time.Millisecond,
		IdleSlowdownAfter:           time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.lifecycle.LastAppendAt = time.Now()
	rc.lifecycle.ActivityVersion = 3

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1,
			Follower: 2, NextOffset: 4, MaxBytes: 1024,
		},
		OpID: 12,
	})
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)

	result := awaitFutureResult(t, future)
	require.Equal(t, 250*time.Millisecond, result.Pull.NextPullAfter)
}

func TestLeaderPullResponsePacesFromExplicitIdlePullMinInterval(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pace-explicit-min", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		ReplicationIdlePollInterval: time.Second,
		IdleSlowdownAfter:           time.Hour,
		IdlePullMinInterval:         25 * time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.lifecycle.LastAppendAt = time.Now()
	rc.lifecycle.ActivityVersion = 3

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1,
			Follower: 2, NextOffset: 4, MaxBytes: 1024,
		},
		OpID: 13,
	})
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)

	result := awaitFutureResult(t, future)
	require.Equal(t, 25*time.Millisecond, result.Pull.NextPullAfter)
}

func TestLeaderPullResponsePacesLaggingFollowerWithRecordsImmediately(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := testMeta("pace-lagging", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	_, err = cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: []ch.Record{
		{ID: 1, Payload: []byte("a"), SizeBytes: 1},
		{ID: 2, Payload: []byte("b"), SizeBytes: 1},
		{ID: 3, Payload: []byte("c"), SizeBytes: 1},
	}})
	require.NoError(t, err)

	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleSlowdownAfter: time.Millisecond, IdlePullMinInterval: 10 * time.Millisecond, IdlePullMaxInterval: time.Second,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.HW = 3
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 1}

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1,
			Follower: 2, NextOffset: 2, MaxBytes: 1024,
		},
		OpID: 11,
	})
	r.handleStoreReadLogResult(sink.awaitResultKind(t, worker.TaskStoreReadLog))

	result := awaitFutureResult(t, future)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.Equal(t, uint64(3), result.Pull.ActivityVersion)
	require.Len(t, result.Pull.Records, 2)
	require.Zero(t, result.Pull.NextPullAfter)
	require.False(t, rc.followers[2].Parked)
}

func TestLeaderPullContextCancelRemovesWaiterBeforeLateReadLogCompletion(t *testing.T) {
	factory := newNonCancelingBlockingReadLogFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, LeaderRecentRecordCacheSize: -1})
	require.NoError(t, err)
	defer g.Close()
	defer factory.UnblockReadLogs()

	meta := ch.Meta{Key: "1:pull-cancel", ID: ch.ChannelID{ID: "pull-cancel", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	ctx, cancel := context.WithCancel(context.Background())
	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind:    EventPull,
		Key:     meta.Key,
		OpID:    99,
		Context: ctx,
		Pull:    transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024},
	})
	require.NoError(t, err)
	factory.waitReadLogStarted(t)

	cancel()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0)}))
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer waitCancel()
	_, err = pullFuture.Await(waitCtx)
	require.ErrorIs(t, err, context.Canceled)
	rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
	require.Empty(t, rc.pullWaiters)

	factory.UnblockReadLogs()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Unix(1, 0).Add(time.Millisecond)}))
	require.Empty(t, rc.pullWaiters)
}

func TestLeaderIgnoresAckAfterLeaderEpochBump(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:a", ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	staleAck := Event{
		Kind: EventAck,
		Key:  meta.Key,
		Ack:  transport.AckRequest{ChannelKey: meta.Key, Epoch: 1, LeaderEpoch: 1, Follower: 2, MatchOffset: 100},
	}
	meta.LeaderEpoch = 2
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, staleAck))

	future, err := g.Submit(context.Background(), meta.Key, appendQuorumEvent(meta, 1, "requires-current-ack"))
	require.NoError(t, err)
	requireFuturePending(t, future)
}

func TestLeaderIgnoresAckFromUnknownFollower(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:ack-unknown", ID: ch.ChannelID{ID: "ack-unknown", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	future, err := g.Submit(context.Background(), meta.Key, appendQuorumEvent(meta, 1, "requires-known-follower"))
	require.NoError(t, err)
	requireFuturePending(t, future)

	ackFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventAck, Key: meta.Key, Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: 1, LeaderEpoch: 1, Follower: 99, MatchOffset: 1}})
	require.NoError(t, err)
	_, err = ackFuture.Await(context.Background())
	require.NoError(t, err)
	requireFuturePending(t, future)
}

func TestLeaderIgnoresAckWithMismatchedChannelKey(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := ch.Meta{Key: "1:ack-key", ID: ch.ChannelID{ID: "ack-key", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	future, err := g.Submit(context.Background(), meta.Key, appendQuorumEvent(meta, 1, "requires-matching-ack-key"))
	require.NoError(t, err)
	requireFuturePending(t, future)

	ackFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventAck, Key: meta.Key, Ack: transport.AckRequest{ChannelKey: "1:other", Epoch: 1, LeaderEpoch: 1, Follower: 2, MatchOffset: 1}})
	require.NoError(t, err)
	_, err = ackFuture.Await(context.Background())
	require.NoError(t, err)
	requireFuturePending(t, future)
}

func TestLeaderStoppedAckRejectsMatchAboveLEO(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("leader-stopped-ack-above-leo", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1, 2}
	meta.MinISR = 2
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdleEvictCheckInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.state.Progress[1] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 3
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handleAck(Event{
		Kind: EventAck, Key: meta.Key, Future: future,
		Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, MatchOffset: 4, ActivityVersion: 3, Stopped: true},
	})
	_, err := future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.False(t, rc.followers[2].Stopped)
	require.Zero(t, rc.followers[2].StopAckVersion)
	require.Equal(t, uint64(3), rc.followers[2].Match)
	require.Equal(t, uint64(3), rc.state.Progress[2].Match)
	require.Equal(t, uint64(3), rc.state.HW)
	require.Contains(t, r.channels, meta.Key)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)
}

func TestLeaderStoppedAckRejectsMismatchedOfferedVersion(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := testMeta("leader-stopped-ack-offer-fence", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1, 2}
	meta.MinISR = 2
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.state.Progress[1] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.lifecycle.ActivityVersion = 3
	r.syncLeaderFollowers(rc)
	rc.followers[2].StopOffered = true
	rc.followers[2].StopOfferedVersion = 2

	future := NewFuture()
	r.handleAck(Event{
		Kind: EventAck, Key: meta.Key, Future: future,
		Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true},
	})
	_, err := future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.False(t, rc.followers[2].Stopped)
	require.Zero(t, rc.followers[2].StopAckVersion)
	require.Equal(t, uint64(3), rc.followers[2].Match)
	require.Equal(t, uint64(3), rc.state.Progress[2].Match)
	require.Equal(t, uint64(3), rc.state.HW)
}

func TestLeaderEvictsOnlyAfterAllFollowersStoppedAck(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("leader-evict-after-all-stopped", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2, 3}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdleEvictCheckInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[3] = machine.ReplicaProgress{Match: 3}
	r.syncLeaderFollowers(rc)

	firstFuture := NewFuture()
	r.handleAck(Event{
		Kind: EventAck, Key: meta.Key, Future: firstFuture,
		Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true},
	})
	require.NoError(t, awaitFutureResult(t, firstFuture).Err)
	require.Contains(t, r.channels, meta.Key)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)

	secondFuture := NewFuture()
	r.handleAck(Event{
		Kind: EventAck, Key: meta.Key, Future: secondFuture,
		Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 3, MatchOffset: 3, ActivityVersion: 3, Stopped: true},
	})
	require.NoError(t, awaitFutureResult(t, secondFuture).Err)
	require.Equal(t, LeaderLifecycleCheckpointing, rc.runtimeLifecycle.LeaderPhase)
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.Contains(t, r.channels, meta.Key)

	completeLeaderCheckpointAndDue(t, r, checkpoint)
	require.NotContains(t, r.channels, meta.Key)
}

func TestStoppedAckRetiresObsoletePullHintInflightAndAllowsLeaderEviction(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("stopped-ack-retires-pull-hint", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1, 2}
	meta.MinISR = 2
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdleEvictCheckInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.state.Progress[1] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 3
	r.syncLeaderFollowers(rc)
	rc.followers[2].HintInflight = true
	rc.followers[2].HintInflightOpID = 99
	rc.followers[2].LastHintVersion = 3
	rc.pullHintInflight = map[ch.OpID]pullHintInflight{
		99: {follower: 2, activityVersion: 3, reason: transport.PullHintReasonAppend},
	}

	future := NewFuture()
	r.handleAck(Event{
		Kind: EventAck, Key: meta.Key, Future: future,
		Ack: transport.AckRequest{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
		},
	})
	require.NoError(t, awaitFutureResult(t, future).Err)
	require.Empty(t, rc.pullHintInflight)
	require.False(t, rc.followers[2].HintInflight)
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	completeLeaderCheckpointAndDue(t, r, checkpoint)
	require.NotContains(t, r.channels, meta.Key)
}

func TestOldPullHintCompletionDoesNotClearNewerHintInflight(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := testMeta("old-pull-hint-completion", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 4
	rc.lifecycle.ActivityVersion = 4
	rc.followers[2].HintInflight = true
	rc.followers[2].HintInflightOpID = 12
	rc.followers[2].LastHintVersion = 4
	rc.pullHintInflight = map[ch.OpID]pullHintInflight{
		11: {follower: 2, activityVersion: 3, reason: transport.PullHintReasonAppend},
		12: {follower: 2, activityVersion: 4, reason: transport.PullHintReasonAppend},
	}

	r.handleRPCPullHintResult(worker.Result{
		Kind:        worker.TaskRPCPullHint,
		Fence:       ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 11},
		RPCPullHint: &worker.RPCPullHintResult{},
	})

	require.True(t, rc.followers[2].HintInflight)
	require.Contains(t, rc.pullHintInflight, ch.OpID(12))
}

func TestLeaderStoppedAckFenceBumpRequiresCurrentFenceAcksBeforeEviction(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("leader-stopped-ack-fence-bump", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2, 3}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Hour, IdleEvictCheckInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.state.Progress[1] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[3] = machine.ReplicaProgress{Match: 3}
	rc.lifecycle.LastAppendAt = time.Now()
	rc.lifecycle.ActivityVersion = 3
	r.syncLeaderFollowers(rc)

	for _, follower := range []ch.NodeID{2, 3} {
		future := NewFuture()
		r.handleAck(Event{
			Kind: EventAck, Key: meta.Key, Future: future,
			Ack: transport.AckRequest{
				ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
				Follower: follower, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
			},
		})
		require.NoError(t, awaitFutureResult(t, future).Err)
	}
	require.True(t, rc.followers[2].Stopped)
	require.True(t, rc.followers[3].Stopped)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)

	fenced := meta
	fenced.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, fenced))

	rc.lifecycle.LastAppendAt = time.Now().Add(-2 * time.Hour)
	r.tickLifecycle(rc, time.Now())
	require.Contains(t, r.channels, meta.Key)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)

	firstFuture := NewFuture()
	r.handleAck(Event{
		Kind: EventAck, Key: meta.Key, Future: firstFuture,
		Ack: transport.AckRequest{
			ChannelKey: meta.Key, Epoch: fenced.Epoch, LeaderEpoch: fenced.LeaderEpoch,
			Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
		},
	})
	require.NoError(t, awaitFutureResult(t, firstFuture).Err)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)

	secondFuture := NewFuture()
	r.handleAck(Event{
		Kind: EventAck, Key: meta.Key, Future: secondFuture,
		Ack: transport.AckRequest{
			ChannelKey: meta.Key, Epoch: fenced.Epoch, LeaderEpoch: fenced.LeaderEpoch,
			Follower: 3, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
		},
	})
	require.NoError(t, awaitFutureResult(t, secondFuture).Err)
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.Equal(t, fenced.LeaderEpoch, checkpoint.Fence.LeaderEpoch)
	completeLeaderCheckpointAndDue(t, r, checkpoint)
	require.NotContains(t, r.channels, meta.Key)
}

func TestSingleNodeClusterLeaderEvictsAfterIdleCheckpoint(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("single-node-cluster-leader-evict", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdleEvictCheckInterval: time.Millisecond, AppendBatchMaxRecords: 1,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	res := appendDirect(t, r, sink, meta, 1, "single-node cluster append")
	require.NoError(t, res.Err)
	rc := r.channels[meta.Key]
	require.Equal(t, uint64(1), rc.state.LEO)
	require.Equal(t, uint64(1), rc.state.HW)

	r.tickLifecycle(rc, rc.lifecycle.LastAppendAt.Add(time.Hour))
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	completeLeaderCheckpointAndDue(t, r, checkpoint)
	require.NotContains(t, r.channels, meta.Key)
}

func TestLeaderAppendPopulatesRecentRecordCacheAfterDurableStore(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-populate")
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))

	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "b").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	rc := r.channels[meta.Key]
	require.Equal(t, uint64(2), rc.recentRecords.base())
	require.Equal(t, uint64(3), rc.recentRecords.lastOffset())
	records, ok := rc.recentRecords.slice(2, 3, 1024)
	require.True(t, ok)
	require.Equal(t, []uint64{2, 3}, recordIndexes(records))
}

func TestLeaderRecentRecordCacheClearsOnMetadataFenceAndFollowerRole(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("recent-cache-clear")
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 10, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.False(t, r.channels[meta.Key].recentRecords.empty())

	updated := meta
	updated.LeaderEpoch = 2
	require.NoError(t, applyMetaDirect(t, r, updated))
	require.True(t, r.channels[meta.Key].recentRecords.empty())

	updated.Leader = 2
	require.NoError(t, applyMetaDirect(t, r, updated))
	require.True(t, r.channels[meta.Key].recentRecords.empty())
}

func TestApplyMetaColdSingleNodeClusterLeaderCanEvictWithoutAppend(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("cold-leader-evict", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdleEvictCheckInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Zero(t, rc.lifecycle.LastAppendAt)

	r.tickLifecycle(rc, time.Now().Add(time.Hour))
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	completeLeaderCheckpointAndDue(t, r, checkpoint)
	require.NotContains(t, r.channels, meta.Key)
}

func TestLeaderCheckpointCompletionStaleAfterNewAppendDoesNotEvict(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("leader-stale-checkpoint-new-append", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdleEvictCheckInterval: time.Millisecond, AppendBatchMaxRecords: 1,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "before checkpoint").Err)
	rc := r.channels[meta.Key]
	r.tickLifecycle(rc, rc.lifecycle.LastAppendAt.Add(time.Hour))
	staleCheckpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)

	appendFuture := NewFuture()
	r.handleAppend(appendEventWithFuture(meta, 2, "after checkpoint", appendFuture))
	require.False(t, rc.lifecycle.CheckpointInflight)
	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	require.NoError(t, awaitFutureResult(t, appendFuture).Err)

	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: staleCheckpoint})
	require.Contains(t, r.channels, meta.Key)
}

func TestLeaderCheckpointCompletionDefersEvictionBehindQueuedAppend(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("leader-checkpoint-defers-to-append", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = rc.state.LEO
	rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleCheckpointing
	rc.lifecycle.CheckpointInflight = true
	rc.lifecycle.CheckpointOpID = 99
	rc.lifecycle.CheckpointActivityVersion = rc.lifecycle.ActivityVersion

	appendFuture := NewFuture()
	require.NoError(t, r.Submit(PriorityNormal, appendEventWithFuture(meta, 1, "queued append", appendFuture)))
	require.NoError(t, r.SubmitCompletion(Event{Kind: EventWorkerResult, Key: meta.Key, Worker: worker.Result{
		Kind: worker.TaskStoreCheckpoint,
		Fence: ch.Fence{
			ChannelKey: meta.Key, Generation: rc.state.Generation,
			Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 99,
		},
		StoreCheckpoint: &worker.StoreCheckpointResult{},
	}}))

	events := r.mailbox.DrainInto(nil, defaultReactorDrain)
	require.Len(t, events, 2)
	for _, event := range events {
		r.handle(event)
	}

	require.Contains(t, r.channels, meta.Key)
	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	require.NoError(t, awaitFutureResult(t, appendFuture).Err)
	require.Contains(t, r.channels, meta.Key)
	require.False(t, rc.lifecycle.CheckpointInflight)
}

func TestLeaderReadyEvictionDefersWhenAppendSubmitsAfterReadyEventQueued(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("leader-ready-evict-defers-to-late-append", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = rc.state.LEO
	rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleCheckpointing
	rc.lifecycle.CheckpointInflight = true
	rc.lifecycle.CheckpointOpID = 99
	rc.lifecycle.CheckpointActivityVersion = rc.lifecycle.ActivityVersion

	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: worker.Result{
		Kind: worker.TaskStoreCheckpoint,
		Fence: ch.Fence{
			ChannelKey: meta.Key, Generation: rc.state.Generation,
			Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 99,
		},
		StoreCheckpoint: &worker.StoreCheckpointResult{},
	}})
	require.True(t, rc.lifecycle.CheckpointReadyQueued)

	appendFuture := NewFuture()
	require.NoError(t, r.Submit(PriorityNormal, appendEventWithFuture(meta, 1, "late append", appendFuture)))
	events := r.mailbox.DrainInto(nil, defaultReactorDrain)
	require.Len(t, events, 2)
	require.Equal(t, EventLeaderEvictReady, events[0].Kind)
	require.Equal(t, EventAppend, events[1].Kind)
	for _, event := range events {
		r.handle(event)
	}
	for _, event := range r.mailbox.DrainInto(nil, defaultReactorDrain) {
		r.handle(event)
	}

	require.Contains(t, r.channels, meta.Key)
	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	require.NoError(t, awaitFutureResult(t, appendFuture).Err)
	require.Contains(t, r.channels, meta.Key)
	require.False(t, rc.lifecycle.CheckpointReady)
}

func TestLeaderReadyEvictionDefersWhileAppendReservedBeforeSubmit(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("leader-ready-evict-defers-reserved-append", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = rc.state.LEO
	rc.lifecycle.CheckpointReady = true
	rc.lifecycle.CheckpointReadyActivityVersion = rc.lifecycle.ActivityVersion

	release := r.reserveAppend(meta.Key)
	r.submitLeaderEvictReady(rc, time.Now(), r.currentAppendSubmitSeq(meta.Key))
	events := r.mailbox.DrainInto(nil, defaultReactorDrain)
	require.Len(t, events, 1)
	require.Equal(t, EventLeaderEvictReady, events[0].Kind)
	r.handle(events[0])
	require.Contains(t, r.channels, meta.Key)

	appendFuture := NewFuture()
	release()
	require.NoError(t, r.Submit(PriorityNormal, appendEventWithFuture(meta, 1, "reserved append", appendFuture)))
	for _, event := range r.mailbox.DrainInto(nil, defaultReactorDrain) {
		r.handle(event)
	}
	require.Contains(t, r.channels, meta.Key)
	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	require.NoError(t, awaitFutureResult(t, appendFuture).Err)
	require.Contains(t, r.channels, meta.Key)
}

func TestLeaderReadyEvictionReservationRetryWaitsForInterval(t *testing.T) {
	factory := store.NewMemoryFactory()
	pools := newDirectTestPools(t, factory, captureCompletionSink{results: make(chan worker.Result, 4)})
	defer pools.Close()

	meta := testMeta("leader-ready-evict-reservation-retry", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictCheckInterval: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.CheckpointReady = true
	rc.lifecycle.CheckpointReadyActivityVersion = rc.lifecycle.ActivityVersion

	release := r.reserveAppend(meta.Key)
	now := time.Now()
	r.submitLeaderEvictReady(rc, now, r.currentAppendSubmitSeq(meta.Key))
	events := r.mailbox.DrainInto(nil, defaultReactorDrain)
	require.Len(t, events, 1)
	r.handle(events[0])
	require.Contains(t, r.channels, meta.Key)
	retryAt := rc.lifecycle.CheckpointRetryAt
	require.False(t, retryAt.IsZero())

	r.processDue(now.Add(time.Second))
	require.Empty(t, r.mailbox.DrainInto(nil, defaultReactorDrain))
	require.Contains(t, r.channels, meta.Key)

	release()
	r.processDue(retryAt.Add(time.Millisecond))
	events = r.mailbox.DrainInto(nil, defaultReactorDrain)
	require.Len(t, events, 1)
	r.handle(events[0])
	require.NotContains(t, r.channels, meta.Key)
}

func TestLeaderReadyEvictionIgnoresUnrelatedAppendReservation(t *testing.T) {
	factory := store.NewMemoryFactory()
	pools := newDirectTestPools(t, factory, captureCompletionSink{results: make(chan worker.Result, 4)})
	defer pools.Close()

	meta := testMeta("leader-ready-evict-unrelated-reservation", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	otherMeta := testMeta("leader-ready-evict-other-channel", 1, 1)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, applyMetaDirect(t, r, otherMeta))
	rc := r.channels[meta.Key]
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.CheckpointReady = true
	rc.lifecycle.CheckpointReadyActivityVersion = rc.lifecycle.ActivityVersion

	now := time.Now()
	r.submitLeaderEvictReady(rc, now, r.currentAppendSubmitSeq(meta.Key))
	otherRelease := r.reserveAppend(otherMeta.Key)
	defer otherRelease()
	events := r.mailbox.DrainInto(nil, defaultReactorDrain)
	require.Len(t, events, 1)

	r.handle(events[0])
	require.NotContains(t, r.channels, meta.Key)
}

func TestLeaderPromotionRefreshesActivityVersionFromDurableLEO(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("leader-promotion-activity-version", 1, 2)
	meta.MinISR = 2
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	require.Equal(t, ch.RoleFollower, rc.state.Role)

	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	_, err = cs.ApplyFollower(context.Background(), store.ApplyFollowerRequest{
		LeaderHW: 3,
		Records: []ch.Record{
			{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1},
			{ID: 2, Index: 2, Payload: []byte("b"), SizeBytes: 1},
			{ID: 3, Index: 3, Payload: []byte("c"), SizeBytes: 1},
		},
	})
	require.NoError(t, err)
	rc.state.LEO = 3
	rc.state.HW = 3
	require.Zero(t, rc.lifecycle.ActivityVersion)

	promoted := meta
	promoted.Leader = 1
	promoted.LeaderEpoch = 2
	require.NoError(t, applyMetaDirect(t, r, promoted))
	require.Equal(t, ch.RoleLeader, rc.state.Role)
	require.Equal(t, uint64(3), rc.lifecycle.ActivityVersion)

	rc.lifecycle.LoadedAt = time.Now().Add(-time.Hour)
	r.syncLeaderFollowers(rc)
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	r.syncLeaderFollowers(rc)
	pullFuture := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     promoted.Key,
		Future:  pullFuture,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: promoted.Key, ChannelID: promoted.ID, Epoch: promoted.Epoch, LeaderEpoch: promoted.LeaderEpoch,
			Follower: 2, NextOffset: 4, MaxBytes: 1024,
		},
		OpID: 41,
	})
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
	pullResult := awaitFutureResult(t, pullFuture)
	require.Equal(t, transport.PullControlStop, pullResult.Pull.Control)
	require.Equal(t, uint64(3), pullResult.Pull.ActivityVersion)

	ackFuture := NewFuture()
	r.handleAck(Event{
		Kind:   EventAck,
		Key:    promoted.Key,
		Future: ackFuture,
		Ack: transport.AckRequest{
			ChannelKey: promoted.Key, Epoch: promoted.Epoch, LeaderEpoch: promoted.LeaderEpoch,
			Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
		},
	})
	require.NoError(t, awaitFutureResult(t, ackFuture).Err)

	r.tryEvictLeader(rc, time.Now())
	r.handleLeaderCheckpointResult(rc, sink.awaitResultKind(t, worker.TaskStoreCheckpoint))
	events := r.mailbox.DrainInto(nil, defaultReactorDrain)
	require.Len(t, events, 1)
	r.handle(events[0])
	require.NotContains(t, r.channels, promoted.Key)
}

func TestLeaderPullCoveredByRecentCacheCompletesWithoutStoreRead(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pull-covered-by-cache", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 10,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "b").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 101,
	})

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.Zero(t, result.Pull.NextPullAfter)
	require.Equal(t, uint64(2), result.Pull.LeaderLEO)
	require.Len(t, result.Pull.Records, 2)
	require.Equal(t, uint64(1), result.Pull.Records[0].Index)
	require.Equal(t, uint64(2), result.Pull.Records[1].Index)
	rc := r.channels[meta.Key]
	require.NotNil(t, rc.followers[2])
	require.False(t, rc.followers[2].Parked)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
}

func TestLeaderPullCacheHitDoesNotOfferStopWithRecords(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pull-cache-hit-no-stop-with-records", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2,
		IdleEvictAfter: time.Millisecond, IdlePullMinInterval: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	rc.state.HW = 1
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 1}
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = rc.state.LEO
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 338,
	})

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Len(t, result.Pull.Records, 1)
	require.Equal(t, uint64(1), result.Pull.Records[0].Index)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.NotNil(t, rc.followers[2])
	require.False(t, rc.followers[2].StopOffered)
	require.Zero(t, rc.followers[2].StopOfferedVersion)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
}

func TestLeaderPullCaughtUpCompletesEmptyWithoutStoreRead(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pull-caught-up-cache", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, IdlePullMinInterval: time.Millisecond, IdleEvictAfter: 2 * time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	rc := r.channels[meta.Key]
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 1

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 2, MaxBytes: 1024,
		},
		OpID: 102,
	})

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Equal(t, transport.PullControlContinue, result.Pull.Control)
	require.Equal(t, uint64(1), result.Pull.LeaderLEO)
	require.Empty(t, result.Pull.Records)
	require.Greater(t, result.Pull.NextPullAfter, time.Duration(0))
	require.NotNil(t, rc.followers[2])
	require.True(t, rc.followers[2].Parked)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
}

func TestLeaderPullBeforeRecentCacheBaseReadsStorePrefixOnly(t *testing.T) {
	factory := newCaptureReadLogRequestFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pull-before-cache-base", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2, LeaderRecentRecordCacheBytes: 1024,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "b").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 333,
	})

	req := factory.awaitRequest(t)
	require.Equal(t, uint64(1), req.FromOffset)
	require.Equal(t, uint64(1), req.MaxOffset)
	require.Equal(t, 1024, req.MaxBytes)
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	waiter := rc.pullWaiters[333]
	require.NotNil(t, waiter)
	require.True(t, waiter.mergeCacheSuffix)
	require.Equal(t, 1024, waiter.maxBytes)
	requireFuturePending(t, future)
}

func TestLeaderPullStorePrefixResultFencedAfterLeaderEpochChange(t *testing.T) {
	factory := newNonCancelingBlockingReadLogFactory()
	g, err := NewGroup(Config{
		LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2,
	})
	require.NoError(t, err)
	defer func() {
		factory.UnblockReadLogs()
		require.NoError(t, g.Close())
	}()

	meta := testMeta("pull-store-prefix-fenced-after-leader-epoch", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	require.NoError(t, awaitSubmit(g, meta.Key, appendEvent(meta, 1, "a")))
	require.NoError(t, awaitSubmit(g, meta.Key, appendEvent(meta, 2, "b")))
	require.NoError(t, awaitSubmit(g, meta.Key, appendEvent(meta, 3, "c")))

	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 339,
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
	})
	require.NoError(t, err)
	factory.waitReadLogStarted(t)

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: updated}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = pullFuture.Await(ctx)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
}

func TestLeaderPullReadsStorePrefixAndMergesRecentCacheSuffix(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pull-merge-cache-suffix", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "b").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 334,
	})
	r.handleStoreReadLogResult(sink.awaitResultKind(t, worker.TaskStoreReadLog))

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Equal(t, []uint64{1, 2, 3}, recordIndexes(result.Pull.Records))
	require.Zero(t, result.Pull.NextPullAfter)
}

func TestLeaderPullReturnsStorePrefixWhenRecentCacheRollsForwardBeforeMerge(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pull-cache-rolls-before-merge", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "b").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 335,
	})
	storeResult := sink.awaitResultKind(t, worker.TaskStoreReadLog)
	require.NoError(t, appendDirect(t, r, sink, meta, 4, "d").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 5, "e").Err)
	r.handleStoreReadLogResult(storeResult)

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Equal(t, []uint64{1}, recordIndexes(result.Pull.Records))
}

func TestLeaderPullMergeHonorsMaxBytes(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pull-merge-cache-max-bytes", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "aa").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "bbb").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 5,
		},
		OpID: 336,
	})
	r.handleStoreReadLogResult(sink.awaitResultKind(t, worker.TaskStoreReadLog))

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Equal(t, []uint64{1, 2}, recordIndexes(result.Pull.Records))
}

func TestLeaderPullMergeSkipsSuffixWhenNextRecordExceedsRemainingBytes(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pull-merge-cache-skip-oversized-suffix", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: 2,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "aaaa").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 2, "bbb").Err)
	require.NoError(t, appendDirect(t, r, sink, meta, 3, "c").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 5,
		},
		OpID: 337,
	})
	r.handleStoreReadLogResult(sink.awaitResultKind(t, worker.TaskStoreReadLog))

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Equal(t, []uint64{1}, recordIndexes(result.Pull.Records))
}

func TestLeaderPullCacheDisabledUsesStoreRead(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("pull-cache-disabled", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		AppendBatchMaxRecords: 1, LeaderRecentRecordCacheSize: -1,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "a").Err)

	future := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 103,
	})
	r.handleStoreReadLogResult(sink.awaitResultKind(t, worker.TaskStoreReadLog))

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Len(t, result.Pull.Records, 1)
	require.Equal(t, uint64(1), result.Pull.Records[0].Index)
}

func TestLeaderCheckpointFenceResetAllowsFutureEviction(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("leader-checkpoint-fence-reset", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16,
		IdleEvictAfter: time.Millisecond, IdleEvictCheckInterval: time.Millisecond, AppendBatchMaxRecords: 1,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "before fence").Err)
	rc := r.channels[meta.Key]

	r.tickLifecycle(rc, rc.lifecycle.LastAppendAt.Add(time.Hour))
	staleCheckpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.True(t, rc.lifecycle.CheckpointInflight)

	fenced := meta
	fenced.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, fenced))
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: staleCheckpoint})
	require.Contains(t, r.channels, meta.Key)
	require.False(t, rc.lifecycle.CheckpointInflight)
	require.Zero(t, rc.lifecycle.CheckpointOpID)
	require.Zero(t, rc.lifecycle.CheckpointActivityVersion)

	r.tickLifecycle(rc, rc.lifecycle.LastAppendAt.Add(2*time.Hour))
	nextCheckpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.Equal(t, fenced.LeaderEpoch, nextCheckpoint.Fence.LeaderEpoch)
	completeLeaderCheckpointAndDue(t, r, nextCheckpoint)
	require.NotContains(t, r.channels, meta.Key)
}

func TestNewAppendCancelsLeaderEvictionAndClearsStoppedFollowersNeedingData(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("append-cancels-leader-eviction", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2, 3}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NoError(t, appendDirect(t, r, sink, meta, 1, "before eviction").Err)
	rc := r.channels[meta.Key]
	rc.lifecycle.ActivityVersion = 1
	rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleCheckpointing
	rc.lifecycle.CheckpointInflight = true
	rc.lifecycle.CheckpointOpID = 99
	rc.lifecycle.CheckpointActivityVersion = 1
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 1}
	rc.state.Progress[3] = machine.ReplicaProgress{Match: 1}
	r.syncLeaderFollowers(rc)
	rc.followers[2].Stopped = true
	rc.followers[2].StopAckVersion = 1
	rc.followers[3].Stopped = true
	rc.followers[3].StopAckVersion = 1

	appendFuture := NewFuture()
	r.handleAppend(appendEventWithFuture(meta, 2, "reactivate followers", appendFuture))
	require.False(t, rc.lifecycle.CheckpointInflight)
	require.Zero(t, rc.lifecycle.CheckpointOpID)
	require.Equal(t, LeaderLifecycleServing, rc.runtimeLifecycle.LeaderPhase)

	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	require.NoError(t, awaitFutureResult(t, appendFuture).Err)
	require.Equal(t, uint64(2), rc.lifecycle.ActivityVersion)
	require.False(t, rc.followers[2].Stopped)
	require.False(t, rc.followers[3].Stopped)
	require.True(t, rc.followers[2].LastPullAt.IsZero())
	require.True(t, rc.followers[3].LastPullAt.IsZero())
}

func appendQuorumEvent(meta ch.Meta, id uint64, payload string) Event {
	event := appendEvent(meta, id, payload)
	event.Append.CommitMode = ch.CommitModeQuorum
	return event
}

func followerTestMeta(id string) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	return ch.Meta{Key: ch.ChannelKeyForID(channelID), ID: channelID, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
}

func completeLeaderCheckpointAndDue(t *testing.T, r *Reactor, checkpoint worker.Result) {
	t.Helper()
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: checkpoint})
	for i := 0; i < 4; i++ {
		r.processDue(time.Now().Add(time.Millisecond))
		events := r.mailbox.DrainInto(nil, defaultReactorDrain)
		if len(events) == 0 {
			return
		}
		for _, event := range events {
			r.handle(event)
		}
	}
}

type capturingTransport struct {
	mu        sync.Mutex
	pullCalls int
	ackCalls  int
	lastPull  transport.PullRequest
	lastAck   transport.AckRequest
	pullResp  transport.PullResponse
	pullErr   error
	ackErr    error
	blockPull chan struct{}
}

func newCapturingTransport() *capturingTransport {
	return &capturingTransport{}
}

func (t *capturingTransport) Pull(ctx context.Context, node ch.NodeID, req transport.PullRequest) (transport.PullResponse, error) {
	t.mu.Lock()
	t.pullCalls++
	t.lastPull = req
	block := t.blockPull
	resp := t.pullResp
	err := t.pullErr
	t.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return transport.PullResponse{}, ctx.Err()
		}
	}
	if err != nil {
		return transport.PullResponse{}, err
	}
	if resp.ChannelKey == "" {
		resp = transport.PullResponse{ChannelKey: req.ChannelKey, Epoch: req.Epoch, LeaderEpoch: req.LeaderEpoch, LeaderHW: req.NextOffset - 1, LeaderLEO: req.NextOffset - 1}
	}
	return resp, nil
}

func (t *capturingTransport) Ack(ctx context.Context, node ch.NodeID, req transport.AckRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ackCalls++
	t.lastAck = req
	return t.ackErr
}

func (t *capturingTransport) Notify(ctx context.Context, node ch.NodeID, req transport.NotifyRequest) error {
	return nil
}

func (t *capturingTransport) PullHint(ctx context.Context, node ch.NodeID, req transport.PullHintRequest) error {
	return nil
}

func (t *capturingTransport) LastPull() transport.PullRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastPull
}

func (t *capturingTransport) LastAck() transport.AckRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastAck
}

func (t *capturingTransport) PullCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pullCalls
}

func (t *capturingTransport) AckCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ackCalls
}

func (t *capturingTransport) SetPullResponse(resp transport.PullResponse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pullResp = resp
}

func (t *capturingTransport) SetPullError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pullErr = err
}

func (t *capturingTransport) SetAckError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ackErr = err
}

func (t *capturingTransport) BlockPulls() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockPull = make(chan struct{})
}

func (t *capturingTransport) UnblockPulls() {
	t.mu.Lock()
	block := t.blockPull
	t.blockPull = nil
	t.mu.Unlock()
	if block != nil {
		close(block)
	}
}

func requireNoWorkerResultKind(t *testing.T, results <-chan worker.Result, kinds ...worker.TaskKind) {
	t.Helper()
	blocked := make(map[worker.TaskKind]struct{}, len(kinds))
	for _, kind := range kinds {
		blocked[kind] = struct{}{}
	}
	quiet := time.NewTimer(20 * time.Millisecond)
	defer quiet.Stop()
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case result := <-results:
			if _, ok := blocked[result.Kind]; ok {
				t.Fatalf("unexpected worker result kind %v", result.Kind)
			}
			resetTimer(quiet, 20*time.Millisecond)
		case <-quiet.C:
			return
		case <-deadline.C:
			return
		}
	}
}

type blockingApplyFactory struct {
	base         *store.MemoryFactory
	applyStarted chan struct{}
	unblock      chan struct{}
}

func newBlockingApplyFactory() *blockingApplyFactory {
	return &blockingApplyFactory{base: store.NewMemoryFactory(), applyStarted: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *blockingApplyFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &blockingApplyStore{ChannelStore: base, parent: f}, nil
}

func (f *blockingApplyFactory) BlockApplies() {
	f.unblock = make(chan struct{})
}

func (f *blockingApplyFactory) ApplyStarted() bool {
	return len(f.applyStarted) > 0
}

func (f *blockingApplyFactory) UnblockApplies() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type blockingApplyStore struct {
	store.ChannelStore
	parent *blockingApplyFactory
}

func (s *blockingApplyStore) ApplyFollower(ctx context.Context, req store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	select {
	case s.parent.applyStarted <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return store.ApplyFollowerResult{}, ctx.Err()
	}
	return s.ChannelStore.ApplyFollower(ctx, req)
}

type captureReadLogRequestFactory struct {
	base     *store.MemoryFactory
	requests chan store.ReadLogRequest
}

func newCaptureReadLogRequestFactory() *captureReadLogRequestFactory {
	return &captureReadLogRequestFactory{base: store.NewMemoryFactory(), requests: make(chan store.ReadLogRequest, 8)}
}

func (f *captureReadLogRequestFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &captureReadLogRequestStore{ChannelStore: base, parent: f}, nil
}

func (f *captureReadLogRequestFactory) awaitRequest(t *testing.T) store.ReadLogRequest {
	t.Helper()
	select {
	case req := <-f.requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReadLog request")
		return store.ReadLogRequest{}
	}
}

type captureReadLogRequestStore struct {
	store.ChannelStore
	parent *captureReadLogRequestFactory
}

func (s *captureReadLogRequestStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	select {
	case s.parent.requests <- req:
	default:
	}
	return s.ChannelStore.ReadLog(ctx, req)
}

type blockingReadLogFactory struct {
	base    *store.MemoryFactory
	started chan struct{}
	unblock chan struct{}
}

func newBlockingReadLogFactory() *blockingReadLogFactory {
	return &blockingReadLogFactory{base: store.NewMemoryFactory(), started: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *blockingReadLogFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &blockingReadLogStore{ChannelStore: base, parent: f}, nil
}

func (f *blockingReadLogFactory) ReadLogStarted() bool {
	return len(f.started) > 0
}

func (f *blockingReadLogFactory) UnblockReadLogs() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type blockingReadLogStore struct {
	store.ChannelStore
	parent *blockingReadLogFactory
}

func (s *blockingReadLogStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	select {
	case s.parent.started <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return store.ReadLogResult{}, ctx.Err()
	}
	return s.ChannelStore.ReadLog(ctx, req)
}

type nonCancelingBlockingReadLogFactory struct {
	base    *store.MemoryFactory
	started chan struct{}
	unblock chan struct{}
}

func newNonCancelingBlockingReadLogFactory() *nonCancelingBlockingReadLogFactory {
	return &nonCancelingBlockingReadLogFactory{base: store.NewMemoryFactory(), started: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *nonCancelingBlockingReadLogFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &nonCancelingBlockingReadLogStore{ChannelStore: base, parent: f}, nil
}

func (f *nonCancelingBlockingReadLogFactory) waitReadLogStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReadLog to start")
	}
}

func (f *nonCancelingBlockingReadLogFactory) UnblockReadLogs() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type nonCancelingBlockingReadLogStore struct {
	store.ChannelStore
	parent *nonCancelingBlockingReadLogFactory
}

func (s *nonCancelingBlockingReadLogStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	select {
	case s.parent.started <- struct{}{}:
	default:
	}
	<-s.parent.unblock
	return s.ChannelStore.ReadLog(ctx, req)
}
