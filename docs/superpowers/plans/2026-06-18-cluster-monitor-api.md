# Cluster Realtime Monitor API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `/manager/cluster-monitor/realtime` API and wire the `/cluster/monitor` page to real manager data while preserving per-card availability for missing cluster observability.

**Architecture:** Keep the API separate from the business realtime monitor endpoint. `internalv2/access/manager` owns route parsing, permissions, and DTOs; `internalv2/app` owns the Prometheus-backed cluster monitor provider and bounded control snapshot assembly; `pkg/metrics` owns the missing low-cardinality metric definitions; `web/src/pages/cluster-monitor` maps API card keys to presentation metadata.

**Tech Stack:** Go, Gin, Prometheus HTTP API, `pkg/metrics`, React, TypeScript, React Intl, Recharts, Vitest, Bun.

---

## File Structure

- Create `internalv2/access/manager/cluster_monitor.go`
  - Defines cluster monitor constants, query, provider interface, response DTOs, handler, disabled/unavailable responses, and query parsing.
- Modify `internalv2/access/manager/server.go`
  - Adds `ClusterMonitor` to `Options`, stores it on `Server`, and registers `GET /manager/cluster-monitor/realtime` under the existing `cluster.node:r` manager group.
- Modify `internalv2/access/manager/server_test.go`
  - Adds route, validation, and permission tests for the cluster monitor endpoint.
- Modify `internalv2/access/manager/FLOW.md`
  - Documents the new route and the data-source boundary.
- Create `internalv2/app/manager_cluster_monitor_prometheus.go`
  - Implements the Prometheus-backed cluster monitor provider and bounded control snapshot merge.
- Create `internalv2/app/manager_cluster_monitor_prometheus_test.go`
  - Tests disabled, ready, partial, unavailable, text stats, and control snapshot behavior.
- Modify `internalv2/app/manager_monitor_prometheus.go`
  - Extracts the existing Prometheus query-range helper so business and cluster providers can share only the HTTP parsing utility.
- Modify `internalv2/app/wiring.go`
  - Wires the cluster monitor provider into the manager server using the same Prometheus base URL and a shared management usecase instance.
- Modify `internalv2/app/app_test.go`
  - Verifies the manager server receives a non-disabled cluster monitor provider when Prometheus is configured.
- Modify `pkg/metrics/controller.go`
  - Adds `wukongim_controller_apply_gap`.
- Modify `pkg/metrics/slot.go`
  - Adds `wukongim_slot_replica_lag_seconds{slot_id,replica_node}`.
- Modify `pkg/metrics/channelv2.go`
  - Adds `wukongim_channelv2_isr_anomaly_channels{reason}`.
- Modify `pkg/metrics/registry_test.go`
  - Verifies the new metric families and labels are registered.
- Modify `web/src/lib/manager-api.types.ts`
  - Adds cluster monitor response types with numeric and text stats.
- Modify `web/src/lib/manager-api.ts`
  - Adds `getClusterRealtimeMonitor`.
- Modify `web/src/lib/manager-api.test.ts`
  - Verifies the new client path and query params.
- Create `web/src/pages/cluster-monitor/metric-config.ts`
  - Maps stable API card keys, stages, snapshot keys, and stat keys to UI labels, colors, precision, and status labels.
- Modify `web/src/pages/cluster-monitor/page.tsx`
  - Replaces production preview-only rendering with API loading, partial, disabled, unavailable, and ready states.
- Modify `web/src/pages/cluster-monitor/page.test.tsx`
  - Mocks the API and verifies ready, partial, disabled, unavailable, range changes, and no silent preview fallback.
- Modify `web/src/pages/cluster-monitor/types.ts`
  - Keeps display model types, allowing API-mapped stats.
- Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts`
  - Adds cluster monitor source-state messages if missing.

## Task 1: Add Manager Access Contract And Route

**Files:**
- Create: `internalv2/access/manager/cluster_monitor.go`
- Modify: `internalv2/access/manager/server.go`
- Modify: `internalv2/access/manager/server_test.go`
- Modify: `internalv2/access/manager/FLOW.md`

- [ ] **Step 1: Write failing route tests**

Add these tests and stub to `internalv2/access/manager/server_test.go`:

```go
func TestManagerClusterRealtimeMonitorReturnsPayload(t *testing.T) {
	provider := &managerClusterMonitorStub{response: ClusterRealtimeMonitorResponse{
		Status:        ClusterRealtimeMonitorStatusReady,
		GeneratedAt:   time.Unix(1781767200, 0).UTC(),
		WindowSeconds: 900,
		StepSeconds:   20,
		Scope:         ClusterRealtimeMonitorScope{View: ClusterRealtimeMonitorScopeCluster},
		Sources: ClusterRealtimeMonitorSources{
			Prometheus:      RealtimeMonitorPrometheusSource{Enabled: true, BaseURL: "http://127.0.0.1:9090", QueryMS: 12},
			ControlSnapshot: ClusterRealtimeMonitorSource{Enabled: true, QueryMS: 1},
		},
		Snapshot: []ClusterRealtimeMonitorSnapshotEntry{{
			Key:       "nodesAlive",
			MetricKey: "nodesAlive",
			Value:     3,
			Tone:      RealtimeMonitorToneNormal,
			Source:    ClusterRealtimeMonitorSourceControlSnapshot,
		}},
		Cards: []ClusterRealtimeMonitorCard{{
			Key:       "rpcSuccessRate",
			Stage:     ClusterRealtimeMonitorStageInternalNetwork,
			Tone:      RealtimeMonitorToneNormal,
			Unit:      "%",
			Value:     99.96,
			Source:    ClusterRealtimeMonitorSourcePrometheus,
			Available: true,
			Series: []RealtimeMonitorPoint{{
				Timestamp: 1781767200000,
				Value:     99.96,
			}},
			Stats: []ClusterRealtimeMonitorStat{{
				Key:   "callsPerSecond",
				Value: testFloat64Ptr(1280),
				Unit:  "calls/s",
			}, {
				Key:  "topReason",
				Text: "timeout",
			}},
		}},
	}}
	srv := New(Options{ClusterMonitor: provider})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/cluster-monitor/realtime?window=15m&step=20s", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.Window != 15*time.Minute || provider.query.Step != 20*time.Second {
		t.Fatalf("query = %#v, want 15m window and 20s step", provider.query)
	}
	if !jsonEqual(rec.Body.String(), `{
		"status": "ready",
		"generated_at": "2026-06-18T10:00:00Z",
		"window_seconds": 900,
		"step_seconds": 20,
		"scope": {"view": "cluster"},
		"sources": {
			"prometheus": {"enabled": true, "base_url": "http://127.0.0.1:9090", "query_ms": 12, "error": ""},
			"control_snapshot": {"enabled": true, "query_ms": 1, "error": ""}
		},
		"snapshot": [{
			"key": "nodesAlive",
			"metric_key": "nodesAlive",
			"value": 3,
			"tone": "normal",
			"source": "control_snapshot"
		}],
		"cards": [{
			"key": "rpcSuccessRate",
			"stage": "internalNetwork",
			"tone": "normal",
			"unit": "%",
			"value": 99.96,
			"source": "prometheus",
			"available": true,
			"error": "",
			"series": [{"timestamp": 1781767200000, "value": 99.96}],
			"stats": [
				{"key": "callsPerSecond", "value": 1280, "unit": "calls/s"},
				{"key": "topReason", "text": "timeout"}
			]
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerClusterRealtimeMonitorRejectsInvalidQuery(t *testing.T) {
	srv := New(Options{ClusterMonitor: &managerClusterMonitorStub{}})

	for _, path := range []string{
		"/manager/cluster-monitor/realtime?window=2m",
		"/manager/cluster-monitor/realtime?step=1s",
		"/manager/cluster-monitor/realtime?step=10m",
		"/manager/cluster-monitor/realtime?step=bad",
	} {
		rec := httptest.NewRecorder()
		srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusBadRequest)
		}
	}
}

func TestManagerClusterRealtimeMonitorRequiresNodeReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		ClusterMonitor: &managerClusterMonitorStub{},
	})

	missing := httptest.NewRecorder()
	srv.Engine().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/manager/cluster-monitor/realtime", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}

	forbidden := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/cluster-monitor/realtime", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(forbidden, req)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want %d", forbidden.Code, http.StatusForbidden)
	}
}

type managerClusterMonitorStub struct {
	response ClusterRealtimeMonitorResponse
	err      error
	query    ClusterRealtimeMonitorQuery
}

func (s *managerClusterMonitorStub) ClusterRealtimeMonitor(_ context.Context, query ClusterRealtimeMonitorQuery) (ClusterRealtimeMonitorResponse, error) {
	s.query = query
	if s.err != nil {
		return ClusterRealtimeMonitorResponse{}, s.err
	}
	return s.response, nil
}

func testFloat64Ptr(v float64) *float64 {
	return &v
}
```

- [ ] **Step 2: Run the route test and verify it fails**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerClusterRealtimeMonitor'
```

Expected: compile failure naming missing `ClusterRealtimeMonitorResponse`, `ClusterRealtimeMonitorQuery`, `ClusterMonitor`, and `handleClusterRealtimeMonitor`.

- [ ] **Step 3: Create `cluster_monitor.go` with DTOs and handler**

Create `internalv2/access/manager/cluster_monitor.go`:

```go
package manager

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// ClusterRealtimeMonitorStatusReady reports that all required cluster monitor queries succeeded.
	ClusterRealtimeMonitorStatusReady = RealtimeMonitorStatusReady
	// ClusterRealtimeMonitorStatusPartial reports that cluster monitor data is usable but incomplete.
	ClusterRealtimeMonitorStatusPartial = RealtimeMonitorStatusPartial
	// ClusterRealtimeMonitorStatusPrometheusDisabled reports that Prometheus cluster monitor queries are not configured.
	ClusterRealtimeMonitorStatusPrometheusDisabled = RealtimeMonitorStatusPrometheusDisabled
	// ClusterRealtimeMonitorStatusPrometheusUnavailable reports that Prometheus cluster monitor queries cannot be run.
	ClusterRealtimeMonitorStatusPrometheusUnavailable = RealtimeMonitorStatusPrometheusUnavailable

	// ClusterRealtimeMonitorScopeCluster identifies the aggregate cluster monitor view.
	ClusterRealtimeMonitorScopeCluster = "cluster"

	// ClusterRealtimeMonitorSourcePrometheus identifies Prometheus as a card or snapshot source.
	ClusterRealtimeMonitorSourcePrometheus = "prometheus"
	// ClusterRealtimeMonitorSourceControlSnapshot identifies the bounded cluster control snapshot source.
	ClusterRealtimeMonitorSourceControlSnapshot = "control_snapshot"

	// Cluster realtime monitor stage constants keep frontend card grouping stable.
	ClusterRealtimeMonitorStageControlPlane       = "controlPlane"
	ClusterRealtimeMonitorStageSlotReplication    = "slotReplication"
	ClusterRealtimeMonitorStageChannelReplication = "channelReplication"
	ClusterRealtimeMonitorStageInternalNetwork    = "internalNetwork"
	ClusterRealtimeMonitorStageRuntimePressure    = "runtimePressure"
	ClusterRealtimeMonitorStageIncidentClosure    = "incidentClosure"
)

// ClusterRealtimeMonitorProvider returns cluster operations monitor snapshots.
type ClusterRealtimeMonitorProvider interface {
	ClusterRealtimeMonitor(context.Context, ClusterRealtimeMonitorQuery) (ClusterRealtimeMonitorResponse, error)
}

// ClusterRealtimeMonitorQuery contains validated cluster monitor request parameters.
type ClusterRealtimeMonitorQuery struct {
	// Window is the requested chart time range.
	Window time.Duration
	// Step is the chart point interval.
	Step time.Duration
}

// ClusterRealtimeMonitorResponse is the manager cluster realtime monitor payload.
type ClusterRealtimeMonitorResponse struct {
	// Status is ready, partial, prometheus_disabled, or prometheus_unavailable.
	Status string `json:"status"`
	// GeneratedAt is the UTC time when the payload was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// WindowSeconds is the chart time range in seconds.
	WindowSeconds int `json:"window_seconds"`
	// StepSeconds is the chart point interval in seconds.
	StepSeconds int `json:"step_seconds"`
	// Scope identifies the aggregate cluster view.
	Scope ClusterRealtimeMonitorScope `json:"scope"`
	// Sources reports source availability and query details.
	Sources ClusterRealtimeMonitorSources `json:"sources"`
	// Snapshot contains compact top-line cluster monitor values.
	Snapshot []ClusterRealtimeMonitorSnapshotEntry `json:"snapshot"`
	// Cards contains graphable cluster monitor card data.
	Cards []ClusterRealtimeMonitorCard `json:"cards"`
}

// ClusterRealtimeMonitorScope identifies the cluster monitor view.
type ClusterRealtimeMonitorScope struct {
	// View is cluster for this monitor endpoint.
	View string `json:"view"`
}

// ClusterRealtimeMonitorSources reports cluster monitor data source state.
type ClusterRealtimeMonitorSources struct {
	// Prometheus reports Prometheus query availability.
	Prometheus RealtimeMonitorPrometheusSource `json:"prometheus"`
	// ControlSnapshot reports bounded control snapshot availability.
	ControlSnapshot ClusterRealtimeMonitorSource `json:"control_snapshot"`
}

// ClusterRealtimeMonitorSource reports a non-Prometheus cluster monitor source.
type ClusterRealtimeMonitorSource struct {
	// Enabled reports whether the source can be queried.
	Enabled bool `json:"enabled"`
	// QueryMS is the total query duration in milliseconds.
	QueryMS int64 `json:"query_ms"`
	// Error contains a non-fatal source error shown to operators.
	Error string `json:"error"`
}

// ClusterRealtimeMonitorSnapshotEntry is one compact cluster monitor summary value.
type ClusterRealtimeMonitorSnapshotEntry struct {
	// Key is the stable snapshot entry key.
	Key string `json:"key"`
	// MetricKey points at the source monitor card key.
	MetricKey string `json:"metric_key"`
	// Value is the numeric summary value.
	Value float64 `json:"value"`
	// Unit is the value unit.
	Unit string `json:"unit,omitempty"`
	// Tone is the status tone used by the frontend.
	Tone string `json:"tone"`
	// Source identifies where the value came from.
	Source string `json:"source"`
}

// ClusterRealtimeMonitorCard is one graphable cluster monitor card.
type ClusterRealtimeMonitorCard struct {
	// Key is the stable card metric key.
	Key string `json:"key"`
	// Stage is the stable cluster operations stage key.
	Stage string `json:"stage"`
	// Tone is the status tone used by the frontend.
	Tone string `json:"tone"`
	// Unit is the primary value unit.
	Unit string `json:"unit"`
	// Value is the latest primary value.
	Value float64 `json:"value"`
	// Source identifies where the card value came from.
	Source string `json:"source"`
	// Available reports whether this card has usable data.
	Available bool `json:"available"`
	// Error contains a card-local source error.
	Error string `json:"error"`
	// Series contains chart points ordered by timestamp.
	Series []RealtimeMonitorPoint `json:"series"`
	// Stats contains compact card statistics.
	Stats []ClusterRealtimeMonitorStat `json:"stats"`
}

// ClusterRealtimeMonitorStat is one compact card statistic.
type ClusterRealtimeMonitorStat struct {
	// Key is the stable statistic key.
	Key string `json:"key"`
	// Value is the numeric statistic value when the stat is numeric.
	Value *float64 `json:"value,omitempty"`
	// Text is the textual statistic value when the stat is a label or reason.
	Text string `json:"text,omitempty"`
	// Unit is the statistic unit.
	Unit string `json:"unit,omitempty"`
}

func (s *Server) handleClusterRealtimeMonitor(c *gin.Context) {
	query, err := parseClusterRealtimeMonitorQuery(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if s == nil || s.clusterMonitor == nil {
		c.JSON(http.StatusOK, clusterRealtimeMonitorDisabledResponse(query, "cluster monitor provider is not configured"))
		return
	}
	response, err := s.clusterMonitor.ClusterRealtimeMonitor(c.Request.Context(), query)
	if err != nil {
		response = clusterRealtimeMonitorUnavailableResponse(query, err.Error())
	}
	c.JSON(http.StatusOK, response)
}

func parseClusterRealtimeMonitorQuery(c *gin.Context) (ClusterRealtimeMonitorQuery, error) {
	query := ClusterRealtimeMonitorQuery{Window: defaultRealtimeMonitorWindow}
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		window, err := parseRealtimeMonitorWindow(raw)
		if err != nil {
			return query, err
		}
		query.Window = window
	}
	query.Step = derivedRealtimeMonitorStep(query.Window)
	if raw := strings.TrimSpace(c.Query("step")); raw != "" {
		step, err := time.ParseDuration(raw)
		if err != nil {
			return query, fmt.Errorf("step invalid")
		}
		query.Step = step
	}
	if query.Step < minRealtimeMonitorStep || query.Step > maxRealtimeMonitorStep {
		return query, fmt.Errorf("step invalid")
	}
	return query, nil
}

func clusterRealtimeMonitorDisabledResponse(query ClusterRealtimeMonitorQuery, message string) ClusterRealtimeMonitorResponse {
	return ClusterRealtimeMonitorResponse{
		Status:        ClusterRealtimeMonitorStatusPrometheusDisabled,
		GeneratedAt:   time.Now().UTC(),
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         ClusterRealtimeMonitorScope{View: ClusterRealtimeMonitorScopeCluster},
		Sources: ClusterRealtimeMonitorSources{
			Prometheus:      RealtimeMonitorPrometheusSource{Enabled: false, Error: message},
			ControlSnapshot: ClusterRealtimeMonitorSource{Enabled: false},
		},
		Snapshot: []ClusterRealtimeMonitorSnapshotEntry{},
		Cards:    []ClusterRealtimeMonitorCard{},
	}
}

func clusterRealtimeMonitorUnavailableResponse(query ClusterRealtimeMonitorQuery, message string) ClusterRealtimeMonitorResponse {
	return ClusterRealtimeMonitorResponse{
		Status:        ClusterRealtimeMonitorStatusPrometheusUnavailable,
		GeneratedAt:   time.Now().UTC(),
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         ClusterRealtimeMonitorScope{View: ClusterRealtimeMonitorScopeCluster},
		Sources: ClusterRealtimeMonitorSources{
			Prometheus:      RealtimeMonitorPrometheusSource{Enabled: true, Error: message},
			ControlSnapshot: ClusterRealtimeMonitorSource{Enabled: false},
		},
		Snapshot: []ClusterRealtimeMonitorSnapshotEntry{},
		Cards:    []ClusterRealtimeMonitorCard{},
	}
}
```

- [ ] **Step 4: Wire server options and route**

Modify `internalv2/access/manager/server.go`:

```go
type Options struct {
	ListenAddr string
	Auth AuthConfig
	Management Management
	Monitor RealtimeMonitorProvider
	ClusterMonitor ClusterRealtimeMonitorProvider
	Top accessapi.TopSnapshotProvider
	Logger wklog.Logger
}

type Server struct {
	mu sync.RWMutex
	engine *gin.Engine
	httpServer *http.Server
	listener net.Listener
	listenAddr string
	addr string
	management Management
	monitor RealtimeMonitorProvider
	clusterMonitor ClusterRealtimeMonitorProvider
	top accessapi.TopSnapshotProvider
	auth authState
	logger wklog.Logger
	started bool
}
```

Set the field in `New`:

```go
clusterMonitor: opts.ClusterMonitor,
```

Register the route in the existing `nodes` group:

```go
nodes.GET("/cluster-monitor/realtime", s.handleClusterRealtimeMonitor)
```

- [ ] **Step 5: Run route tests and verify they pass**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerClusterRealtimeMonitor'
```

Expected: PASS.

- [ ] **Step 6: Update manager flow documentation**

In `internalv2/access/manager/FLOW.md`, add this line to the route table:

```text
GET  /manager/cluster-monitor/realtime (Prometheus-backed cluster operations monitor cards; requires cluster.node:r when Auth.On=true)
```

Add this route description after the business realtime monitor paragraph:

```markdown
`/manager/cluster-monitor/realtime` backs the web cluster operations realtime
monitor card wall. It uses the same window and step parsing bounds as the
business monitor, requires `cluster.node:r` when manager auth is enabled, and
delegates Prometheus and bounded control snapshot reads to the app-wired
cluster monitor provider. This route does not use the node-local top collector
for cluster-wide cards.
```

- [ ] **Step 7: Commit Task 1**

Run:

```bash
git add internalv2/access/manager/cluster_monitor.go internalv2/access/manager/server.go internalv2/access/manager/server_test.go internalv2/access/manager/FLOW.md
git commit -m "feat: add cluster monitor manager route"
```

## Task 2: Implement The Prometheus-Backed Cluster Provider

**Files:**
- Create: `internalv2/app/manager_cluster_monitor_prometheus.go`
- Create: `internalv2/app/manager_cluster_monitor_prometheus_test.go`
- Modify: `internalv2/app/manager_monitor_prometheus.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Write failing provider tests**

Create `internalv2/app/manager_cluster_monitor_prometheus_test.go`:

```go
package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internalv2/access/manager"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerClusterMonitorProviderReturnsDisabledWhenNotEnabled(t *testing.T) {
	provider := newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled: false,
		Now:     func() time.Time { return time.Unix(1781767200, 0).UTC() },
	})

	resp, err := provider.ClusterRealtimeMonitor(context.Background(), accessmanager.ClusterRealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("ClusterRealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.ClusterRealtimeMonitorStatusPrometheusDisabled {
		t.Fatalf("Status = %q, want disabled", resp.Status)
	}
	if resp.Sources.Prometheus.Enabled {
		t.Fatalf("Prometheus.Enabled = true, want false")
	}
}

func TestManagerClusterMonitorProviderMapsPrometheusAndControlSnapshot(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("path = %s, want /api/v1/query_range", r.URL.Path)
		}
		if r.URL.Query().Get("step") != "20" {
			t.Fatalf("step = %q, want 20", r.URL.Query().Get("step"))
		}
		query := r.URL.Query().Get("query")
		if !strings.Contains(query, "wukongim_") {
			t.Fatalf("query = %q, want wukongim metric", query)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled:  true,
		BaseURL:  server.URL,
		Client:   server.Client(),
		Now:      func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control:  managerClusterControlReaderFake{},
	})

	resp, err := provider.ClusterRealtimeMonitor(context.Background(), accessmanager.ClusterRealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("ClusterRealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.ClusterRealtimeMonitorStatusReady {
		t.Fatalf("Status = %q, want ready; source=%#v", resp.Status, resp.Sources.Prometheus)
	}
	if calls.Load() == 0 {
		t.Fatal("Prometheus server was not queried")
	}
	if len(resp.Cards) != len(managerClusterMonitorMetricDefinitions()) {
		t.Fatalf("cards = %d, want %d", len(resp.Cards), len(managerClusterMonitorMetricDefinitions()))
	}
	if resp.Cards[0].Key != "controllerProposeRate" || resp.Cards[0].Value != 15 || !resp.Cards[0].Available {
		t.Fatalf("first card = %#v, want controllerProposeRate latest 15 available", resp.Cards[0])
	}
	if len(resp.Snapshot) == 0 || resp.Snapshot[0].Key != "nodesAlive" || resp.Snapshot[0].Value != 3 {
		t.Fatalf("snapshot = %#v, want nodesAlive from control snapshot", resp.Snapshot)
	}
	if !resp.Sources.ControlSnapshot.Enabled {
		t.Fatalf("ControlSnapshot.Enabled = false, want true")
	}
}

func TestManagerClusterMonitorProviderReturnsPartialForMissingMetric(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 2 {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: managerClusterControlReaderFake{},
	})

	resp, err := provider.ClusterRealtimeMonitor(context.Background(), accessmanager.ClusterRealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("ClusterRealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.ClusterRealtimeMonitorStatusPartial {
		t.Fatalf("Status = %q, want partial", resp.Status)
	}
	var unavailable int
	for _, card := range resp.Cards {
		if !card.Available {
			unavailable++
		}
	}
	if unavailable != 1 {
		t.Fatalf("unavailable cards = %d, want 1", unavailable)
	}
}

func TestManagerClusterMonitorProviderKeepsTextStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"1"],[1781767220,"2"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: managerClusterControlReaderFake{},
	})

	resp, err := provider.ClusterRealtimeMonitor(context.Background(), accessmanager.ClusterRealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("ClusterRealtimeMonitor() error = %v", err)
	}
	incident := resp.Cards[len(resp.Cards)-1]
	var found bool
	for _, stat := range incident.Stats {
		if stat.Key == "topReason" && stat.Text != "" && stat.Value == nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("incident stats = %#v, want text-only topReason", incident.Stats)
	}
}

type managerClusterControlReaderFake struct{}

func (managerClusterControlReaderFake) ListNodes(context.Context) (managementusecase.NodeList, error) {
	return managementusecase.NodeList{
		GeneratedAt:        time.Unix(1781767240, 0).UTC(),
		ControllerLeaderID: 1,
		Items: []managementusecase.Node{{
			NodeID: 1,
			Status: "alive",
		}, {
			NodeID: 2,
			Status: "alive",
		}, {
			NodeID: 3,
			Status: "alive",
		}},
	}, nil
}

func (managerClusterControlReaderFake) ListSlots(context.Context, managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error) {
	return []managementusecase.Slot{{
		Runtime: managementusecase.SlotRuntime{LeaderID: 1, HasQuorum: true},
	}, {
		Runtime: managementusecase.SlotRuntime{LeaderID: 2, HasQuorum: true},
	}}, nil
}
```

- [ ] **Step 2: Run provider test and verify it fails**

Run:

```bash
go test ./internalv2/app -run 'TestManagerClusterMonitorProvider'
```

Expected: compile failure naming missing `newManagerClusterPrometheusMonitorProvider`, options, control reader, and definitions.

- [ ] **Step 3: Extract a small Prometheus query helper**

In `internalv2/app/manager_monitor_prometheus.go`, move the existing `queryRange` body into this package-level helper and update the business provider to call it:

```go
func managerMonitorQueryRange(ctx context.Context, client *http.Client, baseURL, promQL string, start, end time.Time, step time.Duration) ([]accessmanager.RealtimeMonitorPoint, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("prometheus base url invalid: %w", err)
	}
	values := base.Query()
	values.Set("query", promQL)
	values.Set("start", strconv.FormatInt(start.Unix(), 10))
	values.Set("end", strconv.FormatInt(end.Unix(), 10))
	values.Set("step", strconv.FormatInt(int64(step/time.Second), 10))
	base.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create prometheus query request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query prometheus: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read prometheus response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prometheus query_range returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded prometheusQueryRangeResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode prometheus response: %w", err)
	}
	if decoded.Status != "success" {
		if decoded.Error != "" {
			return nil, fmt.Errorf("prometheus query_range failed: %s", decoded.Error)
		}
		return nil, fmt.Errorf("prometheus query_range failed: %s", decoded.Status)
	}
	return parsePrometheusMatrix(decoded.Data.Result)
}
```

Then reduce the business provider method to:

```go
func (p *managerPrometheusMonitorProvider) queryRange(ctx context.Context, promQL string, start, end time.Time, step time.Duration) ([]accessmanager.RealtimeMonitorPoint, error) {
	return managerMonitorQueryRange(ctx, p.client, p.options.BaseURL, promQL, start, end, step)
}
```

- [ ] **Step 4: Implement the cluster provider**

Create `internalv2/app/manager_cluster_monitor_prometheus.go` with these core definitions:

```go
type managerClusterControlReader interface {
	ListNodes(context.Context) (managementusecase.NodeList, error)
	ListSlots(context.Context, managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error)
}

type managerClusterPrometheusMonitorOptions struct {
	// Enabled reports whether Prometheus-backed cluster monitor queries may run.
	Enabled bool
	// BaseURL is the Prometheus HTTP API base URL.
	BaseURL string
	// Client is the HTTP client used for Prometheus API calls.
	Client *http.Client
	// Now returns the current time for deterministic tests.
	Now func() time.Time
	// Control reads bounded manager control snapshots for current cluster health values.
	Control managerClusterControlReader
}

type managerClusterPrometheusMonitorProvider struct {
	options managerClusterPrometheusMonitorOptions
	client  *http.Client
	now     func() time.Time
}

type clusterMonitorMetricDefinition struct {
	key   string
	stage string
	tone  string
	unit  string
	source string
	query func(rateWindow string) string
	stats func(series []accessmanager.RealtimeMonitorPoint, unit string) []accessmanager.ClusterRealtimeMonitorStat
}
```

Add constructor and disabled handling:

```go
func newManagerClusterPrometheusMonitorProvider(options managerClusterPrometheusMonitorOptions) *managerClusterPrometheusMonitorProvider {
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: managerMonitorPrometheusQueryTimeout}
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &managerClusterPrometheusMonitorProvider{options: options, client: client, now: now}
}

func (p *managerClusterPrometheusMonitorProvider) ClusterRealtimeMonitor(ctx context.Context, query accessmanager.ClusterRealtimeMonitorQuery) (accessmanager.ClusterRealtimeMonitorResponse, error) {
	now := p.now().UTC()
	if p == nil || !p.options.Enabled || strings.TrimSpace(p.options.BaseURL) == "" {
		return p.monitorDisabledResponse(query, now), nil
	}

	started := time.Now()
	rateWindow := managerMonitorRateWindow(query.Window, query.Step)
	end := now
	start := end.Add(-query.Window)
	cards, available, firstErr := p.clusterCards(ctx, managerClusterMonitorMetricDefinitions(), rateWindow, start, end, query.Step)
	controlSnapshot, controlSource := p.controlSnapshot(ctx)
	status, sourceErr := clusterMonitorStatus(len(cards), available, firstErr, controlSource)

	return accessmanager.ClusterRealtimeMonitorResponse{
		Status:        status,
		GeneratedAt:   now,
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         accessmanager.ClusterRealtimeMonitorScope{View: accessmanager.ClusterRealtimeMonitorScopeCluster},
		Sources: accessmanager.ClusterRealtimeMonitorSources{
			Prometheus: accessmanager.RealtimeMonitorPrometheusSource{
				Enabled: true,
				BaseURL: strings.TrimRight(strings.TrimSpace(p.options.BaseURL), "/"),
				QueryMS: time.Since(started).Milliseconds(),
				Error:   sourceErr,
			},
			ControlSnapshot: controlSource,
		},
		Snapshot: clusterMonitorSnapshot(cards, controlSnapshot),
		Cards:    cards,
	}, nil
}
```

Use these 12 card definitions in `managerClusterMonitorMetricDefinitions()`:

```go
func managerClusterMonitorMetricDefinitions() []clusterMonitorMetricDefinition {
	return []clusterMonitorMetricDefinition{
		clusterMetric("controllerProposeRate", accessmanager.ClusterRealtimeMonitorStageControlPlane, accessmanager.RealtimeMonitorToneNormal, "cmd/s", "sum(rate(wukongim_controller_decisions_total[%s]))"),
		clusterMetric("controllerApplyGap", accessmanager.ClusterRealtimeMonitorStageControlPlane, accessmanager.RealtimeMonitorToneWarning, "entries", "max(wukongim_controller_apply_gap)"),
		clusterMetric("slotLeaderStability", accessmanager.ClusterRealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneNormal, "%", "(1 - clamp_max(sum(rate(wukongim_slot_leader_elections_total[%s])) / 10, 1)) * 100"),
		clusterMetric("slotReplicaLagP99", accessmanager.ClusterRealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneWarning, "ms", "quantile(0.99, wukongim_slot_replica_lag_seconds) * 1000"),
		clusterMetric("channelISRHealth", accessmanager.ClusterRealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneWarning, "%", "100 - clamp_max(sum(wukongim_channelv2_isr_anomaly_channels), 100)"),
		clusterMetric("channelAppendLatencyP99", accessmanager.ClusterRealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_channelv2_append_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric("internalTraffic", accessmanager.ClusterRealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "B/s", "sum(rate(wukongim_transport_sent_bytes_total[%s])) + sum(rate(wukongim_transport_received_bytes_total[%s]))"),
		clusterMetric("rpcSuccessRate", accessmanager.ClusterRealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "%", "(sum(rate(wukongim_transport_rpc_total{result=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_transport_rpc_total[%s])), 1)) * 100"),
		clusterMetric("rpcLatencyP95", accessmanager.ClusterRealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.95, sum(rate(wukongim_transport_rpc_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric("workqueuePressure", accessmanager.ClusterRealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", "max(wukongim_runtime_pool_queue_depth / clamp_min(wukongim_runtime_pool_queue_capacity, 1)) * 100"),
		clusterMetric("storageWriteP99", accessmanager.ClusterRealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_storage_commit_request_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric("incidentRate", accessmanager.ClusterRealtimeMonitorStageIncidentClosure, accessmanager.RealtimeMonitorToneCritical, "events/min", "sum(rate(wukongim_diagnostics_events_recorded_total{result=~\"error|timeout|partial\"}[%s])) * 60"),
	}
}
```

Implement `clusterMetric` with `fmt.Sprintf` support for one or two `<rate_window>` positions:

```go
func clusterMetric(key, stage, tone, unit, pattern string) clusterMonitorMetricDefinition {
	return clusterMonitorMetricDefinition{
		key:    key,
		stage:  stage,
		tone:   tone,
		unit:   unit,
		source: accessmanager.ClusterRealtimeMonitorSourcePrometheus,
		query: func(rateWindow string) string {
			if strings.Count(pattern, "%s") == 2 {
				return fmt.Sprintf(pattern, rateWindow, rateWindow)
			}
			if strings.Count(pattern, "%s") == 1 {
				return fmt.Sprintf(pattern, rateWindow)
			}
			return pattern
		},
		stats: clusterMonitorStats,
	}
}
```

Implement stat helpers with optional numeric values:

```go
func clusterMonitorStats(series []accessmanager.RealtimeMonitorPoint, unit string) []accessmanager.ClusterRealtimeMonitorStat {
	base := monitorCardStats(series, time.Second)
	out := make([]accessmanager.ClusterRealtimeMonitorStat, 0, 3)
	for _, stat := range base {
		value := stat.Value
		out = append(out, accessmanager.ClusterRealtimeMonitorStat{Key: stat.Key, Value: &value, Unit: unit})
		if len(out) == 2 {
			break
		}
	}
	out = append(out, accessmanager.ClusterRealtimeMonitorStat{Key: "topReason", Text: "none"})
	return out
}
```

Implement `controlSnapshot` using `ListNodes` and `ListSlots`. Count alive nodes, total slots, ready slots, leader-missing slots, and quorum-lost slots. Treat missing control reader as disabled and snapshot errors as partial source errors.

- [ ] **Step 5: Wire provider into the manager server**

Modify `internalv2/app/wiring.go` so `wireManager` shares one management usecase instance:

```go
func (a *App) wireManager() {
	if a.manager == nil && strings.TrimSpace(a.cfg.Manager.ListenAddr) != "" {
		management := a.newManagerManagement()
		a.manager = accessmanager.New(accessmanager.Options{
			ListenAddr: a.cfg.Manager.ListenAddr,
			Auth: accessmanager.AuthConfig{
				On:        a.cfg.Manager.AuthOn,
				JWTSecret: a.cfg.Manager.JWTSecret,
				JWTIssuer: a.cfg.Manager.JWTIssuer,
				JWTExpire: a.cfg.Manager.JWTExpire,
				Users:     managerUserConfigs(a.cfg.Manager.Users),
			},
			Management:     management,
			Monitor:        a.newManagerMonitorProvider(),
			ClusterMonitor: a.newManagerClusterMonitorProvider(management),
			Top:            a.topProvider,
			Logger:         a.logger.Named("access.manager"),
		})
	}
}
```

Add:

```go
func (a *App) newManagerClusterMonitorProvider(control managerClusterControlReader) accessmanager.ClusterRealtimeMonitorProvider {
	prometheusBaseURL := ""
	if listenAddr := strings.TrimSpace(a.cfg.Observability.Prometheus.ListenAddr); listenAddr != "" {
		prometheusBaseURL = "http://" + listenAddr
	}
	return newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled: a.cfg.Observability.MetricsEnabled && a.cfg.Observability.Prometheus.Enabled,
		BaseURL: prometheusBaseURL,
		Control: control,
	})
}
```

- [ ] **Step 6: Update app/FLOW**

Add a paragraph to `internalv2/app/FLOW.md` stating that the cluster monitor provider is Prometheus-backed, uses the management usecase only for bounded control snapshots, and does not read from `topCollector`.

- [ ] **Step 7: Run provider and existing business monitor tests**

Run:

```bash
go test ./internalv2/app -run 'TestManagerClusterMonitorProvider|TestManagerMonitorPrometheusProvider|TestManagerServerReceivesPrometheusMonitorProviderWhenConfigured'
```

Expected: PASS.

- [ ] **Step 8: Commit Task 2**

Run:

```bash
git add internalv2/app/manager_cluster_monitor_prometheus.go internalv2/app/manager_cluster_monitor_prometheus_test.go internalv2/app/manager_monitor_prometheus.go internalv2/app/wiring.go internalv2/app/app_test.go internalv2/app/FLOW.md
git commit -m "feat: add cluster monitor prometheus provider"
```

## Task 3: Register Missing Low-Cardinality Metrics

**Files:**
- Modify: `pkg/metrics/controller.go`
- Modify: `pkg/metrics/slot.go`
- Modify: `pkg/metrics/channelv2.go`
- Modify: `pkg/metrics/registry_test.go`

- [ ] **Step 1: Write failing metric registry tests**

In `pkg/metrics/registry_test.go`, add assertions to the existing controller, slot, and ChannelV2 metric tests:

```go
applyGap := requireMetricFamily(t, families, "wukongim_controller_apply_gap")
if applyGap.GetHelp() == "" {
	t.Fatal("controller apply gap help is empty")
}

slotReplicaLag := requireMetricFamily(t, families, "wukongim_slot_replica_lag_seconds")
if slotReplicaLag.GetHelp() == "" {
	t.Fatal("slot replica lag help is empty")
}

isrAnomalies := requireMetricFamily(t, families, "wukongim_channelv2_isr_anomaly_channels")
if isrAnomalies.GetHelp() == "" {
	t.Fatal("channel ISR anomaly help is empty")
}
```

Add an observation to the registry setup section:

```go
reg.Controller.SetApplyGap(7)
reg.Slot.SetReplicaLag(1, 2, 150*time.Millisecond)
reg.ChannelV2.SetISRAnomalyChannels(map[string]int{
	"isr_insufficient": 3,
	"no_leader":        1,
})
```

- [ ] **Step 2: Run the metric test and verify it fails**

Run:

```bash
go test ./pkg/metrics -run 'TestRegistry'
```

Expected: compile failure naming missing `SetApplyGap`, `SetReplicaLag`, and `SetISRAnomalyChannels`, or failing missing metric families.

- [ ] **Step 3: Add controller apply gap gauge**

In `pkg/metrics/controller.go`, add the field:

```go
applyGap prometheus.Gauge
```

Initialize and register it:

```go
applyGap: prometheus.NewGauge(prometheus.GaugeOpts{
	Name:        "wukongim_controller_apply_gap",
	Help:        "ControllerV2 committed-to-applied Raft log gap.",
	ConstLabels: labels,
}),
```

Include `m.applyGap` in `registry.MustRegister`, set it to zero in the constructor, and add:

```go
// SetApplyGap records the current ControllerV2 committed-to-applied Raft log gap.
func (m *ControllerMetrics) SetApplyGap(gap uint64) {
	if m == nil {
		return
	}
	m.applyGap.Set(float64(gap))
}
```

- [ ] **Step 4: Add Slot replica lag gauge**

In `pkg/metrics/slot.go`, add the field:

```go
replicaLag *prometheus.GaugeVec
```

Initialize and register it:

```go
replicaLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name:        "wukongim_slot_replica_lag_seconds",
	Help:        "Current Slot replica lag in seconds grouped by physical slot and replica node.",
	ConstLabels: labels,
}, []string{"slot_id", "replica_node"}),
```

Add:

```go
// SetReplicaLag records the current lag for one bounded Slot replica.
func (m *SlotMetrics) SetReplicaLag(slotID uint32, replicaNode uint64, lag time.Duration) {
	if m == nil {
		return
	}
	m.replicaLag.WithLabelValues(strconv.FormatUint(uint64(slotID), 10), strconv.FormatUint(replicaNode, 10)).Set(lag.Seconds())
}
```

- [ ] **Step 5: Add Channel ISR anomaly gauges**

In `pkg/metrics/channelv2.go`, add:

```go
var channelV2ISRAnomalyReasons = []string{"isr_insufficient", "no_leader", "replica_gap"}
```

Add the field:

```go
isrAnomalyChannels *prometheus.GaugeVec
```

Initialize and register it:

```go
isrAnomalyChannels: prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name:        "wukongim_channelv2_isr_anomaly_channels",
	Help:        "Current count of ChannelV2 runtime metadata ISR anomalies by low-cardinality reason.",
	ConstLabels: labels,
}, []string{"reason"}),
```

Initialize known reasons to zero in the constructor and add:

```go
// SetISRAnomalyChannels records bounded ChannelV2 ISR anomaly counts by reason.
func (m *ChannelV2Metrics) SetISRAnomalyChannels(counts map[string]int) {
	if m == nil {
		return
	}
	known := make(map[string]struct{}, len(channelV2ISRAnomalyReasons))
	for _, reason := range channelV2ISRAnomalyReasons {
		known[reason] = struct{}{}
		m.isrAnomalyChannels.WithLabelValues(reason).Set(float64(counts[reason]))
	}
	for reason, count := range counts {
		if _, ok := known[reason]; ok {
			continue
		}
		m.isrAnomalyChannels.WithLabelValues(reason).Set(float64(count))
	}
}
```

- [ ] **Step 6: Run metric tests**

Run:

```bash
go test ./pkg/metrics -run 'TestRegistry'
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

Run:

```bash
git add pkg/metrics/controller.go pkg/metrics/slot.go pkg/metrics/channelv2.go pkg/metrics/registry_test.go
git commit -m "feat: add cluster monitor metrics"
```

## Task 4: Wire The Cluster Monitor Page To The API

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`
- Create: `web/src/pages/cluster-monitor/metric-config.ts`
- Modify: `web/src/pages/cluster-monitor/page.tsx`
- Modify: `web/src/pages/cluster-monitor/page.test.tsx`
- Modify: `web/src/pages/cluster-monitor/types.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing manager API client test**

In `web/src/lib/manager-api.test.ts`, add:

```ts
it("fetches cluster realtime monitor data with window and step params", async () => {
  const response = {
    status: "partial",
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "cluster" },
    sources: {
      prometheus: { enabled: true, base_url: "http://127.0.0.1:9090", query_ms: 12, error: "one metric missing" },
      control_snapshot: { enabled: true, query_ms: 1, error: "" },
    },
    snapshot: [],
    cards: [],
  }
  fetchMock.mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }))

  await expect(getClusterRealtimeMonitor({ window: "15m", step: "20s" })).resolves.toEqual(response)

  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/cluster-monitor/realtime?window=15m&step=20s",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )
})
```

Import `getClusterRealtimeMonitor` at the top of the test file.

- [ ] **Step 2: Run the client test and verify it fails**

Run:

```bash
cd web && bunx vitest run src/lib/manager-api.test.ts --pool=threads
```

Expected: fail because `getClusterRealtimeMonitor` is not exported.

- [ ] **Step 3: Add cluster monitor API types**

Add to `web/src/lib/manager-api.types.ts`:

```ts
export type ClusterRealtimeMonitorStatus = "ready" | "partial" | "prometheus_disabled" | "prometheus_unavailable"

export type ClusterRealtimeMonitorTone = "normal" | "warning" | "critical"

export type ClusterRealtimeMonitorPoint = RealtimeMonitorPoint

export type ClusterRealtimeMonitorStat = {
  key: string
  value?: number
  text?: string
  unit?: string
}

export type ClusterRealtimeMonitorCard = {
  key: string
  stage: string
  tone: ClusterRealtimeMonitorTone
  unit: string
  value: number
  source: "prometheus" | "control_snapshot" | string
  available: boolean
  error: string
  series: ClusterRealtimeMonitorPoint[]
  stats: ClusterRealtimeMonitorStat[]
}

export type ClusterRealtimeMonitorSnapshotEntry = {
  key: string
  metric_key: string
  value: number
  unit?: string
  tone: ClusterRealtimeMonitorTone
  source: "prometheus" | "control_snapshot" | string
}

export type ClusterRealtimeMonitorResponse = {
  status: ClusterRealtimeMonitorStatus
  generated_at: string
  window_seconds: number
  step_seconds: number
  scope: {
    view: "cluster"
  }
  sources: {
    prometheus: {
      enabled: boolean
      base_url: string
      query_ms: number
      error: string
    }
    control_snapshot: {
      enabled: boolean
      query_ms: number
      error: string
    }
  }
  snapshot: ClusterRealtimeMonitorSnapshotEntry[]
  cards: ClusterRealtimeMonitorCard[]
}
```

- [ ] **Step 4: Add API client method**

Update imports in `web/src/lib/manager-api.ts` to include `ClusterRealtimeMonitorResponse`, then add:

```ts
export function getClusterRealtimeMonitor(params?: { window?: string; step?: string }) {
  const search = new URLSearchParams()
  if (params?.window) search.set("window", params.window)
  if (params?.step) search.set("step", params.step)
  const query = search.toString()
  return jsonManagerFetch<ClusterRealtimeMonitorResponse>(`/manager/cluster-monitor/realtime${query ? `?${query}` : ""}`)
}
```

- [ ] **Step 5: Create metric config for API mapping**

Create `web/src/pages/cluster-monitor/metric-config.ts`:

```ts
import type { ClusterMonitorMetricKey, ClusterMonitorStage } from "./types"

export const clusterMonitorMetricConfig: Record<ClusterMonitorMetricKey, { titleId: string; chartColor: string; precision: number }> = {
  controllerProposeRate: { titleId: "clusterMonitor.card.controllerProposeRate", chartColor: "#2563eb", precision: 1 },
  controllerApplyGap: { titleId: "clusterMonitor.card.controllerApplyGap", chartColor: "#4f46e5", precision: 0 },
  slotLeaderStability: { titleId: "clusterMonitor.card.slotLeaderStability", chartColor: "#0f766e", precision: 2 },
  slotReplicaLagP99: { titleId: "clusterMonitor.card.slotReplicaLagP99", chartColor: "#14b8a6", precision: 1 },
  channelISRHealth: { titleId: "clusterMonitor.card.channelISRHealth", chartColor: "#16a34a", precision: 2 },
  channelAppendLatencyP99: { titleId: "clusterMonitor.card.channelAppendLatencyP99", chartColor: "#22c55e", precision: 1 },
  internalTraffic: { titleId: "clusterMonitor.card.internalTraffic", chartColor: "#0891b2", precision: 1 },
  rpcSuccessRate: { titleId: "clusterMonitor.card.rpcSuccessRate", chartColor: "#6366f1", precision: 2 },
  rpcLatencyP95: { titleId: "clusterMonitor.card.rpcLatencyP95", chartColor: "#06b6d4", precision: 1 },
  workqueuePressure: { titleId: "clusterMonitor.card.workqueuePressure", chartColor: "#d97706", precision: 1 },
  storageWriteP99: { titleId: "clusterMonitor.card.storageWriteP99", chartColor: "#e11d48", precision: 1 },
  incidentRate: { titleId: "clusterMonitor.card.incidentRate", chartColor: "#dc2626", precision: 2 },
}

export const clusterMonitorStageLabelIds: Record<ClusterMonitorStage, string> = {
  controlPlane: "clusterMonitor.stage.controlPlane",
  slotReplication: "clusterMonitor.stage.slotReplication",
  channelReplication: "clusterMonitor.stage.channelReplication",
  internalNetwork: "clusterMonitor.stage.internalNetwork",
  runtimePressure: "clusterMonitor.stage.runtimePressure",
  incidentClosure: "clusterMonitor.stage.incidentClosure",
}

export const clusterMonitorSnapshotLabelIds: Record<string, string> = {
  nodesAlive: "clusterMonitor.snapshot.nodesAlive",
  slotsReady: "clusterMonitor.snapshot.slotsReady",
  controllerApplyGap: "clusterMonitor.snapshot.controllerApplyGap",
  channelISRAnomalies: "clusterMonitor.snapshot.channelISRAnomalies",
  rpcErrorRate: "clusterMonitor.snapshot.rpcErrorRate",
  queuePressure: "clusterMonitor.snapshot.queuePressure",
  storageWriteP99: "clusterMonitor.snapshot.storageWriteP99",
}

export const clusterMonitorStatLabelIds: Record<string, string> = {
  avg: "clusterMonitor.stat.avg",
  peak: "clusterMonitor.stat.peak",
  total: "clusterMonitor.stat.total",
  callsPerSecond: "clusterMonitor.stat.callsPerSecond",
  errorsPerSecond: "clusterMonitor.stat.errorsPerSecond",
  topReason: "clusterMonitor.stat.topReason",
}

export const clusterMonitorStatusByTone = {
  normal: "clusterMonitor.status.normal",
  warning: "clusterMonitor.status.warning",
  critical: "clusterMonitor.status.critical",
} as const
```

- [ ] **Step 6: Update page tests for API states**

Mock the API in `web/src/pages/cluster-monitor/page.test.tsx`:

```ts
import { getClusterRealtimeMonitor } from "@/lib/manager-api"

vi.mock("@/lib/manager-api", () => ({
  getClusterRealtimeMonitor: vi.fn(),
}))
```

Add a ready fixture:

```ts
function readyClusterMonitorResponse() {
  return {
    status: "ready",
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "cluster" },
    sources: {
      prometheus: { enabled: true, base_url: "http://127.0.0.1:9090", query_ms: 12, error: "" },
      control_snapshot: { enabled: true, query_ms: 1, error: "" },
    },
    snapshot: [
      { key: "nodesAlive", metric_key: "nodesAlive", value: 3, tone: "normal", source: "control_snapshot" },
      { key: "slotsReady", metric_key: "slotLeaderStability", value: 128, tone: "normal", source: "control_snapshot" },
    ],
    cards: [
      {
        key: "controllerProposeRate",
        stage: "controlPlane",
        tone: "normal",
        unit: "cmd/s",
        value: 15,
        source: "prometheus",
        available: true,
        error: "",
        series: [{ timestamp: 1781767200000, value: 15 }],
        stats: [{ key: "avg", value: 14.5, unit: "cmd/s" }, { key: "peak", value: 16, unit: "cmd/s" }],
      },
      {
        key: "incidentRate",
        stage: "incidentClosure",
        tone: "critical",
        unit: "events/min",
        value: 2,
        source: "prometheus",
        available: true,
        error: "",
        series: [{ timestamp: 1781767200000, value: 2 }],
        stats: [{ key: "topReason", text: "timeout" }],
      },
    ],
  } as const
}
```

Update the existing render test to wait for API data:

```ts
vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce(readyClusterMonitorResponse())
renderClusterMonitorPage()
expect(await screen.findByText("Controller Propose Rate")).toBeInTheDocument()
expect(screen.getByText("Incident Rate")).toBeInTheDocument()
expect(getClusterRealtimeMonitor).toHaveBeenCalledWith({ window: "15m" })
```

Add a disabled-state test:

```ts
vi.mocked(getClusterRealtimeMonitor).mockResolvedValueOnce({
  ...readyClusterMonitorResponse(),
  status: "prometheus_disabled",
  sources: {
    prometheus: { enabled: false, base_url: "", query_ms: 0, error: "prometheus is disabled" },
    control_snapshot: { enabled: false, query_ms: 0, error: "" },
  },
  snapshot: [],
  cards: [],
})
renderClusterMonitorPage()
expect(await screen.findByText("Prometheus is not enabled")).toBeInTheDocument()
expect(screen.queryByTestId("cluster-monitor-metric-card")).not.toBeInTheDocument()
```

- [ ] **Step 7: Update `ClusterMonitorPage` to use API state**

In `web/src/pages/cluster-monitor/page.tsx`, import the API and types:

```ts
import { useEffect, useMemo, useState } from "react"
import { getClusterRealtimeMonitor } from "@/lib/manager-api"
import type {
  ClusterRealtimeMonitorCard,
  ClusterRealtimeMonitorResponse,
  ClusterRealtimeMonitorSnapshotEntry as ApiClusterSnapshotEntry,
  ClusterRealtimeMonitorTone,
} from "@/lib/manager-api.types"
```

Use the same state shape as the business monitor page:

```ts
type ClusterMonitorPageState =
  | { kind: "loading" }
  | { kind: "ready"; response: ClusterRealtimeMonitorResponse }
  | { kind: "error"; message: string }
```

Fetch on `timeRange` changes:

```ts
useEffect(() => {
  let cancelled = false
  setState({ kind: "loading" })

  getClusterRealtimeMonitor({ window: timeRange })
    .then((response) => {
      if (!cancelled) setState({ kind: "ready", response })
    })
    .catch((error: unknown) => {
      if (!cancelled) setState({ kind: "error", message: error instanceof Error ? error.message : String(error) })
    })

  return () => {
    cancelled = true
  }
}, [timeRange])
```

Map API cards through `clusterMonitorMetricConfig`, skip unknown keys, keep unavailable cards visible with their configured title and empty series, and map `stat.text` before numeric formatting.

- [ ] **Step 8: Add source state i18n**

Add English messages:

```ts
"clusterMonitor.prometheus.loading": "Loading cluster monitor data...",
"clusterMonitor.prometheus.partialTitle": "Some cluster monitor cards are unavailable",
"clusterMonitor.prometheus.disabledTitle": "Prometheus is not enabled",
"clusterMonitor.prometheus.disabledDescription": "Enable metrics and Prometheus before using the cluster realtime monitor.",
"clusterMonitor.prometheus.unavailableTitle": "Prometheus is unavailable",
"clusterMonitor.prometheus.unavailableDescription": "The manager could not query Prometheus for cluster monitor data.",
```

Add matching Chinese messages:

```ts
"clusterMonitor.prometheus.loading": "正在加载集群监控数据...",
"clusterMonitor.prometheus.partialTitle": "部分集群监控卡片暂不可用",
"clusterMonitor.prometheus.disabledTitle": "Prometheus 未启用",
"clusterMonitor.prometheus.disabledDescription": "启用 metrics 和 Prometheus 后才能查看集群实时监控。",
"clusterMonitor.prometheus.unavailableTitle": "Prometheus 不可用",
"clusterMonitor.prometheus.unavailableDescription": "Manager 无法查询 Prometheus 集群监控数据。",
```

- [ ] **Step 9: Run frontend tests**

Run:

```bash
cd web && bunx vitest run src/lib/manager-api.test.ts src/pages/cluster-monitor/page.test.tsx src/pages/cluster-monitor/preview-data.test.ts --pool=threads
```

Expected: PASS.

- [ ] **Step 10: Commit Task 4**

Run:

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts web/src/pages/cluster-monitor/metric-config.ts web/src/pages/cluster-monitor/page.tsx web/src/pages/cluster-monitor/page.test.tsx web/src/pages/cluster-monitor/types.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: connect cluster monitor page to api"
```

## Task 5: Final Verification And Cleanup

**Files:**
- Modify only files required by failures found during verification.

- [ ] **Step 1: Run focused Go tests**

Run:

```bash
go test ./internalv2/access/manager ./internalv2/app ./pkg/metrics
```

Expected: PASS.

- [ ] **Step 2: Run focused web tests**

Run:

```bash
cd web && bunx vitest run src/lib/manager-api.test.ts src/pages/cluster-monitor/page.test.tsx src/pages/cluster-monitor/preview-data.test.ts src/app/router.test.tsx src/lib/navigation.test.ts src/app/layout/sidebar-nav.test.tsx --pool=threads
```

Expected: PASS.

- [ ] **Step 3: Run TypeScript and production build**

Run:

```bash
cd web && bunx tsc -b
cd web && bunx vite build
```

Expected: both commands exit 0. Vite may print the existing large chunk warning.

- [ ] **Step 4: Inspect generated asset churn**

Run:

```bash
git status --short web/dist/index.html
```

If `web/dist/index.html` changed only because Vite rewrote hashed asset names, restore that generated file before committing:

```bash
git restore -- web/dist/index.html
```

- [ ] **Step 5: Review the final diff**

Run:

```bash
git diff --stat
git diff -- internalv2/access/manager internalv2/app pkg/metrics web/src/lib web/src/pages/cluster-monitor web/src/i18n/messages
```

Expected: only cluster monitor API, low-cardinality metrics, frontend API mapping, and required FLOW/i18n changes are present.

- [ ] **Step 6: Commit any verification fixes**

If verification required small fixes after Task 4, commit them:

```bash
git add internalv2/access/manager internalv2/app pkg/metrics web/src/lib web/src/pages/cluster-monitor web/src/i18n/messages
git commit -m "fix: stabilize cluster monitor api integration"
```

Skip this commit when Task 1 through Task 4 already contain all changes and verification produces no further diff.

## Self-Review

- Spec coverage: endpoint contract is covered by Task 1, Prometheus and control snapshot sources by Task 2, missing low-cardinality metrics by Task 3, frontend integration by Task 4, and verification by Task 5.
- Scope check: this is one coherent API integration. It does not include node drill-down, alert editing, custom dashboards, or high-cardinality channel scans.
- Type consistency: Go uses `ClusterRealtimeMonitor*` DTOs and `ClusterRealtimeMonitorProvider`; frontend uses `ClusterRealtimeMonitor*` API types and maps into existing `ClusterMonitor*` display types.
- Placeholder scan target: this plan should have a clean red-flag scan and every task should name concrete files and commands.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-18-cluster-monitor-api.md`. Two execution options:

1. **Subagent-Driven (recommended)** - dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** - execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
