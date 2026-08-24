package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/benchterminal"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

const (
	terminalFenceMaxRequestBytes  int64 = 4 << 10
	terminalFenceMaxIdentityBytes       = 128
	terminalFenceMaxSessions            = 1_000_000
)

func (s *Server) handleBenchTerminalFencePrepare(c *gin.Context) {
	if s.benchTerminalFence == nil {
		writeBenchError(c, http.StatusNotImplemented, "bench terminal fence controller is not configured")
		return
	}
	var request model.TerminalFencePrepareRequest
	if !s.bindTerminalFenceJSON(c, &request) {
		return
	}
	if !validTerminalFencePrepareRequest(request) {
		writeBenchError(c, http.StatusBadRequest, "invalid terminal fence request")
		return
	}
	grant, err := s.benchTerminalFence.Prepare(c.Request.Context(), benchterminal.PrepareRequest{
		RunID:            request.RunID,
		AssignmentID:     request.AssignmentID,
		ExpectedSessions: request.ExpectedSessions,
	})
	if err != nil {
		status, code, message := terminalFenceHTTPError(err)
		s.logTerminalFenceFailure(c, code, request.ExpectedSessions)
		writeBenchError(c, status, message)
		return
	}
	c.JSON(http.StatusOK, model.TerminalFenceGrant{
		Version:          model.TerminalFenceVersion,
		RunID:            request.RunID,
		AssignmentID:     request.AssignmentID,
		ExpectedSessions: request.ExpectedSessions,
		Epoch:            grant.Epoch,
		Capability:       grant.Capability,
	})
}

func validTerminalFencePrepareRequest(request model.TerminalFencePrepareRequest) bool {
	return request.RunID != "" && request.RunID == strings.TrimSpace(request.RunID) && len(request.RunID) <= terminalFenceMaxIdentityBytes &&
		request.AssignmentID != "" && request.AssignmentID == strings.TrimSpace(request.AssignmentID) && len(request.AssignmentID) <= terminalFenceMaxIdentityBytes &&
		request.ExpectedSessions > 0 && request.ExpectedSessions <= terminalFenceMaxSessions
}

func (s *Server) bindTerminalFenceJSON(c *gin.Context, out any) bool {
	limit := terminalFenceMaxRequestBytes
	if s.benchMaxPayloadBytes > 0 && s.benchMaxPayloadBytes < limit {
		limit = s.benchMaxPayloadBytes
	}
	body := http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeTerminalFenceJSONError(c, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeBenchError(c, http.StatusBadRequest, "invalid terminal fence request")
		return false
	}
	return true
}

func writeTerminalFenceJSONError(c *gin.Context, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeBenchError(c, http.StatusRequestEntityTooLarge, "terminal fence request too large")
		return
	}
	writeBenchError(c, http.StatusBadRequest, "invalid terminal fence request")
}

func terminalFenceHTTPError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, benchterminal.ErrInvalidPrepareRequest):
		return http.StatusBadRequest, "invalid_request", "invalid terminal fence request"
	case errors.Is(err, benchterminal.ErrPreparationConflict):
		return http.StatusConflict, "identity_conflict", "terminal fence identity conflict"
	case errors.Is(err, benchterminal.ErrPreparationFailed):
		return http.StatusServiceUnavailable, "preparation_failed", "terminal fence preparation failed"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "deadline", "terminal fence preparation timed out"
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "canceled", "terminal fence request canceled"
	default:
		return http.StatusInternalServerError, "internal", "terminal fence unavailable"
	}
}

func (s *Server) logTerminalFenceFailure(c *gin.Context, code string, expectedSessions int) {
	method, path := "", ""
	if c != nil && c.Request != nil {
		method = c.Request.Method
		if c.Request.URL != nil {
			path = c.Request.URL.Path
		}
	}
	s.httpLogger().Error("bench terminal fence request failed",
		wklog.Event("internal.access.api.bench_terminal_fence_failed"),
		wklog.String("method", method),
		wklog.String("path", path),
		wklog.String("result", code),
		wklog.Int("expectedSessions", expectedSessions),
	)
}
