package runtime

import (
	"context"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
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
