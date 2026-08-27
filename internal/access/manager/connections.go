package manager

import (
	"errors"
	"net/http"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

const (
	defaultConnectionListLimit = 100
	maxConnectionListLimit     = 100
)

// ConnectionsResponse is one manager connection inventory page.
type ConnectionsResponse struct {
	// Total is the number of active connections on the selected node.
	Total int `json:"total"`
	// Items contains the ordered connection page.
	Items []ConnectionDTO `json:"items"`
	// HasMore reports whether another older page exists.
	HasMore bool `json:"has_more"`
	// NextCursor is the opaque cursor for the next page when HasMore is true.
	NextCursor string `json:"next_cursor,omitempty"`
}

func (s *Server) handleConnections(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, badRequestMessage, err := parseListConnectionsRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", badRequestMessage)
		return
	}
	page, err := s.management.ListConnections(c.Request.Context(), req)
	if err != nil {
		writeConnectionError(c, err, "invalid node_id")
		return
	}
	nextCursor, err := encodeConnectionListCursor(page.NextCursor)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, ConnectionsResponse{
		Total:      page.Total,
		Items:      connectionDTOs(page.Items),
		HasMore:    page.HasMore,
		NextCursor: nextCursor,
	})
}

func (s *Server) handleConnection(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	sessionID, err := parseSessionIDParam(c.Param("session_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid session_id")
		return
	}
	nodeID, err := parseOptionalConnectionNodeID(c.Query("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	item, err := s.management.GetConnection(c.Request.Context(), managementusecase.GetConnectionRequest{
		NodeID:    nodeID,
		SessionID: sessionID,
	})
	if err != nil {
		writeConnectionError(c, err, "invalid session_id")
		return
	}
	c.JSON(http.StatusOK, connectionDTO(item))
}

func parseListConnectionsRequest(c *gin.Context) (managementusecase.ListConnectionsRequest, string, error) {
	nodeID, err := parseOptionalConnectionNodeID(c.Query("node_id"))
	if err != nil {
		return managementusecase.ListConnectionsRequest{}, "invalid node_id", err
	}
	limit, err := parseConnectionListLimit(c.Query("limit"))
	if err != nil {
		return managementusecase.ListConnectionsRequest{}, "invalid limit", err
	}
	cursor, err := decodeConnectionListCursor(c.Query("cursor"))
	if err != nil {
		return managementusecase.ListConnectionsRequest{}, "invalid cursor", err
	}
	return managementusecase.ListConnectionsRequest{NodeID: nodeID, Limit: limit, Cursor: cursor}, "", nil
}

func parseConnectionListLimit(raw string) (int, error) {
	if raw == "" {
		return defaultConnectionListLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > maxConnectionListLimit {
		return 0, strconv.ErrSyntax
	}
	return limit, nil
}

func parseOptionalConnectionNodeID(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseSessionIDParam(raw string) (uint64, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func writeConnectionError(c *gin.Context, err error, badRequestMessage string) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", badRequestMessage)
	case errors.Is(err, metadb.ErrNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "connection not found")
	case errors.Is(err, managementusecase.ErrConnectionReaderUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "connection reader unavailable")
	case controlSnapshotUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller snapshot unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func connectionDTO(item managementusecase.Connection) ConnectionDTO {
	return ConnectionDTO{
		NodeID:      item.NodeID,
		SessionID:   item.SessionID,
		UID:         item.UID,
		DeviceID:    item.DeviceID,
		DeviceFlag:  item.DeviceFlag,
		DeviceLevel: item.DeviceLevel,
		SlotID:      item.SlotID,
		State:       item.State,
		Listener:    item.Listener,
		ConnectedAt: item.ConnectedAt,
		RemoteAddr:  item.RemoteAddr,
		LocalAddr:   item.LocalAddr,
	}
}
