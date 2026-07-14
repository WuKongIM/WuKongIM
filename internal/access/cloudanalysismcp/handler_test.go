package cloudanalysismcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHandlerRequiresExactBearerToken(t *testing.T) {
	handler := mustHandler(t, "run-token-0123456789-0123456789-ab")
	server := httptest.NewServer(handler)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, server.URL, nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-token status = %d, want 401", resp.StatusCode)
	}
}

func TestHandlerListsOnlyBoundedAnalysisToolsAndCallsRunInspect(t *testing.T) {
	token := "run-token-0123456789-0123456789-ab"
	handler := mustHandler(t, token)
	server := httptest.NewServer(handler)
	defer server.Close()

	httpClient := &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL, HTTPClient: httpClient, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{
		"cluster_snapshot", "config_read_redacted", "diagnostics_query", "logs_context", "logs_search",
		"metrics_query_range", "profile_capture", "profile_list", "profile_top", "run_inspect",
		"task_audits_query", "trace_query", "trace_start", "workload_inspect",
	}
	sort.Strings(want)
	if len(names) != len(want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("tools = %v, want %v", names, want)
		}
	}

	called, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "run_inspect", Arguments: map[string]any{"run_id": "run-1"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if called.IsError {
		t.Fatalf("CallTool() IsError=true content=%#v", called.Content)
	}
	structured, ok := called.StructuredContent.(map[string]any)
	if !ok || structured["run_id"] != "run-1" || structured["source"] != "run_inventory" || structured["window"] == nil {
		t.Fatalf("structured content = %#v", called.StructuredContent)
	}
}

func mustHandler(t *testing.T, token string) http.Handler {
	t.Helper()
	effective := model.Scenario{Version: "wkbench/v1", Run: model.RunConfig{ID: "test", RandomSeed: 42}}
	digest, _ := model.DigestScenario(effective)
	sources := mcpSourcesStub{inspection: analysis.RunInspection{
		RunID: "run-1", State: "running", InventoryCount: 12,
		Scenario: analysis.ScenarioInspection{Digest: digest, RandomSeed: 42, HashSlotCount: 256, Effective: &effective},
	}}
	service, err := analysis.New(analysis.Config{
		RunID: "run-1", Nodes: []uint64{1, 2, 3}, MetricQueries: map[string]string{"send_rate": "up"},
	}, sources)
	if err != nil {
		t.Fatalf("analysis.New() error = %v", err)
	}
	handler, err := NewHandler(Config{
		RunID: "run-1", Token: token, TokenExpiresAt: time.Now().Add(time.Hour), Service: service,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (t bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

type mcpSourcesStub struct {
	inspection analysis.RunInspection
}

func (s mcpSourcesStub) InspectRun(context.Context, string) (analysis.RunInspection, error) {
	return s.inspection, nil
}

func (mcpSourcesStub) WorkloadInspect(_ context.Context, runID string) (analysis.SourceResult, error) {
	return analysis.SourceResult{
		Node: "sim", Source: "wkbench_summary", Completeness: analysis.CompletenessComplete,
		Data: analysis.WorkloadInspection{RunID: runID, State: "completed", Status: "passed"},
	}, nil
}

func (mcpSourcesStub) ClusterSnapshot(context.Context) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) MetricsQueryRange(context.Context, analysis.MetricsQueryRangeRequest, string) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) LogsSearch(context.Context, analysis.LogsSearchRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) LogsContext(context.Context, analysis.LogsContextRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) DiagnosticsQuery(context.Context, analysis.DiagnosticsQueryRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) TaskAuditsQuery(context.Context, analysis.TaskAuditsQueryRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) TraceStart(context.Context, analysis.TraceStartRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) TraceQuery(context.Context, analysis.TraceQueryRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) ProfileCapture(context.Context, analysis.ProfileCaptureRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) ProfileTop(context.Context, analysis.ProfileTopRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) ProfileList(context.Context, analysis.ProfileListRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}
func (mcpSourcesStub) ConfigReadRedacted(context.Context, analysis.ConfigReadRequest) (analysis.SourceResult, error) {
	return emptySource(), nil
}

func emptySource() analysis.SourceResult {
	return analysis.SourceResult{Node: "cluster", Source: "test", Completeness: analysis.CompletenessComplete, Data: map[string]any{}}
}
