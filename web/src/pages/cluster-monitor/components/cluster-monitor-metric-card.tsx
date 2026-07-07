import { useState } from "react"
import { CircleHelp } from "lucide-react"
import { useIntl } from "react-intl"
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"

import {
  Tooltip as HelpTooltip,
  TooltipContent as HelpTooltipContent,
  TooltipTrigger as HelpTooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

import type { ClusterMonitorMetricCard as ClusterMonitorMetricCardModel, ClusterMonitorPoint, ClusterMonitorTone } from "../types"

type ClusterMonitorMetricCardProps = {
  card: ClusterMonitorMetricCardModel
}

const toneStyles: Record<ClusterMonitorTone, { dot: string; badge: string }> = {
  normal: {
    dot: "bg-success",
    badge: "border-success/25 bg-success/8 text-success",
  },
  warning: {
    dot: "bg-warning",
    badge: "border-warning/30 bg-warning/8 text-warning",
  },
  critical: {
    dot: "bg-destructive",
    badge: "border-destructive/30 bg-destructive/8 text-destructive",
  },
  preview: {
    dot: "bg-primary",
    badge: "border-primary/20 bg-primary/8 text-primary",
  },
}

const clusterChartPalette = ["#2563eb", "#16a34a", "#dc2626", "#9333ea", "#0891b2", "#d97706"]

type ClusterChartRow = {
  time: string
  [key: string]: number | string
}

type ClusterChartSeries = {
  key: string
  label: string
}

export function buildClusterChartModel(points: ClusterMonitorPoint[], locale: string) {
  const rowsByTimestamp = new Map<number, ClusterChartRow>()
  const seriesByKey = new Map<string, ClusterChartSeries>()
  const timeFormatter = new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" })
  const hasNamedSeries = points.some(clusterPointHasSeriesIdentity)

  for (const point of points) {
    if (hasNamedSeries && !clusterPointHasSeriesIdentity(point)) continue
    const key = clusterPointSeriesKey(point)
    if (!seriesByKey.has(key)) {
      seriesByKey.set(key, { key, label: point.label ?? key })
    }
    const row =
      rowsByTimestamp.get(point.timestamp) ??
      ({
        time: timeFormatter.format(new Date(point.timestamp)),
      } satisfies ClusterChartRow)
    row[key] = point.value
    rowsByTimestamp.set(point.timestamp, row)
  }

  return {
    data: Array.from(rowsByTimestamp.entries())
      .sort(([left], [right]) => left - right)
      .map(([, row]) => row),
    series: Array.from(seriesByKey.values()),
  }
}

function clusterPointHasSeriesIdentity(point: ClusterMonitorPoint) {
  return Boolean(point.seriesKey || point.label)
}

function clusterPointSeriesKey(point: ClusterMonitorPoint) {
  return point.seriesKey || point.label || "value"
}

export function ClusterMonitorMetricCard({ card }: ClusterMonitorMetricCardProps) {
  const intl = useIntl()
  const title = intl.formatMessage({ id: card.titleId })
  const chartModel = buildClusterChartModel(card.series, intl.locale)
  const hasSeries = card.available !== false && chartModel.data.length > 0 && chartModel.series.length > 0
  const gradientBaseId = `cluster-monitor-gradient-${card.key}`
  const showSeriesNames = chartModel.series.length > 1
  const styles = toneStyles[card.tone]

  return (
    <article
      className="flex min-h-[320px] flex-col rounded-lg border border-border bg-card p-4 shadow-none"
      data-testid="cluster-monitor-metric-card"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-1.5">
            <h2 className="truncate text-sm font-semibold text-foreground">{title}</h2>
            <MetricHelpButton description={intl.formatMessage({ id: card.helpId })} label={title} />
          </div>
          <p className="mt-1 text-xs text-muted-foreground">{intl.formatMessage({ id: card.stageLabelId })}</p>
        </div>
        <span className={cn("inline-flex shrink-0 items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium", styles.badge)}>
          <span aria-hidden className={cn("size-1.5 rounded-full", styles.dot)} />
          {intl.formatMessage({ id: card.statusId })}
        </span>
      </div>

      <div className="mt-4 flex items-baseline gap-2">
        <span className="text-3xl font-semibold text-foreground">{card.value}</span>
        {card.unit ? <span className="text-sm text-muted-foreground">{card.unit}</span> : null}
      </div>

      <div className="mt-4 h-[120px]">
        {hasSeries ? (
          <ResponsiveContainer data-testid="cluster-monitor-chart" height="100%" width="100%">
            <AreaChart data={chartModel.data} margin={{ bottom: 0, left: 0, right: 4, top: 8 }}>
              <defs>
                {chartModel.series.map((series, index) => (
                  <linearGradient id={clusterGradientId(gradientBaseId, series.key)} key={series.key} x1="0" x2="0" y1="0" y2="1">
                    <stop offset="5%" stopColor={clusterSeriesColor(card.chartColor, index)} stopOpacity={showSeriesNames ? 0.18 : 0.32} />
                    <stop offset="95%" stopColor={clusterSeriesColor(card.chartColor, index)} stopOpacity={0.02} />
                  </linearGradient>
                ))}
              </defs>
              <XAxis
                axisLine={false}
                dataKey="time"
                fontSize={10}
                interval="preserveStartEnd"
                tickLine={false}
                tickMargin={8}
              />
              <YAxis
                axisLine={false}
                domain={["dataMin", "dataMax"]}
                fontSize={10}
                tickFormatter={formatClusterChartAxisValue}
                tickLine={false}
                width={44}
              />
              <Tooltip
                content={({ active, payload }) => {
                  if (!active || !payload?.length) return null
                  return (
                    <div className="rounded-md border border-border bg-background px-2.5 py-2 text-xs shadow-md">
                      {payload.map((item) => (
                        <p className="font-medium text-foreground" key={String(item.dataKey)}>
                          {showSeriesNames ? (
                            <span className="mr-1" style={{ color: item.color }}>
                              {item.name}:
                            </span>
                          ) : null}
                          {formatClusterChartValue(item.value, card.unit)}
                        </p>
                      ))}
                      <p className="text-muted-foreground">{payload[0].payload.time}</p>
                    </div>
                  )
                }}
              />
              {chartModel.series.map((series, index) => (
                <Area
                  connectNulls
                  dataKey={series.key}
                  fill={`url(#${clusterGradientId(gradientBaseId, series.key)})`}
                  isAnimationActive={false}
                  key={series.key}
                  name={showSeriesNames ? series.label : title}
                  stroke={clusterSeriesColor(card.chartColor, index)}
                  strokeWidth={2}
                  type="monotone"
                />
              ))}
            </AreaChart>
          </ResponsiveContainer>
        ) : (
          <div className="flex h-full items-center justify-center rounded-md border border-dashed border-border/80 bg-background/35 text-xs text-muted-foreground">
            {intl.formatMessage({ id: "clusterMonitor.chart.noSeriesData" })}
          </div>
        )}
      </div>

      <dl className="mt-auto grid grid-cols-3 gap-2 pt-4">
        {card.stats.map((stat) => {
          const label = stat.label ?? (stat.labelId ? intl.formatMessage({ id: stat.labelId }) : "")
          return (
            <div className="min-w-0 rounded-md border border-border bg-background px-2 py-2" key={`${label}:${stat.value}`}>
              <dt className="truncate text-[11px] text-muted-foreground">{label}</dt>
              <dd className="mt-1 truncate text-xs font-semibold text-foreground">{stat.value}</dd>
            </div>
          )
        })}
      </dl>
    </article>
  )
}

function clusterGradientId(baseId: string, seriesKey: string) {
  return `${baseId}-${seriesKey.replace(/[^a-zA-Z0-9_-]/g, "-")}`
}

function clusterSeriesColor(primary: string, index: number) {
  if (index === 0) return primary
  return clusterChartPalette[(index - 1) % clusterChartPalette.length]
}

function MetricHelpButton({ description, label }: { description: string; label: string }) {
  const intl = useIntl()
  const [open, setOpen] = useState(false)

  return (
    <HelpTooltip onOpenChange={setOpen} open={open}>
      <HelpTooltipTrigger asChild>
        <button
          aria-expanded={open}
          aria-label={intl.formatMessage({ id: "clusterMonitor.help.aria" }, { label })}
          className="inline-flex size-5 shrink-0 items-center justify-center rounded-full text-muted-foreground transition hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
          onClick={(event) => {
            event.preventDefault()
            setOpen(true)
          }}
          type="button"
        >
          <CircleHelp aria-hidden="true" className="size-3.5" />
        </button>
      </HelpTooltipTrigger>
      <HelpTooltipContent className="max-w-72 text-left leading-5" side="top" sideOffset={6}>
        {description}
      </HelpTooltipContent>
    </HelpTooltip>
  )
}

export function formatClusterChartValue(value: unknown, unit: string) {
  const numeric = normalizeChartNumber(value)
  if (numeric === null) return "-"
  return appendClusterChartUnit(formatClusterChartNumber(numeric, unit), unit)
}

function formatClusterChartAxisValue(value: unknown) {
  const numeric = normalizeChartNumber(value)
  if (numeric === null) return "-"
  return formatClusterChartNumber(numeric, "")
}

function normalizeChartNumber(value: unknown) {
  const numeric = typeof value === "number" ? value : Number(value)
  return Number.isFinite(numeric) ? numeric : null
}

function formatClusterChartNumber(value: number, unit: string) {
  const maximumFractionDigits = clusterChartFractionDigits(value, unit)
  return value.toLocaleString("en-US", {
    maximumFractionDigits,
    minimumFractionDigits: 0,
  })
}

function clusterChartFractionDigits(value: number, unit: string) {
  if (Number.isInteger(value)) return 0
  const abs = Math.abs(value)
  if (isClusterChartByteRateUnit(unit)) {
    if (abs > 0 && abs < 0.01) return 3
    if (abs > 0 && abs < 1) return 2
    return 1
  }
  if (unit === "%") return 2
  if (abs > 0 && abs < 1) return 2
  return 1
}

function appendClusterChartUnit(value: string, unit: string) {
  if (!unit) return value
  if (unit === "%" || unit === "x" || unit.startsWith("/")) return `${value}${unit}`
  return `${value} ${unit}`
}

function isClusterChartByteRateUnit(unit: string) {
  return unit === "B/s" || unit === "KB/s" || unit === "MB/s" || unit === "GB/s" || unit === "TB/s"
}
