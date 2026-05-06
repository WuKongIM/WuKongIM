import { useMemo } from "react"
import { useIntl } from "react-intl"
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart"
import type { FilteredNetworkMetrics } from "../utils/aggregation"
import { networkChartConfig } from "../utils/charting"
import { compactNumber, formatBytes } from "../utils/formatters"
import { MetricCard } from "./metric-card"

export function MessageTypesCard({ metrics, onClick }: { metrics: FilteredNetworkMetrics; onClick: () => void }) {
  const intl = useIntl()
  const rows = useMemo(() => {
    const grouped = new Map<string, { label: string; tx: number; rx: number; total: number }>()
    for (const item of metrics.traffic.messageTypes) {
      const row = grouped.get(item.message_type) ?? { label: item.message_type, tx: 0, rx: 0, total: 0 }
      if (item.direction === "tx") row.tx += item.bytes_1m
      if (item.direction === "rx") row.rx += item.bytes_1m
      row.total += item.bytes_1m
      grouped.set(item.message_type, row)
    }
    return Array.from(grouped.values()).sort((a, b) => b.total - a.total).slice(0, 5)
  }, [metrics.traffic.messageTypes])
  const top = rows[0]?.label ?? intl.formatMessage({ id: "network.card.noMessageTypes" })
  const totalBytes = rows.reduce((total, row) => total + row.total, 0)

  return (
    <MetricCard
      actionLabel={intl.formatMessage({ id: "network.card.viewDetails" })}
      chart={(
        <ChartContainer className="h-32" config={networkChartConfig(intl)}>
          <BarChart accessibilityLayer data={rows} layout="vertical" margin={{ left: 4, right: 10 }}>
            <CartesianGrid horizontal={false} />
            <XAxis hide type="number" />
            <YAxis dataKey="label" tickLine={false} type="category" width={92} />
            <ChartTooltip content={<ChartTooltipContent />} />
            <Bar dataKey="tx" fill="var(--color-tx)" stackId="bytes" />
            <Bar dataKey="rx" fill="var(--color-rx)" radius={[0, 4, 4, 0]} stackId="bytes" />
          </BarChart>
        </ChartContainer>
      )}
      labels={[
        { label: intl.formatMessage({ id: "network.card.totalTypes" }), value: compactNumber(intl, rows.length) },
        { label: intl.formatMessage({ id: "network.card.totalBytes" }), value: formatBytes(intl, totalBytes) },
      ]}
      onClick={onClick}
      primaryMetric={top}
      title={intl.formatMessage({ id: "network.card.messageTypes" })}
    />
  )
}
