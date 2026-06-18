import { useIntl } from "react-intl"

import { cn } from "@/lib/utils"
import type { BusinessMetricGroup, BusinessMetricValue } from "../view-model"

type BusinessMetricStripProps = {
  groups: BusinessMetricGroup[]
}

export function BusinessMetricStrip({ groups }: BusinessMetricStripProps) {
  const intl = useIntl()

  return (
    <section className="grid gap-3 lg:grid-cols-3">
      {groups.map((group) => (
        <article className="rounded-lg border border-border/80 bg-card p-4" key={group.key}>
          <h2 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: group.titleId })}</h2>
          <div className="mt-3 grid gap-2">
            {group.metrics.map((metric) => (
              <MetricRow intlFormat={(id) => intl.formatMessage({ id })} key={metric.key} metric={metric} />
            ))}
          </div>
        </article>
      ))}
    </section>
  )
}

function MetricRow({
  metric,
  intlFormat,
}: {
  metric: BusinessMetricValue
  intlFormat: (id: string) => string
}) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-md border border-border/70 bg-background/55 px-3 py-2">
      <div className="min-w-0">
        <div className="truncate text-xs font-medium text-muted-foreground">{intlFormat(metric.labelId)}</div>
        <div className="mt-1 text-xs text-muted-foreground">{intlFormat(metric.detailId)}</div>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {metric.source !== "real" ? (
          <span className="rounded-md border border-border bg-card px-1.5 py-0.5 text-[10px] text-muted-foreground">
            {intlFormat(`businessDashboard.source.${metric.source}`)}
          </span>
        ) : null}
        <span
          className={cn(
            "font-mono text-lg font-semibold text-foreground",
            metric.tone === "warning" ? "text-warning" : null,
            metric.tone === "danger" ? "text-destructive" : null,
          )}
        >
          {metric.formatted}
        </span>
      </div>
    </div>
  )
}
