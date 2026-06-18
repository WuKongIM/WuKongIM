import { useIntl } from "react-intl"

import { cn } from "@/lib/utils"

import type { MonitorSnapshotEntry, MonitorTone } from "../types"

type MonitorSnapshotStripProps = {
  entries: MonitorSnapshotEntry[]
}

const toneStyles: Record<MonitorTone, string> = {
  normal: "border-success/25 bg-success/8 text-success",
  warning: "border-warning/30 bg-warning/8 text-warning",
  critical: "border-destructive/30 bg-destructive/8 text-destructive",
  preview: "border-primary/20 bg-primary/8 text-primary",
}

export function MonitorSnapshotStrip({ entries }: MonitorSnapshotStripProps) {
  const intl = useIntl()

  return (
    <section className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-7">
      {entries.map((entry) => (
        <div className="rounded-lg border border-border/80 bg-card/82 px-3 py-3" key={entry.key}>
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs font-medium text-muted-foreground">{intl.formatMessage({ id: entry.labelId })}</span>
            <span aria-hidden className={cn("size-2 rounded-full border", toneStyles[entry.tone])} />
          </div>
          <div className="mt-2 flex items-baseline gap-1">
            <span className="text-lg font-semibold text-foreground">{entry.value}</span>
            {entry.unit ? <span className="text-xs text-muted-foreground">{entry.unit}</span> : null}
          </div>
        </div>
      ))}
    </section>
  )
}
