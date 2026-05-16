import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { ClusterVerdict } from "../view-model"

type ClusterHealthHeroProps = {
  verdict: ClusterVerdict
  generatedAt: string | null
  controllerLeaderId: number
  slotsReady: number
  slotsTotal: number
  nodesAlive: number
  nodesTotal: number
  incidentCount: number
  refreshing: boolean
  onRefresh: () => void
}

const verdictStyles: Record<ClusterVerdict, { pill: string; dot: string }> = {
  healthy: { pill: "border border-success/25 bg-success/10 text-success", dot: "bg-success" },
  degraded: { pill: "border border-warning/25 bg-warning/10 text-warning", dot: "bg-warning" },
  critical: { pill: "border border-destructive/30 bg-destructive/10 text-destructive", dot: "bg-destructive" },
}

export function ClusterHealthHero({
  verdict,
  generatedAt,
  controllerLeaderId,
  slotsReady,
  slotsTotal,
  nodesAlive,
  nodesTotal,
  incidentCount,
  refreshing,
  onRefresh,
}: ClusterHealthHeroProps) {
  const intl = useIntl()
  const formattedTime = generatedAt
    ? new Intl.DateTimeFormat(intl.locale, {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      }).format(new Date(generatedAt))
    : null
  const generatedAtLabel = formattedTime
    ? intl.formatMessage({ id: "clusterDashboard.generatedAt" }, { value: formattedTime })
    : null
  const styles = verdictStyles[verdict]

  return (
    <section className="overflow-hidden rounded-3xl border border-border/80 bg-[linear-gradient(135deg,rgba(255,255,255,0.07),rgba(255,255,255,0.025))] shadow-[0_24px_80px_rgba(0,0,0,0.18)]">
      <div className="flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:justify-between lg:p-6">
        <div className="flex flex-col gap-2">
          <p className="font-mono text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
            {intl.formatMessage({ id: "clusterDashboard.eyebrow" })}
          </p>
          <span
            aria-live="polite"
            className={cn("inline-flex w-fit items-center gap-2 rounded-full px-3 py-1 text-sm font-medium", styles.pill)}
            role="status"
          >
            <span aria-hidden="true" className={cn("size-2 rounded-full", styles.dot)} />
            {intl.formatMessage({ id: `clusterDashboard.verdict.${verdict}` })}
          </span>
          <p className="text-sm text-muted-foreground">
            {intl.formatMessage(
              { id: "clusterDashboard.summary" },
              { incidents: incidentCount, leader: controllerLeaderId, ready: slotsReady, total: slotsTotal, alive: nodesAlive, nodes: nodesTotal },
            )}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          {generatedAtLabel ? <span className="text-xs text-muted-foreground">{generatedAtLabel}</span> : null}
          <Button disabled={refreshing} onClick={onRefresh} size="sm" variant="outline">
            {refreshing ? intl.formatMessage({ id: "common.refreshing" }) : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </div>
      </div>
    </section>
  )
}
