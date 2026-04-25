package replica

import "github.com/WuKongIM/WuKongIM/pkg/channel"

type promotionReason string

const (
	promotionReasonInvalidISR            promotionReason = "invalid_isr"
	promotionReasonInsufficientQuorum    promotionReason = "insufficient_quorum"
	promotionReasonCandidateBelowHW      promotionReason = "candidate_below_hw"
	promotionReasonCandidateMissingState promotionReason = "candidate_missing_state"
)

// DurableReplicaView captures the durable state used to dry-run a promotion.
type DurableReplicaView struct {
	// EpochHistory is the local epoch lineage used to detect divergent peers.
	EpochHistory []channel.EpochPoint
	// LEO is the local durable log end offset.
	LEO uint64
	// HW is the locally known committed high watermark.
	HW uint64
	// CheckpointHW is the durable committed high watermark persisted on disk.
	CheckpointHW uint64
	// OffsetEpoch is the epoch that owns the local LEO.
	OffsetEpoch uint64
}

// PromotionReport describes whether a replica can safely take leadership.
type PromotionReport struct {
	// ProjectedSafeHW is the largest quorum-safe prefix the candidate can prove.
	ProjectedSafeHW uint64
	// ProjectedTruncateTo is the offset the candidate would keep after reconcile.
	ProjectedTruncateTo uint64
	// CommitReadyNow reports whether the candidate can accept appends immediately.
	CommitReadyNow bool
	// CanLead reports whether the candidate is safe to promote.
	CanLead bool
	// Reason explains why the candidate cannot lead when CanLead is false.
	Reason string
}
