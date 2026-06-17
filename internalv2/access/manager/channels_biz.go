package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

const (
	defaultBusinessChannelsLimit = 50
	maxBusinessChannelsLimit     = 200
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

func writeBusinessChannelListError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
	case errors.Is(err, managementusecase.ErrBusinessChannelReaderUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "channel reader unavailable")
	case controlSnapshotUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller snapshot unavailable")
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
