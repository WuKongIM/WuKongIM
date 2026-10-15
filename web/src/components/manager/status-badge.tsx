import { useIntl } from "react-intl"

import { getStatusLabelMessageId, getStatusVariant } from "@/components/manager/status-labels"
import { cn } from "@/lib/utils"

type StatusBadgeProps = {
  value: string
  label?: string
}

function formatValue(value: string) {
  return value.replaceAll("_", " ")
}

export function StatusBadge({ value, label }: StatusBadgeProps) {
  const intl = useIntl()
  const variant = getStatusVariant(value)
  const normalized = value.toLowerCase()
  const messageId = getStatusLabelMessageId(normalized)

  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium capitalize",
        variant === "success" && "border-success/25 bg-success/8 text-success",
        variant === "warning" && "border-warning/25 bg-warning/8 text-warning",
        variant === "danger" && "border-destructive/30 bg-destructive/8 text-destructive",
        variant === "neutral" && "border-border bg-background text-muted-foreground",
      )}
      data-variant={variant}
    >
      {label ?? (messageId ? intl.formatMessage({ id: messageId }) : formatValue(value))}
    </span>
  )
}
