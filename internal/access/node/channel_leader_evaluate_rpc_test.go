package node

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestChannelLeaderEvaluateCandidateRPCRejectsReplicaOutsideISR(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	evaluator := &stubNodeChannelLeaderEvaluator{}
	New(Options{
		Cluster:               node2,
		Presence:              presence.New(presence.Options{}),
		Online:                online.NewRegistry(),
		LocalNodeID:           2,
		ChannelLeaderEvaluate: evaluator,
	})

	client := NewClient(node1)
	_, err := client.EvaluateChannelLeaderCandidate(context.Background(), 2, ChannelLeaderEvaluateRequest{
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID:   "evaluate-outside-isr",
			ChannelType: 2,
			Replicas:    []uint64{2, 3, 4},
			ISR:         []uint64{3, 4},
			Leader:      3,
			MinISR:      2,
			Status:      uint8(channel.StatusActive),
		},
	})

	require.ErrorIs(t, err, channel.ErrInvalidMeta)
	require.Empty(t, evaluator.calls)
}

func TestChannelLeaderEvaluateCandidateRPCReturnsPromotionReport(t *testing.T) {
	network := newFakeClusterNetwork(
		map[uint64][]uint64{1: {1, 2}},
		map[uint64]uint64{1: 1},
	)
	node1 := network.cluster(1)
	node2 := network.cluster(2)

	want := ChannelLeaderPromotionReport{
		NodeID:              2,
		Exists:              true,
		ChannelEpoch:        9,
		LocalLEO:            12,
		LocalCheckpointHW:   9,
		LocalOffsetEpoch:    4,
		CommitReadyNow:      false,
		ProjectedSafeHW:     11,
		ProjectedTruncateTo: 11,
		CanLead:             true,
	}
	evaluator := &stubNodeChannelLeaderEvaluator{report: want}
	New(Options{
		Cluster:               node2,
		Presence:              presence.New(presence.Options{}),
		Online:                online.NewRegistry(),
		LocalNodeID:           2,
		ChannelLeaderEvaluate: evaluator,
	})

	client := NewClient(node1)
	got, err := client.EvaluateChannelLeaderCandidate(context.Background(), 2, ChannelLeaderEvaluateRequest{
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID:    "evaluate-report",
			ChannelType:  2,
			ChannelEpoch: 9,
			Replicas:     []uint64{2, 3},
			ISR:          []uint64{2, 3},
			Leader:       3,
			MinISR:       2,
			Status:       uint8(channel.StatusActive),
		},
	})

	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Len(t, evaluator.calls, 1)
}

func TestChannelLeaderEvaluateRPCNilAdapterReturnsErrorInsteadOfPanicking(t *testing.T) {
	body, err := json.Marshal(ChannelLeaderEvaluateRequest{
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID:   "evaluate-nil-adapter",
			ChannelType: 2,
			Replicas:    []uint64{2, 3},
			ISR:         []uint64{2, 3},
			Leader:      3,
			MinISR:      2,
			Status:      uint8(channel.StatusActive),
		},
	})
	require.NoError(t, err)

	var adapter *Adapter
	require.NotPanics(t, func() {
		_, err = adapter.handleChannelLeaderEvaluateRPC(context.Background(), body)
	})
	require.EqualError(t, err, "access/node: channel leader evaluator not configured")
}

type stubNodeChannelLeaderEvaluator struct {
	calls  []ChannelLeaderEvaluateRequest
	report ChannelLeaderPromotionReport
	err    error
}

func (s *stubNodeChannelLeaderEvaluator) EvaluateChannelLeaderCandidate(_ context.Context, req ChannelLeaderEvaluateRequest) (ChannelLeaderPromotionReport, error) {
	s.calls = append(s.calls, req)
	return s.report, s.err
}
