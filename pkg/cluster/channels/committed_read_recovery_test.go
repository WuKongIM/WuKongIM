package channels

import (
	"context"
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestCommittedReadRecoversColdLeader(t *testing.T) {
	for _, forwarded := range []bool{false, true} {
		name := "local"
		if forwarded {
			name = "forwarded"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			id := ch.ChannelID{ID: "cold-history", Type: 2}
			factory := channelstore.NewMemoryFactory()
			log, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, err)
			_, err = log.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: []ch.Record{
				{ID: 11, Payload: []byte("first")}, {ID: 12, Payload: []byte("tail")},
			}})
			require.NoError(t, err)
			require.NoError(t, log.StoreCheckpoint(ctx, ch.Checkpoint{HW: 1}))
			require.NoError(t, log.Close())
			meta := ch.Meta{ID: id, Epoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Leader: 1,
				Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
			runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{},
				probe: ch.RuntimeProbeResult{Missing: []ch.ChannelID{id}},
				probeAfterApply: &ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{
					ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 5, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 2, LEO: 2,
				}}},
			}
			svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory})
			require.NoError(t, err)
			read := committedReadContract(id)
			var results []CommittedReadResult
			if forwarded {
				response, callErr := svc.handleForwardCommittedReads(ctx, CommittedReadsRequest{Items: []CommittedReadRequest{{
					CommittedRead: read, ExpectedLeader: 1, ExpectedChannelEpoch: 3, ExpectedLeaderEpoch: 5, ExpectedMinISR: 2,
				}}})
				err, results = callErr, response.Items
			} else {
				results, err = svc.ReadCommittedBatch(ctx, []CommittedRead{read})
			}
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.NoError(t, results[0].Err)
			require.Len(t, results[0].Read.Messages, 2, "a cold checkpoint must not hide an acknowledged tail")
			require.Equal(t, uint64(12), results[0].Read.Messages[1].MessageID)
			require.Equal(t, 1, runtime.applyCalls)
			require.Equal(t, meta.RouteGeneration, runtime.lastApplied.RouteGeneration)
		})
	}
}

func TestCommittedReadColdLeaderFailsClosedWithoutRecovery(t *testing.T) {
	for _, missingMeta := range []bool{false, true} {
		name := "recovery-failed"
		if missingMeta {
			name = "missing-authority"
		}
		t.Run(name, func(t *testing.T) {
			id := ch.ChannelID{ID: "cold-empty-history", Type: 2}
			meta := ch.Meta{ID: id, Epoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Leader: 1,
				Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
			metas := []ch.Meta{meta}
			if missingMeta {
				metas = nil
			}
			runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{applyErr: ch.ErrNotReady}, probe: ch.RuntimeProbeResult{Missing: []ch.ChannelID{id}}}
			svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource(metas), Store: channelstore.NewMemoryFactory()})
			require.NoError(t, err)
			response, err := svc.handleForwardCommittedReads(context.Background(), CommittedReadsRequest{Items: []CommittedReadRequest{{
				CommittedRead: committedReadContract(id), ExpectedLeader: 1, ExpectedChannelEpoch: 3, ExpectedLeaderEpoch: 5, ExpectedMinISR: 2,
			}}})
			require.NoError(t, err)
			require.Len(t, response.Items, 1)
			require.ErrorIs(t, response.Items[0].Err, ch.ErrNotReady)
			require.Empty(t, response.Items[0].Read.Messages)
			if missingMeta {
				require.Zero(t, runtime.applyCalls)
			}
		})
	}
}

func TestCommittedReadDoesNotUseRecoveringLeaderHW(t *testing.T) {
	id := ch.ChannelID{ID: "pending-history", Type: 2}
	meta := ch.Meta{ID: id, Epoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Leader: 1,
		Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{applyErr: context.DeadlineExceeded},
		probe: ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{
			ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 5, Role: ch.RoleLeader, Status: ch.StatusActive,
			HW: 1, LEO: 2, RecoveryRequired: true,
		}}},
	}
	svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: channelstore.NewMemoryFactory()})
	require.NoError(t, err)
	results, err := svc.ReadCommittedBatch(context.Background(), []CommittedRead{committedReadContract(id)})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.ErrorIs(t, results[0].Err, context.DeadlineExceeded)
	require.Empty(t, results[0].Read.Messages)
	require.Equal(t, 1, runtime.applyCalls, "join the pending installation instead of treating loaded metadata as recovered")
}

func TestCommittedReadNeverExposesUncommittedTail(t *testing.T) {
	for _, hw := range []uint64{0, 1} {
		for _, reverse := range []bool{false, true} {
			ctx := context.Background()
			id := ch.ChannelID{ID: "uncommitted-history", Type: 2}
			factory := channelstore.NewMemoryFactory()
			log, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, err)
			_, err = log.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 1}, {ID: 2}}})
			require.NoError(t, err)
			require.NoError(t, log.Close())
			meta := ch.Meta{ID: id, Epoch: 3, LeaderEpoch: 5, Leader: 1,
				Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
			runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}, probe: ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{
				ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 5, Role: ch.RoleLeader, Status: ch.StatusActive, HW: hw, LEO: 2,
			}}}}
			svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory})
			require.NoError(t, err)
			read := committedReadContract(id)
			read.Request.Reverse = reverse
			if reverse {
				read.Request.FromSeq = 0
			}
			results, err := svc.ReadCommittedBatch(ctx, []CommittedRead{read})
			require.NoError(t, err)
			require.NoError(t, results[0].Err)
			require.Len(t, results[0].Read.Messages, int(hw), "HW=%d reverse=%t", hw, reverse)
			require.Zero(t, runtime.applyCalls, "a recovered hot Leader uses its live frontier without activation")
		}
	}
}

// A reactor warmed from an older Slot snapshot must be refreshed from the
// authoritative read metadata before it can prove the current leader frontier.
func TestCommittedReadRefreshesOlderRuntimeMetadata(t *testing.T) {
	for _, role := range []ch.Role{ch.RoleFollower, ch.RoleLeader} {
		for _, forwarded := range []bool{false, true} {
			t.Run(fmt.Sprintf("role_%d_forwarded_%t", role, forwarded), func(t *testing.T) {
				ctx := context.Background()
				id := ch.ChannelID{ID: "older-runtime-history", Type: 2}
				factory := channelstore.NewMemoryFactory()
				log, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
				require.NoError(t, err)
				_, err = log.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 71, Payload: []byte("committed")}}})
				require.NoError(t, err)
				require.NoError(t, log.StoreCheckpoint(ctx, ch.Checkpoint{HW: 1}))
				require.NoError(t, log.Close())
				meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
				runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}, probe: ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 4, Role: role, Status: ch.StatusActive, HW: 1, LEO: 1}}}, probeAfterApply: &ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: 3, LeaderEpoch: 5, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 1, LEO: 1}}}}
				svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory})
				require.NoError(t, err)
				var results []CommittedReadResult
				if forwarded {
					response, callErr := svc.handleForwardCommittedReads(ctx, CommittedReadsRequest{Items: []CommittedReadRequest{{CommittedRead: committedReadContract(id), ExpectedLeader: 1, ExpectedChannelEpoch: 3, ExpectedLeaderEpoch: 5, ExpectedMinISR: 2}}})
					err, results = callErr, response.Items
				} else {
					results, err = svc.ReadCommittedBatch(ctx, []CommittedRead{committedReadContract(id)})
				}
				require.NoError(t, err)
				require.Len(t, results, 1)
				require.NoError(t, results[0].Err)
				require.Len(t, results[0].Read.Messages, 1)
				require.Equal(t, uint64(71), results[0].Read.Messages[0].MessageID)
				require.Equal(t, 1, runtime.applyCalls)
				require.Equal(t, meta, runtime.lastApplied)
			})
		}
	}
}

func TestCommittedReadDoesNotReplaceCurrentOrNewerRuntimeAuthority(t *testing.T) {
	for _, tc := range []struct {
		name               string
		epoch, leaderEpoch uint64
		role               ch.Role
		want               error
	}{
		{"current_follower", 3, 5, ch.RoleFollower, ch.ErrNotLeader},
		{"newer_leader_term", 3, 6, ch.RoleLeader, ch.ErrStaleMeta},
		{"newer_channel_epoch", 4, 1, ch.RoleLeader, ch.ErrStaleMeta},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := ch.ChannelID{ID: "protected-runtime-authority", Type: 2}
			meta := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
			runtime := &runtimeHWProbeRuntime{fakeRuntime: &fakeRuntime{}, probe: ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: tc.epoch, LeaderEpoch: tc.leaderEpoch, Role: tc.role, Status: ch.StatusActive, HW: 99, LEO: 99}}}}
			svc, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: channelstore.NewMemoryFactory()})
			require.NoError(t, err)
			results, err := svc.ReadCommittedBatch(context.Background(), []CommittedRead{committedReadContract(id)})
			require.NoError(t, err)
			require.ErrorIs(t, results[0].Err, tc.want)
			require.Empty(t, results[0].Read.Messages)
			require.Zero(t, runtime.applyCalls)
		})
	}
}
