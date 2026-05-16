import { Link } from "react-router-dom"
import { useIntl } from "react-intl"

import type { ClusterMetricValue } from "../view-model"

type ClusterMetricStripProps = {
  metrics: ClusterMetricValue[]
}

export function ClusterMetricStrip({ metrics }: ClusterMetricStripProps) {
  return (
    <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-7">
      {metrics.map((metric) => <MetricTile key={metric.key} metric={metric} />)}
    </section>
  )
}

function MetricTile({ metric }: { metric: ClusterMetricValue }) {
  const intl = useIntl()
  const body = (
    <>
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
          {intl.formatMessage({ id: metric.labelId })}
        </span>
        {metric.source !== "real" ? (
          <span className="rounded-full border border-border bg-background/70 px-2 py-0.5 text-[10px] text-muted-foreground">
            {intl.formatMessage({ id: `clusterDashboard.source.${metric.source}` })}
          </span>
        ) : null}
      </div>
      <div className="mt-2 font-mono text-2xl font-semibold tracking-[-0.04em] text-foreground">{metric.formatted}</div>
      <p className="mt-1 text-xs text-muted-foreground">{metric.detail}</p>
    </>
  )
  const className = "rounded-2xl border border-border/80 bg-card/88 px-4 py-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.035)] transition-colors hover:border-primary/40"
  return metric.href ? <Link className={className} to={metric.href}>{body}</Link> : <div className={className}>{body}</div>
}
