# Cluster Live Monitor Card Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a UI-first `/cluster/monitor` page with cluster runtime monitoring cards, preview data, navigation, routing, i18n, and focused tests.

**Architecture:** Add a new `web/src/pages/cluster-monitor` feature folder that mirrors the business monitor page structure while keeping cluster metric types separate. Wire the page into the existing React Router and cluster navigation, using local deterministic preview data only.

**Tech Stack:** React, TypeScript, React Router, react-intl, Recharts, Testing Library, Vitest, Bun.

---

## File Structure

- Create `web/src/pages/cluster-monitor/types.ts`
  - Defines cluster monitor time ranges, card tones, stages, metric keys, series points, snapshot entries, and preview model shape.
- Create `web/src/pages/cluster-monitor/preview-data.ts`
  - Builds deterministic preview data for 12 cluster monitor cards and 7 snapshot entries.
- Create `web/src/pages/cluster-monitor/preview-data.test.ts`
  - Locks card order, snapshot order, deterministic generation, and special stats.
- Create `web/src/pages/cluster-monitor/page.tsx`
  - Owns selected time range and pause state, then renders header, toolbar, snapshot strip, and card grid.
- Create `web/src/pages/cluster-monitor/page.test.tsx`
  - Verifies the UI preview page, card count, key labels, toolbar state, and time range behavior.
- Create `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`
  - Renders scope, generated time, time range segmented buttons, and pause/resume button.
- Create `web/src/pages/cluster-monitor/components/cluster-monitor-snapshot-strip.tsx`
  - Renders the compact seven-value snapshot row.
- Create `web/src/pages/cluster-monitor/components/cluster-monitor-card-grid.tsx`
  - Renders the responsive card grid.
- Create `web/src/pages/cluster-monitor/components/cluster-monitor-metric-card.tsx`
  - Renders one chart card with title, stage, status pill, value, area chart, and stats.
- Modify `web/src/app/router.tsx`
  - Import `ClusterMonitorPage` and add route `cluster/monitor`.
- Modify `web/src/app/router.test.tsx`
  - Add a focused route test for `/cluster/monitor`.
- Modify `web/src/lib/navigation.ts`
  - Add a cluster navigation item for `/cluster/monitor`, near `/cluster/dashboard`.
- Modify `web/src/lib/navigation.test.ts`
  - Verify the new route stays in the cluster section and has cluster-specific metadata.
- Modify `web/src/app/layout/sidebar-nav.test.tsx`
  - Update the cluster nav order expectation from `["Dashboard", "Nodes", "Slots"]` to `["Dashboard", "Live Monitor", "Nodes"]`.
- Modify `web/src/i18n/messages/en.ts`
  - Add cluster monitor navigation and page strings.
- Modify `web/src/i18n/messages/zh-CN.ts`
  - Add matching Chinese strings.

## Task 1: Lock Preview Model With Failing Tests

**Files:**
- Create: `web/src/pages/cluster-monitor/preview-data.test.ts`
- Create later: `web/src/pages/cluster-monitor/types.ts`
- Create later: `web/src/pages/cluster-monitor/preview-data.ts`

- [ ] **Step 1: Write the failing preview data tests**

```ts
import { expect, test } from "vitest"

import { buildPreviewClusterMonitorModel } from "./preview-data"

test("builds the cluster runtime monitor cards in troubleshooting order", () => {
  const model = buildPreviewClusterMonitorModel("15m", false)

  expect(model.cards.map((card) => card.key)).toEqual([
    "controllerProposeRate",
    "controllerApplyGap",
    "slotLeaderStability",
    "slotReplicaLagP99",
    "channelISRHealth",
    "channelAppendLatencyP99",
    "internalTraffic",
    "rpcSuccessRate",
    "rpcLatencyP95",
    "workqueuePressure",
    "storageWriteP99",
    "incidentRate",
  ])
  expect(model.scopeLabelId).toBe("clusterMonitor.scope.global")
  expect(model.snapshot.map((entry) => entry.labelId)).toEqual([
    "clusterMonitor.snapshot.nodesAlive",
    "clusterMonitor.snapshot.slotsReady",
    "clusterMonitor.snapshot.controllerApplyGap",
    "clusterMonitor.snapshot.channelISRAnomalies",
    "clusterMonitor.snapshot.rpcErrorRate",
    "clusterMonitor.snapshot.queuePressure",
    "clusterMonitor.snapshot.storageWriteP99",
  ])
})

test("builds deterministic preview data with chart series and stats", () => {
  const first = buildPreviewClusterMonitorModel("30m", false)
  const second = buildPreviewClusterMonitorModel("30m", false)

  expect(second).toEqual(first)
  expect(first.cards).toHaveLength(12)
  expect(first.cards[0].series.length).toBeGreaterThan(20)
  expect(first.cards[0].stats).toHaveLength(3)
})

test("uses fixed preview details for cluster-specific operational stats", () => {
  const model = buildPreviewClusterMonitorModel("15m", false)
  const cardsByKey = new Map(model.cards.map((card) => [card.key, card]))

  expect(cardsByKey.get("controllerApplyGap")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.slowNodes",
    value: "1",
  })
  expect(cardsByKey.get("channelISRHealth")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.affectedChannels",
    value: "14",
  })
  expect(cardsByKey.get("rpcSuccessRate")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.timeouts",
    value: "9",
  })
  expect(cardsByKey.get("incidentRate")?.stats).toContainEqual({
    labelId: "clusterMonitor.stat.topReason",
    value: "rpc_timeout",
  })
})
```

- [ ] **Step 2: Run the preview test and verify it fails**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/preview-data.test.ts
```

Expected: fail because `web/src/pages/cluster-monitor/preview-data.ts` does not exist yet.

- [ ] **Step 3: Add the cluster monitor types**

Create `web/src/pages/cluster-monitor/types.ts` with:

```ts
export type ClusterMonitorTimeRange = "5m" | "15m" | "30m" | "1h"

export type ClusterMonitorTone = "normal" | "warning" | "critical" | "preview"

export type ClusterMonitorStage =
  | "controlPlane"
  | "slotReplication"
  | "channelReplication"
  | "internalNetwork"
  | "runtimePressure"
  | "incidentClosure"

export type ClusterMonitorMetricKey =
  | "controllerProposeRate"
  | "controllerApplyGap"
  | "slotLeaderStability"
  | "slotReplicaLagP99"
  | "channelISRHealth"
  | "channelAppendLatencyP99"
  | "internalTraffic"
  | "rpcSuccessRate"
  | "rpcLatencyP95"
  | "workqueuePressure"
  | "storageWriteP99"
  | "incidentRate"

export type ClusterMonitorPoint = {
  timestamp: number
  value: number
}

export type ClusterMonitorStat = {
  labelId: string
  value: string
}

export type ClusterMonitorMetricCard = {
  key: ClusterMonitorMetricKey
  titleId: string
  stage: ClusterMonitorStage
  stageLabelId: string
  statusId: string
  tone: ClusterMonitorTone
  unit: string
  value: string
  series: ClusterMonitorPoint[]
  stats: ClusterMonitorStat[]
  chartColor: string
}

export type ClusterMonitorSnapshotEntry = {
  key: string
  labelId: string
  value: string
  unit?: string
  tone: ClusterMonitorTone
}

export type PreviewClusterMonitorModel = {
  generatedAt: string
  scopeLabelId: string
  timeRange: ClusterMonitorTimeRange
  isPaused: boolean
  snapshot: ClusterMonitorSnapshotEntry[]
  cards: ClusterMonitorMetricCard[]
}
```

- [ ] **Step 4: Add minimal preview data implementation**

Create `web/src/pages/cluster-monitor/preview-data.ts`. Use deterministic series generation, the 12 metric keys from the test, and the 7 snapshot ids from the test.

- [ ] **Step 5: Run preview tests and verify they pass**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/preview-data.test.ts
```

Expected: pass.

## Task 2: Add Failing Page UI Tests

**Files:**
- Create: `web/src/pages/cluster-monitor/page.test.tsx`
- Create later: `web/src/pages/cluster-monitor/page.tsx`
- Create later: component files under `web/src/pages/cluster-monitor/components/`

- [ ] **Step 1: Write the failing page test**

```tsx
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ClusterMonitorPage } from "@/pages/cluster-monitor/page"

function renderClusterMonitorPage() {
  return render(
    <I18nProvider>
      <ClusterMonitorPage />
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
})

test("renders the local preview cluster monitor card wall", () => {
  renderClusterMonitorPage()

  expect(screen.getByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(screen.getByText("UI Preview")).toBeInTheDocument()
  expect(screen.getByText("Cluster control plane, replication, internal network, queue, and storage watermarks.")).toBeInTheDocument()

  const cards = screen.getAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(12)
  expect(within(cards[0]).getByText("Controller Propose Rate")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Slot Leader Stability")).toBeInTheDocument()
  expect(within(cards[4]).getByText("Channel ISR Health")).toBeInTheDocument()
  expect(within(cards[7]).getByText("RPC Success Rate")).toBeInTheDocument()
  expect(within(cards[11]).getByText("Incident Rate")).toBeInTheDocument()

  for (const label of ["Nodes Alive", "Slots Ready", "Apply Gap", "ISR Anomalies", "RPC Errors", "Queue Pressure", "Write P99"]) {
    expect(screen.getByText(label)).toBeInTheDocument()
  }

  for (const label of ["5m time range", "15m time range", "30m time range", "1h time range", "Pause live preview"]) {
    expect(screen.getByRole("button", { name: label })).toBeInTheDocument()
  }
})

test("updates selected time range and pause state from the toolbar", async () => {
  const user = userEvent.setup()
  renderClusterMonitorPage()

  await user.click(screen.getByRole("button", { name: "30m time range" }))
  expect(screen.getByRole("button", { name: "30m time range" })).toHaveAttribute("aria-pressed", "true")

  await user.click(screen.getByRole("button", { name: "Pause live preview" }))
  expect(screen.getByRole("button", { name: "Resume live preview" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Resume live preview" }))
  expect(screen.getByRole("button", { name: "Pause live preview" })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the page test and verify it fails**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/page.test.tsx
```

Expected: fail because `web/src/pages/cluster-monitor/page.tsx` does not exist yet.

- [ ] **Step 3: Add page and components**

Implement `page.tsx` and the four component files by adapting the existing `web/src/pages/monitor` structure with cluster-specific type imports and `clusterMonitor.*` message ids.

- [ ] **Step 4: Run the page test and verify it passes**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/page.test.tsx
```

Expected: pass.

## Task 3: Wire Navigation, Routing, And Localized Labels

**Files:**
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/router.test.tsx`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/lib/navigation.test.ts`
- Modify: `web/src/app/layout/sidebar-nav.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing route and navigation expectations**

Add this test to `web/src/app/router.test.tsx`:

```tsx
test("renders the cluster live monitor route", async () => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/monitor"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(screen.getByText("UI Preview")).toBeInTheDocument()
})
```

Update `web/src/lib/navigation.test.ts`:

```ts
expect(getActiveNavigationSection("/cluster/monitor")?.id).toBe("cluster")
expect(pageMetadata.get("/cluster/monitor")?.titleMessageId).toBe("nav.clusterMonitor.title")
expect(pageMetadata.get("/cluster/monitor")?.pathLabelMessageId).toBe("nav.path.cluster.monitor")
```

Update `web/src/app/layout/sidebar-nav.test.tsx`:

```ts
expect(links.slice(0, 3)).toEqual(["Dashboard", "Live Monitor", "Nodes"])
```

- [ ] **Step 2: Run focused route/navigation tests and verify they fail**

Run:

```bash
cd web && bun run test -- src/app/router.test.tsx src/lib/navigation.test.ts src/app/layout/sidebar-nav.test.tsx
```

Expected: fail because the route and navigation item are not wired yet.

- [ ] **Step 3: Add route and navigation item**

In `web/src/app/router.tsx`, import and route the new page:

```tsx
import { ClusterMonitorPage } from "@/pages/cluster-monitor/page"

{ path: "cluster/monitor", element: <ClusterMonitorPage /> },
```

In `web/src/lib/navigation.ts`, add this item after `/cluster/dashboard`:

```ts
{
  href: "/cluster/monitor",
  titleMessageId: "nav.clusterMonitor.title",
  descriptionMessageId: "nav.clusterMonitor.description",
  pathLabelMessageId: "nav.path.cluster.monitor",
  icon: Activity,
},
```

- [ ] **Step 4: Add localized messages**

Add English messages:

```ts
"nav.path.cluster.monitor": "CLUSTER / MONITOR",
"nav.clusterMonitor.title": "Live Monitor",
"nav.clusterMonitor.description": "Cluster runtime trends, replication, internal network, queues, and storage.",
"clusterMonitor.title": "Live Monitor",
"clusterMonitor.description": "Cluster control plane, replication, internal network, queue, and storage watermarks.",
```

Add the full `clusterMonitor.*` labels used by the preview model, toolbar, snapshot strip, and cards.

Add matching Chinese messages:

```ts
"nav.path.cluster.monitor": "CLUSTER / MONITOR",
"nav.clusterMonitor.title": "实时监控",
"nav.clusterMonitor.description": "集群运行时趋势、复制、内部网络、队列与存储水位。",
"clusterMonitor.title": "实时监控",
"clusterMonitor.description": "集群控制面、复制、内部网络、队列与存储水位。",
```

- [ ] **Step 5: Run focused route/navigation tests and verify they pass**

Run:

```bash
cd web && bun run test -- src/app/router.test.tsx src/lib/navigation.test.ts src/app/layout/sidebar-nav.test.tsx
```

Expected: pass.

## Task 4: Final Verification And Commit

**Files:**
- All files created or modified in Tasks 1-3.

- [ ] **Step 1: Run focused cluster monitor tests**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/preview-data.test.ts src/pages/cluster-monitor/page.test.tsx src/app/router.test.tsx src/lib/navigation.test.ts src/app/layout/sidebar-nav.test.tsx
```

Expected: pass.

- [ ] **Step 2: Run TypeScript build**

Run:

```bash
cd web && bunx tsc -b
```

Expected: exit code 0.

- [ ] **Step 3: Run Vite build**

Run:

```bash
cd web && bunx vite build
```

Expected: exit code 0. If `web/dist/index.html` changes only because asset hashes were regenerated, restore it before commit.

- [ ] **Step 4: Review intentional diff**

Run:

```bash
git diff -- web/src/pages/cluster-monitor web/src/app/router.tsx web/src/app/router.test.tsx web/src/lib/navigation.ts web/src/lib/navigation.test.ts web/src/app/layout/sidebar-nav.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
```

Expected: diff contains only the cluster live monitor UI, tests, route, navigation, and i18n changes.

- [ ] **Step 5: Commit only this feature's files**

Run:

```bash
git add web/src/pages/cluster-monitor web/src/app/router.tsx web/src/app/router.test.tsx web/src/lib/navigation.ts web/src/lib/navigation.test.ts web/src/app/layout/sidebar-nav.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts docs/superpowers/plans/2026-06-18-cluster-live-monitor-card-redesign.md
git commit -m "feat: add cluster live monitor preview"
```

Expected: commit includes only this feature's files and the implementation plan.
