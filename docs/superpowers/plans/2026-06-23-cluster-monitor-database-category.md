# Cluster Monitor Database Category Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a tested Database category to the cluster operations realtime monitor, backed only by `internalv2` storage commit and DB runtime pressure metrics.

**Architecture:** Extend the existing `/manager/realtime-monitor?category=...` contract with a `database` category instead of adding a new endpoint or page. The backend continues to assemble Prometheus-backed cards from `internalv2/app`, with `job="wukongimv2"` injected by the existing selector filter. The web page keeps the current card-wall UI and adds Database as one selectable category; `storageWriteP99` moves from Node to Database so DB pressure is not hidden inside process-level node metrics.

**Tech Stack:** Go, Gin manager API, Prometheus PromQL, React, React Intl, Vitest, Testing Library, Bun.

---

## Scope Rules

- Do not modify `internal/`; it is the old implementation path for this feature.
- Do not add a disk-usage card in this first pass. `wukongim_storage_disk_usage_bytes` exists in `pkg/metrics`, but `internalv2/app` currently wires message DB commit observers, not an internalv2 disk-usage sampler.
- Keep metric cardinality low. Use existing labels: `store`, `lane`, `result`, `stage`, `component`, `pool`, `queue`, and `priority`.
- Preserve existing `node_id` filtering and `job="wukongimv2"` injection by routing all new PromQL through the existing realtime monitor provider.
- Before editing files, run `git status --short` and preserve unrelated local changes.

## File Structure

- Modify `internalv2/access/manager/monitor.go`: add the `database` realtime monitor category and query validation.
- Modify `internalv2/access/manager/server_test.go`: test that `category=database` reaches the provider.
- Modify `internalv2/access/manager/FLOW.md`: document the new Database category on `/manager/realtime-monitor`.
- Modify `internalv2/app/manager_cluster_monitor_prometheus.go`: add Database card definitions and category counts; move `storageWriteP99` from Node to Database.
- Modify `internalv2/app/manager_cluster_monitor_prometheus_test.go`: verify Database cards and job-scoped PromQL.
- Modify `internalv2/app/FLOW.md`: document Database cards as internalv2 Prometheus-backed realtime monitor metrics.
- Modify `web/src/lib/manager-api.types.ts`: add `"database"` to `RealtimeMonitorCategory`.
- Modify `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`: add Database to the category selector.
- Modify `web/src/pages/cluster-monitor/types.ts`: add Database metric keys.
- Modify `web/src/pages/cluster-monitor/metric-config.ts`: add Database metric card configs and stat label reuse.
- Modify `web/src/pages/cluster-monitor/page.test.tsx`: verify the Database selector and Database cards render.
- Modify `web/src/i18n/messages/en.ts`: English Database category and card copy.
- Modify `web/src/i18n/messages/zh-CN.ts`: Chinese Database category and card copy.

## Task 1: Backend Category Contract

**Files:**

- Modify: `internalv2/access/manager/server_test.go`
- Modify: `internalv2/access/manager/monitor.go`

- [ ] **Step 1: Write the failing manager API category test**

Add this test near the existing realtime monitor query parsing tests in `internalv2/access/manager/server_test.go`:

```go
func TestManagerRealtimeMonitorParsesDatabaseCategory(t *testing.T) {
	provider := &managerMonitorStub{response: RealtimeMonitorResponse{
		Status:        RealtimeMonitorStatusReady,
		GeneratedAt:   time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC),
		WindowSeconds: 900,
		StepSeconds:   20,
		Scope:         RealtimeMonitorScope{View: RealtimeMonitorScopeUnified},
		Snapshot:      []RealtimeMonitorSnapshotEntry{},
		Cards:         []RealtimeMonitorCard{},
	}}
	srv := New(Options{RealtimeMonitor: provider})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/realtime-monitor?window=15m&category=database", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if provider.query.Category != RealtimeMonitorCategoryDatabase {
		t.Fatalf("provider query category = %q, want %q", provider.query.Category, RealtimeMonitorCategoryDatabase)
	}
}
```

- [ ] **Step 2: Run the category contract test to verify RED**

Run:

```bash
go test ./internalv2/access/manager -run TestManagerRealtimeMonitorParsesDatabaseCategory -count=1
```

Expected: fail because `RealtimeMonitorCategoryDatabase` is not defined and `category=database` is not accepted.

- [ ] **Step 3: Add the Database category constant and validation**

In `internalv2/access/manager/monitor.go`, add the constant after `RealtimeMonitorCategorySlot` and before `RealtimeMonitorCategoryNode`:

```go
// RealtimeMonitorCategoryDatabase groups internalv2 database commit and storage pressure cards.
RealtimeMonitorCategoryDatabase = "database"
```

Update `isValidRealtimeMonitorCategory` so the switch includes Database:

```go
case RealtimeMonitorCategoryCommon,
	RealtimeMonitorCategoryGateway,
	RealtimeMonitorCategoryInternal,
	RealtimeMonitorCategoryMessage,
	RealtimeMonitorCategoryConversation,
	RealtimeMonitorCategoryChannel,
	RealtimeMonitorCategoryControl,
	RealtimeMonitorCategorySlot,
	RealtimeMonitorCategoryDatabase,
	RealtimeMonitorCategoryNode:
	return true
```

- [ ] **Step 4: Run the category contract test to verify GREEN**

Run:

```bash
go test ./internalv2/access/manager -run TestManagerRealtimeMonitorParsesDatabaseCategory -count=1
```

Expected: pass.

## Task 2: Backend Database Card Provider

**Files:**

- Modify: `internalv2/app/manager_cluster_monitor_prometheus_test.go`
- Modify: `internalv2/app/manager_cluster_monitor_prometheus.go`

- [ ] **Step 1: Write the failing Database provider test**

Add this test near the other category-specific provider tests in `internalv2/app/manager_cluster_monitor_prometheus_test.go`:

```go
func TestManagerClusterMonitorProviderReturnsDatabaseOperatorCards(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
		writePrometheusRangeForTest(w, "7")
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: managerClusterControlReaderFake{},
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryDatabase,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	wantKeys := []string{
		"storageWriteP99",
		"storageCommitErrorRate",
		"storageCommitQueueUsage",
		"storagePhysicalCommitP99",
		"storageCommitBatchRecordsP95",
		"storageCommitBatchBytesP95",
	}
	requireMonitorCardKeysForTest(t, resp.Cards, wantKeys)

	var databaseCount int
	for _, category := range resp.Categories {
		if category.Key == accessmanager.RealtimeMonitorCategoryDatabase {
			databaseCount = category.Count
			break
		}
	}
	if databaseCount != len(wantKeys) {
		t.Fatalf("database category count = %d, want %d; categories=%#v", databaseCount, len(wantKeys), resp.Categories)
	}

	joinedQueries := strings.Join(queries, "\n")
	for _, want := range []string{
		`wukongim_storage_commit_request_duration_seconds_bucket{job="wukongimv2"}[1m]`,
		`wukongim_storage_commit_request_duration_seconds_count{job="wukongimv2",result!="ok"}[1m]`,
		`wukongim_runtime_pool_queue_depth{job="wukongimv2",component="db",pool="message_commit",queue="commit",priority="none"}`,
		`wukongim_runtime_pool_queue_capacity{job="wukongimv2",component="db",pool="message_commit",queue="commit",priority="none"}`,
		`wukongim_storage_commit_batch_duration_seconds_bucket{job="wukongimv2",stage="commit"}[1m]`,
		`wukongim_storage_commit_batch_records_bucket{job="wukongimv2"}[1m]`,
		`wukongim_storage_commit_batch_bytes_bucket{job="wukongimv2"}[1m]`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing %q: %s", want, joinedQueries)
		}
	}
}
```

- [ ] **Step 2: Run the Database provider test to verify RED**

Run:

```bash
go test ./internalv2/app -run TestManagerClusterMonitorProviderReturnsDatabaseOperatorCards -count=1
```

Expected: fail because the Database category and card definitions are not wired in the provider.

- [ ] **Step 3: Add Database metric definitions**

In `internalv2/app/manager_cluster_monitor_prometheus.go`, update `managerClusterMonitorMetricDefinitions()`:

1. Remove the existing Node-category `storageWriteP99` definition.
2. Add these Database definitions after the Slot metrics or after the Internal metrics, keeping related DB cards adjacent:

```go
clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageWriteP99", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_storage_commit_request_duration_seconds_bucket[%s])) by (le)) * 1000"),
clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageCommitErrorRate", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneCritical, "%", prometheusZeroFallback("(sum(rate(wukongim_storage_commit_request_duration_seconds_count{result!=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_storage_commit_request_duration_seconds_count[%s])), 1)) * 100")),
clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageCommitQueueUsage", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("(sum(wukongim_runtime_pool_queue_depth{component=\"db\",pool=\"message_commit\",queue=\"commit\",priority=\"none\"}) / clamp_min(sum(wukongim_runtime_pool_queue_capacity{component=\"db\",pool=\"message_commit\",queue=\"commit\",priority=\"none\"}), 1)) * 100")),
clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storagePhysicalCommitP99", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_storage_commit_batch_duration_seconds_bucket{stage=\"commit\"}[%s])) by (le)) * 1000"),
clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageCommitBatchRecordsP95", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneNormal, "records", "histogram_quantile(0.95, sum(rate(wukongim_storage_commit_batch_records_bucket[%s])) by (le))"),
clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageCommitBatchBytesP95", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneNormal, "B", "histogram_quantile(0.95, sum(rate(wukongim_storage_commit_batch_bytes_bucket[%s])) by (le))"),
```

Update `realtimeMonitorCategories()` so Database appears before Node:

```go
return []accessmanager.RealtimeMonitorCategory{
	{Key: accessmanager.RealtimeMonitorCategoryCommon, Count: common},
	{Key: accessmanager.RealtimeMonitorCategoryGateway, Count: counts[accessmanager.RealtimeMonitorCategoryGateway]},
	{Key: accessmanager.RealtimeMonitorCategoryInternal, Count: counts[accessmanager.RealtimeMonitorCategoryInternal]},
	{Key: accessmanager.RealtimeMonitorCategoryMessage, Count: counts[accessmanager.RealtimeMonitorCategoryMessage]},
	{Key: accessmanager.RealtimeMonitorCategoryConversation, Count: counts[accessmanager.RealtimeMonitorCategoryConversation]},
	{Key: accessmanager.RealtimeMonitorCategoryChannel, Count: counts[accessmanager.RealtimeMonitorCategoryChannel]},
	{Key: accessmanager.RealtimeMonitorCategoryControl, Count: counts[accessmanager.RealtimeMonitorCategoryControl]},
	{Key: accessmanager.RealtimeMonitorCategorySlot, Count: counts[accessmanager.RealtimeMonitorCategorySlot]},
	{Key: accessmanager.RealtimeMonitorCategoryDatabase, Count: counts[accessmanager.RealtimeMonitorCategoryDatabase]},
	{Key: accessmanager.RealtimeMonitorCategoryNode, Count: counts[accessmanager.RealtimeMonitorCategoryNode]},
}
```

- [ ] **Step 4: Run the Database provider test to verify GREEN**

Run:

```bash
go test ./internalv2/app -run TestManagerClusterMonitorProviderReturnsDatabaseOperatorCards -count=1
```

Expected: pass.

## Task 3: Frontend Category and Card Rendering

**Files:**

- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`
- Modify: `web/src/pages/cluster-monitor/types.ts`
- Modify: `web/src/pages/cluster-monitor/metric-config.ts`
- Modify: `web/src/pages/cluster-monitor/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write the failing selector test**

In `web/src/pages/cluster-monitor/page.test.tsx`, update `filters realtime monitor by selected category` so it verifies and selects Database:

```ts
test("filters realtime monitor by selected category", async () => {
  const user = userEvent.setup()
  vi.mocked(getRealtimeMonitor).mockResolvedValue(readyClusterMonitorResponse())
  renderClusterMonitorPage()

  const categorySelect = await screen.findByRole("combobox", { name: "Category" })
  expect(categorySelect).toHaveValue("common")
  expect(within(categorySelect).getByRole("option", { name: "Common" })).toBeInTheDocument()
  expect(within(categorySelect).getByRole("option", { name: "Database" })).toBeInTheDocument()
  expect(within(categorySelect).queryByRole("option", { name: "All" })).not.toBeInTheDocument()
  expect(getRealtimeMonitor).toHaveBeenCalledWith({ window: "15m", category: "common" })

  await user.selectOptions(categorySelect, "database")

  expect(categorySelect).toHaveValue("database")
  expect(getRealtimeMonitor).toHaveBeenLastCalledWith({ window: "15m", category: "database" })
})
```

- [ ] **Step 2: Add a failing Database card render test**

In `web/src/pages/cluster-monitor/page.test.tsx`, add this response helper:

```ts
function databaseOperatorCard(
  key: string,
  tone: "normal" | "warning" | "critical",
  unit: string,
  value: number,
): RealtimeMonitorResponse["cards"][number] {
  return {
    key,
    category: "database",
    source: "prometheus",
    stage: "runtimePressure",
    tone,
    unit,
    value,
    available: true,
    error: "",
    series: [
      { timestamp: 1781767200000, value: value * 0.8 },
      { timestamp: 1781767220000, value },
    ],
    stats: [],
  }
}

function databaseOperatorMonitorResponse(): RealtimeMonitorResponse {
  return {
    status: "ready",
    generated_at: "2026-06-23T10:00:00Z",
    window_seconds: 900,
    step_seconds: 20,
    scope: { view: "realtime_monitor", node_id: 1 },
    sources: {
      prometheus: { enabled: true, base_url: "http://127.0.0.1:9090", query_ms: 12, error: "" },
      control_snapshot: { enabled: false, query_ms: 0, error: "" },
    },
    categories: [
      { key: "common", count: 0 },
      { key: "database", count: 6 },
    ],
    snapshot: [],
    cards: [
      databaseOperatorCard("storageWriteP99", "warning", "ms", 18.4),
      databaseOperatorCard("storageCommitErrorRate", "critical", "%", 0.2),
      databaseOperatorCard("storageCommitQueueUsage", "warning", "%", 37.5),
      databaseOperatorCard("storagePhysicalCommitP99", "warning", "ms", 9.3),
      databaseOperatorCard("storageCommitBatchRecordsP95", "normal", "records", 128),
      databaseOperatorCard("storageCommitBatchBytesP95", "normal", "B", 65536),
    ],
  }
}
```

Add this test:

```ts
test("renders database operator monitor cards", async () => {
  vi.mocked(getRealtimeMonitor).mockResolvedValueOnce(databaseOperatorMonitorResponse())
  renderClusterMonitorPage()

  expect(await screen.findByText("Storage Write P99")).toBeInTheDocument()
  expect(screen.getByText("Storage Commit Errors")).toBeInTheDocument()
  expect(screen.getByText("Storage Commit Queue")).toBeInTheDocument()
  expect(screen.getByText("Physical Commit P99")).toBeInTheDocument()
  expect(screen.getByText("Commit Batch Records P95")).toBeInTheDocument()
  expect(screen.getByText("Commit Batch Bytes P95")).toBeInTheDocument()
})
```

- [ ] **Step 3: Run the frontend tests to verify RED**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/page.test.tsx
```

Expected: fail because `"database"` is not a known category and the new Database card keys do not have metric configs or translations.

- [ ] **Step 4: Add frontend category and metric keys**

In `web/src/lib/manager-api.types.ts`, extend `RealtimeMonitorCategory`:

```ts
  | "slot"
  | "database"
  | "node"
```

In `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`, extend `categories`:

```ts
  "slot",
  "database",
  "node",
```

In `web/src/pages/cluster-monitor/types.ts`, add metric keys:

```ts
  | "storageCommitErrorRate"
  | "storageCommitQueueUsage"
  | "storagePhysicalCommitP99"
  | "storageCommitBatchRecordsP95"
  | "storageCommitBatchBytesP95"
```

- [ ] **Step 5: Add Database card configs**

In `web/src/pages/cluster-monitor/metric-config.ts`, keep the existing `storageWriteP99` config and add:

```ts
  storageCommitErrorRate: {
    titleId: "clusterMonitor.metrics.storageCommitErrorRate",
    helpId: "clusterMonitor.help.storageCommitErrorRate",
    chartColor: "#dc2626",
    precision: 2,
    stage: "runtimePressure",
    tone: "critical",
  },
  storageCommitQueueUsage: {
    titleId: "clusterMonitor.metrics.storageCommitQueueUsage",
    helpId: "clusterMonitor.help.storageCommitQueueUsage",
    chartColor: "#ca8a04",
    precision: 1,
    stage: "runtimePressure",
    tone: "warning",
  },
  storagePhysicalCommitP99: {
    titleId: "clusterMonitor.metrics.storagePhysicalCommitP99",
    helpId: "clusterMonitor.help.storagePhysicalCommitP99",
    chartColor: "#db2777",
    precision: 1,
    stage: "runtimePressure",
    tone: "warning",
  },
  storageCommitBatchRecordsP95: {
    titleId: "clusterMonitor.metrics.storageCommitBatchRecordsP95",
    helpId: "clusterMonitor.help.storageCommitBatchRecordsP95",
    chartColor: "#2563eb",
    precision: 0,
    stage: "runtimePressure",
    tone: "normal",
  },
  storageCommitBatchBytesP95: {
    titleId: "clusterMonitor.metrics.storageCommitBatchBytesP95",
    helpId: "clusterMonitor.help.storageCommitBatchBytesP95",
    chartColor: "#4f46e5",
    precision: 1,
    stage: "runtimePressure",
    tone: "normal",
  },
```

- [ ] **Step 6: Add English and Chinese translations**

In `web/src/i18n/messages/en.ts`, add:

```ts
  "clusterMonitor.category.database": "Database",
  "clusterMonitor.metrics.storageCommitErrorRate": "Storage Commit Errors",
  "clusterMonitor.metrics.storageCommitQueueUsage": "Storage Commit Queue",
  "clusterMonitor.metrics.storagePhysicalCommitP99": "Physical Commit P99",
  "clusterMonitor.metrics.storageCommitBatchRecordsP95": "Commit Batch Records P95",
  "clusterMonitor.metrics.storageCommitBatchBytesP95": "Commit Batch Bytes P95",
  "clusterMonitor.help.storageCommitErrorRate": "Share of internalv2 message DB commit requests ending with non-OK results.",
  "clusterMonitor.help.storageCommitQueueUsage": "Message DB grouped commit queue depth as a percentage of configured capacity.",
  "clusterMonitor.help.storagePhysicalCommitP99": "P99 duration of the physical storage commit stage for grouped message DB commits.",
  "clusterMonitor.help.storageCommitBatchRecordsP95": "P95 number of logical records collected into each grouped message DB commit.",
  "clusterMonitor.help.storageCommitBatchBytesP95": "P95 approximate payload bytes collected into each grouped message DB commit.",
```

In `web/src/i18n/messages/zh-CN.ts`, add:

```ts
  "clusterMonitor.category.database": "数据库",
  "clusterMonitor.metrics.storageCommitErrorRate": "存储提交错误率",
  "clusterMonitor.metrics.storageCommitQueueUsage": "存储提交队列",
  "clusterMonitor.metrics.storagePhysicalCommitP99": "物理提交 P99",
  "clusterMonitor.metrics.storageCommitBatchRecordsP95": "提交批记录数 P95",
  "clusterMonitor.metrics.storageCommitBatchBytesP95": "提交批字节数 P95",
  "clusterMonitor.help.storageCommitErrorRate": "internalv2 message DB 提交请求以非 OK 结果结束的比例。",
  "clusterMonitor.help.storageCommitQueueUsage": "Message DB grouped commit 队列深度相对配置容量的占用比例。",
  "clusterMonitor.help.storagePhysicalCommitP99": "Message DB grouped commit 的物理存储提交阶段 P99 耗时。",
  "clusterMonitor.help.storageCommitBatchRecordsP95": "每次 Message DB grouped commit 收集的逻辑记录数 P95。",
  "clusterMonitor.help.storageCommitBatchBytesP95": "每次 Message DB grouped commit 收集的近似 payload 字节数 P95。",
```

- [ ] **Step 7: Run the frontend tests to verify GREEN**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/page.test.tsx
```

Expected: pass.

## Task 4: FLOW Documentation

**Files:**

- Modify: `internalv2/access/manager/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Update manager access FLOW**

In `internalv2/access/manager/FLOW.md`, update the `/manager/realtime-monitor` category list so it includes `database`:

```text
category (`common`, `gateway`, `internal`, `message`, `conversation`,
`channel`, `control`, `slot`, `database`, or `node`)
```

Add this sentence to the realtime monitor paragraph:

```text
Database cards cover internalv2 message DB group-commit request latency, error rate,
commit queue usage, physical commit latency, and grouped batch shape.
```

- [ ] **Step 2: Update app composition FLOW**

In `internalv2/app/FLOW.md`, update the Prometheus-backed realtime monitor wiring paragraph so it includes Database:

```text
attach one Prometheus-backed realtime monitor provider so `/manager/realtime-monitor`
can expose business-path and cluster-operations card series, including Slot
proposal admission, leader-change, replica-lag, scheduler pressure, and
internalv2 Database group-commit pressure cards, category counts, explicit
disabled/unavailable source states, and bounded `ListNodes`/`ListSlots`
control snapshots through the management usecase
```

- [ ] **Step 3: Check FLOW wording**

Run:

```bash
rg -n "database|Database|realtime-monitor" internalv2/access/manager/FLOW.md internalv2/app/FLOW.md
```

Expected: output shows the Database category and Database group-commit wording in the two FLOW files.

## Task 5: Focused Verification

**Files:**

- Verify all modified Go and web files.

- [ ] **Step 1: Run focused Go tests**

Run:

```bash
go test ./internalv2/access/manager ./internalv2/app ./pkg/metrics
```

Expected: pass.

- [ ] **Step 2: Run focused web tests**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/page.test.tsx src/lib/manager-api.test.ts
```

Expected: pass.

- [ ] **Step 3: Run TypeScript build**

Run:

```bash
cd web && bunx tsc -b
```

Expected: pass.

- [ ] **Step 4: Check diff hygiene**

Run:

```bash
git diff --check
```

Expected: no output and exit code 0.

- [ ] **Step 5: Review touched files only**

Run:

```bash
git diff -- internalv2/access/manager/monitor.go internalv2/access/manager/server_test.go internalv2/access/manager/FLOW.md internalv2/app/manager_cluster_monitor_prometheus.go internalv2/app/manager_cluster_monitor_prometheus_test.go internalv2/app/FLOW.md web/src/lib/manager-api.types.ts web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx web/src/pages/cluster-monitor/types.ts web/src/pages/cluster-monitor/metric-config.ts web/src/pages/cluster-monitor/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
```

Expected: diff contains only the Database realtime monitor category, Database metric definitions, translations, and FLOW documentation.

## Optional Task 6: Commit in an Isolated Worktree

**Files:**

- Stage only the files modified by this plan.

- [ ] **Step 1: Confirm the diff contains only this feature**

Run:

```bash
git status --short
git diff --name-only
```

Expected: every file listed belongs to this Database monitor category feature or was intentionally present before implementation.

- [ ] **Step 2: Stage this feature**

Run:

```bash
git add internalv2/access/manager/monitor.go internalv2/access/manager/server_test.go internalv2/access/manager/FLOW.md internalv2/app/manager_cluster_monitor_prometheus.go internalv2/app/manager_cluster_monitor_prometheus_test.go internalv2/app/FLOW.md web/src/lib/manager-api.types.ts web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx web/src/pages/cluster-monitor/types.ts web/src/pages/cluster-monitor/metric-config.ts web/src/pages/cluster-monitor/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
```

Expected: only this feature's files are staged.

- [ ] **Step 3: Commit**

Run:

```bash
git commit -m "feat: add database realtime monitor category"
```

Expected: commit succeeds after the focused verification commands pass.
