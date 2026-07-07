import { SectionCard } from "@/components/shell/section-card"
import { StatusDot, type StatusTone } from "@/components/shell/status-dot"

type StatusMetricCardProps = {
  label: string
  value: number | string
  tone?: StatusTone
  description?: string
}

export function StatusMetricCard({ description, label, tone = "muted", value }: StatusMetricCardProps) {
  return (
    <SectionCard className="min-h-28" title={label}>
      <div className="flex items-start justify-between gap-3">
        <div className="font-mono text-3xl font-medium tabular-nums text-foreground">{value}</div>
        <StatusDot tone={tone} />
      </div>
      {description ? <p className="mt-2 text-xs text-muted-foreground">{description}</p> : null}
    </SectionCard>
  )
}
