package cloudanalysis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewRejectsUnsafeSessionConfiguration(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	validConfig := func() Config {
		return Config{
			RunID:            "run-1",
			Nodes:            []uint64{1, 2, 3},
			MetricQueries:    map[string]string{"send_rate": "sum(rate(wk_send[1m]))"},
			MaxResponseBytes: 1024,
			SourceTimeout:    2 * time.Second,
			Now:              func() time.Time { return now },
		}
	}
	tests := []struct {
		name   string
		mutate func(*Config)
		nilSrc bool
	}{
		{name: "blank run identity", mutate: func(cfg *Config) { cfg.RunID = "  " }},
		{name: "missing node allowlist", mutate: func(cfg *Config) { cfg.Nodes = nil }},
		{name: "zero node identity", mutate: func(cfg *Config) { cfg.Nodes = []uint64{1, 0} }},
		{name: "blank metric query id", mutate: func(cfg *Config) { cfg.MetricQueries = map[string]string{"": "up"} }},
		{name: "blank metric query", mutate: func(cfg *Config) { cfg.MetricQueries = map[string]string{"up": " "} }},
		{name: "response bound below envelope minimum", mutate: func(cfg *Config) { cfg.MaxResponseBytes = 255 }},
		{name: "source timeout below minimum", mutate: func(cfg *Config) { cfg.SourceTimeout = time.Second - 1 }},
		{name: "source timeout above maximum", mutate: func(cfg *Config) { cfg.SourceTimeout = time.Minute + 1 }},
		{name: "missing sources", mutate: func(*Config) {}, nilSrc: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			var sources Sources = &sourceStub{}
			if tt.nilSrc {
				sources = nil
			}
			if _, err := New(cfg, sources); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}

	service, err := New(Config{RunID: "run-1", Nodes: []uint64{1}}, &sourceStub{})
	if err != nil {
		t.Fatalf("New(defaults) error = %v", err)
	}
	if service.maxResponseBytes != defaultMaxResponseBytes || service.sourceTimeout != defaultSourceTimeout || service.now == nil {
		t.Fatalf("defaults = bytes:%d timeout:%v now-nil:%v", service.maxResponseBytes, service.sourceTimeout, service.now == nil)
	}
}

func TestRunInspectRejectsUntrustedIdentityEvidence(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sourceFailure := errors.New("inventory unavailable")
	invalidScenario := testScenarioInspection()
	invalidScenario.HashSlotCount = 255
	tests := []struct {
		name       string
		inspection RunInspection
		inspectErr error
		wantErr    error
	}{
		{
			name:       "source failure",
			inspection: RunInspection{RunID: "run-1", State: "running", InventoryCount: 3},
			inspectErr: sourceFailure,
			wantErr:    sourceFailure,
		},
		{
			name:       "different run",
			inspection: RunInspection{RunID: "run-other", State: "running", InventoryCount: 3},
			wantErr:    ErrRunIdentityMismatch,
		},
		{
			name:       "invalid scenario identity",
			inspection: RunInspection{RunID: "run-1", State: "running", InventoryCount: 3, Scenario: invalidScenario},
			wantErr:    ErrRunContractMismatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources := newContractSources(tt.inspection)
			sources.inspectErr = tt.inspectErr
			service := mustContractService(t, now, sources)

			if _, err := service.RunInspect(context.Background(), RunRequest{RunID: "run-1"}); !errors.Is(err, tt.wantErr) {
				t.Fatalf("RunInspect() error = %v, want %v", err, tt.wantErr)
			}
			if sources.inspectCalls != 1 || sources.inspectedRunID != "run-1" {
				t.Fatalf("InspectRun calls=%d run=%q, want one exact run-1 call", sources.inspectCalls, sources.inspectedRunID)
			}
		})
	}
}

func TestServiceFailsClosedBeforeLiveSourceOnInvalidRunEvidence(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sourceFailure := errors.New("provider inventory unavailable")
	invalidScenario := testScenarioInspection()
	invalidScenario.Digest = "sha256:not-a-digest"
	tests := []struct {
		name       string
		inspection RunInspection
		inspectErr error
		wantErr    error
	}{
		{
			name:       "inventory error",
			inspection: RunInspection{RunID: "run-1", State: "running", InventoryCount: 3},
			inspectErr: sourceFailure,
			wantErr:    sourceFailure,
		},
		{
			name:       "identity mismatch",
			inspection: RunInspection{RunID: "run-other", State: "running", InventoryCount: 3},
			wantErr:    ErrRunIdentityMismatch,
		},
		{
			name:       "scenario mismatch",
			inspection: RunInspection{RunID: "run-1", State: "running", InventoryCount: 3, Scenario: invalidScenario},
			wantErr:    ErrRunContractMismatch,
		},
		{
			name:       "released empty inventory",
			inspection: RunInspection{RunID: "run-1", State: RunStateReleased, InventoryCount: 0},
			wantErr:    ErrRunReleased,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources := newContractSources(tt.inspection)
			sources.inspectErr = tt.inspectErr
			service := mustContractService(t, now, sources)

			if _, err := service.WorkloadInspect(context.Background(), RunRequest{RunID: "run-1"}); !errors.Is(err, tt.wantErr) {
				t.Fatalf("WorkloadInspect() error = %v, want %v", err, tt.wantErr)
			}
			if sources.calls["workload"] != 0 {
				t.Fatalf("workload source calls = %d, want 0 before live proof", sources.calls["workload"])
			}
		})
	}

	canceledSources := newContractSources(RunInspection{RunID: "run-1", State: "running", InventoryCount: 3})
	canceledService := mustContractService(t, now, canceledSources)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := canceledService.WorkloadInspect(ctx, RunRequest{RunID: "run-1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("WorkloadInspect(canceled) error = %v, want %v", err, context.Canceled)
	}
	if canceledSources.calls["workload"] != 0 {
		t.Fatalf("workload source calls after canceled live proof = %d, want 0", canceledSources.calls["workload"])
	}
}

func TestServiceReadToolsForwardBoundedSelectorsAndProjectEvidence(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	window := &TimeWindow{Start: now.Add(-time.Minute), End: now}
	sources := newContractSources(RunInspection{RunID: "run-1", State: "running", InventoryCount: 3})
	sources.result = SourceResult{
		Node: "node-1", Source: "bounded_fixture", Window: window, Completeness: CompletenessPartial,
		Warnings: []string{"one source was unavailable"}, Data: map[string]any{"rows": 2},
	}
	service := mustContractService(t, now, sources)

	workload, err := service.WorkloadInspect(context.Background(), RunRequest{RunID: "run-1"})
	requireProjectedObservation(t, workload, err, now, window)
	if sources.workloadRunID != "run-1" {
		t.Fatalf("WorkloadInspect source run = %q, want run-1", sources.workloadRunID)
	}
	sources.result.Warnings[0] = "mutated after return"
	if workload.Warnings[0] != "one source was unavailable" {
		t.Fatalf("observation warnings alias source memory: %v", workload.Warnings)
	}
	sources.result.Warnings[0] = "one source was unavailable"

	logs, err := service.LogsSearch(context.Background(), LogsSearchRequest{
		RunID: "run-1", NodeID: 1, Source: "error", Keyword: "raft", Levels: []string{"WARN", "fatal"},
	})
	requireProjectedObservation(t, logs, err, now, window)
	if sources.logsSearchRequest.Limit != 100 || sources.logsSearchRequest.Source != "error" {
		t.Fatalf("LogsSearch source request = %#v", sources.logsSearchRequest)
	}

	logContext, err := service.LogsContext(context.Background(), LogsContextRequest{
		RunID: "run-1", NodeID: 1, Source: "app", Cursor: "opaque:42", Before: 2, After: 3,
	})
	requireProjectedObservation(t, logContext, err, now, window)
	if sources.logsContextRequest.Cursor != "opaque:42" || sources.logsContextRequest.Before != 2 || sources.logsContextRequest.After != 3 {
		t.Fatalf("LogsContext source request = %#v", sources.logsContextRequest)
	}

	diagnostics, err := service.DiagnosticsQuery(context.Background(), DiagnosticsQueryRequest{
		RunID: "run-1", NodeID: 1, SlotID: 7, Stage: "append", Result: "failed",
	})
	requireProjectedObservation(t, diagnostics, err, now, window)
	if sources.diagnosticsRequest.Limit != 100 || sources.diagnosticsRequest.SlotID != 7 {
		t.Fatalf("DiagnosticsQuery source request = %#v", sources.diagnosticsRequest)
	}

	audits, err := service.TaskAuditsQuery(context.Background(), TaskAuditsQueryRequest{
		RunID: "run-1", NodeID: 2, SlotID: 8, Kind: "migrate", Status: "failed", Keyword: "timeout",
	})
	requireProjectedObservation(t, audits, err, now, window)
	if sources.taskAuditsRequest.Limit != 100 || sources.taskAuditsRequest.NodeID != 2 {
		t.Fatalf("TaskAuditsQuery source request = %#v", sources.taskAuditsRequest)
	}

	trace, err := service.TraceQuery(context.Background(), TraceQueryRequest{
		RunID: "run-1", TraceID: "trace-1", NodeID: 3,
	})
	requireProjectedObservation(t, trace, err, now, window)
	if sources.traceQueryRequest.Limit != 100 || sources.traceQueryRequest.TraceID != "trace-1" {
		t.Fatalf("TraceQuery source request = %#v", sources.traceQueryRequest)
	}

	profiles, err := service.ProfileList(context.Background(), ProfileListRequest{RunID: "run-1"})
	requireProjectedObservation(t, profiles, err, now, window)
	if sources.profileListRequest.Limit != 50 {
		t.Fatalf("ProfileList source request = %#v", sources.profileListRequest)
	}

	config, err := service.ConfigReadRedacted(context.Background(), ConfigReadRequest{RunID: "run-1", NodeID: 2})
	requireProjectedObservation(t, config, err, now, window)
	if sources.configReadRequest.NodeID != 2 {
		t.Fatalf("ConfigReadRedacted source request = %#v", sources.configReadRequest)
	}
}

func TestServiceRejectsSelectorsOutsideClosedQueryContracts(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sources := newContractSources(RunInspection{RunID: "run-1", State: "running", InventoryCount: 3})
	service := mustContractService(t, now, sources)
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "metric resolution below one second",
			call: func() error {
				_, err := service.MetricsQueryRange(context.Background(), MetricsQueryRangeRequest{
					RunID: "run-1", QueryID: "send_rate", Start: now.Add(-time.Minute), End: now, Step: time.Second - 1,
				})
				return err
			},
		},
		{
			name: "metric sample count above bound",
			call: func() error {
				_, err := service.MetricsQueryRange(context.Background(), MetricsQueryRangeRequest{
					RunID: "run-1", QueryID: "send_rate", Start: now.Add(-2 * time.Hour), End: now, Step: time.Second,
				})
				return err
			},
		},
		{name: "unknown log source", call: func() error {
			_, err := service.LogsSearch(context.Background(), LogsSearchRequest{RunID: "run-1", NodeID: 1, Source: "kernel"})
			return err
		}},
		{name: "unknown log level", call: func() error {
			_, err := service.LogsSearch(context.Background(), LogsSearchRequest{RunID: "run-1", NodeID: 1, Levels: []string{"trace"}})
			return err
		}},
		{name: "unbounded log level list", call: func() error {
			_, err := service.LogsSearch(context.Background(), LogsSearchRequest{RunID: "run-1", NodeID: 1, Levels: []string{"info", "info", "info", "info", "info", "info"}})
			return err
		}},
		{name: "log context without surrounding rows", call: func() error {
			_, err := service.LogsContext(context.Background(), LogsContextRequest{RunID: "run-1", NodeID: 1, Cursor: "cursor"})
			return err
		}},
		{name: "log context without cursor", call: func() error {
			_, err := service.LogsContext(context.Background(), LogsContextRequest{RunID: "run-1", NodeID: 1, Before: 1})
			return err
		}},
		{name: "diagnostics identity filter above bound", call: func() error {
			_, err := service.DiagnosticsQuery(context.Background(), DiagnosticsQueryRequest{RunID: "run-1", UID: strings.Repeat("u", 257)})
			return err
		}},
		{name: "task audit filter above bound", call: func() error {
			_, err := service.TaskAuditsQuery(context.Background(), TaskAuditsQueryRequest{RunID: "run-1", Keyword: strings.Repeat("x", 129)})
			return err
		}},
		{name: "trace selector can not escape opaque id", call: func() error {
			_, err := service.TraceQuery(context.Background(), TraceQueryRequest{RunID: "run-1", TraceID: "../raw-profile"})
			return err
		}},
		{name: "active trace channel requires type", call: func() error {
			_, err := service.TraceStart(context.Background(), TraceStartRequest{RunID: "run-1", NodeID: 1, Target: "channel", ChannelID: "c-1", TTL: time.Minute})
			return err
		}},
		{name: "profile kind is closed", call: func() error {
			_, err := service.ProfileCapture(context.Background(), ProfileCaptureRequest{RunID: "run-1", NodeID: 1, Kind: "mutex"})
			return err
		}},
		{name: "profile identity can not be a path", call: func() error {
			_, err := service.ProfileTop(context.Background(), ProfileTopRequest{RunID: "run-1", ProfileID: "../cpu.pprof"})
			return err
		}},
		{name: "profile list node must be allowlisted", call: func() error {
			_, err := service.ProfileList(context.Background(), ProfileListRequest{RunID: "run-1", NodeID: 99})
			return err
		}},
		{name: "config node must be allowlisted", call: func() error {
			_, err := service.ConfigReadRedacted(context.Background(), ConfigReadRequest{RunID: "run-1", NodeID: 99})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrInvalidToolInput) {
				t.Fatalf("tool error = %v, want %v", err, ErrInvalidToolInput)
			}
		})
	}
	if sources.inspectCalls != 0 {
		t.Fatalf("invalid selectors reached run inventory %d times", sources.inspectCalls)
	}
}

func TestServicePropagatesSourceFailuresAndReleasesDiagnosticSerialization(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sourceFailure := errors.New("private source unavailable")

	readSources := newContractSources(RunInspection{RunID: "run-1", State: "running", InventoryCount: 3})
	readSources.sourceErr = sourceFailure
	readService := mustContractService(t, now, readSources)
	if _, err := readService.WorkloadInspect(context.Background(), RunRequest{RunID: "run-1"}); !errors.Is(err, sourceFailure) {
		t.Fatalf("WorkloadInspect() error = %v, want %v", err, sourceFailure)
	}

	traceSources := newContractSources(RunInspection{RunID: "run-1", State: "running", InventoryCount: 3})
	traceSources.sourceErr = sourceFailure
	traceService := mustContractService(t, now, traceSources)
	traceRequest := TraceStartRequest{RunID: "run-1", NodeID: 1, Target: "sender_uid", UID: "u-1", TTL: time.Minute}
	if _, err := traceService.TraceStart(context.Background(), traceRequest); !errors.Is(err, sourceFailure) {
		t.Fatalf("TraceStart() error = %v, want %v", err, sourceFailure)
	}
	if !traceService.activeTraceUntil.IsZero() {
		t.Fatalf("failed TraceStart retained reservation until %v", traceService.activeTraceUntil)
	}
	traceSources.sourceErr = nil
	if _, err := traceService.TraceStart(context.Background(), traceRequest); err != nil {
		t.Fatalf("TraceStart() after source recovery error = %v", err)
	}

	profileSources := newContractSources(RunInspection{RunID: "run-1", State: "running", InventoryCount: 3})
	profileSources.sourceErr = sourceFailure
	profileService := mustContractService(t, now, profileSources)
	profileRequest := ProfileCaptureRequest{RunID: "run-1", NodeID: 1, Kind: ProfileHeap}
	if _, err := profileService.ProfileCapture(context.Background(), profileRequest); !errors.Is(err, sourceFailure) {
		t.Fatalf("ProfileCapture() error = %v, want %v", err, sourceFailure)
	}
	if profileService.profileRunning {
		t.Fatal("failed ProfileCapture retained the serialization gate")
	}
	profileSources.sourceErr = nil
	if _, err := profileService.ProfileCapture(context.Background(), profileRequest); err != nil {
		t.Fatalf("ProfileCapture() after source recovery error = %v", err)
	}
}

func TestServiceRejectsInvalidOrUnencodableSourceProjection(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sources := newContractSources(RunInspection{RunID: "run-1", State: "running", InventoryCount: 3})
	service := mustContractService(t, now, sources)

	sources.result = SourceResult{Completeness: "future", Data: map[string]any{"nodes": 3}}
	if _, err := service.ClusterSnapshot(context.Background(), RunRequest{RunID: "run-1"}); !errors.Is(err, ErrInvalidToolInput) {
		t.Fatalf("ClusterSnapshot(invalid completeness) error = %v, want %v", err, ErrInvalidToolInput)
	}

	sources.result = SourceResult{Data: make(chan int)}
	if _, err := service.ClusterSnapshot(context.Background(), RunRequest{RunID: "run-1"}); err == nil || !strings.Contains(err.Error(), "marshal observation") {
		t.Fatalf("ClusterSnapshot(unencodable data) error = %v, want marshal observation error", err)
	}

	sources.result = SourceResult{Data: map[string]any{"nodes": 3}}
	obs, err := service.ClusterSnapshot(context.Background(), RunRequest{RunID: "run-1"})
	if err != nil {
		t.Fatalf("ClusterSnapshot(default projection) error = %v", err)
	}
	if obs.Node != "cluster" || obs.Source != "private_api" || obs.Completeness != CompletenessComplete || obs.Window == nil || obs.Warnings == nil {
		t.Fatalf("default observation projection = %#v", obs)
	}
}

func requireProjectedObservation(t *testing.T, obs Observation, err error, now time.Time, window *TimeWindow) {
	t.Helper()
	if err != nil {
		t.Fatalf("tool error = %v", err)
	}
	if obs.RunID != "run-1" || obs.Node != "node-1" || obs.Source != "bounded_fixture" || !obs.ObservedAt.Equal(now) || obs.Window != window || obs.Completeness != CompletenessPartial {
		t.Fatalf("observation = %#v", obs)
	}
	if len(obs.Warnings) != 1 || obs.Warnings[0] != "one source was unavailable" {
		t.Fatalf("observation warnings = %v", obs.Warnings)
	}
}

func mustContractService(t *testing.T, now time.Time, sources Sources) *Service {
	t.Helper()
	service, err := New(Config{
		RunID: "run-1", Nodes: []uint64{1, 2, 3}, Now: func() time.Time { return now },
		MetricQueries: map[string]string{"send_rate": "sum(rate(wk_send[1m]))"},
	}, sources)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

type contractSources struct {
	*sourceStub
	inspectErr error
	sourceErr  error
	result     SourceResult
	calls      map[string]int

	inspectCalls          int
	inspectedRunID        string
	workloadRunID         string
	logsSearchRequest     LogsSearchRequest
	logsContextRequest    LogsContextRequest
	diagnosticsRequest    DiagnosticsQueryRequest
	taskAuditsRequest     TaskAuditsQueryRequest
	traceStartRequest     TraceStartRequest
	traceQueryRequest     TraceQueryRequest
	profileCaptureRequest ProfileCaptureRequest
	profileListRequest    ProfileListRequest
	configReadRequest     ConfigReadRequest
}

func newContractSources(inspection RunInspection) *contractSources {
	return &contractSources{
		sourceStub: &sourceStub{inspection: inspection},
		calls:      make(map[string]int),
		result:     SourceResult{Data: map[string]any{}, Completeness: CompletenessComplete},
	}
}

func (s *contractSources) InspectRun(ctx context.Context, runID string) (RunInspection, error) {
	s.inspectCalls++
	s.inspectedRunID = runID
	if err := ctx.Err(); err != nil {
		return RunInspection{}, err
	}
	if s.inspectErr != nil {
		return RunInspection{}, s.inspectErr
	}
	return s.sourceStub.InspectRun(ctx, runID)
}

func (s *contractSources) sourceResult(name string) (SourceResult, error) {
	s.calls[name]++
	return s.result, s.sourceErr
}

func (s *contractSources) WorkloadInspect(_ context.Context, runID string) (SourceResult, error) {
	s.workloadRunID = runID
	return s.sourceResult("workload")
}

func (s *contractSources) ClusterSnapshot(context.Context) (SourceResult, error) {
	return s.sourceResult("cluster")
}

func (s *contractSources) LogsSearch(_ context.Context, req LogsSearchRequest) (SourceResult, error) {
	s.logsSearchRequest = req
	return s.sourceResult("logs_search")
}

func (s *contractSources) LogsContext(_ context.Context, req LogsContextRequest) (SourceResult, error) {
	s.logsContextRequest = req
	return s.sourceResult("logs_context")
}

func (s *contractSources) DiagnosticsQuery(_ context.Context, req DiagnosticsQueryRequest) (SourceResult, error) {
	s.diagnosticsRequest = req
	return s.sourceResult("diagnostics")
}

func (s *contractSources) TaskAuditsQuery(_ context.Context, req TaskAuditsQueryRequest) (SourceResult, error) {
	s.taskAuditsRequest = req
	return s.sourceResult("task_audits")
}

func (s *contractSources) TraceStart(_ context.Context, req TraceStartRequest) (SourceResult, error) {
	s.traceStartRequest = req
	return s.sourceResult("trace_start")
}

func (s *contractSources) TraceQuery(_ context.Context, req TraceQueryRequest) (SourceResult, error) {
	s.traceQueryRequest = req
	return s.sourceResult("trace_query")
}

func (s *contractSources) ProfileCapture(_ context.Context, req ProfileCaptureRequest) (SourceResult, error) {
	s.profileCaptureRequest = req
	return s.sourceResult("profile_capture")
}

func (s *contractSources) ProfileList(_ context.Context, req ProfileListRequest) (SourceResult, error) {
	s.profileListRequest = req
	return s.sourceResult("profile_list")
}

func (s *contractSources) ConfigReadRedacted(_ context.Context, req ConfigReadRequest) (SourceResult, error) {
	s.configReadRequest = req
	return s.sourceResult("config_read")
}

var _ Sources = (*contractSources)(nil)
