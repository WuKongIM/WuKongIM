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
	// GeneratedAt is the timestamp when the inventory snapshot was built.
	GeneratedAt time.Time `json:"generated_at"`
	// ControllerLeaderID is the Controller Raft leader known to this node.
	ControllerLeaderID uint64 `json:"controller_leader_id"`
	// Total is the number of returned manager nodes.
	Total int `json:"total"`
	// Items contains the ordered manager node DTO list.
	Items []NodeDTO `json:"items"`
}

// NodeDTO is the manager-facing node response item.
type NodeDTO struct {
	// NodeID is the node identifier.
	NodeID uint64 `json:"node_id"`
	// Name is the operator-facing node name persisted in controller metadata.
	Name string `json:"name"`
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
	// Membership contains durable membership role and lifecycle state.
	Membership NodeMembershipDTO `json:"membership"`
	// Health contains observed node health and operator state.
	Health NodeHealthDTO `json:"health"`
	// Controller contains controller-related node view fields.
	Controller NodeControllerDTO `json:"controller"`
	// SlotStats contains slot hosting summary fields.
	SlotStats NodeSlotStatsDTO `json:"slot_stats"`
	// Slots contains lightweight Slot placement counts.
	Slots NodeSlotsSummaryDTO `json:"slots"`
	// Runtime contains node-local online and gateway counters.
	Runtime NodeRuntimeDTO `json:"runtime"`
	// Actions contains backend business capability hints for UI actions.
	Actions NodeActionsDTO `json:"actions"`
}

// NodeMembershipDTO describes durable cluster membership for one node.
type NodeMembershipDTO struct {
	// Role is the durable cluster membership role, such as data or controller_voter.
	Role string `json:"role"`
	// JoinState is the durable membership lifecycle state.
	JoinState string `json:"join_state"`
	// Schedulable reports whether the planner may place data replicas on this node.
	Schedulable bool `json:"schedulable"`
}

// NodeHealthDTO describes observed node health and operator state.
type NodeHealthDTO struct {
	// Status is the manager-facing health or operator state.
	Status string `json:"status"`
	// LastHeartbeatAt is the latest controller heartbeat timestamp.
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
}

// NodeControllerDTO contains controller-facing node state.
type NodeControllerDTO struct {
	// Role is the controller role summary for the node.
	Role string `json:"role"`
	// Voter reports whether this node is a configured Controller voter.
	Voter bool `json:"voter"`
	// LeaderID is the current Controller Raft leader known locally.
	LeaderID uint64 `json:"leader_id"`
}

// NodeSlotStatsDTO contains slot placement summary fields.
type NodeSlotStatsDTO struct {
	// Count is the number of observed slot peers hosted by the node.
	Count int `json:"count"`
	// LeaderCount is the number of observed slots led by the node.
	LeaderCount int `json:"leader_count"`
}

// NodeSlotsSummaryDTO contains lightweight Slot placement counts.
type NodeSlotsSummaryDTO struct {
	// ReplicaCount is the number of observed Slot replicas hosted by the node.
	ReplicaCount int `json:"replica_count"`
	// LeaderCount is the number of observed Slots led by the node.
	LeaderCount int `json:"leader_count"`
	// FollowerCount is the number of observed non-leader Slot replicas hosted by the node.
	FollowerCount int `json:"follower_count"`
	// QuorumLostCount is the number of hosted Slots whose runtime view lacks quorum.
	QuorumLostCount int `json:"quorum_lost_count"`
	// UnreportedCount is the number of expected hosted Slots missing runtime observation.
	UnreportedCount int `json:"unreported_count"`
}

// NodeRuntimeDTO contains node-local online and gateway counters.
type NodeRuntimeDTO struct {
	// NodeID identifies the cluster node described by this summary.
	NodeID uint64 `json:"node_id"`
	// ActiveOnline counts active authenticated online connections.
	ActiveOnline int `json:"active_online"`
	// ClosingOnline counts authenticated online connections that are closing but not fully removed.
	ClosingOnline int `json:"closing_online"`
	// TotalOnline counts all authenticated online connections tracked by the node.
	TotalOnline int `json:"total_online"`
	// GatewaySessions counts all gateway sessions, including unauthenticated sessions.
	GatewaySessions int `json:"gateway_sessions"`
	// SessionsByListener groups gateway sessions by listener name or address.
	SessionsByListener map[string]int `json:"sessions_by_listener"`
	// AcceptingNewSessions reports whether gateway admission currently accepts new sessions.
	AcceptingNewSessions bool `json:"accepting_new_sessions"`
	// Draining reports whether the target node believes it is in drain mode.
	Draining bool `json:"draining"`
	// Unknown means runtime counters could not be read and must fail closed for scale-in safety.
	Unknown bool `json:"unknown"`
}

// NodeActionsDTO contains backend business capability hints for UI actions.
type NodeActionsDTO struct {
	// CanDrain reports whether the node can be marked draining.
	CanDrain bool `json:"can_drain"`
	// CanResume reports whether the node can be resumed from draining.
	CanResume bool `json:"can_resume"`
	// CanScaleIn reports whether the data-node scale-in flow can be considered.
	CanScaleIn bool `json:"can_scale_in"`
	// CanOnboard reports whether the node can be considered for explicit resource allocation.
	CanOnboard bool `json:"can_onboard"`
}

func (s *Server) handleNodes(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	list, err := s.management.ListNodes(c.Request.Context())
	if err != nil {
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, NodesResponse{
		GeneratedAt:        list.GeneratedAt,
		ControllerLeaderID: list.ControllerLeaderID,
		Total:              len(list.Items),
		Items:              nodeDTOs(list.Items),
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
