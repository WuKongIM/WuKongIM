import { useIntl } from "react-intl"

import { MonitorNodeSelector } from "@/components/manager/monitor-node-selector"
import { MonitorRefreshControls, type MonitorRefreshInterval } from "@/components/manager/monitor-refresh-controls"
import type { ManagerNodesResponse, RealtimeMonitorCategory } from "@/lib/manager-api.types"
import { cn } from "@/lib/utils"

import type { ClusterMonitorTimeRange } from "../types"

type ClusterMonitorToolbarProps = {
  generatedAt: string
  scopeLabelId: string
  scopeLabel?: string
  nodes: ManagerNodesResponse | null
  selectedCategory: RealtimeMonitorCategory
  selectedNodeId: number | null
  timeRange: ClusterMonitorTimeRange
  refreshInterval: MonitorRefreshInterval
  onCategoryChange: (category: RealtimeMonitorCategory) => void
  onNodeChange: (nodeId: number | null) => void
  onTimeRangeChange: (range: ClusterMonitorTimeRange) => void
  onRefresh: () => void
  onRefreshIntervalChange: (interval: MonitorRefreshInterval) => void
}

const ranges: ClusterMonitorTimeRange[] = ["5m", "15m", "30m", "1h"]
const categories: RealtimeMonitorCategory[] = [
  "common",
  "gateway",
  "internal",
  "message",
  "conversation",
  "channel",
  "database",
  "control",
  "slot",
  "node",
]

export function ClusterMonitorToolbar({
  generatedAt,
  scopeLabelId,
  scopeLabel,
  nodes,
  selectedCategory,
  selectedNodeId,
  timeRange,
  refreshInterval,
  onCategoryChange,
  onNodeChange,
  onTimeRangeChange,
  onRefresh,
  onRefreshIntervalChange,
}: ClusterMonitorToolbarProps) {
  const intl = useIntl()
  const formattedTime = new Intl.DateTimeFormat(intl.locale, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(generatedAt))

  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border/80 bg-card/80 p-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.035)] lg:flex-row lg:items-center lg:justify-between">
      <div className="flex flex-wrap items-center gap-2">
        <span className="rounded-md border border-primary/20 bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary">
          {scopeLabel ?? intl.formatMessage({ id: scopeLabelId })}
        </span>
        <span className="text-xs text-muted-foreground">
          {intl.formatMessage({ id: "clusterMonitor.generatedAt" }, { time: formattedTime })}
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <label className="flex h-8 items-center gap-2 rounded-lg border border-border bg-background px-2 text-xs font-medium text-muted-foreground">
          <span>{intl.formatMessage({ id: "clusterMonitor.controls.category" })}</span>
          <select
            aria-label={intl.formatMessage({ id: "clusterMonitor.controls.category" })}
            className="h-6 min-w-28 bg-transparent text-xs font-medium text-foreground outline-none"
            onChange={(event) => onCategoryChange(event.target.value as RealtimeMonitorCategory)}
            value={selectedCategory}
          >
            {categories.map((category) => (
              <option key={category} value={category}>
                {intl.formatMessage({ id: `clusterMonitor.category.${category}` })}
              </option>
            ))}
          </select>
        </label>

        <MonitorNodeSelector
          allNodesLabelId="clusterMonitor.controls.allNodes"
          labelId="clusterMonitor.controls.node"
          nodes={nodes}
          onNodeChange={onNodeChange}
          selectedNodeId={selectedNodeId}
        />

        <div className="inline-flex h-8 items-center rounded-lg border border-border bg-background p-1">
          {ranges.map((range) => (
            <button
              aria-label={`${range} time range`}
              aria-pressed={timeRange === range}
              className={cn(
                "h-6 rounded-md px-2.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
                timeRange === range && "bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground",
              )}
              key={range}
              onClick={() => onTimeRangeChange(range)}
              type="button"
            >
              {range}
            </button>
          ))}
        </div>

        <MonitorRefreshControls
          interval={refreshInterval}
          labels={{
            refresh: intl.formatMessage({ id: "clusterMonitor.controls.refreshNow" }),
            autoRefresh: intl.formatMessage({ id: "clusterMonitor.controls.autoRefresh" }),
            off: intl.formatMessage({ id: "clusterMonitor.controls.autoRefreshOff" }),
            thirtySeconds: intl.formatMessage({ id: "clusterMonitor.controls.autoRefresh30s" }),
            fiveMinutes: intl.formatMessage({ id: "clusterMonitor.controls.autoRefresh5m" }),
            thirtyMinutes: intl.formatMessage({ id: "clusterMonitor.controls.autoRefresh30m" }),
            oneHour: intl.formatMessage({ id: "clusterMonitor.controls.autoRefresh1h" }),
          }}
          onIntervalChange={onRefreshIntervalChange}
          onRefresh={onRefresh}
        />
      </div>
    </section>
  )
}
