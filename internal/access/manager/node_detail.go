package manager

import (
	"errors"
	"net/http"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

// NodeDetailDTO is the manager-facing node detail response body.
type NodeDetailDTO struct {
	NodeDTO
	// Slots contains hosted slot placement details for the node.
	Slots NodeSlotsDTO `json:"slots"`
}

// NodeSlotsDTO contains manager-facing hosted slot identifiers for a node.
type NodeSlotsDTO struct {
	// HostedIDs is the ordered list of slots currently hosted by the node.
	HostedIDs []uint32 `json:"hosted_ids"`
	// LeaderIDs is the ordered list of slots currently led by the node.
	LeaderIDs []uint32 `json:"leader_ids"`
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

func (s *Server) handleNode(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}

	item, err := s.management.GetNode(c.Request.Context(), nodeID)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			jsonError(c, http.StatusNotFound, "not_found", "node not found")
			return
		}
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, nodeDetailDTO(item))
}

func nodeDetailDTO(item managementusecase.NodeDetail) NodeDetailDTO {
	slots := normalizedNodeSlotSummary(item.Node)
	return NodeDetailDTO{
		NodeDTO: nodeDTO(item.Node),
		Slots: NodeSlotsDTO{
			HostedIDs:       cloneUint32s(item.Slots.HostedIDs),
			LeaderIDs:       cloneUint32s(item.Slots.LeaderIDs),
			ReplicaCount:    slots.ReplicaCount,
			LeaderCount:     slots.LeaderCount,
			FollowerCount:   slots.FollowerCount,
			QuorumLostCount: slots.QuorumLostCount,
			UnreportedCount: slots.UnreportedCount,
		},
	}
}

func nodeDTO(item managementusecase.Node) NodeDTO {
	controller := item.Controller
	if controller.Role == "" {
		controller.Role = item.ControllerRole
	}
	slots := normalizedNodeSlotSummary(item)
	runtime := item.Runtime
	if runtime.NodeID == 0 {
		runtime.NodeID = item.NodeID
	}
	if runtime.SessionsByListener == nil {
		runtime.SessionsByListener = map[string]int{}
	}
	health := item.Health
	if health.Status == "" {
		health.Status = item.Status
	}
	if health.LastHeartbeatAt.IsZero() {
		health.LastHeartbeatAt = item.LastHeartbeatAt
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
			Status:          health.Status,
			LastHeartbeatAt: health.LastHeartbeatAt,
		},
		Controller: NodeControllerDTO{
			Role:     controller.Role,
			Voter:    controller.Voter,
			LeaderID: controller.LeaderID,
		},
		SlotStats: NodeSlotStatsDTO{
			Count:       item.SlotCount,
			LeaderCount: item.LeaderSlotCount,
		},
		Slots: NodeSlotsSummaryDTO{
			ReplicaCount:    slots.ReplicaCount,
			LeaderCount:     slots.LeaderCount,
			FollowerCount:   slots.FollowerCount,
			QuorumLostCount: slots.QuorumLostCount,
			UnreportedCount: slots.UnreportedCount,
		},
		Runtime: NodeRuntimeDTO{
			NodeID:               runtime.NodeID,
			ActiveOnline:         runtime.ActiveOnline,
			ClosingOnline:        runtime.ClosingOnline,
			TotalOnline:          runtime.TotalOnline,
			GatewaySessions:      runtime.GatewaySessions,
			SessionsByListener:   runtime.SessionsByListener,
			AcceptingNewSessions: runtime.AcceptingNewSessions,
			Draining:             runtime.Draining,
			Unknown:              runtime.Unknown,
		},
		Actions: NodeActionsDTO{
			CanDrain:   item.Actions.CanDrain,
			CanResume:  item.Actions.CanResume,
			CanScaleIn: item.Actions.CanScaleIn,
			CanOnboard: item.Actions.CanOnboard,
		},
	}
}

func normalizedNodeSlotSummary(item managementusecase.Node) managementusecase.NodeSlotSummary {
	slots := item.Slots
	if slots.ReplicaCount == 0 && item.SlotCount != 0 {
		slots.ReplicaCount = item.SlotCount
	}
	if slots.LeaderCount == 0 && item.LeaderSlotCount != 0 {
		slots.LeaderCount = item.LeaderSlotCount
	}
	if slots.FollowerCount == 0 && slots.ReplicaCount > slots.LeaderCount {
		slots.FollowerCount = slots.ReplicaCount - slots.LeaderCount
	}
	return slots
}

func parseNodeIDParam(raw string) (uint64, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func cloneUint32s(values []uint32) []uint32 {
	if len(values) == 0 {
		return []uint32{}
	}
	return append([]uint32(nil), values...)
}
