package manager

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/gin-gonic/gin"
)

const (
	defaultMessageLimit = 50
	maxMessageLimit     = 200
)

// MessageListResponse is the manager message page body.
type MessageListResponse struct {
	// Items contains the ordered message page items.
	Items []MessageDTO `json:"items"`
	// HasMore reports whether another page exists.
	HasMore bool `json:"has_more"`
	// NextCursor is the opaque cursor for the next page when HasMore is true.
	NextCursor string `json:"next_cursor,omitempty"`
}

// MessageDTO is the manager-facing message response item.
type MessageDTO struct {
	// MessageID is the durable message identifier.
	MessageID uint64 `json:"message_id"`
	// MessageSeq is the committed channel message sequence number.
	MessageSeq uint64 `json:"message_seq"`
	// ClientMsgNo is the client-provided message correlation number.
	ClientMsgNo string `json:"client_msg_no"`
	// ChannelID is the logical channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the logical channel type.
	ChannelType int64 `json:"channel_type"`
	// FromUID is the sender UID recorded on the message.
	FromUID string `json:"from_uid"`
	// Timestamp is the server-side message timestamp in Unix seconds.
	Timestamp int64 `json:"timestamp"`
	// Payload is the raw message payload bytes encoded as base64 in JSON.
	Payload []byte `json:"payload"`
}

type messageCursorPayload struct {
	Version   int    `json:"v"`
	BeforeSeq uint64 `json:"before_seq"`
}

func (s *Server) handleMessages(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	channelID := c.Query("channel_id")
	if channelID == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "channel_id is required")
		return
	}
	channelType, err := parseMessageChannelType(c.Query("channel_type"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		return
	}
	limit, err := parseMessageLimit(c.Query("limit"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid limit")
		return
	}
	cursor, err := decodeMessageCursor(c.Query("cursor"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
		return
	}
	messageID, err := parseMessageID(c.Query("message_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid message_id")
		return
	}

	page, err := s.management.ListMessages(c.Request.Context(), managementusecase.ListMessagesRequest{
		ChannelID:   channelID,
		ChannelType: channelType,
		Limit:       limit,
		Cursor:      cursor,
		MessageID:   messageID,
		ClientMsgNo: c.Query("client_msg_no"),
	})
	if err != nil {
		switch {
		case errors.Is(err, metadb.ErrInvalidArgument):
			jsonError(c, http.StatusBadRequest, "bad_request", "invalid message query")
		case errors.Is(err, metadb.ErrNotFound):
			jsonError(c, http.StatusNotFound, "not_found", "channel not found")
		case channelLeaderUnavailable(err):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "channel leader unavailable")
		default:
			jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	nextCursor, err := encodeMessageCursor(page.NextCursor)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, MessageListResponse{
		Items:      messageDTOs(page.Items),
		HasMore:    page.HasMore,
		NextCursor: nextCursor,
	})
}

func parseMessageChannelType(raw string) (int64, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseMessageLimit(raw string) (int, error) {
	if raw == "" {
		return defaultMessageLimit, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > maxMessageLimit {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseMessageID(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func encodeMessageCursor(cursor managementusecase.MessageListCursor) (string, error) {
	if cursor == (managementusecase.MessageListCursor{}) {
		return "", nil
	}
	payload, err := json.Marshal(messageCursorPayload{
		Version:   1,
		BeforeSeq: cursor.BeforeSeq,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeMessageCursor(raw string) (managementusecase.MessageListCursor, error) {
	if raw == "" {
		return managementusecase.MessageListCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return managementusecase.MessageListCursor{}, err
	}
	var body messageCursorPayload
	if err := json.Unmarshal(payload, &body); err != nil {
		return managementusecase.MessageListCursor{}, err
	}
	if body.Version != 1 || body.BeforeSeq == 0 {
		return managementusecase.MessageListCursor{}, strconv.ErrSyntax
	}
	return managementusecase.MessageListCursor{BeforeSeq: body.BeforeSeq}, nil
}

func messageDTOs(items []managementusecase.Message) []MessageDTO {
	out := make([]MessageDTO, 0, len(items))
	for _, item := range items {
		out = append(out, MessageDTO{
			MessageID:   item.MessageID,
			MessageSeq:  item.MessageSeq,
			ClientMsgNo: item.ClientMsgNo,
			ChannelID:   item.ChannelID,
			ChannelType: item.ChannelType,
			FromUID:     item.FromUID,
			Timestamp:   item.Timestamp,
			Payload:     append([]byte(nil), item.Payload...),
		})
	}
	return out
}

func channelLeaderUnavailable(err error) bool {
	return errors.Is(err, raftcluster.ErrNoLeader) ||
		errors.Is(err, raftcluster.ErrNotLeader) ||
		errors.Is(err, raftcluster.ErrSlotNotFound) ||
		errors.Is(err, channel.ErrNotLeader) ||
		errors.Is(err, channel.ErrStaleMeta)
}
