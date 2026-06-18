import { useIntl } from "react-intl"

import type { BusinessTrendSeries } from "../view-model"

type BusinessMessageTrendsProps = {
  trends: BusinessTrendSeries
}

export function BusinessMessageTrends({ trends }: BusinessMessageTrendsProps) {
  const intl = useIntl()
  const chartCards = [
    {
      key: "throughput",
      titleId: "businessDashboard.trends.throughput",
      data: trends.throughput,
      firstKey: "send" as const,
      secondKey: "deliver" as const,
      firstLabelId: "businessDashboard.metric.sendRate",
      secondLabelId: "businessDashboard.metric.deliverRate",
    },
    {
      key: "latency",
      titleId: "businessDashboard.trends.latency",
      data: trends.latency,
      firstKey: "send" as const,
      secondKey: "delivery" as const,
      firstLabelId: "businessDashboard.metric.sendLatency",
      secondLabelId: "businessDashboard.metric.deliveryLatency",
    },
    {
      key: "failures",
      titleId: "businessDashboard.trends.failures",
      data: trends.failures,
      firstKey: "send" as const,
      secondKey: "delivery" as const,
      firstLabelId: "businessDashboard.metric.sendFailRate",
      secondLabelId: "businessDashboard.metric.deliveryFailRate",
    },
  ]

  return (
    <section className="rounded-lg border border-border/80 bg-card p-4">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h2 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "businessDashboard.trends.title" })}</h2>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">{intl.formatMessage({ id: "businessDashboard.trends.description" })}</p>
        </div>
      </div>
      <div className="mt-4 grid gap-3 xl:grid-cols-3">
        {chartCards.map((chart) => (
          <TrendChart
            data={chart.data}
            firstKey={chart.firstKey}
            firstLabel={intl.formatMessage({ id: chart.firstLabelId })}
            key={chart.key}
            secondKey={chart.secondKey}
            secondLabel={intl.formatMessage({ id: chart.secondLabelId })}
            title={intl.formatMessage({ id: chart.titleId })}
          />
        ))}
      </div>
    </section>
  )
}

type ChartKey = "send" | "deliver" | "delivery"
type ChartDatum = { index: number } & Partial<Record<ChartKey, number>>

function TrendChart({
  data,
  firstKey,
  secondKey,
  firstLabel,
  secondLabel,
  title,
}: {
  data: ChartDatum[]
  firstKey: ChartKey
  secondKey: ChartKey
  firstLabel: string
  secondLabel: string
  title: string
}) {
  const firstPoints = buildPolylinePoints(data, firstKey, [firstKey, secondKey])
  const secondPoints = buildPolylinePoints(data, secondKey, [firstKey, secondKey])

  return (
    <article className="rounded-lg border border-border/70 bg-background/55 p-3">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium text-foreground">{title}</h3>
        <div className="flex shrink-0 items-center gap-2 text-[11px] text-muted-foreground">
          <span className="inline-flex items-center gap-1">
            <span aria-hidden className="size-2 rounded-full bg-chart-1" />
            {firstLabel}
          </span>
          <span className="inline-flex items-center gap-1">
            <span aria-hidden className="size-2 rounded-full bg-chart-2" />
            {secondLabel}
          </span>
        </div>
      </div>
      <div className="mt-3 h-40">
        <svg aria-hidden className="h-full w-full overflow-visible" preserveAspectRatio="none" viewBox="0 0 320 120">
          <path d="M0 104H320" stroke="var(--border)" strokeDasharray="4 5" strokeWidth="1" />
          <path d="M0 60H320" stroke="var(--border)" strokeDasharray="4 5" strokeWidth="1" />
          <path d="M0 16H320" stroke="var(--border)" strokeDasharray="4 5" strokeWidth="1" />
          <polyline fill="none" points={firstPoints} stroke="var(--chart-1)" strokeLinecap="round" strokeLinejoin="round" strokeWidth="3" />
          <polyline fill="none" points={secondPoints} stroke="var(--chart-2)" strokeLinecap="round" strokeLinejoin="round" strokeWidth="3" />
        </svg>
      </div>
    </article>
  )
}

function buildPolylinePoints(data: ChartDatum[], key: ChartKey, domainKeys: ChartKey[]) {
  const width = 320
  const height = 120
  const padding = 12
  const domainValues = data.flatMap((row) => domainKeys.map((domainKey) => row[domainKey] ?? 0))
  const min = Math.min(...domainValues)
  const max = Math.max(...domainValues)
  const range = max - min || 1

  return data
    .map((row, index) => {
      const x = data.length <= 1 ? width / 2 : (index / (data.length - 1)) * width
      const normalized = ((row[key] ?? 0) - min) / range
      const y = height - padding - normalized * (height - padding * 2)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(" ")
}
