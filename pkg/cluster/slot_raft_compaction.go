package cluster

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// SlotRaftCompactionResult is a manager-facing result for one node-local Slot Raft compaction attempt.
type SlotRaftCompactionResult struct {
	// NodeID is the cluster node that handled the local Slot Raft compaction attempt.
	NodeID uint64
	// SlotID is the physical Slot whose local Raft log was compacted.
	SlotID uint32
	// AppliedIndex is the local applied index used as the compaction target.
	AppliedIndex uint64
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether the attempt created a new snapshot and compacted entries.
	Compacted bool
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string
}

// CompactSlotRaftLogOnNode triggers local Slot Raft compaction on one node.
func (c *Cluster) CompactSlotRaftLogOnNode(ctx context.Context, nodeID uint64, slotID uint32) (SlotRaftCompactionResult, error) {
	if c == nil {
		return SlotRaftCompactionResult{}, ErrNotStarted
	}
	if c.IsLocal(multiraft.NodeID(nodeID)) {
		return c.localCompactSlotRaftLog(ctx, nodeID, slotID)
	}
	return c.remoteCompactSlotRaftLog(ctx, nodeID, slotID)
}

func (c *Cluster) localCompactSlotRaftLog(ctx context.Context, nodeID uint64, slotID uint32) (SlotRaftCompactionResult, error) {
	if c == nil || c.runtime == nil {
		return SlotRaftCompactionResult{}, ErrNotStarted
	}
	result, err := c.runtime.CompactLog(ctx, multiraft.SlotID(slotID))
	if errors.Is(err, multiraft.ErrSlotNotFound) {
		return SlotRaftCompactionResult{}, ErrSlotNotFound
	}
	if err != nil {
		return SlotRaftCompactionResult{}, err
	}
	out := slotRaftCompactionResultFromRuntime(result)
	if nodeID != 0 {
		out.NodeID = nodeID
	}
	if slotID != 0 {
		out.SlotID = slotID
	}
	return out, nil
}

func (c *Cluster) remoteCompactSlotRaftLog(ctx context.Context, nodeID uint64, slotID uint32) (SlotRaftCompactionResult, error) {
	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:   managedSlotRPCCompact,
		SlotID: slotID,
	})
	if err != nil {
		return SlotRaftCompactionResult{}, err
	}
	respBody, err := c.RPCService(ctx, multiraft.NodeID(nodeID), multiraft.SlotID(slotID), rpcServiceManagedSlot, body)
	if err != nil {
		return SlotRaftCompactionResult{}, err
	}
	resp, err := decodeManagedSlotResponse(respBody)
	if err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if resp.Compaction == nil {
		return SlotRaftCompactionResult{}, ErrInvalidConfig
	}
	result := *resp.Compaction
	if result.NodeID == 0 {
		result.NodeID = nodeID
	}
	if result.SlotID == 0 {
		result.SlotID = slotID
	}
	return result, nil
}

func slotRaftCompactionResultFromRuntime(result multiraft.LogCompactionResult) SlotRaftCompactionResult {
	return SlotRaftCompactionResult{
		NodeID:              uint64(result.NodeID),
		SlotID:              uint32(result.SlotID),
		AppliedIndex:        result.AppliedIndex,
		BeforeSnapshotIndex: result.BeforeSnapshotIndex,
		AfterSnapshotIndex:  result.AfterSnapshotIndex,
		Compacted:           result.Compacted,
		SkippedReason:       result.SkippedReason,
	}
}
