import { Button } from "@/components/ui/button"
import type { NetworkNodeOption, NetworkTimeRange } from "../utils/aggregation"

type FilterBarProps = {
  nodeOptions: NetworkNodeOption[]
  selectedNodes: number[]
  timeRange: NetworkTimeRange
  autoRefresh: boolean
  refreshing: boolean
  scopeBadges: string[]
  onSelectedNodesChange: (nodeIds: number[]) => void
  onTimeRangeChange: (range: NetworkTimeRange) => void
  onAutoRefreshToggle: () => void
  onRefresh: () => void
  labels: {
    nodeSelector: string
    allNodes: string
    timeRange: string
    last1m: string
    last5m: string
    last15m: string
    autoRefresh: string
    refresh: string
    refreshing: string
  }
}

export function FilterBar({ nodeOptions, selectedNodes, timeRange, autoRefresh, refreshing, scopeBadges, onSelectedNodesChange, onTimeRangeChange, onAutoRefreshToggle, onRefresh, labels }: FilterBarProps) {
  return (
    <section className="rounded-xl border border-border bg-card p-4 shadow-none">
      <div className="grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_auto_auto] lg:items-end">
        <label className="grid gap-1 text-sm font-medium text-foreground">
          {labels.nodeSelector}
          <select
            aria-label={labels.nodeSelector}
            className="min-h-9 rounded-lg border border-border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            multiple
            onChange={(event) => {
              const values = Array.from(event.currentTarget.selectedOptions).map((option) => Number(option.value)).filter(Boolean)
              onSelectedNodesChange(values)
            }}
            value={selectedNodes.map(String)}
          >
            <option value="">{labels.allNodes}</option>
            {nodeOptions.map((node) => <option key={node.nodeId} value={node.nodeId}>{node.label}</option>)}
          </select>
        </label>
        <label className="grid gap-1 text-sm font-medium text-foreground">
          {labels.timeRange}
          <select
            aria-label={labels.timeRange}
            className="h-9 rounded-lg border border-border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            onChange={(event) => onTimeRangeChange(event.currentTarget.value as NetworkTimeRange)}
            value={timeRange}
          >
            <option value="1m">{labels.last1m}</option>
            <option value="5m">{labels.last5m}</option>
            <option value="15m">{labels.last15m}</option>
          </select>
        </label>
        <Button onClick={onRefresh} size="sm" variant="outline">{refreshing ? labels.refreshing : labels.refresh}</Button>
        <label className="flex h-9 items-center gap-2 rounded-lg border border-border bg-background px-3 text-sm text-foreground">
          <input checked={autoRefresh} onChange={onAutoRefreshToggle} type="checkbox" />
          {labels.autoRefresh}
        </label>
      </div>
      <div className="mt-3 flex flex-wrap gap-2 text-xs text-muted-foreground">
        {scopeBadges.map((badge) => <span className="rounded-md border border-border bg-background px-3 py-1.5" key={badge}>{badge}</span>)}
      </div>
    </section>
  )
}
