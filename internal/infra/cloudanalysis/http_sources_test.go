package cloudanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	pprofprofile "github.com/google/pprof/profile"
)

type stubRunStatusSource struct {
	run cloudsim.Run
}

func (s stubRunStatusSource) Status(context.Context, string) (cloudsim.Run, error) {
	return s.run, nil
}

func TestProviderRunInspectorProvesReleasedEmptyInventory(t *testing.T) {
	createdAt := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	effective := model.Scenario{Version: "wkbench/v1", Run: model.RunConfig{ID: "scenario", RandomSeed: 42}}
	digest, _ := model.DigestScenario(effective)
	scenario := analysis.ScenarioInspection{Digest: digest, RandomSeed: 42, HashSlotCount: 256, Effective: &effective}
	locator := cloudsim.RunLocator{
		Schema: cloudsim.RunLocatorSchemaV1, RunID: "run-1", Provider: "fake", Region: "cn-test",
		AccountIDHash: "sha256:account", Repository: "WuKongIM/WuKongIM", SourceSHA: "abc123",
		ScenarioDigest: digest, CreatedAt: createdAt, ExpiresAt: expiresAt, ProvisionWorkflowRunID: 42,
	}
	inspector := ProviderRunInspector{Locator: locator, Scenario: scenario, Source: stubRunStatusSource{run: cloudsim.Run{
		ID: "run-1", State: cloudsim.StateReleased, Provider: "fake", Region: "cn-test", CreatedAt: createdAt,
		AccountIDHash: "sha256:account", Repository: "WuKongIM/WuKongIM", ExpiresAt: expiresAt,
		Tags:      map[string]string{cloudsim.TagSourceSHA: "abc123", cloudsim.TagScenarioDigest: digest},
		Resources: []cloudsim.Resource{},
	}}}
	got, err := inspector.InspectRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("InspectRun() error = %v", err)
	}
	if got.State != analysis.RunState(cloudsim.StateReleased) || got.InventoryCount != 0 || got.SourceSHA != "abc123" || !got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("InspectRun() = %#v", got)
	}
}

func TestStaticRunInspectorCannotProveReleasedRun(t *testing.T) {
	inspector := StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1", State: analysis.RunStateReleased}}
	_, err := inspector.InspectRun(context.Background(), "run-1")
	if !errors.Is(err, analysis.ErrRunContractMismatch) {
		t.Fatalf("InspectRun() error = %v, want ErrRunContractMismatch", err)
	}
}

func TestHTTPSourcesUseManagerCapabilityCredentialAndPrivateRoutes(t *testing.T) {
	var loginCalls atomic.Int32
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manager/login" {
			loginCalls.Add(1)
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode login: %v", err)
			}
			if body["username"] != "analysis" || body["password"] != "run-token" {
				t.Errorf("login body = %#v", body)
			}
			writeJSON(w, map[string]any{"access_token": "manager-jwt"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer manager-jwt" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/manager/nodes":
			writeJSON(w, map[string]any{"total": 3, "items": []any{map[string]any{"node_id": 1}}})
		case "/manager/runtime/workqueues":
			writeJSON(w, map[string]any{"status": "ok", "nodes": []any{}})
		case "/manager/app-logs":
			if r.URL.Query().Get("node_id") != "2" || r.URL.Query().Get("keyword") != "timeout" || r.URL.Query().Get("limit") != "20" {
				t.Errorf("log query = %s", r.URL.RawQuery)
			}
			writeJSON(w, map[string]any{"node_id": 2, "items": []any{}, "cursor": "next"})
		default:
			t.Errorf("unexpected manager path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer manager.Close()

	sources, err := NewHTTPSources(HTTPConfig{
		Inspector:         StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}},
		ManagerBaseURL:    manager.URL,
		ManagerAuth:       ManagerAuth{Username: "analysis", Password: "run-token"},
		PrometheusBaseURL: manager.URL,
		NodeAPIBaseURLs:   map[uint64]string{1: manager.URL, 2: manager.URL, 3: manager.URL},
	})
	if err != nil {
		t.Fatalf("NewHTTPSources() error = %v", err)
	}
	if _, err := sources.ClusterSnapshot(context.Background()); err != nil {
		t.Fatalf("ClusterSnapshot() error = %v", err)
	}
	if _, err := sources.LogsSearch(context.Background(), analysis.LogsSearchRequest{
		NodeID: 2, Source: "app", Keyword: "timeout", Limit: 20,
	}); err != nil {
		t.Fatalf("LogsSearch() error = %v", err)
	}
	if got := loginCalls.Load(); got != 1 {
		t.Fatalf("login calls = %d, want 1", got)
	}
}

func TestHTTPSourcesMetricsUseResolvedQueryAndBoundedEndpoint(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	var seen url.Values
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("path = %s", r.URL.Path)
		}
		seen = r.URL.Query()
		writeJSON(w, map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{}}})
	}))
	defer prom.Close()

	sources, err := NewHTTPSources(HTTPConfig{
		Inspector:      StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}},
		ManagerBaseURL: prom.URL, PrometheusBaseURL: prom.URL,
		NodeAPIBaseURLs: map[uint64]string{1: prom.URL},
	})
	if err != nil {
		t.Fatalf("NewHTTPSources() error = %v", err)
	}
	req := analysis.MetricsQueryRangeRequest{QueryID: "send_rate", Start: now.Add(-time.Hour), End: now, Step: 15 * time.Second}
	if _, err := sources.MetricsQueryRange(context.Background(), req, `sum(rate(wk_send_total[1m]))`); err != nil {
		t.Fatalf("MetricsQueryRange() error = %v", err)
	}
	if seen.Get("query") != `sum(rate(wk_send_total[1m]))` || seen.Get("step") != "15" {
		t.Fatalf("query values = %v", seen)
	}
}

func TestHTTPSourcesTraceStartTargetsOneNodeAndRecordsTTLWindow(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manager/diagnostics/tracking-rules" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["node_id"] != float64(2) || body["ttl_seconds"] != float64(90) {
			t.Errorf("tracking body = %#v", body)
		}
		writeJSON(w, map[string]any{"status": "ok", "rule": map[string]any{"rule_id": "r1"}})
	}))
	defer manager.Close()
	sources, err := NewHTTPSources(HTTPConfig{
		Inspector:         StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}},
		ManagerBaseURL:    manager.URL,
		ManagerAuth:       ManagerAuth{BearerToken: "manager-token"},
		PrometheusBaseURL: manager.URL,
		NodeAPIBaseURLs:   map[uint64]string{1: manager.URL, 2: manager.URL, 3: manager.URL},
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHTTPSources() error = %v", err)
	}
	result, err := sources.TraceStart(context.Background(), analysis.TraceStartRequest{
		NodeID: 2, Target: "sender_uid", UID: "u1", TTL: 90 * time.Second,
	})
	if err != nil {
		t.Fatalf("TraceStart() error = %v", err)
	}
	if result.Window == nil || !result.Window.Start.Equal(now) || !result.Window.End.Equal(now.Add(90*time.Second)) {
		t.Fatalf("TraceStart() window = %#v", result.Window)
	}
}

func TestHTTPSourcesDiagnosticsQueryForwardsPhysicalSlotID(t *testing.T) {
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manager/diagnostics/events" {
			t.Errorf("path = %s", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("node_id") != "2" || query.Get("slot_id") != "7" ||
			query.Get("stage") != "slot.preferred_leader_reconcile" || query.Get("limit") != "10" {
			t.Errorf("diagnostics query = %s", r.URL.RawQuery)
		}
		writeJSON(w, map[string]any{"status": "ok", "events": []any{}})
	}))
	defer manager.Close()
	sources, err := NewHTTPSources(HTTPConfig{
		Inspector:         StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}},
		ManagerBaseURL:    manager.URL,
		ManagerAuth:       ManagerAuth{BearerToken: "manager-token"},
		PrometheusBaseURL: manager.URL,
		NodeAPIBaseURLs:   map[uint64]string{2: manager.URL},
	})
	if err != nil {
		t.Fatalf("NewHTTPSources() error = %v", err)
	}

	_, err = sources.DiagnosticsQuery(context.Background(), analysis.DiagnosticsQueryRequest{
		NodeID: 2,
		SlotID: 7,
		Stage:  "slot.preferred_leader_reconcile",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("DiagnosticsQuery() error = %v", err)
	}
}

func TestHTTPSourcesCaptureListAndSummarizeProfileWithoutRawBytes(t *testing.T) {
	data := encodedProfile(t)
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/debug/pprof/heap" {
			t.Errorf("profile path = %s", r.URL.Path)
		}
		_, _ = w.Write(data)
	}))
	defer node.Close()

	sources, err := NewHTTPSources(HTTPConfig{
		Inspector:      StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}},
		ManagerBaseURL: node.URL, PrometheusBaseURL: node.URL,
		NodeAPIBaseURLs: map[uint64]string{1: node.URL},
		Now:             func() time.Time { return time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewHTTPSources() error = %v", err)
	}
	captured, err := sources.ProfileCapture(context.Background(), analysis.ProfileCaptureRequest{NodeID: 1, Kind: analysis.ProfileHeap})
	if err != nil {
		t.Fatalf("ProfileCapture() error = %v", err)
	}
	metadata, ok := captured.Data.(ProfileMetadata)
	if !ok || metadata.ProfileID == "" || metadata.SizeBytes != len(data) {
		t.Fatalf("capture data = %#v", captured.Data)
	}
	encoded, _ := json.Marshal(captured.Data)
	if bytes.Contains(encoded, data) {
		t.Fatal("capture response contains raw profile bytes")
	}

	top, err := sources.ProfileTop(context.Background(), analysis.ProfileTopRequest{ProfileID: metadata.ProfileID, Limit: 10})
	if err != nil {
		t.Fatalf("ProfileTop() error = %v", err)
	}
	rows, ok := top.Data.([]ProfileTopRow)
	if !ok || len(rows) != 1 || rows[0].Function != "hotFunction" || rows[0].Cumulative != 4096 || rows[0].SampleType != "inuse_space" {
		t.Fatalf("top rows = %#v", top.Data)
	}
	allocTop, err := sources.ProfileTop(context.Background(), analysis.ProfileTopRequest{
		ProfileID: metadata.ProfileID, Limit: 10, SampleType: analysis.ProfileSampleAllocSpace,
	})
	if err != nil {
		t.Fatalf("ProfileTop(alloc_space) error = %v", err)
	}
	allocRows, ok := allocTop.Data.([]ProfileTopRow)
	if !ok || len(allocRows) != 1 || allocRows[0].Cumulative != 8192 || allocRows[0].SampleType != "alloc_space" {
		t.Fatalf("alloc top rows = %#v", allocTop.Data)
	}
	listed, err := sources.ProfileList(context.Background(), analysis.ProfileListRequest{NodeID: 1, Limit: 10})
	if err != nil {
		t.Fatalf("ProfileList() error = %v", err)
	}
	items := listed.Data.([]ProfileMetadata)
	if len(items) != 1 || items[0].ProfileID != metadata.ProfileID {
		t.Fatalf("profile list = %#v", items)
	}
}

func TestHTTPSourcesRejectsProfileLargerThanRetentionBudget(t *testing.T) {
	data := encodedProfile(t)
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer node.Close()

	sources, err := NewHTTPSources(HTTPConfig{
		Inspector:             StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1", State: "running", InventoryCount: 12}},
		ManagerBaseURL:        node.URL,
		PrometheusBaseURL:     node.URL,
		NodeAPIBaseURLs:       map[uint64]string{1: node.URL},
		MaxStoredProfileBytes: 1,
	})
	if err != nil {
		t.Fatalf("NewHTTPSources() error = %v", err)
	}
	if _, err := sources.ProfileCapture(context.Background(), analysis.ProfileCaptureRequest{NodeID: 1, Kind: analysis.ProfileHeap}); err == nil {
		t.Fatal("ProfileCapture() error = nil, want retention-budget rejection")
	}
	listed, err := sources.ProfileList(context.Background(), analysis.ProfileListRequest{NodeID: 1, Limit: 10})
	if err != nil {
		t.Fatalf("ProfileList() error = %v", err)
	}
	if items := listed.Data.([]ProfileMetadata); len(items) != 0 {
		t.Fatalf("profile list = %#v, want empty", items)
	}
}

func TestNewHTTPSourcesRejectsBaseURLWithQuery(t *testing.T) {
	_, err := NewHTTPSources(HTTPConfig{
		Inspector:         StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1"}},
		ManagerBaseURL:    "http://127.0.0.1:5301?target=other",
		PrometheusBaseURL: "http://127.0.0.1:9090",
		NodeAPIBaseURLs:   map[uint64]string{1: "http://127.0.0.1:5001"},
	})
	if err == nil || !strings.Contains(err.Error(), "manager") {
		t.Fatalf("NewHTTPSources() error = %v, want manager URL rejection", err)
	}
}

func TestNewHTTPSourcesRejectsBaseURLWithUserInfo(t *testing.T) {
	_, err := NewHTTPSources(HTTPConfig{
		Inspector:         StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1"}},
		ManagerBaseURL:    "http://user:password@127.0.0.1:5301",
		PrometheusBaseURL: "http://127.0.0.1:9090",
		NodeAPIBaseURLs:   map[uint64]string{1: "http://127.0.0.1:5001"},
	})
	if err == nil || !strings.Contains(err.Error(), "manager") {
		t.Fatalf("NewHTTPSources() error = %v, want userinfo rejection", err)
	}
}

func encodedProfile(t *testing.T) []byte {
	t.Helper()
	fn := &pprofprofile.Function{ID: 1, Name: "hotFunction", SystemName: "hotFunction", Filename: "hot.go"}
	location := &pprofprofile.Location{ID: 1, Line: []pprofprofile.Line{{Function: fn, Line: 42}}}
	profile := &pprofprofile.Profile{
		SampleType: []*pprofprofile.ValueType{{Type: "alloc_space", Unit: "bytes"}, {Type: "inuse_space", Unit: "bytes"}},
		Sample:     []*pprofprofile.Sample{{Location: []*pprofprofile.Location{location}, Value: []int64{8192, 4096}}},
		Location:   []*pprofprofile.Location{location},
		Function:   []*pprofprofile.Function{fn},
		TimeNanos:  time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC).UnixNano(),
	}
	var buf bytes.Buffer
	if err := profile.Write(&buf); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return buf.Bytes()
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
