package channelmigration

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ProbeClient reads a target replica's durable channel state for migration proof checks.
type ProbeClient interface {
	// ProbeChannel returns the target replica's current durable proof report.
	ProbeChannel(ctx context.Context, nodeID channel.NodeID, meta channel.Meta) (ProbeReport, error)
}

// ProbeReport describes one target replica's durable catch-up state.
type ProbeReport struct {
	// ChannelKey fences the report to one channel runtime.
	ChannelKey channel.ChannelKey
	// ChannelEpoch fences the report to the authoritative channel epoch.
	ChannelEpoch uint64
	// LeaderEpoch fences the report to the authoritative leader epoch.
	LeaderEpoch uint64
	// ReplicaID is the reporting replica node.
	ReplicaID channel.NodeID
	// OffsetEpoch is the epoch that owns LogEndOffset when EpochHistory is unavailable.
	OffsetEpoch uint64
	// LogStartOffset is the first local offset available without snapshot bootstrap.
	LogStartOffset uint64
	// LogEndOffset is the durable log end offset visible on the target.
	LogEndOffset uint64
	// CheckpointHW is the target's durable committed high watermark.
	CheckpointHW uint64
	// EpochHistory optionally lets the evaluator prove the epoch at CutoverHW exactly.
	EpochHistory []channel.EpochPoint
	// TruncateTo reports a pending divergence correction before the target can be promoted.
	TruncateTo *uint64
	// SnapshotRequired reports that the target cannot prove the cutover prefix without snapshot bootstrap.
	SnapshotRequired bool
}

// FinalTargetProofRequest asks the evaluator to verify a post-drain target proof.
type FinalTargetProofRequest struct {
	// Meta is the current authoritative channel metadata.
	Meta channel.Meta
	// TargetNode is the desired target replica being proven.
	TargetNode channel.NodeID
	// CutoverLEO is the drained leader LEO stored in the migration task.
	CutoverLEO uint64
	// CutoverHW is the drained leader committed prefix that the target must prove.
	CutoverHW uint64
	// CutoverOffsetEpoch is the epoch that owns CutoverHW on the drained leader.
	CutoverOffsetEpoch uint64
	// Target is the target replica's durable probe report.
	Target ProbeReport
}

// FinalTargetProof is the accepted post-drain target proof used before cutover.
type FinalTargetProof struct {
	// Ready is true only when the target has proven the full cutover prefix.
	Ready bool
	// TargetNode is the proven target replica.
	TargetNode channel.NodeID
	// ChannelEpoch is the authoritative channel epoch used for the proof.
	ChannelEpoch uint64
	// LeaderEpoch is the authoritative leader epoch used for the proof.
	LeaderEpoch uint64
	// CutoverHW is the committed prefix proven on the target.
	CutoverHW uint64
	// CutoverLEO is the drained leader LEO paired with CutoverHW.
	CutoverLEO uint64
	// TargetLEO is the target durable log end offset at proof time.
	TargetLEO uint64
	// TargetCheckpointHW is the target durable checkpoint at proof time.
	TargetCheckpointHW uint64
	// OffsetEpoch is the epoch proven compatible at CutoverHW.
	OffsetEpoch uint64
}

// ProofEvaluator evaluates channel migration proof reports without side effects.
type ProofEvaluator struct{}
