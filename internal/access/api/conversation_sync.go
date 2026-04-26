package api

import (
	"errors"
	"net/http"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleConversationSync(c *gin.Context) {
	if s == nil {
		writeJSONError(c, http.StatusServiceUnavailable, "server not available")
		return
	}

	var req syncConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeJSONError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if req.UID == "" {
		writeJSONError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if s.conversations == nil {
		writeJSONError(c, http.StatusInternalServerError, "conversation usecase not configured")
		return
	}

	lastMsgSeqs, err := parseLegacyLastMsgSeqs(req.UID, req.LastMsgSeqs)
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, "invalid last_msg_seqs")
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = s.conversationDefaultLimit
	}
	if s.conversationMaxLimit > 0 && limit > s.conversationMaxLimit {
		limit = s.conversationMaxLimit
	}

	result, err := s.conversations.Sync(c.Request.Context(), conversationusecase.SyncQuery{
		UID:                 req.UID,
		Version:             req.Version,
		LastMsgSeqs:         lastMsgSeqs,
		MsgCount:            req.MsgCount,
		OnlyUnread:          req.OnlyUnread == 1,
		ExcludeChannelTypes: append([]uint8(nil), req.ExcludeChannelTypes...),
		Limit:               limit,
	})
	if err != nil {
		writeJSONError(c, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]legacyConversationResponse, 0, len(result.Conversations))
	for _, item := range result.Conversations {
		resp = append(resp, newLegacyConversationResponse(req.UID, item))
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleConversationClearUnread(c *gin.Context) {
	var req clearConversationUnreadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if err := validateClearConversationUnreadRequest(req); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	if s == nil || s.conversations == nil {
		writeLegacyJSONError(c, "conversation usecase not configured")
		return
	}

	channelID, err := normalizeLegacyConversationChannelID(req.UID, req.ChannelID, req.ChannelType)
	if err != nil {
		writeLegacyJSONError(c, "invalid channel_id")
		return
	}
	if err := s.conversations.ClearUnread(c.Request.Context(), conversationusecase.ClearUnreadCommand{
		UID:         req.UID,
		ChannelID:   channelID,
		ChannelType: req.ChannelType,
		MessageSeq:  req.MessageSeq,
	}); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
}

func (s *Server) handleConversationSetUnread(c *gin.Context) {
	var req setConversationUnreadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if err := validateSetConversationUnreadRequest(req); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	if s == nil || s.conversations == nil {
		writeLegacyJSONError(c, "conversation usecase not configured")
		return
	}

	channelID, err := normalizeLegacyConversationChannelID(req.UID, req.ChannelID, req.ChannelType)
	if err != nil {
		writeLegacyJSONError(c, "invalid channel_id")
		return
	}
	if err := s.conversations.SetUnread(c.Request.Context(), conversationusecase.SetUnreadCommand{
		UID:         req.UID,
		ChannelID:   channelID,
		ChannelType: req.ChannelType,
		Unread:      req.Unread,
	}); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
}

func validateClearConversationUnreadRequest(req clearConversationUnreadRequest) error {
	if req.UID == "" {
		return errors.New("uid cannot be empty")
	}
	if req.ChannelID == "" || req.ChannelType == 0 {
		return errors.New("channel_id or channel_type cannot be empty")
	}
	return nil
}

func validateSetConversationUnreadRequest(req setConversationUnreadRequest) error {
	if req.UID == "" {
		return errors.New("UID cannot be empty")
	}
	if req.ChannelID == "" || req.ChannelType == 0 {
		return errors.New("channel_id or channel_type cannot be empty")
	}
	if req.Unread < 0 {
		return errors.New("unread cannot be negative")
	}
	return nil
}

func normalizeLegacyConversationChannelID(uid, channelID string, channelType uint8) (string, error) {
	if channelType != frame.ChannelTypePerson {
		return channelID, nil
	}
	return runtimechannelid.NormalizePersonChannel(uid, channelID)
}
