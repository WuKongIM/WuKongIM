package manager

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

// ConnectionsResponse is the manager local connection list response body.
type ConnectionsResponse struct {
	// Total is the number of returned local connections.
	Total int `json:"total"`
	// Items contains the ordered local connection DTO list.
	Items []ConnectionDTO `json:"items"`
}

// ConnectionDTO is the manager-facing local connection response item.
type ConnectionDTO struct {
	// SessionID is the gateway session identifier.
	SessionID uint64 `json:"session_id"`
	// UID is the authenticated user identifier.
	UID string `json:"uid"`
	// DeviceID is the client device identifier.
	DeviceID string `json:"device_id"`
	// DeviceFlag is the stable device flag label.
	DeviceFlag string `json:"device_flag"`
	// DeviceLevel is the stable device level label.
	DeviceLevel string `json:"device_level"`
	// SlotID is the local authoritative slot identifier for the user.
	SlotID uint64 `json:"slot_id"`
	// State is the local runtime connection state.
	State string `json:"state"`
	// Listener is the listener that accepted the connection.
	Listener string `json:"listener"`
	// ConnectedAt is the initial local connection time.
	ConnectedAt time.Time `json:"connected_at"`
	// RemoteAddr is the client remote address observed by the listener.
	RemoteAddr string `json:"remote_addr"`
	// LocalAddr is the local listener address observed by the listener.
	LocalAddr string `json:"local_addr"`
}

func (s *Server) handleConnections(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	items, err := s.management.ListConnections(c.Request.Context())
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, ConnectionsResponse{
		Total: len(items),
		Items: connectionDTOs(items),
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

	item, err := s.management.GetConnection(c.Request.Context(), sessionID)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			jsonError(c, http.StatusNotFound, "not_found", "connection not found")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, connectionDTO(item))
}

func connectionDTOs(items []managementusecase.Connection) []ConnectionDTO {
	out := make([]ConnectionDTO, 0, len(items))
	for _, item := range items {
		out = append(out, connectionDTO(item))
	}
	return out
}

func connectionDTO(item managementusecase.Connection) ConnectionDTO {
	return ConnectionDTO{
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
