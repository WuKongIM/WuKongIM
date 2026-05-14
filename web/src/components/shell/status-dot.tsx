import { cn } from "@/lib/utils"

export type StatusTone = "healthy" | "running" | "warning" | "error" | "muted"

const toneClass: Record<StatusTone, string> = {
  healthy: "bg-[var(--status-healthy)]",
  running: "bg-[var(--status-running)]",
  warning: "bg-[var(--status-warning)]",
  error: "bg-[var(--status-error)]",
  muted: "bg-muted-foreground",
}

export function StatusDot({ className, tone = "muted" }: { className?: string; tone?: StatusTone }) {
  return (
    <span
      aria-hidden
      className={cn("inline-block size-1.5 shrink-0 rounded-full", toneClass[tone], className)}
      data-testid="status-dot"
      data-tone={tone}
    />
  )
}
