import { useCallback, useEffect, useState } from "react"

import { getDashboardMetrics } from "@/lib/manager-api"

export type PulseSeries = {
  latest: number
  peak: number
  avg: number
  series: number[]
}

export type PulseData = {
  sendPerSec: PulseSeries
  deliverPerSec: PulseSeries
  connections: PulseSeries
  sendLatencyP99: PulseSeries
  deliveryLatencyP99: PulseSeries
  sendFailRate: PulseSeries
  deliveryFailRate: PulseSeries
  activeChannels: PulseSeries
  retryQueueDepth: PulseSeries
  fanOutRate: PulseSeries
}

export function useDashboardPulse(generatedAt: string | null): PulseData | null {
  const [data, setData] = useState<PulseData | null>(null)

  const load = useCallback(async () => {
    try {
      const resp = await getDashboardMetrics({ window: "30m", step: "30s" })
      setData({
        sendPerSec: resp.metrics.send_per_sec,
        deliverPerSec: resp.metrics.deliver_per_sec,
        connections: resp.metrics.connections,
        sendLatencyP99: resp.metrics.send_latency_p99_ms,
        deliveryLatencyP99: resp.metrics.delivery_latency_p99_ms,
        sendFailRate: resp.metrics.send_fail_rate_percent,
        deliveryFailRate: resp.metrics.delivery_fail_rate_percent,
        activeChannels: resp.metrics.active_channels,
        retryQueueDepth: resp.metrics.retry_queue_depth,
        fanOutRate: resp.metrics.fan_out_rate,
      })
    } catch {
      // API not available — pulse section won't render
    }
  }, [])

  useEffect(() => {
    if (generatedAt) void load()
  }, [generatedAt, load])

  return data
}
