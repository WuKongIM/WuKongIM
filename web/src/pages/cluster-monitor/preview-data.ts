import type {
  ClusterMonitorMetricCard,
  ClusterMonitorMetricKey,
  ClusterMonitorPoint,
  ClusterMonitorSnapshotEntry,
  ClusterMonitorStage,
  ClusterMonitorTimeRange,
  ClusterMonitorTone,
  PreviewClusterMonitorModel,
} from "./types"

const GENERATED_AT = "2026-06-18T03:45:00.000Z"
const GENERATED_AT_MS = Date.parse(GENERATED_AT)

const rangeConfig: Record<ClusterMonitorTimeRange, { points: number; stepMs: number; scale: number }> = {
  "5m": { points: 30, stepMs: 10_000, scale: 0.82 },
  "15m": { points: 45, stepMs: 20_000, scale: 1 },
  "30m": { points: 60, stepMs: 30_000, scale: 1.12 },
  "1h": { points: 72, stepMs: 50_000, scale: 1.28 },
}

type DynamicStatKind = "avg" | "peak" | "latest" | "p50" | "p95" | "peakP99" | "min"

type StatSpec = {
  labelId: string
  kind?: DynamicStatKind
  value?: string
}

type CardSpec = {
  key: ClusterMonitorMetricKey
  titleId: string
  stage: ClusterMonitorStage
  tone: ClusterMonitorTone
  unit: string
  chartColor: string
  base: number
  amplitude: number
  drift?: number
  pulse?: number
  precision?: number
  stats: [StatSpec, StatSpec, StatSpec]
}

const stageLabelIds: Record<ClusterMonitorStage, string> = {
  controlPlane: "clusterMonitor.stage.controlPlane",
  slotReplication: "clusterMonitor.stage.slotReplication",
  channelReplication: "clusterMonitor.stage.channelReplication",
  internalNetwork: "clusterMonitor.stage.internalNetwork",
  runtimePressure: "clusterMonitor.stage.runtimePressure",
  incidentClosure: "clusterMonitor.stage.incidentClosure",
  sendEntry: "monitor.stage.sendEntry",
  appendCommit: "monitor.stage.appendCommit",
  conversationSync: "monitor.stage.conversationSync",
  onlineDelivery: "monitor.stage.onlineDelivery",
  offlineRetry: "monitor.stage.offlineRetry",
  errorClosure: "monitor.stage.errorClosure",
}

const statusByTone: Record<ClusterMonitorTone, string> = {
  normal: "clusterMonitor.status.normal",
  warning: "clusterMonitor.status.warning",
  critical: "clusterMonitor.status.critical",
  preview: "clusterMonitor.status.preview",
}

const cardSpecs: CardSpec[] = [
  {
    key: "controllerApplyGap",
    titleId: "clusterMonitor.metrics.controllerApplyGap",
    stage: "controlPlane",
    tone: "warning",
    unit: "",
    chartColor: "#1d4ed8",
    base: 22,
    amplitude: 7,
    drift: 0.18,
    pulse: 18,
    stats: [
      { labelId: "clusterMonitor.stat.p95Gap", kind: "p95" },
      { labelId: "clusterMonitor.stat.maxGap", kind: "peak" },
      { labelId: "clusterMonitor.stat.slowNodes", value: "1" },
    ],
  },
  {
    key: "controllerRaftStepQueueUsage",
    titleId: "clusterMonitor.metrics.controllerRaftStepQueueUsage",
    stage: "runtimePressure",
    tone: "warning",
    unit: "%",
    chartColor: "#ea580c",
    base: 14,
    amplitude: 8,
    pulse: 18,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
  {
    key: "controllerRaftStepEnqueueLatencyP99",
    titleId: "clusterMonitor.metrics.controllerRaftStepEnqueueLatencyP99",
    stage: "runtimePressure",
    tone: "warning",
    unit: "ms",
    chartColor: "#9333ea",
    base: 5.4,
    amplitude: 2.2,
    pulse: 6.2,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.p50", kind: "p50" },
      { labelId: "clusterMonitor.stat.p95", kind: "p95" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
    ],
  },
  {
    key: "controllerRaftStepEnqueueErrorRate",
    titleId: "clusterMonitor.metrics.controllerRaftStepEnqueueErrorRate",
    stage: "runtimePressure",
    tone: "critical",
    unit: "%",
    chartColor: "#dc2626",
    base: 0.05,
    amplitude: 0.03,
    pulse: 0.18,
    precision: 2,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.queueFull", value: "0" },
    ],
  },
  {
    key: "controllerStateRevision",
    titleId: "clusterMonitor.metrics.controllerStateRevision",
    stage: "controlPlane",
    tone: "normal",
    unit: "",
    chartColor: "#2563eb",
    base: 42,
    amplitude: 0,
    drift: 0.03,
    precision: 0,
    stats: [
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
    ],
  },
  {
    key: "controllerActiveTasks",
    titleId: "clusterMonitor.metrics.controllerActiveTasks",
    stage: "controlPlane",
    tone: "warning",
    unit: "",
    chartColor: "#ca8a04",
    base: 2,
    amplitude: 1,
    pulse: 2,
    precision: 0,
    stats: [
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
    ],
  },
  {
    key: "controllerFailedTasks",
    titleId: "clusterMonitor.metrics.controllerFailedTasks",
    stage: "incidentClosure",
    tone: "critical",
    unit: "",
    chartColor: "#be123c",
    base: 0,
    amplitude: 0.2,
    pulse: 1,
    precision: 0,
    stats: [
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.warning", value: "0" },
    ],
  },
  {
    key: "controllerNodesUnhealthy",
    titleId: "clusterMonitor.metrics.controllerNodesUnhealthy",
    stage: "incidentClosure",
    tone: "critical",
    unit: "",
    chartColor: "#dc2626",
    base: 0,
    amplitude: 0.3,
    pulse: 1,
    precision: 0,
    stats: [
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.critical", value: "0" },
    ],
  },
  {
    key: "controllerSlotLeaderSkew",
    titleId: "clusterMonitor.metrics.controllerSlotLeaderSkew",
    stage: "controlPlane",
    tone: "warning",
    unit: "",
    chartColor: "#d97706",
    base: 1,
    amplitude: 0.4,
    pulse: 2,
    precision: 0,
    stats: [
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
    ],
  },
  {
    key: "controllerLeaderPresent",
    titleId: "clusterMonitor.metrics.controllerLeaderPresent",
    stage: "controlPlane",
    tone: "normal",
    unit: "",
    chartColor: "#16a34a",
    base: 1,
    amplitude: 0,
    precision: 0,
    stats: [
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
      { labelId: "clusterMonitor.stat.min", kind: "min" },
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
    ],
  },
  {
    key: "slotLeaderStability",
    titleId: "clusterMonitor.metrics.slotLeaderStability",
    stage: "slotReplication",
    tone: "normal",
    unit: "%",
    chartColor: "#0f766e",
    base: 99.72,
    amplitude: 0.09,
    precision: 2,
    stats: [
      { labelId: "clusterMonitor.stat.leaderMissing", value: "0" },
      { labelId: "clusterMonitor.stat.quorumLost", value: "0" },
      { labelId: "clusterMonitor.stat.transfers", value: "2" },
    ],
  },
  {
    key: "slotProposeRate",
    titleId: "clusterMonitor.metrics.slotProposeRate",
    stage: "slotReplication",
    tone: "normal",
    unit: "cmd/s",
    chartColor: "#0891b2",
    base: 8.6,
    amplitude: 1.8,
    pulse: 3.4,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
  {
    key: "slotApplyGap",
    titleId: "clusterMonitor.metrics.slotApplyGap",
    stage: "slotReplication",
    tone: "warning",
    unit: "entries",
    chartColor: "#ca8a04",
    base: 6,
    amplitude: 2,
    drift: 0.05,
    pulse: 7,
    stats: [
      { labelId: "clusterMonitor.stat.p95Gap", kind: "p95" },
      { labelId: "clusterMonitor.stat.maxGap", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
  {
    key: "slotLatencyP99",
    titleId: "clusterMonitor.metrics.slotLatencyP99",
    stage: "slotReplication",
    tone: "warning",
    unit: "ms",
    chartColor: "#16a34a",
    base: 18,
    amplitude: 4.2,
    pulse: 10,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.p50", kind: "p50" },
      { labelId: "clusterMonitor.stat.p95", kind: "p95" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
    ],
  },
  {
    key: "channelAppendLatencyP99",
    titleId: "clusterMonitor.metrics.channelAppendLatencyP99",
    stage: "channelReplication",
    tone: "normal",
    unit: "ms",
    chartColor: "#22c55e",
    base: 34,
    amplitude: 5,
    pulse: 12,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.p50", kind: "p50" },
      { labelId: "clusterMonitor.stat.p95", kind: "p95" },
      { labelId: "clusterMonitor.stat.slowAppends", value: "7" },
    ],
  },
  {
    key: "activeChannels",
    titleId: "clusterMonitor.metrics.activeChannels",
    stage: "channelReplication",
    tone: "normal",
    unit: "",
    chartColor: "#0d9488",
    base: 124,
    amplitude: 18,
    drift: 0.08,
    pulse: 28,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
  {
    key: "internalTraffic",
    titleId: "clusterMonitor.metrics.internalTraffic",
    stage: "internalNetwork",
    tone: "preview",
    unit: "MB/s",
    chartColor: "#4f46e5",
    base: 84,
    amplitude: 11,
    pulse: 18,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.tx", value: "46.2 MB/s" },
      { labelId: "clusterMonitor.stat.rx", value: "41.8 MB/s" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
    ],
  },
  {
    key: "rpcSuccessRate",
    titleId: "clusterMonitor.metrics.rpcSuccessRate",
    stage: "internalNetwork",
    tone: "normal",
    unit: "%",
    chartColor: "#0284c7",
    base: 99.86,
    amplitude: 0.05,
    precision: 2,
    stats: [
      { labelId: "clusterMonitor.stat.callsPerSecond", value: "2.8k" },
      { labelId: "clusterMonitor.stat.errorsPerSecond", value: "3.8" },
      { labelId: "clusterMonitor.stat.timeouts", value: "9" },
    ],
  },
  {
    key: "rpcLatencyP95",
    titleId: "clusterMonitor.metrics.rpcLatencyP95",
    stage: "internalNetwork",
    tone: "warning",
    unit: "ms",
    chartColor: "#7c3aed",
    base: 26,
    amplitude: 4.8,
    pulse: 13,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.p50", kind: "p50" },
      { labelId: "clusterMonitor.stat.p95", kind: "p95" },
      { labelId: "clusterMonitor.stat.inflight", value: "184" },
    ],
  },
  {
    key: "workqueuePressure",
    titleId: "clusterMonitor.metrics.workqueuePressure",
    stage: "runtimePressure",
    tone: "warning",
    unit: "%",
    chartColor: "#d97706",
    base: 62,
    amplitude: 11,
    drift: 0.24,
    pulse: 16,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.busy", value: "5" },
      { labelId: "clusterMonitor.stat.critical", value: "1" },
      { labelId: "clusterMonitor.stat.queueFull", value: "12" },
    ],
  },
  {
    key: "nodeCpuPercent",
    titleId: "clusterMonitor.metrics.nodeCpuPercent",
    stage: "runtimePressure",
    tone: "warning",
    unit: "%",
    chartColor: "#dc2626",
    base: 42,
    amplitude: 9,
    pulse: 18,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
  {
    key: "nodeMemoryRSS",
    titleId: "clusterMonitor.metrics.nodeMemoryRSS",
    stage: "runtimePressure",
    tone: "warning",
    unit: "MB",
    chartColor: "#0f766e",
    base: 512,
    amplitude: 48,
    drift: 1.2,
    pulse: 72,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
  {
    key: "nodeGoroutines",
    titleId: "clusterMonitor.metrics.nodeGoroutines",
    stage: "runtimePressure",
    tone: "warning",
    unit: "",
    chartColor: "#475569",
    base: 820,
    amplitude: 120,
    drift: 2.5,
    pulse: 180,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
  {
    key: "storageWriteP99",
    titleId: "clusterMonitor.metrics.storageWriteP99",
    stage: "runtimePressure",
    tone: "warning",
    unit: "ms",
    chartColor: "#db2777",
    base: 31,
    amplitude: 6.5,
    pulse: 18,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.p50", kind: "p50" },
      { labelId: "clusterMonitor.stat.p95", kind: "p95" },
      { labelId: "clusterMonitor.stat.flushWait", value: "6.4 ms" },
    ],
  },
  {
    key: "storagePebbleDiskUsage",
    titleId: "clusterMonitor.metrics.storagePebbleDiskUsage",
    stage: "runtimePressure",
    tone: "warning",
    unit: "B",
    chartColor: "#be185d",
    base: 1_048_576,
    amplitude: 96_000,
    drift: 2200,
    pulse: 120,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
  {
    key: "storagePebbleReadAmplification",
    titleId: "clusterMonitor.metrics.storagePebbleReadAmplification",
    stage: "runtimePressure",
    tone: "warning",
    unit: "",
    chartColor: "#ca8a04",
    base: 4,
    amplitude: 0.8,
    drift: 0.02,
    pulse: 36,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
  {
    key: "storagePebbleCompactionDebt",
    titleId: "clusterMonitor.metrics.storagePebbleCompactionDebt",
    stage: "runtimePressure",
    tone: "warning",
    unit: "B",
    chartColor: "#7c2d12",
    base: 262_144,
    amplitude: 44_000,
    drift: 1400,
    pulse: 54,
    precision: 1,
    stats: [
      { labelId: "clusterMonitor.stat.avg", kind: "avg" },
      { labelId: "clusterMonitor.stat.peak", kind: "peak" },
      { labelId: "clusterMonitor.stat.latest", kind: "latest" },
    ],
  },
]

function round(value: number, precision = 0) {
  const factor = 10 ** precision
  return Math.round(value * factor) / factor
}

function formatValue(value: number, precision = 0) {
  return new Intl.NumberFormat("en-US", {
    maximumFractionDigits: precision,
    minimumFractionDigits: precision,
  }).format(round(value, precision))
}

function seriesFor(spec: CardSpec, range: ClusterMonitorTimeRange): ClusterMonitorPoint[] {
  const config = rangeConfig[range]
  return Array.from({ length: config.points }, (_, index) => {
    const wave = Math.sin(index / 3.2) * spec.amplitude * config.scale
    const secondWave = Math.cos(index / 7) * spec.amplitude * 0.38
    const drift = (spec.drift ?? 0) * index * config.scale
    const pulse = spec.pulse && index % 19 === 11 ? spec.pulse * config.scale : 0
    const raw = spec.base + wave + secondWave + drift + pulse
    return {
      timestamp: GENERATED_AT_MS - (config.points - index - 1) * config.stepMs,
      value: Math.max(0, round(raw, spec.precision ?? 0)),
    }
  })
}

function percentile(values: number[], ratio: number) {
  const sorted = [...values].sort((a, b) => a - b)
  const index = Math.min(sorted.length - 1, Math.max(0, Math.floor((sorted.length - 1) * ratio)))
  return sorted[index] ?? 0
}

function dynamicStatValue(kind: DynamicStatKind, series: ClusterMonitorPoint[], spec: CardSpec) {
  const values = series.map((point) => point.value)
  const precision = spec.precision ?? 0
  switch (kind) {
    case "avg":
      return formatValue(values.reduce((sum, value) => sum + value, 0) / values.length, precision)
    case "peak":
    case "peakP99":
      return formatValue(Math.max(...values), precision)
    case "latest":
      return formatValue(values[values.length - 1] ?? 0, precision)
    case "p50":
      return formatValue(percentile(values, 0.5), precision)
    case "p95":
      return formatValue(percentile(values, 0.95), precision)
    case "min":
      return formatValue(Math.min(...values), precision)
  }
}

function buildStats(spec: CardSpec, series: ClusterMonitorPoint[]) {
  return spec.stats.map((stat) => ({
    labelId: stat.labelId,
    value: stat.value ?? dynamicStatValue(stat.kind ?? "latest", series, spec),
  }))
}

function buildCard(spec: CardSpec, range: ClusterMonitorTimeRange): ClusterMonitorMetricCard {
  const series = seriesFor(spec, range)
  const latest = series[series.length - 1]?.value ?? spec.base
  return {
    key: spec.key,
    titleId: spec.titleId,
    helpId: `clusterMonitor.help.${spec.key}`,
    stage: spec.stage,
    stageLabelId: stageLabelIds[spec.stage],
    statusId: statusByTone[spec.tone],
    tone: spec.tone,
    unit: spec.unit,
    value: formatValue(latest, spec.precision ?? 0),
    series,
    stats: buildStats(spec, series),
    chartColor: spec.chartColor,
  }
}

export function buildPreviewClusterMonitorModel(
  timeRange: ClusterMonitorTimeRange,
  isPaused: boolean,
): PreviewClusterMonitorModel {
  const cards = cardSpecs.map((spec) => buildCard(spec, timeRange))
  const snapshot: ClusterMonitorSnapshotEntry[] = [
    { key: "nodesAlive", labelId: "clusterMonitor.snapshot.nodesAlive", value: "3/3", tone: "normal" },
    { key: "slotsReady", labelId: "clusterMonitor.snapshot.slotsReady", value: "128/128", tone: "normal" },
    { key: "controllerApplyGap", labelId: "clusterMonitor.snapshot.controllerApplyGap", value: "32", tone: "warning" },
    { key: "rpcErrorRate", labelId: "clusterMonitor.snapshot.rpcErrorRate", value: "0.14", unit: "%", tone: "normal" },
    { key: "queuePressure", labelId: "clusterMonitor.snapshot.queuePressure", value: "68", unit: "%", tone: "warning" },
    { key: "storageWriteP99", labelId: "clusterMonitor.snapshot.storageWriteP99", value: "38.4", unit: "ms", tone: "warning" },
  ]

  return {
    generatedAt: GENERATED_AT,
    scopeLabelId: "clusterMonitor.scope.global",
    timeRange,
    isPaused,
    snapshot,
    cards,
  }
}
