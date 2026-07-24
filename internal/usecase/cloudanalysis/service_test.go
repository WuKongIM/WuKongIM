package cloudanalysis

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestServiceRejectsWrongRunAndReleasedRunBeforeReadingData(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	sources := &sourceStub{inspection: RunInspection{RunID: "run-1", State: "released", InventoryCount: 0}}
	service, err := New(Config{RunID: "run-1", Nodes: []uint64{1, 2, 3}, Now: func() time.Time { return now }}, sources)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = service.ClusterSnapshot(context.Background(), RunRequest{RunID: "run-other"})
	if !errors.Is(err, ErrRunIdentityMismatch) {
		t.Fatalf("ClusterSnapshot(wrong run) error = %v, want ErrRunIdentityMismatch", err)
	}
	_, err = service.ClusterSnapshot(context.Background(), RunRequest{RunID: "run-1"})
	if !errors.Is(err, ErrRunReleased) {
		t.Fatalf("ClusterSnapshot(released) error = %v, want ErrRunReleased", err)
	}
	if sources.clusterCalls != 0 {
		t.Fatalf("cluster source calls = %d, want 0", sources.clusterCalls)
	}
}

func TestServiceMetricsQueryUsesAllowlistedIDAndBoundedRange(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	sources := &sourceStub{inspection: RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}}
	service := mustService(t, now, sources)

	_, err := service.MetricsQueryRange(context.Background(), MetricsQueryRangeRequest{
		RunID: "run-1", QueryID: "arbitrary_promql", Start: now.Add(-time.Hour), End: now, Step: 15 * time.Second,
	})
	if !errors.Is(err, ErrInvalidToolInput) {
		t.Fatalf("MetricsQueryRange(arbitrary) error = %v, want ErrInvalidToolInput", err)
	}
	_, err = service.MetricsQueryRange(context.Background(), MetricsQueryRangeRequest{
		RunID: "run-1", QueryID: "send_rate", Start: now.Add(-73 * time.Hour), End: now, Step: 15 * time.Second,
	})
	if !errors.Is(err, ErrInvalidToolInput) {
		t.Fatalf("MetricsQueryRange(long range) error = %v, want ErrInvalidToolInput", err)
	}
	obs, err := service.MetricsQueryRange(context.Background(), MetricsQueryRangeRequest{
		RunID: "run-1", QueryID: "send_rate", Start: now.Add(-time.Hour), End: now, Step: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("MetricsQueryRange(valid) error = %v", err)
	}
	if obs.RunID != "run-1" || obs.Window == nil || sources.metricsRequest.QueryID != "send_rate" {
		t.Fatalf("observation=%#v request=%#v", obs, sources.metricsRequest)
	}
}

func TestRecipientPipelineMetricQueryContractUsesStableAllowlistedIDs(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	ids := []string{
		MetricQueryDeliveryRecipientAuthorityResolveRate,
		MetricQueryDeliveryRecipientAuthorityResolveItemsRate,
		MetricQueryDeliveryRecipientAuthorityResolveTargetsRate,
		MetricQueryDeliveryRecipientAuthorityResolveP99,
		MetricQueryPresenceEndpointLookupRate,
		MetricQueryPresenceEndpointLookupItemsRate,
		MetricQueryPresenceEndpointLookupGroupsRate,
		MetricQueryPresenceEndpointLookupP99,
		MetricQueryDeliveryAckBatchCumulative,
		MetricQueryDeliveryAckBatchItemsCumulative,
		MetricQueryDeliveryAckBatchShardsCumulative,
		MetricQueryDeliveryAckBatchRejectedCumulative,
		MetricQueryDeliveryAckBatchRollbackCumulative,
		MetricQueryDeliveryAckBatchP99,
	}
	queries := make(map[string]string, len(ids))
	for _, id := range ids {
		if !opaqueIDPattern.MatchString(id) {
			t.Fatalf("metric query ID %q is not a stable opaque identifier", id)
		}
		if _, exists := queries[id]; exists {
			t.Fatalf("duplicate metric query ID %q", id)
		}
		queries[id] = "up"
	}

	sources := &sourceStub{inspection: RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}}
	service, err := New(Config{
		RunID: "run-1", Nodes: []uint64{1, 2, 3}, MetricQueries: queries, Now: func() time.Time { return now },
	}, sources)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, id := range ids {
		_, err := service.MetricsQueryRange(context.Background(), MetricsQueryRangeRequest{
			RunID: "run-1", QueryID: id, Start: now.Add(-5 * time.Minute), End: now, Step: 15 * time.Second,
		})
		if err != nil {
			t.Fatalf("MetricsQueryRange(%q) error = %v", id, err)
		}
		if sources.metricsRequest.QueryID != id {
			t.Fatalf("source query ID = %q, want %q", sources.metricsRequest.QueryID, id)
		}
	}
}

func TestServiceBoundsLogsAndDiagnostics(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	service := mustService(t, now, &sourceStub{inspection: RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}})

	_, err := service.LogsSearch(context.Background(), LogsSearchRequest{RunID: "run-1", NodeID: 99, Limit: 10})
	if !errors.Is(err, ErrInvalidToolInput) {
		t.Fatalf("LogsSearch(node) error = %v, want ErrInvalidToolInput", err)
	}
	_, err = service.LogsSearch(context.Background(), LogsSearchRequest{RunID: "run-1", NodeID: 1, Limit: 201})
	if !errors.Is(err, ErrInvalidToolInput) {
		t.Fatalf("LogsSearch(limit) error = %v, want ErrInvalidToolInput", err)
	}
	_, err = service.DiagnosticsQuery(context.Background(), DiagnosticsQueryRequest{RunID: "run-1", Limit: 501})
	if !errors.Is(err, ErrInvalidToolInput) {
		t.Fatalf("DiagnosticsQuery(limit) error = %v, want ErrInvalidToolInput", err)
	}
}

func TestServiceAllowsOnlyBoundedHeapProfileSampleTypes(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	sources := &sourceStub{inspection: RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}}
	service := mustService(t, now, sources)

	_, err := service.ProfileTop(context.Background(), ProfileTopRequest{
		RunID: "run-1", ProfileID: "p-1-heap-1", SampleType: "goroutine",
	})
	if !errors.Is(err, ErrInvalidToolInput) {
		t.Fatalf("ProfileTop(unbounded sample type) error = %v, want ErrInvalidToolInput", err)
	}
	if _, err := service.ProfileTop(context.Background(), ProfileTopRequest{
		RunID: "run-1", ProfileID: "p-1-heap-1", SampleType: ProfileSampleAllocSpace,
	}); err != nil {
		t.Fatalf("ProfileTop(alloc_space) error = %v", err)
	}
	if sources.profileTopRequest.SampleType != ProfileSampleAllocSpace {
		t.Fatalf("ProfileTop() sample type = %q, want %q", sources.profileTopRequest.SampleType, ProfileSampleAllocSpace)
	}
}

func TestServiceCPUProfileBudgetIsSessionScopedAndSerialized(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	sources := &sourceStub{
		inspection:     RunInspection{RunID: "run-1", State: "running", InventoryCount: 12},
		profileStarted: make(chan struct{}, 1),
		profileRelease: make(chan struct{}),
	}
	service := mustService(t, now, sources)

	firstDone := make(chan error, 1)
	go func() {
		_, err := service.ProfileCapture(context.Background(), ProfileCaptureRequest{
			RunID: "run-1", NodeID: 1, Kind: ProfileCPU, Seconds: 30,
		})
		firstDone <- err
	}()
	<-sources.profileStarted

	_, err := service.ProfileCapture(context.Background(), ProfileCaptureRequest{
		RunID: "run-1", NodeID: 2, Kind: ProfileCPU, Seconds: 1,
	})
	if !errors.Is(err, ErrDiagnosticBusy) {
		t.Fatalf("concurrent ProfileCapture() error = %v, want ErrDiagnosticBusy", err)
	}
	close(sources.profileRelease)
	if err := <-firstDone; err != nil {
		t.Fatalf("first ProfileCapture() error = %v", err)
	}

	sources.profileRelease = nil
	if _, err := service.ProfileCapture(context.Background(), ProfileCaptureRequest{RunID: "run-1", NodeID: 2, Kind: ProfileCPU, Seconds: 30}); err != nil {
		t.Fatalf("second 30s ProfileCapture() error = %v", err)
	}
	_, err = service.ProfileCapture(context.Background(), ProfileCaptureRequest{RunID: "run-1", NodeID: 3, Kind: ProfileCPU, Seconds: 1})
	if !errors.Is(err, ErrDiagnosticBudgetExceeded) {
		t.Fatalf("third ProfileCapture() error = %v, want ErrDiagnosticBudgetExceeded", err)
	}
}

func TestServiceSerializesTraceAndProfileActiveDiagnostics(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	sources := &sourceStub{inspection: RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}}
	service, err := New(Config{
		RunID: "run-1", Nodes: []uint64{1, 2, 3}, Now: func() time.Time { return now },
		MetricQueries: map[string]string{"send_rate": "sum(rate(wk_send[1m]))"},
	}, sources)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TraceStart(context.Background(), TraceStartRequest{
		RunID: "run-1", NodeID: 1, Target: "sender_uid", UID: "u-1", TTL: time.Minute,
	}); err != nil {
		t.Fatalf("TraceStart() error = %v", err)
	}
	if _, err := service.TraceStart(context.Background(), TraceStartRequest{
		RunID: "run-1", NodeID: 2, Target: "sender_uid", UID: "u-2", TTL: time.Minute,
	}); !errors.Is(err, ErrDiagnosticBusy) {
		t.Fatalf("second TraceStart() error = %v, want ErrDiagnosticBusy", err)
	}
	if _, err := service.ProfileCapture(context.Background(), ProfileCaptureRequest{
		RunID: "run-1", NodeID: 2, Kind: ProfileHeap,
	}); !errors.Is(err, ErrDiagnosticBusy) {
		t.Fatalf("ProfileCapture(during trace) error = %v, want ErrDiagnosticBusy", err)
	}
	now = now.Add(time.Minute + time.Second)
	if _, err := service.ProfileCapture(context.Background(), ProfileCaptureRequest{
		RunID: "run-1", NodeID: 2, Kind: ProfileHeap,
	}); err != nil {
		t.Fatalf("ProfileCapture(after trace expiry) error = %v", err)
	}
}

func TestServiceRejectsOversizedObservation(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	sources := &sourceStub{
		inspection:  RunInspection{RunID: "run-1", State: "running", InventoryCount: 12},
		clusterData: strings.Repeat("x", 4096),
	}
	service, err := New(Config{
		RunID: "run-1", Nodes: []uint64{1, 2, 3}, Now: func() time.Time { return now },
		MaxResponseBytes: 512, MetricQueries: map[string]string{"send_rate": "sum(rate(wk_send[1m]))"},
	}, sources)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = service.ClusterSnapshot(context.Background(), RunRequest{RunID: "run-1"})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("ClusterSnapshot() error = %v, want ErrResponseTooLarge", err)
	}
}

func TestRunInspectReportsReleasedWithoutError(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	sources := &sourceStub{inspection: RunInspection{RunID: "run-1", State: "released", InventoryCount: 0}}
	service := mustService(t, now, sources)
	obs, err := service.RunInspect(context.Background(), RunRequest{RunID: "run-1"})
	if err != nil {
		t.Fatalf("RunInspect() error = %v", err)
	}
	if obs.Completeness != CompletenessComplete || obs.Window == nil || obs.Data.(RunInspection).State != RunStateReleased {
		t.Fatalf("observation = %#v", obs)
	}
}

func mustService(t *testing.T, now time.Time, sources *sourceStub) *Service {
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

type sourceStub struct {
	mu                sync.Mutex
	inspection        RunInspection
	clusterCalls      int
	clusterData       any
	metricsRequest    MetricsQueryRangeRequest
	profileStarted    chan struct{}
	profileRelease    chan struct{}
	profileTopRequest ProfileTopRequest
}

func (s *sourceStub) InspectRun(context.Context, string) (RunInspection, error) {
	inspection := s.inspection
	if inspection.Scenario.Effective == nil {
		inspection.Scenario = testScenarioInspection()
	}
	return inspection, nil
}

func (s *sourceStub) WorkloadInspect(_ context.Context, runID string) (SourceResult, error) {
	return SourceResult{
		Node: "sim", Source: "wkbench_summary", Completeness: CompletenessComplete,
		Data: WorkloadInspection{RunID: runID, State: "completed", Status: "passed"},
	}, nil
}

func testScenarioInspection() ScenarioInspection {
	effective := model.Scenario{Version: "wkbench/v1", Run: model.RunConfig{ID: "test", RandomSeed: 42}}
	digest, _ := model.DigestScenario(effective)
	return ScenarioInspection{Digest: digest, RandomSeed: 42, HashSlotCount: 256, Effective: &effective}
}

func (s *sourceStub) ClusterSnapshot(context.Context) (SourceResult, error) {
	s.clusterCalls++
	data := s.clusterData
	if data == nil {
		data = map[string]any{"nodes": 3}
	}
	return SourceResult{Data: data, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) MetricsQueryRange(_ context.Context, req MetricsQueryRangeRequest, _ string) (SourceResult, error) {
	s.metricsRequest = req
	return SourceResult{Data: map[string]any{"series": 1}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) LogsSearch(context.Context, LogsSearchRequest) (SourceResult, error) {
	return SourceResult{Data: []any{}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) LogsContext(context.Context, LogsContextRequest) (SourceResult, error) {
	return SourceResult{Data: []any{}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) DiagnosticsQuery(context.Context, DiagnosticsQueryRequest) (SourceResult, error) {
	return SourceResult{Data: []any{}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) TaskAuditsQuery(context.Context, TaskAuditsQueryRequest) (SourceResult, error) {
	return SourceResult{Data: []any{}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) TraceStart(context.Context, TraceStartRequest) (SourceResult, error) {
	return SourceResult{Data: map[string]any{"started": true}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) TraceQuery(context.Context, TraceQueryRequest) (SourceResult, error) {
	return SourceResult{Data: []any{}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) ProfileCapture(_ context.Context, req ProfileCaptureRequest) (SourceResult, error) {
	if req.Kind == ProfileCPU && s.profileStarted != nil {
		select {
		case s.profileStarted <- struct{}{}:
		default:
		}
	}
	if s.profileRelease != nil {
		<-s.profileRelease
	}
	return SourceResult{Data: map[string]any{"profile_id": "p-1"}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) ProfileTop(_ context.Context, req ProfileTopRequest) (SourceResult, error) {
	s.profileTopRequest = req
	return SourceResult{Data: []any{}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) ProfileList(context.Context, ProfileListRequest) (SourceResult, error) {
	return SourceResult{Data: []any{}, Completeness: CompletenessComplete}, nil
}

func (s *sourceStub) ConfigReadRedacted(context.Context, ConfigReadRequest) (SourceResult, error) {
	return SourceResult{Data: map[string]any{}, Completeness: CompletenessComplete}, nil
}
