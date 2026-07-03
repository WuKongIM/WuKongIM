package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

const maxBenchRuntimeRange = 100000

func (s *Server) handleBenchChannelRuntimeSnapshot(c *gin.Context) {
	if s.benchRuntime == nil {
		writeBenchError(c, http.StatusNotImplemented, "bench channel runtime controller is not configured")
		return
	}
	query, err := runtimeQueryFromSnapshotRequest(c)
	if err != nil {
		writeBenchError(c, http.StatusBadRequest, err.Error())
		return
	}
	query, err = validateRuntimeQuery(query, snapshotRangeBoundPresent(c))
	if err != nil {
		writeBenchError(c, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.benchRuntime.Snapshot(c.Request.Context(), query)
	if err != nil {
		s.logBenchRuntimeFailure(c, "snapshot", query, err)
		writeBenchError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if resp.Version == "" {
		resp.Version = versionV1
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleBenchChannelRuntimeProbe(c *gin.Context) {
	if s.benchRuntime == nil {
		writeBenchError(c, http.StatusNotImplemented, "bench channel runtime controller is not configured")
		return
	}
	var req model.ChannelRuntimeProbeRequest
	if !s.bindBenchRuntimeJSON(c, &req) {
		return
	}
	query := runtimeQueryFromProbeRequest(req)
	query, err := validateRuntimeQuery(query, true)
	if err != nil {
		writeBenchError(c, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.benchRuntime.Probe(c.Request.Context(), query)
	if err != nil {
		s.logBenchRuntimeFailure(c, "probe", query, err)
		writeBenchError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if resp.Version == "" {
		resp.Version = versionV1
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleBenchChannelRuntimeEvict(c *gin.Context) {
	if s.benchRuntime == nil {
		writeBenchError(c, http.StatusNotImplemented, "bench channel runtime controller is not configured")
		return
	}
	var req model.ChannelRuntimeEvictRequest
	if !s.bindBenchRuntimeJSON(c, &req) {
		return
	}
	query := runtimeQueryFromEvictRequest(req)
	query, err := validateRuntimeQuery(query, true)
	if err != nil {
		writeBenchError(c, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.benchRuntime.Evict(c.Request.Context(), query)
	if err != nil {
		s.logBenchRuntimeFailure(c, "evict", query, err)
		writeBenchError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if resp.Version == "" {
		resp.Version = versionV1
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) logBenchRuntimeFailure(c *gin.Context, op string, query model.ChannelRuntimeQuery, err error) {
	if err == nil {
		return
	}
	path := ""
	method := ""
	if c != nil && c.Request != nil {
		r := c.Request
		method = r.Method
		if r.URL != nil {
			path = r.URL.Path
		}
	}
	s.httpLogger().Error("bench channel runtime request failed",
		wklog.Event("internalv2.access.api.bench_runtime_failed"),
		wklog.String("op", op),
		wklog.String("method", method),
		wklog.String("path", path),
		wklog.String("runID", query.RunID),
		wklog.String("profile", query.Profile),
		wklog.ChannelType(int64(query.ChannelType)),
		wklog.Int("rangeStart", query.Range.Start),
		wklog.Int("rangeEnd", query.Range.End),
		wklog.Error(err),
	)
}

func runtimeQueryFromSnapshotRequest(c *gin.Context) (model.ChannelRuntimeQuery, error) {
	query := model.ChannelRuntimeQuery{
		RunID:   c.Query("run_id"),
		Profile: c.Query("profile"),
	}
	if raw := c.Query("channel_type"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 255 {
			return model.ChannelRuntimeQuery{}, fmt.Errorf("channel_type must be a uint8")
		}
		query.ChannelType = uint8(n)
	}
	if raw := c.Query("start"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return model.ChannelRuntimeQuery{}, fmt.Errorf("start must be an integer")
		}
		query.Range.Start = n
	}
	if raw := c.Query("end"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return model.ChannelRuntimeQuery{}, fmt.Errorf("end must be an integer")
		}
		query.Range.End = n
	}
	return query, nil
}

func snapshotRangeBoundPresent(c *gin.Context) bool {
	_, hasStart := c.GetQuery("start")
	_, hasEnd := c.GetQuery("end")
	return hasStart || hasEnd
}

func runtimeQueryFromProbeRequest(req model.ChannelRuntimeProbeRequest) model.ChannelRuntimeQuery {
	return model.ChannelRuntimeQuery{
		RunID:       req.RunID,
		Profile:     req.Profile,
		ChannelType: req.ChannelType,
		Range:       req.Range,
	}
}

func runtimeQueryFromEvictRequest(req model.ChannelRuntimeEvictRequest) model.ChannelRuntimeQuery {
	return model.ChannelRuntimeQuery{
		RunID:       req.RunID,
		Profile:     req.Profile,
		ChannelType: req.ChannelType,
		Range:       req.Range,
	}
}

func validateRuntimeQuery(query model.ChannelRuntimeQuery, requireRange bool) (model.ChannelRuntimeQuery, error) {
	query.RunID = strings.TrimSpace(query.RunID)
	query.Profile = strings.TrimSpace(query.Profile)
	hasRange := query.Range.Start != 0 || query.Range.End != 0
	if !requireRange && !hasRange {
		return query, nil
	}
	if query.RunID == "" {
		return query, fmt.Errorf("run_id is required")
	}
	if query.Profile == "" {
		return query, fmt.Errorf("profile is required")
	}
	if query.ChannelType == 0 {
		return query, fmt.Errorf("channel_type is required")
	}
	if requireRange && !hasRange {
		return query, fmt.Errorf("range is required")
	}
	if query.Range.Start < 0 {
		return query, fmt.Errorf("start must be greater than or equal to 0")
	}
	if query.Range.Start >= query.Range.End {
		return query, fmt.Errorf("range must satisfy start < end")
	}
	if query.Range.End-query.Range.Start > maxBenchRuntimeRange {
		return query, fmt.Errorf("range exceeds max %d", maxBenchRuntimeRange)
	}
	return query, nil
}

func (s *Server) bindBenchRuntimeJSON(c *gin.Context, out any) bool {
	body := c.Request.Body
	if s.benchMaxPayloadBytes > 0 {
		body = http.MaxBytesReader(c.Writer, c.Request.Body, s.benchMaxPayloadBytes)
	}
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeBenchRuntimeJSONError(c, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			writeBenchError(c, http.StatusBadRequest, "invalid request: trailing JSON value")
			return false
		}
		writeBenchRuntimeJSONError(c, err)
		return false
	}
	return true
}

func writeBenchRuntimeJSONError(c *gin.Context, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeBenchError(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("payload too large: max %d bytes", maxBytesErr.Limit))
		return
	}
	writeBenchError(c, http.StatusBadRequest, "invalid request")
}
