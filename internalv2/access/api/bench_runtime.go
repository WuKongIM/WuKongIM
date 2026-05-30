package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
)

const maxBenchRuntimeRange = 100000

func (s *Server) handleBenchChannelRuntimeSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.benchRuntime == nil {
		writeBenchError(w, http.StatusNotImplemented, "bench channel runtime controller is not configured")
		return
	}
	query, err := runtimeQueryFromSnapshotRequest(r)
	if err != nil {
		writeBenchError(w, http.StatusBadRequest, err.Error())
		return
	}
	query, err = validateRuntimeQuery(query, false)
	if err != nil {
		writeBenchError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.benchRuntime.Snapshot(r.Context(), query)
	if err != nil {
		writeBenchError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp.Version == "" {
		resp.Version = versionV1
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleBenchChannelRuntimeProbe(w http.ResponseWriter, r *http.Request) {
	if s.benchRuntime == nil {
		writeBenchError(w, http.StatusNotImplemented, "bench channel runtime controller is not configured")
		return
	}
	var req model.ChannelRuntimeProbeRequest
	if !s.bindBenchJSON(w, r, &req) {
		return
	}
	query := model.ChannelRuntimeQuery{
		RunID:       req.RunID,
		Profile:     req.Profile,
		ChannelType: req.ChannelType,
		Range:       req.Range,
	}
	query, err := validateRuntimeQuery(query, true)
	if err != nil {
		writeBenchError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.benchRuntime.Probe(r.Context(), query)
	if err != nil {
		writeBenchError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp.Version == "" {
		resp.Version = versionV1
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleBenchChannelRuntimeEvict(w http.ResponseWriter, r *http.Request) {
	if s.benchRuntime == nil {
		writeBenchError(w, http.StatusNotImplemented, "bench channel runtime controller is not configured")
		return
	}
	var req model.ChannelRuntimeEvictRequest
	if !s.bindBenchJSON(w, r, &req) {
		return
	}
	query := model.ChannelRuntimeQuery{
		RunID:       req.RunID,
		Profile:     req.Profile,
		ChannelType: req.ChannelType,
		Range:       req.Range,
	}
	query, err := validateRuntimeQuery(query, true)
	if err != nil {
		writeBenchError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.benchRuntime.Evict(r.Context(), query)
	if err != nil {
		writeBenchError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if resp.Version == "" {
		resp.Version = versionV1
	}
	writeJSON(w, http.StatusOK, resp)
}

func runtimeQueryFromSnapshotRequest(r *http.Request) (model.ChannelRuntimeQuery, error) {
	values := r.URL.Query()
	query := model.ChannelRuntimeQuery{
		RunID:   values.Get("run_id"),
		Profile: values.Get("profile"),
	}
	if raw := values.Get("channel_type"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 255 {
			return model.ChannelRuntimeQuery{}, fmt.Errorf("channel_type must be a uint8")
		}
		query.ChannelType = uint8(n)
	}
	if raw := values.Get("start"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return model.ChannelRuntimeQuery{}, fmt.Errorf("start must be an integer")
		}
		query.Range.Start = n
	}
	if raw := values.Get("end"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return model.ChannelRuntimeQuery{}, fmt.Errorf("end must be an integer")
		}
		query.Range.End = n
	}
	return query, nil
}

func validateRuntimeQuery(query model.ChannelRuntimeQuery, requireRange bool) (model.ChannelRuntimeQuery, error) {
	query.RunID = strings.TrimSpace(query.RunID)
	query.Profile = strings.TrimSpace(query.Profile)
	hasRange := query.Range.Start != 0 || query.Range.End != 0
	if !requireRange && !hasRange {
		return query, nil
	}
	if requireRange {
		if query.RunID == "" {
			return query, fmt.Errorf("run_id is required")
		}
		if query.Profile == "" {
			return query, fmt.Errorf("profile is required")
		}
		if query.ChannelType == 0 {
			return query, fmt.Errorf("channel_type is required")
		}
		if !hasRange {
			return query, fmt.Errorf("range is required")
		}
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
