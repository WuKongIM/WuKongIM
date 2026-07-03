package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerSlotRaftCompactNodeResult describes one node-local Slot Raft compaction attempt.
type ManagerSlotRaftCompactNodeResult struct {
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

// ManagerSlotRaftCompactResponse is returned after triggering Slot Raft compaction.
type ManagerSlotRaftCompactResponse struct {
	// GeneratedAt records when the compaction response was assembled.
	GeneratedAt string `json:"generated_at"`
	// Total is the number of node/slot targets.
	Total int `json:"total"`
	// Succeeded is the number of targets that completed the attempt.
	Succeeded int `json:"succeeded"`
	// Failed is the number of targets that returned an error.
	Failed int `json:"failed"`
	// Items contains per-target results ordered by request target order.
	Items []ManagerSlotRaftCompactNodeResult `json:"items"`
}

func (s *Server) handleCompactSlotRaftLog(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseRequiredLogNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	slotID, err := parseRequiredLogSlotID(c.Param("slot_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot_id")
		return
	}
	summary, err := s.management.CompactSlotRaftLog(c.Request.Context(), nodeID, slotID)
	if err != nil {
		writeSlotRaftCompactError(c, err)
		return
	}
	c.JSON(http.StatusOK, slotRaftCompactSummaryDTO(summary))
}

func slotRaftCompactSummaryDTO(summary managementusecase.SlotRaftCompactionSummary) ManagerSlotRaftCompactResponse {
	items := make([]ManagerSlotRaftCompactNodeResult, 0, len(summary.Items))
	for _, item := range summary.Items {
		items = append(items, ManagerSlotRaftCompactNodeResult{
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
	return ManagerSlotRaftCompactResponse{
		GeneratedAt: managerTimeString(summary.GeneratedAt),
		Total:       summary.Total,
		Succeeded:   summary.Succeeded,
		Failed:      summary.Failed,
		Items:       items,
	}
}

func writeSlotRaftCompactError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot raft compaction request")
	case errors.Is(err, managementusecase.ErrSlotRaftOperatorUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot raft unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
