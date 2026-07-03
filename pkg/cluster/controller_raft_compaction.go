package cluster

import (
	"context"

	controllerraft "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// ControllerRaftCompactionResult is a manager-facing result for one node-local Controller Raft compaction attempt.
type ControllerRaftCompactionResult struct {
	// NodeID is the Controller Raft node that handled the attempt.
	NodeID uint64
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

// CompactControllerRaftLogOnNode triggers local Controller Raft compaction on one node.
func (c *Cluster) CompactControllerRaftLogOnNode(ctx context.Context, nodeID uint64) (ControllerRaftCompactionResult, error) {
	if c == nil {
		return ControllerRaftCompactionResult{}, ErrNotStarted
	}
	if c.IsLocal(multiraft.NodeID(nodeID)) {
		return c.localCompactControllerRaftLog(ctx, nodeID)
	}
	return c.remoteCompactControllerRaftLog(ctx, nodeID)
}

func (c *Cluster) localCompactControllerRaftLog(ctx context.Context, nodeID uint64) (ControllerRaftCompactionResult, error) {
	if c == nil || c.controllerHost == nil || c.controllerHost.service == nil {
		return ControllerRaftCompactionResult{}, ErrNotStarted
	}
	result, err := c.controllerHost.service.CompactLog(ctx)
	if err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	out := controllerRaftCompactionResultFromService(result)
	if nodeID != 0 {
		out.NodeID = nodeID
	}
	return out, nil
}

func (c *Cluster) remoteCompactControllerRaftLog(ctx context.Context, nodeID uint64) (ControllerRaftCompactionResult, error) {
	body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCControllerRaftCompact})
	if err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	respBody, err := c.controllerRPCService(ctx, multiraft.NodeID(nodeID), body)
	if err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	resp, err := decodeControllerResponse(controllerRPCControllerRaftCompact, respBody)
	if err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	if resp.ControllerRaftCompaction == nil {
		return ControllerRaftCompactionResult{}, ErrInvalidConfig
	}
	result := *resp.ControllerRaftCompaction
	if result.NodeID == 0 {
		result.NodeID = nodeID
	}
	return result, nil
}

func controllerRaftCompactionResultFromService(result controllerraft.LogCompactionResult) ControllerRaftCompactionResult {
	return ControllerRaftCompactionResult{
		NodeID:              result.NodeID,
		AppliedIndex:        result.AppliedIndex,
		BeforeSnapshotIndex: result.BeforeSnapshotIndex,
		AfterSnapshotIndex:  result.AfterSnapshotIndex,
		Compacted:           result.Compacted,
		SkippedReason:       result.SkippedReason,
	}
}
