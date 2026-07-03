package management

import (
	"context"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
)

// ControllerRaftCompactionNodeResult describes one Controller voter node compaction result.
type ControllerRaftCompactionNodeResult struct {
	// NodeID is the Controller voter node that handled the attempt.
	NodeID uint64
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

// CompactControllerRaftLogsResponse is the manager-facing fan-out compaction result.
type CompactControllerRaftLogsResponse struct {
	// GeneratedAt records when the manager assembled this response.
	GeneratedAt time.Time
	// Total is the number of Controller voter nodes targeted.
	Total int
	// Succeeded is the number of nodes that completed the attempt.
	Succeeded int
	// Failed is the number of nodes that returned an error.
	Failed int
	// Items contains per-node results ordered by node id.
	Items []ControllerRaftCompactionNodeResult
}

// CompactControllerRaftLogs triggers node-local Controller Raft log compaction on all Controller voters.
func (a *App) CompactControllerRaftLogs(ctx context.Context) (CompactControllerRaftLogsResponse, error) {
	if a == nil {
		return CompactControllerRaftLogsResponse{}, nil
	}
	resp := CompactControllerRaftLogsResponse{
		GeneratedAt: a.now(),
		Total:       len(a.controllerPeerIDList),
		Items:       make([]ControllerRaftCompactionNodeResult, 0, len(a.controllerPeerIDList)),
	}
	if a.cluster == nil {
		return resp, nil
	}
	for _, nodeID := range a.controllerPeerIDList {
		result, err := a.cluster.CompactControllerRaftLogOnNode(ctx, nodeID)
		item := controllerRaftCompactionNodeResult(nodeID, result)
		if err != nil {
			item.Success = false
			item.Error = err.Error()
			resp.Failed++
		} else {
			item.Success = true
			resp.Succeeded++
		}
		resp.Items = append(resp.Items, item)
	}
	return resp, nil
}

// CompactControllerRaftLog triggers node-local Controller Raft log compaction on one selected node.
func (a *App) CompactControllerRaftLog(ctx context.Context, nodeID uint64) (CompactControllerRaftLogsResponse, error) {
	if a == nil {
		return CompactControllerRaftLogsResponse{}, nil
	}
	resp := CompactControllerRaftLogsResponse{
		GeneratedAt: a.now(),
		Total:       1,
		Items:       make([]ControllerRaftCompactionNodeResult, 0, 1),
	}
	item := ControllerRaftCompactionNodeResult{NodeID: nodeID}
	if a.cluster == nil {
		item.Success = false
		item.Error = "cluster not configured"
		resp.Failed = 1
		resp.Items = append(resp.Items, item)
		return resp, nil
	}
	result, err := a.cluster.CompactControllerRaftLogOnNode(ctx, nodeID)
	item = controllerRaftCompactionNodeResult(nodeID, result)
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

func controllerRaftCompactionNodeResult(nodeID uint64, result raftcluster.ControllerRaftCompactionResult) ControllerRaftCompactionNodeResult {
	if result.NodeID != 0 {
		nodeID = result.NodeID
	}
	return ControllerRaftCompactionNodeResult{
		NodeID:              nodeID,
		AppliedIndex:        result.AppliedIndex,
		BeforeSnapshotIndex: result.BeforeSnapshotIndex,
		AfterSnapshotIndex:  result.AfterSnapshotIndex,
		Compacted:           result.Compacted,
		SkippedReason:       result.SkippedReason,
	}
}
