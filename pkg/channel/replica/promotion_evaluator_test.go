package replica

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestPromotionEvaluatorUsesQuorumSafePrefixInsteadOfMaxLEO(t *testing.T) {
	meta := promotionTestMeta([]channel.NodeID{2, 3, 4}, 2, 11)
	local := DurableReplicaView{
		EpochHistory: []channel.EpochPoint{{Epoch: 11, StartOffset: 0}},
		LEO:          10,
		HW:           5,
		CheckpointHW: 5,
		OffsetEpoch:  11,
	}

	report, err := EvaluateLeaderPromotion(meta, local, []channel.ReplicaReconcileProof{
		{ReplicaID: 3, OffsetEpoch: 11, LogEndOffset: 8, CheckpointHW: 8},
	})

	require.NoError(t, err)
	require.True(t, report.CanLead)
	require.Equal(t, uint64(8), report.ProjectedSafeHW)
	require.Equal(t, uint64(8), report.ProjectedTruncateTo)
	require.False(t, report.CommitReadyNow)
}

func TestPromotionEvaluatorNeverProjectsBeyondLocalLEO(t *testing.T) {
	meta := promotionTestMeta([]channel.NodeID{2, 3, 4}, 2, 7)
	local := DurableReplicaView{
		EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
		LEO:          9,
		HW:           6,
		CheckpointHW: 6,
		OffsetEpoch:  7,
	}

	report, err := EvaluateLeaderPromotion(meta, local, []channel.ReplicaReconcileProof{
		{ReplicaID: 3, OffsetEpoch: 7, LogEndOffset: 12, CheckpointHW: 12},
		{ReplicaID: 4, OffsetEpoch: 7, LogEndOffset: 11, CheckpointHW: 11},
	})

	require.NoError(t, err)
	require.True(t, report.CanLead)
	require.Equal(t, uint64(9), report.ProjectedSafeHW)
	require.Equal(t, uint64(9), report.ProjectedTruncateTo)
}

func TestPromotionEvaluatorReturnsNoCandidateWhenCandidateFallsBelowHW(t *testing.T) {
	meta := promotionTestMeta([]channel.NodeID{2, 3, 4}, 3, 2)
	local := DurableReplicaView{
		EpochHistory: []channel.EpochPoint{
			{Epoch: 1, StartOffset: 0},
			{Epoch: 2, StartOffset: 6},
		},
		LEO:          9,
		HW:           6,
		CheckpointHW: 6,
		OffsetEpoch:  2,
	}

	report, err := EvaluateLeaderPromotion(meta, local, []channel.ReplicaReconcileProof{
		{ReplicaID: 3, OffsetEpoch: 1, LogEndOffset: 4, CheckpointHW: 4},
		{ReplicaID: 4, OffsetEpoch: 1, LogEndOffset: 4, CheckpointHW: 4},
	})

	require.NoError(t, err)
	require.False(t, report.CanLead)
	require.Equal(t, string(promotionReasonCandidateBelowHW), report.Reason)
}

func TestPromotionEvaluatorMarksCommitReadyWhenDurablePrefixAlreadyVisible(t *testing.T) {
	meta := promotionTestMeta([]channel.NodeID{2, 3}, 2, 5)
	local := DurableReplicaView{
		EpochHistory: []channel.EpochPoint{{Epoch: 5, StartOffset: 0}},
		LEO:          7,
		HW:           7,
		CheckpointHW: 7,
		OffsetEpoch:  5,
	}

	report, err := EvaluateLeaderPromotion(meta, local, []channel.ReplicaReconcileProof{
		{ReplicaID: 3, OffsetEpoch: 5, LogEndOffset: 7, CheckpointHW: 7},
	})

	require.NoError(t, err)
	require.True(t, report.CanLead)
	require.True(t, report.CommitReadyNow)
	require.Equal(t, uint64(7), report.ProjectedSafeHW)
	require.Equal(t, uint64(7), report.ProjectedTruncateTo)
}

func TestPromotionEvaluatorRejectsCandidateWithoutDurableState(t *testing.T) {
	meta := promotionTestMeta([]channel.NodeID{2}, 1, 5)
	local := DurableReplicaView{}

	report, err := EvaluateLeaderPromotion(meta, local, nil)

	require.NoError(t, err)
	require.False(t, report.CanLead)
	require.Equal(t, string(promotionReasonCandidateMissingState), report.Reason)
}

func TestPromotionEvaluatorRequiresActualProofsToReachMinISR(t *testing.T) {
	meta := promotionTestMeta([]channel.NodeID{2, 3, 4}, 3, 8)
	local := DurableReplicaView{
		EpochHistory: []channel.EpochPoint{{Epoch: 8, StartOffset: 0}},
		LEO:          9,
		HW:           7,
		CheckpointHW: 7,
		OffsetEpoch:  8,
	}

	report, err := EvaluateLeaderPromotion(meta, local, []channel.ReplicaReconcileProof{
		{ReplicaID: 3, OffsetEpoch: 8, LogEndOffset: 9, CheckpointHW: 9},
	})

	require.NoError(t, err)
	require.False(t, report.CanLead)
	require.Equal(t, string(promotionReasonInsufficientQuorum), report.Reason)
}

func TestPromotionEvaluatorAllowsMinISR1WithLocalDurableStateOnly(t *testing.T) {
	meta := promotionTestMeta([]channel.NodeID{2}, 1, 3)
	local := DurableReplicaView{
		EpochHistory: []channel.EpochPoint{{Epoch: 3, StartOffset: 0}},
		LEO:          0,
		HW:           0,
		CheckpointHW: 0,
		OffsetEpoch:  3,
	}

	report, err := EvaluateLeaderPromotion(meta, local, nil)

	require.NoError(t, err)
	require.True(t, report.CanLead)
	require.True(t, report.CommitReadyNow)
	require.Equal(t, uint64(0), report.ProjectedSafeHW)
	require.Equal(t, uint64(0), report.ProjectedTruncateTo)
}

func promotionTestMeta(isr []channel.NodeID, minISR int, epoch uint64) channel.Meta {
	return channel.Meta{
		Key:         channel.ChannelKey("group-promotion"),
		ID:          channel.ChannelID{ID: "group-promotion", Type: 2},
		Epoch:       epoch,
		LeaderEpoch: 3,
		Leader:      2,
		Replicas:    append([]channel.NodeID(nil), isr...),
		ISR:         append([]channel.NodeID(nil), isr...),
		MinISR:      minISR,
		Status:      channel.StatusActive,
	}
}
