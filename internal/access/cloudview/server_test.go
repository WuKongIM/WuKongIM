package cloudview

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/WuKongIM/WuKongIM/internal/runtime/cloudviewstate"
)

func TestServerRoutesManagerToHealthyNode(t *testing.T) {
	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("unhealthy-manager"))
	}))
	t.Cleanup(unhealthy.Close)
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte("healthy-manager"))
	}))
	t.Cleanup(healthy.Close)

	server, err := New(Options{
		RunID:              "run-1",
		PublicBaseURL:      "http://198.51.100.20:19443",
		HealthCheckTimeout: time.Second,
		Nodes: []NodeUpstream{
			{ID: 1, APIBaseURL: unhealthy.URL, ManagerBaseURL: unhealthy.URL, WebSocketBaseURL: unhealthy.URL},
			{ID: 2, APIBaseURL: healthy.URL, ManagerBaseURL: healthy.URL, WebSocketBaseURL: healthy.URL},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK || recorder.Body.String() != "healthy-manager" {
		t.Fatalf("GET / = status %d body %q, want healthy manager response", recorder.Code, recorder.Body.String())
	}
}

func TestServerCachesNodeHealthBetweenRequests(t *testing.T) {
	var healthChecks atomic.Int64
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			healthChecks.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte("manager"))
	}))
	t.Cleanup(node.Close)
	server, err := New(Options{
		RunID:               "run-1",
		PublicBaseURL:       "http://198.51.100.20:19443",
		HealthCheckInterval: time.Minute,
		Nodes: []NodeUpstream{{
			ID: 1, APIBaseURL: node.URL, ManagerBaseURL: node.URL, WebSocketBaseURL: node.URL,
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for range 2 {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET / status = %d", recorder.Code)
		}
	}
	if got := healthChecks.Load(); got != 1 {
		t.Fatalf("health checks = %d, want one cached readiness probe", got)
	}
}

func TestServerRateLimitsHTTPPerSourceIP(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte("manager"))
	}))
	t.Cleanup(node.Close)
	server, err := New(Options{
		RunID:         "run-1",
		PublicBaseURL: "http://198.51.100.20:19443",
		Limits: Limits{
			HTTPRequestsPerSecondPerIP:  1,
			HTTPBurstPerIP:              1,
			HTTPRequestsPerSecondGlobal: 100,
			HTTPBurstGlobal:             100,
		},
		Nodes: []NodeUpstream{{
			ID: 1, APIBaseURL: node.URL, ManagerBaseURL: node.URL, WebSocketBaseURL: node.URL,
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	statuses := make([]int, 0, 2)
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "203.0.113.10:50000"
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		statuses = append(statuses, recorder.Code)
	}
	if statuses[0] != http.StatusOK || statuses[1] != http.StatusTooManyRequests {
		t.Fatalf("HTTP statuses = %#v, want [200 429]", statuses)
	}
}

func TestServerStatusReportsRemoteIPv4WithoutChangingRunPurity(t *testing.T) {
	server, err := New(Options{
		RunID:         "run-1",
		PublicBaseURL: "http://198.51.100.20:19443",
		Nodes: []NodeUpstream{{
			ID: 1, APIBaseURL: "http://127.0.0.1:1", ManagerBaseURL: "http://127.0.0.1:1", WebSocketBaseURL: "http://127.0.0.1:1",
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/cloud-view/status", nil)
	request.RemoteAddr = "203.0.113.10:50000"
	request.Header.Set("X-Forwarded-For", "198.51.100.99")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	var status struct {
		ObservedIPv4     string `json:"observed_ipv4"`
		Interactive      bool   `json:"interactive"`
		OperatorModified bool   `json:"operator_modified"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode cloud view status: %v; body=%q", err, recorder.Body.String())
	}
	if recorder.Code != http.StatusOK || status.ObservedIPv4 != "203.0.113.10" {
		t.Fatalf("cloud view status = %#v code=%d, want transport peer IPv4", status, recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cloud view status Cache-Control = %q, want no-store", got)
	}
	if status.Interactive || status.OperatorModified {
		t.Fatalf("cloud view status changed run purity: %#v", status)
	}
}

func TestServerLimitsConcurrentWebSocketsPerSourceIP(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(api.Close)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	webSocket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_, _, _ = connection.ReadMessage()
	}))
	t.Cleanup(webSocket.Close)
	server, err := New(Options{
		RunID:         "run-1",
		PublicBaseURL: "http://198.51.100.20:19443",
		Limits: Limits{
			WebSocketConnectionsPerIP:  1,
			WebSocketConnectionsGlobal: 10,
		},
		Nodes: []NodeUpstream{{
			ID: 1, APIBaseURL: api.URL, ManagerBaseURL: api.URL, WebSocketBaseURL: webSocket.URL,
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	public := httptest.NewServer(server.Handler())
	t.Cleanup(public.Close)
	webSocketURL := "ws" + public.URL[len("http"):] + "/"

	first, _, err := websocket.DefaultDialer.Dial(webSocketURL, nil)
	if err != nil {
		t.Fatalf("dial first WebSocket: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, response, err := websocket.DefaultDialer.Dial(webSocketURL, nil)
	if second != nil {
		_ = second.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second WebSocket = response %#v err %v, want 429", response, err)
	}
}

func TestServerReportsInteractiveAndOperatorModifiedState(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/readyz":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/demo/":
			_, _ = w.Write([]byte("demo"))
		case r.URL.Path == "/manager/test-action" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(node.Close)
	server, err := New(Options{
		RunID:         "gh-123-1",
		PublicBaseURL: "http://198.51.100.20:19443",
		StatePath:     t.TempDir() + "/state.json",
		MetricsPath:   t.TempDir() + "/cloudview.prom",
		Nodes: []NodeUpstream{{
			ID: 1, APIBaseURL: node.URL, ManagerBaseURL: node.URL, WebSocketBaseURL: node.URL,
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/demo/", nil),
		httptest.NewRequest(http.MethodPost, "/manager/test-action", nil),
	} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code < http.StatusOK || recorder.Code >= http.StatusMultipleChoices {
			t.Fatalf("%s %s status = %d", request.Method, request.URL.Path, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/cloud-view/status", nil))
	var status struct {
		RunID              string `json:"run_id"`
		Interactive        bool   `json:"interactive"`
		OperatorModified   bool   `json:"operator_modified"`
		PersistenceHealthy bool   `json:"persistence_healthy"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode cloud view status: %v; body=%q", err, recorder.Body.String())
	}
	if recorder.Code != http.StatusOK || status.RunID != "gh-123-1" || !status.Interactive ||
		!status.OperatorModified || !status.PersistenceHealthy {
		t.Fatalf("cloud view status = %#v code=%d", status, recorder.Code)
	}
}

func TestServerDoesNotMarkManagerLoginAsOperatorModification(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/manager/login" && r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "token"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(node.Close)
	server, err := New(Options{
		RunID:         "gh-123-1",
		PublicBaseURL: "http://198.51.100.20:19443",
		Nodes: []NodeUpstream{{
			ID: 1, APIBaseURL: node.URL, ManagerBaseURL: node.URL, WebSocketBaseURL: node.URL,
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/manager/login", nil))
	statusRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(statusRecorder, httptest.NewRequest(http.MethodGet, "/cloud-view/status", nil))
	var status cloudviewstate.State
	if err := json.Unmarshal(statusRecorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if recorder.Code != http.StatusOK || status.OperatorModified {
		t.Fatalf("login status=%d cloud view state=%#v, want unmodified run", recorder.Code, status)
	}
}

func TestServerPreservesMonotonicRunStateAcrossRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	metricsPath := filepath.Join(filepath.Dir(statePath), "state.prom")
	options := Options{
		RunID: "run-1", PublicBaseURL: "http://198.51.100.20:19443",
		Nodes:     []NodeUpstream{{ID: 1, APIBaseURL: "http://127.0.0.1:1", ManagerBaseURL: "http://127.0.0.1:1", WebSocketBaseURL: "http://127.0.0.1:1"}},
		StatePath: statePath, MetricsPath: metricsPath,
	}
	first, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.state.MarkInteractive(); err != nil {
		t.Fatal(err)
	}
	if err := first.state.MarkOperatorModified(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	state := restarted.state.Snapshot()
	if !state.Interactive || !state.OperatorModified {
		t.Fatalf("restarted state = %#v, want both monotonic markers preserved", state)
	}
}

func TestServerRoutesWebSocketToHealthyNode(t *testing.T) {
	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	t.Cleanup(unhealthy.Close)
	healthyAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(healthyAPI.Close)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	healthyWebSocket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		messageType, _, err := connection.ReadMessage()
		if err == nil {
			_ = connection.WriteMessage(messageType, []byte("node-2"))
		}
	}))
	t.Cleanup(healthyWebSocket.Close)

	server, err := New(Options{
		RunID:         "run-1",
		PublicBaseURL: "http://198.51.100.20:19443",
		Nodes: []NodeUpstream{
			{ID: 1, APIBaseURL: unhealthy.URL, ManagerBaseURL: unhealthy.URL, WebSocketBaseURL: unhealthy.URL},
			{ID: 2, APIBaseURL: healthyAPI.URL, ManagerBaseURL: healthyAPI.URL, WebSocketBaseURL: healthyWebSocket.URL},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	public := httptest.NewServer(server.Handler())
	t.Cleanup(public.Close)
	webSocketURL := "ws" + public.URL[len("http"):] + "/"

	connection, _, err := websocket.DefaultDialer.Dial(webSocketURL, nil)
	if err != nil {
		t.Fatalf("dial public WebSocket: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := connection.WriteMessage(websocket.BinaryMessage, []byte("ping")); err != nil {
		t.Fatalf("write WebSocket message: %v", err)
	}
	_, body, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read WebSocket message: %v", err)
	}
	if string(body) != "node-2" {
		t.Fatalf("WebSocket response = %q, want healthy node", body)
	}
}

func TestServerRetriesWebSocketHandshakeAfterSelectedUpstreamFailure(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(ready.Close)
	failedWebSocket := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	failedWebSocketURL := failedWebSocket.URL
	failedWebSocket.Close()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	healthyWebSocket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = connection.Close()
		}
	}))
	t.Cleanup(healthyWebSocket.Close)
	server, err := New(Options{
		RunID: "run-1", PublicBaseURL: "http://198.51.100.20:19443", HealthCheckInterval: time.Minute,
		Nodes: []NodeUpstream{
			{ID: 1, APIBaseURL: ready.URL, ManagerBaseURL: ready.URL, WebSocketBaseURL: failedWebSocketURL},
			{ID: 2, APIBaseURL: ready.URL, ManagerBaseURL: ready.URL, WebSocketBaseURL: healthyWebSocket.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(server.Handler())
	t.Cleanup(public.Close)
	connection, response, err := websocket.DefaultDialer.Dial("ws"+public.URL[len("http"):]+"/", nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatalf("WebSocket failover handshake: %v", err)
	}
	_ = connection.Close()
}

func TestServerBalancesRequestsAcrossHealthyNodes(t *testing.T) {
	newNode := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/readyz" {
				w.WriteHeader(http.StatusOK)
				return
			}
			_, _ = w.Write([]byte(label))
		}))
	}
	first := newNode("node-1")
	t.Cleanup(first.Close)
	second := newNode("node-2")
	t.Cleanup(second.Close)
	server, err := New(Options{
		RunID:         "run-1",
		PublicBaseURL: "http://198.51.100.20:19443",
		Nodes: []NodeUpstream{
			{ID: 1, APIBaseURL: first.URL, ManagerBaseURL: first.URL, WebSocketBaseURL: first.URL},
			{ID: 2, APIBaseURL: second.URL, ManagerBaseURL: second.URL, WebSocketBaseURL: second.URL},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	responses := make([]string, 0, 2)
	for range 2 {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		responses = append(responses, recorder.Body.String())
	}
	if responses[0] != "node-1" || responses[1] != "node-2" {
		t.Fatalf("balanced responses = %#v, want node-1 then node-2", responses)
	}
}

func TestServerQuarantinesFailedUpstreamUntilNextHealthRefresh(t *testing.T) {
	healthyAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(healthyAPI.Close)
	failedManager := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	failedManagerURL := failedManager.URL
	failedManager.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte("node-2"))
	}))
	t.Cleanup(second.Close)
	server, err := New(Options{
		RunID:         "run-1",
		PublicBaseURL: "http://198.51.100.20:19443", HealthCheckInterval: time.Minute,
		Nodes: []NodeUpstream{
			{ID: 1, APIBaseURL: healthyAPI.URL, ManagerBaseURL: failedManagerURL, WebSocketBaseURL: failedManagerURL},
			{ID: 2, APIBaseURL: second.URL, ManagerBaseURL: second.URL, WebSocketBaseURL: second.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRecorder()
	server.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	if first.Code != http.StatusOK || first.Body.String() != "node-2" {
		t.Fatalf("first request after safe failover = status %d body %q", first.Code, first.Body.String())
	}
	for attempt := 0; attempt < 2; attempt++ {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != http.StatusOK || recorder.Body.String() != "node-2" {
			t.Fatalf("request after quarantine = status %d body %q", recorder.Code, recorder.Body.String())
		}
	}
}

func TestServerDoesNotRetryManagerWriteAfterUpstreamFailure(t *testing.T) {
	healthyAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(healthyAPI.Close)
	failedManager := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	failedManagerURL := failedManager.URL
	failedManager.Close()
	var secondWrites atomic.Int64
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		secondWrites.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(second.Close)
	server, err := New(Options{
		RunID: "run-1", PublicBaseURL: "http://198.51.100.20:19443", HealthCheckInterval: time.Minute,
		Nodes: []NodeUpstream{
			{ID: 1, APIBaseURL: healthyAPI.URL, ManagerBaseURL: failedManagerURL, WebSocketBaseURL: failedManagerURL},
			{ID: 2, APIBaseURL: second.URL, ManagerBaseURL: second.URL, WebSocketBaseURL: second.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/manager/irreversible", nil))
	if recorder.Code != http.StatusBadGateway || secondWrites.Load() != 0 {
		t.Fatalf("Manager write = status %d second writes %d, want 502 and no retry", recorder.Code, secondWrites.Load())
	}
}

func TestServerDoesNotForwardManagerWriteWhenPurityPersistenceFails(t *testing.T) {
	var upstreamWrites atomic.Int64
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		upstreamWrites.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(node.Close)
	directory := filepath.Join(t.TempDir(), "textfiles")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	metricsPath := filepath.Join(directory, "cloud-view.prom")
	server, err := New(Options{
		RunID: "run-1", PublicBaseURL: "http://198.51.100.20:19443", MetricsPath: metricsPath,
		Nodes: []NodeUpstream{{ID: 1, APIBaseURL: node.URL, ManagerBaseURL: node.URL, WebSocketBaseURL: node.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(metricsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/manager/irreversible", nil))
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get(stateHealthHeader) != "degraded" ||
		upstreamWrites.Load() != 0 || !server.state.Snapshot().OperatorModified || server.state.PersistenceHealthy() {
		t.Fatalf("degraded write status=%d header=%q writes=%d state=%#v healthy=%v",
			recorder.Code, recorder.Header().Get(stateHealthHeader), upstreamWrites.Load(),
			server.state.Snapshot(), server.state.PersistenceHealthy())
	}
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestServerGateProbeDoesNotChangeBenchmarkPurity(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(node.Close)
	server, err := New(Options{
		RunID: "run-1", PublicBaseURL: "http://198.51.100.20:19443", GateProbeToken: "gate-secret",
		Nodes: []NodeUpstream{{ID: 1, APIBaseURL: node.URL, ManagerBaseURL: node.URL, WebSocketBaseURL: node.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/demo/", nil),
		httptest.NewRequest(http.MethodPost, "/manager/irreversible", nil),
	} {
		request.Header.Set(GateProbeHeader, "gate-secret")
		server.Handler().ServeHTTP(httptest.NewRecorder(), request)
	}
	state := server.state.Snapshot()
	if state.Interactive || state.OperatorModified {
		t.Fatalf("gate probe state = %#v, want pure benchmark", state)
	}
}

func TestNewRequiresRunID(t *testing.T) {
	_, err := New(Options{
		PublicBaseURL: "http://198.51.100.20:19443",
		Nodes:         []NodeUpstream{{ID: 1, APIBaseURL: "http://127.0.0.1:5001", ManagerBaseURL: "http://127.0.0.1:5301", WebSocketBaseURL: "http://127.0.0.1:5200"}},
	})
	if !errors.Is(err, errInvalidOptions) {
		t.Fatalf("New() error = %v, want errInvalidOptions", err)
	}
}

func TestServerRewritesDemoRouteToPublicWebSocket(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/route" {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"tcp_addr": "10.42.0.11:5100",
				"ws_addr":  "ws://10.42.0.11:5200",
				"wss_addr": "",
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(api.Close)

	server, err := New(Options{
		RunID:         "run-1",
		PublicBaseURL: "http://198.51.100.20:19443",
		Nodes: []NodeUpstream{{
			ID: 1, APIBaseURL: api.URL, ManagerBaseURL: api.URL, WebSocketBaseURL: api.URL,
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/route?uid=demo-user", nil))

	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode route response: %v; body=%q", err, recorder.Body.String())
	}
	if got := response["ws_addr"]; recorder.Code != http.StatusOK || got != "ws://198.51.100.20:19443" {
		t.Fatalf("GET /route = status %d ws_addr %q, want public WebSocket address", recorder.Code, got)
	}
}

func TestServerRoutesPrometheusUnderPrefix(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(node.Close)
	prometheus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	t.Cleanup(prometheus.Close)

	server, err := New(Options{
		RunID:         "run-1",
		PublicBaseURL: "http://198.51.100.20:19443",
		PrometheusURL: prometheus.URL,
		Nodes: []NodeUpstream{{
			ID: 1, APIBaseURL: node.URL, ManagerBaseURL: node.URL, WebSocketBaseURL: node.URL,
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/targets", nil))

	if recorder.Code != http.StatusOK || recorder.Body.String() != "/api/v1/targets" {
		t.Fatalf("GET /prometheus/api/v1/targets = status %d body %q", recorder.Code, recorder.Body.String())
	}
}
