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
  stats: [string, string, string]
}

const stageLabelIds: Record<MonitorStage, string> = {
  sendEntry: "monitor.stage.sendEntry",
  appendCommit: "monitor.stage.appendCommit",
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
    stats: ["monitor.stat.avg", "monitor.stat.peak", "monitor.stat.total5m"],
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
    stats: ["monitor.stat.avg", "monitor.stat.failed", "monitor.stat.topError"],
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
    stats: ["monitor.stat.p50", "monitor.stat.p95", "monitor.stat.peakP99"],
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
    stats: ["monitor.stat.avg", "monitor.stat.peak", "monitor.stat.batches"],
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
    stats: ["monitor.stat.p50", "monitor.stat.p95", "monitor.stat.slowCommits"],
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
    stats: ["monitor.stat.avg", "monitor.stat.peakQueue", "monitor.stat.oldestWait"],
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
    stats: ["monitor.stat.avg", "monitor.stat.peak", "monitor.stat.affectedChannels"],
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
    stats: ["monitor.stat.p50", "monitor.stat.p95", "monitor.stat.timeouts"],
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
    stats: ["monitor.stat.avg", "monitor.stat.peak", "monitor.stat.activeChannels"],
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
    stats: ["monitor.stat.avg", "monitor.stat.peak", "monitor.stat.offlineUsers"],
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
    stats: ["monitor.stat.avg", "monitor.stat.peakQueue", "monitor.stat.retrySuccess"],
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
    stats: ["monitor.stat.topReason", "monitor.stat.protocolErrors", "monitor.stat.rateLimited"],
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

function buildStats(spec: CardSpec, series: MonitorPoint[]) {
  const values = series.map((point) => point.value)
  const avg = values.reduce((sum, value) => sum + value, 0) / values.length
  const peak = Math.max(...values)
  const precision = spec.precision ?? 0

  return spec.stats.map((labelId, index) => {
    const value = index === 0 ? avg : index === 1 ? peak : values.at(-1) ?? 0
    const suffix = spec.unit ? ` ${spec.unit}` : ""

    return {
      labelId,
      value: `${formatValue(value, precision)}${suffix}`,
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
    stage: spec.stage,
    stageLabelId: stageLabelIds[spec.stage],
    statusId: statusByTone[spec.tone],
    tone: spec.tone,
    unit: spec.unit,
    value: formatValue(latest(series), precision),
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
