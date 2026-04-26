package replica

import "github.com/WuKongIM/WuKongIM/pkg/channel"

// lineageDecision is the pure result of comparing a remote log cursor with local epoch lineage.
type lineageDecision struct {
	// matchOffset is the largest offset the local replica can safely trust for the remote peer.
	matchOffset uint64
	// truncateTo asks the remote peer to discard records above matchOffset when its cursor is divergent.
	truncateTo *uint64
	// err reports stale metadata, snapshot requirements, or impossible lineage state.
	err error
}

func offsetEpochForLEO(history []channel.EpochPoint, leo uint64) uint64 {
	if len(history) == 0 {
		return 0
	}

	var epoch uint64
	for _, point := range history {
		if point.StartOffset > leo {
			break
		}
		epoch = point.Epoch
	}
	return epoch
}

func decideLineage(history []channel.EpochPoint, logStartOffset, currentHW, leaderLEO, remoteOffset, offsetEpoch uint64) lineageDecision {
	if remoteOffset < logStartOffset {
		return lineageDecision{matchOffset: remoteOffset, err: channel.ErrSnapshotRequired}
	}

	matchOffset, err := safeLineageMatchOffset(history, currentHW, leaderLEO, remoteOffset, offsetEpoch)
	if matchOffset > leaderLEO {
		matchOffset = leaderLEO
	}
	if matchOffset < logStartOffset && remoteOffset >= logStartOffset {
		return lineageDecision{matchOffset: matchOffset, err: channel.ErrSnapshotRequired}
	}
	if matchOffset > leaderLEO {
		return lineageDecision{matchOffset: matchOffset, err: channel.ErrCorruptState}
	}

	var truncateTo *uint64
	if remoteOffset > matchOffset {
		truncate := matchOffset
		truncateTo = &truncate
	}
	return lineageDecision{matchOffset: matchOffset, truncateTo: truncateTo, err: err}
}

func matchOffsetForProof(history []channel.EpochPoint, currentHW, leaderLEO, logEndOffset, offsetEpoch uint64) (uint64, error) {
	return reconcileProofMatchOffset(history, 0, currentHW, leaderLEO, channel.ReplicaReconcileProof{
		LogEndOffset: logEndOffset,
		OffsetEpoch:  offsetEpoch,
	})
}

func safeLineageMatchOffset(history []channel.EpochPoint, currentHW, leaderLEO, remoteOffset, offsetEpoch uint64) (uint64, error) {
	if len(history) == 0 {
		return minUint64(remoteOffset, leaderLEO), nil
	}
	if offsetEpoch == 0 {
		return minUint64(remoteOffset, currentHW, leaderLEO), nil
	}

	latestEpoch := history[len(history)-1].Epoch
	if offsetEpoch > latestEpoch {
		return minUint64(remoteOffset, leaderLEO), channel.ErrStaleMeta
	}

	for i, point := range history {
		if point.Epoch != offsetEpoch {
			continue
		}
		upper := leaderLEO
		if i+1 < len(history) {
			upper = history[i+1].StartOffset
		}
		return minUint64(remoteOffset, upper, leaderLEO), nil
	}

	return minUint64(remoteOffset, currentHW, leaderLEO), nil
}

func minUint64(first uint64, rest ...uint64) uint64 {
	min := first
	for _, value := range rest {
		if value < min {
			min = value
		}
	}
	return min
}
