package reactor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfigSetsFollowerRecoveryProbe(t *testing.T) {
	cfg := defaultConfig(Config{LocalNode: 1, Store: store.NewMemoryFactory()})
	require.Equal(t, 100*time.Millisecond, cfg.ReplicationIdlePollInterval)
	require.Equal(t, 2*time.Second, cfg.FollowerRecoveryProbeInterval)
	require.Equal(t, time.Second, cfg.FollowerRecoveryProbeJitter)
}

func TestDefaultReactorConfigSetsFollowerRecoveryProbe(t *testing.T) {
	cfg := defaultReactorConfig(ReactorConfig{LocalNode: 1, Store: store.NewMemoryFactory()})
	require.Equal(t, 100*time.Millisecond, cfg.ReplicationIdlePollInterval)
	require.Equal(t, 2*time.Second, cfg.FollowerRecoveryProbeInterval)
	require.Equal(t, time.Second, cfg.FollowerRecoveryProbeJitter)
}

func TestFollowerRecoveryProbeDelayIsDeterministicAndBounded(t *testing.T) {
	delay := followerRecoveryProbeDelay(ch.ChannelKey("1:room"), time.Minute, 30*time.Second)
	require.GreaterOrEqual(t, delay, time.Minute)
	require.LessOrEqual(t, delay, 90*time.Second)
	require.Equal(t, delay, followerRecoveryProbeDelay(ch.ChannelKey("1:room"), time.Minute, 30*time.Second))
}

func TestFollowerRecoveryProbeDelaySaturatesOverflow(t *testing.T) {
	delay := followerRecoveryProbeDelay(ch.ChannelKey("1:room"), time.Duration(1<<63-2), 30*time.Second)
	require.Equal(t, time.Duration(1<<63-1), delay)
}

func TestFollowerParksAfterEmptyCaughtUpPull(t *testing.T) {
	factory := store.NewMemoryFactory()
	net := newCapturingTransport()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("park")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    0,
		LeaderLEO:   0,
		Control:     transport.PullControlContinue,
	})
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, MailboxSize: 16, Store: factory, Pools: pools,
		FollowerRecoveryProbeInterval: time.Minute,
		FollowerRecoveryProbeJitter:   0,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]

	r.tickFollowerReplication(rc, time.Now())
	r.handleRPCPullResult(sink.awaitResultKind(t, worker.TaskRPCPull))

	require.True(t, rc.replication.parked)
	require.False(t, rc.replication.dirty)
	require.True(t, rc.replication.recoveryProbe)
	require.False(t, rc.replication.nextPullAt.IsZero())
}

func TestRecoveryProbeSubmitFailureObserved(t *testing.T) {
	obs := &captureObserver{}
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("recovery-probe-submit-failure")
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16, Observer: obs,
		ReplicationMinBackoff:         time.Millisecond,
		ReplicationMaxBackoff:         time.Millisecond,
		FollowerRecoveryProbeInterval: time.Minute,
		FollowerRecoveryProbeJitter:   time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	now := time.Now()
	rc.replication.parkWithRecovery(meta.Key, now.Add(-2*time.Minute), time.Minute, 0)
	rc.replication.nextPullAt = now.Add(-time.Millisecond)

	r.tickFollowerReplication(rc, now)

	require.Equal(t, 1, obs.RecoveryProbes("err"))
	require.True(t, rc.replication.recoveryProbe)
	require.False(t, rc.replication.pullInflight)
	require.False(t, rc.replication.nextPullAt.IsZero())
}

func TestFollowerParkedCountUpdatesWhenRecordsArrive(t *testing.T) {
	obs := &captureObserver{}
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := followerTestMeta("parked-count-records")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16, Observer: obs})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.parkWithRecovery(meta.Key, time.Now(), time.Minute, 0)
	r.observeFollowerParkedCount(r.countParkedFollowers())
	require.Equal(t, 1, obs.FollowerParked())

	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7
	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			LeaderHW:    1,
			LeaderLEO:   1,
			Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
		}},
	})

	require.Equal(t, 0, obs.FollowerParked())
}

func TestFollowerParkedCountUpdatesWhenMetadataLeavesFollower(t *testing.T) {
	obs := &captureObserver{}
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("parked-count-meta")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16, Observer: obs})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.parkWithRecovery(meta.Key, time.Now(), time.Minute, 0)
	r.observeFollowerParkedCount(r.countParkedFollowers())
	require.Equal(t, 1, obs.FollowerParked())

	leaderMeta := meta
	leaderMeta.Leader = 2
	leaderMeta.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, leaderMeta))

	require.Equal(t, 0, obs.FollowerParked())
}

func TestFollowerParkedCountUpdatesWhenRuntimeEvicted(t *testing.T) {
	obs := &captureObserver{}
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("parked-count-evict")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16, Observer: obs})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.parkWithRecovery(meta.Key, time.Now(), time.Minute, 0)
	r.observeFollowerParkedCount(r.countParkedFollowers())
	require.Equal(t, 1, obs.FollowerParked())

	require.True(t, r.evictRuntimeChannel(meta.Key, rc, "test"))

	require.Equal(t, 0, obs.FollowerParked())
}

func TestParkedFollowerWakesOnValidPullHint(t *testing.T) {
	r, rc := newFollowerReplicationTestRuntime(t)
	now := time.Now()
	rc.replication.parkWithRecovery(rc.state.Key, now, time.Minute, 0)
	r.handleFollowerPullHint(Event{Kind: EventPullHint, Key: rc.state.Key, PullHint: transport.PullHintRequest{
		ChannelKey: rc.state.Key, ChannelID: rc.state.ID, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch,
		Leader: rc.state.Leader, LeaderLEO: rc.state.LEO + 1, ActivityVersion: 1,
	}, Future: NewFuture()})
	require.False(t, rc.replication.parked)
	require.True(t, rc.replication.dirty)
	require.False(t, rc.replication.recoveryProbe)
}

func TestLeaderPullDelayUsesIdleAgeWithoutCoolingPhase(t *testing.T) {
	r := NewReactor(ReactorConfig{
		LocalNode:                   1,
		Store:                       store.NewMemoryFactory(),
		IdleSlowdownAfter:           time.Second,
		IdlePullMinInterval:         time.Millisecond,
		IdlePullMaxInterval:         8 * time.Millisecond,
		ReplicationIdlePollInterval: time.Millisecond,
	})
	rc := &runtimeChannel{lifecycle: channelRuntimeLifecycle{loadedAt: time.Unix(0, 0)}}

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
	require.Eventually(t, func() bool {
		pull := net.LastPull()
		return pull.NextOffset == 1 && pull.AckOffset == 0
	}, time.Second, time.Millisecond)

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
		pull := net.LastPull()
		return pull.NextOffset == 2 && pull.AckOffset == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, 0, net.AckCalls())
}

func TestFollowerStoreApplyImmediatelyReturnsAckOffsetPullWithoutStandaloneAck(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("apply-immediate-ack-pull")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})

	require.NoError(t, submitPullHintDirect(t, r, meta, 1))
	r.handleRPCPullResult(sink.awaitResultKind(t, worker.TaskRPCPull))
	applyResult := sink.awaitResultKind(t, worker.TaskStoreApply)
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
	})

	r.handleStoreApplyResult(applyResult)

	require.Eventually(t, func() bool {
		pull := net.LastPull()
		return net.PullCalls() >= 2 && pull.NextOffset == 2 && pull.AckOffset == 1
	}, 100*time.Millisecond, time.Millisecond)
	requireNoWorkerResultKind(t, sink.results, worker.TaskRPCAck)
	require.Equal(t, 0, net.AckCalls())
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
	followerMeta.LeaderEpoch++
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

func TestMetaFenceClearsFollowerStoppedAckAndAllowsPull(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("meta-fence-stopped-ack")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.lifecycle.acceptFollowerStop(3, 3, 3)
	rc.lifecycle.stage = lifecycleFollowerStoppedAcking
	rc.lifecycle.stoppedAck = lifecycleEffect{inflight: true, opID: 7, version: 3}

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, updated))

	r.handleRPCAckResult(worker.Result{
		Kind:  worker.TaskRPCAck,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation - 1, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
	})
	require.Equal(t, lifecycleLive, rc.lifecycle.stage)
	require.False(t, rc.lifecycle.followerStop.accepted)
	require.False(t, rc.lifecycle.stoppedAck.active())

	r.handleTick(Event{Kind: EventTick, Key: updated.Key, TickNow: time.Unix(1, 0)})
	pullResult := sink.awaitResultKind(t, worker.TaskRPCPull)
	require.Equal(t, updated.LeaderEpoch, pullResult.Fence.LeaderEpoch)
	require.Equal(t, updated.LeaderEpoch, net.LastPull().LeaderEpoch)
}

func TestMetaFenceClearsFollowerStopCheckpointAndAllowsPull(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("meta-fence-stop-checkpoint")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.lifecycle.acceptFollowerStop(3, 3, 3)
	rc.lifecycle.checkpoint = lifecycleEffect{inflight: true, opID: 8, version: 3}

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, updated))

	require.Equal(t, lifecycleLive, rc.lifecycle.stage)
	require.False(t, rc.lifecycle.followerStop.accepted)
	require.False(t, rc.lifecycle.checkpoint.active())

	r.handleTick(Event{Kind: EventTick, Key: updated.Key, TickNow: time.Unix(1, 0)})
	pullResult := sink.awaitResultKind(t, worker.TaskRPCPull)
	require.Equal(t, updated.LeaderEpoch, pullResult.Fence.LeaderEpoch)
	require.Equal(t, updated.LeaderEpoch, net.LastPull().LeaderEpoch)
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

func TestFollowerPullRPCTimeoutClearsInflightForRetry(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	net.BlockPulls()
	defer net.UnblockPulls()

	meta := followerTestMeta("pull-rpc-timeout")
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16,
		PullHintRetryInterval:         5 * time.Millisecond,
		ReplicationMinBackoff:         time.Millisecond,
		ReplicationMaxBackoff:         time.Millisecond,
		ReplicationIdlePollInterval:   time.Hour,
		FollowerRecoveryProbeInterval: time.Hour,
		FollowerRecoveryProbeJitter:   0,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]

	r.tickFollowerReplication(rc, time.Now())
	require.True(t, rc.replication.pullInflight)
	result := sink.awaitResultKind(t, worker.TaskRPCPull)
	require.ErrorIs(t, result.Err, context.DeadlineExceeded)

	r.handleRPCPullResult(result)

	require.False(t, rc.replication.pullInflight)
	require.Zero(t, rc.replication.pullOpID)
	require.ErrorIs(t, rc.replication.lastError, context.DeadlineExceeded)
	require.False(t, rc.replication.nextPullAt.IsZero())
}

func TestFollowerPullRPCTimeoutCapsDefaultRetryInterval(t *testing.T) {
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: store.NewMemoryFactory(), MailboxSize: 16})

	require.Equal(t, time.Second, r.followerPullRPCTimeout())
}

func TestFollowerEmptyPullWithLeaderLEOAheadAdvancesHWAndRetriesAfterBackoff(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("empty-pull-hw")
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16,
		ReplicationIdlePollInterval: time.Hour,
		ReplicationMinBackoff:       25 * time.Millisecond,
		ReplicationMaxBackoff:       25 * time.Millisecond,
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
	require.False(t, rc.replication.nextPullAt.IsZero())
	require.False(t, rc.replication.nextPullAt.Before(before.Add(20*time.Millisecond)))
	require.True(t, rc.replication.nextPullAt.Before(after.Add(100*time.Millisecond)))
}

func TestFollowerEmptyPullWithLeaderLEOAheadAndZeroDelayRetriesAfterBackoff(t *testing.T) {
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

	require.Equal(t, lifecycleFollowerCheckpointing, rc.lifecycle.stage)
	require.True(t, rc.lifecycle.followerStop.accepted)
	require.Equal(t, uint64(3), rc.lifecycle.followerStop.version)
	require.True(t, rc.lifecycle.checkpoint.inflight)
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

func TestFollowerStoppedAckStillUsesStandaloneAck(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("stopped-ack-standalone")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 1
	rc.state.HW = 1
	rc.lifecycle.acceptFollowerStop(1, 1, 1)
	rc.lifecycle.checkpoint.inflight = true
	rc.lifecycle.checkpoint.opID = 7
	rc.lifecycle.checkpoint.version = 1
	rc.lifecycle.stage = lifecycleFollowerCheckpointing

	r.handleStoreCheckpointResult(worker.Result{
		Kind: worker.TaskStoreCheckpoint,
		Fence: ch.Fence{
			ChannelKey:  meta.Key,
			Generation:  rc.state.Generation,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			OpID:        7,
		},
		StoreCheckpoint: &worker.StoreCheckpointResult{},
	})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskRPCAck)})

	require.Equal(t, 1, net.AckCalls())
	ack := net.LastAck()
	require.True(t, ack.Stopped)
	require.Equal(t, uint64(1), ack.MatchOffset)
	require.Equal(t, uint64(1), ack.ActivityVersion)
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
	require.Equal(t, lifecycleFollowerStoppedAcking, rc.lifecycle.stage)
	require.False(t, rc.lifecycle.stoppedAck.retryAt.IsZero())

	net.SetAckError(nil)
	r.tickFollowerReplication(rc, time.Now().Add(time.Second))
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
	require.False(t, rc.lifecycle.followerStop.accepted)
	r.tickFollowerReplication(rc, time.Now())
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.Equal(t, 1, net.AckCalls())
}

func TestStaleFenceStoppedAckCompletionDoesNotAdvanceLifecycle(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	meta := followerTestMeta("stale-stopped-ack-fence")
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.lifecycle.acceptFollowerStop(3, 3, 3)
	rc.lifecycle.stage = lifecycleFollowerStoppedAcking
	rc.lifecycle.stoppedAck = lifecycleEffect{inflight: true, opID: 7, version: 3}

	r.handleRPCAckResult(worker.Result{
		Kind:  worker.TaskRPCAck,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation + 1, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 7},
	})

	require.Contains(t, r.channels, meta.Key)
	require.Equal(t, lifecycleFollowerStoppedAcking, rc.lifecycle.stage)
	require.True(t, rc.lifecycle.stoppedAck.inflight)
	require.Equal(t, ch.OpID(7), rc.lifecycle.stoppedAck.opID)
	require.True(t, rc.lifecycle.followerStop.accepted)
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

	r.handleFollowerPullHint(Event{Kind: EventPullHint, Key: meta.Key, PullHint: transport.PullHintRequest{
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

	r.handleFollowerPullHint(Event{Kind: EventPullHint, Key: meta.Key, PullHint: transport.PullHintRequest{
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

	require.Equal(t, 0, net.PullCalls())
	require.False(t, rc.replication.parked)
	require.False(t, rc.replication.nextPullAt.IsZero())
	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: rc.replication.nextPullAt.Add(time.Millisecond), Future: NewFuture()})
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.True(t, rc.replication.pullInflight)
}

func TestFollowerStaleEmptyPullWithoutLeaderLEOAheadParks(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("stale-empty-without-leader-ahead")
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16,
		ReplicationIdlePollInterval: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7
	rc.replication.dirty = true
	rc.replication.lastActivityVersion = 2

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
			NextPullAfter:   time.Hour,
			Control:         transport.PullControlContinue,
		}},
	})

	require.False(t, rc.replication.dirty)
	require.True(t, rc.replication.parked)
	require.True(t, rc.replication.recoveryProbe)
	require.False(t, rc.replication.nextPullAt.IsZero())
	require.True(t, rc.replication.nextPullAt.Before(time.Now().Add(2*time.Minute)))
}

func TestFollowerIgnoresCaughtUpPullHint(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("caught-up-hint")
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 5
	rc.state.HW = 5
	rc.replication.dirty = false
	rc.replication.parked = true
	rc.replication.nextPullAt = time.Now().Add(time.Hour)

	future := NewFuture()
	r.handleFollowerPullHint(Event{
		Kind:   EventPullHint,
		Key:    meta.Key,
		Future: future,
		PullHint: transport.PullHintRequest{
			ChannelKey:      meta.Key,
			ChannelID:       meta.ID,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			Leader:          meta.Leader,
			LeaderLEO:       5,
			ActivityVersion: 6,
			Reason:          transport.PullHintReasonAppend,
		},
	})

	require.NoError(t, awaitFutureResult(t, future).Err)
	require.Equal(t, 0, net.PullCalls())
	require.False(t, rc.replication.dirty)
	require.True(t, rc.replication.parked)
}

func TestFollowerCaughtUpPullHintReturnsPendingAckOffset(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("caught-up-hint-with-ack")
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 5
	rc.state.HW = 5
	rc.replication.dirty = false
	rc.replication.parked = true
	rc.replication.nextPullAt = time.Now().Add(time.Hour)
	rc.replication.ackReturnOffset = 5
	rc.replication.ackReturnStartedAt = time.Now().Add(-time.Second)

	future := NewFuture()
	r.handleFollowerPullHint(Event{
		Kind:   EventPullHint,
		Key:    meta.Key,
		Future: future,
		PullHint: transport.PullHintRequest{
			ChannelKey:      meta.Key,
			ChannelID:       meta.ID,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			Leader:          meta.Leader,
			LeaderLEO:       5,
			ActivityVersion: 6,
			Reason:          transport.PullHintReasonResume,
		},
	})

	require.NoError(t, awaitFutureResult(t, future).Err)
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.Equal(t, uint64(5), net.LastPull().AckOffset)
	require.Equal(t, uint64(6), net.LastPull().NextOffset)
	require.True(t, rc.replication.pullInflight)
	require.False(t, rc.replication.parked)
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
	r.tickFollowerReplication(rc, time.Now().Add(time.Minute))
	require.Equal(t, 0, net.PullCalls())

	future := NewFuture()
	r.handleFollowerPullHint(Event{
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

func TestFollowerStoreApplyResultSchedulesPullAckOffsetWithoutStandaloneAck(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("apply-pull-ack")
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
	require.Eventually(t, func() bool {
		pull := net.LastPull()
		return net.PullCalls() >= 2 && pull.NextOffset == 2 && pull.AckOffset == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, 0, net.AckCalls())

	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	read, err := cs.ReadCommitted(context.Background(), store.ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, read.Messages, 1)
	require.Equal(t, uint64(1), read.Messages[0].MessageSeq)
}

func TestPendingMetaPullHintCreatesNeedMetaShellAndReportsNotLoaded(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	defer net.UnblockPulls()
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("pending-create")
	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	_, err = cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: []ch.Record{
		{ID: 1, Payload: []byte("a"), SizeBytes: 1},
		{ID: 2, Payload: []byte("b"), SizeBytes: 1},
	}})
	require.NoError(t, err)
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net, MaxChannels: 1})
	require.NoError(t, err)
	defer g.Close()

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventPullHint, Key: meta.Key, PullHint: pullHintForMeta(meta, 4)}))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	pull := net.LastPull()
	require.True(t, pull.NeedMeta)
	require.Equal(t, uint64(3), pull.NextOffset)
	require.Equal(t, uint64(2), pull.AckOffset)

	loaded, err := g.HasChannelState(context.Background(), meta.Key)
	require.NoError(t, err)
	require.False(t, loaded)
	snapshot, err := g.RuntimeSnapshot(context.Background())
	require.NoError(t, err)
	require.Zero(t, snapshot.ActiveTotal)
	appendFuture, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 99, "pending"))
	require.NoError(t, err)
	_, err = appendFuture.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrChannelNotFound)
	pullFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventPull, Key: meta.Key, Pull: transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, NextOffset: 1}})
	require.NoError(t, err)
	_, err = pullFuture.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrChannelNotFound)

	other := followerTestMeta("pending-capacity")
	err = awaitSubmit(g, other.Key, Event{Kind: EventApplyMeta, Key: other.Key, Meta: other})
	require.ErrorIs(t, err, ch.ErrTooManyChannels)
}

func TestPendingMetaPullHintCoalescesAndFencesHints(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	defer net.UnblockPulls()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	meta := followerTestMeta("pending-fence")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})

	require.NoError(t, submitPullHintDirect(t, r, meta, 5))
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	rc := r.channels[meta.Key]
	require.NotNil(t, rc.pending)
	firstOp := rc.pending.pullOpID

	require.NoError(t, submitPullHintDirect(t, r, meta, 6))
	require.Equal(t, firstOp, rc.pending.pullOpID)
	require.Equal(t, 1, net.PullCalls())

	newer := meta
	newer.LeaderEpoch = 2
	require.NoError(t, submitPullHintDirect(t, r, newer, 7))
	rc = r.channels[meta.Key]
	require.NotEqual(t, firstOp, rc.pending.pullOpID)
	require.Equal(t, uint64(2), rc.pending.leaderEpoch)

	stale := meta
	err := submitPullHintDirect(t, r, stale, 8)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Equal(t, uint64(2), rc.pending.leaderEpoch)

	err = applyMetaDirect(t, r, stale)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.NotNil(t, rc.pending)
	require.Equal(t, uint64(2), rc.pending.leaderEpoch)
}

func TestPendingMetaSameFencePullHintDoesNotExtendDeadline(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	defer net.UnblockPulls()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	meta := followerTestMeta("pending-deadline-fixed")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16, PullHintRetryInterval: time.Millisecond})

	require.NoError(t, submitPullHintDirect(t, r, meta, 5))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc.pending)
	firstDeadline := rc.pending.deadline

	require.NoError(t, submitPullHintDirect(t, r, meta, 6))
	require.Equal(t, firstDeadline, rc.pending.deadline)

	r.processDue(firstDeadline.Add(time.Nanosecond))
	require.Nil(t, r.channels[meta.Key])
}

func TestPendingMetaNeedMetaPullResponseAppliesMetaBeforeRecords(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	meta := followerTestMeta("pending-apply-records")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Meta:        &meta,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})

	require.NoError(t, submitPullHintDirect(t, r, meta, 1))
	r.handleRPCPullResult(sink.awaitResultKind(t, worker.TaskRPCPull))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Nil(t, rc.pending)
	require.NotNil(t, rc.state)
	require.Equal(t, ch.RoleFollower, rc.state.Role)
	require.Equal(t, uint64(0), rc.state.LEO)
	r.handleStoreApplyResult(sink.awaitResultKind(t, worker.TaskStoreApply))
	require.Equal(t, uint64(1), rc.state.LEO)

	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	read, err := cs.ReadCommitted(context.Background(), store.ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, read.Messages, 1)
	require.Equal(t, uint64(1), read.Messages[0].MessageSeq)
}

func TestPendingMetaMetricsTrackNeedMetaLifecycle(t *testing.T) {
	obs := &pendingMetaMetricsObserver{}
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	meta := followerTestMeta("pending-metrics-lifecycle")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    0,
		LeaderLEO:   0,
		Meta:        &meta,
	})
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16, Observer: obs})

	require.NoError(t, submitPullHintDirect(t, r, meta, 1))
	require.Equal(t, 1, obs.PendingMetaEvents("created"))
	require.Equal(t, 1, obs.NeedMetaPulls("submitted"))
	require.Equal(t, 1, obs.PendingMetaCount())

	r.handleRPCPullResult(sink.awaitResultKind(t, worker.TaskRPCPull))

	require.Equal(t, 1, obs.NeedMetaPulls("ok"))
	require.Equal(t, 1, obs.PendingMetaEvents("converted"))
	require.Equal(t, 0, obs.PendingMetaCount())
}

func TestPendingMetaMetricsTrackNeedMetaRetryAndRelease(t *testing.T) {
	obs := &pendingMetaMetricsObserver{}
	net := newCapturingTransport()
	net.BlockPulls()
	defer net.UnblockPulls()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	meta := followerTestMeta("pending-metrics-retry")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16, Observer: obs})

	require.NoError(t, submitPullHintDirect(t, r, meta, 1))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc.pending)
	firstOp := rc.pending.pullOpID

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.pending.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: firstOp},
		Err:   temporaryPullError{},
	})

	require.Equal(t, 1, obs.NeedMetaPulls("retry"))
	require.NotEqual(t, firstOp, rc.pending.pullOpID)

	secondOp := rc.pending.pullOpID
	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.pending.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: secondOp},
		Err:   context.DeadlineExceeded,
	})

	require.Nil(t, r.channels[meta.Key])
	require.Equal(t, 1, obs.NeedMetaPulls("err"))
	require.Equal(t, 1, obs.PendingMetaEvents("released"))
	require.Equal(t, context.DeadlineExceeded, obs.LastPendingMetaError("released"))
	require.Equal(t, 0, obs.PendingMetaCount())
}

func TestPendingMetaNeedMetaResponseRejectsNonActiveMeta(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("pending-non-active-meta")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	rc := installPendingMetaForTest(t, r, meta)
	opID := rc.pending.pullOpID
	creating := meta
	creating.Status = ch.StatusCreating

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.pending.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: opID},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Meta:        &creating,
			Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
		}},
	})

	require.Nil(t, r.channels[meta.Key])
}

func TestFollowerNeedMetaFalseResponseWithMetaDoesNotApplyRecords(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("normal-meta-invalid")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7
	rc.replication.pullSubmittedAt = time.Now()

	r.handleRPCPullResult(worker.Result{
		Kind: worker.TaskRPCPull,
		Fence: ch.Fence{
			ChannelKey:  meta.Key,
			Generation:  rc.state.Generation,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			OpID:        7,
		},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			LeaderHW:    1,
			LeaderLEO:   1,
			Meta:        &meta,
			Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
		}},
	})

	require.Zero(t, rc.state.LEO)
	require.Nil(t, rc.replication.pendingPull)
	require.ErrorIs(t, rc.replication.lastError, ch.ErrInvalidConfig)
}

func TestPendingMetaPullErrorsReleaseOrRetry(t *testing.T) {
	t.Run("application errors release without retry", func(t *testing.T) {
		for _, appErr := range []error{ch.ErrNotReady, ch.ErrNotLeader, ch.ErrStaleMeta, ch.ErrChannelNotFound, ch.ErrNotReplica} {
			t.Run(appErr.Error(), func(t *testing.T) {
				factory := store.NewMemoryFactory()
				meta := followerTestMeta("pending-app-error")
				r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
				rc := installPendingMetaForTest(t, r, meta)
				opID := rc.pending.pullOpID

				r.handleRPCPullResult(worker.Result{
					Kind:  worker.TaskRPCPull,
					Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.pending.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: opID},
					Err:   appErr,
				})

				require.Nil(t, r.channels[meta.Key])
			})
		}
	})

	t.Run("transport retry once", func(t *testing.T) {
		net := newCapturingTransport()
		net.BlockPulls()
		defer net.UnblockPulls()
		factory := store.NewMemoryFactory()
		sink := captureCompletionSink{results: make(chan worker.Result, 8)}
		pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
		defer pools.Close()
		meta := followerTestMeta("pending-retry")
		r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
		rc := installPendingMetaForTest(t, r, meta)
		firstOp := rc.pending.pullOpID

		r.handleRPCPullResult(worker.Result{
			Kind:  worker.TaskRPCPull,
			Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.pending.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: firstOp},
			Err:   temporaryPullError{},
		})

		require.NotNil(t, r.channels[meta.Key])
		require.True(t, rc.pending.pullInflight)
		require.Equal(t, 1, rc.pending.retryCount)
		require.NotEqual(t, firstOp, rc.pending.pullOpID)

		secondOp := rc.pending.pullOpID
		r.handleRPCPullResult(worker.Result{
			Kind:  worker.TaskRPCPull,
			Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.pending.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: secondOp},
			Err:   temporaryPullError{},
		})
		require.Nil(t, r.channels[meta.Key])
	})

	t.Run("context cancellation and deadline release immediately", func(t *testing.T) {
		for _, cancelErr := range []error{context.Canceled, context.DeadlineExceeded} {
			factory := store.NewMemoryFactory()
			meta := followerTestMeta("pending-cancel")
			r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
			rc := installPendingMetaForTest(t, r, meta)
			opID := rc.pending.pullOpID

			r.handleRPCPullResult(worker.Result{
				Kind:  worker.TaskRPCPull,
				Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.pending.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: opID},
				Err:   cancelErr,
			})
			require.Nil(t, r.channels[meta.Key])
		}
	})
}

func TestApplyMetaPendingMetaFenceRules(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("pending-apply-meta")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	rc := installPendingMetaForTest(t, r, meta)

	differentLeader := meta
	differentLeader.Leader = 3
	differentLeader.Replicas = []ch.NodeID{2, 3}
	differentLeader.ISR = []ch.NodeID{2, 3}
	err := applyMetaDirect(t, r, differentLeader)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Same(t, rc, r.channels[meta.Key])
	require.NotNil(t, rc.pending)

	differentID := meta
	differentID.ID = ch.ChannelID{ID: "different-id", Type: meta.ID.Type}
	differentID.LeaderEpoch = 2
	err = applyMetaDirect(t, r, differentID)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Same(t, rc, r.channels[meta.Key])
	require.NotNil(t, rc.pending)

	invalidNewer := meta
	invalidNewer.LeaderEpoch = 2
	invalidNewer.MinISR = 3
	err = applyMetaDirect(t, r, invalidNewer)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.Nil(t, r.channels[meta.Key])

	r = NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	installPendingMetaForTest(t, r, meta)
	newer := meta
	newer.LeaderEpoch = 2
	require.NoError(t, applyMetaDirect(t, r, newer))
	rc = r.channels[meta.Key]
	require.Nil(t, rc.pending)
	require.NotNil(t, rc.state)
	require.Equal(t, uint64(2), rc.state.LeaderEpoch)
	require.Equal(t, ch.RoleFollower, rc.state.Role)
}

func TestLoadedNewerPullHintAppliesAuthoritativeMetaThenUsesOrdinaryPull(t *testing.T) {
	base := followerTestMeta("loaded-authoritative-refresh")
	base.Leader = 2
	authoritative := base
	authoritative.Leader = 1
	authoritative.LeaderEpoch = 2
	authoritative.RetentionThroughSeq = 2
	authoritative.WriteFence = ch.WriteFence{Token: "failover", Version: 4, Reason: ch.WriteFenceReasonFailover, Until: time.Now().Add(time.Minute)}
	resolver := newBlockingMetaResolver(authoritative, nil)
	net := newCapturingTransport()
	net.BlockPulls()
	defer net.UnblockPulls()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithMetaResolver(t, factory, net, resolver, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, base))
	loaded := r.channels[base.Key]

	hint := authoritative
	require.NoError(t, submitPullHintDirect(t, r, hint, 9))
	resolver.AwaitStarted(t)
	require.Same(t, loaded, r.channels[base.Key])
	require.Equal(t, uint64(1), loaded.state.LeaderEpoch)
	require.Zero(t, net.PullCalls())

	resolver.Unblock()
	r.handleMetaResolveResult(sink.awaitResultKind(t, worker.TaskMetaResolve))
	r.processDue(time.Now().Add(time.Second))
	require.Same(t, loaded, r.channels[base.Key])
	require.Equal(t, authoritative.LeaderEpoch, loaded.state.LeaderEpoch)
	require.Equal(t, authoritative.Leader, loaded.state.Leader)
	require.Equal(t, ch.RoleFollower, loaded.state.Role)
	require.Equal(t, authoritative.RetentionThroughSeq, loaded.state.RetentionThroughSeq)
	require.Equal(t, authoritative.WriteFence, loaded.state.WriteFence)
	require.Eventually(t, func() bool { return net.PullCalls() == 1 }, time.Second, time.Millisecond)
	require.False(t, net.LastPull().NeedMeta)
	require.Equal(t, authoritative.Leader, net.LastPullNode())
}

func TestLoadedNewerAuthoritativeLocalLeaderUsesLeaderLifecycleWithoutSelfPull(t *testing.T) {
	base := followerTestMeta("loaded-authoritative-local-leader")
	authoritative := base
	authoritative.Leader = 2
	authoritative.LeaderEpoch = 2
	resolver := newBlockingMetaResolver(authoritative, nil)
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithMetaResolver(t, factory, net, resolver, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, base))

	hint := base
	hint.Leader = 3
	hint.LeaderEpoch = 50
	require.NoError(t, submitPullHintDirect(t, r, hint, 50))
	resolver.Unblock()
	r.handleMetaResolveResult(sink.awaitResultKind(t, worker.TaskMetaResolve))
	r.processDue(time.Now().Add(time.Second))

	loaded := r.channels[base.Key]
	require.Equal(t, authoritative.LeaderEpoch, loaded.state.LeaderEpoch)
	require.Equal(t, ch.RoleLeader, loaded.state.Role)
	require.Zero(t, net.PullCalls())
}

func TestLoadedNewerPullHintsSingleFlightWithoutDeadlineExtensionOrHighFencePoison(t *testing.T) {
	base := followerTestMeta("loaded-refresh-singleflight")
	base.Leader = 2
	authoritative := base
	authoritative.Leader = 1
	authoritative.LeaderEpoch = 2
	resolver := newBlockingMetaResolver(authoritative, nil)
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithMetaResolver(t, factory, newCapturingTransport(), resolver, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, base))

	rogueHigh := authoritative
	rogueHigh.Leader = 3
	rogueHigh.LeaderEpoch = 100
	require.NoError(t, submitPullHintDirect(t, r, rogueHigh, 100))
	resolver.AwaitStarted(t)
	refresh := r.loadedMetaRefreshes[base.Key]
	require.NotNil(t, refresh)
	deadline := refresh.deadline
	require.NoError(t, submitPullHintDirect(t, r, authoritative, 5))
	require.Same(t, refresh, r.loadedMetaRefreshes[base.Key])
	require.Equal(t, deadline, refresh.deadline)
	require.Equal(t, 1, resolver.Calls())

	resolver.Unblock()
	r.handleMetaResolveResult(sink.awaitResultKind(t, worker.TaskMetaResolve))
	require.Equal(t, authoritative.LeaderEpoch, r.channels[base.Key].state.LeaderEpoch)
	require.Equal(t, authoritative.Leader, r.channels[base.Key].state.Leader)
	_, scheduled := r.due.slots[dueSlot{kind: dueLoadedMetaRefresh, key: base.Key}]
	require.False(t, scheduled, "successful refresh must remove its stale deadline item")
}

func TestLoadedNewerMetaResolveSurvivesSameFenceApply(t *testing.T) {
	base := followerTestMeta("loaded-refresh-same-fence-apply")
	base.Leader = 2
	authoritative := base
	authoritative.Leader = 1
	authoritative.LeaderEpoch = 2
	resolver := newBlockingMetaResolver(authoritative, nil)
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithMetaResolver(t, factory, newCapturingTransport(), resolver, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, base))

	hint := authoritative
	require.NoError(t, submitPullHintDirect(t, r, hint, 1))
	resolver.AwaitStarted(t)
	refresh := r.loadedMetaRefreshes[base.Key]
	require.NotNil(t, refresh)

	sameFence := base
	sameFence.RetentionThroughSeq = 7
	require.NoError(t, applyMetaDirect(t, r, sameFence))
	require.Same(t, refresh, r.loadedMetaRefreshes[base.Key], "same-fence authority refresh must not cancel newer-fence recovery")

	resolver.Unblock()
	r.handleMetaResolveResult(sink.awaitResultKind(t, worker.TaskMetaResolve))
	require.Equal(t, authoritative.LeaderEpoch, r.channels[base.Key].state.LeaderEpoch)
	require.Equal(t, authoritative.Leader, r.channels[base.Key].state.Leader)
}

func TestLoadedNewerMetaResolveFailureKeepsRuntimeAndAdmissionLease(t *testing.T) {
	base := followerTestMeta("loaded-refresh-failure")
	base.Leader = 2
	resolver := newBlockingMetaResolver(ch.Meta{}, ch.ErrNotReady)
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithMetaResolver(t, factory, newCapturingTransport(), resolver, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, base))
	loaded := r.channels[base.Key]
	loaded.waiters = map[ch.OpID]*Future{99: NewFuture()}
	newer := base
	newer.Leader = 1
	newer.LeaderEpoch = 2

	require.NoError(t, submitPullHintDirect(t, r, newer, 1))
	resolver.Unblock()
	r.handleMetaResolveResult(sink.awaitResultKind(t, worker.TaskMetaResolve))
	require.Same(t, loaded, r.channels[base.Key])
	require.Equal(t, ch.RoleLeader, loaded.state.Role)
	require.Equal(t, uint64(1), loaded.state.LeaderEpoch)
	require.Contains(t, loaded.waiters, ch.OpID(99))
	refresh := r.loadedMetaRefreshes[base.Key]
	require.NotNil(t, refresh)
	require.False(t, refresh.inflight)
	require.NoError(t, submitPullHintDirect(t, r, newer, 2))
	require.Equal(t, 1, resolver.Calls(), "failed resolve must retain its fixed admission lease")

	r.processDue(refresh.deadline.Add(time.Nanosecond))
	require.Nil(t, r.loadedMetaRefreshes, "expired failed resolve lease must be released without another hint")
}

func TestLoadedNewerInvalidAuthoritativeMetaKeepsRuntime(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(base *ch.Meta, resolved *ch.Meta)
	}{
		{name: "key mismatch", mutate: func(_ *ch.Meta, meta *ch.Meta) { meta.Key = "1:other" }},
		{name: "id mismatch", mutate: func(_ *ch.Meta, meta *ch.Meta) { meta.ID.ID = "other" }},
		{name: "same fence", mutate: func(base *ch.Meta, meta *ch.Meta) { meta.LeaderEpoch = base.LeaderEpoch }},
		{name: "not active", mutate: func(_ *ch.Meta, meta *ch.Meta) { meta.Status = ch.StatusCreating }},
		{name: "local not replica", mutate: func(_ *ch.Meta, meta *ch.Meta) {
			meta.Replicas = []ch.NodeID{1, 3}
			meta.ISR = []ch.NodeID{1, 3}
		}},
		{name: "leader not replica", mutate: func(_ *ch.Meta, meta *ch.Meta) { meta.Leader = 3 }},
		{name: "isr outside replicas", mutate: func(_ *ch.Meta, meta *ch.Meta) { meta.ISR = []ch.NodeID{1, 3} }},
		{name: "duplicate replica", mutate: func(_ *ch.Meta, meta *ch.Meta) { meta.Replicas = []ch.NodeID{1, 2, 2} }},
		{name: "duplicate isr", mutate: func(_ *ch.Meta, meta *ch.Meta) { meta.ISR = []ch.NodeID{1, 2, 2} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := followerTestMeta("loaded-refresh-invalid-" + tt.name)
			base.Leader = 2
			resolved := base
			resolved.Leader = 1
			resolved.LeaderEpoch = 2
			tt.mutate(&base, &resolved)
			resolver := newBlockingMetaResolver(resolved, nil)
			factory := store.NewMemoryFactory()
			sink := captureCompletionSink{results: make(chan worker.Result, 8)}
			pools := newDirectTestPoolsWithMetaResolver(t, factory, newCapturingTransport(), resolver, sink)
			defer pools.Close()
			r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
			require.NoError(t, applyMetaDirect(t, r, base))
			loaded := r.channels[base.Key]
			hint := base
			hint.Leader = 1
			hint.LeaderEpoch = 2

			require.NoError(t, submitPullHintDirect(t, r, hint, 1))
			resolver.Unblock()
			r.handleMetaResolveResult(sink.awaitResultKind(t, worker.TaskMetaResolve))
			require.Same(t, loaded, r.channels[base.Key])
			require.Equal(t, uint64(1), loaded.state.LeaderEpoch)
		})
	}
}

func TestLoadedNewerExplicitMetaSupersedesInflightResolve(t *testing.T) {
	base := followerTestMeta("loaded-refresh-explicit")
	base.Leader = 2
	newer := base
	newer.Leader = 1
	newer.LeaderEpoch = 2
	resolver := newBlockingMetaResolver(newer, nil)
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithMetaResolver(t, factory, newCapturingTransport(), resolver, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, base))
	loaded := r.channels[base.Key]

	require.NoError(t, submitPullHintDirect(t, r, newer, 1))
	resolver.AwaitStarted(t)
	require.NotNil(t, r.loadedMetaRefreshes[base.Key])
	require.NoError(t, applyMetaDirect(t, r, newer))
	require.Nil(t, r.loadedMetaRefreshes)

	resolver.Unblock()
	r.handleMetaResolveResult(sink.awaitResultKind(t, worker.TaskMetaResolve))
	require.Same(t, loaded, r.channels[base.Key])
	require.Equal(t, newer.LeaderEpoch, loaded.state.LeaderEpoch)
}

func TestLoadedNewerPullHintWithoutMetaResolverFailsClosed(t *testing.T) {
	factory := store.NewMemoryFactory()
	base := followerTestMeta("loaded-refresh-no-resolver")
	base.Leader = 2
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, base))
	loaded := r.channels[base.Key]
	newer := base
	newer.Leader = 1
	newer.LeaderEpoch = 2

	err := submitPullHintDirect(t, r, newer, 1)
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.Same(t, loaded, r.channels[base.Key])
	require.Equal(t, uint64(1), loaded.state.LeaderEpoch)
}

func TestLoadedLeaderSameOrOlderPullHintDoesNotReleaseRuntime(t *testing.T) {
	for _, tc := range []struct {
		name        string
		epoch       uint64
		leaderEpoch uint64
	}{
		{name: "same fence", epoch: 2, leaderEpoch: 2},
		{name: "older leader epoch", epoch: 2, leaderEpoch: 1},
		{name: "older epoch", epoch: 1, leaderEpoch: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factory := store.NewMemoryFactory()
			obs := &captureObserver{}
			meta := followerTestMeta("loaded-leader-same-or-older-" + tc.name)
			meta.Epoch = 2
			meta.LeaderEpoch = 2
			meta.Leader = 2
			r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16, Observer: obs})
			require.NoError(t, applyMetaDirect(t, r, meta))
			loaded := r.channels[meta.Key]
			require.Equal(t, ch.RoleLeader, loaded.state.Role)

			hint := meta
			hint.Epoch = tc.epoch
			hint.LeaderEpoch = tc.leaderEpoch
			hint.Leader = 1
			err := submitPullHintDirect(t, r, hint, 4)

			require.ErrorIs(t, err, ch.ErrStaleMeta)
			require.Same(t, loaded, r.channels[meta.Key])
			require.NotNil(t, r.channels[meta.Key].state)
			require.Nil(t, r.channels[meta.Key].pending)
			require.Equal(t, 0, obs.RuntimeEvicted())
		})
	}
}

func TestLoadedFollowerInvalidNewerPullHintDoesNotReleaseRuntime(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("loaded-invalid-newer")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	loaded := r.channels[meta.Key]
	require.NotNil(t, loaded.state)

	req := pullHintForMeta(meta, 5)
	req.LeaderEpoch = 2
	req.Leader = 0
	future := NewFuture()
	r.handleFollowerPullHint(Event{Kind: EventPullHint, Key: meta.Key, PullHint: req, Future: future})
	_, err := future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.Same(t, loaded, r.channels[meta.Key])
	require.NotNil(t, r.channels[meta.Key].state)
	require.Nil(t, r.channels[meta.Key].pending)
}

func TestLoadedFollowerMatchedFenceNonReplicaPullHintReturnsNotReplica(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("loaded-not-replica")
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	meta.MinISR = 1
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	loaded := r.channels[meta.Key]

	err := submitPullHintDirect(t, r, meta, 5)
	require.ErrorIs(t, err, ch.ErrNotReplica)
	require.Same(t, loaded, r.channels[meta.Key])
	require.NotNil(t, r.channels[meta.Key].state)
}

func TestPendingMetaApplyMetaCanConvertToLeader(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("pending-becomes-leader")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	installPendingMetaForTest(t, r, meta)
	leader := meta
	leader.LeaderEpoch = 2
	leader.Leader = 2
	leader.Replicas = []ch.NodeID{1, 2}
	leader.ISR = []ch.NodeID{1, 2}

	require.NoError(t, applyMetaDirect(t, r, leader))

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Nil(t, rc.pending)
	require.NotNil(t, rc.state)
	require.Equal(t, ch.RoleLeader, rc.state.Role)
	require.Equal(t, uint64(2), rc.state.LeaderEpoch)
}

func TestPendingMetaDeadlineExpiryReleasesShell(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("pending-expire")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16, PullHintRetryInterval: time.Millisecond})
	installPendingMetaForTest(t, r, meta)

	r.processDue(time.Now().Add(2 * time.Millisecond))

	require.Nil(t, r.channels[meta.Key])
}

func TestRuntimeEvictReleasesPendingMeta(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	defer net.UnblockPulls()
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("pending-runtime-evict")
	g, err := NewGroup(Config{LocalNode: 2, ReactorCount: 1, MailboxSize: 16, Store: factory, Transport: net})
	require.NoError(t, err)
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventPullHint, Key: meta.Key, PullHint: pullHintForMeta(meta, 1)}))

	result, err := g.RuntimeEvict(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)

	require.Equal(t, 1, result.Evicted)
	require.Equal(t, 0, result.SkippedBusy)
	require.Equal(t, 0, result.Missing)
}

func TestPendingMetaRPCBackpressureResultReleasesWithoutRetry(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("pending-rpc-backpressure")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	rc := installPendingMetaForTest(t, r, meta)
	opID := rc.pending.pullOpID

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.pending.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: opID},
		Err:   ch.ErrBackpressured,
	})

	require.Nil(t, r.channels[meta.Key])
}

func TestPendingMetaNeedMetaConversionSchedulesEmptyLaggingPull(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("pending-empty-lagging")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	rc := installPendingMetaForTest(t, r, meta)
	opID := rc.pending.pullOpID

	r.handleRPCPullResult(worker.Result{
		Kind:  worker.TaskRPCPull,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: rc.pending.generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: opID},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			LeaderHW:    0,
			LeaderLEO:   2,
			Meta:        &meta,
		}},
	})

	rc = r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Nil(t, rc.pending)
	require.True(t, rc.replication.dirty)
	require.False(t, rc.replication.nextPullAt.IsZero())
	require.Greater(t, r.due.Len(), 0)
}

func TestPendingMetaApplyMetaFollowerSchedulesReplication(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("pending-apply-schedules")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	installPendingMetaForTest(t, r, meta)

	require.NoError(t, applyMetaDirect(t, r, meta))

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Nil(t, rc.pending)
	require.True(t, rc.replication.dirty)
	require.Greater(t, r.due.Len(), 0)
}

func TestFollowerUnexpectedMetaResponseBacksOffAndSchedulesRetry(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("normal-meta-retry")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16, ReplicationMinBackoff: time.Millisecond, ReplicationMaxBackoff: time.Millisecond})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.pullInflight = true
	rc.replication.pullOpID = 7
	rc.replication.pullSubmittedAt = time.Now()

	r.handleRPCPullResult(worker.Result{
		Kind: worker.TaskRPCPull,
		Fence: ch.Fence{
			ChannelKey:  meta.Key,
			Generation:  rc.state.Generation,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			OpID:        7,
		},
		RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Meta:        &meta,
		}},
	})

	require.ErrorIs(t, rc.replication.lastError, ch.ErrInvalidConfig)
	require.False(t, rc.replication.nextPullAt.IsZero())
	require.Greater(t, r.due.Len(), 0)
}

func TestPendingMetaAcceptsExplicitNonDerivedChannelKey(t *testing.T) {
	net := newCapturingTransport()
	net.BlockPulls()
	defer net.UnblockPulls()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()
	meta := followerTestMeta("explicit-pending-key")
	meta.Key = ch.ChannelKey("explicit:key")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})

	require.NoError(t, submitPullHintDirect(t, r, meta, 1))
	require.NotNil(t, r.channels[meta.Key].pending)
	require.NoError(t, applyMetaDirect(t, r, meta))
	require.NotNil(t, r.channels[meta.Key].state)
}

func TestFollowerReplicationStageMetricsTrackHintPullApplyAndAckReturn(t *testing.T) {
	obs := &replicationStageObserver{}
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	meta := followerTestMeta("replication-stages")
	net.SetPullResponse(transport.PullResponse{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		LeaderEpoch: meta.LeaderEpoch,
		LeaderHW:    1,
		LeaderLEO:   1,
		Records:     []ch.Record{{ID: 10, Index: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16, Observer: obs})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.replication.parkWithRecovery(meta.Key, time.Now(), time.Minute, 0)

	r.handleFollowerPullHint(Event{Kind: EventPullHint, Key: meta.Key, PullHint: transport.PullHintRequest{
		ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
		Leader: meta.Leader, LeaderLEO: 1, ActivityVersion: 1, Reason: transport.PullHintReasonAppend,
	}, Future: NewFuture()})
	pullResult := sink.awaitResultKind(t, worker.TaskRPCPull)
	r.handleRPCPullResult(pullResult)
	applyResult := sink.awaitResultKind(t, worker.TaskStoreApply)
	r.handleStoreApplyResult(applyResult)

	net.SetPullResponse(transport.PullResponse{
		ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, LeaderHW: 1, LeaderLEO: 1,
	})
	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()})
	ackPullResult := sink.awaitResultKind(t, worker.TaskRPCPull)
	require.Equal(t, uint64(1), net.LastPull().AckOffset)
	r.handleRPCPullResult(ackPullResult)

	events := obs.Events()
	requireReplicationStage(t, events, "follower_pull_hint_to_submit", "ok")
	requireReplicationStage(t, events, "follower_pull_rpc", "ok")
	requireReplicationStage(t, events, "follower_store_apply", "ok")
	requireReplicationStage(t, events, "follower_apply_to_ack_return", "ok")
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
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)})
		pull := net.LastPull()
		return net.PullCalls() >= 2 && pull.NextOffset == 2 && pull.AckOffset == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, 0, net.AckCalls())
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

func TestOrdinaryApplyReturnsProgressWithPullAckOffsetAfterDurableApply(t *testing.T) {
	net := newCapturingTransport()
	meta := followerTestMeta("ordinary-progress-ack")
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

	factory.UnblockApplies()
	require.Eventually(t, func() bool {
		_ = awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Millisecond)})
		return net.PullCalls() >= 2 && net.LastPull().AckOffset == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, 0, net.AckCalls())
}

func TestStaleRPCPullCompletionDoesNotClearNewerPullInflight(t *testing.T) {
	state := replicationState{pullInflight: true, pullOpID: 2}
	stale := worker.Result{Kind: worker.TaskRPCPull, Fence: ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 1}, RPCPull: &worker.RPCPullResult{Response: transport.PullResponse{ChannelKey: "1:a", Epoch: 1, LeaderEpoch: 1}}}
	applied := state.applyPullResult(stale, ch.Fence{ChannelKey: "1:a", Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: state.pullOpID}, time.Unix(1, 0))
	require.False(t, applied)
	require.True(t, state.pullInflight)
	require.Equal(t, ch.OpID(2), state.pullOpID)
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

func TestLeaderPullInvalidShapeWinsBeforeMembership(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("pull-invalid-before-membership")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 211,
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 99, NextOffset: 0, MaxBytes: 1024, NeedMeta: true,
		},
	})
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
}

func TestLeaderPullNotFoundWinsBeforeInvalidShape(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	key := ch.ChannelKey("1:missing-invalid-shape")
	future, err := g.Submit(context.Background(), key, Event{
		Kind: EventPull,
		Key:  key,
		OpID: 216,
		Pull: transport.PullRequest{
			ChannelKey: key, ChannelID: ch.ChannelID{ID: "missing-invalid-shape", Type: 1},
			Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 0, MaxBytes: 1024, NeedMeta: true,
		},
	})
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrChannelNotFound)
}

func TestLeaderPullStaleFenceWinsBeforeInvalidShape(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("pull-stale-before-invalid")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 217,
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: ch.ChannelID{ID: "other", Type: meta.ID.Type},
			Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2,
			NextOffset: 0, MaxBytes: 1024, NeedMeta: true,
		},
	})
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
}

func TestLeaderPullNeedMetaReturnsClonedRuntimeMeta(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})
	meta := followerTestMeta("pull-needmeta-clone")
	meta.RetentionThroughSeq = 7
	meta.LeaseUntil = time.Now().Add(30 * time.Second).Round(0)
	meta.WriteFence = ch.WriteFence{Token: "migration-7", Version: 7, Reason: ch.WriteFenceReasonLeaderTransfer, Until: time.Now().Add(time.Minute)}
	require.NoError(t, applyMetaDirect(t, r, meta))

	future := NewFuture()
	r.handleLeaderPull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		OpID:    212,
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024, NeedMeta: true,
		},
	})

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.NotNil(t, result.Pull.Meta)
	require.Equal(t, meta.Key, result.Pull.Meta.Key)
	require.Equal(t, meta.ID, result.Pull.Meta.ID)
	require.Equal(t, meta.Epoch, result.Pull.Meta.Epoch)
	require.Equal(t, meta.LeaderEpoch, result.Pull.Meta.LeaderEpoch)
	require.Equal(t, meta.Leader, result.Pull.Meta.Leader)
	require.Equal(t, meta.Replicas, result.Pull.Meta.Replicas)
	require.Equal(t, meta.ISR, result.Pull.Meta.ISR)
	require.Equal(t, meta.MinISR, result.Pull.Meta.MinISR)
	require.Equal(t, meta.RetentionThroughSeq, result.Pull.Meta.RetentionThroughSeq)
	require.Equal(t, meta.LeaseUntil, result.Pull.Meta.LeaseUntil)
	require.Equal(t, meta.WriteFence, result.Pull.Meta.WriteFence)
	require.Equal(t, meta.Status, result.Pull.Meta.Status)

	result.Pull.Meta.Replicas[0] = 99
	result.Pull.Meta.ISR[0] = 99
	rc := r.channels[meta.Key]
	require.Equal(t, []ch.NodeID{1, 2}, rc.state.Replicas)
	require.Equal(t, []ch.NodeID{1, 2}, rc.state.ISR)
}

func TestLeaderPullNeedMetaMatchedFenceNonReplicaFailsWithNotReplica(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("pull-needmeta-not-replica")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 213,
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 99, NextOffset: 1, MaxBytes: 1024, NeedMeta: true,
		},
	})
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrNotReplica)
}

func TestLeaderPullNeedMetaNonActiveRuntimeFailsNotReadyWithoutRecords(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})
	meta := followerTestMeta("pull-needmeta-not-ready")
	meta.Status = ch.StatusCreating
	require.NoError(t, applyMetaDirect(t, r, meta))

	future := NewFuture()
	r.handleLeaderPull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		OpID:    214,
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024, NeedMeta: true,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := future.Await(ctx)
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.ErrorIs(t, result.Err, ch.ErrNotReady)
	require.Nil(t, result.Pull.Meta)
	require.Empty(t, result.Pull.Records)
}

func TestLeaderPullWithoutNeedMetaKeepsResponseMetadataEmpty(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})
	meta := followerTestMeta("pull-without-needmeta")
	require.NoError(t, applyMetaDirect(t, r, meta))

	future := NewFuture()
	r.handleLeaderPull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		OpID:    215,
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
	})

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Nil(t, result.Pull.Meta)
	require.Equal(t, meta.Key, result.Pull.ChannelKey)
	require.Equal(t, meta.Epoch, result.Pull.Epoch)
	require.Equal(t, meta.LeaderEpoch, result.Pull.LeaderEpoch)
}

func TestLeaderPullWithoutNeedMetaAllowsNonActiveRuntime(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})
	meta := followerTestMeta("pull-without-needmeta-non-active")
	meta.Status = ch.StatusCreating
	require.NoError(t, applyMetaDirect(t, r, meta))

	future := NewFuture()
	r.handleLeaderPull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  future,
		Context: context.Background(),
		OpID:    218,
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
	})

	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Nil(t, result.Pull.Meta)
	require.Equal(t, meta.Key, result.Pull.ChannelKey)
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
	rc.lifecycle.followers[2] = &lifecycleFollower{
		match:              0,
		lastPullAt:         lastPullAt,
		parked:             true,
		stoppedVersion:     1,
		nextExpectedPullAt: nextExpectedPullAt,
	}
	rc.pullWaiters = map[ch.OpID]*pullWaiter{
		99: {future: NewFuture(), follower: 2, nextOffset: 1},
	}

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	require.Equal(t, lastPullAt, rc.lifecycle.followers[2].lastPullAt)
	require.True(t, rc.lifecycle.followers[2].parked)
	require.NotZero(t, rc.lifecycle.followers[2].stoppedVersion)
	require.Equal(t, nextExpectedPullAt, rc.lifecycle.followers[2].nextExpectedPullAt)
	require.Equal(t, uint64(0), rc.lifecycle.followers[2].match)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
}

func TestLeaderPullAckOffsetAdvancesHWAndCompletesQuorumWaiter(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	obs := &appendWaitStageObserver{}

	meta := ch.Meta{
		Key:         "1:pull-ack-complete",
		ID:          ch.ChannelID{ID: "pull-ack-complete", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2},
		ISR:         []ch.NodeID{1, 2},
		MinISR:      2,
		Status:      ch.StatusActive,
	}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1, Observer: obs})
	require.NoError(t, applyMetaDirect(t, r, meta))

	appendFuture := NewFuture()
	r.handleAppend(Event{
		Kind:   EventAppend,
		Key:    meta.Key,
		OpID:   1,
		Future: appendFuture,
		Append: ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeQuorum,
			Messages: []ch.Message{{
				MessageID:   10,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte("a"),
			}},
		},
	})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskStoreAppend)})
	requireFuturePending(t, appendFuture)
	requireAppendWaitStage(t, obs.Events(), "store_append_wait", ch.CommitModeQuorum, "ok")
	requireNoAppendWaitStage(t, obs.Events(), "post_store_commit_wait", ch.CommitModeQuorum, "ok")
	requireNoAppendWaitStage(t, obs.Events(), "quorum_follower_pull_wait", ch.CommitModeQuorum, "ok")

	pullFuture := NewFuture()
	r.handleLeaderPull(Event{
		Kind:   EventPull,
		Key:    meta.Key,
		OpID:   2,
		Future: pullFuture,
		Pull: transport.PullRequest{
			ChannelKey:  meta.Key,
			ChannelID:   meta.ID,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Follower:    2,
			NextOffset:  1,
			MaxBytes:    1024,
		},
	})

	pullResult := awaitFutureResult(t, pullFuture)
	require.NoError(t, pullResult.Err)
	require.Len(t, pullResult.Pull.Records, 1)
	require.Equal(t, uint64(1), pullResult.Pull.Records[0].Index)
	requireFuturePending(t, appendFuture)
	requireAppendWaitStage(t, obs.Events(), "quorum_follower_pull_wait", ch.CommitModeQuorum, "ok")
	requireNoAppendWaitStage(t, obs.Events(), "quorum_ack_offset_wait", ch.CommitModeQuorum, "ok")
	requireNoAppendWaitStage(t, obs.Events(), "quorum_hw_advance_wait", ch.CommitModeQuorum, "ok")
	requireNoAppendWaitStage(t, obs.Events(), "quorum_final_complete_wait", ch.CommitModeQuorum, "ok")
	requireNoAppendWaitStage(t, obs.Events(), "post_store_commit_wait", ch.CommitModeQuorum, "ok")

	ackFuture := NewFuture()
	r.handleLeaderPull(Event{
		Kind:   EventPull,
		Key:    meta.Key,
		OpID:   3,
		Future: ackFuture,
		Pull: transport.PullRequest{
			ChannelKey:  meta.Key,
			ChannelID:   meta.ID,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Follower:    2,
			NextOffset:  2,
			AckOffset:   1,
			MaxBytes:    1024,
		},
	})

	ackResult := awaitFutureResult(t, ackFuture)
	require.NoError(t, ackResult.Err)
	require.Empty(t, ackResult.Pull.Records)
	appendResult := awaitFutureResult(t, appendFuture)
	require.NoError(t, appendResult.Err)
	require.Len(t, appendResult.AppendBatch.Items, 1)
	require.Equal(t, uint64(1), appendResult.AppendBatch.Items[0].MessageSeq)
	requireAppendWaitStage(t, obs.Events(), "quorum_ack_offset_wait", ch.CommitModeQuorum, "ok")
	requireAppendWaitStage(t, obs.Events(), "quorum_hw_advance_wait", ch.CommitModeQuorum, "ok")
	requireAppendWaitStage(t, obs.Events(), "quorum_final_complete_wait", ch.CommitModeQuorum, "ok")
	requireAppendWaitStage(t, obs.Events(), "post_store_commit_wait", ch.CommitModeQuorum, "ok")
	rc := r.channels[meta.Key]
	require.Equal(t, uint64(1), rc.state.HW)
	require.Equal(t, uint64(1), rc.state.Progress[2].Match)
}

func TestLeaderProgressAckAdvancesHWAndCompletesQuorumWaiter(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	obs := &appendWaitStageObserver{}

	meta := ch.Meta{
		Key:         "1:progress-ack-complete",
		ID:          ch.ChannelID{ID: "progress-ack-complete", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2},
		ISR:         []ch.NodeID{1, 2},
		MinISR:      2,
		Status:      ch.StatusActive,
	}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1, Observer: obs})
	require.NoError(t, applyMetaDirect(t, r, meta))

	appendFuture := NewFuture()
	r.handleAppend(Event{
		Kind:   EventAppend,
		Key:    meta.Key,
		OpID:   1,
		Future: appendFuture,
		Append: ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeQuorum,
			Messages: []ch.Message{{
				MessageID:   10,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte("a"),
			}},
		},
	})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskStoreAppend)})
	requireFuturePending(t, appendFuture)

	pullFuture := NewFuture()
	r.handleLeaderPull(Event{
		Kind:   EventPull,
		Key:    meta.Key,
		OpID:   2,
		Future: pullFuture,
		Pull: transport.PullRequest{
			ChannelKey:  meta.Key,
			ChannelID:   meta.ID,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Follower:    2,
			NextOffset:  1,
			MaxBytes:    1024,
		},
	})
	require.NoError(t, awaitFutureResult(t, pullFuture).Err)
	requireFuturePending(t, appendFuture)

	ackFuture := NewFuture()
	r.handleLeaderAck(Event{
		Kind:   EventAck,
		Key:    meta.Key,
		Future: ackFuture,
		Ack: transport.AckRequest{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Follower:    2,
			MatchOffset: 1,
		},
	})

	require.NoError(t, awaitFutureResult(t, ackFuture).Err)
	appendResult := awaitFutureResult(t, appendFuture)
	require.NoError(t, appendResult.Err)
	require.Len(t, appendResult.AppendBatch.Items, 1)
	require.Equal(t, uint64(1), appendResult.AppendBatch.Items[0].MessageSeq)
	requireAppendWaitStage(t, obs.Events(), "quorum_ack_offset_wait", ch.CommitModeQuorum, "ok")
	requireAppendWaitStage(t, obs.Events(), "quorum_hw_advance_wait", ch.CommitModeQuorum, "ok")
	requireAppendWaitStage(t, obs.Events(), "quorum_final_complete_wait", ch.CommitModeQuorum, "ok")
	requireAppendWaitStage(t, obs.Events(), "post_store_commit_wait", ch.CommitModeQuorum, "ok")
	rc := r.channels[meta.Key]
	require.Equal(t, uint64(1), rc.state.HW)
	require.Equal(t, uint64(1), rc.state.Progress[2].Match)
}

func TestLeaderPullRejectsAckOffsetAboveLeaderLEO(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("pull-ack-above-leo")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPull,
		Key:  meta.Key,
		OpID: 301,
		Pull: transport.PullRequest{
			ChannelKey:  meta.Key,
			ChannelID:   meta.ID,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Follower:    2,
			NextOffset:  1,
			AckOffset:   1,
			MaxBytes:    1024,
		},
	})
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
	require.Equal(t, uint64(0), rc.state.HW)
	require.Equal(t, uint64(0), rc.state.Progress[2].Match)
}

func TestLeaderProgressAckRejectsMatchAboveLEO(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	meta := followerTestMeta("progress-ack-above-leo")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventAck,
		Key:  meta.Key,
		Ack: transport.AckRequest{
			ChannelKey:  meta.Key,
			Epoch:       meta.Epoch,
			LeaderEpoch: meta.LeaderEpoch,
			Follower:    2,
			MatchOffset: 1,
		},
	})
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	rc := g.reactors[g.router.PickIndex(meta.Key)].channels[meta.Key]
	require.Equal(t, uint64(0), rc.state.HW)
	require.Equal(t, uint64(0), rc.state.Progress[2].Match)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	require.NotNil(t, rc.lifecycle.followers[2])
	require.True(t, rc.lifecycle.followers[2].parked)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[3] = machine.ReplicaProgress{Match: 2}
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[3] = machine.ReplicaProgress{Match: 3}
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	require.NotZero(t, rc.lifecycle.followers[2].stopOfferedVersion)
	require.Equal(t, uint64(3), rc.lifecycle.followers[2].stopOfferedVersion)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 2}
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	require.Less(t, rc.lifecycle.followers[2].match, rc.state.LEO)
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
	rc.lifecycle.lastAppendAt = time.Now()
	rc.lifecycle.version = 3

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	rc.lifecycle.lastAppendAt = time.Now()
	rc.lifecycle.version = 3

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 1}

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	require.False(t, rc.lifecycle.followers[2].parked)
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

func TestLeaderRejectsOrdinaryAckAfterLeaderEpochBump(t *testing.T) {
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
	err = awaitSubmit(g, meta.Key, staleAck)
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	future, err := g.Submit(context.Background(), meta.Key, appendQuorumEvent(meta, 1, "requires-current-ack"))
	require.NoError(t, err)
	requireFuturePending(t, future)
}

func TestLeaderRejectsOrdinaryAckFromUnknownFollower(t *testing.T) {
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
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	requireFuturePending(t, future)
}

func TestLeaderRejectsOrdinaryAckWithMismatchedChannelKey(t *testing.T) {
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
	require.ErrorIs(t, err, ch.ErrStaleMeta)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = 3
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handleLeaderAck(Event{
		Kind: EventAck, Key: meta.Key, Future: future,
		Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, MatchOffset: 4, ActivityVersion: 3, Stopped: true},
	})
	_, err := future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Zero(t, rc.lifecycle.followers[2].stoppedVersion)
	require.Equal(t, uint64(3), rc.lifecycle.followers[2].match)
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
	rc.lifecycle.version = 3
	r.syncLeaderFollowers(rc)
	rc.lifecycle.followers[2].stopOfferedVersion = 2

	future := NewFuture()
	r.handleLeaderAck(Event{
		Kind: EventAck, Key: meta.Key, Future: future,
		Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true},
	})
	_, err := future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Zero(t, rc.lifecycle.followers[2].stoppedVersion)
	require.Equal(t, uint64(3), rc.lifecycle.followers[2].match)
	require.Equal(t, uint64(3), rc.state.Progress[2].Match)
	require.Equal(t, uint64(3), rc.state.HW)
}

func TestLeaderZeroVersionStoppedAckRecordsStoppedFollower(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := testMeta("leader-zero-stopped-ack", 1, 1)
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
	rc.lifecycle.version = 0
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handleLeaderAck(Event{
		Kind:   EventAck,
		Key:    meta.Key,
		Future: future,
		Ack: transport.AckRequest{
			ChannelKey:      meta.Key,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			Follower:        2,
			MatchOffset:     rc.state.LEO,
			ActivityVersion: 0,
			Stopped:         true,
		},
	})

	require.NoError(t, awaitFutureResult(t, future).Err)
	require.True(t, rc.lifecycle.followers[2].stoppedZero)
	require.True(t, runtimeViewFromChannel(rc, time.Now(), AppendFenceView{}).AllFollowersStopped())
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = 3
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[3] = machine.ReplicaProgress{Match: 3}
	r.syncLeaderFollowers(rc)

	firstFuture := NewFuture()
	r.handleLeaderAck(Event{
		Kind: EventAck, Key: meta.Key, Future: firstFuture,
		Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true},
	})
	require.NoError(t, awaitFutureResult(t, firstFuture).Err)
	require.Contains(t, r.channels, meta.Key)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)

	secondFuture := NewFuture()
	r.handleLeaderAck(Event{
		Kind: EventAck, Key: meta.Key, Future: secondFuture,
		Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 3, MatchOffset: 3, ActivityVersion: 3, Stopped: true},
	})
	require.NoError(t, awaitFutureResult(t, secondFuture).Err)
	require.Equal(t, lifecycleLeaderCheckpointing, rc.lifecycle.stage)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = 3
	r.syncLeaderFollowers(rc)
	rc.lifecycle.followers[2].hint.inflight = true
	rc.lifecycle.followers[2].hint.opID = 99
	rc.lifecycle.followers[2].lastHintVersion = 3
	rc.lifecycle.pullHintInflight = map[ch.OpID]lifecyclePullHintInflight{
		99: {follower: 2, version: 3, reason: transport.PullHintReasonAppend},
	}

	future := NewFuture()
	r.handleLeaderAck(Event{
		Kind: EventAck, Key: meta.Key, Future: future,
		Ack: transport.AckRequest{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
		},
	})
	require.NoError(t, awaitFutureResult(t, future).Err)
	require.Empty(t, rc.lifecycle.pullHintInflight)
	require.False(t, rc.lifecycle.followers[2].hint.inflight)
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
	rc.lifecycle.version = 4
	rc.lifecycle.followers[2].hint.inflight = true
	rc.lifecycle.followers[2].hint.opID = 12
	rc.lifecycle.followers[2].lastHintVersion = 4
	rc.lifecycle.pullHintInflight = map[ch.OpID]lifecyclePullHintInflight{
		11: {follower: 2, version: 3, reason: transport.PullHintReasonAppend},
		12: {follower: 2, version: 4, reason: transport.PullHintReasonAppend},
	}

	r.handleRPCPullHintResult(worker.Result{
		Kind:        worker.TaskRPCPullHint,
		Fence:       ch.Fence{ChannelKey: meta.Key, Generation: rc.state.Generation, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 11},
		RPCPullHint: &worker.RPCPullHintResult{},
	})

	require.True(t, rc.lifecycle.followers[2].hint.inflight)
	require.Contains(t, rc.lifecycle.pullHintInflight, ch.OpID(12))
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
	rc.lifecycle.lastAppendAt = time.Now()
	rc.lifecycle.version = 3
	r.syncLeaderFollowers(rc)

	for _, follower := range []ch.NodeID{2, 3} {
		future := NewFuture()
		r.handleLeaderAck(Event{
			Kind: EventAck, Key: meta.Key, Future: future,
			Ack: transport.AckRequest{
				ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
				Follower: follower, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
			},
		})
		require.NoError(t, awaitFutureResult(t, future).Err)
	}
	require.NotZero(t, rc.lifecycle.followers[2].stoppedVersion)
	require.NotZero(t, rc.lifecycle.followers[3].stoppedVersion)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)

	fenced := meta
	fenced.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, fenced))

	rc.lifecycle.lastAppendAt = time.Now().Add(-2 * time.Hour)
	r.tickLifecycleController(rc, time.Now())
	require.Contains(t, r.channels, meta.Key)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)

	firstFuture := NewFuture()
	r.handleLeaderAck(Event{
		Kind: EventAck, Key: meta.Key, Future: firstFuture,
		Ack: transport.AckRequest{
			ChannelKey: meta.Key, Epoch: fenced.Epoch, LeaderEpoch: fenced.LeaderEpoch,
			Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
		},
	})
	require.NoError(t, awaitFutureResult(t, firstFuture).Err)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)

	secondFuture := NewFuture()
	r.handleLeaderAck(Event{
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

	r.tickLifecycleController(rc, rc.lifecycle.lastAppendAt.Add(time.Hour))
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
	updated.LeaderEpoch++
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
	require.Zero(t, rc.lifecycle.lastAppendAt)

	r.tickLifecycleController(rc, time.Now().Add(time.Hour))
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
	r.tickLifecycleController(rc, rc.lifecycle.lastAppendAt.Add(time.Hour))
	staleCheckpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)

	appendFuture := NewFuture()
	r.handleAppend(appendEventWithFuture(meta, 2, "after checkpoint", appendFuture))
	require.False(t, rc.lifecycle.checkpoint.inflight)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = rc.state.LEO
	rc.lifecycle.stage = lifecycleLeaderCheckpointing
	rc.lifecycle.checkpoint.inflight = true
	rc.lifecycle.checkpoint.opID = 99
	rc.lifecycle.checkpoint.version = rc.lifecycle.version

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
	require.False(t, rc.lifecycle.checkpoint.inflight)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = rc.state.LEO
	rc.lifecycle.stage = lifecycleLeaderCheckpointing
	rc.lifecycle.checkpoint.inflight = true
	rc.lifecycle.checkpoint.opID = 99
	rc.lifecycle.checkpoint.version = rc.lifecycle.version

	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: worker.Result{
		Kind: worker.TaskStoreCheckpoint,
		Fence: ch.Fence{
			ChannelKey: meta.Key, Generation: rc.state.Generation,
			Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 99,
		},
		StoreCheckpoint: &worker.StoreCheckpointResult{},
	}})
	require.True(t, rc.lifecycle.finalCheck.queued)

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
	require.False(t, rc.lifecycle.finalCheck.inflight)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = rc.state.LEO
	rc.lifecycle.finalCheck.inflight = true
	rc.lifecycle.finalCheck.version = rc.lifecycle.version

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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.finalCheck.inflight = true
	rc.lifecycle.finalCheck.version = rc.lifecycle.version

	release := r.reserveAppend(meta.Key)
	now := time.Now()
	r.submitLeaderEvictReady(rc, now, r.currentAppendSubmitSeq(meta.Key))
	events := r.mailbox.DrainInto(nil, defaultReactorDrain)
	require.Len(t, events, 1)
	r.handle(events[0])
	require.Contains(t, r.channels, meta.Key)
	retryAt := rc.lifecycle.checkpoint.retryAt
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.finalCheck.inflight = true
	rc.lifecycle.finalCheck.version = rc.lifecycle.version

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
	require.Zero(t, rc.lifecycle.version)

	promoted := meta
	promoted.Leader = 1
	promoted.LeaderEpoch = 2
	require.NoError(t, applyMetaDirect(t, r, promoted))
	require.Equal(t, ch.RoleLeader, rc.state.Role)
	require.Equal(t, uint64(3), rc.lifecycle.version)

	rc.lifecycle.loadedAt = time.Now().Add(-time.Hour)
	r.syncLeaderFollowers(rc)
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	r.syncLeaderFollowers(rc)
	pullFuture := NewFuture()
	r.handleLeaderPull(Event{
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
	r.handleLeaderAck(Event{
		Kind:   EventAck,
		Key:    promoted.Key,
		Future: ackFuture,
		Ack: transport.AckRequest{
			ChannelKey: promoted.Key, Epoch: promoted.Epoch, LeaderEpoch: promoted.LeaderEpoch,
			Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
		},
	})
	require.NoError(t, awaitFutureResult(t, ackFuture).Err)

	r.driveLifecycle(rc, lifecycleEvent{kind: lifecycleEventIdleTick, now: time.Now()})
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
	r.handleLeaderPull(Event{
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
	require.NotNil(t, rc.lifecycle.followers[2])
	require.False(t, rc.lifecycle.followers[2].parked)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = rc.state.LEO
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	require.NotNil(t, rc.lifecycle.followers[2])
	require.Zero(t, rc.lifecycle.followers[2].stopOfferedVersion)
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
	rc.lifecycle.lastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.version = 1

	future := NewFuture()
	r.handleLeaderPull(Event{
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
	require.NotNil(t, rc.lifecycle.followers[2])
	require.True(t, rc.lifecycle.followers[2].parked)
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
	r.handleLeaderPull(Event{
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
	r.handleLeaderPull(Event{
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
	r.handleLeaderPull(Event{
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
	r.handleLeaderPull(Event{
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
	r.handleLeaderPull(Event{
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
	r.handleLeaderPull(Event{
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

	r.tickLifecycleController(rc, rc.lifecycle.lastAppendAt.Add(time.Hour))
	staleCheckpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.True(t, rc.lifecycle.checkpoint.inflight)

	fenced := meta
	fenced.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, fenced))
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: staleCheckpoint})
	require.Contains(t, r.channels, meta.Key)
	require.False(t, rc.lifecycle.checkpoint.inflight)
	require.Zero(t, rc.lifecycle.checkpoint.opID)
	require.Zero(t, rc.lifecycle.checkpoint.version)

	r.tickLifecycleController(rc, rc.lifecycle.lastAppendAt.Add(2*time.Hour))
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
	rc.lifecycle.version = 1
	rc.lifecycle.stage = lifecycleLeaderCheckpointing
	rc.lifecycle.checkpoint.inflight = true
	rc.lifecycle.checkpoint.opID = 99
	rc.lifecycle.checkpoint.version = 1
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 1}
	rc.state.Progress[3] = machine.ReplicaProgress{Match: 1}
	r.syncLeaderFollowers(rc)
	rc.lifecycle.followers[2].stoppedVersion = 1
	rc.lifecycle.followers[3].stoppedVersion = 1

	appendFuture := NewFuture()
	r.handleAppend(appendEventWithFuture(meta, 2, "reactivate followers", appendFuture))
	require.False(t, rc.lifecycle.checkpoint.inflight)
	require.Zero(t, rc.lifecycle.checkpoint.opID)
	require.Equal(t, lifecycleLive, rc.lifecycle.stage)

	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	require.NoError(t, awaitFutureResult(t, appendFuture).Err)
	require.Equal(t, uint64(2), rc.lifecycle.version)
	require.Zero(t, rc.lifecycle.followers[2].stoppedVersion)
	require.Zero(t, rc.lifecycle.followers[3].stoppedVersion)
	require.True(t, rc.lifecycle.followers[2].lastPullAt.IsZero())
	require.True(t, rc.lifecycle.followers[3].lastPullAt.IsZero())
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

func pullHintForMeta(meta ch.Meta, leaderLEO uint64) transport.PullHintRequest {
	return transport.PullHintRequest{
		ChannelKey:      meta.Key,
		ChannelID:       meta.ID,
		Epoch:           meta.Epoch,
		LeaderEpoch:     meta.LeaderEpoch,
		Leader:          meta.Leader,
		LeaderLEO:       leaderLEO,
		ActivityVersion: leaderLEO,
		Reason:          transport.PullHintReasonAppend,
	}
}

func submitPullHintDirect(t *testing.T, r *Reactor, meta ch.Meta, leaderLEO uint64) error {
	t.Helper()
	future := NewFuture()
	r.handleFollowerPullHint(Event{Kind: EventPullHint, Key: meta.Key, PullHint: pullHintForMeta(meta, leaderLEO), Future: future})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := future.Await(ctx)
	return err
}

func installPendingMetaForTest(t *testing.T, r *Reactor, meta ch.Meta) *runtimeChannel {
	t.Helper()
	cs, err := r.cfg.Store.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	initial, err := cs.Load(context.Background())
	require.NoError(t, err)
	rc := &runtimeChannel{
		store: cs,
		pending: &pendingMetaState{
			key:             meta.Key,
			id:              meta.ID,
			generation:      10,
			epoch:           meta.Epoch,
			leaderEpoch:     meta.LeaderEpoch,
			leader:          meta.Leader,
			leaderLEO:       1,
			activityVersion: 1,
			deadline:        r.pendingMetaDeadline(time.Now()),
			initial: storeInitialState{
				LEO:          initial.LEO,
				HW:           initial.HW,
				CheckpointHW: initial.CheckpointHW,
			},
			pullInflight:           true,
			pullOpID:               11,
			pullSubmittedAt:        time.Now(),
			pullSubmittedAckOffset: initial.LEO,
		},
	}
	r.channels[meta.Key] = rc
	r.schedulePendingMetaDeadline(rc)
	return rc
}

func newFollowerReplicationTestRuntime(t *testing.T) (*Reactor, *runtimeChannel) {
	t.Helper()
	factory := store.NewMemoryFactory()
	meta := followerTestMeta("parked-follower-runtime")
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	return r, rc
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
	lastNode  ch.NodeID
	lastPull  transport.PullRequest
	lastAck   transport.AckRequest
	pullResp  transport.PullResponse
	pullErr   error
	ackErr    error
	blockPull chan struct{}
}

type temporaryPullError struct{}

func (temporaryPullError) Error() string   { return "temporary pull error" }
func (temporaryPullError) Timeout() bool   { return true }
func (temporaryPullError) Temporary() bool { return true }

type pendingMetaMetricsObserver struct {
	captureObserver
	pendingMu        sync.Mutex
	pendingCount     int
	pendingEvents    map[string]int
	pendingErrors    map[string]error
	needMetaPulls    map[string]int
	needMetaPullErrs map[string]error
}

func (o *pendingMetaMetricsObserver) SetPendingMetaCount(reactorID int, count int) {
	o.pendingMu.Lock()
	defer o.pendingMu.Unlock()
	o.pendingCount = count
}

func (o *pendingMetaMetricsObserver) ObservePendingMeta(event string, err error) {
	o.pendingMu.Lock()
	defer o.pendingMu.Unlock()
	if o.pendingEvents == nil {
		o.pendingEvents = make(map[string]int)
	}
	if o.pendingErrors == nil {
		o.pendingErrors = make(map[string]error)
	}
	o.pendingEvents[event]++
	o.pendingErrors[event] = err
}

func (o *pendingMetaMetricsObserver) ObserveNeedMetaPull(result string, err error) {
	o.pendingMu.Lock()
	defer o.pendingMu.Unlock()
	if o.needMetaPulls == nil {
		o.needMetaPulls = make(map[string]int)
	}
	if o.needMetaPullErrs == nil {
		o.needMetaPullErrs = make(map[string]error)
	}
	o.needMetaPulls[result]++
	o.needMetaPullErrs[result] = err
}

func (o *pendingMetaMetricsObserver) PendingMetaCount() int {
	o.pendingMu.Lock()
	defer o.pendingMu.Unlock()
	return o.pendingCount
}

func (o *pendingMetaMetricsObserver) PendingMetaEvents(event string) int {
	o.pendingMu.Lock()
	defer o.pendingMu.Unlock()
	return o.pendingEvents[event]
}

func (o *pendingMetaMetricsObserver) LastPendingMetaError(event string) error {
	o.pendingMu.Lock()
	defer o.pendingMu.Unlock()
	return o.pendingErrors[event]
}

func (o *pendingMetaMetricsObserver) NeedMetaPulls(result string) int {
	o.pendingMu.Lock()
	defer o.pendingMu.Unlock()
	return o.needMetaPulls[result]
}

func newCapturingTransport() *capturingTransport {
	return &capturingTransport{}
}

func (t *capturingTransport) Pull(ctx context.Context, node ch.NodeID, req transport.PullRequest) (transport.PullResponse, error) {
	t.mu.Lock()
	t.pullCalls++
	t.lastNode = node
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

func (t *capturingTransport) LastPullNode() ch.NodeID {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastNode
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

type blockingMetaResolver struct {
	mu      sync.Mutex
	meta    ch.Meta
	err     error
	calls   int
	started chan struct{}
	unblock chan struct{}
}

func newBlockingMetaResolver(meta ch.Meta, err error) *blockingMetaResolver {
	return &blockingMetaResolver{
		meta:    meta,
		err:     err,
		started: make(chan struct{}, 8),
		unblock: make(chan struct{}),
	}
}

func (r *blockingMetaResolver) ResolveChannelMeta(ctx context.Context, _ ch.ChannelID) (ch.Meta, error) {
	r.mu.Lock()
	r.calls++
	meta := r.meta
	err := r.err
	unblock := r.unblock
	r.mu.Unlock()
	select {
	case r.started <- struct{}{}:
	default:
	}
	select {
	case <-unblock:
		return meta, err
	case <-ctx.Done():
		return ch.Meta{}, ctx.Err()
	}
}

func (r *blockingMetaResolver) AwaitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for metadata resolve")
	}
}

func (r *blockingMetaResolver) Unblock() {
	select {
	case <-r.unblock:
	default:
		close(r.unblock)
	}
}

func (r *blockingMetaResolver) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
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
