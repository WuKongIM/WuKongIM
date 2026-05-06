import { useIntl } from "react-intl"
import { Area, AreaChart, XAxis, YAxis } from "recharts"

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart"
import type { FilteredNetworkMetrics, NetworkTimeRange } from "../utils/aggregation"
import { networkChartConfig } from "../utils/charting"
import { formatBps, formatBytes, formatShortTime } from "../utils/formatters"
import { MetricCard } from "./metric-card"

export function TrafficCard({ metrics, onClick, timeRange }: { metrics: FilteredNetworkMetrics; onClick: () => void; timeRange: NetworkTimeRange }) {
  const intl = useIntl()
  const rows = metrics.history.traffic.length > 0
    ? metrics.history.traffic.map((point) => ({ label: formatShortTime(intl, point.at), tx: point.tx_bytes, rx: point.rx_bytes }))
    : [{ label: intl.formatMessage({ id: "network.chart.snapshot" }), tx: metrics.traffic.txBytes, rx: metrics.traffic.rxBytes }]
  const labels = [
    { label: intl.formatMessage({ id: "network.legend.tx" }), value: `${formatBytes(intl, metrics.traffic.txBytes)} (${formatBps(intl, metrics.traffic.txBps)})` },
    { label: intl.formatMessage({ id: "network.legend.rx" }), value: `${formatBytes(intl, metrics.traffic.rxBytes)} (${formatBps(intl, metrics.traffic.rxBps)})` },
    { label: "Total", value: formatBytes(intl, metrics.traffic.txBytes + metrics.traffic.rxBytes) },
  ]

  return (
    <MetricCard
      actionLabel={intl.formatMessage({ id: "network.card.viewDetails" })}
      chart={(
        <div>
          <ChartContainer className="h-28" config={networkChartConfig(intl)}>
            <AreaChart accessibilityLayer data={rows} margin={{ left: 0, right: 8 }}>
              <XAxis dataKey="label" tickLine={false} />
              <YAxis hide />
              <ChartTooltip content={<ChartTooltipContent />} />
              <Area dataKey="tx" fill="var(--color-tx)" fillOpacity={0.25} stroke="var(--color-tx)" type="monotone" />
              <Area dataKey="rx" fill="var(--color-rx)" fillOpacity={0.25} stroke="var(--color-rx)" type="monotone" />
            </AreaChart>
          </ChartContainer>
          {!metrics.traffic.isFilteredByNode && metrics.traffic.scopeNote ? <p className="mt-1 text-[11px] text-muted-foreground">{intl.formatMessage({ id: "network.filter.localTotalNote" })}</p> : null}
          {timeRange !== "1m" ? <p className="mt-1 text-[11px] text-muted-foreground">{intl.formatMessage({ id: "network.filter.windowFallback" })}</p> : null}
        </div>
      )}
      labels={labels}
      onClick={onClick}
      primaryMetric={formatBps(intl, metrics.traffic.txBps + metrics.traffic.rxBps)}
      title={intl.formatMessage({ id: "network.card.traffic" })}
    />
  )
}
