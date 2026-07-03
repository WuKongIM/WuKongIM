package manager

import (
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
	"github.com/gin-gonic/gin"
)

// ControllerRaftCompactionResponse is the manager response for a control-plane-wide compaction trigger.
type ControllerRaftCompactionResponse struct {
	// GeneratedAt records when the fan-out result was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// Total is the number of Controller voter nodes targeted.
	Total int `json:"total"`
	// Succeeded is the number of nodes that completed the attempt.
	Succeeded int `json:"succeeded"`
	// Failed is the number of nodes that returned an error.
	Failed int `json:"failed"`
	// Items contains one result for each targeted Controller voter node.
	Items []ControllerRaftCompactionNodeDTO `json:"items"`
}

// ControllerRaftCompactionNodeDTO describes one node-local compaction attempt.
type ControllerRaftCompactionNodeDTO struct {
	// NodeID is the Controller voter node that handled the attempt.
	NodeID uint64 `json:"node_id"`
	// Success reports whether the node accepted and completed the local attempt.
	Success bool `json:"success"`
	// AppliedIndex is the node-local applied index used as the compaction target.
	AppliedIndex uint64 `json:"applied_index"`
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64 `json:"before_snapshot_index"`
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64 `json:"after_snapshot_index"`
	// Compacted reports whether this attempt created a new snapshot and compacted entries.
	Compacted bool `json:"compacted"`
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string `json:"skipped_reason"`
	// Error is the per-node failure message when Success is false.
	Error string `json:"error"`
}

func (s *Server) handleControllerRaftCompact(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	result, err := s.management.CompactControllerRaftLogs(c.Request.Context())
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, controllerRaftCompactResponseDTO(result))
}

func (s *Server) handleNodeControllerRaftCompact(c *gin.Context) {
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	result, err := s.management.CompactControllerRaftLog(c.Request.Context(), nodeID)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, controllerRaftCompactResponseDTO(result))
}

func controllerRaftCompactResponseDTO(result managementusecase.CompactControllerRaftLogsResponse) ControllerRaftCompactionResponse {
	return ControllerRaftCompactionResponse{
		GeneratedAt: result.GeneratedAt,
		Total:       result.Total,
		Succeeded:   result.Succeeded,
		Failed:      result.Failed,
		Items:       controllerRaftCompactNodeDTOs(result.Items),
	}
}

func controllerRaftCompactNodeDTOs(items []managementusecase.ControllerRaftCompactionNodeResult) []ControllerRaftCompactionNodeDTO {
	out := make([]ControllerRaftCompactionNodeDTO, 0, len(items))
	for _, item := range items {
		out = append(out, ControllerRaftCompactionNodeDTO{
			NodeID:              item.NodeID,
			Success:             item.Success,
			AppliedIndex:        item.AppliedIndex,
			BeforeSnapshotIndex: item.BeforeSnapshotIndex,
			AfterSnapshotIndex:  item.AfterSnapshotIndex,
			Compacted:           item.Compacted,
			SkippedReason:       item.SkippedReason,
			Error:               item.Error,
		})
	}
	return out
}
