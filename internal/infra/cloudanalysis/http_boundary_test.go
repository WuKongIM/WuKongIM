package cloudanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

func TestHTTPSourcesForwardManagerContractsWithoutNetwork(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	seen := make(map[string]int)
	client := &http.Client{Transport: memoryRoundTripper(func(request *http.Request) (*http.Response, error) {
		if got, want := request.Header.Get("Authorization"), "Bearer manager-token"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		mu.Lock()
		seen[request.URL.Path]++
		mu.Unlock()
		switch request.URL.Path {
		case "/base/manager/app-logs":
			query := request.URL.Query()
			if query.Get("node_id") != "2" || query.Get("source") != "error" || query.Get("cursor") != "cursor-1" || query.Get("limit") != "7" {
				t.Errorf("logs context query = %q", request.URL.RawQuery)
			}
			return jsonHTTPResponse(http.StatusOK, `{"status":"partial","truncated":true,"rotated":true,"notes":["bounded note","",7]}`), nil
		case "/base/manager/controller/task-audits":
			query := request.URL.Query()
			if query.Get("node_id") != "2" || query.Get("slot_id") != "7" || query.Get("kind") != "slot_migration" || query.Get("status") != "failed" || query.Get("keyword") != "timeout" || query.Get("limit") != "9" {
				t.Errorf("task audit query = %q", request.URL.RawQuery)
			}
			return jsonHTTPResponse(http.StatusOK, `{"status":"unavailable","notes":["retention gap"]}`), nil
		case "/base/manager/diagnostics/trace/trace%2F1":
			if got := request.URL.Query().Get("node_id"); got != "3" {
				t.Errorf("trace node_id = %q", got)
			}
			return jsonHTTPResponse(http.StatusOK, `{"events":[]}`), nil
		case "/base/manager/nodes/3/config":
			return jsonHTTPResponse(http.StatusOK, `{"cluster":{"slot_count":256}}`), nil
		case "/base/manager/diagnostics/tracking-rules":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode trace body: %v", err)
			}
			if body["target"] != "channel" || body["channel_id"] != "room-1" || body["channel_type"] != float64(2) {
				t.Errorf("trace body = %#v", body)
			}
			if _, exists := body["uid"]; exists {
				t.Errorf("channel trace unexpectedly contains uid: %#v", body)
			}
			return jsonHTTPResponse(http.StatusOK, `{"rule_id":"rule-1"}`), nil
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.String())
			return jsonHTTPResponse(http.StatusNotFound, `{}`), nil
		}
	})}
	sources, err := NewHTTPSources(HTTPConfig{
		Inspector: StaticRunInspector{Inspection: analysis.RunInspection{
			RunID: "run-1", State: "running", InventoryCount: 12, Warnings: []string{"inventory warning"},
		}},
		ManagerBaseURL:    "http://manager.test/base/",
		ManagerAuth:       ManagerAuth{BearerToken: " manager-token "},
		PrometheusBaseURL: "http://prometheus.test/prom/",
		NodeAPIBaseURLs:   map[uint64]string{1: "http://node-1.test/api/"},
		HTTPClient:        client,
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHTTPSources() error = %v", err)
	}

	inspection, err := sources.InspectRun(context.Background(), "run-1")
	if err != nil || inspection.InventoryCount != 12 {
		t.Fatalf("InspectRun() = %#v, %v", inspection, err)
	}
	inspection.Warnings[0] = "mutated"
	if sources.inspector.(StaticRunInspector).Inspection.Warnings[0] != "inventory warning" {
		t.Fatal("InspectRun returned aliased warnings")
	}
	workload, err := sources.WorkloadInspect(context.Background(), "run-1")
	if err != nil || workload.Completeness != analysis.CompletenessUnavailable {
		t.Fatalf("WorkloadInspect() = %#v, %v", workload, err)
	}
	logs, err := sources.LogsContext(context.Background(), analysis.LogsContextRequest{
		NodeID: 2, Source: "error", Cursor: "cursor-1", Before: 3, After: 4,
	})
	if err != nil || logs.Node != "node-2" || logs.Completeness != analysis.CompletenessPartial || len(logs.Warnings) != 4 {
		t.Fatalf("LogsContext() = %#v, %v", logs, err)
	}
	audits, err := sources.TaskAuditsQuery(context.Background(), analysis.TaskAuditsQueryRequest{
		NodeID: 2, SlotID: 7, Kind: "slot_migration", Status: "failed", Keyword: "timeout", Limit: 9,
	})
	if err != nil || audits.Node != "cluster" || audits.Completeness != analysis.CompletenessPartial || len(audits.Warnings) != 1 {
		t.Fatalf("TaskAuditsQuery() = %#v, %v", audits, err)
	}
	trace, err := sources.TraceQuery(context.Background(), analysis.TraceQueryRequest{TraceID: "trace/1", NodeID: 3, Limit: 5})
	if err != nil || trace.Node != "node-3" {
		t.Fatalf("TraceQuery() = %#v, %v", trace, err)
	}
	config, err := sources.ConfigReadRedacted(context.Background(), analysis.ConfigReadRequest{NodeID: 3})
	if err != nil || config.Node != "node-3" {
		t.Fatalf("ConfigReadRedacted() = %#v, %v", config, err)
	}
	started, err := sources.TraceStart(context.Background(), analysis.TraceStartRequest{
		NodeID: 3, Target: "channel", ChannelID: "room-1", ChannelType: 2, TTL: 30 * time.Second,
	})
	if err != nil || started.Window == nil || !started.Window.End.Equal(now.Add(30*time.Second)) {
		t.Fatalf("TraceStart() = %#v, %v", started, err)
	}
	for _, path := range []string{
		"/base/manager/app-logs", "/base/manager/controller/task-audits", "/base/manager/diagnostics/trace/trace%2F1",
		"/base/manager/nodes/3/config", "/base/manager/diagnostics/tracking-rules",
	} {
		if seen[path] != 1 {
			t.Errorf("request count for %s = %d", path, seen[path])
		}
	}
}

func TestManagerClientRefreshesRejectedCapabilityToken(t *testing.T) {
	baseURL, err := url.Parse("http://manager.test")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	sequence := make([]string, 0, 4)
	client := &http.Client{Transport: memoryRoundTripper(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		sequence = append(sequence, request.URL.Path+" "+request.Header.Get("Authorization"))
		if request.URL.Path == "/manager/login" {
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode login body: %v", err)
			}
			if body["username"] != "analysis" || body["password"] != "run-secret" || request.Header.Get("Content-Type") != "application/json" {
				t.Errorf("login request body=%#v content-type=%q", body, request.Header.Get("Content-Type"))
			}
		}
		switch len(sequence) {
		case 1:
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"token-1"}`), nil
		case 2:
			return jsonHTTPResponse(http.StatusUnauthorized, `{"error":"expired"}`), nil
		case 3:
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"token-2"}`), nil
		case 4:
			return jsonHTTPResponse(http.StatusOK, `{"items":[1]}`), nil
		default:
			return nil, errors.New("unexpected request")
		}
	})}
	manager := newManagerClient(baseURL, ManagerAuth{Username: "analysis", Password: "run-secret"}, client, time.Now)
	var output map[string]any
	if err := manager.doJSON(context.Background(), http.MethodGet, "/manager/app-logs", nil, nil, &output); err != nil {
		t.Fatalf("doJSON() error = %v", err)
	}
	if len(sequence) != 4 || sequence[0] != "/manager/login " || sequence[1] != "/manager/app-logs Bearer token-1" ||
		sequence[2] != "/manager/login " || sequence[3] != "/manager/app-logs Bearer token-2" {
		t.Fatalf("request sequence = %#v", sequence)
	}
	if manager.token != "token-2" || len(output["items"].([]any)) != 1 {
		t.Fatalf("manager state token=%q output=%#v", manager.token, output)
	}
}

func TestManagerClientFailsClosedOnAuthenticationAndResponseErrors(t *testing.T) {
	baseURL, err := url.Parse("http://manager.test")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		auth      ManagerAuth
		roundTrip func(*http.Request) (*http.Response, error)
		want      string
	}{
		{
			name: "missing capability password", auth: ManagerAuth{Username: "analysis"},
			roundTrip: func(*http.Request) (*http.Response, error) { return nil, errors.New("must not request") },
			want:      "password is empty",
		},
		{
			name: "login rejected", auth: ManagerAuth{Username: "analysis", Password: "secret"},
			roundTrip: func(*http.Request) (*http.Response, error) { return jsonHTTPResponse(http.StatusForbidden, `{}`), nil },
			want:      "manager login: status 403",
		},
		{
			name: "login omits token", auth: ManagerAuth{Username: "analysis", Password: "secret"},
			roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonHTTPResponse(http.StatusOK, `{"access_token":" "}`), nil
			},
			want: "no access token",
		},
		{
			name: "transport failure", auth: ManagerAuth{BearerToken: "token"},
			roundTrip: func(*http.Request) (*http.Response, error) { return nil, errors.New("transport failed") },
			want:      "transport failed",
		},
		{
			name: "manager status", auth: ManagerAuth{BearerToken: "token"},
			roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonHTTPResponse(http.StatusBadGateway, "  upstream unavailable  "), nil
			},
			want: "status 502: upstream unavailable",
		},
		{
			name: "malformed JSON", auth: ManagerAuth{BearerToken: "token"},
			roundTrip: func(*http.Request) (*http.Response, error) { return jsonHTTPResponse(http.StatusOK, `{`), nil },
			want:      "decode",
		},
		{
			name: "oversized JSON", auth: ManagerAuth{BearerToken: "token"},
			roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonHTTPResponse(http.StatusOK, strings.Repeat("x", maxPrivateJSONBytes+1)), nil
			},
			want: "response exceeds",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newManagerClient(baseURL, test.auth, &http.Client{Transport: memoryRoundTripper(test.roundTrip)}, time.Now)
			var output any
			err := manager.doJSON(context.Background(), http.MethodGet, "/manager/test", nil, nil, &output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("doJSON() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestManagerClusterSnapshotRequiresNodesAndTreatsWorkqueuesAsOptional(t *testing.T) {
	baseURL, err := url.Parse("http://manager.test")
	if err != nil {
		t.Fatal(err)
	}
	newManager := func(nodesStatus, queuesStatus int) *managerClient {
		client := &http.Client{Transport: memoryRoundTripper(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case "/manager/nodes":
				return jsonHTTPResponse(nodesStatus, `{"items":[]}`), nil
			case "/manager/runtime/workqueues":
				return jsonHTTPResponse(queuesStatus, `{"items":[]}`), nil
			default:
				return jsonHTTPResponse(http.StatusNotFound, `{}`), nil
			}
		})}
		return newManagerClient(baseURL, ManagerAuth{}, client, time.Now)
	}

	partial, err := newManager(http.StatusOK, http.StatusServiceUnavailable).clusterSnapshot(context.Background())
	if err != nil || partial.Completeness != analysis.CompletenessPartial || len(partial.Warnings) != 1 {
		t.Fatalf("partial clusterSnapshot() = %#v, %v", partial, err)
	}
	if data := partial.Data.(map[string]any); data["nodes"] == nil || data["workqueues"] != nil {
		t.Fatalf("partial data = %#v", data)
	}
	if _, err := newManager(http.StatusServiceUnavailable, http.StatusOK).clusterSnapshot(context.Background()); err == nil {
		t.Fatal("clusterSnapshot() error = nil when nodes are unavailable")
	}
}

func TestManagerHelpersPreserveBoundedCompletenessSemantics(t *testing.T) {
	if got := completenessFromManager([]string{"not", "an", "object"}); got != analysis.CompletenessComplete {
		t.Fatalf("non-object completeness = %q", got)
	}
	if got := completenessFromManager(map[string]any{"status": "unavailable"}); got != analysis.CompletenessPartial {
		t.Fatalf("unavailable completeness = %q", got)
	}
	if got := warningsFromManager([]string{"not", "an", "object"}); got != nil {
		t.Fatalf("non-object warnings = %#v", got)
	}
	if got := boundedText([]byte("  0123456789  "), 5); got != "012" {
		t.Fatalf("boundedText() = %q", got)
	}
}

func TestNewHTTPSourcesRejectsIncompleteOrUnsafeAdapterConfiguration(t *testing.T) {
	valid := HTTPConfig{
		Inspector:         StaticRunInspector{Inspection: analysis.RunInspection{RunID: "run-1"}},
		ManagerBaseURL:    "http://manager.test",
		PrometheusBaseURL: "http://prometheus.test",
		NodeAPIBaseURLs:   map[uint64]string{1: "http://node-1.test"},
	}
	tests := []struct {
		name   string
		mutate func(*HTTPConfig)
		want   string
	}{
		{name: "missing inspector", mutate: func(cfg *HTTPConfig) { cfg.Inspector = nil }, want: "invalid HTTP config"},
		{name: "missing node allowlist", mutate: func(cfg *HTTPConfig) { cfg.NodeAPIBaseURLs = nil }, want: "invalid HTTP config"},
		{name: "unsafe Prometheus origin", mutate: func(cfg *HTTPConfig) { cfg.PrometheusBaseURL += "?query=escape" }, want: "prometheus base URL"},
		{name: "zero node id", mutate: func(cfg *HTTPConfig) { cfg.NodeAPIBaseURLs = map[uint64]string{0: "http://node.test"} }, want: "node id"},
		{name: "unsafe node origin", mutate: func(cfg *HTTPConfig) { cfg.NodeAPIBaseURLs = map[uint64]string{1: "ftp://node.test"} }, want: "node 1 base URL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			_, err := NewHTTPSources(cfg)
			if !errors.Is(err, ErrInvalidHTTPConfig) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewHTTPSources() error = %v, want ErrInvalidHTTPConfig containing %q", err, test.want)
			}
		})
	}
}

type memoryRoundTripper func(*http.Request) (*http.Response, error)

func (f memoryRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type failingReadCloser struct {
	err error
}

func (r failingReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (failingReadCloser) Close() error               { return nil }
