package channelmigration

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/stretchr/testify/require"
)

func TestFinalTargetProofRejectsLagBelowCutoverHW(t *testing.T) {
	req := finalTargetProofTestRequest()
	req.CutoverHW = 10
	req.Target.CheckpointHW = 9
	req.Target.LogEndOffset = 10

	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(req)

	require.ErrorIs(t, err, channel.ErrNotReady)
	require.False(t, proof.Ready)
}

func TestFinalTargetProofRejectsLEOBelowCutoverLEO(t *testing.T) {
	req := finalTargetProofTestRequest()
	req.CutoverLEO = 12
	req.CutoverHW = 10
	req.Target.LogEndOffset = 11
	req.Target.CheckpointHW = 10

	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(req)

	require.ErrorIs(t, err, channel.ErrNotReady)
	require.False(t, proof.Ready)
}

func TestFinalTargetProofRejectsDivergentOffsetEpoch(t *testing.T) {
	req := finalTargetProofTestRequest()
	req.CutoverOffsetEpoch = 7
	req.Target.OffsetEpoch = 6

	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(req)

	require.ErrorIs(t, err, channel.ErrStaleMeta)
	require.False(t, proof.Ready)
}

func TestFinalTargetProofRejectsSnapshotRequired(t *testing.T) {
	req := finalTargetProofTestRequest()
	req.Target.SnapshotRequired = true

	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(req)

	require.ErrorIs(t, err, channel.ErrSnapshotRequired)
	require.False(t, proof.Ready)
}

func TestFinalTargetProofRejectsLogStartPastCutoverHW(t *testing.T) {
	req := finalTargetProofTestRequest()
	req.Target.LogStartOffset = req.CutoverHW + 1

	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(req)

	require.ErrorIs(t, err, channel.ErrSnapshotRequired)
	require.False(t, proof.Ready)
}

func TestFinalTargetProofRejectsPendingTruncate(t *testing.T) {
	req := finalTargetProofTestRequest()
	truncateTo := uint64(9)
	req.Target.TruncateTo = &truncateTo

	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(req)

	require.ErrorIs(t, err, channel.ErrNotReady)
	require.False(t, proof.Ready)
}

func TestFinalTargetProofRejectsCommitNotReady(t *testing.T) {
	req := finalTargetProofTestRequest()
	req.Target.CommitReady = false

	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(req)

	require.ErrorIs(t, err, channel.ErrNotReady)
	require.False(t, proof.Ready)
}

func TestFinalTargetProofAcceptsCheckpointAtCutoverHW(t *testing.T) {
	req := finalTargetProofTestRequest()

	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(req)

	require.NoError(t, err)
	require.True(t, proof.Ready)
	require.Equal(t, channel.NodeID(3), proof.TargetNode)
	require.Equal(t, uint64(7), proof.ChannelEpoch)
	require.Equal(t, uint64(4), proof.LeaderEpoch)
	require.Equal(t, uint64(10), proof.CutoverHW)
	require.Equal(t, uint64(12), proof.TargetLEO)
	require.Equal(t, uint64(10), proof.TargetCheckpointHW)
	require.Equal(t, uint64(7), proof.OffsetEpoch)
}

func TestFinalTargetProofUsesEpochHistoryAtCutoverHW(t *testing.T) {
	req := finalTargetProofTestRequest()
	req.CutoverHW = 8
	req.CutoverOffsetEpoch = 6
	req.Target.CheckpointHW = 10
	req.Target.OffsetEpoch = 7
	req.Target.EpochHistory = []channel.EpochPoint{
		{Epoch: 6, StartOffset: 0},
		{Epoch: 7, StartOffset: 9},
	}

	proof, err := (ProofEvaluator{}).EvaluateFinalTargetProof(req)

	require.NoError(t, err)
	require.True(t, proof.Ready)
	require.Equal(t, uint64(6), proof.OffsetEpoch)
}

func finalTargetProofTestRequest() FinalTargetProofRequest {
	meta := channel.Meta{
		Key:         "group-proof",
		ID:          channel.ChannelID{ID: "proof", Type: 2},
		Epoch:       7,
		LeaderEpoch: 4,
		Leader:      1,
		Replicas:    []channel.NodeID{1, 2, 3},
		ISR:         []channel.NodeID{1, 2, 3},
		MinISR:      2,
		Status:      channel.StatusActive,
	}
	return FinalTargetProofRequest{
		Meta:               meta,
		TargetNode:         3,
		CutoverLEO:         12,
		CutoverHW:          10,
		CutoverOffsetEpoch: 7,
		Target: ProbeReport{
			ChannelKey:   meta.Key,
			ChannelEpoch: meta.Epoch,
			LeaderEpoch:  meta.LeaderEpoch,
			ReplicaID:    3,
			OffsetEpoch:  7,
			LogEndOffset: 12,
			CheckpointHW: 10,
			CommitReady:  true,
		},
	}
}
