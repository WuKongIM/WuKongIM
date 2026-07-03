package api

import (
	"net/http"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/cmdsync"
	"github.com/gin-gonic/gin"
)

type messageSyncRequest struct {
	UID        string `json:"uid"`
	MessageSeq uint64 `json:"message_seq"`
	Limit      int    `json:"limit"`
}

type messageSyncAckRequest struct {
	UID            string `json:"uid"`
	LastMessageSeq uint64 `json:"last_message_seq"`
}

func (s *Server) handleMessageSync(c *gin.Context) {
	var req messageSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	uid := strings.TrimSpace(req.UID)
	if uid == "" {
		writeLegacyJSONError(c, "uid不能为空！")
		return
	}
	if req.Limit < 0 {
		writeLegacyJSONError(c, "limit不能为负数！")
		return
	}
	if s == nil || s.cmdSync == nil {
		writeLegacyJSONError(c, "cmd sync usecase not configured")
		return
	}

	result, err := s.cmdSync.Sync(c.Request.Context(), cmdsync.SyncQuery{
		UID:        uid,
		MessageSeq: req.MessageSeq,
		Limit:      req.Limit,
	})
	if err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}

	resp := make([]legacyMessageResp, 0, len(result.Messages))
	for _, msg := range result.Messages {
		resp = append(resp, newLegacyMessageResp(uid, msg))
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleMessageSyncAck(c *gin.Context) {
	var req messageSyncAckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	uid := strings.TrimSpace(req.UID)
	if uid == "" {
		writeLegacyJSONError(c, "uid不能为空！")
		return
	}
	if req.LastMessageSeq == 0 {
		writeLegacyJSONError(c, "last_message_seq不能为空！")
		return
	}
	if s == nil || s.cmdSync == nil {
		writeLegacyJSONError(c, "cmd sync usecase not configured")
		return
	}

	if err := s.cmdSync.SyncAck(c.Request.Context(), cmdsync.SyncAckCommand{UID: uid, LastMessageSeq: req.LastMessageSeq}); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
}
