package node

import (
	"context"
	"testing"

	channelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestChannelLeaderTransferRPCRedirectsToCurrentSlotLeader(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {3, 2}},
		map[uint64]uint64{1: 2},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)
	node3 := network.cluster(3)

	id := channel.ChannelID{ID: "transfer-redirect", Type: 2}
	leaderTransferer := &stubNodeChannelLeaderTransferer{
		result: channelmeta.LeaderTransferResult{
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
		Cluster:               node2,
		Presence:              presence.New(presence.Options{}),
		Online:                online.NewRegistry(),
		LocalNodeID:           2,
		ChannelLeaderTransfer: leaderTransferer,
	})
	New(Options{
		Cluster:     node3,
		Presence:    presence.New(presence.Options{}),
		Online:      online.NewRegistry(),
		LocalNodeID: 3,
	})

	client := NewClient(node1)
	got, err := client.TransferChannelLeader(context.Background(), channelmeta.LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: 9,
		ObservedLeaderEpoch:  7,
		TargetNodeID:         4,
	})

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(4), got.Meta.Leader)
	require.Equal(t, []channelmeta.LeaderTransferRequest{{
		ChannelID:            id,
		ObservedChannelEpoch: 9,
		ObservedLeaderEpoch:  7,
		TargetNodeID:         4,
	}}, leaderTransferer.calls)
}

func TestChannelLeaderTransferRPCMapsNoSafeCandidateStatus(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {2}},
		map[uint64]uint64{1: 2},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	New(Options{
		Cluster:               node2,
		Presence:              presence.New(presence.Options{}),
		Online:                online.NewRegistry(),
		LocalNodeID:           2,
		ChannelLeaderTransfer: &stubNodeChannelLeaderTransferer{err: channel.ErrNoSafeChannelLeader},
	})

	client := NewClient(node1)
	_, err := client.TransferChannelLeader(context.Background(), channelmeta.LeaderTransferRequest{
		ChannelID:            channel.ChannelID{ID: "transfer-none", Type: 2},
		ObservedChannelEpoch: 6,
		ObservedLeaderEpoch:  5,
		TargetNodeID:         2,
	})

	require.ErrorIs(t, err, channel.ErrNoSafeChannelLeader)
}

func TestChannelLeaderTransferRPCReturnsAuthoritativeMetaAfterTransfer(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {2}},
		map[uint64]uint64{1: 2},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	id := channel.ChannelID{ID: "transfer-authoritative", Type: 2}
	want := channelmeta.LeaderTransferResult{
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
		Cluster:               node2,
		Presence:              presence.New(presence.Options{}),
		Online:                online.NewRegistry(),
		LocalNodeID:           2,
		ChannelLeaderTransfer: &stubNodeChannelLeaderTransferer{result: want},
	})

	client := NewClient(node1)
	got, err := client.TransferChannelLeader(context.Background(), channelmeta.LeaderTransferRequest{
		ChannelID:            id,
		ObservedChannelEpoch: 11,
		ObservedLeaderEpoch:  8,
		TargetNodeID:         4,
	})

	require.NoError(t, err)
	require.Equal(t, want, got)
}

type stubNodeChannelLeaderTransferer struct {
	calls  []channelmeta.LeaderTransferRequest
	result channelmeta.LeaderTransferResult
	err    error
}

func (s *stubNodeChannelLeaderTransferer) TransferChannelLeaderAuthoritative(_ context.Context, req channelmeta.LeaderTransferRequest) (channelmeta.LeaderTransferResult, error) {
	s.calls = append(s.calls, req)
	return s.result, s.err
}
