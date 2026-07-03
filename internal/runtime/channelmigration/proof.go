package channelmigration

import "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"

// EvaluateFinalTargetProof verifies that a post-drain target can preserve the
// committed cutover prefix before metadata cutover is allowed.
func (ProofEvaluator) EvaluateFinalTargetProof(req FinalTargetProofRequest) (FinalTargetProof, error) {
	if req.Meta.Key == "" || req.TargetNode == 0 || req.CutoverHW > req.CutoverLEO {
		return FinalTargetProof{}, channel.ErrInvalidArgument
	}
	if !containsNode(req.Meta.Replicas, req.TargetNode) {
		return FinalTargetProof{}, channel.ErrInvalidMeta
	}

	target := req.Target
	if target.SnapshotRequired {
		return FinalTargetProof{}, channel.ErrSnapshotRequired
	}
	if target.TruncateTo != nil {
		return FinalTargetProof{}, channel.ErrNotReady
	}
	if err := validateTargetReportFence(req.Meta, req.TargetNode, target); err != nil {
		return FinalTargetProof{}, err
	}
	if target.CheckpointHW > target.LogEndOffset {
		return FinalTargetProof{}, channel.ErrCorruptState
	}
	if target.LogStartOffset > req.CutoverHW {
		return FinalTargetProof{}, channel.ErrSnapshotRequired
	}
	if target.LogEndOffset < req.CutoverLEO || target.CheckpointHW < req.CutoverHW {
		return FinalTargetProof{}, channel.ErrNotReady
	}
	if !target.CommitReady {
		return FinalTargetProof{}, channel.ErrNotReady
	}

	offsetEpoch, err := targetOffsetEpochAt(target, req.CutoverHW)
	if err != nil {
		return FinalTargetProof{}, err
	}
	expectedOffsetEpoch := req.CutoverOffsetEpoch
	if expectedOffsetEpoch == 0 {
		expectedOffsetEpoch = req.Meta.Epoch
	}
	if offsetEpoch != expectedOffsetEpoch {
		return FinalTargetProof{}, channel.ErrStaleMeta
	}

	return FinalTargetProof{
		Ready:              true,
		TargetNode:         req.TargetNode,
		ChannelEpoch:       req.Meta.Epoch,
		LeaderEpoch:        req.Meta.LeaderEpoch,
		CutoverHW:          req.CutoverHW,
		CutoverLEO:         req.CutoverLEO,
		TargetLEO:          target.LogEndOffset,
		TargetCheckpointHW: target.CheckpointHW,
		OffsetEpoch:        offsetEpoch,
	}, nil
}

func validateTargetReportFence(meta channel.Meta, targetNode channel.NodeID, report ProbeReport) error {
	if report.ChannelKey != meta.Key ||
		report.ChannelEpoch != meta.Epoch ||
		report.LeaderEpoch != meta.LeaderEpoch ||
		report.ReplicaID != targetNode {
		return channel.ErrStaleMeta
	}
	return nil
}

func targetOffsetEpochAt(report ProbeReport, offset uint64) (uint64, error) {
	if len(report.EpochHistory) == 0 {
		if report.OffsetEpoch == 0 {
			return 0, channel.ErrStaleMeta
		}
		return report.OffsetEpoch, nil
	}

	var (
		epoch uint64
		found bool
	)
	for _, point := range report.EpochHistory {
		if point.StartOffset > offset {
			break
		}
		epoch = point.Epoch
		found = true
	}
	if !found {
		return 0, channel.ErrSnapshotRequired
	}
	return epoch, nil
}

func containsNode(ids []channel.NodeID, target channel.NodeID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
