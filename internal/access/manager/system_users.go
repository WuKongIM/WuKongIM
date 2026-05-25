package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// SystemUsersResponse is the manager system UID list response body.
type SystemUsersResponse struct {
	// Items contains persisted system UID rows.
	Items []SystemUserDTO `json:"items"`
	// Total is the number of returned rows.
	Total int `json:"total"`
}

// SystemUserDTO is one manager-facing system UID row.
type SystemUserDTO struct {
	// UID is the system account user identifier.
	UID string `json:"uid"`
}

// MutateSystemUsersResponseDTO is the manager system UID mutation response body.
type MutateSystemUsersResponseDTO struct {
	// UIDs contains normalized UID values accepted by the mutation.
	UIDs []string `json:"uids"`
	// Changed reports whether the mutation was accepted.
	Changed bool `json:"changed"`
}

type mutateSystemUsersBody struct {
	UIDs []string `json:"uids"`
}

func (s *Server) handleSystemUsers(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	resp, err := s.management.ListSystemUsers(c.Request.Context())
	if err != nil {
		writeSystemUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, systemUsersResponseDTO(resp))
}

func (s *Server) handleSystemUsersAdd(c *gin.Context) {
	s.handleSystemUsersMutation(c, true)
}

func (s *Server) handleSystemUsersRemove(c *gin.Context) {
	s.handleSystemUsersMutation(c, false)
}

func (s *Server) handleSystemUsersMutation(c *gin.Context, add bool) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body mutateSystemUsersBody
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid system user request")
		return
	}
	req := managementusecase.MutateSystemUsersRequest{UIDs: body.UIDs}
	var resp managementusecase.MutateSystemUsersResponse
	var err error
	if add {
		resp, err = s.management.AddSystemUsers(c.Request.Context(), req)
	} else {
		resp, err = s.management.RemoveSystemUsers(c.Request.Context(), req)
	}
	if err != nil {
		writeSystemUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, mutateSystemUsersResponseDTO(resp))
}

func systemUsersResponseDTO(resp managementusecase.ListSystemUsersResponse) SystemUsersResponse {
	items := make([]SystemUserDTO, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, SystemUserDTO{UID: item.UID})
	}
	return SystemUsersResponse{Items: items, Total: resp.Total}
}

func mutateSystemUsersResponseDTO(resp managementusecase.MutateSystemUsersResponse) MutateSystemUsersResponseDTO {
	return MutateSystemUsersResponseDTO{UIDs: resp.UIDs, Changed: resp.Changed}
}

func writeSystemUserError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid system user request")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
