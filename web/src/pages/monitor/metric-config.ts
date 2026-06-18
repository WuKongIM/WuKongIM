import type { MonitorMetricKey, MonitorStage, MonitorTone } from "./types"

type MonitorMetricConfig = {
  titleId: string
  chartColor: string
  precision: number
}

export const monitorMetricConfig: Record<MonitorMetricKey, MonitorMetricConfig> = {
  sendRate: { titleId: "monitor.metrics.sendRate", chartColor: "#2563eb", precision: 1 },
  sendSuccessRate: { titleId: "monitor.metrics.sendSuccessRate", chartColor: "#16a34a", precision: 2 },
  entryLatencyP99: { titleId: "monitor.metrics.entryLatencyP99", chartColor: "#d97706", precision: 1 },
  commitRate: { titleId: "monitor.metrics.commitRate", chartColor: "#0f766e", precision: 1 },
  commitLatencyP99: { titleId: "monitor.metrics.commitLatencyP99", chartColor: "#7c3aed", precision: 1 },
  pendingCommitBacklog: { titleId: "monitor.metrics.pendingCommitBacklog", chartColor: "#ca8a04", precision: 0 },
  deliveryRate: { titleId: "monitor.metrics.deliveryRate", chartColor: "#0891b2", precision: 1 },
  deliveryLatencyP99: { titleId: "monitor.metrics.deliveryLatencyP99", chartColor: "#db2777", precision: 1 },
  fanOutRatio: { titleId: "monitor.metrics.fanOutRatio", chartColor: "#4f46e5", precision: 2 },
  offlineEnqueueRate: { titleId: "monitor.metrics.offlineEnqueueRate", chartColor: "#64748b", precision: 1 },
  retryQueueDepth: { titleId: "monitor.metrics.retryQueueDepth", chartColor: "#ea580c", precision: 0 },
  pathErrorRate: { titleId: "monitor.metrics.pathErrorRate", chartColor: "#dc2626", precision: 2 },
  activeConnections: { titleId: "monitor.metrics.onlineConnections", chartColor: "#0d9488", precision: 0 },
}

export const monitorStageLabelIds: Record<MonitorStage, string> = {
  sendEntry: "monitor.stage.sendEntry",
  appendCommit: "monitor.stage.appendCommit",
  onlineDelivery: "monitor.stage.onlineDelivery",
  offlineRetry: "monitor.stage.offlineRetry",
  errorClosure: "monitor.stage.errorClosure",
}

export const monitorStatusByTone: Record<MonitorTone, string> = {
  normal: "monitor.status.normal",
  warning: "monitor.status.warning",
  critical: "monitor.status.critical",
  preview: "monitor.status.preview",
}

export const monitorSnapshotLabelIds: Record<string, string> = {
  send: "monitor.snapshot.send",
  delivery: "monitor.snapshot.delivery",
  entryP99: "monitor.snapshot.entryP99",
  deliveryP99: "monitor.snapshot.deliveryP99",
  errors: "monitor.snapshot.errors",
  retryDepth: "monitor.snapshot.retryDepth",
  online: "monitor.snapshot.online",
}

export const monitorStatLabelIds: Record<string, string> = {
  avg: "monitor.stat.avg",
  peak: "monitor.stat.peak",
  total: "monitor.stat.total",
}

export const monitorUnavailableReasonLabelIds: Record<string, string> = {
  no_entry_latency_samples: "monitor.noData.entryLatencySamples",
  no_commit_latency_samples: "monitor.noData.commitLatencySamples",
  no_delivery_latency_samples: "monitor.noData.deliveryLatencySamples",
}
