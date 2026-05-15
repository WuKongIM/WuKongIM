import { useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { ManagerApiError } from "@/lib/manager-api"

import { ChartGrid } from "./components/chart-grid"
import { MetricChart } from "./components/metric-chart"
import { MonitorControls } from "./components/monitor-controls"
import type { MetricConfig, MonitorState, NodeId, TimeRange } from "./types"
import { useMonitorData } from "./use-monitor-data"

const MESSAGE_METRICS: MetricConfig[] = [
  { key: "sendRate", apiKey: "send_rate", labelKey: "monitor.metrics.sendRate", unit: "msg/s", color: "--chart-1", format: (v) => v.toFixed(0) },
  { key: "deliverRate", apiKey: "deliver_rate", labelKey: "monitor.metrics.deliverRate", unit: "msg/s", color: "--chart-2", format: (v) => v.toFixed(0) },
  { key: "sendLatencyP99", apiKey: "send_latency_p99", labelKey: "monitor.metrics.sendLatencyP99", unit: "ms", color: "--chart-3", format: (v) => v.toFixed(1) },
  { key: "deliveryLatencyP99", apiKey: "delivery_latency_p99", labelKey: "monitor.metrics.deliveryLatencyP99", unit: "ms", color: "--chart-4", format: (v) => v.toFixed(1) },
  { key: "sendFailRate", apiKey: "send_fail_rate", labelKey: "monitor.metrics.sendFailRate", unit: "%", color: "--chart-5", format: (v) => v.toFixed(2) },
  { key: "deliveryFailRate", apiKey: "delivery_fail_rate", labelKey: "monitor.metrics.deliveryFailRate", unit: "%", color: "--chart-5", format: (v) => v.toFixed(2) },
  { key: "retryQueueDepth", apiKey: "retry_queue_depth", labelKey: "monitor.metrics.retryQueueDepth", unit: "", color: "--chart-1", format: (v) => v.toFixed(0) },
  { key: "fanOutRate", apiKey: "fan_out_rate", labelKey: "monitor.metrics.fanOutRate", unit: "x", color: "--chart-2", format: (v) => v.toFixed(1) },
]

const CONNECTION_METRICS: MetricConfig[] = [
  { key: "onlineConnections", apiKey: "online_connections", labelKey: "monitor.metrics.onlineConnections", unit: "", color: "--chart-2", format: (v) => v.toFixed(0) },
  { key: "activeChannels", apiKey: "active_channels", labelKey: "monitor.metrics.activeChannels", unit: "", color: "--chart-3", format: (v) => v.toFixed(0) },
]

function formatErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) return "error" as const
  if (error.status === 403) return "forbidden" as const
  if (error.status === 503) return "unavailable" as const
  return "error" as const
}

function availableMetrics(configs: MetricConfig[], data: ReturnType<typeof useMonitorData>["data"]) {
  return configs.filter((config) => data[config.key]?.length > 0)
}

export function MonitorPage() {
  const intl = useIntl()
  const [state, setState] = useState<MonitorState>({
    selectedNode: "all",
    timeRange: "5m",
    isPaused: false,
  })

  const monitor = useMonitorData(state.timeRange, state.isPaused, state.selectedNode)
  const messageMetrics = availableMetrics(MESSAGE_METRICS, monitor.data)
  const connectionMetrics = availableMetrics(CONNECTION_METRICS, monitor.data)
  const hasMetrics = messageMetrics.length > 0 || connectionMetrics.length > 0
  const title = intl.formatMessage({ id: "monitor.title" })

  const handleNodeChange = (node: NodeId) => {
    setState((prev) => ({ ...prev, selectedNode: node }))
  }

  const handleTimeRangeChange = (range: TimeRange) => {
    setState((prev) => ({ ...prev, timeRange: range }))
  }

  const handlePauseToggle = () => {
    setState((prev) => ({ ...prev, isPaused: !prev.isPaused }))
  }

  return (
    <PageContainer>
      <PageHeader
        title={title}
        description={intl.formatMessage({ id: "monitor.description" })}
      />

      <div className="space-y-6">
        <MonitorControls
          nodes={monitor.nodes}
          selectedNode={state.selectedNode}
          onNodeChange={handleNodeChange}
          timeRange={state.timeRange}
          onTimeRangeChange={handleTimeRangeChange}
          isPaused={state.isPaused}
          onPauseToggle={handlePauseToggle}
          nodeFilterEnabled={monitor.nodeFilterEnabled}
        />

        {monitor.loading ? (
          <ResourceState kind="loading" title={title} />
        ) : monitor.error ? (
          <ResourceState
            kind={formatErrorKind(monitor.error)}
            onRetry={() => { void monitor.refresh() }}
            title={title}
          />
        ) : hasMetrics ? (
          <>
            {messageMetrics.length > 0 ? (
              <ChartGrid title={intl.formatMessage({ id: "monitor.section.messageFlow" })}>
                {messageMetrics.map((metric) => (
                  <MetricChart
                    key={metric.key}
                    label={intl.formatMessage({ id: metric.labelKey })}
                    data={monitor.data[metric.key]}
                    unit={metric.unit}
                    color={metric.color}
                    formatValue={metric.format}
                  />
                ))}
              </ChartGrid>
            ) : null}

            {connectionMetrics.length > 0 ? (
              <ChartGrid title={intl.formatMessage({ id: "monitor.section.connections" })}>
                {connectionMetrics.map((metric) => (
                  <MetricChart
                    key={metric.key}
                    label={intl.formatMessage({ id: metric.labelKey })}
                    data={monitor.data[metric.key]}
                    unit={metric.unit}
                    color={metric.color}
                    formatValue={metric.format}
                  />
                ))}
              </ChartGrid>
            ) : null}
          </>
        ) : (
          <ResourceState kind="empty" title={title} />
        )}
      </div>
    </PageContainer>
  )
}
