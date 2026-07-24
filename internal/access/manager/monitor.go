package manager

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
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

	// RealtimeMonitorScopeUnified identifies the unified cluster operations realtime monitor view.
	RealtimeMonitorScopeUnified = "realtime_monitor"

	// RealtimeMonitorSourcePrometheus identifies Prometheus as a card or snapshot source.
	RealtimeMonitorSourcePrometheus = "prometheus"
	// RealtimeMonitorSourceControlSnapshot identifies the bounded cluster control snapshot source.
	RealtimeMonitorSourceControlSnapshot = "control_snapshot"

	// RealtimeMonitorCategoryCommon returns the default lightweight operator view.
	RealtimeMonitorCategoryCommon = "common"
	// RealtimeMonitorCategoryGateway groups gateway ingress and connection-facing cards.
	RealtimeMonitorCategoryGateway = "gateway"
	// RealtimeMonitorCategoryInternal groups internal node transport and RPC cards.
	RealtimeMonitorCategoryInternal = "internal"
	// RealtimeMonitorCategoryMessage groups message append, commit, delivery, retry, and path error cards.
	RealtimeMonitorCategoryMessage = "message"
	// RealtimeMonitorCategoryConversation groups conversation sync and active-cache cards.
	RealtimeMonitorCategoryConversation = "conversation"
	// RealtimeMonitorCategoryChannel groups channel runtime and append cards.
	RealtimeMonitorCategoryChannel = "channel"
	// RealtimeMonitorCategoryDatabase groups database commit latency, batching, queue pressure, and Pebble engine cards.
	RealtimeMonitorCategoryDatabase = "database"
	// RealtimeMonitorCategoryControl groups control-plane cards.
	RealtimeMonitorCategoryControl = "control"
	// RealtimeMonitorCategorySlot groups Slot replication cards.
	RealtimeMonitorCategorySlot = "slot"
	// RealtimeMonitorCategoryNode groups node runtime, workqueue, and Go GC cards.
	RealtimeMonitorCategoryNode = "node"
	// RealtimeMonitorCategoryGoroutines groups direct node/module/task goroutine ownership snapshots.
	RealtimeMonitorCategoryGoroutines = "goroutines"

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
	// RealtimeMonitorStageConversationSync identifies conversation sync and active-cache cards.
	RealtimeMonitorStageConversationSync = "conversationSync"
	// RealtimeMonitorStageOnlineDelivery identifies online delivery cards.
	RealtimeMonitorStageOnlineDelivery = "onlineDelivery"
	// RealtimeMonitorStageOfflineRetry identifies offline retry cards.
	RealtimeMonitorStageOfflineRetry = "offlineRetry"
	// RealtimeMonitorStageErrorClosure identifies error closure cards.
	RealtimeMonitorStageErrorClosure = "errorClosure"
	// RealtimeMonitorStageControlPlane identifies control-plane health cards.
	RealtimeMonitorStageControlPlane = "controlPlane"
	// RealtimeMonitorStageSlotReplication identifies Slot replication health cards.
	RealtimeMonitorStageSlotReplication = "slotReplication"
	// RealtimeMonitorStageChannelReplication identifies Channel replication health cards.
	RealtimeMonitorStageChannelReplication = "channelReplication"
	// RealtimeMonitorStageInternalNetwork identifies internal node RPC and transport cards.
	RealtimeMonitorStageInternalNetwork = "internalNetwork"
	// RealtimeMonitorStageRuntimePressure identifies node runtime pressure cards.
	RealtimeMonitorStageRuntimePressure = "runtimePressure"
	// RealtimeMonitorStageIncidentClosure identifies incident closure and error-rate cards.
	RealtimeMonitorStageIncidentClosure = "incidentClosure"
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
	// NodeID restricts Prometheus metrics to one cluster node when non-zero.
	NodeID uint64
	// Category restricts returned monitor cards to one operator-facing category.
	Category string
}

// RealtimeMonitorResponse is the unified manager realtime monitor payload.
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
	// Categories contains selectable card categories and their current card counts.
	Categories []RealtimeMonitorCategory `json:"categories"`
	// Snapshot contains compact top-line monitor values.
	Snapshot []RealtimeMonitorSnapshotEntry `json:"snapshot"`
	// Cards contains graphable monitor card data.
	Cards []RealtimeMonitorCard `json:"cards"`
	// Goroutines contains direct node/module/task snapshots when the goroutines category is active.
	Goroutines *RealtimeMonitorGoroutines `json:"goroutines,omitempty"`
}

// RealtimeMonitorScope identifies the monitor source and local node.
type RealtimeMonitorScope struct {
	// View is realtime_monitor for the unified monitor endpoint.
	View string `json:"view"`
	// NodeID is the local node ID when known.
	NodeID uint64 `json:"node_id,omitempty"`
}

// RealtimeMonitorSources reports monitor data source state.
type RealtimeMonitorSources struct {
	// Prometheus reports Prometheus query availability.
	Prometheus RealtimeMonitorPrometheusSource `json:"prometheus"`
	// ControlSnapshot reports bounded control snapshot availability.
	ControlSnapshot RealtimeMonitorSource `json:"control_snapshot"`
	// Goroutines reports direct node RPC snapshot availability.
	Goroutines *RealtimeMonitorSource `json:"goroutines,omitempty"`
}

// RealtimeMonitorGoroutines is the direct cluster goroutine ownership snapshot.
type RealtimeMonitorGoroutines struct {
	// Status is ready or partial.
	Status string `json:"status"`
	// GeneratedAt is the UTC snapshot time.
	GeneratedAt time.Time `json:"generated_at"`
	// Nodes preserves node identity for every direct snapshot.
	Nodes []RealtimeMonitorGoroutineNode `json:"nodes"`
}

// RealtimeMonitorGoroutineNode is one node's direct goroutine ownership state.
type RealtimeMonitorGoroutineNode struct {
	// NodeID is the stable cluster node identifier.
	NodeID uint64 `json:"node_id"`
	// Name is the operator-facing node name.
	Name string `json:"name"`
	// Status is the node status from the control snapshot.
	Status string `json:"status"`
	// Supported reports whether the node implements direct goroutine snapshots.
	Supported bool `json:"supported"`
	// Error is a bounded non-fatal snapshot error.
	Error string `json:"error,omitempty"`
	// Snapshot is present when Supported is true and the read succeeded.
	Snapshot *managementusecase.GoroutineSnapshot `json:"snapshot,omitempty"`
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

// RealtimeMonitorSource reports one non-Prometheus monitor source state.
type RealtimeMonitorSource struct {
	// Enabled reports whether the source can be queried.
	Enabled bool `json:"enabled"`
	// QueryMS is the total query duration in milliseconds.
	QueryMS int64 `json:"query_ms"`
	// Error contains a non-fatal source error shown to operators.
	Error string `json:"error"`
}

// RealtimeMonitorCategory is one selectable realtime monitor category.
type RealtimeMonitorCategory struct {
	// Key is the stable category key.
	Key string `json:"key"`
	// Count is the number of cards available in this category.
	Count int `json:"count"`
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
	// Source identifies where the value came from.
	Source string `json:"source,omitempty"`
}

// RealtimeMonitorCard is one graphable monitor card.
type RealtimeMonitorCard struct {
	// Key is the stable card metric key.
	Key string `json:"key"`
	// Category is the stable operator-facing category key.
	Category string `json:"category"`
	// Stage is the stable business path stage key.
	Stage string `json:"stage"`
	// Source identifies where the card value came from.
	Source string `json:"source"`
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
	// Label optionally names the series for multi-line charts.
	Label string `json:"label,omitempty"`
	// SeriesKey optionally provides a stable key for the labeled series.
	SeriesKey string `json:"series_key,omitempty"`
}

// RealtimeMonitorStat is one compact card statistic.
type RealtimeMonitorStat struct {
	// Key is the stable statistic key.
	Key string `json:"key"`
	// Label is an optional display label for dynamic statistics such as per-node values.
	Label string `json:"label,omitempty"`
	// Value is the numeric statistic value. Zero is meaningful and must be serialized.
	Value float64 `json:"value"`
	// Text is the textual statistic value when the stat is a label or reason.
	Text string `json:"text,omitempty"`
	// Unit is the statistic unit.
	Unit string `json:"unit,omitempty"`
}

func (s *Server) handleRealtimeMonitor(c *gin.Context) {
	query, err := parseRealtimeMonitorQuery(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if s == nil || s.realtimeMonitor == nil {
		c.JSON(http.StatusOK, realtimeMonitorDisabledResponse(query, "prometheus monitor provider is not configured"))
		return
	}
	response, err := s.realtimeMonitor.RealtimeMonitor(c.Request.Context(), query)
	if err != nil {
		response = realtimeMonitorUnavailableResponse(query, err.Error())
	}
	c.JSON(http.StatusOK, response)
}

func parseRealtimeMonitorQuery(c *gin.Context) (RealtimeMonitorQuery, error) {
	query := RealtimeMonitorQuery{Window: defaultRealtimeMonitorWindow, Category: RealtimeMonitorCategoryCommon}
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
	if raw := strings.TrimSpace(c.Query("category")); raw != "" {
		if !isValidRealtimeMonitorCategory(raw) {
			return query, fmt.Errorf("category invalid")
		}
		query.Category = raw
	}
	return query, nil
}

func isValidRealtimeMonitorCategory(category string) bool {
	switch category {
	case RealtimeMonitorCategoryCommon,
		RealtimeMonitorCategoryGateway,
		RealtimeMonitorCategoryInternal,
		RealtimeMonitorCategoryMessage,
		RealtimeMonitorCategoryConversation,
		RealtimeMonitorCategoryChannel,
		RealtimeMonitorCategoryDatabase,
		RealtimeMonitorCategoryControl,
		RealtimeMonitorCategorySlot,
		RealtimeMonitorCategoryNode,
		RealtimeMonitorCategoryGoroutines:
		return true
	default:
		return false
	}
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
		Scope:         RealtimeMonitorScope{View: RealtimeMonitorScopeUnified, NodeID: query.NodeID},
		Sources: RealtimeMonitorSources{
			Prometheus:      RealtimeMonitorPrometheusSource{Enabled: false, Error: message},
			ControlSnapshot: RealtimeMonitorSource{Enabled: false},
		},
		Categories: []RealtimeMonitorCategory{},
		Snapshot:   []RealtimeMonitorSnapshotEntry{},
		Cards:      []RealtimeMonitorCard{},
	}
}

func realtimeMonitorUnavailableResponse(query RealtimeMonitorQuery, message string) RealtimeMonitorResponse {
	return RealtimeMonitorResponse{
		Status:        RealtimeMonitorStatusPrometheusUnavailable,
		GeneratedAt:   time.Now().UTC(),
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         RealtimeMonitorScope{View: RealtimeMonitorScopeUnified, NodeID: query.NodeID},
		Sources: RealtimeMonitorSources{
			Prometheus:      RealtimeMonitorPrometheusSource{Enabled: true, Error: message},
			ControlSnapshot: RealtimeMonitorSource{Enabled: false},
		},
		Categories: []RealtimeMonitorCategory{},
		Snapshot:   []RealtimeMonitorSnapshotEntry{},
		Cards:      []RealtimeMonitorCard{},
	}
}
