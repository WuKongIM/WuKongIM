package channelmeta

import (
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// LeaderRepairRequest describes a channel leader repair request independent of RPC transport DTOs.
type LeaderRepairRequest struct {
	// ChannelID identifies the channel whose authoritative leader should be repaired.
	ChannelID channel.ChannelID
	// ObservedChannelEpoch carries the caller's last observed channel epoch.
	ObservedChannelEpoch uint64
	// ObservedLeaderEpoch carries the caller's last observed leader epoch.
	ObservedLeaderEpoch uint64
	// Reason records why the caller asked for leader repair.
	Reason string
}

// LeaderRepairResult returns authoritative runtime metadata after a repair attempt.
type LeaderRepairResult struct {
	// Meta is the authoritative runtime metadata after the repair attempt.
	Meta metadb.ChannelRuntimeMeta
	// Changed reports whether repair persisted a changed authoritative record.
	Changed bool
}

// LeaderTransferRequest describes an explicit safe channel leader transfer request.
type LeaderTransferRequest struct {
	// ChannelID identifies the channel whose leader should move.
	ChannelID channel.ChannelID
	// ObservedChannelEpoch carries the caller's last observed channel epoch.
	ObservedChannelEpoch uint64
	// ObservedLeaderEpoch carries the caller's last observed leader epoch.
	ObservedLeaderEpoch uint64
	// TargetNodeID is the requested new channel leader.
	TargetNodeID uint64
}

// LeaderTransferResult returns authoritative runtime metadata after transfer validation.
type LeaderTransferResult struct {
	// Meta is the authoritative runtime metadata after transfer or validation.
	Meta metadb.ChannelRuntimeMeta
	// Changed reports whether transfer persisted a changed authoritative record.
	Changed bool
}

// LeaderEvaluateRequest asks a replica to evaluate local leader promotion safety.
type LeaderEvaluateRequest struct {
	// Meta is the authoritative runtime metadata used for the dry-run.
	Meta metadb.ChannelRuntimeMeta
}

// LeaderPromotionReport describes one replica's channel leader promotion evaluation.
type LeaderPromotionReport struct {
	// NodeID identifies the evaluated replica node.
	NodeID uint64
	// Exists reports whether local durable state exists on this node.
	Exists bool
	// ChannelEpoch echoes the evaluated channel epoch.
	ChannelEpoch uint64
	// LocalLEO is the local durable log end offset.
	LocalLEO uint64
	// LocalCheckpointHW is the local durable checkpoint high watermark.
	LocalCheckpointHW uint64
	// LocalOffsetEpoch is the epoch that owns LocalLEO.
	LocalOffsetEpoch uint64
	// CommitReadyNow reports whether the replica can accept appends immediately.
	CommitReadyNow bool
	// ProjectedSafeHW is the largest quorum-safe prefix the replica can prove.
	ProjectedSafeHW uint64
	// ProjectedTruncateTo is the offset the replica would keep after reconcile.
	ProjectedTruncateTo uint64
	// CanLead reports whether the replica is safe to promote.
	CanLead bool
	// Reason explains why the replica cannot safely lead when CanLead is false.
	Reason string
}
