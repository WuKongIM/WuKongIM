package api

import (
	"errors"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)

type clearConversationUnreadRequest struct {
	UID         string `json:"uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	MessageSeq  uint64 `json:"message_seq"`
}

type setConversationUnreadRequest struct {
	UID         string `json:"uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	Unread      int    `json:"unread"`
}

func (s *Server) handleConversationClearUnread(c *gin.Context) {
	var req clearConversationUnreadRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := validateClearConversationUnreadRequest(req); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	if s == nil || s.conversations == nil {
		writeJSONError(c, "conversation usecase not configured")
		return
	}
	channelID, err := normalizeLegacyConversationChannelID(req.UID, req.ChannelID, req.ChannelType)
	if err != nil {
		writeJSONError(c, "invalid channel_id")
		return
	}
	writeMutationResult(c, s.conversations.ClearUnread(c.Request.Context(), conversationusecase.ClearUnreadCommand{
		UID:         req.UID,
		ChannelID:   channelID,
		ChannelType: req.ChannelType,
		MessageSeq:  req.MessageSeq,
	}))
}

func (s *Server) handleConversationSetUnread(c *gin.Context) {
	var req setConversationUnreadRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := validateSetConversationUnreadRequest(req); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	if s == nil || s.conversations == nil {
		writeJSONError(c, "conversation usecase not configured")
		return
	}
	channelID, err := normalizeLegacyConversationChannelID(req.UID, req.ChannelID, req.ChannelType)
	if err != nil {
		writeJSONError(c, "invalid channel_id")
		return
	}
	writeMutationResult(c, s.conversations.SetUnread(c.Request.Context(), conversationusecase.SetUnreadCommand{
		UID:         req.UID,
		ChannelID:   channelID,
		ChannelType: req.ChannelType,
		Unread:      req.Unread,
	}))
}

func (s *Server) handleConversationDelete(c *gin.Context) {
	var req clearConversationUnreadRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := validateClearConversationUnreadRequest(req); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	if s == nil || s.conversations == nil {
		writeJSONError(c, "conversation usecase not configured")
		return
	}
	channelID, err := normalizeLegacyConversationChannelID(req.UID, req.ChannelID, req.ChannelType)
	if err != nil {
		writeJSONError(c, "invalid channel_id")
		return
	}
	writeMutationResult(c, s.conversations.DeleteConversation(c.Request.Context(), conversationusecase.DeleteConversationCommand{
		UID:         req.UID,
		ChannelID:   channelID,
		ChannelType: req.ChannelType,
		MessageSeq:  req.MessageSeq,
	}))
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
