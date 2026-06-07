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
	LastAt         int64  `json:"last_at"`
	LastMessageSeq uint64 `json:"last_message_seq"`
	ChannelID      string `json:"channel_id"`
	ChannelType    int64  `json:"channel_type"`
}

type conversationListResponse struct {
	Conversations      []conversationListItem  `json:"conversations"`
	NextCursor         *conversationListCursor `json:"next_cursor,omitempty"`
	More               int                     `json:"more"`
	Truncated          bool                    `json:"truncated"`
	ScannedMemberships int                     `json:"scanned_memberships"`
}

type conversationListItem struct {
	ChannelID        string `json:"channel_id"`
	ChannelType      int64  `json:"channel_type"`
	LastMessageID    uint64 `json:"last_message_id"`
	LastMessageIDStr string `json:"last_message_idstr"`
	LastMessageSeq   uint64 `json:"last_message_seq"`
	LastAt           int64  `json:"last_at"`
	FromUID          string `json:"from_uid"`
	ClientMsgNo      string `json:"client_msg_no"`
	Payload          []byte `json:"payload"`
	UpdatedAt        int64  `json:"updated_at"`
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
		Result:             "ok",
		Duration:           time.Since(start),
		ScannedMemberships: result.ScannedMemberships,
		ReturnedItems:      len(result.Items),
		Truncated:          result.Truncated,
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
		LastAt:         c.LastAt,
		LastMessageSeq: c.LastMessageSeq,
		ChannelID:      c.ChannelID,
		ChannelType:    c.ChannelType,
	}
}

func newConversationListResponse(uid string, result conversationusecase.ListResult) conversationListResponse {
	resp := conversationListResponse{
		Conversations:      make([]conversationListItem, 0, len(result.Items)),
		More:               boolToInt(result.HasMore),
		Truncated:          result.Truncated,
		ScannedMemberships: result.ScannedMemberships,
	}
	for _, item := range result.Items {
		resp.Conversations = append(resp.Conversations, newConversationListItem(uid, item))
	}
	if result.HasMore {
		cursor := conversationListCursor{
			LastAt:         result.NextCursor.LastAt,
			LastMessageSeq: result.NextCursor.LastMessageSeq,
			ChannelID:      result.NextCursor.ChannelID,
			ChannelType:    result.NextCursor.ChannelType,
		}
		resp.NextCursor = &cursor
	}
	return resp
}

func newConversationListItem(uid string, item conversationusecase.Conversation) conversationListItem {
	return conversationListItem{
		ChannelID:        legacyMessageChannelID(uid, item.ChannelID, uint8(item.ChannelType)),
		ChannelType:      item.ChannelType,
		LastMessageID:    item.LastMessageID,
		LastMessageIDStr: strconv.FormatUint(item.LastMessageID, 10),
		LastMessageSeq:   item.LastMessageSeq,
		LastAt:           item.LastAt,
		FromUID:          item.FromUID,
		ClientMsgNo:      item.ClientMsgNo,
		Payload:          append([]byte(nil), item.Payload...),
		UpdatedAt:        item.UpdatedAt,
	}
}
