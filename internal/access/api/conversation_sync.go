package api

import (
	"net/http"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
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
