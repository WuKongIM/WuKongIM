import { useIntl } from "react-intl"
import { Area, AreaChart, XAxis, YAxis } from "recharts"

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart"
import type { FilteredNetworkMetrics } from "../utils/aggregation"
import { networkChartConfig } from "../utils/charting"
import { compactNumber, formatPercent, formatShortTime } from "../utils/formatters"
import { MetricCard } from "./metric-card"

export function RpcSuccessCard({ metrics, onClick }: { metrics: FilteredNetworkMetrics; onClick: () => void }) {
  const intl = useIntl()
  const rows = metrics.history.rpc.length > 0
    ? metrics.history.rpc.map((point) => ({ label: formatShortTime(intl, point.at), success: point.success, failures: point.errors, expected: point.expected_timeouts }))
    : [{ label: intl.formatMessage({ id: "network.chart.snapshot" }), success: metrics.rpc.success, failures: metrics.rpc.failures, expected: metrics.rpc.expectedTimeouts }]

  return (
    <MetricCard
      actionLabel={intl.formatMessage({ id: "network.card.viewDetails" })}
      alertLevel={metrics.rpc.alertLevel}
      chart={(
        <ChartContainer className="h-32" config={networkChartConfig(intl)}>
          <AreaChart accessibilityLayer data={rows} margin={{ left: 0, right: 8 }}>
            <XAxis dataKey="label" tickLine={false} />
            <YAxis hide />
            <ChartTooltip content={<ChartTooltipContent />} />
            <Area dataKey="success" fill="var(--color-success)" fillOpacity={0.25} stackId="rpc" stroke="var(--color-success)" type="monotone" />
            <Area dataKey="failures" fill="var(--color-failures)" fillOpacity={0.25} stackId="rpc" stroke="var(--color-failures)" type="monotone" />
            <Area dataKey="expected" fill="var(--color-expected)" fillOpacity={0.25} stackId="rpc" stroke="var(--color-expected)" type="monotone" />
          </AreaChart>
        </ChartContainer>
      )}
      labels={[
        { label: intl.formatMessage({ id: "network.card.totalCalls" }), value: compactNumber(intl, metrics.rpc.calls) },
        { label: intl.formatMessage({ id: "network.legend.success" }), value: compactNumber(intl, metrics.rpc.success) },
        { label: intl.formatMessage({ id: "network.card.failures" }), value: compactNumber(intl, metrics.rpc.failures) },
      ]}
      onClick={onClick}
      primaryMetric={formatPercent(intl, metrics.rpc.successRate)}
      title={intl.formatMessage({ id: "network.card.rpcSuccess" })}
    />
  )
}
