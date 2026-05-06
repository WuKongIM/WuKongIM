import { useIntl } from "react-intl"
import { Line, LineChart, XAxis, YAxis } from "recharts"

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart"
import type { FilteredNetworkMetrics } from "../utils/aggregation"
import { networkChartConfig } from "../utils/charting"
import { formatLatency, formatShortTime } from "../utils/formatters"
import { MetricCard } from "./metric-card"

export function RpcLatencyCard({ metrics, onClick }: { metrics: FilteredNetworkMetrics; onClick: () => void }) {
  const intl = useIntl()
  const rows = metrics.history.rpc.length > 0
    ? metrics.history.rpc.map((point) => ({ label: formatShortTime(intl, point.at), p95: metrics.latency.p95Ms }))
    : [{ label: intl.formatMessage({ id: "network.chart.snapshot" }), p95: metrics.latency.p95Ms }]

  return (
    <MetricCard
      actionLabel={intl.formatMessage({ id: "network.card.viewDetails" })}
      alertLevel={metrics.latency.alertLevel}
      chart={(
        <ChartContainer className="h-32" config={networkChartConfig(intl)}>
          <LineChart accessibilityLayer data={rows} margin={{ left: 0, right: 8 }}>
            <XAxis dataKey="label" tickLine={false} />
            <YAxis hide />
            <ChartTooltip content={<ChartTooltipContent />} />
            <Line dataKey="p95" dot={false} stroke="var(--color-p95)" strokeWidth={2} type="monotone" />
          </LineChart>
        </ChartContainer>
      )}
      labels={[
        { label: "P50", value: formatLatency(intl, metrics.latency.p50Ms) },
        { label: "P95", value: formatLatency(intl, metrics.latency.p95Ms) },
        { label: "P99", value: formatLatency(intl, metrics.latency.p99Ms) },
      ]}
      onClick={onClick}
      primaryMetric={formatLatency(intl, metrics.latency.p95Ms)}
      title={intl.formatMessage({ id: "network.card.rpcLatency" })}
    />
  )
}
