import { Activity, ArrowUpRight, MessageSquareText } from "lucide-react"
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { DashboardMetricsResponse } from "@/lib/manager-api"
import type { BusinessVerdict } from "../view-model"

type BusinessHealthHeroProps = {
  verdict: BusinessVerdict
  generatedAt: string | null
  metrics: DashboardMetricsResponse
}

const verdictStyles: Record<BusinessVerdict, { panel: string; pill: string; dot: string }> = {
  normal: {
    panel: "border-success/25 bg-success/8",
    pill: "border border-success/25 bg-success/10 text-success",
    dot: "bg-success",
  },
  degraded: {
    panel: "border-warning/35 bg-warning/8",
    pill: "border border-warning/25 bg-warning/10 text-warning",
    dot: "bg-warning",
  },
  critical: {
    panel: "border-destructive/35 bg-destructive/8",
    pill: "border border-destructive/30 bg-destructive/10 text-destructive",
    dot: "bg-destructive",
  },
}

export function BusinessHealthHero({ verdict, generatedAt, metrics }: BusinessHealthHeroProps) {
  const intl = useIntl()
  const styles = verdictStyles[verdict]
  const formattedTime = generatedAt
    ? new Intl.DateTimeFormat(intl.locale, { dateStyle: "short", timeStyle: "medium" }).format(new Date(generatedAt))
    : null

  return (
    <section className="grid gap-4 border-b border-border/80 pb-4 lg:grid-cols-[minmax(0,1fr)_420px]">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-[11px] font-semibold uppercase text-muted-foreground">
            {intl.formatMessage({ id: "nav.path.business.dashboard" })}
          </span>
          <span className="rounded-md border border-border bg-card px-2 py-1 text-xs font-medium text-muted-foreground">
            {intl.formatMessage({ id: "businessDashboard.previewBadge" })}
          </span>
        </div>
        <h1 className="mt-3 text-3xl font-semibold text-foreground sm:text-4xl">
          {intl.formatMessage({ id: "businessDashboard.title" })}
        </h1>
        <p className="mt-2 max-w-3xl text-sm leading-6 text-muted-foreground">
          {intl.formatMessage({ id: "businessDashboard.description" })}
        </p>
        <div className="mt-4 flex flex-wrap gap-2">
          <Button asChild size="sm">
            <Link to="/cluster/monitor">
              <Activity aria-hidden className="size-3.5" />
              {intl.formatMessage({ id: "businessDashboard.actions.openMonitor" })}
            </Link>
          </Button>
          <Button asChild size="sm" variant="outline">
            <Link to="/business/messages">
              <MessageSquareText aria-hidden className="size-3.5" />
              {intl.formatMessage({ id: "businessDashboard.actions.openMessages" })}
            </Link>
          </Button>
        </div>
      </div>

      <aside className={cn("rounded-lg border p-4", styles.panel)}>
        <div className="flex items-start justify-between gap-3">
          <div>
            <p className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "businessDashboard.health.title" })}</p>
            <span aria-live="polite" className={cn("mt-2 inline-flex w-fit items-center gap-2 rounded-md px-2.5 py-1 text-sm font-medium", styles.pill)} role="status">
            <span aria-hidden="true" className={cn("size-2 rounded-full", styles.dot)} />
            {intl.formatMessage({ id: `businessDashboard.verdict.${verdict}` })}
            </span>
          </div>
          <ArrowUpRight aria-hidden className="size-4 text-muted-foreground" />
        </div>
        <p className="mt-3 text-sm leading-6 text-muted-foreground">
          {intl.formatMessage(
            { id: "businessDashboard.summary" },
            {
              send: metrics.metrics.send_per_sec.latest.toLocaleString(),
              deliver: metrics.metrics.deliver_per_sec.latest.toLocaleString(),
              fail: Math.max(metrics.metrics.send_fail_rate_percent.latest, metrics.metrics.delivery_fail_rate_percent.latest).toFixed(2),
              connections: metrics.metrics.connections.latest.toLocaleString(),
              retry: metrics.metrics.retry_queue_depth.latest.toLocaleString(),
            },
          )}
        </p>
        <dl className="mt-4 grid gap-2 text-xs text-muted-foreground sm:grid-cols-2">
          <div className="rounded-md border border-border/70 bg-background/55 px-3 py-2">
            <dt>{intl.formatMessage({ id: "businessDashboard.window" })}</dt>
            <dd className="mt-1 font-mono text-sm font-semibold text-foreground">{Math.round(metrics.window_seconds / 60)}m</dd>
          </div>
          {formattedTime ? (
            <div className="rounded-md border border-border/70 bg-background/55 px-3 py-2">
              <dt>{intl.formatMessage({ id: "businessDashboard.generatedAtLabel" })}</dt>
              <dd className="mt-1 truncate font-mono text-sm font-semibold text-foreground">{formattedTime}</dd>
            </div>
          ) : null}
        </dl>
      </aside>
    </section>
  )
}
