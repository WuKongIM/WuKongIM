package node

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestChannelLeaderRepairRPCRedirectsToCurrentSlotLeader(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {3, 2}},
		map[uint64]uint64{1: 2},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)
	node3 := network.cluster(3)

	id := channel.ChannelID{ID: "repair-redirect", Type: 2}
	leaderRepairer := &stubNodeChannelLeaderRepairer{
		result: ChannelLeaderRepairResult{
			Meta: metadb.ChannelRuntimeMeta{
				ChannelID:    id.ID,
				ChannelType:  int64(id.Type),
				ChannelEpoch: 9,
				LeaderEpoch:  8,
				Replicas:     []uint64{2, 3, 4},
				ISR:          []uint64{2, 3, 4},
				Leader:       4,
				MinISR:       2,
				Status:       uint8(channel.StatusActive),
			},
			Changed: true,
		},
	}

	New(Options{
		Cluster:             node2,
		Presence:            presence.New(presence.Options{}),
		Online:              online.NewRegistry(),
		LocalNodeID:         2,
		ChannelLeaderRepair: leaderRepairer,
	})
	New(Options{
		Cluster:     node3,
		Presence:    presence.New(presence.Options{}),
		Online:      online.NewRegistry(),
		LocalNodeID: 3,
	})

	client := NewClient(node1)
	got, err := client.RepairChannelLeader(context.Background(), ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: 9,
		ObservedLeaderEpoch:  7,
		Reason:               "leader_dead",
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(4), got.Meta.Leader)
	require.Len(t, leaderRepairer.calls, 1)
}

func TestChannelLeaderRepairRPCMapsNoSafeCandidateStatus(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {2}},
		map[uint64]uint64{1: 2},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	New(Options{
		Cluster:             node2,
		Presence:            presence.New(presence.Options{}),
		Online:              online.NewRegistry(),
		LocalNodeID:         2,
		ChannelLeaderRepair: &stubNodeChannelLeaderRepairer{err: channel.ErrNoSafeChannelLeader},
	})

	client := NewClient(node1)
	_, err := client.RepairChannelLeader(context.Background(), ChannelLeaderRepairRequest{
		ChannelID:            channel.ChannelID{ID: "repair-none", Type: 2},
		ObservedChannelEpoch: 6,
		ObservedLeaderEpoch:  5,
		Reason:               "leader_dead",
	})

	require.ErrorIs(t, err, channel.ErrNoSafeChannelLeader)
}

func TestChannelLeaderRepairRPCReturnsAuthoritativeMetaAfterRepair(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {2}},
		map[uint64]uint64{1: 2},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	id := channel.ChannelID{ID: "repair-authoritative", Type: 2}
	want := ChannelLeaderRepairResult{
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID:    id.ID,
			ChannelType:  int64(id.Type),
			ChannelEpoch: 11,
			LeaderEpoch:  9,
			Replicas:     []uint64{2, 3, 4},
			ISR:          []uint64{2, 3, 4},
			Leader:       4,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
		},
		Changed: true,
	}

	New(Options{
		Cluster:             node2,
		Presence:            presence.New(presence.Options{}),
		Online:              online.NewRegistry(),
		LocalNodeID:         2,
		ChannelLeaderRepair: &stubNodeChannelLeaderRepairer{result: want},
	})

	client := NewClient(node1)
	got, err := client.RepairChannelLeader(context.Background(), ChannelLeaderRepairRequest{
		ChannelID:            id,
		ObservedChannelEpoch: 11,
		ObservedLeaderEpoch:  8,
		Reason:               "leader_dead",
	})

	require.NoError(t, err)
	require.Equal(t, want, got)
}

type stubNodeChannelLeaderRepairer struct {
	calls  []ChannelLeaderRepairRequest
	result ChannelLeaderRepairResult
	err    error
}

func (s *stubNodeChannelLeaderRepairer) RepairChannelLeaderAuthoritative(_ context.Context, req ChannelLeaderRepairRequest) (ChannelLeaderRepairResult, error) {
	s.calls = append(s.calls, req)
	return s.result, s.err
}
