package manager

import (
	"errors"
	"net/http"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
type ControllerLogEntryDTO = LogEntryDTO

// SlotLogEntriesResponse is the manager slot log entries response body.
type SlotLogEntriesResponse struct {
	// NodeID is the node whose local Slot log was read.
	NodeID uint64 `json:"node_id"`
	// SlotID is the physical Slot identifier.
	SlotID uint32 `json:"slot_id"`
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
	Items []SlotLogEntryDTO `json:"items"`
}

// SlotLogEntryDTO is one manager-facing Slot Raft log entry summary.
type SlotLogEntryDTO = LogEntryDTO

// LogEntryDTO is one manager-facing distributed Raft log entry summary.
type LogEntryDTO struct {
	// Index is the Raft log index.
	Index uint64 `json:"index"`
	// Term is the Raft term stored on the entry.
	Term uint64 `json:"term"`
	// Type is the normalized Raft entry type.
	Type string `json:"type"`
	// CreatedAtMS is the proposer-issued command timestamp in Unix milliseconds when known.
	CreatedAtMS int64 `json:"created_at_ms,omitempty"`
	// DataSize is the payload size in bytes.
	DataSize int `json:"data_size"`
	// DecodeStatus reports whether the entry payload was decoded for inspection.
	DecodeStatus string `json:"decode_status,omitempty"`
	// DecodedType is the stable command or payload type when decoding succeeds.
	DecodedType string `json:"decoded_type,omitempty"`
	// Decoded is a redacted JSON-friendly payload summary for manager inspection.
	Decoded map[string]any `json:"decoded,omitempty"`
}

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
		writeLogError(c, err, "controller log read unavailable", "controller log not found")
		return
	}
	c.JSON(http.StatusOK, controllerLogEntriesDTO(page))
}

func (s *Server) handleSlotLogs(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseSlotLogEntriesRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	page, err := s.management.ListSlotLogEntries(c.Request.Context(), req)
	if err != nil {
		writeLogError(c, err, "slot log read unavailable", "slot log not found")
		return
	}
	c.JSON(http.StatusOK, slotLogEntriesDTO(page))
}

func parseControllerLogEntriesRequest(c *gin.Context) (managementusecase.ListControllerLogEntriesRequest, error) {
	nodeID, err := parseRequiredLogNodeID(c.Query("node_id"))
	if err != nil {
		return managementusecase.ListControllerLogEntriesRequest{}, errors.New("invalid node_id")
	}
	limit, err := parseLogLimit(c.Query("limit"))
	if err != nil {
		return managementusecase.ListControllerLogEntriesRequest{}, errors.New("invalid limit")
	}
	cursor, err := parseLogCursor(c.Query("cursor"))
	if err != nil {
		return managementusecase.ListControllerLogEntriesRequest{}, errors.New("invalid cursor")
	}
	return managementusecase.ListControllerLogEntriesRequest{NodeID: nodeID, Limit: limit, Cursor: cursor}, nil
}

func parseSlotLogEntriesRequest(c *gin.Context) (managementusecase.ListSlotLogEntriesRequest, error) {
	slotID, err := parseRequiredLogSlotID(c.Param("slot_id"))
	if err != nil {
		return managementusecase.ListSlotLogEntriesRequest{}, errors.New("invalid slot_id")
	}
	nodeID, err := parseRequiredLogNodeID(c.Query("node_id"))
	if err != nil {
		return managementusecase.ListSlotLogEntriesRequest{}, errors.New("invalid node_id")
	}
	limit, err := parseLogLimit(c.Query("limit"))
	if err != nil {
		return managementusecase.ListSlotLogEntriesRequest{}, errors.New("invalid limit")
	}
	cursor, err := parseLogCursor(c.Query("cursor"))
	if err != nil {
		return managementusecase.ListSlotLogEntriesRequest{}, errors.New("invalid cursor")
	}
	return managementusecase.ListSlotLogEntriesRequest{NodeID: nodeID, SlotID: slotID, Limit: limit, Cursor: cursor}, nil
}

func parseRequiredLogNodeID(raw string) (uint64, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseRequiredLogSlotID(raw string) (uint32, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return uint32(value), nil
}

func parseLogLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseLogCursor(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, strconv.ErrSyntax
	}
	return value, nil
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
		out = append(out, logEntryDTO(item))
	}
	return out
}

func slotLogEntriesDTO(page managementusecase.SlotLogEntriesResponse) SlotLogEntriesResponse {
	return SlotLogEntriesResponse{
		NodeID:       page.NodeID,
		SlotID:       page.SlotID,
		FirstIndex:   page.FirstIndex,
		LastIndex:    page.LastIndex,
		CommitIndex:  page.CommitIndex,
		AppliedIndex: page.AppliedIndex,
		NextCursor:   page.NextCursor,
		Items:        slotLogEntryDTOs(page.Items),
	}
}

func slotLogEntryDTOs(items []managementusecase.SlotLogEntry) []SlotLogEntryDTO {
	out := make([]SlotLogEntryDTO, 0, len(items))
	for _, item := range items {
		out = append(out, logEntryDTO(item))
	}
	return out
}

func logEntryDTO(item managementusecase.LogEntry) LogEntryDTO {
	return LogEntryDTO{
		Index:        item.Index,
		Term:         item.Term,
		Type:         item.Type,
		CreatedAtMS:  item.CreatedAtMS,
		DataSize:     item.DataSize,
		DecodeStatus: item.DecodeStatus,
		DecodedType:  item.DecodedType,
		Decoded:      item.Decoded,
	}
}

func writeLogError(c *gin.Context, err error, unavailableMessage, notFoundMessage string) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid log request")
	case errors.Is(err, metadb.ErrNotFound), errors.Is(err, cluster.ErrSlotNotFound):
		jsonError(c, http.StatusNotFound, "not_found", notFoundMessage)
	case errors.Is(err, managementusecase.ErrLogReaderUnavailable), controlSnapshotUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", unavailableMessage)
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
