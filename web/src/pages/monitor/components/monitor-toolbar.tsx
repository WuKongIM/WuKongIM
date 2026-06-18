import { Pause, Play } from "lucide-react"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

import type { TimeRange } from "../types"

type MonitorToolbarProps = {
  generatedAt: string
  scopeLabelId: string
  timeRange: TimeRange
  isPaused: boolean
  onTimeRangeChange: (range: TimeRange) => void
  onPauseToggle: () => void
}

const ranges: TimeRange[] = ["5m", "15m", "30m", "1h"]

export function MonitorToolbar({
  generatedAt,
  scopeLabelId,
  timeRange,
  isPaused,
  onTimeRangeChange,
  onPauseToggle,
}: MonitorToolbarProps) {
  const intl = useIntl()
  const formattedTime = new Intl.DateTimeFormat(intl.locale, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(generatedAt))
  const pauseLabel = intl.formatMessage({ id: isPaused ? "monitor.controls.resumeLivePreview" : "monitor.controls.pauseLivePreview" })

  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border/80 bg-card/80 p-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.035)] lg:flex-row lg:items-center lg:justify-between">
      <div className="flex flex-wrap items-center gap-2">
        <span className="rounded-md border border-primary/20 bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary">
          {intl.formatMessage({ id: scopeLabelId })}
        </span>
        <span className="text-xs text-muted-foreground">
          {intl.formatMessage({ id: "monitor.generatedAt" }, { time: formattedTime })}
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="inline-flex h-8 items-center rounded-lg border border-border bg-background p-1">
          {ranges.map((range) => (
            <button
              aria-label={`${range} time range`}
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

        <Button aria-label={pauseLabel} onClick={onPauseToggle} size="sm" type="button" variant="outline">
          {isPaused ? <Play aria-hidden className="size-3.5" /> : <Pause aria-hidden className="size-3.5" />}
          {intl.formatMessage({ id: isPaused ? "monitor.controls.resume" : "monitor.controls.pause" })}
        </Button>
      </div>
    </section>
  )
}
