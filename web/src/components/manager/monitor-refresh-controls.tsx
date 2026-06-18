import { RefreshCw } from "lucide-react"

import { cn } from "@/lib/utils"

export type MonitorRefreshInterval = "off" | "30s" | "5m" | "30m" | "1h"

type MonitorRefreshControlsProps = {
  interval: MonitorRefreshInterval
  labels: {
    refresh: string
    autoRefresh: string
    off: string
    thirtySeconds: string
    fiveMinutes: string
    thirtyMinutes: string
    oneHour: string
  }
  onIntervalChange: (interval: MonitorRefreshInterval) => void
  onRefresh: () => void
}

const refreshIntervalMs: Record<Exclude<MonitorRefreshInterval, "off">, number> = {
  "30s": 30_000,
  "5m": 5 * 60_000,
  "30m": 30 * 60_000,
  "1h": 60 * 60_000,
}

export function monitorRefreshIntervalMs(interval: MonitorRefreshInterval) {
  return interval === "off" ? null : refreshIntervalMs[interval]
}

export function MonitorRefreshControls({ interval, labels, onIntervalChange, onRefresh }: MonitorRefreshControlsProps) {
  return (
    <div className="inline-flex h-8 items-stretch overflow-hidden rounded-lg border border-border bg-background">
      <button
        aria-label={labels.refresh}
        className={cn(
          "inline-flex w-9 items-center justify-center border-r border-border text-muted-foreground transition-colors",
          "hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
        )}
        onClick={onRefresh}
        type="button"
      >
        <RefreshCw aria-hidden className="size-4" />
      </button>
      <select
        aria-label={labels.autoRefresh}
        className="h-full min-w-24 bg-transparent px-2 text-xs font-medium text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
        onChange={(event) => onIntervalChange(event.currentTarget.value as MonitorRefreshInterval)}
        value={interval}
      >
        <option value="off">{labels.off}</option>
        <option value="30s">{labels.thirtySeconds}</option>
        <option value="5m">{labels.fiveMinutes}</option>
        <option value="30m">{labels.thirtyMinutes}</option>
        <option value="1h">{labels.oneHour}</option>
      </select>
    </div>
  )
}
