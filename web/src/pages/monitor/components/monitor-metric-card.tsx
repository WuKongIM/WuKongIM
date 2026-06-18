import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { useIntl } from "react-intl"

import { cn } from "@/lib/utils"

import type { MonitorMetricCard as MonitorMetricCardModel, MonitorTone } from "../types"

type MonitorMetricCardProps = {
  card: MonitorMetricCardModel
}

const toneStyles: Record<MonitorTone, { dot: string; badge: string }> = {
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

export function MonitorMetricCard({ card }: MonitorMetricCardProps) {
  const intl = useIntl()
  const isAvailable = card.available
  const chartData = isAvailable
    ? card.series.map((point) => ({
        time: new Intl.DateTimeFormat(intl.locale, { hour: "2-digit", minute: "2-digit" }).format(new Date(point.timestamp)),
        value: point.value,
      }))
    : []
  const gradientId = `monitor-gradient-${card.key}`
  const styles = toneStyles[card.tone]
  const hasSeries = card.available && chartData.length > 0
  const noDataMessage = card.unavailableReasonLabelId
    ? intl.formatMessage({ id: card.unavailableReasonLabelId })
    : card.error

  return (
    <article
      className="flex min-h-[330px] flex-col rounded-lg border border-border/80 bg-card/88 p-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.035)]"
      data-testid="monitor-metric-card"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: card.titleId })}</h2>
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
          <ResponsiveContainer height="100%" width="100%">
            <AreaChart data={chartData} margin={{ bottom: 0, left: 0, right: 4, top: 8 }}>
              <defs>
                <linearGradient id={gradientId} x1="0" x2="0" y1="0" y2="1">
                  <stop offset="5%" stopColor={card.chartColor} stopOpacity={0.32} />
                  <stop offset="95%" stopColor={card.chartColor} stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <XAxis
                axisLine={false}
                dataKey="time"
                fontSize={10}
                interval="preserveStartEnd"
                tickLine={false}
                tickMargin={8}
              />
              <YAxis axisLine={false} domain={["dataMin", "dataMax"]} fontSize={10} tickLine={false} width={36} />
              <Tooltip
                content={({ active, payload }) => {
                  if (!active || !payload?.length) return null
                  return (
                    <div className="rounded-md border border-border bg-background px-2.5 py-2 text-xs shadow-md">
                      <p className="font-medium text-foreground">
                        {payload[0].value as number} {card.unit}
                      </p>
                      <p className="text-muted-foreground">{payload[0].payload.time}</p>
                    </div>
                  )
                }}
              />
              <Area
                dataKey="value"
                fill={`url(#${gradientId})`}
                isAnimationActive={false}
                stroke={card.chartColor}
                strokeWidth={2}
                type="monotone"
              />
            </AreaChart>
          </ResponsiveContainer>
        ) : (
          <div className="flex h-full flex-col items-center justify-center rounded-md border border-dashed border-border/80 bg-background/45 px-4 text-center">
            <p className="text-sm font-medium text-foreground">{intl.formatMessage({ id: "monitor.card.noData" })}</p>
            {noDataMessage ? <p className="mt-1 max-w-full break-words text-xs text-muted-foreground">{noDataMessage}</p> : null}
          </div>
        )}
      </div>

      {card.stats.length > 0 ? (
        <dl className="mt-auto grid grid-cols-3 gap-2 pt-4">
          {card.stats.map((stat) => (
            <div className="min-w-0 rounded-md border border-border/70 bg-background/55 px-2 py-2" key={stat.labelId}>
              <dt className="truncate text-[11px] text-muted-foreground">{intl.formatMessage({ id: stat.labelId })}</dt>
              <dd className="mt-1 truncate text-xs font-semibold text-foreground">{stat.value}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <div className="mt-auto pt-4" />
      )}
    </article>
  )
}
