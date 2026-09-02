package cluster

import (
	"context"
	"strings"
	"testing"
	"time"

	runtimeops "github.com/WuKongIM/WuKongIM/internal/runtime/opsmcp"
	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	observe "github.com/WuKongIM/WuKongIM/internal/usecase/opsobserve"
)

func TestOpsObservationExactInventoryBoundaries(t *testing.T) {
	t.Parallel()

	inventory := &contractOpsInventory{
		slots: []management.Slot{
			{SlotID: 3, State: management.SlotState{Quorum: "ready", Sync: "matched"}},
			{SlotID: 7, State: management.SlotState{Quorum: "lost", Sync: "mismatch"}},
		},
		channel: management.ChannelRuntimeMeta{
			ChannelID: "orders", ChannelType: 2, SlotID: 7,
			Degraded: true, DegradedReason: "isr_below_minimum",
			WriteFenceToken: "must-not-leak", WriteFenceVersion: 9,
		},
	}
	source := NewOpsObservationSource(OpsObservationSourceConfig{Inventory: inventory})

	slotResult, err := source.SlotInspect(context.Background(), observe.SlotInspectRequest{SlotID: 7})
	if err != nil {
		t.Fatalf("SlotInspect() error = %v", err)
	}
	if slotResult.Status != observe.StatusDegraded || len(slotResult.ReasonCodes) != 1 || slotResult.ReasonCodes[0].Code != "slot_not_converged" {
		t.Fatalf("SlotInspect() result = %#v", slotResult)
	}
	if data, ok := slotResult.Data.(slotData); !ok || data.SlotID != 7 {
		t.Fatalf("SlotInspect() data = %#v, want exact Slot 7", slotResult.Data)
	}
	if _, err := source.SlotInspect(context.Background(), observe.SlotInspectRequest{SlotID: 8}); err == nil {
		t.Fatal("SlotInspect(missing) error = nil")
	}

	channelResult, err := source.ChannelRuntimeInspect(context.Background(), observe.ChannelRuntimeInspectRequest{
		ChannelID: "orders", ChannelType: 2,
	})
	if err != nil {
		t.Fatalf("ChannelRuntimeInspect() error = %v", err)
	}
	if inventory.channelID != "orders" || inventory.channelType != 2 {
		t.Fatalf("runtime lookup = %q/%d, want orders/2", inventory.channelID, inventory.channelType)
	}
	data, ok := channelResult.Data.(channelRuntimeData)
	if !ok || data.ChannelID != "orders" || data.WriteFenceVersion != 9 || channelResult.Status != observe.StatusDegraded {
		t.Fatalf("ChannelRuntimeInspect() result = %#v", channelResult)
	}
	if encoded := strings.ToLower(strings.TrimSpace(data.WriteFenceReason)); strings.Contains(encoded, "must-not-leak") {
		t.Fatalf("safe runtime projection leaked write fence token: %#v", data)
	}
}

func TestOpsObservationEvidencePortsPreserveExactSelectorsAndSafety(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0).UTC()
	end := start.Add(time.Minute)
	tasks := &contractOpsTasks{
		active: management.ListControllerTasksResponse{Total: 1, Items: []management.ControllerTask{{
			TaskID: "move-1", Kind: "slot_move", Status: "failed", LastError: "private controller detail",
		}}},
		audits: management.ControllerTaskAuditListResponse{Total: 1, Items: []management.ControllerTaskAuditSnapshot{{
			TaskID: "move-1", Kind: "slot_move", Status: "failed",
		}}},
	}
	metrics := &contractOpsMetrics{data: observe.MetricRangeData{QueryID: observe.MetricQuerySlotApplyGap}}
	diagnosticsReader := &contractOpsDiagnostics{response: management.DiagnosticsQueryResponse{
		Scope: "cluster", Status: management.DiagnosticsStatusPartial,
		Events: []management.DiagnosticsEvent{{TraceID: "trace-1", Stage: "slot_apply", Error: "private stack"}},
	}}
	configReader := &contractOpsConfig{snapshot: management.NodeConfigSnapshot{
		NodeID: 4,
		Groups: []management.NodeConfigGroup{{ID: "cluster", Items: []management.NodeConfigItem{{
			Key: "WK_CLUSTER_LISTEN_ADDR", Value: "10.0.0.4:7000",
		}}}},
	}}
	profiles := &contractOpsProfiles{data: map[string]any{"kind": "cpu"}, window: runtimeops.ProfileAnalysisWindow{Start: start, End: end}}
	source := NewOpsObservationSource(OpsObservationSourceConfig{
		Tasks: tasks, Metrics: metrics, Diagnostics: diagnosticsReader, Config: configReader, Profiles: profiles,
	})

	taskResult, err := source.ControllerTasksQuery(context.Background(), observe.ControllerTasksQueryRequest{
		Kind: "slot_move", Status: "failed", Limit: 5,
	})
	if err != nil {
		t.Fatalf("ControllerTasksQuery() error = %v", err)
	}
	if tasks.activeRequest.Kind != "slot_move" || tasks.auditRequest.Status != "failed" || tasks.activeRequest.Limit != 5 {
		t.Fatalf("task selectors active=%#v audits=%#v", tasks.activeRequest, tasks.auditRequest)
	}
	if taskResult.Status != observe.StatusDegraded || taskResult.Completeness != observe.CompletenessComplete {
		t.Fatalf("task result = %#v", taskResult)
	}
	taskData := taskResult.Data.(controllerTasksData)
	if len(taskData.Active) != 1 || taskData.Active[0].TaskID != "move-1" || len(taskData.Retained) != 1 {
		t.Fatalf("task data = %#v", taskData)
	}

	metricRequest := observe.MetricsQueryRangeRequest{
		QueryID: observe.MetricQuerySlotApplyGap, NodeID: 4, Start: start, End: end, StepSeconds: 15,
	}
	metricResult, err := source.MetricsQueryRange(context.Background(), metricRequest)
	if err != nil {
		t.Fatalf("MetricsQueryRange() error = %v", err)
	}
	if metrics.request != metricRequest || metricResult.Window == nil || metricResult.Window.Start != start || metricResult.Window.End != end {
		t.Fatalf("metric request/result = %#v / %#v", metrics.request, metricResult)
	}

	diagnosticsResult, err := source.DiagnosticsQuery(context.Background(), observe.DiagnosticsQueryRequest{
		NodeID: 4, SlotID: 7, TraceID: "trace-1", Stage: "slot_apply", Result: "error",
		Start: start, End: end, Limit: 12,
	})
	if err != nil {
		t.Fatalf("DiagnosticsQuery() error = %v", err)
	}
	if diagnosticsReader.request.NodeID != 4 || diagnosticsReader.request.Query.TraceID != "trace-1" || diagnosticsReader.request.Query.SlotID != 7 || diagnosticsReader.request.Query.Limit != 12 {
		t.Fatalf("diagnostics selector = %#v", diagnosticsReader.request)
	}
	if diagnosticsResult.Completeness != observe.CompletenessPartial || diagnosticsResult.Window == nil {
		t.Fatalf("diagnostics result = %#v", diagnosticsResult)
	}
	diagnosticData := diagnosticsResult.Data.(diagnosticsData)
	if len(diagnosticData.Events) != 1 || diagnosticData.Events[0].TraceID != "trace-1" {
		t.Fatalf("diagnostics data = %#v", diagnosticData)
	}

	configResult, err := source.ConfigReadRedacted(context.Background(), observe.ConfigReadRedactedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ConfigReadRedacted() error = %v", err)
	}
	configData := configResult.Data.(configData)
	if configReader.nodeID != 4 || configData.Groups[0].Items[0].Value != "******" || !configData.Groups[0].Items[0].Redacted {
		t.Fatalf("config result = %#v", configResult)
	}

	profileResult, err := source.PprofAnalyze(context.Background(), observe.PprofAnalyzeRequest{
		NodeID: 4, Kind: "cpu", Seconds: 10, Rows: 20,
	})
	if err != nil {
		t.Fatalf("PprofAnalyze() error = %v", err)
	}
	if profiles.request.NodeID != 4 || profiles.request.Kind != "cpu" || profiles.request.Seconds != 10 || profiles.request.Rows != 20 {
		t.Fatalf("profile request = %#v", profiles.request)
	}
	if profileResult.Window == nil || profileResult.Window.Start != start || profileResult.Window.End != end {
		t.Fatalf("profile result = %#v", profileResult)
	}
}

func TestOpsObservationLogsStayUntrustedAndBounded(t *testing.T) {
	t.Parallel()

	logs := &contractOpsLogs{response: management.ApplicationLogEntriesResponse{
		Cursor: "next", Rotated: true,
		Items: []management.ApplicationLogEntry{{Seq: 9, Level: "error", Raw: strings.Repeat("x", observe.MaxLogLineBytes+20)}},
	}}
	source := NewOpsObservationSource(OpsObservationSourceConfig{Logs: logs})

	search, err := source.LogsSearch(context.Background(), observe.LogsSearchRequest{
		NodeID: 4, Source: "error", Keyword: "timeout", Levels: []string{"error"}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("LogsSearch() error = %v", err)
	}
	if logs.request.NodeID != 4 || logs.request.Source != "error" || logs.request.Keyword != "timeout" || logs.request.Limit != 10 {
		t.Fatalf("search request = %#v", logs.request)
	}
	page := search.Data.(rawLogPage)
	if page.ContentTrust != "untrusted" || len(page.Items) != 1 || len(page.Items[0].Raw) != observe.MaxLogLineBytes || !page.Items[0].Truncated {
		t.Fatalf("bounded log page = %#v", page)
	}
	if len(search.Warnings) != 2 {
		t.Fatalf("log warnings = %#v, want trust and truncation warnings", search.Warnings)
	}

	_, err = source.LogsContext(context.Background(), observe.LogsContextRequest{
		NodeID: 4, Source: "app", Cursor: "opaque", Before: 2, After: 3,
	})
	if err != nil {
		t.Fatalf("LogsContext() error = %v", err)
	}
	if logs.request.Cursor != "opaque" || logs.request.Keyword != "" || logs.request.Limit != 5 || logs.request.Before != 2 || logs.request.After != 3 {
		t.Fatalf("context request = %#v", logs.request)
	}
}

func TestOpsObservationNodeInspectCombinesCompleteHealthyEvidence(t *testing.T) {
	t.Parallel()

	inventory := &contractOpsInventory{dynamic: management.DynamicNodeDiagnosticsResponse{
		Node: management.Node{NodeID: 4, Status: "alive", Health: management.NodeHealth{Freshness: "fresh", RuntimeReady: true}},
		Sources: management.DynamicNodeDiagnosticSources{
			ControlSnapshot: management.DynamicNodeDiagnosticSource{Available: true},
			TaskAudit:       management.DynamicNodeDiagnosticSource{Available: true},
			SlotRuntime:     management.DynamicNodeDiagnosticSource{Available: true},
		},
	}}
	diagnosticsReader := &contractOpsDiagnostics{response: management.DiagnosticsQueryResponse{
		Status: management.DiagnosticsStatusOK,
		Events: []management.DiagnosticsEvent{{TraceID: "trace-4", Stage: "append", Result: "ok"}},
	}}
	metrics := &contractOpsMetrics{data: observe.MetricRangeData{QueryID: observe.MetricQueryRuntimeQueuePressure}}
	source := NewOpsObservationSource(OpsObservationSourceConfig{
		Inventory: inventory, Diagnostics: diagnosticsReader, Metrics: metrics,
	})

	result, err := source.NodeInspect(context.Background(), observe.NodeInspectRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("NodeInspect() error = %v", err)
	}
	if result.Status != observe.StatusHealthy || result.Completeness != observe.CompletenessComplete || result.Freshness != observe.FreshnessFresh || len(result.Warnings) != 0 {
		t.Fatalf("NodeInspect() result = %#v", result)
	}
	if inventory.dynamicRequest.NodeID != 4 || inventory.dynamicRequest.TaskLimit != 20 || inventory.dynamicRequest.AuditLimit != 10 || inventory.dynamicRequest.SlotLimit != 256 {
		t.Fatalf("dynamic diagnostics request = %#v", inventory.dynamicRequest)
	}
	if diagnosticsReader.request.NodeID != 4 || diagnosticsReader.request.Query.Limit != 50 {
		t.Fatalf("retained diagnostics request = %#v", diagnosticsReader.request)
	}
	if metrics.request.QueryID != observe.MetricQueryRuntimeQueuePressure || metrics.request.NodeID != 4 {
		t.Fatalf("workqueue metric request = %#v", metrics.request)
	}
	data := result.Data.(nodeInspectData)
	if data.Diagnostics == nil || len(data.Diagnostics.Events) != 1 || data.WorkqueueMetrics == nil {
		t.Fatalf("combined node data = %#v", data)
	}
}

func TestOpsObservationSourceFailsClosedWhenCapabilitiesAreUnwired(t *testing.T) {
	t.Parallel()

	source := NewOpsObservationSource(OpsObservationSourceConfig{})
	checks := []struct {
		name string
		run  func() error
	}{
		{name: "cluster inventory", run: func() error {
			_, err := source.ClusterHealth(context.Background(), observe.ClusterHealthRequest{})
			return err
		}},
		{name: "node inventory", run: func() error {
			_, err := source.NodeInspect(context.Background(), observe.NodeInspectRequest{NodeID: 1})
			return err
		}},
		{name: "slot inventory", run: func() error {
			_, err := source.SlotInspect(context.Background(), observe.SlotInspectRequest{SlotID: 1})
			return err
		}},
		{name: "channel runtime", run: func() error {
			_, err := source.ChannelRuntimeInspect(context.Background(), observe.ChannelRuntimeInspectRequest{ChannelID: "g1", ChannelType: 2})
			return err
		}},
		{name: "controller tasks", run: func() error {
			_, err := source.ControllerTasksQuery(context.Background(), observe.ControllerTasksQueryRequest{Limit: 1})
			return err
		}},
		{name: "metrics", run: func() error {
			_, err := source.MetricsQueryRange(context.Background(), observe.MetricsQueryRangeRequest{QueryID: observe.MetricQueryTargetsUp})
			return err
		}},
		{name: "logs", run: func() error {
			_, err := source.LogsSearch(context.Background(), observe.LogsSearchRequest{NodeID: 1, Source: "app", Limit: 1})
			return err
		}},
		{name: "diagnostics", run: func() error {
			_, err := source.DiagnosticsQuery(context.Background(), observe.DiagnosticsQueryRequest{Limit: 1})
			return err
		}},
		{name: "config", run: func() error {
			_, err := source.ConfigReadRedacted(context.Background(), observe.ConfigReadRedactedRequest{NodeID: 1})
			return err
		}},
		{name: "backup", run: func() error {
			_, err := source.BackupInspect(context.Background(), observe.BackupInspectRequest{Limit: 1})
			return err
		}},
		{name: "profiles", run: func() error {
			_, err := source.PprofAnalyze(context.Background(), observe.PprofAnalyzeRequest{NodeID: 1, Kind: "heap", Rows: 1})
			return err
		}},
	}
	for _, check := range checks {
		if err := check.run(); err == nil {
			t.Errorf("%s capability returned nil error while unwired", check.name)
		}
	}
}

type contractOpsInventory struct {
	slots          []management.Slot
	channel        management.ChannelRuntimeMeta
	channelID      string
	channelType    int64
	dynamic        management.DynamicNodeDiagnosticsResponse
	dynamicRequest management.DynamicNodeDiagnosticsRequest
}

func (*contractOpsInventory) ListNodes(context.Context) (management.NodeList, error) {
	return management.NodeList{}, nil
}

func (f *contractOpsInventory) ListSlots(context.Context, management.ListSlotsOptions) ([]management.Slot, error) {
	return append([]management.Slot(nil), f.slots...), nil
}

func (f *contractOpsInventory) DynamicNodeDiagnostics(_ context.Context, request management.DynamicNodeDiagnosticsRequest) (management.DynamicNodeDiagnosticsResponse, error) {
	f.dynamicRequest = request
	return f.dynamic, nil
}

func (f *contractOpsInventory) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (management.ChannelRuntimeMeta, error) {
	f.channelID, f.channelType = channelID, channelType
	return f.channel, nil
}

type contractOpsTasks struct {
	active        management.ListControllerTasksResponse
	audits        management.ControllerTaskAuditListResponse
	activeErr     error
	auditErr      error
	activeRequest management.ListControllerTasksRequest
	auditRequest  management.ControllerTaskAuditListRequest
}

func (f *contractOpsTasks) ListControllerTasks(_ context.Context, request management.ListControllerTasksRequest) (management.ListControllerTasksResponse, error) {
	f.activeRequest = request
	return f.active, f.activeErr
}

func (f *contractOpsTasks) ListControllerTaskAudits(_ context.Context, request management.ControllerTaskAuditListRequest) (management.ControllerTaskAuditListResponse, error) {
	f.auditRequest = request
	return f.audits, f.auditErr
}

type contractOpsMetrics struct {
	request observe.MetricsQueryRangeRequest
	data    observe.MetricRangeData
	err     error
}

func (f *contractOpsMetrics) QueryOpsMetrics(_ context.Context, request observe.MetricsQueryRangeRequest) (observe.MetricRangeData, error) {
	f.request = request
	return f.data, f.err
}

type contractOpsDiagnostics struct {
	request  management.DiagnosticsQueryRequest
	response management.DiagnosticsQueryResponse
	err      error
}

func (f *contractOpsDiagnostics) QueryDiagnostics(_ context.Context, request management.DiagnosticsQueryRequest) (management.DiagnosticsQueryResponse, error) {
	f.request = request
	return f.response, f.err
}

type contractOpsConfig struct {
	nodeID   uint64
	snapshot management.NodeConfigSnapshot
	err      error
}

func (f *contractOpsConfig) NodeConfigSnapshot(_ context.Context, nodeID uint64) (management.NodeConfigSnapshot, error) {
	f.nodeID = nodeID
	return f.snapshot, f.err
}

type contractOpsProfiles struct {
	request runtimeops.ProfileAnalysisRequest
	data    any
	window  runtimeops.ProfileAnalysisWindow
	err     error
}

func (f *contractOpsProfiles) AnalyzeOpsProfile(_ context.Context, request runtimeops.ProfileAnalysisRequest) (any, *runtimeops.ProfileAnalysisWindow, error) {
	f.request = request
	if f.err != nil {
		return nil, nil, f.err
	}
	window := f.window
	return f.data, &window, nil
}

type contractOpsLogs struct {
	request  management.ApplicationLogEntriesRequest
	response management.ApplicationLogEntriesResponse
	err      error
}

func (f *contractOpsLogs) ApplicationLogEntries(_ context.Context, request management.ApplicationLogEntriesRequest) (management.ApplicationLogEntriesResponse, error) {
	f.request = request
	return f.response, f.err
}
