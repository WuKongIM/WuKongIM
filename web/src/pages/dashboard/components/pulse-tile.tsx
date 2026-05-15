import { Line, LineChart, ResponsiveContainer } from "recharts"

type PulseTileProps = {
  label: string
  value: number
  valueSuffix?: string
  sub: string
  series: number[]
  tone?: "default" | "danger"
}

export function PulseTile({
  label,
  value,
  valueSuffix,
  sub,
  series,
  tone = "default",
}: PulseTileProps) {
  const data = series.map((v, i) => ({ i, v }))
  const strokeColor =
    tone === "danger" ? "var(--chart-4)" : "var(--chart-1)"

  return (
    <div className="relative flex flex-col justify-between rounded-2xl border border-border/80 bg-card/88 px-4 py-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.035)]">
      <span className="font-mono text-[11px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
        {label}
      </span>

      <div className="mt-1">
        <span className="font-mono text-2xl font-semibold tracking-[-0.04em] text-foreground">
          {value.toLocaleString()}
        </span>
        {valueSuffix && (
          <span className="text-sm text-muted-foreground"> {valueSuffix}</span>
        )}
      </div>

      <p className="mt-1 text-xs text-muted-foreground">{sub}</p>

      <div className="mt-3 h-10" aria-label={`${label}: ${value}`}>
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
            <Line
              dataKey="v"
              dot={false}
              strokeWidth={1.5}
              type="monotone"
              stroke={strokeColor}
              isAnimationActive={false}
            />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}
