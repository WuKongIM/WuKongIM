package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
	"github.com/gin-gonic/gin"
)

// ControllerLogEntriesResponse is the manager Controller log entries response body.
type ControllerLogEntriesResponse struct {
	// NodeID is the node whose local Controller log was read.
	NodeID uint64 `json:"node_id"`
	// FirstIndex is the first available local Raft log index.
	FirstIndex uint64 `json:"first_index"`
	// LastIndex is the last available local Raft log index.
	LastIndex uint64 `json:"last_index"`
	// CommitIndex is the queried node's local committed index watermark.
	CommitIndex uint64 `json:"commit_index"`
	// AppliedIndex is the queried node's local applied index watermark.
	AppliedIndex uint64 `json:"applied_index"`
	// NextCursor is the cursor for the next older page. Zero means no more entries.
	NextCursor uint64 `json:"next_cursor,omitempty"`
	// Items contains entries ordered newest first.
	Items []ControllerLogEntryDTO `json:"items"`
}

// ControllerLogEntryDTO is one manager-facing Controller Raft log entry summary.
type ControllerLogEntryDTO = SlotLogEntryDTO

func (s *Server) handleControllerLogs(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseControllerLogEntriesRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	page, err := s.management.ListControllerLogEntries(c.Request.Context(), req)
	if err != nil {
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller log read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, controllerLogEntriesDTO(page))
}

func parseControllerLogEntriesRequest(c *gin.Context) (managementusecase.ListControllerLogEntriesRequest, error) {
	nodeID, err := parseNodeIDParam(c.Query("node_id"))
	if err != nil {
		return managementusecase.ListControllerLogEntriesRequest{}, errors.New("invalid node_id")
	}
	limit, err := parseSlotLogLimit(c.Query("limit"))
	if err != nil {
		return managementusecase.ListControllerLogEntriesRequest{}, errors.New("invalid limit")
	}
	cursor, err := parseSlotLogCursor(c.Query("cursor"))
	if err != nil {
		return managementusecase.ListControllerLogEntriesRequest{}, errors.New("invalid cursor")
	}
	return managementusecase.ListControllerLogEntriesRequest{NodeID: nodeID, Limit: limit, Cursor: cursor}, nil
}

func controllerLogEntriesDTO(page managementusecase.ControllerLogEntriesResponse) ControllerLogEntriesResponse {
	return ControllerLogEntriesResponse{
		NodeID:       page.NodeID,
		FirstIndex:   page.FirstIndex,
		LastIndex:    page.LastIndex,
		CommitIndex:  page.CommitIndex,
		AppliedIndex: page.AppliedIndex,
		NextCursor:   page.NextCursor,
		Items:        controllerLogEntryDTOs(page.Items),
	}
}

func controllerLogEntryDTOs(items []managementusecase.ControllerLogEntry) []ControllerLogEntryDTO {
	out := make([]ControllerLogEntryDTO, 0, len(items))
	for _, item := range items {
		out = append(out, ControllerLogEntryDTO{
			Index:        item.Index,
			Term:         item.Term,
			Type:         item.Type,
			DataSize:     item.DataSize,
			DecodeStatus: item.DecodeStatus,
			DecodedType:  item.DecodedType,
			Decoded:      item.Decoded,
		})
	}
	return out
}
