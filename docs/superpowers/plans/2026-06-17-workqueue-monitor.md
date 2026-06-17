# Workqueue Monitor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Web cluster-operations Workqueue monitor that shows local-node runtime queue and worker pressure from `wukongimv2`.

**Architecture:** Reuse the existing `internalv2/app` top collector as the single source of local-node runtime pressure. Expose a manager-facing read-only endpoint at `/manager/runtime/workqueues` so the Web app stays on the manager listener, then add a dense `/cluster/workqueues` UI that renders the returned pressure items.

**Tech Stack:** Go, Gin, `internalv2` manager API, `topCollector`, TypeScript, React, React Router, Vitest, existing Web shell and i18n utilities.

---

## Pre-Execution Notes

- The repository may contain unrelated dirty `pkg/gateway` changes. Do not stage, commit, revert, or reformat those files while executing this plan.
- Read `internalv2/access/manager/FLOW.md`, `internalv2/app/FLOW.md`, `pkg/workqueue/FLOW.md`, and the target package files before editing each area.
- Keep this v1 local-node only. Do not add node fan-out or cluster aggregation.
- Keep Web calls on `/manager/runtime/workqueues`, not `/top/v1/snapshot`.

## File Structure

Backend files:

- `internalv2/app/top_collector.go`: enrich `TopPressureItem` with wait p99, task p99, and admission error rate.
- `internalv2/app/top_collector_test.go`: tests for pressure scoring and diagnostic enrichment.
- `internalv2/access/manager/server.go`: add `Top` to `Options`, `Server`, and route registration.
- `internalv2/access/manager/runtime_workqueues.go`: new manager DTOs, query parsing, response mapping, and handler.
- `internalv2/access/manager/server_test.go`: route, auth, warming-up, and unavailable tests.
- `internalv2/app/wiring.go`: pass `a.topProvider` into manager.
- `internalv2/app/FLOW.md`: mention manager Workqueue route and top provider wiring.
- `internalv2/access/manager/FLOW.md`: document `GET /manager/runtime/workqueues`.

Frontend files:

- `web/src/lib/manager-api.types.ts`: add runtime Workqueue response types.
- `web/src/lib/manager-api.ts`: add `getRuntimeWorkqueues`.
- `web/src/lib/manager-api.test.ts`: verify the client path.
- `web/src/lib/navigation.ts`: add Cluster Ops navigation item and legacy redirect.
- `web/src/lib/navigation.test.ts`: cover metadata and redirect.
- `web/src/app/router.tsx`: add `/cluster/workqueues` route and `/workqueues` redirect.
- `web/src/app/router.test.tsx`: cover route/redirect.
- `web/src/pages/workqueues/page.tsx`: new page with controls, summary, table, and details.
- `web/src/pages/workqueues/page.test.tsx`: UI behavior tests.
- `web/src/i18n/messages/zh-CN.ts`: Chinese strings.
- `web/src/i18n/messages/en.ts`: English strings.

## Task 1: Enrich Top Collector Pressure Items

**Files:**
- Modify: `internalv2/app/top_collector.go`
- Test: `internalv2/app/top_collector_test.go`

- [ ] **Step 1: Write failing pressure enrichment tests**

Add these tests near the existing top collector pressure tests in `internalv2/app/top_collector_test.go`:

```go
func TestTopCollectorPressureIncludesWaitTaskAndAdmissionErrors(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID:          1,
		CollectInterval: time.Second,
		HistoryWindow:   time.Minute,
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})

	collector.SetQueue("gateway", "async_send", "send", "none", 6, 10)
	collector.observeDurationMS("pressure.gateway.async_send.send.none.wait", 3*time.Millisecond)
	collector.observeDurationMS("pressure.gateway.async_send.send.none.wait", 9*time.Millisecond)
	collector.observeDurationMS("pressure.gateway.async_send.dispatch.ok.task", 5*time.Millisecond)
	collector.observeDurationMS("pressure.gateway.async_send.dispatch.ok.task", 15*time.Millisecond)
	collector.addCounter("pressure.gateway.async_send.send.none.admission_error", 0)
	collector.recordSampleAt(time.Unix(100, 0))

	collector.SetQueue("gateway", "async_send", "send", "none", 8, 10)
	collector.observeDurationMS("pressure.gateway.async_send.send.none.wait", 10*time.Millisecond)
	collector.observeDurationMS("pressure.gateway.async_send.dispatch.ok.task", 20*time.Millisecond)
	collector.addCounter("pressure.gateway.async_send.send.none.admission_error", 4)
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewRuntime,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	if snapshot.Pressure == nil || len(snapshot.Pressure.Top) != 1 {
		t.Fatalf("Pressure.Top = %#v, want one item", snapshot.Pressure)
	}
	item := snapshot.Pressure.Top[0]
	if item.Component != "gateway" || item.Pool != "async_send" || item.Queue != "send" || item.Priority != "none" {
		t.Fatalf("pressure item identity = %#v, want gateway async_send send none", item)
	}
	if math.Abs(item.Score-0.8) > 0.000001 || item.Level != "degraded" {
		t.Fatalf("score/level = %.3f/%s, want 0.8/degraded", item.Score, item.Level)
	}
	if item.WaitP99MS <= 0 {
		t.Fatalf("WaitP99MS = %v, want populated", item.WaitP99MS)
	}
	if item.TaskP99MS <= 0 {
		t.Fatalf("TaskP99MS = %v, want populated", item.TaskP99MS)
	}
	if math.Abs(item.AdmissionErrorPerSec-0.4) > 0.000001 {
		t.Fatalf("AdmissionErrorPerSec = %v, want 0.4", item.AdmissionErrorPerSec)
	}
}

func TestTopCollectorPressureUsesInflightWhenHigherThanQueueDepth(t *testing.T) {
	collector := newTopCollector(topCollectorOptions{
		NodeID:          1,
		CollectInterval: time.Second,
		HistoryWindow:   time.Minute,
		ClusterSnapshot: func() clusterv2.Snapshot {
			return clusterv2.Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
		},
	})

	collector.SetQueue("channelv2", "store_append", "worker", "none", 1, 10)
	collector.SetInflight("channelv2", "store_append", 9, 10)
	collector.recordSampleAt(time.Unix(100, 0))
	collector.SetQueue("channelv2", "store_append", "worker", "none", 1, 10)
	collector.SetInflight("channelv2", "store_append", 9, 10)
	collector.recordSampleAt(time.Unix(110, 0))

	snapshot, err := collector.SnapshotTop(context.Background(), accessapi.TopSnapshotQuery{
		Window: 10 * time.Second,
		View:   accessapi.TopViewRuntime,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("SnapshotTop() error = %v", err)
	}
	item := snapshot.Pressure.Top[0]
	if math.Abs(item.Score-0.9) > 0.000001 || item.Level != "degraded" {
		t.Fatalf("score/level = %.3f/%s, want inflight score 0.9 degraded", item.Score, item.Level)
	}
	if item.Inflight != 9 || item.Workers != 10 {
		t.Fatalf("inflight/workers = %d/%d, want 9/10", item.Inflight, item.Workers)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/app -run 'TestTopCollectorPressureIncludesWaitTaskAndAdmissionErrors|TestTopCollectorPressureUsesInflightWhenHigherThanQueueDepth'
```

Expected: the first test fails because `WaitP99MS`, `TaskP99MS`, and `AdmissionErrorPerSec` are not populated yet.

- [ ] **Step 3: Implement pressure diagnostic enrichment**

In `internalv2/app/top_collector.go`, update `buildPressureLocked` to accept the current sample and compute diagnostic fields from the retained sample window. Replace the current call in `SnapshotTop`:

```go
pressure := c.buildPressureLocked(window, query.Limit)
```

Then replace `buildPressureLocked` with this shape:

```go
func (c *topCollector) buildPressureLocked(window []topSample, limit int) *accessapi.TopPressure {
	if limit <= 0 {
		limit = 20
	}
	if len(window) == 0 {
		return &accessapi.TopPressure{OverallLevel: "ok"}
	}
	sample := window[len(window)-1]
	seconds := sampleWindowSeconds(window)
	byKey := make(map[string]*accessapi.TopPressureItem)
	for key, value := range sample.gauges {
		parts := strings.Split(key, ".")
		if len(parts) != 6 || parts[0] != "pressure" {
			continue
		}
		base := strings.Join(parts[:5], ".")
		item := byKey[base]
		if item == nil {
			item = &accessapi.TopPressureItem{
				Component: parts[1],
				Pool:      parts[2],
				Queue:     parts[3],
				Priority:  parts[4],
			}
			byKey[base] = item
		}
		switch parts[5] {
		case "depth":
			item.Depth = value
		case "capacity":
			item.Capacity = value
		case "inflight":
			item.Inflight = value
		case "workers":
			item.Workers = value
		}
	}

	items := make([]accessapi.TopPressureItem, 0, len(byKey))
	componentScores := make(map[string]float64)
	overall := "ok"
	for base, item := range byKey {
		item.Score = pressureScore(item.Depth, item.Capacity)
		if inflightScore := inflightPressureScore(item.Inflight, item.Workers); inflightScore > item.Score {
			item.Score = inflightScore
		}
		item.Level = pressureLevel(item.Score)
		item.Hint = pressureHint(*item)
		item.WaitP99MS = percentile(histogramValues(window, base+".wait"), 0.99)
		item.TaskP99MS = pressureTaskP99MS(window, item.Component, item.Pool)
		item.AdmissionErrorPerSec = pressureAdmissionErrorRate(window, base, seconds)
		items = append(items, *item)
		if item.Score > componentScores[item.Component] {
			componentScores[item.Component] = item.Score
		}
		if severityRank(item.Level) > severityRank(overall) {
			overall = item.Level
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Component != items[j].Component {
			return items[i].Component < items[j].Component
		}
		if items[i].Pool != items[j].Pool {
			return items[i].Pool < items[j].Pool
		}
		if items[i].Queue != items[j].Queue {
			return items[i].Queue < items[j].Queue
		}
		return items[i].Priority < items[j].Priority
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return &accessapi.TopPressure{
		OverallLevel:    overall,
		ComponentScores: componentScores,
		Top:             items,
	}
}

func sampleWindowSeconds(window []topSample) float64 {
	if len(window) < 2 {
		return 0
	}
	return window[len(window)-1].at.Sub(window[0].at).Seconds()
}

func pressureTaskP99MS(window []topSample, component, pool string) float64 {
	prefix := "pressure." + safeTopLabel(component) + "." + safeTopLabel(pool) + "."
	values := make([]float64, 0)
	for _, key := range histogramKeys(window, prefix) {
		if strings.HasSuffix(key, ".task") {
			values = append(values, histogramValues(window, key)...)
		}
	}
	return percentile(values, 0.99)
}

func pressureAdmissionErrorRate(window []topSample, base string, seconds float64) float64 {
	if len(window) < 2 || seconds <= 0 {
		return 0
	}
	first := window[0]
	last := window[len(window)-1]
	return float64(counterDelta(first.counters, last.counters, base+".admission_error")) / seconds
}
```

Update any remaining calls to `buildPressureLocked(last, ...)`, including alert generation, to pass a window:

```go
pressure := c.buildPressureLocked(window, topMaxRetainedAlerts)
```

When alert generation currently only has `first` and `last`, derive the window once at the start of `alertSignalsLocked` with:

```go
window := c.windowLocked(topAlertSignalWindow)
```

and use that same `window` for traffic and pressure calculations.

- [ ] **Step 4: Run focused collector tests**

Run:

```bash
go test ./internalv2/app -run 'TestTopCollector'
```

Expected: all `TestTopCollector*` tests pass.

- [ ] **Step 5: Commit collector enrichment**

```bash
git add internalv2/app/top_collector.go internalv2/app/top_collector_test.go
git commit -m "feat: enrich top pressure diagnostics"
```

## Task 2: Add Manager Runtime Workqueue Endpoint

**Files:**
- Modify: `internalv2/access/manager/server.go`
- Create: `internalv2/access/manager/runtime_workqueues.go`
- Test: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Write failing manager route tests**

Add imports to `internalv2/access/manager/server_test.go`:

```go
import (
	"errors"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
)
```

Add these tests and helper near other manager route tests:

```go
type managerTopStub struct {
	snapshot accessapi.TopSnapshot
	err      error
	query    accessapi.TopSnapshotQuery
}

func (s *managerTopStub) SnapshotTop(_ context.Context, query accessapi.TopSnapshotQuery) (accessapi.TopSnapshot, error) {
	s.query = query
	if s.err != nil {
		return accessapi.TopSnapshot{}, s.err
	}
	return s.snapshot, nil
}

func TestManagerRuntimeWorkqueuesReturnsLocalTopPressure(t *testing.T) {
	generatedAt := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	top := &managerTopStub{snapshot: accessapi.TopSnapshot{
		GeneratedAt:   generatedAt,
		WindowSeconds: 10,
		Node: accessapi.TopNodeSnapshot{
			ID:    1,
			Name:  "node-1",
			Ready: true,
		},
		Pressure: &accessapi.TopPressure{
			OverallLevel: "degraded",
			Top: []accessapi.TopPressureItem{{
				Component:            "gateway",
				Pool:                 "async_send",
				Queue:                "send",
				Priority:             "none",
				Level:                "degraded",
				Score:                0.82,
				Depth:                82,
				Capacity:             100,
				WaitP99MS:            12.4,
				TaskP99MS:            20.5,
				AdmissionErrorPerSec: 0.3,
				Hint:                 "queue depth is approaching capacity",
			}},
		},
		Sources: accessapi.TopSources{
			Collector: accessapi.TopSourceStatus{Available: true, SampleCount: 10},
			Metrics:   accessapi.TopMetricsSource{Enabled: false, Required: false},
		},
	}}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Top: top,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues?window=10s&limit=100", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if top.query.View != accessapi.TopViewRuntime || top.query.Window != 10*time.Second || top.query.Limit != 100 {
		t.Fatalf("query = %#v, want runtime 10s limit 100", top.query)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at":"2026-06-17T10:00:00Z",
		"window_seconds":10,
		"scope":{"view":"local_node","node_id":1,"node_name":"node-1","ready":true},
		"summary":{
			"overall_level":"degraded",
			"total":1,
			"ok":0,
			"busy":0,
			"degraded":1,
			"critical":0,
			"hottest":{"component":"gateway","pool":"async_send","queue":"send","priority":"none","level":"degraded","score":0.82}
		},
		"items":[{
			"component":"gateway",
			"pool":"async_send",
			"queue":"send",
			"priority":"none",
			"level":"degraded",
			"score":0.82,
			"depth":82,
			"capacity":100,
			"inflight":0,
			"workers":0,
			"wait_p99_ms":12.4,
			"task_p99_ms":20.5,
			"admission_error_per_sec":0.3,
			"hint":"queue depth is approaching capacity"
		}],
		"sources":{"collector":{"available":true,"sample_count":10},"metrics":{"enabled":false,"required":false},"notes":[]}
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerRuntimeWorkqueuesMapsUnavailableSources(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"top snapshot provider is not configured"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerRuntimeWorkqueuesMapsWarmingUp(t *testing.T) {
	srv := New(Options{Top: &managerTopStub{err: accessapi.ErrTopWarmingUp}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"top collector warming up"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerRuntimeWorkqueuesRequiresNodeReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Top: &managerTopStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerRuntimeWorkqueues'
```

Expected: compile fails because `Options.Top` and the route do not exist.

- [ ] **Step 3: Add manager option, server field, and route group**

In `internalv2/access/manager/server.go`, import the API package:

```go
accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
```

Add to `Options`:

```go
// Top provides local runtime pressure snapshots for read-only runtime views.
Top accessapi.TopSnapshotProvider
```

Add to `Server`:

```go
top accessapi.TopSnapshotProvider
```

Set it in `New`:

```go
top: opts.Top,
```

Register the route:

```go
runtime := s.engine.Group("/manager")
if s.auth.enabled() {
	runtime.Use(s.requirePermission("cluster.node", "r"))
}
runtime.GET("/runtime/workqueues", s.handleRuntimeWorkqueues)
```

- [ ] **Step 4: Implement runtime_workqueues.go**

Create `internalv2/access/manager/runtime_workqueues.go`:

```go
package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	"github.com/gin-gonic/gin"
)

const (
	defaultRuntimeWorkqueueWindow = 10 * time.Second
	minRuntimeWorkqueueWindow     = 2 * time.Second
	defaultRuntimeWorkqueueLimit  = 100
	maxRuntimeWorkqueueLimit      = 200
)

// RuntimeWorkqueuesResponse is the manager-facing local runtime pressure response.
type RuntimeWorkqueuesResponse struct {
	// GeneratedAt is when the snapshot was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// WindowSeconds is the aggregation window represented by rates and percentiles.
	WindowSeconds int `json:"window_seconds"`
	// Scope describes the local node that produced the snapshot.
	Scope RuntimeWorkqueueScopeDTO `json:"scope"`
	// Summary contains count and hottest-item totals for the UI.
	Summary RuntimeWorkqueueSummaryDTO `json:"summary"`
	// Items contains ordered local runtime pressure entries.
	Items []RuntimeWorkqueueItemDTO `json:"items"`
	// Sources reports collector and metrics source status.
	Sources RuntimeWorkqueueSourcesDTO `json:"sources"`
}

// RuntimeWorkqueueScopeDTO describes the local-node response scope.
type RuntimeWorkqueueScopeDTO struct {
	// View is always local_node for v1.
	View string `json:"view"`
	// NodeID is the cluster node that served the snapshot.
	NodeID uint64 `json:"node_id"`
	// NodeName is the operator-facing node name.
	NodeName string `json:"node_name"`
	// Ready reports whether required local cluster parts are ready.
	Ready bool `json:"ready"`
}

// RuntimeWorkqueueSummaryDTO summarizes the returned pressure list.
type RuntimeWorkqueueSummaryDTO struct {
	OverallLevel string                   `json:"overall_level"`
	Total        int                      `json:"total"`
	OK           int                      `json:"ok"`
	Busy         int                      `json:"busy"`
	Degraded     int                      `json:"degraded"`
	Critical     int                      `json:"critical"`
	Hottest      *RuntimeWorkqueueHotDTO  `json:"hottest,omitempty"`
}

// RuntimeWorkqueueHotDTO identifies the highest pressure item.
type RuntimeWorkqueueHotDTO struct {
	Component string  `json:"component"`
	Pool      string  `json:"pool"`
	Queue     string  `json:"queue"`
	Priority  string  `json:"priority"`
	Level     string  `json:"level"`
	Score     float64 `json:"score"`
}

// RuntimeWorkqueueItemDTO describes one runtime queue or worker pressure signal.
type RuntimeWorkqueueItemDTO struct {
	Component            string  `json:"component"`
	Pool                 string  `json:"pool"`
	Queue                string  `json:"queue"`
	Priority             string  `json:"priority"`
	Level                string  `json:"level"`
	Score                float64 `json:"score"`
	Depth                int64   `json:"depth"`
	Capacity             int64   `json:"capacity"`
	Inflight             int64   `json:"inflight"`
	Workers              int64   `json:"workers"`
	WaitP99MS            float64 `json:"wait_p99_ms"`
	TaskP99MS            float64 `json:"task_p99_ms"`
	AdmissionErrorPerSec float64 `json:"admission_error_per_sec"`
	Hint                 string  `json:"hint"`
}

// RuntimeWorkqueueSourcesDTO reports source availability for the snapshot.
type RuntimeWorkqueueSourcesDTO struct {
	Collector accessapi.TopSourceStatus `json:"collector"`
	Metrics   accessapi.TopMetricsSource `json:"metrics"`
	Notes     []string                  `json:"notes"`
}

func (s *Server) handleRuntimeWorkqueues(c *gin.Context) {
	if s == nil || s.top == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "top snapshot provider is not configured")
		return
	}
	query, err := parseRuntimeWorkqueueQuery(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	snapshot, err := s.top.SnapshotTop(c.Request.Context(), query)
	if err != nil {
		if errors.Is(err, accessapi.ErrTopWarmingUp) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "top collector warming up")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", "runtime workqueue snapshot failed")
		return
	}
	c.JSON(http.StatusOK, runtimeWorkqueuesResponse(snapshot))
}

func parseRuntimeWorkqueueQuery(c *gin.Context) (accessapi.TopSnapshotQuery, error) {
	query := accessapi.TopSnapshotQuery{
		Window: defaultRuntimeWorkqueueWindow,
		View:   accessapi.TopViewRuntime,
		Limit:  defaultRuntimeWorkqueueLimit,
	}
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return query, errors.New("window must be a duration")
		}
		if d < minRuntimeWorkqueueWindow {
			return query, errors.New("window must be at least 2s")
		}
		query.Window = d
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return query, errors.New("limit must be positive")
		}
		if n > maxRuntimeWorkqueueLimit {
			n = maxRuntimeWorkqueueLimit
		}
		query.Limit = n
	}
	return query, nil
}

func runtimeWorkqueuesResponse(snapshot accessapi.TopSnapshot) RuntimeWorkqueuesResponse {
	items := runtimeWorkqueueItems(snapshot.Pressure)
	return RuntimeWorkqueuesResponse{
		GeneratedAt:   snapshot.GeneratedAt,
		WindowSeconds: snapshot.WindowSeconds,
		Scope: RuntimeWorkqueueScopeDTO{
			View:     "local_node",
			NodeID:   snapshot.Node.ID,
			NodeName: snapshot.Node.Name,
			Ready:    snapshot.Node.Ready,
		},
		Summary: runtimeWorkqueueSummary(snapshot.Pressure, items),
		Items:   items,
		Sources: RuntimeWorkqueueSourcesDTO{
			Collector: snapshot.Sources.Collector,
			Metrics:   snapshot.Sources.Metrics,
			Notes:     append([]string(nil), snapshot.Sources.Notes...),
		},
	}
}

func runtimeWorkqueueItems(pressure *accessapi.TopPressure) []RuntimeWorkqueueItemDTO {
	if pressure == nil || len(pressure.Top) == 0 {
		return []RuntimeWorkqueueItemDTO{}
	}
	out := make([]RuntimeWorkqueueItemDTO, 0, len(pressure.Top))
	for _, item := range pressure.Top {
		out = append(out, RuntimeWorkqueueItemDTO{
			Component:            item.Component,
			Pool:                 item.Pool,
			Queue:                item.Queue,
			Priority:             item.Priority,
			Level:                item.Level,
			Score:                item.Score,
			Depth:                item.Depth,
			Capacity:             item.Capacity,
			Inflight:             item.Inflight,
			Workers:              item.Workers,
			WaitP99MS:            item.WaitP99MS,
			TaskP99MS:            item.TaskP99MS,
			AdmissionErrorPerSec: item.AdmissionErrorPerSec,
			Hint:                 item.Hint,
		})
	}
	return out
}

func runtimeWorkqueueSummary(pressure *accessapi.TopPressure, items []RuntimeWorkqueueItemDTO) RuntimeWorkqueueSummaryDTO {
	summary := RuntimeWorkqueueSummaryDTO{OverallLevel: "ok", Total: len(items)}
	if pressure != nil && strings.TrimSpace(pressure.OverallLevel) != "" {
		summary.OverallLevel = pressure.OverallLevel
	}
	for _, item := range items {
		switch item.Level {
		case "busy":
			summary.Busy++
		case "degraded":
			summary.Degraded++
		case "critical":
			summary.Critical++
		default:
			summary.OK++
		}
	}
	if len(items) > 0 {
		top := items[0]
		summary.Hottest = &RuntimeWorkqueueHotDTO{
			Component: top.Component,
			Pool:      top.Pool,
			Queue:     top.Queue,
			Priority:  top.Priority,
			Level:     top.Level,
			Score:     top.Score,
		}
	}
	return summary
}
```

- [ ] **Step 5: Run manager route tests**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerRuntimeWorkqueues'
```

Expected: all runtime workqueue manager tests pass.

- [ ] **Step 6: Commit manager endpoint**

```bash
git add internalv2/access/manager/server.go internalv2/access/manager/runtime_workqueues.go internalv2/access/manager/server_test.go
git commit -m "feat: expose manager workqueue pressure"
```

## Task 3: Wire Manager Top Provider And Update Flow Docs

**Files:**
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/access/manager/FLOW.md`
- Test: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing app wiring test**

In `internalv2/app/app_test.go`, add a test near other manager wiring tests:

```go
func TestManagerServerReceivesTopProviderWhenConfigured(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}
	app, err := newTestApp(t, Config{
		API: APIConfig{ListenAddr: "127.0.0.1:0"},
		Manager: ManagerConfig{
			ListenAddr: "127.0.0.1:0",
			AuthOn:    true,
			JWTSecret: "test-secret",
			Users: []ManagerUserConfig{{
				Username: "admin",
				Password: "secret",
				Permissions: []ManagerPermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			}},
		},
		Top: TopConfig{APIEnabled: true},
	}, WithCluster(cluster))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if app.topProvider == nil {
		t.Fatalf("topProvider = nil, want configured collector")
	}
	managerServer, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/runtime/workqueues", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueManagerTokenForAppTest(t, managerServer, "admin"))
	managerServer.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want warming-up service unavailable before samples; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "top collector warming up") {
		t.Fatalf("body = %s, want top collector warming up", rec.Body.String())
	}
}
```

If there is no helper named `mustIssueManagerTokenForAppTest`, add this test helper near other app manager helpers:

```go
func mustIssueManagerTokenForAppTest(t *testing.T, srv *accessmanager.Server, username string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/login", strings.NewReader(`{"username":"`+username+`","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("login unmarshal error = %v", err)
	}
	if body.AccessToken == "" {
		t.Fatalf("login access_token is empty")
	}
	return body.AccessToken
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internalv2/app -run TestManagerServerReceivesTopProviderWhenConfigured
```

Expected: test fails because manager is not wired with the top provider yet.

- [ ] **Step 3: Wire top provider into manager**

In `internalv2/app/wiring.go`, update `wireManager`:

```go
a.manager = accessmanager.New(accessmanager.Options{
	ListenAddr: a.cfg.Manager.ListenAddr,
	Auth: accessmanager.AuthConfig{
		On:        a.cfg.Manager.AuthOn,
		JWTSecret: a.cfg.Manager.JWTSecret,
		JWTIssuer: a.cfg.Manager.JWTIssuer,
		JWTExpire: a.cfg.Manager.JWTExpire,
		Users:     managerUserConfigs(a.cfg.Manager.Users),
	},
	Management: a.newManagerManagement(),
	Top:        a.topProvider,
	Logger:     a.logger.Named("access.manager"),
})
```

- [ ] **Step 4: Update flow docs**

In `internalv2/access/manager/FLOW.md`, add to the route list:

```text
GET  /manager/runtime/workqueues (local-node runtime pressure; requires cluster.node:r when Auth.On=true)
```

Add a short paragraph:

```markdown
`/manager/runtime/workqueues` exposes a local-node Workqueue/runtime pressure
view backed by the `internalv2/app` top collector. It forces the runtime view,
does not fan out to peer nodes, and remains independent of Prometheus metrics.
When the top collector is not configured or is warming up, the route returns
`service_unavailable`.
```

In `internalv2/app/FLOW.md`, update the manager construction bullet so it mentions:

```text
attach the local top provider for `/manager/runtime/workqueues` when
Top.APIEnabled creates a collector
```

- [ ] **Step 5: Run app and manager tests**

Run:

```bash
go test ./internalv2/app ./internalv2/access/manager
```

Expected: both packages pass.

- [ ] **Step 6: Commit wiring and docs**

```bash
git add internalv2/app/wiring.go internalv2/app/app_test.go internalv2/app/FLOW.md internalv2/access/manager/FLOW.md
git commit -m "feat: wire workqueue monitor provider"
```

## Task 4: Add Frontend API Types And Client

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Test: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client test**

In `web/src/lib/manager-api.test.ts`, add `getRuntimeWorkqueues` to the import list. Then add:

```ts
it("fetches local runtime workqueue pressure", async () => {
  const response = {
    generated_at: "2026-06-17T10:00:00Z",
    window_seconds: 10,
    scope: { view: "local_node", node_id: 1, node_name: "node-1", ready: true },
    summary: {
      overall_level: "degraded",
      total: 1,
      ok: 0,
      busy: 0,
      degraded: 1,
      critical: 0,
      hottest: {
        component: "gateway",
        pool: "async_send",
        queue: "send",
        priority: "none",
        level: "degraded",
        score: 0.82,
      },
    },
    items: [{
      component: "gateway",
      pool: "async_send",
      queue: "send",
      priority: "none",
      level: "degraded",
      score: 0.82,
      depth: 82,
      capacity: 100,
      inflight: 0,
      workers: 0,
      wait_p99_ms: 12.4,
      task_p99_ms: 20.5,
      admission_error_per_sec: 0.3,
      hint: "queue depth is approaching capacity",
    }],
    sources: {
      collector: { available: true, sample_count: 10 },
      metrics: { enabled: false, required: false },
      notes: [],
    },
  }
  fetchMock.mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }))

  await expect(getRuntimeWorkqueues({ window: "10s", limit: 100 })).resolves.toEqual(response)
  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/runtime/workqueues?window=10s&limit=100",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && npm test -- --run src/lib/manager-api.test.ts
```

Expected: compile fails because `getRuntimeWorkqueues` is missing.

- [ ] **Step 3: Add response types**

In `web/src/lib/manager-api.types.ts`, add:

```ts
export type ManagerRuntimeWorkqueueLevel = "ok" | "busy" | "degraded" | "critical" | string

export type ManagerRuntimeWorkqueueHotItem = {
  component: string
  pool: string
  queue: string
  priority: string
  level: ManagerRuntimeWorkqueueLevel
  score: number
}

export type ManagerRuntimeWorkqueueItem = {
  component: string
  pool: string
  queue: string
  priority: string
  level: ManagerRuntimeWorkqueueLevel
  score: number
  depth: number
  capacity: number
  inflight: number
  workers: number
  wait_p99_ms: number
  task_p99_ms: number
  admission_error_per_sec: number
  hint: string
}

export type ManagerRuntimeWorkqueuesResponse = {
  generated_at: string
  window_seconds: number
  scope: {
    view: "local_node" | string
    node_id: number
    node_name: string
    ready: boolean
  }
  summary: {
    overall_level: ManagerRuntimeWorkqueueLevel
    total: number
    ok: number
    busy: number
    degraded: number
    critical: number
    hottest?: ManagerRuntimeWorkqueueHotItem
  }
  items: ManagerRuntimeWorkqueueItem[]
  sources: {
    collector: {
      available: boolean
      sample_count?: number
      warming_up?: boolean
    }
    metrics: {
      enabled: boolean
      required: boolean
    }
    notes: string[]
  }
}
```

- [ ] **Step 4: Add API client function**

In `web/src/lib/manager-api.ts`, import `ManagerRuntimeWorkqueuesResponse` and add:

```ts
export function getRuntimeWorkqueues(params?: { window?: string; limit?: number }) {
  const search = new URLSearchParams()
  if (params?.window) search.set("window", params.window)
  if (typeof params?.limit === "number") search.set("limit", String(params.limit))
  const query = search.toString()
  return jsonManagerFetch<ManagerRuntimeWorkqueuesResponse>(`/manager/runtime/workqueues${query ? `?${query}` : ""}`)
}
```

- [ ] **Step 5: Run API client test**

Run:

```bash
cd web && npm test -- --run src/lib/manager-api.test.ts
```

Expected: `manager-api.test.ts` passes.

- [ ] **Step 6: Commit API client changes**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add workqueue manager client"
```

## Task 5: Add Navigation, Route, And I18n

**Files:**
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/lib/navigation.test.ts`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/router.test.tsx`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Create: `web/src/pages/workqueues/page.tsx`
- Test: `web/src/pages/workqueues/page.test.tsx`

- [ ] **Step 1: Create a temporary minimal page and failing route/navigation tests**

Create `web/src/pages/workqueues/page.tsx`:

```tsx
import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"

export function WorkqueuesPage() {
  const intl = useIntl()
  return (
    <PageContainer>
      <PageHeader
        description={intl.formatMessage({ id: "workqueues.description" })}
        title={intl.formatMessage({ id: "workqueues.title" })}
      />
    </PageContainer>
  )
}
```

Create `web/src/pages/workqueues/page.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { WorkqueuesPage } from "@/pages/workqueues/page"

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return { ...actual, getRuntimeWorkqueues: vi.fn() }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
})

test("renders the workqueue page heading", () => {
  render(
    <I18nProvider>
      <WorkqueuesPage />
    </I18nProvider>,
  )

  expect(screen.getByRole("heading", { name: "Workqueue Monitor" })).toBeInTheDocument()
})
```

Update `web/src/lib/navigation.test.ts` expectations:

```ts
expect(getActiveNavigationSection("/cluster/workqueues")?.id).toBe("cluster")
expect(pageMetadata.get("/cluster/workqueues")?.titleMessageId).toBe("nav.workqueues.title")
expect(pageMetadata.get("/cluster/workqueues")?.pathLabelMessageId).toBe("nav.path.cluster.workqueues")
expect(legacyRouteRedirects["/workqueues"]).toBe("/cluster/workqueues")
```

Update `web/src/app/router.test.tsx` legacy cases:

```ts
["/workqueues", "/cluster/workqueues"],
```

Add a route render test:

```tsx
test("renders the workqueue monitor route", async () => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/workqueues"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Workqueue Monitor" })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd web && npm test -- --run src/lib/navigation.test.ts src/app/router.test.tsx src/pages/workqueues/page.test.tsx
```

Expected: tests fail because navigation, route, and i18n keys are not wired yet.

- [ ] **Step 3: Add navigation item and route**

In `web/src/lib/navigation.ts`, import `Gauge` from `lucide-react` and add a Cluster Ops item after Tasks or before Diagnostics:

```ts
{
  href: "/cluster/workqueues",
  titleMessageId: "nav.workqueues.title",
  descriptionMessageId: "nav.workqueues.description",
  pathLabelMessageId: "nav.path.cluster.workqueues",
  icon: Gauge,
  aliases: ["/workqueues"],
},
```

Add legacy redirect:

```ts
"/workqueues": "/cluster/workqueues",
```

In `web/src/app/router.tsx`, import the page:

```tsx
import { WorkqueuesPage } from "@/pages/workqueues/page"
```

Add route:

```tsx
{ path: "cluster/workqueues", element: <WorkqueuesPage /> },
```

Add legacy redirect:

```tsx
{ path: "workqueues", element: <Navigate replace to="/cluster/workqueues" /> },
```

- [ ] **Step 4: Add i18n strings**

In `web/src/i18n/messages/en.ts`, add:

```ts
"nav.path.cluster.workqueues": "CLUSTER / WORKQUEUES",
"nav.workqueues.title": "Workqueues",
"nav.workqueues.description": "Local runtime queue and worker pressure.",
"workqueues.title": "Workqueue Monitor",
"workqueues.description": "Inspect local-node runtime queues, worker saturation, wait latency, and admission pressure.",
```

In `web/src/i18n/messages/zh-CN.ts`, add:

```ts
"nav.path.cluster.workqueues": "CLUSTER / WORKQUEUES",
"nav.workqueues.title": "Workqueue",
"nav.workqueues.description": "节点本地运行时队列与 worker 压力。",
"workqueues.title": "Workqueue 监控",
"workqueues.description": "查看当前节点的运行时队列、worker 饱和度、等待延迟与入队压力。",
```

- [ ] **Step 5: Run route/navigation tests**

Run:

```bash
cd web && npm test -- --run src/lib/navigation.test.ts src/app/router.test.tsx src/pages/workqueues/page.test.tsx
```

Expected: tests pass with the minimal page.

- [ ] **Step 6: Commit routing and i18n**

```bash
git add web/src/lib/navigation.ts web/src/lib/navigation.test.ts web/src/app/router.tsx web/src/app/router.test.tsx web/src/pages/workqueues/page.tsx web/src/pages/workqueues/page.test.tsx web/src/i18n/messages/zh-CN.ts web/src/i18n/messages/en.ts
git commit -m "feat: add workqueue monitor route"
```

## Task 6: Implement Workqueue Monitor UI

**Files:**
- Modify: `web/src/pages/workqueues/page.tsx`
- Modify: `web/src/pages/workqueues/page.test.tsx`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/i18n/messages/en.ts`

- [ ] **Step 1: Replace page test with data-driven UI tests**

Replace `web/src/pages/workqueues/page.test.tsx` with:

```tsx
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { WorkqueuesPage } from "@/pages/workqueues/page"

const getRuntimeWorkqueuesMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return { ...actual, getRuntimeWorkqueues: (...args: unknown[]) => getRuntimeWorkqueuesMock(...args) }
})

const workqueueResponse = {
  generated_at: "2026-06-17T10:00:00Z",
  window_seconds: 10,
  scope: { view: "local_node", node_id: 1, node_name: "node-1", ready: true },
  summary: {
    overall_level: "degraded",
    total: 2,
    ok: 1,
    busy: 0,
    degraded: 1,
    critical: 0,
    hottest: {
      component: "gateway",
      pool: "async_send",
      queue: "send",
      priority: "none",
      level: "degraded",
      score: 0.82,
    },
  },
  items: [
    {
      component: "gateway",
      pool: "async_send",
      queue: "send",
      priority: "none",
      level: "degraded",
      score: 0.82,
      depth: 82,
      capacity: 100,
      inflight: 0,
      workers: 0,
      wait_p99_ms: 12.4,
      task_p99_ms: 20.5,
      admission_error_per_sec: 0.3,
      hint: "queue depth is approaching capacity",
    },
    {
      component: "db",
      pool: "message_commit",
      queue: "commit",
      priority: "none",
      level: "ok",
      score: 0.2,
      depth: 2,
      capacity: 10,
      inflight: 0,
      workers: 1,
      wait_p99_ms: 1.1,
      task_p99_ms: 2.2,
      admission_error_per_sec: 0,
      hint: "",
    },
  ],
  sources: {
    collector: { available: true, sample_count: 10 },
    metrics: { enabled: false, required: false },
    notes: [],
  },
}

function renderPage() {
  return render(
    <I18nProvider>
      <WorkqueuesPage />
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getRuntimeWorkqueuesMock.mockReset()
})

test("renders summary and pressure rows", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue(workqueueResponse)

  renderPage()

  expect(await screen.findByRole("heading", { name: "Workqueue Monitor" })).toBeInTheDocument()
  expect(screen.getByText("degraded")).toBeInTheDocument()
  expect(screen.getByText("gateway")).toBeInTheDocument()
  expect(screen.getByText("async_send")).toBeInTheDocument()
  expect(screen.getByText("82 / 100")).toBeInTheDocument()
  expect(screen.getByText("12.4 ms")).toBeInTheDocument()
  expect(screen.getByText("0.30/s")).toBeInTheDocument()
})

test("filters ok rows when abnormal only is enabled", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue(workqueueResponse)

  renderPage()

  expect(await screen.findByText("message_commit")).toBeInTheDocument()
  fireEvent.click(screen.getByLabelText("Abnormal only"))
  expect(screen.queryByText("message_commit")).not.toBeInTheDocument()
  expect(screen.getByText("async_send")).toBeInTheDocument()
})

test("filters rows by component", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue(workqueueResponse)

  renderPage()

  const component = await screen.findByLabelText("Component")
  fireEvent.change(component, { target: { value: "db" } })
  expect(screen.getByText("message_commit")).toBeInTheDocument()
  expect(screen.queryByText("async_send")).not.toBeInTheDocument()
})

test("shows warming state for service unavailable responses", async () => {
  getRuntimeWorkqueuesMock.mockRejectedValue(new ManagerApiError(503, "service_unavailable", "top collector warming up"))

  renderPage()

  expect(await screen.findByRole("status")).toHaveAttribute("data-kind", "unavailable")
})

test("shows empty state when no pressure items are returned", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue({ ...workqueueResponse, summary: { ...workqueueResponse.summary, total: 0 }, items: [] })

  renderPage()

  expect(await screen.findByRole("status")).toHaveAttribute("data-kind", "empty")
})

test("refreshes with the selected window", async () => {
  getRuntimeWorkqueuesMock.mockResolvedValue(workqueueResponse)

  renderPage()

  await screen.findByText("async_send")
  fireEvent.change(screen.getByLabelText("Window"), { target: { value: "30s" } })
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }))

  await waitFor(() => {
    expect(getRuntimeWorkqueuesMock).toHaveBeenLastCalledWith({ window: "30s", limit: 100 })
  })
})
```

- [ ] **Step 2: Run page test to verify it fails**

Run:

```bash
cd web && npm test -- --run src/pages/workqueues/page.test.tsx
```

Expected: tests fail because the page still only renders the heading.

- [ ] **Step 3: Implement page state, loading, filtering, and table**

Replace `web/src/pages/workqueues/page.tsx` with:

```tsx
import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { ManagerApiError, getRuntimeWorkqueues } from "@/lib/manager-api"
import type { ManagerRuntimeWorkqueueItem, ManagerRuntimeWorkqueuesResponse } from "@/lib/manager-api.types"

type WorkqueueWindow = "10s" | "30s" | "1m"

const REFRESH_INTERVAL_MS = 5000

function formatNumber(value: number, digits = 0) {
  return Number.isFinite(value) ? value.toFixed(digits) : "-"
}

function formatMS(value: number) {
  if (!value) return "-"
  return `${formatNumber(value, 1)} ms`
}

function formatRate(value: number) {
  if (!value) return "-"
  return `${formatNumber(value, 2)}/s`
}

function levelRank(level: string) {
  if (level === "critical") return 4
  if (level === "degraded") return 3
  if (level === "busy") return 2
  if (level === "ok") return 1
  return 0
}

function sortedItems(items: ManagerRuntimeWorkqueueItem[]) {
  return [...items].sort((a, b) => {
    if (b.score !== a.score) return b.score - a.score
    if (levelRank(b.level) !== levelRank(a.level)) return levelRank(b.level) - levelRank(a.level)
    return `${a.component}/${a.pool}/${a.queue}`.localeCompare(`${b.component}/${b.pool}/${b.queue}`)
  })
}

function errorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) return "error" as const
  if (error.status === 403) return "forbidden" as const
  if (error.status === 503) return "unavailable" as const
  return "error" as const
}

function levelClass(level: string) {
  if (level === "critical") return "border-destructive/40 bg-destructive/10 text-destructive"
  if (level === "degraded") return "border-amber-500/40 bg-amber-500/10 text-amber-700"
  if (level === "busy") return "border-sky-500/40 bg-sky-500/10 text-sky-700"
  return "border-emerald-500/40 bg-emerald-500/10 text-emerald-700"
}

export function WorkqueuesPage() {
  const intl = useIntl()
  const [windowValue, setWindowValue] = useState<WorkqueueWindow>("10s")
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [abnormalOnly, setAbnormalOnly] = useState(false)
  const [component, setComponent] = useState("all")
  const [response, setResponse] = useState<ManagerRuntimeWorkqueuesResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const load = useCallback(async (refreshingLoad: boolean) => {
    setLoading((current) => refreshingLoad ? current : true)
    setRefreshing(refreshingLoad)
    setError(null)
    try {
      const next = await getRuntimeWorkqueues({ window: windowValue, limit: 100 })
      setResponse(next)
    } catch (caught) {
      setError(caught instanceof Error ? caught : new Error("runtime workqueue request failed"))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [windowValue])

  useEffect(() => {
    void load(false)
  }, [load])

  useEffect(() => {
    if (!autoRefresh) return undefined
    const timer = window.setInterval(() => {
      void load(true)
    }, REFRESH_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [autoRefresh, load])

  const components = useMemo(() => {
    const values = new Set(response?.items.map((item) => item.component) ?? [])
    return ["all", ...Array.from(values).sort()]
  }, [response])

  const visibleItems = useMemo(() => {
    const items = sortedItems(response?.items ?? [])
    return items.filter((item) => {
      if (abnormalOnly && item.level === "ok") return false
      if (component !== "all" && item.component !== component) return false
      return true
    })
  }, [abnormalOnly, component, response])

  const title = intl.formatMessage({ id: "workqueues.title" })

  return (
    <PageContainer>
      <PageHeader
        description={intl.formatMessage({ id: "workqueues.description" })}
        title={title}
      />

      <div className="space-y-5">
        <section className="rounded-xl border border-border bg-card p-4">
          <div className="grid gap-3 md:grid-cols-[160px_180px_1fr_auto_auto] md:items-end">
            <label className="grid gap-1 text-sm font-medium text-foreground">
              {intl.formatMessage({ id: "workqueues.controls.window" })}
              <select
                aria-label={intl.formatMessage({ id: "workqueues.controls.window" })}
                className="h-9 rounded-lg border border-border bg-background px-3 text-sm"
                onChange={(event) => setWindowValue(event.currentTarget.value as WorkqueueWindow)}
                value={windowValue}
              >
                <option value="10s">10s</option>
                <option value="30s">30s</option>
                <option value="1m">1m</option>
              </select>
            </label>
            <label className="grid gap-1 text-sm font-medium text-foreground">
              {intl.formatMessage({ id: "workqueues.controls.component" })}
              <select
                aria-label={intl.formatMessage({ id: "workqueues.controls.component" })}
                className="h-9 rounded-lg border border-border bg-background px-3 text-sm"
                onChange={(event) => setComponent(event.currentTarget.value)}
                value={component}
              >
                {components.map((value) => (
                  <option key={value} value={value}>
                    {value === "all" ? intl.formatMessage({ id: "workqueues.controls.allComponents" }) : value}
                  </option>
                ))}
              </select>
            </label>
            <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
              {response ? (
                <>
                  <span className="rounded-md border border-border bg-background px-3 py-2">
                    {intl.formatMessage({ id: "workqueues.scope.node" }, { id: response.scope.node_id, name: response.scope.node_name || `node-${response.scope.node_id}` })}
                  </span>
                  <span className="rounded-md border border-border bg-background px-3 py-2">
                    {intl.formatMessage({ id: "workqueues.scope.samples" }, { count: response.sources.collector.sample_count ?? 0 })}
                  </span>
                </>
              ) : null}
            </div>
            <Button onClick={() => { void load(true) }} size="sm" variant="outline">
              {refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
            </Button>
            <div className="flex flex-wrap gap-2">
              <label className="flex h-9 items-center gap-2 rounded-lg border border-border bg-background px-3 text-sm">
                <input checked={autoRefresh} onChange={() => setAutoRefresh((value) => !value)} type="checkbox" />
                {intl.formatMessage({ id: "workqueues.controls.autoRefresh" })}
              </label>
              <label className="flex h-9 items-center gap-2 rounded-lg border border-border bg-background px-3 text-sm">
                <input checked={abnormalOnly} onChange={() => setAbnormalOnly((value) => !value)} type="checkbox" />
                {intl.formatMessage({ id: "workqueues.controls.abnormalOnly" })}
              </label>
            </div>
          </div>
        </section>

        {loading ? (
          <ResourceState kind="loading" title={title} />
        ) : error ? (
          <ResourceState kind={errorKind(error)} onRetry={() => { void load(false) }} title={title} />
        ) : response && response.items.length === 0 ? (
          <ResourceState kind="empty" title={title} description={intl.formatMessage({ id: "workqueues.empty" })} />
        ) : response ? (
          <>
            <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-5">
              {[
                [intl.formatMessage({ id: "workqueues.summary.level" }), response.summary.overall_level],
                [intl.formatMessage({ id: "workqueues.summary.total" }), response.summary.total],
                [intl.formatMessage({ id: "workqueues.summary.degraded" }), response.summary.degraded + response.summary.critical],
                [intl.formatMessage({ id: "workqueues.summary.hottest" }), response.summary.hottest ? `${response.summary.hottest.component}/${response.summary.hottest.pool}` : "-"],
                [intl.formatMessage({ id: "workqueues.summary.window" }), `${response.window_seconds}s`],
              ].map(([label, value]) => (
                <div className="rounded-xl border border-border bg-card p-4" key={String(label)}>
                  <div className="text-xs font-semibold uppercase text-muted-foreground">{label}</div>
                  <div className="mt-2 text-xl font-semibold text-foreground">{value}</div>
                </div>
              ))}
            </section>

            {visibleItems.length === 0 ? (
              <ResourceState kind="empty" title={title} description={intl.formatMessage({ id: "workqueues.filteredEmpty" })} />
            ) : (
              <div className="overflow-x-auto rounded-xl border border-border bg-card">
                <table className="min-w-[1120px] w-full text-left text-sm">
                  <thead className="border-b border-border text-xs uppercase text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.level" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.component" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.pool" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.queue" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.depth" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.inflight" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.score" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.wait" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.task" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.admission" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "workqueues.table.hint" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleItems.map((item) => (
                      <tr className="border-b border-border/70 last:border-0" key={`${item.component}-${item.pool}-${item.queue}-${item.priority}`}>
                        <td className="px-3 py-3">
                          <span className={`rounded-full border px-2 py-1 text-xs font-semibold ${levelClass(item.level)}`}>{item.level}</span>
                        </td>
                        <td className="px-3 py-3 font-medium text-foreground">{item.component}</td>
                        <td className="px-3 py-3">{item.pool}</td>
                        <td className="px-3 py-3">{item.queue}</td>
                        <td className="px-3 py-3">{item.depth} / {item.capacity}</td>
                        <td className="px-3 py-3">{item.inflight} / {item.workers}</td>
                        <td className="px-3 py-3">{formatNumber(item.score * 100, 0)}%</td>
                        <td className="px-3 py-3">{formatMS(item.wait_p99_ms)}</td>
                        <td className="px-3 py-3">{formatMS(item.task_p99_ms)}</td>
                        <td className="px-3 py-3">{formatRate(item.admission_error_per_sec)}</td>
                        <td className="max-w-64 px-3 py-3 text-muted-foreground">{item.hint || "-"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        ) : null}
      </div>
    </PageContainer>
  )
}
```

- [ ] **Step 4: Add UI i18n strings**

In `web/src/i18n/messages/en.ts`, add:

```ts
"workqueues.controls.window": "Window",
"workqueues.controls.component": "Component",
"workqueues.controls.allComponents": "All components",
"workqueues.controls.autoRefresh": "Auto refresh",
"workqueues.controls.abnormalOnly": "Abnormal only",
"workqueues.scope.node": "{name} · node {id}",
"workqueues.scope.samples": "{count} samples",
"workqueues.summary.level": "Overall",
"workqueues.summary.total": "Items",
"workqueues.summary.degraded": "Degraded/Critical",
"workqueues.summary.hottest": "Hottest",
"workqueues.summary.window": "Window",
"workqueues.table.level": "Level",
"workqueues.table.component": "Component",
"workqueues.table.pool": "Pool",
"workqueues.table.queue": "Queue",
"workqueues.table.depth": "Depth / Capacity",
"workqueues.table.inflight": "Inflight / Workers",
"workqueues.table.score": "Score",
"workqueues.table.wait": "Wait P99",
"workqueues.table.task": "Task P99",
"workqueues.table.admission": "Admission errors",
"workqueues.table.hint": "Hint",
"workqueues.empty": "Current node has no runtime queue pressure samples.",
"workqueues.filteredEmpty": "No runtime queues match the current filters.",
```

In `web/src/i18n/messages/zh-CN.ts`, add:

```ts
"workqueues.controls.window": "窗口",
"workqueues.controls.component": "组件",
"workqueues.controls.allComponents": "全部组件",
"workqueues.controls.autoRefresh": "自动刷新",
"workqueues.controls.abnormalOnly": "只看异常",
"workqueues.scope.node": "{name} · 节点 {id}",
"workqueues.scope.samples": "{count} 个样本",
"workqueues.summary.level": "整体",
"workqueues.summary.total": "条目",
"workqueues.summary.degraded": "降级/严重",
"workqueues.summary.hottest": "最高压力",
"workqueues.summary.window": "窗口",
"workqueues.table.level": "级别",
"workqueues.table.component": "组件",
"workqueues.table.pool": "Pool",
"workqueues.table.queue": "队列",
"workqueues.table.depth": "深度 / 容量",
"workqueues.table.inflight": "执行中 / Workers",
"workqueues.table.score": "分数",
"workqueues.table.wait": "等待 P99",
"workqueues.table.task": "任务 P99",
"workqueues.table.admission": "入队错误",
"workqueues.table.hint": "提示",
"workqueues.empty": "当前节点没有运行时队列压力样本。",
"workqueues.filteredEmpty": "当前筛选条件下没有运行时队列。",
```

- [ ] **Step 5: Run page tests**

Run:

```bash
cd web && npm test -- --run src/pages/workqueues/page.test.tsx
```

Expected: workqueue page tests pass.

- [ ] **Step 6: Commit page implementation**

```bash
git add web/src/pages/workqueues/page.tsx web/src/pages/workqueues/page.test.tsx web/src/i18n/messages/zh-CN.ts web/src/i18n/messages/en.ts
git commit -m "feat: render workqueue monitor"
```

## Task 7: Final Verification

**Files:**
- Verify all touched files
- Do not touch unrelated dirty `pkg/gateway` files

- [ ] **Step 1: Run backend verification**

Run:

```bash
go test ./internalv2/app ./internalv2/access/manager
```

Expected: both packages pass.

- [ ] **Step 2: Run frontend verification**

Run:

```bash
cd web && npm test -- --run src/lib/manager-api.test.ts src/lib/navigation.test.ts src/app/router.test.tsx src/pages/workqueues/page.test.tsx
```

Expected: all listed Vitest suites pass.

- [ ] **Step 3: Inspect final git state**

Run:

```bash
git status --short
```

Expected: only unrelated pre-existing files remain dirty. The Workqueue monitor implementation should be committed in the task commits.

- [ ] **Step 4: Summarize implementation**

Prepare a concise final note listing:

- backend manager endpoint added;
- top collector pressure diagnostics added;
- Web Workqueue page added under Cluster Ops;
- tests run and results;
- any unrelated dirty files left untouched.
