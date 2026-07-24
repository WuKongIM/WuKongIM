// Package opsobserve owns bounded, entry-independent operations observations.
package opsobserve

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	// ObservationSchema is the stable schema identifier returned by every tool.
	ObservationSchema = "wukongim/ops-observation/v1"
	// MaxResponseBytes is the maximum encoded response size for one observation.
	MaxResponseBytes = 1 << 20
	// MaxMetricRange is the largest metrics window accepted by V1.
	MaxMetricRange = 24 * time.Hour
	// MaxMetricPointsPerSeries bounds requested samples for one series.
	MaxMetricPointsPerSeries = 2000
	// MaxMetricSeries bounds returned metric series.
	MaxMetricSeries = 100
	// MaxLogLines bounds log results.
	MaxLogLines = 200
	// MaxLogLineBytes bounds one raw log line.
	MaxLogLineBytes = 8 << 10
	// MaxDiagnosticsEvents bounds diagnostic results.
	MaxDiagnosticsEvents = 500
	// MaxPprofSeconds bounds one CPU profile.
	MaxPprofSeconds = 30
	// MaxPprofRows bounds parsed profile rows.
	MaxPprofRows = 100
)

// Metric query IDs are the only V1 time-series queries accepted from agents.
const (
	MetricQueryTargetsUp             = "targets_up"
	MetricQueryMessageIngressRate    = "message_ingress_rate"
	MetricQueryMessageDeliveryRate   = "message_delivery_rate"
	MetricQueryAppendErrorRate       = "append_error_rate"
	MetricQueryRuntimeQueuePressure  = "runtime_queue_pressure"
	MetricQueryProcessCPURate        = "process_cpu_rate"
	MetricQueryProcessResidentMemory = "process_resident_memory"
	MetricQueryGoGoroutines          = "go_goroutines"
	MetricQueryGatewayConnections    = "gateway_active_connections"
	MetricQuerySlotApplyGap          = "slot_apply_gap"
)

var metricQueryIDs = map[string]struct{}{
	MetricQueryTargetsUp: {}, MetricQueryMessageIngressRate: {}, MetricQueryMessageDeliveryRate: {},
	MetricQueryAppendErrorRate: {}, MetricQueryRuntimeQueuePressure: {}, MetricQueryProcessCPURate: {},
	MetricQueryProcessResidentMemory: {}, MetricQueryGoGoroutines: {}, MetricQueryGatewayConnections: {},
	MetricQuerySlotApplyGap: {},
}

// MetricQueryIDs returns the frozen server-owned V1 query identifiers.
func MetricQueryIDs() []string {
	return []string{
		MetricQueryTargetsUp, MetricQueryMessageIngressRate, MetricQueryMessageDeliveryRate,
		MetricQueryAppendErrorRate, MetricQueryRuntimeQueuePressure, MetricQueryProcessCPURate,
		MetricQueryProcessResidentMemory, MetricQueryGoGoroutines, MetricQueryGatewayConnections,
		MetricQuerySlotApplyGap,
	}
}

var (
	// ErrInvalidConfig reports an unusable observation service configuration.
	ErrInvalidConfig = errors.New("internal/usecase/opsobserve: invalid config")
	// ErrInvalidToolInput reports a request outside a frozen tool contract.
	ErrInvalidToolInput = errors.New("internal/usecase/opsobserve: invalid tool input")
	// ErrResponseTooLarge reports a response above the V1 transport bound.
	ErrResponseTooLarge = errors.New("internal/usecase/opsobserve: response too large")
)

// Freshness classifies the age of returned evidence.
type Freshness string

const (
	// FreshnessFresh means the source considers the evidence current.
	FreshnessFresh Freshness = "fresh"
	// FreshnessStale means usable evidence is older than its normal interval.
	FreshnessStale Freshness = "stale"
	// FreshnessMissing means no usable evidence was available.
	FreshnessMissing Freshness = "missing"
)

// Completeness classifies source coverage.
type Completeness string

const (
	// CompletenessComplete means all requested evidence was returned.
	CompletenessComplete Completeness = "complete"
	// CompletenessPartial means some requested evidence was returned.
	CompletenessPartial Completeness = "partial"
	// CompletenessUnavailable means no requested evidence was returned.
	CompletenessUnavailable Completeness = "unavailable"
)

// Status is the built-in health verdict.
type Status string

const (
	// StatusHealthy means the available evidence passed built-in health rules.
	StatusHealthy Status = "healthy"
	// StatusDegraded means available evidence crossed a built-in threshold.
	StatusDegraded Status = "degraded"
	// StatusUnknown means the required evidence is missing or inconclusive.
	StatusUnknown Status = "unknown"
)

// TimeWindow is the inclusive evidence interval.
type TimeWindow struct {
	// Start is the inclusive beginning.
	Start time.Time `json:"start"`
	// End is the inclusive end.
	End time.Time `json:"end"`
}

// ReasonCode contains one stable verdict reason and its evidence.
type ReasonCode struct {
	// Code is a stable low-cardinality reason identifier.
	Code string `json:"code"`
	// Actual is the bounded observed value when known.
	Actual any `json:"actual,omitempty"`
	// Threshold is the built-in rule threshold when applicable.
	Threshold any `json:"threshold,omitempty"`
}

// Observation is the common result envelope for all V1 tools.
type Observation struct {
	// Schema identifies the envelope contract.
	Schema string `json:"schema"`
	// ClusterID is the configured cluster identity.
	ClusterID string `json:"cluster_id"`
	// ObservedAt records when the observation completed.
	ObservedAt time.Time `json:"observed_at"`
	// Window is the source interval when applicable.
	Window *TimeWindow `json:"window,omitempty"`
	// Freshness classifies evidence age.
	Freshness Freshness `json:"freshness"`
	// Completeness classifies source coverage.
	Completeness Completeness `json:"completeness"`
	// Status is the built-in health verdict.
	Status Status `json:"status"`
	// ReasonCodes explain the verdict with bounded evidence.
	ReasonCodes []ReasonCode `json:"reason_codes"`
	// Warnings contains safe caveats and truncation notices.
	Warnings []string `json:"warnings"`
	// Data is the tool-specific bounded projection.
	Data any `json:"data"`
}

// SourceResult is the normalized output of one narrow observation port.
type SourceResult struct {
	// Window is the exact source interval when known.
	Window *TimeWindow
	// Freshness classifies evidence age.
	Freshness Freshness
	// Completeness classifies source coverage.
	Completeness Completeness
	// Status is the source health verdict.
	Status Status
	// ReasonCodes contain stable evidence-backed reasons.
	ReasonCodes []ReasonCode
	// Warnings contain safe source caveats.
	Warnings []string
	// Data is the bounded tool-specific result.
	Data any
}

// Source is the closed-world read port required by the V1 tool set.
type Source interface {
	// ClusterHealth returns aggregate cluster health without channel scans.
	ClusterHealth(context.Context, ClusterHealthRequest) (SourceResult, error)
	// NodeInspect returns one exact node observation.
	NodeInspect(context.Context, NodeInspectRequest) (SourceResult, error)
	// SlotInspect returns one exact physical Slot observation.
	SlotInspect(context.Context, SlotInspectRequest) (SourceResult, error)
	// ChannelRuntimeInspect performs one exact channel runtime point lookup.
	ChannelRuntimeInspect(context.Context, ChannelRuntimeInspectRequest) (SourceResult, error)
	// ControllerTasksQuery returns bounded Controller task evidence.
	ControllerTasksQuery(context.Context, ControllerTasksQueryRequest) (SourceResult, error)
	// MetricsQueryRange executes one server-owned metric query identifier.
	MetricsQueryRange(context.Context, MetricsQueryRangeRequest) (SourceResult, error)
	// LogsSearch returns bounded ordinary application log lines.
	LogsSearch(context.Context, LogsSearchRequest) (SourceResult, error)
	// LogsContext returns bounded context around an opaque log cursor.
	LogsContext(context.Context, LogsContextRequest) (SourceResult, error)
	// DiagnosticsQuery returns bounded retained diagnostic events.
	DiagnosticsQuery(context.Context, DiagnosticsQueryRequest) (SourceResult, error)
	// ConfigReadRedacted returns one allowlisted redacted node config.
	ConfigReadRedacted(context.Context, ConfigReadRedactedRequest) (SourceResult, error)
	// BackupInspect returns bounded backup and restore-point evidence.
	BackupInspect(context.Context, BackupInspectRequest) (SourceResult, error)
	// PprofAnalyze captures and parses one bounded runtime profile in memory.
	PprofAnalyze(context.Context, PprofAnalyzeRequest) (SourceResult, error)
}

// ClusterHealthRequest selects the aggregate cluster view.
type ClusterHealthRequest struct{}

// NodeInspectRequest selects one exact cluster node.
type NodeInspectRequest struct {
	NodeID uint64 `json:"node_id" jsonschema:"exact positive cluster node ID"`
}

// SlotInspectRequest selects one exact physical Slot.
type SlotInspectRequest struct {
	SlotID uint32 `json:"slot_id" jsonschema:"exact physical Slot ID"`
}

// ChannelRuntimeInspectRequest selects one exact channel.
type ChannelRuntimeInspectRequest struct {
	ChannelID   string `json:"channel_id" jsonschema:"exact channel ID, not a substring or pattern"`
	ChannelType uint8  `json:"channel_type" jsonschema:"positive channel type"`
}

// ControllerTasksQueryRequest selects a bounded Controller task page.
type ControllerTasksQueryRequest struct {
	Kind   string `json:"kind,omitempty" jsonschema:"optional exact server-defined task kind"`
	Status string `json:"status,omitempty" jsonschema:"optional exact task status"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum rows, default 100 and maximum 200"`
}

// MetricsQueryRangeRequest selects one server-owned time-series query.
type MetricsQueryRangeRequest struct {
	QueryID     string    `json:"query_id" jsonschema:"server-owned query ID: targets_up, message_ingress_rate, message_delivery_rate, append_error_rate, runtime_queue_pressure, process_cpu_rate, process_resident_memory, go_goroutines, gateway_active_connections, or slot_apply_gap"`
	NodeID      uint64    `json:"node_id,omitempty" jsonschema:"optional exact low-cardinality node filter"`
	Start       time.Time `json:"start" jsonschema:"inclusive RFC3339 start time"`
	End         time.Time `json:"end" jsonschema:"inclusive RFC3339 end time"`
	StepSeconds int       `json:"step_seconds" jsonschema:"sample resolution in seconds"`
}

// MetricRangeData is the bounded result of one server-owned metric query.
type MetricRangeData struct {
	QueryID string         `json:"query_id"`
	Series  []MetricSeries `json:"series"`
}

// MetricSeries contains allowlisted labels and bounded Prometheus matrix values.
type MetricSeries struct {
	Labels map[string]string   `json:"labels"`
	Values [][]json.RawMessage `json:"values"`
}

// LogsSearchRequest searches one ordinary application log source.
type LogsSearchRequest struct {
	NodeID  uint64   `json:"node_id" jsonschema:"exact positive cluster node ID"`
	Source  string   `json:"source" jsonschema:"ordinary application log source: app or error"`
	Keyword string   `json:"keyword,omitempty" jsonschema:"literal keyword, not a regular expression"`
	Levels  []string `json:"levels,omitempty" jsonschema:"optional normalized log levels"`
	Limit   int      `json:"limit,omitempty" jsonschema:"default 100 and maximum 200"`
}

// LogsContextRequest selects lines around one opaque cursor.
type LogsContextRequest struct {
	NodeID uint64 `json:"node_id" jsonschema:"exact positive cluster node ID"`
	Source string `json:"source" jsonschema:"ordinary application log source: app or error"`
	Cursor string `json:"cursor" jsonschema:"opaque cursor returned by logs_search"`
	Before int    `json:"before,omitempty" jsonschema:"lines before the cursor"`
	After  int    `json:"after,omitempty" jsonschema:"lines after the cursor"`
}

// DiagnosticsQueryRequest selects bounded retained diagnostics.
type DiagnosticsQueryRequest struct {
	NodeID  uint64    `json:"node_id,omitempty" jsonschema:"optional exact cluster node ID"`
	SlotID  uint32    `json:"slot_id,omitempty" jsonschema:"optional exact physical Slot ID"`
	TraceID string    `json:"trace_id,omitempty" jsonschema:"optional exact trace ID"`
	Stage   string    `json:"stage,omitempty" jsonschema:"optional exact diagnostic stage"`
	Result  string    `json:"result,omitempty" jsonschema:"optional exact diagnostic result"`
	Start   time.Time `json:"start,omitempty" jsonschema:"optional inclusive RFC3339 start time"`
	End     time.Time `json:"end,omitempty" jsonschema:"optional inclusive RFC3339 end time"`
	Limit   int       `json:"limit,omitempty" jsonschema:"default 100 and maximum 500"`
}

// ConfigReadRedactedRequest selects one node's allowlisted config.
type ConfigReadRedactedRequest struct {
	NodeID uint64 `json:"node_id" jsonschema:"exact positive cluster node ID"`
}

// BackupInspectRequest selects bounded backup history.
type BackupInspectRequest struct {
	JobID string `json:"job_id,omitempty" jsonschema:"optional exact backup job ID"`
	Limit int    `json:"limit,omitempty" jsonschema:"default 50 and maximum 100"`
}

// PprofAnalyzeRequest selects one bounded active runtime profile.
type PprofAnalyzeRequest struct {
	NodeID     uint64 `json:"node_id" jsonschema:"exact positive cluster node ID"`
	Kind       string `json:"kind" jsonschema:"cpu, heap, or goroutine"`
	Seconds    int    `json:"seconds,omitempty" jsonschema:"CPU duration, default 10 and maximum 30"`
	SampleType string `json:"sample_type,omitempty" jsonschema:"heap sample type: inuse_space or alloc_space"`
	Rows       int    `json:"rows,omitempty" jsonschema:"parsed top rows, default 30 and maximum 100"`
}
