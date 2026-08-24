package api

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"strconv"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/gin-gonic/gin"
)

type conversationListRequest struct {
	UID               string `json:"uid"`
	Cursor            string `json:"cursor"`
	Limit             int    `json:"limit"`
	CompletedCoverage int64  `json:"completed_coverage"`
}

type conversationRetryRequest struct {
	UID      string                `json:"uid"`
	Channels []conversationListKey `json:"channels"`
}

type conversationListCursor struct {
	ActiveAt    int64  `json:"active_at"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
}

type conversationListResponse struct {
	Conversations           []conversationListItem `json:"conversations"`
	Deletes                 []conversationListKey  `json:"deletes"`
	Unresolved              []conversationListKey  `json:"unresolved"`
	NextCursor              string                 `json:"next_cursor,omitempty"`
	Done                    bool                   `json:"done"`
	Coverage                int64                  `json:"coverage"`
	TombstonesRetainedSince int64                  `json:"tombstones_retained_since"`
	ResetRequired           bool                   `json:"reset_required"`
}

type conversationListKey struct {
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
}

type conversationListItem struct {
	ChannelID    string                   `json:"channel_id"`
	ChannelType  int64                    `json:"channel_type"`
	ActiveAt     int64                    `json:"active_at"`
	ReadSeq      uint64                   `json:"read_seq"`
	DeletedToSeq uint64                   `json:"deleted_to_seq"`
	Unread       uint64                   `json:"unread"`
	LastMessage  *conversationLastMessage `json:"last_message"`
}

type conversationLastMessage struct {
	MessageID         uint64 `json:"message_id"`
	MessageIDStr      string `json:"message_idstr"`
	MessageSeq        uint64 `json:"message_seq"`
	FromUID           string `json:"from_uid"`
	ClientMsgNo       string `json:"client_msg_no"`
	ServerTimestampMS int64  `json:"server_timestamp_ms"`
	Payload           []byte `json:"payload"`
}

func (s *Server) registerConversationRoutes() {
	if s == nil || s.engine == nil {
		return
	}
	s.engine.POST("/conversation/list", s.handleConversationList)
	s.engine.POST("/conversation/retry", s.handleConversationRetry)
	s.engine.POST("/conversations/clearUnread", s.handleConversationClearUnread)
	s.engine.POST("/conversations/setUnread", s.handleConversationSetUnread)
	s.engine.POST("/conversations/delete", s.handleConversationDelete)
	s.engine.POST("/conversations/activate", s.handleConversationActivate)
}

func (s *Server) handleConversationRetry(c *gin.Context) {
	var req conversationRetryRequest
	if !bindJSON(c, &req) {
		return
	}
	if req.UID == "" || len(req.Channels) == 0 {
		writeJSONError(c, "uid或channels不能为空！")
		return
	}
	if s == nil || s.conversations == nil {
		writeJSONError(c, "conversation usecase not configured")
		return
	}
	keys := make([]conversationusecase.ConversationKey, len(req.Channels))
	for index, key := range req.Channels {
		if key.ChannelID == "" || key.ChannelType <= 0 || key.ChannelType > 255 {
			writeJSONError(c, "channel_id或channel_type错误！")
			return
		}
		channelID, err := normalizeLegacyConversationChannelID(req.UID, key.ChannelID, uint8(key.ChannelType))
		if err != nil {
			writeJSONError(c, "invalid channel_id")
			return
		}
		keys[index] = conversationusecase.ConversationKey{ChannelID: channelID, ChannelType: key.ChannelType}
	}
	result, err := s.conversations.Retry(c.Request.Context(), conversationusecase.RetryRequest{UID: req.UID, Keys: keys})
	if err != nil {
		writeJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, newConversationListResponse(req.UID, result))
}

func (s *Server) handleConversationList(c *gin.Context) {
	start := time.Now()
	var req conversationListRequest
	if !bindJSON(c, &req) {
		s.observeConversationList(ConversationListObservation{Result: "invalid_request", Duration: time.Since(start)})
		return
	}
	if req.UID == "" {
		writeJSONError(c, "uid不能为空！")
		s.observeConversationList(ConversationListObservation{Result: "invalid_request", Duration: time.Since(start)})
		return
	}
	if s == nil || s.conversations == nil {
		writeJSONError(c, "conversation usecase not configured")
		s.observeConversationList(ConversationListObservation{Result: "not_configured", Duration: time.Since(start)})
		return
	}
	cursor, err := decodeConversationListCursor(req.Cursor)
	if err != nil {
		writeJSONError(c, "cursor格式错误！")
		s.observeConversationList(ConversationListObservation{Result: "invalid_request", Duration: time.Since(start)})
		return
	}
	result, err := s.conversations.List(c.Request.Context(), conversationusecase.ListRequest{
		UID:               req.UID,
		Cursor:            cursor.toUsecase(),
		Limit:             req.Limit,
		CompletedCoverage: req.CompletedCoverage,
	})
	if err != nil {
		writeJSONError(c, err.Error())
		s.observeConversationList(ConversationListObservation{Result: "error", Duration: time.Since(start)})
		return
	}
	s.observeConversationList(ConversationListObservation{
		Result:            "ok",
		Duration:          time.Since(start),
		ScannedCandidates: result.ScannedCandidates,
		ReturnedItems:     len(result.Items),
		Deletes:           len(result.Deletes),
		Unresolved:        len(result.Unresolved),
		Done:              result.Done,
	})
	c.JSON(http.StatusOK, newConversationListResponse(req.UID, result))
}

func (s *Server) observeConversationList(event ConversationListObservation) {
	if s == nil || s.conversationObserver == nil {
		return
	}
	if event.Result == "" {
		event.Result = "unknown"
	}
	s.conversationObserver.ObserveConversationList(event)
}

func (c conversationListCursor) toUsecase() conversationusecase.Cursor {
	return conversationusecase.Cursor{
		ActiveAt:    c.ActiveAt,
		ChannelID:   c.ChannelID,
		ChannelType: c.ChannelType,
	}
}

func newConversationListResponse(uid string, result conversationusecase.ListResult) conversationListResponse {
	resp := conversationListResponse{
		Conversations:           make([]conversationListItem, 0, len(result.Items)),
		Deletes:                 make([]conversationListKey, 0, len(result.Deletes)),
		Unresolved:              make([]conversationListKey, 0, len(result.Unresolved)),
		Done:                    result.Done,
		Coverage:                result.Coverage,
		TombstonesRetainedSince: result.TombstonesRetainedSince,
		ResetRequired:           result.ResetRequired,
	}
	for _, item := range result.Items {
		resp.Conversations = append(resp.Conversations, newConversationListItem(uid, item))
	}
	for _, key := range result.Deletes {
		resp.Deletes = append(resp.Deletes, conversationListKey{
			ChannelID: legacyMessageChannelID(uid, key.ChannelID, uint8(key.ChannelType)), ChannelType: key.ChannelType,
		})
	}
	for _, key := range result.Unresolved {
		resp.Unresolved = append(resp.Unresolved, conversationListKey{
			ChannelID: legacyMessageChannelID(uid, key.ChannelID, uint8(key.ChannelType)), ChannelType: key.ChannelType,
		})
	}
	if !result.Done || result.NextCursor.ChannelID != "" {
		cursor := conversationListCursor{
			ActiveAt:    result.NextCursor.ActiveAt,
			ChannelID:   result.NextCursor.ChannelID,
			ChannelType: result.NextCursor.ChannelType,
		}
		resp.NextCursor = encodeConversationListCursor(cursor)
	}
	return resp
}

const conversationListCursorVersion = byte(1)

var errInvalidConversationListCursor = errors.New("invalid conversation list cursor")

func encodeConversationListCursor(cursor conversationListCursor) string {
	if cursor.ChannelID == "" || len(cursor.ChannelID) > int(^uint16(0)) {
		return ""
	}
	payload := make([]byte, 19+len(cursor.ChannelID))
	payload[0] = conversationListCursorVersion
	binary.BigEndian.PutUint64(payload[1:9], uint64(cursor.ActiveAt))
	binary.BigEndian.PutUint64(payload[9:17], uint64(cursor.ChannelType))
	binary.BigEndian.PutUint16(payload[17:19], uint16(len(cursor.ChannelID)))
	copy(payload[19:], cursor.ChannelID)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeConversationListCursor(encoded string) (conversationListCursor, error) {
	if encoded == "" {
		return conversationListCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) < 19 || payload[0] != conversationListCursorVersion {
		return conversationListCursor{}, errInvalidConversationListCursor
	}
	channelIDLen := int(binary.BigEndian.Uint16(payload[17:19]))
	if channelIDLen == 0 || len(payload) != 19+channelIDLen {
		return conversationListCursor{}, errInvalidConversationListCursor
	}
	activeAt := int64(binary.BigEndian.Uint64(payload[1:9]))
	channelType := int64(binary.BigEndian.Uint64(payload[9:17]))
	if activeAt < 0 || channelType <= 0 || channelType > 255 {
		return conversationListCursor{}, errInvalidConversationListCursor
	}
	return conversationListCursor{ActiveAt: activeAt, ChannelID: string(payload[19:]), ChannelType: channelType}, nil
}

func newConversationListItem(uid string, item conversationusecase.Conversation) conversationListItem {
	out := conversationListItem{
		ChannelID:    legacyMessageChannelID(uid, item.ChannelID, uint8(item.ChannelType)),
		ChannelType:  item.ChannelType,
		ActiveAt:     item.ActiveAt,
		ReadSeq:      item.ReadSeq,
		DeletedToSeq: item.DeletedToSeq,
		Unread:       item.Unread,
	}
	if item.LastMessage != nil {
		out.LastMessage = &conversationLastMessage{
			MessageID:         item.LastMessage.MessageID,
			MessageIDStr:      strconv.FormatUint(item.LastMessage.MessageID, 10),
			MessageSeq:        item.LastMessage.MessageSeq,
			FromUID:           item.LastMessage.FromUID,
			ClientMsgNo:       item.LastMessage.ClientMsgNo,
			ServerTimestampMS: item.LastMessage.ServerTimestampMS,
			Payload:           item.LastMessage.Payload,
		}
	}
	return out
}
