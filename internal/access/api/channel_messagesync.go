package api

import (
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/gin-gonic/gin"
)

type syncChannelMessagesRequest struct {
	LoginUID         string           `json:"login_uid"`
	ChannelID        string           `json:"channel_id"`
	ChannelType      uint8            `json:"channel_type"`
	StartMessageSeq  uint64           `json:"start_message_seq"`
	EndMessageSeq    uint64           `json:"end_message_seq"`
	Limit            int              `json:"limit"`
	PullMode         message.PullMode `json:"pull_mode"`
	EventSummaryMode string           `json:"event_summary_mode"`
}

type syncChannelMessagesResponse struct {
	StartMessageSeq uint64              `json:"start_message_seq"`
	EndMessageSeq   uint64              `json:"end_message_seq"`
	More            int                 `json:"more"`
	Messages        []legacyMessageResp `json:"messages"`
}

func (s *Server) handleChannelMessageSync(c *gin.Context) {
	var req syncChannelMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if s == nil || s.messages == nil {
		writeLegacyJSONError(c, "message usecase not configured")
		return
	}

	result, err := s.messages.SyncChannelMessages(c.Request.Context(), message.SyncChannelMessagesQuery{
		LoginUID:         req.LoginUID,
		ChannelID:        req.ChannelID,
		ChannelType:      req.ChannelType,
		StartMessageSeq:  req.StartMessageSeq,
		EndMessageSeq:    req.EndMessageSeq,
		Limit:            req.Limit,
		PullMode:         req.PullMode,
		EventSummaryMode: req.EventSummaryMode,
	})
	if err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}

	resp := syncChannelMessagesResponse{
		StartMessageSeq: req.StartMessageSeq,
		EndMessageSeq:   req.EndMessageSeq,
		More:            boolToInt(result.More),
		Messages:        make([]legacyMessageResp, 0, len(result.Messages)),
	}
	for _, msg := range result.Messages {
		resp.Messages = append(resp.Messages, newLegacyMessageResp(req.LoginUID, msg))
	}
	c.JSON(http.StatusOK, resp)
}

func writeLegacyJSONError(c *gin.Context, message string) {
	if c == nil {
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{
		"msg":    message,
		"status": http.StatusBadRequest,
	})
}
