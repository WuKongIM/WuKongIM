import { useIntl } from "react-intl"

import { StatusBadge } from "@/components/manager/status-badge"
import type { FilteredNetworkMetrics } from "../utils/aggregation"
import { formatTimestamp } from "../utils/formatters"
import { MetricCard } from "./metric-card"

export function EventsCard({ metrics, onClick }: { metrics: FilteredNetworkMetrics; onClick: () => void }) {
  const intl = useIntl()
  const events = [...metrics.events].sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime()).slice(0, 3)

  return (
    <MetricCard
      actionLabel={intl.formatMessage({ id: "network.card.viewAllEvents" })}
      alertLevel={events.some((event) => event.severity === "error" || event.severity === "danger") ? "danger" : events.length > 0 ? "warning" : "none"}
      chart={(
        <div className="grid gap-2">
          {events.length === 0 ? <p className="rounded-lg border border-border bg-background p-3 text-sm text-muted-foreground">{intl.formatMessage({ id: "network.card.noEvents" })}</p> : null}
          {events.map((event) => (
            <div className="rounded-lg border border-border bg-background p-2 text-xs" key={`${event.at}-${event.kind}-${event.target_node}`}>
              <div className="flex items-center justify-between gap-2">
                <StatusBadge value={event.severity} />
                <span className="text-muted-foreground">node {event.target_node}</span>
              </div>
              <div className="mt-1 font-medium text-foreground">{event.kind}</div>
              <div className="mt-1 text-muted-foreground">{formatTimestamp(intl, event.at)}</div>
            </div>
          ))}
        </div>
      )}
      labels={events.length > 0 ? [{ label: intl.formatMessage({ id: "network.card.viewAllEvents" }), value: events.length }] : []}
      onClick={onClick}
      primaryMetric={intl.formatMessage({ id: "network.card.eventCount" }, { count: metrics.events.length })}
      title={intl.formatMessage({ id: "network.card.events" })}
    />
  )
}
