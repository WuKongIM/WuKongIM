export type TimeRange = "5m" | "15m" | "30m" | "1h"

export type MonitorTone = "normal" | "warning" | "critical" | "preview"

export type MonitorStage = "sendEntry" | "appendCommit" | "onlineDelivery" | "offlineRetry" | "errorClosure"

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

export type NodeId = "all" | string

export type MetricDataPoint = MonitorPoint

export type MetricKey =
  | "sendRate"
  | "deliverRate"
  | "sendLatencyP99"
  | "deliveryLatencyP99"
  | "sendFailRate"
  | "deliveryFailRate"
  | "retryQueueDepth"
  | "onlineConnections"
  | "activeChannels"
  | "fanOutRate"

export type MonitorData = Record<MetricKey, MetricDataPoint[]>

export type MonitorNodeOption = {
  id: NodeId
  label: string
  isLocal: boolean
  available: boolean
}
