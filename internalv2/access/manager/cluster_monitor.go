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
	// ClusterRealtimeMonitorStatusReady reports that all required cluster monitor queries succeeded.
	ClusterRealtimeMonitorStatusReady = RealtimeMonitorStatusReady
	// ClusterRealtimeMonitorStatusPartial reports that cluster monitor data is usable but incomplete.
	ClusterRealtimeMonitorStatusPartial = RealtimeMonitorStatusPartial
	// ClusterRealtimeMonitorStatusPrometheusDisabled reports that Prometheus cluster monitor queries are not configured.
	ClusterRealtimeMonitorStatusPrometheusDisabled = RealtimeMonitorStatusPrometheusDisabled
	// ClusterRealtimeMonitorStatusPrometheusUnavailable reports that Prometheus cluster monitor queries cannot be run.
	ClusterRealtimeMonitorStatusPrometheusUnavailable = RealtimeMonitorStatusPrometheusUnavailable

	// ClusterRealtimeMonitorScopeCluster identifies the aggregate cluster monitor view.
	ClusterRealtimeMonitorScopeCluster = "cluster"

	// ClusterRealtimeMonitorSourcePrometheus identifies Prometheus as a card or snapshot source.
	ClusterRealtimeMonitorSourcePrometheus = "prometheus"
	// ClusterRealtimeMonitorSourceControlSnapshot identifies the bounded cluster control snapshot source.
	ClusterRealtimeMonitorSourceControlSnapshot = "control_snapshot"

	// ClusterRealtimeMonitorStageControlPlane identifies control-plane health cards.
	ClusterRealtimeMonitorStageControlPlane = "controlPlane"
	// ClusterRealtimeMonitorStageSlotReplication identifies Slot replication health cards.
	ClusterRealtimeMonitorStageSlotReplication = "slotReplication"
	// ClusterRealtimeMonitorStageChannelReplication identifies Channel replication health cards.
	ClusterRealtimeMonitorStageChannelReplication = "channelReplication"
	// ClusterRealtimeMonitorStageInternalNetwork identifies internal node RPC and transport cards.
	ClusterRealtimeMonitorStageInternalNetwork = "internalNetwork"
	// ClusterRealtimeMonitorStageRuntimePressure identifies node runtime pressure cards.
	ClusterRealtimeMonitorStageRuntimePressure = "runtimePressure"
	// ClusterRealtimeMonitorStageIncidentClosure identifies incident closure and error-rate cards.
	ClusterRealtimeMonitorStageIncidentClosure = "incidentClosure"
)

// ClusterRealtimeMonitorProvider returns cluster operations monitor snapshots.
type ClusterRealtimeMonitorProvider interface {
	ClusterRealtimeMonitor(context.Context, ClusterRealtimeMonitorQuery) (ClusterRealtimeMonitorResponse, error)
}

// ClusterRealtimeMonitorQuery contains validated cluster monitor request parameters.
type ClusterRealtimeMonitorQuery struct {
	// Window is the requested chart time range.
	Window time.Duration
	// Step is the chart point interval.
	Step time.Duration
	// NodeID restricts cluster monitor metrics and snapshots to one node when non-zero.
	NodeID uint64
}

// ClusterRealtimeMonitorResponse is the manager cluster realtime monitor payload.
type ClusterRealtimeMonitorResponse struct {
	// Status is ready, partial, prometheus_disabled, or prometheus_unavailable.
	Status string `json:"status"`
	// GeneratedAt is the UTC time when the payload was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// WindowSeconds is the chart time range in seconds.
	WindowSeconds int `json:"window_seconds"`
	// StepSeconds is the chart point interval in seconds.
	StepSeconds int `json:"step_seconds"`
	// Scope identifies the aggregate cluster view.
	Scope ClusterRealtimeMonitorScope `json:"scope"`
	// Sources reports source availability and query details.
	Sources ClusterRealtimeMonitorSources `json:"sources"`
	// Snapshot contains compact top-line cluster monitor values.
	Snapshot []ClusterRealtimeMonitorSnapshotEntry `json:"snapshot"`
	// Cards contains graphable cluster monitor card data.
	Cards []ClusterRealtimeMonitorCard `json:"cards"`
}

// ClusterRealtimeMonitorScope identifies the cluster monitor view.
type ClusterRealtimeMonitorScope struct {
	// View is cluster for this monitor endpoint.
	View string `json:"view"`
	// NodeID is the selected cluster node when the request is node-scoped.
	NodeID uint64 `json:"node_id,omitempty"`
}

// ClusterRealtimeMonitorSources reports cluster monitor data source state.
type ClusterRealtimeMonitorSources struct {
	// Prometheus reports Prometheus query availability.
	Prometheus RealtimeMonitorPrometheusSource `json:"prometheus"`
	// ControlSnapshot reports bounded control snapshot availability.
	ControlSnapshot ClusterRealtimeMonitorSource `json:"control_snapshot"`
}

// ClusterRealtimeMonitorSource reports one cluster monitor source state.
type ClusterRealtimeMonitorSource struct {
	// Enabled reports whether the source can be queried.
	Enabled bool `json:"enabled"`
	// QueryMS is the total query duration in milliseconds.
	QueryMS int64 `json:"query_ms"`
	// Error contains a non-fatal source error shown to operators.
	Error string `json:"error"`
}

// ClusterRealtimeMonitorSnapshotEntry is one compact cluster monitor summary value.
type ClusterRealtimeMonitorSnapshotEntry struct {
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
	// Source identifies where the value came from.
	Source string `json:"source"`
}

// ClusterRealtimeMonitorCard is one graphable cluster monitor card.
type ClusterRealtimeMonitorCard struct {
	// Key is the stable card metric key.
	Key string `json:"key"`
	// Stage is the stable cluster operations stage key.
	Stage string `json:"stage"`
	// Tone is the status tone used by the frontend.
	Tone string `json:"tone"`
	// Unit is the primary value unit.
	Unit string `json:"unit"`
	// Value is the latest primary value.
	Value float64 `json:"value"`
	// Source identifies where the card value came from.
	Source string `json:"source"`
	// Available reports whether this card has usable data.
	Available bool `json:"available"`
	// Error contains a card-local source error.
	Error string `json:"error"`
	// Series contains chart points ordered by timestamp.
	Series []RealtimeMonitorPoint `json:"series"`
	// Stats contains compact card statistics.
	Stats []ClusterRealtimeMonitorStat `json:"stats"`
}

// ClusterRealtimeMonitorStat is one compact card statistic.
type ClusterRealtimeMonitorStat struct {
	// Key is the stable statistic key.
	Key string `json:"key"`
	// Label is an optional display label for dynamic statistics such as per-node values.
	Label string `json:"label,omitempty"`
	// Value is the numeric statistic value when the stat is numeric.
	Value *float64 `json:"value,omitempty"`
	// Text is the textual statistic value when the stat is a label or reason.
	Text string `json:"text,omitempty"`
	// Unit is the statistic unit.
	Unit string `json:"unit,omitempty"`
}

func (s *Server) handleClusterRealtimeMonitor(c *gin.Context) {
	query, err := parseClusterRealtimeMonitorQuery(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if s == nil || s.clusterMonitor == nil {
		c.JSON(http.StatusOK, clusterRealtimeMonitorDisabledResponse(query, "cluster monitor provider is not configured"))
		return
	}
	response, err := s.clusterMonitor.ClusterRealtimeMonitor(c.Request.Context(), query)
	if err != nil {
		response = clusterRealtimeMonitorUnavailableResponse(query, err.Error())
	}
	c.JSON(http.StatusOK, response)
}

func parseClusterRealtimeMonitorQuery(c *gin.Context) (ClusterRealtimeMonitorQuery, error) {
	query := ClusterRealtimeMonitorQuery{Window: defaultRealtimeMonitorWindow}
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
	nodeID, err := parseOptionalConnectionNodeID(strings.TrimSpace(c.Query("node_id")))
	if err != nil {
		return query, fmt.Errorf("invalid node_id")
	}
	query.NodeID = nodeID
	return query, nil
}

func clusterRealtimeMonitorDisabledResponse(query ClusterRealtimeMonitorQuery, message string) ClusterRealtimeMonitorResponse {
	return ClusterRealtimeMonitorResponse{
		Status:        ClusterRealtimeMonitorStatusPrometheusDisabled,
		GeneratedAt:   time.Now().UTC(),
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         ClusterRealtimeMonitorScope{View: ClusterRealtimeMonitorScopeCluster, NodeID: query.NodeID},
		Sources: ClusterRealtimeMonitorSources{
			Prometheus:      RealtimeMonitorPrometheusSource{Enabled: false, Error: message},
			ControlSnapshot: ClusterRealtimeMonitorSource{Enabled: false},
		},
		Snapshot: []ClusterRealtimeMonitorSnapshotEntry{},
		Cards:    []ClusterRealtimeMonitorCard{},
	}
}

func clusterRealtimeMonitorUnavailableResponse(query ClusterRealtimeMonitorQuery, message string) ClusterRealtimeMonitorResponse {
	return ClusterRealtimeMonitorResponse{
		Status:        ClusterRealtimeMonitorStatusPrometheusUnavailable,
		GeneratedAt:   time.Now().UTC(),
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         ClusterRealtimeMonitorScope{View: ClusterRealtimeMonitorScopeCluster, NodeID: query.NodeID},
		Sources: ClusterRealtimeMonitorSources{
			Prometheus:      RealtimeMonitorPrometheusSource{Enabled: true, Error: message},
			ControlSnapshot: ClusterRealtimeMonitorSource{Enabled: false},
		},
		Snapshot: []ClusterRealtimeMonitorSnapshotEntry{},
		Cards:    []ClusterRealtimeMonitorCard{},
	}
}
