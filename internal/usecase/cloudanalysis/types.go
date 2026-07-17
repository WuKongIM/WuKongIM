// Package cloudanalysis owns the bounded, run-scoped Analysis MCP use cases.
// It depends only on narrow observability ports and never accepts arbitrary
// URLs, files, commands, or process selectors.
package cloudanalysis

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	// MaxMetricRange is the largest query window accepted by one metrics tool call.
	MaxMetricRange = 72 * time.Hour
	// MaxMetricSamples is the largest requested sample count per series.
	MaxMetricSamples = 5000
	// MaxLogLines is the largest log result requested per call.
	MaxLogLines = 200
	// MaxDiagnosticsEvents is the largest diagnostics result requested per call.
	MaxDiagnosticsEvents = 500
	// MaxTaskAudits is the largest retained task-audit result requested per call.
	MaxTaskAudits = 200
	// MaxTraceTTL is the largest active diagnostics tracking lifetime.
	MaxTraceTTL = 15 * time.Minute
	// MaxCPUProfileSeconds is the per-capture CPU profile ceiling.
	MaxCPUProfileSeconds = 30
	// MaxSessionCPUProfileSeconds is the cumulative CPU profile budget per Analysis Session.
	MaxSessionCPUProfileSeconds = 60
	defaultMaxResponseBytes     = 1 << 20
	defaultSourceTimeout        = 20 * time.Second
)

var (
	// ErrInvalidConfig reports an unusable Analysis Session configuration.
	ErrInvalidConfig = errors.New("internal/usecase/cloudanalysis: invalid config")
	// ErrInvalidToolInput reports a tool argument outside its strict contract.
	ErrInvalidToolInput = errors.New("internal/usecase/cloudanalysis: invalid tool input")
	// ErrRunIdentityMismatch reports a request not bound to this Analysis Session.
	ErrRunIdentityMismatch = errors.New("internal/usecase/cloudanalysis: run identity mismatch")
	// ErrRunContractMismatch reports inconsistent locator, inventory, or scenario identity.
	ErrRunContractMismatch = errors.New("internal/usecase/cloudanalysis: run contract mismatch")
	// ErrRunReleased reports provider-confirmed empty inventory for this run.
	ErrRunReleased = errors.New("internal/usecase/cloudanalysis: run released")
	// ErrResponseTooLarge reports a result above the configured MCP response bound.
	ErrResponseTooLarge = errors.New("internal/usecase/cloudanalysis: response too large")
	// ErrDiagnosticBusy reports another active diagnostic capture in progress.
	ErrDiagnosticBusy = errors.New("internal/usecase/cloudanalysis: diagnostic busy")
	// ErrDiagnosticBudgetExceeded reports cumulative active diagnostic budget exhaustion.
	ErrDiagnosticBudgetExceeded = errors.New("internal/usecase/cloudanalysis: diagnostic budget exceeded")
)

// Completeness classifies whether an Observation fully covers its requested source.
type Completeness string

const (
	// CompletenessComplete means all requested sources returned within bounds.
	CompletenessComplete Completeness = "complete"
	// CompletenessPartial means usable data returned with explicit missing portions.
	CompletenessPartial Completeness = "partial"
	// CompletenessUnavailable means the requested source returned no usable data.
	CompletenessUnavailable Completeness = "unavailable"
)

// TimeWindow records the bounded data or perturbation interval for an Observation.
type TimeWindow struct {
	// Start is the inclusive beginning of the observed interval.
	Start time.Time `json:"start"`
	// End is the inclusive end of the observed interval.
	End time.Time `json:"end"`
}

// Observation is the common bounded envelope returned by every Analysis MCP tool.
type Observation struct {
	// RunID is the exact Run Identity bound to this Analysis Session.
	RunID string `json:"run_id"`
	// Node identifies the selected node or aggregate scope.
	Node string `json:"node"`
	// Source identifies the private observability surface used by the tool.
	Source string `json:"source"`
	// ObservedAt records when the gateway completed this bounded observation.
	ObservedAt time.Time `json:"observed_at"`
	// Window is the requested data or active perturbation interval when applicable.
	Window *TimeWindow `json:"window,omitempty"`
	// Completeness classifies coverage of the requested source.
	Completeness Completeness `json:"completeness"`
	// Warnings contains explicit caveats, truncation, or source failures.
	Warnings []string `json:"warnings"`
	// Data contains the tool-specific bounded result.
	Data any `json:"data"`
}

// SourceResult is the normalized output returned by an observability port.
type SourceResult struct {
	// Node identifies the source node or aggregate scope.
	Node string
	// Source identifies the underlying private observability surface.
	Source string
	// Window is the exact source data or perturbation interval when known.
	Window *TimeWindow
	// Completeness classifies source coverage.
	Completeness Completeness
	// Warnings records bounded source caveats.
	Warnings []string
	// Data is the bounded source-specific result.
	Data any
}

// RunState is the provider-neutral lifecycle state relevant to analysis.
type RunState string

const (
	// RunStateReleased means provider inventory reports the run released.
	RunStateReleased RunState = "released"
)

// ScenarioInspection is the exact workload contract bound to a run.
type ScenarioInspection struct {
	// Digest is the canonical SHA-256 identity of Effective.
	Digest string `json:"digest"`
	// RandomSeed is the deterministic wkbench generation seed.
	RandomSeed int64 `json:"random_seed"`
	// HashSlotCount is the cluster hash-slot identity used by the workload.
	HashSlotCount uint32 `json:"hash_slot_count"`
	// Effective is the fully loaded wkbench/v1 scenario after bounded overrides.
	Effective *model.Scenario `json:"effective"`
}

// RunInspection is provider- or local-runtime proof about one exact run.
type RunInspection struct {
	// RunID is the exact inspected Run Identity.
	RunID string `json:"run_id"`
	// State is the reconciled run lifecycle state.
	State RunState `json:"state"`
	// InventoryCount is the current relevant resource count.
	InventoryCount int `json:"inventory_count"`
	// Provider identifies the run authority.
	Provider string `json:"provider"`
	// Region identifies the run location.
	Region string `json:"region"`
	// SourceSHA is the deployed immutable commit when known.
	SourceSHA string `json:"source_sha,omitempty"`
	// ExpiresAt is the immutable Run Lease deadline when known.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// Scenario is the exact effective workload contract deployed for the run.
	Scenario ScenarioInspection `json:"scenario"`
	// Warnings contains inventory ambiguity or local-mode caveats.
	Warnings []string `json:"warnings"`
}

// WorkloadInspection is a bounded, message-free projection of the local
// wkbench final summary for one exact Simulation Run.
type WorkloadInspection struct {
	// RunID is the exact Simulation Run identity embedded in the effective scenario.
	RunID string `json:"run_id"`
	// State is in_progress while no final summary exists, or completed once parsed.
	State string `json:"state"`
	// Status is passed or failed only after completion.
	Status string `json:"status,omitempty"`
	// ExitCode is the stable wkbench result code after completion.
	ExitCode int `json:"exit_code,omitempty"`
	// StabilityVerdict is wkbench's evidence-aware long-run classification.
	StabilityVerdict string `json:"stability_verdict,omitempty"`
	// Summary contains only the measurements used by declared limit gates.
	Summary WorkloadSummary `json:"summary"`
	// Violations contains enforced workload limit failures.
	Violations []WorkloadLimit `json:"violations"`
	// LimitWarnings contains non-enforced workload limit warnings.
	LimitWarnings []WorkloadLimit `json:"limit_warnings"`
	// PhaseWindows contains actual coordinator phase intervals.
	PhaseWindows []WorkloadPhaseWindow `json:"phase_windows"`
	// FailedWorkers contains bounded structured worker failure evidence.
	FailedWorkers []WorkloadWorkerFailure `json:"failed_workers"`
	// FailedWorkersTruncated reports that additional failure details were omitted.
	FailedWorkersTruncated bool `json:"failed_workers_truncated"`
}

// WorkloadLimit describes one enforced violation or non-enforced warning.
type WorkloadLimit struct {
	// Name is the stable workload limit identifier.
	Name string `json:"name"`
	// Actual is the measured value.
	Actual float64 `json:"actual"`
	// Limit is the configured threshold.
	Limit float64 `json:"limit"`
	// Hard reports whether the limit affects the workload exit code.
	Hard bool `json:"hard"`
}

// WorkloadPhaseWindow records one actual coordinator phase interval.
type WorkloadPhaseWindow struct {
	// Phase is the stable wkbench lifecycle phase name.
	Phase string `json:"phase"`
	// StartedAt is when the coordinator began the phase.
	StartedAt time.Time `json:"started_at"`
	// EndedAt is when the coordinator observed the phase terminal result.
	EndedAt time.Time `json:"ended_at"`
}

// WorkloadWorkerFailure is one bounded structured worker failure.
type WorkloadWorkerFailure struct {
	// WorkerID identifies the failed or unreachable worker.
	WorkerID string `json:"worker_id"`
	// Phase identifies the lifecycle phase when the failure was observed.
	Phase string `json:"phase,omitempty"`
	// ReasonCode is a stable machine-readable failure classification.
	ReasonCode string `json:"reason_code"`
	// Detail is a bounded redacted diagnostic description.
	Detail string `json:"detail,omitempty"`
	// ObservedAt records when the coordinator observed the failure.
	ObservedAt time.Time `json:"observed_at"`
}

// WorkloadSummary contains bounded load-generator quality measurements.
type WorkloadSummary struct {
	// SendSuccess is the successful send acknowledgement count during the measured run phase.
	SendSuccess uint64 `json:"send_success"`
	// ConnectErrorRate is the final failed-connection fraction across workers.
	ConnectErrorRate float64 `json:"connect_error_rate"`
	// SendackErrorRate is the final failed-SENDACK fraction across workers.
	SendackErrorRate float64 `json:"sendack_error_rate"`
	// RecvVerifyErrorRate is the final receive-verification failure fraction.
	RecvVerifyErrorRate float64 `json:"recv_verify_error_rate"`
	// WorkerFailed is the number of workers that did not complete successfully.
	WorkerFailed int `json:"worker_failed"`
	// SendackMaxWorkerP99 is the highest final per-worker SENDACK p99 latency.
	SendackMaxWorkerP99 string `json:"sendack_max_worker_p99"`
	// ReceiveMaxWorkerP99 is the highest final per-worker receive p99 latency.
	ReceiveMaxWorkerP99 string `json:"recv_max_worker_p99"`
}

// RunRequest selects the exact run bound to the session.
type RunRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id" jsonschema:"exact Simulation Run identity"`
}

// MetricsQueryRangeRequest selects one allowlisted metric query over a bounded interval.
type MetricsQueryRangeRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id" jsonschema:"exact Simulation Run identity"`
	// QueryID selects one server-side allowlisted PromQL expression.
	QueryID string `json:"query_id" jsonschema:"allowlisted metric query identifier"`
	// Start is the inclusive query start time.
	Start time.Time `json:"start" jsonschema:"RFC3339 query start"`
	// End is the inclusive query end time.
	End time.Time `json:"end" jsonschema:"RFC3339 query end"`
	// Step is the sample resolution.
	Step time.Duration `json:"step" jsonschema:"sample step as Go duration nanoseconds"`
}

// LogsSearchRequest selects bounded ordinary application log entries.
type LogsSearchRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// NodeID selects one cluster node.
	NodeID uint64 `json:"node_id"`
	// Source selects a fixed application log source such as app or error.
	Source string `json:"source,omitempty"`
	// Keyword is a bounded literal search string.
	Keyword string `json:"keyword,omitempty"`
	// Levels contains normalized allowlisted log levels.
	Levels []string `json:"levels,omitempty"`
	// Limit is the maximum returned line count.
	Limit int `json:"limit,omitempty"`
}

// LogsContextRequest selects a bounded page around an opaque log cursor.
type LogsContextRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// NodeID selects one cluster node.
	NodeID uint64 `json:"node_id"`
	// Source selects a fixed application log source.
	Source string `json:"source,omitempty"`
	// Cursor is an opaque cursor previously returned by logs_search or logs_context.
	Cursor string `json:"cursor"`
	// Before is the maximum number of preceding entries.
	Before int `json:"before,omitempty"`
	// After is the maximum number of following entries.
	After int `json:"after,omitempty"`
}

// DiagnosticsQueryRequest selects bounded retained diagnostic events.
type DiagnosticsQueryRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// NodeID optionally restricts the query to one cluster node.
	NodeID uint64 `json:"node_id,omitempty"`
	// TraceID filters by diagnostics trace identity.
	TraceID string `json:"trace_id,omitempty"`
	// ClientMsgNo filters by client message number.
	ClientMsgNo string `json:"client_msg_no,omitempty"`
	// ChannelKey filters by redacted diagnostics channel key.
	ChannelKey string `json:"channel_key,omitempty"`
	// UID filters by sender identity without returning it in event DTOs.
	UID string `json:"uid,omitempty"`
	// Stage filters by diagnostics stage.
	Stage string `json:"stage,omitempty"`
	// Result filters by normalized diagnostics result.
	Result string `json:"result,omitempty"`
	// Limit is the maximum returned event count.
	Limit int `json:"limit,omitempty"`
}

// TaskAuditsQueryRequest selects bounded retained Controller task history.
type TaskAuditsQueryRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// NodeID filters tasks involving one node.
	NodeID uint64 `json:"node_id,omitempty"`
	// SlotID filters tasks involving one physical Slot.
	SlotID uint32 `json:"slot_id,omitempty"`
	// Kind filters by Controller workflow kind.
	Kind string `json:"kind,omitempty"`
	// Status filters by retained task status.
	Status string `json:"status,omitempty"`
	// Keyword is a bounded literal summary/reason filter.
	Keyword string `json:"keyword,omitempty"`
	// Limit is the maximum returned task count.
	Limit int `json:"limit,omitempty"`
}

// TraceStartRequest installs one expiring diagnostics tracking rule.
type TraceStartRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// NodeID selects exactly one cluster node for active diagnostics.
	NodeID uint64 `json:"node_id"`
	// Target selects sender_uid or channel.
	Target string `json:"target"`
	// UID is required when Target is sender_uid.
	UID string `json:"uid,omitempty"`
	// ChannelID is required when Target is channel.
	ChannelID string `json:"channel_id,omitempty"`
	// ChannelType is required when Target is channel.
	ChannelType uint8 `json:"channel_type,omitempty"`
	// TTL is the expiring tracking lifetime.
	TTL time.Duration `json:"ttl"`
}

// TraceQueryRequest selects retained events for one exact diagnostics trace.
type TraceQueryRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// TraceID is the exact diagnostics trace identity.
	TraceID string `json:"trace_id"`
	// NodeID optionally restricts the query to one cluster node.
	NodeID uint64 `json:"node_id,omitempty"`
	// Limit is the maximum returned event count.
	Limit int `json:"limit,omitempty"`
}

// ProfileKind selects one allowlisted Go runtime profile class.
type ProfileKind string

const (
	// ProfileCPU captures an active bounded CPU profile.
	ProfileCPU ProfileKind = "cpu"
	// ProfileHeap captures an instantaneous heap profile.
	ProfileHeap ProfileKind = "heap"
	// ProfileGoroutine captures an instantaneous goroutine profile.
	ProfileGoroutine ProfileKind = "goroutine"
)

// ProfileCaptureRequest captures one bounded profile from one cluster node.
type ProfileCaptureRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// NodeID selects exactly one cluster node.
	NodeID uint64 `json:"node_id"`
	// Kind selects cpu, heap, or goroutine.
	Kind ProfileKind `json:"kind"`
	// Seconds is required for CPU and must be zero for snapshots.
	Seconds int `json:"seconds,omitempty"`
}

// ProfileTopRequest requests a bounded symbolic summary of one captured profile.
type ProfileTopRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// ProfileID is an opaque gateway-issued capture identity.
	ProfileID string `json:"profile_id"`
	// Limit is the maximum returned symbolic row count.
	Limit int `json:"limit,omitempty"`
}

// ProfileListRequest lists bounded profile metadata for one node or all nodes.
type ProfileListRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// NodeID optionally filters captures to one cluster node.
	NodeID uint64 `json:"node_id,omitempty"`
	// Limit is the maximum returned profile count.
	Limit int `json:"limit,omitempty"`
}

// ConfigReadRequest selects one node's allowlisted redacted effective config.
type ConfigReadRequest struct {
	// RunID is the exact Run Identity.
	RunID string `json:"run_id"`
	// NodeID selects exactly one cluster node.
	NodeID uint64 `json:"node_id"`
}

// Sources combines the narrow private observability ports required by Analysis MCP.
type Sources interface {
	// InspectRun proves the current live or released state of one exact run.
	InspectRun(context.Context, string) (RunInspection, error)
	// WorkloadInspect returns a bounded final wkbench summary or explicit in-progress state.
	WorkloadInspect(context.Context, string) (SourceResult, error)
	// ClusterSnapshot returns a bounded aggregate node/runtime snapshot.
	ClusterSnapshot(context.Context) (SourceResult, error)
	// MetricsQueryRange executes one pre-resolved allowlisted PromQL expression.
	MetricsQueryRange(context.Context, MetricsQueryRangeRequest, string) (SourceResult, error)
	// LogsSearch returns bounded ordinary application log entries.
	LogsSearch(context.Context, LogsSearchRequest) (SourceResult, error)
	// LogsContext returns bounded context around an opaque cursor.
	LogsContext(context.Context, LogsContextRequest) (SourceResult, error)
	// DiagnosticsQuery returns bounded retained diagnostic events.
	DiagnosticsQuery(context.Context, DiagnosticsQueryRequest) (SourceResult, error)
	// TaskAuditsQuery returns bounded retained Controller task histories.
	TaskAuditsQuery(context.Context, TaskAuditsQueryRequest) (SourceResult, error)
	// TraceStart installs one expiring diagnostics tracking rule.
	TraceStart(context.Context, TraceStartRequest) (SourceResult, error)
	// TraceQuery returns retained events for one exact diagnostics trace.
	TraceQuery(context.Context, TraceQueryRequest) (SourceResult, error)
	// ProfileCapture captures one bounded active or snapshot profile.
	ProfileCapture(context.Context, ProfileCaptureRequest) (SourceResult, error)
	// ProfileTop returns a bounded symbolic summary of one capture.
	ProfileTop(context.Context, ProfileTopRequest) (SourceResult, error)
	// ProfileList returns bounded capture metadata.
	ProfileList(context.Context, ProfileListRequest) (SourceResult, error)
	// ConfigReadRedacted returns allowlisted effective config without secrets.
	ConfigReadRedacted(context.Context, ConfigReadRequest) (SourceResult, error)
}
