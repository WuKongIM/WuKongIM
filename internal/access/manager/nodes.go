package manager

import (
	"context"
	"errors"
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/gin-gonic/gin"
)

// NodesResponse is the manager node list response body.
type NodesResponse struct {
	// Total is the number of returned manager nodes.
	Total int `json:"total"`
	// Items contains the ordered manager node DTO list.
	Items []NodeDTO `json:"items"`
}

// NodeDTO is the manager-facing node response item.
type NodeDTO struct {
	// NodeID is the node identifier.
	NodeID uint64 `json:"node_id"`
	// Addr is the cluster listen address of the node.
	Addr string `json:"addr"`
	// Status is the manager-facing node status string.
	Status string `json:"status"`
	// LastHeartbeatAt is the latest controller heartbeat timestamp.
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	// IsLocal reports whether the node is the current process node.
	IsLocal bool `json:"is_local"`
	// CapacityWeight is the configured controller capacity weight.
	CapacityWeight int `json:"capacity_weight"`
	// Controller contains controller-related node view fields.
	Controller NodeControllerDTO `json:"controller"`
	// SlotStats contains slot hosting summary fields.
	SlotStats NodeSlotStatsDTO `json:"slot_stats"`
	// DistributedLog contains controller and Slot Raft log health summaries.
	DistributedLog NodeDistributedLogDTO `json:"distributed_log"`
}

// NodeControllerDTO contains controller-facing node state.
type NodeControllerDTO struct {
	// Role is the controller role summary for the node.
	Role string `json:"role"`
}

// NodeSlotStatsDTO contains slot placement summary fields.
type NodeSlotStatsDTO struct {
	// Count is the number of observed slot peers hosted by the node.
	Count int `json:"count"`
	// LeaderCount is the number of observed slots led by the node.
	LeaderCount int `json:"leader_count"`
}

// NodeDistributedLogDTO contains distributed log health summaries for one node.
type NodeDistributedLogDTO struct {
	// Controller contains controller Raft placement and leadership context.
	Controller NodeControllerLogDTO `json:"controller"`
	// Slots contains Slot Raft log health aggregated for Slots hosted by the node.
	Slots NodeSlotLogHealthDTO `json:"slots"`
}

// NodeControllerLogDTO contains controller Raft membership context for one node.
type NodeControllerLogDTO struct {
	// Role is the current controller role summary for the node.
	Role string `json:"role"`
	// LeaderID is the current controller Raft leader known locally.
	LeaderID uint64 `json:"leader_id"`
	// Voter reports whether this node is configured as a controller voter.
	Voter bool `json:"voter"`
}

// NodeSlotLogHealthDTO contains aggregated Slot Raft log health for one node.
type NodeSlotLogHealthDTO struct {
	// ReplicaCount is the number of observed Slot Raft replicas hosted by the node.
	ReplicaCount int `json:"replica_count"`
	// LeaderCount is the number of observed Slot Raft leaders hosted by the node.
	LeaderCount int `json:"leader_count"`
	// FollowerCount is the number of observed Slot Raft followers hosted by the node.
	FollowerCount int `json:"follower_count"`
	// MaxCommitLag is the largest leader commit to local commit gap.
	MaxCommitLag uint64 `json:"max_commit_lag"`
	// MaxApplyGap is the largest local commit to applied gap.
	MaxApplyGap uint64 `json:"max_apply_gap"`
	// UnavailableCount is the number of hosted Slots whose log watermark could not be read.
	UnavailableCount int `json:"unavailable_count"`
	// UnhealthyCount is the number of hosted Slots that are unavailable, quorum-lost, or lagging.
	UnhealthyCount int `json:"unhealthy_count"`
	// Samples contains ordered Slot log samples that explain non-healthy state.
	Samples []NodeSlotLogSampleDTO `json:"samples"`
}

// NodeSlotLogSampleDTO contains one sampled Slot Raft log health row.
type NodeSlotLogSampleDTO struct {
	// SlotID is the physical Slot identity for the sampled log row.
	SlotID uint32 `json:"slot_id"`
	// Role is this node's role in the Slot Raft group.
	Role string `json:"role"`
	// LeaderID is the Slot Raft leader reported by the runtime view.
	LeaderID uint64 `json:"leader_id"`
	// CommitIndex is this node's local committed Raft log index for the Slot.
	CommitIndex uint64 `json:"commit_index"`
	// AppliedIndex is this node's local applied Raft log index for the Slot.
	AppliedIndex uint64 `json:"applied_index"`
	// LeaderCommitIndex is the Slot leader's committed Raft log index when available.
	LeaderCommitIndex uint64 `json:"leader_commit_index"`
	// CommitLag is LeaderCommitIndex minus CommitIndex when both are known.
	CommitLag uint64 `json:"commit_lag"`
	// ApplyGap is CommitIndex minus AppliedIndex when both are known.
	ApplyGap uint64 `json:"apply_gap"`
	// Quorum is a manager-facing quorum summary: healthy or lost.
	Quorum string `json:"quorum"`
	// Status is a manager-facing log health summary for this Slot sample.
	Status string `json:"status"`
}

func (s *Server) handleNodes(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	items, err := s.management.ListNodes(c.Request.Context())
	if err != nil {
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, NodesResponse{
		Total: len(items),
		Items: nodeDTOs(items),
	})
}

func nodeDTOs(items []managementusecase.Node) []NodeDTO {
	out := make([]NodeDTO, 0, len(items))
	for _, item := range items {
		out = append(out, nodeDTO(item))
	}
	return out
}

func leaderConsistentReadUnavailable(err error) bool {
	return errors.Is(err, raftcluster.ErrNoLeader) ||
		errors.Is(err, raftcluster.ErrNotLeader) ||
		errors.Is(err, raftcluster.ErrNotStarted) ||
		errors.Is(err, context.DeadlineExceeded)
}
