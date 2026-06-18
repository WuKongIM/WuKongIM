package manager

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	defaultRealtimeMonitorWindow = 15 * time.Minute
	defaultRealtimeMonitorPoints = 45
	minRealtimeMonitorStep       = 5 * time.Second
	maxRealtimeMonitorStep       = 5 * time.Minute

	// RealtimeMonitorStatusReady reports that all required monitor queries succeeded.
	RealtimeMonitorStatusReady = "ready"
	// RealtimeMonitorStatusPartial reports that monitor data is usable but incomplete.
	RealtimeMonitorStatusPartial = "partial"
	// RealtimeMonitorStatusPrometheusDisabled reports that Prometheus monitor queries are not configured.
	RealtimeMonitorStatusPrometheusDisabled = "prometheus_disabled"
	// RealtimeMonitorStatusPrometheusUnavailable reports that the Prometheus HTTP API cannot be queried.
	RealtimeMonitorStatusPrometheusUnavailable = "prometheus_unavailable"

	// RealtimeMonitorScopePrometheus identifies Prometheus as the monitor data source.
	RealtimeMonitorScopePrometheus = "prometheus"

	// RealtimeMonitorToneNormal is the normal card status tone.
	RealtimeMonitorToneNormal = "normal"
	// RealtimeMonitorToneWarning is the warning card status tone.
	RealtimeMonitorToneWarning = "warning"
	// RealtimeMonitorToneCritical is the critical card status tone.
	RealtimeMonitorToneCritical = "critical"

	// RealtimeMonitorStageSendEntry identifies gateway SEND ingress cards.
	RealtimeMonitorStageSendEntry = "sendEntry"
	// RealtimeMonitorStageAppendCommit identifies append and commit cards.
	RealtimeMonitorStageAppendCommit = "appendCommit"
	// RealtimeMonitorStageOnlineDelivery identifies online delivery cards.
	RealtimeMonitorStageOnlineDelivery = "onlineDelivery"
	// RealtimeMonitorStageOfflineRetry identifies offline retry cards.
	RealtimeMonitorStageOfflineRetry = "offlineRetry"
	// RealtimeMonitorStageErrorClosure identifies error closure cards.
	RealtimeMonitorStageErrorClosure = "errorClosure"
)

// RealtimeMonitorProvider returns Prometheus-backed manager monitor snapshots.
type RealtimeMonitorProvider interface {
	RealtimeMonitor(context.Context, RealtimeMonitorQuery) (RealtimeMonitorResponse, error)
}

// RealtimeMonitorQuery contains validated realtime monitor request parameters.
type RealtimeMonitorQuery struct {
	// Window is the requested chart time range.
	Window time.Duration
	// Step is the chart point interval.
	Step time.Duration
}

// RealtimeMonitorResponse is the manager business realtime monitor payload.
type RealtimeMonitorResponse struct {
	// Status is ready, partial, prometheus_disabled, or prometheus_unavailable.
	Status string `json:"status"`
	// GeneratedAt is the UTC time when the payload was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// WindowSeconds is the chart time range in seconds.
	WindowSeconds int `json:"window_seconds"`
	// StepSeconds is the chart point interval in seconds.
	StepSeconds int `json:"step_seconds"`
	// Scope identifies the monitor source and optional node identity.
	Scope RealtimeMonitorScope `json:"scope"`
	// Sources reports source availability and query details.
	Sources RealtimeMonitorSources `json:"sources"`
	// Snapshot contains compact top-line monitor values.
	Snapshot []RealtimeMonitorSnapshotEntry `json:"snapshot"`
	// Cards contains graphable monitor card data.
	Cards []RealtimeMonitorCard `json:"cards"`
}

// RealtimeMonitorScope identifies the monitor source and local node.
type RealtimeMonitorScope struct {
	// View is prometheus for this monitor endpoint.
	View string `json:"view"`
	// NodeID is the local node ID when known.
	NodeID uint64 `json:"node_id,omitempty"`
	// NodeName is the local node name when known.
	NodeName string `json:"node_name,omitempty"`
}

// RealtimeMonitorSources reports monitor data source state.
type RealtimeMonitorSources struct {
	// Prometheus reports Prometheus query availability.
	Prometheus RealtimeMonitorPrometheusSource `json:"prometheus"`
}

// RealtimeMonitorPrometheusSource reports Prometheus query availability.
type RealtimeMonitorPrometheusSource struct {
	// Enabled reports whether Prometheus is configured as a monitor source.
	Enabled bool `json:"enabled"`
	// BaseURL is the Prometheus HTTP API base URL when configured.
	BaseURL string `json:"base_url"`
	// QueryMS is the total query duration in milliseconds.
	QueryMS int64 `json:"query_ms"`
	// Error contains a non-fatal source error shown to operators.
	Error string `json:"error"`
}

// RealtimeMonitorSnapshotEntry is one compact monitor summary value.
type RealtimeMonitorSnapshotEntry struct {
	// Key is the stable snapshot entry key.
	Key string `json:"key"`
	// MetricKey points at the source monitor card key.
	MetricKey string `json:"metric_key"`
	// Value is the numeric summary value.
	Value float64 `json:"value"`
	// Unit is the value unit.
	Unit string `json:"unit,omitempty"`
	// Tone is the status tone used by the frontend.
	Tone string `json:"tone"`
}

// RealtimeMonitorCard is one graphable monitor card.
type RealtimeMonitorCard struct {
	// Key is the stable card metric key.
	Key string `json:"key"`
	// Stage is the stable business path stage key.
	Stage string `json:"stage"`
	// Tone is the status tone used by the frontend.
	Tone string `json:"tone"`
	// Unit is the primary value unit.
	Unit string `json:"unit"`
	// Value is the latest primary value.
	Value float64 `json:"value"`
	// Series contains chart points ordered by timestamp.
	Series []RealtimeMonitorPoint `json:"series"`
	// Stats contains compact card statistics.
	Stats []RealtimeMonitorStat `json:"stats"`
	// Available reports whether this card has usable data.
	Available bool `json:"available"`
	// UnavailableReason is a stable low-cardinality reason when Available is false.
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	// Error contains a card-local source error.
	Error string `json:"error"`
}

// RealtimeMonitorPoint is one chart point.
type RealtimeMonitorPoint struct {
	// Timestamp is the Unix timestamp in milliseconds.
	Timestamp int64 `json:"timestamp"`
	// Value is the numeric point value.
	Value float64 `json:"value"`
}

// RealtimeMonitorStat is one compact card statistic.
type RealtimeMonitorStat struct {
	// Key is the stable statistic key.
	Key string `json:"key"`
	// Value is the numeric statistic value.
	Value float64 `json:"value"`
}

func (s *Server) handleRealtimeMonitor(c *gin.Context) {
	query, err := parseRealtimeMonitorQuery(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if s == nil || s.monitor == nil {
		c.JSON(http.StatusOK, realtimeMonitorDisabledResponse(query, "prometheus monitor provider is not configured"))
		return
	}
	response, err := s.monitor.RealtimeMonitor(c.Request.Context(), query)
	if err != nil {
		response = realtimeMonitorUnavailableResponse(query, err.Error())
	}
	c.JSON(http.StatusOK, response)
}

func parseRealtimeMonitorQuery(c *gin.Context) (RealtimeMonitorQuery, error) {
	query := RealtimeMonitorQuery{Window: defaultRealtimeMonitorWindow}
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		window, err := parseRealtimeMonitorWindow(raw)
		if err != nil {
			return query, err
		}
		query.Window = window
	}
	query.Step = derivedRealtimeMonitorStep(query.Window)
	if raw := strings.TrimSpace(c.Query("step")); raw != "" {
		step, err := time.ParseDuration(raw)
		if err != nil {
			return query, fmt.Errorf("step invalid")
		}
		query.Step = step
	}
	if query.Step < minRealtimeMonitorStep || query.Step > maxRealtimeMonitorStep {
		return query, fmt.Errorf("step invalid")
	}
	return query, nil
}

func parseRealtimeMonitorWindow(raw string) (time.Duration, error) {
	window, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("window invalid")
	}
	switch window {
	case 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour:
		return window, nil
	default:
		return 0, fmt.Errorf("window invalid")
	}
}

func derivedRealtimeMonitorStep(window time.Duration) time.Duration {
	step := time.Duration(int64(window) / int64(defaultRealtimeMonitorPoints))
	if step < minRealtimeMonitorStep {
		return minRealtimeMonitorStep
	}
	if step > maxRealtimeMonitorStep {
		return maxRealtimeMonitorStep
	}
	return step.Round(time.Second)
}

func realtimeMonitorDisabledResponse(query RealtimeMonitorQuery, message string) RealtimeMonitorResponse {
	return RealtimeMonitorResponse{
		Status:        RealtimeMonitorStatusPrometheusDisabled,
		GeneratedAt:   time.Now().UTC(),
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         RealtimeMonitorScope{View: RealtimeMonitorScopePrometheus},
		Sources: RealtimeMonitorSources{
			Prometheus: RealtimeMonitorPrometheusSource{Enabled: false, Error: message},
		},
		Snapshot: []RealtimeMonitorSnapshotEntry{},
		Cards:    []RealtimeMonitorCard{},
	}
}

func realtimeMonitorUnavailableResponse(query RealtimeMonitorQuery, message string) RealtimeMonitorResponse {
	return RealtimeMonitorResponse{
		Status:        RealtimeMonitorStatusPrometheusUnavailable,
		GeneratedAt:   time.Now().UTC(),
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         RealtimeMonitorScope{View: RealtimeMonitorScopePrometheus},
		Sources: RealtimeMonitorSources{
			Prometheus: RealtimeMonitorPrometheusSource{Enabled: true, Error: message},
		},
		Snapshot: []RealtimeMonitorSnapshotEntry{},
		Cards:    []RealtimeMonitorCard{},
	}
}
