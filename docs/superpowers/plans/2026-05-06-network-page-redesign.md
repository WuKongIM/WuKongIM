# Network Page Redesign Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the manager `/network` page into a cloud-monitoring style dashboard that lets operators assess node-to-node communication health in under five seconds.

**Architecture:** Keep the backend API unchanged for the MVP and move the frontend from one monolithic page into focused hooks, aggregation utilities, reusable metric cards, and card-specific chart components. The page fetches `/manager/network/summary`, applies URL-backed node/time filters client-side, renders eight scannable metric cards, and opens a detail drawer for deeper tables/config without reintroducing the old 12-section page overload.

**Tech Stack:** React 19, TypeScript, React Intl, React Router URL search params, Recharts, existing shadcn-style UI components, Vitest, Testing Library, Bun.

---

## Scope Decisions

- Frontend-only MVP: do not change Go manager APIs in this plan.
- Keep cluster wording consistent: use "single-node cluster" for deployment shape and "local node" only for node-local observation scope.
- Current `/manager/network/summary` has one-minute local history only; `5m` and `15m` UI selections persist in URL but reuse available history until backend `?window=` support exists.
- Current traffic payload is local-total by message type and does not expose per-peer traffic; selected-node traffic cards must show the local-total values with a note rather than inventing peer traffic.
- Preserve the old page as a rollback component during migration; the new page is the default.
- Do not modify `wukongim.conf.example`; no runtime server config changes are part of this frontend-only plan.

## File Structure

- Create `web/src/pages/network/legacy-page.tsx`
  - Holds the current monolithic `NetworkPage` implementation renamed to `NetworkLegacyPage` for rollback/reference.
- Replace `web/src/pages/network/page.tsx`
  - Becomes the new composition root for the cloud monitoring dashboard.
  - Owns selected detail-card state and wires hooks/components together.
- Create `web/src/pages/network/components/filter-bar.tsx`
  - Renders node multi-select, time range select, manual refresh, auto-refresh toggle, and scope badges.
- Create `web/src/pages/network/components/metric-card.tsx`
  - Reusable card shell with title, primary metric, mini chart slot, labels, alert styling, loading/error support, and keyboard-click behavior.
- Create `web/src/pages/network/components/node-health-card.tsx`
  - Donut chart and alive/suspect/dead/draining labels.
- Create `web/src/pages/network/components/connection-pool-card.tsx`
  - Stacked horizontal bar for cluster and data-plane pool utilization.
- Create `web/src/pages/network/components/rpc-latency-card.tsx`
  - P95 primary metric and P50/P95/P99 trend display.
- Create `web/src/pages/network/components/rpc-success-card.tsx`
  - Success-rate primary metric and success/failure/expected-timeout chart.
- Create `web/src/pages/network/components/traffic-card.tsx`
  - TX/RX mini area chart and local-total caveat when peer breakdown is unavailable.
- Create `web/src/pages/network/components/errors-card.tsx`
  - Dial/queue/timeout stacked bar and warning/danger threshold treatment.
- Create `web/src/pages/network/components/message-types-card.tsx`
  - Top-five message type horizontal bars by total bytes.
- Create `web/src/pages/network/components/events-card.tsx`
  - Latest three events with severity, kind, target node, timestamp, and "View all events".
- Create `web/src/pages/network/components/detail-drawer.tsx`
  - Right-side drawer on desktop and full-width mobile sheet with full charts/tables/config/raw JSON.
- Create `web/src/pages/network/hooks/use-node-filter.ts`
  - URL-backed selected node IDs, time range, and auto-refresh state.
- Create `web/src/pages/network/hooks/use-network-data.ts`
  - Fetches summary, refreshes manually/automatically, exposes loading/error/filtered metrics.
- Create `web/src/pages/network/utils/aggregation.ts`
  - Pure aggregation/filtering logic and alert-level calculation.
- Create `web/src/pages/network/utils/formatters.ts`
  - Locale-aware number, bytes, bps, latency, percent, and timestamp formatting helpers.
- Create `web/src/pages/network/utils/charting.ts`
  - Shared chart color palette and `ChartConfig` factory.
- Create `web/src/pages/network/utils/aggregation.test.ts`
  - Unit tests for all-nodes and selected-node aggregation.
- Create `web/src/pages/network/hooks/use-node-filter.test.tsx`
  - URL persistence tests for filter state.
- Create `web/src/pages/network/hooks/use-network-data.test.tsx`
  - Data fetch, manual refresh, and auto-refresh tests.
- Modify `web/src/pages/network/page.test.tsx`
  - Replace old layout assertions with dashboard/filter/card/detail drawer behavior.
- Modify `web/src/pages/page-shells.test.tsx`
  - Update shell smoke expectation for `/network` to a stable new heading/card label.
- Modify `web/src/i18n/messages/en.ts`
  - Add new network dashboard labels and remove/keep old keys as needed for legacy component compilation.
- Modify `web/src/i18n/messages/zh-CN.ts`
  - Add matching Chinese translations for every new key.
- Modify `web/src/lib/env.ts`
  - Optional helper for a rollback flag if the implementation chooses runtime fallback (`VITE_NETWORK_PAGE_V2_ENABLED=false`).
- Modify `web/src/lib/manager-api.test.ts` only if `env.ts` adds a feature-flag helper that needs coverage.

## Task 1: Preserve Legacy Page and Add Dashboard Smoke Tests

**Files:**
- Create: `web/src/pages/network/legacy-page.tsx`
- Modify: `web/src/pages/network/page.tsx`
- Modify: `web/src/pages/network/page.test.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`

- [ ] **Step 1: Copy the current page into a legacy component**

Copy the current contents of `web/src/pages/network/page.tsx` into `web/src/pages/network/legacy-page.tsx` and rename the exported component:

```tsx
export function NetworkLegacyPage() {
  // Existing NetworkPage body moved here unchanged.
}
```

Do not delete old helper functions from the copied file yet; keeping it compiling is the rollback safety net.

- [ ] **Step 2: Write failing tests for the new dashboard shell**

Replace the first layout test in `web/src/pages/network/page.test.tsx` with assertions for the new cloud dashboard shape:

```tsx
test("renders filter bar and focused network metric cards", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)

  renderNetworkPage()

  expect(screen.getByRole("status")).toHaveAttribute("data-kind", "loading")
  expect(await screen.findByRole("heading", { name: "Network" })).toBeInTheDocument()
  expect(screen.getByLabelText("Node selector")).toBeInTheDocument()
  expect(screen.getByLabelText("Time range")).toHaveValue("1m")
  expect(screen.getByText("Local Node: 1")).toBeInTheDocument()
  expect(screen.getByText("Controller Leader: 2")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()

  for (const title of [
    "Node Health Status",
    "Connection Pool Status",
    "RPC Call Latency",
    "RPC Success Rate",
    "Network Traffic",
    "Network Errors",
    "Message Type Distribution",
    "Recent Events",
  ]) {
    expect(screen.getByText(title)).toBeInTheDocument()
  }

  expect(screen.queryByText("Peer Pool Balance")).not.toBeInTheDocument()
  expect(screen.queryByText("Discovery & Config")).not.toBeInTheDocument()
})
```

Expected current failure: the old monolithic page has no filter bar and does not render the eight focused card titles.

- [ ] **Step 3: Update route smoke tests for the new network label**

In `web/src/pages/page-shells.test.tsx`, change the network route expectation from the old section title to a new stable card title:

```tsx
["/network", "Network", "Node Health Status"],
```

For the Chinese smoke case, use:

```tsx
["/network", "网络", "节点健康状态"],
```

- [ ] **Step 4: Run tests to verify RED**

Run:

```bash
cd web
bun run test src/pages/network/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: FAIL because `NetworkPage` still renders the legacy layout.

## Task 2: Add Pure Formatting and Aggregation Utilities

**Files:**
- Create: `web/src/pages/network/utils/formatters.ts`
- Create: `web/src/pages/network/utils/charting.ts`
- Create: `web/src/pages/network/utils/aggregation.ts`
- Create: `web/src/pages/network/utils/aggregation.test.ts`

- [ ] **Step 1: Write aggregation tests for all-nodes metrics**

Create `web/src/pages/network/utils/aggregation.test.ts` with a local fixture derived from `networkSummaryFixture` and this test:

```ts
import { describe, expect, it } from "vitest"

import type { ManagerNetworkSummaryResponse } from "@/lib/manager-api.types"
import { aggregateNetworkMetrics } from "./aggregation"

const summary = makeNetworkSummaryFixture()

describe("aggregateNetworkMetrics", () => {
  it("uses headline and history metrics for all nodes", () => {
    const metrics = aggregateNetworkMetrics(summary, [])

    expect(metrics.isAllNodes).toBe(true)
    expect(metrics.health).toMatchObject({ alive: 2, suspect: 0, dead: 0, draining: 1, total: 3 })
    expect(metrics.pools.cluster).toEqual({ active: 3, idle: 4 })
    expect(metrics.pools.dataPlane).toEqual({ active: 2, idle: 1 })
    expect(metrics.errors.total).toBe(6)
    expect(metrics.rpc.calls).toBe(40)
    expect(metrics.rpc.successRate).toBe(0.975)
    expect(metrics.latency.p95Ms).toBe(7)
    expect(metrics.traffic.txBytes).toBe(4096)
    expect(metrics.events).toHaveLength(1)
  })
})
```

`makeNetworkSummaryFixture()` can live in the same test file initially; do not import test fixtures from `page.test.tsx`.

- [ ] **Step 2: Add selected-node aggregation tests**

Add a second test:

```ts
it("aggregates selected peer nodes without inventing per-peer traffic", () => {
  const metrics = aggregateNetworkMetrics(summary, [2])

  expect(metrics.isAllNodes).toBe(false)
  expect(metrics.selectedNodeIds).toEqual([2])
  expect(metrics.health).toMatchObject({ alive: 1, suspect: 0, dead: 0, draining: 0, total: 1 })
  expect(metrics.pools.cluster).toEqual({ active: 1, idle: 2 })
  expect(metrics.pools.dataPlane).toEqual({ active: 2, idle: 1 })
  expect(metrics.errors).toMatchObject({ dial: 1, queueFull: 2, timeouts: 3, remote: 4, total: 10 })
  expect(metrics.rpc.calls).toBe(40)
  expect(metrics.rpc.successRate).toBe(0.975)
  expect(metrics.traffic.isFilteredByNode).toBe(false)
  expect(metrics.traffic.scopeNote).toMatch(/local total/i)
  expect(metrics.events.map((event) => event.target_node)).toEqual([2])
})
```

- [ ] **Step 3: Run aggregation tests to verify RED**

Run:

```bash
cd web
bun run test src/pages/network/utils/aggregation.test.ts
```

Expected: FAIL because the utility files do not exist.

- [ ] **Step 4: Implement formatting helpers**

Create `web/src/pages/network/utils/formatters.ts`:

```ts
import type { IntlShape } from "react-intl"

export function compactNumber(intl: IntlShape, value: number) {
  return new Intl.NumberFormat(intl.locale).format(value)
}

export function formatBytes(intl: IntlShape, value: number) {
  if (value >= 1024 * 1024) return `${new Intl.NumberFormat(intl.locale, { maximumFractionDigits: 1 }).format(value / 1024 / 1024)} MB`
  if (value >= 1024) return `${new Intl.NumberFormat(intl.locale, { maximumFractionDigits: 1 }).format(value / 1024)} KB`
  return `${compactNumber(intl, value)} B`
}

export function formatBps(intl: IntlShape, value: number) {
  return `${compactNumber(intl, value)} bps`
}

export function formatLatency(intl: IntlShape, value: number) {
  if (value <= 0) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return `${compactNumber(intl, value)} ms`
}

export function formatPercent(intl: IntlShape, value: number | null) {
  if (value === null || Number.isNaN(value)) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return new Intl.NumberFormat(intl.locale, { maximumFractionDigits: 1, style: "percent" }).format(value)
}

export function formatTimestamp(intl: IntlShape, value: string) {
  const date = new Date(value)
  if (!value || value.startsWith("0001-") || Number.isNaN(date.getTime())) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return new Intl.DateTimeFormat(intl.locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date)
}

export function formatShortTime(intl: IntlShape, value: string) {
  const date = new Date(value)
  if (!value || value.startsWith("0001-") || Number.isNaN(date.getTime())) return intl.formatMessage({ id: "network.latency.insufficientSamples" })
  return new Intl.DateTimeFormat(intl.locale, { hour: "2-digit", minute: "2-digit" }).format(date)
}
```

- [ ] **Step 5: Implement chart palette/config**

Create `web/src/pages/network/utils/charting.ts`:

```ts
import type { IntlShape } from "react-intl"

import type { ChartConfig } from "@/components/ui/chart"

export const networkChartColors = {
  alive: "hsl(142 70% 45%)",
  suspect: "hsl(38 92% 50%)",
  dead: "hsl(0 72% 51%)",
  draining: "hsl(217 91% 60%)",
  active: "hsl(199 89% 48%)",
  idle: "hsl(210 14% 70%)",
  tx: "hsl(24 95% 53%)",
  rx: "hsl(173 80% 40%)",
  success: "hsl(142 70% 45%)",
  failures: "hsl(0 72% 51%)",
  expected: "hsl(217 91% 60%)",
  dial: "hsl(18 92% 48%)",
  queue: "hsl(38 92% 50%)",
  timeout: "hsl(0 72% 51%)",
  remote: "hsl(330 81% 60%)",
  p50: "hsl(173 80% 40%)",
  p95: "hsl(24 95% 53%)",
  p99: "hsl(0 72% 51%)",
}

export function networkChartConfig(intl: IntlShape): ChartConfig {
  return {
    alive: { label: intl.formatMessage({ id: "network.legend.alive" }), color: networkChartColors.alive },
    suspect: { label: intl.formatMessage({ id: "network.legend.suspect" }), color: networkChartColors.suspect },
    dead: { label: intl.formatMessage({ id: "network.legend.dead" }), color: networkChartColors.dead },
    draining: { label: intl.formatMessage({ id: "network.legend.draining" }), color: networkChartColors.draining },
    active: { label: intl.formatMessage({ id: "network.legend.active" }), color: networkChartColors.active },
    idle: { label: intl.formatMessage({ id: "network.legend.idle" }), color: networkChartColors.idle },
    tx: { label: intl.formatMessage({ id: "network.legend.tx" }), color: networkChartColors.tx },
    rx: { label: intl.formatMessage({ id: "network.legend.rx" }), color: networkChartColors.rx },
    success: { label: intl.formatMessage({ id: "network.legend.success" }), color: networkChartColors.success },
    failures: { label: intl.formatMessage({ id: "network.rpc.abnormalFailures" }), color: networkChartColors.failures },
    expected: { label: intl.formatMessage({ id: "network.rpc.expectedLongPollExpiries" }), color: networkChartColors.expected },
    dial: { label: intl.formatMessage({ id: "network.legend.dial" }), color: networkChartColors.dial },
    queue: { label: intl.formatMessage({ id: "network.legend.queue" }), color: networkChartColors.queue },
    timeout: { label: intl.formatMessage({ id: "network.legend.timeout" }), color: networkChartColors.timeout },
    remote: { label: intl.formatMessage({ id: "network.legend.remote" }), color: networkChartColors.remote },
    p50: { label: "P50", color: networkChartColors.p50 },
    p95: { label: "P95", color: networkChartColors.p95 },
    p99: { label: "P99", color: networkChartColors.p99 },
  }
}
```

- [ ] **Step 6: Implement aggregation types and helpers**

Create `web/src/pages/network/utils/aggregation.ts` with these exported types and functions:

```ts
import type {
  ManagerNetworkEvent,
  ManagerNetworkHistory,
  ManagerNetworkPeer,
  ManagerNetworkRPCService,
  ManagerNetworkSummaryResponse,
  ManagerNetworkTrafficMessageType,
} from "@/lib/manager-api.types"

export type NetworkTimeRange = "1m" | "5m" | "15m"
export type NetworkAlertLevel = "none" | "warning" | "danger"

export type NetworkNodeOption = {
  nodeId: number
  label: string
  health: string
  isLocal: boolean
}

export type FilteredNetworkMetrics = {
  isAllNodes: boolean
  selectedNodeIds: number[]
  selectedPeers: ManagerNetworkPeer[]
  nodeOptions: NetworkNodeOption[]
  services: ManagerNetworkRPCService[]
  history: ManagerNetworkHistory
  health: { alive: number; suspect: number; dead: number; draining: number; total: number }
  pools: {
    cluster: { active: number; idle: number }
    dataPlane: { active: number; idle: number }
    active: number
    idle: number
    utilization: number
    alertLevel: NetworkAlertLevel
  }
  latency: { p50Ms: number; p95Ms: number; p99Ms: number; alertLevel: NetworkAlertLevel }
  rpc: { calls: number; success: number; failures: number; expectedTimeouts: number; successRate: number | null; alertLevel: NetworkAlertLevel }
  traffic: {
    txBytes: number
    rxBytes: number
    txBps: number
    rxBps: number
    messageTypes: ManagerNetworkTrafficMessageType[]
    isFilteredByNode: boolean
    scopeNote: string | null
  }
  errors: { dial: number; queueFull: number; timeouts: number; remote: number; total: number; alertLevel: NetworkAlertLevel }
  events: ManagerNetworkEvent[]
}
```

Use these implementation rules:

```ts
export function aggregateNetworkMetrics(summary: ManagerNetworkSummaryResponse, selectedNodeIds: number[]): FilteredNetworkMetrics {
  const selected = normalizeSelectedNodeIds(selectedNodeIds)
  const isAllNodes = selected.length === 0
  const selectedPeers = isAllNodes ? summary.peers : summary.peers.filter((peer) => selected.includes(peer.node_id))
  const services = isAllNodes ? summary.services : summary.services.filter((service) => selected.includes(service.target_node))

  const health = isAllNodes ? headlineHealth(summary) : selectedPeerHealth(selectedPeers)
  const pools = aggregatePools(summary, selectedPeers, isAllNodes)
  const rpc = aggregateRpc(services, selectedPeers, isAllNodes)
  const latency = aggregateLatency(services, selectedPeers, isAllNodes)
  const errors = aggregateErrors(summary, selectedPeers, services, isAllNodes)
  const traffic = aggregateTraffic(summary, isAllNodes)
  const events = filterEventsByNodes(summary.events, selected)

  return {
    isAllNodes,
    selectedNodeIds: selected,
    selectedPeers,
    nodeOptions: nodeOptionsFromSummary(summary),
    services,
    history: summary.history ?? { window_seconds: 60, step_seconds: 60, traffic: [], rpc: [], errors: [] },
    health,
    pools,
    latency,
    rpc,
    traffic,
    errors,
    events,
  }
}
```

Key calculations:

- Health selected mode counts `peer.health` values only; if no selected remote peer exists, return zero counts instead of assuming local health.
- Pool utilization is `active / (active + idle)` and warns above `0.8`.
- Latency uses call-weighted averages for `p50_ms`, `p95_ms`, and `p99_ms`, ignoring non-positive samples; if selected services have no percentile data, fall back to selected peer `rpc.p95_ms` for P95 only.
- Latency alert is `danger` when P95 is above `100`, otherwise `none`.
- RPC success rate excludes expected long-poll timeouts from completed calls: `completed = max(calls_1m - expected_timeout_1m, 0)`.
- RPC failures are `timeout_1m + queue_full_1m + remote_error_1m + other_error_1m`.
- RPC alert is `danger` when success rate is below `0.95`; keep `none` when success rate is `null`.
- Error alert is `danger` when total errors are above `10`, `warning` when above `0`.
- Traffic always returns `summary.traffic` for the MVP and sets `isFilteredByNode=false` with a local-total scope note in selected mode.
- Message types are sorted by `bytes_1m` descending in card-specific code, not in the core aggregation.

- [ ] **Step 7: Run aggregation tests to verify GREEN**

Run:

```bash
cd web
bun run test src/pages/network/utils/aggregation.test.ts
```

Expected: PASS.

- [ ] **Step 8: Commit pure utilities**

Run:

```bash
git add web/src/pages/network/utils/aggregation.ts web/src/pages/network/utils/aggregation.test.ts web/src/pages/network/utils/formatters.ts web/src/pages/network/utils/charting.ts
git commit -m "Add network dashboard aggregation utilities"
```

## Task 3: Add URL-backed Filter and Data Hooks

**Files:**
- Create: `web/src/pages/network/hooks/use-node-filter.ts`
- Create: `web/src/pages/network/hooks/use-node-filter.test.tsx`
- Create: `web/src/pages/network/hooks/use-network-data.ts`
- Create: `web/src/pages/network/hooks/use-network-data.test.tsx`

- [ ] **Step 1: Write filter hook tests**

Create `web/src/pages/network/hooks/use-node-filter.test.tsx` with a small harness:

```tsx
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { expect, test } from "vitest"

import { useNodeFilter } from "./use-node-filter"

function Harness() {
  const filter = useNodeFilter()
  return (
    <div>
      <div data-testid="nodes">{filter.selectedNodes.join(",") || "all"}</div>
      <div data-testid="range">{filter.timeRange}</div>
      <div data-testid="auto">{String(filter.autoRefresh)}</div>
      <button onClick={() => filter.setSelectedNodes([2, 3])}>nodes</button>
      <button onClick={() => filter.setTimeRange("15m")}>range</button>
      <button onClick={filter.toggleAutoRefresh}>auto</button>
    </div>
  )
}

test("reads and writes network filter state through URL params", async () => {
  const user = userEvent.setup()
  render(<Harness />, { wrapper: ({ children }) => <MemoryRouter initialEntries={["/network?nodes=2&range=5m&autoRefresh=1"]}>{children}</MemoryRouter> })

  expect(screen.getByTestId("nodes")).toHaveTextContent("2")
  expect(screen.getByTestId("range")).toHaveTextContent("5m")
  expect(screen.getByTestId("auto")).toHaveTextContent("true")

  await user.click(screen.getByRole("button", { name: "nodes" }))
  await user.click(screen.getByRole("button", { name: "range" }))
  await user.click(screen.getByRole("button", { name: "auto" }))

  expect(screen.getByTestId("nodes")).toHaveTextContent("2,3")
  expect(screen.getByTestId("range")).toHaveTextContent("15m")
  expect(screen.getByTestId("auto")).toHaveTextContent("false")
})
```

- [ ] **Step 2: Run filter hook test to verify RED**

Run:

```bash
cd web
bun run test src/pages/network/hooks/use-node-filter.test.tsx
```

Expected: FAIL because the hook does not exist.

- [ ] **Step 3: Implement `useNodeFilter`**

Create `web/src/pages/network/hooks/use-node-filter.ts`:

```ts
import { useCallback, useMemo } from "react"
import { useSearchParams } from "react-router-dom"

import type { NetworkTimeRange } from "../utils/aggregation"

const validRanges = new Set<NetworkTimeRange>(["1m", "5m", "15m"])

function parseNodes(value: string | null) {
  if (!value) return []
  return value.split(",").map((item) => Number(item)).filter((item) => Number.isInteger(item) && item > 0)
}

function serializeNodes(nodeIds: number[]) {
  return Array.from(new Set(nodeIds)).sort((a, b) => a - b).join(",")
}

export function useNodeFilter() {
  const [searchParams, setSearchParams] = useSearchParams()

  const selectedNodes = useMemo(() => parseNodes(searchParams.get("nodes")), [searchParams])
  const timeRange = useMemo<NetworkTimeRange>(() => {
    const value = searchParams.get("range") as NetworkTimeRange | null
    return value && validRanges.has(value) ? value : "1m"
  }, [searchParams])
  const autoRefresh = searchParams.get("autoRefresh") === "1"

  const updateParams = useCallback((update: (next: URLSearchParams) => void) => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current)
      update(next)
      return next
    }, { replace: true })
  }, [setSearchParams])

  const setSelectedNodes = useCallback((nodeIds: number[]) => {
    updateParams((next) => {
      const serialized = serializeNodes(nodeIds)
      if (serialized) next.set("nodes", serialized)
      else next.delete("nodes")
    })
  }, [updateParams])

  const setTimeRange = useCallback((range: NetworkTimeRange) => {
    updateParams((next) => {
      next.set("range", range)
    })
  }, [updateParams])

  const toggleAutoRefresh = useCallback(() => {
    updateParams((next) => {
      if (next.get("autoRefresh") === "1") next.delete("autoRefresh")
      else next.set("autoRefresh", "1")
    })
  }, [updateParams])

  return {
    selectedNodes,
    timeRange,
    autoRefresh,
    setSelectedNodes,
    setTimeRange,
    toggleAutoRefresh,
    isNodeSelected: (nodeId: number) => selectedNodes.includes(nodeId),
  }
}
```

- [ ] **Step 4: Write data hook tests**

Create `web/src/pages/network/hooks/use-network-data.test.tsx` with a component harness and mocked API:

```tsx
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { useNetworkData } from "./use-network-data"

const getNetworkSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return { ...actual, getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args) }
})

function Harness({ autoRefresh = false, selectedNodes = [] }: { autoRefresh?: boolean; selectedNodes?: number[] }) {
  const state = useNetworkData({ autoRefresh, selectedNodes })
  return (
    <div>
      <div data-testid="loading">{String(state.loading)}</div>
      <div data-testid="alive">{state.filteredData?.health.alive ?? "none"}</div>
      <button onClick={() => { void state.refresh() }}>refresh</button>
    </div>
  )
}

test("fetches and aggregates network summary", async () => {
  getNetworkSummaryMock.mockResolvedValue(makeNetworkSummaryFixture())

  render(<Harness selectedNodes={[2]} />)

  expect(screen.getByTestId("loading")).toHaveTextContent("true")
  expect(await screen.findByTestId("alive")).toHaveTextContent("1")
})

test("manual refresh fetches again", async () => {
  getNetworkSummaryMock.mockResolvedValue(makeNetworkSummaryFixture())
  const user = userEvent.setup()

  render(<Harness />)

  await screen.findByText("2")
  await user.click(screen.getByRole("button", { name: "refresh" }))

  await waitFor(() => expect(getNetworkSummaryMock).toHaveBeenCalledTimes(2))
})
```

Use a local `makeNetworkSummaryFixture()` helper in this test file or move shared fixtures into `web/src/pages/network/test-fixtures.ts` if duplication grows.

- [ ] **Step 5: Implement `useNetworkData`**

Create `web/src/pages/network/hooks/use-network-data.ts`:

```ts
import { useCallback, useEffect, useMemo, useState } from "react"

import { getNetworkSummary } from "@/lib/manager-api"
import type { ManagerNetworkSummaryResponse } from "@/lib/manager-api.types"

import { aggregateNetworkMetrics, type FilteredNetworkMetrics } from "../utils/aggregation"

const autoRefreshMs = 30_000

type NetworkDataState = {
  summary: ManagerNetworkSummaryResponse | null
  loading: boolean
  refreshing: boolean
  error: Error | null
}

export function useNetworkData({ selectedNodes, autoRefresh }: { selectedNodes: number[]; autoRefresh: boolean }) {
  const [state, setState] = useState<NetworkDataState>({ summary: null, loading: true, refreshing: false, error: null })

  const loadNetwork = useCallback(async (refreshing: boolean) => {
    setState((current) => ({ ...current, loading: refreshing ? current.loading : true, refreshing, error: null }))
    try {
      const summary = await getNetworkSummary()
      setState({ summary, loading: false, refreshing: false, error: null })
    } catch (error) {
      setState({ summary: null, loading: false, refreshing: false, error: error instanceof Error ? error : new Error("network summary request failed") })
    }
  }, [])

  useEffect(() => {
    void loadNetwork(false)
  }, [loadNetwork])

  useEffect(() => {
    if (!autoRefresh) return undefined
    const timer = window.setInterval(() => {
      void loadNetwork(true)
    }, autoRefreshMs)
    return () => window.clearInterval(timer)
  }, [autoRefresh, loadNetwork])

  const filteredData = useMemo<FilteredNetworkMetrics | null>(() => {
    if (!state.summary) return null
    return aggregateNetworkMetrics(state.summary, selectedNodes)
  }, [selectedNodes, state.summary])

  return {
    ...state,
    filteredData,
    refresh: () => loadNetwork(true),
  }
}
```

- [ ] **Step 6: Run hook tests to verify GREEN**

Run:

```bash
cd web
bun run test src/pages/network/hooks/use-node-filter.test.tsx src/pages/network/hooks/use-network-data.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit hooks**

Run:

```bash
git add web/src/pages/network/hooks/use-node-filter.ts web/src/pages/network/hooks/use-node-filter.test.tsx web/src/pages/network/hooks/use-network-data.ts web/src/pages/network/hooks/use-network-data.test.tsx
git commit -m "Add network dashboard filter hooks"
```

## Task 4: Build Reusable Filter Bar and Metric Card Shell

**Files:**
- Create: `web/src/pages/network/components/filter-bar.tsx`
- Create: `web/src/pages/network/components/metric-card.tsx`
- Modify: `web/src/pages/network/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Add interaction tests for node/time filters**

Add this test to `web/src/pages/network/page.test.tsx`:

```tsx
test("filters dashboard cards by selected node and keeps time range controls visible", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)
  const user = userEvent.setup()

  renderNetworkPage()

  expect(await screen.findByText("Connection Pool Status")).toBeInTheDocument()
  await user.selectOptions(screen.getByLabelText("Node selector"), ["2"])
  await user.selectOptions(screen.getByLabelText("Time range"), "15m")

  expect(screen.getByLabelText("Time range")).toHaveValue("15m")
  expect(screen.getByText("Selected nodes: node-2")).toBeInTheDocument()
  expect(screen.getByText("Cluster: 1/2")).toBeInTheDocument()
  expect(screen.getByText("Data Plane: 2/1")).toBeInTheDocument()
})
```

Expected failure: filter controls/components do not exist yet.

- [ ] **Step 2: Add new i18n keys**

In `web/src/i18n/messages/en.ts`, add keys near the existing `network.*` block:

```ts
"network.filter.nodeSelector": "Node selector",
"network.filter.allNodes": "All Nodes",
"network.filter.timeRange": "Time range",
"network.filter.last1m": "Last 1 minute",
"network.filter.last5m": "Last 5 minutes",
"network.filter.last15m": "Last 15 minutes",
"network.filter.autoRefresh": "Auto-refresh 30s",
"network.filter.selectedNodes": "Selected nodes: {value}",
"network.filter.allNodesScope": "All nodes",
"network.filter.localTotalNote": "Current API exposes local-total traffic only; selected node traffic is not inferred.",
"network.card.nodeHealth": "Node Health Status",
"network.card.connectionPool": "Connection Pool Status",
"network.card.rpcLatency": "RPC Call Latency",
"network.card.rpcSuccess": "RPC Success Rate",
"network.card.traffic": "Network Traffic",
"network.card.errors": "Network Errors",
"network.card.messageTypes": "Message Type Distribution",
"network.card.events": "Recent Events",
"network.card.viewDetails": "View details",
"network.card.viewAllEvents": "View all events",
"network.card.totalNodes": "Total: {count}",
"network.card.totalCalls": "Total Calls: {count}",
"network.card.failures": "Failures: {count}",
"network.card.totalMessages": "Total Messages: {count}",
"network.card.totalTypes": "Total Types: {count}",
"network.card.noEvents": "No recent network events",
"network.card.noMessageTypes": "No message type samples",
"network.detail.rawJson": "Raw JSON",
"network.detail.relatedConfig": "Related Config",
"network.detail.serviceBreakdown": "Service Breakdown",
```

Add matching `zh-CN` translations, including `"network.card.nodeHealth": "节点健康状态"` for the shell test.

- [ ] **Step 3: Implement `MetricCard` shell**

Create `web/src/pages/network/components/metric-card.tsx`:

```tsx
import type { KeyboardEvent, ReactNode } from "react"

import { cn } from "@/lib/cn"
import type { NetworkAlertLevel } from "../utils/aggregation"

type MetricLabel = { label: string; value: string | number }

export type MetricCardProps = {
  title: string
  primaryMetric: string | number
  chart: ReactNode
  labels?: MetricLabel[]
  alertLevel?: NetworkAlertLevel
  onClick?: () => void
  actionLabel?: string
}

export function MetricCard({ title, primaryMetric, chart, labels = [], alertLevel = "none", onClick, actionLabel }: MetricCardProps) {
  const interactiveProps = onClick
    ? { role: "button", tabIndex: 0, onClick, onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault()
        onClick()
      }
    } }
    : {}

  return (
    <div
      aria-label={actionLabel ? `${title}. ${actionLabel}` : title}
      className={cn(
        "group rounded-xl border bg-card p-4 text-card-foreground transition hover:-translate-y-0.5 hover:shadow-sm",
        onClick && "cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        alertLevel === "warning" && "border-amber-500/40 bg-amber-500/5",
        alertLevel === "danger" && "border-destructive/40 bg-destructive/5",
      )}
      {...interactiveProps}
    >
      <div className={cn("text-xs font-semibold uppercase tracking-[0.16em] text-muted-foreground", alertLevel === "danger" && "text-destructive")}>{title}</div>
      <div className="mt-3 text-3xl font-semibold tracking-tight text-foreground">{primaryMetric}</div>
      <div className="mt-4 min-h-32">{chart}</div>
      {labels.length > 0 ? (
        <div className="mt-4 flex flex-wrap gap-x-4 gap-y-2 text-xs text-muted-foreground">
          {labels.map((item) => <span key={item.label}>{item.label}: <span className="font-medium text-foreground">{item.value}</span></span>)}
        </div>
      ) : null}
    </div>
  )
}
```

If `@/lib/cn` does not export the expected helper in this codebase version, use `@/lib/utils` consistently with existing UI components.

- [ ] **Step 4: Implement `FilterBar`**

Create `web/src/pages/network/components/filter-bar.tsx`:

```tsx
import type { NetworkNodeOption, NetworkTimeRange } from "../utils/aggregation"
import { Button } from "@/components/ui/button"

type FilterBarProps = {
  nodeOptions: NetworkNodeOption[]
  selectedNodes: number[]
  timeRange: NetworkTimeRange
  autoRefresh: boolean
  refreshing: boolean
  scopeBadges: string[]
  onSelectedNodesChange: (nodeIds: number[]) => void
  onTimeRangeChange: (range: NetworkTimeRange) => void
  onAutoRefreshToggle: () => void
  onRefresh: () => void
  labels: {
    nodeSelector: string
    allNodes: string
    timeRange: string
    last1m: string
    last5m: string
    last15m: string
    autoRefresh: string
    refresh: string
    refreshing: string
  }
}

export function FilterBar({ nodeOptions, selectedNodes, timeRange, autoRefresh, refreshing, scopeBadges, onSelectedNodesChange, onTimeRangeChange, onAutoRefreshToggle, onRefresh, labels }: FilterBarProps) {
  return (
    <section className="rounded-xl border border-border bg-card p-4 shadow-none">
      <div className="grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_auto_auto] lg:items-end">
        <label className="grid gap-1 text-sm font-medium text-foreground">
          {labels.nodeSelector}
          <select
            aria-label={labels.nodeSelector}
            className="min-h-9 rounded-lg border border-border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            multiple
            value={selectedNodes.map(String)}
            onChange={(event) => {
              const values = Array.from(event.currentTarget.selectedOptions).map((option) => Number(option.value)).filter(Boolean)
              onSelectedNodesChange(values)
            }}
          >
            <option value="">{labels.allNodes}</option>
            {nodeOptions.map((node) => <option key={node.nodeId} value={node.nodeId}>{node.label}</option>)}
          </select>
        </label>
        <label className="grid gap-1 text-sm font-medium text-foreground">
          {labels.timeRange}
          <select
            aria-label={labels.timeRange}
            className="h-9 rounded-lg border border-border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            value={timeRange}
            onChange={(event) => onTimeRangeChange(event.currentTarget.value as NetworkTimeRange)}
          >
            <option value="1m">{labels.last1m}</option>
            <option value="5m">{labels.last5m}</option>
            <option value="15m">{labels.last15m}</option>
          </select>
        </label>
        <Button onClick={onRefresh} size="sm" variant="outline">{refreshing ? labels.refreshing : labels.refresh}</Button>
        <label className="flex h-9 items-center gap-2 rounded-lg border border-border bg-background px-3 text-sm text-foreground">
          <input checked={autoRefresh} onChange={onAutoRefreshToggle} type="checkbox" />
          {labels.autoRefresh}
        </label>
      </div>
      <div className="mt-3 flex flex-wrap gap-2 text-xs text-muted-foreground">
        {scopeBadges.map((badge) => <span className="rounded-md border border-border bg-background px-3 py-1.5" key={badge}>{badge}</span>)}
      </div>
    </section>
  )
}
```

Implementation note: native multi-select is acceptable for MVP tests and keeps dependencies minimal. A richer dropdown can be polished later.

- [ ] **Step 5: Run focused page tests to verify expected failures only**

Run:

```bash
cd web
bun run test src/pages/network/page.test.tsx
```

Expected: still FAIL until card components/page are wired, but TypeScript import errors for `FilterBar`/`MetricCard` should be gone once referenced.

## Task 5: Implement Eight Metric Cards

**Files:**
- Create: all card files in `web/src/pages/network/components/*-card.tsx`
- Modify: `web/src/pages/network/page.test.tsx`

- [ ] **Step 1: Add page assertions for primary card metrics**

Add to `web/src/pages/network/page.test.tsx`:

```tsx
test("shows primary metrics for the eight dashboard cards", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)

  renderNetworkPage()

  expect(await screen.findByText("Node Health Status")).toBeInTheDocument()
  expect(screen.getByText("2 / 3")).toBeInTheDocument()
  expect(screen.getByText("3 / 4")).toBeInTheDocument()
  expect(screen.getByText("7 ms")).toBeInTheDocument()
  expect(screen.getByText("97.5%")).toBeInTheDocument()
  expect(screen.getByText("192 bps")).toBeInTheDocument()
  expect(screen.getByText("6")).toBeInTheDocument()
  expect(screen.getByText("channel_long_poll_fetch")).toBeInTheDocument()
  expect(screen.getByText("1 event")).toBeInTheDocument()
})
```

Adjust the rate expectation if the formatter chooses a different `bps`/byte-rate display, but keep the test specific enough to catch a missing traffic primary metric.

- [ ] **Step 2: Implement `NodeHealthCard`**

Use a donut chart with `PieChart`, `Pie`, `Cell`, `LabelList`, and `ChartContainer`. Primary metric is `${alive} / ${total}` where `total = alive + suspect + dead + draining`. Labels are alive/suspect/dead/draining counts. Alert `danger` when dead > 0 and `warning` when suspect > 0.

- [ ] **Step 3: Implement `ConnectionPoolCard`**

Use vertical `BarChart` rows:

```ts
const rows = [
  { label: intl.formatMessage({ id: "network.peer.clusterPool" }), active: metrics.pools.cluster.active, idle: metrics.pools.cluster.idle },
  { label: intl.formatMessage({ id: "network.peer.dataPlanePool" }), active: metrics.pools.dataPlane.active, idle: metrics.pools.dataPlane.idle },
]
```

Primary metric is cluster `active / idle` in all-nodes mode to match the headline, and selected-node aggregated cluster `active / idle` in selected mode. Bottom labels must include `Cluster: active/idle`, `Data Plane: active/idle`, and `Total: active+idle`.

- [ ] **Step 4: Implement `RpcLatencyCard`**

Use `LineChart` or `AreaChart` with P95 history. Current history does not include percentiles, so use one snapshot point from aggregated `latency` when no percentile history exists. Primary metric is `formatLatency(intl, metrics.latency.p95Ms)`. Labels are P50/P95/P99.

- [ ] **Step 5: Implement `RpcSuccessCard`**

Use stacked area/bar chart for success, failures, and expected timeouts. Primary metric is `formatPercent(intl, metrics.rpc.successRate)`. Labels are total calls, success, and failures.

- [ ] **Step 6: Implement `TrafficCard`**

Use dual-line area chart over `metrics.history.traffic` when available; otherwise use a snapshot row. Primary metric is current TX+RX rate. If selected nodes are active and `metrics.traffic.isFilteredByNode` is false, render the local-total note from i18n.

- [ ] **Step 7: Implement `ErrorsCard`**

Use stacked bar chart with dial/queue/timeout counts. Primary metric is `metrics.errors.total`. Labels are dial errors, queue full, and timeouts. Alert level comes from `metrics.errors.alertLevel`.

- [ ] **Step 8: Implement `MessageTypesCard`**

Group `metrics.traffic.messageTypes` by `message_type`, sum TX/RX bytes, sort by total bytes descending, take top five, and render a horizontal bar chart. Primary metric is the top message type name or "No samples". Labels include total types and total messages/bytes where available. Do not treat RX and TX rows as separate message types.

- [ ] **Step 9: Implement `EventsCard`**

Render latest three `metrics.events` sorted newest first. Primary metric is `"{count} event"`/`"{count} events"` for the current one-minute payload. Use `StatusBadge` for severity when possible; otherwise render a small bordered severity pill.

- [ ] **Step 10: Run card/page tests**

Run:

```bash
cd web
bun run test src/pages/network/page.test.tsx
```

Expected: still may FAIL until the page composition and i18n wiring are complete, but card component TypeScript should compile.

## Task 6: Compose New Network Page and Detail Drawer

**Files:**
- Replace: `web/src/pages/network/page.tsx`
- Create: `web/src/pages/network/components/detail-drawer.tsx`
- Modify: `web/src/pages/network/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Add detail drawer test**

Add to `web/src/pages/network/page.test.tsx`:

```tsx
test("opens a detail drawer from metric cards", async () => {
  getNetworkSummaryMock.mockResolvedValue(networkSummaryFixture)
  const user = userEvent.setup()

  renderNetworkPage()

  await user.click(await screen.findByLabelText(/RPC Call Latency/i))

  expect(screen.getByRole("dialog")).toBeInTheDocument()
  expect(screen.getByText("RPC Call Latency Details")).toBeInTheDocument()
  expect(screen.getByText("Service Breakdown")).toBeInTheDocument()
  expect(screen.getAllByText("channel_long_poll_fetch").length).toBeGreaterThan(0)
})
```

- [ ] **Step 2: Implement `DetailDrawer`**

Create `web/src/pages/network/components/detail-drawer.tsx` using existing `Sheet` components:

```tsx
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import type { FilteredNetworkMetrics } from "../utils/aggregation"

export type NetworkDetailKind = "health" | "pools" | "latency" | "success" | "traffic" | "errors" | "messageTypes" | "events"

export function DetailDrawer({ kind, open, onOpenChange, metrics, title }: { kind: NetworkDetailKind | null; open: boolean; onOpenChange: (open: boolean) => void; metrics: FilteredNetworkMetrics | null; title: string }) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-2xl" side="right">
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
          <SheetDescription>Expanded network metric details for the current filter scope.</SheetDescription>
        </SheetHeader>
        {!metrics || !kind ? null : <div className="grid gap-4 p-4">{/* render kind-specific details */}</div>}
      </SheetContent>
    </Sheet>
  )
}
```

Fill kind-specific content with:

- `latency` and `success`: service breakdown table using `metrics.services`.
- `traffic`: message type rows and local-total caveat.
- `events`: full event list.
- `pools`: selected peer pool rows.
- `health`: health distribution rows.
- `errors`: dial/queue/timeout/remote totals.
- A collapsed/preformatted raw JSON section for `metrics` is acceptable for MVP if the full bespoke table is not available for every kind.

- [ ] **Step 3: Replace `NetworkPage` with new composition root**

In `web/src/pages/network/page.tsx`, import the hooks/cards and render:

```tsx
export function NetworkPage() {
  const intl = useIntl()
  const filter = useNodeFilter()
  const network = useNetworkData({ selectedNodes: filter.selectedNodes, autoRefresh: filter.autoRefresh })
  const [detailKind, setDetailKind] = useState<NetworkDetailKind | null>(null)

  const summary = network.summary
  const metrics = network.filteredData
  const errorKind = mapErrorKind(network.error)

  return (
    <PageContainer>
      <PageHeader title={intl.formatMessage({ id: "network.title" })} description={intl.formatMessage({ id: "network.description" })} />
      {network.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "network.title" })} description={intl.formatMessage({ id: "network.loading" })} /> : null}
      {!network.loading && network.error ? <ResourceState kind={errorKind} onRetry={() => { void network.refresh() }} title={resourceTitle(intl, network.error)} /> : null}
      {!network.loading && !network.error && summary && metrics ? (
        <>
          <FilterBar
            autoRefresh={filter.autoRefresh}
            nodeOptions={metrics.nodeOptions}
            onAutoRefreshToggle={filter.toggleAutoRefresh}
            onRefresh={() => { void network.refresh() }}
            onSelectedNodesChange={filter.setSelectedNodes}
            onTimeRangeChange={filter.setTimeRange}
            refreshing={network.refreshing}
            scopeBadges={scopeBadges(intl, summary, metrics, filter.timeRange)}
            selectedNodes={filter.selectedNodes}
            timeRange={filter.timeRange}
            labels={filterLabels(intl)}
          />
          <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            <NodeHealthCard metrics={metrics} onClick={() => setDetailKind("health")} />
            <ConnectionPoolCard metrics={metrics} onClick={() => setDetailKind("pools")} />
            <RpcLatencyCard metrics={metrics} onClick={() => setDetailKind("latency")} />
            <RpcSuccessCard metrics={metrics} onClick={() => setDetailKind("success")} />
            <TrafficCard metrics={metrics} onClick={() => setDetailKind("traffic")} timeRange={filter.timeRange} />
            <ErrorsCard metrics={metrics} onClick={() => setDetailKind("errors")} />
            <MessageTypesCard metrics={metrics} onClick={() => setDetailKind("messageTypes")} />
            <EventsCard metrics={metrics} onClick={() => setDetailKind("events")} />
          </section>
          <DetailDrawer kind={detailKind} metrics={metrics} onOpenChange={(open) => { if (!open) setDetailKind(null) }} open={detailKind !== null} title={detailTitle(intl, detailKind)} />
        </>
      ) : null}
    </PageContainer>
  )
}
```

Keep `mapErrorKind` and `resourceTitle` from the legacy page or move them into a local helper in `page.tsx`.

- [ ] **Step 4: Add small helper functions in `page.tsx`**

Add helpers for labels and badges:

```tsx
function filterLabels(intl: IntlShape) {
  return {
    nodeSelector: intl.formatMessage({ id: "network.filter.nodeSelector" }),
    allNodes: intl.formatMessage({ id: "network.filter.allNodes" }),
    timeRange: intl.formatMessage({ id: "network.filter.timeRange" }),
    last1m: intl.formatMessage({ id: "network.filter.last1m" }),
    last5m: intl.formatMessage({ id: "network.filter.last5m" }),
    last15m: intl.formatMessage({ id: "network.filter.last15m" }),
    autoRefresh: intl.formatMessage({ id: "network.filter.autoRefresh" }),
    refresh: intl.formatMessage({ id: "common.refresh" }),
    refreshing: intl.formatMessage({ id: "common.refreshing" }),
  }
}
```

`scopeBadges()` should include local node ID, controller leader ID, generated timestamp, selected node label, time range, and single-node cluster badge when `summary.headline.remote_peers === 0`.

- [ ] **Step 5: Run network page tests to verify GREEN**

Run:

```bash
cd web
bun run test src/pages/network/page.test.tsx
```

Expected: PASS after updating any brittle old assertions to the new dashboard behavior.

- [ ] **Step 6: Commit page composition**

Run:

```bash
git add web/src/pages/network/page.tsx web/src/pages/network/legacy-page.tsx web/src/pages/network/components web/src/pages/network/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "Redesign network page as metric card dashboard"
```

## Task 7: Refresh Remaining Tests and Rollback Flag

**Files:**
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/lib/env.ts` if adding flag
- Modify: `web/src/lib/manager-api.test.ts` if adding flag
- Modify: `web/README.md` if adding a public Vite flag

- [ ] **Step 1: Decide whether to wire the rollback flag**

If the team wants runtime rollback, add this to `web/src/lib/env.ts`:

```ts
export function isNetworkPageV2Enabled() {
  return import.meta.env.VITE_NETWORK_PAGE_V2_ENABLED !== "false"
}
```

Then in `web/src/pages/network/page.tsx`, render `NetworkLegacyPage` when false:

```tsx
export function NetworkPage() {
  if (!isNetworkPageV2Enabled()) return <NetworkLegacyPage />
  return <NetworkDashboardPage />
}
```

If the team does not need runtime rollback, keep `legacy-page.tsx` only as source-level rollback and skip this step.

- [ ] **Step 2: Add env helper tests if the flag is wired**

In `web/src/lib/manager-api.test.ts` or a new `web/src/lib/env.test.ts`, cover default true and explicit false:

```ts
vi.stubEnv("VITE_NETWORK_PAGE_V2_ENABLED", "false")
expect(isNetworkPageV2Enabled()).toBe(false)
```

- [ ] **Step 3: Update README if the flag is public**

If the flag is wired, add to `web/README.md`:

```md
- `VITE_NETWORK_PAGE_V2_ENABLED=false` temporarily rolls `/network` back to the legacy observability page.
```

- [ ] **Step 4: Run shell and env tests**

Run:

```bash
cd web
bun run test src/pages/page-shells.test.tsx src/lib/manager-api.test.ts
```

Expected: PASS. If a new env test file was created, include it in the command.

- [ ] **Step 5: Commit test/rollback updates**

Run:

```bash
git add web/src/pages/page-shells.test.tsx web/src/lib/env.ts web/src/lib/manager-api.test.ts web/README.md
git commit -m "Cover network dashboard shell and rollback flag"
```

Only include files that actually changed.

## Task 8: Focused Verification and Cleanup

**Files:**
- Potentially modified generated build artifacts; restore if unintended.

- [ ] **Step 1: Run all network-focused web tests**

Run:

```bash
cd web
bun run test src/pages/network/page.test.tsx src/pages/network/utils/aggregation.test.ts src/pages/network/hooks/use-node-filter.test.tsx src/pages/network/hooks/use-network-data.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 2: Run all web tests**

Run:

```bash
cd web
bun run test
```

Expected: PASS.

- [ ] **Step 3: Run production build**

Run:

```bash
cd web
bun run build
```

Expected: PASS. Existing Vite chunk-size warnings are acceptable if the build exits successfully.

- [ ] **Step 4: Inspect working tree for accidental generated output**

Run:

```bash
git status --short
```

Expected: only source/test/docs files related to the network dashboard should be changed. Do not commit `web/dist` or other generated build outputs unless the repository already tracks and intentionally updates them.

- [ ] **Step 5: Run root unit tests only if frontend changes touched shared API contracts**

Most work is under `web/`, so Go tests are not required for pure frontend changes. If API types or backend endpoints were changed despite this plan, run:

```bash
go test ./internal/... ./pkg/...
```

Expected: PASS.

- [ ] **Step 6: Final commit if prior task commits were skipped**

Run:

```bash
git add web/src/pages/network web/src/pages/page-shells.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/lib/env.ts web/README.md
git commit -m "Redesign network observability dashboard"
```

Only include files that actually changed and do not commit unrelated user changes.

## Manual Testing Checklist

- [ ] `/network` loads with the new filter bar and eight metric cards.
- [ ] All Nodes view shows headline health, pool, error, RPC, traffic, message type, and event metrics.
- [ ] Selecting one remote node changes connection pool, RPC, errors, events, and health counts to selected-node values.
- [ ] Selecting multiple remote nodes aggregates counts without duplicating labels.
- [ ] Time range selection persists in URL and remains visible after refresh.
- [ ] Manual refresh calls the summary endpoint again.
- [ ] Auto-refresh calls the summary endpoint every 30 seconds and can be disabled.
- [ ] Traffic card shows the local-total caveat when selected-node filtering is active.
- [ ] Alert states trigger for pool utilization > 80%, P95 > 100ms, success rate < 95%, and errors > 10.
- [ ] Detail drawer opens from each card, traps focus, closes with the close button/Escape, and works on mobile width.
- [ ] Single-node cluster still renders as a healthy deployment shape with the "single-node cluster" badge.
- [ ] Forbidden and unavailable API errors still map to `ResourceState` correctly.

## Notes for Future Backend Enhancement

- Add `/manager/network/summary?window=1m|5m|15m` only after this frontend MVP lands; then update `getNetworkSummary` to accept a `window` option and pass `filter.timeRange` from `useNetworkData`.
- Add `/manager/network/summary?nodes=1,2,3` only if server-side filtering becomes necessary for large clusters.
- Add peer traffic breakdown before claiming selected-node TX/RX is filtered.
