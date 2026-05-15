import { useCallback, useEffect, useMemo, useState } from "react"

import { getMonitorMetrics } from "@/lib/manager-api"
import type { ManagerMonitorMetricsResponse, ManagerMonitorMetricSeries } from "@/lib/manager-api.types"

import type { MetricDataPoint, MetricKey, MonitorData, MonitorNodeOption, NodeId, TimeRange } from "./types"

const REFRESH_INTERVAL = 5000

const metricApiKeys: Record<MetricKey, string> = {
  sendRate: "send_rate",
  deliverRate: "deliver_rate",
  sendLatencyP99: "send_latency_p99",
  deliveryLatencyP99: "delivery_latency_p99",
  sendFailRate: "send_fail_rate",
  deliveryFailRate: "delivery_fail_rate",
  retryQueueDepth: "retry_queue_depth",
  onlineConnections: "online_connections",
  activeChannels: "active_channels",
  fanOutRate: "fan_out_rate",
}

function emptyMonitorData(): MonitorData {
  return {
    sendRate: [],
    deliverRate: [],
    sendLatencyP99: [],
    deliveryLatencyP99: [],
    sendFailRate: [],
    deliveryFailRate: [],
    retryQueueDepth: [],
    onlineConnections: [],
    activeChannels: [],
    fanOutRate: [],
  }
}

function toMetricPoints(series?: ManagerMonitorMetricSeries): MetricDataPoint[] {
  return (series?.points ?? []).map((point) => ({
    timestamp: Date.parse(point.at),
    value: point.value,
  }))
}

function monitorDataFromResponse(response: ManagerMonitorMetricsResponse): MonitorData {
  const data = emptyMonitorData()
  for (const key of Object.keys(metricApiKeys) as MetricKey[]) {
    data[key] = toMetricPoints(response.metrics[metricApiKeys[key]])
  }
  return data
}

function nodeOptionsFromResponse(response: ManagerMonitorMetricsResponse): MonitorNodeOption[] {
  return response.nodes.map((node) => ({
    id: String(node.node_id),
    label: `${node.name || `node-${node.node_id}`}${node.is_local ? " (local)" : ""}`,
    isLocal: node.is_local,
    available: node.available,
  }))
}

export function useMonitorData(timeRange: TimeRange, isPaused: boolean, selectedNode: NodeId) {
  const [response, setResponse] = useState<ManagerMonitorMetricsResponse | null>(null)
  const [data, setData] = useState<MonitorData>(() => emptyMonitorData())
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const load = useCallback(async (refreshingLoad: boolean) => {
    setLoading((current) => (refreshingLoad ? current : true))
    setRefreshing(refreshingLoad)
    setError(null)
    try {
      const next = await getMonitorMetrics({
        window: timeRange,
        step: "5s",
        ...(selectedNode === "all" ? {} : { nodeId: selectedNode }),
      })
      setResponse(next)
      setData(monitorDataFromResponse(next))
      setLoading(false)
      setRefreshing(false)
    } catch (caught) {
      setError(caught instanceof Error ? caught : new Error("monitor metrics request failed"))
      setLoading(false)
      setRefreshing(false)
    }
  }, [selectedNode, timeRange])

  useEffect(() => {
    void load(false)
  }, [load])

  useEffect(() => {
    if (isPaused) return undefined
    const timer = window.setInterval(() => {
      void load(true)
    }, REFRESH_INTERVAL)
    return () => window.clearInterval(timer)
  }, [isPaused, load])

  const nodes = useMemo<MonitorNodeOption[]>(() => {
    if (!response) return []
    return nodeOptionsFromResponse(response)
  }, [response])

  return {
    data,
    nodes,
    loading,
    refreshing,
    error,
    generatedAt: response?.generated_at ?? null,
    nodeFilterEnabled: response?.capabilities.node_filter ?? false,
    refresh: () => load(true),
  }
}
