export type TimeRange = "5m" | "15m" | "30m" | "1h"

export type NodeId = "all" | string

export type MetricDataPoint = {
  timestamp: number
  value: number
}

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

export type MetricConfig = {
  key: MetricKey
  apiKey: string
  labelKey: string
  unit: string
  color: string
  format?: (value: number) => string
}

export type MonitorState = {
  selectedNode: NodeId
  timeRange: TimeRange
  isPaused: boolean
}

export type MonitorNodeOption = {
  id: NodeId
  label: string
  isLocal: boolean
  available: boolean
}
