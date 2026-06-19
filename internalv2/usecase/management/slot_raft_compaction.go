package management

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SlotRaftOperator exposes node-local Slot Raft compaction operations.
type SlotRaftOperator interface {
	// CompactSlotRaftLog forces one node's local Slot Raft log compaction.
	CompactSlotRaftLog(context.Context, uint64, uint32) (SlotRaftCompactionResult, error)
}

// ErrSlotRaftOperatorUnavailable reports that Slot Raft operations are not wired.
var ErrSlotRaftOperatorUnavailable = errors.New("internalv2/usecase/management: slot raft operator unavailable")

// SlotRaftCompactionResult describes one node-local Slot Raft compaction attempt.
type SlotRaftCompactionResult struct {
	// NodeID is the node that handled the attempt.
	NodeID uint64
	// SlotID is the physical Slot whose local Raft log was compacted.
	SlotID uint32
	// AppliedIndex is the node-local applied index used as the compaction target.
	AppliedIndex uint64
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether this attempt created a new snapshot and compacted entries.
	Compacted bool
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string
	// Error is the per-node failure message when present.
	Error string
}

// SlotRaftCompactNodeResult describes one Slot Raft compaction target result.
type SlotRaftCompactNodeResult struct {
	// NodeID is the node that handled the attempt.
	NodeID uint64
	// SlotID is the physical Slot whose local Raft log was compacted.
	SlotID uint32
	// Success reports whether the node accepted and completed the local attempt.
	Success bool
	// AppliedIndex is the node-local applied index used as the compaction target.
	AppliedIndex uint64
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether this attempt created a new snapshot and compacted entries.
	Compacted bool
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string
	// Error is the per-node failure message when Success is false.
	Error string
}

// SlotRaftCompactionSummary is returned after triggering Slot Raft compaction.
type SlotRaftCompactionSummary struct {
	// GeneratedAt records when the result was assembled.
	GeneratedAt time.Time
	// Total is the number of node/slot targets.
	Total int
	// Succeeded is the number of targets that completed the attempt.
	Succeeded int
	// Failed is the number of targets that returned an error.
	Failed int
	// Items contains per-target results ordered by request target order.
	Items []SlotRaftCompactNodeResult
}

// CompactSlotRaftLog forces one selected node's local Slot Raft log compaction.
func (a *App) CompactSlotRaftLog(ctx context.Context, nodeID uint64, slotID uint32) (SlotRaftCompactionSummary, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotRaftCompactionSummary{}, err
	}
	if nodeID == 0 || slotID == 0 {
		return SlotRaftCompactionSummary{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.slotRaft == nil {
		return SlotRaftCompactionSummary{}, ErrSlotRaftOperatorUnavailable
	}
	result, err := a.slotRaft.CompactSlotRaftLog(ctx, nodeID, slotID)
	item := slotRaftCompactNodeResult(nodeID, slotID, result, err)
	summary := SlotRaftCompactionSummary{
		GeneratedAt: a.now(),
		Total:       1,
		Items:       []SlotRaftCompactNodeResult{item},
	}
	if err != nil {
		summary.Failed = 1
	} else {
		summary.Succeeded = 1
	}
	return summary, nil
}

func slotRaftCompactNodeResult(nodeID uint64, slotID uint32, result SlotRaftCompactionResult, err error) SlotRaftCompactNodeResult {
	if result.NodeID != 0 {
		nodeID = result.NodeID
	}
	if result.SlotID != 0 {
		slotID = result.SlotID
	}
	item := SlotRaftCompactNodeResult{
		NodeID:              nodeID,
		SlotID:              slotID,
		Success:             err == nil,
		AppliedIndex:        result.AppliedIndex,
		BeforeSnapshotIndex: result.BeforeSnapshotIndex,
		AfterSnapshotIndex:  result.AfterSnapshotIndex,
		Compacted:           result.Compacted,
		SkippedReason:       result.SkippedReason,
		Error:               result.Error,
	}
	if err != nil && item.Error == "" {
		item.Error = err.Error()
	}
	return item
}
