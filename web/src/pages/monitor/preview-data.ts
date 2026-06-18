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

const GENERATED_AT = "2026-06-18T03:30:00.000Z"
const GENERATED_AT_MS = Date.parse(GENERATED_AT)

const rangeConfig: Record<TimeRange, { points: number; stepMs: number; scale: number }> = {
  "5m": { points: 30, stepMs: 10_000, scale: 0.82 },
  "15m": { points: 45, stepMs: 20_000, scale: 1 },
  "30m": { points: 60, stepMs: 30_000, scale: 1.12 },
  "1h": { points: 72, stepMs: 50_000, scale: 1.28 },
}

type CardSpec = {
  key: MonitorMetricKey
  titleId: string
  stage: MonitorStage
  tone: MonitorTone
  unit: string
  chartColor: string
  base: number
  amplitude: number
  drift?: number
  pulse?: number
  precision?: number
  stats: [StatSpec, StatSpec, StatSpec]
}

type DynamicStatKind = "avg" | "peak" | "latest" | "p50" | "p95" | "peakP99"

type StatSpec = {
  labelId: string
  kind?: DynamicStatKind
  value?: string
}

const stageLabelIds: Record<MonitorStage, string> = {
  sendEntry: "monitor.stage.sendEntry",
  appendCommit: "monitor.stage.appendCommit",
  conversationSync: "monitor.stage.conversationSync",
  onlineDelivery: "monitor.stage.onlineDelivery",
  offlineRetry: "monitor.stage.offlineRetry",
  errorClosure: "monitor.stage.errorClosure",
}

const statusByTone: Record<MonitorTone, string> = {
  normal: "monitor.status.normal",
  warning: "monitor.status.warning",
  critical: "monitor.status.critical",
  preview: "monitor.status.preview",
}

const cardSpecs: CardSpec[] = [
  {
    key: "sendRate",
    titleId: "monitor.metrics.sendRate",
    stage: "sendEntry",
    tone: "normal",
    unit: "msg/s",
    chartColor: "#2563eb",
    base: 1240,
    amplitude: 130,
    pulse: 165,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.total5m", value: "372k" },
    ],
  },
  {
    key: "sendSuccessRate",
    titleId: "monitor.metrics.sendSuccessRate",
    stage: "sendEntry",
    tone: "normal",
    unit: "%",
    chartColor: "#16a34a",
    base: 99.72,
    amplitude: 0.08,
    precision: 2,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.failed", value: "41" },
      { labelId: "monitor.stat.topError", value: "auth_expired" },
    ],
  },
  {
    key: "entryLatencyP99",
    titleId: "monitor.metrics.entryLatencyP99",
    stage: "sendEntry",
    tone: "warning",
    unit: "ms",
    chartColor: "#d97706",
    base: 36,
    amplitude: 7,
    pulse: 18,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.p50", kind: "p50" },
      { labelId: "monitor.stat.p95", kind: "p95" },
      { labelId: "monitor.stat.peakP99", kind: "peakP99" },
    ],
  },
  {
    key: "commitRate",
    titleId: "monitor.metrics.commitRate",
    stage: "appendCommit",
    tone: "normal",
    unit: "msg/s",
    chartColor: "#0f766e",
    base: 1215,
    amplitude: 105,
    pulse: 128,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.batches", value: "362" },
    ],
  },
  {
    key: "commitLatencyP99",
    titleId: "monitor.metrics.commitLatencyP99",
    stage: "appendCommit",
    tone: "normal",
    unit: "ms",
    chartColor: "#7c3aed",
    base: 28,
    amplitude: 4.5,
    pulse: 9,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.p50", kind: "p50" },
      { labelId: "monitor.stat.p95", kind: "p95" },
      { labelId: "monitor.stat.slowCommits", value: "7" },
    ],
  },
  {
    key: "pendingCommitBacklog",
    titleId: "monitor.metrics.pendingCommitBacklog",
    stage: "appendCommit",
    tone: "warning",
    unit: "",
    chartColor: "#ca8a04",
    base: 88,
    amplitude: 24,
    drift: 0.7,
    pulse: 44,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peakQueue", kind: "peak" },
      { labelId: "monitor.stat.oldestWait", value: "4.8s" },
    ],
  },
  {
    key: "conversationSyncRate",
    titleId: "monitor.metrics.conversationSyncRate",
    stage: "conversationSync",
    tone: "normal",
    unit: "req/s",
    chartColor: "#0284c7",
    base: 312,
    amplitude: 34,
    pulse: 42,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.total5m", value: "93.6k" },
    ],
  },
  {
    key: "conversationSyncLatencyP99",
    titleId: "monitor.metrics.conversationSyncLatencyP99",
    stage: "conversationSync",
    tone: "warning",
    unit: "ms",
    chartColor: "#9333ea",
    base: 24,
    amplitude: 4.2,
    pulse: 12,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.p50", kind: "p50" },
      { labelId: "monitor.stat.p95", kind: "p95" },
      { labelId: "monitor.stat.peakP99", kind: "peakP99" },
    ],
  },
  {
    key: "conversationSyncErrorRate",
    titleId: "monitor.metrics.conversationSyncErrorRate",
    stage: "conversationSync",
    tone: "critical",
    unit: "%",
    chartColor: "#e11d48",
    base: 0.06,
    amplitude: 0.02,
    pulse: 0.08,
    precision: 2,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.topReason", value: "none" },
    ],
  },
  {
    key: "conversationReturnedItems",
    titleId: "monitor.metrics.conversationReturnedItems",
    stage: "conversationSync",
    tone: "normal",
    unit: "items",
    chartColor: "#059669",
    base: 36,
    amplitude: 5.5,
    pulse: 13,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.activeChannels", value: "1,906" },
    ],
  },
  {
    key: "conversationRecentLoadLatencyP99",
    titleId: "monitor.metrics.conversationRecentLoadLatencyP99",
    stage: "conversationSync",
    tone: "warning",
    unit: "ms",
    chartColor: "#c026d3",
    base: 42,
    amplitude: 6.5,
    pulse: 16,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.p50", kind: "p50" },
      { labelId: "monitor.stat.p95", kind: "p95" },
      { labelId: "monitor.stat.peakP99", kind: "peakP99" },
    ],
  },
  {
    key: "conversationActiveDirtyRows",
    titleId: "monitor.metrics.conversationActiveDirtyRows",
    stage: "conversationSync",
    tone: "warning",
    unit: "rows",
    chartColor: "#f59e0b",
    base: 74,
    amplitude: 21,
    drift: 0.45,
    pulse: 33,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peakQueue", kind: "peak" },
      { labelId: "monitor.stat.affectedChannels", value: "412" },
    ],
  },
  {
    key: "conversationActiveOldestDirtyAge",
    titleId: "monitor.metrics.conversationActiveOldestDirtyAge",
    stage: "conversationSync",
    tone: "warning",
    unit: "s",
    chartColor: "#ea580c",
    base: 2.6,
    amplitude: 0.6,
    pulse: 1.4,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.oldestWait", value: "5.2s" },
    ],
  },
  {
    key: "conversationActiveFlushLatencyP99",
    titleId: "monitor.metrics.conversationActiveFlushLatencyP99",
    stage: "conversationSync",
    tone: "warning",
    unit: "ms",
    chartColor: "#0d9488",
    base: 18,
    amplitude: 3.4,
    pulse: 8,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.p50", kind: "p50" },
      { labelId: "monitor.stat.p95", kind: "p95" },
      { labelId: "monitor.stat.peakP99", kind: "peakP99" },
    ],
  },
  {
    key: "conversationActiveFlushErrorRate",
    titleId: "monitor.metrics.conversationActiveFlushErrorRate",
    stage: "conversationSync",
    tone: "critical",
    unit: "%",
    chartColor: "#dc2626",
    base: 0.03,
    amplitude: 0.012,
    pulse: 0.05,
    precision: 2,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.commitErrors", value: "2" },
    ],
  },
  {
    key: "conversationAuthorityPressureRate",
    titleId: "monitor.metrics.conversationAuthorityPressureRate",
    stage: "conversationSync",
    tone: "warning",
    unit: "events/s",
    chartColor: "#4f46e5",
    base: 4.1,
    amplitude: 0.7,
    pulse: 1.4,
    precision: 2,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.affectedNodes", value: "3" },
    ],
  },
  {
    key: "deliveryRate",
    titleId: "monitor.metrics.deliveryRate",
    stage: "onlineDelivery",
    tone: "normal",
    unit: "msg/s",
    chartColor: "#0891b2",
    base: 1186,
    amplitude: 112,
    pulse: 104,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.total5m", value: "356k" },
    ],
  },
  {
    key: "deliveryLatencyP99",
    titleId: "monitor.metrics.deliveryLatencyP99",
    stage: "onlineDelivery",
    tone: "warning",
    unit: "ms",
    chartColor: "#db2777",
    base: 52,
    amplitude: 8,
    pulse: 24,
    precision: 1,
    stats: [
      { labelId: "monitor.stat.p50", kind: "p50" },
      { labelId: "monitor.stat.p95", kind: "p95" },
      { labelId: "monitor.stat.timeouts", value: "23" },
    ],
  },
  {
    key: "fanOutRatio",
    titleId: "monitor.metrics.fanOutRatio",
    stage: "onlineDelivery",
    tone: "preview",
    unit: "x",
    chartColor: "#4f46e5",
    base: 2.8,
    amplitude: 0.35,
    precision: 2,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.activeChannels", value: "2,148" },
    ],
  },
  {
    key: "offlineEnqueueRate",
    titleId: "monitor.metrics.offlineEnqueueRate",
    stage: "offlineRetry",
    tone: "normal",
    unit: "msg/s",
    chartColor: "#64748b",
    base: 148,
    amplitude: 28,
    pulse: 56,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peak", kind: "peak" },
      { labelId: "monitor.stat.offlineUsers", value: "6,420" },
    ],
  },
  {
    key: "retryQueueDepth",
    titleId: "monitor.metrics.retryQueueDepth",
    stage: "offlineRetry",
    tone: "warning",
    unit: "",
    chartColor: "#ea580c",
    base: 214,
    amplitude: 36,
    drift: 1.1,
    pulse: 68,
    stats: [
      { labelId: "monitor.stat.avg", kind: "avg" },
      { labelId: "monitor.stat.peakQueue", kind: "peak" },
      { labelId: "monitor.stat.retrySuccess", value: "97.8%" },
    ],
  },
  {
    key: "pathErrorRate",
    titleId: "monitor.metrics.pathErrorRate",
    stage: "errorClosure",
    tone: "critical",
    unit: "%",
    chartColor: "#dc2626",
    base: 0.21,
    amplitude: 0.08,
    pulse: 0.22,
    precision: 2,
    stats: [
      { labelId: "monitor.stat.topReason", value: "timeout" },
      { labelId: "monitor.stat.protocolErrors", value: "14" },
      { labelId: "monitor.stat.rateLimited", value: "8" },
    ],
  },
]

function buildSeries(spec: CardSpec, range: TimeRange): MonitorPoint[] {
  const config = rangeConfig[range]
  const values: MonitorPoint[] = []

  for (let index = 0; index < config.points; index += 1) {
    const wave = Math.sin(index / 4.2) * spec.amplitude
    const secondary = Math.cos(index / 7.5) * spec.amplitude * 0.42
    const burst = index % 17 === 11 ? (spec.pulse ?? spec.amplitude * 0.8) : 0
    const taper = spec.drift ? index * spec.drift : 0
    const value = Math.max(0, (spec.base + wave + secondary + burst + taper) * config.scale)
    const precision = spec.precision ?? 0

    values.push({
      timestamp: GENERATED_AT_MS - (config.points - index - 1) * config.stepMs,
      value: Number(value.toFixed(precision)),
    })
  }

  return values
}

function formatValue(value: number, precision = 0) {
  return value.toLocaleString("en-US", {
    maximumFractionDigits: precision,
    minimumFractionDigits: precision,
  })
}

function appendUnit(value: string, unit: string) {
  if (!unit) return value
  if (unit === "%" || unit === "x") return `${value}${unit}`
  return `${value} ${unit}`
}

function percentile(values: number[], ratio: number) {
  const sorted = [...values].sort((a, b) => a - b)
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil(sorted.length * ratio) - 1))
  return sorted[index] ?? 0
}

function dynamicStatValue(kind: DynamicStatKind, values: number[]) {
  if (kind === "avg") return values.reduce((sum, value) => sum + value, 0) / values.length
  if (kind === "peak" || kind === "peakP99") return Math.max(...values)
  if (kind === "p50") return percentile(values, 0.5)
  if (kind === "p95") return percentile(values, 0.95)
  return values.at(-1) ?? 0
}

function buildStats(spec: CardSpec, series: MonitorPoint[]) {
  const values = series.map((point) => point.value)
  const precision = spec.precision ?? 0

  return spec.stats.map((stat) => {
    if (stat.value !== undefined) {
      return {
        labelId: stat.labelId,
        value: stat.value,
      }
    }

    const value = dynamicStatValue(stat.kind ?? "latest", values)

    return {
      labelId: stat.labelId,
      value: appendUnit(formatValue(value, precision), spec.unit),
    }
  })
}

function latest(series: MonitorPoint[]) {
  return series.at(-1)?.value ?? 0
}

function makeCard(spec: CardSpec, range: TimeRange): MonitorMetricCard {
  const series = buildSeries(spec, range)
  const precision = spec.precision ?? 0

  return {
    key: spec.key,
    titleId: spec.titleId,
    helpId: `monitor.help.${spec.key}`,
    stage: spec.stage,
    stageLabelId: stageLabelIds[spec.stage],
    statusId: statusByTone[spec.tone],
    tone: spec.tone,
    unit: spec.unit,
    value: formatValue(latest(series), precision),
    available: true,
    series,
    stats: buildStats(spec, series),
    chartColor: spec.chartColor,
  }
}

function makeSnapshot(cards: MonitorMetricCard[]): MonitorSnapshotEntry[] {
  const byKey = new Map(cards.map((card) => [card.key, card]))

  return [
    { key: "send", labelId: "monitor.snapshot.send", value: byKey.get("sendRate")?.value ?? "-", unit: "msg/s", tone: "normal" },
    { key: "delivery", labelId: "monitor.snapshot.delivery", value: byKey.get("deliveryRate")?.value ?? "-", unit: "msg/s", tone: "normal" },
    { key: "entryP99", labelId: "monitor.snapshot.entryP99", value: byKey.get("entryLatencyP99")?.value ?? "-", unit: "ms", tone: "warning" },
    { key: "conversationSyncP99", labelId: "monitor.snapshot.conversationSyncP99", value: byKey.get("conversationSyncLatencyP99")?.value ?? "-", unit: "ms", tone: "warning" },
    { key: "conversationSyncErrors", labelId: "monitor.snapshot.conversationSyncErrors", value: byKey.get("conversationSyncErrorRate")?.value ?? "-", unit: "%", tone: "critical" },
    { key: "conversationDirtyAge", labelId: "monitor.snapshot.conversationDirtyAge", value: byKey.get("conversationActiveOldestDirtyAge")?.value ?? "-", unit: "s", tone: "warning" },
    { key: "conversationFlushErrors", labelId: "monitor.snapshot.conversationFlushErrors", value: byKey.get("conversationActiveFlushErrorRate")?.value ?? "-", unit: "%", tone: "critical" },
    { key: "deliveryP99", labelId: "monitor.snapshot.deliveryP99", value: byKey.get("deliveryLatencyP99")?.value ?? "-", unit: "ms", tone: "warning" },
    { key: "errors", labelId: "monitor.snapshot.errors", value: byKey.get("pathErrorRate")?.value ?? "-", unit: "%", tone: "critical" },
    { key: "retryDepth", labelId: "monitor.snapshot.retryDepth", value: byKey.get("retryQueueDepth")?.value ?? "-", tone: "warning" },
    { key: "online", labelId: "monitor.snapshot.online", value: "84,320", tone: "preview" },
  ]
}

export function buildPreviewMonitorModel(timeRange: TimeRange, isPaused: boolean): PreviewMonitorModel {
  const cards = cardSpecs.map((spec) => makeCard(spec, timeRange))

  return {
    generatedAt: GENERATED_AT,
    scopeLabelId: "monitor.scope.global",
    timeRange,
    isPaused,
    snapshot: makeSnapshot(cards),
    cards,
  }
}
