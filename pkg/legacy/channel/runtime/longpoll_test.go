package runtime

import (
	"context"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/stretchr/testify/require"
)

func TestRuntimeLongPollServeLanePollTimesOutWhenLaneHasNoReadyItems(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(2701, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)

	resp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      testFollowerLaneFor(meta.Key, 4),
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Millisecond,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch},
		},
	})
	require.NoError(t, err)
	require.Equal(t, LanePollStatusOK, resp.Status)
	require.True(t, resp.TimedOut)
	require.NotZero(t, resp.SessionID)
	require.NotZero(t, resp.SessionEpoch)
}

func TestRuntimeLongPollHWOnlyReadySkipsFetchWhenFollowerAtLEO(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(27010, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)
	replica := env.factory.replicas[0]
	laneID := testFollowerLaneFor(meta.Key, 4)

	openResp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      laneID,
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Millisecond,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, ChannelGeneration: 1},
		},
	})
	require.NoError(t, err)
	require.True(t, openResp.TimedOut)

	replica.mu.Lock()
	replica.state.LEO = 5
	replica.state.HW = 5
	replica.state.CheckpointHW = 5
	replica.state.OffsetEpoch = meta.Epoch
	replica.fetchResult = core.ReplicaFetchResult{HW: 5}
	replica.fetchCalls = 0
	replica.mu.Unlock()
	session, ok := env.runtime.leaderLanes.Session(PeerLaneKey{Peer: 2, LaneID: laneID})
	require.True(t, ok)
	session.MarkHWOnlyReady(meta.Key, meta.Epoch)

	resp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:    2,
		LaneID:       laneID,
		LaneCount:    4,
		SessionID:    openResp.SessionID,
		SessionEpoch: openResp.SessionEpoch,
		Op:           LanePollOpPoll,
		MaxWait:      time.Millisecond,
		MaxBytes:     64 * 1024,
		MaxChannels:  64,
		CursorDelta: []LaneCursorDelta{{
			ChannelKey:        meta.Key,
			ChannelEpoch:      meta.Epoch,
			ChannelGeneration: 1,
			MatchOffset:       5,
			OffsetEpoch:       meta.Epoch,
		}},
	})
	require.NoError(t, err)
	require.False(t, resp.TimedOut)
	require.Len(t, resp.Items, 1)
	require.Equal(t, LanePollItemFlagHWOnly, resp.Items[0].Flags)
	require.Equal(t, uint64(5), resp.Items[0].LeaderHW)
	require.Equal(t, 0, replica.fetchCalls)
}

func TestLongPollOpenActivatesTrackedMissingChannels(t *testing.T) {
	activator := &recordingActivator{}
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Activator = activator
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(27011, 4, 1, []core.NodeID{1, 2})

	activator.fn = func(context.Context, core.ChannelKey, ActivationSource) (core.Meta, error) {
		require.NoError(t, env.runtime.EnsureChannel(meta))
		replica := env.factory.replicas[len(env.factory.replicas)-1]
		replica.mu.Lock()
		replica.state.LEO = 1
		replica.fetchResult = core.ReplicaFetchResult{
			HW: 1,
			Records: []core.Record{
				{Payload: []byte("cold"), SizeBytes: len("cold")},
			},
		}
		replica.mu.Unlock()
		return meta, nil
	}

	resp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      testFollowerLaneFor(meta.Key, 4),
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Millisecond,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, activator.callCount())
	require.Equal(t, meta.Key, activator.calls[0].key)
	require.Equal(t, ActivationSourceLaneOpen, activator.calls[0].source)
	require.Equal(t, LanePollStatusOK, resp.Status)
	require.False(t, resp.TimedOut)
	require.Len(t, resp.Items, 1)
	require.Equal(t, meta.Key, resp.Items[0].ChannelKey)
	require.Equal(t, LanePollItemFlagData, resp.Items[0].Flags)
}

func TestRuntimeLongPollServeLanePollReturnsRetentionResetImmediately(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Hour
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(27012, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)
	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 10
	replica.fetchResult = core.ReplicaFetchResult{
		HW: 10,
		RetentionReset: &core.RetentionReset{
			RetentionThroughSeq:   5,
			RetainedThroughOffset: 5,
			MinAvailableSeq:       6,
		},
	}
	replica.mu.Unlock()

	resp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      testFollowerLaneFor(meta.Key, 4),
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Hour,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch},
		},
		CursorDelta: []LaneCursorDelta{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, MatchOffset: 1, OffsetEpoch: meta.Epoch},
		},
	})

	require.NoError(t, err)
	require.Equal(t, LanePollStatusOK, resp.Status)
	require.False(t, resp.TimedOut)
	require.Len(t, resp.Items, 1)
	require.Equal(t, LanePollItemFlagReset, resp.Items[0].Flags)
	require.Empty(t, resp.Items[0].Records)
	require.NotNil(t, resp.Items[0].RetentionReset)
	require.Equal(t, uint64(5), resp.Items[0].RetentionReset.RetainedThroughOffset)
}

func TestRuntimeLongPollRetentionResetCursorAtFloorFetchesAvailableRecords(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(27013, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)
	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 6
	replica.state.HW = 6
	replica.state.RetentionThroughSeq = 5
	replica.state.MinAvailableSeq = 6
	replica.fetchResult = core.ReplicaFetchResult{
		HW: 6,
		RetentionReset: &core.RetentionReset{
			RetentionThroughSeq:   5,
			RetainedThroughOffset: 5,
			MinAvailableSeq:       6,
		},
	}
	replica.mu.Unlock()

	laneID := testFollowerLaneFor(meta.Key, 4)
	openResp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      laneID,
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Millisecond,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, ChannelGeneration: 1},
		},
		CursorDelta: []LaneCursorDelta{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, ChannelGeneration: 1, MatchOffset: 1, OffsetEpoch: meta.Epoch},
		},
	})
	require.NoError(t, err)
	require.Equal(t, LanePollStatusOK, openResp.Status)
	require.False(t, openResp.TimedOut)
	require.Len(t, openResp.Items, 1)
	require.Equal(t, LanePollItemFlagReset, openResp.Items[0].Flags)

	replica.mu.Lock()
	replica.fetchResult = core.ReplicaFetchResult{
		HW: 6,
		Records: []core.Record{
			{Payload: []byte("after-retention"), SizeBytes: len("after-retention")},
		},
	}
	replica.mu.Unlock()

	resp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:    2,
		LaneID:       laneID,
		LaneCount:    4,
		SessionID:    openResp.SessionID,
		SessionEpoch: openResp.SessionEpoch,
		Op:           LanePollOpPoll,
		MaxWait:      time.Millisecond,
		MaxBytes:     64 * 1024,
		MaxChannels:  64,
		CursorDelta: []LaneCursorDelta{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, ChannelGeneration: 1, MatchOffset: 5, OffsetEpoch: meta.Epoch},
		},
	})
	require.NoError(t, err)
	require.Equal(t, LanePollStatusOK, resp.Status)
	require.False(t, resp.TimedOut)
	require.Len(t, resp.Items, 1)
	require.Equal(t, LanePollItemFlagData, resp.Items[0].Flags)
	require.Len(t, resp.Items[0].Records, 1)
	require.Equal(t, []byte("after-retention"), resp.Items[0].Records[0].Payload)
}

func TestApplyMetaUpdatesLaneTargetsForLeaderChannels(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(2702, 4, 1, []core.NodeID{1, 2, 3})
	mustEnsureLocal(t, env.runtime, meta)

	ch, ok := env.runtime.lookupChannel(meta.Key)
	require.True(t, ok)
	require.Len(t, ch.replicationTargetsSnapshot(), 2)

	require.NoError(t, env.runtime.ApplyMeta(testMetaLocal(2702, 5, 3, []core.NodeID{1, 2, 3})))
	require.Empty(t, ch.replicationTargetsSnapshot())

	require.NoError(t, env.runtime.ApplyMeta(testMetaLocal(2702, 6, 1, []core.NodeID{1, 2, 3})))
	require.Len(t, ch.replicationTargetsSnapshot(), 2)
}

func TestApplyMetaPreservesLeaderLaneCursorOnWriteFenceOnlyChange(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(27021, 4, 1, []core.NodeID{1, 2})
	meta.WriteFence = core.WriteFence{
		Token:   "migration-task",
		Version: 3,
		Reason:  core.WriteFenceReasonMigration,
		Until:   time.Unix(1_700_000_000, 0).UTC().Add(time.Minute),
	}
	mustEnsureLocal(t, env.runtime, meta)

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 1
	replica.state.HW = 1
	replica.state.OffsetEpoch = meta.Epoch
	replica.fetchResult = core.ReplicaFetchResult{HW: 1}
	replica.mu.Unlock()

	laneID := testFollowerLaneFor(meta.Key, 4)
	_, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      laneID,
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Millisecond,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, ChannelGeneration: 1},
		},
		CursorDelta: []LaneCursorDelta{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, ChannelGeneration: 1, MatchOffset: 1, OffsetEpoch: meta.Epoch},
		},
	})
	require.NoError(t, err)

	session, ok := env.runtime.leaderLanes.Session(PeerLaneKey{Peer: 2, LaneID: laneID})
	require.True(t, ok)
	before, ok := session.Cursor(meta.Key)
	require.True(t, ok)
	require.Equal(t, uint64(1), before.MatchOffset)

	cleared := meta
	cleared.WriteFence = core.WriteFence{Version: 4}
	require.NoError(t, env.runtime.ApplyMeta(cleared))

	after, ok := session.Cursor(meta.Key)
	require.True(t, ok)
	require.Equal(t, before, after)
}

func TestApplyMetaResetsLeaderLaneCursorOnRetentionChange(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(27022, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 3
	replica.state.HW = 3
	replica.state.OffsetEpoch = meta.Epoch
	replica.fetchResult = core.ReplicaFetchResult{HW: 3}
	replica.mu.Unlock()

	laneID := testFollowerLaneFor(meta.Key, 4)
	_, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      laneID,
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Millisecond,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, ChannelGeneration: 1},
		},
		CursorDelta: []LaneCursorDelta{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, ChannelGeneration: 1, MatchOffset: 3, OffsetEpoch: meta.Epoch},
		},
	})
	require.NoError(t, err)

	session, ok := env.runtime.leaderLanes.Session(PeerLaneKey{Peer: 2, LaneID: laneID})
	require.True(t, ok)
	_, ok = session.Cursor(meta.Key)
	require.True(t, ok)

	retained := meta
	retained.RetentionThroughSeq = 2
	require.NoError(t, env.runtime.ApplyMeta(retained))

	_, ok = session.Cursor(meta.Key)
	require.False(t, ok)
}

func TestRuntimeLongPollLeaderAppendWakesParkedLanePoll(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Second
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(2703, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)

	handle, ok := env.runtime.Channel(meta.Key)
	require.True(t, ok)

	laneID := testFollowerLaneFor(meta.Key, 4)
	respCh := make(chan struct {
		resp LanePollResponseEnvelope
		err  error
	}, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go func() {
		resp, err := env.runtime.ServeLanePoll(ctx, LanePollRequestEnvelope{
			ReplicaID:   2,
			LaneID:      laneID,
			LaneCount:   4,
			Op:          LanePollOpOpen,
			MaxWait:     time.Second,
			MaxBytes:    64 * 1024,
			MaxChannels: 64,
			FullMembership: []LaneMembership{
				{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch},
			},
		})
		respCh <- struct {
			resp LanePollResponseEnvelope
			err  error
		}{resp: resp, err: err}
	}()

	require.Eventually(t, func() bool {
		session, ok := env.runtime.leaderLanes.Session(PeerLaneKey{Peer: 2, LaneID: laneID})
		if !ok {
			return false
		}
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.parked != nil
	}, time.Second, time.Millisecond)

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 1
	replica.fetchResult = core.ReplicaFetchResult{
		HW: 1,
		Records: []core.Record{
			{Payload: []byte("wake"), SizeBytes: len("wake")},
		},
	}
	replica.mu.Unlock()

	_, err := handle.Append(context.Background(), []core.Record{
		{Payload: []byte("wake"), SizeBytes: len("wake")},
	})
	require.NoError(t, err)

	select {
	case got := <-respCh:
		require.NoError(t, got.err)
		require.False(t, got.resp.TimedOut)
		require.Len(t, got.resp.Items, 1)
		require.Equal(t, meta.Key, got.resp.Items[0].ChannelKey)
		require.Equal(t, LanePollItemFlagData, got.resp.Items[0].Flags)
	case <-time.After(time.Second):
		t.Fatal("expected parked long poll to wake after leader append")
	}
}

func TestRuntimeLongPollLeaderAppendDebouncesParkedLanePollForBatching(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Second
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.LongPollDataNotifyDelay = 120 * time.Millisecond
	})
	firstKey := testChannelKeyForLane(t, 1, 4, "debounce-first")
	secondKey := testChannelKeyForLane(t, 1, 4, "debounce-second")
	firstMeta := core.Meta{Key: firstKey, Epoch: 4, Leader: 1, Replicas: []core.NodeID{1, 2}, ISR: []core.NodeID{1, 2}, MinISR: 1}
	secondMeta := core.Meta{Key: secondKey, Epoch: 4, Leader: 1, Replicas: []core.NodeID{1, 2}, ISR: []core.NodeID{1, 2}, MinISR: 1}
	mustEnsureLocal(t, env.runtime, firstMeta)
	mustEnsureLocal(t, env.runtime, secondMeta)

	laneID := testFollowerLaneFor(firstMeta.Key, 4)
	require.Equal(t, laneID, testFollowerLaneFor(secondMeta.Key, 4))
	respCh := make(chan struct {
		resp LanePollResponseEnvelope
		err  error
	}, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go func() {
		resp, err := env.runtime.ServeLanePoll(ctx, LanePollRequestEnvelope{
			ReplicaID:   2,
			LaneID:      laneID,
			LaneCount:   4,
			Op:          LanePollOpOpen,
			MaxWait:     time.Second,
			MaxBytes:    64 * 1024,
			MaxChannels: 64,
			FullMembership: []LaneMembership{
				{ChannelKey: firstMeta.Key, ChannelEpoch: firstMeta.Epoch, ChannelGeneration: 1},
				{ChannelKey: secondMeta.Key, ChannelEpoch: secondMeta.Epoch, ChannelGeneration: 2},
			},
		})
		respCh <- struct {
			resp LanePollResponseEnvelope
			err  error
		}{resp: resp, err: err}
	}()

	require.Eventually(t, func() bool {
		session, ok := env.runtime.leaderLanes.Session(PeerLaneKey{Peer: 2, LaneID: laneID})
		if !ok {
			return false
		}
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.parked != nil
	}, time.Second, time.Millisecond)

	firstReplica := env.factory.replicas[0]
	firstReplica.mu.Lock()
	firstReplica.state.LEO = 1
	firstReplica.state.HW = 1
	firstReplica.state.OffsetEpoch = firstMeta.Epoch
	firstReplica.fetchResult = core.ReplicaFetchResult{
		HW:      1,
		Records: []core.Record{{Payload: []byte("first"), SizeBytes: len("first")}},
	}
	firstReplica.mu.Unlock()
	env.runtime.onChannelAppend(firstMeta.Key)

	select {
	case got := <-respCh:
		require.NoError(t, got.err)
		t.Fatalf("data wake returned before debounce window: %+v", got.resp)
	case <-time.After(40 * time.Millisecond):
	}

	secondReplica := env.factory.replicas[1]
	secondReplica.mu.Lock()
	secondReplica.state.LEO = 1
	secondReplica.state.HW = 1
	secondReplica.state.OffsetEpoch = secondMeta.Epoch
	secondReplica.fetchResult = core.ReplicaFetchResult{
		HW:      1,
		Records: []core.Record{{Payload: []byte("second"), SizeBytes: len("second")}},
	}
	secondReplica.mu.Unlock()
	env.runtime.onChannelAppend(secondMeta.Key)

	got := <-respCh
	require.NoError(t, got.err)
	require.False(t, got.resp.TimedOut)
	require.Len(t, got.resp.Items, 2)
	require.ElementsMatch(t, []core.ChannelKey{firstMeta.Key, secondMeta.Key}, []core.ChannelKey{
		got.resp.Items[0].ChannelKey,
		got.resp.Items[1].ChannelKey,
	})
}

func TestRuntimeLongPollLeaderCommitAdvanceWakesParkedLanePollWithHWOnly(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = 150 * time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(27031, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 1
	replica.state.HW = 0
	replica.fetchResult = core.ReplicaFetchResult{
		HW: 0,
		Records: []core.Record{
			{Payload: []byte("wake"), SizeBytes: len("wake")},
		},
	}
	replica.mu.Unlock()

	laneID := testFollowerLaneFor(meta.Key, 4)
	openResp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      laneID,
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Millisecond,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch},
		},
	})
	require.NoError(t, err)
	require.Equal(t, LanePollStatusOK, openResp.Status)
	require.Len(t, openResp.Items, 1)
	require.Equal(t, LanePollItemFlagData, openResp.Items[0].Flags)
	require.Equal(t, uint64(0), openResp.Items[0].LeaderHW)

	session, ok := env.runtime.leaderLanes.Session(PeerLaneKey{Peer: 2, LaneID: laneID})
	require.True(t, ok)
	session.mu.Lock()
	session.cursor[meta.Key] = LaneCursorDelta{
		ChannelKey:   meta.Key,
		ChannelEpoch: meta.Epoch,
		MatchOffset:  1,
		OffsetEpoch:  meta.Epoch,
	}
	session.mu.Unlock()

	replica.mu.Lock()
	replica.fetchResult = core.ReplicaFetchResult{HW: 1}
	replica.mu.Unlock()

	respCh := make(chan struct {
		resp LanePollResponseEnvelope
		err  error
	}, 1)
	go func() {
		resp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
			ReplicaID:    2,
			LaneID:       laneID,
			LaneCount:    4,
			SessionID:    openResp.SessionID,
			SessionEpoch: openResp.SessionEpoch,
			Op:           LanePollOpPoll,
			MaxWait:      150 * time.Millisecond,
			MaxBytes:     64 * 1024,
			MaxChannels:  64,
		})
		respCh <- struct {
			resp LanePollResponseEnvelope
			err  error
		}{resp: resp, err: err}
	}()

	require.Eventually(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.parked != nil
	}, time.Second, time.Millisecond)

	require.NoError(t, replica.ApplyProgressAck(context.Background(), core.ReplicaProgressAckRequest{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		ReplicaID:   2,
		MatchOffset: 1,
	}))

	select {
	case got := <-respCh:
		require.NoError(t, got.err)
		require.False(t, got.resp.TimedOut)
		require.Len(t, got.resp.Items, 1)
		require.Equal(t, meta.Key, got.resp.Items[0].ChannelKey)
		require.Equal(t, LanePollItemFlagHWOnly, got.resp.Items[0].Flags)
		require.Equal(t, uint64(1), got.resp.Items[0].LeaderHW)
		require.Empty(t, got.resp.Items[0].Records)
	case <-time.After(time.Second):
		t.Fatal("expected parked long poll to wake after leader commit advance")
	}
}

func TestRuntimeLongPollCommitHWOnlyWakeDebouncesUntilDataCanPiggyback(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = 250 * time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.LongPollHWOnlyNotifyDelay = 120 * time.Millisecond
	})
	meta := testMetaLocal(27032, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 1
	replica.state.HW = 0
	replica.state.OffsetEpoch = meta.Epoch
	replica.fetchResult = core.ReplicaFetchResult{
		HW: 0,
		Records: []core.Record{
			{Payload: []byte("first"), SizeBytes: len("first")},
		},
	}
	replica.mu.Unlock()

	laneID := testFollowerLaneFor(meta.Key, 4)
	openResp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      laneID,
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Millisecond,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch, ChannelGeneration: 1},
		},
	})
	require.NoError(t, err)
	require.Len(t, openResp.Items, 1)
	require.Equal(t, LanePollItemFlagData, openResp.Items[0].Flags)

	respCh := make(chan struct {
		resp LanePollResponseEnvelope
		err  error
	}, 1)
	go func() {
		resp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
			ReplicaID:    2,
			LaneID:       laneID,
			LaneCount:    4,
			SessionID:    openResp.SessionID,
			SessionEpoch: openResp.SessionEpoch,
			Op:           LanePollOpPoll,
			MaxWait:      250 * time.Millisecond,
			MaxBytes:     64 * 1024,
			MaxChannels:  64,
			CursorDelta: []LaneCursorDelta{{
				ChannelKey:        meta.Key,
				ChannelEpoch:      meta.Epoch,
				ChannelGeneration: 1,
				MatchOffset:       1,
				OffsetEpoch:       meta.Epoch,
			}},
		})
		respCh <- struct {
			resp LanePollResponseEnvelope
			err  error
		}{resp: resp, err: err}
	}()

	require.Eventually(t, func() bool {
		session, ok := env.runtime.leaderLanes.Session(PeerLaneKey{Peer: 2, LaneID: laneID})
		if !ok {
			return false
		}
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.parked != nil
	}, time.Second, time.Millisecond)

	replica.mu.Lock()
	replica.fetchResult = core.ReplicaFetchResult{HW: 1}
	replica.mu.Unlock()
	require.NoError(t, replica.ApplyProgressAck(context.Background(), core.ReplicaProgressAckRequest{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		ReplicaID:   2,
		MatchOffset: 1,
	}))

	select {
	case got := <-respCh:
		require.NoError(t, got.err)
		t.Fatalf("commit-only HW wake returned before debounce: %+v", got.resp)
	case <-time.After(40 * time.Millisecond):
	}

	replica.mu.Lock()
	replica.state.LEO = 2
	replica.state.HW = 1
	replica.fetchResult = core.ReplicaFetchResult{
		HW: 1,
		Records: []core.Record{
			{Payload: []byte("second"), SizeBytes: len("second")},
		},
	}
	replica.mu.Unlock()
	env.runtime.onChannelAppend(meta.Key)

	got := <-respCh
	require.NoError(t, got.err)
	require.False(t, got.resp.TimedOut)
	require.Len(t, got.resp.Items, 1)
	require.Equal(t, LanePollItemFlagData, got.resp.Items[0].Flags)
	require.Equal(t, uint64(1), got.resp.Items[0].LeaderHW)

	time.Sleep(120 * time.Millisecond)
	idleResp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:    2,
		LaneID:       laneID,
		LaneCount:    4,
		SessionID:    got.resp.SessionID,
		SessionEpoch: got.resp.SessionEpoch,
		Op:           LanePollOpPoll,
		MaxWait:      time.Millisecond,
		MaxBytes:     64 * 1024,
		MaxChannels:  64,
		CursorDelta: []LaneCursorDelta{{
			ChannelKey:        meta.Key,
			ChannelEpoch:      meta.Epoch,
			ChannelGeneration: 1,
			MatchOffset:       2,
			OffsetEpoch:       meta.Epoch,
		}},
	})
	require.NoError(t, err)
	require.True(t, idleResp.TimedOut)
}

func TestRuntimeLongPollInitialOpenReturnsAlreadyAppendedData(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(2704, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)

	handle, ok := env.runtime.Channel(meta.Key)
	require.True(t, ok)

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 1
	replica.fetchResult = core.ReplicaFetchResult{
		HW: 1,
		Records: []core.Record{
			{Payload: []byte("first"), SizeBytes: len("first")},
		},
	}
	replica.mu.Unlock()

	_, err := handle.Append(context.Background(), []core.Record{
		{Payload: []byte("first"), SizeBytes: len("first")},
	})
	require.NoError(t, err)

	resp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:   2,
		LaneID:      testFollowerLaneFor(meta.Key, 4),
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWait:     time.Millisecond,
		MaxBytes:    64 * 1024,
		MaxChannels: 64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch},
		},
	})
	require.NoError(t, err)
	require.Equal(t, LanePollStatusOK, resp.Status)
	require.False(t, resp.TimedOut)
	require.Len(t, resp.Items, 1)
	require.Equal(t, meta.Key, resp.Items[0].ChannelKey)
	require.Equal(t, LanePollItemFlagData, resp.Items[0].Flags)
	require.Len(t, resp.Items[0].Records, 1)
}

func TestRuntimeLongPollReopenReturnsAlreadyAppendedData(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(2705, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)

	handle, ok := env.runtime.Channel(meta.Key)
	require.True(t, ok)

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 1
	replica.fetchResult = core.ReplicaFetchResult{
		HW: 1,
		Records: []core.Record{
			{Payload: []byte("reopen"), SizeBytes: len("reopen")},
		},
	}
	replica.mu.Unlock()

	_, err := handle.Append(context.Background(), []core.Record{
		{Payload: []byte("reopen"), SizeBytes: len("reopen")},
	})
	require.NoError(t, err)

	resp, err := env.runtime.ServeLanePoll(context.Background(), LanePollRequestEnvelope{
		ReplicaID:    2,
		LaneID:       testFollowerLaneFor(meta.Key, 4),
		LaneCount:    4,
		SessionID:    701,
		SessionEpoch: 1,
		Op:           LanePollOpOpen,
		MaxWait:      time.Millisecond,
		MaxBytes:     64 * 1024,
		MaxChannels:  64,
		FullMembership: []LaneMembership{
			{ChannelKey: meta.Key, ChannelEpoch: meta.Epoch},
		},
	})
	require.NoError(t, err)
	require.Equal(t, LanePollStatusOK, resp.Status)
	require.False(t, resp.TimedOut)
	require.Len(t, resp.Items, 1)
	require.Equal(t, meta.Key, resp.Items[0].ChannelKey)
	require.Equal(t, LanePollItemFlagData, resp.Items[0].Flags)
	require.Len(t, resp.Items[0].Records, 1)
}
