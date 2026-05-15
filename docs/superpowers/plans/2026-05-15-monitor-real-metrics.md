# Monitor Real Metrics Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Web Monitor page mock data with a real Manager API backed by the existing dashboard metrics collector.

**Architecture:** Add a dedicated `GET /manager/monitor/metrics` API in the manager access layer. The management usecase maps existing dashboard collector series into timestamped monitor metrics, fans out through node RPC for all-node scope, and exposes capability metadata so the React Monitor page renders only metrics reported by the API.

**Tech Stack:** Go manager API/usecase tests, existing `pkg/metrics.DashboardCollector`, React 19, Vitest, Recharts, existing manager API client.

---

### Task 1: Backend Monitor API Contract

**Files:**
- Test: `internal/access/manager/server_test.go`
- Create: `internal/usecase/management/monitor_metrics.go`
- Create: `internal/access/manager/monitor_metrics.go`
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`

- [ ] **Step 1: Write the failing manager route test**
  - Add a test that calls `GET /manager/monitor/metrics?window=5m&step=5s`.
  - Stub the management layer with deterministic monitor metrics.
  - Assert JSON includes `scope.view=local_node`, `capabilities.node_filter=false`, one `nodes` item, and timestamped `metrics.send_rate.points`.

- [ ] **Step 2: Run backend test and verify RED**
  - Run: `go test ./internal/access/manager -run TestServerHandleMonitorMetrics -count=1`
  - Expected: fail because the route does not exist or the interface method is missing.

- [ ] **Step 3: Implement the minimal backend route**
  - Add `GetMonitorMetrics(ctx, nodeID, window, step)` to the manager usecase interface.
  - Implement request validation using the same duration bounds as dashboard metrics.
  - Return 503 warming-up for `metrics.ErrInsufficientData`.

- [ ] **Step 4: Run backend test and verify GREEN**
  - Run: `go test ./internal/access/manager -run TestServerHandleMonitorMetrics -count=1`
  - Expected: pass.

### Task 2: Usecase Mapping

**Files:**
- Test: `internal/usecase/management/monitor_metrics_test.go`
- Modify: `internal/usecase/management/monitor_metrics.go`

- [ ] **Step 1: Write failing usecase mapping tests**
  - Test timestamp reconstruction from `generated_at/window/step`.
  - Test metric keys include current real collector metrics only: send/deliver rate, connections, latency, fail rates, active channels, retry queue, fan-out.

- [ ] **Step 2: Run usecase test and verify RED**
  - Run: `go test ./internal/usecase/management -run TestMonitorMetrics -count=1`
  - Expected: fail until mapping exists.

- [ ] **Step 3: Implement minimal mapping**
  - Build `MonitorMetricsResult` DTO with `Scope`, `Capabilities`, `Nodes`, and `Metrics` map.
  - Reuse `GetDashboardMetrics` collector query; do not invent unsupported CPU/storage/IO values.

- [ ] **Step 4: Run usecase test and verify GREEN**
  - Run: `go test ./internal/usecase/management -run TestMonitorMetrics -count=1`
  - Expected: pass.

### Task 3: Frontend API Client And Hook

**Files:**
- Test: `web/src/lib/manager-api.test.ts`
- Test: `web/src/pages/monitor/use-monitor-data.test.tsx`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/pages/monitor/types.ts`
- Modify: `web/src/pages/monitor/use-monitor-data.ts`

- [ ] **Step 1: Write failing frontend tests**
  - Assert `getMonitorMetrics({window:"5m", step:"5s"})` calls `/manager/monitor/metrics?window=5m&step=5s`.
  - Mock the API and assert `useMonitorData` exposes real point values, nodes, loading, refresh, and pause behavior.

- [ ] **Step 2: Run frontend tests and verify RED**
  - Run: `cd web && yarn test src/lib/manager-api.test.ts src/pages/monitor/use-monitor-data.test.tsx`
  - Expected: fail because API client/hook do not exist or still generate mock data.

- [ ] **Step 3: Implement API client and hook**
  - Add monitor response TypeScript types.
  - Replace random generation with polling `getMonitorMetrics` every 5 seconds when not paused.
  - Preserve the time-range control by passing `window=<range>&step=5s`.

- [ ] **Step 4: Run frontend tests and verify GREEN**
  - Run: `cd web && yarn test src/lib/manager-api.test.ts src/pages/monitor/use-monitor-data.test.tsx`
  - Expected: pass.

### Task 4: Monitor Page Rendering

**Files:**
- Test: `web/src/pages/monitor/page.test.tsx`
- Modify: `web/src/pages/monitor/page.tsx`
- Modify: `web/src/pages/monitor/components/metric-chart.tsx`
- Modify: `web/src/pages/monitor/components/monitor-controls.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page test**
  - Mock `getMonitorMetrics` with real metrics.
  - Assert the Monitor page renders real metric names and does not render unsupported mock-only sections.
  - Assert warming/error states use `ResourceState`.

- [ ] **Step 2: Run page test and verify RED**
  - Run: `cd web && yarn test src/pages/monitor/page.test.tsx src/pages/page-shells.test.tsx`
  - Expected: fail until the page consumes the new hook contract.

- [ ] **Step 3: Implement capability-aware rendering**
  - Render message flow and connection sections from API metrics.
  - Hide unsupported node health/storage sections until backend exposes real data.
  - Disable node selector when `capabilities.node_filter=false`.

- [ ] **Step 4: Run page test and verify GREEN**
  - Run: `cd web && yarn test src/pages/monitor/page.test.tsx src/pages/page-shells.test.tsx`
  - Expected: pass.

### Task 5: Documentation And Targeted Verification

**Files:**
- Modify: `web/README.md`
- Optional: `docs/development/PROJECT_KNOWLEDGE.md` only if a reusable project rule is discovered.

- [ ] **Step 1: Update Web API matrix**
  - Change `/monitor` from placeholder to `GET /manager/monitor/metrics` implemented.

- [ ] **Step 2: Run targeted backend verification**
  - Run: `go test ./internal/access/manager ./internal/usecase/management ./pkg/metrics`
  - Expected: pass.

- [ ] **Step 3: Run targeted frontend verification**
  - Run: `cd web && yarn test src/lib/manager-api.test.ts src/pages/monitor/page.test.tsx src/pages/monitor/use-monitor-data.test.tsx src/pages/page-shells.test.tsx`
  - Expected: pass.

- [ ] **Step 4: Run frontend build**
  - Run: `cd web && yarn build`
  - Expected: pass.


### Task 6: Cluster Monitor Aggregation

**Files:**
- Modify: `pkg/metrics/dashboard_collector.go`
- Modify: `internal/usecase/management/monitor_metrics.go`
- Add: `internal/access/node/monitor_metrics_rpc.go`
- Add: `internal/access/node/monitor_metrics_codec.go`
- Modify: `web/src/pages/monitor/use-monitor-data.ts`

- [x] Add raw delta series to the dashboard collector so fail-rate and fan-out aggregation can be recomputed from real numerators and denominators.
- [x] Add node RPC for remote local monitor collector reads.
- [x] Aggregate all-node metrics in the management usecase and support optional `node_id` queries.
- [x] Pass selected node IDs from the Monitor page to the Manager API.
