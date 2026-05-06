import { useIntl } from "react-intl"

import { StatusBadge } from "@/components/manager/status-badge"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import type { FilteredNetworkMetrics } from "../utils/aggregation"
import { compactNumber, formatBytes, formatLatency, formatPercent, formatTimestamp } from "../utils/formatters"

export type NetworkDetailKind = "health" | "pools" | "latency" | "success" | "traffic" | "errors" | "messageTypes" | "events"

type DetailDrawerProps = {
  kind: NetworkDetailKind | null
  open: boolean
  onOpenChange: (open: boolean) => void
  metrics: FilteredNetworkMetrics | null
  title: string
}

export function DetailDrawer({ kind, open, onOpenChange, metrics, title }: DetailDrawerProps) {
  const intl = useIntl()

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-2xl" side="right">
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
          <SheetDescription>{intl.formatMessage({ id: "network.detail.description" })}</SheetDescription>
        </SheetHeader>
        {!metrics || !kind ? null : (
          <div className="grid gap-4 p-4">
            {kind === "latency" || kind === "success" ? <ServiceBreakdown metrics={metrics} /> : null}
            {kind === "health" ? <HealthBreakdown metrics={metrics} /> : null}
            {kind === "pools" ? <PoolBreakdown metrics={metrics} /> : null}
            {kind === "traffic" || kind === "messageTypes" ? <TrafficBreakdown metrics={metrics} /> : null}
            {kind === "errors" ? <ErrorBreakdown metrics={metrics} /> : null}
            {kind === "events" ? <EventBreakdown metrics={metrics} /> : null}
            <details className="rounded-lg border border-border bg-background p-3">
              <summary className="cursor-pointer text-sm font-medium text-foreground">{intl.formatMessage({ id: "network.detail.rawJson" })}</summary>
              <pre className="mt-3 max-h-72 overflow-auto rounded bg-muted p-3 text-xs text-muted-foreground">{JSON.stringify(metrics, null, 2)}</pre>
            </details>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}

function ServiceBreakdown({ metrics }: { metrics: FilteredNetworkMetrics }) {
  const intl = useIntl()

  return (
    <section className="rounded-lg border border-border bg-background p-3">
      <h3 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "network.detail.serviceBreakdown" })}</h3>
      <div className="mt-3 overflow-x-auto">
        <table className="min-w-full text-left text-sm">
          <thead className="text-xs uppercase tracking-[0.14em] text-muted-foreground">
            <tr>
              <th className="px-2 py-2">{intl.formatMessage({ id: "network.table.service" })}</th>
              <th className="px-2 py-2">{intl.formatMessage({ id: "network.table.target" })}</th>
              <th className="px-2 py-2">{intl.formatMessage({ id: "network.table.calls1m" })}</th>
              <th className="px-2 py-2">{intl.formatMessage({ id: "network.table.p95" })}</th>
              <th className="px-2 py-2">{intl.formatMessage({ id: "network.table.success" })}</th>
            </tr>
          </thead>
          <tbody>
            {metrics.services.map((service) => {
              const completed = Math.max(service.calls_1m - service.expected_timeout_1m, 0)
              const successRate = completed > 0 ? Math.min(service.success_1m, completed) / completed : null
              return (
                <tr className="border-t border-border" key={`${service.service_id}-${service.target_node}`}>
                  <td className="px-2 py-2 font-medium text-foreground">{service.service}</td>
                  <td className="px-2 py-2 text-muted-foreground">{service.target_node}</td>
                  <td className="px-2 py-2 text-muted-foreground">{compactNumber(intl, service.calls_1m)}</td>
                  <td className="px-2 py-2 text-muted-foreground">{formatLatency(intl, service.p95_ms)}</td>
                  <td className="px-2 py-2 text-muted-foreground">{formatPercent(intl, successRate)}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function HealthBreakdown({ metrics }: { metrics: FilteredNetworkMetrics }) {
  const intl = useIntl()
  const rows: Array<[string, string | number]> = [
    [intl.formatMessage({ id: "network.legend.alive" }), metrics.health.alive],
    [intl.formatMessage({ id: "network.legend.suspect" }), metrics.health.suspect],
    [intl.formatMessage({ id: "network.legend.dead" }), metrics.health.dead],
    [intl.formatMessage({ id: "network.legend.draining" }), metrics.health.draining],
  ]
  return <KeyRows title={intl.formatMessage({ id: "network.card.nodeHealth" })} rows={rows} />
}

function PoolBreakdown({ metrics }: { metrics: FilteredNetworkMetrics }) {
  const intl = useIntl()
  return <KeyRows title={intl.formatMessage({ id: "network.card.connectionPool" })} rows={[
    [intl.formatMessage({ id: "network.peer.clusterPool" }), `${metrics.pools.cluster.active}/${metrics.pools.cluster.idle}`],
    [intl.formatMessage({ id: "network.peer.dataPlanePool" }), `${metrics.pools.dataPlane.active}/${metrics.pools.dataPlane.idle}`],
    ["Utilization", formatPercent(intl, metrics.pools.utilization)],
  ]} />
}

function TrafficBreakdown({ metrics }: { metrics: FilteredNetworkMetrics }) {
  const intl = useIntl()
  const rows: Array<[string, string | number]> = metrics.traffic.messageTypes.map((item) => [item.message_type, `${item.direction.toUpperCase()} ${formatBytes(intl, item.bytes_1m)}`])
  return <KeyRows title={intl.formatMessage({ id: "network.card.messageTypes" })} rows={rows.length ? rows : [[intl.formatMessage({ id: "network.card.noMessageTypes" }), "-"]]} />
}

function ErrorBreakdown({ metrics }: { metrics: FilteredNetworkMetrics }) {
  const intl = useIntl()
  return <KeyRows title={intl.formatMessage({ id: "network.card.errors" })} rows={[
    [intl.formatMessage({ id: "network.legend.dial" }), metrics.errors.dial],
    [intl.formatMessage({ id: "network.legend.queue" }), metrics.errors.queueFull],
    [intl.formatMessage({ id: "network.legend.timeout" }), metrics.errors.timeouts],
    [intl.formatMessage({ id: "network.legend.remote" }), metrics.errors.remote],
  ]} />
}

function EventBreakdown({ metrics }: { metrics: FilteredNetworkMetrics }) {
  const intl = useIntl()
  return (
    <section className="rounded-lg border border-border bg-background p-3">
      <h3 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "network.card.events" })}</h3>
      <div className="mt-3 grid gap-2">
        {metrics.events.map((event) => (
          <div className="rounded-lg border border-border p-3 text-sm" key={`${event.at}-${event.kind}-${event.target_node}`}>
            <div className="flex flex-wrap items-center gap-2"><StatusBadge value={event.severity} /><span>{event.kind}</span><span className="text-muted-foreground">node {event.target_node}</span></div>
            <p className="mt-2 text-muted-foreground">{event.message}</p>
            <p className="mt-1 text-xs text-muted-foreground">{formatTimestamp(intl, event.at)}</p>
          </div>
        ))}
      </div>
    </section>
  )
}

function KeyRows({ title, rows }: { title: string; rows: Array<[string, string | number]> }) {
  return (
    <section className="rounded-lg border border-border bg-background p-3">
      <h3 className="text-sm font-semibold text-foreground">{title}</h3>
      <div className="mt-3 grid gap-2">
        {rows.map(([label, value]) => (
          <div className="flex items-center justify-between gap-3 rounded-lg bg-muted/40 px-3 py-2 text-sm" key={label}>
            <span className="text-muted-foreground">{label}</span>
            <span className="font-medium text-foreground">{value}</span>
          </div>
        ))}
      </div>
    </section>
  )
}
