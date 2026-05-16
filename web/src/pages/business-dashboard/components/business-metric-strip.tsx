import { useIntl } from "react-intl"

import type { BusinessMetricValue } from "../view-model"

type BusinessMetricStripProps = {
  metrics: BusinessMetricValue[]
}

export function BusinessMetricStrip({ metrics }: BusinessMetricStripProps) {
  const intl = useIntl()
  return (
    <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
      {metrics.map((metric) => (
        <div className="rounded-2xl border border-border/80 bg-card/88 px-4 py-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.035)]" key={metric.key}>
          <div className="flex items-center justify-between gap-2">
            <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
              {intl.formatMessage({ id: metric.labelId })}
            </span>
            {metric.source !== "real" ? (
              <span className="rounded-full border border-border bg-background/70 px-2 py-0.5 text-[10px] text-muted-foreground">
                {intl.formatMessage({ id: `businessDashboard.source.${metric.source}` })}
              </span>
            ) : null}
          </div>
          <div className="mt-2 font-mono text-2xl font-semibold tracking-[-0.04em] text-foreground">{metric.formatted}</div>
          <p className="mt-1 text-xs text-muted-foreground">{metric.detail}</p>
        </div>
      ))}
    </section>
  )
}
