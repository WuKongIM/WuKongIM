import { Link } from "react-router-dom"
import { useIntl } from "react-intl"

import { StatusBadge } from "@/components/manager/status-badge"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import type { ClusterTopologyRow } from "../view-model"

type ClusterTopologyWatermarksProps = {
  rows: ClusterTopologyRow[]
}

export function ClusterTopologyWatermarks({ rows }: ClusterTopologyWatermarksProps) {
  const intl = useIntl()

  return (
    <SectionCard title={intl.formatMessage({ id: "clusterDashboard.topology.title" })}>
      <div className="space-y-2">
        {rows.map((row) => (
          <div className="grid gap-2 rounded-xl border border-border/60 bg-muted/20 px-3 py-2 text-sm lg:grid-cols-[1fr_auto_auto_auto_auto_auto] lg:items-center" key={row.nodeId}>
            <div className="flex min-w-0 items-center gap-2">
              <span className="truncate font-medium text-foreground">{row.name}</span>
              <span className="font-mono text-xs text-muted-foreground">#{row.nodeId}</span>
              {row.isLocal ? <span className="rounded-full border border-primary/25 bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">{intl.formatMessage({ id: "clusterDashboard.topology.local" })}</span> : null}
            </div>
            <StatusBadge value={row.status} />
            <span className="text-xs text-muted-foreground">{row.role} · {row.slotsCount} slots · {row.leaderCount} leaders</span>
            <span className="font-mono text-xs text-muted-foreground">first {row.firstIndex} / applied {row.appliedIndex} / snapshot {row.snapshotIndex}</span>
            <span className="font-mono text-xs text-muted-foreground">RPC {row.rpcErrorRate?.toFixed(2) ?? "--"}% · p95 {row.rpcP95Ms ?? "--"}ms</span>
            <Button aria-label={intl.formatMessage({ id: "clusterDashboard.topology.openNode" }, { id: row.nodeId })} asChild size="sm" variant="ghost">
              <Link to={row.href}>↗</Link>
            </Button>
          </div>
        ))}
      </div>
    </SectionCard>
  )
}
