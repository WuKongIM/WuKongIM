package cloudanalysis

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

var opaqueIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// Config binds one Analysis Session to an exact run, node set, and metric allowlist.
type Config struct {
	// RunID is the only Run Identity accepted by this Analysis Session.
	RunID string
	// Nodes is the complete allowlisted cluster node set.
	Nodes []uint64
	// MetricQueries maps stable query IDs to server-owned PromQL expressions.
	MetricQueries map[string]string
	// MaxResponseBytes is the JSON response ceiling for every tool call.
	MaxResponseBytes int
	// SourceTimeout is the default private source deadline.
	SourceTimeout time.Duration
	// Now supplies deterministic observation timestamps.
	Now func() time.Time
}

// Service validates tool inputs, live run identity, response bounds, and Diagnostic Budget.
type Service struct {
	runID            string
	nodes            map[uint64]struct{}
	metricQueries    map[string]string
	maxResponseBytes int
	sourceTimeout    time.Duration
	now              func() time.Time
	sources          Sources

	diagnosticMu       sync.Mutex
	profileRunning     bool
	activeTraceUntil   time.Time
	cpuProfileUsedSecs int
}

// New creates one run-scoped bounded Analysis Session.
func New(cfg Config, sources Sources) (*Service, error) {
	if strings.TrimSpace(cfg.RunID) == "" || len(cfg.Nodes) == 0 || sources == nil {
		return nil, ErrInvalidConfig
	}
	nodes := make(map[uint64]struct{}, len(cfg.Nodes))
	for _, nodeID := range cfg.Nodes {
		if nodeID == 0 {
			return nil, ErrInvalidConfig
		}
		nodes[nodeID] = struct{}{}
	}
	queries := make(map[string]string, len(cfg.MetricQueries))
	for id, query := range cfg.MetricQueries {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(query) == "" {
			return nil, ErrInvalidConfig
		}
		queries[id] = query
	}
	maxResponseBytes := cfg.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	if maxResponseBytes < 256 {
		return nil, ErrInvalidConfig
	}
	sourceTimeout := cfg.SourceTimeout
	if sourceTimeout == 0 {
		sourceTimeout = defaultSourceTimeout
	}
	if sourceTimeout < time.Second || sourceTimeout > time.Minute {
		return nil, ErrInvalidConfig
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		runID: cfg.RunID, nodes: nodes, metricQueries: queries, maxResponseBytes: maxResponseBytes,
		sourceTimeout: sourceTimeout, now: now, sources: sources,
	}, nil
}

// RunInspect returns current live or released inventory proof for the exact run.
func (s *Service) RunInspect(ctx context.Context, req RunRequest) (Observation, error) {
	if err := s.validateRunID(req.RunID); err != nil {
		return Observation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.sourceTimeout)
	defer cancel()
	inspection, err := s.sources.InspectRun(ctx, s.runID)
	if err != nil {
		return Observation{}, err
	}
	if inspection.RunID != s.runID {
		return Observation{}, ErrRunIdentityMismatch
	}
	if !validScenarioInspection(inspection.Scenario) {
		return Observation{}, ErrRunContractMismatch
	}
	return s.finish(SourceResult{
		Node: "run", Source: "run_inventory", Completeness: CompletenessComplete,
		Warnings: inspection.Warnings, Data: inspection,
	})
}

// ClusterSnapshot returns a bounded aggregate manager/runtime snapshot.
func (s *Service) ClusterSnapshot(ctx context.Context, req RunRequest) (Observation, error) {
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.ClusterSnapshot(callCtx)
	})
}

// WorkloadInspect returns the simulator-local final wkbench summary without
// exposing raw worker reports, logs, messages, files, or paths.
func (s *Service) WorkloadInspect(ctx context.Context, req RunRequest) (Observation, error) {
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.WorkloadInspect(callCtx, req.RunID)
	})
}

// MetricsQueryRange executes one allowlisted server-owned PromQL expression.
func (s *Service) MetricsQueryRange(ctx context.Context, req MetricsQueryRangeRequest) (Observation, error) {
	query, ok := s.metricQueries[req.QueryID]
	if !ok || req.Start.IsZero() || req.End.IsZero() || !req.End.After(req.Start) || req.End.Sub(req.Start) > MaxMetricRange ||
		req.Step < time.Second || req.Step > 15*time.Minute || int(req.End.Sub(req.Start)/req.Step)+1 > MaxMetricSamples {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		result, err := s.sources.MetricsQueryRange(callCtx, req, query)
		if result.Window == nil {
			result.Window = &TimeWindow{Start: req.Start, End: req.End}
		}
		return result, err
	})
}

// LogsSearch returns bounded ordinary application logs from one allowlisted node.
func (s *Service) LogsSearch(ctx context.Context, req LogsSearchRequest) (Observation, error) {
	if !s.validNode(req.NodeID) || len(req.Keyword) > 128 || !validLogSource(req.Source) || !validLogLevels(req.Levels) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > MaxLogLines {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.LogsSearch(callCtx, req)
	})
}

// LogsContext returns bounded entries around one opaque log cursor.
func (s *Service) LogsContext(ctx context.Context, req LogsContextRequest) (Observation, error) {
	if !s.validNode(req.NodeID) || !validLogSource(req.Source) || strings.TrimSpace(req.Cursor) == "" || len(req.Cursor) > 2048 ||
		req.Before < 0 || req.After < 0 || req.Before+req.After < 1 || req.Before+req.After > MaxLogLines {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.LogsContext(callCtx, req)
	})
}

// DiagnosticsQuery returns bounded retained diagnostics events.
func (s *Service) DiagnosticsQuery(ctx context.Context, req DiagnosticsQueryRequest) (Observation, error) {
	if req.NodeID != 0 && !s.validNode(req.NodeID) || !boundedStrings(256, req.TraceID, req.ClientMsgNo, req.ChannelKey, req.UID, req.Stage, req.Result) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > MaxDiagnosticsEvents {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.DiagnosticsQuery(callCtx, req)
	})
}

// TaskAuditsQuery returns bounded retained Controller task history.
func (s *Service) TaskAuditsQuery(ctx context.Context, req TaskAuditsQueryRequest) (Observation, error) {
	if req.NodeID != 0 && !s.validNode(req.NodeID) || !boundedStrings(128, req.Kind, req.Status, req.Keyword) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > MaxTaskAudits {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.TaskAuditsQuery(callCtx, req)
	})
}

// TraceStart installs one expiring, one-node diagnostics tracking rule.
func (s *Service) TraceStart(ctx context.Context, req TraceStartRequest) (Observation, error) {
	validSelector := req.Target == "sender_uid" && strings.TrimSpace(req.UID) != "" && len(req.UID) <= 256 ||
		req.Target == "channel" && strings.TrimSpace(req.ChannelID) != "" && len(req.ChannelID) <= 256 && req.ChannelType > 0
	if !s.validNode(req.NodeID) || !validTraceTarget(req.Target) || !validSelector || req.TTL < time.Second || req.TTL > MaxTraceTTL {
		return Observation{}, ErrInvalidToolInput
	}
	if err := s.validateRunID(req.RunID); err != nil {
		return Observation{}, err
	}
	if _, err := s.ensureLive(ctx); err != nil {
		return Observation{}, err
	}
	reservedUntil := s.now().UTC().Add(req.TTL)
	s.diagnosticMu.Lock()
	if s.profileRunning || s.activeTraceUntil.After(s.now().UTC()) {
		s.diagnosticMu.Unlock()
		return Observation{}, ErrDiagnosticBusy
	}
	s.activeTraceUntil = reservedUntil
	s.diagnosticMu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, s.sourceTimeout)
	defer cancel()
	result, err := s.sources.TraceStart(callCtx, req)
	if err != nil {
		s.diagnosticMu.Lock()
		if s.activeTraceUntil.Equal(reservedUntil) {
			s.activeTraceUntil = time.Time{}
		}
		s.diagnosticMu.Unlock()
		return Observation{}, err
	}
	return s.finish(result)
}

// TraceQuery returns bounded retained events for one exact diagnostics trace.
func (s *Service) TraceQuery(ctx context.Context, req TraceQueryRequest) (Observation, error) {
	if req.NodeID != 0 && !s.validNode(req.NodeID) || !validOpaqueID(req.TraceID) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > MaxDiagnosticsEvents {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.TraceQuery(callCtx, req)
	})
}

// ProfileCapture enforces one-at-a-time active diagnostics and CPU budget.
func (s *Service) ProfileCapture(ctx context.Context, req ProfileCaptureRequest) (Observation, error) {
	if err := s.validateRunID(req.RunID); err != nil {
		return Observation{}, err
	}
	if !s.validNode(req.NodeID) || !validProfileRequest(req) {
		return Observation{}, ErrInvalidToolInput
	}
	if _, err := s.ensureLive(ctx); err != nil {
		return Observation{}, err
	}
	s.diagnosticMu.Lock()
	if s.profileRunning || s.activeTraceUntil.After(s.now().UTC()) {
		s.diagnosticMu.Unlock()
		return Observation{}, ErrDiagnosticBusy
	}
	if req.Kind == ProfileCPU {
		if s.cpuProfileUsedSecs+req.Seconds > MaxSessionCPUProfileSeconds {
			s.diagnosticMu.Unlock()
			return Observation{}, ErrDiagnosticBudgetExceeded
		}
		s.cpuProfileUsedSecs += req.Seconds
	}
	s.profileRunning = true
	s.diagnosticMu.Unlock()
	defer func() {
		s.diagnosticMu.Lock()
		s.profileRunning = false
		s.diagnosticMu.Unlock()
	}()
	timeout := s.sourceTimeout
	if req.Kind == ProfileCPU {
		timeout += time.Duration(req.Seconds) * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := s.sources.ProfileCapture(callCtx, req)
	if err != nil {
		return Observation{}, err
	}
	return s.finish(result)
}

// ProfileTop returns a bounded symbolic summary for one opaque capture identity.
func (s *Service) ProfileTop(ctx context.Context, req ProfileTopRequest) (Observation, error) {
	if !validOpaqueID(req.ProfileID) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 30
	}
	if req.Limit < 1 || req.Limit > 100 {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.ProfileTop(callCtx, req)
	})
}

// ProfileList returns bounded capture metadata for one or all allowlisted nodes.
func (s *Service) ProfileList(ctx context.Context, req ProfileListRequest) (Observation, error) {
	if req.NodeID != 0 && !s.validNode(req.NodeID) {
		return Observation{}, ErrInvalidToolInput
	}
	if req.Limit == 0 {
		req.Limit = 50
	}
	if req.Limit < 1 || req.Limit > 100 {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.ProfileList(callCtx, req)
	})
}

// ConfigReadRedacted returns one node's allowlisted effective config snapshot.
func (s *Service) ConfigReadRedacted(ctx context.Context, req ConfigReadRequest) (Observation, error) {
	if !s.validNode(req.NodeID) {
		return Observation{}, ErrInvalidToolInput
	}
	return s.read(ctx, req.RunID, func(callCtx context.Context) (SourceResult, error) {
		return s.sources.ConfigReadRedacted(callCtx, req)
	})
}

func (s *Service) read(ctx context.Context, runID string, call func(context.Context) (SourceResult, error)) (Observation, error) {
	if err := s.validateRunID(runID); err != nil {
		return Observation{}, err
	}
	if _, err := s.ensureLive(ctx); err != nil {
		return Observation{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.sourceTimeout)
	defer cancel()
	result, err := call(callCtx)
	if err != nil {
		return Observation{}, err
	}
	return s.finish(result)
}

func (s *Service) ensureLive(ctx context.Context) (RunInspection, error) {
	callCtx, cancel := context.WithTimeout(ctx, s.sourceTimeout)
	defer cancel()
	inspection, err := s.sources.InspectRun(callCtx, s.runID)
	if err != nil {
		return RunInspection{}, err
	}
	if inspection.RunID != s.runID {
		return RunInspection{}, ErrRunIdentityMismatch
	}
	if !validScenarioInspection(inspection.Scenario) {
		return RunInspection{}, ErrRunContractMismatch
	}
	if inspection.State == RunStateReleased && inspection.InventoryCount == 0 {
		return RunInspection{}, ErrRunReleased
	}
	return inspection, nil
}

func (s *Service) finish(result SourceResult) (Observation, error) {
	completeness := result.Completeness
	if completeness == "" {
		completeness = CompletenessComplete
	}
	if completeness != CompletenessComplete && completeness != CompletenessPartial && completeness != CompletenessUnavailable {
		return Observation{}, ErrInvalidToolInput
	}
	warnings := append([]string(nil), result.Warnings...)
	if warnings == nil {
		warnings = []string{}
	}
	observedAt := s.now().UTC()
	window := result.Window
	if window == nil {
		window = &TimeWindow{Start: observedAt, End: observedAt}
	}
	observation := Observation{
		RunID: s.runID, Node: result.Node, Source: result.Source, ObservedAt: observedAt,
		Window: window, Completeness: completeness, Warnings: warnings, Data: result.Data,
	}
	if observation.Node == "" {
		observation.Node = "cluster"
	}
	if observation.Source == "" {
		observation.Source = "private_api"
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return Observation{}, fmt.Errorf("marshal observation: %w", err)
	}
	if len(encoded) > s.maxResponseBytes {
		return Observation{}, fmt.Errorf("%w: bytes=%d limit=%d", ErrResponseTooLarge, len(encoded), s.maxResponseBytes)
	}
	return observation, nil
}

func (s *Service) validateRunID(runID string) error {
	if strings.TrimSpace(runID) == "" || runID != s.runID {
		return ErrRunIdentityMismatch
	}
	return nil
}

func (s *Service) validNode(nodeID uint64) bool {
	_, ok := s.nodes[nodeID]
	return ok
}

func validLogSource(source string) bool {
	return source == "" || source == "app" || source == "error"
}

func validLogLevels(levels []string) bool {
	if len(levels) > 5 {
		return false
	}
	for _, level := range levels {
		switch strings.ToLower(level) {
		case "debug", "info", "warn", "error", "fatal":
		default:
			return false
		}
	}
	return true
}

func validTraceTarget(target string) bool {
	return target == "sender_uid" || target == "channel"
}

func validProfileRequest(req ProfileCaptureRequest) bool {
	switch req.Kind {
	case ProfileCPU:
		return req.Seconds >= 1 && req.Seconds <= MaxCPUProfileSeconds
	case ProfileHeap, ProfileGoroutine:
		return req.Seconds == 0
	default:
		return false
	}
}

func validOpaqueID(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && opaqueIDPattern.MatchString(value)
}

func boundedStrings(max int, values ...string) bool {
	for _, value := range values {
		if len(value) > max {
			return false
		}
	}
	return true
}

func validScenarioInspection(scenario ScenarioInspection) bool {
	return strings.HasPrefix(scenario.Digest, "sha256:") && len(scenario.Digest) == len("sha256:")+64 &&
		scenario.RandomSeed != 0 && scenario.HashSlotCount == 256 && scenario.Effective != nil &&
		scenario.Effective.Version == "wkbench/v1" && scenario.Effective.Run.RandomSeed == scenario.RandomSeed
}
