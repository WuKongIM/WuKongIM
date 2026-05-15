import { useIntl } from "react-intl"
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"

import { SectionCard } from "@/components/shell/section-card"
import type { BusinessTrendSeries } from "../view-model"

type BusinessMessageTrendsProps = {
  trends: BusinessTrendSeries
}

export function BusinessMessageTrends({ trends }: BusinessMessageTrendsProps) {
  const intl = useIntl()
  return (
    <SectionCard description={intl.formatMessage({ id: "businessDashboard.trends.description" })} title={intl.formatMessage({ id: "businessDashboard.trends.title" })}>
      <div className="grid gap-3 xl:grid-cols-3">
        <TrendChart data={trends.throughput} firstKey="send" secondKey="deliver" />
        <TrendChart data={trends.latency} firstKey="send" secondKey="delivery" />
        <TrendChart data={trends.failures} firstKey="send" secondKey="delivery" />
      </div>
    </SectionCard>
  )
}

function TrendChart({ data, firstKey, secondKey }: { data: object[]; firstKey: string; secondKey: string }) {
  return (
    <div className="h-44 rounded-2xl border border-border/70 bg-muted/20 p-2">
      <ResponsiveContainer height="100%" width="100%">
        <LineChart data={data} margin={{ top: 8, right: 10, bottom: 8, left: 0 }}>
          <XAxis dataKey="index" hide />
          <YAxis width={36} />
          <Tooltip />
          <Line dataKey={firstKey} dot={false} isAnimationActive={false} stroke="var(--chart-1)" strokeWidth={2} type="monotone" />
          <Line dataKey={secondKey} dot={false} isAnimationActive={false} stroke="var(--chart-2)" strokeWidth={2} type="monotone" />
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}
