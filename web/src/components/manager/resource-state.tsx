import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

type ResourceStateKind = "loading" | "empty" | "forbidden" | "unavailable" | "error"

type ResourceStateProps = {
  kind: ResourceStateKind
  title: string
  description?: string
  retryLabel?: string
  onRetry?: () => void
}

export function ResourceState({
  kind,
  title,
  description,
  retryLabel = "Retry",
  onRetry,
}: ResourceStateProps) {
  const intl = useIntl()

  return (
    <div
      className="rounded-2xl border border-border/80 bg-card/88 px-5 py-6 text-sm text-muted-foreground shadow-[inset_0_1px_0_rgba(255,255,255,0.035)]"
      data-kind={kind}
      role="status"
    >
      <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
        <span
          className={cn(
            "size-2 rounded-full",
            kind === "loading" && "bg-[var(--status-running)]",
            kind === "empty" && "bg-[var(--status-healthy)]",
            (kind === "forbidden" || kind === "unavailable") && "bg-[var(--status-warning)]",
            kind === "error" && "bg-[var(--status-error)]",
          )}
        />
        {title}
      </div>
      <p className="mt-2 max-w-2xl leading-6">
        {description ?? intl.formatMessage({ id: `resourceState.${kind}` })}
      </p>
      {onRetry ? (
        <Button className="mt-4" onClick={onRetry} size="sm" variant="outline">
          {retryLabel === "Retry" ? intl.formatMessage({ id: "common.retry" }) : retryLabel}
        </Button>
      ) : null}
    </div>
  )
}
