export type TimeRange = "5m" | "15m" | "30m" | "1h"

export type MonitorTone = "normal" | "warning" | "critical" | "preview"

export type MonitorStage =
  | "sendEntry"
  | "appendCommit"
  | "conversationSync"
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
  | "conversationSyncRate"
  | "conversationSyncLatencyP99"
  | "conversationSyncErrorRate"
  | "conversationReturnedItems"
  | "conversationRecentLoadLatencyP99"
  | "conversationActiveDirtyRows"
  | "conversationActiveOldestDirtyAge"
  | "conversationActiveFlushLatencyP99"
  | "conversationActiveFlushErrorRate"
  | "conversationAuthorityPressureRate"
  | "deliveryRate"
  | "deliveryLatencyP99"
  | "fanOutRatio"
  | "offlineEnqueueRate"
  | "retryQueueDepth"
  | "pathErrorRate"
  | "activeConnections"

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
  available: boolean
  unavailableReasonLabelId?: string
  error?: string
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
