# Dashboard Split Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the generic Overview tab and deliver separate Cluster Ops and Business dashboards, with cluster-internal RPC/transport metrics on the Cluster dashboard and client-facing message metrics on the Business dashboard.

**Architecture:** Add two focused dashboard page modules with pure view-models and small presentational components. Reuse existing manager APIs (`overview`, `nodes`, `channel-cluster/summary`, `network/summary`, `dashboard/metrics`, `monitor/metrics`) and route legacy `/dashboard` and `/monitor` bookmarks into the new section-specific pages.

**Tech Stack:** React 19, TypeScript, react-router-dom, react-intl, Recharts, Tailwind CSS 4, Vitest, Testing Library, existing manager API client.

---

## Safety Notes

- Do not modify unrelated current work in `web/src/pages/nodes/*`, `web/src/pages/slots/*`, `web/dist/index.html`, or any other dirty file unless this task explicitly needs it.
- Before implementing, run `git status --short` and preserve user changes. If unexpected edits appear in files you are about to touch, stop and ask.
- The current repository uses “单节点集群” wording for deployment shape. Keep that wording in Chinese copy.

## File Map

### Create

- `web/src/pages/cluster-dashboard/page.tsx` — fetches authoritative cluster data, composes the Cluster Ops dashboard, handles partial network failures.
- `web/src/pages/cluster-dashboard/view-model.ts` — pure cluster derivations: verdict, incidents, topology rows, RPC/traffic metrics, source labels.
- `web/src/pages/cluster-dashboard/view-model.test.ts` — unit tests for cluster calculations.
- `web/src/pages/cluster-dashboard/page.test.tsx` — integration tests for fetch, render, refresh, fatal and partial errors.
- `web/src/pages/cluster-dashboard/components/cluster-health-hero.tsx` — cluster verdict hero with refresh.
- `web/src/pages/cluster-dashboard/components/cluster-metric-strip.tsx` — node/slot/task/channel/RPC/internal-message cards.
- `web/src/pages/cluster-dashboard/components/cluster-link-trends.tsx` — internal transport and RPC trend chart.
- `web/src/pages/cluster-dashboard/components/cluster-replication-health.tsx` — slot/channel health donut and links.
- `web/src/pages/cluster-dashboard/components/cluster-incident-list.tsx` — cluster anomalies and network events.
- `web/src/pages/cluster-dashboard/components/cluster-topology-watermarks.tsx` — node watermarks and per-node RPC summary.
- `web/src/pages/business-dashboard/page.tsx` — fetches business metrics and optional management summaries, composes dashboard.
- `web/src/pages/business-dashboard/view-model.ts` — pure business derivations: metric summaries, verdict, risks, deterministic sample fallbacks.
- `web/src/pages/business-dashboard/view-model.test.ts` — unit tests for business calculations.
- `web/src/pages/business-dashboard/page.test.tsx` — integration tests for render, refresh, errors, sample badges, links.
- `web/src/pages/business-dashboard/components/business-health-hero.tsx` — business verdict hero with refresh.
- `web/src/pages/business-dashboard/components/business-metric-strip.tsx` — send/deliver/latency/failure/connection/retry/fan-out cards.
- `web/src/pages/business-dashboard/components/business-message-trends.tsx` — larger business trend charts.
- `web/src/pages/business-dashboard/components/business-risk-list.tsx` — retry/failure/latency/delivery gap risks.
- `web/src/pages/business-dashboard/components/business-entry-cards.tsx` — users/channels/messages/system-users/live-monitor links with source badges.

### Modify

- `web/src/lib/navigation.ts` — remove Overview section, add Cluster and Business dashboard items, update default active fallback and legacy redirects.
- `web/src/app/router.tsx` — add `/cluster/dashboard`, `/business/dashboard`, `/business/monitor`; redirect `/`, `/dashboard`, `/monitor`.
- `web/src/auth/protected-route.tsx` — redirect authenticated `/login` visits to `/cluster/dashboard`.
- `web/src/app/layout/topbar.test.tsx` — assert Overview is removed and Cluster Ops is active.
- `web/src/app/layout/sidebar-nav.test.tsx` — assert new section defaults and sidebar ordering.
- `web/src/app/router.test.tsx` — update auth redirects and legacy redirect expectations.
- `web/src/lib/navigation.test.ts` — update section list, active section, metadata, redirects.
- `web/src/pages/page-shells.test.tsx` — add shell cases for new dashboards and `/business/monitor`; update old route expectations.
- `web/src/i18n/messages/en.ts` — add `clusterDashboard.*`, `businessDashboard.*`, new nav path/title keys; keep old keys only if referenced.
- `web/src/i18n/messages/zh-CN.ts` — Chinese equivalents with “单节点集群” wording.
- `web/README.md` — update page/API matrix and legacy redirects.

### Optional Cleanup After Green Tests

- Delete `web/src/pages/dashboard/page.tsx`, `web/src/pages/dashboard/page.test.tsx`, `web/src/pages/dashboard/view-model.ts`, `web/src/pages/dashboard/view-model.test.ts`, `web/src/pages/dashboard/use-dashboard-pulse.ts`, and dashboard-only components after their logic has moved and no imports remain.
- Keep `web/src/pages/monitor/*` for now; it is routed as `/business/monitor`.

---

## Task 1: Navigation And Route Skeletons

**Files:**
- Test: `web/src/lib/navigation.test.ts`
- Test: `web/src/app/router.test.tsx`
- Test: `web/src/app/layout/topbar.test.tsx`
- Test: `web/src/app/layout/sidebar-nav.test.tsx`
- Test: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/auth/protected-route.tsx`
- Create: `web/src/pages/cluster-dashboard/page.tsx`
- Create: `web/src/pages/business-dashboard/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing navigation unit tests**

In `web/src/lib/navigation.test.ts`, update the section test to expect three top-level sections:

```typescript
test("defines the three section-specific top-level sections", () => {
  expect(navigationSections.map((section) => section.id)).toEqual([
    "cluster",
    "business",
    "system",
  ])
})
```

Add metadata expectations:

```typescript
test("adds section dashboard metadata", () => {
  expect(pageMetadata.get("/cluster/dashboard")?.titleMessageId).toBe("nav.clusterDashboard.title")
  expect(pageMetadata.get("/cluster/dashboard")?.pathLabelMessageId).toBe("nav.path.cluster.dashboard")
  expect(pageMetadata.get("/business/dashboard")?.titleMessageId).toBe("nav.businessDashboard.title")
  expect(pageMetadata.get("/business/monitor")?.titleMessageId).toBe("nav.monitor.title")
})
```

Update active-section and redirect tests:

```typescript
test("selects the active section from new nested routes", () => {
  expect(getActiveNavigationSection("/cluster/dashboard")?.id).toBe("cluster")
  expect(getActiveNavigationSection("/business/dashboard")?.id).toBe("business")
  expect(getActiveNavigationSection("/business/monitor")?.id).toBe("business")
  expect(getActiveNavigationSection("/system/connections")?.id).toBe("system")
})

test("maps legacy overview routes to section-specific routes", () => {
  expect(legacyRouteRedirects["/dashboard"]).toBe("/cluster/dashboard")
  expect(legacyRouteRedirects["/monitor"]).toBe("/business/monitor")
  expect(legacyRouteRedirects["/channel-cluster/list"]).toBe("/cluster/channels?tab=list")
})
```

- [ ] **Step 2: Write failing shell/router tests**

Update `web/src/app/router.test.tsx`:

```typescript
test("redirects authenticated /login visits to the cluster dashboard", async () => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })

  render(<AppProviders><RouterProvider router={router} /></AppProviders>)

  expect(await screen.findByRole("heading", { name: "Cluster Dashboard" })).toBeInTheDocument()
  expect(router.state.location.pathname).toBe("/cluster/dashboard")
})

test.each([
  ["/", "/cluster/dashboard"],
  ["/dashboard", "/cluster/dashboard"],
  ["/monitor", "/business/monitor"],
  ["/nodes", "/cluster/nodes"],
  ["/network", "/cluster/diagnostics?tab=network"],
])("redirects legacy %s to %s", async (from, to) => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: [from] })
  render(<AppProviders><RouterProvider router={router} /></AppProviders>)
  await screen.findByRole("main")
  expect(router.state.location.pathname + router.state.location.search).toBe(to)
})
```

Update `web/src/app/layout/topbar.test.tsx` to assert Overview is gone:

```typescript
expect(within(banner).queryByRole("link", { name: "Overview" })).not.toBeInTheDocument()
expect(within(banner).getByRole("link", { name: "Cluster Ops" })).toHaveAttribute("aria-current", "page")
```

Update `web/src/app/layout/sidebar-nav.test.tsx`:

```typescript
test("shows cluster dashboard first in the cluster section", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/dashboard"] })
  render(<AppProviders><RouterProvider router={router} /></AppProviders>)

  const nav = await screen.findByRole("navigation", { name: "Primary navigation" })
  const links = within(nav).getAllByRole("link").map((link) => link.textContent)
  expect(links.slice(0, 3)).toEqual(["Dashboard", "Nodes", "Slots"])
})

test("shows business dashboard and live monitor first in the business section", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/business/dashboard"] })
  render(<AppProviders><RouterProvider router={router} /></AppProviders>)

  const nav = await screen.findByRole("navigation", { name: "Primary navigation" })
  const links = within(nav).getAllByRole("link").map((link) => link.textContent)
  expect(links.slice(0, 3)).toEqual(["Dashboard", "Live Monitor", "Users"])
})
```

Update `web/src/pages/page-shells.test.tsx` route cases to include:

```typescript
["/cluster/dashboard", "Cluster Dashboard", "Internal Link Trends"],
["/business/dashboard", "Business Dashboard", "Business Message Trends"],
["/business/monitor", "Live Monitor", "Message Flow"],
```

Remove direct `/dashboard` and `/monitor` shell render expectations; they are redirect tests now.

- [ ] **Step 3: Run tests to verify RED**

Run:

```bash
cd web && yarn test src/lib/navigation.test.ts src/app/router.test.tsx src/app/layout/topbar.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/page-shells.test.tsx
```

Expected: FAIL because routes, navigation items, placeholder pages, and i18n keys do not exist yet.

- [ ] **Step 4: Add temporary dashboard skeleton pages**

Create `web/src/pages/cluster-dashboard/page.tsx`:

```typescript
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"

export function ClusterDashboardPage() {
  return (
    <PageContainer>
      <PageHeader
        title="Cluster Dashboard"
        description="Cluster health, internal transport, RPC quality, replication, and topology watermarks."
      />
      <SectionCard title="Internal Link Trends">
        <p className="text-sm text-muted-foreground">Cluster dashboard content is loading.</p>
      </SectionCard>
    </PageContainer>
  )
}
```

Create `web/src/pages/business-dashboard/page.tsx`:

```typescript
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"

export function BusinessDashboardPage() {
  return (
    <PageContainer>
      <PageHeader
        title="Business Dashboard"
        description="Client-facing message quality, retry pressure, online usage, and management entry points."
      />
      <SectionCard title="Business Message Trends">
        <p className="text-sm text-muted-foreground">Business dashboard content is loading.</p>
      </SectionCard>
    </PageContainer>
  )
}
```

These are only for route skeletons. Later tasks replace literal copy with i18n and real data.

- [ ] **Step 5: Update navigation and router minimally**

Modify `web/src/lib/navigation.ts`:

- Remove `  overview section from `navigationSections` and the `NavigationSectionId` union.
- Add a dashboard item as the first Cluster section item:

```typescript
{
  href: "/cluster/dashboard",
  titleMessageId: "nav.clusterDashboard.title",
  descriptionMessageId: "nav.clusterDashboard.description",
  pathLabelMessageId: "nav.path.cluster.dashboard",
  icon: LayoutDashboard,
  aliases: ["/dashboard"],
},
```

- Change the Cluster section `href` to `/cluster/dashboard`.
- Add a dashboard item and monitor item as the first Business section items:

```typescript
{
  href: "/business/dashboard",
  titleMessageId: "nav.businessDashboard.title",
  descriptionMessageId: "nav.businessDashboard.description",
  pathLabelMessageId: "nav.path.business.dashboard",
  icon: LayoutDashboard,
},
{
  href: "/business/monitor",
  titleMessageId: "nav.monitor.title",
  descriptionMessageId: "nav.monitor.description",
  pathLabelMessageId: "nav.path.business.monitor",
  icon: Activity,
  aliases: ["/monitor"],
},
```

- Change the Business section `href` to `/business/dashboard`.
- Add legacy redirects:

```typescript
"/dashboard": "/cluster/dashboard",
"/monitor": "/business/monitor",
```

- Change `getActiveNavigationItem` fallback from `/dashboard` to `/cluster/dashboard`.
- Change `getActiveNavigationSection` fallback from `navigationSections[0]` only after verifying `navigationSections[0].id === "cluster"`.

Modify `web/src/app/router.tsx`:

- Import `ClusterDashboardPage` and `BusinessDashboardPage`.
- Change index redirect to `/cluster/dashboard`.
- Remove active `/dashboard` and `/monitor` page routes.
- Add:

```typescript
{ path: "cluster/dashboard", element: <ClusterDashboardPage /> },
{ path: "business/dashboard", element: <BusinessDashboardPage /> },
{ path: "business/monitor", element: <MonitorPage /> },
{ path: "dashboard", element: <Navigate replace to="/cluster/dashboard" /> },
{ path: "monitor", element: <Navigate replace to="/business/monitor" /> },
```

Modify `web/src/auth/protected-route.tsx`:

```typescript
return <Navigate replace to="/cluster/dashboard" />
```

in `PublicOnlyRoute` for authenticated users.

- [ ] **Step 6: Add temporary i18n keys**

Add to `web/src/i18n/messages/en.ts`:

```typescript
"nav.path.cluster.dashboard": "CLUSTER / DASHBOARD",
"nav.path.business.dashboard": "BUSINESS / DASHBOARD",
"nav.path.business.monitor": "BUSINESS / MONITOR",
"nav.clusterDashboard.title": "Dashboard",
"nav.clusterDashboard.description": "Cluster health, internal transport, RPC quality, and replication state.",
"nav.businessDashboard.title": "Dashboard",
"nav.businessDashboard.description": "Message quality, traffic, retry pressure, and business entry points.",
```

Add to `web/src/i18n/messages/zh-CN.ts`:

```typescript
"nav.path.cluster.dashboard": "CLUSTER / DASHBOARD",
"nav.path.business.dashboard": "BUSINESS / DASHBOARD",
"nav.path.business.monitor": "BUSINESS / MONITOR",
"nav.clusterDashboard.title": "仪表盘",
"nav.clusterDashboard.description": "集群健康、内部传输、RPC 质量与复制状态。",
"nav.businessDashboard.title": "仪表盘",
"nav.businessDashboard.description": "消息质量、业务流量、重试压力与管理入口。",
```

Keep `nav.section.overview` temporarily if old tests still reference it, but it should not be used by `navigationSections`.

- [ ] **Step 7: Run tests to verify GREEN**

Run:

```bash
cd web && yarn test src/lib/navigation.test.ts src/app/router.test.tsx src/app/layout/topbar.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS for the updated navigation skeleton.

- [ ] **Step 8: Commit navigation skeleton**

```bash
git add web/src/lib/navigation.ts web/src/app/router.tsx web/src/auth/protected-route.tsx web/src/pages/cluster-dashboard/page.tsx web/src/pages/business-dashboard/page.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/lib/navigation.test.ts web/src/app/router.test.tsx web/src/app/layout/topbar.test.tsx web/src/app/layout/sidebar-nav.test.tsx web/src/pages/page-shells.test.tsx
git commit -m "feat(web): split dashboard navigation"
```

---

## Task 2: Cluster Dashboard View Model

**Files:**
- Create: `web/src/pages/cluster-dashboard/view-model.test.ts`
- Create: `web/src/pages/cluster-dashboard/view-model.ts`
- Optional reference: `web/src/pages/dashboard/view-model.ts`
- Optional reference: `web/src/pages/network/utils/aggregation.ts`

- [ ] **Step 1: Write failing tests for cluster verdict and incidents**

Create `web/src/pages/cluster-dashboard/view-model.test.ts`. Reuse fixtures from `web/src/pages/dashboard/view-model.test.ts`, but import from the new module:

```typescript
import { describe, expect, it } from "vitest"
import type { IntlShape } from "react-intl"
import type {
  ManagerChannelClusterSummaryResponse,
  ManagerNetworkSummaryResponse,
  ManagerNodesResponse,
  ManagerOverviewResponse,
} from "@/lib/manager-api.types"
import {
  buildClusterIncidents,
  buildClusterMetricStrip,
  buildInternalLinkMetrics,
  buildTopologyRows,
  computeClusterVerdict,
} from "./view-model"

const intl = {
  formatMessage: ({ id }: { id: string }, values?: Record<string, unknown>) => `${id}${JSON.stringify(values ?? {})}`,
} as Pick<IntlShape, "formatMessage">
```

Add tests:

```typescript
describe("computeClusterVerdict", () => {
  it("returns healthy with no anomalies", () => {
    expect(computeClusterVerdict(makeOverview(), makeNodes())).toBe("healthy")
  })

  it("returns degraded for slot quorum loss or retrying tasks", () => {
    expect(computeClusterVerdict(makeOverview({ slotsQuorumLost: 1 }), makeNodes())).toBe("degraded")
    expect(computeClusterVerdict(makeOverview({ tasksRetrying: 1 }), makeNodes())).toBe("degraded")
  })

  it("returns critical for failed tasks or restore_failed raft health", () => {
    expect(computeClusterVerdict(makeOverview({ tasksFailed: 1 }), makeNodes())).toBe("critical")
    expect(computeClusterVerdict(makeOverview(), makeNodes([{ raft_health: "restore_failed" }]))).toBe("critical")
  })
})

describe("buildClusterIncidents", () => {
  it("includes slot, task, raft, and network event incidents", () => {
    const incidents = buildClusterIncidents(intl, makeOverviewWithOneRetry(), makeNodes([{ raft_health: "snapshot_required" }]), makeNetworkSummary())

    expect(incidents.map((item) => item.key)).toContain("task-retrying-9-rebalance")
    expect(incidents.some((item) => item.key.startsWith("controller-raft-"))).toBe(true)
    expect(incidents.some((item) => item.key.startsWith("network-event-"))).toBe(true)
  })
})
```

Keep fixture helpers local to the test file. The helpers should return minimal valid `ManagerOverviewResponse`, `ManagerNodesResponse`, `ManagerChannelClusterSummaryResponse`, and `ManagerNetworkSummaryResponse` objects.

- [ ] **Step 2: Write failing tests for RPC and internal message derivation**

Add tests:

```typescript
describe("buildInternalLinkMetrics", () => {
  it("derives rpc error rate without counting expected long-poll expiries", () => {
    const metrics = buildInternalLinkMetrics(makeNetworkSummary({
      rpc: [{ calls_1m: 100, success_1m: 90, expected_timeout_1m: 8, timeout_1m: 2 }],
      historyRpc: [{ at: "2026-05-15T08:00:00Z", calls: 100, success: 90, errors: 2, expected_timeouts: 8 }],
    }))

    expect(metrics.rpcErrorRate.value).toBeCloseTo(2.17, 1)
    expect(metrics.rpcErrorRate.source).toBe("derived")
    expect(metrics.rpcCallsPerSecond.value).toBeCloseTo(100 / 60, 2)
  })

  it("handles zero rpc calls", () => {
    const metrics = buildInternalLinkMetrics(makeNetworkSummary({ rpc: [] }))
    expect(metrics.rpcErrorRate.value).toBe(0)
    expect(metrics.rpcErrorRate.formatted).toBe("0.00%")
  })

  it("derives internal message rate from rpc history when no direct metric exists", () => {
    const metrics = buildInternalLinkMetrics(makeNetworkSummary({
      historyRpc: [
        { at: "2026-05-15T08:00:00Z", calls: 30, success: 30, errors: 0, expected_timeouts: 0 },
        { at: "2026-05-15T08:01:00Z", calls: 90, success: 90, errors: 0, expected_timeouts: 0 },
      ],
    }))

    expect(metrics.internalMessagesPerSecond.value).toBeCloseTo(1.5, 2)
    expect(metrics.internalMessagesPerSecond.source).toBe("derived")
  })
})
```

- [ ] **Step 3: Write failing tests for metric strip and topology rows**

Add tests:

```typescript
describe("buildClusterMetricStrip", () => {
  it("returns node, slot, task, channel, internal message, rpc error, and rpc latency cards", () => {
    const strip = buildClusterMetricStrip(makeOverview(), makeChannelCluster(), buildInternalLinkMetrics(makeNetworkSummary()))
    expect(strip.map((item) => item.key)).toEqual([
      "nodes",
      "slots",
      "tasks",
      "channels",
      "internalMessages",
      "rpcErrorRate",
      "rpcLatency",
    ])
  })
})

describe("buildTopologyRows", () => {
  it("maps node watermarks and attaches per-node rpc summaries", () => {
    const rows = buildTopologyRows(makeNodes([{ node_id: 2, raft_health: "healthy", applied_index: 42 }]), makeNetworkSummary())
    expect(rows[0]).toMatchObject({ nodeId: 2, appliedIndex: 42, rpcErrorRate: expect.any(Number) })
  })
})
```

- [ ] **Step 4: Run tests to verify RED**

Run:

```bash
cd web && yarn test src/pages/cluster-dashboard/view-model.test.ts
```

Expected: FAIL because `view-model.ts` is missing.

- [ ] **Step 5: Implement cluster view-model functions**

Create `web/src/pages/cluster-dashboard/view-model.ts` with these exported types and functions:

```typescript
import type { IntlShape } from "react-intl"
import type {
  ManagerChannelClusterSummaryResponse,
  ManagerNetworkEvent,
  ManagerNetworkRPCHistoryPoint,
  ManagerNetworkRPCService,
  ManagerNetworkSummaryResponse,
  ManagerNetworkTrafficHistoryPoint,
  ManagerNodesResponse,
  ManagerOverviewResponse,
} from "@/lib/manager-api.types"

export type MetricSource = "real" | "derived" | "sample"
export type ClusterVerdict = "healthy" | "degraded" | "critical"

export type ClusterMetricValue = {
  key: string
  labelId: string
  value: number
  formatted: string
  detail: string
  tone: "default" | "warning" | "danger"
  source: MetricSource
  href?: string
}

export type ClusterIncident = {
  key: string
  severity: "critical" | "warning"
  title: string
  detail: string
  href: string
  ariaLabel: string
}

export type ClusterLinkMetrics = {
  internalMessagesPerSecond: ClusterMetricValue
  rpcCallsPerSecond: ClusterMetricValue
  rpcErrorRate: ClusterMetricValue
  rpcLatencyP95: ClusterMetricValue
  rpcInflight: ClusterMetricValue
  queueFull: ClusterMetricValue
  timeouts: ClusterMetricValue
  trafficSeries: Array<{ at: string; txBytes: number; rxBytes: number }>
  rpcSeries: Array<{ at: string; calls: number; errors: number; expectedTimeouts: number }>
}

export type ClusterTopologyRow = {
  nodeId: number
  name: string
  status: string
  isLocal: boolean
  role: string
  slotsCount: number
  leaderCount: number
  raftHealth: string
  hasWatermark: boolean
  firstIndex: number
  appliedIndex: number
  snapshotIndex: number
  rpcErrorRate: number | null
  rpcP95Ms: number | null
  href: string
}

const CRITICAL_RAFT_STATES = new Set(["restore_failed"])
const DEGRADED_RAFT_STATES = new Set(["snapshot_required", "snapshot_transferring", "append_catchup", "compaction_degraded"])

export function computeClusterVerdict(overview: ManagerOverviewResponse, nodes: ManagerNodesResponse): ClusterVerdict {
  if (overview.tasks.failed > 0) return "critical"
  if (nodes.items.some((node) => node.controller.raft_health && CRITICAL_RAFT_STATES.has(node.controller.raft_health))) return "critical"
  if (overview.slots.quorum_lost > 0 || overview.slots.leader_missing > 0 || overview.tasks.retrying > 0) return "degraded"
  if (nodes.items.some((node) => node.controller.raft_health && DEGRADED_RAFT_STATES.has(node.controller.raft_health))) return "degraded"
  return "healthy"
}
```

Then implement:

- `buildClusterIncidents(intl, overview, nodes, network?)`
  - Copy the existing slot/task/raft incident logic from `web/src/pages/dashboard/view-model.ts`.
  - Change i18n ids from `dashboard.incidents.*` to `clusterDashboard.incidents.*`.
  - Use new controller links: `/cluster/diagnostics?tab=controller-logs&node_id=${id}`.
  - Add network events:

```typescript
function networkEventIncident(intl: Pick<IntlShape, "formatMessage">, event: ManagerNetworkEvent, index: number): ClusterIncident {
  const title = intl.formatMessage({ id: "clusterDashboard.incidents.networkEventTitle" }, { kind: event.kind, node: event.target_node })
  return {
    key: `network-event-${event.at}-${index}`,
    severity: event.severity === "error" || event.severity === "critical" ? "critical" : "warning",
    title,
    detail: event.message || event.service,
    href: "/cluster/diagnostics?tab=network",
    ariaLabel: title,
  }
}
```

- `buildInternalLinkMetrics(network?)`
  - If `network` is missing, return zero values with `source: "sample"` and empty series.
  - Compute abnormal failures from each service: `timeout_1m + queue_full_1m + remote_error_1m + other_error_1m`.
  - Compute completed calls: `Math.max(calls - expectedTimeouts, 0)`.
  - `rpcErrorRate = completedCalls > 0 ? failures / completedCalls * 100 : 0`.
  - `rpcCallsPerSecond = calls / 60` using the current one-minute service counters.
  - `internalMessagesPerSecond` uses the latest `history.rpc.calls / history.step_seconds` when history exists; otherwise `rpcCallsPerSecond`.
  - `rpcLatencyP95` is a weighted average of service `p95_ms`, weighted by `Math.max(calls_1m, 1)`.
  - `trafficSeries` maps `history.traffic` to `{ at, txBytes, rxBytes }`.
  - `rpcSeries` maps `history.rpc` to `{ at, calls, errors, expectedTimeouts }`.

- `buildClusterMetricStrip(overview, channelCluster, linkMetrics)`
  - Return seven `ClusterMetricValue` objects keyed `nodes`, `slots`, `tasks`, `channels`, `internalMessages`, `rpcErrorRate`, `rpcLatency`.
  - Use `source: "real"` for overview/channel counts and the source from link metrics for derived values.

- `buildTopologyRows(nodes, network?)`
  - Copy current topology row mapping.
  - Add per-node RPC summary from `network.peers.find(peer.node_id === node.node_id)`.
  - Link rows to `/cluster/diagnostics?tab=controller-logs&node_id=${node_id}`.

- [ ] **Step 6: Run tests to verify GREEN**

Run:

```bash
cd web && yarn test src/pages/cluster-dashboard/view-model.test.ts
```

Expected: PASS.

- [ ] **Step 7: Commit cluster view model**

```bash
git add web/src/pages/cluster-dashboard/view-model.ts web/src/pages/cluster-dashboard/view-model.test.ts
git commit -m "feat(web): add cluster dashboard view model"
```

---

## Task 3: Cluster Dashboard UI And Data Loading

**Files:**
- Test: `web/src/pages/cluster-dashboard/page.test.tsx`
- Modify: `web/src/pages/cluster-dashboard/page.tsx`
- Create: `web/src/pages/cluster-dashboard/components/cluster-health-hero.tsx`
- Create: `web/src/pages/cluster-dashboard/components/cluster-metric-strip.tsx`
- Create: `web/src/pages/cluster-dashboard/components/cluster-link-trends.tsx`
- Create: `web/src/pages/cluster-dashboard/components/cluster-replication-health.tsx`
- Create: `web/src/pages/cluster-dashboard/components/cluster-incident-list.tsx`
- Create: `web/src/pages/cluster-dashboard/components/cluster-topology-watermarks.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing cluster page tests**

Create `web/src/pages/cluster-dashboard/page.test.tsx` with API mocks:

```typescript
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { ClusterDashboardPage } from "./page"

const getOverviewMock = vi.fn()
const getNodesMock = vi.fn()
const getTasksMock = vi.fn()
const getChannelClusterSummaryMock = vi.fn()
const getNetworkSummaryMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getOverview: (...args: unknown[]) => getOverviewMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getTasks: (...args: unknown[]) => getTasksMock(...args),
    getChannelClusterSummary: (...args: unknown[]) => getChannelClusterSummaryMock(...args),
    getNetworkSummary: (...args: unknown[]) => getNetworkSummaryMock(...args),
  }
})
```

Add tests:

```typescript
test("renders cluster health and internal link metrics", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getNodesMock.mockResolvedValue(nodesFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  getChannelClusterSummaryMock.mockResolvedValue(channelClusterFixture)
  getNetworkSummaryMock.mockResolvedValue(networkFixture)

  renderClusterDashboard()

  expect(await screen.findByRole("heading", { name: "Cluster Dashboard" })).toBeInTheDocument()
  expect(screen.getByText("Cluster degraded")).toBeInTheDocument()
  expect(screen.getByText("Internal message/s")).toBeInTheDocument()
  expect(screen.getByText("RPC error rate")).toBeInTheDocument()
  expect(screen.getByText("Internal Link Trends")).toBeInTheDocument()
  expect(screen.getByText("Slot & channel replication")).toBeInTheDocument()
  expect(screen.getByText("Topology watermarks")).toBeInTheDocument()
})

test("keeps health sections visible when network summary fails", async () => {
  getOverviewMock.mockResolvedValue(overviewFixture)
  getNodesMock.mockResolvedValue(nodesFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  getChannelClusterSummaryMock.mockResolvedValue(channelClusterFixture)
  getNetworkSummaryMock.mockRejectedValue(new ManagerApiError(503, "unavailable", "network unavailable"))

  renderClusterDashboard()

  expect(await screen.findByText("Cluster degraded")).toBeInTheDocument()
  expect(screen.getByText("Network metrics unavailable")).toBeInTheDocument()
  expect(screen.getByText("sample")).toBeInTheDocument()
})

test("fatal overview failure renders ResourceState", async () => {
  getOverviewMock.mockRejectedValue(new ManagerApiError(403, "forbidden", "forbidden"))
  getNodesMock.mockResolvedValue(nodesFixture)
  getTasksMock.mockResolvedValue(tasksFixture)
  getChannelClusterSummaryMock.mockResolvedValue(channelClusterFixture)
  getNetworkSummaryMock.mockResolvedValue(networkFixture)

  renderClusterDashboard()

  expect(await screen.findByText(/permission/i)).toBeInTheDocument()
})

test("refresh refetches all cluster endpoints", async () => {
  const user = userEvent.setup()
  mockSuccessfulClusterDashboard()
  renderClusterDashboard()

  await screen.findByText("Cluster degraded")
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  await waitFor(() => expect(getOverviewMock).toHaveBeenCalledTimes(2))
  expect(getNetworkSummaryMock).toHaveBeenCalledTimes(2)
})
```

- [ ] **Step 2: Run page test to verify RED**

Run:

```bash
cd web && yarn test src/pages/cluster-dashboard/page.test.tsx
```

Expected: FAIL because the page still renders the skeleton.

- [ ] **Step 3: Add cluster i18n keys**

Add English keys:

```typescript
"clusterDashboard.title": "Cluster Dashboard",
"clusterDashboard.description": "Cluster health, internal transport, RPC quality, replication state, and topology watermarks.",
"clusterDashboard.eyebrow": "Cluster Ops",
"clusterDashboard.verdict.healthy": "Cluster healthy",
"clusterDashboard.verdict.degraded": "Cluster degraded",
"clusterDashboard.verdict.critical": "Cluster critical",
"clusterDashboard.generatedAt": "Generated: {value}",
"clusterDashboard.summary": "{incidents} incidents · controller leader {leader} · {ready}/{total} slots ready · {alive}/{nodes} nodes alive",
"clusterDashboard.metric.nodes": "Nodes alive",
"clusterDashboard.metric.slots": "Slots ready",
"clusterDashboard.metric.tasks": "Tasks failed/retrying",
"clusterDashboard.metric.channels": "Channel ISR anomalies",
"clusterDashboard.metric.internalMessages": "Internal message/s",
"clusterDashboard.metric.rpcErrorRate": "RPC error rate",
"clusterDashboard.metric.rpcLatency": "RPC p95",
"clusterDashboard.source.real": "real",
"clusterDashboard.source.derived": "derived",
"clusterDashboard.source.sample": "sample",
"clusterDashboard.links.title": "Internal Link Trends",
"clusterDashboard.links.description": "Transport TX/RX and RPC calls/errors from the cluster network summary.",
"clusterDashboard.links.unavailable": "Network metrics unavailable",
"clusterDashboard.replication.title": "Slot & channel replication",
"clusterDashboard.incidents.title": "Active incidents",
"clusterDashboard.incidents.empty": "No active cluster incidents.",
"clusterDashboard.incidents.networkEventTitle": "{kind} on node {node}",
"clusterDashboard.incidents.inspect": "Inspect",
"clusterDashboard.topology.title": "Topology watermarks",
"clusterDashboard.topology.local": "local",
"clusterDashboard.topology.openNode": "Open node {id}",
```

Add equivalent Chinese keys under `clusterDashboard.*`, using “单节点集群” where scope text is needed.

- [ ] **Step 4: Implement components**

Implement simple, focused components:

- `cluster-health-hero.tsx`
  - Props: `verdict`, `generatedAt`, `controllerLeaderId`, `nodesAlive`, `nodesTotal`, `slotsReady`, `slotsTotal`, `incidentCount`, `refreshing`, `onRefresh`.
  - Use the visual style from old `HealthHero`, but message ids under `clusterDashboard.*`.

- `cluster-metric-strip.tsx`
  - Props: `{ metrics: ClusterMetricValue[] }`.
  - Render a responsive grid `sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-7`.
  - Include a small source badge for `derived` and `sample`.

- `cluster-link-trends.tsx`
  - Props: `{ linkMetrics: ClusterLinkMetrics; networkError: Error | null }`.
  - Render warning text when `networkError` exists.
  - Use `ResponsiveContainer`, `LineChart`, `Line`, `XAxis`, `YAxis`, and `Tooltip`.

- `cluster-replication-health.tsx`
  - Use the old `SlotChannelHealth` as reference.
  - Props: `slots`, `channelCluster`.
  - Link to `/cluster/slots` and `/cluster/channels?tab=overview`.

- `cluster-incident-list.tsx`
  - Use old `IncidentList` as reference.
  - Props: `{ items: ClusterIncident[] }`.

- `cluster-topology-watermarks.tsx`
  - Props: `{ rows: ClusterTopologyRow[] }`.
  - Render compact row cards that include node, status, role, slots, raft health, applied index, and RPC summary.

- [ ] **Step 5: Implement page orchestrator**

Replace skeleton `web/src/pages/cluster-dashboard/page.tsx` with:

- State containing `overview`, `tasks`, `nodes`, `channelCluster`, `network`, `networkError`, `loading`, `refreshing`, `error`.
- `loadDashboard(refreshing)` that fetches required health APIs with `Promise.all`:

```typescript
const [overview, tasks, nodes, channelCluster] = await Promise.all([
  getOverview(),
  getTasks(),
  getNodes(),
  getChannelClusterSummary(),
])
```

- Fetch `getNetworkSummary()` separately so network failure is partial:

```typescript
let network = null
let networkError = null
try {
  network = await getNetworkSummary()
} catch (error) {
  networkError = error instanceof Error ? error : new Error("network summary request failed")
}
```

- Fatal errors only come from the four authoritative health APIs.
- Derive:

```typescript
const verdict = computeClusterVerdict(overview, nodes)
const linkMetrics = buildInternalLinkMetrics(network)
const metricStrip = buildClusterMetricStrip(overview, channelCluster, linkMetrics)
const incidents = buildClusterIncidents(intl, overview, nodes, network)
const topologyRows = buildTopologyRows(nodes, network)
```

- Render hero, metric strip, link trends, replication/incidents grid, topology.

- [ ] **Step 6: Run tests to verify GREEN**

Run:

```bash
cd web && yarn test src/pages/cluster-dashboard/view-model.test.ts src/pages/cluster-dashboard/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit cluster dashboard page**

```bash
git add web/src/pages/cluster-dashboard web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat(web): add cluster operations dashboard"
```

---

## Task 4: Business Dashboard View Model

**Files:**
- Create: `web/src/pages/business-dashboard/view-model.test.ts`
- Create: `web/src/pages/business-dashboard/view-model.ts`

- [ ] **Step 1: Write failing business view-model tests**

Create `web/src/pages/business-dashboard/view-model.test.ts`:

```typescript
import { describe, expect, it } from "vitest"
import type { DashboardMetricsResponse } from "@/lib/manager-api"
import {
  buildBusinessEntryCards,
  buildBusinessMetricStrip,
  buildBusinessRisks,
  buildBusinessTrendSeries,
  computeBusinessVerdict,
} from "./view-model"
```

Add a `metricsFixture` with all dashboard metrics:

```typescript
const series = (latest: number, peak = latest, avg = latest) => ({ latest, peak, avg, series: [avg, latest] })
const metricsFixture: DashboardMetricsResponse = {
  generated_at: "2026-05-15T08:30:00Z",
  window_seconds: 300,
  step_seconds: 30,
  points: 10,
  metrics: {
    send_per_sec: series(2800),
    deliver_per_sec: series(2700),
    connections: series(18400),
    send_latency_p99_ms: series(31),
    delivery_latency_p99_ms: series(42),
    send_fail_rate_percent: series(0.02),
    delivery_fail_rate_percent: series(0.01),
    active_channels: series(2143),
    retry_queue_depth: series(8),
    fan_out_rate: series(3.4),
  },
}
```

Add tests:

```typescript
describe("computeBusinessVerdict", () => {
  it("returns normal for healthy message quality", () => {
    expect(computeBusinessVerdict(metricsFixture)).toBe("normal")
  })

  it("returns degraded for retry pressure or elevated failure rate", () => {
    expect(computeBusinessVerdict(withMetric(metricsFixture, "retry_queue_depth", 200))).toBe("degraded")
    expect(computeBusinessVerdict(withMetric(metricsFixture, "send_fail_rate_percent", 2))).toBe("degraded")
  })

  it("returns critical for severe failure rate or latency", () => {
    expect(computeBusinessVerdict(withMetric(metricsFixture, "delivery_fail_rate_percent", 10))).toBe("critical")
    expect(computeBusinessVerdict(withMetric(metricsFixture, "delivery_latency_p99_ms", 5000))).toBe("critical")
  })
})

describe("buildBusinessMetricStrip", () => {
  it("returns all business quality metrics", () => {
    expect(buildBusinessMetricStrip(metricsFixture).map((item) => item.key)).toEqual([
      "sendRate",
      "deliverRate",
      "sendLatency",
      "deliveryLatency",
      "sendFailRate",
      "deliveryFailRate",
      "connections",
      "activeChannels",
      "retryQueue",
      "fanOut",
    ])
  })
})

describe("buildBusinessEntryCards", () => {
  it("uses deterministic sample labels when optional summaries are absent", () => {
    const cards = buildBusinessEntryCards()
    expect(cards.map((card) => card.source)).toEqual(["sample", "sample", "real", "sample", "real"])
    expect(cards.find((card) => card.key === "monitor")?.href).toBe("/business/monitor")
  })
})
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
cd web && yarn test src/pages/business-dashboard/view-model.test.ts
```

Expected: FAIL because `view-model.ts` is missing.

- [ ] **Step 3: Implement business view model**

Create `web/src/pages/business-dashboard/view-model.ts`:

```typescript
import type { DashboardMetricsResponse, DashboardMetricsSeriesDTO } from "@/lib/manager-api"
import type { ManagerBusinessChannelsResponse, ManagerSystemUsersResponse, ManagerUsersResponse } from "@/lib/manager-api.types"

export type BusinessVerdict = "normal" | "degraded" | "critical"
export type MetricSource = "real" | "derived" | "sample"

export type BusinessMetricValue = {
  key: string
  labelId: string
  value: number
  formatted: string
  detail: string
  tone: "default" | "warning" | "danger"
  source: MetricSource
}

export type BusinessRisk = {
  key: string
  severity: "warning" | "critical"
  titleId: string
  detail: string
  href: string
}

export type BusinessEntryCard = {
  key: "users" | "channels" | "messages" | "systemUsers" | "monitor"
  titleId: string
  descriptionId: string
  href: string
  value: string
  source: MetricSource
}

export function computeBusinessVerdict(metrics: DashboardMetricsResponse): BusinessVerdict {
  const m = metrics.metrics
  if (m.delivery_fail_rate_percent.latest >= 5 || m.send_fail_rate_percent.latest >= 5 || m.delivery_latency_p99_ms.latest >= 3000) return "critical"
  if (m.delivery_fail_rate_percent.latest >= 1 || m.send_fail_rate_percent.latest >= 1 || m.retry_queue_depth.latest >= 100 || m.delivery_latency_p99_ms.latest >= 1000) return "degraded"
  return "normal"
}
```

Implement:

- `buildBusinessMetricStrip(metrics)` returns the ten metric cards from the spec.
- `buildBusinessTrendSeries(metrics)` returns chart-ready send/deliver, latency, and failure arrays:

```typescript
function toTrend(series: DashboardMetricsSeriesDTO) {
  return series.series.map((value, index) => ({ index, value }))
}
```

- `buildBusinessRisks(metrics)` returns critical/warning risks when:
  - retry queue >= 100 warning.
  - send or delivery failure >= 1 warning, >= 5 critical.
  - delivery p99 >= 1000 warning, >= 3000 critical.
  - deliver rate is less than 80% of send rate while send rate > 0.
- `buildBusinessEntryCards({ users, channels, systemUsers } = {})`
  - Users card value: `users?.items.length` if provided, otherwise `12.1k` with `source: "sample"`.
  - Channels card value: `channels?.items.length` if provided, otherwise `2.1k` with `source: "sample"`.
  - Messages card value: `"Search"` with `source: "real"` because it is a real entry point, not a count.
  - System Users card value: `String(systemUsers.total)` if provided, otherwise `24` sample.
  - Monitor card value: `"Live"` with `source: "real"`.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
cd web && yarn test src/pages/business-dashboard/view-model.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit business view model**

```bash
git add web/src/pages/business-dashboard/view-model.ts web/src/pages/business-dashboard/view-model.test.ts
git commit -m "feat(web): add business dashboard view model"
```

---

## Task 5: Business Dashboard UI And Data Loading

**Files:**
- Test: `web/src/pages/business-dashboard/page.test.tsx`
- Modify: `web/src/pages/business-dashboard/page.tsx`
- Create: `web/src/pages/business-dashboard/components/business-health-hero.tsx`
- Create: `web/src/pages/business-dashboard/components/business-metric-strip.tsx`
- Create: `web/src/pages/business-dashboard/components/business-message-trends.tsx`
- Create: `web/src/pages/business-dashboard/components/business-risk-list.tsx`
- Create: `web/src/pages/business-dashboard/components/business-entry-cards.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing business page tests**

Create `web/src/pages/business-dashboard/page.test.tsx` with mocks for `getDashboardMetrics`, `getUsers`, `getBusinessChannels`, and `getSystemUsers`:

```typescript
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import { BusinessDashboardPage } from "./page"

const getDashboardMetricsMock = vi.fn()
const getUsersMock = vi.fn()
const getBusinessChannelsMock = vi.fn()
const getSystemUsersMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getDashboardMetrics: (...args: unknown[]) => getDashboardMetricsMock(...args),
    getUsers: (...args: unknown[]) => getUsersMock(...args),
    getBusinessChannels: (...args: unknown[]) => getBusinessChannelsMock(...args),
    getSystemUsers: (...args: unknown[]) => getSystemUsersMock(...args),
  }
})
```

Add tests:

```typescript
test("renders business quality metrics and management entry cards", async () => {
  mockSuccessfulBusinessDashboard()
  renderBusinessDashboard()

  expect(await screen.findByRole("heading", { name: "Business Dashboard" })).toBeInTheDocument()
  expect(screen.getByText("Business normal")).toBeInTheDocument()
  expect(screen.getByText("Send msg/s")).toBeInTheDocument()
  expect(screen.getByText("Deliver msg/s")).toBeInTheDocument()
  expect(screen.getByText("Business Message Trends")).toBeInTheDocument()
  expect(screen.getByRole("link", { name: /Users/ })).toHaveAttribute("href", "/business/users")
  expect(screen.getByRole("link", { name: /Live Monitor/ })).toHaveAttribute("href", "/business/monitor")
})

test("shows sample badges when optional summaries are unavailable", async () => {
  getDashboardMetricsMock.mockResolvedValue(metricsFixture)
  getUsersMock.mockRejectedValue(new ManagerApiError(503, "unavailable", "users unavailable"))
  getBusinessChannelsMock.mockRejectedValue(new ManagerApiError(503, "unavailable", "channels unavailable"))
  getSystemUsersMock.mockRejectedValue(new ManagerApiError(503, "unavailable", "system users unavailable"))

  renderBusinessDashboard()

  expect(await screen.findByText("Business normal")).toBeInTheDocument()
  expect(screen.getAllByText("sample").length).toBeGreaterThan(0)
})

test("metrics failure renders ResourceState", async () => {
  getDashboardMetricsMock.mockRejectedValue(new ManagerApiError(503, "service_unavailable", "metrics warming"))
  renderBusinessDashboard()

  expect(await screen.findByRole("status")).toHaveAttribute("data-kind", "unavailable")
})

test("refresh refetches metrics and optional summaries", async () => {
  const user = userEvent.setup()
  mockSuccessfulBusinessDashboard()
  renderBusinessDashboard()

  await screen.findByText("Business normal")
  await user.click(screen.getByRole("button", { name: "Refresh" }))

  await waitFor(() => expect(getDashboardMetricsMock).toHaveBeenCalledTimes(2))
  expect(getUsersMock).toHaveBeenCalledTimes(2)
})
```

- [ ] **Step 2: Run page test to verify RED**

Run:

```bash
cd web && yarn test src/pages/business-dashboard/page.test.tsx
```

Expected: FAIL because the page still renders the skeleton.

- [ ] **Step 3: Add business i18n keys**

Add English keys:

```typescript
"businessDashboard.title": "Business Dashboard",
"businessDashboard.description": "Client-facing message quality, retry pressure, online usage, and management entry points.",
"businessDashboard.eyebrow": "Business Management",
"businessDashboard.verdict.normal": "Business normal",
"businessDashboard.verdict.degraded": "Business degraded",
"businessDashboard.verdict.critical": "Business critical",
"businessDashboard.generatedAt": "Generated: {value}",
"businessDashboard.summary": "send {send}/s · deliver {deliver}/s · failures {fail}% · {connections} connections · retry queue {retry}",
"businessDashboard.metric.sendRate": "Send msg/s",
"businessDashboard.metric.deliverRate": "Deliver msg/s",
"businessDashboard.metric.sendLatency": "Send p99",
"businessDashboard.metric.deliveryLatency": "Delivery p99",
"businessDashboard.metric.sendFailRate": "Send failures",
"businessDashboard.metric.deliveryFailRate": "Delivery failures",
"businessDashboard.metric.connections": "Online connections",
"businessDashboard.metric.activeChannels": "Active channels",
"businessDashboard.metric.retryQueue": "Retry queue",
"businessDashboard.metric.fanOut": "Fan-out",
"businessDashboard.trends.title": "Business Message Trends",
"businessDashboard.trends.description": "Send/deliver throughput, latency, and failure rate.",
"businessDashboard.risks.title": "Business risks",
"businessDashboard.risks.empty": "No active business risks.",
"businessDashboard.risks.retryQueue": "Retry queue pressure",
"businessDashboard.risks.failureRate": "Elevated failure rate",
"businessDashboard.risks.deliveryLatency": "Delivery latency regression",
"businessDashboard.risks.deliveryGap": "Delivery rate below send rate",
"businessDashboard.entries.title": "Management entry points",
"businessDashboard.entries.users": "Users",
"businessDashboard.entries.usersDescription": "User, device, and token management.",
"businessDashboard.entries.channels": "Channels",
"businessDashboard.entries.channelsDescription": "Channel profiles, subscribers, allowlists, and denylists.",
"businessDashboard.entries.messages": "Messages",
"businessDashboard.entries.messagesDescription": "Message search, retention, and failure follow-up.",
"businessDashboard.entries.systemUsers": "System Users",
"businessDashboard.entries.systemUsersDescription": "System UIDs for bots and notification accounts.",
"businessDashboard.entries.monitor": "Live Monitor",
"businessDashboard.entries.monitorDescription": "Detailed business metric charts.",
"businessDashboard.source.real": "real",
"businessDashboard.source.sample": "sample",
"businessDashboard.source.derived": "derived",
```

Add Chinese equivalents under `businessDashboard.*`.

- [ ] **Step 4: Implement business components**

Implement:

- `business-health-hero.tsx`
  - Props: `verdict`, `generatedAt`, `metrics`, `refreshing`, `onRefresh`.
  - Similar chrome to cluster hero; use business copy and business verdict styles.

- `business-metric-strip.tsx`
  - Props: `{ metrics: BusinessMetricValue[] }`.
  - Render responsive grid `sm:grid-cols-2 lg:grid-cols-5`.
  - Show source badge for non-real values.

- `business-message-trends.tsx`
  - Props: `trends` from view-model.
  - Render three cards: throughput, latency, failures.
  - Use Recharts `LineChart` or `AreaChart`.

- `business-risk-list.tsx`
  - Props: `{ risks: BusinessRisk[] }`.
  - Empty state when no risk.
  - Link risk actions to `/business/messages` or `/business/monitor`.

- `business-entry-cards.tsx`
  - Props: `{ cards: BusinessEntryCard[] }`.
  - Render as links, with `sample` badge when source is sample.

- [ ] **Step 5: Implement page orchestrator**

Replace `web/src/pages/business-dashboard/page.tsx` skeleton with:

- Fatal required fetch: `getDashboardMetrics({ window: "30m", step: "30s" })`.
- Optional summaries fetched separately and allowed to fail:

```typescript
const [users, channels, systemUsers] = await Promise.all([
  getUsers({ limit: 1 }).catch(() => null),
  getBusinessChannels({ limit: 1 }).catch(() => null),
  getSystemUsers().catch(() => null),
])
```

- State: `metrics`, `users`, `channels`, `systemUsers`, `loading`, `refreshing`, `error`.
- Derive `verdict`, `strip`, `trends`, `risks`, and `entryCards` from the view model.
- Render hero, metric strip, trends/risk grid, entry cards.

- [ ] **Step 6: Run tests to verify GREEN**

Run:

```bash
cd web && yarn test src/pages/business-dashboard/view-model.test.ts src/pages/business-dashboard/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit business dashboard page**

```bash
git add web/src/pages/business-dashboard web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat(web): add business management dashboard"
```

---

## Task 6: Cleanup Generic Dashboard And Update Docs

**Files:**
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/README.md`
- Optional delete: `web/src/pages/dashboard/*`

- [ ] **Step 1: Search for stale generic dashboard imports**

Run:

```bash
rg "@/pages/dashboard|pages/dashboard|DashboardPage|dashboard\." web/src
```

Expected: only old i18n keys or no live imports. If `DashboardPage` is still imported in `router.tsx`, remove it.

- [ ] **Step 2: Delete old generic dashboard module if unused**

If the search confirms no imports remain, delete:

```bash
rm -rf web/src/pages/dashboard
```

Do not delete `web/src/pages/monitor`; it backs `/business/monitor`.

- [ ] **Step 3: Remove stale nav/i18n keys only after search confirms unused**

Run:

```bash
rg "nav\.section\.overview|nav\.path\.overview|dashboard\." web/src
```

- Remove `nav.section.overview`, `nav.path.overview.dashboard`, `nav.path.overview.monitor` if no code references them.
- Remove old `dashboard.*` keys only if no components reference them.
- Do not remove `monitor.*` keys; `/business/monitor` still uses them.

- [ ] **Step 4: Update `web/README.md`**

Update Page And API Matrix:

```markdown
| `/cluster/dashboard` | `GET /manager/overview`, `GET /manager/tasks`, `GET /manager/nodes`, `GET /manager/channel-cluster/summary`, `GET /manager/network/summary` | Implemented |
| `/business/dashboard` | `GET /manager/dashboard/metrics`, optional `GET /manager/users`, `GET /manager/channels`, `GET /manager/system-users` for entry summaries | Implemented |
| `/business/monitor` | `GET /manager/monitor/metrics`, optional `node_id` filter | Implemented |
```

Remove `/dashboard` and `/monitor` as primary page rows.

Update Legacy Redirects:

```markdown
- Overview routes: `/dashboard` redirects to `/cluster/dashboard`; `/monitor` redirects to `/business/monitor`.
```

- [ ] **Step 5: Run cleanup verification tests**

Run:

```bash
cd web && yarn test src/lib/navigation.test.ts src/app/router.test.tsx src/app/layout/topbar.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/page-shells.test.tsx src/pages/cluster-dashboard/view-model.test.ts src/pages/cluster-dashboard/page.test.tsx src/pages/business-dashboard/view-model.test.ts src/pages/business-dashboard/page.test.tsx src/pages/monitor/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Run frontend build**

Run:

```bash
cd web && yarn build
```

Expected: PASS.

- [ ] **Step 7: Commit cleanup and docs**

```bash
git add web/README.md web/src/app/router.tsx web/src/lib/navigation.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/pages
git commit -m "chore(web): remove generic overview dashboard"
```

---

## Task 7: Final Verification

**Files:**
- No code changes expected unless verification finds issues.

- [ ] **Step 1: Run targeted frontend test suite**

Run:

```bash
cd web && yarn test src/lib/navigation.test.ts src/app/router.test.tsx src/app/layout/topbar.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/page-shells.test.tsx src/pages/cluster-dashboard/view-model.test.ts src/pages/cluster-dashboard/page.test.tsx src/pages/business-dashboard/view-model.test.ts src/pages/business-dashboard/page.test.tsx src/pages/monitor/page.test.tsx
```

Expected: PASS.

- [ ] **Step 2: Run full frontend tests if targeted tests pass**

Run:

```bash
cd web && yarn test
```

Expected: PASS. If this is too slow, record the reason and the targeted test coverage that passed.

- [ ] **Step 3: Run frontend build**

Run:

```bash
cd web && yarn build
```

Expected: PASS.

- [ ] **Step 4: Inspect navigation manually in dev server if practical**

Run:

```bash
cd web && yarn dev
```

Open the dev URL and verify:

- Topbar sections: Cluster Ops, Business, System.
- Cluster Ops default route is `/cluster/dashboard`.
- Business default route is `/business/dashboard`.
- `/business/monitor` renders the existing live monitor page.
- `/cluster/diagnostics?tab=network` still renders network diagnostics.

- [ ] **Step 5: Commit any verification fixes**

If verification required fixes:

```bash
git add <changed files>
git commit -m "fix(web): stabilize dashboard split"
```

If no fixes were needed, do not create an empty commit.

---

## Implementation Notes

- The Cluster dashboard should never silently replace failed authoritative health data with mock values. Mock/sample fallback is only for optional dashboard-only summaries.
- Network summary is secondary on the Cluster dashboard. If it fails, keep cluster health visible and show “Network metrics unavailable”.
- Business optional summary fetches are secondary. If they fail, keep business metrics visible and show sample badges on entry cards.
- Keep old `/monitor` compatibility by redirecting to `/business/monitor`; do not duplicate the Monitor page implementation.
- Prefer small component props based on view-model outputs. Avoid putting metric math inside React components.
- Use English comments only when logic is not obvious, especially around derived RPC error rate and expected long-poll expiry exclusion.
