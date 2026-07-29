package manager

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

const (
	defaultBusinessChannelsLimit       = 50
	maxBusinessChannelsLimit           = 200
	defaultBusinessChannelMembersLimit = 100
	maxBusinessChannelMembersLimit     = 500
	maxBusinessChannelJSONBodyBytes    = 1 << 20
	businessMemberListSubscribers      = "subscribers"
	businessMemberListAllowlist        = "allowlist"
	businessMemberListDenylist         = "denylist"
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

// BusinessChannelDetailDTO is one authoritative business channel detail.
type BusinessChannelDetailDTO struct {
	// BusinessChannelListItemDTO contains the channel summary fields.
	BusinessChannelListItemDTO
	// HasSubscribers reports whether the ordinary subscriber set is non-empty.
	HasSubscribers bool `json:"has_subscribers"`
	// HasAllowlist reports whether the allowlist is non-empty.
	HasAllowlist bool `json:"has_allowlist"`
	// HasDenylist reports whether the denylist is non-empty.
	HasDenylist bool `json:"has_denylist"`
}

// BusinessChannelMembersResponse is one member page or exact lookup result.
type BusinessChannelMembersResponse struct {
	// Items contains the ordered UID-only rows.
	Items []BusinessChannelMemberDTO `json:"items"`
	// HasMore reports whether another page exists.
	HasMore bool `json:"has_more"`
	// NextCursor is the opaque next-page cursor when HasMore is true.
	NextCursor string `json:"next_cursor,omitempty"`
}

// BusinessChannelMemberDTO is one UID-only member row.
type BusinessChannelMemberDTO struct {
	// UID is the exact member identifier.
	UID string `json:"uid"`
}

// MutateBusinessChannelMembersResponseDTO reports processed and changed UIDs.
type MutateBusinessChannelMembersResponseDTO struct {
	// ChannelID is the exact parent channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64 `json:"channel_type"`
	// ListKind identifies subscribers, allowlist, or denylist.
	ListKind string `json:"list"`
	// RequestedCount is the normalized distinct UID count.
	RequestedCount int `json:"requested_count"`
	// ChangedCount is the exact durable set-change count.
	ChangedCount int `json:"changed_count"`
}

type businessChannelCreateBody struct {
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	Ban         bool   `json:"ban"`
	Disband     bool   `json:"disband"`
	SendBan     bool   `json:"send_ban"`
}

type businessChannelUpdateBody struct {
	Ban     bool `json:"ban"`
	Disband bool `json:"disband"`
	SendBan bool `json:"send_ban"`
}

type mutateBusinessChannelMembersBody struct {
	UIDs []string `json:"uids"`
}

func (s *Server) handleBusinessChannels(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseOptionalConnectionNodeID(c.Query("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
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
		NodeID:     nodeID,
		Limit:      limit,
		Cursor:     cursor,
		TypeFilter: typeFilter,
		Keyword:    strings.TrimSpace(c.Query("keyword")),
	})
	if err != nil {
		writeBusinessChannelListError(c, err)
		return
	}
	nextCursor, err := encodeBusinessChannelCursor(page.NextCursor)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, BusinessChannelsListResponse{
		Items:      businessChannelListItemDTOs(page.Items),
		HasMore:    page.HasMore,
		NextCursor: nextCursor,
	})
}

func (s *Server) handleBusinessChannel(c *gin.Context) {
	channelType, ok := parseBusinessChannelTypeParamForRequest(c)
	if !ok {
		return
	}
	channelID, ok := parseBusinessChannelIDParamForRequest(c)
	if !ok {
		return
	}
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	detail, err := s.management.GetBusinessChannel(c.Request.Context(), channelID, channelType)
	if err != nil {
		writeBusinessChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, businessChannelDetailDTO(detail))
}

func (s *Server) handleBusinessChannelCreate(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body businessChannelCreateBody
	if err := bindStrictUTF8JSON(c, &body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel")
		return
	}
	detail, err := s.management.CreateBusinessChannel(c.Request.Context(), managementusecase.CreateBusinessChannelRequest{
		ChannelID: body.ChannelID, ChannelType: body.ChannelType,
		Ban: body.Ban, Disband: body.Disband, SendBan: body.SendBan,
	})
	if err != nil {
		writeBusinessChannelError(c, err)
		return
	}
	c.JSON(http.StatusCreated, businessChannelDetailDTO(detail))
}

func (s *Server) handleBusinessChannelUpdate(c *gin.Context) {
	channelType, ok := parseBusinessChannelTypeParamForRequest(c)
	if !ok {
		return
	}
	channelID, ok := parseBusinessChannelIDParamForRequest(c)
	if !ok {
		return
	}
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body businessChannelUpdateBody
	if err := bindStrictUTF8JSON(c, &body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel")
		return
	}
	detail, err := s.management.UpdateBusinessChannel(c.Request.Context(), managementusecase.UpdateBusinessChannelRequest{
		ChannelID: channelID, ChannelType: channelType,
		Ban: body.Ban, Disband: body.Disband, SendBan: body.SendBan,
	})
	if err != nil {
		writeBusinessChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, businessChannelDetailDTO(detail))
}

func (s *Server) handleBusinessChannelSubscribers(c *gin.Context) {
	s.handleBusinessChannelMembers(c, businessMemberListSubscribers)
}

func (s *Server) handleBusinessChannelAllowlist(c *gin.Context) {
	s.handleBusinessChannelMembers(c, businessMemberListAllowlist)
}

func (s *Server) handleBusinessChannelDenylist(c *gin.Context) {
	s.handleBusinessChannelMembers(c, businessMemberListDenylist)
}

func (s *Server) handleBusinessChannelMembers(c *gin.Context, listKind string) {
	channelType, ok := parseBusinessChannelTypeParamForRequest(c)
	if !ok {
		return
	}
	channelID, ok := parseBusinessChannelIDParamForRequest(c)
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
	if c.Query("uid") != "" && c.Query("cursor") != "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "uid and cursor are mutually exclusive")
		return
	}
	cursor, err := decodeBusinessChannelMemberCursor(c.Query("cursor"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
		return
	}
	page, err := s.management.ListBusinessChannelMembers(c.Request.Context(), managementusecase.ListBusinessChannelMembersRequest{
		ChannelID: channelID, ChannelType: channelType, ListKind: listKind,
		Limit: limit, Cursor: cursor, UID: c.Query("uid"),
	})
	if err != nil {
		writeBusinessChannelError(c, err)
		return
	}
	nextCursor, err := encodeBusinessChannelMemberCursor(page.NextCursor)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, BusinessChannelMembersResponse{
		Items: businessChannelMemberDTOs(page.Items), HasMore: page.HasMore, NextCursor: nextCursor,
	})
}

func (s *Server) handleBusinessChannelSubscribersAdd(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, businessMemberListSubscribers, true)
}

func (s *Server) handleBusinessChannelSubscribersRemove(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, businessMemberListSubscribers, false)
}

func (s *Server) handleBusinessChannelAllowlistAdd(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, businessMemberListAllowlist, true)
}

func (s *Server) handleBusinessChannelAllowlistRemove(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, businessMemberListAllowlist, false)
}

func (s *Server) handleBusinessChannelDenylistAdd(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, businessMemberListDenylist, true)
}

func (s *Server) handleBusinessChannelDenylistRemove(c *gin.Context) {
	s.handleBusinessChannelMemberMutation(c, businessMemberListDenylist, false)
}

func (s *Server) handleBusinessChannelMemberMutation(c *gin.Context, listKind string, add bool) {
	channelType, ok := parseBusinessChannelTypeParamForRequest(c)
	if !ok {
		return
	}
	channelID, ok := parseBusinessChannelIDParamForRequest(c)
	if !ok {
		return
	}
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body mutateBusinessChannelMembersBody
	if err := bindStrictUTF8JSON(c, &body); err != nil {
		s.auditBusinessChannelMemberMutation(c, channelID, channelType, listKind, add, nil, managementusecase.MutateBusinessChannelMembersResponse{}, err)
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid member list request")
		return
	}
	resp, err := s.management.MutateBusinessChannelMembers(c.Request.Context(), managementusecase.MutateBusinessChannelMembersRequest{
		ChannelID: channelID, ChannelType: channelType, ListKind: listKind,
		UIDs: body.UIDs, Add: add,
	})
	s.auditBusinessChannelMemberMutation(c, channelID, channelType, listKind, add, body.UIDs, resp, err)
	if err != nil {
		writeBusinessChannelError(c, err)
		return
	}
	c.JSON(http.StatusOK, MutateBusinessChannelMembersResponseDTO{
		ChannelID: resp.ChannelID, ChannelType: resp.ChannelType, ListKind: resp.ListKind,
		RequestedCount: resp.RequestedCount, ChangedCount: resp.ChangedCount,
	})
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
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 || value > 255 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseBusinessChannelTypeParamForRequest(c *gin.Context) (int64, bool) {
	value, err := strconv.ParseInt(c.Param("channel_type"), 10, 64)
	if err != nil || value <= 0 || value > 255 {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		return 0, false
	}
	return value, true
}

func parseBusinessChannelIDParamForRequest(c *gin.Context) (string, bool) {
	value, err := url.PathUnescape(c.Param("channel_id"))
	if err != nil || value == "" || !utf8.ValidString(value) {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_id")
		return "", false
	}
	return value, true
}

func bindStrictUTF8JSON(c *gin.Context, dst any) error {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBusinessChannelJSONBodyBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxBusinessChannelJSONBodyBytes || !utf8.Valid(body) {
		return metadb.ErrInvalidArgument
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	return c.ShouldBindJSON(dst)
}

func writeBusinessChannelListError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
	case errors.Is(err, managementusecase.ErrBusinessChannelReaderUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "channel reader unavailable")
	case errors.Is(err, managementusecase.ErrBusinessChannelControlUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller snapshot unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func writeBusinessChannelError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid business channel request")
	case errors.Is(err, metadb.ErrNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "channel not found")
	case errors.Is(err, metadb.ErrAlreadyExists):
		jsonError(c, http.StatusConflict, "conflict", "channel state conflict")
	case errors.Is(err, managementusecase.ErrBusinessChannelControlUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller snapshot unavailable")
	case errors.Is(err, managementusecase.ErrBusinessChannelOperatorUnavailable),
		errors.Is(err, managementusecase.ErrBusinessChannelReaderUnavailable),
		errors.Is(err, managementusecase.ErrBusinessChannelAuthorityUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot leader authoritative operation unavailable")
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

func businessChannelDetailDTO(detail managementusecase.BusinessChannelDetail) BusinessChannelDetailDTO {
	return BusinessChannelDetailDTO{
		BusinessChannelListItemDTO: businessChannelListItemDTO(detail.BusinessChannelListItem),
		HasSubscribers:             detail.HasSubscribers,
		HasAllowlist:               detail.HasAllowlist,
		HasDenylist:                detail.HasDenylist,
	}
}

func businessChannelMemberDTOs(items []managementusecase.BusinessChannelMember) []BusinessChannelMemberDTO {
	out := make([]BusinessChannelMemberDTO, 0, len(items))
	for _, item := range items {
		out = append(out, BusinessChannelMemberDTO{UID: item.UID})
	}
	return out
}

func (s *Server) auditBusinessChannelMemberMutation(
	c *gin.Context,
	channelID string,
	channelType int64,
	listKind string,
	add bool,
	uids []string,
	resp managementusecase.MutateBusinessChannelMembersResponse,
	err error,
) {
	if s == nil || s.logger == nil {
		return
	}
	operator := ""
	if value, ok := c.Get(managerUsernameContextKey); ok {
		operator, _ = value.(string)
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	operation := "remove"
	if add {
		operation = "add"
	}
	requestedCount := resp.RequestedCount
	if requestedCount == 0 {
		requestedCount = len(uids)
	}
	fields := []wklog.Field{
		wklog.Event("internal.access.manager.channel_member_mutation"),
		wklog.String("operator", strings.TrimSpace(operator)),
		wklog.String("channel_id", strings.TrimSpace(channelID)),
		wklog.Int64("channel_type", channelType),
		wklog.String("list_kind", listKind),
		wklog.String("operation", operation),
		wklog.Int("requested_count", requestedCount),
		wklog.Int("changed_count", resp.ChangedCount),
		wklog.String("result", result),
		wklog.String("time", time.Now().UTC().Format(time.RFC3339Nano)),
	}
	if len(uids) > 0 {
		fields = append(fields, wklog.String("uid_sample", redactBusinessChannelUIDSample(strings.TrimSpace(uids[0]))))
	}
	if err != nil {
		fields = append(fields, wklog.Error(err))
	}
	s.logger.Info("Manager channel member mutation", fields...)
}

func redactBusinessChannelUIDSample(uid string) string {
	runes := []rune(uid)
	switch len(runes) {
	case 0:
		return ""
	case 1:
		return "*"
	case 2:
		return "**"
	default:
		return string(runes[0]) + "***" + string(runes[len(runes)-1])
	}
}
