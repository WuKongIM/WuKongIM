# Business Live Monitor Card Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `/business/monitor` as a cloud-console-style business message-path monitor card wall using local preview data only.

**Architecture:** Replace the current API-backed generic chart grid with a preview-data model and focused React components for toolbar, snapshot strip, card grid, and metric chart cards. The page keeps the existing route and navigation, avoids backend/API changes, and makes the message path ordering explicit in local card configuration.

**Tech Stack:** React 19, TypeScript, Recharts, lucide-react, react-intl, Tailwind CSS, Vitest, Testing Library, Bun.

---

## File Structure

- Modify `web/src/pages/monitor/page.test.tsx` to assert the new preview card-wall contract and that `getMonitorMetrics` is not called.
- Create `web/src/pages/monitor/preview-data.test.ts` to lock the preview metric model: 12 cards, required path-stage keys, snapshot entries, deterministic series.
- Modify `web/src/pages/monitor/types.ts` to replace the old API-series-only types with card-wall model types.
- Create `web/src/pages/monitor/preview-data.ts` for deterministic local preview data and formatter helpers.
- Create `web/src/pages/monitor/components/monitor-toolbar.tsx` for time range and Live/Pause controls.
- Create `web/src/pages/monitor/components/monitor-snapshot-strip.tsx` for compact current health values.
- Create `web/src/pages/monitor/components/monitor-metric-card.tsx` for one chart card.
- Create `web/src/pages/monitor/components/monitor-card-grid.tsx` for the responsive card wall.
- Modify `web/src/pages/monitor/page.tsx` to assemble the preview model and new components without calling the manager API.
- Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts` to add monitor card-wall copy and keep existing navigation copy.
- Delete `web/src/pages/monitor/use-monitor-data.ts` because preview mode must not depend on `getMonitorMetrics`.
- Delete `web/src/pages/monitor/components/chart-grid.tsx`, `web/src/pages/monitor/components/metric-chart.tsx`, and `web/src/pages/monitor/components/monitor-controls.tsx` after the new page no longer imports them.

---

### Task 1: Page-Level Contract Test

**Files:**
- Modify: `web/src/pages/monitor/page.test.tsx`
- Existing context: `web/src/pages/monitor/page.tsx`

- [ ] **Step 1: Replace the old API-backed tests with the failing preview UI contract**

Replace `web/src/pages/monitor/page.test.tsx` with:

```tsx
import { render, screen, within } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { MonitorPage } from "@/pages/monitor/page"

const getMonitorMetricsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return { ...actual, getMonitorMetrics: (...args: unknown[]) => getMonitorMetricsMock(...args) }
})

function renderMonitorPage() {
  return render(
    <I18nProvider>
      <MonitorPage />
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getMonitorMetricsMock.mockReset()
})

test("renders the business path preview monitor without requesting manager metrics", () => {
  renderMonitorPage()

  expect(screen.getByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(screen.getByText("UI Preview")).toBeInTheDocument()
  expect(screen.getByText("Global business message path health trends.")).toBeInTheDocument()
  expect(getMonitorMetricsMock).not.toHaveBeenCalled()
})

test("renders twelve cloud monitor chart cards in message path order", () => {
  renderMonitorPage()

  const cards = screen.getAllByTestId("monitor-metric-card")
  expect(cards).toHaveLength(12)

  expect(within(cards[0]).getByText("Send Rate")).toBeInTheDocument()
  expect(within(cards[3]).getByText("Commit Rate")).toBeInTheDocument()
  expect(within(cards[6]).getByText("Delivery Rate")).toBeInTheDocument()
  expect(within(cards[10]).getByText("Retry Queue Depth")).toBeInTheDocument()
  expect(within(cards[11]).getByText("Path Error Rate")).toBeInTheDocument()
})

test("renders the snapshot strip and live controls", () => {
  renderMonitorPage()

  expect(screen.getByText("Send")).toBeInTheDocument()
  expect(screen.getByText("Delivery")).toBeInTheDocument()
  expect(screen.getByText("Entry P99")).toBeInTheDocument()
  expect(screen.getByText("Delivery P99")).toBeInTheDocument()
  expect(screen.getByText("Errors")).toBeInTheDocument()
  expect(screen.getByText("Retry Depth")).toBeInTheDocument()
  expect(screen.getByText("Online")).toBeInTheDocument()

  expect(screen.getByRole("button", { name: "5m time range" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "15m time range" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "30m time range" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "1h time range" })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Pause live preview" })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the focused test and confirm it fails for the right reason**

Run:

```bash
cd web && bun run test -- src/pages/monitor/page.test.tsx
```

Expected: FAIL because the current page still calls `getMonitorMetrics`, renders the old API-backed chart grid, and does not render `UI Preview` or `data-testid="monitor-metric-card"`.

- [ ] **Step 3: Keep the failing test uncommitted until the page turns green**

Run:

```bash
git diff -- web/src/pages/monitor/page.test.tsx
```

Expected: the diff contains only the preview card-wall contract test. Do not commit this red test by itself; Task 4 commits it together with the implementation that makes it pass.

---

### Task 2: Preview Data Model

**Files:**
- Create: `web/src/pages/monitor/preview-data.test.ts`
- Modify: `web/src/pages/monitor/types.ts`
- Create: `web/src/pages/monitor/preview-data.ts`

- [ ] **Step 1: Write the failing preview-data test**

Create `web/src/pages/monitor/preview-data.test.ts`:

```ts
import { describe, expect, test } from "vitest"

import { buildPreviewMonitorModel } from "./preview-data"

describe("buildPreviewMonitorModel", () => {
  test("builds twelve ordered message-path cards", () => {
    const model = buildPreviewMonitorModel("15m", false)

    expect(model.cards.map((card) => card.key)).toEqual([
      "sendRate",
      "sendSuccessRate",
      "entryLatencyP99",
      "commitRate",
      "commitLatencyP99",
      "pendingCommitBacklog",
      "deliveryRate",
      "deliveryLatencyP99",
      "fanOutRatio",
      "offlineEnqueueRate",
      "retryQueueDepth",
      "pathErrorRate",
    ])
  })

  test("builds snapshot entries for the global path", () => {
    const model = buildPreviewMonitorModel("15m", false)

    expect(model.scopeLabelId).toBe("monitor.scope.global")
    expect(model.snapshot.map((entry) => entry.labelId)).toEqual([
      "monitor.snapshot.send",
      "monitor.snapshot.delivery",
      "monitor.snapshot.entryP99",
      "monitor.snapshot.deliveryP99",
      "monitor.snapshot.errors",
      "monitor.snapshot.retryDepth",
      "monitor.snapshot.online",
    ])
  })

  test("keeps preview series deterministic for a selected range", () => {
    const first = buildPreviewMonitorModel("30m", false)
    const second = buildPreviewMonitorModel("30m", false)

    expect(first.generatedAt).toBe(second.generatedAt)
    expect(first.cards[0].series).toEqual(second.cards[0].series)
    expect(first.cards[0].stats).toHaveLength(3)
    expect(first.cards[0].series.length).toBeGreaterThan(20)
  })
})
```

- [ ] **Step 2: Run the preview-data test and confirm it fails**

Run:

```bash
cd web && bun run test -- src/pages/monitor/preview-data.test.ts
```

Expected: FAIL because `preview-data.ts` and `buildPreviewMonitorModel` do not exist.

- [ ] **Step 3: Replace monitor types with card-wall model types**

Replace `web/src/pages/monitor/types.ts` with:

```ts
export type TimeRange = "5m" | "15m" | "30m" | "1h"

export type MonitorTone = "normal" | "warning" | "critical" | "preview"

export type MonitorStage =
  | "sendEntry"
  | "appendCommit"
  | "onlineDelivery"
  | "offlineRetry"
  | "errorClosure"

export type MonitorMetricKey =
  | "sendRate"
  | "sendSuccessRate"
  | "entryLatencyP99"
  | "commitRate"
  | "commitLatencyP99"
  | "pendingCommitBacklog"
  | "deliveryRate"
  | "deliveryLatencyP99"
  | "fanOutRatio"
  | "offlineEnqueueRate"
  | "retryQueueDepth"
  | "pathErrorRate"

export type MonitorPoint = {
  timestamp: number
  value: number
}

export type MonitorStat = {
  labelId: string
  value: string
}

export type MonitorMetricCard = {
  key: MonitorMetricKey
  titleId: string
  stage: MonitorStage
  stageLabelId: string
  statusId: string
  tone: MonitorTone
  unit: string
  value: string
  series: MonitorPoint[]
  stats: MonitorStat[]
  chartColor: string
}

export type MonitorSnapshotEntry = {
  key: string
  labelId: string
  value: string
  unit?: string
  tone: MonitorTone
}

export type PreviewMonitorModel = {
  generatedAt: string
  scopeLabelId: string
  timeRange: TimeRange
  isPaused: boolean
  snapshot: MonitorSnapshotEntry[]
  cards: MonitorMetricCard[]
}
```

- [ ] **Step 4: Add deterministic preview data**

Create `web/src/pages/monitor/preview-data.ts`:

```ts
import type {
  MonitorMetricCard,
  MonitorMetricKey,
  MonitorPoint,
  MonitorSnapshotEntry,
  MonitorStage,
  MonitorTone,
  PreviewMonitorModel,
  TimeRange,
} from "./types"

const BASE_TIME = Date.parse("2026-06-18T08:30:00Z")

const rangeMinutes: Record<TimeRange, number> = {
  "5m": 5,
  "15m": 15,
  "30m": 30,
  "1h": 60,
}

type CardDefinition = {
  key: MonitorMetricKey
  titleId: string
  stage: MonitorStage
  stageLabelId: string
  statusId: string
  tone: MonitorTone
  unit: string
  chartColor: string
  base: number
  amplitude: number
  slope?: number
  spike?: number
  decimals?: number
  stats: (series: MonitorPoint[]) => { labelId: string; value: string }[]
}

function formatNumber(value: number, decimals = 0) {
  return new Intl.NumberFormat("en-US", {
    maximumFractionDigits: decimals,
    minimumFractionDigits: decimals,
  }).format(value)
}

function average(series: MonitorPoint[]) {
  return series.reduce((sum, point) => sum + point.value, 0) / Math.max(series.length, 1)
}

function peak(series: MonitorPoint[]) {
  return Math.max(...series.map((point) => point.value))
}

function latest(series: MonitorPoint[]) {
  return series[series.length - 1]?.value ?? 0
}

function percentile(series: MonitorPoint[], ratio: number) {
  const values = series.map((point) => point.value).sort((a, b) => a - b)
  const index = Math.min(values.length - 1, Math.max(0, Math.floor(values.length * ratio)))
  return values[index] ?? 0
}

function makeSeries(definition: CardDefinition, timeRange: TimeRange): MonitorPoint[] {
  const minutes = rangeMinutes[timeRange]
  const points = Math.max(24, minutes * 4)
  const stepMs = (minutes * 60 * 1000) / points

  return Array.from({ length: points }, (_, index) => {
    const wave = Math.sin(index / 2.8) * definition.amplitude
    const secondary = Math.cos(index / 5.5) * definition.amplitude * 0.35
    const spike = definition.spike && index % 17 === 11 ? definition.spike : 0
    const trend = (definition.slope ?? 0) * index
    const value = Math.max(0, definition.base + wave + secondary + spike + trend)

    return {
      timestamp: BASE_TIME - (points - index - 1) * stepMs,
      value,
    }
  })
}

const cardDefinitions: CardDefinition[] = [
  {
    key: "sendRate",
    titleId: "monitor.metrics.sendRate",
    stage: "sendEntry",
    stageLabelId: "monitor.stage.sendEntry",
    statusId: "monitor.status.normal",
    tone: "normal",
    unit: "msg/s",
    chartColor: "--chart-1",
    base: 12480,
    amplitude: 1160,
    spike: 1900,
    stats: (series) => [
      { labelId: "monitor.stat.avg", value: `${formatNumber(average(series))} msg/s` },
      { labelId: "monitor.stat.peak", value: `${formatNumber(peak(series))} msg/s` },
      { labelId: "monitor.stat.total5m", value: "3.71M" },
    ],
  },
  {
    key: "sendSuccessRate",
    titleId: "monitor.metrics.sendSuccessRate",
    stage: "sendEntry",
    stageLabelId: "monitor.stage.sendEntry",
    statusId: "monitor.status.normal",
    tone: "normal",
    unit: "%",
    chartColor: "--chart-1",
    base: 99.96,
    amplitude: 0.018,
    decimals: 2,
    stats: () => [
      { labelId: "monitor.stat.failed", value: "148" },
      { labelId: "monitor.stat.topError", value: "auth_expired" },
      { labelId: "monitor.stat.affectedNodes", value: "1" },
    ],
  },
  {
    key: "entryLatencyP99",
    titleId: "monitor.metrics.entryLatencyP99",
    stage: "sendEntry",
    stageLabelId: "monitor.stage.sendEntry",
    statusId: "monitor.status.warning",
    tone: "warning",
    unit: "ms",
    chartColor: "--chart-3",
    base: 38,
    amplitude: 6,
    spike: 28,
    decimals: 1,
    stats: (series) => [
      { labelId: "monitor.stat.p50", value: `${formatNumber(percentile(series, 0.5), 1)} ms` },
      { labelId: "monitor.stat.p95", value: `${formatNumber(percentile(series, 0.95), 1)} ms` },
      { labelId: "monitor.stat.peakP99", value: `${formatNumber(peak(series), 1)} ms` },
    ],
  },
  {
    key: "commitRate",
    titleId: "monitor.metrics.commitRate",
    stage: "appendCommit",
    stageLabelId: "monitor.stage.appendCommit",
    statusId: "monitor.status.normal",
    tone: "normal",
    unit: "msg/s",
    chartColor: "--chart-2",
    base: 12180,
    amplitude: 980,
    spike: 1200,
    stats: (series) => [
      { labelId: "monitor.stat.avg", value: `${formatNumber(average(series))} msg/s` },
      { labelId: "monitor.stat.peak", value: `${formatNumber(peak(series))} msg/s` },
      { labelId: "monitor.stat.batches", value: "1,842" },
    ],
  },
  {
    key: "commitLatencyP99",
    titleId: "monitor.metrics.commitLatencyP99",
    stage: "appendCommit",
    stageLabelId: "monitor.stage.appendCommit",
    statusId: "monitor.status.warning",
    tone: "warning",
    unit: "ms",
    chartColor: "--chart-3",
    base: 54,
    amplitude: 9,
    spike: 34,
    decimals: 1,
    stats: (series) => [
      { labelId: "monitor.stat.p50", value: `${formatNumber(percentile(series, 0.5), 1)} ms` },
      { labelId: "monitor.stat.p95", value: `${formatNumber(percentile(series, 0.95), 1)} ms` },
      { labelId: "monitor.stat.slowCommits", value: "32" },
    ],
  },
  {
    key: "pendingCommitBacklog",
    titleId: "monitor.metrics.pendingCommitBacklog",
    stage: "appendCommit",
    stageLabelId: "monitor.stage.appendCommit",
    statusId: "monitor.status.warning",
    tone: "warning",
    unit: "msgs",
    chartColor: "--chart-3",
    base: 1420,
    amplitude: 260,
    slope: 5,
    spike: 620,
    stats: (series) => [
      { labelId: "monitor.stat.peakQueue", value: formatNumber(peak(series)) },
      { labelId: "monitor.stat.oldestWait", value: "4.8s" },
      { labelId: "monitor.stat.affectedChannels", value: "18" },
    ],
  },
  {
    key: "deliveryRate",
    titleId: "monitor.metrics.deliveryRate",
    stage: "onlineDelivery",
    stageLabelId: "monitor.stage.onlineDelivery",
    statusId: "monitor.status.normal",
    tone: "normal",
    unit: "msg/s",
    chartColor: "--chart-2",
    base: 35760,
    amplitude: 3200,
    spike: 4100,
    stats: (series) => [
      { labelId: "monitor.stat.avg", value: `${formatNumber(average(series))} msg/s` },
      { labelId: "monitor.stat.peak", value: `${formatNumber(peak(series))} msg/s` },
      { labelId: "monitor.stat.total5m", value: "10.8M" },
    ],
  },
  {
    key: "deliveryLatencyP99",
    titleId: "monitor.metrics.deliveryLatencyP99",
    stage: "onlineDelivery",
    stageLabelId: "monitor.stage.onlineDelivery",
    statusId: "monitor.status.warning",
    tone: "warning",
    unit: "ms",
    chartColor: "--chart-3",
    base: 82,
    amplitude: 16,
    spike: 52,
    decimals: 1,
    stats: (series) => [
      { labelId: "monitor.stat.p50", value: `${formatNumber(percentile(series, 0.5), 1)} ms` },
      { labelId: "monitor.stat.p95", value: `${formatNumber(percentile(series, 0.95), 1)} ms` },
      { labelId: "monitor.stat.timeouts", value: "21" },
    ],
  },
  {
    key: "fanOutRatio",
    titleId: "monitor.metrics.fanOutRatio",
    stage: "onlineDelivery",
    stageLabelId: "monitor.stage.onlineDelivery",
    statusId: "monitor.status.normal",
    tone: "normal",
    unit: "x",
    chartColor: "--chart-2",
    base: 2.86,
    amplitude: 0.28,
    spike: 0.45,
    decimals: 2,
    stats: (series) => [
      { labelId: "monitor.stat.avg", value: `${formatNumber(average(series), 2)}x` },
      { labelId: "monitor.stat.peak", value: `${formatNumber(peak(series), 2)}x` },
      { labelId: "monitor.stat.activeChannels", value: "18,420" },
    ],
  },
  {
    key: "offlineEnqueueRate",
    titleId: "monitor.metrics.offlineEnqueueRate",
    stage: "offlineRetry",
    stageLabelId: "monitor.stage.offlineRetry",
    statusId: "monitor.status.normal",
    tone: "normal",
    unit: "msg/s",
    chartColor: "--chart-5",
    base: 1860,
    amplitude: 310,
    spike: 620,
    stats: (series) => [
      { labelId: "monitor.stat.avg", value: `${formatNumber(average(series))} msg/s` },
      { labelId: "monitor.stat.peak", value: `${formatNumber(peak(series))} msg/s` },
      { labelId: "monitor.stat.offlineUsers", value: "42,018" },
    ],
  },
  {
    key: "retryQueueDepth",
    titleId: "monitor.metrics.retryQueueDepth",
    stage: "offlineRetry",
    stageLabelId: "monitor.stage.offlineRetry",
    statusId: "monitor.status.critical",
    tone: "critical",
    unit: "msgs",
    chartColor: "--chart-4",
    base: 820,
    amplitude: 130,
    slope: 8,
    spike: 420,
    stats: () => [
      { labelId: "monitor.stat.oldestWait", value: "18.4s" },
      { labelId: "monitor.stat.retrySuccess", value: "97.8%" },
      { labelId: "monitor.stat.topReason", value: "timeout" },
    ],
  },
  {
    key: "pathErrorRate",
    titleId: "monitor.metrics.pathErrorRate",
    stage: "errorClosure",
    stageLabelId: "monitor.stage.errorClosure",
    statusId: "monitor.status.warning",
    tone: "warning",
    unit: "%",
    chartColor: "--chart-4",
    base: 0.18,
    amplitude: 0.04,
    spike: 0.12,
    decimals: 2,
    stats: () => [
      { labelId: "monitor.stat.protocolErrors", value: "64" },
      { labelId: "monitor.stat.rateLimited", value: "210" },
      { labelId: "monitor.stat.commitErrors", value: "9" },
    ],
  },
]

function buildCard(definition: CardDefinition, timeRange: TimeRange): MonitorMetricCard {
  const series = makeSeries(definition, timeRange)
  const value = latest(series)
  const decimals = definition.decimals ?? 0

  return {
    key: definition.key,
    titleId: definition.titleId,
    stage: definition.stage,
    stageLabelId: definition.stageLabelId,
    statusId: definition.statusId,
    tone: definition.tone,
    unit: definition.unit,
    value: formatNumber(value, decimals),
    series,
    stats: definition.stats(series),
    chartColor: definition.chartColor,
  }
}

function snapshotFromCards(cards: MonitorMetricCard[]): MonitorSnapshotEntry[] {
  const byKey = new Map(cards.map((card) => [card.key, card]))
  const sendRate = byKey.get("sendRate")
  const deliveryRate = byKey.get("deliveryRate")
  const entryP99 = byKey.get("entryLatencyP99")
  const deliveryP99 = byKey.get("deliveryLatencyP99")
  const errorRate = byKey.get("pathErrorRate")
  const retryDepth = byKey.get("retryQueueDepth")

  return [
    { key: "send", labelId: "monitor.snapshot.send", value: sendRate?.value ?? "0", unit: "msg/s", tone: "normal" },
    { key: "delivery", labelId: "monitor.snapshot.delivery", value: deliveryRate?.value ?? "0", unit: "msg/s", tone: "normal" },
    { key: "entryP99", labelId: "monitor.snapshot.entryP99", value: entryP99?.value ?? "0", unit: "ms", tone: "warning" },
    { key: "deliveryP99", labelId: "monitor.snapshot.deliveryP99", value: deliveryP99?.value ?? "0", unit: "ms", tone: "warning" },
    { key: "errors", labelId: "monitor.snapshot.errors", value: errorRate?.value ?? "0", unit: "%", tone: "warning" },
    { key: "retryDepth", labelId: "monitor.snapshot.retryDepth", value: retryDepth?.value ?? "0", unit: "msgs", tone: "critical" },
    { key: "online", labelId: "monitor.snapshot.online", value: "128,420", unit: "conns", tone: "normal" },
  ]
}

export function buildPreviewMonitorModel(timeRange: TimeRange, isPaused: boolean): PreviewMonitorModel {
  const cards = cardDefinitions.map((definition) => buildCard(definition, timeRange))

  return {
    generatedAt: new Date(BASE_TIME).toISOString(),
    scopeLabelId: "monitor.scope.global",
    timeRange,
    isPaused,
    snapshot: snapshotFromCards(cards),
    cards,
  }
}
```

- [ ] **Step 5: Run the preview-data test and confirm it passes**

Run:

```bash
cd web && bun run test -- src/pages/monitor/preview-data.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit the preview model**

Run:

```bash
git add web/src/pages/monitor/types.ts web/src/pages/monitor/preview-data.ts web/src/pages/monitor/preview-data.test.ts
git commit -m "feat: add monitor preview card model"
```

Expected: commit succeeds with only the preview model and its test staged.

---

### Task 3: Monitor Card Components

**Files:**
- Create: `web/src/pages/monitor/components/monitor-toolbar.tsx`
- Create: `web/src/pages/monitor/components/monitor-snapshot-strip.tsx`
- Create: `web/src/pages/monitor/components/monitor-metric-card.tsx`
- Create: `web/src/pages/monitor/components/monitor-card-grid.tsx`

- [ ] **Step 1: Create the toolbar component**

Create `web/src/pages/monitor/components/monitor-toolbar.tsx`:

```tsx
import { Pause, Play } from "lucide-react"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

import type { TimeRange } from "../types"

type MonitorToolbarProps = {
  generatedAt: string
  isPaused: boolean
  onPauseToggle: () => void
  onTimeRangeChange: (range: TimeRange) => void
  scopeLabelId: string
  timeRange: TimeRange
}

const timeRanges: TimeRange[] = ["5m", "15m", "30m", "1h"]

export function MonitorToolbar({
  generatedAt,
  isPaused,
  onPauseToggle,
  onTimeRangeChange,
  scopeLabelId,
  timeRange,
}: MonitorToolbarProps) {
  const intl = useIntl()
  const formattedTime = new Intl.DateTimeFormat(intl.locale, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(generatedAt))

  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border/80 bg-card px-3 py-2">
      <span className="rounded-md border border-border bg-muted px-2 py-1 text-xs font-medium text-muted-foreground">
        {intl.formatMessage({ id: scopeLabelId })}
      </span>
      <span className="text-xs text-muted-foreground">
        {intl.formatMessage({ id: "monitor.generatedAt" }, { time: formattedTime })}
      </span>
      <div className="ml-auto inline-flex h-9 items-center rounded-md bg-muted p-1 text-muted-foreground">
        {timeRanges.map((range) => (
          <button
            aria-label={`${range} time range`}
            className={cn(
              "inline-flex h-7 min-w-10 items-center justify-center rounded-sm px-3 text-sm font-medium transition-colors",
              timeRange === range ? "bg-background text-foreground shadow-sm" : "hover:bg-background/60 hover:text-foreground",
            )}
            key={range}
            onClick={() => onTimeRangeChange(range)}
            type="button"
          >
            {range}
          </button>
        ))}
      </div>
      <Button
        aria-label={isPaused ? "Resume live preview" : "Pause live preview"}
        onClick={onPauseToggle}
        size="sm"
        variant={isPaused ? "default" : "outline"}
      >
        {isPaused ? <Play aria-hidden className="size-3.5" /> : <Pause aria-hidden className="size-3.5" />}
        {intl.formatMessage({ id: isPaused ? "monitor.controls.resume" : "monitor.controls.pause" })}
      </Button>
    </div>
  )
}
```

- [ ] **Step 2: Create the snapshot strip component**

Create `web/src/pages/monitor/components/monitor-snapshot-strip.tsx`:

```tsx
import { useIntl } from "react-intl"

import { cn } from "@/lib/utils"

import type { MonitorSnapshotEntry, MonitorTone } from "../types"

type MonitorSnapshotStripProps = {
  entries: MonitorSnapshotEntry[]
}

const toneClass: Record<MonitorTone, string> = {
  normal: "text-success",
  warning: "text-warning",
  critical: "text-destructive",
  preview: "text-muted-foreground",
}

export function MonitorSnapshotStrip({ entries }: MonitorSnapshotStripProps) {
  const intl = useIntl()

  return (
    <section className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-7">
      {entries.map((entry) => (
        <div className="rounded-lg border border-border/80 bg-card px-3 py-2" key={entry.key}>
          <div className="truncate text-xs font-medium text-muted-foreground">
            {intl.formatMessage({ id: entry.labelId })}
          </div>
          <div className="mt-1 flex items-baseline gap-1">
            <span className={cn("font-mono text-lg font-semibold text-foreground", toneClass[entry.tone])}>
              {entry.value}
            </span>
            {entry.unit ? <span className="text-xs text-muted-foreground">{entry.unit}</span> : null}
          </div>
        </div>
      ))}
    </section>
  )
}
```

- [ ] **Step 3: Create the metric card component**

Create `web/src/pages/monitor/components/monitor-metric-card.tsx`:

```tsx
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { useIntl } from "react-intl"

import { cn } from "@/lib/utils"

import type { MonitorMetricCard as MonitorMetricCardModel, MonitorTone } from "../types"

type MonitorMetricCardProps = {
  card: MonitorMetricCardModel
}

const toneStyles: Record<MonitorTone, { dot: string; text: string; panel: string }> = {
  normal: {
    dot: "bg-success",
    text: "text-success",
    panel: "border-border/80",
  },
  warning: {
    dot: "bg-warning",
    text: "text-warning",
    panel: "border-warning/30",
  },
  critical: {
    dot: "bg-destructive",
    text: "text-destructive",
    panel: "border-destructive/35",
  },
  preview: {
    dot: "bg-muted-foreground",
    text: "text-muted-foreground",
    panel: "border-border/80",
  },
}

function formatTime(timestamp: number) {
  return new Date(timestamp).toLocaleTimeString("en-US", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })
}

export function MonitorMetricCard({ card }: MonitorMetricCardProps) {
  const intl = useIntl()
  const tone = toneStyles[card.tone]
  const chartData = card.series.map((point) => ({
    time: formatTime(point.timestamp),
    value: point.value,
  }))
  const gradientId = `monitor-gradient-${card.key}`

  return (
    <article
      className={cn("flex min-h-[312px] flex-col rounded-lg border bg-card p-4", tone.panel)}
      data-testid="monitor-metric-card"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="truncate text-sm font-semibold text-foreground">
            {intl.formatMessage({ id: card.titleId })}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">
            {intl.formatMessage({ id: card.stageLabelId })} / {intl.formatMessage({ id: "monitor.scope.global" })}
          </p>
        </div>
        <span className={cn("inline-flex shrink-0 items-center gap-1.5 text-xs font-medium", tone.text)}>
          <span aria-hidden className={cn("size-2 rounded-full", tone.dot)} />
          {intl.formatMessage({ id: card.statusId })}
        </span>
      </div>

      <div className="mt-4 flex items-baseline gap-2">
        <span className="font-mono text-3xl font-semibold text-foreground">{card.value}</span>
        <span className="text-sm text-muted-foreground">{card.unit}</span>
      </div>

      <div className="mt-3 h-[142px]">
        <ResponsiveContainer height="100%" width="100%">
          <AreaChart data={chartData} margin={{ bottom: 0, left: 0, right: 0, top: 8 }}>
            <defs>
              <linearGradient id={gradientId} x1="0" x2="0" y1="0" y2="1">
                <stop offset="5%" stopColor={`var(${card.chartColor})`} stopOpacity={0.28} />
                <stop offset="95%" stopColor={`var(${card.chartColor})`} stopOpacity={0} />
              </linearGradient>
            </defs>
            <XAxis
              axisLine={false}
              dataKey="time"
              interval="preserveStartEnd"
              tickLine={false}
              tick={{ fill: "hsl(var(--muted-foreground))", fontSize: 10 }}
            />
            <YAxis
              axisLine={false}
              tickLine={false}
              tick={{ fill: "hsl(var(--muted-foreground))", fontSize: 10 }}
              width={42}
            />
            <Tooltip
              content={({ active, payload }) => {
                if (!active || !payload?.length) return null
                const value = payload[0].value
                return (
                  <div className="rounded-md border border-border bg-background px-2 py-1 text-xs shadow-md">
                    <div className="font-mono font-semibold text-foreground">
                      {String(value)} {card.unit}
                    </div>
                    <div className="text-muted-foreground">{payload[0].payload.time}</div>
                  </div>
                )
              }}
            />
            <Area
              dataKey="value"
              fill={`url(#${gradientId})`}
              isAnimationActive={false}
              stroke={`var(${card.chartColor})`}
              strokeWidth={2}
              type="monotone"
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>

      <dl className="mt-3 grid grid-cols-3 gap-2">
        {card.stats.map((stat) => (
          <div className="min-w-0 rounded-md border border-border/70 bg-background/55 px-2 py-1.5" key={stat.labelId}>
            <dt className="truncate text-[11px] text-muted-foreground">{intl.formatMessage({ id: stat.labelId })}</dt>
            <dd className="mt-1 truncate font-mono text-xs font-semibold text-foreground">{stat.value}</dd>
          </div>
        ))}
      </dl>
    </article>
  )
}
```

- [ ] **Step 4: Create the responsive card grid**

Create `web/src/pages/monitor/components/monitor-card-grid.tsx`:

```tsx
import { MonitorMetricCard } from "./monitor-metric-card"
import type { MonitorMetricCard as MonitorMetricCardModel } from "../types"

type MonitorCardGridProps = {
  cards: MonitorMetricCardModel[]
}

export function MonitorCardGrid({ cards }: MonitorCardGridProps) {
  return (
    <section className="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
      {cards.map((card) => (
        <MonitorMetricCard card={card} key={card.key} />
      ))}
    </section>
  )
}
```

- [ ] **Step 5: Commit the new components**

Run:

```bash
git add web/src/pages/monitor/components/monitor-toolbar.tsx web/src/pages/monitor/components/monitor-snapshot-strip.tsx web/src/pages/monitor/components/monitor-metric-card.tsx web/src/pages/monitor/components/monitor-card-grid.tsx
git commit -m "feat: add monitor card wall components"
```

Expected: commit succeeds with only the new component files staged.

---

### Task 4: Page Assembly And i18n

**Files:**
- Modify: `web/src/pages/monitor/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Replace the old monitor page with preview card-wall assembly**

Replace `web/src/pages/monitor/page.tsx` with:

```tsx
import { useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"

import { MonitorCardGrid } from "./components/monitor-card-grid"
import { MonitorSnapshotStrip } from "./components/monitor-snapshot-strip"
import { MonitorToolbar } from "./components/monitor-toolbar"
import { buildPreviewMonitorModel } from "./preview-data"
import type { TimeRange } from "./types"

export function MonitorPage() {
  const intl = useIntl()
  const [timeRange, setTimeRange] = useState<TimeRange>("15m")
  const [isPaused, setIsPaused] = useState(false)
  const model = useMemo(() => buildPreviewMonitorModel(timeRange, isPaused), [isPaused, timeRange])

  return (
    <PageContainer className="max-w-[1600px] gap-4">
      <section className="border-b border-border/80 pb-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-[11px] font-semibold uppercase text-muted-foreground">
                {intl.formatMessage({ id: "nav.path.business.monitor" })}
              </span>
              <span className="rounded-md border border-border bg-card px-2 py-1 text-xs font-medium text-muted-foreground">
                {intl.formatMessage({ id: "monitor.previewBadge" })}
              </span>
            </div>
            <h1 className="mt-3 text-3xl font-semibold text-foreground sm:text-4xl">
              {intl.formatMessage({ id: "monitor.title" })}
            </h1>
            <p className="mt-2 max-w-3xl text-sm leading-6 text-muted-foreground">
              {intl.formatMessage({ id: "monitor.cardWallDescription" })}
            </p>
          </div>
        </div>
        <div className="mt-4">
          <MonitorToolbar
            generatedAt={model.generatedAt}
            isPaused={model.isPaused}
            onPauseToggle={() => setIsPaused((current) => !current)}
            onTimeRangeChange={setTimeRange}
            scopeLabelId={model.scopeLabelId}
            timeRange={model.timeRange}
          />
        </div>
      </section>

      <MonitorSnapshotStrip entries={model.snapshot} />
      <MonitorCardGrid cards={model.cards} />
    </PageContainer>
  )
}
```

- [ ] **Step 2: Add English monitor messages**

In `web/src/i18n/messages/en.ts`, replace the existing monitor block from `"monitor.title"` through `"monitor.metrics.activeChannels"` with:

```ts
  "monitor.title": "Live Monitor",
  "monitor.description": "Real-time message throughput, connection trends, and latency percentiles.",
  "monitor.cardWallDescription": "Global business message path health trends.",
  "monitor.previewBadge": "UI Preview",
  "monitor.generatedAt": "Updated {time}",
  "monitor.scope.global": "Global Aggregate",
  "monitor.controls.node": "Node",
  "monitor.controls.allNodes": "All Nodes",
  "monitor.controls.timeRange": "Time Range",
  "monitor.controls.pause": "Pause",
  "monitor.controls.resume": "Resume",
  "monitor.section.messageFlow": "Message Flow",
  "monitor.section.connections": "Connection Status",
  "monitor.stage.sendEntry": "Send Entry",
  "monitor.stage.appendCommit": "Append And Commit",
  "monitor.stage.onlineDelivery": "Online Delivery",
  "monitor.stage.offlineRetry": "Offline And Retry",
  "monitor.stage.errorClosure": "Error Closure",
  "monitor.status.normal": "Normal",
  "monitor.status.warning": "Warning",
  "monitor.status.critical": "Critical",
  "monitor.status.preview": "Preview",
  "monitor.snapshot.send": "Send",
  "monitor.snapshot.delivery": "Delivery",
  "monitor.snapshot.entryP99": "Entry P99",
  "monitor.snapshot.deliveryP99": "Delivery P99",
  "monitor.snapshot.errors": "Errors",
  "monitor.snapshot.retryDepth": "Retry Depth",
  "monitor.snapshot.online": "Online",
  "monitor.metrics.sendRate": "Send Rate",
  "monitor.metrics.sendSuccessRate": "Send Success Rate",
  "monitor.metrics.entryLatencyP99": "Entry Latency P99",
  "monitor.metrics.commitRate": "Commit Rate",
  "monitor.metrics.commitLatencyP99": "Commit Latency P99",
  "monitor.metrics.pendingCommitBacklog": "Pending Commit Backlog",
  "monitor.metrics.deliveryRate": "Delivery Rate",
  "monitor.metrics.deliveryLatencyP99": "Delivery Latency P99",
  "monitor.metrics.fanOutRatio": "Fan-out Ratio",
  "monitor.metrics.offlineEnqueueRate": "Offline Enqueue Rate",
  "monitor.metrics.retryQueueDepth": "Retry Queue Depth",
  "monitor.metrics.pathErrorRate": "Path Error Rate",
  "monitor.metrics.deliverRate": "Deliver Rate",
  "monitor.metrics.sendLatencyP99": "Send Latency P99",
  "monitor.metrics.deliveryFailRate": "Delivery Fail Rate",
  "monitor.metrics.sendFailRate": "Send Fail Rate",
  "monitor.metrics.fanOutRate": "Fan-out Rate",
  "monitor.metrics.onlineConnections": "Online Connections",
  "monitor.metrics.activeChannels": "Active Channels",
  "monitor.stat.avg": "Avg",
  "monitor.stat.peak": "Peak",
  "monitor.stat.total5m": "5m Total",
  "monitor.stat.failed": "Failed",
  "monitor.stat.topError": "Top Error",
  "monitor.stat.affectedNodes": "Affected Nodes",
  "monitor.stat.p50": "P50",
  "monitor.stat.p95": "P95",
  "monitor.stat.peakP99": "Peak P99",
  "monitor.stat.batches": "Batches",
  "monitor.stat.slowCommits": "Slow Commits",
  "monitor.stat.peakQueue": "Peak Queue",
  "monitor.stat.oldestWait": "Oldest Wait",
  "monitor.stat.affectedChannels": "Affected Channels",
  "monitor.stat.timeouts": "Timeouts",
  "monitor.stat.activeChannels": "Active Channels",
  "monitor.stat.offlineUsers": "Offline Users",
  "monitor.stat.retrySuccess": "Retry Success",
  "monitor.stat.topReason": "Top Reason",
  "monitor.stat.protocolErrors": "Protocol Errors",
  "monitor.stat.rateLimited": "Rate Limited",
  "monitor.stat.commitErrors": "Commit Errors",
```

- [ ] **Step 3: Add Simplified Chinese monitor messages**

In `web/src/i18n/messages/zh-CN.ts`, replace the existing monitor block from `"monitor.title"` through `"monitor.metrics.activeChannels"` with:

```ts
  "monitor.title": "实时监控",
  "monitor.description": "消息吞吐量、连接数趋势与延迟分位数实时面板。",
  "monitor.cardWallDescription": "全局业务消息链路健康趋势。",
  "monitor.previewBadge": "UI 预览",
  "monitor.generatedAt": "更新时间 {time}",
  "monitor.scope.global": "全局聚合",
  "monitor.controls.node": "节点",
  "monitor.controls.allNodes": "所有节点",
  "monitor.controls.timeRange": "时间范围",
  "monitor.controls.pause": "暂停",
  "monitor.controls.resume": "恢复",
  "monitor.section.messageFlow": "消息流量",
  "monitor.section.connections": "连接状态",
  "monitor.stage.sendEntry": "入口发送",
  "monitor.stage.appendCommit": "写入提交",
  "monitor.stage.onlineDelivery": "在线投递",
  "monitor.stage.offlineRetry": "离线与重试",
  "monitor.stage.errorClosure": "异常闭环",
  "monitor.status.normal": "正常",
  "monitor.status.warning": "波动",
  "monitor.status.critical": "告警",
  "monitor.status.preview": "预览",
  "monitor.snapshot.send": "发送",
  "monitor.snapshot.delivery": "投递",
  "monitor.snapshot.entryP99": "入口 P99",
  "monitor.snapshot.deliveryP99": "投递 P99",
  "monitor.snapshot.errors": "错误率",
  "monitor.snapshot.retryDepth": "重试深度",
  "monitor.snapshot.online": "在线连接",
  "monitor.metrics.sendRate": "发送速率",
  "monitor.metrics.sendSuccessRate": "发送成功率",
  "monitor.metrics.entryLatencyP99": "入口延迟 P99",
  "monitor.metrics.commitRate": "提交速率",
  "monitor.metrics.commitLatencyP99": "写入延迟 P99",
  "monitor.metrics.pendingCommitBacklog": "待提交积压",
  "monitor.metrics.deliveryRate": "投递速率",
  "monitor.metrics.deliveryLatencyP99": "投递延迟 P99",
  "monitor.metrics.fanOutRatio": "扇出倍率",
  "monitor.metrics.offlineEnqueueRate": "离线入队速率",
  "monitor.metrics.retryQueueDepth": "重试队列深度",
  "monitor.metrics.pathErrorRate": "链路错误率",
  "monitor.metrics.deliverRate": "投递速率",
  "monitor.metrics.sendLatencyP99": "发送延迟 P99",
  "monitor.metrics.deliveryFailRate": "投递失败率",
  "monitor.metrics.sendFailRate": "发送失败率",
  "monitor.metrics.fanOutRate": "扇出倍率",
  "monitor.metrics.onlineConnections": "在线连接数",
  "monitor.metrics.activeChannels": "活跃频道数",
  "monitor.stat.avg": "平均",
  "monitor.stat.peak": "峰值",
  "monitor.stat.total5m": "5 分钟总量",
  "monitor.stat.failed": "失败数",
  "monitor.stat.topError": "主要错误",
  "monitor.stat.affectedNodes": "影响节点",
  "monitor.stat.p50": "P50",
  "monitor.stat.p95": "P95",
  "monitor.stat.peakP99": "P99 峰值",
  "monitor.stat.batches": "批次数",
  "monitor.stat.slowCommits": "慢提交",
  "monitor.stat.peakQueue": "队列峰值",
  "monitor.stat.oldestWait": "最老等待",
  "monitor.stat.affectedChannels": "影响频道",
  "monitor.stat.timeouts": "超时数",
  "monitor.stat.activeChannels": "活跃频道",
  "monitor.stat.offlineUsers": "离线用户",
  "monitor.stat.retrySuccess": "重试成功",
  "monitor.stat.topReason": "主要原因",
  "monitor.stat.protocolErrors": "协议错误",
  "monitor.stat.rateLimited": "限流拒绝",
  "monitor.stat.commitErrors": "提交错误",
```

- [ ] **Step 4: Run the page and preview-data tests**

Run:

```bash
cd web && bun run test -- src/pages/monitor/page.test.tsx src/pages/monitor/preview-data.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit the green page contract, page assembly, and messages**

Run:

```bash
git add web/src/pages/monitor/page.test.tsx web/src/pages/monitor/page.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: render business monitor card wall"
```

Expected: commit succeeds with the page contract test, page assembly, and i18n files staged.

---

### Task 5: Remove Old API-Backed Monitor UI Files

**Files:**
- Delete: `web/src/pages/monitor/use-monitor-data.ts`
- Delete: `web/src/pages/monitor/components/chart-grid.tsx`
- Delete: `web/src/pages/monitor/components/metric-chart.tsx`
- Delete: `web/src/pages/monitor/components/monitor-controls.tsx`

- [ ] **Step 1: Delete obsolete monitor files**

Run:

```bash
rm web/src/pages/monitor/use-monitor-data.ts \
  web/src/pages/monitor/components/chart-grid.tsx \
  web/src/pages/monitor/components/metric-chart.tsx \
  web/src/pages/monitor/components/monitor-controls.tsx
```

- [ ] **Step 2: Verify there are no stale imports**

Run:

```bash
rg -n "use-monitor-data|ChartGrid|MetricChart|MonitorControls|MetricConfig|MonitorData|NodeId|MonitorNodeOption" web/src/pages/monitor web/src
```

Expected: no output, except references in git diff context are acceptable before commit.

- [ ] **Step 3: Run focused tests**

Run:

```bash
cd web && bun run test -- src/pages/monitor/page.test.tsx src/pages/monitor/preview-data.test.ts
```

Expected: PASS.

- [ ] **Step 4: Commit cleanup**

Run:

```bash
git add -u web/src/pages/monitor
git commit -m "chore: remove old monitor chart grid"
```

Expected: commit succeeds with only deleted obsolete monitor files staged.

---

### Task 6: Final Verification

**Files:**
- Verify: `web/src/pages/monitor/*`
- Verify: `web/src/i18n/messages/en.ts`
- Verify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Run the focused monitor tests**

Run:

```bash
cd web && bun run test -- src/pages/monitor/page.test.tsx src/pages/monitor/preview-data.test.ts
```

Expected: PASS with 2 test files passing.

- [ ] **Step 2: Run TypeScript build**

Run:

```bash
cd web && bunx tsc -b
```

Expected: exit code 0.

- [ ] **Step 3: Run production build**

Run:

```bash
cd web && bunx vite build
```

Expected: exit code 0. If this rewrites generated dist assets such as `web/dist/index.html`, restore generated churn that is unrelated to this UI source change before final review.

- [ ] **Step 4: Inspect final source diff**

Run:

```bash
git status --short
git diff -- web/src/pages/monitor web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
```

Expected: only intentional monitor page, preview model, component, test, and i18n changes remain. Existing unrelated worktree changes outside this scope must remain untouched.
