package api

import (
	"context"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/gin-gonic/gin"
	"net/http"
)

// messageLookupUsecase keeps optional exact lookup separate from basic SEND
// capability while production wiring supplies the full message application.
type messageLookupUsecase interface {
	LookupMessages(context.Context, messageusecase.LookupMessagesQuery) (messageusecase.SyncChannelMessagesResult, error)
}

type messageLookupRequest struct {
	LoginUID     string   `json:"login_uid"`
	ChannelID    string   `json:"channel_id"`
	ChannelType  uint8    `json:"channel_type"`
	MessageSeqs  []uint64 `json:"message_seqs"`
	MessageIDs   []uint64 `json:"message_ids"`
	ClientMsgNos []string `json:"client_msg_nos"`
}

func (s *Server) handleMessageLookup(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	var req messageLookupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeJSONError(c, "数据格式有误！")
		return
	}
	app, ok := s.messages.(messageLookupUsecase)
	if !ok {
		writeJSONError(c, "message lookup not configured")
		return
	}
	result, err := app.LookupMessages(c.Request.Context(), messageusecase.LookupMessagesQuery{LoginUID: req.LoginUID, ChannelID: req.ChannelID, ChannelType: req.ChannelType, MessageSeqs: req.MessageSeqs, MessageIDs: req.MessageIDs, ClientMsgNos: req.ClientMsgNos})
	if err != nil {
		writeJSONError(c, err.Error())
		return
	}
	resp := syncChannelMessagesResponse{Messages: make([]legacyMessageResp, 0, len(result.Messages))}
	for _, m := range result.Messages {
		resp.Messages = append(resp.Messages, newLegacyMessageResp(req.LoginUID, m))
	}
	c.JSON(http.StatusOK, resp)
}
