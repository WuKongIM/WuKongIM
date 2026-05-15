import type { ReactNode } from "react"

type ChartGridProps = {
  title: string
  children: ReactNode
}

export function ChartGrid({ title, children }: ChartGridProps) {
  return (
    <div className="space-y-4">
      <h2 className="font-mono text-sm font-semibold uppercase tracking-[0.12em] text-muted-foreground">
        {title}
      </h2>
      <div className="grid grid-cols-4 gap-4">{children}</div>
    </div>
  )
}
