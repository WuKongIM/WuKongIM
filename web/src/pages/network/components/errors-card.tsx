import { useIntl } from "react-intl"
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart"
import type { FilteredNetworkMetrics } from "../utils/aggregation"
import { networkChartConfig } from "../utils/charting"
import { compactNumber } from "../utils/formatters"
import { MetricCard } from "./metric-card"

export function ErrorsCard({ metrics, onClick }: { metrics: FilteredNetworkMetrics; onClick: () => void }) {
  const intl = useIntl()
  const rows = [{
    label: intl.formatMessage({ id: "network.chart.snapshot" }),
    dial: metrics.errors.dial,
    queue: metrics.errors.queueFull,
    timeout: metrics.errors.timeouts,
  }]

  return (
    <MetricCard
      actionLabel={intl.formatMessage({ id: "network.card.viewDetails" })}
      alertLevel={metrics.errors.alertLevel}
      chart={(
        <ChartContainer className="h-32" config={networkChartConfig(intl)}>
          <BarChart accessibilityLayer data={rows} layout="vertical" margin={{ left: 4, right: 10 }}>
            <CartesianGrid horizontal={false} />
            <XAxis hide type="number" />
            <YAxis dataKey="label" hide type="category" />
            <ChartTooltip content={<ChartTooltipContent />} />
            <Bar dataKey="dial" fill="var(--color-dial)" stackId="errors" />
            <Bar dataKey="queue" fill="var(--color-queue)" stackId="errors" />
            <Bar dataKey="timeout" fill="var(--color-timeout)" radius={[0, 4, 4, 0]} stackId="errors" />
          </BarChart>
        </ChartContainer>
      )}
      labels={[
        { label: intl.formatMessage({ id: "network.legend.dial" }), value: compactNumber(intl, metrics.errors.dial) },
        { label: intl.formatMessage({ id: "network.legend.queue" }), value: compactNumber(intl, metrics.errors.queueFull) },
        { label: intl.formatMessage({ id: "network.legend.timeout" }), value: compactNumber(intl, metrics.errors.timeouts) },
      ]}
      onClick={onClick}
      primaryMetric={compactNumber(intl, metrics.errors.total)}
      title={intl.formatMessage({ id: "network.card.errors" })}
    />
  )
}
