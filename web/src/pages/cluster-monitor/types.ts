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
  | "slotProposeRate"
  | "slotApplyGap"
  | "slotLatencyP99"
  | "channelAppendLatencyP99"
  | "activeChannels"
  | "internalTraffic"
  | "rpcSuccessRate"
  | "rpcLatencyP95"
  | "workqueuePressure"
  | "nodeCpuPercent"
  | "nodeMemoryRSS"
  | "nodeGoroutines"
  | "storageWriteP99"

export type ClusterMonitorPoint = {
  timestamp: number
  value: number
  label?: string
  seriesKey?: string
}

export type ClusterMonitorStat = {
  labelId?: string
  label?: string
  value: string
}

export type ClusterMonitorMetricCard = {
  key: ClusterMonitorMetricKey
  titleId: string
  helpId: string
  stage: ClusterMonitorStage
  stageLabelId: string
  statusId: string
  tone: ClusterMonitorTone
  unit: string
  value: string
  available?: boolean
  error?: string
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
