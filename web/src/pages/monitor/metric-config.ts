import type { MonitorMetricKey, MonitorStage, MonitorTone } from "./types"

type MonitorMetricConfig = {
  titleId: string
  helpId: string
  chartColor: string
  precision: number
}

export const monitorMetricConfig: Record<MonitorMetricKey, MonitorMetricConfig> = {
  sendRate: { titleId: "monitor.metrics.sendRate", helpId: "monitor.help.sendRate", chartColor: "#2563eb", precision: 1 },
  sendSuccessRate: { titleId: "monitor.metrics.sendSuccessRate", helpId: "monitor.help.sendSuccessRate", chartColor: "#16a34a", precision: 2 },
  entryLatencyP99: { titleId: "monitor.metrics.entryLatencyP99", helpId: "monitor.help.entryLatencyP99", chartColor: "#d97706", precision: 1 },
  commitRate: { titleId: "monitor.metrics.commitRate", helpId: "monitor.help.commitRate", chartColor: "#0f766e", precision: 1 },
  commitLatencyP99: { titleId: "monitor.metrics.commitLatencyP99", helpId: "monitor.help.commitLatencyP99", chartColor: "#7c3aed", precision: 1 },
  pendingCommitBacklog: { titleId: "monitor.metrics.pendingCommitBacklog", helpId: "monitor.help.pendingCommitBacklog", chartColor: "#ca8a04", precision: 0 },
  conversationSyncRate: { titleId: "monitor.metrics.conversationSyncRate", helpId: "monitor.help.conversationSyncRate", chartColor: "#0284c7", precision: 1 },
  conversationSyncLatencyP99: {
    titleId: "monitor.metrics.conversationSyncLatencyP99",
    helpId: "monitor.help.conversationSyncLatencyP99",
    chartColor: "#9333ea",
    precision: 1,
  },
  conversationSyncErrorRate: {
    titleId: "monitor.metrics.conversationSyncErrorRate",
    helpId: "monitor.help.conversationSyncErrorRate",
    chartColor: "#e11d48",
    precision: 2,
  },
  conversationReturnedItems: {
    titleId: "monitor.metrics.conversationReturnedItems",
    helpId: "monitor.help.conversationReturnedItems",
    chartColor: "#059669",
    precision: 1,
  },
  conversationRecentLoadLatencyP99: {
    titleId: "monitor.metrics.conversationRecentLoadLatencyP99",
    helpId: "monitor.help.conversationRecentLoadLatencyP99",
    chartColor: "#c026d3",
    precision: 1,
  },
  conversationActiveDirtyRows: {
    titleId: "monitor.metrics.conversationActiveDirtyRows",
    helpId: "monitor.help.conversationActiveDirtyRows",
    chartColor: "#f59e0b",
    precision: 0,
  },
  conversationActiveOldestDirtyAge: {
    titleId: "monitor.metrics.conversationActiveOldestDirtyAge",
    helpId: "monitor.help.conversationActiveOldestDirtyAge",
    chartColor: "#ea580c",
    precision: 1,
  },
  conversationActiveFlushLatencyP99: {
    titleId: "monitor.metrics.conversationActiveFlushLatencyP99",
    helpId: "monitor.help.conversationActiveFlushLatencyP99",
    chartColor: "#0d9488",
    precision: 1,
  },
  conversationActiveFlushErrorRate: {
    titleId: "monitor.metrics.conversationActiveFlushErrorRate",
    helpId: "monitor.help.conversationActiveFlushErrorRate",
    chartColor: "#dc2626",
    precision: 2,
  },
  conversationAuthorityPressureRate: {
    titleId: "monitor.metrics.conversationAuthorityPressureRate",
    helpId: "monitor.help.conversationAuthorityPressureRate",
    chartColor: "#4f46e5",
    precision: 2,
  },
  deliveryRate: { titleId: "monitor.metrics.deliveryRate", helpId: "monitor.help.deliveryRate", chartColor: "#0891b2", precision: 1 },
  deliveryLatencyP99: { titleId: "monitor.metrics.deliveryLatencyP99", helpId: "monitor.help.deliveryLatencyP99", chartColor: "#db2777", precision: 1 },
  fanOutRatio: { titleId: "monitor.metrics.fanOutRatio", helpId: "monitor.help.fanOutRatio", chartColor: "#4f46e5", precision: 2 },
  offlineEnqueueRate: { titleId: "monitor.metrics.offlineEnqueueRate", helpId: "monitor.help.offlineEnqueueRate", chartColor: "#64748b", precision: 1 },
  retryQueueDepth: { titleId: "monitor.metrics.retryQueueDepth", helpId: "monitor.help.retryQueueDepth", chartColor: "#ea580c", precision: 0 },
  pathErrorRate: { titleId: "monitor.metrics.pathErrorRate", helpId: "monitor.help.pathErrorRate", chartColor: "#dc2626", precision: 2 },
  activeConnections: { titleId: "monitor.metrics.onlineConnections", helpId: "monitor.help.onlineConnections", chartColor: "#0d9488", precision: 0 },
}

export const monitorStageLabelIds: Record<MonitorStage, string> = {
  sendEntry: "monitor.stage.sendEntry",
  appendCommit: "monitor.stage.appendCommit",
  conversationSync: "monitor.stage.conversationSync",
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
  conversationSyncP99: "monitor.snapshot.conversationSyncP99",
  conversationSyncErrors: "monitor.snapshot.conversationSyncErrors",
  conversationDirtyAge: "monitor.snapshot.conversationDirtyAge",
  conversationFlushErrors: "monitor.snapshot.conversationFlushErrors",
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
  no_conversation_sync_samples: "monitor.noData.no_conversation_sync_samples",
  no_conversation_sync_latency_samples: "monitor.noData.no_conversation_sync_latency_samples",
  no_conversation_recent_load_samples: "monitor.noData.no_conversation_recent_load_samples",
  no_conversation_active_flush_samples: "monitor.noData.no_conversation_active_flush_samples",
  prometheus_query_error: "monitor.noData.unavailable",
  prometheus_no_data: "monitor.noData.unavailable",
}
