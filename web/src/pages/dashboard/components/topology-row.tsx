import { Link } from "react-router-dom"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { StatusBadge } from "@/components/manager/status-badge"
import { cn } from "@/lib/utils"
import type { TopologyRowData } from "../view-model"

type TopologyRowProps = {
  row: TopologyRowData
}

function statusDotClass(status: string): string {
  switch (status.toLowerCase()) {
    case "alive":
      return "bg-[var(--status-healthy)]"
    case "draining":
    case "suspect":
      return "bg-[var(--status-warning)]"
    case "dead":
      return "bg-[var(--status-error)]"
    default:
      return "bg-muted-foreground"
  }
}

export function TopologyRow({ row }: TopologyRowProps) {
  const intl = useIntl()

  const localLabel = intl.formatMessage({ id: "dashboard.topology.local" })
  const slotsLabel = intl.formatMessage(
    { id: "dashboard.topology.slotsValue" },
    { count: row.slotsCount, leaders: row.leaderCount },
  )
  const watermarkLabel = row.hasWatermark
    ? `first ${row.firstIndex} / applied ${row.appliedIndex} / snapshot ${row.snapshotIndex}`
    : intl.formatMessage({ id: "dashboard.topology.watermarkUnreported" })
  const openNodeLabel = intl.formatMessage(
    { id: "dashboard.topology.openNode" },
    { id: row.nodeId },
  )

  return (
    <div className="grid grid-cols-[auto_1fr_auto_auto_auto_auto_auto] items-center gap-3 rounded-xl border border-border/60 bg-muted/20 px-3 py-2 text-sm">
      {/* 1. Status dot */}
      <span
        className={cn("size-2 rounded-full", statusDotClass(row.status))}
        aria-hidden="true"
      />

      {/* 2. Name block */}
      <div className="flex items-center gap-1.5 truncate">
        <span className="font-medium text-foreground">{row.name}</span>
        <span className="font-mono text-xs text-muted-foreground">(#{row.nodeId})</span>
        {row.isLocal && (
          <span className="rounded-full border border-primary/25 bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
            {localLabel}
          </span>
        )}
      </div>

      {/* 3. Status badge */}
      <StatusBadge value={row.status} />

      {/* 4. Role */}
      <span className="text-xs text-muted-foreground">{row.role}</span>

      {/* 5. Slots */}
      <span className="text-xs text-muted-foreground">{slotsLabel}</span>

      {/* 6. Watermark */}
      <span className="text-xs text-muted-foreground">{watermarkLabel}</span>

      {/* 7. Action button */}
      <Button asChild size="sm" variant="ghost" aria-label={openNodeLabel}>
        <Link to={row.href}>↗</Link>
      </Button>
    </div>
  )
}
