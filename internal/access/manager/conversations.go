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
	defaultRecentConversationsLimit   = 50
	maxRecentConversationsLimit       = 200
	defaultRecentConversationMsgCount = 1
	maxRecentConversationMsgCount     = 10
)

// RecentConversationsResponseDTO is the manager recent-conversation response body.
type RecentConversationsResponseDTO struct {
	// UID is the normalized queried user id.
	UID string `json:"uid"`
	// Limit is the applied conversation limit.
	Limit int `json:"limit"`
	// MsgCount is the applied recent-message preview limit.
	MsgCount int `json:"msg_count"`
	// OnlyUnread reports whether unread filtering was applied.
	OnlyUnread bool `json:"only_unread"`
	// Truncated reports whether more matching conversations were detected.
	Truncated bool `json:"truncated"`
	// Items contains recent conversation rows.
	Items []RecentConversationDTO `json:"items"`
}

// RecentConversationDTO is one manager recent-conversation row.
type RecentConversationDTO struct {
	// UID is the owner user for this conversation row.
	UID string `json:"uid"`
	// ChannelID is the display channel id returned by conversation sync.
	ChannelID string `json:"channel_id"`
	// ChannelType is the WuKong channel type.
	ChannelType int64 `json:"channel_type"`
	// Unread counts unread messages for UID in this conversation.
	Unread int `json:"unread"`
	// Timestamp is the latest message timestamp in Unix seconds.
	Timestamp int64 `json:"timestamp"`
	// LastMsgSeq is the latest message sequence known to conversation sync.
	LastMsgSeq uint32 `json:"last_msg_seq"`
	// LastClientMsgNo is the latest client message number when present.
	LastClientMsgNo string `json:"last_client_msg_no"`
	// ReadToMsgSeq is UID's read cursor for this conversation.
	ReadToMsgSeq uint32 `json:"read_to_msg_seq"`
	// Version is the sync compatibility version timestamp.
	Version int64 `json:"version"`
	// RecentMessages contains newest message previews for this conversation.
	RecentMessages []MessageDTO `json:"recent_messages"`
}

func (s *Server) handleConversations(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	uid := strings.TrimSpace(c.Query("uid"))
	if uid == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "uid is required")
		return
	}
	limit, err := parseRecentConversationsLimit(c.Query("limit"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid limit")
		return
	}
	msgCount, err := parseRecentConversationMsgCount(c.Query("msg_count"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid msg_count")
		return
	}
	onlyUnread, err := parseOptionalBool(c.Query("only_unread"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid only_unread")
		return
	}

	result, err := s.management.ListRecentConversations(c.Request.Context(), managementusecase.RecentConversationsRequest{
		UID: uid, Limit: limit, MsgCount: msgCount, OnlyUnread: onlyUnread,
	})
	if err != nil {
		switch {
		case errors.Is(err, metadb.ErrInvalidArgument):
			jsonError(c, http.StatusBadRequest, "bad_request", "invalid conversation query")
		case errors.Is(err, managementusecase.ErrRecentConversationsUnavailable):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "recent conversations unavailable")
		case channelLeaderUnavailable(err):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "channel leader unavailable")
		default:
			jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	c.JSON(http.StatusOK, recentConversationsDTO(result))
}

func parseRecentConversationsLimit(raw string) (int, error) {
	if raw == "" {
		return defaultRecentConversationsLimit, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > maxRecentConversationsLimit {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseRecentConversationMsgCount(raw string) (int, error) {
	if raw == "" {
		return defaultRecentConversationMsgCount, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > maxRecentConversationMsgCount {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseOptionalBool(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	return strconv.ParseBool(raw)
}

func recentConversationsDTO(resp managementusecase.RecentConversationsResponse) RecentConversationsResponseDTO {
	return RecentConversationsResponseDTO{
		UID: resp.UID, Limit: resp.Limit, MsgCount: resp.MsgCount, OnlyUnread: resp.OnlyUnread, Truncated: resp.Truncated,
		Items: recentConversationDTOs(resp.Items),
	}
}

func recentConversationDTOs(items []managementusecase.RecentConversation) []RecentConversationDTO {
	out := make([]RecentConversationDTO, 0, len(items))
	for _, item := range items {
		out = append(out, RecentConversationDTO{
			UID: item.UID, ChannelID: item.ChannelID, ChannelType: int64(item.ChannelType), Unread: item.Unread,
			Timestamp: item.Timestamp, LastMsgSeq: item.LastMsgSeq, LastClientMsgNo: item.LastClientMsgNo,
			ReadToMsgSeq: item.ReadToMsgSeq, Version: item.Version, RecentMessages: messageDTOs(item.RecentMessages),
		})
	}
	return out
}
