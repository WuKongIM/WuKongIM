import { useIntl } from "react-intl"
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"

import { SectionCard } from "@/components/shell/section-card"
import type { ClusterLinkMetrics } from "../view-model"

type ClusterLinkTrendsProps = {
  linkMetrics: ClusterLinkMetrics
  networkError: Error | null
}

export function ClusterLinkTrends({ linkMetrics, networkError }: ClusterLinkTrendsProps) {
  const intl = useIntl()
  const data = linkMetrics.rpcSeries.length > 0 ? linkMetrics.rpcSeries : [{ at: "sample", calls: 0, errors: 0, expectedTimeouts: 0 }]

  return (
    <SectionCard description={intl.formatMessage({ id: "clusterDashboard.links.description" })} title={intl.formatMessage({ id: "clusterDashboard.links.title" })}>
      {networkError ? (
        <p className="mb-3 rounded-2xl border border-warning/25 bg-warning/10 px-3 py-2 text-sm text-warning">
          {intl.formatMessage({ id: "clusterDashboard.links.unavailable" })}
        </p>
      ) : null}
      <div className="h-56">
        <ResponsiveContainer height="100%" width="100%">
          <LineChart data={data} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
            <XAxis dataKey="at" hide />
            <YAxis width={36} />
            <Tooltip />
            <Line dataKey="calls" dot={false} isAnimationActive={false} stroke="var(--chart-1)" strokeWidth={2} type="monotone" />
            <Line dataKey="errors" dot={false} isAnimationActive={false} stroke="var(--chart-4)" strokeWidth={2} type="monotone" />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </SectionCard>
  )
}
