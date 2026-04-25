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
      return "success"
    case "quorum_lost":
    case "leader_missing":
    case "draining":
    case "retrying":
    case "suspect":
      return "warning"
    case "failed":
    case "dead":
    case "service_unavailable":
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
        "inline-flex items-center rounded-md border px-2 py-1 text-xs font-medium capitalize",
        variant === "success" && "border-border bg-muted text-foreground",
        variant === "warning" && "border-border bg-secondary text-foreground",
        variant === "danger" && "border-destructive/30 bg-destructive/10 text-destructive",
        variant === "neutral" && "border-border bg-background text-muted-foreground",
      )}
      data-variant={variant}
    >
      {formatValue(value)}
    </span>
  )
}
