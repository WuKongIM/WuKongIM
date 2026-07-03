package manager

import (
	"context"
	"errors"
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/gin-gonic/gin"
)

// NodesResponse is the manager node list response body.
type NodesResponse struct {
	// GeneratedAt is the timestamp when the inventory snapshot was built.
	GeneratedAt time.Time `json:"generated_at"`
	// ControllerLeaderID is the Controller leader known to this node.
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
	// Name is the operator-facing node name.
	Name string `json:"name"`
	// Addr is the cluster listen address of the node.
	Addr string `json:"addr"`
	// Status is the manager-facing node status string.
	Status string `json:"status"`
	// LastHeartbeatAt is the best available control snapshot timestamp for this node.
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	// IsLocal reports whether the node is the current process node.
	IsLocal bool `json:"is_local"`
	// CapacityWeight is the current relative planner capacity.
	CapacityWeight int `json:"capacity_weight"`
	// Membership contains durable membership role and lifecycle state.
	Membership NodeMembershipDTO `json:"membership"`
	// Health contains observed node health and operator state.
	Health NodeHealthDTO `json:"health"`
	// Controller contains Controller role and voter context.
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
	// Role is the durable cluster membership role.
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
	// LastHeartbeatAt is the best available control snapshot timestamp for this node.
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	// Fresh reports whether the latest durable health report is within its TTL.
	Fresh bool `json:"fresh"`
	// Freshness is fresh, stale, or missing.
	Freshness string `json:"freshness"`
	// RuntimeReady reports whether the node can serve foreground traffic.
	RuntimeReady bool `json:"runtime_ready"`
	// ReportAgeMS is the current health report age in milliseconds.
	ReportAgeMS int64 `json:"report_age_ms"`
	// ReportTTLMS is the configured freshness TTL in milliseconds.
	ReportTTLMS int64 `json:"report_ttl_ms"`
	// ObservedControlRevision is the latest Controller revision observed by the node.
	ObservedControlRevision uint64 `json:"observed_control_revision"`
	// ObservedSlotRevision is the latest local Slot observation revision.
	ObservedSlotRevision uint64 `json:"observed_slot_revision"`
	// ErrorCode is a bounded machine-readable runtime reason.
	ErrorCode string `json:"error_code,omitempty"`
}

// NodeControllerDTO contains controller-facing node state.
type NodeControllerDTO struct {
	// Role is the controller role summary for the node.
	Role string `json:"role"`
	// Voter reports whether this node is a configured Controller voter.
	Voter bool `json:"voter"`
	// LeaderID is the current Controller leader known locally.
	LeaderID uint64 `json:"leader_id"`
	// RaftHealth is the summarized local Controller Raft health state.
	RaftHealth string `json:"raft_health"`
	// FirstIndex is the first available local Controller Raft log index.
	FirstIndex uint64 `json:"first_index"`
	// AppliedIndex is the queried node's applied Controller Raft index watermark.
	AppliedIndex uint64 `json:"applied_index"`
	// SnapshotIndex is the latest persisted Controller Raft snapshot index.
	SnapshotIndex uint64 `json:"snapshot_index"`
}

// NodeSlotStatsDTO contains slot placement summary fields.
type NodeSlotStatsDTO struct {
	// Count is the number of desired Slot replicas hosted by the node.
	Count int `json:"count"`
	// LeaderCount is the number of observed slots led by the node.
	LeaderCount int `json:"leader_count"`
}

// NodeSlotsSummaryDTO contains lightweight Slot placement counts.
type NodeSlotsSummaryDTO struct {
	// ReplicaCount is the number of desired Slot replicas hosted by the node.
	ReplicaCount int `json:"replica_count"`
	// LeaderCount is the number of actual Slot Raft leaders hosted by the node.
	LeaderCount int `json:"leader_count"`
	// FollowerCount is the number of desired Slot replicas that are not observed leaders.
	FollowerCount int `json:"follower_count"`
	// QuorumLostCount is reserved for runtime observation once available.
	QuorumLostCount int `json:"quorum_lost_count"`
	// UnreportedCount is reserved for runtime observation once available.
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
	// PendingActivations counts local sessions accepted but not yet authority-active.
	PendingActivations int `json:"pending_activations"`
	// SessionsByListener groups gateway sessions by listener name or address.
	SessionsByListener map[string]int `json:"sessions_by_listener"`
	// AcceptingNewSessions reports whether gateway admission currently accepts new sessions.
	AcceptingNewSessions bool `json:"accepting_new_sessions"`
	// Draining reports whether the target node believes it is in drain mode.
	Draining bool `json:"draining"`
	// Unknown means runtime counters could not be read.
	Unknown bool `json:"unknown"`
}

// NodeActionsDTO contains backend business capability hints for UI actions.
type NodeActionsDTO struct {
	// CanDrain reports whether the legacy node-drain action can be used.
	CanDrain bool `json:"can_drain"`
	// CanResume reports whether the legacy node-resume action can be used.
	CanResume bool `json:"can_resume"`
	// CanScaleIn reports whether the data-node scale-in flow can be considered.
	CanScaleIn bool `json:"can_scale_in"`
	// CanOnboard reports whether the node can be considered for explicit resource allocation.
	CanOnboard bool `json:"can_onboard"`
	// CanMoveSlotsIn reports whether Slot replicas can be moved into this data node.
	CanMoveSlotsIn bool `json:"can_move_slots_in"`
	// CanMoveSlotsOut reports whether Slot replicas can be moved out of this data node without changing node lifecycle.
	CanMoveSlotsOut bool `json:"can_move_slots_out"`
	// CanPromoteControllerVoter reports whether Controller voter promotion can be considered.
	CanPromoteControllerVoter bool `json:"can_promote_controller_voter"`
}

func (s *Server) handleNodes(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	list, err := s.management.ListNodes(c.Request.Context())
	if err != nil {
		if controlSnapshotUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller snapshot unavailable")
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

func nodeDTO(item managementusecase.Node) NodeDTO {
	sessionsByListener := item.Runtime.SessionsByListener
	if sessionsByListener == nil {
		sessionsByListener = map[string]int{}
	}
	return NodeDTO{
		NodeID:          item.NodeID,
		Name:            item.Name,
		Addr:            item.Addr,
		Status:          item.Status,
		LastHeartbeatAt: item.LastHeartbeatAt,
		IsLocal:         item.IsLocal,
		CapacityWeight:  item.CapacityWeight,
		Membership: NodeMembershipDTO{
			Role:        item.Membership.Role,
			JoinState:   item.Membership.JoinState,
			Schedulable: item.Membership.Schedulable,
		},
		Health: NodeHealthDTO{
			Status:                  item.Health.Status,
			LastHeartbeatAt:         item.Health.LastHeartbeatAt,
			Fresh:                   item.Health.Fresh,
			Freshness:               item.Health.Freshness,
			RuntimeReady:            item.Health.RuntimeReady,
			ReportAgeMS:             item.Health.ReportAgeMS,
			ReportTTLMS:             item.Health.ReportTTLMS,
			ObservedControlRevision: item.Health.ObservedControlRevision,
			ObservedSlotRevision:    item.Health.ObservedSlotRevision,
			ErrorCode:               item.Health.ErrorCode,
		},
		Controller: NodeControllerDTO{
			Role:          item.Controller.Role,
			Voter:         item.Controller.Voter,
			LeaderID:      item.Controller.LeaderID,
			RaftHealth:    item.Controller.RaftHealth,
			FirstIndex:    item.Controller.FirstIndex,
			AppliedIndex:  item.Controller.AppliedIndex,
			SnapshotIndex: item.Controller.SnapshotIndex,
		},
		SlotStats: NodeSlotStatsDTO{
			Count:       item.Slots.ReplicaCount,
			LeaderCount: item.Slots.LeaderCount,
		},
		Slots: NodeSlotsSummaryDTO{
			ReplicaCount:    item.Slots.ReplicaCount,
			LeaderCount:     item.Slots.LeaderCount,
			FollowerCount:   item.Slots.FollowerCount,
			QuorumLostCount: item.Slots.QuorumLostCount,
			UnreportedCount: item.Slots.UnreportedCount,
		},
		Runtime: NodeRuntimeDTO{
			NodeID:               item.Runtime.NodeID,
			ActiveOnline:         item.Runtime.ActiveOnline,
			ClosingOnline:        item.Runtime.ClosingOnline,
			TotalOnline:          item.Runtime.TotalOnline,
			GatewaySessions:      item.Runtime.GatewaySessions,
			PendingActivations:   item.Runtime.PendingActivations,
			SessionsByListener:   sessionsByListener,
			AcceptingNewSessions: item.Runtime.AcceptingNewSessions,
			Draining:             item.Runtime.Draining,
			Unknown:              item.Runtime.Unknown,
		},
		Actions: NodeActionsDTO{
			CanDrain:                  item.Actions.CanDrain,
			CanResume:                 item.Actions.CanResume,
			CanScaleIn:                item.Actions.CanScaleIn,
			CanOnboard:                item.Actions.CanOnboard,
			CanMoveSlotsIn:            item.Actions.CanMoveSlotsIn,
			CanMoveSlotsOut:           item.Actions.CanMoveSlotsOut,
			CanPromoteControllerVoter: item.Actions.CanPromoteControllerVoter,
		},
	}
}

func controlSnapshotUnavailable(err error) bool {
	return errors.Is(err, cluster.ErrNotStarted) ||
		errors.Is(err, cluster.ErrStopping) ||
		errors.Is(err, context.DeadlineExceeded)
}
