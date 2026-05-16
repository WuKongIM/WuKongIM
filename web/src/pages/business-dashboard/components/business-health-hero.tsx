import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { DashboardMetricsResponse } from "@/lib/manager-api"
import type { BusinessVerdict } from "../view-model"

type BusinessHealthHeroProps = {
  verdict: BusinessVerdict
  generatedAt: string | null
  metrics: DashboardMetricsResponse
  refreshing: boolean
  onRefresh: () => void
}

const verdictStyles: Record<BusinessVerdict, { pill: string; dot: string }> = {
  normal: { pill: "border border-success/25 bg-success/10 text-success", dot: "bg-success" },
  degraded: { pill: "border border-warning/25 bg-warning/10 text-warning", dot: "bg-warning" },
  critical: { pill: "border border-destructive/30 bg-destructive/10 text-destructive", dot: "bg-destructive" },
}

export function BusinessHealthHero({ verdict, generatedAt, metrics, refreshing, onRefresh }: BusinessHealthHeroProps) {
  const intl = useIntl()
  const styles = verdictStyles[verdict]
  const formattedTime = generatedAt
    ? new Intl.DateTimeFormat(intl.locale, { dateStyle: "short", timeStyle: "medium" }).format(new Date(generatedAt))
    : null

  return (
    <section className="overflow-hidden rounded-3xl border border-border/80 bg-[linear-gradient(135deg,rgba(255,255,255,0.07),rgba(255,255,255,0.025))] shadow-[0_24px_80px_rgba(0,0,0,0.18)]">
      <div className="flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:justify-between lg:p-6">
        <div className="flex flex-col gap-2">
          <p className="font-mono text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
            {intl.formatMessage({ id: "businessDashboard.eyebrow" })}
          </p>
          <span aria-live="polite" className={cn("inline-flex w-fit items-center gap-2 rounded-full px-3 py-1 text-sm font-medium", styles.pill)} role="status">
            <span aria-hidden="true" className={cn("size-2 rounded-full", styles.dot)} />
            {intl.formatMessage({ id: `businessDashboard.verdict.${verdict}` })}
          </span>
          <p className="text-sm text-muted-foreground">
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
        </div>
        <div className="flex shrink-0 items-center gap-3">
          {formattedTime ? <span className="text-xs text-muted-foreground">{intl.formatMessage({ id: "businessDashboard.generatedAt" }, { value: formattedTime })}</span> : null}
          <Button disabled={refreshing} onClick={onRefresh} size="sm" variant="outline">
            {refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </div>
      </div>
    </section>
  )
}
