package api

import (
	"net/http"
	"strconv"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
	"github.com/gin-gonic/gin"
)

const (
	diagnosticsDefaultLimit = 100
	diagnosticsMaxLimit     = 500
)

func (s *Server) registerDiagnosticsRoutes() {
	s.engine.GET("/debug/diagnostics/trace/:trace_id", s.handleDiagnosticsTrace)
	s.engine.GET("/debug/diagnostics/message", s.handleDiagnosticsMessage)
	s.engine.GET("/debug/diagnostics/events", s.handleDiagnosticsEvents)
}

func (s *Server) handleDiagnosticsTrace(c *gin.Context) {
	limit, ok := diagnosticsLimit(c)
	if !ok {
		return
	}
	query := diagnostics.Query{
		TraceID: c.Param("trace_id"),
		Limit:   limit,
	}
	s.writeDiagnosticsResult(c, query)
}

func (s *Server) handleDiagnosticsMessage(c *gin.Context) {
	limit, ok := diagnosticsLimit(c)
	if !ok {
		return
	}
	query := diagnostics.Query{Limit: limit}
	if clientMsgNo := c.Query("client_msg_no"); clientMsgNo != "" {
		query.ClientMsgNo = clientMsgNo
		s.writeDiagnosticsResult(c, query)
		return
	}
	channelKey := c.Query("channel_key")
	messageSeqRaw := c.Query("message_seq")
	if channelKey == "" || messageSeqRaw == "" {
		writeJSONError(c, http.StatusBadRequest, "missing diagnostics message lookup")
		return
	}
	messageSeq, err := strconv.ParseUint(messageSeqRaw, 10, 64)
	if err != nil || messageSeq == 0 {
		writeJSONError(c, http.StatusBadRequest, "invalid message_seq")
		return
	}
	query.ChannelKey = channelKey
	query.MessageSeq = messageSeq
	s.writeDiagnosticsResult(c, query)
}

func (s *Server) handleDiagnosticsEvents(c *gin.Context) {
	limit, ok := diagnosticsLimit(c)
	if !ok {
		return
	}
	query := diagnostics.Query{
		Stage: diagnostics.Stage(c.Query("stage")),
		Limit: limit,
	}
	s.writeDiagnosticsResult(c, query)
}

func (s *Server) writeDiagnosticsResult(c *gin.Context, query diagnostics.Query) {
	if s == nil || s.diagnostics == nil {
		writeJSONError(c, http.StatusNotFound, "diagnostics not available")
		return
	}
	c.JSON(http.StatusOK, s.diagnostics.QueryDiagnostics(c.Request.Context(), query))
}

func diagnosticsLimit(c *gin.Context) (int, bool) {
	raw := c.Query("limit")
	if raw == "" {
		return diagnosticsDefaultLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > diagnosticsMaxLimit {
		writeJSONError(c, http.StatusBadRequest, "invalid limit")
		return 0, false
	}
	return limit, true
}
