package replica

import (
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

	peerMatches := make(map[channel.NodeID]uint64, len(proofs))
	for _, proof := range proofs {
		if proof.ReplicaID == 0 || !slices.Contains(meta.ISR, proof.ReplicaID) {
			continue
		}
		if proof.CheckpointHW > proof.LogEndOffset {
			continue
		}
		matchOffset := promotionMatchOffset(local.EpochHistory, proof.LogEndOffset, proof.OffsetEpoch, local.LEO)
		if current, ok := peerMatches[proof.ReplicaID]; ok && current >= matchOffset {
			continue
		}
		peerMatches[proof.ReplicaID] = matchOffset
	}

	matches := make([]uint64, 0, len(meta.ISR))
	matches = append(matches, local.LEO)
	for _, matchOffset := range peerMatches {
		matches = append(matches, matchOffset)
	}
	if len(matches) < meta.MinISR {
		report.Reason = string(promotionReasonInsufficientQuorum)
		return report, nil
	}

	slices.Sort(matches)
	candidate := matches[len(matches)-meta.MinISR]
	if candidate > local.LEO {
		candidate = local.LEO
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

func promotionMatchOffset(history []channel.EpochPoint, logEndOffset, offsetEpoch, leaderLEO uint64) uint64 {
	if len(history) == 0 || offsetEpoch == 0 {
		if logEndOffset > leaderLEO {
			return leaderLEO
		}
		return logEndOffset
	}

	matchOffset := uint64(0)
	index := -1
	for i, point := range history {
		if point.Epoch <= offsetEpoch {
			index = i
			continue
		}
		break
	}
	if index >= 0 {
		matchOffset = leaderLEO
		if index+1 < len(history) {
			matchOffset = history[index+1].StartOffset
		}
	}
	if logEndOffset > matchOffset {
		return matchOffset
	}
	return logEndOffset
}
