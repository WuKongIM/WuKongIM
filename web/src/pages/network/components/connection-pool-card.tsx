import { useIntl } from "react-intl"
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart"
import type { FilteredNetworkMetrics } from "../utils/aggregation"
import { networkChartConfig } from "../utils/charting"
import { MetricCard } from "./metric-card"

export function ConnectionPoolCard({ metrics, onClick }: { metrics: FilteredNetworkMetrics; onClick: () => void }) {
  const intl = useIntl()
  const rows = [
    { label: intl.formatMessage({ id: "network.peer.clusterPool" }), active: metrics.pools.cluster.active, idle: metrics.pools.cluster.idle },
    { label: intl.formatMessage({ id: "network.peer.dataPlanePool" }), active: metrics.pools.dataPlane.active, idle: metrics.pools.dataPlane.idle },
  ]

  return (
    <MetricCard
      actionLabel={intl.formatMessage({ id: "network.card.viewDetails" })}
      alertLevel={metrics.pools.alertLevel}
      chart={(
        <ChartContainer className="h-32" config={networkChartConfig(intl)}>
          <BarChart accessibilityLayer data={rows} layout="vertical" margin={{ left: 4, right: 10 }}>
            <CartesianGrid horizontal={false} />
            <XAxis hide type="number" />
            <YAxis dataKey="label" tickLine={false} type="category" width={78} />
            <ChartTooltip content={<ChartTooltipContent />} />
            <Bar dataKey="active" fill="var(--color-active)" stackId="pool" radius={[4, 0, 0, 4]} />
            <Bar dataKey="idle" fill="var(--color-idle)" stackId="pool" radius={[0, 4, 4, 0]} />
          </BarChart>
        </ChartContainer>
      )}
      labels={[
        { label: "Cluster", value: `${metrics.pools.cluster.active}/${metrics.pools.cluster.idle}` },
        { label: "Data Plane", value: `${metrics.pools.dataPlane.active}/${metrics.pools.dataPlane.idle}` },
        { label: "Total", value: metrics.pools.active + metrics.pools.idle },
      ]}
      onClick={onClick}
      primaryMetric={`${metrics.pools.cluster.active} / ${metrics.pools.cluster.idle}`}
      title={intl.formatMessage({ id: "network.card.connectionPool" })}
    />
  )
}
