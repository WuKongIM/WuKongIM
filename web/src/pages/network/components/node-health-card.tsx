import { useIntl } from "react-intl"
import { Cell, LabelList, Pie, PieChart } from "recharts"

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart"
import type { FilteredNetworkMetrics } from "../utils/aggregation"
import { networkChartColors, networkChartConfig } from "../utils/charting"
import { MetricCard } from "./metric-card"

export function NodeHealthCard({ metrics, onClick }: { metrics: FilteredNetworkMetrics; onClick: () => void }) {
  const intl = useIntl()
  const rows = [
    { key: "alive", name: intl.formatMessage({ id: "network.legend.alive" }), value: metrics.health.alive, fill: networkChartColors.alive },
    { key: "suspect", name: intl.formatMessage({ id: "network.legend.suspect" }), value: metrics.health.suspect, fill: networkChartColors.suspect },
    { key: "dead", name: intl.formatMessage({ id: "network.legend.dead" }), value: metrics.health.dead, fill: networkChartColors.dead },
    { key: "draining", name: intl.formatMessage({ id: "network.legend.draining" }), value: metrics.health.draining, fill: networkChartColors.draining },
  ]
  const alertLevel = metrics.health.dead > 0 ? "danger" : metrics.health.suspect > 0 ? "warning" : "none"

  return (
    <MetricCard
      actionLabel={intl.formatMessage({ id: "network.card.viewDetails" })}
      alertLevel={alertLevel}
      chart={(
        <ChartContainer className="h-32" config={networkChartConfig(intl)}>
          <PieChart accessibilityLayer>
            <ChartTooltip content={<ChartTooltipContent hideLabel />} />
            <Pie data={rows} dataKey="value" innerRadius={30} nameKey="name" outerRadius={52}>
              {rows.map((entry) => <Cell fill={entry.fill} key={entry.key} />)}
              <LabelList dataKey="value" position="outside" />
            </Pie>
          </PieChart>
        </ChartContainer>
      )}
      labels={[
        { label: intl.formatMessage({ id: "network.legend.alive" }), value: metrics.health.alive },
        { label: intl.formatMessage({ id: "network.legend.suspect" }), value: metrics.health.suspect },
        { label: intl.formatMessage({ id: "network.legend.dead" }), value: metrics.health.dead },
        { label: intl.formatMessage({ id: "network.legend.draining" }), value: metrics.health.draining },
      ]}
      onClick={onClick}
      primaryMetric={`${metrics.health.alive} / ${metrics.health.total}`}
      title={intl.formatMessage({ id: "network.card.nodeHealth" })}
    />
  )
}
