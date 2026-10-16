package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

// Failure inventory: cold reads activate a runtime; LEO leaks uncommitted data;
// stale placement appears inactive; recovering HW appears committed; malformed
// probe coverage fabricates absence; optional sequence reads still open storage.
func TestObserveLocalChannelsKeepsColdRuntimeMissingAndUsesCheckpoint(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "observe-cold", Type: 2}
	factory := channelstore.NewMemoryFactory()
	log, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = log.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 1}, {ID: 2}}})
	require.NoError(t, err)
	require.NoError(t, log.StoreCheckpoint(ctx, ch.Checkpoint{HW: 1}))
	require.NoError(t, log.Close())
	meta := observationTestMeta(id)
	runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}, probe: ch.RuntimeProbeResult{Missing: []ch.ChannelID{id}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		got, err := svc.ObserveLocalChannels(ctx, []RuntimeObservationTarget{observationTestTarget(meta)}, true)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.True(t, got[0].Known)
		require.False(t, got[0].Active)
		require.NotNil(t, got[0].MaxMessageSeq)
		require.Equal(t, uint64(1), *got[0].MaxMessageSeq)
		require.Zero(t, runtime.applyCalls)
	}
}

func TestObserveLocalChannelsPreservesUnknownAndRecoveredHW(t *testing.T) {
	id := ch.ChannelID{ID: "observe-hot", Type: 2}
	meta := observationTestMeta(id)
	for _, tc := range []struct {
		name                    string
		probe                   ch.RuntimeProbeResult
		mutate                  func(*RuntimeObservationTarget)
		known, active, sequence bool
	}{
		{name: "hot", probe: ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 7, LEO: 9}}}, known: true, active: true, sequence: true},
		{name: "recovering", probe: ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 7, LEO: 9, RecoveryRequired: true}}}, known: true, active: true},
		{name: "stale-runtime-route", probe: ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 5, RouteGeneration: 6, Role: ch.RoleLeader, Status: ch.StatusActive}}}},
		{name: "wrong-role", probe: ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Role: ch.RoleFollower, Status: ch.StatusActive}}}},
		{name: "stale-target", probe: ch.RuntimeProbeResult{Missing: []ch.ChannelID{id}}, mutate: func(target *RuntimeObservationTarget) { target.RouteGeneration++ }},
		{name: "incomplete-probe", probe: ch.RuntimeProbeResult{}},
		{name: "contradictory-probe", probe: ch.RuntimeProbeResult{Missing: []ch.ChannelID{id}, Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Role: ch.RoleLeader, Status: ch.StatusActive}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}, probe: tc.probe}
			factory := channelstore.NewMemoryFactory()
			log, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, err)
			records := make([]ch.Record, 9)
			for i := range records {
				records[i].ID = uint64(i + 1)
			}
			_, err = log.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: records})
			require.NoError(t, err)
			require.NoError(t, log.Close())
			svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory})
			require.NoError(t, err)
			target := observationTestTarget(meta)
			if tc.mutate != nil {
				tc.mutate(&target)
			}
			got, err := svc.ObserveLocalChannels(context.Background(), []RuntimeObservationTarget{target}, true)
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, tc.known, got[0].Known)
			require.Equal(t, tc.active, got[0].Active)
			if tc.sequence {
				require.NotNil(t, got[0].MaxMessageSeq)
				require.Equal(t, uint64(7), *got[0].MaxMessageSeq)
			} else {
				require.Nil(t, got[0].MaxMessageSeq)
			}
			require.Zero(t, runtime.applyCalls)
		})
	}
}

// SyncOnce is valid command content, so observation preserves the existing
// manager tail semantics instead of treating the flag as a native control.
func TestObserveLocalChannelsPreservesSyncOnceTail(t *testing.T) {
	for _, count := range []int{3, 600} {
		id := ch.ChannelID{ID: "internal-tail", Type: 2}
		meta := observationTestMeta(id)
		factory := channelstore.NewMemoryFactory()
		log, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
		require.NoError(t, err)
		records := make([]ch.Record, count)
		for i := range records {
			records[i] = ch.Record{ID: uint64(i + 1), SyncOnce: i > 0}
		}
		_, err = log.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: records})
		require.NoError(t, err)
		require.NoError(t, log.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: uint64(count)}))
		require.NoError(t, log.Close())
		runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}, probe: ch.RuntimeProbeResult{Missing: []ch.ChannelID{id}}}
		svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory})
		require.NoError(t, err)
		got, err := svc.ObserveLocalChannels(context.Background(), []RuntimeObservationTarget{observationTestTarget(meta)}, true)
		require.NoError(t, err)
		require.True(t, got[0].Known)
		require.NotNil(t, got[0].MaxMessageSeq)
		require.Equal(t, uint64(count), *got[0].MaxMessageSeq)
		require.Zero(t, runtime.applyCalls)
	}
}

func TestObserveLocalChannelsWithoutSequenceNeedsNoStore(t *testing.T) {
	id := ch.ChannelID{ID: "observe-only", Type: 2}
	meta := observationTestMeta(id)
	runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}, probe: ch.RuntimeProbeResult{Missing: []ch.ChannelID{id}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta})})
	require.NoError(t, err)
	got, err := svc.ObserveLocalChannels(context.Background(), []RuntimeObservationTarget{observationTestTarget(meta)}, false)
	require.NoError(t, err)
	require.True(t, got[0].Known)
	require.False(t, got[0].Active)
	require.Nil(t, got[0].MaxMessageSeq)
}

func observationTestMeta(id ch.ChannelID) ch.Meta {
	return ch.Meta{ID: id, Epoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
}

func TestObserveLocalChannelsHonorsAuthoritativeRetentionAheadOfLocalStore(t *testing.T) {
	id := ch.ChannelID{ID: "retained-tail", Type: 2}
	meta := observationTestMeta(id)
	meta.RetentionThroughSeq = 2
	factory := channelstore.NewMemoryFactory()
	log, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = log.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 1}, {ID: 2}}})
	require.NoError(t, err)
	require.NoError(t, log.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 2}))
	require.NoError(t, log.Close())
	runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}, probe: ch.RuntimeProbeResult{Missing: []ch.ChannelID{id}}}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory})
	require.NoError(t, err)
	got, err := svc.ObserveLocalChannels(context.Background(), []RuntimeObservationTarget{observationTestTarget(meta)}, true)
	require.NoError(t, err)
	require.NotNil(t, got[0].MaxMessageSeq)
	require.Zero(t, *got[0].MaxMessageSeq)
}

func observationTestTarget(meta ch.Meta) RuntimeObservationTarget {
	return RuntimeObservationTarget{ChannelID: meta.ID, LeaderID: meta.Leader, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, RouteGeneration: meta.RouteGeneration}
}
