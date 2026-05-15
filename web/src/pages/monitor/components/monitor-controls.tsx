import { Pause, Play } from "lucide-react"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"

import type { MonitorNodeOption, NodeId, TimeRange } from "../types"

type MonitorControlsProps = {
  nodes: MonitorNodeOption[]
  selectedNode: NodeId
  onNodeChange: (node: NodeId) => void
  timeRange: TimeRange
  onTimeRangeChange: (range: TimeRange) => void
  isPaused: boolean
  onPauseToggle: () => void
  nodeFilterEnabled: boolean
}

const TIME_RANGES: TimeRange[] = ["5m", "15m", "30m", "1h"]

export function MonitorControls({
  nodes,
  selectedNode,
  onNodeChange,
  timeRange,
  onTimeRangeChange,
  isPaused,
  onPauseToggle,
  nodeFilterEnabled,
}: MonitorControlsProps) {
  const intl = useIntl()
  const scopedNode = nodes.find((node) => node.isLocal) ?? nodes[0]
  const effectiveSelectedNode = nodeFilterEnabled ? selectedNode : (scopedNode?.id ?? selectedNode)
  const visibleNodes = nodeFilterEnabled ? nodes : scopedNode ? [scopedNode] : []

  return (
    <div className="flex items-center gap-4">
      <div className="flex items-center gap-2">
        <span className="text-sm text-muted-foreground">
          {intl.formatMessage({ id: "monitor.controls.node" })}
        </span>
        <select
          aria-label="Node filter"
          value={effectiveSelectedNode}
          onChange={(e) => onNodeChange(e.target.value as NodeId)}
          disabled={!nodeFilterEnabled}
          className="h-9 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {nodeFilterEnabled ? (
            <option value="all">
              {intl.formatMessage({ id: "monitor.controls.allNodes" })}
            </option>
          ) : null}
          {visibleNodes.map((node) => (
            <option key={node.id} value={node.id}>
              {node.label}
            </option>
          ))}
        </select>
      </div>

      <div className="flex items-center gap-2">
        <span className="text-sm text-muted-foreground">
          {intl.formatMessage({ id: "monitor.controls.timeRange" })}
        </span>
        <div className="inline-flex h-9 items-center justify-center rounded-md bg-muted p-1 text-muted-foreground">
          {TIME_RANGES.map((range) => (
            <button
              key={range}
              onClick={() => onTimeRangeChange(range)}
              className={`inline-flex items-center justify-center whitespace-nowrap rounded-sm px-3 py-1 text-sm font-medium ring-offset-background transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 ${
                timeRange === range
                  ? "bg-background text-foreground shadow-sm"
                  : "hover:bg-background/50 hover:text-foreground"
              }`}
              aria-label={`${range} time range`}
            >
              {range}
            </button>
          ))}
        </div>
      </div>

      <Button
        variant={isPaused ? "default" : "outline"}
        size="sm"
        onClick={onPauseToggle}
        className="ml-auto"
      >
        {isPaused ? (
          <>
            <Play className="mr-2 h-4 w-4" />
            {intl.formatMessage({ id: "monitor.controls.resume" })}
          </>
        ) : (
          <>
            <Pause className="mr-2 h-4 w-4" />
            {intl.formatMessage({ id: "monitor.controls.pause" })}
          </>
        )}
      </Button>
    </div>
  )
}
