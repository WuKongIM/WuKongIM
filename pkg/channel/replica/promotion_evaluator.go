package replica

import (
	"errors"
	"slices"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

// EvaluateLeaderPromotion dry-runs the reconcile safety rules for a candidate.
func EvaluateLeaderPromotion(meta channel.Meta, local DurableReplicaView, proofs []channel.ReplicaReconcileProof) (PromotionReport, error) {
	report := PromotionReport{}
	if local.HW > local.LEO || local.CheckpointHW > local.LEO || local.CheckpointHW > local.HW {
		return PromotionReport{}, channel.ErrCorruptState
	}
	if meta.MinISR <= 0 || meta.MinISR > len(meta.ISR) {
		report.Reason = string(promotionReasonInvalidISR)
		return report, nil
	}
	if !hasDurableReplicaState(local) {
		report.Reason = string(promotionReasonCandidateMissingState)
		return report, nil
	}

	matchOffsets := make(map[channel.NodeID]uint64, len(proofs)+1)
	if meta.Leader != 0 {
		matchOffsets[meta.Leader] = local.LEO
	}
	for _, proof := range proofs {
		if proof.ReplicaID == 0 || !slices.Contains(meta.ISR, proof.ReplicaID) {
			continue
		}
		if proof.CheckpointHW > proof.LogEndOffset {
			continue
		}
		matchOffset, err := reconcileProofMatchOffset(local.EpochHistory, local.LogStartOffset, local.HW, local.LEO, proof)
		if err != nil {
			if errors.Is(err, channel.ErrStaleMeta) || errors.Is(err, channel.ErrSnapshotRequired) {
				continue
			}
			return PromotionReport{}, err
		}
		if current, ok := matchOffsets[proof.ReplicaID]; ok && current >= matchOffset {
			continue
		}
		matchOffsets[proof.ReplicaID] = matchOffset
	}

	candidate, ok, err := reconcileQuorumCandidate(meta.ISR, matchOffsets, meta.MinISR, local.HW, local.LEO)
	if err != nil {
		return PromotionReport{}, err
	}
	if !ok {
		report.Reason = string(promotionReasonInsufficientQuorum)
		return report, nil
	}

	report.ProjectedSafeHW = candidate
	report.ProjectedTruncateTo = candidate
	report.CommitReadyNow = candidate == local.LEO && local.CheckpointHW >= candidate
	if candidate < local.HW {
		report.Reason = string(promotionReasonCandidateBelowHW)
		return report, nil
	}
	report.CanLead = true
	return report, nil
}

func hasDurableReplicaState(local DurableReplicaView) bool {
	return local.LEO > 0 ||
		local.HW > 0 ||
		local.CheckpointHW > 0 ||
		local.OffsetEpoch > 0 ||
		len(local.EpochHistory) > 0
}
