package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

const (
	defaultUsersLimit = 50
	maxUsersLimit     = 200
)

// UsersListResponse is the manager user page response body.
type UsersListResponse struct {
	// Items contains the ordered page items.
	Items []UserListItemDTO `json:"items"`
	// HasMore reports whether another page exists.
	HasMore bool `json:"has_more"`
	// NextCursor is the opaque cursor for the next page when HasMore is true.
	NextCursor string `json:"next_cursor,omitempty"`
}

// UserListItemDTO is the manager-facing user list response item.
type UserListItemDTO struct {
	// UID is the user identifier.
	UID string `json:"uid"`
	// SlotID is the owning physical slot identifier.
	SlotID uint32 `json:"slot_id"`
	// HashSlot is the logical hash slot derived from the UID.
	HashSlot uint16 `json:"hash_slot"`
	// Online reports whether at least one online route exists.
	Online bool `json:"online"`
	// OnlineDeviceCount counts distinct online device flags.
	OnlineDeviceCount int `json:"online_device_count"`
	// OnlineDeviceFlags lists stable manager-facing online device flags.
	OnlineDeviceFlags []string `json:"online_device_flags"`
	// DeviceCount counts stored device token rows.
	DeviceCount int `json:"device_count"`
	// TokenSetCount counts stored device token rows with non-empty tokens.
	TokenSetCount int `json:"token_set_count"`
}

// UserDetailDTO is the manager-facing user detail response body.
type UserDetailDTO struct {
	// UID is the user identifier.
	UID string `json:"uid"`
	// SlotID is the owning physical slot identifier.
	SlotID uint32 `json:"slot_id"`
	// HashSlot is the logical hash slot derived from the UID.
	HashSlot uint16 `json:"hash_slot"`
	// Online reports whether at least one online route exists.
	Online bool `json:"online"`
	// Devices lists stored and online device summaries.
	Devices []UserDeviceDTO `json:"devices"`
	// Connections lists currently known online routes.
	Connections []ConnectionDTO `json:"connections"`
}

// UserDeviceDTO is the manager-facing stored-device summary response body.
type UserDeviceDTO struct {
	// DeviceFlag is the stable manager-facing device flag.
	DeviceFlag string `json:"device_flag"`
	// DeviceLevel is the stable manager-facing device level.
	DeviceLevel string `json:"device_level"`
	// TokenSet reports whether the stored token is non-empty.
	TokenSet bool `json:"token_set"`
	// Online reports whether at least one route exists for this device flag.
	Online bool `json:"online"`
	// OnlineSessionCount counts online sessions for this device flag.
	OnlineSessionCount int `json:"online_session_count"`
}

// KickUserResponseDTO is the manager force-offline response body.
type KickUserResponseDTO struct {
	// UID is the user identifier.
	UID string `json:"uid"`
	// DeviceFlag is the normalized device flag label.
	DeviceFlag string `json:"device_flag"`
	// Changed reports whether the action was accepted.
	Changed bool `json:"changed"`
}

// ResetUserTokenResponseDTO is the manager token reset response body.
type ResetUserTokenResponseDTO struct {
	// UID is the user identifier.
	UID string `json:"uid"`
	// DeviceFlag is the normalized device flag label.
	DeviceFlag string `json:"device_flag"`
	// DeviceLevel is the normalized device level label.
	DeviceLevel string `json:"device_level"`
	// Token is the new token and is only returned by this action.
	Token string `json:"token"`
}

type kickUserRequestBody struct {
	DeviceFlag string `json:"device_flag"`
}

type resetUserTokenRequestBody struct {
	DeviceFlag  string `json:"device_flag"`
	DeviceLevel string `json:"device_level"`
	Token       string `json:"token"`
}

func (s *Server) handleUsers(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	limit, err := parseUsersLimit(c.Query("limit"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid limit")
		return
	}
	cursor, err := decodeUserListCursor(c.Query("cursor"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
		return
	}
	page, err := s.management.ListUsers(c.Request.Context(), managementusecase.ListUsersRequest{
		Limit:   limit,
		Cursor:  cursor,
		Keyword: strings.TrimSpace(c.Query("keyword")),
	})
	if err != nil {
		writeUserError(c, err, "invalid cursor", "users not found")
		return
	}
	nextCursor, err := encodeUserListCursor(page.NextCursor)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, UsersListResponse{
		Items:      userListItemDTOs(page.Items),
		HasMore:    page.HasMore,
		NextCursor: nextCursor,
	})
}

func (s *Server) handleUser(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	item, err := s.management.GetUser(c.Request.Context(), c.Param("uid"))
	if err != nil {
		writeUserError(c, err, "invalid uid", "user not found")
		return
	}
	c.JSON(http.StatusOK, userDetailDTO(item))
}

func (s *Server) handleUserKick(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	var body kickUserRequestBody
	if err := bindOptionalJSON(c, &body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	resp, err := s.management.KickUser(c.Request.Context(), managementusecase.KickUserRequest{
		UID:        c.Param("uid"),
		DeviceFlag: body.DeviceFlag,
	})
	if err != nil {
		writeUserError(c, err, "invalid device_flag", "user not found")
		return
	}
	c.JSON(http.StatusOK, kickUserResponseDTO(resp))
}

func (s *Server) handleUserTokenReset(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	var body resetUserTokenRequestBody
	if err := bindOptionalJSON(c, &body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	resp, err := s.management.ResetUserToken(c.Request.Context(), managementusecase.ResetUserTokenRequest{
		UID:         c.Param("uid"),
		DeviceFlag:  body.DeviceFlag,
		DeviceLevel: body.DeviceLevel,
		Token:       body.Token,
	})
	if err != nil {
		writeUserError(c, err, "invalid device token reset request", "user not found")
		return
	}
	c.JSON(http.StatusOK, resetUserTokenResponseDTO(resp))
}

func parseUsersLimit(raw string) (int, error) {
	if raw == "" {
		return defaultUsersLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > maxUsersLimit {
		return 0, strconv.ErrSyntax
	}
	return limit, nil
}

func writeUserError(c *gin.Context, err error, badRequestMessage, notFoundMessage string) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", badRequestMessage)
	case errors.Is(err, metadb.ErrNotFound):
		jsonError(c, http.StatusNotFound, "not_found", notFoundMessage)
	case slotLeaderAuthoritativeReadUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot leader authoritative read unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func userListItemDTOs(items []managementusecase.UserListItem) []UserListItemDTO {
	out := make([]UserListItemDTO, 0, len(items))
	for _, item := range items {
		out = append(out, userListItemDTO(item))
	}
	return out
}

func userListItemDTO(item managementusecase.UserListItem) UserListItemDTO {
	return UserListItemDTO{
		UID:               item.UID,
		SlotID:            item.SlotID,
		HashSlot:          item.HashSlot,
		Online:            item.Online,
		OnlineDeviceCount: item.OnlineDeviceCount,
		OnlineDeviceFlags: append([]string(nil), item.OnlineDeviceFlags...),
		DeviceCount:       item.DeviceCount,
		TokenSetCount:     item.TokenSetCount,
	}
}

func userDetailDTO(item managementusecase.UserDetail) UserDetailDTO {
	return UserDetailDTO{
		UID:         item.UID,
		SlotID:      item.SlotID,
		HashSlot:    item.HashSlot,
		Online:      item.Online,
		Devices:     userDeviceDTOs(item.Devices),
		Connections: connectionDTOs(item.Connections),
	}
}

func userDeviceDTOs(items []managementusecase.UserDevice) []UserDeviceDTO {
	out := make([]UserDeviceDTO, 0, len(items))
	for _, item := range items {
		out = append(out, UserDeviceDTO{
			DeviceFlag:         item.DeviceFlag,
			DeviceLevel:        item.DeviceLevel,
			TokenSet:           item.TokenSet,
			Online:             item.Online,
			OnlineSessionCount: item.OnlineSessionCount,
		})
	}
	return out
}

func kickUserResponseDTO(resp managementusecase.KickUserResponse) KickUserResponseDTO {
	return KickUserResponseDTO{UID: resp.UID, DeviceFlag: resp.DeviceFlag, Changed: resp.Changed}
}

func resetUserTokenResponseDTO(resp managementusecase.ResetUserTokenResponse) ResetUserTokenResponseDTO {
	return ResetUserTokenResponseDTO{UID: resp.UID, DeviceFlag: resp.DeviceFlag, DeviceLevel: resp.DeviceLevel, Token: resp.Token}
}
