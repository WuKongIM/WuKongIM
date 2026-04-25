import { Card, CardContent } from "@/components/ui/card"

type MetricPlaceholderProps = {
  label: string
  hint?: string
}

export function MetricPlaceholder({ label, hint }: MetricPlaceholderProps) {
  return (
    <Card className="border border-border bg-muted/30 shadow-none">
      <CardContent className="space-y-2 pt-4">
        <div className="text-[11px] font-medium uppercase tracking-[0.18em] text-muted-foreground">
          {label}
        </div>
        <div className="text-2xl font-semibold text-foreground">--</div>
        <p className="text-xs leading-5 text-muted-foreground">{hint ?? "Waiting for live data."}</p>
      </CardContent>
    </Card>
  )
}
