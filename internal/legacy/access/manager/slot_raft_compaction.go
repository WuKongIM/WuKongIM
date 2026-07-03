package manager

import (
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
	"github.com/gin-gonic/gin"
)

// SlotRaftCompactionResponse is the manager response for a node-local Slot Raft compaction trigger.
type SlotRaftCompactionResponse struct {
	// GeneratedAt records when the result was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// Total is the number of node/slot targets.
	Total int `json:"total"`
	// Succeeded is the number of targets that completed the attempt.
	Succeeded int `json:"succeeded"`
	// Failed is the number of targets that returned an error.
	Failed int `json:"failed"`
	// Items contains one result for each targeted node/slot pair.
	Items []SlotRaftCompactionNodeDTO `json:"items"`
}

// SlotRaftCompactionNodeDTO describes one node-local Slot Raft compaction attempt.
type SlotRaftCompactionNodeDTO struct {
	// NodeID is the cluster node that handled the attempt.
	NodeID uint64 `json:"node_id"`
	// SlotID is the physical Slot whose local Raft log was compacted.
	SlotID uint32 `json:"slot_id"`
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
	// Error is the per-target failure message when Success is false.
	Error string `json:"error"`
}

func (s *Server) handleNodeSlotRaftCompact(c *gin.Context) {
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	slotID, err := parseSlotIDParam(c.Param("slot_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot_id")
		return
	}
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	result, err := s.management.CompactSlotRaftLog(c.Request.Context(), nodeID, slotID)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, slotRaftCompactResponseDTO(result))
}

func slotRaftCompactResponseDTO(result managementusecase.CompactSlotRaftLogResponse) SlotRaftCompactionResponse {
	return SlotRaftCompactionResponse{
		GeneratedAt: result.GeneratedAt,
		Total:       result.Total,
		Succeeded:   result.Succeeded,
		Failed:      result.Failed,
		Items:       slotRaftCompactNodeDTOs(result.Items),
	}
}

func slotRaftCompactNodeDTOs(items []managementusecase.SlotRaftCompactionNodeResult) []SlotRaftCompactionNodeDTO {
	out := make([]SlotRaftCompactionNodeDTO, 0, len(items))
	for _, item := range items {
		out = append(out, SlotRaftCompactionNodeDTO{
			NodeID:              item.NodeID,
			SlotID:              item.SlotID,
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
