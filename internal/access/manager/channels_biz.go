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
	defaultBusinessChannelsLimit       = 50
	maxBusinessChannelsLimit           = 200
	defaultBusinessChannelMembersLimit = 100
	maxBusinessChannelMembersLimit     = 500
)

// BusinessChannelsListResponse is the manager business channel page body.
type BusinessChannelsListResponse struct {
	// Items contains the ordered page items.
	Items []BusinessChannelListItemDTO `json:"items"`
	// HasMore reports whether another page exists.
	HasMore bool `json:"has_more"`
	// NextCursor is the opaque cursor for the next page when HasMore is true.
	NextCursor string `json:"next_cursor,omitempty"`
}

// BusinessChannelListItemDTO is the manager-facing business channel summary.
type BusinessChannelListItemDTO struct {
	// ChannelID is the logical channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64 `json:"channel_type"`
	// SlotID is the owning physical slot identifier.
	SlotID uint32 `json:"slot_id"`
	// HashSlot is the logical hash slot derived from the channel ID.
	HashSlot uint16 `json:"hash_slot"`
	// Ban reports whether the channel is banned.
	Ban bool `json:"ban"`
	// Disband reports whether the channel is disbanded.
	Disband bool `json:"disband"`
	// SendBan reports whether sending is blocked for the channel.
	SendBan bool `json:"send_ban"`
	// SubscriberMutationVersion is the durable subscriber mutation fence.
	SubscriberMutationVersion uint64 `json:"subscriber_mutation_version"`
}

// BusinessChannelDetailDTO is the manager-facing business channel detail.
type BusinessChannelDetailDTO struct {
	BusinessChannelListItemDTO
	// HasSubscribers reports whether the ordinary subscriber list is non-empty.
	HasSubscribers bool `json:"has_subscribers"`
	// HasAllowlist reports whether the allowlist is non-empty.
	HasAllowlist bool `json:"has_allowlist"`
	// HasDenylist reports whether the denylist is non-empty.
	HasDenylist bool `json:"has_denylist"`
}

// BusinessChannelMembersResponse is the manager channel member page body.
type BusinessChannelMembersResponse struct {
	// Items contains the ordered page items.
	Items []BusinessChannelMemberDTO `json:"items"`
	// HasMore reports whether another page exists.
	HasMore bool `json:"has_more"`
	// NextCursor is the opaque cursor for the next page when HasMore is true.
	NextCursor string `json:"next_cursor,omitempty"`
}

// BusinessChannelMemberDTO is one manager-facing channel member.
type BusinessChannelMemberDTO struct {
	// UID is the member user identifier.
	UID string `json:"uid"`
}

// MutateBusinessChannelMembersResponseDTO is the manager member mutation response.
type MutateBusinessChannelMembersResponseDTO struct {
	// ChannelID is the logical channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64 `json:"channel_type"`
	// ListKind is subscribers, allowlist, or denylist.
	ListKind string `json:"list"`
	// Changed reports whether the mutation was accepted.
	Changed bool `json:"changed"`
}

type businessChannelUpsertBody struct {
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	Ban         bool   `json:"ban"`
	Disband     bool   `json:"disband"`
	SendBan     bool   `json:"send_ban"`
}

type mutateBusinessChannelMembersBody struct {
	UIDs []string `json:"uids"`
}

func (s *Server) handleBusinessChannels(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	limit, err := parseBusinessChannelsLimit(c.Query("limit"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid limit")
		return
	}
	typeFilter, err := parseOptionalBusinessChannelType(c.Query("type"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid type")
		return
	}
	cursor, err := decodeBusinessChannelCursor(c.Query("cursor"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
		return
	}
	page, err := s.management.ListBusinessChannels(c.Request.Context(), managementusecase.ListBusinessChannelsRequest{
		Limit:      limit,
		Cursor:     cursor,
		TypeFilter: typeFilter,
		Keyword:    strings.TrimSpace(c.Query("keyword")),
	})
	if err != nil {
		writeBusinessChannelError(c, err, "invalid cursor", "channel not found")
		return
	}
	nextCursor, err := encodeBusinessChannelCursor(page.NextCursor)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, BusinessChannelsListResponse{Items: businessChannelListItemDTOs(page.Items), HasMore: page.HasMore, NextCursor: nextCursor})
}

func (s *Server) handleBusinessChannel(c *gin.Context) {
	channelType, ok := parseBusinessChannelTypeParamForRequest(c)
	if !ok {
		return
	}
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	item, err := s.management.GetBusinessChannel(c.Request.Context(), c.Param("channel_id"), channelType)
	if err != nil {
		writeBusinessChannelError(c, err, "invalid channel", "channel not found")
		return
	}
	c.JSON(http.StatusOK, businessChannelDetailDTO(item))
}

func (s *Server) handleBusinessChannelUpsert(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body businessChannelUpsertBody
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel")
		return
	}
	if _, err := validateBusinessChannelType(body.ChannelType); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		return
	}
	item, err := s.management.UpsertBusinessChannel(c.Request.Context(), managementusecase.UpsertBusinessChannelRequest{
		ChannelID:   body.ChannelID,
		ChannelType: body.ChannelType,
		Ban:         body.Ban,
		Disband:     body.Disband,
		SendBan:     body.SendBan,
	})
	if err != nil {
		writeBusinessChannelError(c, err, "invalid channel", "channel not found")
		return
	}
	c.JSON(http.StatusOK, businessChannelDetailDTO(item))
}

func (s *Server) handleBusinessChannelSubscribers(c *gin.Context) {
	s.handleBusinessChannelMembers(c, "subscribers")
}

func (s *Server) handleBusinessChannelAllowlist(c *gin.Context) {
	s.handleBusinessChannelMembers(c, "allowlist")
}

func (s *Server) handleBusinessChannelDenylist(c *gin.Context) {
	s.handleBusinessChannelMembers(c, "denylist")
}

func (s *Server) handleBusinessChannelSubscribersAdd(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, "subscribers", true)
}

func (s *Server) handleBusinessChannelSubscribersRemove(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, "subscribers", false)
}

func (s *Server) handleBusinessChannelAllowlistAdd(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, "allowlist", true)
}

func (s *Server) handleBusinessChannelAllowlistRemove(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, "allowlist", false)
}

func (s *Server) handleBusinessChannelDenylistAdd(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, "denylist", true)
}

func (s *Server) handleBusinessChannelDenylistRemove(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, "denylist", false)
}

func (s *Server) handleBusinessChannelMembers(c *gin.Context, listKind string) {
	channelType, ok := parseBusinessChannelTypeParamForRequest(c)
	if !ok {
		return
	}
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	limit, err := parseBusinessChannelMembersLimit(c.Query("limit"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid limit")
		return
	}
	cursor, err := decodeBusinessChannelMemberCursor(c.Query("cursor"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
		return
	}
	page, err := s.management.ListBusinessChannelMembers(c.Request.Context(), managementusecase.ListBusinessChannelMembersRequest{
		ChannelID:   c.Param("channel_id"),
		ChannelType: channelType,
		ListKind:    listKind,
		Limit:       limit,
		Cursor:      cursor,
	})
	if err != nil {
		writeBusinessChannelError(c, err, "invalid member list request", "channel not found")
		return
	}
	nextCursor, err := encodeBusinessChannelMemberCursor(page.NextCursor)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, BusinessChannelMembersResponse{Items: businessChannelMemberDTOs(page.Items), HasMore: page.HasMore, NextCursor: nextCursor})
}

func (s *Server) handleBusinessChannelMemberMutation(c *gin.Context, listKind string, add bool) {
	channelType, ok := parseBusinessChannelTypeParamForRequest(c)
	if !ok {
		return
	}
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body mutateBusinessChannelMembersBody
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid member list request")
		return
	}
	resp, err := s.management.MutateBusinessChannelMembers(c.Request.Context(), managementusecase.MutateBusinessChannelMembersRequest{
		ChannelID:   c.Param("channel_id"),
		ChannelType: channelType,
		ListKind:    listKind,
		UIDs:        body.UIDs,
		Add:         add,
	})
	if err != nil {
		writeBusinessChannelError(c, err, "invalid member list request", "channel not found")
		return
	}
	c.JSON(http.StatusOK, mutateBusinessChannelMembersResponseDTO(resp))
}

func parseBusinessChannelsLimit(raw string) (int, error) {
	if raw == "" {
		return defaultBusinessChannelsLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > maxBusinessChannelsLimit {
		return 0, strconv.ErrSyntax
	}
	return limit, nil
}

func parseBusinessChannelMembersLimit(raw string) (int, error) {
	if raw == "" {
		return defaultBusinessChannelMembersLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > maxBusinessChannelMembersLimit {
		return 0, strconv.ErrSyntax
	}
	return limit, nil
}

func parseOptionalBusinessChannelType(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	return validateBusinessChannelTypeString(raw)
}

func parseBusinessChannelTypeParamForRequest(c *gin.Context) (int64, bool) {
	channelType, err := validateBusinessChannelTypeString(c.Param("channel_type"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		return 0, false
	}
	return channelType, true
}

func validateBusinessChannelTypeString(raw string) (int64, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return validateBusinessChannelType(value)
}

func validateBusinessChannelType(value int64) (int64, error) {
	if value <= 0 || value > 255 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func writeBusinessChannelError(c *gin.Context, err error, badRequestMessage, notFoundMessage string) {
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

func businessChannelListItemDTOs(items []managementusecase.BusinessChannelListItem) []BusinessChannelListItemDTO {
	out := make([]BusinessChannelListItemDTO, 0, len(items))
	for _, item := range items {
		out = append(out, businessChannelListItemDTO(item))
	}
	return out
}

func businessChannelListItemDTO(item managementusecase.BusinessChannelListItem) BusinessChannelListItemDTO {
	return BusinessChannelListItemDTO{
		ChannelID:                 item.ChannelID,
		ChannelType:               item.ChannelType,
		SlotID:                    item.SlotID,
		HashSlot:                  item.HashSlot,
		Ban:                       item.Ban,
		Disband:                   item.Disband,
		SendBan:                   item.SendBan,
		SubscriberMutationVersion: item.SubscriberMutationVersion,
	}
}

func businessChannelDetailDTO(item managementusecase.BusinessChannelDetail) BusinessChannelDetailDTO {
	return BusinessChannelDetailDTO{
		BusinessChannelListItemDTO: businessChannelListItemDTO(item.BusinessChannelListItem),
		HasSubscribers:             item.HasSubscribers,
		HasAllowlist:               item.HasAllowlist,
		HasDenylist:                item.HasDenylist,
	}
}

func businessChannelMemberDTOs(items []managementusecase.BusinessChannelMember) []BusinessChannelMemberDTO {
	out := make([]BusinessChannelMemberDTO, 0, len(items))
	for _, item := range items {
		out = append(out, BusinessChannelMemberDTO{UID: item.UID})
	}
	return out
}

func mutateBusinessChannelMembersResponseDTO(resp managementusecase.MutateBusinessChannelMembersResponse) MutateBusinessChannelMembersResponseDTO {
	return MutateBusinessChannelMembersResponseDTO{ChannelID: resp.ChannelID, ChannelType: resp.ChannelType, ListKind: resp.ListKind, Changed: resp.Changed}
}
