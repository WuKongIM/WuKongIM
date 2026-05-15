import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"

import type { MetricDataPoint } from "../types"

type MetricChartProps = {
  label: string
  data: MetricDataPoint[]
  unit: string
  color: string
  formatValue?: (value: number) => string
}

export function MetricChart({ label, data, unit, color, formatValue }: MetricChartProps) {
  const chartData = data.map((point) => {
    return {
      time: new Date(point.timestamp).toLocaleTimeString("en-US", {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
      }),
      value: point.value,
    }
  })

  const formatter = formatValue || ((v: number) => v.toFixed(1))

  return (
    <div className="relative flex flex-col rounded-2xl border border-border/80 bg-card/88 p-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.035)]">
      <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
        {label}
      </span>

      <div className="mt-4 h-[180px]">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={chartData} margin={{ top: 5, right: 5, bottom: 5, left: 5 }}>
            <defs>
              <linearGradient id={`gradient-${color}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor={`var(${color})`} stopOpacity={0.3} />
                <stop offset="95%" stopColor={`var(${color})`} stopOpacity={0} />
              </linearGradient>
            </defs>
            <XAxis
              dataKey="time"
              stroke="hsl(var(--muted-foreground))"
              fontSize={10}
              tickLine={false}
              axisLine={false}
              interval="preserveStartEnd"
              tickFormatter={(value, index) => {
                // Show only the first and last time labels to keep dense charts readable.
                if (index === 0 || index === chartData.length - 1) {
                  return value
                }
                return ""
              }}
            />
            <YAxis
              stroke="hsl(var(--muted-foreground))"
              fontSize={10}
              tickLine={false}
              axisLine={false}
              tickFormatter={(value) => formatter(value)}
              width={45}
            />
            <Tooltip
              content={({ active, payload }) => {
                if (!active || !payload || payload.length === 0) return null
                const value = payload[0].value as number
                return (
                  <div className="rounded-lg border bg-background px-3 py-2 shadow-md">
                    <p className="text-xs font-medium">
                      {formatter(value)} {unit}
                    </p>
                    <p className="text-xs text-muted-foreground">{payload[0].payload.time}</p>
                  </div>
                )
              }}
            />
            <Area
              type="monotone"
              dataKey="value"
              stroke={`var(${color})`}
              strokeWidth={2}
              fill={`url(#gradient-${color})`}
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}
