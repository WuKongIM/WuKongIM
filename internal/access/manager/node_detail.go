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
	return NodeDetailDTO{
		NodeDTO: nodeDTO(item.Node),
		Slots: NodeSlotsDTO{
			HostedIDs: cloneUint32s(item.Slots.HostedIDs),
			LeaderIDs: cloneUint32s(item.Slots.LeaderIDs),
		},
	}
}

func nodeDTO(item managementusecase.Node) NodeDTO {
	return NodeDTO{
		NodeID:          item.NodeID,
		Addr:            item.Addr,
		Status:          item.Status,
		LastHeartbeatAt: item.LastHeartbeatAt,
		IsLocal:         item.IsLocal,
		CapacityWeight:  item.CapacityWeight,
		Controller: NodeControllerDTO{
			Role: item.ControllerRole,
		},
		SlotStats: NodeSlotStatsDTO{
			Count:       item.SlotCount,
			LeaderCount: item.LeaderSlotCount,
		},
	}
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
