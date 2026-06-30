import { cn } from "@/lib/utils"

type StatusBadgeProps = {
  value: string
}

function resolveVariant(value: string) {
  switch (value.toLowerCase()) {
    case "alive":
    case "ready":
    case "in_sync":
    case "active":
    case "healthy":
      return "success"
    case "quorum_lost":
    case "leader_missing":
    case "no_leader":
    case "isr_insufficient":
    case "draining":
    case "retrying":
    case "suspect":
    case "append_catchup":
    case "needs_snapshot":
    case "snapshot_required":
    case "snapshot_transferring":
    case "compaction_degraded":
    case "missing":
    case "not_ready":
    case "stale":
      return "warning"
    case "failed":
    case "dead":
    case "service_unavailable":
    case "restore_failed":
      return "danger"
    default:
      return "neutral"
  }
}

function formatValue(value: string) {
  return value.replaceAll("_", " ")
}

export function StatusBadge({ value }: StatusBadgeProps) {
  const variant = resolveVariant(value)

  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-1 text-xs font-medium capitalize",
        variant === "success" && "border-primary/25 bg-primary/10 text-primary",
        variant === "warning" && "border-warning/25 bg-warning/10 text-warning",
        variant === "danger" && "border-destructive/30 bg-destructive/10 text-destructive",
        variant === "neutral" && "border-border bg-background/70 text-muted-foreground",
      )}
      data-variant={variant}
    >
      {formatValue(value)}
    </span>
  )
}
