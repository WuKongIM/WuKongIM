package api

import (
	"net/http"
	"runtime/pprof"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/gin-gonic/gin"
)

const (
	diagnosticsDefaultLimit = 100
	diagnosticsMaxLimit     = 500
)

func (s *Server) registerDiagnosticsRoutes() {
	if s == nil || s.engine == nil {
		return
	}
	s.engine.GET("/debug/diagnostics/trace/:trace_id", s.handleDiagnosticsTrace)
	s.engine.GET("/debug/diagnostics/message", s.handleDiagnosticsMessage)
	s.engine.GET("/debug/diagnostics/events", s.handleDiagnosticsEvents)
}

func (s *Server) handleDebugConfig(c *gin.Context) {
	if s == nil || s.debugConfig == nil {
		writeDebugJSONError(c, http.StatusNotFound, "not found")
		return
	}
	c.JSON(http.StatusOK, s.debugConfig())
}

func (s *Server) handleDebugCluster(c *gin.Context) {
	if s == nil || s.debugCluster == nil {
		writeDebugJSONError(c, http.StatusNotFound, "not found")
		return
	}
	c.JSON(http.StatusOK, s.debugCluster())
}

func (s *Server) handleDebugGoroutines(c *gin.Context) {
	if profile := pprof.Lookup("goroutine"); profile != nil {
		c.Status(http.StatusOK)
		_ = profile.WriteTo(c.Writer, 1)
		return
	}
	writeDebugJSONError(c, http.StatusInternalServerError, "goroutine profile unavailable")
}

func (s *Server) handleDebugGoroutinesSummary(c *gin.Context) {
	if s.goroutineSnapshot == nil {
		writeDebugJSONError(c, http.StatusNotFound, "goroutine registry not configured")
		return
	}
	c.JSON(http.StatusOK, s.goroutineSnapshot())
}

func (s *Server) handleDiagnosticsTrace(c *gin.Context) {
	limit, ok := diagnosticsLimit(c)
	if !ok {
		return
	}
	traceID := strings.TrimSpace(c.Param("trace_id"))
	if traceID == "" {
		writeDebugJSONError(c, http.StatusBadRequest, "trace_id is required")
		return
	}
	s.writeDiagnosticsResult(c, diagnostics.Query{
		TraceID: traceID,
		Limit:   limit,
	})
}

func (s *Server) handleDiagnosticsMessage(c *gin.Context) {
	limit, ok := diagnosticsLimit(c)
	if !ok {
		return
	}
	query := diagnostics.Query{Limit: limit}
	clientMsgNo := strings.TrimSpace(c.Query("client_msg_no"))
	channelKey := strings.TrimSpace(c.Query("channel_key"))
	messageSeqRaw := strings.TrimSpace(c.Query("message_seq"))
	hasClientSelector := clientMsgNo != ""
	hasChannelSelector := channelKey != "" || messageSeqRaw != ""
	if hasClientSelector && hasChannelSelector {
		writeDebugJSONError(c, http.StatusBadRequest, "invalid message selector")
		return
	}
	if hasClientSelector {
		query.ClientMsgNo = clientMsgNo
		s.writeDiagnosticsResult(c, query)
		return
	}
	if channelKey == "" || messageSeqRaw == "" {
		writeDebugJSONError(c, http.StatusBadRequest, "missing diagnostics message lookup")
		return
	}
	messageSeq, err := strconv.ParseUint(messageSeqRaw, 10, 64)
	if err != nil || messageSeq == 0 {
		writeDebugJSONError(c, http.StatusBadRequest, "invalid message_seq")
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
	result, valid := parseDiagnosticsResult(c.Query("result"))
	if !valid {
		writeDebugJSONError(c, http.StatusBadRequest, "invalid result")
		return
	}
	query := diagnostics.Query{
		Stage:      diagnostics.Stage(strings.TrimSpace(c.Query("stage"))),
		UID:        strings.TrimSpace(c.Query("uid")),
		ChannelKey: strings.TrimSpace(c.Query("channel_key")),
		Result:     result,
		Limit:      limit,
	}
	s.writeDiagnosticsResult(c, query)
}

func (s *Server) writeDiagnosticsResult(c *gin.Context, query diagnostics.Query) {
	if s == nil || s.diagnostics == nil {
		writeDebugJSONError(c, http.StatusNotFound, "diagnostics not available")
		return
	}
	c.JSON(http.StatusOK, s.diagnostics.QueryDiagnostics(c.Request.Context(), query))
}

func diagnosticsLimit(c *gin.Context) (int, bool) {
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return diagnosticsDefaultLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > diagnosticsMaxLimit {
		writeDebugJSONError(c, http.StatusBadRequest, "invalid limit")
		return 0, false
	}
	return limit, true
}

func parseDiagnosticsResult(raw string) (diagnostics.Result, bool) {
	switch diagnostics.Result(strings.TrimSpace(raw)) {
	case "":
		return "", true
	case diagnostics.ResultOK:
		return diagnostics.ResultOK, true
	case diagnostics.ResultError:
		return diagnostics.ResultError, true
	case diagnostics.ResultTimeout:
		return diagnostics.ResultTimeout, true
	case diagnostics.ResultCanceled:
		return diagnostics.ResultCanceled, true
	case diagnostics.ResultPartial:
		return diagnostics.ResultPartial, true
	case diagnostics.ResultDropped:
		return diagnostics.ResultDropped, true
	case diagnostics.ResultSkipped:
		return diagnostics.ResultSkipped, true
	default:
		return "", false
	}
}

func writeDebugJSONError(c *gin.Context, status int, message string) {
	if message == "" {
		message = http.StatusText(status)
	}
	c.JSON(status, gin.H{"error": message})
}
