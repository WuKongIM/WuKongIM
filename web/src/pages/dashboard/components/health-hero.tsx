import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { Verdict } from "../view-model"

type HealthHeroProps = {
  verdict: Verdict
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

const verdictStyles: Record<
  Verdict,
  { pill: string; dot: string }
> = {
  healthy: {
    pill: "border border-success/25 bg-success/10 text-success",
    dot: "bg-success",
  },
  degraded: {
    pill: "border border-warning/25 bg-warning/10 text-warning",
    dot: "bg-warning",
  },
  critical: {
    pill: "border border-destructive/30 bg-destructive/10 text-destructive",
    dot: "bg-destructive",
  },
}

export function HealthHero({
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
}: HealthHeroProps) {
  const intl = useIntl()
  const styles = verdictStyles[verdict]

  const verdictLabel = intl.formatMessage({ id: `dashboard.verdict.${verdict}` })

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

  const summary =
    verdict === "healthy" && formattedTime
      ? intl.formatMessage(
          { id: "dashboard.verdict.summaryHealthy" },
          { time: formattedTime },
        )
      : intl.formatMessage(
          { id: "dashboard.verdict.summary" },
          {
            incidents: incidentCount,
            id: controllerLeaderId,
            ready: slotsReady,
            total: slotsTotal,
            alive: nodesAlive,
            totalNodes: nodesTotal,
          },
        )

  const generatedAtLabel = generatedAt && formattedTime
    ? intl.formatMessage({ id: "dashboard.generatedAtValue" }, { value: formattedTime })
    : null

  const cockpitEyebrow = intl.formatMessage({ id: "dashboard.cockpitEyebrow" })
  const scope = intl.formatMessage({ id: "dashboard.scope" })

  return (
    <section className="overflow-hidden rounded-3xl border border-border/80 bg-[linear-gradient(135deg,rgba(255,255,255,0.07),rgba(255,255,255,0.025))] shadow-[0_24px_80px_rgba(0,0,0,0.18)]">
      <div className="flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:justify-between lg:p-6">
        {/* Left: verdict pill + summary */}
        <div className="flex flex-col gap-2">
          <span
            role="status"
            aria-live="polite"
            className={cn(
              "inline-flex w-fit items-center gap-2 rounded-full px-3 py-1 text-sm font-medium",
              styles.pill,
            )}
          >
            <span className={cn("size-2 rounded-full", styles.dot)} aria-hidden="true" />
            {verdictLabel}
          </span>
          <p className="text-sm text-muted-foreground">{summary}</p>
        </div>

        {/* Right: timestamp + refresh */}
        <div className="flex shrink-0 items-center gap-3">
          {generatedAtLabel ? (
            <span className="text-xs text-muted-foreground">{generatedAtLabel}</span>
          ) : null}
          <Button size="sm" variant="outline" onClick={onRefresh} disabled={refreshing}>
            {refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        </div>
      </div>

      {/* Eyebrow row */}
      <div className="border-t border-border/80 bg-background/35 px-5 py-3 lg:px-6">
        <p className="font-mono text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
          {cockpitEyebrow}
          <span className="mx-2 opacity-40" aria-hidden="true">·</span>
          {scope}
        </p>
      </div>
    </section>
  )
}
