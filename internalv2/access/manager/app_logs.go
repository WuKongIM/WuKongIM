package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	applog "github.com/WuKongIM/WuKongIM/internalv2/log"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ApplicationLogSourcesResponse is the manager ordinary application log source response body.
type ApplicationLogSourcesResponse struct {
	// NodeID is the node whose ordinary application log sources were listed.
	NodeID uint64 `json:"node_id"`
	// Sources contains the fixed ordinary application log sources.
	Sources []ApplicationLogSourceDTO `json:"sources"`
}

// ApplicationLogSourceDTO describes one fixed ordinary application log source.
type ApplicationLogSourceDTO struct {
	// Name is the stable source identifier.
	Name string `json:"name"`
	// File is the fixed source file name.
	File string `json:"file"`
	// Available reports whether the source file can currently be read.
	Available bool `json:"available"`
	// SizeBytes is the current source size in bytes when known.
	SizeBytes int64 `json:"size_bytes"`
	// ModifiedAt is the RFC3339Nano source modification time when known.
	ModifiedAt string `json:"modified_at,omitempty"`
}

// ApplicationLogEntriesResponse is the manager ordinary application log page response body.
type ApplicationLogEntriesResponse struct {
	// NodeID is the node whose ordinary application log source was read.
	NodeID uint64 `json:"node_id"`
	// Source is the fixed source identifier that was read.
	Source string `json:"source"`
	// Cursor is the opaque reader cursor for the next read.
	Cursor string `json:"cursor"`
	// Rotated reports whether the source rotated before or during the read.
	Rotated bool `json:"rotated"`
	// Items contains ordinary application log entries.
	Items []ApplicationLogEntryDTO `json:"items"`
}

// ApplicationLogEntryDTO is one manager-facing ordinary application log entry.
type ApplicationLogEntryDTO struct {
	// Seq is the reader-owned entry sequence.
	Seq uint64 `json:"seq"`
	// Offset is the byte offset in the source when known.
	Offset uint64 `json:"offset"`
	// Time is the RFC3339Nano parsed log time when available.
	Time string `json:"time,omitempty"`
	// Level is the normalized log level.
	Level string `json:"level"`
	// Module is the parsed module or logger name.
	Module string `json:"module"`
	// Caller is the parsed caller location.
	Caller string `json:"caller"`
	// Message is the parsed human-readable message.
	Message string `json:"message"`
	// Fields contains structured display fields.
	Fields map[string]any `json:"fields"`
	// Raw is the raw log line representation.
	Raw string `json:"raw"`
	// Truncated reports whether Raw or Fields were shortened.
	Truncated bool `json:"truncated"`
}

// ApplicationLogStreamEvent is one NDJSON event from the ordinary app log stream route.
type ApplicationLogStreamEvent struct {
	// Type is the stream event type: line, rotation, heartbeat, or error.
	Type string `json:"type"`
	// Cursor is the latest opaque reader cursor.
	Cursor string `json:"cursor,omitempty"`
	// Item is present on line events.
	Item *ApplicationLogEntryDTO `json:"item,omitempty"`
	// Rotated reports source rotation on rotation events.
	Rotated bool `json:"rotated,omitempty"`
	// Error is the stable error code on error events.
	Error string `json:"error,omitempty"`
	// Message is the human-readable error message on error events.
	Message string `json:"message,omitempty"`
}

func (s *Server) handleApplicationLogSources(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseApplicationLogSourcesRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	resp, err := s.management.ApplicationLogSources(c.Request.Context(), req)
	if err != nil {
		writeApplicationLogError(c, err)
		return
	}
	c.JSON(http.StatusOK, applicationLogSourcesDTO(resp))
}

func (s *Server) handleApplicationLogEntries(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseApplicationLogEntriesRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	resp, err := s.management.ApplicationLogEntries(c.Request.Context(), req)
	if err != nil {
		writeApplicationLogError(c, err)
		return
	}
	c.JSON(http.StatusOK, applicationLogEntriesDTO(resp))
}

func (s *Server) handleApplicationLogStream(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseApplicationLogEntriesRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")

	resp, err := s.management.ApplicationLogEntries(c.Request.Context(), req)
	if err != nil {
		writeApplicationLogStreamEvent(c, ApplicationLogStreamEvent{
			Type:    "error",
			Error:   applicationLogErrorCode(err),
			Message: applicationLogErrorMessage(err),
		})
		return
	}
	if len(resp.Items) > 0 {
		for _, item := range applicationLogEntryDTOs(resp.Items) {
			entry := item
			writeApplicationLogStreamEvent(c, ApplicationLogStreamEvent{
				Type:   "line",
				Cursor: resp.Cursor,
				Item:   &entry,
			})
		}
		return
	}
	if resp.Rotated {
		writeApplicationLogStreamEvent(c, ApplicationLogStreamEvent{
			Type:    "rotation",
			Cursor:  resp.Cursor,
			Rotated: true,
		})
		return
	}
	writeApplicationLogStreamEvent(c, ApplicationLogStreamEvent{
		Type:   "heartbeat",
		Cursor: resp.Cursor,
	})
}

func parseApplicationLogSourcesRequest(c *gin.Context) (managementusecase.ApplicationLogSourcesRequest, error) {
	nodeID, err := parseRequiredApplicationLogNodeID(c.Query("node_id"))
	if err != nil {
		return managementusecase.ApplicationLogSourcesRequest{}, errors.New("invalid node_id")
	}
	return managementusecase.ApplicationLogSourcesRequest{NodeID: nodeID}, nil
}

func parseApplicationLogEntriesRequest(c *gin.Context) (managementusecase.ApplicationLogEntriesRequest, error) {
	nodeID, err := parseRequiredApplicationLogNodeID(c.Query("node_id"))
	if err != nil {
		return managementusecase.ApplicationLogEntriesRequest{}, errors.New("invalid node_id")
	}
	limit, err := parseApplicationLogLimit(c.Query("limit"))
	if err != nil {
		return managementusecase.ApplicationLogEntriesRequest{}, errors.New("invalid limit")
	}
	source := strings.TrimSpace(c.Query("source"))
	if source == "" {
		source = applog.AppLogSourceApp
	}
	return managementusecase.ApplicationLogEntriesRequest{
		NodeID:  nodeID,
		Source:  source,
		Limit:   limit,
		Cursor:  c.Query("cursor"),
		Keyword: c.Query("keyword"),
		Levels:  parseApplicationLogLevels(c.Query("levels")),
	}, nil
}

func parseRequiredApplicationLogNodeID(raw string) (uint64, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseApplicationLogLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseApplicationLogLevels(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	levels := make([]string, 0, len(parts))
	for _, part := range parts {
		level := strings.TrimSpace(part)
		if level != "" {
			levels = append(levels, level)
		}
	}
	return levels
}

func writeApplicationLogError(c *gin.Context, err error) {
	switch {
	case isApplicationLogBadRequest(err):
		jsonError(c, http.StatusBadRequest, "bad_request", applicationLogErrorMessage(err))
	case isApplicationLogNotFound(err):
		jsonError(c, http.StatusNotFound, "not_found", "application log not found")
	case errors.Is(err, managementusecase.ErrApplicationLogReaderUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "application log reader unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", "application log read failed")
	}
}

func isApplicationLogBadRequest(err error) bool {
	return errors.Is(err, metadb.ErrInvalidArgument) ||
		errors.Is(err, applog.ErrAppLogInvalidSource) ||
		errors.Is(err, applog.ErrAppLogInvalidCursor)
}

func isApplicationLogNotFound(err error) bool {
	return errors.Is(err, metadb.ErrNotFound) || errors.Is(err, applog.ErrAppLogNotFound)
}

func applicationLogErrorCode(err error) string {
	switch {
	case isApplicationLogBadRequest(err):
		return "bad_request"
	case isApplicationLogNotFound(err):
		return "not_found"
	case errors.Is(err, managementusecase.ErrApplicationLogReaderUnavailable):
		return "service_unavailable"
	default:
		return "internal_error"
	}
}

func applicationLogErrorMessage(err error) string {
	switch {
	case errors.Is(err, applog.ErrAppLogInvalidSource):
		return "invalid application log source"
	case errors.Is(err, applog.ErrAppLogInvalidCursor):
		return "invalid application log cursor"
	case errors.Is(err, metadb.ErrInvalidArgument):
		return "invalid application log request"
	case isApplicationLogNotFound(err):
		return "application log not found"
	case errors.Is(err, managementusecase.ErrApplicationLogReaderUnavailable):
		return "application log reader unavailable"
	default:
		return "application log read failed"
	}
}

func applicationLogSourcesDTO(resp managementusecase.ApplicationLogSourcesResponse) ApplicationLogSourcesResponse {
	return ApplicationLogSourcesResponse{
		NodeID:  resp.NodeID,
		Sources: applicationLogSourceDTOs(resp.Sources),
	}
}

func applicationLogSourceDTOs(items []managementusecase.ApplicationLogSource) []ApplicationLogSourceDTO {
	out := make([]ApplicationLogSourceDTO, 0, len(items))
	for _, item := range items {
		dto := ApplicationLogSourceDTO{
			Name:      item.Name,
			File:      item.File,
			Available: item.Available,
			SizeBytes: item.SizeBytes,
		}
		if !item.ModifiedAt.IsZero() {
			dto.ModifiedAt = item.ModifiedAt.Format(rfc3339NanoLayout)
		}
		out = append(out, dto)
	}
	return out
}

func applicationLogEntriesDTO(resp managementusecase.ApplicationLogEntriesResponse) ApplicationLogEntriesResponse {
	return ApplicationLogEntriesResponse{
		NodeID:  resp.NodeID,
		Source:  resp.Source,
		Cursor:  resp.Cursor,
		Rotated: resp.Rotated,
		Items:   applicationLogEntryDTOs(resp.Items),
	}
}

func applicationLogEntryDTOs(items []managementusecase.ApplicationLogEntry) []ApplicationLogEntryDTO {
	out := make([]ApplicationLogEntryDTO, 0, len(items))
	for _, item := range items {
		dto := ApplicationLogEntryDTO{
			Seq:       item.Seq,
			Offset:    item.Offset,
			Level:     item.Level,
			Module:    item.Module,
			Caller:    item.Caller,
			Message:   item.Message,
			Fields:    item.Fields,
			Raw:       item.Raw,
			Truncated: item.Truncated,
		}
		if !item.Time.IsZero() {
			dto.Time = item.Time.Format(rfc3339NanoLayout)
		}
		out = append(out, dto)
	}
	return out
}

func writeApplicationLogStreamEvent(c *gin.Context, event ApplicationLogStreamEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = c.Writer.Write(append(body, '\n'))
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

const rfc3339NanoLayout = "2006-01-02T15:04:05.999999999Z07:00"
