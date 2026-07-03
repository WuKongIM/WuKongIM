package management

import (
	"context"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
)

// SlotRaftCompactionNodeResult describes one node-local Slot Raft compaction result.
type SlotRaftCompactionNodeResult struct {
	// NodeID is the cluster node that handled the local Slot Raft attempt.
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

// CompactSlotRaftLogResponse is the manager-facing one-slot compaction result.
type CompactSlotRaftLogResponse struct {
	// GeneratedAt records when the manager assembled this response.
	GeneratedAt time.Time
	// Total is the number of node/slot targets.
	Total int
	// Succeeded is the number of targets that completed the attempt.
	Succeeded int
	// Failed is the number of targets that returned an error.
	Failed int
	// Items contains per-target results ordered by request target order.
	Items []SlotRaftCompactionNodeResult
}

// CompactSlotRaftLog triggers node-local Slot Raft log compaction on one selected node and Slot.
func (a *App) CompactSlotRaftLog(ctx context.Context, nodeID uint64, slotID uint32) (CompactSlotRaftLogResponse, error) {
	if a == nil {
		return CompactSlotRaftLogResponse{}, nil
	}
	resp := CompactSlotRaftLogResponse{
		GeneratedAt: a.now(),
		Total:       1,
		Items:       make([]SlotRaftCompactionNodeResult, 0, 1),
	}
	item := SlotRaftCompactionNodeResult{NodeID: nodeID, SlotID: slotID}
	if a.cluster == nil {
		item.Success = false
		item.Error = "cluster not configured"
		resp.Failed = 1
		resp.Items = append(resp.Items, item)
		return resp, nil
	}
	result, err := a.cluster.CompactSlotRaftLogOnNode(ctx, nodeID, slotID)
	item = slotRaftCompactionNodeResult(nodeID, slotID, result)
	if err != nil {
		item.Success = false
		item.Error = err.Error()
		resp.Failed = 1
	} else {
		item.Success = true
		resp.Succeeded = 1
	}
	resp.Items = append(resp.Items, item)
	return resp, nil
}

func slotRaftCompactionNodeResult(nodeID uint64, slotID uint32, result raftcluster.SlotRaftCompactionResult) SlotRaftCompactionNodeResult {
	if result.NodeID != 0 {
		nodeID = result.NodeID
	}
	if result.SlotID != 0 {
		slotID = result.SlotID
	}
	return SlotRaftCompactionNodeResult{
		NodeID:              nodeID,
		SlotID:              slotID,
		AppliedIndex:        result.AppliedIndex,
		BeforeSnapshotIndex: result.BeforeSnapshotIndex,
		AfterSnapshotIndex:  result.AfterSnapshotIndex,
		Compacted:           result.Compacted,
		SkippedReason:       result.SkippedReason,
	}
}
