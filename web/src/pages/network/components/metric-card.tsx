import type { KeyboardEvent, ReactNode } from "react"

import { cn } from "@/lib/cn"
import type { NetworkAlertLevel } from "../utils/aggregation"

type MetricLabel = { label: string; value: string | number }

export type MetricCardProps = {
  title: string
  primaryMetric: string | number
  chart: ReactNode
  labels?: MetricLabel[]
  alertLevel?: NetworkAlertLevel
  onClick?: () => void
  actionLabel?: string
}

export function MetricCard({ title, primaryMetric, chart, labels = [], alertLevel = "none", onClick, actionLabel }: MetricCardProps) {
  const interactiveProps = onClick
    ? {
      role: "button",
      tabIndex: 0,
      onClick,
      onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault()
          onClick()
        }
      },
    }
    : {}

  return (
    <div
      aria-label={actionLabel ? `${title}. ${actionLabel}` : title}
      className={cn(
        "group rounded-xl border bg-card p-4 text-card-foreground transition hover:-translate-y-0.5 hover:shadow-sm",
        onClick && "cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        alertLevel === "warning" && "border-amber-500/40 bg-amber-500/5",
        alertLevel === "danger" && "border-destructive/40 bg-destructive/5",
      )}
      {...interactiveProps}
    >
      <div className={cn("text-xs font-semibold uppercase tracking-[0.16em] text-muted-foreground", alertLevel === "danger" && "text-destructive")}>{title}</div>
      <div className="mt-3 text-3xl font-semibold tracking-tight text-foreground">{primaryMetric}</div>
      <div className="mt-4 min-h-32">{chart}</div>
      {labels.length > 0 ? (
        <div className="mt-4 flex flex-wrap gap-x-4 gap-y-2 text-xs text-muted-foreground">
          {labels.map((item) => <span key={`${item.label}-${item.value}`}>{item.label}: {item.value}</span>)}
        </div>
      ) : null}
    </div>
  )
}
