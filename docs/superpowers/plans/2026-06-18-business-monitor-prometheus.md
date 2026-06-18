# Business Monitor Prometheus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the business realtime monitor API and UI data path on Prometheus query APIs, with clear frontend guidance when Prometheus is disabled or unavailable.

**Architecture:** Add a dedicated manager monitor provider that queries Prometheus via PromQL and returns the card-wall response shape. The manager HTTP layer owns route parsing, auth, and response mapping; the web layer owns display metadata and maps stable metric keys to localized card UI.

**Tech Stack:** Go `net/http`, `encoding/json`, Gin manager routes, internalv2 app wiring, Prometheus HTTP API `/api/v1/query` and `/api/v1/query_range`, React, TypeScript, Vitest, Testing Library.

---

## File Structure

- Create `internalv2/access/manager/monitor.go`: route DTOs, query parsing, handler, and disabled/unavailable response mapping.
- Modify `internalv2/access/manager/server.go`: add a `Monitor` provider option and register `GET /manager/monitor/realtime` under `cluster.node:r`.
- Modify `internalv2/access/manager/server_test.go`: add route tests using a fake monitor provider.
- Create `internalv2/app/manager_monitor_prometheus.go`: Prometheus HTTP client, PromQL metric definitions, series/stat assembly, and provider implementation.
- Modify `internalv2/app/config.go`: expose enough effective Prometheus config to build the provider; no new config key is required.
- Modify `internalv2/app/wiring.go`: wire the Prometheus monitor provider into manager options when manager is configured.
- Modify `internalv2/app/app_test.go`: add wiring tests for enabled and disabled Prometheus monitor states.
- Modify `internalv2/access/manager/FLOW.md` and `internalv2/app/FLOW.md`: document the new manager realtime monitor flow.
- Modify `web/src/lib/manager-api.types.ts`: replace old monitor response types with the realtime card response types.
- Modify `web/src/lib/manager-api.ts`: replace `getMonitorMetrics` with `getRealtimeMonitor`.
- Modify `web/src/lib/manager-api.test.ts`: update client path tests.
- Create `web/src/pages/monitor/metric-config.ts`: frontend-only title, stage label, stat label, color, and precision mapping for stable metric keys.
- Modify `web/src/pages/monitor/types.ts`: align page model with API data plus presentation mapping.
- Modify `web/src/pages/monitor/page.tsx`: call the realtime API, render loading/ready/partial/disabled/unavailable states, and remove production preview fallback.
- Modify `web/src/pages/monitor/components/*`: accept API-backed numeric values and disabled/partial source warnings.
- Modify `web/src/pages/monitor/page.test.tsx`: cover ready, disabled, partial, and time-range reload behavior.
- Modify `web/src/pages/monitor/preview-data.ts` and `preview-data.test.ts`: keep only test-fixture data or remove if unused.

## Task 1: Manager Route Contract

**Files:**
- Create: `internalv2/access/manager/monitor.go`
- Modify: `internalv2/access/manager/server.go`
- Test: `internalv2/access/manager/server_test.go`

- [ ] **Step 1: Write the failing route test**

Add tests near the existing runtime workqueue tests:

```go
func TestManagerRealtimeMonitorReturnsPrometheusPayload(t *testing.T) {
	generatedAt := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	provider := &managerMonitorStub{response: RealtimeMonitorResponse{
		Status:        "ready",
		GeneratedAt:   generatedAt,
		WindowSeconds: 900,
		StepSeconds:   20,
		Scope: RealtimeMonitorScope{
			View:     "prometheus",
			NodeID:   1,
			NodeName: "node-1",
		},
		Sources: RealtimeMonitorSources{
			Prometheus: RealtimeMonitorPrometheusSource{Enabled: true, BaseURL: "http://127.0.0.1:9090", QueryMS: 18},
		},
		Cards: []RealtimeMonitorCard{{
			Key:       "sendRate",
			Stage:     "sendEntry",
			Tone:      "normal",
			Unit:      "msg/s",
			Value:     12.5,
			Available: true,
			Series: []RealtimeMonitorPoint{{Timestamp: 1781767200000, Value: 12.5}},
			Stats:  []RealtimeMonitorStat{{Key: "avg", Value: 12.5}},
		}},
	}}
	srv := New(Options{Monitor: provider})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/monitor/realtime?window=15m&step=20s", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.Window != 15*time.Minute || provider.query.Step != 20*time.Second {
		t.Fatalf("query = %#v, want 15m/20s", provider.query)
	}
	if !jsonEqual(rec.Body.String(), `{
		"status":"ready",
		"generated_at":"2026-06-18T10:00:00Z",
		"window_seconds":900,
		"step_seconds":20,
		"scope":{"view":"prometheus","node_id":1,"node_name":"node-1"},
		"sources":{"prometheus":{"enabled":true,"base_url":"http://127.0.0.1:9090","query_ms":18,"error":""}},
		"snapshot":null,
		"cards":[{"key":"sendRate","stage":"sendEntry","tone":"normal","unit":"msg/s","value":12.5,"series":[{"timestamp":1781767200000,"value":12.5}],"stats":[{"key":"avg","value":12.5}],"available":true,"error":""}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
go test ./internalv2/access/manager -run TestManagerRealtimeMonitorReturnsPrometheusPayload -count=1
```

Expected: fail because `Options.Monitor`, `RealtimeMonitorResponse`, and the route do not exist.

- [ ] **Step 3: Implement the minimal route contract**

Add the provider interface and DTOs in `monitor.go`:

```go
type RealtimeMonitorProvider interface {
	RealtimeMonitor(context.Context, RealtimeMonitorQuery) (RealtimeMonitorResponse, error)
}

type RealtimeMonitorQuery struct {
	Window time.Duration
	Step   time.Duration
}

type RealtimeMonitorResponse struct {
	Status        string                  `json:"status"`
	GeneratedAt   time.Time               `json:"generated_at"`
	WindowSeconds int                     `json:"window_seconds"`
	StepSeconds   int                     `json:"step_seconds"`
	Scope         RealtimeMonitorScope    `json:"scope"`
	Sources       RealtimeMonitorSources  `json:"sources"`
	Snapshot      []RealtimeMonitorSnapshotEntry `json:"snapshot"`
	Cards         []RealtimeMonitorCard   `json:"cards"`
}
```

In `server.go`, add `Monitor RealtimeMonitorProvider` to `Options`, store it on `Server`, and register:

```go
monitor := s.engine.Group("/manager")
if s.auth.enabled() {
	monitor.Use(s.requirePermission("cluster.node", "r"))
}
monitor.GET("/monitor/realtime", s.handleRealtimeMonitor)
```

- [ ] **Step 4: Run the route test and verify GREEN**

Run:

```bash
go test ./internalv2/access/manager -run 'TestManagerRealtimeMonitor(ReturnsPrometheusPayload|RejectsInvalidQuery|RequiresNodeReadPermission)' -count=1
```

Expected: route tests pass after adding invalid query and permission tests.

## Task 2: Prometheus Query Provider

**Files:**
- Create: `internalv2/app/manager_monitor_prometheus.go`
- Test: `internalv2/app/manager_monitor_prometheus_test.go`

- [ ] **Step 1: Write failing provider tests**

Create tests with `httptest.Server` that serves Prometheus API responses:

```go
func TestManagerMonitorPrometheusProviderMapsQueryRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("path = %s, want /api/v1/query_range", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()

	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		NodeID:  1,
		NodeName: "node-1",
		Client: server.Client(),
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{Window: 15 * time.Minute, Step: 20 * time.Second})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != "ready" {
		t.Fatalf("Status = %q, want ready", resp.Status)
	}
	if len(resp.Cards) == 0 || resp.Cards[0].Series[0].Timestamp != 1781767200000 {
		t.Fatalf("cards = %#v, want mapped timestamp series", resp.Cards)
	}
}
```

Add disabled and unavailable tests:

```go
func TestManagerMonitorPrometheusProviderReturnsDisabledWhenNotEnabled(t *testing.T) {
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{Enabled: false})
	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{Window: 15 * time.Minute, Step: 20 * time.Second})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != "prometheus_disabled" || resp.Sources.Prometheus.Enabled {
		t.Fatalf("response = %#v, want disabled source", resp)
	}
}
```

- [ ] **Step 2: Run provider tests and verify RED**

Run:

```bash
go test ./internalv2/app -run TestManagerMonitorPrometheusProvider -count=1
```

Expected: fail because provider does not exist.

- [ ] **Step 3: Implement the Prometheus client and card definitions**

Implement a focused provider with:

```go
type managerPrometheusMonitorOptions struct {
	Enabled  bool
	BaseURL  string
	NodeID   uint64
	NodeName string
	Client   *http.Client
	Now      func() time.Time
}
```

Use one metric definition slice:

```go
type monitorMetricDefinition struct {
	Key      string
	Stage    string
	Tone     string
	Unit     string
	Query    func(rateWindow string) string
	Stats    []string
	Optional bool
}
```

Each card uses `/api/v1/query_range?query=...&start=...&end=...&step=...`. Parse Prometheus matrix values into millisecond timestamps and float values. Compute `value`, `avg`, `peak`, and `total` locally from returned series where applicable.

- [ ] **Step 4: Run provider tests and verify GREEN**

Run:

```bash
go test ./internalv2/app -run TestManagerMonitorPrometheusProvider -count=1
```

Expected: provider tests pass.

## Task 3: App Wiring and Config Surface

**Files:**
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/config.go`
- Test: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing wiring tests**

Add one test that builds an app with manager and Prometheus enabled:

```go
func TestManagerServerReceivesPrometheusMonitorProviderWhenConfigured(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}
	app, err := New(Config{
		Manager: ManagerConfig{ListenAddr: "127.0.0.1:0"},
		API: APIConfig{ListenAddr: "127.0.0.1:0"},
		Observability: ObservabilityConfig{
			MetricsEnabled: true,
			Prometheus: PrometheusConfig{Enabled: true, ListenAddr: "127.0.0.1:9090"},
		},
	}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	srv, ok := app.manager.(*accessmanager.Server)
	if !ok {
		t.Fatalf("manager = %T, want *accessmanager.Server", app.manager)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/monitor/realtime", nil)
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want OK disabled/unavailable monitor payload; body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run wiring test and verify RED**

Run:

```bash
go test ./internalv2/app -run TestManagerServerReceivesPrometheusMonitorProviderWhenConfigured -count=1
```

Expected: fail because manager has no monitor provider wiring.

- [ ] **Step 3: Wire provider into manager options**

Add an app helper:

```go
func (a *App) newManagerMonitorProvider() accessmanager.RealtimeMonitorProvider {
	return newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled:  a.cfg.Observability.MetricsEnabled && a.cfg.Observability.Prometheus.Enabled,
		BaseURL:  "http://" + strings.TrimSpace(a.cfg.Observability.Prometheus.ListenAddr),
		NodeID:   a.cfg.NodeID,
		NodeName: fmt.Sprintf("node-%d", a.cfg.NodeID),
	})
}
```

Pass it in `wireManager`:

```go
Monitor: a.newManagerMonitorProvider(),
```

- [ ] **Step 4: Run wiring test and verify GREEN**

Run:

```bash
go test ./internalv2/app -run TestManagerServerReceivesPrometheusMonitorProviderWhenConfigured -count=1
```

Expected: wiring test passes.

## Task 4: Frontend API Types and Client

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client test**

Replace the old monitor helper import with `getRealtimeMonitor` and add:

```ts
it("builds realtime monitor query parameters", async () => {
  fetchMock.mockResolvedValueOnce(jsonResponse({
    status: "prometheus_disabled",
    generated_at: "2026-06-18T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "prometheus" },
    sources: { prometheus: { enabled: false, base_url: "", query_ms: 0, error: "prometheus is disabled" } },
    snapshot: [],
    cards: [],
  }))

  await getRealtimeMonitor({ window: "15m", step: "20s" })

  expect(fetchMock).toHaveBeenCalledWith(
    expect.stringContaining("/manager/monitor/realtime?window=15m&step=20s"),
    expect.any(Object),
  )
})
```

- [ ] **Step 2: Run API client test and verify RED**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts -t "realtime monitor"
```

Expected: fail because `getRealtimeMonitor` does not exist.

- [ ] **Step 3: Implement types and client helper**

Add types:

```ts
export type RealtimeMonitorStatus = "ready" | "partial" | "prometheus_disabled" | "prometheus_unavailable"
export type RealtimeMonitorResponse = {
  status: RealtimeMonitorStatus
  generated_at: string
  window_seconds: number
  step_seconds: number
  scope: { view: "prometheus"; node_id?: number; node_name?: string }
  sources: { prometheus: { enabled: boolean; base_url: string; query_ms: number; error: string } }
  snapshot: RealtimeMonitorSnapshotEntry[]
  cards: RealtimeMonitorCard[]
}
```

Add helper:

```ts
export function getRealtimeMonitor(params?: { window?: string; step?: string }) {
  const search = new URLSearchParams()
  if (params?.window) search.set("window", params.window)
  if (params?.step) search.set("step", params.step)
  const query = search.toString()
  return jsonManagerFetch<RealtimeMonitorResponse>(`/manager/monitor/realtime${query ? `?${query}` : ""}`)
}
```

- [ ] **Step 4: Run API client test and verify GREEN**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts -t "realtime monitor"
```

Expected: API client test passes.

## Task 5: Frontend Monitor Page Integration

**Files:**
- Create: `web/src/pages/monitor/metric-config.ts`
- Modify: `web/src/pages/monitor/types.ts`
- Modify: `web/src/pages/monitor/page.tsx`
- Modify: `web/src/pages/monitor/components/monitor-toolbar.tsx`
- Modify: `web/src/pages/monitor/components/monitor-metric-card.tsx`
- Modify: `web/src/pages/monitor/components/monitor-snapshot-strip.tsx`
- Modify: `web/src/pages/monitor/page.test.tsx`

- [ ] **Step 1: Write failing page tests**

Update `page.test.tsx` to mock the manager API:

```ts
vi.mock("@/lib/manager-api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/manager-api")>("@/lib/manager-api")
  return {
    ...actual,
    getRealtimeMonitor: vi.fn(),
  }
})
```

Add ready and disabled tests:

```ts
test("renders realtime monitor cards from prometheus data", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(readyMonitorResponse())
  renderMonitorPage()
  expect(await screen.findByText("Send Rate")).toBeInTheDocument()
  expect(screen.getByText("12.5")).toBeInTheDocument()
})

test("shows prometheus setup guidance when monitor is disabled", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(disabledMonitorResponse())
  renderMonitorPage()
  expect(await screen.findByText(/Prometheus monitoring is not enabled/i)).toBeInTheDocument()
  expect(screen.getByText("WK_METRICS_ENABLE=true")).toBeInTheDocument()
  expect(screen.getByText("WK_PROMETHEUS_ENABLE=true")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run page tests and verify RED**

Run:

```bash
cd web && bun run test -- src/pages/monitor/page.test.tsx
```

Expected: fail because page still uses preview data.

- [ ] **Step 3: Implement API-backed page model**

Create `metric-config.ts` with display-only metadata:

```ts
export const monitorMetricConfig = {
  sendRate: { titleId: "monitor.metrics.sendRate", chartColor: "#2563eb", precision: 1 },
  sendSuccessRate: { titleId: "monitor.metrics.sendSuccessRate", chartColor: "#16a34a", precision: 2 },
  entryLatencyP99: { titleId: "monitor.metrics.entryLatencyP99", chartColor: "#d97706", precision: 1 },
} as const
```

In `page.tsx`, fetch on mount and when range changes:

```ts
useEffect(() => {
  let cancelled = false
  setState({ status: "loading" })
  getRealtimeMonitor({ window: timeRange }).then((response) => {
    if (!cancelled) setState({ status: "ready", response })
  }).catch((error) => {
    if (!cancelled) setState({ status: "error", error })
  })
  return () => {
    cancelled = true
  }
}, [timeRange])
```

Map API cards to UI cards with config-owned labels and colors. Render disabled/unavailable status as an empty state with the setup commands.

- [ ] **Step 4: Run page tests and verify GREEN**

Run:

```bash
cd web && bun run test -- src/pages/monitor/page.test.tsx
```

Expected: monitor page tests pass.

## Task 6: Docs and Flow Updates

**Files:**
- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Write doc update**

Add to `internalv2/access/manager/FLOW.md`:

```text
GET /manager/monitor/realtime (Prometheus-backed business realtime monitor cards; requires cluster.node:r when Auth.On=true)
```

Add to `internalv2/app/FLOW.md` under Prometheus wiring:

```text
Manager realtime monitor queries use the configured Prometheus HTTP API. They do not use topCollector or in-process dashboard ring buffers.
```

- [ ] **Step 2: Review doc consistency**

Run:

```bash
rg -n "monitor/realtime|topCollector|DashboardCollector|Prometheus" internalv2/access/manager/FLOW.md internalv2/app/FLOW.md
```

Expected: monitor route is documented, and docs do not claim business monitor uses `topCollector`.

## Task 7: Verification

**Files:**
- No new files.

- [ ] **Step 1: Run focused backend tests**

Run:

```bash
go test ./internalv2/access/manager ./internalv2/app -run 'RealtimeMonitor|PrometheusMonitor|ManagerServerReceivesPrometheusMonitor' -count=1
```

Expected: pass.

- [ ] **Step 2: Run focused frontend tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/monitor/page.test.tsx
```

Expected: pass.

- [ ] **Step 3: Run typecheck**

Run:

```bash
cd web && bunx tsc -b
```

Expected: pass.

- [ ] **Step 4: Run package-level backend tests touched by routing and wiring**

Run:

```bash
go test ./internalv2/access/manager ./internalv2/app -count=1
```

Expected: pass.

- [ ] **Step 5: Inspect worktree**

Run:

```bash
git status --short
```

Expected: only feature files and pre-existing unrelated dirty files are present. Do not revert unrelated user changes.
