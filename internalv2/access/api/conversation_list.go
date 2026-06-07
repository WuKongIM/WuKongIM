package api

import (
	"net/http"
	"strconv"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/gin-gonic/gin"
)

type conversationListRequest struct {
	UID    string                 `json:"uid"`
	Cursor conversationListCursor `json:"cursor"`
	Limit  int                    `json:"limit"`
}

type conversationListCursor struct {
	ActiveAt    int64  `json:"active_at"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
}

type conversationListResponse struct {
	Conversations []conversationListItem  `json:"conversations"`
	NextCursor    *conversationListCursor `json:"next_cursor,omitempty"`
	More          int                     `json:"more"`
}

type conversationListItem struct {
	ChannelID    string                   `json:"channel_id"`
	ChannelType  int64                    `json:"channel_type"`
	ActiveAt     int64                    `json:"active_at"`
	ReadSeq      uint64                   `json:"read_seq"`
	DeletedToSeq uint64                   `json:"deleted_to_seq"`
	SparseActive bool                     `json:"sparse_active"`
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
	result, err := s.conversations.List(c.Request.Context(), conversationusecase.ListRequest{
		UID:    req.UID,
		Cursor: req.Cursor.toUsecase(),
		Limit:  req.Limit,
	})
	if err != nil {
		writeJSONError(c, err.Error())
		s.observeConversationList(ConversationListObservation{Result: "error", Duration: time.Since(start)})
		return
	}
	s.observeConversationList(ConversationListObservation{
		Result:           "ok",
		Duration:         time.Since(start),
		ReturnedItems:    len(result.Items),
		SparseItems:      countConversationSparseItems(result.Items),
		LastMessageLoads: len(result.Items),
		// Stale active-index detection lives below the HTTP adapter and is not
		// surfaced by the current usecase result yet.
		ActiveIndexStaleSkips: 0,
		More:                  result.HasMore,
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
		Conversations: make([]conversationListItem, 0, len(result.Items)),
		More:          boolToInt(result.HasMore),
	}
	for _, item := range result.Items {
		resp.Conversations = append(resp.Conversations, newConversationListItem(uid, item))
	}
	if result.HasMore {
		cursor := conversationListCursor{
			ActiveAt:    result.NextCursor.ActiveAt,
			ChannelID:   result.NextCursor.ChannelID,
			ChannelType: result.NextCursor.ChannelType,
		}
		resp.NextCursor = &cursor
	}
	return resp
}

func newConversationListItem(uid string, item conversationusecase.Conversation) conversationListItem {
	out := conversationListItem{
		ChannelID:    legacyMessageChannelID(uid, item.ChannelID, uint8(item.ChannelType)),
		ChannelType:  item.ChannelType,
		ActiveAt:     item.ActiveAt,
		ReadSeq:      item.ReadSeq,
		DeletedToSeq: item.DeletedToSeq,
		SparseActive: item.SparseActive,
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
			Payload:           append([]byte(nil), item.LastMessage.Payload...),
		}
	}
	return out
}

func countConversationSparseItems(items []conversationusecase.Conversation) int {
	count := 0
	for _, item := range items {
		if item.SparseActive {
			count++
		}
	}
	return count
}
